package win32

import (
	"image"
	"unsafe"

	"viewrab/internal/ui/platform"
)

// drag is a drag of the program's own, begun by StartDrag. The window it
// began in keeps the pointer captured, so its motion and the release that
// drops arrive there, and are passed on to the window under the pointer.
// It is touched only on the UI thread.
type drag struct {
	from    *window // the window it began in, or nil when no drag is under way
	over    *window // the window under the pointer, or nil
	x, y    float64 // the pointer, in over's coordinates
	hotspot image.Point
	icon    uintptr // the window that shows the icon, or zero
}

// StartDrag implements platform.Window. A drag begins only while a button
// pressed in the window is still held.
func (w *window) StartDrag(icon *image.RGBA, hotspot image.Point) bool {
	var ok bool
	w.b.call(func() {
		g := &w.b.drag
		if g.from != nil || w.buttons == 0 {
			return
		}
		*g = drag{from: w, hotspot: hotspot}
		p := cursorPos()
		if icon != nil {
			g.icon = newDragIcon(w.b, icon, point{p.X - int32(hotspot.X), p.Y - int32(hotspot.Y)})
		}
		g.update(p)
		ok = true
	})
	return ok
}

// update follows the pointer to the screen point p: the icon moves with it,
// and the windows it leaves and enters are told.
func (g *drag) update(p point) {
	if g.icon != 0 {
		procSetWindowPos.Call(g.icon, 0, uintptr(p.X-int32(g.hotspot.X)), uintptr(p.Y-int32(g.hotspot.Y)),
			0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
	}
	over := g.from.b.windowAt(p)
	var x, y float64
	if over != nil {
		c := p
		procScreenToClient.Call(over.hwnd, uintptr(unsafe.Pointer(&c)))
		x, y = float64(c.X), float64(c.Y)
	}
	switch {
	case over != g.over:
		if g.over != nil {
			g.over.emit(platform.DragEvent{Kind: platform.DragLeave, X: g.x, Y: g.y, Own: true})
		}
		if over != nil {
			over.emit(platform.DragEvent{Kind: platform.DragEnter, X: x, Y: y, Own: true})
		}
	case over != nil:
		over.emit(platform.DragEvent{Kind: platform.DragMove, X: x, Y: y, Own: true})
	}
	g.over, g.x, g.y = over, x, y
}

// drop ends the drag with the release at the screen point p: on the window
// under it, if one of the program's is.
func (g *drag) drop(p point) {
	g.update(p)
	from, over, x, y := g.from, g.over, g.x, g.y
	g.end()
	if over != nil {
		over.emit(platform.DropEvent{X: x, Y: y, Own: true})
	}
	from.emit(platform.DragEndEvent{Dropped: over != nil, Outside: over == nil})
}

// cancel ends the drag without a drop.
func (g *drag) cancel() {
	from, over := g.from, g.over
	if over != nil {
		over.emit(platform.DragEvent{Kind: platform.DragLeave, X: g.x, Y: g.y, Own: true})
	}
	g.end()
	from.emit(platform.DragEndEvent{Outside: over == nil})
}

// end removes the icon and gives up the pointer. The drag is over before the
// capture is released, as losing it would otherwise cancel the drag.
func (g *drag) end() {
	from, icon := g.from, g.icon
	*g = drag{}
	if icon != 0 {
		procDestroyWindow.Call(icon)
	}
	from.releasePointer()
}

// windowAt returns the program's window at the screen point p, or nil.
func (b *Backend) windowAt(p point) *window {
	h, _, _ := procWindowFromPoint.Call(uintptr(*(*uint64)(unsafe.Pointer(&p))))
	if h == 0 {
		return nil
	}
	root, _, _ := procGetAncestor.Call(h, gaRoot)
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.windows[root]
}

// newDragIcon shows img in a layered window whose top-left corner is at the
// screen point at. The window lets the pointer through to the windows beneath
// it. img is premultiplied, as UpdateLayeredWindow requires for per-pixel
// alpha.
func newDragIcon(b *Backend, img *image.RGBA, at point) uintptr {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	hwnd, _, _ := procCreateWindowEx.Call(wsExLayered|wsExTransparent|wsExToolWindow|wsExTopmost|wsExNoActivate,
		uintptr(unsafe.Pointer(utf16Ptr(className))), 0, wsPopup,
		uintptr(at.X), uintptr(at.Y), uintptr(w), uintptr(h), 0, 0, b.instance, 0)
	if hwnd == 0 {
		return 0
	}
	screen, _, _ := procGetDC.Call(0)
	defer procReleaseDC.Call(0, screen)
	mem, _, _ := procCreateCompatDC.Call(screen)
	defer procDeleteDC.Call(mem)
	bmp := newDIB(img)
	if bmp == 0 {
		procDestroyWindow.Call(hwnd)
		return 0
	}
	defer procDeleteObject.Call(bmp)
	old, _, _ := procSelectObject.Call(mem, bmp)
	defer procSelectObject.Call(mem, old)
	sz, src := size{int32(w), int32(h)}, point{}
	blend := blendFunction{op: acSrcOver, alpha: 255, format: acSrcAlpha}
	procUpdateLayeredWindow.Call(hwnd, screen, uintptr(unsafe.Pointer(&at)), uintptr(unsafe.Pointer(&sz)),
		mem, uintptr(unsafe.Pointer(&src)), 0, uintptr(unsafe.Pointer(&blend)), ulwAlpha)
	procShowWindow.Call(hwnd, swShowNA)
	return hwnd
}
