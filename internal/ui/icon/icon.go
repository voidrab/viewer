// Package icon draws the viewer's toolbar icons, simple stroked outlines on a
// 24 by 24 grid rasterized with anti-aliasing at any size, and its logo.
package icon

import (
	"image"
	"image/color"
	"math"
	"slices"

	"golang.org/x/image/vector"

	"viewrab/internal/ui/paint"
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

// builder accumulates one path from lines, Bézier curves and arcs.
type builder struct{ path Path }

// at starts a path at (x, y).
func at(x, y float64) *builder { return &builder{Path{Pts: []Pt{{x, y}}}} }

// to adds a line to each point given, x then y.
func (b *builder) to(xy ...float64) *builder {
	for i := 0; i+1 < len(xy); i += 2 {
		b.path.Pts = append(b.path.Pts, Pt{xy[i], xy[i+1]})
	}
	return b
}

// curve adds a cubic Bézier curve with control points (x1, y1) and (x2, y2),
// ending at (x, y).
func (b *builder) curve(x1, y1, x2, y2, x, y float64) *builder {
	p0 := b.path.Pts[len(b.path.Pts)-1]
	const steps = 10
	for i := 1; i <= steps; i++ {
		t := float64(i) / steps
		u := 1 - t
		b.path.Pts = append(b.path.Pts, Pt{
			u*u*u*p0.X + 3*u*u*t*x1 + 3*u*t*t*x2 + t*t*t*x,
			u*u*u*p0.Y + 3*u*u*t*y1 + 3*u*t*t*y2 + t*t*t*y,
		})
	}
	return b
}

// arc adds the arc of the circle about (cx, cy) with radius r from angle a0
// to a1, in degrees clockwise from the positive x axis (y points down).
func (b *builder) arc(cx, cy, r, a0, a1 float64) *builder {
	steps := max(int(math.Abs(a1-a0)/10), 2)
	for i := 0; i <= steps; i++ {
		a := (a0 + (a1-a0)*float64(i)/float64(steps)) * math.Pi / 180
		b.path.Pts = append(b.path.Pts, Pt{cx + r*math.Cos(a), cy + r*math.Sin(a)})
	}
	return b
}

// closed finishes the path, joining its end back to its start.
func (b *builder) closed() Path { b.path.Closed = true; return b.path }

// rounded returns the open path through the given points with each inner
// corner rounded off to radius r, or less where a side is too short for it.
func rounded(r float64, xy ...float64) Path {
	pts := poly(false, xy...).Pts
	out := []Pt{pts[0]}
	for i := 1; i+1 < len(pts); i++ {
		a, p, c := pts[i-1], pts[i], pts[i+1]
		la, lc := math.Hypot(a.X-p.X, a.Y-p.Y), math.Hypot(c.X-p.X, c.Y-p.Y)
		k := min(r, la/2, lc/2)
		t1 := Pt{p.X + (a.X-p.X)*k/la, p.Y + (a.Y-p.Y)*k/la}
		t2 := Pt{p.X + (c.X-p.X)*k/lc, p.Y + (c.Y-p.Y)*k/lc}
		// A quadratic curve through the corner stands in for the arc.
		const steps = 6
		for j := 0; j <= steps; j++ {
			t := float64(j) / steps
			u := 1 - t
			out = append(out, Pt{u*u*t1.X + 2*u*t*p.X + t*t*t2.X, u*u*t1.Y + 2*u*t*p.Y + t*t*t2.Y})
		}
	}
	return Path{Pts: append(out, pts[len(pts)-1])}
}

// The toolbar icons, each drawn on the Grid square. They follow the Lucide
// icons the webview build uses (ISC licence).
var (
	// Open is an open folder.
	Open = Icon{rounded(2, 6, 14, 8.2, 10, 21.9, 10, 20.2, 20, 2, 20, 2, 3, 8.6, 3, 10.6, 6, 20, 6, 20, 10)}
	Prev = Icon{line(15, 18, 9, 12, 15, 6)}
	Next = Icon{line(9, 18, 15, 12, 9, 6)}
	Up   = Icon{line(6, 15, 12, 9, 18, 15)}
	Down = Icon{line(6, 9, 12, 15, 18, 9)}
	// Chevron is the small arrow that marks a drop-down.
	Chevron = Down
	Plus    = Icon{line(5, 12, 19, 12), line(12, 5, 12, 19)}
	Minus   = Icon{line(5, 12, 19, 12)}
	Close   = Icon{line(18, 6, 6, 18), line(6, 6, 18, 18)}
	Search  = Icon{circle(11, 11, 7), line(16, 16, 20, 20)}
	// Shield is a shield with a tick: PDF/A conformance.
	Shield = Icon{
		at(20, 13).curve(20, 18, 16.5, 20.5, 12.34, 21.95).to(11.67, 21.94).
			curve(7.5, 20.5, 4, 18, 4, 13).to(4, 6).curve(4, 5.45, 4.45, 5, 5, 5).
			curve(7, 5, 9.5, 3.8, 11.24, 2.28).curve(11.68, 1.9, 12.32, 1.9, 12.76, 2.28).
			curve(14.51, 3.81, 17, 5, 19, 5).curve(19.55, 5, 20, 5.45, 20, 6).closed(),
		line(9, 12, 11, 14, 15, 10),
	}
	// Sun and Moon switch to the light and the dark theme.
	Sun = Icon{
		circle(12, 12, 4),
		line(12, 2, 12, 4), line(12, 20, 12, 22), line(4.93, 4.93, 6.34, 6.34), line(17.66, 17.66, 19.07, 19.07),
		line(2, 12, 4, 12), line(20, 12, 22, 12), line(6.34, 17.66, 4.93, 19.07), line(17.66, 6.34, 19.07, 4.93),
	}
	Moon     = Icon{at(12, 3).arc(16.5, 7.5, 4.5*math.Sqrt2, -135, -315).arc(12, 12, 9, 0, 270).closed()}
	FitWidth = Icon{line(3, 5, 3, 19), line(21, 5, 21, 19), line(6, 12, 18, 12), line(9, 9, 6, 12, 9, 15), line(15, 9, 18, 12, 15, 15)}
	Menu     = Icon{line(4, 7, 20, 7), line(4, 12, 20, 12), line(4, 17, 20, 17)}
	Check    = Icon{line(5, 12.5, 10, 17.5, 19, 7.5)}
)

// The window controls: hairline glyphs, drawn smaller than the toolbar icons.
var (
	WinMinimize = Icon{line(6, 12, 18, 12)}
	WinMaximize = Icon{shape(6.5, 6.5, 17.5, 6.5, 17.5, 17.5, 6.5, 17.5)}
	// WinRestore is two overlapping windows, shown while maximised.
	WinRestore = Icon{shape(6, 9, 15, 9, 15, 18, 6, 18), line(9, 9, 9, 6, 18, 6, 18, 15, 15, 15)}
	WinClose   = Icon{line(7, 7, 17, 17), line(17, 7, 7, 17)}
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
