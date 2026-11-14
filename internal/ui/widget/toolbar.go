package widget

import (
	"image"
	"image/color"

	"viewer/internal/ui/icon"
	"viewer/internal/ui/paint"
	"viewer/internal/ui/platform"
	"viewer/internal/ui/text"
	"viewer/internal/ui/theme"
)

// ButtonID names a toolbar button.
type ButtonID int

// The toolbar's buttons, left to right.
const (
	ButtonOpen ButtonID = iota
	ButtonPrev
	ButtonNext
	ButtonZoomOut
	ButtonZoomMode // the zoom button, which shows ZoomText
	ButtonZoomIn
	ButtonFind
	ButtonPDFA
	buttonCount
)

// Toolbar geometry, in pixels.
const (
	toolPad     = 10
	toolButton  = 28
	toolGap     = 4
	toolSection = 12 // extra space between groups
	toolIcon    = 18
)

// Toolbar is the row of buttons above the document.
type Toolbar struct {
	Palette theme.Palette
	Face    *text.Face

	// PageText is shown between the page buttons, such as "3 / 12", and
	// ZoomText on the zoom button, such as "Fit width" or "125%".
	PageText, ZoomText string
	// Disabled buttons are drawn dimmed and ignore the pointer. On buttons
	// are drawn as switched on, for example while their panel is open.
	Disabled, On map[ButtonID]bool

	rects   [buttonCount]image.Rectangle
	pageBox image.Rectangle // where PageText is drawn
	width   int
	laid    bool
	hover   ButtonID // -1 when none
	press   ButtonID // -1 when none
}

// NewToolbar returns a toolbar using palette and face.
func NewToolbar(palette theme.Palette, face *text.Face) *Toolbar {
	return &Toolbar{Palette: palette, Face: face, Disabled: map[ButtonID]bool{}, On: map[ButtonID]bool{}, hover: -1, press: -1}
}

// Height returns the toolbar's height.
func (t *Toolbar) Height() int { return theme.ToolbarHeight }

// icons maps each button to its icon; the zoom button shows text instead.
var icons = map[ButtonID]icon.Icon{
	ButtonOpen: icon.Open, ButtonPrev: icon.Prev, ButtonNext: icon.Next,
	ButtonZoomOut: icon.Minus, ButtonZoomIn: icon.Plus, ButtonFind: icon.Search, ButtonPDFA: icon.Shield,
}

// Layout records width as the bar's width and positions the buttons from the
// left edge. The page and zoom buttons are sized to fit their text, so longer
// text pushes the later buttons right.
func (t *Toolbar) Layout(width int) {
	t.width = width
	y := (theme.ToolbarHeight - toolButton) / 2
	x := toolPad
	place := func(id ButtonID, w int) {
		t.rects[id] = image.Rect(x, y, x+w, y+toolButton)
		x += w + toolGap
	}
	place(ButtonOpen, toolButton)
	x += toolSection
	place(ButtonPrev, toolButton)
	pageW := max(t.Face.Width(t.PageText)+16, toolButton)
	t.pageBox = image.Rect(x, y, x+pageW, y+toolButton)
	x += pageW + toolGap
	place(ButtonNext, toolButton)
	x += toolSection
	place(ButtonZoomOut, toolButton)
	place(ButtonZoomMode, max(t.Face.Width(t.ZoomText)+16, 72))
	place(ButtonZoomIn, toolButton)
	x += toolSection
	place(ButtonFind, toolButton)
	place(ButtonPDFA, toolButton)
	t.laid = true
}

// hitTest returns the enabled button at p, or -1.
func (t *Toolbar) hitTest(p image.Point) ButtonID {
	for id := ButtonID(0); id < buttonCount; id++ {
		if p.In(t.rects[id]) && !t.Disabled[id] {
			return id
		}
	}
	return -1
}

// HandleEvent applies a pointer event given in the toolbar's own coordinates.
// It returns an Action of kind ActionButton when the left button is released
// over the button it was pressed on, and the zero Action otherwise.
func (t *Toolbar) HandleEvent(ev platform.PointerEvent) Action {
	if !t.laid {
		t.Layout(t.width)
	}
	p := image.Pt(int(ev.X), int(ev.Y))
	switch ev.Kind {
	case platform.Move:
		t.hover = t.hitTest(p)
	case platform.Leave:
		t.hover = -1
	case platform.Press:
		if ev.Button == platform.Left {
			t.press = t.hitTest(p)
		}
	case platform.Release:
		pressed := t.press
		t.press = -1
		if ev.Button == platform.Left && pressed >= 0 && t.hitTest(p) == pressed {
			return Action{Kind: ActionButton, Index: int(pressed)}
		}
	}
	return Action{}
}

// Draw paints the toolbar with its top-left corner at origin.
func (t *Toolbar) Draw(c *paint.Canvas, origin image.Point) {
	if !t.laid {
		t.Layout(t.width)
	}
	p := t.Palette
	bar := image.Rect(origin.X, origin.Y, origin.X+max(t.width, t.rects[ButtonPDFA].Max.X+toolPad), origin.Y+theme.ToolbarHeight)
	c.Fill(bar, p.Chrome)
	c.Fill(image.Rect(bar.Min.X, bar.Max.Y-1, bar.Max.X, bar.Max.Y), p.Border)

	for id := ButtonID(0); id < buttonCount; id++ {
		r := t.rects[id].Add(origin)
		fg := p.Muted
		switch {
		case t.Disabled[id]:
			fg = p.Faint
		case t.press == id && t.hover == id:
			c.Fill(r, p.Active)
			fg = p.Text
		case t.On[id]:
			c.Fill(r, p.Selection)
			fg = p.Accent
		case t.hover == id:
			c.Fill(r, p.Hover)
			fg = p.Text
		}
		if ic, ok := icons[id]; ok {
			pad := (toolButton - toolIcon) / 2
			icon.Draw(c, r.Inset(pad), ic, fg, 2)
			continue
		}
		t.drawCentred(c, r, t.ZoomText, fg)
	}
	fg := p.Muted
	if t.Disabled[ButtonPrev] && t.Disabled[ButtonNext] {
		fg = p.Faint
	}
	t.drawCentred(c, t.pageBox.Add(origin), t.PageText, fg)
}

// drawCentred draws s centred in r, shortened to fit.
func (t *Toolbar) drawCentred(c *paint.Canvas, r image.Rectangle, s string, col color.NRGBA) {
	s = t.Face.Fit(s, r.Dx())
	t.Face.Draw(c, r.Min.X+(r.Dx()-t.Face.Width(s))/2, r.Min.Y+(r.Dy()-t.Face.Height())/2, s, col)
}
