package widget

import (
	"image"
	"image/color"
	"testing"

	"viewer/internal/ui/paint"
	"viewer/internal/ui/platform"
	"viewer/internal/ui/text"
	"viewer/internal/ui/theme"
)

var palette = theme.Light()

func newBar(t *testing.T, width int, titles ...string) *TabBar {
	t.Helper()
	face, err := text.Mono().Face(theme.FontSize, false)
	if err != nil {
		t.Fatal(err)
	}
	b := NewTabBar(palette, face)
	b.Titles, b.Active = titles, 0
	b.Layout(width)
	return b
}

func ptr(kind platform.PointerKind, button platform.Button, x, y int) platform.PointerEvent {
	return platform.PointerEvent{Kind: kind, Button: button, X: float64(x), Y: float64(y)}
}

// center returns the middle of tab i.
func center(b *TabBar, i int) (x, y int) {
	r := b.tabs[i]
	return (r.Min.X + r.Max.X) / 2, (r.Min.Y + r.Max.Y) / 2
}

func TestLayoutSizesTabsToTheirTitles(t *testing.T) {
	b := newBar(t, 1000, "a.pdf", "a much longer file name.pdf", strings(80))
	if len(b.tabs) != 3 {
		t.Fatalf("%d tabs", len(b.tabs))
	}
	w := func(i int) int { return b.tabs[i].Dx() }
	if !(w(0) < w(1)) || w(2) != tabMaxWidth {
		t.Errorf("tab widths %d %d %d: want short < longer, and the longest capped at %d", w(0), w(1), w(2), tabMaxWidth)
	}
	// Tabs are contiguous, the new-tab button follows the last, and each is
	// the bar's height.
	for i := 1; i < 3; i++ {
		if b.tabs[i].Min.X != b.tabs[i-1].Max.X {
			t.Errorf("gap between tabs %d and %d", i-1, i)
		}
	}
	if b.newTab.Min.X != b.tabs[2].Max.X || b.newTab.Dx() != newWidth || b.tabs[0].Dy() != theme.TabBarHeight || b.Height() != theme.TabBarHeight {
		t.Errorf("new-tab button %v, tab height %d", b.newTab, b.tabs[0].Dy())
	}
	// A short title is widened to tabMinWidth.
	if w := newBar(t, 1000, "x").tabs[0].Dx(); w != tabMinWidth {
		t.Errorf("short tab is %d wide, want the minimum %d", w, tabMinWidth)
	}
}

func strings(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = 'x'
	}
	return string(out)
}

func TestLayoutShrinksTabsToFit(t *testing.T) {
	b := newBar(t, 400, strings(30), strings(30), strings(30), strings(30))
	if b.newTab.Max.X > 400 {
		t.Errorf("the bar overflows: new-tab button ends at %d of 400", b.newTab.Max.X)
	}
	for i, r := range b.tabs {
		if r.Dx() < tabMinWidth {
			t.Errorf("tab %d is %d wide, below the minimum", i, r.Dx())
		}
	}
	// With more tabs than can fit at the minimum, the minimum wins.
	many := newBar(t, 100, strings(10), strings(10), strings(10), strings(10))
	for i, r := range many.tabs {
		if r.Dx() != tabMinWidth {
			t.Errorf("crowded tab %d is %d wide", i, r.Dx())
		}
	}
	// Layout accepts a zero or negative width and a bar with no tabs.
	empty := newBar(t, 0)
	if len(empty.tabs) != 0 || empty.newTab.Dx() != newWidth {
		t.Errorf("empty bar: %v %v", empty.tabs, empty.newTab)
	}
	newBar(t, -50, "a")
}

