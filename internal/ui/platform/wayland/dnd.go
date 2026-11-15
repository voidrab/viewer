package wayland

import (
	"fmt"
	"image"
	"io"
	"os"
	"slices"
	"sync"
	"time"

	"viewer/internal/ui/platform"
	"viewer/internal/ui/platform/desktop"
)

// Opcodes and events of drag and drop on wl_data_device, wl_data_source and
// wl_data_offer, and the drag actions.
const (
	deviceStartDrag = 0
	deviceEnter     = 1
	deviceLeave     = 2
	deviceMotion    = 3
	deviceDrop      = 4

	sourceSetActions = 2
	sourceFinished   = 4

	offerAccept     = 0
	offerFinish     = 3
	offerSetActions = 4
	offerAction     = 2

	actionCopy = 1
	actionMove = 2
	actionAsk  = 4
)

// uriListMime is the format files are dragged in.
const uriListMime = desktop.URIListMime

// ownMime marks the drags the program begins. The process ID tells them from
// those of another copy of the program, whose windows cannot take them.
var ownMime = fmt.Sprintf("application/x-viewer-drag;pid=%d", os.Getpid())

// leaveGrace is how soon after a drag left a window a cancel still counts as
// ending over that window. Compositors report the leave of a cancelled drag
// either before or after the cancel, so a cancel this soon after a leave is
// taken to end over that window.
const leaveGrace = 100 * time.Millisecond

// now and hostname are replaced by tests.
var (
	now      = time.Now
	hostname = os.Hostname
)

// dnd is the state of drag and drop: the drag the program began, if any, and
// the drag over one of its windows, whoever began it.
type dnd struct {
	mu sync.Mutex

	source  uint32  // the wl_data_source of the program's drag, or zero
	from    *window // the window that began it
	icon    uint32  // its icon's wl_surface, or zero
	iconBuf *buffer // the icon's buffer, or nil

	offer  uint32  // the wl_data_offer of the drag over a window, or zero
	over   *window // the window that takes it, or nil when none does
	own    bool    // the drag over the window is the program's own
	action uint32  // the action the compositor chose for the offer
	x, y   float64 // where it is over the window
	leftAt time.Time
}

// StartDrag implements platform.Window with wl_data_device.start_drag.
func (w *window) StartDrag(icon *image.RGBA, hotspot image.Point) bool {
	return w.b.startDrag(w, icon, hotspot)
}

func (b *Backend) startDrag(w *window, icon *image.RGBA, hotspot image.Point) bool {
	g := &b.dnd
	g.mu.Lock()
	defer g.mu.Unlock()
	if b.clip.device == 0 || g.source != 0 {
		return false
	}
	c := b.c
	var src uint32
	src = c.NewID(func(opcode uint16, d *Decoder) { b.onDragSource(src, opcode, d) })
	c.Send(b.dataManager, dataManagerCreateSource, func(e *Encoder) { e.Uint(src) })
	c.Send(src, sourceOffer, func(e *Encoder) { e.String(ownMime) })
	if b.dataVersion >= 3 {
		c.Send(src, sourceSetActions, func(e *Encoder) { e.Uint(actionMove) })
	}
	var surface uint32
	if icon != nil {
		surface = c.NewID(nil)
		c.Send(b.compositor, compositorCreateSurface, func(e *Encoder) { e.Uint(surface) })
	}
	c.Send(b.clip.device, deviceStartDrag, func(e *Encoder) {
		e.Uint(src).Uint(w.surface).Uint(surface).Uint(b.in.lastSerial())
	})
	g.source, g.from, g.icon = src, w, surface
	if surface == 0 {
		return true
	}
	// Without an offset, the icon's top-left corner would sit at the pointer.
	// Attaching the buffer at minus the hotspot puts the hotspot there instead.
	size := platform.Size{W: icon.Rect.Dx(), H: icon.Rect.Dy()}
	if g.iconBuf = b.newBuffer(size, func() {}); g.iconBuf != nil {
		desktop.CopyBGRA(g.iconBuf.mem.data, g.iconBuf.stride, icon)
		c.Send(surface, surfaceAttach, func(e *Encoder) {
			e.Uint(g.iconBuf.id).Int(int32(-hotspot.X)).Int(int32(-hotspot.Y))
		})
		c.Send(surface, surfaceDamageBuffer, func(e *Encoder) { e.Int(0).Int(0).Int(int32(size.W)).Int(int32(size.H)) })
	}
	c.Send(surface, surfaceCommit, nil)
	return true
}

// onDragSource handles the events of src, the source of a drag the program
// began: a send request, which is answered with no data, and the end of the
// drag, dropped or cancelled.
func (b *Backend) onDragSource(src uint32, opcode uint16, d *Decoder) {
	switch opcode {
	case sourceSend:
		_, fd := d.String(), d.FD()
		if fd >= 0 {
			os.NewFile(uintptr(fd), "drag").Close()
		}
	case sourceCancel:
		g := &b.dnd
		g.mu.Lock()
		outside := g.over == nil && now().Sub(g.leftAt) >= leaveGrace
		g.mu.Unlock()
		b.endDrag(src, platform.DragEndEvent{Outside: outside})
	case sourceFinished:
		b.endDrag(src, platform.DragEndEvent{Dropped: true})
	}
}

