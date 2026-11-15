package x11

import (
	"testing"

	"viewer/internal/ui/platform"
)

func TestKeymapLookup(t *testing.T) {
	// Codes 8 to 13, four groups' worth of columns each: a letter with a
	// third level, a single lower-case letter, a key with a second group, a
	// keypad key, a Cyrillic letter given alone, and a code without keysyms.
	km := &keymap{min: 8, per: 6, numLock: 0x10, modeSwitch: 0x20, level3: 0x80, alt: 0x8, super: 0x40, syms: []uint32{
		'q', 'Q', 0, 0, '@', 0x7d9,
		'z', 0, 0, 0, 0, 0,
		'y', 'Y', 0x6ce, 0x6ee, 0, 0,
		0xff9c, 0xffb1, 0, 0, 0, 0,
		0x6c1, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0,
	}}
	for _, tc := range []struct {
		code  byte
		state uint16
		key   platform.Key
		r     rune
	}{
		{8, 0, 0, 'q'},
		{8, stateShift, 0, 'Q'},
		{8, stateLock, 0, 'Q'},
		{8, stateLock | stateShift, 0, 'q'},
		{8, 0x80, 0, '@'},
		{8, 0x80 | stateShift, 0, 'Ω'},
		{9, stateShift, 0, 'Z'},        // the case pair of a lone letter
		{9, 0x80, 0, 'z'},              // a missing third level falls back
		{9, 0x80 | stateShift, 0, 'Z'}, // and so does a missing fourth
		{10, 0x20, 0, 'н'},             // Mode_switch picks the second group
		{10, 1 << 13, 0, 'н'},          // as does an XKB group in the state
		{10, 1<<13 | stateShift, 0, 'Н'},
		{8, 1 << 13, 0, 'q'}, // a key without a second group uses its first
		{11, 0, platform.KeyEnd, 0},
		{11, 0x10, 0, '1'},
		{11, 0x10 | stateShift, platform.KeyEnd, 0},
		{12, stateShift, 0, 'А'},
		{13, 0, 0, 0},
		{7, 0, 0, 0},  // below the first code
		{60, 0, 0, 0}, // past the last
	} {
		if k, r := km.lookup(tc.code, tc.state); k != tc.key || r != tc.r {
			t.Errorf("lookup(%d, %#x) = %v %q, want %v %q", tc.code, tc.state, k, r, tc.key, tc.r)
		}
	}
	if m := km.mods(stateShift | stateControl | 0x8 | 0x40); m != platform.Shift|platform.Ctrl|platform.Alt|platform.Super {
		t.Errorf("mods = %v", m)
	}
	if (&keymap{}).keysyms(8) != nil {
		t.Error("an empty keymap has keysyms")
	}
}

func TestLoadKeymapFindsModifiers(t *testing.T) {
	_, b := startBackend(t, func(f *fakeX) {
		// Alt_L on Mod3, Super_R on Mod2 beside Num_Lock, Mode_switch on Mod5.
		f.keysyms = append(f.keysyms, ksAltL, 0, ksSuperR, 0, ksModeSwitch, 0)
		f.modmap[5] = []byte{16}
		f.modmap[4] = []byte{13, 17}
		f.modmap[7] = []byte{15, 18}
	})
	km := b.in.keymap
	if km.alt != 1<<5 || km.super != 1<<4 || km.numLock != 1<<4 || km.modeSwitch != 1<<7 || km.level3 != 1<<7 {
		t.Errorf("alt %#x super %#x numLock %#x modeSwitch %#x level3 %#x", km.alt, km.super, km.numLock, km.modeSwitch, km.level3)
	}
	// Defaults where the mapping binds no Alt or Super.
	_, b = startBackend(t, nil)
	if km := b.in.keymap; km.alt != 1<<3 || km.super != 1<<6 {
		t.Errorf("default alt %#x super %#x", km.alt, km.super)
	}
}

