package pageview

import (
	"image"
	"image/color"
	"testing"

	"viewrab/internal/ui/paint"
	"viewrab/internal/ui/platform"
)

func ptr(kind platform.PointerKind, b platform.Button, x, y int) platform.PointerEvent {
	return platform.PointerEvent{Kind: kind, Button: b, X: float64(x), Y: float64(y)}
}

// tall is a view of ten pages in a 124x300 viewport: a vertical scrollbar, and
// no horizontal one at fit width.
func tall(t *testing.T) *View {
	t.Helper()
	v, _ := newView(t, openDoc(t, 10), image.Pt(124, 300), Options{})
	return v
}

func TestThumbReflectsTheScrollPosition(t *testing.T) {
	v := tall(t)
	vp, doc := v.lay.Viewport(), v.lay.DocumentSize()
	b, ok := v.bar(vertical)
	if !ok || b.span != vp.Y || b.strip != image.Rect(vp.X-barWide, 0, vp.X, vp.Y) {
		t.Fatalf("vertical bar = %+v, %v", b, ok)
	}
	if want := b.span * vp.Y / doc.Y; b.length != max(want, minThumb) || b.start != 0 {
		t.Errorf("thumb at the top: start %d length %d, want 0 and %d", b.start, b.length, want)
	}
	v.lay.ScrollTo(image.Pt(0, doc.Y))
	b, _ = v.bar(vertical)
	if b.start+b.length != b.span {
		t.Errorf("thumb at the bottom ends at %d of %d", b.start+b.length, b.span)
	}
	if _, ok := v.bar(horizontal); ok {
		t.Error("a horizontal bar although the page fits across")
	}
	// A very long document keeps a thumb that can be grabbed.
	long, _ := newView(t, openDoc(t, 200), image.Pt(124, 300), Options{})
	if b, _ := long.bar(vertical); b.length != minThumb {
		t.Errorf("thumb of a long document is %d long, want the minimum %d", b.length, minThumb)
	}
}

func TestNoBarsWhenThePagesFit(t *testing.T) {
	v, _ := newView(t, openDoc(t, 1), image.Pt(124, 124), Options{})
	if _, ok := v.bar(vertical); ok {
		t.Error("a vertical bar for one page that fits")
	}
	c := paint.New(124, 124)
	v.Draw(c)
	if got := c.Img.RGBAAt(122, 60); got != (color.RGBA{0x3a, 0x3b, 0x3f, 255}) && got != paper {
		t.Errorf("pixel at the right edge = %v, want only background or page", got)
	}
}

func TestDraggingTheThumbScrolls(t *testing.T) {
	v := tall(t)
	x := v.lay.Viewport().X - 3
	b, _ := v.bar(vertical)
	if !v.HandlePointer(ptr(platform.Press, platform.Left, x, b.start+5)) {
		t.Fatal("pressing the thumb did not ask for a redraw")
	}
	if v.ptr.drag != vertical || v.ptr.grab != 5 {
		t.Fatalf("drag %v grab %d", v.ptr.drag, v.ptr.grab)
	}
	room := b.span - b.length
	v.HandlePointer(ptr(platform.Move, platform.None, x, 5+room/2))
	if got, want := v.lay.Scroll().Y, v.bar2max()/2; abs(got-want) > v.bar2max()/room+1 {
		t.Errorf("scrolled to %d, want about %d", got, want)
	}
	v.HandlePointer(ptr(platform.Move, platform.None, x, 5000)) // far below: the end
	if got := v.lay.Scroll().Y; got != v.bar2max() {
		t.Errorf("dragged past the end: scroll %d, want %d", got, v.bar2max())
	}
	v.HandlePointer(ptr(platform.Move, platform.None, x, -5000))
	if got := v.lay.Scroll().Y; got != 0 {
		t.Errorf("dragged past the start: scroll %d", got)
	}
	if !v.HandlePointer(ptr(platform.Release, platform.Left, x, 100)) || v.ptr.drag != noAxis {
		t.Error("releasing did not end the drag")
	}
	scroll := v.lay.Scroll()
	v.HandlePointer(ptr(platform.Move, platform.None, x, 200))
	if v.lay.Scroll() != scroll {
		t.Error("the page moved after the thumb was released")
	}
}

// bar2max is the largest vertical scroll offset.
func (v *View) bar2max() int { return v.lay.DocumentSize().Y - v.lay.Viewport().Y }