// endDrag ends the drag of src, releasing its objects, and tells the window
// that began it how it ended.
func (b *Backend) endDrag(src uint32, ev platform.DragEndEvent) {
	g := &b.dnd
	g.mu.Lock()
	if src != g.source {
		g.mu.Unlock()
		return
	}
	c := b.c
	c.Send(src, sourceDestroy, nil)
	if g.icon != 0 {
		c.Send(g.icon, surfaceDestroy, nil)
	}
	if g.iconBuf != nil {
		c.Send(g.iconBuf.id, bufferDestroy, nil)
		g.iconBuf.mem.free()
	}
	from := g.from
	g.source, g.from, g.icon, g.iconBuf = 0, nil, 0, nil
	g.mu.Unlock()
	from.emit(ev)
}

// onOfferAction records the drag action the compositor chose for offer.
func (b *Backend) onOfferAction(offer uint32, d *Decoder) {
	action := d.Uint()
	g := &b.dnd
	g.mu.Lock()
	if offer == g.offer {
		g.action = action
	}
	g.mu.Unlock()
}

// onDrag handles the drag events of the data device: a drag entering one of
// the program's windows, moving over it, leaving it and dropping on it. A
// window takes files and the program's own drags; it refuses anything else.
func (b *Backend) onDrag(opcode uint16, d *Decoder) {
	g := &b.dnd
	switch opcode {
	case deviceEnter:
		serial, surface, x, y, offer := d.Uint(), d.Uint(), d.Fixed(), d.Fixed(), d.Uint()
		b.dropOffer() // one whose leave never came
		mimes := b.takeMimes(offer)
		own := slices.Contains(mimes, ownMime)
		mime, action := uriListMime, uint32(actionCopy)
		if own {
			mime, action = ownMime, actionMove
		}
		w := b.windowFor(surface)
		g.mu.Lock()
		defer g.mu.Unlock()
		g.offer, g.over, g.own, g.action, g.x, g.y = offer, nil, own, 0, x, y
		if offer == 0 {
			return
		}
		if w == nil || !own && !slices.Contains(mimes, uriListMime) {
			b.c.Send(offer, offerAccept, func(e *Encoder) { e.Uint(serial).Uint(0) }) // a null type refuses it
			return
		}
		g.over = w
		b.c.Send(offer, offerAccept, func(e *Encoder) { e.Uint(serial).String(mime) })
		if b.dataVersion >= 3 {
			b.c.Send(offer, offerSetActions, func(e *Encoder) { e.Uint(action).Uint(action) })
		}
		w.emit(platform.DragEvent{Kind: platform.DragEnter, X: x, Y: y, Own: own})
	case deviceMotion:
		_, x, y := d.Uint(), d.Fixed(), d.Fixed()
		g.mu.Lock()
		defer g.mu.Unlock()
		g.x, g.y = x, y
		if g.over != nil {
			g.over.emit(platform.DragEvent{Kind: platform.DragMove, X: x, Y: y, Own: g.own})
		}
	case deviceLeave:
		g.mu.Lock()
		if g.over != nil {
			g.over.emit(platform.DragEvent{Kind: platform.DragLeave, X: g.x, Y: g.y, Own: g.own})
			g.leftAt = now()
		}
		g.mu.Unlock()
		b.dropOffer()
	case deviceDrop:
		g.mu.Lock()
		w, offer, own, action, x, y := g.over, g.offer, g.own, g.action, g.x, g.y
		g.offer, g.over = 0, nil
		g.mu.Unlock()
		switch {
		case w == nil:
			if offer != 0 {
				b.c.Send(offer, offerDestroy, nil)
			}
		case own:
			b.finishOffer(offer, action)
			w.emit(platform.DropEvent{X: x, Y: y, Own: true})
		default:
			b.receiveFiles(w, offer, action, x, y)
		}
	}
}

// takeMimes returns the formats offer lists, forgetting them.
func (b *Backend) takeMimes(offer uint32) []string {
	cb := &b.clip
	cb.mu.Lock()
	defer cb.mu.Unlock()
	mimes := cb.mimes[offer]
	delete(cb.mimes, offer)
	return mimes
}

// dropOffer destroys the offer of the drag over a window, if there is one.
func (b *Backend) dropOffer() {
	g := &b.dnd
	g.mu.Lock()
	offer := g.offer
	g.offer, g.over = 0, nil
	g.mu.Unlock()
	if offer != 0 {
		b.c.Send(offer, offerDestroy, nil)
	}
}

// finishOffer tells the source of a dropped offer that the drop is done, as
// version 3 requires for an action other than ask, and destroys the offer.
func (b *Backend) finishOffer(offer, action uint32) {
	if b.dataVersion >= 3 && action != 0 && action != actionAsk {
		b.c.Send(offer, offerFinish, nil)
	}
	b.c.Send(offer, offerDestroy, nil)
}

// receiveFiles reads the URIs of the files dropped with offer, in the
// background, and tells w of the local files among them.
func (b *Backend) receiveFiles(w *window, offer, action uint32, x, y float64) {
	r, wr, err := pipe()
	if err != nil {
		b.c.Send(offer, offerDestroy, nil)
		return
	}
	b.c.Send(offer, offerReceive, func(e *Encoder) { e.String(uriListMime).FD(int(wr.Fd())) })
	wr.Close()
	go func() {
		defer r.Close()
		r.SetReadDeadline(now().Add(desktop.PasteTimeout))
		data, err := io.ReadAll(io.LimitReader(r, desktop.MaxPaste))
		b.finishOffer(offer, action)
		host, _ := hostname()
		if paths := desktop.ParseURIList(string(data), host); err == nil && len(paths) > 0 {
			w.emit(platform.DropEvent{X: x, Y: y, Paths: paths})
		}
	}()
}