func TestKeyEvents(t *testing.T) {
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{})
	f.event(inputEv(evKeyPress, 8, 10, w.id, 0, 0, 0, 0, stateShift|stateControl))
	if k := want[platform.KeyEvent](t, w); k.Rune != 'A' || !k.Down || k.Mods != platform.Shift|platform.Ctrl {
		t.Errorf("key = %+v", k)
	}
	f.event(inputEv(evKeyRelease, 10, 11, w.id, 0, 0, 0, 0, 0))
	if k := want[platform.KeyEvent](t, w); k.Key != platform.KeyEnter || k.Down {
		t.Errorf("release = %+v", k)
	}
	// A repeat without XKB: a release and a press at the same time. Only the
	// press is reported. They arrive together, as the server sends them.
	f.events(inputEv(evKeyRelease, 9, 12, w.id, 0, 0, 0, 0, 0), inputEv(evKeyPress, 9, 12, w.id, 0, 0, 0, 0, 0))
	if k := want[platform.KeyEvent](t, w); k.Rune != 'b' || !k.Down {
		t.Errorf("repeat = %+v", k)
	}
	f.event(inputEv(evKeyPress, 9, 13, 0x999, 0, 0, 0, 0, 0)) // an unknown window
	quiet(t, f, w)
	// A changed keyboard mapping is fetched again; a pointer mapping is not.
	f.set(func(f *fakeX) { f.keysyms[0], f.keysyms[1] = 'x', 'X' })
	f.event(xev(evMappingNotify, func(b []byte) { b[4] = 1 }))
	settle(t, f, w)
	f.event(inputEv(evKeyPress, 8, 14, w.id, 0, 0, 0, 0, 0))
	if k := want[platform.KeyEvent](t, w); k.Rune != 'x' {
		t.Errorf("after remapping = %+v", k)
	}
}

func TestPointerEvents(t *testing.T) {
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{})
	f.event(inputEv(evButtonPress, 1, 20, w.id, 110, 120, 10, 20, stateControl))
	if p := want[platform.PointerEvent](t, w); p.Kind != platform.Press || p.Button != platform.Left || p.X != 10 || p.Y != 20 || p.Mods != platform.Ctrl {
		t.Errorf("press = %+v", p)
	}
	if b.in.press != (press{button: 1, rootX: 110, rootY: 120, x: 10, y: 20}) || b.in.now() != 20 {
		t.Errorf("press recorded %+v at %d", b.in.press, b.in.now())
	}
	for _, tc := range []struct {
		button byte
		want   platform.Button
	}{{2, platform.Middle}, {3, platform.Right}, {8, platform.None}} {
		f.event(inputEv(evButtonRelease, tc.button, 21, w.id, 0, 0, 1, 2, 0))
		if p := want[platform.PointerEvent](t, w); p.Kind != platform.Release || p.Button != tc.want {
			t.Errorf("release %d = %+v", tc.button, p)
		}
	}
	for button, d := range map[byte][2]float64{4: {0, -10}, 5: {0, 10}, 6: {-10, 0}, 7: {10, 0}} {
		f.event(inputEv(evButtonPress, button, 22, w.id, 0, 0, 3, 4, stateShift))
		if s := want[platform.ScrollEvent](t, w); s.DX != d[0] || s.DY != d[1] || s.X != 3 || s.Mods != platform.Shift {
			t.Errorf("wheel %d = %+v", button, s)
		}
		f.event(inputEv(evButtonRelease, button, 22, w.id, 0, 0, 3, 4, 0)) // ignored
	}
	quiet(t, f, w)
	// Motion: of several queued, only the last is reported.
	f.events(inputEv(evMotionNotify, 0, 23, w.id, 0, 0, 5, 6, 0), inputEv(evMotionNotify, 0, 23, w.id, 0, 0, 6, 6, 0),
		inputEv(evMotionNotify, 0, 23, w.id, 0, 0, 7, 6, 0))
	if p := want[platform.PointerEvent](t, w); p.Kind != platform.Move || p.X != 7 {
		t.Errorf("motion = %+v", p)
	}
	f.event(inputEv(evMotionNotify, 0, 23, 0x999, 0, 0, 0, 0, 0))
	quiet(t, f, w)
}

