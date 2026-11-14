// Package platform is the seam between the viewer and the operating system's
// windowing: a Window shows a bitmap and delivers input events. Fake is an
// in-memory implementation for tests.
package platform

import "image"

// Size is a width and height in device pixels.
type Size struct{ W, H int }

// Modifiers is a bit set of held modifier keys.
type Modifiers uint8

// Modifier keys.
const (
	Shift Modifiers = 1 << iota
	Ctrl
	Alt
	Super
)

// Button is a pointer button.
type Button uint8

// Pointer buttons. None marks a pointer move with no button involved.
const (
	None Button = iota
	Left
	Middle
	Right
)

// PointerKind tells what a PointerEvent reports.
type PointerKind uint8

// Pointer event kinds.
const (
	Move PointerKind = iota
	Press
	Release
	Enter
	Leave
)

// Key is a non-text key; printable input arrives as KeyEvent.Rune.
type Key uint16

// Keys the viewer reacts to.
const (
	KeyNone Key = iota
	KeyEnter
	KeyEscape
	KeyTab
	KeyBackspace
	KeyDelete
	KeyLeft
	KeyRight
	KeyUp
	KeyDown
	KeyPageUp
	KeyPageDown
	KeyHome
	KeyEnd
)

// Event is one input or lifecycle notification from a window.
type Event interface{ isEvent() }

// ResizeEvent reports the drawable area and the device pixel ratio. A window
// always gets one before its first FrameEvent.
type ResizeEvent struct {
	Size  Size
	Scale float64
}

// FrameEvent asks the application to redraw and Present.
type FrameEvent struct{}

// CloseEvent reports that the user asked to close the window.
type CloseEvent struct{}

// FocusEvent reports keyboard focus gained or lost.
type FocusEvent struct{ Focused bool }

// PointerEvent reports pointer motion or a button change at (X, Y) in device
// pixels from the window's top-left corner.
type PointerEvent struct {
	Kind   PointerKind
	Button Button
	X, Y   float64
	Mods   Modifiers
}

// ScrollEvent reports wheel or touchpad scrolling at (X, Y), where the pointer
// is, in device pixels from the window's top-left corner. A positive DY moves
// content up.
type ScrollEvent struct {
	DX, DY float64
	X, Y   float64
	Mods   Modifiers
}

// PasteEvent delivers the clipboard's text, in answer to Window.RequestPaste.
type PasteEvent struct{ Text string }

// KeyEvent reports a key press or release. Rune is the text the key produces,
// or zero for a key without text.
type KeyEvent struct {
	Key  Key
	Rune rune
	Down bool
	Mods Modifiers
}

func (ResizeEvent) isEvent()  {}
func (FrameEvent) isEvent()   {}
func (CloseEvent) isEvent()   {}
func (FocusEvent) isEvent()   {}
func (PointerEvent) isEvent() {}
func (ScrollEvent) isEvent()  {}
func (KeyEvent) isEvent()     {}
func (PasteEvent) isEvent()   {}

// WindowOptions describes a window to open.
type WindowOptions struct {
	Title   string
	Size    Size
	MinSize Size
	// AppID names the application to the desktop (Wayland app ID).
	AppID string
}

// Window is a top-level window. Events must be drained by one goroutine;
// Present and the setters may be called from any goroutine.
type Window interface {
	// Events returns the channel that delivers input and lifecycle events.
	// The channel is closed when the window closes.
	Events() <-chan Event
	// Present shows img, which must match the last reported size. The backend
	// copies it, so the caller may reuse the buffer.
	Present(img *image.RGBA)
	// Invalidate requests a FrameEvent.
	Invalidate()
	SetTitle(title string)
	// Activate asks the compositor to bring the window to the front, citing
	// token, the activation token of the user action behind the request. It
	// does nothing without a token or where the compositor offers no way to ask.
	Activate(token string)
	// SetClipboard makes text the clipboard's content, for other programs to
	// paste. It must follow a key press or click, as the compositor requires.
	SetClipboard(text string)
	// RequestPaste asks for the clipboard's text, which arrives later as a
	// PasteEvent, or not at all if the clipboard holds no text.
	RequestPaste()
	// Close destroys the window and closes the Events channel.
	Close()
}

// Platform opens windows.
type Platform interface {
	NewWindow(opts WindowOptions) (Window, error)
}
