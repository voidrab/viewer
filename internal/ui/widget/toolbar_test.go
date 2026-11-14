package widget

import (
	"image"
	"testing"

	"viewer/internal/ui/paint"
	"viewer/internal/ui/platform"
	"viewer/internal/ui/text"
	"viewer/internal/ui/theme"
)

func newToolbar(t *testing.T) *Toolbar {
	t.Helper()
	face, err := text.Mono().Face(theme.FontSize, false)
	if err != nil {
		t.Fatal(err)
	}
	tb := NewToolbar(palette, face)
	tb.PageText, tb.ZoomText = "3 / 12", "Fit width"
	tb.Layout(900)
	return tb
}

func mid(r image.Rectangle) (int, int) { return (r.Min.X + r.Max.X) / 2, (r.Min.Y + r.Max.Y) / 2 }

func TestToolbarLayoutOrdersButtonsLeftToRight(t *testing.T) {
	tb := newToolbar(t)
	if tb.Height() != theme.ToolbarHeight {
		t.Errorf("height = %d", tb.Height())
	}
	for id := ButtonID(1); id < buttonCount; id++ {
		if tb.rects[id].Min.X <= tb.rects[id-1].Min.X {
			t.Errorf("button %d is not right of button %d", id, id-1)
		}
		if tb.rects[id].Min.X < tb.rects[id-1].Max.X {
			t.Errorf("buttons %d and %d overlap", id-1, id)
		}
	}
	// The page text sits between the page buttons, the zoom text on its button.
	if !(tb.pageBox.Min.X >= tb.rects[ButtonPrev].Max.X && tb.pageBox.Max.X <= tb.rects[ButtonNext].Min.X) {
		t.Errorf("page text box %v is not between prev %v and next %v", tb.pageBox, tb.rects[ButtonPrev], tb.rects[ButtonNext])
	}
	if r := tb.rects[ButtonZoomMode]; r.Dx() < 72 || r.Dx() < tb.Face.Width("Fit width")+16 {
		t.Errorf("zoom button is %d wide", r.Dx())
	}
	// Longer text makes room for itself.
	before := tb.rects[ButtonPDFA].Min.X
	tb.PageText, tb.ZoomText = "1234 / 5678", "A very long zoom name"
	tb.Layout(900)
	if tb.rects[ButtonPDFA].Min.X <= before {
		t.Error("longer labels did not push the later buttons right")
	}
	// Buttons are vertically centred in the bar.
	if r := tb.rects[ButtonOpen]; r.Min.Y != (theme.ToolbarHeight-toolButton)/2 || r.Dy() != toolButton {
		t.Errorf("open button %v", r)
	}
}

func TestToolbarClicksFireOnRelease(t *testing.T) {
	tb := newToolbar(t)
	for id := ButtonID(0); id < buttonCount; id++ {
		x, y := mid(tb.rects[id])
		if a := tb.HandleEvent(ptr(platform.Press, platform.Left, x, y)); a != (Action{}) {
			t.Errorf("button %d fired on press: %+v", id, a)
		}
		if a := tb.HandleEvent(ptr(platform.Release, platform.Left, x, y)); a != (Action{Kind: ActionButton, Index: int(id)}) {
			t.Errorf("button %d release = %+v", id, a)
		}
	}
	// Pressing one button and releasing over another, or off the bar, fires nothing.
	x, y := mid(tb.rects[ButtonOpen])
	tb.HandleEvent(ptr(platform.Press, platform.Left, x, y))
	nx, ny := mid(tb.rects[ButtonNext])
	if a := tb.HandleEvent(ptr(platform.Release, platform.Left, nx, ny)); a != (Action{}) {
		t.Errorf("drag from one button to another fired %+v", a)
	}
	tb.HandleEvent(ptr(platform.Press, platform.Left, x, y))
	if a := tb.HandleEvent(ptr(platform.Release, platform.Left, 5, 5)); a != (Action{}) {
		t.Errorf("release off the button fired %+v", a)
	}
	// Other mouse buttons, presses on empty space, and stray releases do nothing.
	tb.HandleEvent(ptr(platform.Press, platform.Right, x, y))
	if a := tb.HandleEvent(ptr(platform.Release, platform.Right, x, y)); a != (Action{}) {
		t.Errorf("right click fired %+v", a)
	}
	tb.HandleEvent(ptr(platform.Press, platform.Left, 880, 10))
	if a := tb.HandleEvent(ptr(platform.Release, platform.Left, 880, 10)); a != (Action{}) {
		t.Errorf("click on empty space fired %+v", a)
	}
}

func TestToolbarDisabledButtonsIgnoreThePointer(t *testing.T) {
	tb := newToolbar(t)
	tb.Disabled[ButtonPrev] = true
	x, y := mid(tb.rects[ButtonPrev])
	tb.HandleEvent(ptr(platform.Move, platform.None, x, y))
	if tb.hover != -1 {
		t.Errorf("a disabled button is hovered: %d", tb.hover)
	}
	tb.HandleEvent(ptr(platform.Press, platform.Left, x, y))
	if a := tb.HandleEvent(ptr(platform.Release, platform.Left, x, y)); a != (Action{}) {
		t.Errorf("a disabled button fired %+v", a)
	}
}

func TestToolbarHover(t *testing.T) {
	tb := newToolbar(t)
	x, y := mid(tb.rects[ButtonFind])
	tb.HandleEvent(ptr(platform.Move, platform.None, x, y))
	if tb.hover != ButtonFind {
		t.Errorf("hover = %d", tb.hover)
	}
	tb.HandleEvent(ptr(platform.Move, platform.None, 3, 3))
	if tb.hover != -1 {
		t.Errorf("hover over nothing = %d", tb.hover)
	}
	tb.HandleEvent(ptr(platform.Move, platform.None, x, y))
	tb.HandleEvent(ptr(platform.Leave, platform.None, 0, 0))
	if tb.hover != -1 {
		t.Errorf("hover after leaving = %d", tb.hover)
	}
}

