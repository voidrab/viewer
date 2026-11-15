//go:build !windows

package wayland

import (
	"errors"
	"image"
	"sync"

	"viewrab/internal/ui/platform"
	"viewrab/internal/ui/platform/desktop"
)

// Opcodes and event numbers of the interfaces a window uses.
const (
	compositorCreateSurface = 0

	surfaceDestroy      = 0
	surfaceAttach       = 1
	surfaceFrame        = 3
	surfaceDamageBuffer = 9
	surfaceCommit       = 6

	shmCreatePool = 0

	poolCreateBuffer = 0
	poolDestroy      = 1

	bufferDestroy = 0
	bufferRelease = 0

	wmBaseGetXdgSurface = 2
	wmBasePong          = 3
	wmBasePing          = 0

	xdgSurfaceDestroy      = 0
	xdgSurfaceGetToplevel  = 1
	xdgSurfaceAckConfigure = 4
	xdgSurfaceConfigure    = 0

	decorationDestroy     = 0
	decorationGetToplevel = 1
	decorationSetMode     = 1
	decorationConfigure   = 0
	decorationModeClient  = 1
	decorationModeServer  = 2

	activationActivate = 2 // xdg_activation_v1.activate(token, surface)

	toplevelDestroy        = 0
	toplevelSetTitle       = 2
	toplevelSetAppID       = 3
	toplevelShowWindowMenu = 4
	toplevelMove           = 5
	toplevelResize         = 6
	toplevelSetMinSize     = 8
	toplevelSetMaximized   = 9
	toplevelUnsetMaximized = 10
	toplevelSetMinimized   = 13
	toplevelConfigure      = 0
	toplevelClose          = 1

	toplevelStateMaximized  = 1
	toplevelStateFullscreen = 2
)

// ErrNoGlobal is returned by Open when the compositor lacks a required
// interface.
var ErrNoGlobal = errors.New("wayland: compositor lacks a required interface")

// eventQueue is how many events a window buffers before it starts dropping
// the newest, which keeps a stalled application from blocking dispatch.
const eventQueue = 1024

// Backend is a platform.Platform that opens windows on a Wayland compositor.
type Backend struct {
	c *Client

	compositor, shm, wmBase, seat uint32
	// decoration is zxdg_decoration_manager_v1, or zero when the compositor
	// does not offer it.
	decoration uint32
	// activation is xdg_activation_v1, or zero when the compositor has none.
	activation uint32
	// dataManager is wl_data_device_manager, or zero without one: there is
	// then no clipboard.
	dataManager uint32
	dataVersion uint32 // the version of wl_data_device_manager bound
	clip        clipboard
	dnd         dnd
	// cursorShape is wp_cursor_shape_manager_v1, or zero without one: the
	// client then sets no cursor.
	cursorShape uint32

	in input

	mu      sync.Mutex
	windows map[*window]struct{}
	done    chan struct{}
}

// Open starts a backend on conn, binding wl_compositor, wl_shm and
// xdg_wm_base, and begins dispatching events. Close ends it.
func Open(conn *Conn) (*Backend, error) {
	b := &Backend{c: NewClient(conn), windows: map[*window]struct{}{}, done: make(chan struct{})}
	b.clip.mimes = map[uint32][]string{}
	b.in.repeatRate, b.in.repeatDelay = defaultRepeatRate, defaultRepeatDelay
	if err := b.c.LoadGlobals(); err != nil {
		return nil, err
	}
	var err error
	if b.compositor, _, err = b.c.Bind("wl_compositor", 4, nil); err != nil {
		return nil, errors.Join(ErrNoGlobal, err)
	}
	if b.shm, _, err = b.c.Bind("wl_shm", 1, nil); err != nil {
		return nil, errors.Join(ErrNoGlobal, err)
	}
	if b.wmBase, _, err = b.c.Bind("xdg_wm_base", 1, b.onWmBase); err != nil {
		return nil, errors.Join(ErrNoGlobal, err)
	}
	// Cursor shapes are optional. The manager is bound before the seat, whose
	// pointer needs it for its cursor device.
	b.cursorShape, _, _ = b.c.Bind("wp_cursor_shape_manager_v1", 1, nil)
	// The seat is optional: without one, windows receive no input events.
	if b.seat, _, err = b.c.Bind("wl_seat", 5, b.onSeat); err == nil {
		if err := b.c.Roundtrip(); err != nil {
			return nil, err
		}
	}
	// The clipboard, and drag and drop, are optional too, and need a seat.
	if b.seat != 0 {
		if b.dataManager, b.dataVersion, err = b.c.Bind("wl_data_device_manager", 3, nil); err == nil {
			b.clip.device = b.c.NewID(b.onDataDevice)
			b.c.Send(b.dataManager, dataManagerGetDevice, func(e *Encoder) { e.Uint(b.clip.device).Uint(b.seat) })
		}
	}
	// zxdg_decoration_manager_v1 is optional. With it, windows ask to draw
	// their own title bar, which the compositor may refuse. Without it,
	// windows make no request and the compositor's default applies.
	b.decoration, _, _ = b.c.Bind("zxdg_decoration_manager_v1", 1, nil)
	// xdg_activation_v1 lets a window ask to be brought forward. It is
	// optional: without it, Activate does nothing.
	b.activation, _, _ = b.c.Bind("xdg_activation_v1", 1, nil)
	go b.run()
	return b, nil
}

