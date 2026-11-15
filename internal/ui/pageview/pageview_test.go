package pageview

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/voidrab/gopdfrab"

	"viewrab/internal/testpdf"
	"viewrab/internal/ui/paint"
	"viewrab/internal/ui/platform"
)

var (
	paper = color.RGBA{255, 255, 255, 255}
	red   = color.RGBA{255, 0, 0, 255}
)

// openDoc opens a document of n 100x100 pages.
func openDoc(t *testing.T, n int) *gopdfrab.Document {
	t.Helper()
	sizes := make([][2]int, n)
	for i := range sizes {
		sizes[i] = [2]int{100, 100}
	}
	doc, err := gopdfrab.OpenBytes(testpdf.BuildPages(sizes))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { doc.Close() })
	return doc
}

// newView builds a view and a channel that receives one value per
// invalidation.
func newView(t *testing.T, doc Document, vp image.Point, opts Options) (*View, chan struct{}) {
	t.Helper()
	inv := make(chan struct{}, 64)
	v, err := New(doc, vp, opts, func() { inv <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	v.now = (&autoClock{}).now
	t.Cleanup(v.Close)
	return v, inv
}

// autoClock is a clock that moves a second on at every reading, so that
// changes of zoom made by a test are never rapid.
type autoClock struct{ t time.Time }

func (c *autoClock) now() time.Time {
	c.t = c.t.Add(time.Second)
	return c.t
}

// inflightCount returns how many renders v has asked for and not received.
func inflightCount(v *View) int {
	v.pool.mu.Lock()
	defer v.pool.mu.Unlock()
	return len(v.inflight)
}

// testKey is the key of a 5x5 part of a page at dpi.
func testKey(page, dpi int) key { return key{page: page, dpi: dpi, rect: image.Rect(0, 0, 5, 5)} }

func waitInv(t *testing.T, inv chan struct{}) {
	t.Helper()
	select {
	case <-inv:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for a render")
	}
}

func TestDrawRendersVisiblePagesInTheBackground(t *testing.T) {
	v, inv := newView(t, openDoc(t, 3), image.Pt(124, 200), Options{Gap: 12, Margin: 12})
	gate := make(chan struct{})
	v.render = func(ctx context.Context, p *gopdfrab.Page, scale float64, region image.Rectangle) (*image.RGBA, error) {
		<-gate // nothing is rendered until the placeholder has been looked at
		return renderPage(ctx, p, scale, region)
	}
	c := paint.New(124, 200)
	v.Draw(c)
	r := v.Layout().ViewRect(0)
	mid := r.Min.Add(r.Size().Div(2))
	if got := c.Img.RGBAAt(mid.X, mid.Y); got != paper {
		t.Fatalf("placeholder pixel = %v, want blank paper", got)
	}
	if got := c.Img.RGBAAt(2, 2); got != (color.RGBA{0x3a, 0x3b, 0x3f, 255}) {
		t.Errorf("background pixel = %v", got)
	}
	close(gate)
	waitInv(t, inv)
	v.Draw(c)
	if got := c.Img.RGBAAt(mid.X, mid.Y); got != red {
		t.Errorf("rendered pixel = %v, want red", got)
	}
}

func TestStaleBitmapStandsInWhileRerendering(t *testing.T) {
	gate := make(chan struct{})
	v, inv := newView(t, openDoc(t, 1), image.Pt(124, 124), Options{})
	v.render = func(ctx context.Context, p *gopdfrab.Page, scale float64, region image.Rectangle) (*image.RGBA, error) {
		if scale > 1.1 { // the render after zooming waits
			<-gate
		}
		return renderPage(ctx, p, scale, region)
	}
	c := paint.New(124, 124)
	v.Draw(c)
	waitInv(t, inv)
	v.HandleEvent(platform.KeyEvent{Down: true, Rune: '+', Mods: platform.Ctrl})
	v.Draw(c) // the new scale is not rendered yet, so the old bitmap is stretched
	r := v.Layout().ViewRect(0)
	if got := c.Img.RGBAAt(r.Min.X+r.Dx()/2, r.Min.Y+r.Dy()/2); got != red {
		t.Errorf("stand-in pixel = %v, want red", got)
	}
	close(gate)
	waitInv(t, inv)
}

func TestSchedulingIsDeduplicated(t *testing.T) {
	var calls atomic.Int32
	gate := make(chan struct{})
	v, inv := newView(t, openDoc(t, 1), image.Pt(124, 124), Options{})
	v.render = func(ctx context.Context, p *gopdfrab.Page, scale float64, region image.Rectangle) (*image.RGBA, error) {
		calls.Add(1)
		<-gate
		return image.NewRGBA(image.Rect(0, 0, 1, 1)), nil
	}
	c := paint.New(124, 124)
	v.Draw(c)
	v.Draw(c)
	v.Draw(c)
	close(gate)
	waitInv(t, inv)
	waitFor(t, "the renders to finish", func() bool { return inflightCount(v) == 0 })
	// One quick preview and the page itself, however often it was drawn.
	if n := calls.Load(); n != 2 {
		t.Errorf("render called %d times", n)
	}
}

func TestRenderFailureIsNotCachedAndIsRetried(t *testing.T) {
	var calls atomic.Int32
	done := make(chan struct{}, 4)
	v, inv := newView(t, openDoc(t, 1), image.Pt(124, 124), Options{})
	v.render = func(ctx context.Context, p *gopdfrab.Page, scale float64, region image.Rectangle) (*image.RGBA, error) {
		if scale < 0.6 { // the preview never succeeds
			return nil, errors.New("no preview")
		}
		defer func() { done <- struct{}{} }()
		if calls.Add(1) == 1 {
			return nil, errors.New("boom")
		}
		return image.NewRGBA(image.Rect(0, 0, 1, 1)), nil
	}
	c := paint.New(124, 124)
	v.Draw(c)
	<-done
	// The failed attempt leaves nothing behind, so the next Draw tries again.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if inflightCount(v) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("failed render stayed in flight")
		}
		time.Sleep(time.Millisecond)
	}
	v.Draw(c)
	waitInv(t, inv)
	if calls.Load() != 2 {
		t.Errorf("the page was rendered %d times", calls.Load())
	}
}

