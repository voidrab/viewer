package widget

import (
	"image"
	"image/color"

	"viewer/internal/ui/paint"
	"viewer/internal/ui/text"
	"viewer/internal/ui/theme"
)

// MessageKind selects the colour of a status message.
type MessageKind int

// Message kinds.
const (
	MessageInfo MessageKind = iota
	MessageOK
	MessageWarn
	MessageBad
)

// Status bar geometry, in pixels.
const (
	statusPad = 12
	statusGap = 14
)

// StatusBar is the line at the bottom of a window: what is open on the left,
// a transient message after it, and a few values on the right.
type StatusBar struct {
	Palette theme.Palette
	Face    *text.Face

	// Info is the open document's name and size.
	Info string
	// Message is shown after Info in the colour of Kind; empty for none.
	Message string
	Kind    MessageKind
	// Claimed is the conformance the document claims, such as "PDF/A-1b",
	// shown in the OK colour before Right; empty for none.
	Claimed string
	// Right are values shown right-aligned, in order, such as the zoom.
	Right []string
}

// NewStatusBar returns a status bar using palette and face.
func NewStatusBar(palette theme.Palette, face *text.Face) *StatusBar {
	return &StatusBar{Palette: palette, Face: face}
}

// Height returns the bar's height.
func (s *StatusBar) Height() int { return theme.StatusHeight }

// messageColour returns the colour of a message of kind k.
func (s *StatusBar) messageColour(k MessageKind) color.NRGBA {
	switch k {
	case MessageOK:
		return s.Palette.OK
	case MessageWarn:
		return s.Palette.Warn
	case MessageBad:
		return s.Palette.Bad
	}
	return s.Palette.Text
}

// Draw paints the bar into r. The right-hand values keep their room; Info and
// the message are shortened with an ellipsis to share what is left.
func (s *StatusBar) Draw(c *paint.Canvas, r image.Rectangle) {
	p := s.Palette
	c.Fill(r, p.Chrome)
	c.Fill(image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+1), p.Border)
	c.PushClip(r)
	defer c.PopClip()

	y := r.Min.Y + (r.Dy()-s.Face.Height())/2
	// limit is the x coordinate where the left-hand content must end. It
	// starts at the right padding and moves left past each right-hand value
	// and the gap after it.
	limit := r.Max.X - statusPad
	for i := len(s.Right) - 1; i >= 0; i-- {
		limit -= s.Face.Width(s.Right[i])
		s.Face.Draw(c, limit, y, s.Right[i], p.Muted)
		limit -= statusGap
	}
	if s.Claimed != "" {
		limit -= s.Face.Width(s.Claimed)
		s.Face.Draw(c, limit, y, s.Claimed, p.OK)
		limit -= statusGap
	}

	x := r.Min.X + statusPad
	if s.Info != "" {
		info := s.Face.Fit(s.Info, limit-x)
		s.Face.Draw(c, x, y, info, p.Muted)
		x += s.Face.Width(info) + statusGap
	}
	// A message reads like a console line: an accent mark, then the text.
	if s.Message != "" && limit-x > 0 {
		const mark = "› "
		s.Face.Draw(c, x, y, s.Face.Fit(mark, limit-x), p.Accent)
		x += s.Face.Width(mark)
		s.Face.Draw(c, x, y, s.Face.Fit(s.Message, limit-x), s.messageColour(s.Kind))
	}
}
