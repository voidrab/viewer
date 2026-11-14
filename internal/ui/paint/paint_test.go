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
	c.Stroke(image.Rect(0, 0, 4, 4), 0, red) // zero width draws nothing more
	if at(c, 0, 0).A != 0 {
		t.Error("zero-width stroke painted")
	}
	small := New(4, 4)
	small.Stroke(small.Bounds(), 9, red) // wider than the box: fills it
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