func TestScrollingAwayCancelsRenders(t *testing.T) {
	started := make(chan struct{}, 8)
	var mu sync.Mutex
	ran := map[int]bool{}
	v, inv := newView(t, openDoc(t, 12), image.Pt(124, 300), Options{Workers: 1})
	v.render = func(ctx context.Context, p *gopdfrab.Page, scale float64, region image.Rectangle) (*image.RGBA, error) {
		mu.Lock()
		for i, q := range v.pages {
			if q == p {
				ran[i] = true
			}
		}
		mu.Unlock()
		started <- struct{}{}
		<-ctx.Done() // runs until it is cancelled
		return nil, ctx.Err()
	}
	c := paint.New(124, 300)
	v.Draw(c) // several pages visible: page 0 renders, the others wait for the worker
	<-started
	v.HandleEvent(platform.KeyEvent{Down: true, Key: platform.KeyEnd})
	v.Draw(c) // pages 0-2 are no longer visible: their renders are cancelled
	v.Close()
	mu.Lock()
	defer mu.Unlock()
	early := 0
	for _, i := range []int{0, 1, 2} {
		if ran[i] {
			early++
		}
	}
	if early != 1 {
		t.Errorf("renders started for pages %v; only the first of the early pages should have", ran)
	}
	select {
	case <-inv:
		t.Error("a cancelled render invalidated the view")
	default:
	}
}

func TestStaleResultIsDiscarded(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan struct{})
	v, inv := newView(t, openDoc(t, 12), image.Pt(124, 300), Options{})
	var once sync.Once
	v.render = func(ctx context.Context, p *gopdfrab.Page, scale float64, region image.Rectangle) (*image.RGBA, error) {
		once.Do(func() { close(started) })
		<-gate // ignores cancellation and still succeeds
		return image.NewRGBA(image.Rect(0, 0, 1, 1)), nil
	}
	c := paint.New(124, 300)
	v.Draw(c)
	<-started
	v.HandleEvent(platform.KeyEvent{Down: true, Key: platform.KeyEnd})
	v.Draw(c) // withdraws the interest in the early pages
	close(gate)
	v.Close()
	// Only the renders of pages still on screen may have invalidated.
	// The view's bitmaps were dropped with it, and a late result is not kept.
	v.pool.mu.Lock()
	defer v.pool.mu.Unlock()
	if len(v.pool.cache) != 0 {
		t.Errorf("%d bitmaps survived the view", len(v.pool.cache))
	}
	_ = inv
}

func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	v, _ := newView(t, openDoc(t, 1), image.Pt(124, 124), Options{CacheBytes: 100})
	p := v.pool
	p.mu.Lock()
	for i := range 3 {
		p.store(v, testKey(i, 72), image.NewRGBA(image.Rect(0, 0, 5, 5))) // 100 bytes each
	}
	n := len(p.cache)
	_, oldest := p.cache[cacheKey{v.id, testKey(0, 72)}]
	p.mu.Unlock()
	if n != 1 || oldest {
		t.Errorf("cache holds %d entries, oldest kept: %v", n, oldest)
	}
	if p.lookup(v, testKey(2, 72)) == nil || p.lookup(v, testKey(0, 72)) != nil {
		t.Error("lookup disagrees with eviction")
	}
}

