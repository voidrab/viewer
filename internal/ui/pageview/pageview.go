// Package pageview shows a PDF document as a scrolling column of pages. It
// owns the layout, renders visible pages in the background and caches the
// results; the caller draws it into a canvas and feeds it input events.
package pageview

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"math"

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
	// Pool renders the view's pages and caches their bitmaps. Views of
	// several documents should share one. If Pool is nil, the view creates
	// its own pool from CacheBytes and Workers and closes it with itself.
	Pool *Pool
	// CacheBytes bounds the bitmap memory, and Workers the renders in flight,
	// of a pool the view makes for itself.
	CacheBytes, Workers int
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
	if o.ScrollSpeed == 0 {
		o.ScrollSpeed = 5
	}
}

// zoomStep is the factor by which one zoom step multiplies the scale.
const zoomStep = 1.25

// key identifies a rendered bitmap: a page at a whole dpi.
type key struct{ page, dpi int }

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
// goroutine; rendering happens on the pool's workers.
type View struct {
	pages      []*gopdfrab.Page
	render     renderFunc
	opts       Options
	invalidate func()
	lay        *layout.Layout

	pool     *Pool
	ownsPool bool
	// The rest is the pool's: it is guarded by the pool's lock.
	id       int
	inflight map[key]*job // the renders asked for and not yet delivered
	running  bool         // a render of this view is in progress
	inactive bool         // the view is in the background
}

// New builds a view of doc sized to the viewport. invalidate is called from
// worker goroutines whenever a newly rendered page should be drawn.
func New(doc Document, viewport image.Point, opts Options, invalidate func()) (*View, error) {
	opts.fill()
	n, err := doc.Pages()
	if err != nil {
		return nil, err
	}
	v := &View{opts: opts, invalidate: invalidate, render: renderPage, pool: opts.Pool}
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
	if v.pool == nil {
		v.pool, v.ownsPool = NewPool(PoolOptions{Workers: opts.Workers, CacheBytes: opts.CacheBytes}), true
	}
	v.pool.attach(v)
	return v, nil
}

// Layout exposes the geometry for hit testing and status display.
func (v *View) Layout() *layout.Layout { return v.lay }

// Close drops the view's pending renders and cached bitmaps, and returns once
// none of its renders is running. The document stays open.
func (v *View) Close() {
	v.pool.detach(v)
	if v.ownsPool {
		v.pool.Close()
	}
}

// SetActive says whether the view is the one being looked at. Pages of a view
// in the background render after those of the active view.
func (v *View) SetActive(active bool) { v.pool.setActive(v, active) }

// Prefetch asks for the pages now in view to be rendered at the current zoom,
// without drawing them, so that they are ready when the view is shown.
func (v *View) Prefetch() { v.want(nil, image.Rectangle{}) }

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
	v.want(c, rect)
}

// want asks the pool for the visible pages at the current zoom, dropping
// requests for pages scrolled away and for zoom levels other than the current
// one. With a canvas it also draws what it has of each page: its bitmap, or a
// stretched earlier bitmap while the one at the current zoom renders, or blank
// paper.
func (v *View) want(c *paint.Canvas, rect image.Rectangle) {
	first, last := v.lay.Visible()
	dpi := dpiFor(v.lay.Scale())
	// One page beyond each edge is rendered ahead, so that scrolling finds it
	// ready instead of blank. The pool ranks it behind the visible pages. At
	// a zoom where those pages would not fit the pool's budget they would
	// push each other out, and be rendered over and over, so it is skipped.
	lo, hi := max(first-1, 0), min(last+1, v.lay.Pages())
	if v.pool.tooBig(v.lay, lo, hi) {
		lo, hi = first, last
	}
	v.pool.cancelStale(v, func(k key) bool { return k.dpi == dpi && k.page >= lo && k.page < hi })
	mid := v.lay.Current()
	for i := lo; i < hi; i++ {
		k := key{i, dpi}
		visible := i >= first && i < last
		if c == nil || !visible {
			if !v.pool.has(v, k) {
				v.pool.submit(v, k, abs(i-mid))
			}
			continue
		}
		r := v.lay.ViewRect(i).Add(rect.Min)
		if img := v.pool.lookup(v, k); img != nil {
			c.DrawScaled(r, img) // Rendered pages are opaque, so they need no white fill.
			continue
		}
		c.Fill(r, color.NRGBA{255, 255, 255, 255})
		if img := v.pool.standIn(v, i); img != nil {
			c.DrawScaled(r, img)
		}
		v.pool.submit(v, k, abs(i-mid))
	}
}

func abs(v int) int { return max(v, -v) }

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
