package layout

import (
	"image"
	"math"
	"testing"
)

// sample returns a layout of three pages: two portrait pages and one wide page.
func sample() *Layout {
	return New([]Size{{100, 200}, {100, 200}, {200, 100}}, image.Pt(220, 300), 10, 10)
}

func TestFitWidthAndGeometry(t *testing.T) {
	l := sample()
	if l.Viewport() != image.Pt(220, 300) {
		t.Errorf("Viewport = %v", l.Viewport())
	}
	if l.Mode() != ZoomFitWidth || l.Pages() != 3 {
		t.Fatalf("mode %v pages %d", l.Mode(), l.Pages())
	}
	// Widest page 200pt into 200px of usable width: scale 1.
	if l.Scale() != 1 {
		t.Fatalf("Scale = %v", l.Scale())
	}
	if got := l.PageRect(0); got != image.Rect(60, 10, 160, 210) {
		t.Errorf("PageRect(0) = %v", got)
	}
	if got := l.PageRect(2); got != image.Rect(10, 430, 210, 530) {
		t.Errorf("PageRect(2) = %v", got)
	}
	if got := l.DocumentSize(); got != image.Pt(220, 540) {
		t.Errorf("DocumentSize = %v", got)
	}
}

func TestScrollVisibleCurrent(t *testing.T) {
	l := sample()
	if f, e := l.Visible(); f != 0 || e != 2 {
		t.Errorf("Visible = %d,%d", f, e)
	}
	l.ScrollBy(image.Pt(0, 1000))
	if l.Scroll().Y != 240 { // 540 total - 300 viewport
		t.Errorf("Scroll = %v", l.Scroll())
	}
	if f, e := l.Visible(); f != 1 || e != 3 {
		t.Errorf("Visible = %d,%d", f, e)
	}
	if c := l.Current(); c != 1 { // the middle, y=390, is inside page 1 (220..420)
		t.Errorf("Current = %d", c)
	}
	l.ScrollTo(image.Pt(0, 1000))
	l.ScrollBy(image.Pt(0, 0))
	if c := l.Current(); c != 1 {
		t.Errorf("Current = %d", c)
	}
	l.ScrollTo(image.Pt(0, -5))
	if l.Scroll() != (image.Point{}) || l.Current() != 0 {
		t.Errorf("after ScrollTo: %v %d", l.Scroll(), l.Current())
	}
	l.GoTo(1)
	if l.Scroll().Y != 210 {
		t.Errorf("GoTo(1) -> %v", l.Scroll())
	}
	l.GoTo(99) // clamped to the last page, then to the document
	l.GoTo(-4)
	if l.Scroll().Y != 0 {
		t.Errorf("GoTo(-4) -> %v", l.Scroll())
	}
	if r := l.ViewRect(0); r != l.PageRect(0) {
		t.Errorf("ViewRect = %v", r)
	}
}

func TestZoomKeepsTheViewAnchored(t *testing.T) {
	l := sample()
	l.SetZoom(1)
	l.ScrollTo(image.Pt(0, 110)) // halfway down page 0 (top 10, height 200)
	l.SetZoom(2)
	if l.Scale() != 2 || l.Mode() != ZoomFixed {
		t.Fatalf("zoom %v mode %v", l.Scale(), l.Mode())
	}
	if y := l.Scroll().Y; y != 10+200 { // still halfway down page 0, now 400 high
		t.Errorf("Scroll.Y = %d", y)
	}
	l.SetZoom(100)
	if l.Scale() != MaxScale {
		t.Errorf("Scale = %v", l.Scale())
	}
	l.SetZoom(0)
	if l.Scale() != MinScale {
		t.Errorf("Scale = %v", l.Scale())
	}
}

func TestHorizontalScrollWhenWiderThanViewport(t *testing.T) {
	l := sample()
	l.SetZoom(2) // widest page 400px in a 220px viewport
	if l.DocumentSize().X != 420 {
		t.Fatalf("DocumentSize = %v", l.DocumentSize())
	}
	l.ScrollBy(image.Pt(1000, 0))
	if l.Scroll().X != 200 {
		t.Errorf("Scroll.X = %d", l.Scroll().X)
	}
	l.SetViewport(320, 300) // keeps the same fraction of the scrollable width
	if l.Scroll().X != 100 {
		t.Errorf("Scroll.X after resize = %d", l.Scroll().X)
	}
}