func TestEvents(t *testing.T) {
	v, _ := newView(t, openDoc(t, 5), image.Pt(124, 124), Options{})
	lay := v.Layout()
	if !v.HandleEvent(platform.ResizeEvent{Size: platform.Size{W: 224, H: 100}, Scale: 1}) || lay.Viewport() != image.Pt(224, 100) {
		t.Errorf("resize: %v", lay.Viewport())
	}
	step := func(ev platform.Event) image.Point {
		before := lay.Scroll()
		if !v.HandleEvent(ev) {
			t.Errorf("%#v not handled", ev)
		}
		return lay.Scroll().Sub(before)
	}
	key := func(k platform.Key) platform.Event { return platform.KeyEvent{Down: true, Key: k} }
	ctrl := func(r rune) platform.Event { return platform.KeyEvent{Down: true, Rune: r, Mods: platform.Ctrl} }

	if d := step(platform.ScrollEvent{DY: 10}); d != image.Pt(0, 50) {
		t.Errorf("wheel scrolled %v", d)
	}
	if d := step(key(platform.KeyDown)); d != image.Pt(0, 40) {
		t.Errorf("Down scrolled %v", d)
	}
	if d := step(key(platform.KeyUp)); d != image.Pt(0, -40) {
		t.Errorf("Up scrolled %v", d)
	}
	if d := step(key(platform.KeyPageDown)); d != image.Pt(0, 90) {
		t.Errorf("PageDown scrolled %v", d)
	}
	if d := step(key(platform.KeyPageUp)); d != image.Pt(0, -90) {
		t.Errorf("PageUp scrolled %v", d)
	}
	step(key(platform.KeyEnd))
	if lay.Scroll().Y == 0 {
		t.Error("End did not scroll")
	}
	step(key(platform.KeyHome))
	if lay.Scroll() != (image.Point{}) {
		t.Errorf("Home -> %v", lay.Scroll())
	}
	step(key(platform.KeyRight))
	if lay.Current() != 1 {
		t.Errorf("Right -> page %d", lay.Current())
	}
	step(key(platform.KeyLeft))
	if lay.Current() != 0 {
		t.Errorf("Left -> page %d", lay.Current())
	}

	base := lay.Scale()
	step(ctrl('='))
	if lay.Scale() <= base {
		t.Error("Ctrl+= did not zoom in")
	}
	step(ctrl('-'))
	step(ctrl('+'))
	step(platform.ScrollEvent{DY: -10, Mods: platform.Ctrl}) // wheel up zooms in
	if lay.Scale() <= base {
		t.Errorf("Ctrl+wheel up: scale %v", lay.Scale())
	}
	step(ctrl('0'))
	if lay.Mode() != 1 { // fit width
		t.Errorf("Ctrl+0 left mode %v", lay.Mode())
	}

	for _, ev := range []platform.Event{
		ctrl('x'), key(platform.KeyTab), platform.KeyEvent{Key: platform.KeyDown}, // a release
		platform.FocusEvent{}, platform.PointerEvent{},
	} {
		if v.HandleEvent(ev) {
			t.Errorf("%#v should not be handled", ev)
		}
	}
}

type failingDoc struct {
	*gopdfrab.Document
	pagesErr, pageErr error
}

func (d failingDoc) Pages() (int, error) {
	if d.pagesErr != nil {
		return 0, d.pagesErr
	}
	return d.Document.Pages()
}

func (d failingDoc) Page(i int) (*gopdfrab.Page, error) {
	if d.pageErr != nil {
		return nil, d.pageErr
	}
	return d.Document.Page(i)
}

func TestNewReportsDocumentErrors(t *testing.T) {
	doc := openDoc(t, 2)
	boom := errors.New("boom")
	if _, err := New(failingDoc{Document: doc, pagesErr: boom}, image.Pt(10, 10), Options{}, nil); !errors.Is(err, boom) {
		t.Errorf("Pages failure = %v", err)
	}
	if _, err := New(failingDoc{Document: doc, pageErr: boom}, image.Pt(10, 10), Options{}, nil); !errors.Is(err, boom) {
		t.Errorf("Page failure = %v", err)
	}
}

func TestDefaultOptions(t *testing.T) {
	v, _ := newView(t, openDoc(t, 1), image.Pt(10, 10), Options{})
	o := v.opts
	if o.Gap != 12 || o.Margin != 12 || o.ScrollSpeed != 5 {
		t.Errorf("defaults = %+v", o)
	}
}

