package pageview

import (
	"context"
	"errors"
	"image"
	"image/color"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/voidrab/gopdfrab"

	"viewer/internal/testpdf"
	"viewer/internal/ui/paint"
	"viewer/internal/ui/platform"
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
	t.Cleanup(v.Close)
	return v, inv
}

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
	waitInv(t, inv)
	v.Draw(c)
	if got := c.Img.RGBAAt(mid.X, mid.Y); got != red {
		t.Errorf("rendered pixel = %v, want red", got)
	}
}

func TestStaleBitmapStandsInWhileRerendering(t *testing.T) {
	gate := make(chan struct{})
	v, inv := newView(t, openDoc(t, 1), image.Pt(124, 124), Options{})
	v.render = func(ctx context.Context, p *gopdfrab.Page, scale float64) (*image.RGBA, error) {
		if scale > 1.5 { // the re-render after zooming waits
			<-gate
		}
		return renderPage(ctx, p, scale)
	}
	c := paint.New(124, 124)
	v.Draw(c)
	waitInv(t, inv)
	v.HandleEvent(platform.KeyEvent{Down: true, Rune: '+', Mods: platform.Ctrl})
	v.Draw(c) // the new scale is not rendered yet: the old bitmap is stretched
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
	v.render = func(ctx context.Context, p *gopdfrab.Page, scale float64) (*image.RGBA, error) {
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
	if n := calls.Load(); n != 1 {
		t.Errorf("render called %d times", n)
	}
}

func TestRenderFailureIsNotCachedAndIsRetried(t *testing.T) {
	var calls atomic.Int32
	done := make(chan struct{}, 4)
	v, inv := newView(t, openDoc(t, 1), image.Pt(124, 124), Options{})
	v.render = func(ctx context.Context, p *gopdfrab.Page, scale float64) (*image.RGBA, error) {
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
		v.mu.Lock()
		idle := len(v.inflight) == 0
		v.mu.Unlock()
		if idle {
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
		t.Errorf("render called %d times", calls.Load())
	}
}

func TestScrollingAwayCancelsRenders(t *testing.T) {
	started := make(chan struct{}, 8)
	var mu sync.Mutex
	ran := map[int]bool{}
	v, inv := newView(t, openDoc(t, 12), image.Pt(124, 300), Options{Workers: 1})
	v.render = func(ctx context.Context, p *gopdfrab.Page, scale float64) (*image.RGBA, error) {
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
	if !ran[0] || ran[1] || ran[2] {
		t.Errorf("renders started for pages %v; only page 0 of the early pages should have", ran)
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
	v.render = func(ctx context.Context, p *gopdfrab.Page, scale float64) (*image.RGBA, error) {
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
	v.mu.Lock()
	defer v.mu.Unlock()
	for k := range v.cache {
		if k.page < 6 {
			t.Errorf("a result for the hidden page %d was cached", k.page)
		}
	}
	_ = inv
}

func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	v, _ := newView(t, openDoc(t, 1), image.Pt(124, 124), Options{CacheBytes: 100})
	v.mu.Lock()
	for i := range 3 {
		v.store(key{i, 72}, image.NewRGBA(image.Rect(0, 0, 5, 5))) // 100 bytes each
	}
	n := len(v.cache)
	_, oldest := v.cache[key{0, 72}]
	v.mu.Unlock()
	if n != 1 || oldest {
		t.Errorf("cache holds %d entries, oldest kept: %v", n, oldest)
	}
	if v.lookup(key{2, 72}) == nil || v.lookup(key{0, 72}) != nil {
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
	if o.Gap != 12 || o.Margin != 12 || o.CacheBytes != 256<<20 || o.Workers < 1 || o.ScrollSpeed != 5 {
		t.Errorf("defaults = %+v", o)
	}
}
