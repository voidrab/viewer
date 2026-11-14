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
	"time"

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
	// Scrollbar is the colour of the scrollbars drawn over the pages, whose
	// opacity the view sets; zero means a mid gray.
	Scrollbar color.NRGBA
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
	if o.Scrollbar == (color.NRGBA{}) {
		o.Scrollbar = color.NRGBA{0x9a, 0x9a, 0xa0, 0xff}
	}
}

// zoomStep is the factor by which one zoom step multiplies the scale.
const zoomStep = 1.25

// key identifies a rendered bitmap: the part rect of a page, in pixels at a
// whole dpi counted from the page's top-left corner.
type key struct {
	page, dpi int
	rect      image.Rectangle
}

// Document is the part of *gopdfrab.Document a View needs.
type Document interface {
	Pages() (int, error)
	Page(i int) (*gopdfrab.Page, error)
}

// renderFunc renders the part region of p at scale; tests replace it.
type renderFunc func(ctx context.Context, p *gopdfrab.Page, scale float64, region image.Rectangle) (*image.RGBA, error)

func renderPage(ctx context.Context, p *gopdfrab.Page, scale float64, region image.Rectangle) (*image.RGBA, error) {
	return p.Render(ctx, gopdfrab.RenderOptions{Scale: scale, Region: region})
}

// View is a document in a viewport. Draw and HandleEvent belong to one
// goroutine; rendering happens on the pool's workers.
type View struct {
	pages      []*gopdfrab.Page
	sizes      []layout.Size // of the pages, in points
	render     renderFunc
	opts       Options
	invalidate func()
	lay        *layout.Layout
	ptr        pointerState

	// Rapid zooming is not rendered until it pauses; see deferring.
	now       func() time.Time
	after     func(d time.Duration, f func()) stopper
	dpi       int       // the dpi last drawn at
	changedAt time.Time // when it last changed
	quietAt   time.Time // the time before which renders at a new dpi wait
	timer     stopper

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
	v := &View{opts: opts, invalidate: invalidate, render: renderPage, pool: opts.Pool, now: time.Now, after: afterFunc}
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
	v.sizes = sizes
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
	if v.timer != nil {
		v.timer.Stop()
	}
	v.pool.detach(v)
	if v.ownsPool {
		v.pool.Close()
	}
}

// SetActive says whether the view is the one being looked at. Pages of a view
// in the background render after those of the active view.
func (v *View) SetActive(active bool) { v.pool.setActive(v, active) }

// Prefetch asks for the parts that the view wants at the current zoom to be
// rendered, without drawing them, so that they are ready when the view is
// shown.
func (v *View) Prefetch() { v.want(nil, image.Rectangle{}) }

// dpiFor returns the whole dpi, at least 1, that a page is rendered at for
// scale. Cached bitmaps are keyed by this value.
func dpiFor(scale float64) int { return max(1, int(math.Round(72*scale))) }

// Rapid zooming, as the mouse wheel makes it, goes through many resolutions,
// each of which would be rendered and thrown away by the next step. A change of
// zoom less than rapidWithin after the one before it is therefore not rendered
// until the zoom has been still for settleDelay; the stretched bitmaps already
// there are shown meanwhile. A single step renders at once.
const (
	rapidWithin = 250 * time.Millisecond
	settleDelay = 80 * time.Millisecond
)

// stopper is what View needs of a timer.
type stopper interface{ Stop() bool }

func afterFunc(d time.Duration, f func()) stopper { return time.AfterFunc(d, f) }

// deferring reports whether renders at dpi should wait for the zoom to settle,
// and if so arranges for the view to be invalidated when they may start.
func (v *View) deferring(dpi int) bool {
	now := v.now()
	if dpi != v.dpi {
		rapid := v.dpi != 0 && now.Sub(v.changedAt) < rapidWithin
		v.dpi, v.changedAt = dpi, now
		v.quietAt = time.Time{}
		if rapid {
			v.quietAt = now.Add(settleDelay)
		}
	}
	if !now.Before(v.quietAt) {
		return false
	}
	if v.timer != nil {
		v.timer.Stop()
	}
	v.timer = v.after(v.quietAt.Sub(now), v.invalidate)
	return true
}

