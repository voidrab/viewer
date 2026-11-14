package widget

import (
	"image"
	"testing"

	"viewer/internal/ui/paint"
	"viewer/internal/ui/platform"
)

func TestFieldSelectAllIsReplacedByTyping(t *testing.T) {
	var f Field
	f.SetText("12")
	f.SelectAll()
	if !f.All || f.Cursor != 2 {
		t.Fatalf("SelectAll: all %v cursor %d", f.All, f.Cursor)
	}
	if changed := f.Key(platform.KeyEvent{Down: true, Rune: '7'}); !changed || f.String() != "7" || f.All {
		t.Errorf("typing over the selection: %v %q all %v", changed, f.String(), f.All)
	}
	// Backspace and Delete remove a selection whole.
	for _, k := range []platform.Key{platform.KeyBackspace, platform.KeyDelete} {
		f.SetText("345")
		f.SelectAll()
		if changed := f.Key(key(k, 0)); !changed || f.String() != "" || f.Cursor != 0 {
			t.Errorf("key %v over the selection: %v %q", k, changed, f.String())
		}
	}
	// Ctrl+A selects all; moving the caret ends the selection, Left going to
	// the start of what was selected.
	f.SetText("abc")
	if f.Key(platform.KeyEvent{Down: true, Rune: 'a', Mods: platform.Ctrl}) || !f.All {
		t.Error("Ctrl+A did not select all")
	}
	f.Key(key(platform.KeyLeft, 0))
	if f.All || f.Cursor != 0 {
		t.Errorf("Left from a selection: all %v cursor %d", f.All, f.Cursor)
	}
	// Selecting nothing selects nothing.
	f.SetText("")
	f.SelectAll()
	if f.All {
		t.Error("an empty field has a selection")
	}
	// Keys a field has no use for, releases, and deleting from an empty
	// field change nothing.
	for _, ev := range []platform.KeyEvent{key(platform.KeyEnter, 0), {Rune: 'x'}, key(platform.KeyBackspace, 0), key(platform.KeyDelete, 0)} {
		if f.Key(ev) || f.String() != "" {
			t.Errorf("%+v changed the text to %q", ev, f.String())
		}
	}
}

func TestFieldAcceptFiltersTypingAndPasting(t *testing.T) {
	f := Field{Accept: func(r rune) bool { return r >= '0' && r <= '9' }}
	if changed := f.Key(platform.KeyEvent{Down: true, Rune: 'x'}); changed || f.String() != "" {
		t.Errorf("a refused rune: %v %q", changed, f.String())
	}
	if !f.Insert("1a2") || f.String() != "12" {
		t.Errorf("Insert kept %q", f.String())
	}
	if f.Insert("abc") {
		t.Error("inserting only refused runes changed the text")
	}
}

func TestFieldDrawAndCaretAt(t *testing.T) {
	face := newFind(t).Face
	r := image.Rect(10, 0, 110, 20)
	c := paint.New(120, 20)
	c.Fill(c.Bounds(), palette.Raised)
	f := Field{Right: true}
	f.SetText("42")
	f.SelectAll()
	f.Draw(c, r, face, palette, palette.Text, true)
	ink := inkIn(c, r, palette.Text)
	if ink.Empty() || ink.Max.X < r.Max.X-4 {
		t.Errorf("right-aligned text drawn at %v in %v", ink, r)
	}
	if got := c.Img.RGBAAt(r.Max.X-face.Width("42")+1, 1+face.Height()/2); got == nrgba(palette.Raised) {
		t.Error("the selection was not drawn")
	}
	if caret := inkIn(c, r, palette.Accent); caret.Empty() {
		t.Error("the caret was not drawn")
	}
	// A click left of right-aligned text puts the caret at the start, and on
	// its right half after it.
	if f.CaretAt(face, r, r.Min.X) != 0 || f.All {
		t.Errorf("caret at the far left = %d, all %v", f.Cursor, f.All)
	}
	if f.CaretAt(face, r, r.Max.X-1) != 2 {
		t.Errorf("caret at the far right = %d", f.Cursor)
	}
	// Without the caret, none is drawn.
	c.Fill(c.Bounds(), palette.Raised)
	f.Draw(c, r, face, palette, palette.Text, false)
	if caret := inkIn(c, r, palette.Accent); !caret.Empty() {
		t.Error("a caret was drawn")
	}
	// Text wider than the field is left aligned and shifted to show the caret.
	f.SetText("12345678901234567890")
	if o := f.origin(face, r); o >= r.Min.X {
		t.Errorf("long text starts at %d", o)
	}
}