func TestClicksSelectCloseAndCreate(t *testing.T) {
	b := newBar(t, 800, "one.pdf", "two.pdf", "three.pdf")
	x, y := center(b, 1)
	if a := b.HandleEvent(ptr(platform.Press, platform.Left, x, y)); a != (Action{Kind: ActionSelect, Index: 1}) {
		t.Errorf("click on a tab = %+v", a)
	}
	b.HandleEvent(ptr(platform.Release, platform.Left, x, y))

	cr := closeRect(b.tabs[2])
	cx, cy := (cr.Min.X+cr.Max.X)/2, (cr.Min.Y+cr.Max.Y)/2
	if a := b.HandleEvent(ptr(platform.Press, platform.Left, cx, cy)); a != (Action{Kind: ActionClose, Index: 2}) {
		t.Errorf("click on a close button = %+v", a)
	}
	if a := b.HandleEvent(ptr(platform.Press, platform.Middle, x, y)); a != (Action{Kind: ActionClose, Index: 1}) {
		t.Errorf("middle click = %+v", a)
	}
	nx, ny := (b.newTab.Min.X+b.newTab.Max.X)/2, 10
	if a := b.HandleEvent(ptr(platform.Press, platform.Left, nx, ny)); a != (Action{Kind: ActionNew}) {
		t.Errorf("click on + = %+v", a)
	}
	// Other buttons, and the empty bar to the right, do nothing.
	for _, ev := range []platform.PointerEvent{
		ptr(platform.Press, platform.Right, x, y),
		ptr(platform.Press, platform.Middle, nx, ny),
		ptr(platform.Press, platform.Right, nx, ny),
		ptr(platform.Press, platform.Left, 790, 10),
		ptr(platform.Press, platform.Left, -5, 10),
	} {
		if a := b.HandleEvent(ev); a != (Action{}) {
			t.Errorf("%+v did %+v", ev, a)
		}
	}
	// A release with nothing pressed is harmless.
	if a := b.HandleEvent(ptr(platform.Release, platform.Left, 5, 5)); a != (Action{}) {
		t.Errorf("stray release = %+v", a)
	}
}

func TestDraggingReordersTabs(t *testing.T) {
	titles := []string{"a.pdf", "b.pdf", "c.pdf", "d.pdf"}
	drag := func(from, toX int) (Action, bool) {
		b := newBar(t, 900, titles...)
		x, y := center(b, from)
		b.HandleEvent(ptr(platform.Press, platform.Left, x, y))
		b.HandleEvent(ptr(platform.Move, platform.None, (x+toX)/2, y))
		b.HandleEvent(ptr(platform.Move, platform.None, toX, y))
		dragging := b.Dragging()
		return b.HandleEvent(ptr(platform.Release, platform.Left, toX, y)), dragging
	}
	probe := newBar(t, 900, titles...)
	right := probe.tabs[3].Max.X - 5 // right half of the last tab: after it
	left := probe.tabs[0].Min.X + 2  // before the first tab

	if a, d := drag(0, right); a != (Action{Kind: ActionReorder, Index: 0, To: 3}) || !d {
		t.Errorf("drag first to the end = %+v (dragging %v)", a, d)
	}
	if a, _ := drag(3, left); a != (Action{Kind: ActionReorder, Index: 3, To: 0}) {
		t.Errorf("drag last to the front = %+v", a)
	}
	// The gap before the next tab is the slot the tab already fills.
	if a, _ := drag(1, probe.tabs[2].Min.X+2); a != (Action{}) {
		t.Errorf("drag onto the next tab's left edge = %+v, want nothing", a)
	}
	// Dropping back where it started changes nothing.
	b := newBar(t, 900, titles...)
	x, y := center(b, 2)
	b.HandleEvent(ptr(platform.Press, platform.Left, x, y))
	b.HandleEvent(ptr(platform.Move, platform.None, x+20, y))
	b.HandleEvent(ptr(platform.Move, platform.None, x, y))
	if a := b.HandleEvent(ptr(platform.Release, platform.Left, x, y)); a != (Action{}) {
		t.Errorf("drop in place = %+v", a)
	}
	// A tiny movement is a click, not a drag.
	b.HandleEvent(ptr(platform.Press, platform.Left, x, y))
	b.HandleEvent(ptr(platform.Move, platform.None, x+2, y))
	if b.Dragging() {
		t.Error("a 2 pixel wobble started a drag")
	}
	if a := b.HandleEvent(ptr(platform.Release, platform.Left, x+2, y)); a != (Action{}) {
		t.Errorf("click release = %+v", a)
	}
	// Releasing a different button ends the press without reordering.
	b.HandleEvent(ptr(platform.Press, platform.Left, x, y))
	b.HandleEvent(ptr(platform.Move, platform.None, x+80, y))
	if a := b.HandleEvent(ptr(platform.Release, platform.Right, x+80, y)); a != (Action{}) || b.Dragging() {
		t.Errorf("right-button release = %+v", a)
	}
}

