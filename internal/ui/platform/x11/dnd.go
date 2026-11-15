package x11

import (
	"image"
	"slices"
	"sync"
	"time"
	"viewer/internal/ui/platform/desktop"

	"viewer/internal/ui/platform"
)

// xdndTarget is the state of a drag from another program over one of the
// program's windows, under the XDND protocol. The dragging program sends
// client messages as the pointer enters, moves over, leaves or drops on the
// window, and the window answers whether it takes the drop.
type xdndTarget struct {
	mu sync.Mutex
	xdndDrag
}

// xdndDrag is the state of one drag from another program.
type xdndDrag struct {
	source  uint32  // the dragging program's window, or zero
	w       *window // the window it is over
	accept  bool    // the drag carries files
	entered bool    // a DragEnter has gone out
	x, y    float64
	// origin is where the window is on the root, worked out at the first
	// move; the window stays put while something is dragged over it.
	origin      image.Point
	originKnown bool
	// dropping is set from a drop until its files arrive, and timer bounds
	// how long they may take.
	dropping bool
	timer    *time.Timer
}

// onMessage handles an XDND client message sent to window w.
func (t *xdndTarget) onMessage(b *Backend, w *window, typ uint32, data func(int) uint32) {
	switch typ {
	case b.atom("XdndEnter"):
		types := []uint32{data(2), data(3), data(4)}
		if data(1)&1 != 0 {
			// More than three types: the full list is on the source window.
			p, err := b.c.getProperty(data(0), b.atom("XdndTypeList"), false, 4096)
			if err == nil {
				types = p.u32s()
			}
		}
		t.mu.Lock()
		t.reset()
		t.source, t.w = data(0), w
		t.accept = slices.Contains(types, b.atom(desktop.URIListMime))
		t.mu.Unlock()
	case b.atom("XdndPosition"):
		t.onPosition(b, w, data(0), int(data(2)>>16), int(data(2)&0xffff))
	case b.atom("XdndLeave"):
		t.mu.Lock()
		if data(0) == t.source && t.w == w {
			t.leave()
			t.reset()
		}
		t.mu.Unlock()
	case b.atom("XdndDrop"):
		t.onDrop(b, w, data(0), data(2))
	}
}

// reset forgets the drag. The caller holds t.mu.
func (t *xdndTarget) reset() {
	if t.timer != nil {
		t.timer.Stop()
	}
	t.xdndDrag = xdndDrag{}
}

// leave tells the window the drag has left it. The caller holds t.mu.
func (t *xdndTarget) leave() {
	if t.entered {
		t.w.emit(platform.DragEvent{Kind: platform.DragLeave, X: t.x, Y: t.y})
	}
}

// onPosition handles the drag moving to (rootX, rootY), answering with
// XdndStatus whether the window takes the drop.
func (t *xdndTarget) onPosition(b *Backend, w *window, source uint32, rootX, rootY int) {
	t.mu.Lock()
	ok := source == t.source && t.w == w && !t.dropping
	accept, origin, known := t.accept, t.origin, t.originKnown
	t.mu.Unlock()
	if !ok {
		return
	}
	if !known {
		ox, oy, _, err := b.c.translate(w.id, b.root, 0, 0)
		if err != nil {
			return
		}
		origin = image.Pt(ox, oy)
		t.mu.Lock()
		t.origin, t.originKnown = origin, true
		t.mu.Unlock()
	}
	x, y := rootX-origin.X, rootY-origin.Y
	action := uint32(0)
	if accept {
		action = b.atom("XdndActionCopy")
		kind := platform.DragMove
		t.mu.Lock()
		if !t.entered {
			kind, t.entered = platform.DragEnter, true
		}
		t.x, t.y = float64(x), float64(y)
		t.mu.Unlock()
		w.emit(platform.DragEvent{Kind: kind, X: float64(x), Y: float64(y)})
	}
	// Flags: whether the drop is taken, and that every move should be told.
	flags := uint32(2)
	if accept {
		flags |= 1
	}
	b.c.Send(sendEvent(source, 0, clientMessage(source, b.atom("XdndStatus"), w.id, flags, 0, 0, action)))
}

// xdndFinish tells the source the drop is over, and whether it was taken.
func (b *Backend) xdndFinish(source, window uint32, taken bool) {
	ok, action := uint32(0), uint32(0)
	if taken {
		ok, action = 1, b.atom("XdndActionCopy")
	}
	b.c.Send(sendEvent(source, 0, clientMessage(source, b.atom("XdndFinished"), window, ok, action)))
}

// onDrop handles a drop: the files are asked for, and arrive by
// SelectionNotify.
func (t *xdndTarget) onDrop(b *Backend, w *window, source, time uint32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if source != t.source || t.w != w || t.dropping {
		return
	}
	if !t.accept {
		t.reset()
		b.xdndFinish(source, w.id, false)
		return
	}
	t.dropping = true
	t.timer = after(desktop.PasteTimeout, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.dropping && t.source == source {
			t.leave()
			t.reset()
			b.xdndFinish(source, w.id, false)
		}
	})
	b.c.Send(newReq(opConvertSelection, 0).u32(w.id).u32(b.atom("XdndSelection")).u32(b.atom(desktop.URIListMime)).
		u32(b.atom("XdndSelection")).u32(time))
}

