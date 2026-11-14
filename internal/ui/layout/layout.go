// Package layout places the pages of a document in one vertical scroll
// column and maps between document, page and viewport coordinates. It is pure
// geometry: no rendering and no windowing.
package layout

import (
	"cmp"
	"image"
	"math"
	"sort"
)

// ZoomMode picks how the scale follows the viewport.
type ZoomMode uint8

// Zoom modes.
const (
	// ZoomFixed uses Layout.Scale as given.
	ZoomFixed ZoomMode = iota
	// ZoomFitWidth scales the widest page to the viewport width.
	ZoomFitWidth
	// ZoomFitPage scales the current page to fit the viewport entirely.
	ZoomFitPage
)

// Scale limits, in device pixels per point.
const (
	MinScale = 0.1
	MaxScale = 8
)

// Size is a page's displayed size in points.
type Size struct{ W, H float64 }

// Layout is a column of pages at a scale, shown through a viewport.
type Layout struct {
	pages    []Size
	viewport image.Point
	gap      int
	margin   int

	mode   ZoomMode
	scale  float64
	scroll image.Point // top-left of the viewport in document pixels

	tops  []int // document y of each page's top edge
	width int   // widest page in pixels
	total int   // document height in pixels
}

// New returns a layout of pages with gap pixels between them and margin
// pixels around the column, shown at fit-width zoom through viewport.
func New(pages []Size, viewport image.Point, gap, margin int) *Layout {
	l := &Layout{pages: pages, viewport: viewport, gap: gap, margin: margin, mode: ZoomFitWidth, scale: 1}
	l.relayout()
	return l
}

// Viewport returns the size of the viewport.
func (l *Layout) Viewport() image.Point { return l.viewport }

// Pages returns the number of pages.
func (l *Layout) Pages() int { return len(l.pages) }

// Mode returns the zoom mode.
func (l *Layout) Mode() ZoomMode { return l.mode }

// Scale returns the current device pixels per point.
func (l *Layout) Scale() float64 { return l.scale }

// Scroll returns the document position at the viewport's top-left corner.
func (l *Layout) Scroll() image.Point { return l.scroll }

// DocumentSize returns the size of the whole column in pixels.
func (l *Layout) DocumentSize() image.Point { return image.Pt(l.width+2*l.margin, l.total) }

// SetViewport resizes the viewport, keeping the top of the view anchored.
func (l *Layout) SetViewport(w, h int) {
	anchor := l.anchor()
	l.viewport = image.Pt(w, h)
	l.relayout()
	l.restore(anchor)
}

// SetZoom sets a fixed scale, clamped to the limits, anchored at the view's top.
func (l *Layout) SetZoom(scale float64) {
	anchor := l.anchor()
	l.mode = ZoomFixed
	l.scale = clamp(scale, MinScale, MaxScale)
	l.relayout()
	l.restore(anchor)
}

// SetMode selects fit-width or fit-page zoom (or keeps the scale for
// ZoomFixed), anchored at the view's top.
func (l *Layout) SetMode(mode ZoomMode) {
	anchor := l.anchor()
	l.mode = mode
	l.relayout()
	l.restore(anchor)
}

// ScrollBy moves the view by d pixels, clamped to the document.
func (l *Layout) ScrollBy(d image.Point) { l.ScrollTo(l.scroll.Add(d)) }

// ScrollTo puts p at the viewport's top-left corner, clamped to the document.
func (l *Layout) ScrollTo(p image.Point) {
	maxX := max(0, l.width+2*l.margin-l.viewport.X)
	maxY := max(0, l.total-l.viewport.Y)
	l.scroll = image.Pt(clamp(p.X, 0, maxX), clamp(p.Y, 0, maxY))
}

// PageRect returns page i's rectangle in document pixels. Pages are centred
// horizontally in the column, or in the viewport when narrower than it.
func (l *Layout) PageRect(i int) image.Rectangle {
	s := l.pixelSize(i)
	col := max(l.width+2*l.margin, l.viewport.X)
	x := (col - s.X) / 2
	return image.Rect(x, l.tops[i], x+s.X, l.tops[i]+s.Y)
}

// ViewRect returns page i's rectangle in viewport pixels.
func (l *Layout) ViewRect(i int) image.Rectangle { return l.PageRect(i).Sub(l.scroll) }

