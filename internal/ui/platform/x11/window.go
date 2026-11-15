//go:build !windows

package x11

import (
	"errors"
	"image"
	"image/draw"
	"os"
	"slices"
	"sync"
	"time"

	"viewrab/internal/ui/platform"
)

// frameInterval is the shortest time between two frames. The server gives
// no signal of when the screen shows a frame, so drawing is held to about
// the refresh rate of most screens. Tests shorten it.
var frameInterval = time.Second / 60

// windowEvents are the events a window selects.
const windowEvents = maskKeyPress | maskKeyRelease | maskButtonPress | maskButtonRelease |
	maskEnterWindow | maskLeaveWindow | maskPointerMotion | maskExposure |
	maskStructureNotify | maskFocusChange | maskPropertyChange

// xdndVersion is the version of the XDND protocol windows speak.
const xdndVersion = 5

// window is a top-level X window.
type window struct {
	b      *Backend
	id, gc uint32
	events chan platform.Event

	mu        sync.Mutex
	size      platform.Size
	state     platform.StateEvent // the state last reported
	cursor    platform.Cursor
	mapped    bool
	closed    bool
	decorated bool

	// Frame pacing: a FrameEvent goes out only when the server has drawn the
	// last frame and frameInterval has passed, and at most one at a time.
	// busy is set while the last frame is outstanding. frameQueued is set
	// from when a FrameEvent is sent until Present answers it. frameWanted
	// records a frame asked for meanwhile.
	busy, frameQueued, frameWanted bool

	// The frame's pixels: in shared memory, or else in pix, sent with
	// PutImage.
	seg     *shmSeg
	pix     []byte
	pixSize platform.Size
}

// NewWindow opens a window. Its first ResizeEvent and FrameEvent arrive once
// the window manager has mapped it.
func (b *Backend) NewWindow(opts platform.WindowOptions) (platform.Window, error) {
	c := b.c
	w := &window{b: b, id: c.NewID(), gc: c.NewID(), events: make(chan platform.Event, eventQueue), size: opts.Size}
	w.decorated = !b.clientDecorated()
	a := (&windowAttrs{}).set(cwBackPixmap, 0).set(cwBorderPixel, 0).set(cwBitGravity, 1).
		set(cwEventMask, windowEvents).set(cwColormap, b.colormap)
	r := newReq(opCreateWindow, b.depth).u32(w.id).u32(b.root).i16(0).i16(0).
		u16(uint16(opts.Size.W)).u16(uint16(opts.Size.H)).u16(0).u16(1).u32(b.visual)
	a.put(r)
	if err := c.Send(r); err != nil {
		return nil, err
	}
	// The GC is used only for PutImage, so graphics exposures are turned off.
	err := errors.Join(
		c.Send(newReq(opCreateGC, 0).u32(w.gc).u32(w.id).u32(1<<16).u32(0)),
		w.setProperties(opts),
	)
	b.mu.Lock()
	b.windows[w.id] = w
	b.mu.Unlock()
	if err == nil {
		err = c.Send(newReq(opMapWindow, 0).u32(w.id))
	}
	if err != nil {
		w.Close()
		return nil, err
	}
	return w, nil
}