func TestHoverTracksThePointer(t *testing.T) {
	b := newBar(t, 800, "one.pdf", "two.pdf")
	x, y := center(b, 1)
	b.HandleEvent(ptr(platform.Move, platform.None, x, y))
	if b.hover.tab != 1 || b.hover.close || b.hover.plus {
		t.Errorf("hover = %+v", b.hover)
	}
	cr := closeRect(b.tabs[1])
	b.HandleEvent(ptr(platform.Move, platform.None, cr.Min.X+2, cr.Min.Y+2))
	if !b.hover.close {
		t.Error("the close button is not hovered")
	}
	b.HandleEvent(ptr(platform.Move, platform.None, b.newTab.Min.X+3, 10))
	if !b.hover.plus || b.hover.tab != -1 {
		t.Errorf("hover over + = %+v", b.hover)
	}
	b.HandleEvent(ptr(platform.Leave, platform.None, 0, 0))
	if b.hover != noHit {
		t.Errorf("hover after leaving = %+v", b.hover)
	}
}

func pixel(c *paint.Canvas, x, y int) color.RGBA { return c.Img.RGBAAt(x, y) }

func nrgba(c color.NRGBA) color.RGBA { return color.RGBA{c.R, c.G, c.B, c.A} }

func TestDrawPaintsTheBar(t *testing.T) {
	b := newBar(t, 400, "one.pdf", "two.pdf")
	b.Active = 0
	c := paint.New(400, 60)
	b.Draw(c, image.Pt(0, 10))
	// The active tab is filled with the canvas colour over the border line.
	if got := pixel(c, 3, 10+theme.TabBarHeight-1); got != nrgba(palette.Canvas) {
		t.Errorf("under the active tab = %v, want canvas", got)
	}
	// An inactive tab is filled with the chrome colour, and the border line
	// runs under it.
	x := b.tabs[1].Min.X + 3
	if got := pixel(c, x, 10+5); got != nrgba(palette.Chrome) {
		t.Errorf("inactive tab = %v, want chrome", got)
	}
	if got := pixel(c, x, 10+theme.TabBarHeight-1); got != nrgba(palette.Border) {
		t.Errorf("line under an inactive tab = %v, want border", got)
	}
	// Separator on the right edge of a tab.
	if got := pixel(c, b.tabs[0].Max.X-1, 10+5); got != nrgba(palette.Border) {
		t.Errorf("separator = %v, want border", got)
	}
	// Nothing above the bar's origin.
	if got := pixel(c, 3, 5); got.A != 0 {
		t.Errorf("painted above the bar: %v", got)
	}
	// The tab titles and the + glyph are drawn.
	hasInk := func(r image.Rectangle) bool {
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				p := pixel(c, x, y)
				if p != nrgba(palette.Chrome) && p != nrgba(palette.Canvas) && p != nrgba(palette.Border) && p.A != 0 {
					return true
				}
			}
		}
		return false
	}
	if !hasInk(b.tabs[0].Add(image.Pt(0, 10)).Inset(4)) || !hasInk(b.newTab.Add(image.Pt(0, 10))) {
		t.Error("no text drawn in the first tab or the new-tab button")
	}
}

