// Package widget holds the viewer's interface elements. Widgets lay
// themselves out in their own coordinates, draw at an offset, and return what
// the user asked for instead of changing the application.
package widget

import (
	"image"
	"image/color"
	"math"

	"viewer/internal/ui/icon"
	"viewer/internal/ui/paint"
	"viewer/internal/ui/platform"
	"viewer/internal/ui/text"
	"viewer/internal/ui/theme"
)

// ActionKind says what the user did to a TabBar.
type ActionKind int

// Tab bar actions.
const (
	ActionNone       ActionKind = iota
	ActionSelect                // the tab at Index was clicked
	ActionClose                 // the tab at Index asked to be closed
	ActionNew                   // the new-tab button was clicked
	ActionReorder               // the tab at Index was dragged to position To
	ActionButton                // the toolbar button at Index was clicked
	ActionTitlePress            // the left button went down on the title bar at At
	ActionMove                  // the title bar is being dragged: move the window
	ActionMenu                  // the title bar was right-clicked at At
)

// Action is the result of an event handled by a widget.
type Action struct {
	Kind  ActionKind
	Index int
	To    int
	At    image.Point // where the pointer was, in the widget's coordinates
}

// Tab geometry, in pixels.
const (
	tabPadLeft  = 12
	tabPadRight = 6
	tabGap      = 4
	closeSize   = 18
	tabMinWidth = 48
	tabMaxWidth = 220
	newWidth    = 30
	dragSlop    = 4
)

// TabBar is a row of tabs with close buttons and a new-tab button.
type TabBar struct {
	Palette theme.Palette
	Face    *text.Face
	// Titles are the tab titles, in order. Active is the selected tab, or -1.
	Titles []string
	Active int

	tabs    []image.Rectangle // laid out in the bar's own coordinates
	newTab  image.Rectangle
	width   int
	hover   hit
	press   int // the tab a button went down on, or -1
	pressX  int
	drag    bool
	dropAt  int // the gap, 0..len(Titles), a dragged tab would drop into
	hasHits bool
}

// hit is where the pointer is over the bar.
type hit struct {
	tab   int // -1 for none
	close bool
	plus  bool
}

var noHit = hit{tab: -1}

// NewTabBar returns a tab bar using palette and face.
func NewTabBar(palette theme.Palette, face *text.Face) *TabBar {
	return &TabBar{Palette: palette, Face: face, Active: -1, hover: noHit, press: -1}
}

// Layout sizes the tabs to fit width pixels: each as wide as its title wants,
// within limits, shrinking together when the bar is full.
func (b *TabBar) Layout(width int) {
	b.width = width
	b.tabs = b.tabs[:0]
	natural := make([]int, len(b.Titles))
	total := 0
	for i, title := range b.Titles {
		w := tabPadLeft + b.Face.Width(title) + tabGap + closeSize + tabPadRight
		natural[i] = min(max(w, tabMinWidth), tabMaxWidth)
		total += natural[i]
	}
	if avail := width - newWidth; total > avail && total > 0 {
		scale := math.Max(float64(avail), 0) / float64(total)
		for i := range natural {
			natural[i] = max(int(float64(natural[i])*scale), tabMinWidth)
		}
	}
	x := 0
	for _, w := range natural {
		b.tabs = append(b.tabs, image.Rect(x, 0, x+w, theme.TabBarHeight))
		x += w
	}
	b.newTab = image.Rect(x, 0, x+newWidth, theme.TabBarHeight)
	b.hasHits = true
}

// Height returns the bar's height.
func (b *TabBar) Height() int { return theme.TabBarHeight }

// closeRect is the close button inside tab r.
func closeRect(r image.Rectangle) image.Rectangle {
	x1 := r.Max.X - tabPadRight
	cy := (r.Min.Y + r.Max.Y) / 2
	return image.Rect(x1-closeSize, cy-closeSize/2, x1, cy+closeSize/2)
}

// hitTest returns what is at p.
func (b *TabBar) hitTest(p image.Point) hit {
	for i, r := range b.tabs {
		if p.In(r) {
			return hit{tab: i, close: p.In(closeRect(r))}
		}
	}
	if p.In(b.newTab) {
		return hit{tab: -1, plus: true}
	}
	return noHit
}

// gapAt returns the drop gap nearest to x: before the first tab whose middle
// lies right of x, or after the last.
func (b *TabBar) gapAt(x int) int {
	for i, r := range b.tabs {
		if x < (r.Min.X+r.Max.X)/2 {
			return i
		}
	}
	return len(b.tabs)
}

