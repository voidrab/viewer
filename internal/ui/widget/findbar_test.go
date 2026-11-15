package widget

import (
	"image"
	"testing"

	"viewrab/internal/ui/paint"
	"viewrab/internal/ui/platform"
	"viewrab/internal/ui/text"
	"viewrab/internal/ui/theme"
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
		{key(platform.KeyLeft, 0), FindNone}, // caret movement changes no text
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

// findRect is where the tests put the field.
var findRect = image.Rect(300, 5, 300+FindWidth, 31)

func TestFindBarGeometry(t *testing.T) {
	b := newFind(t)
	r := findRect
	// Closed, or open with no query, the field has no buttons and the text
	// takes the room right of the icon.
	if ta := b.textArea(r); ta.Min.X <= r.Min.X+findIcon || ta.Max.X != r.Max.X-findPad {
		t.Errorf("text area %v in %v", ta, r)
	}
	if b.partAt(r, image.Pt(r.Max.X-5, r.Min.Y+13)) != partText {
		t.Error("a closed field has buttons")
	}
	// During a search the buttons sit in a row at the right end, in order,
	// with the count and then the text left of them.
	b.Open = true
	b.SetText("x")
	b.Current, b.Total = 1, 3
	prev, next := b.button(r, partPrev), b.button(r, partNext)
	if !(prev.Max.X <= next.Min.X && next.Max.X <= r.Max.X) {
		t.Errorf("buttons overlap or leave the field: %v %v in %v", prev, next, r)
	}
	if ta := b.textArea(r); ta.Max.X > prev.Min.X-b.Face.Width("1 / 3") {
		t.Errorf("text area %v runs into the count", ta)
	}
	// A field too narrow for its parts still has a text area.
	if ta := b.textArea(image.Rect(0, 0, 40, 26)); ta.Dx() < 0 {
		t.Errorf("text area %v", ta)
	}
}

func TestFindBarCountText(t *testing.T) {
	b := newFind(t)
	for _, tc := range []struct {
		open      bool
		text      string
		cur, tot  int
		searching bool
		want      string
	}{
		{true, "", 0, 0, false, ""},
		{false, "x", 3, 12, false, ""},
		{true, "x", 0, 0, true, "…"},
		{true, "x", 0, 0, false, "No matches"},
		{true, "x", 3, 12, true, "3 / 12"},
	} {
		b.Open = tc.open
		b.SetText(tc.text)
		b.Current, b.Total, b.Searching = tc.cur, tc.tot, tc.searching
		if got := b.countText(); got != tc.want {
			t.Errorf("%+v: %q, want %q", tc, got, tc.want)
		}
	}
}

func TestFindBarDrawsPlaceholderTextCountAndCaret(t *testing.T) {
	b := newFind(t)
	c := paint.New(600, 40)
	r := findRect
	// Closed and empty: the icon and a faint placeholder, on the chrome.
	c.Fill(c.Bounds(), palette.Chrome)
	b.Draw(c, r)
	if ink := inkIn(c, b.textArea(r), palette.Faint); ink.Empty() {
		t.Error("the placeholder was not drawn")
	}
	if got := c.Img.RGBAAt(r.Min.X+2, r.Min.Y+13); got != nrgba(palette.Chrome) {
		t.Error("a closed field has a background")
	}
	// Hovered, it is washed.
	b.hover = partText
	b.Draw(c, r)
	if got := c.Img.RGBAAt(r.Min.X+2, r.Min.Y+13); got == nrgba(palette.Chrome) {
		t.Error("a hovered field was not highlighted")
	}
	b.hover = partNone

	b.Open = true
	b.SetText("needle")
	b.Current, b.Total = 2, 9
	c.Fill(c.Bounds(), palette.Chrome)
	b.Draw(c, r)
	if got := c.Img.RGBAAt(r.Min.X+findPad+findIcon+2, r.Min.Y+4); got != nrgba(palette.Raised) {
		t.Errorf("an open field is %v, want the raised colour", got)
	}
	ta := b.textArea(r)
	if ink := inkIn(c, ta, palette.Text); ink.Empty() {
		t.Error("the query was not drawn")
	}
	if ink := inkIn(c, ta, palette.Accent); ink.Empty() || ink.Dx() != 1 {
		t.Errorf("the caret is %v, want a one-pixel line", ink)
	}
	if ink := inkIn(c, image.Rect(ta.Max.X, r.Min.Y, r.Max.X, r.Max.Y), palette.Muted); ink.Empty() {
		t.Error("the count and buttons were not drawn")
	}
	// "No matches" is drawn in the bad colour.
	b.Total = 0
	b.Draw(c, r)
	if ink := inkIn(c, r, palette.Bad); ink.Empty() {
		t.Error("no matches was not drawn in the bad colour")
	}
	// A query longer than the field scrolls so that the caret stays in it.
	b.SetText("a very long query that cannot possibly fit in the field of the bar")
	c.Fill(c.Bounds(), palette.Chrome)
	b.Draw(c, r)
	ta = b.textArea(r)
	if ink := inkIn(c, ta, palette.Accent); ink.Empty() || !ink.In(ta) {
		t.Errorf("the caret of a long query is at %v, outside the field %v", ink, ta)
	}
	// A hovered button is drawn in the hover colour, its arrow in the text one.
	b.Total = 9
	b.hover = partNext
	c.Fill(c.Bounds(), palette.Chrome)
	b.Draw(c, r)
	if ink := inkIn(c, b.button(r, partNext), palette.Text); ink.Empty() {
		t.Error("a hovered button was not highlighted")
	}
}

func TestFindBarPointer(t *testing.T) {
	b := newFind(t)
	r := findRect
	centre := func(rect image.Rectangle) (float64, float64) {
		return float64(rect.Min.X+rect.Max.X) / 2, float64(rect.Min.Y+rect.Max.Y) / 2
	}
	ev := func(kind platform.PointerKind, btn platform.Button, x, y float64) platform.PointerEvent {
		return platform.PointerEvent{Kind: kind, Button: btn, X: x, Y: y}
	}
	// A press on a closed field asks to start a search.
	x, y := centre(r)
	if act, over := b.HandlePointer(ev(platform.Press, platform.Left, x, y), r); act != FindOpen || !over {
		t.Errorf("press on the closed field = %v, %v", act, over)
	}
	if act, over := b.HandlePointer(ev(platform.Release, platform.Left, x, y), r); act != FindNone || !over {
		t.Errorf("its release = %v, %v", act, over)
	}

	b.Open = true
	b.SetText("hello")
	b.Current, b.Total = 1, 3
	click := func(part findPart) (FindAction, bool) {
		x, y := centre(b.button(r, part))
		_, over := b.HandlePointer(ev(platform.Move, platform.None, x, y), r)
		if !over || b.hover != part {
			t.Fatalf("hover over %v: %v, %v", part, over, b.hover)
		}
		b.HandlePointer(ev(platform.Press, platform.Left, x, y), r)
		return b.HandlePointer(ev(platform.Release, platform.Left, x, y), r)
	}
	for part, want := range map[findPart]FindAction{partPrev: FindPrev, partNext: FindNext} {
		if act, over := click(part); act != want || !over {
			t.Errorf("clicking part %v = %v, %v, want %v", part, act, over, want)
		}
	}
	// A press on a button and a release elsewhere do nothing, but the field
	// still takes the release.
	x, y = centre(b.button(r, partNext))
	b.HandlePointer(ev(platform.Press, platform.Left, x, y), r)
	if act, over := b.HandlePointer(ev(platform.Release, platform.Left, 5, 150), r); act != FindNone || !over {
		t.Errorf("press on a button, release outside = %v, %v", act, over)
	}
	// A press in the text puts the caret there: at the start, middle and end.
	ta := b.textArea(r)
	for _, tc := range []struct {
		x    int
		want int
	}{{ta.Min.X, 0}, {ta.Min.X + b.Face.Width("hel") + 1, 3}, {ta.Max.X - 1, 5}} {
		b.HandlePointer(ev(platform.Press, platform.Left, float64(tc.x), y), r)
		b.HandlePointer(ev(platform.Release, platform.Left, float64(tc.x), y), r)
		if b.Cursor != tc.want {
			t.Errorf("a press at x=%d put the caret at %d, want %d", tc.x, b.Cursor, tc.want)
		}
	}
	// Right-button presses on the field are taken but do nothing; presses and
	// releases elsewhere are not on the field at all.
	if act, over := b.HandlePointer(ev(platform.Press, platform.Right, x, y), r); act != FindNone || !over {
		t.Errorf("right press on the field = %v, %v", act, over)
	}
	if _, over := b.HandlePointer(ev(platform.Press, platform.Left, 5, 150), r); over {
		t.Error("a press beside the field was taken")
	}
	if _, over := b.HandlePointer(ev(platform.Release, platform.Left, 5, 150), r); over {
		t.Error("a release beside the field was taken")
	}
	b.HandlePointer(ev(platform.Press, platform.Left, x, y), r)
	b.HandlePointer(ev(platform.Release, platform.Right, x, y), r) // another button's release does not click
	if _, over := b.HandlePointer(ev(platform.Move, platform.None, 5, 150), r); over {
		t.Error("a move beside the field was taken with no button held")
	}
	b.HandlePointer(ev(platform.Leave, platform.None, 0, 0), r)
	if b.hover != partNone {
		t.Error("the hover outlived the pointer")
	}
}
