// Package win32 is the platform backend for Windows, through the Win32 API
// called directly from Go: windows that show a bitmap and deliver input, the
// clipboard, drags between the program's windows and files dropped from
// Explorer. It also holds what the program needs of the Windows desktop
// beyond its windows: the common file dialogs, the system's light or dark
// preference and the opening of links.
//
// The files without a build constraint hold the parts that need no Windows
// API, so that they are tested on every system.
package win32

import (
	"strings"
	"unicode"
	"unicode/utf16"

	"viewrab/internal/ui/platform"
)

// Virtual-key codes of the keys the viewer reacts to.
const (
	vkBack     = 0x08
	vkTab      = 0x09
	vkReturn   = 0x0d
	vkShift    = 0x10
	vkControl  = 0x11
	vkMenu     = 0x12 // Alt
	vkEscape   = 0x1b
	vkPrior    = 0x21
	vkNext     = 0x22
	vkEnd      = 0x23
	vkHome     = 0x24
	vkLeft     = 0x25
	vkUp       = 0x26
	vkRight    = 0x27
	vkDown     = 0x28
	vkDelete   = 0x2e
	vkLWin     = 0x5b
	vkRWin     = 0x5c
	vkSeparate = 0x6c // the keypad's Enter on some keyboards
)

// virtualKeys maps the virtual-key codes of navigation and editing keys to
// platform keys.
var virtualKeys = map[uintptr]platform.Key{
	vkReturn: platform.KeyEnter, vkSeparate: platform.KeyEnter, vkEscape: platform.KeyEscape,
	vkTab: platform.KeyTab, vkBack: platform.KeyBackspace, vkDelete: platform.KeyDelete,
	vkLeft: platform.KeyLeft, vkRight: platform.KeyRight, vkUp: platform.KeyUp, vkDown: platform.KeyDown,
	vkPrior: platform.KeyPageUp, vkNext: platform.KeyPageDown, vkHome: platform.KeyHome, vkEnd: platform.KeyEnd,
}

// modifiers returns the modifiers held, given a test of whether the key with
// a virtual-key code is down.
func modifiers(down func(vk int) bool) platform.Modifiers {
	var m platform.Modifiers
	if down(vkShift) {
		m |= platform.Shift
	}
	if down(vkControl) {
		m |= platform.Ctrl
	}
	if down(vkMenu) {
		m |= platform.Alt
	}
	if down(vkLWin) || down(vkRWin) {
		m |= platform.Super
	}
	return m
}

// altGr is how Windows reports AltGr: as Ctrl and Alt held together.
const altGr = platform.Ctrl | platform.Alt

// keyDown turns a key press, its virtual-key code and the character
// MapVirtualKey gives for it, into the event to report, if any. A navigation
// key is reported at once. A key typed with Ctrl alone is reported as its
// unshifted character in lower case, as X11 reports a shortcut, because
// Windows then types a control character or nothing. Every other character is
// left to keyChar.
func keyDown(vk, char uintptr, mods platform.Modifiers) (platform.KeyEvent, bool) {
	if k, ok := virtualKeys[vk]; ok {
		return platform.KeyEvent{Key: k, Down: true, Mods: mods}, true
	}
	r := rune(char &^ 0x80000000) // the top bit marks a dead key
	if mods&altGr != platform.Ctrl || r < 0x20 {
		return platform.KeyEvent{}, false
	}
	if mods&platform.Shift == 0 {
		r = unicode.ToLower(r)
	}
	return platform.KeyEvent{Rune: r, Down: true, Mods: mods}, true
}

// keyUp turns a key release into the event to report: only navigation keys
// report releases, as keyChar reports no release for a character.
func keyUp(vk uintptr, mods platform.Modifiers) (platform.KeyEvent, bool) {
	k, ok := virtualKeys[vk]
	return platform.KeyEvent{Key: k, Mods: mods}, ok
}

// chars assembles the UTF-16 code units of WM_CHAR messages into runes.
type chars struct{ high uint16 } // a high surrogate awaiting its pair

