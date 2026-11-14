package wayland

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"viewer/internal/ui/platform"
)

// seatSetup holds a configured 100x100 window and the seat objects of a
// compositor whose wl_seat advertises the capabilities passed to
// newSeatSetup.
type seatSetup struct {
	f        *fakeWM
	srv      *server
	win      platform.Window
	surface  uint32
	pointer  chan uint32
	keyboard chan uint32
}

func newSeatSetup(t *testing.T, caps uint32) *seatSetup {
	t.Helper()
	ss := &seatSetup{pointer: make(chan uint32, 1), keyboard: make(chan uint32, 1)}
	a, b := pair(t)
	f := &fakeWM{t: t, log: make(chan string, 256), ids: map[string]uint32{},
		poolMem: map[uint32][]byte{}, bufMem: map[uint32][]byte{}, bufSize: map[uint32][3]int32{}}
	var seatID uint32
	f.afterBind = func(s *server, iface string, id uint32) {
		if iface == "wl_seat" {
			seatID = id
			s.send(id, seatCapabilites, func(e *Encoder) { e.Uint(caps) })
		}
	}
	globals := append(append([]Global(nil), wmGlobals...), Global{Name: 4, Interface: "wl_seat", Version: 5})
	base := f.handler(globals)
	ss.srv = serve(t, b, func(s *server, ev Event, d *Decoder) {
		if seatID != 0 && ev.Object == seatID {
			switch ev.Opcode {
			case seatGetPointer:
				ss.pointer <- d.Uint()
			case seatGetKeyboard:
				ss.keyboard <- d.Uint()
			}
			return
		}
		base(s, ev, d)
	})
	be, err := Open(a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { be.Close() })
	win, err := be.NewWindow(platform.WindowOptions{Size: platform.Size{W: 100, H: 100}})
	if err != nil {
		t.Fatal(err)
	}
	f.wait("commit")
	f.configure(ss.srv, 100, 100, 1)
	next(t, win)
	next(t, win)
	f.mu.Lock()
	ss.surface = f.surface
	f.mu.Unlock()
	ss.f, ss.win = f, win
	return ss
}

// keymapFD returns a memfd holding text, as a compositor sends it.
func keymapFD(t *testing.T, text string) int {
	t.Helper()
	fd, err := unix.MemfdCreate("keymap", unix.MFD_CLOEXEC)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unix.Write(fd, []byte(text)); err != nil {
		t.Fatal(err)
	}
	return fd
}

