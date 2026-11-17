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
	"sync"
	"time"

	"github.com/voidrab/gopdfrab"

	"viewrab/internal/ui/layout"
	"viewrab/internal/ui/paint"
	"viewrab/internal/ui/platform"
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
	// OpenURI is called with the address of a link the user follows; nil
	// means such links do nothing.
	OpenURI func(uri string)
	// Selection is the colour laid over selected text; zero means a
	// translucent blue.
	Selection color.NRGBA
	// Match and CurrentMatch are the colours laid over the matches of a search
	// and over the one that is current; zero means translucent yellow and
	// orange.
	Match, CurrentMatch color.NRGBA
	// LinkHover is the colour laid over the link under the pointer; zero
	// means translucent blue.
	LinkHover color.NRGBA
	// Scrollbar is the colour of the scrollbars drawn over the pages, whose
	// opacity the view sets; zero means a mid gray.
	Scrollbar color.NRGBA
	// Render, if not nil, renders pages in place of the document's own
	// rendering. It must return the pixels gopdfrab's Page.Render returns. It
	// lets pages render elsewhere, such as in other processes.
	Render RenderFunc
	// Parallel is the number of renders of the view that may run at once;
	// zero means one. A document's own rendering handles one page at a time,
	// so a larger value helps only with a Render that renders elsewhere, such
	// as in other processes that each have the document open.
	Parallel int
	// Zoom is the zoom the view opens at, and the one Ctrl+0 goes back to;
	// the zero value means fit width.
	Zoom ZoomPreset
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
	if o.Zoom == (ZoomPreset{}) {
		o.Zoom = ZoomPresets[0] // fit width
	}
	if o.Selection == (color.NRGBA{}) {
		o.Selection = color.NRGBA{0x3b, 0x72, 0xd9, 0x5a}
	}
	if o.Match == (color.NRGBA{}) {
		o.Match = color.NRGBA{0xff, 0xd0, 0x00, 0x78}
	}
	if o.CurrentMatch == (color.NRGBA{}) {
		o.CurrentMatch = color.NRGBA{0xff, 0x8c, 0x00, 0xb4}
	}
	if o.LinkHover == (color.NRGBA{}) {
		o.LinkHover = color.NRGBA{0x2f, 0x80, 0xed, 0x50}
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

// RenderFunc renders the part region of p at scale, as gopdfrab's Page.Render
// does with those options.
type RenderFunc func(ctx context.Context, p *gopdfrab.Page, scale float64, region image.Rectangle) (*image.RGBA, error)

func renderPage(ctx context.Context, p *gopdfrab.Page, scale float64, region image.Rectangle) (*image.RGBA, error) {
	return p.Render(ctx, gopdfrab.RenderOptions{Scale: scale, Region: region})
}

// View is a document in a viewport. Draw and HandleEvent belong to one
// goroutine; rendering happens on the pool's workers.
type View struct {
	pages      []*gopdfrab.Page
	sizes      []layout.Size // of the pages, in points
	render     RenderFunc
	parallel   int // the renders of the view that may run at once
	opts       Options
	invalidate func()
	lay        *layout.Layout
	ptr        pointerState

	// Page text and links, read in the background, and the state of the
	// selection and the search. dataMu guards datas, loading and find.
	dataMu     sync.Mutex
	datas      map[int]*pageData
	loading    map[int]bool
	loadCtx    context.Context
	loadCancel context.CancelFunc
	loadWG     sync.WaitGroup
	glyphs     func(ctx context.Context, page int) ([]gopdfrab.Glyph, error)
	links      func(ctx context.Context, page int) ([]gopdfrab.Link, error)
	text       textState
	find       *findState // the search, or nil

	// Rapid zooming is not rendered until it pauses; see deferring.
	now       func() time.Time
	after     func(d time.Duration, f func()) stopper
	dpi       int       // the dpi last drawn at
	density   float64   // device pixels per view pixel, from the last resize
	changedAt time.Time // when it last changed
	quietAt   time.Time // the time before which renders at a new dpi wait
	timer     stopper

	pool     *Pool
	ownsPool bool
	// The rest is the pool's: it is guarded by the pool's lock.
	id       int
	inflight map[key]*job // the renders asked for and not yet delivered
	running  int          // the renders of this view in progress
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
	v := &View{opts: opts, invalidate: invalidate, render: renderPage, parallel: max(1, opts.Parallel), pool: opts.Pool,
		now: time.Now, after: afterFunc, datas: map[int]*pageData{}, loading: map[int]bool{}}
	if opts.Render != nil {
		v.render = opts.Render
	}
	v.loadCtx, v.loadCancel = context.WithCancel(context.Background())
	v.glyphs = func(ctx context.Context, i int) ([]gopdfrab.Glyph, error) { return v.pages[i].Glyphs(ctx) }
	v.links = func(ctx context.Context, i int) ([]gopdfrab.Link, error) { return v.pages[i].Links(ctx) }
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
	v.SetZoomPreset(v.opts.Zoom)
	if v.pool == nil {
		v.pool, v.ownsPool = NewPool(PoolOptions{Workers: opts.Workers, CacheBytes: opts.CacheBytes}), true
	}
	v.pool.attach(v, viewport)
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
	v.ClearFind()
	v.loadCancel()
	v.loadWG.Wait()
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

// maxDensity bounds the device pixels per view pixel pages are rendered at.
// A display of three, as phones have, would cost more than twice the renders
// and memory of two for a difference hard to see.
const maxDensity = 2

// renderDPI returns the dpi pages are rendered at: the dpi of the zoom,
// multiplied by the display's density up to maxDensity.
func (v *View) renderDPI() int {
	return dpiFor(v.lay.Scale() * min(max(v.density, 1), maxDensity))
}

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
	v.revealFind()
	v.want(c, rect)
	v.drawText(c, rect.Min)
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
	dpi := v.renderDPI()
	view := image.Rectangle{Max: v.lay.Viewport()}
	plan := v.plan(dpi, view, true)
	if plan.bytes > v.pool.budgetBytes()/2 {
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
	// The parts are placed in device pixels, where they meet with no seam.
	dvr, drect := c.Device(vr), c.Device(rect)
	px := v.pixelSize(pg.page, dpi)
	// A soft shadow, fading out over a few pixels, lifts the paper off the
	// backdrop as in the webview build.
	c.Shadow(vr, 0, 0, pageShadow)
	type placed struct {
		at  image.Rectangle
		img *image.RGBA
	}
	var done []placed
	covered := true
	for _, r := range parts(px) {
		at := place(dvr, r, px)
		if !at.Overlaps(drect) {
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
			c.DrawScaled(place(dvr, old.k.rect, v.pixelSize(pg.page, old.k.dpi)), old.img)
		}
	}
	for _, d := range done {
		c.DrawScaled(d.at, d.img)
	}
}

// pageShadow is the opacity of each ring of a page's shadow, outwards.
var pageShadow = []uint8{36, 20, 10, 5}

func abs(v int) int { return max(v, -v) }

// SetColors changes the colours of the backdrop behind the pages and of the
// selection laid over text.
func (v *View) SetColors(background, selection color.NRGBA) {
	v.opts.Background, v.opts.Selection = background, selection
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

// CSSScale is the layout scale of a zoom of 100%: 96 pixels per inch, as CSS
// counts them, over PDF's 72 points per inch. Zoom percentages are given in
// these units, so that a page shows at the same size in both builds.
const CSSScale = 96.0 / 72

// ZoomPreset is one of the zooms offered in the zoom menu: a fit mode, or a
// fixed zoom when Mode is layout.ZoomFixed, where Scale 1 is 100%.
type ZoomPreset struct {
	Label string
	Mode  layout.ZoomMode
	Scale float64
}

// ZoomPresets are the zooms the zoom menu offers, as in the webview build: the
// two fit modes, then fixed scales in ascending order.
var ZoomPresets = []ZoomPreset{
	{"fit width", layout.ZoomFitWidth, 0}, {"fit page", layout.ZoomFitPage, 0},
	{"50%", layout.ZoomFixed, 0.5}, {"75%", layout.ZoomFixed, 0.75}, {"100%", layout.ZoomFixed, 1},
	{"125%", layout.ZoomFixed, 1.25}, {"150%", layout.ZoomFixed, 1.5}, {"200%", layout.ZoomFixed, 2},
	{"300%", layout.ZoomFixed, 3},
}

// DefaultZoom is the zoom the application opens documents at: 150%.
var DefaultZoom = ZoomPresets[6]

// SetZoomPreset applies the zoom preset p.
func (v *View) SetZoomPreset(p ZoomPreset) {
	if p.Mode == layout.ZoomFixed {
		v.lay.SetZoom(p.Scale * CSSScale)
		return
	}
	v.lay.SetMode(p.Mode)
}

// ZoomPresetIndex returns the index in ZoomPresets of the zoom in use, or -1
// for a scale no preset has.
func (v *View) ZoomPresetIndex() int {
	for i, p := range ZoomPresets {
		if p.Mode == v.lay.Mode() && (p.Mode != layout.ZoomFixed || math.Abs(p.Scale*CSSScale-v.lay.Scale()) < 1e-9) {
			return i
		}
	}
	return -1
}

// ZoomLabel describes the zoom for display: the label of its preset, such as
// "fit width", or else a percentage.
func (v *View) ZoomLabel() string {
	if i := v.ZoomPresetIndex(); i >= 0 {
		return ZoomPresets[i].Label
	}
	return v.ZoomPercent()
}

// ZoomPercent returns the zoom as a percentage, such as "134%", whatever the
// zoom mode.
func (v *View) ZoomPercent() string {
	return fmt.Sprintf("%d%%", int(math.Round(v.lay.Scale()/CSSScale*100)))
}

// HandleEvent applies a resize, scroll or key event and reports whether the
// view needs redrawing. Events it does not use return false.
func (v *View) HandleEvent(ev platform.Event) bool {
	switch ev := ev.(type) {
	case platform.ResizeEvent:
		v.density = ev.Scale
		v.lay.SetViewport(ev.Size.W, ev.Size.H)
		v.pool.setViewport(v, v.lay.Viewport())
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
			v.SetZoomPreset(v.opts.Zoom)
		case 'a':
			v.SelectAll()
		default:
			return false
		}
		return true
	}
	switch ev.Key {
	case platform.KeyEscape:
		return v.ClearSelection()
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