func TestClickingTheTrackCentresTheThumbThere(t *testing.T) {
	v := tall(t)
	x := v.lay.Viewport().X - 3
	b, _ := v.bar(vertical)
	y := b.span * 3 / 4
	v.HandlePointer(ptr(platform.Press, platform.Left, x, y))
	b2, _ := v.bar(vertical)
	if mid := b2.start + b2.length/2; abs(mid-y) > 3 {
		t.Errorf("thumb centred at %d after a click at %d", mid, y)
	}
	if v.ptr.drag != vertical {
		t.Error("a click on the track did not start a drag")
	}
	v.HandlePointer(ptr(platform.Release, platform.Left, x, y))
}

func TestHorizontalBarWhenTheViewIsNarrowerThanThePage(t *testing.T) {
	v := tall(t)
	v.lay.SetZoom(3)
	h, ok := v.bar(horizontal)
	if !ok {
		t.Fatal("no horizontal bar although the page is wider than the view")
	}
	vbar, _ := v.bar(vertical)
	vp := v.lay.Viewport()
	if h.span != vp.X-barWide || vbar.span != vp.Y-barWide {
		t.Errorf("bars overlap in the corner: spans %d and %d", h.span, vbar.span)
	}
	y := vp.Y - 3
	v.HandlePointer(ptr(platform.Press, platform.Left, h.start+2, y))
	v.HandlePointer(ptr(platform.Move, platform.None, 5000, y))
	if got, want := v.lay.Scroll().X, v.lay.DocumentSize().X-vp.X; got != want {
		t.Errorf("dragged to the right end: scroll %d, want %d", got, want)
	}
	v.HandlePointer(ptr(platform.Release, platform.Left, 5000, y))
	// Drawn along the bottom edge.
	c := paint.New(vp.X, vp.Y)
	v.Draw(c)
	h, _ = v.bar(horizontal)
	if got := c.Img.RGBAAt(h.start+h.length/2, vp.Y-barInset-2); got.A == 0 || got == (color.RGBA{0x3a, 0x3b, 0x3f, 255}) {
		t.Errorf("no thumb at the bottom edge: %v", got)
	}
}

func TestDraggingThePage(t *testing.T) {
	v := tall(t)
	v.lay.ScrollTo(image.Pt(0, 100))
	// The left button selects text; only the middle one drags the page.
	v.HandlePointer(ptr(platform.Press, platform.Left, 50, 150))
	v.HandlePointer(ptr(platform.Move, platform.None, 50, 120))
	if v.ptr.pan || v.lay.Scroll().Y != 100 {
		t.Errorf("a left-button drag panned: scroll %v", v.lay.Scroll())
	}
	v.HandlePointer(ptr(platform.Release, platform.Left, 50, 120))
	if v.HandlePointer(ptr(platform.Press, platform.Middle, 50, 150)) {
		t.Error("pressing on a page asked for a redraw")
	}
	if !v.HandlePointer(ptr(platform.Move, platform.None, 50, 120)) || v.lay.Scroll().Y != 130 {
		t.Errorf("dragging up by 30: scroll %v", v.lay.Scroll())
	}
	if v.HandlePointer(ptr(platform.Move, platform.None, 50, 120)) {
		t.Error("a move that changed nothing asked for a redraw")
	}
	v.HandlePointer(ptr(platform.Move, platform.None, 50, 200))
	if v.lay.Scroll().Y != 50 {
		t.Errorf("dragging down by 80: scroll %v", v.lay.Scroll())
	}
	v.HandlePointer(ptr(platform.Release, platform.Middle, 50, 200))
	v.HandlePointer(ptr(platform.Move, platform.None, 50, 10))
	if v.lay.Scroll().Y != 50 {
		t.Error("the page followed the pointer after the release")
	}
	// Other buttons neither drag nor release a drag.
	if v.HandlePointer(ptr(platform.Press, platform.Right, 50, 150)) || v.ptr.pan {
		t.Error("a right-button press started a drag")
	}
	if v.HandlePointer(ptr(platform.Release, platform.Right, 50, 150)) {
		t.Error("a right-button release asked for a redraw")
	}
	v.HandlePointer(ptr(platform.Press, platform.Middle, 50, 150))
	v.HandlePointer(ptr(platform.Release, platform.Left, 50, 150))
	if !v.ptr.pan {
		t.Error("a left-button release ended a middle-button drag")
	}
}

