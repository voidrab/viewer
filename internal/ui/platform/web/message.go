package web

import "viewrab/internal/ui/platform"

// Message is an event the page's thread forwards to the worker the program
// runs in, decoded from the plain object web/viewrab.js posts. The page keeps
// the DOM and the worker keeps the program, so every DOM event the window
// takes arrives as one of these, with only the fields its type uses set.
type Message struct {
	// Type is the DOM event's type, such as "pointerdown" or "keydown", or
	// "resize" for a change of the window's size and "paste" for the text of
	// a paste event.
	Type string
	// X and Y are where a mouse, pointer, wheel or drag event happened, in
	// pixels from the canvas's top-left corner.
	X, Y float64
	// Button is a pointer event's DOM button.
	Button int
	// The modifiers held, as the DOM reports them.
	Shift, Ctrl, Alt, Meta, AltGraph bool
	// Key and Code are a keyboard event's key and code.
	Key, Code string
	// DX, DY and Mode are a wheel event's deltas and their DOM delta mode;
	// DX and DY are also a "pan"'s distances, in pixels.
	DX, DY float64
	Mode   int
	// W and H are the window's size, for "resize", and the page's height
	// for "wheel", which deltas in pages are measured in.
	W, H int
	// Scale is the device pixels per CSS pixel, for "resize": the page's
	// devicePixelRatio. A value that is not positive means 1.
	Scale float64
	// Text is the text of a "paste".
	Text string
	// Factor is the zoom of a "pinch" since the one before, at (X, Y).
	Factor float64
}

// mods returns the modifiers m reports held.
func (m Message) mods() platform.Modifiers {
	return Modifiers(m.Shift, m.Ctrl, m.Alt, m.Meta, m.AltGraph)
}

// Event returns the event m reports to the window, and whether it reports
// one: none for a message of a type it does not know, nor for a drop or a
// paste, which the window handles itself.
func (m Message) Event() (platform.Event, bool) {
	switch m.Type {
	case "pointerdown", "pointerup":
		b, ok := Button(m.Button)
		kind := platform.Press
		if m.Type == "pointerup" {
			kind = platform.Release
		}
		return platform.PointerEvent{Kind: kind, Button: b, X: m.X, Y: m.Y, Mods: m.mods()}, ok
	case "pointermove":
		return platform.PointerEvent{Kind: platform.Move, X: m.X, Y: m.Y, Mods: m.mods()}, true
	case "pointerenter":
		return platform.PointerEvent{Kind: platform.Enter, X: m.X, Y: m.Y, Mods: m.mods()}, true
	case "pointerleave":
		return platform.PointerEvent{Kind: platform.Leave, X: m.X, Y: m.Y, Mods: m.mods()}, true
	case "pan":
		return platform.PanEvent{DX: m.DX, DY: m.DY, X: m.X, Y: m.Y}, true
	case "pinch":
		return platform.ZoomEvent{Factor: m.Factor, X: m.X, Y: m.Y}, m.Factor > 0
	case "wheel":
		dx, dy := Wheel(m.DX, m.DY, m.Mode, float64(m.H))
		return platform.ScrollEvent{DX: dx, DY: dy, X: m.X, Y: m.Y, Mods: m.mods()}, true
	case "keydown", "keyup":
		ev, ok := Key(m.Key, m.Code, m.Type == "keydown", m.mods())
		return ev, ok
	case "focus", "blur":
		return platform.FocusEvent{Focused: m.Type == "focus"}, true
	case "dragenter":
		return platform.DragEvent{Kind: platform.DragEnter, X: m.X, Y: m.Y}, true
	case "dragover":
		return platform.DragEvent{Kind: platform.DragMove, X: m.X, Y: m.Y}, true
	case "dragleave":
		return platform.DragEvent{Kind: platform.DragLeave, X: m.X, Y: m.Y}, true
	case "resize":
		if m.W <= 0 || m.H <= 0 {
			return nil, false
		}
		scale := m.Scale
		if !(scale > 0) {
			scale = 1
		}
		return platform.ResizeEvent{Size: platform.Size{W: m.W, H: m.H}, Scale: scale}, true
	}
	return nil, false
}