// wholePage, tileSize and previewDPI set how pages are split into parts. A
// page of up to wholePage pixels is rendered as one part; a larger one is split
// into square parts of tileSize pixels, so that memory follows the parts in
// view. previewDPI is the resolution of the quick render that stands in for a
// page with nothing cached.
const (
	wholePage  = 3 << 20
	tileSize   = 1024
	previewDPI = 36
)

// Draw paints the visible pages into all of c, whose bounds are the viewport.
// A page whose parts are still being rendered shows paper, with a stretched
// earlier bitmap of the page where one exists.
func (v *View) Draw(c *paint.Canvas) { v.DrawIn(c, c.Bounds()) }

// DrawIn paints the view into rect of c, which must be the viewport the view
// was sized to.
func (v *View) DrawIn(c *paint.Canvas, rect image.Rectangle) {
	c.PushClip(rect)
	defer c.PopClip()
	c.Fill(rect, v.opts.Background)
	v.want(c, rect)
	v.drawBars(c, rect.Min)
}

// pixelSize returns the size of page i rendered at dpi, which is what the
// parts of it are counted in.
func (v *View) pixelSize(i, dpi int) image.Point {
	s := v.sizes[i]
	return image.Pt(max(1, int(math.Ceil(s.W*float64(dpi)/72))), max(1, int(math.Ceil(s.H*float64(dpi)/72))))
}

// parts returns the parts a page of pixel size px is rendered in: itself, or a
// grid of tiles.
func parts(px image.Point) []image.Rectangle {
	if px.X*px.Y <= wholePage {
		return []image.Rectangle{{Max: px}}
	}
	var out []image.Rectangle
	for y := 0; y < px.Y; y += tileSize {
		for x := 0; x < px.X; x += tileSize {
			out = append(out, image.Rect(x, y, min(x+tileSize, px.X), min(y+tileSize, px.Y)))
		}
	}
	return out
}

// place returns where part r of a page of pixel size px lies, given the
// rectangle vr the page occupies in the view. Neighbouring parts share their
// edges exactly, so tiles leave no seams.
func place(vr image.Rectangle, r image.Rectangle, px image.Point) image.Rectangle {
	x := func(v int) int { return vr.Min.X + (v*vr.Dx()+px.X/2)/px.X }
	y := func(v int) int { return vr.Min.Y + (v*vr.Dy()+px.Y/2)/px.Y }
	return image.Rect(x(r.Min.X), y(r.Min.Y), x(r.Max.X), y(r.Max.Y))
}

// want makes one pass over the view. It asks the pool for the parts in the
// viewport and, at lower priority, for those within half a viewport of it, so
// that scrolling finds them ready. It also asks for a preview of each page with
// nothing cached, and drops all other requests. With a canvas, it also draws
// each visible page.
func (v *View) want(c *paint.Canvas, rect image.Rectangle) {
	dpi := dpiFor(v.lay.Scale())
	view := image.Rectangle{Max: v.lay.Viewport()}
	plan := v.plan(dpi, view, true)
	if plan.bytes > v.pool.budget/2 {
		// Rendering ahead would push the parts in view out of the cache, and
		// they would be rendered over and over.
		plan = v.plan(dpi, view, false)
	}
	if v.deferring(dpi) {
		plan.wanted = nil
	}
	keep := make(map[key]bool, len(plan.wanted)+len(plan.pages))
	for _, w := range plan.wanted {
		keep[w.k] = true
	}
	var previews []key
	for _, pg := range plan.pages {
		if !v.pool.hasPage(v, pg.page) {
			previews = append(previews, v.previewKey(pg.page))
		}
	}
	for _, k := range previews {
		keep[k] = true
	}
	v.pool.cancelStale(v, func(k key) bool { return keep[k] })
	for _, k := range previews {
		v.pool.submit(v, k, -1)
	}
	for _, pg := range plan.pages {
		if c != nil {
			v.drawPage(c, rect, pg, dpi)
		}
	}
	for _, w := range plan.wanted {
		if !v.pool.has(v, w.k) {
			v.pool.submit(v, w.k, w.base)
		}
	}
}

