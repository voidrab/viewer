package theme

import (
	"image/color"
	"testing"
)

func TestPalettes(t *testing.T) {
	light, dark := Light(), Dark()
	if light.Chrome != (color.NRGBA{0xf3, 0xf3, 0xf4, 255}) || light.Accent != (color.NRGBA{0x3b, 0x72, 0xd9, 255}) {
		t.Errorf("light palette: chrome %v accent %v", light.Chrome, light.Accent)
	}
	if dark.Canvas != (color.NRGBA{0x12, 0x13, 0x15, 255}) || dark.Text != (color.NRGBA{0xe2, 0xe4, 0xe9, 255}) {
		t.Errorf("dark palette: canvas %v text %v", dark.Canvas, dark.Text)
	}
	// Every colour is set, and the washes are translucent.
	for name, p := range map[string]Palette{"light": light, "dark": dark} {
		for field, c := range map[string]color.NRGBA{
			"Chrome": p.Chrome, "Canvas": p.Canvas, "Raised": p.Raised, "Border": p.Border, "Text": p.Text,
			"Muted": p.Muted, "Faint": p.Faint, "Accent": p.Accent, "OK": p.OK, "Bad": p.Bad, "Warn": p.Warn,
		} {
			if c.A != 255 {
				t.Errorf("%s %s = %v, want opaque", name, field, c)
			}
		}
		for field, c := range map[string]color.NRGBA{"Hover": p.Hover, "Active": p.Active, "Selection": p.Selection} {
			if c.A == 0 || c.A == 255 {
				t.Errorf("%s %s = %v, want translucent", name, field, c)
			}
		}
	}
	// Light text is darker than the chrome, and dark text is lighter, when
	// their RGB channels are summed.
	lum := func(c color.NRGBA) int { return int(c.R) + int(c.G) + int(c.B) }
	if lum(light.Text) >= lum(light.Chrome) || lum(dark.Text) <= lum(dark.Chrome) {
		t.Error("text does not contrast with the chrome")
	}
}
