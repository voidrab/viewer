package widget

import (
	"image"
	"image/color"
	"strconv"

	"viewer/internal/ui/icon"
	"viewer/internal/ui/paint"
	"viewer/internal/ui/platform"
	"viewer/internal/ui/text"
	"viewer/internal/ui/theme"
)

// ButtonID names a toolbar control.
type ButtonID int

// The toolbar's controls, left to right.
const (
	ButtonOpen ButtonID = iota
	ButtonPrev
	ButtonPage // the page number field
	ButtonNext
	ButtonZoomOut
	ButtonZoomMode // the zoom drop-down, which shows ZoomText
	ButtonZoomIn
	ButtonFind // where the find field goes; the toolbar leaves it empty
	ButtonPDFA
	ButtonTheme // switches between the light and the dark theme
	// The window controls, at the right end, shown when Controls is set.
	ButtonMinimize
	ButtonMaximize
	ButtonCloseWindow
	buttonCount
)

// Toolbar geometry, in pixels.
const (
	toolPad    = 10 // left of the logo
	logoSize   = 18
	toolButton = 26
	toolGap    = 4  // between controls
	groupGap   = 2  // between the controls of a group
	groupLead  = 10 // extra space before a group
	toolIcon   = 16
	pageWidth  = 42 // the page number field, 3.2em
	chevron    = 12 // the drop-down's arrow

	winButton = 28 // a window control
	winGap    = 2
	winPad    = 6 // right of the last control, and left of the first
	winIcon   = 16
)

// docOnly are the controls shown only while a document is open.
var docOnly = map[ButtonID]bool{
	ButtonPrev: true, ButtonPage: true, ButtonNext: true, ButtonZoomOut: true, ButtonZoomMode: true,
	ButtonZoomIn: true, ButtonFind: true, ButtonPDFA: true,
}

// Toolbar is the row of controls above the document, which is also the
// window's title bar.
type Toolbar struct {
	Palette theme.Palette
	Face    *text.Face

	// Page is the page shown, counting from 1, out of Pages.
	Page, Pages int
	// PageField is the page number being typed while EditingPage is set.
	PageField   Field
	EditingPage bool
	// ZoomText is shown in the zoom drop-down, such as "fit width" or
	// "125%". ZoomChoices are all the texts it may show, which set its width
	// so that it does not change with the zoom.
	ZoomText    string
	ZoomChoices []string
	// NoDocument hides the controls that work on a document.
	NoDocument bool
	// Disabled controls are drawn dimmed and ignore the pointer. On controls
	// are drawn as switched on, for example while their panel is open.
	Disabled, On map[ButtonID]bool
	// Controls shows the window controls, for a window the compositor does
	// not decorate, which makes the toolbar its title bar. Maximized shows
	// the maximise control as one that restores.
	Controls, Maximized bool

	rects    [buttonCount]image.Rectangle
	logo     image.Rectangle
	pagesBox image.Rectangle // where "/ Pages" is drawn
	width    int
	laid     bool
	hover    ButtonID // -1 when none
	press    ButtonID // -1 when none
	// grab is where a left button went down on the bar's empty part, or nil.
	// Moving the pointer beyond dragSlop from it starts a window move or, when
	// the press was on the logo and a document is open, a drag of every tab.
	grab *image.Point
}

// NewToolbar returns a toolbar using palette and face.
func NewToolbar(palette theme.Palette, face *text.Face) *Toolbar {
	return &Toolbar{
		Palette: palette, Face: face, Disabled: map[ButtonID]bool{}, On: map[ButtonID]bool{}, hover: -1, press: -1,
		PageField: Field{Right: true, Accept: func(r rune) bool { return r >= '0' && r <= '9' }},
	}
}

// Height returns the toolbar's height.
func (t *Toolbar) Height() int { return theme.ToolbarHeight }

// icons maps each button to its icon. The page field, the zoom drop-down, the
// find field and the theme switch have none here.
var icons = map[ButtonID]icon.Icon{
	ButtonOpen: icon.Open, ButtonPrev: icon.Prev, ButtonNext: icon.Next,
	ButtonZoomOut: icon.Minus, ButtonZoomIn: icon.Plus, ButtonPDFA: icon.Shield,
	ButtonMinimize: icon.WinMinimize, ButtonMaximize: icon.WinMaximize, ButtonCloseWindow: icon.WinClose,
}

// isControl reports whether id is a window control.
func isControl(id ButtonID) bool { return id >= ButtonMinimize }

// pagesText is the page count shown after the page field.
func (t *Toolbar) pagesText() string { return "/ " + strconv.Itoa(t.Pages) }

// zoomWidth returns the width of the zoom drop-down: room for the widest text
// it may show and its arrow.
func (t *Toolbar) zoomWidth() int {
	w := t.Face.Width(t.ZoomText)
	for _, s := range t.ZoomChoices {
		w = max(w, t.Face.Width(s))
	}
	return w + 6 + chevron + 10
}

