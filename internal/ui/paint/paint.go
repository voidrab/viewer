// Package paint is a small software painter over an RGBA bitmap: filled and
// stroked rectangles, square or rounded, image blits and a clip stack. Text
// and icons build on it.
package paint

import (
	"image"
	"image/color"
	"math"
)

// Canvas draws into Img, limited to the current clip rectangle.
type Canvas struct {
	Img   *image.RGBA
	clips []image.Rectangle
}

// New returns a canvas over a fresh w x h bitmap.
func New(w, h int) *Canvas {
	return &Canvas{Img: image.NewRGBA(image.Rect(0, 0, w, h))}
}

// Bounds returns the bitmap's rectangle.
func (c *Canvas) Bounds() image.Rectangle { return c.Img.Rect }

// Clip returns the rectangle drawing is currently limited to.
func (c *Canvas) Clip() image.Rectangle {
	if n := len(c.clips); n > 0 {
		return c.clips[n-1]
	}
	return c.Img.Rect
}

// PushClip narrows the clip to its intersection with r.
func (c *Canvas) PushClip(r image.Rectangle) {
	c.clips = append(c.clips, c.Clip().Intersect(r))
}

// PopClip restores the clip before the matching PushClip. An unmatched call
// does nothing.
func (c *Canvas) PopClip() {
	if n := len(c.clips); n > 0 {
		c.clips = c.clips[:n-1]
	}
}

// Fill paints r with col, blending source-over; col is non-premultiplied.
func (c *Canvas) Fill(r image.Rectangle, col color.NRGBA) {
	r = r.Intersect(c.Clip())
	if r.Empty() || col.A == 0 {
		return
	}
	pre := color.RGBAModel.Convert(col).(color.RGBA)
	if col.A == 255 {
		// An opaque fill replaces what is there: the first row is written
		// once and copied to the others.
		first := c.Img.Pix[c.Img.PixOffset(r.Min.X, r.Min.Y):][:4*r.Dx()]
		for i := 0; i < len(first); i += 4 {
			first[i], first[i+1], first[i+2], first[i+3] = pre.R, pre.G, pre.B, 255
		}
		for y := r.Min.Y + 1; y < r.Max.Y; y++ {
			copy(c.Img.Pix[c.Img.PixOffset(r.Min.X, y):][:4*r.Dx()], first)
		}
		return
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := c.Img.Pix[c.Img.PixOffset(r.Min.X, y):][:4*r.Dx()]
		for i := 0; i < len(row); i += 4 {
			blend(row[i:i+4:i+4], pre)
		}
	}
}

// Stroke paints a border of the given width just inside r.
func (c *Canvas) Stroke(r image.Rectangle, width int, col color.NRGBA) {
	w := min(width, r.Dx()/2+1, r.Dy()/2+1)
	if w <= 0 {
		return
	}
	c.Fill(image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+w), col)
	c.Fill(image.Rect(r.Min.X, r.Max.Y-w, r.Max.X, r.Max.Y), col)
	c.Fill(image.Rect(r.Min.X, r.Min.Y+w, r.Min.X+w, r.Max.Y-w), col)
	c.Fill(image.Rect(r.Max.X-w, r.Min.Y+w, r.Max.X, r.Max.Y-w), col)
}

// FillRound paints r with col like Fill, with its corners rounded to radius
// and their edges anti-aliased. A radius of half the shorter side or more
// makes the ends round, and a square r a circle.
func (c *Canvas) FillRound(r image.Rectangle, radius int, col color.NRGBA) {
	c.round(r, radius, 0, col)
}

// StrokeRound paints a border of the given width just inside r like Stroke,
// following the corners rounded to radius.
func (c *Canvas) StrokeRound(r image.Rectangle, radius, width int, col color.NRGBA) {
	if width <= 0 {
		return
	}
	c.round(r, radius, width, col)
}

// round paints the rounded rectangle r, or with a positive width only the
// band of that width inside its edge. The straight parts are plain fills; only
// the corner squares are worked out pixel by pixel.
func (c *Canvas) round(r image.Rectangle, radius, width int, col color.NRGBA) {
	radius = min(radius, r.Dx()/2, r.Dy()/2)
	if radius <= 0 {
		if width > 0 {
			c.Stroke(r, width, col)
		} else {
			c.Fill(r, col)
		}
		return
	}
	if col.A == 0 {
		return
	}
	x0, y0, x1, y1, k := r.Min.X, r.Min.Y, r.Max.X, r.Max.Y, radius
	if width > radius {
		// A band wider than the corners: no straight parts to spare.
		c.curve(r, r, radius, width, col)
		return
	}
	var straight []image.Rectangle
	if width > 0 {
		straight = []image.Rectangle{
			image.Rect(x0+k, y0, x1-k, y0+width), image.Rect(x0+k, y1-width, x1-k, y1),
			image.Rect(x0, y0+k, x0+width, y1-k), image.Rect(x1-width, y0+k, x1, y1-k),
		}
	} else {
		straight = []image.Rectangle{
			image.Rect(x0+k, y0, x1-k, y1), image.Rect(x0, y0+k, x0+k, y1-k), image.Rect(x1-k, y0+k, x1, y1-k),
		}
	}
	for _, s := range straight {
		c.Fill(s, col)
	}
	for _, corner := range []image.Rectangle{
		image.Rect(x0, y0, x0+k, y0+k), image.Rect(x1-k, y0, x1, y0+k),
		image.Rect(x0, y1-k, x0+k, y1), image.Rect(x1-k, y1-k, x1, y1),
	} {
		c.curve(r, corner, radius, width, col)
	}
}

