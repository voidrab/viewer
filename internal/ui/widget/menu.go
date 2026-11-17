package widget

import (
	"image"

	"viewrab/internal/ui/icon"
	"viewrab/internal/ui/paint"
	"viewrab/internal/ui/platform"
	"viewrab/internal/ui/text"
	"viewrab/internal/ui/theme"
)

// Menu geometry, in pixels.
const (
	menuPad   = 4  // around the items
	menuItem  = 26 // an item's height
	menuCheck = 22 // room for the check mark left of the labels
	menuRight = 16 // right of the labels
	menuGap   = 32 // between a label and its shortcut
)

// menuShadow is the opacity of each ring of the shadow under a menu.
var menuShadow = []uint8{28, 14, 6}

// Menu is a drop-down list of choices, such as the zoom presets, shown below
// the control that opened it and drawn over everything else. Its coordinates
// are the window's.
type Menu struct {
	Palette theme.Palette
	Face    *text.Face

	// Items are the labels of the choices, and Checked the one in use, or -1.
	Items   []string
	Checked int
	// Shortcuts, when set, are the key shortcuts shown right of the items,
	// and Disabled, when set, the items that cannot be chosen now.
	Shortcuts []string
	Disabled  []bool
	// Open is set while the menu shows.
	Open bool

	anchor  image.Rectangle // the control the menu drops from
	limit   image.Rectangle // what the menu must stay inside
	hover   int             // the highlighted item, or -1
	swallow bool            // the release of the press that closed the menu is still to come
}

// NewMenu returns a closed menu using palette and face.
func NewMenu(palette theme.Palette, face *text.Face) *Menu {
	return &Menu{Palette: palette, Face: face, Checked: -1, hover: -1}
}

// shortcut returns the shortcut of item i, or "".
func (m *Menu) shortcut(i int) string {
	if i < len(m.Shortcuts) {
		return m.Shortcuts[i]
	}
	return ""
}

// enabled reports whether item i can be chosen.
func (m *Menu) enabled(i int) bool { return i >= 0 && (i >= len(m.Disabled) || !m.Disabled[i]) }

// Show opens the menu below anchor, kept inside limit, with the checked item
// highlighted.
func (m *Menu) Show(anchor, limit image.Rectangle) {
	m.Open, m.anchor, m.limit, m.hover = true, anchor, limit, m.Checked
}

// Close closes the menu.
func (m *Menu) Close() { m.Open, m.hover = false, -1 }

// Rect returns where the open menu is: below its anchor, at least as wide, and
// moved left or up as far as it takes to stay inside its limit.
func (m *Menu) Rect() image.Rectangle {
	w, keys := 0, 0
	for i, it := range m.Items {
		w = max(w, m.Face.Width(it))
		keys = max(keys, m.Face.Width(m.shortcut(i)))
	}
	if keys > 0 {
		w += menuGap + keys
	}
	w = max(w+menuCheck+menuRight+2*menuPad, m.anchor.Dx())
	h := len(m.Items)*menuItem + 2*menuPad
	r := image.Rect(m.anchor.Min.X, m.anchor.Max.Y+2, m.anchor.Min.X+w, m.anchor.Max.Y+2+h)
	if d := r.Max.X - m.limit.Max.X; d > 0 {
		r = r.Sub(image.Pt(min(d, r.Min.X-m.limit.Min.X), 0))
	}
	if d := r.Max.Y - m.limit.Max.Y; d > 0 {
		r = r.Sub(image.Pt(0, min(d, r.Min.Y-m.limit.Min.Y)))
	}
	return r
}

// menuRow returns where item i is in the menu at r.
func menuRow(r image.Rectangle, i int) image.Rectangle {
	y := r.Min.Y + menuPad + i*menuItem
	return image.Rect(r.Min.X+menuPad, y, r.Max.X-menuPad, y+menuItem)
}

// itemAt returns the item at p, or -1.
func (m *Menu) itemAt(p image.Point) int {
	r := m.Rect()
	for i := range m.Items {
		if p.In(menuRow(r, i)) {
			return i
		}
	}
	return -1
}

// Draw paints the menu if it is open.
func (m *Menu) Draw(c *paint.Canvas) {
	if !m.Open {
		return
	}
	p := m.Palette
	r := m.Rect()
	c.Shadow(r, 6, 1, menuShadow)
	c.FillRound(r, 6, p.Raised)
	c.StrokeRound(r, 6, 1, p.Border)
	for i, label := range m.Items {
		ir := menuRow(r, i)
		fg := p.Text
		if !m.enabled(i) {
			fg = p.Faint
		} else if i == m.hover {
			c.FillRound(ir, theme.Radius, p.Selection)
		}
		if i == m.Checked {
			fg = p.Accent
			cy := ir.Min.Y + (ir.Dy()-14)/2
			icon.Draw(c, image.Rect(ir.Min.X+5, cy, ir.Min.X+19, cy+14), icon.Check, p.Accent, 2)
		}
		ty := ir.Min.Y + (ir.Dy()-m.Face.Height())/2
		m.Face.Draw(c, ir.Min.X+menuCheck, ty, label, fg)
		if k := m.shortcut(i); k != "" {
			m.Face.Draw(c, ir.Max.X-menuRight+menuPad-m.Face.Width(k), ty, k, p.Muted)
		}
	}
}

// HandlePointer applies a pointer event in window coordinates. While the menu
// is open it takes every event: a click on an item chooses it, returning its
// index, and a press anywhere else closes the menu. It also takes the release
// of the press that closed it. It returns -1 when nothing is chosen, and false
// when the event is not the menu's.
func (m *Menu) HandlePointer(ev platform.PointerEvent) (chosen int, taken bool) {
	if !m.Open {
		if m.swallow && ev.Kind == platform.Release {
			m.swallow = false
			return -1, true
		}
		return -1, false
	}
	at := m.itemAt(image.Pt(int(ev.X), int(ev.Y)))
	switch ev.Kind {
	case platform.Move, platform.Enter:
		if at >= 0 {
			m.hover = at
		}
	case platform.Press:
		if at < 0 {
			m.Close()
			m.swallow = true
		}
	case platform.Release:
		if m.enabled(at) && ev.Button == platform.Left {
			m.Close()
			return at, true
		}
	}
	return -1, true
}

// HandleKey applies a key press while the menu is open: the arrows, Home and
// End move the highlight, Enter or Space chooses the highlighted item, and
// Escape closes the menu. It returns the index chosen, or -1.
func (m *Menu) HandleKey(ev platform.KeyEvent) int {
	if !ev.Down {
		return -1
	}
	n := len(m.Items)
	switch {
	case ev.Key == platform.KeyDown:
		m.hover = min(m.hover+1, n-1)
	case ev.Key == platform.KeyUp:
		m.hover = max(m.hover-1, 0)
	case ev.Key == platform.KeyHome:
		m.hover = 0
	case ev.Key == platform.KeyEnd:
		m.hover = n - 1
	case ev.Key == platform.KeyEscape:
		m.Close()
	case ev.Key == platform.KeyEnter || ev.Rune == ' ':
		chosen := m.hover
		if !m.enabled(chosen) {
			return -1
		}
		m.Close()
		return chosen
	}
	return -1
}
