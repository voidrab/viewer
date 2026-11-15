//go:build !windows

package wayland

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"viewrab/internal/ui/platform"
	"viewrab/internal/ui/platform/desktop"
)

// The bits of the XKB modifier mask have fixed positions.
const (
	modShift = 1 << 0
	modLock  = 1 << 1
	modCtrl  = 1 << 2
	modAlt   = 1 << 3
	modSuper = 1 << 6
	modMod5  = 1 << 7 // AltGr / ISO_Level3_Shift on common layouts
)

// ErrKeymap is returned for a keymap without usable key definitions.
var ErrKeymap = errors.New("wayland: keymap defines no keys")

// Keymap maps evdev key codes to keysyms for one layout. It reads the
// flattened text keymap a compositor sends, which spells out every key, so no
// include resolution is needed.
type Keymap struct {
	// groups[g][code] lists the keysym names of key code at each shift level
	// in layout group g.
	groups []map[uint32][]string
}

var (
	reKeycode = regexp.MustCompile(`<([^>]+)>\s*=\s*(\d+)\s*;`)
	reKeyDef  = regexp.MustCompile(`(?s)\bkey\s+<([^>]+)>\s*\{(.*?)\}\s*;`)
	reSymbols = regexp.MustCompile(`(?s)(?:symbols\[\s*(?:[Gg]roup)?(\d)\s*\]\s*=\s*)?\[([^\]]*)\]`)
	reAlias   = regexp.MustCompile(`alias\s+<([^>]+)>\s*=\s*<([^>]+)>\s*;`)
)

// ParseKeymap reads a text-v1 XKB keymap. Key codes in the keymap are XKB
// codes, which are the evdev codes plus eight; Lookup takes evdev codes.
func ParseKeymap(text string) (*Keymap, error) {
	section := func(name string) string {
		i := strings.Index(text, name)
		if i < 0 {
			return ""
		}
		// The section ends at the brace that closes its first '{'.
		depth := 0
		for j := i; j < len(text); j++ {
			switch text[j] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return text[i : j+1]
				}
			}
		}
		return text[i:]
	}
	codes := map[string]uint32{}
	keycodes := section("xkb_keycodes")
	for _, m := range reKeycode.FindAllStringSubmatch(keycodes, -1) {
		n, _ := strconv.ParseUint(m[2], 10, 32)
		codes[m[1]] = uint32(n)
	}
	for _, m := range reAlias.FindAllStringSubmatch(keycodes, -1) {
		if n, ok := codes[m[2]]; ok {
			codes[m[1]] = n
		}
	}

	km := &Keymap{}
	for _, def := range reKeyDef.FindAllStringSubmatch(section("xkb_symbols"), -1) {
		code, ok := codes[def[1]]
		if !ok || code < 8 {
			continue
		}
		next := 0
		for _, sm := range reSymbols.FindAllStringSubmatch(def[2], -1) {
			group := next
			if sm[1] != "" {
				group = int(sm[1][0] - '1')
			}
			next = group + 1
			for len(km.groups) <= group {
				km.groups = append(km.groups, map[uint32][]string{})
			}
			var syms []string
			for _, s := range strings.Split(sm[2], ",") {
				syms = append(syms, strings.TrimSpace(s))
			}
			km.groups[group][code-8] = syms
		}
	}
	if len(km.groups) == 0 {
		return nil, ErrKeymap
	}
	return km, nil
}

// Lookup resolves evdev key code under the modifier mask and layout group to
// a non-text key or the rune it types. Both are zero for an unknown key.
func (km *Keymap) Lookup(code uint32, mods uint32, group uint32) (platform.Key, rune) {
	g := int(group)
	if g >= len(km.groups) {
		g = 0
	}
	syms := km.groups[g][code]
	if len(syms) == 0 {
		return platform.KeyNone, 0
	}
	level := 0
	if mods&modShift != 0 {
		level |= 1
	}
	if mods&modMod5 != 0 {
		level |= 2
	}
	// Caps Lock shifts letters only.
	if mods&modLock != 0 && level < 2 {
		if _, r := resolve(syms[0]); unicode.IsLetter(r) {
			level ^= 1
		}
	}
	if level >= len(syms) {
		level &= 1 // a key with fewer levels falls back to its shift pair
	}
	if level >= len(syms) {
		level = 0
	}
	return resolve(syms[level])
}

