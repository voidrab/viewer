package widget

import (
	"fmt"
	"image"
	"unicode"

	"viewer/internal/ui/icon"
	"viewer/internal/ui/paint"
	"viewer/internal/ui/platform"
	"viewer/internal/ui/text"
	"viewer/internal/ui/theme"
)

// FindAction says what the user asked of a FindBar.
type FindAction int

// Find bar actions.
const (
	FindNone   FindAction = iota
	FindEdited            // the text changed: search for it again
	FindNext              // go to the next match
	FindPrev              // go to the previous match
	FindClose             // close the bar
)

// Find bar geometry, in pixels.
const (
	findWidth  = 380
	findHeight = 34
	findMargin = 12
	findButton = 26
	findPad    = 10
)

// findPart is what the pointer is over in a FindBar.
type findPart int

const (
	partNone findPart = iota
	partText
	partPrev
	partNext
	partClose
)

// FindBar is a one-line text field with the count of matches and buttons to
// step through them, drawn over the top-right corner of the content.
type FindBar struct {
	Palette theme.Palette
	Face    *text.Face

	// Text is the query and Cursor the caret's place in it, between runes.
	Text   []rune
	Cursor int
	// Current is the match shown, counting from 1, out of Total. Searching
	// says the search is still going.
	Current, Total int
	Searching      bool

	hover, press findPart
}

// NewFindBar returns an empty find bar using palette and face.
func NewFindBar(palette theme.Palette, face *text.Face) *FindBar {
	return &FindBar{Palette: palette, Face: face}
}

// SetText replaces the query, with the caret after it.
func (b *FindBar) SetText(s string) {
	b.Text = []rune(s)
	b.Cursor = len(b.Text)
}

// Rect returns where the bar lies within area, the content it covers.
func (b *FindBar) Rect(area image.Rectangle) image.Rectangle {
	w := min(findWidth, area.Dx()-2*findMargin)
	return image.Rect(area.Max.X-findMargin-w, area.Min.Y+findMargin, area.Max.X-findMargin, area.Min.Y+findMargin+findHeight)
}

// button returns where a button lies in the bar at r.
func (b *FindBar) button(r image.Rectangle, p findPart) image.Rectangle {
	n := int(partClose - p) // buttons to the right of it
	x1 := r.Max.X - 4 - n*findButton
	y := r.Min.Y + (r.Dy()-findButton)/2
	return image.Rect(x1-findButton, y, x1, y+findButton)
}

// countText is the result count shown in the bar.
func (b *FindBar) countText() string {
	switch {
	case b.Total > 0:
		return fmt.Sprintf("%d/%d", b.Current, b.Total)
	case len(b.Text) == 0:
		return ""
	case b.Searching:
		return "…"
	}
	return "No results"
}

// textArea returns where the query is drawn within the bar at r: left of the
// count and the buttons.
func (b *FindBar) textArea(r image.Rectangle) image.Rectangle {
	right := b.button(r, partPrev).Min.X - 8 - b.Face.Width(b.countText())
	return image.Rect(r.Min.X+findPad, r.Min.Y+3, max(right-8, r.Min.X+findPad), r.Max.Y-3)
}

// partAt returns what is at p in the bar at r.
func (b *FindBar) partAt(r image.Rectangle, p image.Point) findPart {
	if !p.In(r) {
		return partNone
	}
	for _, part := range []findPart{partPrev, partNext, partClose} {
		if p.In(b.button(r, part)) {
			return part
		}
	}
	return partText
}

// Draw paints the bar over the content at area.
func (b *FindBar) Draw(c *paint.Canvas, area image.Rectangle) {
	r := b.Rect(area)
	p := b.Palette
	c.Fill(r, p.Border)
	c.Fill(r.Inset(1), p.Raised)

	ta := b.textArea(r)
	c.PushClip(ta)
	// The caret stays in view: a long query is shown from its end.
	shift := max(0, b.Face.Width(string(b.Text[:b.Cursor]))-ta.Dx()+2)
	ty := r.Min.Y + (r.Dy()-b.Face.Height())/2
	b.Face.Draw(c, ta.Min.X-shift, ty, string(b.Text), p.Text)
	cx := ta.Min.X - shift + b.Face.Width(string(b.Text[:b.Cursor]))
	c.Fill(image.Rect(cx, ty, cx+1, ty+b.Face.Height()), p.Accent)
	c.PopClip()

	count := b.countText()
	col := p.Muted
	if b.Total == 0 && len(b.Text) > 0 && !b.Searching {
		col = p.Bad
	}
	b.Face.Draw(c, b.button(r, partPrev).Min.X-8-b.Face.Width(count), ty, count, col)
	for _, part := range []findPart{partPrev, partNext, partClose} {
		br := b.button(r, part)
		if b.hover == part {
			c.Fill(br, p.Hover)
		}
		ic := map[findPart]icon.Icon{partPrev: icon.Up, partNext: icon.Down, partClose: icon.Close}[part]
		icon.Draw(c, br.Inset(5), ic, p.Muted, 2)
	}
}