// wantedPart is a part to have rendered, with how far from the viewport's
// middle it lies, in steps of 64 pixels, or more if it is beyond the viewport.
type wantedPart struct {
	k    key
	base int
}

// pagePlan is a page with something in view and where it lies.
type pagePlan struct {
	page int
	vr   image.Rectangle // where the page lies in the view
}

// framePlan is what one pass over the view wants.
type framePlan struct {
	wanted []wantedPart
	pages  []pagePlan // the pages that intersect the viewport
	bytes  int        // the bitmap memory the wanted parts take
}

// previewKey is the key of the quick render that stands in for page.
func (v *View) previewKey(page int) key {
	px := v.pixelSize(page, previewDPI)
	return key{page: page, dpi: previewDPI, rect: image.Rectangle{Max: px}}
}

// plan works out the parts wanted for the viewport view at dpi: those that
// intersect it, and with ahead also those within half a viewport of it.
func (v *View) plan(dpi int, view image.Rectangle, ahead bool) framePlan {
	reach := view
	if ahead {
		reach = view.Inset(-max(view.Dx(), view.Dy()) / 2)
	}
	centre := image.Pt(view.Dx()/2, view.Dy()/2)
	var fp framePlan
	for i := range v.pages {
		vr := v.lay.ViewRect(i)
		if !vr.Overlaps(reach) {
			continue
		}
		visible := vr.Overlaps(view)
		if visible {
			fp.pages = append(fp.pages, pagePlan{i, vr})
		}
		px := v.pixelSize(i, dpi)
		for _, r := range parts(px) {
			at := place(vr, r, px)
			if !at.Overlaps(reach) {
				continue
			}
			mid := at.Min.Add(at.Max).Div(2)
			dist := max(abs(mid.X-centre.X), abs(mid.Y-centre.Y)) / 64
			if !at.Overlaps(view) {
				dist += penalty // ahead of the view: after everything in it
			}
			fp.wanted = append(fp.wanted, wantedPart{key{i, dpi, r}, dist})
			fp.bytes += 4 * r.Dx() * r.Dy()
		}
	}
	return fp
}

// drawPage paints page pg: paper, an earlier bitmap of it stretched in place,
// then each part cached at dpi over that.
func (v *View) drawPage(c *paint.Canvas, rect image.Rectangle, pg pagePlan, dpi int) {
	vr := pg.vr.Add(rect.Min)
	px := v.pixelSize(pg.page, dpi)
	type placed struct {
		at  image.Rectangle
		img *image.RGBA
	}
	var done []placed
	covered := true
	for _, r := range parts(px) {
		at := place(vr, r, px)
		if !at.Overlaps(rect) {
			continue
		}
		if img := v.pool.lookup(v, key{pg.page, dpi, r}); img != nil {
			done = append(done, placed{at, img})
		} else {
			covered = false
		}
	}
	v.pool.prune(v, pg.page, dpi, !covered)
	if !covered {
		// Paper, and an earlier bitmap of the page stretched in place, show
		// where parts are still to come. prune keeps the newest earlier bitmap
		// only while parts are missing.
		c.Fill(vr, color.NRGBA{255, 255, 255, 255})
		for _, old := range v.pool.standIns(v, pg.page, dpi) {
			c.DrawScaled(place(vr, old.k.rect, v.pixelSize(pg.page, old.k.dpi)), old.img)
		}
	}
	for _, d := range done {
		c.DrawScaled(d.at, d.img)
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
			v.zoomAt(math.Pow(zoomStep, -ev.DY/10), image.Pt(int(ev.X), int(ev.Y)))
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

// zoomBy zooms by the factor f around the middle of the view.
func (v *View) zoomBy(f float64) {
	w, h := v.viewport()
	v.zoomAt(f, image.Pt(w/2, h/2))
}

// zoomAt zooms by the factor f, keeping the content under the viewport point
// at in place.
func (v *View) zoomAt(f float64, at image.Point) { v.lay.ZoomAt(v.lay.Scale()*f, at) }
