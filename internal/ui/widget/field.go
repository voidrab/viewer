package widget

import (
	"image"
	"image/color"
	"unicode"

	"viewrab/internal/ui/paint"
	"viewrab/internal/ui/platform"
	"viewrab/internal/ui/text"
	"viewrab/internal/ui/theme"
)

// Field is the editable text of a one-line input: the text, the caret, and
// whether all of the text is selected. While All is set, typing replaces the
// text.
type Field struct {
	// Text is the content and Cursor the caret's place in it, between runes.
	Text   []rune
	Cursor int
	// All is set while all of Text is selected.
	All bool
	// Accept, if set, limits the runes that may be typed or pasted.
	Accept func(r rune) bool
	// Right aligns the text with the right edge of the field.
	Right bool
}

// SetText replaces the text, with the caret after it and nothing selected.
func (f *Field) SetText(s string) {
	f.Text = []rune(s)
	f.Cursor = len(f.Text)
	f.All = false
}

// String returns the text.
func (f *Field) String() string { return string(f.Text) }

// SelectAll selects all of the text, with the caret after it.
func (f *Field) SelectAll() {
	f.Cursor = len(f.Text)
	f.All = len(f.Text) > 0
}

// clearAll deletes the text if all of it is selected, and reports whether it
// did.
func (f *Field) clearAll() bool {
	if !f.All {
		return false
	}
	f.Text, f.Cursor, f.All = f.Text[:0], 0, false
	return true
}

// Key applies an editing key press: typing, deletion, caret movement and
// Ctrl+A. It reports whether the text changed.
func (f *Field) Key(ev platform.KeyEvent) (changed bool) {
	if !ev.Down {
		return false
	}
	ctrl := ev.Mods&platform.Ctrl != 0
	switch {
	case ctrl && ev.Rune == 'a':
		f.SelectAll()
		return false
	case ev.Key == platform.KeyBackspace:
		if f.clearAll() {
			return true
		}
		if f.Cursor > 0 {
			f.Text = append(f.Text[:f.Cursor-1], f.Text[f.Cursor:]...)
			f.Cursor--
			return true
		}
		return false
	case ev.Key == platform.KeyDelete:
		if f.clearAll() {
			return true
		}
		if f.Cursor < len(f.Text) {
			f.Text = append(f.Text[:f.Cursor], f.Text[f.Cursor+1:]...)
			return true
		}
		return false
	case ev.Key == platform.KeyLeft:
		f.Cursor = max(f.Cursor-1, 0)
		if f.All {
			f.Cursor = 0
		}
	case ev.Key == platform.KeyRight:
		f.Cursor = min(f.Cursor+1, len(f.Text))
	case ev.Key == platform.KeyHome:
		f.Cursor = 0
	case ev.Key == platform.KeyEnd:
		f.Cursor = len(f.Text)
	case ev.Rune >= ' ' && ev.Rune != 0x7f && ev.Mods&(platform.Ctrl|platform.Alt) == 0:
		return f.Insert(string(ev.Rune))
	default:
		return false
	}
	f.All = false
	return false
}

// Insert puts s at the caret, replacing the text if all of it is selected,
// and reports whether the text changed. With Accept set, the runes it refuses
// are left out.
func (f *Field) Insert(s string) bool {
	rs := make([]rune, 0, len(s))
	for _, r := range s {
		if f.Accept == nil || f.Accept(r) {
			rs = append(rs, r)
		}
	}
	if len(rs) == 0 {
		return false
	}
	f.clearAll()
	f.Text = append(f.Text[:f.Cursor], append(rs, f.Text[f.Cursor:]...)...)
	f.Cursor += len(rs)
	return true
}

// origin returns where the text starts within r: at its left edge, or right
// aligned, and shifted left as far as it takes to keep the caret in view.
func (f *Field) origin(face *text.Face, r image.Rectangle) int {
	w := face.Width(string(f.Text))
	x := r.Min.X
	if f.Right && w < r.Dx()-1 {
		x = r.Max.X - 1 - w
	}
	return x - max(0, face.Width(string(f.Text[:f.Cursor]))-r.Dx()+2)
}

// Draw paints the text within r in col, the selection behind it when all of
// it is selected, and the caret when caret is set.
func (f *Field) Draw(c *paint.Canvas, r image.Rectangle, face *text.Face, p theme.Palette, col color.NRGBA, caret bool) {
	c.PushClip(r)
	defer c.PopClip()
	x := f.origin(face, r)
	y := r.Min.Y + (r.Dy()-face.Height())/2
	if f.All {
		c.Fill(image.Rect(x, y, x+face.Width(string(f.Text)), y+face.Height()), p.Selection)
	}
	face.Draw(c, x, y, string(f.Text), col)
	if caret {
		cx := x + face.Width(string(f.Text[:f.Cursor]))
		c.Fill(image.Rect(cx, y, cx+1, y+face.Height()), p.Accent)
	}
}

// CaretAt returns the caret position nearest x in the field drawn in r, and
// puts the caret there, ending a selection of all the text.
func (f *Field) CaretAt(face *text.Face, r image.Rectangle, x int) int {
	o := f.origin(face, r)
	f.Cursor, f.All = len(f.Text), false
	for i := range f.Text {
		mid := o + (face.Width(string(f.Text[:i]))+face.Width(string(f.Text[:i+1])))/2
		if x < mid {
			f.Cursor = i
			break
		}
	}
	return f.Cursor
}

// squash returns s with each run of white space replaced by one space, and none
// at either end.
func squash(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if unicode.IsSpace(r) {
			if len(out) > 0 && out[len(out)-1] != ' ' {
				out = append(out, ' ')
			}
			continue
		}
		out = append(out, r)
	}
	if n := len(out); n > 0 && out[n-1] == ' ' {
		out = out[:n-1]
	}
	return string(out)
}
