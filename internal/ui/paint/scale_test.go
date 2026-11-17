package paint

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// TestNewScaled verifies a scaled canvas's sizes: logical bounds, a bitmap of
// the device size, and scale 1 for a scale that is not positive.
func TestNewScaled(t *testing.T) {
	c := NewScaled(10, 4, 2.5)
	if c.Bounds() != image.Rect(0, 0, 10, 4) || c.Img.Rect != image.Rect(0, 0, 25, 10) || c.Scale() != 2.5 {
		t.Errorf("NewScaled(10, 4, 2.5): bounds %v, bitmap %v, scale %v", c.Bounds(), c.Img.Rect, c.Scale())
	}
	for _, s := range []float64{0, -1, math.NaN()} {
		if c := NewScaled(3, 3, s); c.Scale() != 1 || c.Img.Rect != image.Rect(0, 0, 3, 3) {
			t.Errorf("NewScaled(3, 3, %v): scale %v, bitmap %v", s, c.Scale(), c.Img.Rect)
		}
	}
}

// TestScaledFillsLeaveNoSeams verifies that fills side by side at a
// fractional scale cover the bitmap exactly, with no gap or overlap.
func TestScaledFillsLeaveNoSeams(t *testing.T) {
	for _, s := range []float64{1.5, 2.625} {
		c := NewScaled(7, 1, s)
		for x := range 7 {
			c.Fill(image.Rect(x, 0, x+1, 1), color.NRGBA{0, 0, 0, 128})
		}
		for x := range c.Img.Rect.Dx() {
			if a := at(c, x, 0).A; a != 128 {
				t.Errorf("scale %v: device pixel %d has alpha %d, want 128 from one fill", s, x, a)
			}
		}
	}
}

// TestScaledDrawing verifies that strokes, rounded fills, clips, images and
// stretched images land on the device pixels of their logical places.
func TestScaledDrawing(t *testing.T) {
	c := NewScaled(10, 10, 2)
	c.Fill(c.Bounds(), white)
	c.Stroke(image.Rect(1, 1, 5, 5), 1, red)
	if at(c, 2, 3) != (color.RGBA{255, 0, 0, 255}) || at(c, 3, 4) != (color.RGBA{255, 0, 0, 255}) || at(c, 4, 4) != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("a 1-pixel stroke at scale 2 is not 2 device pixels wide: %v %v %v", at(c, 2, 3), at(c, 3, 4), at(c, 4, 4))
	}

	c.PushClip(image.Rect(6, 0, 10, 10))
	c.FillRound(image.Rect(5, 5, 9, 9), 2, red)
	c.PopClip()
	if at(c, 11, 13) != (color.RGBA{255, 255, 255, 255}) || at(c, 14, 14) != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("FillRound is not clipped at device pixel 12: %v %v", at(c, 11, 13), at(c, 14, 14))
	}
	c.StrokeRound(image.Rect(0, 6, 4, 10), 1, 1, red)
	if at(c, 0, 16) != (color.RGBA{255, 0, 0, 255}) || at(c, 4, 16) != (color.RGBA{255, 255, 255, 255}) {
		t.Errorf("StrokeRound at scale 2: %v %v", at(c, 0, 16), at(c, 4, 16))
	}

	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{0, 0, 255, 255})
	c.DrawImage(image.Pt(8, 0), img)
	if at(c, 16, 0) != (color.RGBA{0, 0, 255, 255}) {
		t.Errorf("DrawImage does not place its device pixels at the logical point: %v", at(c, 16, 0))
	}

	c.DrawScaled(image.Rect(8, 8, 9, 9), img)
	if at(c, 16, 16) != (color.RGBA{0, 0, 255, 255}) || at(c, 17, 17) != (color.RGBA{}) {
		t.Errorf("DrawScaled does not copy a device-sized image one to one: %v %v", at(c, 16, 16), at(c, 17, 17))
	}
}
