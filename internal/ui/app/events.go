package app

import (
	"fmt"
	"image"
	"strconv"
	"strings"

	"viewer/internal/ui/pageview"

	"viewer/internal/ui/platform"
	"viewer/internal/ui/widget"
)

// hit returns the section at p. The window's frame lies over the edges of the
// sections it borders.
func (a *App) hit(p image.Point) section {
	toolbar, tabs, content, _ := a.layout()
	switch {
	case a.edgeAt(p) != 0:
		return sectionFrame
	case p.In(a.panelRect()):
		return sectionPanel
	case p.In(toolbar):
		return sectionToolbar
	case p.In(tabs):
		return sectionTabs
	case p.In(content):
		return sectionContent
	}
	return sectionNone
}

// origin returns the origin of a section's own coordinates.
func (a *App) origin(s section) image.Point {
	toolbar, tabs, content, _ := a.layout()
	switch s {
	case sectionToolbar:
		return toolbar.Min
	case sectionTabs:
		return tabs.Min
	case sectionContent:
		return content.Min
	case sectionPanel:
		return a.panelRect().Min
	}
	return image.Point{}
}

// pointer routes a pointer event to the section it belongs to. While a button
// is held, every event goes to the section the press began in, so a tab can
// be dragged beyond the bar.
func (a *App) pointer(ev platform.PointerEvent) {
	// An open menu takes every event, and a press elsewhere closes it.
	if chosen, taken := a.menu.HandlePointer(ev); taken {
		a.choose(chosen)
		a.win.Invalidate()
		return
	}
	p := image.Pt(int(ev.X), int(ev.Y))
	// A press outside the page field ends the typing of a page number, which
	// then goes to that page, as an input does when it loses the focus.
	if ev.Kind == platform.Press && a.toolbar.EditingPage && !p.In(a.toolbar.ButtonRect(widget.ButtonPage).Add(a.origin(sectionToolbar))) {
		a.endPageEdit(true)
	}
	under := a.hit(p)
	if ev.Kind == platform.Enter {
		// The pointer comes back from elsewhere, such as from a move the
		// compositor ran, with no button held for any section.
		a.capture = sectionNone
	}
	if a.capture == sectionNone {
		cursor := platform.ResizeCursor(a.edgeAt(p))
		if ev.Kind == platform.Leave {
			cursor = platform.CursorDefault
		}
		a.setCursor(cursor)
	}
	target := under
	if a.capture != sectionNone {
		target = a.capture
	}
	// Leaving a section ends its hover.
	if a.capture == sectionNone && under != a.hover {
		a.deliver(a.hover, platform.PointerEvent{Kind: platform.Leave})
		a.hover = under
	}
	if ev.Kind == platform.Leave {
		a.deliver(a.hover, ev)
		a.hover = sectionNone
		a.win.Invalidate()
		return
	}
	if ev.Kind == platform.Press && a.capture == sectionNone {
		a.capture = under
	}
	local := ev
	o := a.origin(target)
	local.X, local.Y = ev.X-float64(o.X), ev.Y-float64(o.Y)
	a.deliver(target, local)
	if ev.Kind == platform.Release {
		a.capture = sectionNone
	}
	a.win.Invalidate()
}

// deliver passes ev, in section coordinates, to the toolbar, the tab bar, the
// page view, the panel or the frame and carries out what it asks for. The
// status section takes no pointer events.
func (a *App) deliver(s section, ev platform.PointerEvent) {
	switch s {
	case sectionFrame:
		a.frameAction(ev)
	case sectionPanel:
		a.panelAction(a.panel.HandlePointer(ev, a.panelRect().Size()))
	case sectionToolbar:
		// The find field lies on the toolbar but takes its own events.
		if a.current() != nil {
			a.syncFind() // The step buttons show only with matches.
			if act, over := a.findBar.HandlePointer(ev, a.toolbar.ButtonRect(widget.ButtonFind)); over {
				a.toolbar.HandleEvent(platform.PointerEvent{Kind: platform.Leave})
				a.findAction(act)
				return
			}
		}
		a.toolbarAction(a.toolbar.HandleEvent(ev))
	case sectionTabs:
		a.tabAction(a.tabs.HandleEvent(ev))
	case sectionContent:
		d := a.current()
		if d == nil {
			a.hintPointer(ev)
			return
		}
		d.view.HandlePointer(ev)
	}
}

// tabAction carries out what the tab bar asked for.
func (a *App) tabAction(act widget.Action) {
	switch act.Kind {
	case widget.ActionSelect:
		if act.Index >= 0 && act.Index < len(a.docs) {
			a.active = act.Index
		}
	case widget.ActionClose:
		a.closeTab(act.Index)
	case widget.ActionNew:
		a.pick()
	case widget.ActionReorder:
		a.move(act.Index, act.To)
	case widget.ActionDragOut:
		a.beginDrag(act.Index, act.At.Add(a.origin(sectionTabs)))
	}
}