func TestFitPage(t *testing.T) {
	l := sample()
	l.SetMode(ZoomFitPage)
	// Page 0: min(200/100, 280/200) = 1.4.
	if math.Abs(l.Scale()-1.4) > 1e-9 || l.Mode() != ZoomFitPage {
		t.Errorf("Scale = %v", l.Scale())
	}
	l.SetViewport(0, 0) // degenerate viewport falls to the minimum
	if l.Scale() != MinScale {
		t.Errorf("Scale = %v", l.Scale())
	}
}

func TestHitPage(t *testing.T) {
	l := sample()
	i, pt, ok := l.HitPage(image.Pt(110, 60))
	if !ok || i != 0 || pt != (Size{50, 50}) {
		t.Errorf("HitPage = %d %v %v", i, pt, ok)
	}
	if _, _, ok := l.HitPage(image.Pt(5, 60)); ok {
		t.Error("hit the margin")
	}
}

func TestEmptyDocument(t *testing.T) {
	l := New(nil, image.Pt(100, 100), 10, 10)
	l.SetZoom(2)
	l.SetViewport(50, 50)
	l.GoTo(0)
	if f, e := l.Visible(); f != 0 || e != 0 {
		t.Errorf("Visible = %d,%d", f, e)
	}
	if l.Current() != 0 || l.DocumentSize().Y != 0 || l.Scroll() != (image.Point{}) {
		t.Errorf("empty layout: %d %v %v", l.Current(), l.DocumentSize(), l.Scroll())
	}
	if _, _, ok := l.HitPage(image.Pt(1, 1)); ok {
		t.Error("hit in an empty document")
	}
}

func TestSinglePageGap(t *testing.T) {
	l := New([]Size{{50, 50}}, image.Pt(100, 100), 10, 5)
	if got := l.DocumentSize(); got.Y != 100 { // fit width: scale 1.8
		t.Errorf("fit-width DocumentSize = %v", got)
	}
	l.SetZoom(1)
	if got := l.DocumentSize(); got.Y != 60 {
		t.Errorf("DocumentSize = %v", got)
	}
}

func TestZoomAtKeepsThePointUnderTheCursor(t *testing.T) {
	// Zooming in always leaves room to scroll, so the point can stay put.
	for _, at := range []image.Point{{110, 150}, {70, 20}, {100, 290}, {150, 10}} {
		for _, scale := range []float64{1.5, 2, 3.5} {
			l := sample()
			l.SetZoom(1)
			l.ScrollTo(image.Pt(0, 150))
			page, pt, ok := l.HitPage(at)
			if !ok {
				t.Fatalf("no page at %v (scroll %v)", at, l.Scroll())
			}
			l.ZoomAt(scale, at)
			if l.Mode() != ZoomFixed || l.Scale() != scale {
				t.Fatalf("mode %v scale %v after ZoomAt(%v)", l.Mode(), l.Scale(), scale)
			}
			got, gpt, ok := l.HitPage(at)
			if !ok || got != page || math.Abs(gpt.W-pt.W) > 1/scale+0.01 || math.Abs(gpt.H-pt.H) > 1/scale+0.01 {
				t.Errorf("at %v, scale %v: the point under the cursor is page %d (%.1f, %.1f), was page %d (%.1f, %.1f)",
					at, scale, got, gpt.W, gpt.H, page, pt.W, pt.H)
			}
		}
	}
}

func TestZoomAtIsClampedAndHandlesAnEmptyDocument(t *testing.T) {
	l := sample()
	l.ZoomAt(1e9, image.Pt(10, 10))
	if l.Scale() != MaxScale {
		t.Errorf("scale %v, want the maximum %v", l.Scale(), MaxScale)
	}
	l.ZoomAt(0, image.Pt(10, 10))
	if l.Scale() != MinScale {
		t.Errorf("scale %v, want the minimum %v", l.Scale(), MinScale)
	}
	empty := New(nil, image.Pt(100, 100), 10, 10)
	empty.ZoomAt(2, image.Pt(5, 5))
	if empty.Scale() != 2 || empty.Mode() != ZoomFixed {
		t.Errorf("empty document: scale %v mode %v", empty.Scale(), empty.Mode())
	}
	// A point below the last page, or right of it, stays on the last page.
	l = sample()
	l.ScrollTo(image.Pt(0, math.MaxInt32))
	l.ZoomAt(1.5, image.Pt(219, 299))
}
