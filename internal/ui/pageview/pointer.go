package pageview

import (
	"image"
	"image/color"

	"viewer/internal/ui/paint"
	"viewer/internal/ui/platform"
)

// Scrollbars are drawn over the pages: a thumb along the right edge, and along
// the bottom when the page is wider than the view, thin at rest and wide under
// the pointer. A press on a thumb drags it, a press on the track beside it
// centres the thumb there and drags it; any other press drags the page.
const (
	barThin  = 6  // width of a thumb at rest
	barWide  = 12 // width of a thumb, and of the strip that takes clicks
	barInset = 2  // gap between a thumb at rest and the view's edge
	minThumb = 28 // shortest thumb, so that it can still be grabbed
)

// axis is a scrollbar direction.
type axis uint8

const (
	noAxis axis = iota
	vertical
	horizontal
)

// pointerState is what the pointer is doing in the view.
type pointerState struct {
	hover axis        // the scrollbar under the pointer
	drag  axis        // the scrollbar being dragged
	grab  int         // where in the thumb it was taken, from the thumb's start
	pan   bool        // the page is being dragged
	last  image.Point // where the pointer was last
}

// bar is the geometry of one scrollbar along its axis.
type bar struct {
	strip         image.Rectangle // where clicks count
	start, length int             // the thumb, from the top or left of the view
	span          int             // the room the thumb moves in, the track's length
	max           int             // the largest scroll offset along the axis
}

// bar returns the scrollbar along a, or false when the page fits the view
// along it and there is none.
func (v *View) bar(a axis) (bar, bool) {
	vp, doc, scroll := v.lay.Viewport(), v.lay.DocumentSize(), v.lay.Scroll()
	needV, needH := doc.Y > vp.Y, doc.X > vp.X
	var size, total, offset int
	var b bar
	switch a {
	case vertical:
		if !needV {
			return bar{}, false
		}
		size, total, offset = vp.Y, doc.Y, scroll.Y
		b.span = vp.Y
		if needH {
			b.span -= barWide // leave the corner to the other bar
		}
		b.strip = image.Rect(vp.X-barWide, 0, vp.X, b.span)
	default:
		if !needH {
			return bar{}, false
		}
		size, total, offset = vp.X, doc.X, scroll.X
		b.span = vp.X
		if needV {
			b.span -= barWide
		}
		b.strip = image.Rect(0, vp.Y-barWide, b.span, vp.Y)
	}
	b.max = total - size
	b.length = min(max(b.span*size/total, minThumb), b.span)
	b.start = (b.span - b.length) * offset / b.max
	return b, true
}

// barAt returns the scrollbar whose strip holds p.
func (v *View) barAt(p image.Point) axis {
	for _, a := range []axis{vertical, horizontal} {
		if b, ok := v.bar(a); ok && p.In(b.strip) {
			return a
		}
	}
	return noAxis
}

// along returns p's coordinate along a.
func along(a axis, p image.Point) int {
	if a == vertical {
		return p.Y
	}
	return p.X
}

// HandlePointer applies a pointer event, in the viewport's coordinates, and
// reports whether the view needs redrawing.
func (v *View) HandlePointer(ev platform.PointerEvent) bool {
	p := image.Pt(int(ev.X), int(ev.Y))
	s := &v.ptr
	switch ev.Kind {
	case platform.Leave:
		changed := s.hover != noAxis
		s.hover = noAxis
		return changed
	case platform.Press:
		if ev.Button != platform.Left {
			return false
		}
		if a := v.barAt(p); a != noAxis {
			b, _ := v.bar(a)
			s.hover, s.drag, s.grab = a, a, along(a, p)-b.start
			if s.grab < 0 || s.grab >= b.length { // beside the thumb: centre it
				s.grab = b.length / 2
			}
			v.dragBar(p)
			return true
		}
		s.pan, s.last = true, p
		return false
	case platform.Release:
		if ev.Button != platform.Left {
			return false
		}
		changed := s.drag != noAxis
		s.drag, s.pan = noAxis, false
		if h := v.barAt(p); h != s.hover {
			s.hover, changed = h, true
		}
		return changed
	}
	// Moves, and the pointer entering.
	switch {
	case s.drag != noAxis:
		v.dragBar(p)
		return true
	case s.pan:
		before := v.lay.Scroll()
		v.lay.ScrollBy(s.last.Sub(p))
		s.last = p
		return v.lay.Scroll() != before
	}
	h := v.barAt(p)
	changed := h != s.hover
	s.hover = h
	return changed
}

// dragBar scrolls so that the thumb being dragged follows the pointer at p.
func (v *View) dragBar(p image.Point) {
	s := &v.ptr
	b, ok := v.bar(s.drag)
	if !ok {
		return
	}
	room := b.span - b.length
	start := min(max(along(s.drag, p)-s.grab, 0), room)
	offset := 0
	if room > 0 {
		offset = (start*b.max + room/2) / room
	}
	scroll := v.lay.Scroll()
	if s.drag == vertical {
		scroll.Y = offset
	} else {
		scroll.X = offset
	}
	v.lay.ScrollTo(scroll)
}

// drawBars paints the scrollbars over the view, whose top-left corner is at
// origin of c.
func (v *View) drawBars(c *paint.Canvas, origin image.Point) {
	for _, a := range []axis{vertical, horizontal} {
		b, ok := v.bar(a)
		if !ok {
			continue
		}
		active := v.ptr.hover == a || v.ptr.drag == a
		width, alpha := barThin, uint8(150)
		if active {
			width, alpha = barWide-barInset, 220
			c.Fill(b.strip.Add(origin), v.faint())
		}
		thumb := image.Rect(b.strip.Max.X-barInset-width, b.start, b.strip.Max.X-barInset, b.start+b.length)
		if a == horizontal {
			thumb = image.Rect(b.start, b.strip.Max.Y-barInset-width, b.start+b.length, b.strip.Max.Y-barInset)
		}
		col := v.opts.Scrollbar
		col.A = alpha
		c.Fill(thumb.Add(origin), col)
	}
}

// faint is the colour of the track under an active thumb.
func (v *View) faint() color.NRGBA {
	col := v.opts.Scrollbar
	col.A = 50
	return col
}
