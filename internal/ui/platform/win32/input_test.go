package win32

import (
	"slices"
	"testing"

	"viewrab/internal/ui/platform"
)

func TestModifiers(t *testing.T) {
	held := func(vks ...int) func(int) bool {
		return func(vk int) bool { return slices.Contains(vks, vk) }
	}
	cases := []struct {
		vks  []int
		want platform.Modifiers
	}{
		{nil, 0},
		{[]int{vkShift}, platform.Shift},
		{[]int{vkControl, vkMenu}, platform.Ctrl | platform.Alt},
		{[]int{vkLWin}, platform.Super},
		{[]int{vkRWin, vkShift}, platform.Super | platform.Shift},
	}
	for _, c := range cases {
		if got := modifiers(held(c.vks...)); got != c.want {
			t.Errorf("modifiers(%v) = %v, want %v", c.vks, got, c.want)
		}
	}
}

func TestKeyDown(t *testing.T) {
	cases := []struct {
		name      string
		vk, char  uintptr
		mods      platform.Modifiers
		want      platform.KeyEvent
		reporting bool
	}{
		{"navigation key", vkLeft, 0, platform.Shift, platform.KeyEvent{Key: platform.KeyLeft, Down: true, Mods: platform.Shift}, true},
		{"keypad enter", vkSeparate, 0, 0, platform.KeyEvent{Key: platform.KeyEnter, Down: true}, true},
		{"ctrl+letter in lower case", 'A', 'A', platform.Ctrl, platform.KeyEvent{Rune: 'a', Down: true, Mods: platform.Ctrl}, true},
		{"ctrl+shift+letter in upper case", 'A', 'A', platform.Ctrl | platform.Shift, platform.KeyEvent{Rune: 'A', Down: true, Mods: platform.Ctrl | platform.Shift}, true},
		{"ctrl+symbol", 0xbb, '=', platform.Ctrl, platform.KeyEvent{Rune: '=', Down: true, Mods: platform.Ctrl}, true},
		{"ctrl+dead key", 0xba, 0x80000000 | '^', platform.Ctrl, platform.KeyEvent{Rune: '^', Down: true, Mods: platform.Ctrl}, true},
		{"letter without ctrl waits for WM_CHAR", 'A', 'A', 0, platform.KeyEvent{}, false},
		{"altgr waits for WM_CHAR", 'Q', 'Q', platform.Ctrl | platform.Alt, platform.KeyEvent{}, false},
		{"ctrl alone types nothing", vkControl, 0, platform.Ctrl, platform.KeyEvent{}, false},
	}
	for _, c := range cases {
		got, ok := keyDown(c.vk, c.char, c.mods)
		if ok != c.reporting || got != c.want {
			t.Errorf("%s: keyDown = %+v, %v; want %+v, %v", c.name, got, ok, c.want, c.reporting)
		}
	}
}

func TestKeyUpReportsOnlyNavigationKeys(t *testing.T) {
	if got, ok := keyUp(vkEscape, platform.Ctrl); !ok || got != (platform.KeyEvent{Key: platform.KeyEscape, Mods: platform.Ctrl}) {
		t.Errorf("keyUp(Escape) = %+v, %v", got, ok)
	}
	if _, ok := keyUp('A', 0); ok {
		t.Error("the release of a letter is reported")
	}
}

func TestKeyChar(t *testing.T) {
	var c chars
	cases := []struct {
		name      string
		unit      uint16
		mods      platform.Modifiers
		want      platform.KeyEvent
		reporting bool
	}{
		{"letter", 'x', platform.Shift, platform.KeyEvent{Rune: 'x', Down: true, Mods: platform.Shift}, true},
		{"altgr types text", '@', platform.Ctrl | platform.Alt, platform.KeyEvent{Rune: '@', Down: true}, true},
		{"control character", 0x01, platform.Ctrl, platform.KeyEvent{}, false},
		{"ctrl alone", '=', platform.Ctrl, platform.KeyEvent{}, false},
		{"enter", '\r', 0, platform.KeyEvent{}, false},
		{"delete", 0x7f, 0, platform.KeyEvent{}, false},
		{"high surrogate waits", 0xd83d, 0, platform.KeyEvent{}, false},
		{"low surrogate completes", 0xde00, 0, platform.KeyEvent{Rune: '😀', Down: true}, true},
		{"lone low surrogate", 0xde00, 0, platform.KeyEvent{}, false},
	}
	for _, tc := range cases {
		got, ok := c.keyChar(tc.unit, tc.mods)
		if ok != tc.reporting || got != tc.want {
			t.Errorf("%s: keyChar = %+v, %v; want %+v, %v", tc.name, got, ok, tc.want, tc.reporting)
		}
	}
}

func TestWheel(t *testing.T) {
	if dx, dy := wheel(-wheelDelta, false); dx != 0 || dy != scrollStep {
		t.Errorf("a notch towards the user scrolls (%v, %v), want (0, %v)", dx, dy, scrollStep)
	}
	if dx, dy := wheel(wheelDelta/2, true); dx != scrollStep/2 || dy != 0 {
		t.Errorf("half a notch right scrolls (%v, %v), want (%v, 0)", dx, dy, scrollStep/2)
	}
}

func TestEdgeHitsCoverEverySideAndCorner(t *testing.T) {
	for edges := range platform.Edge(16) {
		_, ok := edgeHits[edges]
		if want := platform.ResizeCursor(edges) != platform.CursorDefault; ok != want {
			t.Errorf("edges %04b: has a hit-test code %v, want %v", edges, ok, want)
		}
	}
}

func TestCursorID(t *testing.T) {
	for c := platform.CursorDefault; c <= platform.CursorPointer; c++ {
		if _, ok := cursorIDs[c]; !ok {
			t.Errorf("cursor %d has no system cursor", c)
		}
	}
	if got := cursorID(platform.CursorResizeNE); got != idcSizeNESW {
		t.Errorf("cursorID(ResizeNE) = %d, want %d", got, idcSizeNESW)
	}
	if got := cursorID(platform.Cursor(200)); got != idcArrow {
		t.Errorf("an unknown cursor shows %d, want the arrow", got)
	}
}

func TestClipboardTextRoundTrips(t *testing.T) {
	units := clipboardText("a\nb\r\nc😀")
	want := []uint16{'a', '\r', '\n', 'b', '\r', '\n', 'c', 0xd83d, 0xde00, 0}
	if len(units) != len(want) {
		t.Fatalf("clipboardText = %v, want %v", units, want)
	}
	for i := range want {
		if units[i] != want[i] {
			t.Fatalf("clipboardText = %v, want %v", units, want)
		}
	}
	if got := pastedText(append(units, 'x')); got != "a\nb\nc😀" {
		t.Errorf("pastedText = %q", got)
	}
	if got := pastedText([]uint16{'h', 'i'}); got != "hi" {
		t.Errorf("pastedText without a NUL = %q", got)
	}
}
