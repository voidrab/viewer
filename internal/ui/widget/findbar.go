package widget

import (
	"fmt"
	"image"

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
	FindClose             // close the search
	FindOpen              // the field was clicked while closed: start a search
)

// Find field geometry, in pixels.
const (
	// FindWidth is the preferred width of the find field; FindMinWidth is the
	// narrowest it shrinks to in a narrow toolbar.
	FindWidth    = 195
	FindMinWidth = 90
	findPad      = 8
	findIcon     = 14
	findButton   = 20
)

// findPart is what the pointer is over in a FindBar.
type findPart int

const (
	partNone findPart = iota
	partText
	partPrev
	partNext
)

// FindBar is the find field in the toolbar: a search icon, the query, the
// count of matches and, during a search, buttons to step through them. Closed,
// it shows a placeholder and a click on it starts a search.
type FindBar struct {
	Palette theme.Palette
	Face    *text.Face

	// Field is the query being edited.
	Field
	// Open is set while a search is going on, and the field has the keys.
	Open bool
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

// stepping reports whether the step buttons show: during a search that found
// something.
func (b *FindBar) stepping() bool { return b.Open && len(b.Text) > 0 && b.Total > 0 }

// button returns where a step button lies in the field at r.
func (b *FindBar) button(r image.Rectangle, p findPart) image.Rectangle {
	n := int(partNext - p) // buttons to the right of it
	x1 := r.Max.X - 3 - n*findButton
	y := r.Min.Y + (r.Dy()-findButton)/2
	return image.Rect(x1-findButton, y, x1, y+findButton)
}

// countText is the result count shown in the field.
func (b *FindBar) countText() string {
	switch {
	case !b.Open || len(b.Text) == 0:
		return ""
	case b.Total > 0:
		return fmt.Sprintf("%d / %d", b.Current, b.Total)
	case b.Searching:
		return "…"
	}
	return "No matches"
}

// countRight returns where the count ends: left of the step buttons, if shown.
func (b *FindBar) countRight(r image.Rectangle) int {
	if b.stepping() {
		return b.button(r, partPrev).Min.X - 4
	}
	return r.Max.X - findPad
}

// textArea returns where the query is drawn within the field at r: right of
// the icon and left of the count and the buttons.
func (b *FindBar) textArea(r image.Rectangle) image.Rectangle {
	left := r.Min.X + findPad + findIcon + 6
	right := b.countRight(r)
	if count := b.countText(); count != "" {
		right -= b.Face.Width(count) + 6
	}
	return image.Rect(left, r.Min.Y+3, max(right, left), r.Max.Y-3)
}

// partAt returns what is at p in the field at r.
func (b *FindBar) partAt(r image.Rectangle, p image.Point) findPart {
	if !p.In(r) {
		return partNone
	}
	if b.stepping() {
		for _, part := range []findPart{partPrev, partNext} {
			if p.In(b.button(r, part)) {
				return part
			}
		}
	}
	return partText
}

// Draw paints the field into r.
func (b *FindBar) Draw(c *paint.Canvas, r image.Rectangle) {
	p := b.Palette
	switch {
	case b.Open:
		c.FillRound(r, theme.Radius, p.Raised)
		c.StrokeRound(r, theme.Radius, 1, p.Border)
	case b.hover != partNone:
		c.FillRound(r, theme.Radius, p.Hover)
	}
	iy := r.Min.Y + (r.Dy()-findIcon)/2
	icon.Draw(c, image.Rect(r.Min.X+findPad, iy, r.Min.X+findPad+findIcon, iy+findIcon), icon.Search, p.Muted, 1.6)

	ta := b.textArea(r)
	ty := r.Min.Y + (r.Dy()-b.Face.Height())/2
	if len(b.Text) == 0 && !b.Open {
		b.Face.Draw(c, ta.Min.X, ty, b.Face.Fit("find", ta.Dx()), p.Faint)
	} else {
		b.Field.Draw(c, ta, b.Face, p, p.Text, b.Open)
	}

	if count := b.countText(); count != "" {
		col := p.Muted
		if b.Total == 0 && !b.Searching {
			col = p.Bad
		}
		b.Face.Draw(c, b.countRight(r)-b.Face.Width(count), ty, count, col)
	}
	if !b.stepping() {
		return
	}
	for part, ic := range map[findPart]icon.Icon{partPrev: icon.Up, partNext: icon.Down} {
		br := b.button(r, part)
		fg := p.Muted
		if b.hover == part {
			c.FillRound(br, theme.Radius, p.Hover)
			fg = p.Text
		}
		icon.Draw(c, br.Inset(3), ic, fg, 1.6)
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
	}
	if b.Key(ev) {
		return FindEdited
	}
	return FindNone
}

// Paste inserts s at the caret, with each run of white space replaced by one
// space and none at either end.
func (b *FindBar) Paste(s string) FindAction {
	if b.Insert(squash(s)) {
		return FindEdited
	}
	return FindNone
}

// HandlePointer applies a pointer event, in the same coordinates as r, the
// field's rectangle. over reports whether the event was on the field, or
// belongs to a press that began there, which then takes it; the action is what
// the user asked for.
func (b *FindBar) HandlePointer(ev platform.PointerEvent, r image.Rectangle) (act FindAction, over bool) {
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
		if !b.Open {
			return FindOpen, true
		}
		if part == partText {
			b.CaretAt(b.Face, b.textArea(r), p.X)
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
			}
		}
		return FindNone, part != partNone || pressed != partNone
	}
	return FindNone, part != partNone || b.press != partNone
}