// Layout records width as the bar's width and places the controls: the logo,
// Open, and the page and zoom groups from the left edge; the window controls,
// if shown, the theme switch, PDF/A and the find field from the right edge.
// The find field shrinks, down to FindMinWidth, to fit a narrow bar.
func (t *Toolbar) Layout(width int) {
	t.width = width
	y := (theme.ToolbarHeight - toolButton) / 2
	ly := (theme.ToolbarHeight - logoSize) / 2
	t.logo = image.Rect(toolPad, ly, toolPad+logoSize, ly+logoSize)
	x := t.logo.Max.X + 4 + toolGap
	place := func(id ButtonID, w, gap int) {
		t.rects[id] = image.Rect(x, y, x+w, y+toolButton)
		x += w + gap
	}
	place(ButtonOpen, toolButton, toolGap)
	x += groupLead
	place(ButtonPrev, toolButton, groupGap)
	place(ButtonPage, pageWidth, groupGap)
	pw := t.Face.Width(t.pagesText())
	t.pagesBox = image.Rect(x, y, x+pw, y+toolButton)
	x += pw + 2 + groupGap
	place(ButtonNext, toolButton, toolGap)
	x += groupLead
	place(ButtonZoomOut, toolButton, groupGap)
	place(ButtonZoomMode, t.zoomWidth(), groupGap)
	place(ButtonZoomIn, toolButton, toolGap)
	left := x

	right := width - winPad
	for id := ButtonMinimize; id <= ButtonCloseWindow; id++ {
		t.rects[id] = image.Rectangle{}
	}
	if t.Controls {
		x = right - 3*winButton - 2*winGap
		right = x - winPad - toolGap
		for id := ButtonMinimize; id <= ButtonCloseWindow; id++ {
			t.rects[id] = image.Rect(x, y, x+winButton, y+winButton)
			x += winButton + winGap
		}
	}
	placeLeft := func(id ButtonID, w int) {
		t.rects[id] = image.Rect(right-w, y, right, y+toolButton)
		right -= w + toolGap
	}
	placeLeft(ButtonTheme, toolButton)
	placeLeft(ButtonPDFA, toolButton)
	right -= 4 // the find field's margin
	placeLeft(ButtonFind, min(max(right-left-groupLead, FindMinWidth), FindWidth))
	t.laid = true
}

// shown reports whether control id is on the bar.
func (t *Toolbar) shown(id ButtonID) bool {
	return !t.rects[id].Empty() && !(t.NoDocument && docOnly[id])
}

// onButton reports whether p is on a shown control, enabled or not.
func (t *Toolbar) onButton(p image.Point) bool {
	for id := range buttonCount {
		if t.shown(id) && p.In(t.rects[id]) {
			return true
		}
	}
	return false
}

// hitTest returns the enabled control at p, or -1.
func (t *Toolbar) hitTest(p image.Point) ButtonID {
	for id := range buttonCount {
		if t.shown(id) && p.In(t.rects[id]) && !t.Disabled[id] {
			return id
		}
	}
	return -1
}

// HandleEvent applies a pointer event given in the toolbar's own coordinates.
// A left release over the control it was pressed on returns ActionButton,
// except for the page field and the zoom drop-down, which return it on the
// press: the page field to take the keys, and the zoom drop-down to open its
// menu. On the empty part of the bar, a left press returns ActionTitlePress,
// the first move past dragSlop returns ActionMove, or ActionDragOut with Index
// -1 from the logo while a document is open, and a right press returns
// ActionMenu; these carry At set to the pointer. Other events return the zero
// Action.
func (t *Toolbar) HandleEvent(ev platform.PointerEvent) Action {
	if !t.laid {
		t.Layout(t.width)
	}
	p := image.Pt(int(ev.X), int(ev.Y))
	switch ev.Kind {
	case platform.Move:
		t.hover = t.hitTest(p)
		if g := t.grab; g != nil && (abs(p.X-g.X) > dragSlop || abs(p.Y-g.Y) > dragSlop) {
			t.grab = nil
			if g.In(t.logo) && !t.NoDocument {
				return Action{Kind: ActionDragOut, Index: -1, At: p}
			}
			return Action{Kind: ActionMove, At: p}
		}
	case platform.Leave:
		t.hover = -1
	case platform.Press:
		empty := !t.onButton(p)
		switch {
		case ev.Button == platform.Left && empty:
			t.grab = &p
			return Action{Kind: ActionTitlePress, At: p}
		case ev.Button == platform.Left:
			t.press = t.hitTest(p)
			if t.press == ButtonPage || t.press == ButtonZoomMode {
				id := t.press
				t.press = -1
				return Action{Kind: ActionButton, Index: int(id), At: p}
			}
		case ev.Button == platform.Right && empty:
			return Action{Kind: ActionMenu, At: p}
		}
	case platform.Release:
		pressed := t.press
		t.press, t.grab = -1, nil
		if ev.Button == platform.Left && pressed >= 0 && t.hitTest(p) == pressed {
			return Action{Kind: ActionButton, Index: int(pressed)}
		}
	}
	return Action{}
}