// Visible returns the half-open range [first, last) of pages that intersect
// the viewport, or an empty range for an empty document.
func (l *Layout) Visible() (first, last int) {
	if len(l.pages) == 0 {
		return 0, 0
	}
	top, bottom := l.scroll.Y, l.scroll.Y+l.viewport.Y
	first = sort.Search(len(l.pages), func(i int) bool { return l.tops[i]+l.pixelSize(i).Y > top })
	last = sort.Search(len(l.pages), func(i int) bool { return l.tops[i] >= bottom })
	return first, max(first, last)
}

// Current returns the page that holds the middle of the viewport, which is
// the one a page counter should show.
func (l *Layout) Current() int {
	if len(l.pages) == 0 {
		return 0
	}
	mid := l.scroll.Y + l.viewport.Y/2
	i := sort.Search(len(l.pages), func(i int) bool { return l.tops[i] > mid }) - 1
	return clamp(i, 0, len(l.pages)-1)
}

// GoTo scrolls so that page i's top edge lies gap pixels below the top of the
// view, clamped to the document.
func (l *Layout) GoTo(i int) {
	if len(l.pages) == 0 {
		return
	}
	l.ScrollTo(image.Pt(l.scroll.X, l.tops[clamp(i, 0, len(l.pages)-1)]-l.gap))
}

// HitPage returns the page under viewport point p and the point's position on
// that page in points, or ok == false over the background.
func (l *Layout) HitPage(p image.Point) (page int, pt Size, ok bool) {
	first, last := l.Visible()
	for i := first; i < last; i++ {
		r := l.ViewRect(i)
		if p.In(r) {
			return i, Size{float64(p.X-r.Min.X) / l.scale, float64(p.Y-r.Min.Y) / l.scale}, true
		}
	}
	return 0, Size{}, false
}

// anchor is a position inside the document that survives a relayout: a page
// and the fraction of its height the view's top edge is at.
type anchor struct {
	page int
	frac float64
	x    float64
}

func (l *Layout) anchor() anchor {
	if len(l.pages) == 0 {
		return anchor{}
	}
	i := sort.Search(len(l.pages), func(i int) bool { return l.tops[i] > l.scroll.Y }) - 1
	i = max(i, 0)
	h := max(l.pixelSize(i).Y, 1)
	return anchor{page: i, frac: float64(l.scroll.Y-l.tops[i]) / float64(h), x: l.xFrac()}
}

func (l *Layout) restore(a anchor) {
	if len(l.pages) == 0 {
		l.scroll = image.Point{}
		return
	}
	y := l.tops[a.page] + int(math.Round(a.frac*float64(l.pixelSize(a.page).Y)))
	l.ScrollTo(image.Pt(int(math.Round(a.x*float64(max(0, l.width+2*l.margin-l.viewport.X)))), y))
}

// xFrac is the horizontal scroll as a fraction of the scrollable width.
func (l *Layout) xFrac() float64 {
	span := l.width + 2*l.margin - l.viewport.X
	if span <= 0 {
		return 0
	}
	return float64(l.scroll.X) / float64(span)
}

func (l *Layout) pixelSize(i int) image.Point {
	s := l.pages[i]
	return image.Pt(max(1, int(math.Round(s.W*l.scale))), max(1, int(math.Round(s.H*l.scale))))
}

// relayout recomputes the scale for fit modes and the page positions.
func (l *Layout) relayout() {
	if len(l.pages) > 0 {
		avail := float64(l.viewport.X - 2*l.margin)
		switch l.mode {
		case ZoomFitWidth:
			var w float64
			for _, p := range l.pages {
				w = max(w, p.W)
			}
			l.scale = fit(avail / w)
		case ZoomFitPage:
			p := l.pages[l.Current()]
			l.scale = fit(math.Min(avail/p.W, float64(l.viewport.Y-2*l.margin)/p.H))
		}
	}
	l.tops = l.tops[:0]
	l.width = 0
	y := l.margin
	for i := range l.pages {
		s := l.pixelSize(i)
		l.tops = append(l.tops, y)
		y += s.Y + l.gap
		l.width = max(l.width, s.X)
	}
	l.total = max(0, y-l.gap) + l.margin
	if len(l.pages) == 0 {
		l.total = 0
	}
	l.ScrollTo(l.scroll)
}

// fit clamps a fit scale to the range [MinScale, MaxScale]. A NaN, or a value
// below MinScale such as the one a non-positive viewport yields, becomes
// MinScale.
func fit(s float64) float64 {
	if math.IsNaN(s) || s < MinScale {
		return MinScale
	}
	return math.Min(s, MaxScale)
}

// clamp limits v to [lo, hi].
func clamp[T cmp.Ordered](v, lo, hi T) T { return max(lo, min(hi, v)) }
