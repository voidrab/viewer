package web

import (
	"testing"

	"viewrab/internal/ui/platform"
)

func TestMessageEvent(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    Message
		want platform.Event
	}{
		{"press", Message{Type: "pointerdown", Button: 2, X: 3, Y: 4, Ctrl: true},
			platform.PointerEvent{Kind: platform.Press, Button: platform.Right, X: 3, Y: 4, Mods: platform.Ctrl}},
		{"release", Message{Type: "pointerup", Button: 0, X: 1},
			platform.PointerEvent{Kind: platform.Release, Button: platform.Left, X: 1}},
		{"press of a button not taken", Message{Type: "pointerdown", Button: 3}, nil},
		{"move", Message{Type: "pointermove", X: 5, Y: 6, Shift: true},
			platform.PointerEvent{Kind: platform.Move, X: 5, Y: 6, Mods: platform.Shift}},
		{"enter", Message{Type: "pointerenter"}, platform.PointerEvent{Kind: platform.Enter}},
		{"leave", Message{Type: "pointerleave"}, platform.PointerEvent{Kind: platform.Leave}},
		{"wheel", Message{Type: "wheel", DY: 1, Mode: deltaPage, H: 500, X: 7, Y: 8},
			platform.ScrollEvent{DY: 100, X: 7, Y: 8}},
		{"key down", Message{Type: "keydown", Key: "f", Code: "KeyF", Ctrl: true},
			platform.KeyEvent{Rune: 'f', Down: true, Mods: platform.Ctrl}},
		{"key up", Message{Type: "keyup", Key: "Enter"}, platform.KeyEvent{Key: platform.KeyEnter}},
		{"key not reported", Message{Type: "keyup", Key: "a"}, nil},
		{"focus", Message{Type: "focus"}, platform.FocusEvent{Focused: true}},
		{"blur", Message{Type: "blur"}, platform.FocusEvent{}},
		{"drag enter", Message{Type: "dragenter", X: 1, Y: 2}, platform.DragEvent{Kind: platform.DragEnter, X: 1, Y: 2}},
		{"drag over", Message{Type: "dragover"}, platform.DragEvent{Kind: platform.DragMove}},
		{"drag leave", Message{Type: "dragleave"}, platform.DragEvent{Kind: platform.DragLeave}},
		{"resize", Message{Type: "resize", W: 800, H: 600},
			platform.ResizeEvent{Size: platform.Size{W: 800, H: 600}, Scale: 1}},
		{"pan", Message{Type: "pan", DX: 1.5, DY: -3, X: 3, Y: 4}, platform.PanEvent{DX: 1.5, DY: -3, X: 3, Y: 4}},
		{"pinch", Message{Type: "pinch", Factor: 1.1, X: 3, Y: 4}, platform.ZoomEvent{Factor: 1.1, X: 3, Y: 4}},
		{"pinch to nothing", Message{Type: "pinch"}, nil},
		{"resize on a dense display", Message{Type: "resize", W: 393, H: 852, Scale: 2.75},
			platform.ResizeEvent{Size: platform.Size{W: 393, H: 852}, Scale: 2.75}},
		{"resize to nothing", Message{Type: "resize", W: 0, H: 600}, nil},
		{"drop", Message{Type: "drop"}, nil},
		{"paste", Message{Type: "paste", Text: "x"}, nil},
		{"unknown", Message{Type: "click"}, nil},
	} {
		got, ok := tc.m.Event()
		if ok != (tc.want != nil) || got != tc.want && ok {
			t.Errorf("%s: Event = %#v, %v; want %#v", tc.name, got, ok, tc.want)
		}
	}
}