func TestPointerEvents(t *testing.T) {
	ss := newSeatSetup(t, seatCapPointer|seatCapKeyboard)
	p := <-ss.pointer
	<-ss.keyboard
	s, win := ss.srv, ss.win

	s.send(p, pointerEnter, func(e *Encoder) { e.Uint(1).Uint(ss.surface).Fixed(10.5).Fixed(20) })
	if ev := next(t, win); ev != (platform.PointerEvent{Kind: platform.Enter, X: 10.5, Y: 20}) {
		t.Errorf("enter = %#v", ev)
	}
	s.send(p, pointerMotion, func(e *Encoder) { e.Uint(0).Fixed(30).Fixed(40.25) })
	if ev := next(t, win); ev != (platform.PointerEvent{Kind: platform.Move, X: 30, Y: 40.25}) {
		t.Errorf("motion = %#v", ev)
	}
	for _, tc := range []struct {
		code   uint32
		state  uint32
		kind   platform.PointerKind
		button platform.Button
	}{
		{btnLeft, 1, platform.Press, platform.Left},
		{btnLeft, 0, platform.Release, platform.Left},
		{btnRight, 1, platform.Press, platform.Right},
		{btnMiddle, 1, platform.Press, platform.Middle},
		{0x113, 1, platform.Press, platform.None},
	} {
		s.send(p, pointerButton, func(e *Encoder) { e.Uint(1).Uint(0).Uint(tc.code).Uint(tc.state) })
		ev := next(t, win).(platform.PointerEvent)
		if ev.Kind != tc.kind || ev.Button != tc.button || ev.X != 30 || ev.Y != 40.25 {
			t.Errorf("button %#x = %#v", tc.code, ev)
		}
	}
	s.send(p, pointerAxis, func(e *Encoder) { e.Uint(0).Uint(axisVertical).Fixed(10) })
	s.send(p, pointerAxis, func(e *Encoder) { e.Uint(0).Uint(1).Fixed(-3) })
	if ev := next(t, win); ev != (platform.ScrollEvent{DY: 10, X: 30, Y: 40.25}) {
		t.Errorf("vertical scroll = %#v", ev)
	}
	if ev := next(t, win); ev != (platform.ScrollEvent{DX: -3, X: 30, Y: 40.25}) {
		t.Errorf("horizontal scroll = %#v", ev)
	}
	s.send(p, pointerLeave, func(e *Encoder) { e.Uint(1).Uint(ss.surface) })
	if ev := next(t, win); ev.(platform.PointerEvent).Kind != platform.Leave {
		t.Errorf("leave = %#v", ev)
	}
	// With no pointer focus, motion and axis events go nowhere.
	s.send(p, pointerMotion, func(e *Encoder) { e.Uint(0).Fixed(1).Fixed(1) })
	s.send(p, pointerAxis, func(e *Encoder) { e.Uint(0).Uint(0).Fixed(1) })
	s.send(p, 9, nil) // an event this client does not use
	s.send(p, pointerEnter, func(e *Encoder) { e.Uint(1).Uint(9999).Fixed(0).Fixed(0) })
	s.send(p, pointerMotion, func(e *Encoder) { e.Uint(0).Fixed(2).Fixed(2) })
	// The enter below must be the next event, which shows that the junk
	// events above produced none.
	s.send(p, pointerEnter, func(e *Encoder) { e.Uint(1).Uint(ss.surface).Fixed(7).Fixed(8) })
	if ev := next(t, win); ev != (platform.PointerEvent{Kind: platform.Enter, X: 7, Y: 8}) {
		t.Errorf("event after junk = %#v", ev)
	}
}

func TestKeyboardEvents(t *testing.T) {
	ss := newSeatSetup(t, seatCapKeyboard)
	k := <-ss.keyboard
	s, win := ss.srv, ss.win

	// Keys before a keymap arrives, or with no focus, produce nothing.
	s.send(k, keyboardKey, func(e *Encoder) { e.Uint(1).Uint(0).Uint(16).Uint(1) })
	text := sampleKeymap
	fd := keymapFD(t, text)
	s.send(k, keyboardKeymap, func(e *Encoder) { e.Uint(1).FD(fd).Uint(uint32(len(text))) })
	unix.Close(fd)
	s.send(k, keyboardKey, func(e *Encoder) { e.Uint(1).Uint(0).Uint(16).Uint(1) }) // no focus yet

	s.send(k, keyboardEnter, func(e *Encoder) { e.Uint(1).Uint(ss.surface).Array([]byte{1, 0, 0, 0}) })
	if ev := next(t, win); ev != (platform.FocusEvent{Focused: true}) {
		t.Fatalf("enter = %#v", ev)
	}
	s.send(k, keyboardKey, func(e *Encoder) { e.Uint(1).Uint(0).Uint(16).Uint(1) })
	if ev := next(t, win); ev != (platform.KeyEvent{Rune: 'q', Down: true}) {
		t.Errorf("q = %#v", ev)
	}
	s.send(k, keyboardModifiers, func(e *Encoder) { e.Uint(1).Uint(modShift).Uint(0).Uint(modCtrl).Uint(0) })
	s.send(k, keyboardKey, func(e *Encoder) { e.Uint(1).Uint(0).Uint(16).Uint(0) })
	if ev := next(t, win); ev != (platform.KeyEvent{Rune: 'Q', Mods: platform.Shift | platform.Ctrl}) {
		t.Errorf("shift+ctrl q release = %#v", ev)
	}
	s.send(k, keyboardKey, func(e *Encoder) { e.Uint(1).Uint(0).Uint(28).Uint(1) })
	if ev := next(t, win); ev != (platform.KeyEvent{Key: platform.KeyEnter, Down: true, Mods: platform.Shift | platform.Ctrl}) {
		t.Errorf("enter key = %#v", ev)
	}
	s.send(k, 9, nil) // an event this client does not use
	s.send(k, keyboardLeave, func(e *Encoder) { e.Uint(1).Uint(ss.surface) })
	if ev := next(t, win); ev != (platform.FocusEvent{}) {
		t.Errorf("leave = %#v", ev)
	}
	// An enter for an unknown surface, followed by a leave, produces no events.
	s.send(k, keyboardEnter, func(e *Encoder) { e.Uint(1).Uint(9999).Array(nil) })
	s.send(k, keyboardLeave, func(e *Encoder) { e.Uint(1).Uint(9999) })
	s.send(k, keyboardEnter, func(e *Encoder) { e.Uint(1).Uint(ss.surface).Array(nil) })
	if ev := next(t, win); ev != (platform.FocusEvent{Focused: true}) {
		t.Errorf("event after junk = %#v", ev)
	}
}