// setProperties sets the window's title, class, size hints, protocols and
// the other properties window managers read before it is mapped.
func (w *window) setProperties(opts platform.WindowOptions) error {
	b, id := w.b, w.id
	prop := func(name string, typ uint32, format byte, data []byte) error {
		return b.c.Send(changeProperty(propReplace, id, b.atom(name), typ, format, data))
	}
	host, _ := hostname()
	class := []byte(opts.AppID + "\x00" + opts.AppID + "\x00")
	// WM_NORMAL_HINTS: the minimum size, flagged PMinSize.
	hints := propU32(1<<4, 0, 0, 0, 0, uint32(opts.MinSize.W), uint32(opts.MinSize.H), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	errs := []error{
		w.setTitle(opts.Title),
		b.c.Send(changeProperty(propReplace, id, atomWMClass, atomString, 8, class)),
		b.c.Send(changeProperty(propReplace, id, atomWMNormal, atomSizeHints, 32, hints)),
		// WM_HINTS: the window takes input and starts in the normal state.
		b.c.Send(changeProperty(propReplace, id, atomWMHints, atomWMHints, 32, propU32(1|2, 1, 1, 0, 0, 0, 0, 0, 0))),
		prop("WM_CLIENT_MACHINE", atomString, 8, []byte(host)),
		prop("WM_PROTOCOLS", atomAtom, 32, propU32(b.atom("WM_DELETE_WINDOW"), b.atom("_NET_WM_PING"))),
		prop("_NET_WM_PID", atomCardinal, 32, propU32(uint32(os.Getpid()))),
		prop("_NET_WM_WINDOW_TYPE", atomAtom, 32, propU32(b.atom("_NET_WM_WINDOW_TYPE_NORMAL"))),
		prop("XdndAware", atomAtom, 32, propU32(xdndVersion)),
	}
	if !w.decorated {
		// _MOTIF_WM_HINTS with only the decorations flag, and none of them.
		errs = append(errs, prop("_MOTIF_WM_HINTS", b.atom("_MOTIF_WM_HINTS"), 32, propU32(2, 0, 0, 0, 0)))
	}
	if opts.Icon != nil {
		errs = append(errs, prop("_NET_WM_ICON", atomCardinal, 32, iconData(opts.Icon)))
	}
	b.startupMu.Lock()
	if b.startupID != "" {
		errs = append(errs, prop("_NET_STARTUP_ID", b.atom("UTF8_STRING"), 8, []byte(b.startupID)))
	}
	b.startupMu.Unlock()
	return errors.Join(errs...)
}

// iconData encodes img as a _NET_WM_ICON: its width and height, then each
// pixel as ARGB, not premultiplied.
func iconData(img image.Image) []byte {
	nrgba := image.NewNRGBA(img.Bounds())
	draw.Draw(nrgba, nrgba.Rect, img, img.Bounds().Min, draw.Src)
	w, h := nrgba.Rect.Dx(), nrgba.Rect.Dy()
	out := propU32(uint32(w), uint32(h))
	for i := 0; i < len(nrgba.Pix); i += 4 {
		p := nrgba.Pix[i:]
		out = order.AppendUint32(out, uint32(p[3])<<24|uint32(p[0])<<16|uint32(p[1])<<8|uint32(p[2]))
	}
	return out
}

// setTitle sets both the legacy Latin-1 and the UTF-8 title.
func (w *window) setTitle(title string) error {
	b := w.b
	return errors.Join(
		b.c.Send(changeProperty(propReplace, w.id, atomWMName, atomString, 8, latin1(title))),
		b.c.Send(changeProperty(propReplace, w.id, b.atom("_NET_WM_NAME"), b.atom("UTF8_STRING"), 8, []byte(title))),
	)
}

// SetTitle implements platform.Window.
func (w *window) SetTitle(title string) { w.setTitle(title) }

// Events implements platform.Window.
func (w *window) Events() <-chan platform.Event { return w.events }

// emit queues ev, dropping it when the application has stalled.
func (w *window) emit(ev platform.Event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		w.send(ev)
	}
}

// send queues ev and reports whether it was queued. The caller holds w.mu
// and has checked that the window is open.
func (w *window) send(ev platform.Event) bool {
	select {
	case w.events <- ev:
		return true
	default:
		return false
	}
}

// onMap handles the window being shown: the first time, it reports the
// window's state and size and asks for the first frame.
func (w *window) onMap() {
	w.mu.Lock()
	first := !w.mapped
	w.mapped = true
	w.mu.Unlock()
	if !first {
		return
	}
	w.b.endStartup()
	w.readState()
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		w.send(platform.ResizeEvent{Size: w.size, Scale: 1})
	}
	w.requestFrame()
}

// onConfigure records the window's size, reporting a change once the window
// is mapped.
func (w *window) onConfigure(width, height int) {
	size := platform.Size{W: width, H: height}
	w.mu.Lock()
	defer w.mu.Unlock()
	if size == w.size {
		return
	}
	w.size = size
	if w.mapped && !w.closed {
		w.send(platform.ResizeEvent{Size: size, Scale: 1})
		w.requestFrame()
	}
}

