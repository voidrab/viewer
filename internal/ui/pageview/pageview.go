// Package pageview shows a PDF document as a scrolling column of pages. It
// owns the layout, renders visible pages in the background and caches the
// results; the caller draws it into a canvas and feeds it input events.
package pageview

import (
	"container/list"
	"context"
	"fmt"
	"image"
	"image/color"
	"math"
	"sync"

	"github.com/voidrab/gopdfrab"

	"viewer/internal/ui/layout"
	"viewer/internal/ui/paint"
	"viewer/internal/ui/platform"
)

// Options tunes a View. The zero value is usable.
type Options struct {
	// Gap is the space between pages and Margin the space around the column,
	// in pixels. Zero means 12 and 12.
	Gap, Margin int
	// Background is painted behind the pages.
	Background color.NRGBA
	// CacheBytes bounds the memory of cached page bitmaps; zero means 256 MiB.
	CacheBytes int
	// Workers is the number of pages rendered at once; zero means one, since
	// the pages of a document render one after another whatever the number.
	Workers int
	// ScrollSpeed multiplies wheel scroll distances; zero means 5.
	ScrollSpeed float64
}

func (o *Options) fill() {
	if o.Gap == 0 {
		o.Gap = 12
	}
	if o.Margin == 0 {
		o.Margin = 12
	}
	if o.Background == (color.NRGBA{}) {
		o.Background = color.NRGBA{0x3a, 0x3b, 0x3f, 255}
	}
	if o.CacheBytes == 0 {
		o.CacheBytes = 256 << 20
	}
	if o.Workers == 0 {
		o.Workers = 1
	}
	if o.ScrollSpeed == 0 {
		o.ScrollSpeed = 5
	}
}

// zoomStep is the factor by which one zoom step multiplies the scale.
const zoomStep = 1.25

// key identifies a rendered bitmap: a page at a whole dpi.
type key struct{ page, dpi int }

type entry struct {
	key  key
	img  *image.RGBA
	elem *list.Element
}

// Document is the part of *gopdfrab.Document a View needs.
type Document interface {
	Pages() (int, error)
	Page(i int) (*gopdfrab.Page, error)
}

// renderFunc renders p at scale; tests replace it.
type renderFunc func(ctx context.Context, p *gopdfrab.Page, scale float64) (*image.RGBA, error)

func renderPage(ctx context.Context, p *gopdfrab.Page, scale float64) (*image.RGBA, error) {
	return p.Render(ctx, gopdfrab.RenderOptions{Scale: scale})
}

// View is a document in a viewport. Draw and HandleEvent belong to one
// goroutine; rendering happens on workers it starts itself.
type View struct {
	pages      []*gopdfrab.Page
	render     renderFunc
	opts       Options
	invalidate func()
	lay        *layout.Layout

	ctx    context.Context
	cancel context.CancelFunc
	sem    chan struct{}
	wg     sync.WaitGroup

	mu       sync.Mutex
	cache    map[key]*entry
	lru      *list.List // front is most recently used
	bytes    int
	inflight map[key]context.CancelFunc
}

// New builds a view of doc sized to the viewport. invalidate is called from
// worker goroutines whenever a newly rendered page should be drawn.
func New(doc Document, viewport image.Point, opts Options, invalidate func()) (*View, error) {
	opts.fill()
	n, err := doc.Pages()
	if err != nil {
		return nil, err
	}
	v := &View{
		opts: opts, invalidate: invalidate, render: renderPage,
		sem: make(chan struct{}, opts.Workers), cache: map[key]*entry{},
		lru: list.New(), inflight: map[key]context.CancelFunc{},
	}
	v.ctx, v.cancel = context.WithCancel(context.Background())
	sizes := make([]layout.Size, n)
	for i := range n {
		p, err := doc.Page(i)
		if err != nil {
			return nil, err
		}
		v.pages = append(v.pages, p)
		w, h := p.Size()
		sizes[i] = layout.Size{W: w, H: h}
	}
	v.lay = layout.New(sizes, viewport, opts.Gap, opts.Margin)
	return v, nil
}

// Layout exposes the geometry for hit testing and status display.
func (v *View) Layout() *layout.Layout { return v.lay }

