package platform

import "testing"

func TestCursorCSSName(t *testing.T) {
	for c, want := range map[Cursor]string{
		CursorDefault: "default", CursorPointer: "pointer",
		CursorResizeSE: "se-resize", Cursor(200): "default",
	} {
		if got := c.CSSName(); got != want {
			t.Errorf("Cursor(%d).CSSName() = %q, want %q", c, got, want)
		}
	}
	for c := CursorResizeN; c <= CursorPointer; c++ {
		if c.CSSName() == "default" {
			t.Errorf("Cursor(%d) has no CSS name", c)
		}
	}
}
