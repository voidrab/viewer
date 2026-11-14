package wayland

import (
	"errors"
	"image"
	"sync"

	"viewer/internal/ui/platform"
)

// Opcodes and event numbers of the interfaces a window uses.
const (
	compositorCreateSurface = 0

	surfaceDestroy      = 0
	surfaceAttach       = 1
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
	decorationModeServer  = 2

	activationActivate = 2 // xdg_activation_v1.activate(token, surface)

	toplevelDestroy     = 0
	toplevelSetTitle    = 2
	toplevelSetAppID    = 3
	toplevelSetMinSize  = 8
	toplevelConfigure   = 0
	toplevelClose       = 1
	toplevelStateActive = 4
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
	clip        clipboard

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
	// The seat is optional: without one, windows receive no input events.
	if b.seat, _, err = b.c.Bind("wl_seat", 5, b.onSeat); err == nil {
		if err := b.c.Roundtrip(); err != nil {
			return nil, err
		}
	}
	// The clipboard is optional too, and needs a seat.
	if b.seat != 0 {
		if b.dataManager, _, err = b.c.Bind("wl_data_device_manager", 3, nil); err == nil {
			b.clip.device = b.c.NewID(b.onDataDevice)
			b.c.Send(b.dataManager, dataManagerGetDevice, func(e *Encoder) { e.Uint(b.clip.device).Uint(b.seat) })
		}
	}
	// Server-side decoration is optional. Without zxdg_decoration_manager_v1,
	// windows do not request it and the compositor's default applies.
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
		w.requestServerDecoration(),
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

// requestServerDecoration asks the compositor to draw the window's title bar
// and borders. It does nothing when the compositor does not offer
// zxdg_decoration_manager_v1.
func (w *window) requestServerDecoration() error {
	if w.b.decoration == 0 {
		return nil
	}
	c := w.b.c
	w.decoration = c.NewID(nil)
	return errors.Join(
		c.Send(w.b.decoration, decorationGetToplevel, func(e *Encoder) { e.Uint(w.decoration).Uint(w.toplevel) }),
		c.Send(w.decoration, decorationSetMode, func(e *Encoder) { e.Uint(decorationModeServer) }),
	)
}

// window is a Wayland xdg toplevel.
type window struct {
	b                             *Backend
	surface, xdgSurface, toplevel uint32
	decoration                    uint32 // zero without server-side decoration
	events                        chan platform.Event

	mu         sync.Mutex
	size       platform.Size
	configured bool
	closed     bool
	buffers    []*buffer
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
	if w.closed {
		return
	}
	select {
	case w.events <- ev:
	default:
	}
}

// onToplevel handles xdg_toplevel.configure and close.
func (w *window) onToplevel(opcode uint16, d *Decoder) {
	switch opcode {
	case toplevelConfigure:
		width, height := d.Int(), d.Int()
		w.mu.Lock()
		if width > 0 && height > 0 {
			w.size = platform.Size{W: int(width), H: int(height)}
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
	w.mu.Unlock()
	w.emit(platform.ResizeEvent{Size: size, Scale: 1})
	w.emit(platform.FrameEvent{})
}

// Events implements platform.Window.
func (w *window) Events() <-chan platform.Event { return w.events }

// Invalidate implements platform.Window.
func (w *window) Invalidate() { w.emit(platform.FrameEvent{}) }

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

// Present implements platform.Window. A frame that does not match the
// window's current size is dropped; a FrameEvent follows the next resize.
func (w *window) Present(img *image.RGBA) {
	size := platform.Size{W: img.Rect.Dx(), H: img.Rect.Dy()}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || !w.configured || size != w.size {
		return
	}
	buf := w.freeBuffer(size)
	if buf == nil {
		return
	}
	copyARGB(buf.mem.data, buf.stride, img)
	buf.busy = true
	c := w.b.c
	c.Send(w.surface, surfaceAttach, func(e *Encoder) { e.Uint(buf.id).Int(0).Int(0) })
	c.Send(w.surface, surfaceDamageBuffer, func(e *Encoder) { e.Int(0).Int(0).Int(int32(size.W)).Int(int32(size.H)) })
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
	stride := size.W * 4
	mem, err := newShmMem(stride * size.H)
	if err != nil {
		return nil
	}
	c := w.b.c
	buf := &buffer{mem: mem, size: size, stride: stride}
	buf.pool = c.NewID(nil)
	buf.id = c.NewID(func(opcode uint16, _ *Decoder) {
		if opcode == bufferRelease {
			w.release(buf)
		}
	})
	c.Send(w.b.shm, shmCreatePool, func(e *Encoder) { e.Uint(buf.pool).FD(mem.fd).Int(int32(len(mem.data))) })
	c.Send(buf.pool, poolCreateBuffer, func(e *Encoder) {
		e.Uint(buf.id).Int(0).Int(int32(size.W)).Int(int32(size.H)).Int(int32(stride)).Uint(shmFormatARGB8888)
	})
	// The buffer keeps the pool's memory alive; the pool object is not needed.
	c.Send(buf.pool, poolDestroy, nil)
	w.buffers = append(w.buffers, buf)
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