// Close stops all rendering and releases the cache. The document stays open.
func (v *View) Close() {
	v.cancel()
	v.wg.Wait()
	v.mu.Lock()
	v.cache, v.bytes = map[key]*entry{}, 0
	v.lru.Init()
	v.mu.Unlock()
}

// dpiFor returns the whole dpi, at least 1, that a page is rendered at for
// scale. Cached bitmaps are keyed by this value.
func dpiFor(scale float64) int { return max(1, int(math.Round(72*scale))) }

// Draw paints the visible pages into all of c, whose bounds are the viewport.
// A page still being rendered shows a stretched earlier bitmap of it if there
// is one, or blank paper.
func (v *View) Draw(c *paint.Canvas) { v.DrawIn(c, c.Bounds()) }

// DrawIn paints the view into rect of c, which must be the viewport the view
// was sized to.
func (v *View) DrawIn(c *paint.Canvas, rect image.Rectangle) {
	c.PushClip(rect)
	defer c.PopClip()
	c.Fill(rect, v.opts.Background)
	first, last := v.lay.Visible()
	dpi := dpiFor(v.lay.Scale())
	v.cancelHidden(first, last)
	for i := first; i < last; i++ {
		r := v.lay.ViewRect(i).Add(rect.Min)
		if img := v.lookup(key{i, dpi}); img != nil {
			c.DrawScaled(r, img) // rendered pages are opaque: no paper needed under them
			continue
		}
		c.Fill(r, color.NRGBA{255, 255, 255, 255})
		if img := v.anyRender(i); img != nil {
			c.DrawScaled(r, img)
		}
		v.schedule(key{i, dpi})
	}
}

// Page returns the zero-based index of the page at the middle of the view.
func (v *View) Page() int { return v.lay.Current() }

// Pages returns the number of pages.
func (v *View) Pages() int { return v.lay.Pages() }

// GoToPage scrolls to page i, clamped to the document.
func (v *View) GoToPage(i int) { v.lay.GoTo(i) }

// ZoomIn and ZoomOut change the zoom by one step.
func (v *View) ZoomIn()  { v.zoomBy(zoomStep) }
func (v *View) ZoomOut() { v.zoomBy(1 / zoomStep) }

// zoomPresets are the scales CycleZoom steps through after the two fit modes.
var zoomPresets = []float64{0.5, 0.75, 1, 1.25, 1.5, 2, 3}

// CycleZoom steps to the next zoom preset: fit width, fit page, then the fixed
// scales in ascending order, and round again. From a scale between presets it
// goes to the next one up.
func (v *View) CycleZoom() {
	switch v.lay.Mode() {
	case layout.ZoomFitWidth:
		v.lay.SetMode(layout.ZoomFitPage)
		return
	case layout.ZoomFitPage:
		v.lay.SetZoom(zoomPresets[0])
		return
	}
	for _, p := range zoomPresets {
		if p > v.lay.Scale()+1e-9 {
			v.lay.SetZoom(p)
			return
		}
	}
	v.lay.SetMode(layout.ZoomFitWidth)
}

// ZoomLabel describes the zoom for display: "Fit width", "Fit page" or a
// percentage.
func (v *View) ZoomLabel() string {
	switch v.lay.Mode() {
	case layout.ZoomFitWidth:
		return "Fit width"
	case layout.ZoomFitPage:
		return "Fit page"
	}
	return fmt.Sprintf("%d%%", int(math.Round(v.lay.Scale()*100)))
}

// lookup returns the cached bitmap for k and marks it recently used.
func (v *View) lookup(k key) *image.RGBA {
	v.mu.Lock()
	defer v.mu.Unlock()
	e := v.cache[k]
	if e == nil {
		return nil
	}
	v.lru.MoveToFront(e.elem)
	return e.img
}

// anyRender returns some cached bitmap of page i at another scale.
func (v *View) anyRender(i int) *image.RGBA {
	v.mu.Lock()
	defer v.mu.Unlock()
	for el := v.lru.Front(); el != nil; el = el.Next() {
		if e := el.Value.(*entry); e.key.page == i {
			return e.img
		}
	}
	return nil
}

// cancelHidden stops renders of pages outside [first, last).
func (v *View) cancelHidden(first, last int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for k, cancel := range v.inflight {
		if k.page < first || k.page >= last {
			cancel()
			delete(v.inflight, k)
		}
	}
}

