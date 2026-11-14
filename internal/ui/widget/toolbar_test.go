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

func newToolbar(t *testing.T) *Toolbar {
	t.Helper()
	face, err := text.Mono().Face(theme.FontSize, false)
	if err != nil {
		t.Fatal(err)
	}
	tb := NewToolbar(palette, face)
	tb.Page, tb.Pages, tb.ZoomText = 3, 12, "fit width"
	tb.ZoomChoices = []string{"fit width", "fit page", "100%"}
	tb.Controls = true
	tb.Layout(900)
	return tb
}

func mid(r image.Rectangle) (int, int) { return (r.Min.X + r.Max.X) / 2, (r.Min.Y + r.Max.Y) / 2 }

// empty returns a point on the bar's empty part: between the zoom group and
// the find field.
func empty(tb *Toolbar) (int, int) {
	return (tb.rects[ButtonZoomIn].Max.X + tb.rects[ButtonFind].Min.X) / 2, theme.ToolbarHeight / 2
}

func TestToolbarLayoutOrdersControlsLeftToRight(t *testing.T) {
	tb := newToolbar(t)
	if tb.Height() != theme.ToolbarHeight {
		t.Errorf("height = %d", tb.Height())
	}
	if tb.logo.Max.X > tb.rects[ButtonOpen].Min.X {
		t.Errorf("logo %v runs into Open %v", tb.logo, tb.rects[ButtonOpen])
	}
	for id := ButtonID(1); id < buttonCount; id++ {
		if tb.rects[id].Min.X < tb.rects[id-1].Max.X {
			t.Errorf("control %d is not right of control %d: %v %v", id, id-1, tb.rects[id-1], tb.rects[id])
		}
	}
	// The page count sits between the page field and Next.
	if !(tb.pagesBox.Min.X >= tb.rects[ButtonPage].Max.X && tb.pagesBox.Max.X <= tb.rects[ButtonNext].Min.X) {
		t.Errorf("page count %v is not between the field %v and next %v", tb.pagesBox, tb.rects[ButtonPage], tb.rects[ButtonNext])
	}
	// The zoom drop-down fits the widest of its choices, whatever it shows.
	w := tb.rects[ButtonZoomMode].Dx()
	tb.ZoomText = "100%"
	tb.Layout(900)
	if tb.rects[ButtonZoomMode].Dx() != w || w < tb.Face.Width("fit width")+chevron {
		t.Errorf("zoom drop-down is %d wide, then %d", w, tb.rects[ButtonZoomMode].Dx())
	}
	// Text wider than every choice widens it, and a longer count pushes the
	// later controls right.
	before := tb.rects[ButtonZoomOut].Min.X
	tb.ZoomText, tb.Pages = "a zoom wider than any choice", 12345
	tb.Layout(900)
	if tb.rects[ButtonZoomMode].Dx() <= w || tb.rects[ButtonZoomOut].Min.X <= before {
		t.Error("longer text did not make room for itself")
	}
	// The find field is FindWidth wide when there is room, and shrinks, but
	// not below FindMinWidth, when there is not.
	tb.ZoomText, tb.Pages = "fit width", 12
	tb.Layout(900)
	if tb.rects[ButtonFind].Dx() != FindWidth {
		t.Errorf("find field is %d wide", tb.rects[ButtonFind].Dx())
	}
	tb.Layout(640)
	if w := tb.rects[ButtonFind].Dx(); w >= FindWidth || w < FindMinWidth {
		t.Errorf("find field in a narrow bar is %d wide", w)
	}
	tb.Layout(300)
	if tb.rects[ButtonFind].Dx() != FindMinWidth {
		t.Errorf("find field in a tiny bar is %d wide", tb.rects[ButtonFind].Dx())
	}
	// Controls are vertically centred in the bar.
	if r := tb.rects[ButtonOpen]; r.Min.Y != (theme.ToolbarHeight-toolButton)/2 || r.Dy() != toolButton {
		t.Errorf("open button %v", r)
	}
}