func abs(v int) int { return max(v, -v) }

// dim returns col at the opacity of a disabled control.
func dim(col color.NRGBA) color.NRGBA {
	col.A = uint8(int(col.A) * 35 / 100)
	return col
}

// Draw paints the toolbar with its top-left corner at origin. The find field's
// place is left for its owner to draw.
func (t *Toolbar) Draw(c *paint.Canvas, origin image.Point) {
	if !t.laid {
		t.Layout(t.width)
	}
	p := t.Palette
	bar := image.Rect(origin.X, origin.Y, origin.X+max(t.width, t.rects[ButtonZoomIn].Max.X+toolPad), origin.Y+theme.ToolbarHeight)
	c.Fill(bar, p.Chrome)
	c.Fill(image.Rect(bar.Min.X, bar.Max.Y-1, bar.Max.X, bar.Max.Y), p.Border)
	c.DrawImage(t.logo.Min.Add(origin), icon.Logo(logoSize))

	for id := range buttonCount {
		if !t.shown(id) || id == ButtonFind {
			continue
		}
		r := t.rects[id].Add(origin)
		radius := theme.Radius
		if isControl(id) {
			radius = winButton / 2
		}
		fg := p.Muted
		switch {
		case id == ButtonCloseWindow && t.hover == id:
			c.FillRound(r, radius, theme.CloseHover)
			fg = color.NRGBA{255, 255, 255, 255}
		case t.Disabled[id]:
			fg = dim(p.Muted)
		case t.press == id && t.hover == id, t.On[id]:
			c.FillRound(r, radius, p.Active)
			fg = p.Text
		case t.hover == id:
			c.FillRound(r, radius, p.Hover)
			fg = p.Text
		}
		switch id {
		case ButtonPage:
			t.drawPage(c, r)
		case ButtonZoomMode:
			t.drawZoom(c, r)
		case ButtonTheme:
			// The switch shows the theme it switches to.
			ic := icon.Moon
			if p.Dark {
				ic = icon.Sun
			}
			icon.Draw(c, r.Inset((toolButton-toolIcon)/2), ic, fg, 1.6)
		case ButtonMinimize, ButtonMaximize, ButtonCloseWindow:
			ic := icons[id]
			if id == ButtonMaximize && t.Maximized {
				ic = icon.WinRestore
			}
			icon.Draw(c, r.Inset((winButton-winIcon)/2), ic, fg, 1.8)
		default:
			icon.Draw(c, r.Inset((toolButton-toolIcon)/2), icons[id], fg, 1.6)
		}
	}
	if t.shown(ButtonPage) {
		r := t.pagesBox.Add(origin)
		t.Face.Draw(c, r.Min.X, r.Min.Y+(r.Dy()-t.Face.Height())/2, t.pagesText(), p.Muted)
	}
}

// drawPage draws the page field into r. It shows the page number, or while the
// field is being edited, the typed text with its caret inside a focus ring.
func (t *Toolbar) drawPage(c *paint.Canvas, r image.Rectangle) {
	p := t.Palette
	text := r.Inset(4)
	if !t.EditingPage {
		page := strconv.Itoa(t.Page)
		t.Face.Draw(c, text.Max.X-1-t.Face.Width(page), text.Min.Y+(text.Dy()-t.Face.Height())/2, page, p.Text)
		return
	}
	c.FillRound(r, theme.Radius, p.Hover)
	c.StrokeRound(r.Inset(-2), theme.Radius+2, 1, p.Accent)
	t.PageField.Draw(c, text, t.Face, p, p.Text, true)
}

// drawZoom draws the zoom drop-down into r: its text and arrow.
func (t *Toolbar) drawZoom(c *paint.Canvas, r image.Rectangle) {
	label := t.Face.Fit(t.ZoomText, r.Dx()-6-chevron-6)
	t.Face.Draw(c, r.Min.X+6, r.Min.Y+(r.Dy()-t.Face.Height())/2, label, t.Palette.Text)
	cy := r.Min.Y + (r.Dy()-chevron)/2
	icon.Draw(c, image.Rect(r.Max.X-4-chevron, cy, r.Max.X-4, cy+chevron), icon.Chevron, t.Palette.Muted, 2)
}

// ButtonRect returns where control id is, in the toolbar's own coordinates.
func (t *Toolbar) ButtonRect(id ButtonID) image.Rectangle {
	if !t.laid {
		t.Layout(t.width)
	}
	return t.rects[id]
}