// curve paints the part within area of the rounded rectangle r, or of its
// band of the given width, blending each pixel by how much of it is covered.
func (c *Canvas) curve(r, area image.Rectangle, radius, width int, col color.NRGBA) {
	area = area.Intersect(c.Clip())
	cx, cy := float64(r.Min.X+r.Max.X)/2, float64(r.Min.Y+r.Max.Y)/2
	hx, hy := float64(r.Dx())/2, float64(r.Dy())/2
	inner := float64(max(radius-width, 0))
	for y := area.Min.Y; y < area.Max.Y; y++ {
		row := c.Img.Pix[c.Img.PixOffset(area.Min.X, y):][:4*area.Dx()]
		for i := 0; i < len(row); i += 4 {
			px, py := float64(area.Min.X+i/4)+0.5-cx, float64(y)+0.5-cy
			cov := coverage(px, py, hx, hy, float64(radius))
			if width > 0 {
				w := float64(width)
				cov -= coverage(px, py, hx-w, hy-w, inner)
			}
			if cov <= 0 {
				continue
			}
			// Premultiplied, at the covered share of the colour's opacity.
			a := uint32(float64(col.A)*min(cov, 1) + 0.5)
			pre := color.RGBA{uint8(uint32(col.R) * a / 255), uint8(uint32(col.G) * a / 255), uint8(uint32(col.B) * a / 255), uint8(a)}
			blend(row[i:i+4:i+4], pre)
		}
	}
}

// Shadow paints a soft shadow around r, whose corners are rounded to radius:
// a ring of black at each of the opacities given, outwards from r's edge, the
// rings dropped by drop pixels.
func (c *Canvas) Shadow(r image.Rectangle, radius, drop int, opacities []uint8) {
	for i, a := range opacities {
		c.StrokeRound(r.Inset(-1-i).Add(image.Pt(0, drop)), radius+i, 1, color.NRGBA{0, 0, 0, a})
	}
}

// coverage returns how much of the pixel centred at (px, py) lies inside the
// rectangle of half-size (hx, hy) centred at the origin, with corners rounded
// to radius r: 1 well inside, 0 well outside, and in between on the edge. An
// empty rectangle covers nothing.
func coverage(px, py, hx, hy, r float64) float64 {
	if hx <= 0 || hy <= 0 {
		return 0
	}
	qx, qy := math.Abs(px)-hx+r, math.Abs(py)-hy+r
	d := math.Hypot(max(qx, 0), max(qy, 0)) + min(max(qx, qy), 0) - r
	return max(min(0.5-d, 1), 0)
}

// DrawImage blends src over the canvas with its top-left corner at p. src
// must be premultiplied, as image.RGBA samples are.
func (c *Canvas) DrawImage(p image.Point, src *image.RGBA) {
	dst := src.Rect.Sub(src.Rect.Min).Add(p).Intersect(c.Clip())
	if dst.Empty() {
		return
	}
	off := dst.Min.Sub(p).Add(src.Rect.Min)
	for y := 0; y < dst.Dy(); y++ {
		s := src.Pix[src.PixOffset(off.X, off.Y+y):][:4*dst.Dx()]
		d := c.Img.Pix[c.Img.PixOffset(dst.Min.X, dst.Min.Y+y):][:4*dst.Dx()]
		for i := 0; i < len(s); i += 4 {
			blend(d[i:i+4:i+4], color.RGBA{s[i], s[i+1], s[i+2], s[i+3]})
		}
	}
}

// blend composites the premultiplied pixel src over d in place.
func blend(d []uint8, src color.RGBA) {
	if src.A == 255 {
		d[0], d[1], d[2], d[3] = src.R, src.G, src.B, 255
		return
	}
	inv := 255 - uint32(src.A)
	d[0] = uint8(uint32(src.R) + (uint32(d[0])*inv+127)/255)
	d[1] = uint8(uint32(src.G) + (uint32(d[1])*inv+127)/255)
	d[2] = uint8(uint32(src.B) + (uint32(d[2])*inv+127)/255)
	d[3] = uint8(uint32(src.A) + (uint32(d[3])*inv+127)/255)
}

// DrawScaled replaces the pixels of dst with src stretched to fill it, using
// nearest-neighbour sampling. src must be opaque, because its pixels replace
// the canvas's rather than blend over them. It is meant for showing a page
// rendered at a slightly different size than its slot.
func (c *Canvas) DrawScaled(dst image.Rectangle, src *image.RGBA) {
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	if dst.Empty() || sw == 0 || sh == 0 {
		return
	}
	clip := dst.Intersect(c.Clip())
	if clip.Empty() {
		return
	}
	if dst.Dx() == sw && dst.Dy() == sh {
		from := clip.Min.Sub(dst.Min).Add(src.Rect.Min)
		for y := 0; y < clip.Dy(); y++ {
			copy(c.Img.Pix[c.Img.PixOffset(clip.Min.X, clip.Min.Y+y):][:4*clip.Dx()], src.Pix[src.PixOffset(from.X, from.Y+y):])
		}
		return
	}
	// The source column of each destination column is the same on every row.
	cols := make([]int, clip.Dx())
	for i := range cols {
		cols[i] = 4 * ((clip.Min.X + i - dst.Min.X) * sw / dst.Dx())
	}
	for y := clip.Min.Y; y < clip.Max.Y; y++ {
		sy := src.Rect.Min.Y + (y-dst.Min.Y)*sh/dst.Dy()
		srow := src.Pix[src.PixOffset(src.Rect.Min.X, sy):]
		row := c.Img.Pix[c.Img.PixOffset(clip.Min.X, y):][:4*clip.Dx()]
		for i := 0; i < len(row); i += 4 {
			s := srow[cols[i/4] : cols[i/4]+4 : cols[i/4]+4]
			row[i], row[i+1], row[i+2], row[i+3] = s[0], s[1], s[2], s[3]
		}
	}
}