// New connects to the compositor named by the environment and opens a
// Backend on it.
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

// Close disconnects from the compositor, which ends every window.
func (b *Backend) Close() error {
	err := b.c.Close()
	<-b.done
	return err
}

// run dispatches events until the connection ends, then closes the windows.
func (b *Backend) run() {
	defer close(b.done)
	for b.c.Dispatch() == nil {
	}
	b.in.stopRepeat()
	b.mu.Lock()
	ws := make([]*window, 0, len(b.windows))
	for w := range b.windows {
		ws = append(ws, w)
	}
	b.mu.Unlock()
	for _, w := range ws {
		w.shutdown()
	}
}

// onWmBase answers the compositor's liveness pings.
func (b *Backend) onWmBase(opcode uint16, d *Decoder) {
	if opcode == wmBasePing {
		serial := d.Uint()
		b.c.Send(b.wmBase, wmBasePong, func(e *Encoder) { e.Uint(serial) })
	}
}

// NewWindow opens a window. Its first ResizeEvent and FrameEvent arrive once
// the compositor has configured it.
func (b *Backend) NewWindow(opts platform.WindowOptions) (platform.Window, error) {
	w := &window{b: b, events: make(chan platform.Event, eventQueue), size: opts.Size}
	c := b.c
	w.surface = c.NewID(nil)
	w.xdgSurface = c.NewID(w.onXdgSurface)
	w.toplevel = c.NewID(w.onToplevel)
	err := errors.Join(
		c.Send(b.compositor, compositorCreateSurface, func(e *Encoder) { e.Uint(w.surface) }),
		c.Send(b.wmBase, wmBaseGetXdgSurface, func(e *Encoder) { e.Uint(w.xdgSurface).Uint(w.surface) }),
		c.Send(w.xdgSurface, xdgSurfaceGetToplevel, func(e *Encoder) { e.Uint(w.toplevel) }),
		w.requestClientDecoration(),
		c.Send(w.toplevel, toplevelSetTitle, func(e *Encoder) { e.String(opts.Title) }),
		c.Send(w.toplevel, toplevelSetAppID, func(e *Encoder) { e.String(opts.AppID) }),
		c.Send(w.toplevel, toplevelSetMinSize, func(e *Encoder) { e.Int(int32(opts.MinSize.W)).Int(int32(opts.MinSize.H)) }),
		// The initial commit, without a buffer, asks for the first configure.
		c.Send(w.surface, surfaceCommit, nil),
	)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.windows[w] = struct{}{}
	b.mu.Unlock()
	return w, nil
}

// requestClientDecoration tells the compositor that the window draws its own
// title bar and frame. The compositor answers with the mode it settles on,
// which the window reports in its StateEvents. It does nothing when the
// compositor does not offer zxdg_decoration_manager_v1.
func (w *window) requestClientDecoration() error {
	if w.b.decoration == 0 {
		return nil
	}
	c := w.b.c
	w.decoration = c.NewID(w.onDecoration)
	return errors.Join(
		c.Send(w.b.decoration, decorationGetToplevel, func(e *Encoder) { e.Uint(w.decoration).Uint(w.toplevel) }),
		c.Send(w.decoration, decorationSetMode, func(e *Encoder) { e.Uint(decorationModeClient) }),
	)
}

// onDecoration records the decoration mode the compositor chose. It takes
// effect with the next xdg_surface.configure.
func (w *window) onDecoration(opcode uint16, d *Decoder) {
	if opcode == decorationConfigure {
		mode := d.Uint()
		w.mu.Lock()
		w.pending.Decorated = mode == decorationModeServer
		w.mu.Unlock()
	}
}