// HandleEvent applies a pointer event, in the bar's own coordinates, and
// returns what it asks for.
func (b *TabBar) HandleEvent(ev platform.PointerEvent) Action {
	p := image.Pt(int(ev.X), int(ev.Y))
	switch ev.Kind {
	case platform.Move:
		b.hover = b.hitTest(p)
		if b.press >= 0 && !b.drag && max(p.X-b.pressX, b.pressX-p.X) > dragSlop {
			b.drag = true
		}
		if b.drag {
			b.dropAt = b.gapAt(p.X)
		}
	case platform.Leave:
		b.hover = noHit
	case platform.Press:
		h := b.hitTest(p)
		switch {
		case h.plus && ev.Button == platform.Left:
			return Action{Kind: ActionNew}
		case h.tab < 0:
		case ev.Button == platform.Middle || (ev.Button == platform.Left && h.close):
			return Action{Kind: ActionClose, Index: h.tab}
		case ev.Button == platform.Left:
			b.press, b.pressX, b.drag, b.dropAt = h.tab, p.X, false, h.tab
			return Action{Kind: ActionSelect, Index: h.tab}
		}
	case platform.Release:
		from, dragged := b.press, b.drag
		b.press, b.drag = -1, false
		if ev.Button == platform.Left && dragged && from >= 0 {
			// Dropping into gap g puts the tab at g, or g-1 once it has left
			// its old place to the left of the gap.
			to := b.dropAt
			if to > from {
				to--
			}
			if to != from {
				return Action{Kind: ActionReorder, Index: from, To: to}
			}
		}
	}
	return Action{}
}

// Dragging reports whether a tab is being dragged within the bar.
func (b *TabBar) Dragging() bool { return b.drag }

// Draw paints the bar with its top-left corner at origin.
func (b *TabBar) Draw(c *paint.Canvas, origin image.Point) {
	if !b.hasHits {
		b.Layout(b.width)
	}
	p := b.Palette
	at := func(r image.Rectangle) image.Rectangle { return r.Add(origin) }
	bar := image.Rect(0, 0, max(b.width, b.newTab.Max.X), theme.TabBarHeight)
	c.Fill(at(bar), p.Chrome)
	// The line under the tabs; the active tab covers it to open into the page.
	c.Fill(at(image.Rect(bar.Min.X, bar.Max.Y-1, bar.Max.X, bar.Max.Y)), p.Border)

	textY := (theme.TabBarHeight - b.Face.Height()) / 2
	for i, r := range b.tabs {
		fg := p.Muted
		switch {
		case i == b.Active:
			c.Fill(at(image.Rect(r.Min.X, r.Min.Y, r.Max.X-1, r.Max.Y)), p.Canvas)
			fg = p.Text
		case i == b.hover.tab:
			fg = p.Text
		}
		c.Fill(at(image.Rect(r.Max.X-1, r.Min.Y, r.Max.X, r.Max.Y-1)), p.Border)
		if b.drag && i == b.press {
			c.Fill(at(r), p.Hover)
		}

		cr := closeRect(r)
		title := b.Face.Fit(b.Titles[i], cr.Min.X-tabGap-(r.Min.X+tabPadLeft))
		b.Face.Draw(c, origin.X+r.Min.X+tabPadLeft, origin.Y+textY, title, fg)
		if i == b.Active || i == b.hover.tab {
			if i == b.hover.tab && b.hover.close {
				c.Fill(at(cr), p.Hover)
			}
			b.drawGlyph(c, at(cr), "×", p.Muted)
		}
	}
	// The new-tab button is a plus in a round-cornered square, as the toolbar's
	// buttons are.
	nb := at(newButton(b.newTab))
	fg := p.Muted
	if b.hover.plus {
		c.FillRound(nb, theme.Radius, p.Hover)
		fg = p.Text
	}
	icon.Draw(c, nb.Inset((toolButton-toolIcon)/2), icon.Plus, fg, 1.6)

	if b.drag {
		x := b.newTab.Min.X
		if b.dropAt < len(b.tabs) {
			x = b.tabs[b.dropAt].Min.X
		}
		c.Fill(at(image.Rect(x-1, 3, x+1, theme.TabBarHeight-3)), p.Accent)
	}
}

// newButton returns the square the new-tab button is drawn in, within its
// place r on the bar: toolButton pixels, from the bar's gap after the tabs.
func newButton(r image.Rectangle) image.Rectangle {
	y := r.Min.Y + (r.Dy()-toolButton)/2
	return image.Rect(r.Min.X+tabGap, y, r.Min.X+tabGap+toolButton, y+toolButton)
}

// drawGlyph draws s centred in r.
func (b *TabBar) drawGlyph(c *paint.Canvas, r image.Rectangle, s string, col color.NRGBA) {
	x := r.Min.X + (r.Dx()-b.Face.Width(s))/2
	y := r.Min.Y + (r.Dy()-b.Face.Height())/2
	b.Face.Draw(c, x, y, s, col)
}

// TabRect returns where tab i is, in the bar's own coordinates, or the empty
// rectangle for an index outside the tabs.
func (b *TabBar) TabRect(i int) image.Rectangle {
	if i < 0 || i >= len(b.tabs) {
		return image.Rectangle{}
	}
	return b.tabs[i]
}

// CloseRect returns where the close button of tab i is, or the empty
// rectangle for an index outside the tabs.
func (b *TabBar) CloseRect(i int) image.Rectangle {
	if i < 0 || i >= len(b.tabs) {
		return image.Rectangle{}
	}
	return closeRect(b.tabs[i])
}

// NewTabRect returns where the new-tab button is.
func (b *TabBar) NewTabRect() image.Rectangle { return b.newTab }
