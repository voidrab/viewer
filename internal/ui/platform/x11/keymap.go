package x11

import (
	"sync"
	"unicode"

	"viewer/internal/ui/platform"
)

// Keysyms that the modifier mapping is searched for.
const (
	ksNumLock    = 0xff7f
	ksModeSwitch = 0xff7e
	ksLevel3     = 0xfe03
	ksAltL       = 0xffe9
	ksAltR       = 0xffea
	ksMetaL      = 0xffe7
	ksSuperL     = 0xffeb
	ksSuperR     = 0xffec
)

// Core modifier state bits.
const (
	stateShift   = 1 << 0
	stateLock    = 1 << 1
	stateControl = 1 << 2
)

// keymap is the server's core keyboard mapping: the keysyms of each key
// code, and which modifier bits the special modifier keys are bound to.
type keymap struct {
	min                                     byte
	per                                     int      // keysyms per key code
	syms                                    []uint32 // per keysyms for each code from min
	numLock, modeSwitch, level3, alt, super uint16
}

// loadKeymap fetches the keyboard and modifier mappings.
func (in *input) loadKeymap(c *Conn) error {
	min, max := c.Setup.MinKeycode, c.Setup.MaxKeycode
	km := &keymap{min: min}
	rep, err := c.Call(newReq(opGetKeyboardMapping, 0).u8(min).u8(max - min + 1).pad(2))
	if err != nil {
		return err
	}
	km.per = int(rep[1])
	for i := 32; i+4 <= len(rep); i += 4 {
		km.syms = append(km.syms, u32At(rep, i))
	}
	rep, err = c.Call(newReq(opGetModifierMapping, 0))
	if err != nil {
		return err
	}
	per := int(rep[1])
	for mod := range 8 {
		for i := range per {
			off := 32 + mod*per + i
			if off >= len(rep) || rep[off] == 0 {
				continue
			}
			bit := uint16(1) << mod
			for _, s := range km.keysyms(rep[off]) {
				switch s {
				case ksNumLock:
					km.numLock |= bit
				case ksModeSwitch:
					km.modeSwitch |= bit
				case ksLevel3:
					km.level3 |= bit
				case ksAltL, ksAltR, ksMetaL:
					km.alt |= bit
				case ksSuperL, ksSuperR:
					km.super |= bit
				}
			}
		}
	}
	// Without a binding in the mapping, the conventional bits apply.
	if km.alt == 0 {
		km.alt = 1 << 3 // Mod1
	}
	if km.super == 0 {
		km.super = 1 << 6 // Mod4
	}
	in.mu.Lock()
	in.keymap = km
	in.mu.Unlock()
	return nil
}

// keysyms returns the keysyms of code.
func (km *keymap) keysyms(code byte) []uint32 {
	i := (int(code) - int(km.min)) * km.per
	if code < km.min || km.per == 0 || i+km.per > len(km.syms) {
		return nil
	}
	return km.syms[i : i+km.per]
}

// lookup resolves key code under the modifier state to a non-text key or the
// rune it types, following the core protocol's rules extended as XKB lays
// out its levels: columns 0 and 1 are the first group's first two levels, 2
// and 3 the second group's, and 4 and 5, then 6 and 7, their third and
// fourth levels.
func (km *keymap) lookup(code byte, state uint16) (platform.Key, rune) {
	syms := km.keysyms(code)
	col := func(i int) uint32 {
		if i < len(syms) {
			return syms[i]
		}
		return 0
	}
	group := 0
	if state&km.modeSwitch != 0 || state>>13&3 != 0 {
		group = 1
	}
	base := [2][4]int{{0, 1, 4, 5}, {2, 3, 6, 7}}
	if group == 1 && col(2) == 0 && col(3) == 0 {
		group = 0 // a key without a second group uses its first
	}
	level := 0
	shift := state&stateShift != 0
	sym0, sym1 := col(base[group][0]), col(base[group][1])
	if sym1 == 0 {
		// A key with one keysym has its case pair as the second level.
		if r := keysymRune(sym0); unicode.IsLower(r) {
			sym1 = runeKeysym(unicode.ToUpper(r))
		} else {
			sym1 = sym0
		}
	}
	switch {
	case state&km.numLock != 0 && isKeypad(sym1):
		if !shift {
			level = 1
		}
	case shift:
		level = 1
	}
	if state&stateLock != 0 {
		// Caps Lock shifts letters only.
		if r := keysymRune(sym0); unicode.IsLetter(r) && unicode.ToUpper(r) != r {
			level ^= 1
		}
	}
	if state&km.level3 != 0 {
		level += 2
	}
	var sym uint32
	switch level {
	case 0:
		sym = sym0
	case 1:
		sym = sym1
	default:
		if sym = col(base[group][level]); sym == 0 {
			sym = col(base[group][level-2])
		}
	}
	return resolveKeysym(sym)
}

// isKeypad reports whether s is a keypad keysym.
func isKeypad(s uint32) bool { return s >= 0xff80 && s <= 0xffbd }

// input is the pointer and keyboard state shared by all windows.
type input struct {
	mu      sync.Mutex
	keymap  *keymap
	pointer *window // the window the pointer is in, or nil
	focus   *window // the window with keyboard focus, or nil
	time    uint32  // the server time of the latest event that had one
	press   press   // the latest button press
	// heldLeave is the window the pointer left while a button was held, so
	// that it leaves once the button is up.
	heldLeave *window
}

// press is a button press, with where it was on the root and in its window.
type press struct {
	button       byte
	rootX, rootY int
	x, y         int
}

// setTime records the server time of an event.
func (in *input) setTime(t uint32) {
	if t == 0 {
		return
	}
	in.mu.Lock()
	in.time = t
	in.mu.Unlock()
}

// now returns the server time of the latest event.
func (in *input) now() uint32 {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.time
}

// mods converts a core modifier state.
func (km *keymap) mods(state uint16) platform.Modifiers {
	var out platform.Modifiers
	if state&stateShift != 0 {
		out |= platform.Shift
	}
	if state&stateControl != 0 {
		out |= platform.Ctrl
	}
	if state&km.alt != 0 {
		out |= platform.Alt
	}
	if state&km.super != 0 {
		out |= platform.Super
	}
	return out
}
