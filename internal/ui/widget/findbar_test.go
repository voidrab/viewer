package widget

import (
	"image"
	"testing"

	"viewer/internal/ui/paint"
	"viewer/internal/ui/platform"
	"viewer/internal/ui/text"
	"viewer/internal/ui/theme"
)

func newFind(t *testing.T) *FindBar {
	t.Helper()
	face, err := text.Mono().Face(theme.FontSize, false)
	if err != nil {
		t.Fatal(err)
	}
	return NewFindBar(palette, face)
}

func typeText(b *FindBar, s string) {
	for _, r := range s {
		b.HandleKey(platform.KeyEvent{Down: true, Rune: r})
	}
}

func key(k platform.Key, mods platform.Modifiers) platform.KeyEvent {
	return platform.KeyEvent{Down: true, Key: k, Mods: mods}
}

func TestFindBarTyping(t *testing.T) {
	b := newFind(t)
	if act := b.HandleKey(platform.KeyEvent{Down: true, Rune: 'a'}); act != FindEdited {
		t.Errorf("typing = %v", act)
	}
	typeText(b, "bcd")
	if string(b.Text) != "abcd" || b.Cursor != 4 {
		t.Fatalf("text %q cursor %d", string(b.Text), b.Cursor)
	}
	b.HandleKey(key(platform.KeyLeft, 0))
	b.HandleKey(key(platform.KeyLeft, 0))
	typeText(b, "X") // in the middle
	if string(b.Text) != "abXcd" || b.Cursor != 3 {
		t.Errorf("insert in the middle: %q cursor %d", string(b.Text), b.Cursor)
	}
	if act := b.HandleKey(key(platform.KeyBackspace, 0)); act != FindEdited || string(b.Text) != "abcd" || b.Cursor != 2 {
		t.Errorf("backspace: %v %q %d", act, string(b.Text), b.Cursor)
	}
	if act := b.HandleKey(key(platform.KeyDelete, 0)); act != FindEdited || string(b.Text) != "abd" || b.Cursor != 2 {
		t.Errorf("delete: %v %q %d", act, string(b.Text), b.Cursor)
	}
	b.HandleKey(key(platform.KeyHome, 0))
	if act := b.HandleKey(key(platform.KeyBackspace, 0)); act != FindNone || b.Cursor != 0 {
		t.Errorf("backspace at the start: %v, cursor %d", act, b.Cursor)
	}
	b.HandleKey(key(platform.KeyLeft, 0)) // cannot go left of the start
	b.HandleKey(key(platform.KeyEnd, 0))
	if act := b.HandleKey(key(platform.KeyDelete, 0)); act != FindNone || b.Cursor != 3 {
		t.Errorf("delete at the end: %v, cursor %d", act, b.Cursor)
	}
	b.HandleKey(key(platform.KeyRight, 0)) // nor right of the end
	if b.Cursor != 3 {
		t.Errorf("cursor %d after Right at the end", b.Cursor)
	}
	for _, tc := range []struct {
		ev   platform.KeyEvent
		want FindAction
	}{
		{key(platform.KeyEnter, 0), FindNext},
		{key(platform.KeyEnter, platform.Shift), FindPrev},
		{key(platform.KeyEscape, 0), FindClose},
		{platform.KeyEvent{Key: platform.KeyEscape}, FindNone},                    // a release
		{platform.KeyEvent{Down: true, Rune: 'v', Mods: platform.Ctrl}, FindNone}, // a shortcut, not text
		{platform.KeyEvent{Down: true, Rune: 'x', Mods: platform.Alt}, FindNone},
		{platform.KeyEvent{Down: true, Rune: 0x7f}, FindNone}, // DEL
		{platform.KeyEvent{Down: true, Rune: '\t'}, FindNone}, // a control character
		{key(platform.KeyTab, 0), FindNone},
	} {
		if got := b.HandleKey(tc.ev); got != tc.want {
			t.Errorf("%+v = %v, want %v", tc.ev, got, tc.want)
		}
	}
	if string(b.Text) != "abd" {
		t.Errorf("text changed to %q by keys that are not text", string(b.Text))
	}
}