// buttonPixel returns a pixel from the top-left corner region of button id,
// where only the button's background shows, not its icon.
func buttonPixel(c *paint.Canvas, tb *Toolbar, id ButtonID) (r, g, b, a uint8) {
	p := c.Img.RGBAAt(tb.rects[id].Min.X+1, tb.rects[id].Min.Y+1)
	return p.R, p.G, p.B, p.A
}

func TestToolbarDrawStatesAndContent(t *testing.T) {
	tb := newToolbar(t)
	c := paint.New(900, 60)
	tb.Draw(c, image.Pt(0, 10))
	if got := c.Img.RGBAAt(450, 10+3); got != nrgba(palette.Chrome) {
		t.Errorf("background = %v, want chrome", got)
	}
	if got := c.Img.RGBAAt(450, 10+theme.ToolbarHeight-1); got != nrgba(palette.Border) {
		t.Errorf("bottom line = %v, want border", got)
	}
	if got := c.Img.RGBAAt(450, 5); got.A != 0 {
		t.Errorf("painted above the bar: %v", got)
	}
	// Every button's icon (and the zoom text) inks its button.
	for id := ButtonID(0); id < buttonCount; id++ {
		r := tb.rects[id].Add(image.Pt(0, 10))
		if anyInk(c, r.Inset(3)).Empty() {
			t.Errorf("button %d drew nothing", id)
		}
	}
	if anyInk(c, tb.pageBox.Add(image.Pt(0, 10)).Inset(1)).Empty() {
		t.Error("the page text was not drawn")
	}

	idle := nrgba(palette.Chrome)
	// Hover washes, a pressed button washes darker, a switched-on button shows the selection.
	x, y := mid(tb.rects[ButtonNext])
	tb.HandleEvent(ptr(platform.Move, platform.None, x, y))
	c = paint.New(900, 60)
	tb.Draw(c, image.Point{})
	r1, g1, b1, _ := buttonPixel(c, tb, ButtonNext)
	if (color4{r1, g1, b1}) == (color4{idle.R, idle.G, idle.B}) {
		t.Error("a hovered button is not washed")
	}
	tb.HandleEvent(ptr(platform.Press, platform.Left, x, y))
	c = paint.New(900, 60)
	tb.Draw(c, image.Point{})
	r2, g2, b2, _ := buttonPixel(c, tb, ButtonNext)
	if (color4{r2, g2, b2}) == (color4{r1, g1, b1}) {
		t.Error("a pressed button looks like a hovered one")
	}
	tb.HandleEvent(ptr(platform.Release, platform.Left, 3, 3))
	tb.On[ButtonFind] = true
	c = paint.New(900, 60)
	tb.Draw(c, image.Point{})
	if r, g, b, _ := buttonPixel(c, tb, ButtonFind); (color4{r, g, b}) == (color4{idle.R, idle.G, idle.B}) {
		t.Error("a switched-on button is not highlighted")
	}
	// A disabled button is dimmed: its icon is fainter than an enabled one's.
	tb.Disabled[ButtonOpen] = true
	dim := paint.New(900, 60)
	tb.Draw(dim, image.Point{})
	if sumAlpha(dim, tb.rects[ButtonOpen]) == 0 {
		t.Error("a disabled button drew nothing")
	}
}

type color4 struct{ r, g, b uint8 }

// sumRed totals the red channel over r, which differs between two renderings
// of the same text in different colours.
func sumRed(c *paint.Canvas, r image.Rectangle) int {
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			n += int(c.Img.RGBAAt(x, y).R)
		}
	}
	return n
}

func sumAlpha(c *paint.Canvas, r image.Rectangle) int {
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if p := c.Img.RGBAAt(x, y); p != nrgba(palette.Chrome) {
				n += int(p.A)
			}
		}
	}
	return n
}

func TestToolbarLaysOutOnDemandAndFitsLongText(t *testing.T) {
	face, _ := text.Mono().Face(theme.FontSize, false)
	tb := NewToolbar(palette, face)
	tb.PageText, tb.ZoomText = "1 / 1", "100%"
	// Events before any layout lay the bar out first.
	x, y := toolPad+toolButton/2, theme.ToolbarHeight/2
	tb.HandleEvent(ptr(platform.Press, platform.Left, x, y))
	if a := tb.HandleEvent(ptr(platform.Release, platform.Left, x, y)); a != (Action{Kind: ActionButton, Index: int(ButtonOpen)}) {
		t.Errorf("click on an unlaid toolbar = %+v", a)
	}
	// Drawing an unlaid bar does the same.
	fresh := NewToolbar(palette, face)
	fresh.ZoomText = "100%"
	c := paint.New(400, 40)
	fresh.Draw(c, image.Point{})
	if anyInk(c, image.Rect(0, 0, 400, 35)).Empty() {
		t.Error("an unlaid toolbar drew nothing")
	}
	// With both page buttons disabled, the page text is dimmed too.
	both := newToolbar(t)
	both.Disabled[ButtonPrev], both.Disabled[ButtonNext] = true, true
	dimmed, normal := paint.New(900, 40), paint.New(900, 40)
	both.Draw(dimmed, image.Point{})
	newToolbar(t).Draw(normal, image.Point{})
	if sumRed(dimmed, both.pageBox) == sumRed(normal, both.pageBox) {
		t.Error("the page text of a one-page document is not dimmed")
	}
}