func TestCrossingAndFocus(t *testing.T) {
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{})
	kind := func() platform.PointerKind { return want[platform.PointerEvent](t, w).Kind }
	f.event(crossEv(evEnterNotify, 0, w.id, 1, 2, 0, notifyNormal))
	if k := kind(); k != platform.Enter {
		t.Errorf("enter = %v", k)
	}
	f.event(crossEv(evEnterNotify, 0, w.id, 1, 2, 0, notifyNormal))              // already in
	f.event(crossEv(evEnterNotify, notifyInferior, w.id, 1, 2, 0, notifyNormal)) // from a child
	f.event(crossEv(evLeaveNotify, 0, w.id, 1, 2, 0, notifyUngrab))              // a grab's end
	f.event(crossEv(evEnterNotify, 0, 0x999, 1, 2, 0, notifyNormal))
	quiet(t, f, w)
	// The window manager taking the pointer counts as leaving.
	f.event(crossEv(evLeaveNotify, 0, w.id, 1, 2, 0, notifyGrab))
	if k := kind(); k != platform.Leave {
		t.Errorf("grab leave = %v", k)
	}
	f.event(crossEv(evLeaveNotify, 0, w.id, 1, 2, 0, notifyNormal)) // already out
	f.event(crossEv(evEnterNotify, 0, w.id, 1, 2, 0, notifyGrab))   // a grab's start
	quiet(t, f, w)
	f.event(crossEv(evEnterNotify, 0, w.id, 1, 2, 0, notifyUngrab))
	if k := kind(); k != platform.Enter {
		t.Errorf("ungrab enter = %v", k)
	}
	// Leaving with a button held waits for the button.
	const button1 = 0x100
	f.event(crossEv(evLeaveNotify, 0, w.id, -5, 2, button1, notifyNormal))
	f.event(inputEv(evButtonRelease, 3, 30, w.id, 0, 0, -5, 2, button1|0x400)) // another button stays down
	if p := want[platform.PointerEvent](t, w); p.Kind != platform.Release {
		t.Errorf("release = %+v", p)
	}
	quiet(t, f, w)
	f.event(inputEv(evButtonRelease, 1, 31, w.id, 0, 0, -5, 2, button1))
	want[platform.PointerEvent](t, w) // the release
	if k := kind(); k != platform.Leave {
		t.Errorf("deferred leave = %v", k)
	}
	// Coming back while held cancels the leave.
	f.event(crossEv(evEnterNotify, 0, w.id, 1, 2, 0, notifyNormal))
	kind()
	f.event(crossEv(evLeaveNotify, 0, w.id, -5, 2, button1, notifyNormal))
	f.event(crossEv(evEnterNotify, 0, w.id, 1, 2, button1, notifyNormal))
	f.event(inputEv(evButtonRelease, 1, 32, w.id, 0, 0, 1, 2, button1))
	want[platform.PointerEvent](t, w)
	quiet(t, f, w)

	focus := func(code, detail, mode byte) { f.event(focusEv(code, detail, w.id, mode)) }
	focus(evFocusIn, 0, notifyNormal)
	if e := want[platform.FocusEvent](t, w); !e.Focused {
		t.Errorf("focus = %+v", e)
	}
	focus(evFocusIn, 0, notifyNormal) // already focused
	focus(evFocusOut, 0, notifyGrab)  // the manager's keyboard grab
	focus(evFocusOut, notifyPointer, notifyNormal)
	f.event(focusEv(evFocusIn, 0, 0x999, notifyNormal))
	quiet(t, f, w)
	focus(evFocusOut, 0, notifyNormal)
	if e := want[platform.FocusEvent](t, w); e.Focused {
		t.Errorf("unfocus = %+v", e)
	}
	focus(evFocusOut, 0, notifyNormal)
	quiet(t, f, w)
}
