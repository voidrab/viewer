package wayland

import (
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"viewer/internal/ui/platform"
)

// Opcodes, events and constants of wl_seat, wl_pointer and wl_keyboard.
const (
	seatGetPointer  = 0
	seatGetKeyboard = 1
	seatCapabilites = 0

	seatCapPointer  = 1
	seatCapKeyboard = 2

	pointerEnter  = 0
	pointerLeave  = 1
	pointerMotion = 2
	pointerButton = 3
	pointerAxis   = 4

	keyboardKeymap    = 0
	keyboardEnter     = 1
	keyboardLeave     = 2
	keyboardKey       = 3
	keyboardModifiers = 4
	keyboardRepeat    = 5

	axisVertical = 0

	btnLeft   = 0x110
	btnRight  = 0x111
	btnMiddle = 0x112

	buttonPressed = 1
)

// defaultRepeatRate and defaultRepeatDelay are the key repeat settings used
// until the compositor announces its own.
const (
	defaultRepeatRate  = 25
	defaultRepeatDelay = 600 * time.Millisecond
)

// input is the seat state shared by all windows. It is touched only by the
// dispatching goroutine.
type input struct {
	pointer, keyboard uint32 // object IDs, zero until created
	pointerWin        *window
	keyboardWin       *window
	px, py            float64
	keymap            *Keymap
	mods, group       uint32
	serial            atomic.Uint32 // that of the latest event that carried one
	enterSerial       atomic.Uint32 // that of the latest pointer entry
	cursorDevice      atomic.Uint32 // the pointer's wp_cursor_shape_device_v1, or zero

	// The compositor does not repeat keys; the client does, at the rate and
	// after the delay it announces. Without an announcement these defaults, the
	// ones most toolkits use, apply. A rate of zero turns repeating off.
	repeatRate  int32
	repeatDelay time.Duration
	repeating   *keyRepeat // the key being repeated, if any
}

// lastSerial returns the serial of the latest input event. The compositor
// requires it with requests, such as setting the clipboard, that an input event
// caused.
func (in *input) lastSerial() uint32 { return in.serial.Load() }

// keyRepeat is a held key being repeated until stop is closed.
type keyRepeat struct {
	code uint32
	stop chan struct{}
	once sync.Once
}

func (k *keyRepeat) end() { k.once.Do(func() { close(k.stop) }) }

// startRepeat repeats the press ev of the key with the given code in w.
func (in *input) startRepeat(w *window, code uint32, ev platform.KeyEvent) {
	in.stopRepeat()
	if in.repeatRate <= 0 {
		return
	}
	k := &keyRepeat{code: code, stop: make(chan struct{})}
	in.repeating = k
	delay, interval := in.repeatDelay, time.Second/time.Duration(in.repeatRate)
	go func() {
		t := time.NewTimer(delay)
		defer t.Stop()
		for {
			select {
			case <-k.stop:
				return
			case <-t.C:
				w.emit(ev)
				t.Reset(interval)
			}
		}
	}()
}

// stopRepeat ends the repeat in progress.
func (in *input) stopRepeat() {
	if in.repeating != nil {
		in.repeating.end()
		in.repeating = nil
	}
}

// onSeat creates the pointer and keyboard objects the seat offers.
func (b *Backend) onSeat(opcode uint16, d *Decoder) {
	if opcode != seatCapabilites {
		return
	}
	caps := d.Uint()
	if caps&seatCapPointer != 0 && b.in.pointer == 0 {
		b.in.pointer = b.c.NewID(b.onPointer)
		b.c.Send(b.seat, seatGetPointer, func(e *Encoder) { e.Uint(b.in.pointer) })
		b.newCursorDevice()
	}
	if caps&seatCapKeyboard != 0 && b.in.keyboard == 0 {
		b.in.keyboard = b.c.NewID(b.onKeyboard)
		b.c.Send(b.seat, seatGetKeyboard, func(e *Encoder) { e.Uint(b.in.keyboard) })
	}
}

// windowFor returns the window whose wl_surface has the given ID.
func (b *Backend) windowFor(surface uint32) *window {
	b.mu.Lock()
	defer b.mu.Unlock()
	for w := range b.windows {
		if w.surface == surface {
			return w
		}
	}
	return nil
}