// HandleKey applies a key press to the field and says what it asks for.
func (b *FindBar) HandleKey(ev platform.KeyEvent) FindAction {
	if !ev.Down {
		return FindNone
	}
	switch ev.Key {
	case platform.KeyEscape:
		return FindClose
	case platform.KeyEnter:
		if ev.Mods&platform.Shift != 0 {
			return FindPrev
		}
		return FindNext
	case platform.KeyBackspace:
		if b.Cursor > 0 {
			b.Text = append(b.Text[:b.Cursor-1], b.Text[b.Cursor:]...)
			b.Cursor--
			return FindEdited
		}
	case platform.KeyDelete:
		if b.Cursor < len(b.Text) {
			b.Text = append(b.Text[:b.Cursor], b.Text[b.Cursor+1:]...)
			return FindEdited
		}
	case platform.KeyLeft:
		b.Cursor = max(b.Cursor-1, 0)
	case platform.KeyRight:
		b.Cursor = min(b.Cursor+1, len(b.Text))
	case platform.KeyHome:
		b.Cursor = 0
	case platform.KeyEnd:
		b.Cursor = len(b.Text)
	default:
		if ev.Rune >= ' ' && ev.Rune != 0x7f && ev.Mods&(platform.Ctrl|platform.Alt) == 0 {
			b.insert(string(ev.Rune))
			return FindEdited
		}
	}
	return FindNone
}

// Paste inserts text at the caret, with line breaks as spaces.
func (b *FindBar) Paste(s string) FindAction {
	s = squash(s)
	if s == "" {
		return FindNone
	}
	b.insert(s)
	return FindEdited
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

func (b *FindBar) insert(s string) {
	rs := []rune(s)
	b.Text = append(b.Text[:b.Cursor], append(rs, b.Text[b.Cursor:]...)...)
	b.Cursor += len(rs)
}

// HandlePointer applies a pointer event, in the same coordinates as area, the
// content the bar covers. over reports whether the event was on the bar, which
// then takes it; the action is what a button asked for.
func (b *FindBar) HandlePointer(ev platform.PointerEvent, area image.Rectangle) (act FindAction, over bool) {
	r := b.Rect(area)
	p := image.Pt(int(ev.X), int(ev.Y))
	part := b.partAt(r, p)
	switch ev.Kind {
	case platform.Move, platform.Enter:
		b.hover = part
	case platform.Leave:
		b.hover = partNone
	case platform.Press:
		if ev.Button != platform.Left || part == partNone {
			return FindNone, part != partNone
		}
		b.press = part
		if part == partText {
			b.Cursor = b.caretAt(r, p.X)
		}
	case platform.Release:
		pressed := b.press
		b.press = partNone
		if ev.Button == platform.Left && pressed != partNone && pressed == part {
			switch part {
			case partPrev:
				return FindPrev, true
			case partNext:
				return FindNext, true
			case partClose:
				return FindClose, true
			}
		}
	}
	return FindNone, part != partNone || b.press != partNone
}

// caretAt returns the caret position nearest x in the field of the bar at r.
func (b *FindBar) caretAt(r image.Rectangle, x int) int {
	ta := b.textArea(r)
	shift := max(0, b.Face.Width(string(b.Text[:b.Cursor]))-ta.Dx()+2)
	for i := range b.Text {
		mid := ta.Min.X - shift + (b.Face.Width(string(b.Text[:i]))+b.Face.Width(string(b.Text[:i+1])))/2
		if x < mid {
			return i
		}
	}
	return len(b.Text)
}