// window is a Wayland xdg toplevel.
type window struct {
	b                             *Backend
	surface, xdgSurface, toplevel uint32
	decoration                    uint32 // zero without server-side decoration
	events                        chan platform.Event

	mu         sync.Mutex
	size       platform.Size
	pending    platform.StateEvent // the state of the configure under way
	state      platform.StateEvent // the state last reported
	cursor     platform.Cursor
	configured bool

	// Frame pacing: the window sends a FrameEvent only when the compositor is
	// ready for a new frame, and at most one at a time. frameCallback is the
	// wl_callback of the last commit until the compositor has shown it, or
	// zero; frameQueued is set from sending a FrameEvent until Present
	// answers it; frameWanted records a frame asked for meanwhile.
	frameCallback            uint32
	frameQueued, frameWanted bool
	closed                   bool
	buffers                  []*buffer
}

// buffer is one wl_buffer backed by shared memory.
type buffer struct {
	id     uint32
	mem    *shmMem
	size   platform.Size
	busy   bool // the compositor still holds it
	stale  bool // no longer the right size; destroy once released
	pool   uint32
	stride int
}

// emit queues ev, dropping it when the application has stalled.
func (w *window) emit(ev platform.Event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		w.send(ev)
	}
}

// send queues ev and reports whether it was queued: it is dropped when the
// application has stalled. The caller holds w.mu and has checked that the
// window is open.
func (w *window) send(ev platform.Event) bool {
	select {
	case w.events <- ev:
		return true
	default:
		return false
	}
}

// requestFrame sends a FrameEvent now if the window may draw, and otherwise
// records that one is wanted: before the first configure, while the compositor
// has yet to show the last frame, and while a FrameEvent is already waiting.
// A frame asked for while one waits is wanted again afterwards, because the
// application may already have drawn that one before the change that asked.
// The caller holds w.mu.
func (w *window) requestFrame() {
	switch {
	case w.closed:
	case !w.configured || w.frameCallback != 0 || w.frameQueued:
		w.frameWanted = true
	default:
		w.frameWanted = false
		w.frameQueued = w.send(platform.FrameEvent{})
	}
}