func TestHoveringAThumbHighlightsIt(t *testing.T) {
	v := tall(t)
	x := v.lay.Viewport().X - 3
	if v.HandlePointer(ptr(platform.Move, platform.None, 40, 40)) {
		t.Error("moving over a page changed the view")
	}
	if !v.HandlePointer(ptr(platform.Move, platform.None, x, 40)) || v.ptr.hover != vertical {
		t.Fatal("moving onto the bar did not highlight it")
	}
	if v.HandlePointer(ptr(platform.Move, platform.None, x, 60)) {
		t.Error("moving along the bar asked for a redraw")
	}
	c := paint.New(124, 300)
	v.Draw(c)
	wide := c.Img.RGBAAt(x-barWide/2+barInset, 5)
	v.HandlePointer(ptr(platform.Leave, platform.None, 0, 0))
	if v.HandlePointer(ptr(platform.Leave, platform.None, 0, 0)) {
		t.Error("leaving twice asked for a redraw the second time")
	}
	c2 := paint.New(124, 300)
	v.Draw(c2)
	thin := c2.Img.RGBAAt(x-barWide/2+barInset, 5)
	if wide == thin {
		t.Errorf("the thumb looks the same hovered and at rest: %v", wide)
	}
	// A release over the bar highlights it again, and one elsewhere clears it.
	v.HandlePointer(ptr(platform.Press, platform.Left, 40, 40))
	if !v.HandlePointer(ptr(platform.Release, platform.Left, x, 40)) || v.ptr.hover != vertical {
		t.Error("releasing over the bar did not highlight it")
	}
}

func TestWheelZoomKeepsThePointUnderThePointer(t *testing.T) {
	v := tall(t)
	v.lay.ScrollTo(image.Pt(0, 400))
	at := image.Pt(60, 150)
	page, pt, ok := v.lay.HitPage(at)
	if !ok {
		t.Fatal("no page under the pointer")
	}
	if !v.HandleEvent(platform.ScrollEvent{DY: -10, X: float64(at.X), Y: float64(at.Y), Mods: platform.Ctrl}) {
		t.Fatal("Ctrl+wheel was not handled")
	}
	got, gpt, ok := v.lay.HitPage(at)
	if !ok || got != page || abs(int(gpt.H-pt.H)) > 1 {
		t.Errorf("under the pointer: page %d at %.1f, was page %d at %.1f", got, gpt.H, page, pt.H)
	}
	// Keyboard zoom keeps the middle of the view.
	mid := image.Pt(62, 150)
	page, pt, _ = v.lay.HitPage(mid)
	v.HandleEvent(platform.KeyEvent{Down: true, Rune: '+', Mods: platform.Ctrl})
	got, gpt, ok = v.lay.HitPage(mid)
	if !ok || got != page || abs(int(gpt.H-pt.H)) > 1 {
		t.Errorf("middle of the view: page %d at %.1f, was page %d at %.1f", got, gpt.H, page, pt.H)
	}
}

func TestScrollbarColourIsConfigurable(t *testing.T) {
	v, _ := newView(t, openDoc(t, 10), image.Pt(124, 300), Options{Scrollbar: color.NRGBA{255, 0, 0, 255}})
	c := paint.New(124, 300)
	v.Draw(c)
	if got := c.Img.RGBAAt(124-barInset-2, 3); got.R <= got.G || got.R <= got.B {
		t.Errorf("thumb colour %v is not red", got)
	}
}

func TestDraggingABarThatDisappearsOrHasNoRoom(t *testing.T) {
	v := tall(t)
	x := v.lay.Viewport().X - 3
	v.HandlePointer(ptr(platform.Press, platform.Left, x, 10))
	v.lay.SetZoom(0.05) // everything fits now: no bar left to drag
	before := v.lay.Scroll()
	v.HandlePointer(ptr(platform.Move, platform.None, x, 200))
	if v.lay.Scroll() != before {
		t.Error("a bar that is gone still scrolled the page")
	}
	// A view shorter than the minimum thumb: the thumb fills the track.
	short, _ := newView(t, openDoc(t, 10), image.Pt(124, minThumb-8), Options{})
	b, _ := short.bar(vertical)
	if b.length != b.span {
		t.Fatalf("thumb %d of a track of %d", b.length, b.span)
	}
	short.HandlePointer(ptr(platform.Press, platform.Left, 124-3, 3))
	short.HandlePointer(ptr(platform.Move, platform.None, 124-3, 10))
	if short.lay.Scroll().Y != 0 {
		t.Errorf("scrolled to %d with no room for the thumb to move", short.lay.Scroll().Y)
	}
}
