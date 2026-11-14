package paint

import (
	"image"
	"image/color"
	"testing"
)

var (
	red   = color.NRGBA{255, 0, 0, 255}
	white = color.NRGBA{255, 255, 255, 255}
)

func at(c *Canvas, x, y int) color.RGBA { return c.Img.RGBAAt(x, y) }

func TestFillClipsAndBlends(t *testing.T) {
	c := New(10, 10)
	c.Fill(c.Bounds(), white)
	c.Fill(image.Rect(-5, -5, 3, 3), red)
	if at(c, 0, 0) != (color.RGBA{255, 0, 0, 255}) || at(c, 3, 3) != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("fill: %v %v", at(c, 0, 0), at(c, 3, 3))
	}
	// Half-transparent black over white.
	c.Fill(image.Rect(5, 5, 6, 6), color.NRGBA{0, 0, 0, 128})
	if p := at(c, 5, 5); p.R != 127 || p.A != 255 {
		t.Errorf("blend = %v", p)
	}
	// Fully transparent and empty fills draw nothing.
	c.Fill(image.Rect(7, 7, 8, 8), color.NRGBA{0, 0, 0, 0})
	c.Fill(image.Rect(2, 2, 2, 5), red)
	if at(c, 7, 7) != (color.RGBA{255, 255, 255, 255}) || at(c, 2, 3) != (color.RGBA{255, 255, 255, 255}) {
		t.Error("no-op fill painted")
	}
}

func TestClipStack(t *testing.T) {
	c := New(10, 10)
	c.PopClip() // unmatched: ignored
	if c.Clip() != c.Bounds() {
		t.Fatalf("Clip = %v", c.Clip())
	}
	c.PushClip(image.Rect(2, 2, 8, 8))
	c.PushClip(image.Rect(0, 0, 5, 5))
	if c.Clip() != image.Rect(2, 2, 5, 5) {
		t.Fatalf("nested Clip = %v", c.Clip())
	}
	c.Fill(c.Bounds(), red)
	if at(c, 1, 1).A != 0 || at(c, 4, 4).A != 255 || at(c, 5, 5).A != 0 {
		t.Error("clip not honoured")
	}
	c.PopClip()
	c.PopClip()
	if c.Clip() != c.Bounds() {
		t.Errorf("Clip after pops = %v", c.Clip())
	}
}

func TestStroke(t *testing.T) {
	c := New(10, 10)
	c.Stroke(image.Rect(1, 1, 9, 9), 2, red)
	for _, p := range [][2]int{{1, 1}, {8, 8}, {2, 5}, {7, 5}, {5, 2}, {5, 7}} {
		if at(c, p[0], p[1]).A != 255 {
			t.Errorf("border pixel %v unpainted", p)
		}
	}
	if at(c, 4, 4).A != 0 || at(c, 0, 0).A != 0 {
		t.Error("stroke painted the inside or outside")
	}
	c.Stroke(image.Rect(0, 0, 4, 4), 0, red) // A zero width draws nothing.
	if at(c, 0, 0).A != 0 {
		t.Error("zero-width stroke painted")
	}
	small := New(4, 4)
	small.Stroke(small.Bounds(), 9, red) // A width larger than the box fills it.
	if at(small, 2, 2).A != 255 {
		t.Error("oversized stroke left a hole")
	}
}

func TestDrawImage(t *testing.T) {
	src := image.NewRGBA(image.Rect(10, 10, 12, 12))
	src.SetRGBA(10, 10, color.RGBA{255, 0, 0, 255})
	src.SetRGBA(11, 10, color.RGBA{0, 0, 128, 128})
	c := New(4, 4)
	c.Fill(c.Bounds(), white)
	c.DrawImage(image.Pt(-1, 0), src) // clipped at the left edge
	if at(c, 0, 0) != (color.RGBA{127, 127, 255, 255}) {
		t.Errorf("blended pixel = %v", at(c, 0, 0))
	}
	c.DrawImage(image.Pt(2, 2), src)
	if at(c, 2, 2) != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("drawn pixel = %v", at(c, 2, 2))
	}
	before := append([]uint8(nil), c.Img.Pix...)
	c.DrawImage(image.Pt(50, 50), src) // fully outside
	for i, v := range c.Img.Pix {
		if v != before[i] {
			t.Fatal("outside draw changed pixels")
		}
	}
}

