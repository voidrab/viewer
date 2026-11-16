package web

import (
	"testing"

	"viewrab/internal/ui/platform"
)

func TestModifiers(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		shift, ctrl, alt, meta, altGraph bool
		want                             platform.Modifiers
	}{
		{name: "none"},
		{name: "all", shift: true, ctrl: true, alt: true, meta: true,
			want: platform.Shift | platform.Ctrl | platform.Alt | platform.Super},
		{name: "AltGr", ctrl: true, alt: true, altGraph: true},
		{name: "shifted AltGr", shift: true, ctrl: true, alt: true, altGraph: true, want: platform.Shift},
	} {
		if got := Modifiers(tc.shift, tc.ctrl, tc.alt, tc.meta, tc.altGraph); got != tc.want {
			t.Errorf("%s: Modifiers = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestKey(t *testing.T) {
	for _, tc := range []struct {
		name      string
		key, code string
		down      bool
		mods      platform.Modifiers
		want      platform.KeyEvent
		report    bool
	}{
		{name: "named down", key: "ArrowLeft", down: true, mods: platform.Shift,
			want: platform.KeyEvent{Key: platform.KeyLeft, Down: true, Mods: platform.Shift}, report: true},
		{name: "named up", key: "Escape",
			want: platform.KeyEvent{Key: platform.KeyEscape}, report: true},
		{name: "text", key: "é", down: true,
			want: platform.KeyEvent{Rune: 'é', Down: true}, report: true},
		{name: "shifted text", key: "A", down: true, mods: platform.Shift,
			want: platform.KeyEvent{Rune: 'A', Down: true, Mods: platform.Shift}, report: true},
		{name: "text released", key: "a"},
		{name: "unnamed key", key: "F5", down: true},
		{name: "dead key", key: "Dead", down: true},
		{name: "control character", key: "\x7f", down: true},
		{name: "invalid", key: "\xff", down: true},
		{name: "shortcut", key: "O", code: "KeyO", down: true, mods: platform.Ctrl,
			want: platform.KeyEvent{Rune: 'o', Down: true, Mods: platform.Ctrl}, report: true},
		{name: "shifted shortcut", key: "O", code: "KeyO", down: true, mods: platform.Ctrl | platform.Shift,
			want: platform.KeyEvent{Rune: 'O', Down: true, Mods: platform.Ctrl | platform.Shift}, report: true},
		{name: "Ctrl with a character the code has not", key: "é", code: "Quote", down: true, mods: platform.Ctrl | platform.Shift,
			want: platform.KeyEvent{Rune: 'é', Down: true, Mods: platform.Ctrl | platform.Shift}, report: true},
		{name: "browser's shortcut", key: "r", code: "KeyR", down: true, mods: platform.Ctrl,
			want: platform.KeyEvent{Rune: 'r', Down: true, Mods: platform.Ctrl}, report: true},
		{name: "paste left to the browser", key: "v", code: "KeyV", down: true, mods: platform.Ctrl,
			want: platform.KeyEvent{Rune: 'v', Down: true, Mods: platform.Ctrl}, report: true},
		{name: "non-Latin layout", key: "щ", code: "KeyO", down: true, mods: platform.Ctrl,
			want: platform.KeyEvent{Rune: 'o', Down: true, Mods: platform.Ctrl}, report: true},
		{name: "non-Latin digit row", key: "é", code: "Digit2", down: true, mods: platform.Ctrl,
			want: platform.KeyEvent{Rune: '2', Down: true, Mods: platform.Ctrl}, report: true},
		{name: "non-Latin without code", key: "щ", code: "Semicolon", down: true, mods: platform.Ctrl,
			want: platform.KeyEvent{Rune: 'щ', Down: true, Mods: platform.Ctrl}, report: true},
		{name: "Alt shortcut", key: "f", down: true, mods: platform.Alt},
		{name: "Super with Ctrl", key: "q", down: true, mods: platform.Ctrl | platform.Super},
		{name: "named with Alt", key: "Home", down: true, mods: platform.Alt,
			want: platform.KeyEvent{Key: platform.KeyHome, Down: true, Mods: platform.Alt}, report: true},
	} {
		got, report := Key(tc.key, tc.code, tc.down, tc.mods)
		if got != tc.want || report != tc.report {
			t.Errorf("%s: Key = %+v, %v; want %+v, %v", tc.name, got, report, tc.want, tc.report)
		}
	}
}

func TestCodeRune(t *testing.T) {
	for code, want := range map[string]rune{"KeyQ": 'q', "Digit0": '0'} {
		if got, ok := codeRune(code); !ok || got != want {
			t.Errorf("codeRune(%q) = %q, %v; want %q", code, got, ok, want)
		}
	}
	for _, code := range []string{"", "Key", "KeyAB", "Numpad1", "Space"} {
		if _, ok := codeRune(code); ok {
			t.Errorf("codeRune(%q) reports a rune", code)
		}
	}
}

func TestButton(t *testing.T) {
	for b, want := range map[int]platform.Button{0: platform.Left, 1: platform.Middle, 2: platform.Right} {
		if got, ok := Button(b); !ok || got != want {
			t.Errorf("Button(%d) = %v, %v; want %v", b, got, ok, want)
		}
	}
	for _, b := range []int{-1, 3, 4} {
		if got, ok := Button(b); ok || got != platform.None {
			t.Errorf("Button(%d) = %v, %v; want none", b, got, ok)
		}
	}
}

func TestWheel(t *testing.T) {
	for _, tc := range []struct {
		name           string
		dx, dy         float64
		mode           int
		page           float64
		wantDX, wantDY float64
	}{
		{name: "pixels: a notch", dy: 100, mode: deltaPixel, wantDY: 20},
		{name: "pixels: sideways", dx: -50, mode: deltaPixel, wantDX: -10},
		{name: "lines: a notch", dy: 3, mode: deltaLine, wantDY: 10},
		{name: "pages", dy: -1, mode: deltaPage, page: 800, wantDY: -160},
		{name: "unknown mode as pixels", dy: 10, mode: 7, wantDY: 2},
	} {
		dx, dy := Wheel(tc.dx, tc.dy, tc.mode, tc.page)
		if dx != tc.wantDX || dy != tc.wantDY {
			t.Errorf("%s: Wheel = %v, %v; want %v, %v", tc.name, dx, dy, tc.wantDX, tc.wantDY)
		}
	}
}