func TestFindBarPaste(t *testing.T) {
	b := newFind(t)
	b.SetText("ab")
	b.HandleKey(key(platform.KeyLeft, 0))
	if act := b.Paste("x\ny\n  z"); act != FindEdited || string(b.Text) != "ax y zb" || b.Cursor != 6 {
		t.Errorf("paste: %v %q %d", act, string(b.Text), b.Cursor)
	}
	b.SetText("")
	if act := b.Paste("tail \n"); act != FindEdited || string(b.Text) != "tail" {
		t.Errorf("paste with trailing white space: %v %q", act, string(b.Text))
	}
	b.SetText("ax y zb")
	if act := b.Paste(" \n "); act != FindNone || string(b.Text) != "ax y zb" {
		t.Errorf("pasting only white space: %v %q", act, string(b.Text))
	}
}

func TestFindBarGeometry(t *testing.T) {
	b := newFind(t)
	area := image.Rect(0, 100, 800, 500)
	r := b.Rect(area)
	if r.Dx() != findWidth || r.Dy() != findHeight || r.Max.X != area.Max.X-findMargin || r.Min.Y != area.Min.Y+findMargin {
		t.Errorf("rect = %v in %v", r, area)
	}
	if narrow := b.Rect(image.Rect(0, 0, 200, 100)); narrow.Dx() != 200-2*findMargin {
		t.Errorf("rect in a narrow area = %v", narrow)
	}
	// The buttons sit in a row at the right end, in order.
	prev, next, closeBtn := b.button(r, partPrev), b.button(r, partNext), b.button(r, partClose)
	if !(prev.Max.X <= next.Min.X && next.Max.X <= closeBtn.Min.X && closeBtn.Max.X <= r.Max.X) {
		t.Errorf("buttons overlap or leave the bar: %v %v %v in %v", prev, next, closeBtn, r)
	}
	// A bar too narrow for its parts still has a text area.
	if ta := b.textArea(image.Rect(0, 0, 60, findHeight)); ta.Dx() < 0 {
		t.Errorf("text area %v", ta)
	}
}

func TestFindBarCountText(t *testing.T) {
	b := newFind(t)
	for _, tc := range []struct {
		text      string
		cur, tot  int
		searching bool
		want      string
	}{
		{"", 0, 0, false, ""},
		{"x", 0, 0, true, "…"},
		{"x", 0, 0, false, "No results"},
		{"x", 3, 12, true, "3/12"},
	} {
		b.SetText(tc.text)
		b.Current, b.Total, b.Searching = tc.cur, tc.tot, tc.searching
		if got := b.countText(); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc, got, tc.want)
		}
	}
}

func TestFindBarDrawsTextCountAndCaret(t *testing.T) {
	b := newFind(t)
	area := image.Rect(0, 0, 600, 200)
	c := paint.New(600, 200)
	c.Fill(c.Bounds(), palette.Canvas)
	b.SetText("needle")
	b.Current, b.Total = 2, 9
	b.Draw(c, area)
	r := b.Rect(area)
	if got := c.Img.RGBAAt(r.Min.X+findPad+2, r.Min.Y+findHeight/2); got == nrgba(palette.Canvas) {
		t.Error("the bar was not drawn")
	}
	ta := b.textArea(r)
	if ink := inkIn(c, ta, palette.Text); ink.Empty() {
		t.Error("the query was not drawn")
	}
	if ink := inkIn(c, ta, palette.Accent); ink.Empty() || ink.Dx() != 1 {
		t.Errorf("the caret is %v, want a one-pixel line", ink)
	}
	if ink := inkIn(c, r, palette.Muted); ink.Empty() {
		t.Error("the count and buttons were not drawn")
	}
	// "No results" is drawn in the bad colour.
	b.Total = 0
	c.Fill(c.Bounds(), palette.Canvas)
	b.Draw(c, area)
	if ink := inkIn(c, r, palette.Bad); ink.Empty() {
		t.Error("no results was not drawn in the bad colour")
	}
	// A query longer than the field scrolls so that the caret stays in it.
	b.SetText("a very long query that cannot possibly fit in the field of the bar")
	c.Fill(c.Bounds(), palette.Canvas)
	b.Draw(c, area)
	if ink := inkIn(c, ta, palette.Accent); ink.Empty() || !ink.In(ta) {
		t.Errorf("the caret of a long query is at %v, outside the field %v", ink, ta)
	}
	// A hovered button is drawn in the hover colour.
	b.hover = partClose
	c.Fill(c.Bounds(), palette.Canvas)
	b.Draw(c, area)
	cb := b.button(r, partClose)
	if got := c.Img.RGBAAt(cb.Min.X+1, cb.Min.Y+1); got == nrgba(palette.Raised) {
		t.Error("a hovered button was not highlighted")
	}
}

