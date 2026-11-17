package pageview

import (
	"image"
	"testing"

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
