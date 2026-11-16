// Package web is the platform backend for browsers, for a build to
// WebAssembly that runs in a web worker: one window, drawn on the page's
// canvas through an OffscreenCanvas. Its input is the canvas's DOM events,
// which web/viewrab.js forwards from the page's thread. Files live in the
// in-memory file system of web/memfs.js, in the worker.
//
// The files without a build constraint hold the parts that need no browser,
// so that they are tested on every system.
package web

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"viewrab/internal/ui/platform"
)

// namedKeys maps the DOM's names of navigation and editing keys, the key
// property of a KeyboardEvent, to platform keys.
var namedKeys = map[string]platform.Key{
	"Enter": platform.KeyEnter, "Escape": platform.KeyEscape, "Tab": platform.KeyTab,
	"Backspace": platform.KeyBackspace, "Delete": platform.KeyDelete,
	"ArrowLeft": platform.KeyLeft, "ArrowRight": platform.KeyRight,
	"ArrowUp": platform.KeyUp, "ArrowDown": platform.KeyDown,
	"PageUp": platform.KeyPageUp, "PageDown": platform.KeyPageDown,
	"Home": platform.KeyHome, "End": platform.KeyEnd,
}

// Modifiers returns the modifiers a DOM event reports held. AltGr, which
// browsers on Windows report as Ctrl and Alt together, types text, so it
// reports neither.
func Modifiers(shift, ctrl, alt, meta, altGraph bool) platform.Modifiers {
	var m platform.Modifiers
	if shift {
		m |= platform.Shift
	}
	if ctrl && !altGraph {
		m |= platform.Ctrl
	}
	if alt && !altGraph {
		m |= platform.Alt
	}
	if meta {
		m |= platform.Super
	}
	return m
}

// Key turns a KeyboardEvent, its key and code properties, into the event to
// report, if any. A named key is reported pressed and released. A character
// is reported only pressed. With Ctrl, it is reported as its unshifted
// character in lower case, as the desktop backends report a shortcut; a
// layout that types no Latin letter with the key takes the letter or digit
// of its place on the keyboard, given by the code, instead. Characters typed
// with Alt or Super are left to the browser and the desktop.
//
// Whether the browser's own handling of the key is prevented is decided on
// the page's thread, by web/viewrab.js, which cannot wait for the worker.
func Key(key, code string, down bool, mods platform.Modifiers) (platform.KeyEvent, bool) {
	if k, ok := namedKeys[key]; ok {
		return platform.KeyEvent{Key: k, Down: down, Mods: mods}, true
	}
	r, size := utf8.DecodeRuneInString(key)
	if !down || size != len(key) || r == utf8.RuneError || unicode.IsControl(r) ||
		mods&(platform.Alt|platform.Super) != 0 {
		return platform.KeyEvent{}, false
	}
	if mods&platform.Ctrl != 0 {
		if r > unicode.MaxASCII {
			if c, ok := codeRune(code); ok {
				r = c
			}
		}
		if mods&platform.Shift == 0 {
			r = unicode.ToLower(r)
		}
	}
	return platform.KeyEvent{Rune: r, Down: true, Mods: mods}, true
}

// codeRune returns the letter or digit of a key's place on the keyboard,
// given as the code of a KeyboardEvent such as "KeyO" or "Digit1".
func codeRune(code string) (rune, bool) {
	for _, prefix := range []string{"Key", "Digit"} {
		if rest, ok := strings.CutPrefix(code, prefix); ok && len(rest) == 1 {
			return unicode.ToLower(rune(rest[0])), true
		}
	}
	return 0, false
}

// Button returns the pointer button a DOM event's button property names,
// and whether it is one the viewer takes.
func Button(b int) (platform.Button, bool) {
	switch b {
	case 0:
		return platform.Left, true
	case 1:
		return platform.Middle, true
	case 2:
		return platform.Right, true
	}
	return platform.None, false
}

// DOM delta modes, the units of a WheelEvent's deltas.
const (
	deltaPixel = 0
	deltaLine  = 1
	deltaPage  = 2
)

// pixelsPerStep is how many pixels of a browser's wheel delta make one unit
// of a ScrollEvent. A ScrollEvent's unit scrolls the pages by five pixels,
// so a touchpad moves them as far as the fingers move, and the notch of a
// wheel, which browsers report as about 100 pixels, scrolls about as far as
// on the desktop backends.
const pixelsPerStep = 5

// stepsPerLine is how many units of a ScrollEvent one line of a WheelEvent in
// lines makes: Firefox reports three lines a notch, which makes the ten units
// a desktop backend reports.
const stepsPerLine = 10.0 / 3

// Wheel converts a WheelEvent's deltas, in the units of mode, into the scroll
// of a ScrollEvent; page is the height of the window, in pixels, for deltas in
// pages. Both count positive down and right, as a ScrollEvent does.
func Wheel(dx, dy float64, mode int, page float64) (float64, float64) {
	switch mode {
	case deltaLine:
		return dx * stepsPerLine, dy * stepsPerLine
	case deltaPage:
		return dx * page / pixelsPerStep, dy * page / pixelsPerStep
	}
	return dx / pixelsPerStep, dy / pixelsPerStep
}

// cursors names the CSS cursor of each platform cursor.
var cursors = map[platform.Cursor]string{
	platform.CursorResizeN: "n-resize", platform.CursorResizeS: "s-resize",
	platform.CursorResizeW: "w-resize", platform.CursorResizeE: "e-resize",
	platform.CursorResizeNW: "nw-resize", platform.CursorResizeNE: "ne-resize",
	platform.CursorResizeSW: "sw-resize", platform.CursorResizeSE: "se-resize",
	platform.CursorPointer: "pointer",
}

// CursorName returns the CSS cursor that shows c.
func CursorName(c platform.Cursor) string {
	if name, ok := cursors[c]; ok {
		return name
	}
	return "default"
}
