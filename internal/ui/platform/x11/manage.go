package x11

import (
	"strconv"
	"strings"

	"viewer/internal/ui/platform"
)

// _NET_WM_MOVERESIZE directions, and the source of a request made by an
// application.
const (
	moveResizeMove = 8
	sourceApp      = 1
)

// moveResizeDirs maps the edges to resize from to _NET_WM_MOVERESIZE
// directions.
var moveResizeDirs = map[platform.Edge]uint32{
	platform.EdgeTop | platform.EdgeLeft:     0,
	platform.EdgeTop:                         1,
	platform.EdgeTop | platform.EdgeRight:    2,
	platform.EdgeRight:                       3,
	platform.EdgeBottom | platform.EdgeRight: 4,
	platform.EdgeBottom:                      5,
	platform.EdgeBottom | platform.EdgeLeft:  6,
	platform.EdgeLeft:                        7,
}

// toRoot sends a client message about window to the window manager.
func (b *Backend) toRoot(window uint32, typ string, data ...uint32) {
	b.c.Send(sendEvent(b.root, maskSubstructureNotify|maskSubstructureRedirct, clientMessage(window, b.atom(typ), data...)))
}

// moveResize hands the pointer to the window manager, which moves or
// resizes the window in direction dir until the button of the latest press
// is released.
func (w *window) moveResize(dir uint32) {
	b := w.b
	if !b.supports("_NET_WM_MOVERESIZE") {
		return
	}
	b.in.mu.Lock()
	p := b.in.press
	b.in.mu.Unlock()
	// The press holds the pointer for the window; the manager needs it free.
	b.c.Send(newReq(opUngrabPointer, 0).u32(b.in.now()))
	b.toRoot(w.id, "_NET_WM_MOVERESIZE", uint32(p.rootX), uint32(p.rootY), dir, uint32(p.button), sourceApp)
}

// Move implements platform.Window with _NET_WM_MOVERESIZE.
func (w *window) Move() { w.moveResize(moveResizeMove) }

// Resize implements platform.Window with _NET_WM_MOVERESIZE.
func (w *window) Resize(edges platform.Edge) {
	if dir, ok := moveResizeDirs[edges]; ok {
		w.moveResize(dir)
	}
}

// ShowMenu implements platform.Window with _GTK_SHOW_WINDOW_MENU, which
// takes the point on the root.
func (w *window) ShowMenu(x, y int) {
	b := w.b
	if !b.supports("_GTK_SHOW_WINDOW_MENU") {
		return
	}
	b.in.mu.Lock()
	p := b.in.press
	b.in.mu.Unlock()
	b.c.Send(newReq(opUngrabPointer, 0).u32(b.in.now()))
	b.toRoot(w.id, "_GTK_SHOW_WINDOW_MENU", 0, uint32(p.rootX-p.x+x), uint32(p.rootY-p.y+y))
}

// SetMaximized implements platform.Window with _NET_WM_STATE.
func (w *window) SetMaximized(maximized bool) {
	action := uint32(0) // remove
	if maximized {
		action = 1 // add
	}
	b := w.b
	b.toRoot(w.id, "_NET_WM_STATE", action, b.atom("_NET_WM_STATE_MAXIMIZED_VERT"), b.atom("_NET_WM_STATE_MAXIMIZED_HORZ"), sourceApp)
}

// iconicState is the ICCCM state of a minimised window.
const iconicState = 3

// Minimize implements platform.Window with WM_CHANGE_STATE.
func (w *window) Minimize() { w.b.toRoot(w.id, "WM_CHANGE_STATE", iconicState) }

// Activate implements platform.Window with _NET_ACTIVE_WINDOW. token is a
// startup notification ID, whose launch it also ends; its timestamp, when it
// has one, lets the window manager weigh the request against what the user
// did since. Without a token the request cites the latest event's time.
func (w *window) Activate(token string) {
	b := w.b
	t := b.in.now()
	if token != "" {
		b.sendStartup("remove: ID=" + quoteStartup(token))
		if i := strings.LastIndex(token, "_TIME"); i >= 0 {
			if n, err := strconv.ParseUint(token[i+5:], 10, 32); err == nil {
				t = uint32(n)
			}
		}
	}
	b.toRoot(w.id, "_NET_ACTIVE_WINDOW", sourceApp, t, 0)
}

// endStartup ends the startup notification of the launch, if any, which the
// first window shown completes.
func (b *Backend) endStartup() {
	b.startupMu.Lock()
	id := b.startupID
	b.startupID = ""
	b.startupMu.Unlock()
	if id != "" {
		b.sendStartup("remove: ID=" + quoteStartup(id))
	}
}

// quoteStartup quotes a value of a startup notification message.
func quoteStartup(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

// sendStartup broadcasts a startup notification message: a NUL-terminated
// string in 20-byte pieces, the first of type _NET_STARTUP_INFO_BEGIN and
// the rest _NET_STARTUP_INFO.
func (b *Backend) sendStartup(msg string) {
	data := append([]byte(msg), 0)
	typ := b.atom("_NET_STARTUP_INFO_BEGIN")
	for len(data) > 0 {
		ev := clientMessage(b.selWin, typ)
		ev[1] = 8
		n := copy(ev[12:], data)
		data = data[n:]
		b.c.Send(sendEvent(b.root, maskPropertyChange, ev))
		typ = b.atom("_NET_STARTUP_INFO")
	}
}

// SetCursor implements platform.Window. The server shows the cursor
// whenever the pointer is over the window.
func (w *window) SetCursor(c platform.Cursor) {
	w.mu.Lock()
	same := w.cursor == c
	w.cursor = c
	w.mu.Unlock()
	if !same {
		b := w.b
		b.c.Send(changeAttrs(w.id, (&windowAttrs{}).set(cwCursor, b.cursors.get(b, c))))
	}
}