// onSelection receives the files of a drop on requestor, in prop.
func (t *xdndTarget) onSelection(b *Backend, requestor, prop uint32) {
	t.mu.Lock()
	if !t.dropping || t.w.id != requestor {
		t.mu.Unlock()
		return
	}
	w, source, x, y := t.w, t.source, t.x, t.y
	t.reset()
	t.mu.Unlock()
	var paths []string
	if prop != atomNone {
		if v, err := b.c.getProperty(requestor, prop, true, desktop.MaxPaste); err == nil {
			paths = desktop.ParseURIList(string(v.value), host())
		}
	}
	b.xdndFinish(source, w.id, len(paths) > 0)
	if len(paths) > 0 {
		w.emit(platform.DropEvent{X: x, Y: y, Paths: paths})
	} else {
		w.emit(platform.DragEvent{Kind: platform.DragLeave, X: x, Y: y})
	}
}

// drag is a drag the program began, of its own tabs. The windows it can
// drop on are all the program's, so the drag needs no protocol: the window
// that begins it grabs the pointer and works out which window is under it.
type drag struct {
	mu      sync.Mutex
	on      bool
	from    *window
	icon    uint32 // the window showing the icon, or zero
	hotspot image.Point
	over    *window // the window under the pointer, or nil
	x, y    float64 // where in it
	spots   map[uint32]dropSpot
}

// SHAPE's ShapeRectangles request code, with its Set operation and Input
// region kind.
const (
	shapeRectangles = 1
	shapeSet        = 0
	shapeInput      = 2
)

// active reports whether a drag is under way.
func (g *drag) active() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.on
}

// Grab modes and results.
const (
	grabModeAsync = 1
	grabSuccess   = 0
)

// StartDrag implements platform.Window.
func (w *window) StartDrag(icon *image.RGBA, hotspot image.Point) bool {
	b, g := w.b, &w.b.drag
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.on {
		return false
	}
	t := b.in.now()
	rep, err := b.c.Call(newReq(opGrabPointer, 0).u32(w.id).u16(maskButtonRelease | maskPointerMotion).
		u8(grabModeAsync).u8(grabModeAsync).u32(0).u32(0).u32(t))
	if err != nil || rep[1] != grabSuccess {
		return false
	}
	// The keyboard is grabbed for Escape, which cancels; without it, the drag
	// still works.
	b.c.Send(newReq(opGrabKeyboard, 0).u32(w.id).u32(t).u8(grabModeAsync).u8(grabModeAsync).pad(2))
	g.on, g.from, g.hotspot, g.over, g.spots = true, w, hotspot, nil, b.dropSpots()
	rootX, rootY := b.pointerRoot()
	if icon != nil {
		g.icon = b.dragIcon(icon, rootX-hotspot.X, rootY-hotspot.Y)
	}
	g.update(b, rootX, rootY)
	return true
}

// pointerRoot returns where the pointer is on the root.
func (b *Backend) pointerRoot() (int, int) {
	rep, err := b.c.Call(newReq(opQueryPointer, 0).u32(b.root))
	if err != nil {
		return 0, 0
	}
	return i16At(rep, 16), i16At(rep, 18)
}

// dragIcon shows img in an unmanaged window at (x, y) on the root, with
// alpha where the server offers it. When SHAPE is present, the window has an
// empty input region, so pointer input passes through it.
func (b *Backend) dragIcon(img *image.RGBA, x, y int) uint32 {
	c := b.c
	id, gc := c.NewID(), c.NewID()
	w, h := img.Rect.Dx(), img.Rect.Dy()
	depth, visual, colormap, pf := b.depth, b.visual, b.colormap, b.pixels
	if b.argb != 0 {
		depth, visual, colormap = 32, b.argb, b.argbColormap
		pf = b.argbPixels
	}
	a := (&windowAttrs{}).set(cwBackPixmap, 0).set(cwBorderPixel, 0).set(cwOverrideRedirect, 1).set(cwColormap, colormap)
	r := newReq(opCreateWindow, depth).u32(id).u32(b.root).i16(x).i16(y).u16(uint16(w)).u16(uint16(h)).u16(0).u16(1).u32(visual)
	a.put(r)
	c.Send(r)
	c.Send(changeProperty(propReplace, id, b.atom("_NET_WM_WINDOW_TYPE"), atomAtom, 32, propU32(b.atom("_NET_WM_WINDOW_TYPE_DND"))))
	if b.shape.present {
		// An empty input region: the pointer is never over the icon.
		c.Send(newReq(b.shape.major, shapeRectangles).u8(shapeSet).u8(shapeInput).u8(0).pad(1).u32(id).i16(0).i16(0))
	}
	c.Send(newReq(opMapWindow, 0).u32(id))
	c.Send(newReq(opCreateGC, 0).u32(gc).u32(id).u32(0))
	pix := make([]byte, 4*w*h)
	pf.convert(pix, 4*w, img)
	b.putImage(id, gc, depth, w, h, pix)
	c.Send(newReq(opFreeGC, 0).u32(gc))
	return id
}