func TestDrawInPaintsOnlyItsRectangle(t *testing.T) {
	v, inv := newView(t, openDoc(t, 2), image.Pt(124, 124), Options{})
	c := paint.New(300, 300)
	rect := image.Rect(100, 150, 224, 274)
	v.DrawIn(c, rect)
	waitInv(t, inv)
	v.DrawIn(c, rect)
	if got := c.Img.RGBAAt(99, 200); got.A != 0 {
		t.Errorf("painted left of the rectangle: %v", got)
	}
	if got := c.Img.RGBAAt(150, 149); got.A != 0 {
		t.Errorf("painted above the rectangle: %v", got)
	}
	// The first page starts at the layout's offset inside the rectangle.
	r := v.Layout().ViewRect(0).Add(rect.Min)
	mid := r.Min.Add(r.Size().Div(2))
	if got := c.Img.RGBAAt(mid.X, mid.Y); got != red {
		t.Errorf("page pixel = %v, want red", got)
	}
}

func TestPageNavigationAndZoomSteps(t *testing.T) {
	v, _ := newView(t, openDoc(t, 5), image.Pt(124, 124), Options{})
	if v.Pages() != 5 || v.Page() != 0 {
		t.Fatalf("pages %d, page %d", v.Pages(), v.Page())
	}
	v.GoToPage(3)
	if v.Page() != 3 {
		t.Errorf("after GoToPage(3), page %d", v.Page())
	}
	v.GoToPage(99)
	if v.Page() != 4 {
		t.Errorf("after GoToPage(99), page %d", v.Page())
	}
	before := v.Layout().Scale()
	v.ZoomIn()
	if v.Layout().Scale() <= before {
		t.Error("ZoomIn did not zoom in")
	}
	v.ZoomOut()
	v.ZoomOut()
	if v.Layout().Scale() >= before {
		t.Error("ZoomOut did not zoom out")
	}
}

func TestZoomLabelNamesThePresetOrThePercentage(t *testing.T) {
	v, _ := newView(t, openDoc(t, 3), image.Pt(124, 124), Options{})
	want := []string{"fit width", "fit page", "50%", "75%", "100%", "125%", "150%", "200%", "300%"}
	for i, p := range ZoomPresets {
		v.SetZoomPreset(p)
		if got := v.ZoomLabel(); got != want[i] {
			t.Errorf("preset %d: label %q, want %q", i, got, want[i])
		}
	}
	// A preset's percentage is in CSS pixels: 100% is 96 dpi.
	v.SetZoomPreset(ZoomPresets[4])
	if got := v.Layout().Scale(); math.Abs(got-CSSScale) > 1e-9 {
		t.Errorf("100%% is scale %v, want %v", got, CSSScale)
	}
	// A scale no preset has is labelled with its percentage.
	v.Layout().SetZoom(1.337 * CSSScale)
	if got := v.ZoomLabel(); got != "134%" || v.ZoomPresetIndex() != -1 {
		t.Errorf("at 133.7%%: label %q, preset %d", got, v.ZoomPresetIndex())
	}
}

func TestZoomPresetsApplyAndAreRecognised(t *testing.T) {
	v, _ := newView(t, openDoc(t, 3), image.Pt(124, 124), Options{})
	for i, p := range ZoomPresets {
		v.SetZoomPreset(p)
		if got := v.ZoomPresetIndex(); got != i {
			t.Errorf("after %q: preset %d, want %d", p.Label, got, i)
		}
	}
	// Fit modes still report the scale they arrive at.
	v.SetZoomPreset(ZoomPresets[0])
	if want := fmt.Sprintf("%d%%", int(math.Round(v.Layout().Scale()*75))); v.ZoomPercent() != want {
		t.Errorf("ZoomPercent = %q, want %q", v.ZoomPercent(), want)
	}
}

func TestSetColorsRepaintsTheBackdrop(t *testing.T) {
	v, _ := newView(t, openDoc(t, 1), image.Pt(60, 60), Options{})
	bg, sel := color.NRGBA{10, 20, 30, 255}, color.NRGBA{1, 2, 3, 4}
	v.SetColors(bg, sel)
	if v.opts.Background != bg || v.opts.Selection != sel {
		t.Errorf("colours %v %v", v.opts.Background, v.opts.Selection)
	}
	c := paint.New(60, 60)
	v.Draw(c)
	if p := c.Img.RGBAAt(0, 0); p != (color.RGBA{10, 20, 30, 255}) {
		t.Errorf("backdrop = %v", p)
	}
}
