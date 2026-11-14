package wayland

import (
	"errors"
	"testing"

	"viewer/internal/ui/platform"
)

const sampleKeymap = `xkb_keymap {
xkb_keycodes "evdev+aliases(qwerty)" {
	minimum = 8;
	maximum = 255;
	<ESC> = 9;
	<AE01> = 10;
	<AD01> = 24;
	<AC01> = 38;
	<AC10> = 47;
	<RTRN> = 36;
	<SPCE> = 65;
	<UP> = 111;
	<I999> = 3;
	<AE02> = 11;
	<AB01> = 52;
	alias <LatQ> = <AD01>;
	alias <Bogus> = <NOPE>;
};
xkb_types "complete" {
	type "ONE_LEVEL" { modifiers = none; };
};
xkb_symbols "pc+us+de:2" {
	name[Group1]= "English (US)";
	key <ESC> {	[	Escape	] };
	key <AE01> {	[	1,	exclam	] };
	key <AD01> {
		type= "ALPHABETIC",
		symbols[Group1]= [ q, Q ],
		symbols[Group2]= [ x, X ]
	};
	key <AC01> { [ a, A, adiaeresis, Adiaeresis ] };
	key <AC10> { [ semicolon, colon, U00E9 ] };
	key <RTRN> {	[	Return	] };
	key <SPCE> {	[	space	] };
	key <UP> { [ Up ] };
	key <I999> { [ q ] };
	key <ZZZZ> { [ q ] };
	key <AE02> { [ NoSymbol ] };
	key <AB01> {
		symbols[1]= [ z, Z ],
		symbols[2]= [ y, Y, at, Greek_OMEGA ]
	};
};
};`

func TestParseKeymapAndLookup(t *testing.T) {
	km, err := ParseKeymap(sampleKeymap)
	if err != nil {
		t.Fatal(err)
	}
	// evdev code = XKB code - 8.
	for _, tc := range []struct {
		name       string
		code, mods uint32
		group      uint32
		wantKey    platform.Key
		wantRune   rune
	}{
		{"q", 16, 0, 0, platform.KeyNone, 'q'},
		{"shift q", 16, modShift, 0, platform.KeyNone, 'Q'},
		{"caps q", 16, modLock, 0, platform.KeyNone, 'Q'},
		{"caps shift q", 16, modLock | modShift, 0, platform.KeyNone, 'q'},
		{"caps 1 is not a letter", 2, modLock, 0, platform.KeyNone, '1'},
		{"shift 1", 2, modShift, 0, platform.KeyNone, '!'},
		{"group 2 q", 16, 0, 1, platform.KeyNone, 'x'},
		{"missing group falls back", 16, 0, 5, platform.KeyNone, 'q'},
		{"altgr a", 30, modMod5, 0, platform.KeyNone, 'ä'},
		{"altgr shift a", 30, modMod5 | modShift, 0, platform.KeyNone, 'Ä'},
		{"altgr on a 2-level key", 2, modMod5, 0, platform.KeyNone, '1'},
		{"altgr shift on a 3-level key falls back to shift", 39, modMod5 | modShift, 0, platform.KeyNone, ':'},
		{"shift on a one-level key", 1, modShift, 0, platform.KeyEscape, 0},
		{"unicode name", 39, modMod5, 0, platform.KeyNone, 'é'},
		{"semicolon", 39, 0, 0, platform.KeyNone, ';'},
		{"enter", 28, 0, 0, platform.KeyEnter, 0},
		{"escape", 1, 0, 0, platform.KeyEscape, 0},
		{"space", 57, 0, 0, platform.KeyNone, ' '},
		{"up", 103, 0, 0, platform.KeyUp, 0},
		{"alias resolved", 16, 0, 0, platform.KeyNone, 'q'},
		{"unknown key", 200, 0, 0, platform.KeyNone, 0},
		{"NoSymbol", 3, 0, 0, platform.KeyNone, 0},
		{"numeric group 1", 44, 0, 0, platform.KeyNone, 'z'},
		{"numeric group 2", 44, modMod5, 1, platform.KeyNone, '@'},
	} {
		key, r := km.Lookup(tc.code, tc.mods, tc.group)
		if key != tc.wantKey || r != tc.wantRune {
			t.Errorf("%s: Lookup = %v, %q; want %v, %q", tc.name, key, r, tc.wantKey, tc.wantRune)
		}
	}
}

func TestParseKeymapEmpty(t *testing.T) {
	for _, text := range []string{"", "xkb_keymap { xkb_symbols \"x\" { }; };", "xkb_keycodes \"a\" { <A> = 9;"} {
		if _, err := ParseKeymap(text); !errors.Is(err, ErrKeymap) {
			t.Errorf("ParseKeymap(%q) = %v", text, err)
		}
	}
}

func TestResolve(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  platform.Key
		r    rune
	}{
		{"Prior", platform.KeyPageUp, 0}, {"Next", platform.KeyPageDown, 0},
		{"Home", platform.KeyHome, 0}, {"End", platform.KeyEnd, 0},
		{"Tab", platform.KeyTab, 0}, {"BackSpace", platform.KeyBackspace, 0},
		{"Delete", platform.KeyDelete, 0}, {"Left", platform.KeyLeft, 0},
		{"Right", platform.KeyRight, 0}, {"Down", platform.KeyDown, 0},
		{"KP_5", platform.KeyNone, '5'}, {"comma", platform.KeyNone, ','},
		{"7", platform.KeyNone, '7'}, {"ü", platform.KeyNone, 'ü'},
		{"U20AC", platform.KeyNone, '€'}, {"U0", platform.KeyNone, 0},
		{"UZZZZ", platform.KeyNone, 0}, {"U110000", platform.KeyNone, 0},
		{"ssharp", platform.KeyNone, 'ß'}, {"Shift_L", platform.KeyNone, 0},
	} {
		if k, r := resolve(tc.name); k != tc.key || r != tc.r {
			t.Errorf("resolve(%q) = %v, %q", tc.name, k, r)
		}
	}
}

func TestModMask(t *testing.T) {
	got := modMask(modShift | modCtrl | modAlt | modSuper | modLock | modMod5)
	if got != platform.Shift|platform.Ctrl|platform.Alt|platform.Super {
		t.Errorf("modMask = %b", got)
	}
	if modMask(0) != 0 {
		t.Error("modMask(0) != 0")
	}
}