func TestSeatCapabilities(t *testing.T) {
	// Capability changes after the first do not create objects twice, and
	// unknown seat events are ignored.
	ss := newSeatSetup(t, seatCapPointer)
	<-ss.pointer
	ss.srv.send(ss.f.ids["wl_seat"], seatCapabilites, func(e *Encoder) { e.Uint(seatCapPointer | seatCapKeyboard) })
	ss.srv.send(ss.f.ids["wl_seat"], seatCapabilites, func(e *Encoder) { e.Uint(seatCapPointer | seatCapKeyboard) })
	ss.srv.send(ss.f.ids["wl_seat"], 1, func(e *Encoder) { e.String("seat0") }) // wl_seat.name
	<-ss.keyboard
	select {
	case <-ss.pointer:
		t.Error("a second pointer was created")
	default:
	}
	select {
	case <-ss.keyboard:
		t.Error("a second keyboard was created")
	default:
	}
}

func TestReadKeymapFailures(t *testing.T) {
	if readKeymap(-1, 10) != nil {
		t.Error("keymap from fd -1")
	}
	// A descriptor that cannot be read (a directory) or holds garbage.
	dir, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fd, _ := unix.Dup(int(dir.Fd()))
	dir.Close()
	if readKeymap(fd, 10) != nil {
		t.Error("keymap from a directory")
	}
	if readKeymap(keymapFD(t, "garbage"), 7) != nil {
		t.Error("keymap from garbage")
	}
}

func TestMapButton(t *testing.T) {
	if mapButton(btnLeft) != platform.Left || mapButton(btnRight) != platform.Right ||
		mapButton(btnMiddle) != platform.Middle || mapButton(1) != platform.None {
		t.Error("mapButton")
	}
}

func TestOpenFailsWhenTheSeatRoundtripDies(t *testing.T) {
	a, b := pair(t)
	f := &fakeWM{t: t, log: make(chan string, 64), ids: map[string]uint32{}}
	f.afterBind = func(s *server, iface string, id uint32) {
		if iface == "wl_seat" {
			s.conn.Close() // hang up instead of answering the roundtrip
		}
	}
	globals := append(append([]Global(nil), wmGlobals...), Global{Name: 4, Interface: "wl_seat", Version: 5})
	serve(t, b, f.handler(globals))
	if _, err := Open(a); err == nil {
		t.Error("Open succeeded although the compositor hung up")
	}
}

// focusedKeyboard returns a seat whose keyboard has the sample keymap and
// focus, announcing the key repeat settings given (milliseconds).
func focusedKeyboard(t *testing.T, rate, delay int32) (*seatSetup, uint32) {
	t.Helper()
	ss := newSeatSetup(t, seatCapKeyboard)
	k := <-ss.keyboard
	fd := keymapFD(t, sampleKeymap)
	ss.srv.send(k, keyboardKeymap, func(e *Encoder) { e.Uint(1).FD(fd).Uint(uint32(len(sampleKeymap))) })
	unix.Close(fd)
	ss.srv.send(k, keyboardRepeat, func(e *Encoder) { e.Int(rate).Int(delay) })
	ss.srv.send(k, keyboardEnter, func(e *Encoder) { e.Uint(1).Uint(ss.surface).Array(nil) })
	if ev := next(t, ss.win); ev != (platform.FocusEvent{Focused: true}) {
		t.Fatalf("enter = %#v", ev)
	}
	return ss, k
}