// keyChar turns the code unit of a WM_CHAR into the event to report, if any.
// Control characters are left out, since their keys report themselves through
// keyDown, and so are characters typed with Ctrl alone. AltGr, which arrives
// as Ctrl and Alt, types text and is reported without either.
func (c *chars) keyChar(unit uint16, mods platform.Modifiers) (platform.KeyEvent, bool) {
	r := rune(unit)
	switch {
	case utf16.IsSurrogate(r) && unit < 0xdc00:
		c.high = unit
		return platform.KeyEvent{}, false
	case utf16.IsSurrogate(r):
		r = utf16.DecodeRune(rune(c.high), r)
		c.high = 0
		if r == unicode.ReplacementChar {
			return platform.KeyEvent{}, false
		}
	}
	if mods&altGr == altGr {
		mods &^= altGr
	}
	if r < 0x20 || r == 0x7f || mods&platform.Ctrl != 0 {
		return platform.KeyEvent{}, false
	}
	return platform.KeyEvent{Rune: r, Down: true, Mods: mods}, true
}

// scrollStep is how far one notch of a wheel scrolls, in the units of a
// ScrollEvent: what Wayland compositors report for a click.
const scrollStep = 10

// wheelDelta is the rotation Windows reports for one notch of a wheel.
const wheelDelta = 120

// wheel converts the rotation of a WM_MOUSEWHEEL, or of a WM_MOUSEHWHEEL when
// horizontal, into the scroll of a ScrollEvent. Turning the wheel towards the
// user reports a negative rotation and scrolls down; tilting it right reports
// a positive one and scrolls right.
func wheel(delta int16, horizontal bool) (dx, dy float64) {
	d := float64(delta) * scrollStep / wheelDelta
	if horizontal {
		return d, 0
	}
	return 0, -d
}

// Hit-test codes, which name the part of a window a point is on.
const (
	htClient      = 1
	htCaption     = 2
	htLeft        = 10
	htRight       = 11
	htTop         = 12
	htTopLeft     = 13
	htTopRight    = 14
	htBottom      = 15
	htBottomLeft  = 16
	htBottomRight = 17
)

// edgeHits maps the edges a resize pulls on to the hit-test code of that
// part of the frame.
var edgeHits = map[platform.Edge]uintptr{
	platform.EdgeTop: htTop, platform.EdgeBottom: htBottom,
	platform.EdgeLeft: htLeft, platform.EdgeRight: htRight,
	platform.EdgeTop | platform.EdgeLeft: htTopLeft, platform.EdgeTop | platform.EdgeRight: htTopRight,
	platform.EdgeBottom | platform.EdgeLeft: htBottomLeft, platform.EdgeBottom | platform.EdgeRight: htBottomRight,
}

// Identifiers of the system's cursors, for LoadCursor.
const (
	idcArrow    = 32512
	idcIBeam    = 32513
	idcSizeNWSE = 32642
	idcSizeNESW = 32643
	idcSizeWE   = 32644
	idcSizeNS   = 32645
	idcHand     = 32649
)

// cursorIDs maps cursors to the system cursors that show them.
var cursorIDs = map[platform.Cursor]uintptr{
	platform.CursorDefault: idcArrow, platform.CursorPointer: idcHand,
	platform.CursorText:    idcIBeam,
	platform.CursorResizeN: idcSizeNS, platform.CursorResizeS: idcSizeNS,
	platform.CursorResizeW: idcSizeWE, platform.CursorResizeE: idcSizeWE,
	platform.CursorResizeNW: idcSizeNWSE, platform.CursorResizeSE: idcSizeNWSE,
	platform.CursorResizeNE: idcSizeNESW, platform.CursorResizeSW: idcSizeNESW,
}

// cursorID returns the system cursor that shows c, the arrow for a cursor
// it does not know.
func cursorID(c platform.Cursor) uintptr {
	if id, ok := cursorIDs[c]; ok {
		return id
	}
	return idcArrow
}

// clipboardText encodes text as the clipboard's CF_UNICODETEXT holds it:
// UTF-16 with Windows line ends, ending in a NUL.
func clipboardText(text string) []uint16 {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\n", "\r\n")
	return append(utf16.Encode([]rune(text)), 0)
}

// pastedText decodes CF_UNICODETEXT, up to its NUL, with Windows line ends
// turned into the newlines the rest of the program uses.
func pastedText(units []uint16) string {
	return strings.ReplaceAll(chosenFirst(units), "\r\n", "\n")
}