// onFrame handles the compositor's signal that it has shown the last frame,
// so a new one may be drawn.
func (w *window) onFrame(opcode uint16, _ *Decoder) {
	if opcode != callbackDone {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.frameCallback = 0
	if w.frameWanted {
		w.requestFrame()
	}
}

// onToplevel handles xdg_toplevel.configure and close.
func (w *window) onToplevel(opcode uint16, d *Decoder) {
	switch opcode {
	case toplevelConfigure:
		width, height, states := d.Int(), d.Int(), d.Array()
		w.mu.Lock()
		if width > 0 && height > 0 {
			w.size = platform.Size{W: int(width), H: int(height)}
		}
		w.pending.Maximized = false
		for i := 0; i+4 <= len(states); i += 4 {
			switch order.Uint32(states[i:]) {
			case toplevelStateMaximized, toplevelStateFullscreen:
				w.pending.Maximized = true
			}
		}
		w.mu.Unlock()
	case toplevelClose:
		w.emit(platform.CloseEvent{})
	}
}

// onXdgSurface acknowledges a configure and reports the new size.
func (w *window) onXdgSurface(opcode uint16, d *Decoder) {
	if opcode != xdgSurfaceConfigure {
		return
	}
	serial := d.Uint()
	w.b.c.Send(w.xdgSurface, xdgSurfaceAckConfigure, func(e *Encoder) { e.Uint(serial) })
	w.mu.Lock()
	size := w.size
	w.configured = true
	for _, b := range w.buffers {
		if b.size != size {
			b.stale = true
		}
	}
	w.dropStale()
	state, changed := w.pending, w.pending != w.state
	w.state = w.pending
	w.mu.Unlock()
	if changed {
		w.emit(state)
	}
	w.emit(platform.ResizeEvent{Size: size, Scale: 1})
	w.mu.Lock()
	w.requestFrame()
	w.mu.Unlock()
}

// Events implements platform.Window.
func (w *window) Events() <-chan platform.Event { return w.events }

// Invalidate implements platform.Window. The FrameEvent waits until the
// compositor is ready for a frame, and several requests made meanwhile bring
// one FrameEvent.
func (w *window) Invalidate() {
	w.mu.Lock()
	w.requestFrame()
	w.mu.Unlock()
}

// Activate implements platform.Window with xdg_activation_v1.
func (w *window) Activate(token string) {
	if token == "" || w.b.activation == 0 {
		return
	}
	w.b.c.Send(w.b.activation, activationActivate, func(e *Encoder) { e.String(token).Uint(w.surface) })
}

// SetTitle implements platform.Window.
func (w *window) SetTitle(title string) {
	w.b.c.Send(w.toplevel, toplevelSetTitle, func(e *Encoder) { e.String(title) })
}

// Present implements platform.Window, answering the FrameEvent sent last. A
// frame that does not match the window's current size is dropped, as is one
// there is no memory for; a frame wanted meanwhile is then sent at once.
func (w *window) Present(img *image.RGBA) {
	size := platform.Size{W: img.Rect.Dx(), H: img.Rect.Dy()}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.frameQueued = false
	if w.closed || !w.configured {
		return
	}
	var buf *buffer
	if size == w.size {
		buf = w.freeBuffer(size)
	}
	if buf == nil {
		if w.frameWanted {
			w.requestFrame()
		}
		return
	}
	desktop.CopyBGRA(buf.mem.data, buf.stride, img)
	buf.busy = true
	c := w.b.c
	c.Send(w.surface, surfaceAttach, func(e *Encoder) { e.Uint(buf.id).Int(0).Int(0) })
	c.Send(w.surface, surfaceDamageBuffer, func(e *Encoder) { e.Int(0).Int(0).Int(int32(size.W)).Int(int32(size.H)) })
	// The compositor signals on this callback when it is ready for the next
	// frame, which paces drawing to what it shows.
	w.frameCallback = c.NewID(w.onFrame)
	c.Send(w.surface, surfaceFrame, func(e *Encoder) { e.Uint(w.frameCallback) })
	c.Send(w.surface, surfaceCommit, nil)
}

// freeBuffer returns an idle buffer of size, creating one when no idle buffer
// of that size exists. It returns nil if the memory cannot be allocated. The
// caller holds w.mu.
func (w *window) freeBuffer(size platform.Size) *buffer {
	for _, b := range w.buffers {
		if !b.busy && !b.stale && b.size == size {
			return b
		}
	}
	var buf *buffer
	buf = w.b.newBuffer(size, func() { w.release(buf) })
	if buf == nil {
		return nil
	}
	w.buffers = append(w.buffers, buf)
	return buf
}

// newBuffer creates a buffer of size in its own shared memory, calling
// release whenever the compositor releases it. It returns nil if the memory
// cannot be allocated.
func (b *Backend) newBuffer(size platform.Size, release func()) *buffer {
	stride := size.W * 4
	mem, err := newShmMem(stride * size.H)
	if err != nil {
		return nil
	}
	c := b.c
	buf := &buffer{mem: mem, size: size, stride: stride}
	buf.pool = c.NewID(nil)
	buf.id = c.NewID(func(opcode uint16, _ *Decoder) {
		if opcode == bufferRelease {
			release()
		}
	})
	c.Send(b.shm, shmCreatePool, func(e *Encoder) { e.Uint(buf.pool).FD(mem.fd).Int(int32(len(mem.data))) })
	c.Send(buf.pool, poolCreateBuffer, func(e *Encoder) {
		e.Uint(buf.id).Int(0).Int(int32(size.W)).Int(int32(size.H)).Int(int32(stride)).Uint(shmFormatARGB8888)
	})
	// The buffer keeps the pool's memory alive; the pool object is not needed.
	c.Send(buf.pool, poolDestroy, nil)
	return buf
}

// release marks buf idle, destroying it when it is no longer wanted.
func (w *window) release(buf *buffer) {
	w.mu.Lock()
	defer w.mu.Unlock()
	buf.busy = false
	w.dropStale()
}

// dropStale destroys the idle buffers that no longer fit. The caller holds
// w.mu.
func (w *window) dropStale() {
	kept := w.buffers[:0]
	for _, b := range w.buffers {
		if b.stale && !b.busy {
			w.b.c.Send(b.id, bufferDestroy, nil)
			b.mem.free()
			continue
		}
		kept = append(kept, b)
	}
	w.buffers = kept
}

// Close implements platform.Window: it destroys the window's objects and
// closes the Events channel.
func (w *window) Close() {
	c := w.b.c
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	for _, b := range w.buffers {
		b.stale = true
		b.busy = false
	}
	w.dropStale()
	w.mu.Unlock()
	if w.decoration != 0 {
		c.Send(w.decoration, decorationDestroy, nil)
	}
	c.Send(w.toplevel, toplevelDestroy, nil)
	c.Send(w.xdgSurface, xdgSurfaceDestroy, nil)
	c.Send(w.surface, surfaceDestroy, nil)
	w.shutdown()
}

// shutdown ends the window's event stream.
func (w *window) shutdown() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.closed = true
	close(w.events)
	w.b.mu.Lock()
	delete(w.b.windows, w)
	w.b.mu.Unlock()
}