// schedule starts rendering k unless it is already underway.
func (v *View) schedule(k key) {
	v.mu.Lock()
	if _, busy := v.inflight[k]; busy {
		v.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(v.ctx)
	v.inflight[k] = cancel
	v.mu.Unlock()

	v.wg.Add(1)
	go func() {
		defer v.wg.Done()
		defer cancel()
		acquired := false
		select {
		case v.sem <- struct{}{}:
			acquired = true
		case <-ctx.Done():
		}
		if acquired {
			defer func() { <-v.sem }()
		}
		// select picks at random when both cases are ready, so a cancelled
		// job may have acquired a worker; it must not render.
		if ctx.Err() != nil {
			v.drop(k)
			return
		}
		img, err := v.render(ctx, v.pages[k.page], float64(k.dpi)/72)
		v.mu.Lock()
		// Only the render that is still wanted may store its result.
		_, wanted := v.inflight[k]
		if err == nil && wanted {
			v.store(k, img)
		}
		delete(v.inflight, k)
		v.mu.Unlock()
		if err == nil && wanted {
			v.invalidate()
		}
	}()
}

// drop forgets that k is in flight.
func (v *View) drop(k key) {
	v.mu.Lock()
	delete(v.inflight, k)
	v.mu.Unlock()
}

// store caches img under k and evicts the least recently used bitmaps beyond
// the budget. The caller holds v.mu.
func (v *View) store(k key, img *image.RGBA) {
	e := &entry{key: k, img: img}
	e.elem = v.lru.PushFront(e)
	v.cache[k] = e
	v.bytes += len(img.Pix)
	for v.bytes > v.opts.CacheBytes && v.lru.Len() > 1 {
		old := v.lru.Back().Value.(*entry)
		v.lru.Remove(old.elem)
		delete(v.cache, old.key)
		v.bytes -= len(old.img.Pix)
	}
}

// HandleEvent applies a resize, scroll or key event and reports whether the
// view needs redrawing. Events it does not use return false.
func (v *View) HandleEvent(ev platform.Event) bool {
	switch ev := ev.(type) {
	case platform.ResizeEvent:
		v.lay.SetViewport(ev.Size.W, ev.Size.H)
		return true
	case platform.ScrollEvent:
		if ev.Mods&platform.Ctrl != 0 {
			v.zoomBy(math.Pow(zoomStep, -ev.DY/10))
			return true
		}
		v.lay.ScrollBy(image.Pt(int(ev.DX*v.opts.ScrollSpeed), int(ev.DY*v.opts.ScrollSpeed)))
		return true
	case platform.KeyEvent:
		if ev.Down {
			return v.key(ev)
		}
	}
	return false
}

func (v *View) key(ev platform.KeyEvent) bool {
	_, vh := v.viewport()
	page := image.Pt(0, vh*9/10)
	if ev.Mods&platform.Ctrl != 0 {
		switch ev.Rune {
		case '=', '+':
			v.zoomBy(zoomStep)
		case '-':
			v.zoomBy(1 / zoomStep)
		case '0':
			v.lay.SetMode(layout.ZoomFitWidth)
		default:
			return false
		}
		return true
	}
	switch ev.Key {
	case platform.KeyPageDown:
		v.lay.ScrollBy(page)
	case platform.KeyPageUp:
		v.lay.ScrollBy(image.Pt(0, -page.Y))
	case platform.KeyDown:
		v.lay.ScrollBy(image.Pt(0, 40))
	case platform.KeyUp:
		v.lay.ScrollBy(image.Pt(0, -40))
	case platform.KeyHome:
		v.lay.ScrollTo(image.Point{})
	case platform.KeyEnd:
		v.lay.ScrollTo(image.Pt(0, math.MaxInt32))
	case platform.KeyRight:
		v.lay.GoTo(v.lay.Current() + 1)
	case platform.KeyLeft:
		v.lay.GoTo(v.lay.Current() - 1)
	default:
		return false
	}
	return true
}

// viewport returns the viewport size the layout was last given.
func (v *View) viewport() (w, h int) {
	vp := v.lay.Viewport()
	return vp.X, vp.Y
}

func (v *View) zoomBy(f float64) { v.lay.SetZoom(v.lay.Scale() * f) }
