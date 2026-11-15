package text

import (
	"errors"
	"image"
	"image/color"
	"math"
	"testing"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"

	"viewrab/internal/ui/paint"
)

var black = color.NRGBA{0, 0, 0, 255}

func face(t *testing.T, size float64, bold bool) *Face {
	t.Helper()
	f, err := Mono().Face(size, bold)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// inkBounds returns the rectangle covering every pixel c has painted.
func inkBounds(c *paint.Canvas) image.Rectangle {
	var r image.Rectangle
	b := c.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if c.Img.RGBAAt(x, y).A != 0 {
				r = r.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return r
}

func TestMetricsAndMonospacing(t *testing.T) {
	f := face(t, 14, false)
	if f.Height() < 14 || f.Height() > 24 || f.Ascent() <= 0 || f.Ascent() >= f.Height() {
		t.Errorf("height %d ascent %d at 14px", f.Height(), f.Ascent())
	}
	// Every glyph advances the same.
	if a, b := f.Width("iiii"), f.Width("WWWW"); a != b {
		t.Errorf("Width(iiii) = %d, Width(WWWW) = %d; a monospace face advances equally", a, b)
	}
	// Width grows with the text and with the size.
	if f.Width("abcdef") <= f.Width("abc") || face(t, 28, false).Width("abc") <= f.Width("abc") {
		t.Error("Width does not scale with text length or size")
	}
	if f.Width("") != 0 {
		t.Errorf("Width of nothing = %d", f.Width(""))
	}
	// A Liberation Mono advance is 0.6 em.
	if w := face(t, 20, false).Width("a"); math.Abs(float64(w)-12) > 1 {
		t.Errorf("advance at 20px = %d, want about 12", w)
	}
}

func TestDrawPaintsInsideItsLine(t *testing.T) {
	f := face(t, 16, false)
	c := paint.New(120, 40)
	f.Draw(c, 10, 8, "Hello", black)
	ink := inkBounds(c)
	if ink.Empty() {
		t.Fatal("nothing was drawn")
	}
	line := image.Rect(10, 8, 10+f.Width("Hello"), 8+f.Height())
	if !ink.In(line) {
		t.Errorf("ink %v spills outside the line box %v", ink, line)
	}
	// No ink may extend below the line box.
	if ink.Max.Y > 8+f.Ascent()+f.Height()-f.Ascent() {
		t.Errorf("ink reaches %d", ink.Max.Y)
	}
	// Some pixel is solid, and the paint is the requested colour.
	solid := false
	for y := range 40 {
		for x := range 120 {
			if p := c.Img.RGBAAt(x, y); p.A == 255 && p.R == 0 && p.G == 0 && p.B == 0 {
				solid = true
			}
		}
	}
	if !solid {
		t.Error("no fully covered pixel of the text colour")
	}
}

func TestDrawHonoursTheClipColourAndBold(t *testing.T) {
	f := face(t, 16, false)
	c := paint.New(120, 40)
	c.PushClip(image.Rect(0, 0, 25, 40))
	f.Draw(c, 10, 8, "Hello world", black)
	if ink := inkBounds(c); ink.Empty() || ink.Max.X > 25 {
		t.Errorf("clipped text ink = %v", ink)
	}
	c.PopClip()

	// The text is drawn in the requested colour.
	red := paint.New(60, 30)
	f.Draw(red, 2, 2, "M", color.NRGBA{255, 0, 0, 255})
	found := false
	for y := range 30 {
		for x := range 60 {
			if p := red.Img.RGBAAt(x, y); p.A == 255 && p.R == 255 && p.G == 0 {
				found = true
			}
		}
	}
	if !found {
		t.Error("the text was not drawn in red")
	}

	// Bold covers more ink than regular.
	count := func(bold bool) int {
		c := paint.New(120, 40)
		face(t, 16, bold).Draw(c, 4, 4, "Hello", black)
		n := 0
		for i := 3; i < len(c.Img.Pix); i += 4 {
			n += int(c.Img.Pix[i])
		}
		return n
	}
	if count(true) <= count(false) {
		t.Error("bold text is not heavier than regular")
	}
}

func TestDrawSkipsWhatCannotShow(t *testing.T) {
	f := face(t, 16, false)
	c := paint.New(60, 30)
	f.Draw(c, 0, 0, "", black)
	f.Draw(c, 0, 0, "text", color.NRGBA{0, 0, 0, 0})
	if !inkBounds(c).Empty() {
		t.Error("something was drawn")
	}
}

func TestFit(t *testing.T) {
	f := face(t, 14, false)
	adv := f.Width("a")
	full := "Hello, world"
	if got := f.Fit(full, f.Width(full)); got != full {
		t.Errorf("Fit of text that fits = %q", got)
	}
	got := f.Fit(full, adv*6)
	if f.Width(got) > adv*6 || got == full || []rune(got)[len([]rune(got))-1] != '…' {
		t.Errorf("Fit(%q, %d) = %q (%dpx)", full, adv*6, got, f.Width(got))
	}
	// Only the ellipsis fits.
	if got := f.Fit(full, f.Width("…")); got != "…" {
		t.Errorf("Fit into one cell = %q", got)
	}
	// Not even the ellipsis fits.
	if got := f.Fit(full, 1); got != "" {
		t.Errorf("Fit into 1px = %q", got)
	}
	// Multi-byte text is cut on rune boundaries.
	if got := f.Fit("äöüäöüäöü", adv*4); f.Width(got) > adv*4 || got == "" {
		t.Errorf("Fit of umlauts = %q", got)
	}
}

func TestFacesAreCachedAndValidated(t *testing.T) {
	fam := Mono()
	a, err := fam.Face(12, false)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := fam.Face(12, false); b != a {
		t.Error("the same face was built twice")
	}
	if b, _ := fam.Face(12, true); b == a {
		t.Error("bold and regular share a face")
	}
	if b, _ := fam.Face(13, false); b == a {
		t.Error("two sizes share a face")
	}
	for _, size := range []float64{0, -3, math.NaN()} {
		if _, err := fam.Face(size, false); !errors.Is(err, ErrSize) {
			t.Errorf("Face(%v) = %v, want ErrSize", size, err)
		}
	}
}

func TestFaceFailuresAreReported(t *testing.T) {
	boom := errors.New("boom")
	origParse, origNew := parseFont, newFace
	defer func() { parseFont, newFace = origParse, origNew }()

	parseFont = func([]byte) (*opentype.Font, error) { return nil, boom }
	if _, err := Mono().Face(12, false); !errors.Is(err, boom) {
		t.Errorf("parse failure = %v", err)
	}
	parseFont = origParse
	newFace = func(*opentype.Font, *opentype.FaceOptions) (font.Face, error) { return nil, boom }
	if _, err := Mono().Face(12, false); !errors.Is(err, boom) {
		t.Errorf("face failure = %v", err)
	}
}