func TestFindBarPointer(t *testing.T) {
	b := newFind(t)
	area := image.Rect(0, 0, 600, 200)
	r := b.Rect(area)
	b.SetText("hello")
	centre := func(rect image.Rectangle) (float64, float64) {
		return float64(rect.Min.X+rect.Max.X) / 2, float64(rect.Min.Y+rect.Max.Y) / 2
	}
	ev := func(kind platform.PointerKind, btn platform.Button, x, y float64) platform.PointerEvent {
		return platform.PointerEvent{Kind: kind, Button: btn, X: x, Y: y}
	}
	click := func(part findPart) (FindAction, bool) {
		x, y := centre(b.button(r, part))
		_, over := b.HandlePointer(ev(platform.Move, platform.None, x, y), area)
		if !over || b.hover != part {
			t.Fatalf("hover over %v: %v, %v", part, over, b.hover)
		}
		b.HandlePointer(ev(platform.Press, platform.Left, x, y), area)
		return b.HandlePointer(ev(platform.Release, platform.Left, x, y), area)
	}
	for part, want := range map[findPart]FindAction{partPrev: FindPrev, partNext: FindNext, partClose: FindClose} {
		if act, over := click(part); act != want || !over {
			t.Errorf("clicking part %v = %v, %v, want %v", part, act, over, want)
		}
	}
	// Pressing a button and releasing elsewhere does nothing.
	x, y := centre(b.button(r, partNext))
	b.HandlePointer(ev(platform.Press, platform.Left, x, y), area)
	if act, _ := b.HandlePointer(ev(platform.Release, platform.Left, float64(r.Min.X+findPad), y), area); act != FindNone {
		t.Errorf("press on a button, release on the field = %v", act)
	}
	// A press in the field puts the caret there: at the start, middle and end.
	ta := b.textArea(r)
	for _, tc := range []struct {
		x    int
		want int
	}{{ta.Min.X, 0}, {ta.Min.X + b.Face.Width("hel") + 1, 3}, {ta.Max.X - 1, 5}} {
		b.HandlePointer(ev(platform.Press, platform.Left, float64(tc.x), y), area)
		b.HandlePointer(ev(platform.Release, platform.Left, float64(tc.x), y), area)
		if b.Cursor != tc.want {
			t.Errorf("a press at x=%d put the caret at %d, want %d", tc.x, b.Cursor, tc.want)
		}
	}
	// Right-button presses on the bar are taken but do nothing; presses and
	// releases elsewhere are not on the bar at all.
	if act, over := b.HandlePointer(ev(platform.Press, platform.Right, x, y), area); act != FindNone || !over {
		t.Errorf("right press on the bar = %v, %v", act, over)
	}
	if _, over := b.HandlePointer(ev(platform.Press, platform.Left, 5, 150), area); over {
		t.Error("a press beside the bar was taken")
	}
	b.HandlePointer(ev(platform.Release, platform.Left, 5, 150), area)
	b.HandlePointer(ev(platform.Press, platform.Left, x, y), area)
	b.HandlePointer(ev(platform.Release, platform.Right, x, y), area) // another button's release does not click
	b.HandlePointer(ev(platform.Release, platform.Left, 5, 150), area)
	b.HandlePointer(ev(platform.Leave, platform.None, 0, 0), area)
	if b.hover != partNone {
		t.Error("the hover outlived the pointer")
	}
}
