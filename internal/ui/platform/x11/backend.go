package x11

import (
	"errors"
	"net"
	"os"
	"slices"
	"sync"
	"viewer/internal/ui/platform/desktop"

	"viewer/internal/ui/platform"
)

// Event codes of the core protocol.
const (
	evKeyPress         = 2
	evKeyRelease       = 3
	evButtonPress      = 4
	evButtonRelease    = 5
	evMotionNotify     = 6
	evEnterNotify      = 7
	evLeaveNotify      = 8
	evFocusIn          = 9
	evFocusOut         = 10
	evExpose           = 12
	evMapNotify        = 19
	evConfigureNotify  = 22
	evPropertyNotify   = 28
	evSelectionClear   = 29
	evSelectionRequest = 30
	evSelectionNotify  = 31
	evClientMessage    = 33
	evMappingNotify    = 34
)

// atomNames are the atoms the backend interns when it starts.
var atomNames = []string{
	"WM_PROTOCOLS", "WM_DELETE_WINDOW", "WM_CHANGE_STATE", "WM_CLIENT_MACHINE",
	"UTF8_STRING", "_NET_WM_NAME", "_NET_WM_PID", "_NET_WM_PING", "_NET_WM_ICON",
	"_NET_WM_STATE", "_NET_WM_STATE_MAXIMIZED_VERT", "_NET_WM_STATE_MAXIMIZED_HORZ",
	"_NET_WM_STATE_FULLSCREEN", "_NET_WM_MOVERESIZE", "_NET_ACTIVE_WINDOW",
	"_NET_SUPPORTED", "_NET_SUPPORTING_WM_CHECK", "_NET_WM_WINDOW_TYPE",
	"_NET_WM_WINDOW_TYPE_NORMAL", "_NET_WM_WINDOW_TYPE_DND", "_NET_STARTUP_ID",
	"_NET_STARTUP_INFO_BEGIN", "_NET_STARTUP_INFO", "_MOTIF_WM_HINTS", "_GTK_SHOW_WINDOW_MENU",
	"CLIPBOARD", "TARGETS", "TEXT", "INCR", "text/plain;charset=utf-8", "text/plain",
	"VIEWER_SELECTION", "XdndAware", "XdndEnter", "XdndPosition", "XdndStatus", "XdndLeave",
	"XdndDrop", "XdndFinished", "XdndSelection", "XdndTypeList", "XdndActionCopy",
	desktop.URIListMime,
}

// eventQueue is how many events a window buffers before it starts dropping
// the newest, which keeps a stalled application from blocking dispatch.
const eventQueue = 1024

// Backend is a platform.Platform that opens windows on an X server.
type Backend struct {
	c      *Conn
	screen Screen
	root   uint32
	atoms  map[string]uint32

	// The visual windows are made with, its depth, the colormap for it, and
	// how its pixels are laid out.
	visual   uint32
	depth    byte
	colormap uint32
	pixels   pixelFormat

	// argb is a 32-bit visual with alpha, for the drag icon, with its
	// colormap; zero when the server has none.
	argb         uint32
	argbColormap uint32
	argbPixels   pixelFormat

	shm    extension // MIT-SHM, usable only with descriptor passing
	shape  extension
	render extension
	// argbPict is RENDER's 32-bit ARGB picture format, for cursors.
	argbPict uint32

	// wm holds what the window manager supports, from _NET_SUPPORTED;
	// noWM is set when there is none at all.
	wm   []uint32
	noWM bool

	// selWin is an unmapped window that owns the clipboard and receives
	// pasted text and startup messages.
	selWin uint32

	in      input
	clip    clipboard
	drop    xdndTarget
	drag    drag
	cursors cursors

	// startupID is the startup notification the launch began with, which the
	// first window to map ends.
	startupMu sync.Mutex
	startupID string

	mu      sync.Mutex
	windows map[uint32]*window
	done    chan struct{}
}

// ErrVisual is returned when the screen has no visual the client can draw
// with: 24-bit TrueColor at 32 bits per pixel.
var ErrVisual = errors.New("x11: the screen has no 24-bit TrueColor visual")

// Open starts a backend on conn and begins dispatching events. Backend.Close
// ends it.
func Open(conn *Conn) (*Backend, error) {
	b := &Backend{c: conn, screen: conn.Setup.Screens[0], windows: map[uint32]*window{}, done: make(chan struct{})}
	b.root = b.screen.Root
	if err := b.pickVisual(); err != nil {
		return nil, err
	}
	var err error
	if b.atoms, err = conn.internAtoms(atomNames); err != nil {
		return nil, err
	}
	if err := b.queryExtensions(); err != nil {
		return nil, err
	}
	b.queryWM()
	if err := b.in.loadKeymap(conn); err != nil {
		return nil, err
	}
	b.selWin = conn.NewID()
	a := (&windowAttrs{}).set(cwOverrideRedirect, 1).set(cwEventMask, maskPropertyChange)
	r := newReq(opCreateWindow, 0).u32(b.selWin).u32(b.root).i16(-10).i16(-10).u16(1).u16(1).u16(0).u16(2).u32(0)
	a.put(r)
	if err := conn.Send(r); err != nil {
		return nil, err
	}
	b.cursors.init(b)
	b.startupID = os.Getenv("DESKTOP_STARTUP_ID")
	os.Unsetenv("DESKTOP_STARTUP_ID") // not for the programs the viewer starts
	go b.run()
	return b, nil
}

