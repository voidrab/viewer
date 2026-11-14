package app

import (
	"image"

	"viewer/internal/ui/platform"
	"viewer/internal/ui/widget"
)

// hit returns the section at p.
func (a *App) hit(p image.Point) section {
	toolbar, tabs, content, _ := a.layout()
	switch {
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
	}
	return image.Point{}
}

// pointer routes a pointer event to the section it belongs to. While a button
// is held, every event goes to the section the press began in, so a tab can
// be dragged beyond the bar.
func (a *App) pointer(ev platform.PointerEvent) {
	p := image.Pt(int(ev.X), int(ev.Y))
	under := a.hit(p)
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

// deliver passes ev, in section coordinates, to the toolbar, the tab bar or
// the page view and carries out what it asks for. The status section takes no
// pointer events.
func (a *App) deliver(s section, ev platform.PointerEvent) {
	switch s {
	case sectionToolbar:
		a.toolbarAction(a.toolbar.HandleEvent(ev))
	case sectionTabs:
		a.tabAction(a.tabs.HandleEvent(ev))
	case sectionContent:
		if d := a.current(); d != nil {
			d.view.HandlePointer(ev)
		}
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
	}
}

// toolbarAction carries out what the toolbar asked for.
func (a *App) toolbarAction(act widget.Action) {
	if act.Kind != widget.ActionButton {
		return
	}
	id := widget.ButtonID(act.Index)
	switch id {
	case widget.ButtonOpen:
		a.pick()
		return
	case widget.ButtonFind:
		a.say(widget.MessageWarn, findUnavailable)
		return
	case widget.ButtonPDFA:
		a.verify()
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
		d.view.CycleZoom()
	}
}

// selectTab makes tab i, wrapped around the ends, the active one.
func (a *App) selectTab(i int) {
	if n := len(a.docs); n > 0 {
		a.active = (i%n + n) % n
		a.win.Invalidate()
	}
}

// findUnavailable is the warning shown when find is requested, which the app
// does not yet support.
const findUnavailable = "Find is not available yet"

// key handles shortcuts, passing everything else to the active view. It
// reports whether the shortcut asks to quit.
func (a *App) key(ev platform.KeyEvent) (quit bool) {
	if !ev.Down {
		return false
	}
	if ev.Mods&platform.Ctrl != 0 && a.shortcut(ev) {
		return ev.Rune == 'q'
	}
	if d := a.current(); d != nil && d.view.HandleEvent(ev) {
		a.win.Invalidate()
	}
	return false
}

// shortcut runs the action of the Ctrl shortcut ev and reports whether ev is
// one. Ctrl+Q counts as a shortcut but does nothing here; the caller quits.
func (a *App) shortcut(ev platform.KeyEvent) bool {
	switch {
	case ev.Rune == 'q':
	case ev.Rune == 'o' || ev.Rune == 't':
		a.pick()
	case ev.Rune == 'w':
		a.closeTab(a.active)
	case ev.Rune == 'n':
		a.say(widget.MessageWarn, "Several windows are not available yet")
	case ev.Rune == 'f':
		a.say(widget.MessageWarn, findUnavailable)
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