func TestDrawShowsHoverCloseAndDropMarker(t *testing.T) {
	b := newBar(t, 600, "one.pdf", "two.pdf", "three.pdf")
	b.Active = 2
	// A hovered close button is washed with the hover colour.
	cr := closeRect(b.tabs[0])
	b.HandleEvent(ptr(platform.Move, platform.None, cr.Min.X+2, cr.Min.Y+2))
	c := paint.New(600, 40)
	b.Draw(c, image.Point{})
	if got := pixel(c, cr.Min.X+1, cr.Min.Y+1); got == nrgba(palette.Chrome) {
		t.Errorf("the hovered close button is not washed: %v", got)
	}
	// An inactive, unhovered tab shows no close button.
	other := closeRect(b.tabs[1])
	if got := pixel(c, other.Min.X+1, other.Min.Y+1); got != nrgba(palette.Chrome) {
		t.Errorf("a close button shows on an unhovered tab: %v", got)
	}
	// Hovering + and dragging a tab shows the drop marker in the accent colour.
	b.HandleEvent(ptr(platform.Move, platform.None, b.newTab.Min.X+3, 10))
	x, y := center(b, 0)
	b.HandleEvent(ptr(platform.Press, platform.Left, x, y))
	b.HandleEvent(ptr(platform.Move, platform.None, b.tabs[1].Max.X+2, y)) // gap before tab 2
	c = paint.New(600, 40)
	b.Draw(c, image.Point{})
	if got := pixel(c, b.tabs[2].Min.X, 12); got != nrgba(palette.Accent) {
		t.Errorf("drop marker = %v, want accent", got)
	}
	// Dropping past the last tab marks the end, beside the + button.
	b.HandleEvent(ptr(platform.Move, platform.None, b.tabs[2].Max.X-2, y))
	c = paint.New(600, 40)
	b.Draw(c, image.Point{})
	if got := pixel(c, b.newTab.Min.X, 12); got != nrgba(palette.Accent) {
		t.Errorf("end drop marker = %v, want accent", got)
	}
	// The dragged tab is washed with the hover colour.
	if got := pixel(c, b.tabs[0].Min.X+2, 4); got == nrgba(palette.Chrome) {
		t.Errorf("dragged tab is not washed: %v", got)
	}
}

func TestDrawLaysOutOnDemand(t *testing.T) {
	face, _ := text.Mono().Face(theme.FontSize, false)
	b := NewTabBar(palette, face)
	b.Titles = []string{"a"}
	c := paint.New(200, 40)
	b.Draw(c, image.Point{}) // Draw lays out an unlaid bar before painting it
	if pixel(c, 3, 3).A == 0 {
		t.Error("an unlaid bar painted nothing")
	}
	// A long title is cut to fit before its close button.
	long := newBar(t, 300, strings(60))
	c = paint.New(300, 40)
	long.Draw(c, image.Point{})
	cr := closeRect(long.tabs[0])
	for y := range 40 {
		for x := cr.Min.X - tabGap + 1; x < cr.Min.X; x++ {
			if p := pixel(c, x, y); p != nrgba(palette.Canvas) && p != nrgba(palette.Chrome) && p != nrgba(palette.Border) && p.A != 0 {
				t.Fatalf("the title reaches the close button at (%d,%d): %v", x, y, p)
			}
		}
	}
}

func TestDrawWashesTheHoveredNewTabButton(t *testing.T) {
	b := newBar(t, 400, "one.pdf")
	c := paint.New(400, 40)
	b.Draw(c, image.Point{})
	plain := pixel(c, b.newTab.Min.X+4, 4)
	b.HandleEvent(ptr(platform.Move, platform.None, b.newTab.Min.X+8, 10))
	c = paint.New(400, 40)
	b.Draw(c, image.Point{})
	if got := pixel(c, b.newTab.Min.X+4, 4); got == plain {
		t.Errorf("the hovered + button looks like the idle one: %v", got)
	}
}

func TestTabBarRectangles(t *testing.T) {
	b := newBar(t, 800, "one.pdf", "two.pdf")
	if b.TabRect(1) != b.tabs[1] || b.NewTabRect() != b.newTab || b.CloseRect(0) != closeRect(b.tabs[0]) {
		t.Error("the accessors disagree with the layout")
	}
	for _, i := range []int{-1, 2} {
		if !b.TabRect(i).Empty() || !b.CloseRect(i).Empty() {
			t.Errorf("rectangles for tab %d, which does not exist", i)
		}
	}
}
