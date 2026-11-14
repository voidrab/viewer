// Package icon draws the viewer's toolbar icons: simple stroked outlines on a
// 24 by 24 grid, rasterized with anti-aliasing at any size.
package icon

import (
	"image"
	"image/color"
	"math"
	"slices"

	"golang.org/x/image/vector"

	"viewer/internal/ui/paint"
)

// Grid is the side of the square the icons are drawn on.
const Grid = 24

// Pt is a point on the grid.
type Pt struct{ X, Y float64 }

// Path is a polyline, closed back to its first point if Closed.
type Path struct {
	Pts    []Pt
	Closed bool
}

// Icon is a set of paths drawn together.
type Icon []Path

// line returns the open path through the given coordinates, x then y.
func line(xy ...float64) Path { return poly(false, xy...) }

// shape returns the closed path through the given coordinates, x then y.
func shape(xy ...float64) Path { return poly(true, xy...) }

func poly(closed bool, xy ...float64) Path {
	p := Path{Closed: closed}
	for i := 0; i+1 < len(xy); i += 2 {
		p.Pts = append(p.Pts, Pt{xy[i], xy[i+1]})
	}
	return p
}

// circle returns a closed path approximating a circle.
func circle(cx, cy, r float64) Path {
	const n = 28
	p := Path{Closed: true}
	for i := range n {
		a := 2 * math.Pi * float64(i) / n
		p.Pts = append(p.Pts, Pt{cx + r*math.Cos(a), cy + r*math.Sin(a)})
	}
	return p
}

// The toolbar icons, each drawn on the Grid square.
var (
	Open     = Icon{shape(3, 6, 9, 6, 11, 9, 21, 9, 21, 19, 3, 19)}
	Prev     = Icon{line(15, 5, 8, 12, 15, 19)}
	Next     = Icon{line(9, 5, 16, 12, 9, 19)}
	Up       = Icon{line(5, 15, 12, 8, 19, 15)}
	Down     = Icon{line(5, 9, 12, 16, 19, 9)}
	Plus     = Icon{line(5, 12, 19, 12), line(12, 5, 12, 19)}
	Minus    = Icon{line(5, 12, 19, 12)}
	Close    = Icon{line(6, 6, 18, 18), line(18, 6, 6, 18)}
	Search   = Icon{circle(10.5, 10.5, 6.5), line(15.5, 15.5, 21, 21)}
	Shield   = Icon{shape(12, 3, 20, 6, 20, 12, 12, 21, 4, 12, 4, 6), line(8.5, 12, 11, 14.5, 15.5, 9.5)}
	FitWidth = Icon{line(3, 5, 3, 19), line(21, 5, 21, 19), line(6, 12, 18, 12), line(9, 9, 6, 12, 9, 15), line(15, 9, 18, 12, 15, 15)}
	Menu     = Icon{line(4, 7, 20, 7), line(4, 12, 20, 12), line(4, 17, 20, 17)}
)

// Draw strokes ic, scaled to fill rect, in col with the given line width in
// grid units, within the canvas's clip.
func Draw(c *paint.Canvas, rect image.Rectangle, ic Icon, col color.NRGBA, width float64) {
	if rect.Empty() || col.A == 0 {
		return
	}
	w, h := rect.Dx(), rect.Dy()
	sx, sy := float64(w)/Grid, float64(h)/Grid
	var r vector.Rasterizer
	r.Reset(w, h)
	add := func(poly []Pt) {
		if !positive(poly) {
			slices.Reverse(poly)
		}
		for i, p := range poly {
			x, y := float32(p.X*sx), float32(p.Y*sy)
			if i == 0 {
				r.MoveTo(x, y)
			} else {
				r.LineTo(x, y)
			}
		}
		r.ClosePath()
	}
	for _, path := range ic {
		strokePath(path, width, add)
	}
	mask := image.NewRGBA(image.Rect(0, 0, w, h))
	r.Draw(mask, mask.Bounds(), image.NewUniform(col), image.Point{})
	c.DrawImage(rect.Min, mask)
}

// strokePath calls emit with the polygons whose union is the stroke of path:
// a rectangle for each segment and a disc at each point, which rounds the
// joins and caps.
func strokePath(path Path, width float64, emit func([]Pt)) {
	h := width / 2
	n := len(path.Pts)
	if n == 0 {
		return
	}
	segs := n - 1
	if path.Closed {
		segs = n
	}
	for i := range segs {
		a, b := path.Pts[i], path.Pts[(i+1)%n]
		dx, dy := b.X-a.X, b.Y-a.Y
		l := math.Hypot(dx, dy)
		if l == 0 {
			continue
		}
		nx, ny := -dy/l*h, dx/l*h
		emit([]Pt{{a.X + nx, a.Y + ny}, {b.X + nx, b.Y + ny}, {b.X - nx, b.Y - ny}, {a.X - nx, a.Y - ny}})
	}
	for _, p := range path.Pts {
		const steps = 12
		disc := make([]Pt, steps)
		for i := range disc {
			a := 2 * math.Pi * float64(i) / steps
			disc[i] = Pt{p.X + h*math.Cos(a), p.Y + h*math.Sin(a)}
		}
		emit(disc)
	}
}

// positive reports whether poly winds the positive way.
func positive(poly []Pt) bool {
	var area float64
	for i, p := range poly {
		q := poly[(i+1)%len(poly)]
		area += p.X*q.Y - q.X*p.Y
	}
	return area > 0
}
