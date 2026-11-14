package app

import (
	"image"
	"time"

	"viewer/internal/ui/platform"
	"viewer/internal/ui/widget"
)

// Window frame geometry, in pixels, for a window the compositor does not
// decorate: the band along its edges that resizes it, and how far a corner
// reaches along each edge.
const (
	frameBorder = 5
	frameCorner = 16
)

// doubleClick is the longest gap between the presses of a double click on the
// title bar.
const doubleClick = 400 * time.Millisecond

// framed reports whether the window draws its own frame: the compositor does
// not decorate it, and it is not maximised, so its edges can be dragged.
func (a *App) framed() bool { return !a.decorated && !a.maximized }

// edgeAt returns the edges of the window frame at p, or none away from them.
// Near a corner, along either of its edges, p is on the corner.
func (a *App) edgeAt(p image.Point) platform.Edge {
	w, h := a.size.W, a.size.H
	if !a.framed() || !p.In(image.Rect(0, 0, w, h)) {
		return 0
	}
	side := func(v, size, reach int, low, high platform.Edge) platform.Edge {
		switch {
		case v < reach:
			return low
		case v >= size-reach:
			return high
		}
		return 0
	}
	x := side(p.X, w, frameBorder, platform.EdgeLeft, platform.EdgeRight)
	y := side(p.Y, h, frameBorder, platform.EdgeTop, platform.EdgeBottom)
	if x != 0 && y == 0 {
		y = side(p.Y, h, frameCorner, platform.EdgeTop, platform.EdgeBottom)
	}
	if y != 0 && x == 0 {
		x = side(p.X, w, frameCorner, platform.EdgeLeft, platform.EdgeRight)
	}
	return x | y
}

// setCursor shows c over the window, when it is not already shown.
func (a *App) setCursor(c platform.Cursor) {
	if c != a.cursor {
		a.cursor = c
		a.win.SetCursor(c)
	}
}

// grabbed hands the pointer to the compositor, for a move, a resize or its
// window menu. The release of the press that began it goes to the compositor,
// so no section keeps the pointer.
func (a *App) grabbed() { a.capture = sectionNone }

// frameAction resizes the window from the edges under a left press.
func (a *App) frameAction(ev platform.PointerEvent) {
	if ev.Kind == platform.Press && ev.Button == platform.Left {
		if edges := a.edgeAt(image.Pt(int(ev.X), int(ev.Y))); edges != 0 {
			a.win.Resize(edges)
			a.grabbed()
		}
	}
}

// titleAction carries out what the toolbar asked for as the title bar: a
// double click maximises or restores, a drag moves, a right click opens the
// window menu.
func (a *App) titleAction(act widget.Action) {
	switch act.Kind {
	case widget.ActionTitlePress:
		now := a.opts.Now()
		if now.Sub(a.titlePress) < doubleClick {
			a.titlePress = time.Time{}
			a.win.SetMaximized(!a.maximized)
			return
		}
		a.titlePress = now
	case widget.ActionMove:
		a.win.Move()
		a.grabbed()
	case widget.ActionMenu:
		o := a.origin(sectionToolbar)
		a.win.ShowMenu(o.X+act.At.X, o.Y+act.At.Y)
		a.grabbed()
	}
}

// controlAction carries out a click on a window control, reporting whether it
// was one.
func (a *App) controlAction(id widget.ButtonID) bool {
	switch id {
	case widget.ButtonMinimize:
		a.win.Minimize()
	case widget.ButtonMaximize:
		a.win.SetMaximized(!a.maximized)
	case widget.ButtonCloseWindow:
		a.quitting = true
	default:
		return false
	}
	return true
}
