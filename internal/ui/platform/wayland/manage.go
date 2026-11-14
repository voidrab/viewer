package wayland

import "viewer/internal/ui/platform"

// Opcodes of wp_cursor_shape_manager_v1 and wp_cursor_shape_device_v1.
const (
	cursorManagerGetPointer = 1
	cursorDeviceSetShape    = 1
)

// cursorShapes maps each cursor to its wp_cursor_shape_device_v1 shape.
var cursorShapes = map[platform.Cursor]uint32{
	platform.CursorDefault:  1,
	platform.CursorResizeE:  18,
	platform.CursorResizeN:  19,
	platform.CursorResizeNE: 20,
	platform.CursorResizeNW: 21,
	platform.CursorResizeS:  22,
	platform.CursorResizeSE: 23,
	platform.CursorResizeSW: 24,
	platform.CursorResizeW:  25,
}

// newCursorDevice makes the cursor shape device of the seat's pointer, when
// the compositor offers wp_cursor_shape_manager_v1. Without it, the client
// sets no cursor and the compositor chooses the one it shows.
func (b *Backend) newCursorDevice() {
	if b.cursorShape == 0 {
		return
	}
	id := b.c.NewID(nil)
	b.c.Send(b.cursorShape, cursorManagerGetPointer, func(e *Encoder) { e.Uint(id).Uint(b.in.pointer) })
	b.in.cursorDevice.Store(id)
}

// setShape shows c as the pointer's image, as of the latest pointer entry.
func (b *Backend) setShape(c platform.Cursor) {
	if dev := b.in.cursorDevice.Load(); dev != 0 {
		serial := b.in.enterSerial.Load()
		b.c.Send(dev, cursorDeviceSetShape, func(e *Encoder) { e.Uint(serial).Uint(cursorShapes[c]) })
	}
}

// SetCursor implements platform.Window. The window shows c from now on
// whenever the pointer enters it.
func (w *window) SetCursor(c platform.Cursor) {
	w.mu.Lock()
	w.cursor = c
	w.mu.Unlock()
	w.b.setShape(c)
}

// currentCursor returns the cursor the window shows.
func (w *window) currentCursor() platform.Cursor {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cursor
}

// Move implements platform.Window with xdg_toplevel.move.
func (w *window) Move() {
	if w.b.seat == 0 {
		return
	}
	w.b.c.Send(w.toplevel, toplevelMove, func(e *Encoder) { e.Uint(w.b.seat).Uint(w.b.in.lastSerial()) })
}

// Resize implements platform.Window with xdg_toplevel.resize.
func (w *window) Resize(edges platform.Edge) {
	if w.b.seat == 0 {
		return
	}
	w.b.c.Send(w.toplevel, toplevelResize, func(e *Encoder) {
		e.Uint(w.b.seat).Uint(w.b.in.lastSerial()).Uint(uint32(edges))
	})
}

// ShowMenu implements platform.Window with xdg_toplevel.show_window_menu.
func (w *window) ShowMenu(x, y int) {
	if w.b.seat == 0 {
		return
	}
	w.b.c.Send(w.toplevel, toplevelShowWindowMenu, func(e *Encoder) {
		e.Uint(w.b.seat).Uint(w.b.in.lastSerial()).Int(int32(x)).Int(int32(y))
	})
}

// SetMaximized implements platform.Window.
func (w *window) SetMaximized(maximized bool) {
	op := uint16(toplevelUnsetMaximized)
	if maximized {
		op = toplevelSetMaximized
	}
	w.b.c.Send(w.toplevel, op, nil)
}

// Minimize implements platform.Window.
func (w *window) Minimize() { w.b.c.Send(w.toplevel, toplevelSetMinimized, nil) }
