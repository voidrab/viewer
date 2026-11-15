package x11

import "viewrab/internal/ui/platform"

// Crossing and focus details and modes.
const (
	notifyInferior = 2
	notifyPointer  = 5

	notifyNormal = 0
	notifyGrab   = 1
	notifyUngrab = 2
)

// scrollStep is how far one click of a wheel scrolls, in the units of a
// ScrollEvent: what Wayland compositors report for a click.
const scrollStep = 10

// wheelSteps maps the buttons a wheel clicks, up, down, left and right, to
// the scroll each click makes.
var wheelSteps = map[byte][2]float64{4: {0, -scrollStep}, 5: {0, scrollStep}, 6: {-scrollStep, 0}, 7: {scrollStep, 0}}

// Button state bits of the core modifier state.
const stateButtons = 0x1f00

// onKey handles a key press or release in a window with focus.
func (b *Backend) onKey(ev []byte) {
	code, t, id, state := ev[1], u32At(ev, 4), u32At(ev, 12), u16At(ev, 28)
	in := &b.in
	in.setTime(t)
	in.mu.Lock()
	km := in.keymap
	in.mu.Unlock()
	key, r := km.lookup(code, state)
	down := ev[0]&0x7f == evKeyPress
	if b.drag.active() {
		if down && key == platform.KeyEscape {
			b.drag.cancel(b)
		}
		return
	}
	w := b.windowFor(id)
	if w == nil {
		return
	}
	if !down {
		// Without XKB's detectable repeat, the server reports a repeat of a
		// held key as a release and a press with the same time. The release
		// is dropped, so that the repeat is reported only as a press.
		if next, ok := b.c.Peek(); ok && next[0]&0x7f == evKeyPress && next[1] == code && u32At(next, 4) == t {
			return
		}
	}
	w.emit(platform.KeyEvent{Key: key, Rune: r, Down: down, Mods: km.mods(state)})
}

// onPointer handles a button press or release, or pointer motion.
func (b *Backend) onPointer(ev []byte) {
	kind, button, t, id := ev[0]&0x7f, ev[1], u32At(ev, 4), u32At(ev, 12)
	rootX, rootY, x, y, state := i16At(ev, 20), i16At(ev, 22), i16At(ev, 24), i16At(ev, 26), u16At(ev, 28)
	in := &b.in
	in.setTime(t)
	if kind == evMotionNotify {
		// Motion already queued after this supersedes it.
		if next, ok := b.c.Peek(); ok && next[0]&0x7f == evMotionNotify && u32At(next, 12) == id {
			return
		}
	}
	if b.drag.active() {
		b.drag.onPointer(b, kind, rootX, rootY)
		return
	}
	w := b.windowFor(id)
	if w == nil {
		return
	}
	in.mu.Lock()
	mods := in.keymap.mods(state)
	in.mu.Unlock()
	fx, fy := float64(x), float64(y)
	switch kind {
	case evMotionNotify:
		w.emit(platform.PointerEvent{Kind: platform.Move, X: fx, Y: fy, Mods: mods})
	case evButtonPress:
		if d, ok := wheelSteps[button]; ok {
			s := platform.ScrollEvent{DX: d[0], DY: d[1], X: fx, Y: fy, Mods: mods}
			w.emit(s)
			return
		}
		in.mu.Lock()
		in.press = press{button: button, rootX: rootX, rootY: rootY, x: x, y: y}
		in.mu.Unlock()
		w.emit(platform.PointerEvent{Kind: platform.Press, Button: mapButton(button), X: fx, Y: fy, Mods: mods})
	case evButtonRelease:
		if _, ok := wheelSteps[button]; ok {
			return
		}
		w.emit(platform.PointerEvent{Kind: platform.Release, Button: mapButton(button), X: fx, Y: fy, Mods: mods})
		// The pointer left while a button held it. It counts as having left
		// once the last button is up.
		in.mu.Lock()
		left := in.heldLeave == w && state&stateButtons&^(0x80<<button) == 0
		if left {
			in.heldLeave = nil
			if in.pointer == w {
				in.pointer = nil
			}
		}
		in.mu.Unlock()
		if left {
			w.emit(platform.PointerEvent{Kind: platform.Leave, X: fx, Y: fy, Mods: mods})
		}
	}
}

// mapButton converts a core button number.
func mapButton(n byte) platform.Button {
	switch n {
	case 1:
		return platform.Left
	case 2:
		return platform.Middle
	case 3:
		return platform.Right
	}
	return platform.None
}

// onCrossing handles the pointer entering or leaving a window. A window
// manager taking the pointer, for a move for example, counts as a leave, and
// giving it back counts as an enter. As on Wayland, the pointer leaves only
// once no button holds it in the window.
func (b *Backend) onCrossing(ev []byte) {
	detail, t, id, mode := ev[1], u32At(ev, 4), u32At(ev, 12), ev[30]
	x, y, state := float64(i16At(ev, 24)), float64(i16At(ev, 26)), u16At(ev, 28)
	b.in.setTime(t)
	w := b.windowFor(id)
	if w == nil || detail == notifyInferior || b.drag.active() {
		return
	}
	enter := ev[0]&0x7f == evEnterNotify
	in := &b.in
	in.mu.Lock()
	mods := in.keymap.mods(state)
	var kind platform.PointerKind
	switch {
	case enter && mode != notifyGrab:
		in.heldLeave = nil
		if in.pointer == w {
			in.mu.Unlock()
			return
		}
		in.pointer, kind = w, platform.Enter
	case !enter && mode != notifyUngrab:
		if in.pointer != w {
			in.mu.Unlock()
			return
		}
		if mode == notifyNormal && state&stateButtons != 0 {
			in.heldLeave = w
			in.mu.Unlock()
			return
		}
		in.pointer, kind = nil, platform.Leave
	default:
		in.mu.Unlock()
		return
	}
	in.mu.Unlock()
	w.emit(platform.PointerEvent{Kind: kind, X: x, Y: y, Mods: mods})
}

// onFocus handles a window gaining or losing keyboard focus. Grabs, such as
// the window manager's while it switches windows, do not change the focus.
func (b *Backend) onFocus(ev []byte) {
	detail, id, mode := ev[1], u32At(ev, 4), ev[8]
	w := b.windowFor(id)
	if w == nil || detail == notifyPointer || mode == notifyGrab || mode == notifyUngrab {
		return
	}
	focused := ev[0]&0x7f == evFocusIn
	in := &b.in
	in.mu.Lock()
	switch {
	case focused && in.focus != w:
		in.focus = w
	case !focused && in.focus == w:
		in.focus = nil
	default:
		in.mu.Unlock()
		return
	}
	in.mu.Unlock()
	w.emit(platform.FocusEvent{Focused: focused})
}