// readState reads how the window manager shows the window and reports a
// change.
func (w *window) readState() {
	b := w.b
	p, err := b.c.getProperty(w.id, b.atom("_NET_WM_STATE"), false, 1024)
	if err != nil {
		return
	}
	states := p.u32s()
	has := func(name string) bool { return slices.Contains(states, b.atom(name)) }
	state := platform.StateEvent{
		Maximized: has("_NET_WM_STATE_FULLSCREEN") ||
			has("_NET_WM_STATE_MAXIMIZED_VERT") && has("_NET_WM_STATE_MAXIMIZED_HORZ"),
		Decorated: w.decorated,
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if state != w.state && !w.closed {
		w.state = state
		w.send(state)
	}
}

// requestFrame sends a FrameEvent now if the window may draw, and otherwise
// records that one is wanted: before the window is mapped, while the last
// frame is outstanding, and while a FrameEvent is already waiting. A frame
// asked for while one waits is wanted again afterwards, because the
// application may already have drawn that one before the change that asked.
// The caller holds w.mu.
func (w *window) requestFrame() {
	switch {
	case w.closed:
	case !w.mapped || w.busy || w.frameQueued:
		w.frameWanted = true
	default:
		w.frameWanted = false
		w.frameQueued = w.send(platform.FrameEvent{})
	}
}

// Invalidate implements platform.Window.
func (w *window) Invalidate() {
	w.mu.Lock()
	w.requestFrame()
	w.mu.Unlock()
}

// Present implements platform.Window, answering the FrameEvent sent last. A
// frame that does not match the window's current size is dropped; a frame
// wanted meanwhile is then sent at once.
func (w *window) Present(img *image.RGBA) {
	size := platform.Size{W: img.Rect.Dx(), H: img.Rect.Dy()}
	w.mu.Lock()
	w.frameQueued = false
	if w.closed || !w.mapped || w.busy || size != w.size || size.W == 0 || size.H == 0 || !w.buffer(size) {
		if w.frameWanted {
			w.requestFrame()
		}
		w.mu.Unlock()
		return
	}
	// busy keeps the buffer from being replaced or freed meanwhile, so the
	// frame is converted and sent without holding up event dispatch.
	w.busy = true
	seg, pix := w.seg, w.pix
	w.mu.Unlock()
	b := w.b
	start := time.Now()
	if seg != nil {
		b.pixels.convert(seg.data, 4*size.W, img)
		b.c.Send(newReq(b.shm.major, shmPutImage).u32(w.id).u32(w.gc).
			u16(uint16(size.W)).u16(uint16(size.H)).u16(0).u16(0).u16(uint16(size.W)).u16(uint16(size.H)).
			i16(0).i16(0).u8(b.depth).u8(imageFormatZPixmap).u8(0).pad(1).u32(seg.id).u32(0))
	} else {
		b.pixels.convert(pix, 4*size.W, img)
		b.putImage(w.id, w.gc, b.depth, size.W, size.H, pix)
	}
	// Once the server has drawn the frame, and so is done with the memory,
	// and the interval has passed, the next frame may come.
	go func() {
		err := b.c.Sync()
		time.Sleep(frameInterval - time.Since(start))
		w.mu.Lock()
		defer w.mu.Unlock()
		w.busy = false
		if w.closed {
			w.freeBuffer() // left to this moment by Close
			return
		}
		if err == nil && w.frameWanted {
			w.requestFrame()
		}
	}()
}

// buffer makes sure the frame's memory fits size. Shared memory that cannot
// be made is given up for good, in favour of PutImage. The caller holds w.mu.
func (w *window) buffer(size platform.Size) bool {
	if w.pixSize == size && (w.seg != nil || w.pix != nil) {
		return true
	}
	w.freeBuffer()
	b := w.b
	if b.shm.present {
		seg, err := b.newShmSeg(4 * size.W * size.H)
		if err == nil {
			w.seg, w.pixSize = seg, size
			return true
		}
		b.shm.present = false
	}
	w.pix, w.pixSize = make([]byte, 4*size.W*size.H), size
	return true
}

// freeBuffer releases the frame's memory. The caller holds w.mu.
func (w *window) freeBuffer() {
	if w.seg != nil {
		w.seg.free(w.b)
		w.seg = nil
	}
	w.pix, w.pixSize = nil, platform.Size{}
}

// Close implements platform.Window: it destroys the window and closes the
// Events channel.
func (w *window) Close() {
	if !w.shutdown() {
		return
	}
	c := w.b.c
	c.Send(newReq(opFreeGC, 0).u32(w.gc))
	c.Send(newReq(opDestroyWindow, 0).u32(w.id))
}

// shutdown ends the window's event stream and frees its frame memory, unless
// a frame is in flight, which frees it when it completes. It reports whether
// the window was open.
func (w *window) shutdown() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return false
	}
	w.closed = true
	close(w.events)
	if !w.busy {
		w.freeBuffer()
	}
	w.b.mu.Lock()
	delete(w.b.windows, w.id)
	w.b.mu.Unlock()
	return true
}