// quiet reports whether win delivers no event for d.
func quiet(win platform.Window, d time.Duration) bool {
	select {
	case <-win.Events():
		return false
	case <-time.After(d):
		return true
	}
}

// drain discards events until none arrives for a while, or the window closes.
func drain(win platform.Window) {
	for {
		select {
		case _, ok := <-win.Events():
			if !ok {
				return
			}
		case <-time.After(40 * time.Millisecond):
			return
		}
	}
}

func TestHeldKeysRepeatUntilReleased(t *testing.T) {
	ss, k := focusedKeyboard(t, 200, 20)
	s, win := ss.srv, ss.win
	press := func(code, state uint32) {
		s.send(k, keyboardKey, func(e *Encoder) { e.Uint(1).Uint(0).Uint(code).Uint(state) })
	}

	press(16, 1)
	want := platform.KeyEvent{Rune: 'q', Down: true}
	for i := 0; i < 4; i++ { // the press and then repeats of it
		if ev := next(t, win); ev != want {
			t.Fatalf("event %d = %#v, want %#v", i, ev, want)
		}
	}
	// Pressing another key moves the repeat to that key. Repeats of q already
	// on their way may still arrive; after them only Return repeats.
	press(28, 1)
	enter := platform.KeyEvent{Key: platform.KeyEnter, Down: true}
	for ev := next(t, win); ev != enter; ev = next(t, win) {
		if ev != want {
			t.Fatalf("event %#v while the repeat changed hands", ev)
		}
	}
	// Releasing a key that is not repeating changes nothing; releasing the
	// repeating one stops it.
	press(16, 0)
	released := platform.KeyEvent{Rune: 'q'}
	for ev := next(t, win); ev != enter; ev = next(t, win) {
		if ev != released {
			t.Fatalf("after releasing q: %#v", ev)
		}
	}
	if ev := next(t, win); ev != enter {
		t.Errorf("Return stopped repeating when q was released: %#v", ev)
	}
	press(28, 0)
	drain(win)
	if !quiet(win, 80*time.Millisecond) {
		t.Error("a released key kept repeating")
	}
	// A key with no symbol is not repeated.
	press(3, 1)
	if ev := next(t, win); ev != (platform.KeyEvent{Down: true}) {
		t.Errorf("symbol-less key = %#v", ev)
	}
	if !quiet(win, 80*time.Millisecond) {
		t.Error("a key with no symbol repeated")
	}
}

func TestKeyRepeatStopsWhenFocusIsLost(t *testing.T) {
	ss, k := focusedKeyboard(t, 200, 20)
	s, win := ss.srv, ss.win
	s.send(k, keyboardKey, func(e *Encoder) { e.Uint(1).Uint(0).Uint(16).Uint(1) })
	next(t, win)
	next(t, win) // repeating
	s.send(k, keyboardLeave, func(e *Encoder) { e.Uint(1).Uint(ss.surface) })
	drain(win)
	if !quiet(win, 80*time.Millisecond) {
		t.Error("a key kept repeating after the window lost focus")
	}
}

func TestKeyRepeatCanBeSwitchedOff(t *testing.T) {
	ss, k := focusedKeyboard(t, 0, 20)
	ss.srv.send(k, keyboardKey, func(e *Encoder) { e.Uint(1).Uint(0).Uint(16).Uint(1) })
	next(t, ss.win)
	if !quiet(ss.win, 100*time.Millisecond) {
		t.Error("a key repeated although the compositor turned repeating off")
	}
}

func TestKeyRepeatEndsWithTheConnection(t *testing.T) {
	ss, k := focusedKeyboard(t, 200, 20)
	ss.srv.send(k, keyboardKey, func(e *Encoder) { e.Uint(1).Uint(0).Uint(16).Uint(1) })
	next(t, ss.win)
	ss.srv.conn.Close() // the compositor goes away while a key is held
	drain(ss.win)
}
