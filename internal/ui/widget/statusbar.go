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
	// Right are values shown right-aligned, in order, such as the claimed
	// conformance, the zoom and the page counter.
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
	return s.Palette.Accent
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
	// limit is where the left-hand content must end: the left edge of the
	// right-hand values, less a gap.
	limit := r.Max.X - statusPad
	for i := len(s.Right) - 1; i >= 0; i-- {
		limit -= s.Face.Width(s.Right[i])
		s.Face.Draw(c, limit, y, s.Right[i], p.Muted)
		limit -= statusGap
	}

	x := r.Min.X + statusPad
	if s.Info != "" {
		info := s.Face.Fit(s.Info, limit-x)
		s.Face.Draw(c, x, y, info, p.Muted)
		x += s.Face.Width(info) + statusGap
	}
	if s.Message != "" && limit-x > 0 {
		s.Face.Draw(c, x, y, s.Face.Fit(s.Message, limit-x), s.messageColour(s.Kind))
	}
}