// resolve turns a keysym into a navigation key or a typed rune. The keysym is
// a name such as "Return" or "a", or, as KWin writes its keymaps, a hex value
// such as "0xff0d" or "0x61".
func resolve(name string) (platform.Key, rune) {
	if strings.HasPrefix(name, "0x") {
		v, err := strconv.ParseUint(name[2:], 16, 32)
		if err != nil {
			return platform.KeyNone, 0
		}
		return desktop.ResolveKeysym(uint32(v))
	}
	if k, ok := namedKeys[name]; ok {
		return k, 0
	}
	if r, ok := namedRunes[name]; ok {
		return platform.KeyNone, r
	}
	if rs := []rune(name); len(rs) == 1 {
		return platform.KeyNone, rs[0]
	}
	if len(name) > 1 && name[0] == 'U' {
		if n, err := strconv.ParseUint(name[1:], 16, 32); err == nil && n > 0 && n <= unicode.MaxRune {
			return platform.KeyNone, rune(n)
		}
	}
	if r, ok := latin1[name]; ok {
		return platform.KeyNone, r
	}
	return platform.KeyNone, 0
}

var namedKeys = map[string]platform.Key{
	"Return": platform.KeyEnter, "KP_Enter": platform.KeyEnter, "ISO_Enter": platform.KeyEnter,
	"Escape": platform.KeyEscape, "Tab": platform.KeyTab, "ISO_Left_Tab": platform.KeyTab,
	"BackSpace": platform.KeyBackspace, "Delete": platform.KeyDelete, "KP_Delete": platform.KeyDelete,
	"Left": platform.KeyLeft, "KP_Left": platform.KeyLeft,
	"Right": platform.KeyRight, "KP_Right": platform.KeyRight,
	"Up": platform.KeyUp, "KP_Up": platform.KeyUp,
	"Down": platform.KeyDown, "KP_Down": platform.KeyDown,
	"Prior": platform.KeyPageUp, "Page_Up": platform.KeyPageUp, "KP_Prior": platform.KeyPageUp,
	"Next": platform.KeyPageDown, "Page_Down": platform.KeyPageDown, "KP_Next": platform.KeyPageDown,
	"Home": platform.KeyHome, "KP_Home": platform.KeyHome,
	"End": platform.KeyEnd, "KP_End": platform.KeyEnd,
}

// namedRunes maps keysym names of ASCII characters to the runes they type.
var namedRunes = map[string]rune{
	"space": ' ', "exclam": '!', "quotedbl": '"', "numbersign": '#', "dollar": '$', "percent": '%',
	"ampersand": '&', "apostrophe": '\'', "quoteright": '\'', "parenleft": '(', "parenright": ')',
	"asterisk": '*', "plus": '+', "comma": ',', "minus": '-', "period": '.', "slash": '/',
	"colon": ':', "semicolon": ';', "less": '<', "equal": '=', "greater": '>', "question": '?',
	"at": '@', "bracketleft": '[', "backslash": '\\', "bracketright": ']', "asciicircum": '^',
	"underscore": '_', "grave": '`', "quoteleft": '`', "braceleft": '{', "bar": '|',
	"braceright": '}', "asciitilde": '~',
	"KP_Add": '+', "KP_Subtract": '-', "KP_Multiply": '*', "KP_Divide": '/', "KP_Decimal": '.',
	"KP_0": '0', "KP_1": '1', "KP_2": '2', "KP_3": '3', "KP_4": '4',
	"KP_5": '5', "KP_6": '6', "KP_7": '7', "KP_8": '8', "KP_9": '9',
}

// latin1 maps common keysym names of non-ASCII characters to their runes.
var latin1 = map[string]rune{
	"adiaeresis": 'ä', "Adiaeresis": 'Ä', "odiaeresis": 'ö', "Odiaeresis": 'Ö',
	"udiaeresis": 'ü', "Udiaeresis": 'Ü', "ssharp": 'ß', "eacute": 'é', "Eacute": 'É',
	"egrave": 'è', "Egrave": 'È', "agrave": 'à', "Agrave": 'À', "aacute": 'á', "Aacute": 'Á',
	"ccedilla": 'ç', "Ccedilla": 'Ç', "ntilde": 'ñ', "Ntilde": 'Ñ', "section": '§',
	"degree": '°', "EuroSign": '€', "acute": '´', "diaeresis": '¨', "sterling": '£',
}

// modMask converts XKB modifier state to platform modifiers.
func modMask(m uint32) platform.Modifiers {
	var out platform.Modifiers
	if m&modShift != 0 {
		out |= platform.Shift
	}
	if m&modCtrl != 0 {
		out |= platform.Ctrl
	}
	if m&modAlt != 0 {
		out |= platform.Alt
	}
	if m&modSuper != 0 {
		out |= platform.Super
	}
	return out
}
