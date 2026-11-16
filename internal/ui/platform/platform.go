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

// FrameEvent asks the application to redraw and Present. A window may hold
// back further FrameEvents until Present answers this one, and until the
// compositor is ready for another frame, so the application must answer
// every FrameEvent with Present.
type FrameEvent struct{}

// CloseEvent reports that the user asked to close the window.
type CloseEvent struct{}

// FocusEvent reports keyboard focus gained or lost.
type FocusEvent struct{ Focused bool }

// PointerEvent reports pointer motion or a button change at (X, Y) in device
// pixels from the window's top-left corner. While a button is held, the
// pointer stays in the window that took the press, so motion may report
// points outside it, and it leaves only once every button is up.
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

// StateEvent reports how the compositor shows the window. It arrives before
// the ResizeEvent of the change, and only when the state differs from the last
// one reported, which starts as the zero StateEvent.
type StateEvent struct {
	// Maximized reports that the window fills the screen, maximised or full
	// screen, and so has no edges to resize from.
	Maximized bool
	// Decorated reports that the compositor draws the window's title bar and
	// frame, so the application should not draw its own window controls.
	Decorated bool
}

// DragKind tells what a DragEvent reports.
type DragKind uint8

// Drag event kinds.
const (
	DragEnter DragKind = iota
	DragMove
	DragLeave
)

// DragEvent reports a drag over the window at (X, Y), in device pixels from
// its top-left corner. The drag is either files from another program or, with
// Own set, the program's own drag begun by StartDrag. A drop ends it with a
// DropEvent instead of a DragLeave.
type DragEvent struct {
	Kind DragKind
	X, Y float64
	Own  bool
}

// DropEvent reports a drop on the window at (X, Y): the program's own drag,
// with Own set, or else the local files in Paths.
type DropEvent struct {
	X, Y  float64
	Own   bool
	Paths []string
}

// DragEndEvent reports, to the window that began it, the end of a drag begun
// with StartDrag. Dropped reports that a window of the program took the drag;
// otherwise the drag was cancelled. Outside reports that a cancelled drag
// ended away from every window of the program.
type DragEndEvent struct {
	Dropped bool
	Outside bool
}

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
func (StateEvent) isEvent()   {}
func (DragEvent) isEvent()    {}
func (DropEvent) isEvent()    {}
func (DragEndEvent) isEvent() {}

// Edge is a set of window edges, the side or corner a resize pulls on. The
// values are those of xdg_toplevel's resize_edge.
type Edge uint8

// Window edges; a corner is the union of its two sides.
const (
	EdgeTop    Edge = 1
	EdgeBottom Edge = 2
	EdgeLeft   Edge = 4
	EdgeRight  Edge = 8
)

// Cursor is a pointer image the compositor draws.
type Cursor uint8

// Cursors. The resize cursors point the way an edge or corner moves.
const (
	CursorDefault Cursor = iota
	CursorResizeN
	CursorResizeS
	CursorResizeW
	CursorResizeE
	CursorResizeNW
	CursorResizeNE
	CursorResizeSW
	CursorResizeSE
	CursorPointer // the hand over a link
)

// ResizeCursor returns the cursor for resizing from edges, or CursorDefault
// for a set of edges that is no side or corner.
func ResizeCursor(edges Edge) Cursor {
	switch edges {
	case EdgeTop:
		return CursorResizeN
	case EdgeBottom:
		return CursorResizeS
	case EdgeLeft:
		return CursorResizeW
	case EdgeRight:
		return CursorResizeE
	case EdgeTop | EdgeLeft:
		return CursorResizeNW
	case EdgeTop | EdgeRight:
		return CursorResizeNE
	case EdgeBottom | EdgeLeft:
		return CursorResizeSW
	case EdgeBottom | EdgeRight:
		return CursorResizeSE
	}
	return CursorDefault
}

// cssNames are the CSS names of the cursors.
var cssNames = [...]string{
	CursorDefault: "default", CursorPointer: "pointer",
	CursorResizeN: "n-resize", CursorResizeS: "s-resize", CursorResizeW: "w-resize", CursorResizeE: "e-resize",
	CursorResizeNW: "nw-resize", CursorResizeNE: "ne-resize", CursorResizeSW: "sw-resize", CursorResizeSE: "se-resize",
}

// CSSName returns the CSS name of c, which is also the name that cursor themes
// use for it. It returns "default" for a cursor it does not know.
func (c Cursor) CSSName() string {
	if int(c) < len(cssNames) {
		return cssNames[c]
	}
	return "default"
}

// WindowOptions describes a window to open.
type WindowOptions struct {
	Title   string
	Size    Size
	MinSize Size
	// AppID names the application to the desktop: the Wayland app ID, and on
	// X11 the window's class.
	AppID string
	// Icon, if not nil, is the window's icon on desktops that take it from the
	// window, as X11 does, rather than from the application's desktop entry.
	Icon image.Image
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
	// Invalidate requests a FrameEvent. Requests made before the window is
	// ready for a frame may be answered by a single FrameEvent.
	Invalidate()
	SetTitle(title string)
	// Activate asks the compositor to bring the window to the front. token is
	// the activation token of the user action behind the request: a Wayland
	// activation token or an X11 startup notification ID. Where the
	// desktop requires a token, as Wayland does, it does nothing without one;
	// it also does nothing where the compositor offers no way to ask.
	Activate(token string)
	// SetClipboard makes text the clipboard's content, for other programs to
	// paste. It must follow a key press or click, as the compositor requires.
	SetClipboard(text string)
	// RequestPaste asks for the clipboard's text, which arrives later as a
	// PasteEvent, or not at all if the clipboard holds no text.
	RequestPaste()
	// Move lets the user move the window with the pointer, from the button
	// press last delivered until its release.
	Move()
	// Resize lets the user resize the window from edges with the pointer, from
	// the button press last delivered until its release.
	Resize(edges Edge)
	// SetMaximized maximises the window, or restores it from maximised.
	SetMaximized(maximized bool)
	// Minimize hides the window to the taskbar or dock.
	Minimize()
	// ShowMenu opens the compositor's window menu at (x, y), following the
	// button press last delivered.
	ShowMenu(x, y int)
	// StartDrag begins a drag of the program's own, made from the button press
	// last delivered. If icon is not nil, it is shown under the pointer, with
	// hotspot, a point in icon, placed at the pointer. It reports whether the
	// drag began; a drag that did ends with a DragEndEvent.
	StartDrag(icon *image.RGBA, hotspot image.Point) bool
	// SetCursor sets the pointer's image while it is over the window.
	SetCursor(c Cursor)
	// Close destroys the window and closes the Events channel.
	Close()
}

// Platform opens windows.
type Platform interface {
	NewWindow(opts WindowOptions) (Window, error)
}