// toolbarAction carries out what the toolbar asked for.
func (a *App) toolbarAction(act widget.Action) {
	if act.Kind == widget.ActionDragOut {
		a.beginDrag(act.Index, act.At.Add(a.origin(sectionToolbar)))
		return
	}
	if act.Kind != widget.ActionButton {
		a.titleAction(act)
		return
	}
	id := widget.ButtonID(act.Index)
	if a.controlAction(id) {
		return
	}
	switch id {
	case widget.ButtonOpen:
		a.pick()
		return
	case widget.ButtonFind:
		a.openFind()
		return
	case widget.ButtonPDFA:
		a.setPanel(!a.panelOpen)
		return
	case widget.ButtonTheme:
		a.toggleTheme()
		return
	}
	d := a.current()
	if d == nil {
		return
	}
	switch id {
	case widget.ButtonPrev:
		d.view.GoToPage(d.view.Page() - 1)
	case widget.ButtonNext:
		d.view.GoToPage(d.view.Page() + 1)
	case widget.ButtonZoomOut:
		d.view.ZoomOut()
	case widget.ButtonZoomIn:
		d.view.ZoomIn()
	case widget.ButtonZoomMode:
		a.openZoomMenu()
	case widget.ButtonPage:
		a.startPageEdit(act.At)
	}
}

// menuKind says which drop-down the menu belongs to.
type menuKind int

const (
	menuZoom    menuKind = iota // the zoom presets
	menuProfile                 // the PDF/A panel's profiles
)

// showMenu opens the menu of kind with items, checked at checked, below
// anchor, in window coordinates.
func (a *App) showMenu(kind menuKind, items []string, checked int, anchor image.Rectangle) {
	a.menuKind = kind
	a.menu.Items, a.menu.Checked = items, checked
	a.menu.Show(anchor, image.Rect(0, 0, a.size.W, a.size.H))
}

// openZoomMenu opens the zoom presets below the zoom drop-down.
func (a *App) openZoomMenu() {
	anchor := a.toolbar.ButtonRect(widget.ButtonZoomMode).Add(a.origin(sectionToolbar))
	a.showMenu(menuZoom, a.toolbar.ZoomChoices, a.current().view.ZoomPresetIndex(), anchor)
}

// choose carries out the choice of item i, if it is one, in the menu.
func (a *App) choose(i int) {
	if i < 0 || i >= len(a.menu.Items) {
		return
	}
	switch a.menuKind {
	case menuZoom:
		if d := a.current(); d != nil {
			d.view.SetZoomPreset(pageview.ZoomPresets[i])
		}
	case menuProfile:
		a.panel.Profile = i
	}
}

// startPageEdit gives the page field the keys, with the page number in it
// selected so that typing replaces it. If the field is already being edited,
// it instead moves the caret to the toolbar point at.
func (a *App) startPageEdit(at image.Point) {
	tb := a.toolbar
	if tb.EditingPage {
		tb.PageField.CaretAt(a.face, tb.ButtonRect(widget.ButtonPage).Inset(4), at.X)
		return
	}
	tb.EditingPage, a.pageDoc = true, a.current()
	tb.PageField.SetText(strconv.Itoa(tb.Page))
	tb.PageField.SelectAll()
}

// endPageEdit takes the keys back from the page field. With commit set, the
// view goes to the page typed, clamped to the document, unless what was typed
// is not a number.
func (a *App) endPageEdit(commit bool) {
	if !a.toolbar.EditingPage {
		return
	}
	a.toolbar.EditingPage = false
	d := a.pageDoc
	a.pageDoc = nil
	if n, err := strconv.Atoi(a.toolbar.PageField.String()); commit && d != nil && err == nil {
		d.view.GoToPage(min(max(n, 1), d.view.Pages()) - 1)
	}
}

// pageKey applies a key press to the page field and reports whether it took
// it: Enter goes to the page typed and Escape leaves the field; the field
// takes every other key except Ctrl shortcuts, Ctrl+A aside.
func (a *App) pageKey(ev platform.KeyEvent) bool {
	switch ev.Key {
	case platform.KeyEnter:
		a.endPageEdit(true)
		return true
	case platform.KeyEscape:
		a.endPageEdit(false)
		return true
	}
	if ev.Mods&platform.Ctrl != 0 && ev.Rune != 'a' {
		return false
	}
	a.toolbar.PageField.Key(ev)
	return true
}

// hintPointer handles a pointer event, in content coordinates, over the empty
// state: its open button lights up under the pointer and opens a file when
// clicked.
func (a *App) hintPointer(ev platform.PointerEvent) {
	_, _, content, _ := a.layout()
	_, _, _, button := a.hintLayout(content)
	over := image.Pt(int(ev.X), int(ev.Y)).Add(content.Min).In(button)
	a.hintHover = over && ev.Kind != platform.Leave
	if over && ev.Kind == platform.Release && ev.Button == platform.Left {
		a.pick()
	}
}

