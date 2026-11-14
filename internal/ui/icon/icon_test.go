package icon

import (
	"image"
	"image/color"
	"testing"

	"viewer/internal/ui/paint"
)

var black = color.NRGBA{0, 0, 0, 255}

func alphaAt(c *paint.Canvas, x, y int) uint8 { return c.Img.RGBAAt(x, y).A }

// inkRect returns the bounds of the painted pixels.
func inkRect(c *paint.Canvas) image.Rectangle {
	var r image.Rectangle
	b := c.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if alphaAt(c, x, y) != 0 {
				r = r.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return r
}

func TestEveryIconDrawsInsideItsBox(t *testing.T) {
	icons := map[string]Icon{
		"Open": Open, "Prev": Prev, "Next": Next, "Up": Up, "Down": Down, "Plus": Plus, "Minus": Minus,
		"Close": Close, "Search": Search, "Shield": Shield, "FitWidth": FitWidth, "Menu": Menu,
	}
	for name, ic := range icons {
		c := paint.New(60, 60)
		box := image.Rect(10, 10, 10+Grid, 10+Grid)
		Draw(c, box, ic, black, 2)
		ink := inkRect(c)
		if ink.Empty() {
			t.Errorf("%s drew nothing", name)
			continue
		}
		if !ink.In(box) {
			t.Errorf("%s ink %v spills outside its box %v", name, ink, box)
		}
		if max(ink.Dx(), ink.Dy()) < 12 || min(ink.Dx(), ink.Dy()) < 2 {
			t.Errorf("%s is only %v", name, ink)
		}
	}
}

func TestOverlappingStrokesStaySolid(t *testing.T) {
	// The crossing of the plus is covered by two lines and five discs: it must
	// be exactly as opaque as a lone line, not more and not wrapped around to less.
	c := paint.New(Grid, Grid)
	Draw(c, image.Rect(0, 0, Grid, Grid), Plus, black, 3)
	if a := alphaAt(c, 12, 12); a != 255 {
		t.Errorf("crossing alpha = %d, want 255", a)
	}
	if a := alphaAt(c, 8, 11); a != 255 { // inside the horizontal arm
		t.Errorf("arm alpha = %d, want 255", a)
	}
	// The colour is the requested one wherever it is covered.
	red := paint.New(Grid, Grid)
	Draw(red, image.Rect(0, 0, Grid, Grid), Plus, color.NRGBA{255, 0, 0, 255}, 3)
	if p := red.Img.RGBAAt(12, 12); p != (color.RGBA{255, 0, 0, 255}) {
		t.Errorf("colour = %v", p)
	}
	// A closed shape overlaps itself at every joint and stays solid too.
	s := paint.New(Grid, Grid)
	Draw(s, image.Rect(0, 0, Grid, Grid), Shield, black, 3)
	if a := alphaAt(s, 4, 8); a != 255 { // on the shield's left edge
		t.Errorf("shield edge alpha = %d", a)
	}
}

func TestPrevAndNextAreMirrorImages(t *testing.T) {
	prev, next := paint.New(Grid, Grid), paint.New(Grid, Grid)
	box := image.Rect(0, 0, Grid, Grid)
	Draw(prev, box, Prev, black, 2)
	Draw(next, box, Next, black, 2)
	for y := range Grid {
		for x := range Grid {
			a, b := int(alphaAt(prev, x, y)), int(alphaAt(next, Grid-1-x, y))
			if d := a - b; d > 2 || d < -2 {
				t.Fatalf("(%d,%d): prev %d, mirrored next %d", x, y, a, b)
			}
		}
	}
}

func TestDrawScalesToTheBoxAndKeepsAspect(t *testing.T) {
	small, big := paint.New(40, 40), paint.New(100, 100)
	Draw(small, image.Rect(0, 0, 12, 12), Search, black, 2)
	Draw(big, image.Rect(0, 0, 48, 48), Search, black, 2)
	if s, b := inkRect(small), inkRect(big); s.Dx() >= b.Dx() || s.Dx() > 12 || b.Dx() > 48 || b.Dx() < 30 {
		t.Errorf("small icon %v, big icon %v", s, b)
	}
	// A non-square box stretches the grid to fit it.
	wide := paint.New(100, 50)
	Draw(wide, image.Rect(0, 0, 96, 24), Minus, black, 2)
	if r := inkRect(wide); r.Dx() < 55 || r.Dy() > 6 {
		t.Errorf("wide minus ink = %v", r)
	}
	// Thicker lines cover more.
	thin, thick := paint.New(Grid, Grid), paint.New(Grid, Grid)
	Draw(thin, image.Rect(0, 0, Grid, Grid), Minus, black, 1)
	Draw(thick, image.Rect(0, 0, Grid, Grid), Minus, black, 4)
	if inkRect(thick).Dy() <= inkRect(thin).Dy() {
		t.Error("a wider stroke is not taller")
	}
}

func TestDrawHonoursClipAndSkipsWhatCannotShow(t *testing.T) {
	c := paint.New(60, 60)
	c.PushClip(image.Rect(0, 0, 20, 60))
	Draw(c, image.Rect(10, 10, 10+Grid, 10+Grid), Plus, black, 3)
	if ink := inkRect(c); ink.Empty() || ink.Max.X > 20 {
		t.Errorf("clipped ink = %v", ink)
	}
	c.PopClip()

	blank := paint.New(30, 30)
	Draw(blank, image.Rectangle{}, Plus, black, 2)
	Draw(blank, image.Rect(0, 0, 24, 24), Plus, color.NRGBA{0, 0, 0, 0}, 2)
	Draw(blank, image.Rect(0, 0, 24, 24), nil, black, 2)
	if !inkRect(blank).Empty() {
		t.Error("something was drawn")
	}
}

func TestDegeneratePaths(t *testing.T) {
	c := paint.New(Grid, Grid)
	box := image.Rect(0, 0, Grid, Grid)
	// No points, a repeated point (no segment, but a dot), a single point.
	Draw(c, box, Icon{{}, line(5, 5, 5, 5), line(12, 12)}, black, 3)
	ink := inkRect(c)
	if ink.Empty() || !image.Pt(12, 12).In(ink) || !image.Pt(5, 5).In(ink) {
		t.Errorf("dots missing: %v", ink)
	}
	if a := alphaAt(c, 12, 12); a < 200 {
		t.Errorf("a one-point path is not a solid dot: alpha %d", a)
	}
}