// dropSpot is one of the program's windows as a drag sees it: the window,
// and where its origin is on the root.
type dropSpot struct {
	w      *window
	ox, oy int
}

// dropSpots maps the top-level window that holds each of the program's
// windows, a window manager's frame or the window itself, to the window and
// its origin. Windows stay put during a drag, so this is worked out once, as
// the drag begins, with the round trips of all windows in flight together.
func (b *Backend) dropSpots() map[uint32]dropSpot {
	b.mu.Lock()
	ws := make([]*window, 0, len(b.windows))
	for _, w := range b.windows {
		ws = append(ws, w)
	}
	b.mu.Unlock()
	origins := make([]func() ([]byte, error), len(ws))
	for i, w := range ws {
		origins[i] = b.c.Start(newReq(opTranslateCoordinates, 0).u32(w.id).u32(b.root).i16(0).i16(0))
	}
	spots := map[uint32]dropSpot{}
	for i, w := range ws {
		rep, err := origins[i]()
		if top := b.topLevel(w.id); err == nil && top != 0 {
			spots[top] = dropSpot{w: w, ox: i16At(rep, 12), oy: i16At(rep, 14)}
		}
	}
	return spots
}

// topLevel returns the child of the root that holds window, or zero.
func (b *Backend) topLevel(window uint32) uint32 {
	for range 8 {
		rep, err := b.c.Call(newReq(opQueryTree, 0).u32(window))
		if err != nil {
			return 0
		}
		if parent := u32At(rep, 12); parent != b.root {
			window = parent
			continue
		}
		return window
	}
	return 0
}

// windowAt returns the program's window under (rootX, rootY), and where the
// point is in it: the top-level window under the point, looked up in spots.
func (b *Backend) windowAt(spots map[uint32]dropSpot, rootX, rootY int) (*window, float64, float64) {
	_, _, top, err := b.c.translate(b.root, b.root, rootX, rootY)
	s, ok := spots[top]
	if err != nil || !ok {
		return nil, 0, 0
	}
	return s.w, float64(rootX - s.ox), float64(rootY - s.oy)
}

// update follows the pointer to (rootX, rootY): the icon moves with it, and
// the windows it leaves and enters are told. The caller holds g.mu.
func (g *drag) update(b *Backend, rootX, rootY int) {
	if g.icon != 0 {
		b.c.Send(newReq(opConfigureWindow, 0).u32(g.icon).u16(1 | 2).pad(2).
			u32(uint32(int32(rootX - g.hotspot.X))).u32(uint32(int32(rootY - g.hotspot.Y))))
	}
	w, x, y := b.windowAt(g.spots, rootX, rootY)
	switch {
	case w != g.over:
		if g.over != nil {
			g.over.emit(platform.DragEvent{Kind: platform.DragLeave, X: g.x, Y: g.y, Own: true})
		}
		if w != nil {
			w.emit(platform.DragEvent{Kind: platform.DragEnter, X: x, Y: y, Own: true})
		}
	case w != nil:
		w.emit(platform.DragEvent{Kind: platform.DragMove, X: x, Y: y, Own: true})
	}
	g.over, g.x, g.y = w, x, y
}

// onPointer handles the grabbed pointer moving or its button coming up,
// which drops.
func (g *drag) onPointer(b *Backend, kind byte, rootX, rootY int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.on {
		return
	}
	switch kind {
	case evMotionNotify:
		g.update(b, rootX, rootY)
	case evButtonRelease:
		g.update(b, rootX, rootY)
		over, from := g.over, g.from
		g.end(b)
		if over != nil {
			over.emit(platform.DropEvent{X: g.x, Y: g.y, Own: true})
		}
		from.emit(platform.DragEndEvent{Dropped: over != nil, Outside: over == nil})
	}
}

// cancel ends the drag without a drop.
func (g *drag) cancel(b *Backend) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.on {
		return
	}
	over, from := g.over, g.from
	if over != nil {
		over.emit(platform.DragEvent{Kind: platform.DragLeave, X: g.x, Y: g.y, Own: true})
	}
	g.end(b)
	from.emit(platform.DragEndEvent{Outside: over == nil})
}

// end releases the pointer and keyboard and removes the icon. The caller
// holds g.mu.
func (g *drag) end(b *Backend) {
	t := b.in.now()
	b.c.Send(newReq(opUngrabPointer, 0).u32(t))
	b.c.Send(newReq(opUngrabKeyboard, 0).u32(t))
	if g.icon != 0 {
		b.c.Send(newReq(opDestroyWindow, 0).u32(g.icon))
	}
	g.on, g.icon = false, 0
}