// selectTab makes tab i, wrapped around the ends, the active one.
func (a *App) selectTab(i int) {
	if n := len(a.docs); n > 0 {
		a.active = (i%n + n) % n
		a.win.Invalidate()
	}
}

// key handles shortcuts, passing everything else to the active view. It
// reports whether the shortcut asks to quit.
func (a *App) key(ev platform.KeyEvent) (quit bool) {
	if !ev.Down {
		return false
	}
	if a.menu.Open {
		a.choose(a.menu.HandleKey(ev))
		a.win.Invalidate()
		return false
	}
	if a.toolbar.EditingPage && a.pageKey(ev) {
		a.win.Invalidate()
		return false
	}
	if ev.Mods&platform.Ctrl != 0 && a.shortcut(ev) {
		return ev.Rune == 'q'
	}
	if a.findBar.Open && ev.Mods&platform.Ctrl == 0 {
		a.findAction(a.findBar.HandleKey(ev))
		a.win.Invalidate()
		// The keys that move through the document still do, with the bar open.
		switch ev.Key {
		case platform.KeyUp, platform.KeyDown, platform.KeyPageUp, platform.KeyPageDown:
		default:
			return false
		}
	}
	if d := a.current(); d != nil && d.view.HandleEvent(ev) {
		a.win.Invalidate()
		return false
	}
	// Escape that clears no selection closes the panel.
	if ev.Key == platform.KeyEscape && a.panelOpen {
		a.setPanel(false)
	}
	return false
}

// openFind shows the find bar for the active document, starting from the text
// selected in it.
func (a *App) openFind() {
	d := a.current()
	if d == nil {
		return
	}
	a.endPageEdit(false)
	if a.findBar.Open && a.findDoc == d {
		a.findBar.SelectAll()
		return
	}
	a.findDoc, a.findBar.Open = d, true
	a.findBar.SetText("")
	if sel := firstLine(d.view.SelectedText()); sel != "" {
		a.findBar.SetText(sel)
		a.findBar.SelectAll()
		d.view.Find(sel)
	}
	a.win.Invalidate()
}

// firstLine returns the first line of s, cut to at most 100 runes and trimmed
// of white space.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 100 {
		s = string(r[:100])
	}
	return strings.TrimSpace(s)
}

// closeFind hides the find bar and removes the highlights of its search.
func (a *App) closeFind() {
	if a.findDoc != nil {
		a.findDoc.view.ClearFind()
	}
	a.findDoc, a.findBar.Open = nil, false
	a.findBar.SetText("")
	a.win.Invalidate()
}

// findAction carries out what the find bar asked for.
func (a *App) findAction(act widget.FindAction) {
	if act == widget.FindOpen {
		a.openFind()
		return
	}
	if a.findDoc == nil {
		return
	}
	v := a.findDoc.view
	switch act {
	case widget.FindEdited:
		v.Find(string(a.findBar.Text))
	case widget.FindNext:
		v.FindNext()
	case widget.FindPrev:
		v.FindPrev()
	case widget.FindClose:
		a.closeFind()
	}
}

// copySelection puts the selected text on the clipboard.
func (a *App) copySelection() {
	d := a.current()
	if d == nil {
		return
	}
	text := d.view.SelectedText()
	if text == "" {
		a.say(widget.MessageInfo, "Nothing is selected")
		return
	}
	a.win.SetClipboard(text)
	a.say(widget.MessageOK, fmt.Sprintf("Copied %d characters", len([]rune(text))))
}

// shortcut runs the action of the Ctrl shortcut ev and reports whether ev is
// one. Ctrl+Q closes the other windows here; the caller closes this one.
func (a *App) shortcut(ev platform.KeyEvent) bool {
	switch {
	case ev.Rune == 'q':
		a.s.each(a, func(b *App) { b.quitting = true })
	case ev.Rune == 'o' || ev.Rune == 't':
		a.pick()
	case ev.Rune == 'w':
		a.closeTab(a.active)
	case ev.Rune == 'n':
		if err := a.s.openWindow(nil, nil); err != nil {
			a.say(widget.MessageWarn, "Could not open a window: "+err.Error())
		}
	case ev.Rune == 'f':
		a.openFind()
	case ev.Rune == 'c':
		a.copySelection()
	case ev.Rune == 'v':
		if a.findBar.Open {
			a.win.RequestPaste()
		}
	case ev.Rune >= '1' && ev.Rune <= '9':
		if i := int(ev.Rune - '1'); i < len(a.docs) {
			a.selectTab(i)
		}
	case ev.Key == platform.KeyTab && ev.Mods&platform.Shift != 0, ev.Key == platform.KeyPageUp:
		a.selectTab(a.active - 1)
	case ev.Key == platform.KeyTab, ev.Key == platform.KeyPageDown:
		a.selectTab(a.active + 1)
	default:
		return false
	}
	return true
}
