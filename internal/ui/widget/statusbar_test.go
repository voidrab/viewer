package widget

import (
	"image"
	"image/color"
	"testing"

	"viewrab/internal/ui/paint"
	"viewrab/internal/ui/text"
	"viewrab/internal/ui/theme"
)

func newStatus(t *testing.T) *StatusBar {
	t.Helper()
	face, err := text.Mono().Face(theme.SmallFontSize, false)
	if err != nil {
		t.Fatal(err)
	}
	return NewStatusBar(palette, face)
}

// inkIn returns the rectangle of pixels in rect that match col within
// inkTolerance in each channel. The background, the border and fully
// transparent pixels are ignored.
func inkIn(c *paint.Canvas, rect image.Rectangle, col color.NRGBA) image.Rectangle {
	var out image.Rectangle
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			p := c.Img.RGBAAt(x, y)
			if p == nrgba(palette.Chrome) || p == nrgba(palette.Border) || p.A == 0 {
				continue
			}
			if near(p, nrgba(col)) {
				out = out.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return out
}

// inkTolerance is how far a pixel's channels may be from a colour for it to
// count as ink in that colour: well under the distance between any two of a
// palette's text colours.
const inkTolerance = 32

// near reports whether p is within inkTolerance of q in every channel.
func near(p, q color.RGBA) bool {
	d := func(a, b uint8) int { return abs(int(a) - int(b)) }
	return d(p.R, q.R) <= inkTolerance && d(p.G, q.G) <= inkTolerance && d(p.B, q.B) <= inkTolerance && p.A == q.A
}

// anyInk returns the rectangle of pixels in rect that are neither background
// nor border, including partly covered glyph edges.
func anyInk(c *paint.Canvas, rect image.Rectangle) image.Rectangle {
	var out image.Rectangle
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			p := c.Img.RGBAAt(x, y)
			if p != nrgba(palette.Chrome) && p != nrgba(palette.Border) && p.A != 0 {
				out = out.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return out
}

func TestStatusBarLayout(t *testing.T) {
	s := newStatus(t)
	s.Info = "invoice.pdf  1.2 MB"
	s.Right = []string{"PDF/A-1b", "100%", "3 / 12"}
	rect := image.Rect(0, 100, 800, 100+s.Height())
	c := paint.New(800, 200)
	s.Draw(c, rect)

	if got := c.Img.RGBAAt(400, 100+s.Height()-2); got != nrgba(palette.Chrome) {
		t.Errorf("background = %v, want chrome", got)
	}
	if got := c.Img.RGBAAt(400, 100); got != nrgba(palette.Border) {
		t.Errorf("top line = %v, want border", got)
	}
	if got := c.Img.RGBAAt(400, 99); got.A != 0 {
		t.Errorf("painted above the bar: %v", got)
	}
	ink := anyInk(c, image.Rect(0, 101, 800, 100+s.Height()))
	if ink.Empty() {
		t.Fatal("no text drawn")
	}
	// Info starts at the left padding; the right values end at the right
	// padding.
	if left := anyInk(c, image.Rect(0, 101, 400, 100+s.Height())); left.Min.X < statusPad || left.Min.X > statusPad+3 {
		t.Errorf("info starts at x=%d, want about %d", left.Min.X, statusPad)
	}
	if ink.Max.X > 800-statusPad+1 || ink.Max.X < 800-statusPad-6 {
		t.Errorf("right values end at x=%d, want about %d", ink.Max.X, 800-statusPad)
	}
	// Text is vertically centred in the bar.
	mid := (ink.Min.Y + ink.Max.Y) / 2
	if centre := 100 + s.Height()/2; mid < centre-4 || mid > centre+4 {
		t.Errorf("text centre y=%d, bar centre %d", mid, centre)
	}
}

func TestStatusBarMessageColours(t *testing.T) {
	s := newStatus(t)
	s.Info = "a.pdf"
	for kind, want := range map[MessageKind]color.NRGBA{
		MessageInfo: palette.Text, MessageOK: palette.OK, MessageWarn: palette.Warn, MessageBad: palette.Bad,
	} {
		s.Message, s.Kind = "converted", kind
		rect := image.Rect(0, 0, 500, s.Height())
		c := paint.New(500, s.Height())
		s.Draw(c, rect)
		after := image.Rect(statusPad+s.Face.Width("a.pdf")+statusGap-2, 0, 500, s.Height())
		if inkIn(c, after, want).Empty() {
			t.Errorf("message kind %d is not drawn in its colour", kind)
		}
	}
	// No message: nothing after the info.
	s.Message = ""
	c := paint.New(500, s.Height())
	s.Draw(c, image.Rect(0, 0, 500, s.Height()))
	if !inkIn(c, image.Rect(statusPad+s.Face.Width("a.pdf")+statusGap, 0, 500, s.Height()), palette.Accent).Empty() {
		t.Error("a message appeared without one set")
	}
}

func TestStatusBarShortensWhatDoesNotFit(t *testing.T) {
	s := newStatus(t)
	s.Info = "a-very-long-document-name-indeed.pdf  12.4 MB"
	s.Message = "something worth saying at length"
	s.Right = []string{"PDF/A-1b", "100%"}
	width := 260
	c := paint.New(width, s.Height())
	s.Draw(c, image.Rect(0, 0, width, s.Height()))
	// The info is shortened so that it stops short of the right-hand values,
	// which keep their full width.
	rightW := s.Face.Width("PDF/A-1b") + statusGap + s.Face.Width("100%")
	rightStart := width - statusPad - rightW
	if left := anyInk(c, image.Rect(0, 1, rightStart-statusGap/2, s.Height())); left.Max.X > rightStart-statusGap+2 {
		t.Errorf("the info runs to x=%d, into the values that start at %d", left.Max.X, rightStart)
	}
	if anyInk(c, image.Rect(rightStart-2, 1, width, s.Height())).Empty() {
		t.Error("the right-hand values were squeezed out")
	}
	// A bar too narrow for its content must draw without panicking. The
	// message is skipped when no room is left after the info.
	narrow := paint.New(60, s.Height())
	s.Draw(narrow, image.Rect(0, 0, 60, s.Height()))
	s.Info, s.Right = "", []string{"x"}
	s.Draw(narrow, image.Rect(0, 0, 60, s.Height()))
	// And an empty bar draws just its chrome.
	s.Message, s.Right = "", nil
	empty := paint.New(100, s.Height())
	s.Draw(empty, image.Rect(0, 0, 100, s.Height()))
	if got := empty.Img.RGBAAt(50, s.Height()-3); got != nrgba(palette.Chrome) {
		t.Errorf("empty bar = %v", got)
	}
}

func TestStatusBarShowsTheClaimedConformanceInTheOKColour(t *testing.T) {
	s := newStatus(t)
	s.Info, s.Claimed, s.Right = "a.pdf", "PDF/A-2b", []string{"134%"}
	c := paint.New(500, s.Height())
	s.Draw(c, image.Rect(0, 0, 500, s.Height()))
	ok := inkIn(c, image.Rect(0, 0, 500, s.Height()), palette.OK)
	zoom := 500 - statusPad - s.Face.Width("134%")
	if ok.Empty() || ok.Max.X > zoom-statusGap+1 {
		t.Errorf("claimed conformance at %v, want it left of the zoom at %d", ok, zoom)
	}
}