func TestDrawScaled(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	src.SetRGBA(0, 0, color.RGBA{255, 0, 0, 255})
	src.SetRGBA(1, 0, color.RGBA{0, 255, 0, 255})
	src.SetRGBA(0, 1, color.RGBA{0, 0, 255, 255})
	src.SetRGBA(1, 1, color.RGBA{255, 255, 255, 255})

	c := New(8, 8)
	c.DrawScaled(image.Rect(2, 2, 6, 6), src) // 2x enlargement
	for _, tc := range []struct {
		x, y int
		want color.RGBA
	}{
		{2, 2, color.RGBA{255, 0, 0, 255}}, {3, 3, color.RGBA{255, 0, 0, 255}},
		{4, 2, color.RGBA{0, 255, 0, 255}}, {2, 5, color.RGBA{0, 0, 255, 255}},
		{5, 5, color.RGBA{255, 255, 255, 255}},
	} {
		if got := at(c, tc.x, tc.y); got != tc.want {
			t.Errorf("(%d,%d) = %v, want %v", tc.x, tc.y, got, tc.want)
		}
	}
	if at(c, 1, 1).A != 0 || at(c, 6, 6).A != 0 {
		t.Error("painted outside the destination")
	}

	// Shrinking, with the destination partly clipped away, and the same-size
	// shortcut.
	small := New(4, 4)
	small.PushClip(image.Rect(0, 0, 1, 4))
	small.DrawScaled(image.Rect(0, 0, 4, 4), src)
	if at(small, 0, 0) != (color.RGBA{255, 0, 0, 255}) || at(small, 1, 0).A != 0 {
		t.Errorf("clipped scaled draw: %v %v", at(small, 0, 0), at(small, 1, 0))
	}
	same := New(2, 2)
	same.DrawScaled(image.Rect(0, 0, 2, 2), src)
	if at(same, 1, 0) != (color.RGBA{0, 255, 0, 255}) {
		t.Errorf("same-size draw = %v", at(same, 1, 0))
	}
	// Degenerate sizes draw nothing and do not panic.
	same.DrawScaled(image.Rectangle{}, src)
	same.DrawScaled(image.Rect(0, 0, 2, 2), image.NewRGBA(image.Rectangle{}))
}

func TestDrawScaledSameSizePartlyOutsideTheClip(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 3; x++ {
			src.SetRGBA(x, y, color.RGBA{uint8(10 * x), uint8(10 * y), 7, 255})
		}
	}
	c := New(4, 4)
	c.DrawScaled(image.Rect(-1, -2, 2, 1), src) // only the source's lower-right 2x1 part shows
	for _, p := range [][2]int{{0, 0}, {1, 0}} {
		want := color.RGBA{uint8(10 * (p[0] + 1)), 20, 7, 255}
		if got := at(c, p[0], p[1]); got != want {
			t.Errorf("(%d,%d) = %v, want %v", p[0], p[1], got, want)
		}
	}
	if at(c, 2, 0).A != 0 || at(c, 0, 1).A != 0 {
		t.Error("painted outside the destination")
	}
	// A destination wholly outside the canvas draws nothing.
	c.DrawScaled(image.Rect(10, 10, 13, 13), src)
	c.DrawScaled(image.Rect(10, 10, 20, 20), src)
}

func TestOpaqueFillReplacesWhatIsThere(t *testing.T) {
	c := New(3, 3)
	c.Fill(c.Bounds(), color.NRGBA{9, 9, 9, 128})
	c.Fill(image.Rect(1, 0, 3, 2), color.NRGBA{200, 100, 50, 255})
	if got := at(c, 2, 1); got != (color.RGBA{200, 100, 50, 255}) {
		t.Errorf("opaque fill = %v", got)
	}
	if got := at(c, 0, 0); got.A != 128 {
		t.Errorf("a pixel outside the fill changed: %v", got)
	}
}

