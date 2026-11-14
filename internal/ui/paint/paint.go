// Package paint is a small software painter over an RGBA bitmap: filled and
// stroked rectangles, image blits and a clip stack. Text and icons build on it.
package paint

import (
	"image"
	"image/color"
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

// DrawScaled draws src stretched to fill dst, using nearest-neighbour
// sampling. src must be opaque: its pixels replace the canvas's, which is what
// a rendered page does, and a stretch of them is copied a row at a time. It is
// also meant for showing a page rendered at a slightly different size than its
// slot.
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