func (b *Backend) onPointer(opcode uint16, d *Decoder) {
	in := &b.in
	switch opcode {
	case pointerEnter:
		serial, surface, x, y := d.Uint(), d.Uint(), d.Fixed(), d.Fixed()
		in.serial.Store(serial)
		in.enterSerial.Store(serial)
		in.pointerWin, in.px, in.py = b.windowFor(surface), x, y
		// The cursor is the client's to set on every entry.
		if in.pointerWin != nil {
			b.setShape(in.pointerWin.currentCursor())
		}
		in.emitPointer(platform.Enter, platform.None)
	case pointerLeave:
		in.emitPointer(platform.Leave, platform.None)
		in.pointerWin = nil
	case pointerMotion:
		_, x, y := d.Uint(), d.Fixed(), d.Fixed()
		in.px, in.py = x, y
		in.emitPointer(platform.Move, platform.None)
	case pointerButton:
		serial, _, button, state := d.Uint(), d.Uint(), d.Uint(), d.Uint()
		in.serial.Store(serial)
		kind := platform.Release
		if state == buttonPressed {
			kind = platform.Press
		}
		in.emitPointer(kind, mapButton(button))
	case pointerAxis:
		_, axis, value := d.Uint(), d.Uint(), d.Fixed()
		if in.pointerWin == nil {
			return
		}
		ev := platform.ScrollEvent{X: in.px, Y: in.py, Mods: modMask(in.mods)}
		if axis == axisVertical {
			ev.DY = value
		} else {
			ev.DX = value
		}
		in.pointerWin.emit(ev)
	}
}

func (in *input) emitPointer(kind platform.PointerKind, button platform.Button) {
	if in.pointerWin == nil {
		return
	}
	in.pointerWin.emit(platform.PointerEvent{Kind: kind, Button: button, X: in.px, Y: in.py, Mods: modMask(in.mods)})
}

// mapButton converts a Linux input button code.
func mapButton(code uint32) platform.Button {
	switch code {
	case btnLeft:
		return platform.Left
	case btnRight:
		return platform.Right
	case btnMiddle:
		return platform.Middle
	}
	return platform.None
}

func (b *Backend) onKeyboard(opcode uint16, d *Decoder) {
	in := &b.in
	switch opcode {
	case keyboardKeymap:
		_, fd, size := d.Uint(), d.FD(), int(d.Uint())
		in.keymap = readKeymap(fd, size)
	case keyboardEnter:
		serial, surface := d.Uint(), d.Uint()
		in.serial.Store(serial)
		d.Array() // the keys held on entry
		in.keyboardWin = b.windowFor(surface)
		if in.keyboardWin != nil {
			in.keyboardWin.emit(platform.FocusEvent{Focused: true})
		}
	case keyboardLeave:
		in.stopRepeat()
		if in.keyboardWin != nil {
			in.keyboardWin.emit(platform.FocusEvent{})
		}
		in.keyboardWin = nil
	case keyboardKey:
		serial, _, code, state := d.Uint(), d.Uint(), d.Uint(), d.Uint()
		in.serial.Store(serial)
		if in.keyboardWin == nil || in.keymap == nil {
			return
		}
		key, r := in.keymap.Lookup(code, in.mods, in.group)
		ev := platform.KeyEvent{Key: key, Rune: r, Down: state == buttonPressed, Mods: modMask(in.mods)}
		in.keyboardWin.emit(ev)
		switch {
		case ev.Down && (key != platform.KeyNone || r != 0): // modifiers do not repeat
			in.startRepeat(in.keyboardWin, code, ev)
		case !ev.Down && in.repeating != nil && in.repeating.code == code:
			in.stopRepeat()
		}
	case keyboardRepeat:
		rate, delay := d.Int(), d.Int()
		in.repeatRate, in.repeatDelay = rate, time.Duration(delay)*time.Millisecond
	case keyboardModifiers:
		_, depressed, latched, locked, group := d.Uint(), d.Uint(), d.Uint(), d.Uint(), d.Uint()
		in.mods, in.group = depressed|latched|locked, group
	}
}

// readKeymap reads and parses a keymap of size bytes from fd, which it
// closes. It returns nil if the keymap cannot be used.
func readKeymap(fd, size int) *Keymap {
	if fd < 0 {
		return nil
	}
	f := os.NewFile(uintptr(fd), "keymap")
	defer f.Close()
	// Read from offset zero, ignoring the descriptor's file offset.
	text, err := io.ReadAll(io.NewSectionReader(f, 0, int64(size)))
	if err != nil {
		return nil
	}
	km, err := ParseKeymap(string(text))
	if err != nil {
		return nil
	}
	return km
}