func TestFillRoundRoundsTheCornersOnly(t *testing.T) {
	c := New(20, 20)
	c.Fill(c.Bounds(), white)
	c.FillRound(image.Rect(2, 2, 18, 12), 4, red)
	if p := at(c, 10, 7); p != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("middle = %v", p)
	}
	if p := at(c, 10, 2); p != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("straight top edge = %v", p)
	}
	if p := at(c, 2, 2); p != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("corner pixel = %v, want it left out", p)
	}
	// The curve is anti-aliased: a pixel on it is partly covered.
	if p := at(c, 3, 3); p.G == 0 || p.G == 255 {
		t.Errorf("pixel on the curve = %v, want a blend", p)
	}
	if p := at(c, 1, 7); p != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("outside = %v", p)
	}
}

func TestFillRoundOfASquareIsACircle(t *testing.T) {
	c := New(20, 20)
	c.FillRound(image.Rect(0, 0, 20, 20), 100, red)
	if at(c, 10, 10).A != 255 || at(c, 0, 0).A != 0 || at(c, 2, 2).A != 0 || at(c, 10, 0).A == 0 {
		t.Errorf("circle: centre %v corner %v near corner %v top %v", at(c, 10, 10), at(c, 0, 0), at(c, 2, 2), at(c, 10, 0))
	}
}

func TestFillRoundFallsBackAndClips(t *testing.T) {
	c := New(10, 10)
	// No radius is a plain fill, corners included.
	c.FillRound(image.Rect(0, 0, 4, 4), 0, red)
	if at(c, 0, 0).A != 255 {
		t.Error("radius 0 did not fill the corner")
	}
	// Outside the clip, or transparent, nothing is drawn.
	c.PushClip(image.Rect(5, 5, 10, 10))
	c.FillRound(image.Rect(0, 0, 5, 5), 2, red)
	c.FillRound(image.Rect(5, 5, 10, 10), 2, color.NRGBA{})
	c.PopClip()
	if at(c, 4, 4).A != 0 || at(c, 7, 7).A != 0 {
		t.Error("clipped or transparent round fill painted")
	}
	if coverage(0, 0, 0, 3, 1) != 0 {
		t.Error("an empty rectangle covers something")
	}
}

func TestStrokeRoundPaintsOnlyTheBorder(t *testing.T) {
	c := New(20, 20)
	c.StrokeRound(image.Rect(0, 0, 20, 14), 4, 1, red)
	if at(c, 10, 0).A != 255 || at(c, 0, 7).A != 255 || at(c, 19, 7).A != 255 {
		t.Errorf("edges: top %v left %v right %v", at(c, 10, 0), at(c, 0, 7), at(c, 19, 7))
	}
	if at(c, 10, 7).A != 0 || at(c, 0, 0).A != 0 {
		t.Errorf("inside %v or corner %v painted", at(c, 10, 7), at(c, 0, 0))
	}
	// No width draws nothing; no radius is a square stroke.
	c2 := New(10, 10)
	c2.StrokeRound(c2.Bounds(), 3, 0, red)
	if at(c2, 5, 0).A != 0 {
		t.Error("zero-width stroke painted")
	}
	c2.StrokeRound(c2.Bounds(), 0, 1, red)
	if at(c2, 0, 0).A != 255 || at(c2, 5, 5).A != 0 {
		t.Error("square stroke wrong")
	}
}

func TestStrokeRoundWiderThanItsCorners(t *testing.T) {
	c := New(20, 20)
	c.StrokeRound(image.Rect(0, 0, 20, 20), 2, 5, red)
	if at(c, 10, 2).A != 255 || at(c, 4, 4).A != 255 || at(c, 10, 10).A != 0 || at(c, 0, 0).A == 255 {
		t.Errorf("band: edge %v inner corner %v middle %v outer corner %v", at(c, 10, 2), at(c, 4, 4), at(c, 10, 10), at(c, 0, 0))
	}
}

func TestShadowFadesOutwards(t *testing.T) {
	c := New(30, 30)
	r := image.Rect(10, 10, 20, 20)
	c.Shadow(r, 0, 1, []uint8{80, 40})
	inner, outer := at(c, 15, 21).A, at(c, 15, 22).A // below r, dropped a pixel
	if inner == 0 || outer == 0 || outer >= inner || at(c, 15, 15).A != 0 {
		t.Errorf("shadow: inner ring %d outer ring %d inside %d", inner, outer, at(c, 15, 15).A)
	}
}