// New connects to the server named by $DISPLAY and opens a Backend on it.
func New() (*Backend, error) {
	conn, err := Dial()
	if err != nil {
		return nil, err
	}
	b, err := Open(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return b, nil
}

// Close disconnects from the server, which ends every window.
func (b *Backend) Close() error {
	err := b.c.Close()
	<-b.done
	return err
}

// atom returns an interned atom.
func (b *Backend) atom(name string) uint32 { return b.atoms[name] }

// Visual classes.
const visualTrueColor = 4

// pickVisual chooses the visual windows are made with: the root's if it is
// 24-bit TrueColor, or else another 24-bit TrueColor visual. It also looks for
// a 32-bit visual with alpha for the drag icon.
func (b *Backend) pickVisual() error {
	bpp := map[byte]byte{}
	for _, f := range b.c.Setup.Formats {
		bpp[f.Depth] = f.BitsPerPixel
	}
	usable := func(v Visual) bool {
		return v.Class == visualTrueColor && bpp[v.Depth] == 32 && v.Depth == 24
	}
	var chosen *Visual
	for i, v := range b.screen.Visuals {
		if !usable(v) {
			continue
		}
		if chosen == nil || v.ID == b.screen.RootVisual {
			chosen = &b.screen.Visuals[i]
		}
	}
	if chosen == nil {
		return ErrVisual
	}
	pf, ok := newPixelFormat(*chosen, b.c.Setup.ImageByteOrder)
	if !ok {
		return ErrVisual
	}
	b.visual, b.depth, b.pixels, b.colormap = chosen.ID, chosen.Depth, pf, b.screen.Colormap
	if chosen.ID != b.screen.RootVisual {
		b.colormap = b.c.NewID()
		if err := b.c.Send(newReq(opCreateColormap, 0).u32(b.colormap).u32(b.root).u32(chosen.ID)); err != nil {
			return err
		}
	}
	for _, v := range b.screen.Visuals {
		if v.Class == visualTrueColor && v.Depth == 32 && bpp[32] == 32 {
			if pf, ok := newPixelFormat(v, b.c.Setup.ImageByteOrder); ok {
				b.argb, b.argbPixels = v.ID, pf
				b.argbColormap = b.c.NewID()
				return b.c.Send(newReq(opCreateColormap, 0).u32(b.argbColormap).u32(b.root).u32(v.ID))
			}
		}
	}
	return nil
}

// queryExtensions looks up the extensions the backend can use.
func (b *Backend) queryExtensions() error {
	exts, err := b.c.queryExtensions("MIT-SHM", "SHAPE", "RENDER", "XKEYBOARD")
	if err != nil {
		return err
	}
	b.shm, b.shape, b.render = exts[0], exts[1], exts[2]
	if exts[3].present {
		b.detectableRepeat(exts[3].major)
	}
	// Shared memory segments are passed as file descriptors, which needs
	// MIT-SHM 1.2 or later and a Unix socket.
	if _, local := b.c.sock.(*net.UnixConn); b.shm.present && (!local || !b.shmFDs()) {
		b.shm.present = false
	}
	if b.render.present {
		b.argbPict = b.renderARGB()
		b.render.present = b.argbPict != 0
	}
	return nil
}

// XKB minor opcodes, and the per-client flag that has the server report a
// held key's repeats as presses alone, without a release before each.
const (
	xkbUseExtension     = 0
	xkbPerClientFlags   = 21
	xkbUseCoreKeyboard  = 0x100
	xkbDetectableRepeat = 1
)

// detectableRepeat asks XKB to report a held key's repeats as presses
// alone. Where the server refuses, onKey drops the extra releases itself.
func (b *Backend) detectableRepeat(major byte) {
	rep, err := b.c.Call(newReq(major, xkbUseExtension).u16(1).u16(0))
	if err != nil || rep[1] == 0 {
		return
	}
	b.c.Call(newReq(major, xkbPerClientFlags).u16(xkbUseCoreKeyboard).pad(2).
		u32(xkbDetectableRepeat).u32(xkbDetectableRepeat).u32(0).u32(0).u32(0))
}

// shmFDs reports whether MIT-SHM is at least version 1.2.
func (b *Backend) shmFDs() bool {
	rep, err := b.c.Call(newReq(b.shm.major, shmQueryVersion))
	return err == nil && (u16At(rep, 8) > 1 || u16At(rep, 8) == 1 && u16At(rep, 10) >= 2)
}

// queryWM reads what the window manager supports. There is a manager when
// the root names a check window that names itself.
func (b *Backend) queryWM() {
	check := b.atom("_NET_SUPPORTING_WM_CHECK")
	p, err := b.c.getProperty(b.root, check, false, 4)
	ids := p.u32s()
	if err != nil || len(ids) == 0 {
		b.noWM = true
		return
	}
	p, err = b.c.getProperty(ids[0], check, false, 4)
	if self := p.u32s(); err != nil || len(self) == 0 || self[0] != ids[0] {
		b.noWM = true
		return
	}
	p, _ = b.c.getProperty(b.root, b.atom("_NET_SUPPORTED"), false, 1<<16)
	b.wm = p.u32s()
}

// supports reports whether the window manager supports the hint name.
func (b *Backend) supports(name string) bool { return slices.Contains(b.wm, b.atom(name)) }

// clientDecorated reports whether windows draw their own title bar and
// frame. They do when the window manager can move and resize them on
// request, or when there is no manager to draw a frame; otherwise the
// manager's frame stays, since without it the window could not be moved.
func (b *Backend) clientDecorated() bool {
	return b.noWM || b.supports("_NET_WM_MOVERESIZE")
}

// run dispatches events until the connection ends, then closes the windows.
func (b *Backend) run() {
	defer close(b.done)
	for {
		ev, err := b.c.Next()
		if err != nil {
			break
		}
		b.handle(ev)
	}
	b.mu.Lock()
	ws := make([]*window, 0, len(b.windows))
	for _, w := range b.windows {
		ws = append(ws, w)
	}
	b.mu.Unlock()
	for _, w := range ws {
		w.shutdown()
	}
}

// windowFor returns the window with the given ID, or nil.
func (b *Backend) windowFor(id uint32) *window {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.windows[id]
}

// handle applies one event.
func (b *Backend) handle(ev []byte) {
	code := ev[0] &^ 0x80
	if b.shm.present && code == b.shm.firstEvent { // ShmCompletion, which is never asked for
		return
	}
	switch code {
	case evKeyPress, evKeyRelease:
		b.onKey(ev)
	case evButtonPress, evButtonRelease, evMotionNotify:
		b.onPointer(ev)
	case evEnterNotify, evLeaveNotify:
		b.onCrossing(ev)
	case evFocusIn, evFocusOut:
		b.onFocus(ev)
	case evExpose:
		if w := b.windowFor(u32At(ev, 4)); w != nil {
			w.Invalidate()
		}
	case evMapNotify:
		if w := b.windowFor(u32At(ev, 8)); w != nil {
			w.onMap()
		}
	case evConfigureNotify:
		if w := b.windowFor(u32At(ev, 8)); w != nil {
			w.onConfigure(int(u16At(ev, 20)), int(u16At(ev, 22)))
		}
	case evPropertyNotify:
		b.onProperty(ev)
	case evSelectionClear:
		b.onSelectionClear(ev)
	case evSelectionRequest:
		b.onSelectionRequest(ev)
	case evSelectionNotify:
		b.onSelectionNotify(ev)
	case evClientMessage:
		b.onClientMessage(ev)
	case evMappingNotify:
		if ev[4] != 2 { // the pointer's mapping does not matter
			b.in.loadKeymap(b.c)
		}
	}
}

// onProperty handles a property change: the window manager's state of a
// window, or a step of a clipboard transfer.
func (b *Backend) onProperty(ev []byte) {
	window, prop, deleted := u32At(ev, 4), u32At(ev, 8), ev[16] == 1
	b.in.setTime(u32At(ev, 12))
	if w := b.windowFor(window); w != nil && prop == b.atom("_NET_WM_STATE") {
		w.readState()
		return
	}
	b.clipProperty(window, prop, deleted)
}

// onClientMessage handles the window manager's protocol messages and the
// XDND messages of a drag from another program.
func (b *Backend) onClientMessage(ev []byte) {
	window, typ := u32At(ev, 4), u32At(ev, 8)
	w := b.windowFor(window)
	if w == nil {
		return
	}
	data := func(i int) uint32 { return u32At(ev, 12+4*i) }
	switch typ {
	case b.atom("WM_PROTOCOLS"):
		switch data(0) {
		case b.atom("WM_DELETE_WINDOW"):
			w.emit(platform.CloseEvent{})
		case b.atom("_NET_WM_PING"):
			reply := slices.Clone(ev)
			order.PutUint32(reply[4:], b.root)
			b.c.Send(sendEvent(b.root, maskSubstructureNotify|maskSubstructureRedirect, reply))
		}
	default:
		b.drop.onMessage(b, w, typ, data)
	}
}
