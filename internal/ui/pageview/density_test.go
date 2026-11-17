package pageview

import (
	"image"
	"math"
	"testing"

	"viewrab/internal/ui/layout"
	"viewrab/internal/ui/paint"
	"viewrab/internal/ui/platform"
)

// TestPagesRenderAtTheDisplayDensity verifies that pages render at the zoom
// times the display's density, which is bounded by maxDensity, and fill
// their place on a scaled canvas.
func TestPagesRenderAtTheDisplayDensity(t *testing.T) {
	for _, c := range []struct {
		scale, want float64 // the display's density and the one rendered at
	}{{0, 1}, {1.5, 1.5}, {2, 2}, {3, 2}} {
		v, inv := newView(t, openDoc(t, 1), image.Pt(124, 200), Options{Gap: 12, Margin: 12})
		v.HandleEvent(platform.ResizeEvent{Size: platform.Size{W: 124, H: 200}, Scale: c.scale})
		if got, want := v.renderDPI(), dpiFor(v.lay.Scale()*c.want); got != want {
			t.Errorf("scale %v: renderDPI() = %d, want %d", c.scale, got, want)
		}
		canvas := paint.NewScaled(124, 200, max(c.scale, 1))
		v.Draw(canvas)
		waitInv(t, inv)
		v.Draw(canvas)
		r := canvas.Device(v.Layout().ViewRect(0))
		mid := r.Min.Add(r.Size().Div(2))
		if got := canvas.Img.RGBAAt(mid.X, mid.Y); got != red {
			t.Errorf("scale %v: the page's middle in device pixels is %v, want red", c.scale, got)
		}
	}
}

// TestDefaultZoomFitsNarrowViews verifies that a view too narrow for the
// default zoom's pages opens, and goes back on Ctrl+0, at fit width, and
// that a wide one keeps the default zoom.
func TestDefaultZoomFitsNarrowViews(t *testing.T) {
	// A page of 100 points at 150% is 200 pixels wide, 224 with the margins.
	for _, c := range []struct {
		width int
		mode  layout.ZoomMode
	}{{400, layout.ZoomFixed}, {150, layout.ZoomFitWidth}} {
		v, _ := newView(t, openDoc(t, 1), image.Pt(c.width, 300), Options{Gap: 12, Margin: 12, Zoom: DefaultZoom})
		if v.lay.Mode() != c.mode {
			t.Errorf("width %d: opened in mode %v, want %v", c.width, v.lay.Mode(), c.mode)
		}
		v.ZoomIn()
		v.HandleEvent(platform.KeyEvent{Rune: '0', Down: true, Mods: platform.Ctrl})
		if v.lay.Mode() != c.mode {
			t.Errorf("width %d: Ctrl+0 went to mode %v, want %v", c.width, v.lay.Mode(), c.mode)
		}
	}
}

// TestPinchZoomsAroundItsMiddle verifies that a pinch zooms by its factor,
// keeping the point between the fingers in place.
func TestPinchZoomsAroundItsMiddle(t *testing.T) {
	v, _ := newView(t, openDoc(t, 3), image.Pt(200, 300), Options{Gap: 12, Margin: 12, Zoom: ZoomPresets[4]})
	before := v.lay.Scale()
	at := image.Pt(100, 150)
	_, pt, _ := v.lay.HitPage(at)
	if !v.HandleEvent(platform.ZoomEvent{Factor: 1.5, X: float64(at.X), Y: float64(at.Y)}) {
		t.Fatal("a pinch asked for no redraw")
	}
	if got := v.lay.Scale(); math.Abs(got-before*1.5) > 1e-9 {
		t.Errorf("scale %v after a pinch of 1.5 from %v", got, before)
	}
	if _, after, _ := v.lay.HitPage(at); math.Abs(after.W-pt.W) > 1 || math.Abs(after.H-pt.H) > 1 {
		t.Errorf("the point under the fingers moved from %v to %v", pt, after)
	}
}