func TestToolbarClicksFireOnRelease(t *testing.T) {
	tb := newToolbar(t)
	for id := ButtonID(0); id < buttonCount; id++ {
		x, y := mid(tb.rects[id])
		press := tb.HandleEvent(ptr(platform.Press, platform.Left, x, y))
		release := tb.HandleEvent(ptr(platform.Release, platform.Left, x, y))
		// The page field and the zoom drop-down answer the press at once,
		// the other controls the release.
		if id == ButtonPage || id == ButtonZoomMode {
			if press != (Action{Kind: ActionButton, Index: int(id), At: image.Pt(x, y)}) || release != (Action{}) {
				t.Errorf("control %d: press %+v release %+v", id, press, release)
			}
			continue
		}
		if press != (Action{}) || release != (Action{Kind: ActionButton, Index: int(id)}) {
			t.Errorf("control %d: press %+v release %+v", id, press, release)
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
	// Other mouse buttons, and a release on empty space, fire nothing.
	tb.HandleEvent(ptr(platform.Press, platform.Right, x, y))
	if a := tb.HandleEvent(ptr(platform.Release, platform.Right, x, y)); a != (Action{}) {
		t.Errorf("right click fired %+v", a)
	}
	ex, ey := empty(tb)
	tb.HandleEvent(ptr(platform.Press, platform.Left, ex, ey))
	if a := tb.HandleEvent(ptr(platform.Release, platform.Left, ex, ey)); a != (Action{}) {
		t.Errorf("click on empty space fired %+v", a)
	}
}

func TestToolbarEmptySpaceIsATitleBar(t *testing.T) {
	tb := newToolbar(t)
	x, y := empty(tb)
	// A left press on empty space is reported, and moving past the slop
	// starts a move, once.
	if a := tb.HandleEvent(ptr(platform.Press, platform.Left, x, y)); a != (Action{Kind: ActionTitlePress, At: image.Pt(x, y)}) {
		t.Errorf("press = %+v", a)
	}
	if a := tb.HandleEvent(ptr(platform.Move, platform.None, x+2, y+2)); a != (Action{}) {
		t.Errorf("a move within the slop = %+v", a)
	}
	if a := tb.HandleEvent(ptr(platform.Move, platform.None, x, y+10)); a != (Action{Kind: ActionMove, At: image.Pt(x, y+10)}) {
		t.Errorf("a move past the slop = %+v", a)
	}
	if a := tb.HandleEvent(ptr(platform.Move, platform.None, x-100, y+10)); a != (Action{}) {
		t.Errorf("a second move = %+v", a)
	}
	tb.HandleEvent(ptr(platform.Release, platform.Left, x-100, y+10))
	// After a release, moving starts nothing.
	tb.HandleEvent(ptr(platform.Press, platform.Left, x, y))
	tb.HandleEvent(ptr(platform.Release, platform.Left, x, y))
	if a := tb.HandleEvent(ptr(platform.Move, platform.None, x+50, y)); a != (Action{}) {
		t.Errorf("a move after the release = %+v", a)
	}
	// A right press there asks for the window menu.
	if a := tb.HandleEvent(ptr(platform.Press, platform.Right, x, y)); a != (Action{Kind: ActionMenu, At: image.Pt(x, y)}) {
		t.Errorf("right press = %+v", a)
	}
	// The logo is part of the title bar too.
	lx, ly := mid(tb.logo)
	if a := tb.HandleEvent(ptr(platform.Press, platform.Left, lx, ly)); a.Kind != ActionTitlePress {
		t.Errorf("press on the logo = %+v", a)
	}
	tb.HandleEvent(ptr(platform.Release, platform.Left, lx, ly))
	// Controls, disabled ones and the find field are not empty space.
	tb.Disabled[ButtonPrev] = true
	for _, id := range []ButtonID{ButtonOpen, ButtonPrev, ButtonPage, ButtonFind} {
		x, y := mid(tb.rects[id])
		if a := tb.HandleEvent(ptr(platform.Press, platform.Right, x, y)); a != (Action{}) {
			t.Errorf("right press on %d = %+v", id, a)
		}
		if a := tb.HandleEvent(ptr(platform.Press, platform.Left, x, y)); a.Kind == ActionTitlePress {
			t.Errorf("left press on %d = %+v", id, a)
		}
		tb.HandleEvent(ptr(platform.Release, platform.Left, x, y))
	}
}

func TestToolbarWithoutADocumentShowsOnlyOpenAndTheme(t *testing.T) {
	tb := newToolbar(t)
	tb.NoDocument = true
	c := paint.New(900, 40)
	tb.Draw(c, image.Point{})
	for id := range buttonCount {
		x, y := mid(tb.rects[id])
		drawn := !anyInk(c, tb.rects[id].Inset(3)).Empty()
		if docOnly[id] {
			if drawn {
				t.Errorf("control %d is drawn with no document", id)
			}
			// Its place is the title bar's.
			if a := tb.HandleEvent(ptr(platform.Press, platform.Left, x, y)); a.Kind != ActionTitlePress {
				t.Errorf("press where control %d would be = %+v", id, a)
			}
			tb.HandleEvent(ptr(platform.Release, platform.Left, x, y))
		} else if !drawn {
			t.Errorf("control %d is not drawn", id)
		}
	}
	if anyInk(c, tb.pagesBox).Empty() == false {
		t.Error("the page count is drawn with no document")
	}
}

func TestToolbarWindowControls(t *testing.T) {
	tb := newToolbar(t)
	// The controls sit against the right edge.
	if r := tb.rects[ButtonCloseWindow]; r.Max.X != 900-winPad || r.Dx() != winButton {
		t.Errorf("close control at %v", r)
	}
	// Hovering the close control fills it with red.
	x, y := mid(tb.rects[ButtonCloseWindow])
	tb.HandleEvent(ptr(platform.Move, platform.None, x, y))
	c := paint.New(900, 40)
	tb.Draw(c, image.Point{})
	if got := c.Img.RGBAAt(tb.rects[ButtonCloseWindow].Min.X+4, y); got != nrgba(theme.CloseHover) {
		t.Errorf("hovered close control = %v, want red", got)
	}
	// Maximised, the maximise control shows the restore glyph instead.
	normal := paint.New(900, 40)
	tb.Draw(normal, image.Point{})
	tb.Maximized = true
	restore := paint.New(900, 40)
	tb.Draw(restore, image.Point{})
	if sumAlpha(normal, tb.rects[ButtonMaximize]) == sumAlpha(restore, tb.rects[ButtonMaximize]) {
		t.Error("the maximise control looks the same when maximised")
	}
	// Without Controls, there are none to see or click, and the theme switch
	// moves to the right edge.
	minimize := tb.rects[ButtonMinimize]
	tb.Controls = false
	tb.Layout(900)
	for id := ButtonMinimize; id <= ButtonCloseWindow; id++ {
		if !tb.rects[id].Empty() {
			t.Errorf("hidden control %d at %v", id, tb.rects[id])
		}
	}
	if r := tb.rects[ButtonTheme]; r.Max.X != 900-winPad {
		t.Errorf("theme switch at %v", r)
	}
	// The space they left goes to the other controls on the right.
	if tb.rects[ButtonPDFA].Min.X <= minimize.Min.X {
		t.Errorf("PDF/A button at %v, left of where minimise was, %v", tb.rects[ButtonPDFA], minimize)
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
	x, y := mid(tb.rects[ButtonPDFA])
	tb.HandleEvent(ptr(platform.Move, platform.None, x, y))
	if tb.hover != ButtonPDFA {
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

// buttonPixel returns the pixel a few pixels inside the top-left corner of
// button id: past its rounded corner, and short of its icon, so it shows only
// the button's background.
func buttonPixel(c *paint.Canvas, tb *Toolbar, id ButtonID) (r, g, b, a uint8) {
	p := c.Img.RGBAAt(tb.rects[id].Min.X+3, tb.rects[id].Min.Y+3)
	return p.R, p.G, p.B, p.A
}

func TestToolbarDrawStatesAndContent(t *testing.T) {
	tb := newToolbar(t)
	c := paint.New(900, 60)
	tb.Draw(c, image.Pt(0, 10))
	ex, _ := empty(tb)
	if got := c.Img.RGBAAt(ex, 10+3); got != nrgba(palette.Chrome) {
		t.Errorf("background = %v, want chrome", got)
	}
	if got := c.Img.RGBAAt(ex, 10+theme.ToolbarHeight-1); got != nrgba(palette.Border) {
		t.Errorf("bottom line = %v, want border", got)
	}
	if got := c.Img.RGBAAt(ex, 5); got.A != 0 {
		t.Errorf("painted above the bar: %v", got)
	}
	// The logo, every control but the find field, and the page count ink
	// their places; the find field's is left for its owner.
	if anyInk(c, tb.logo.Add(image.Pt(0, 10))).Empty() {
		t.Error("the logo was not drawn")
	}
	for id := range buttonCount {
		r := tb.rects[id].Add(image.Pt(0, 10))
		if drawn := !anyInk(c, r.Inset(3)).Empty(); drawn != (id != ButtonFind) {
			t.Errorf("control %d drawn: %v", id, drawn)
		}
	}
	if anyInk(c, tb.pagesBox.Add(image.Pt(0, 10))).Empty() {
		t.Error("the page count was not drawn")
	}

	idle := nrgba(palette.Chrome)
	// A hovered button gets the Hover wash, a button that is pressed and
	// hovered gets the Active wash, and so does a switched-on button.
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
	tb.On[ButtonPDFA] = true
	c = paint.New(900, 60)
	tb.Draw(c, image.Point{})
	if r, g, b, _ := buttonPixel(c, tb, ButtonPDFA); (color4{r, g, b}) == (color4{idle.R, idle.G, idle.B}) {
		t.Error("a switched-on button is not highlighted")
	}
	// A disabled button still draws, dimmed.
	enabled := paint.New(900, 60)
	tb.Draw(enabled, image.Point{})
	tb.Disabled[ButtonOpen] = true
	dimmed := paint.New(900, 60)
	tb.Draw(dimmed, image.Point{})
	for _, r := range []image.Rectangle{tb.rects[ButtonOpen]} {
		if sumAlpha(dimmed, r) == 0 {
			t.Errorf("disabled control at %v drew nothing", r)
		}
		if sumRed(dimmed, r) == sumRed(enabled, r) {
			t.Errorf("disabled control at %v looks enabled", r)
		}
	}
}

func TestToolbarThemeSwitchShowsTheOtherTheme(t *testing.T) {
	tb := newToolbar(t)
	light := paint.New(900, 40)
	tb.Draw(light, image.Point{})
	tb.Palette = theme.Dark()
	dark := paint.New(900, 40)
	tb.Draw(dark, image.Point{})
	// The moon and the sun cover different pixels.
	r := tb.rects[ButtonTheme]
	if inkShape(light, r, palette.Chrome) == inkShape(dark, r, theme.Dark().Chrome) {
		t.Error("the theme switch shows the same icon in both themes")
	}
}

// inkShape returns how many pixels in r differ from bg: the size of what is
// drawn there, whatever its colour.
func inkShape(c *paint.Canvas, r image.Rectangle, bg color.NRGBA) int {
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if c.Img.RGBAAt(x, y) != nrgba(bg) {
				n++
			}
		}
	}
	return n
}

func TestToolbarPageFieldWhileEditing(t *testing.T) {
	tb := newToolbar(t)
	r := tb.rects[ButtonPage]
	shown := paint.New(900, 40)
	tb.Draw(shown, image.Point{})
	tb.EditingPage = true
	tb.PageField.SetText("7")
	tb.PageField.SelectAll()
	editing := paint.New(900, 40)
	tb.Draw(editing, image.Point{})
	// A focus ring in the accent colour surrounds the field, and the text
	// typed replaces the page number.
	if inkIn(editing, r.Inset(-2), palette.Accent).Empty() {
		t.Error("no focus ring around the page field")
	}
	if sumAlpha(editing, r) == sumAlpha(shown, r) {
		t.Error("the field looks the same while it is edited")
	}
	// The field takes only digits.
	tb.PageField.SetText("")
	tb.PageField.Insert("1a2")
	if tb.PageField.String() != "12" {
		t.Errorf("page field took %q", tb.PageField.String())
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

func TestToolbarLaysOutOnDemand(t *testing.T) {
	face, _ := text.Mono().Face(theme.FontSize, false)
	tb := NewToolbar(palette, face)
	tb.Page, tb.Pages, tb.ZoomText = 1, 1, "100%"
	// Events before any layout lay the bar out first.
	x, y := mid(tb.ButtonRect(ButtonOpen))
	fresh := NewToolbar(palette, face)
	fresh.HandleEvent(ptr(platform.Press, platform.Left, x, y))
	if a := fresh.HandleEvent(ptr(platform.Release, platform.Left, x, y)); a != (Action{Kind: ActionButton, Index: int(ButtonOpen)}) {
		t.Errorf("click on an unlaid toolbar = %+v", a)
	}
	// Drawing an unlaid bar does the same.
	unlaid := NewToolbar(palette, face)
	unlaid.ZoomText = "100%"
	c := paint.New(400, 40)
	unlaid.Draw(c, image.Point{})
	if anyInk(c, image.Rect(0, 0, 400, 35)).Empty() {
		t.Error("an unlaid toolbar drew nothing")
	}
}

func TestToolbarButtonRect(t *testing.T) {
	tb := newToolbar(t)
	if tb.ButtonRect(ButtonNext) != tb.rects[ButtonNext] {
		t.Error("ButtonRect disagrees with the layout")
	}
	fresh := NewToolbar(palette, tb.Face)
	if fresh.ButtonRect(ButtonOpen).Empty() {
		t.Error("an unlaid toolbar has no button rectangle")
	}
}
