package platform

import (
	"errors"
	"fmt"
	"image"
	"sync"
)

// Fake is an in-memory Platform for tests: it records what is shown and lets
// the test inject events.
type Fake struct {
	mu      sync.Mutex
	windows []*FakeWindow
	// Err, when set, is returned by NewWindow.
	Err error
}

// NewWindow opens a FakeWindow that has already received its first
// ResizeEvent and FrameEvent.
func (f *Fake) NewWindow(opts WindowOptions) (Window, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return nil, f.Err
	}
	w := &FakeWindow{Options: opts, title: opts.Title, events: make(chan Event, 256)}
	w.events <- ResizeEvent{Size: opts.Size, Scale: 1}
	w.events <- FrameEvent{}
	f.windows = append(f.windows, w)
	return w, nil
}

// Windows returns every window opened so far.
func (f *Fake) Windows() []*FakeWindow {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*FakeWindow(nil), f.windows...)
}

// ErrFakeClosed is returned by FakeWindow.Send after Close.
var ErrFakeClosed = errors.New("platform: window closed")

// FakeWindow is the Window that Fake opens.
type FakeWindow struct {
	// Options are the options the window was opened with.
	Options WindowOptions

	mu          sync.Mutex
	events      chan Event
	title       string
	presented   []*image.RGBA
	invalidated int
	activations []string
	clipboard   string
	closed      bool
	calls       []string
	cursor      Cursor
}

// Events implements Window.
func (w *FakeWindow) Events() <-chan Event { return w.events }

// Present implements Window, keeping a copy of img.
func (w *FakeWindow) Present(img *image.RGBA) {
	cp := image.NewRGBA(img.Rect)
	copy(cp.Pix, img.Pix)
	w.mu.Lock()
	w.presented = append(w.presented, cp)
	w.mu.Unlock()
}

// Invalidate implements Window by counting the request and queuing a frame.
func (w *FakeWindow) Invalidate() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.invalidated++
	if !w.closed {
		w.events <- FrameEvent{}
	}
}

// SetTitle implements Window.
func (w *FakeWindow) SetTitle(title string) {
	w.mu.Lock()
	w.title = title
	w.mu.Unlock()
}

// Activate implements Window by recording the token.
func (w *FakeWindow) Activate(token string) {
	w.mu.Lock()
	w.activations = append(w.activations, token)
	w.mu.Unlock()
}

// Activations returns the tokens the window was asked to activate with.
func (w *FakeWindow) Activations() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.activations...)
}

// SetClipboard implements Window by keeping the text.
func (w *FakeWindow) SetClipboard(text string) {
	w.mu.Lock()
	w.clipboard = text
	w.mu.Unlock()
}

// Clipboard returns the text last given to SetClipboard.
func (w *FakeWindow) Clipboard() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.clipboard
}

// RequestPaste implements Window by queuing the clipboard's text, if any, as a
// PasteEvent.
func (w *FakeWindow) RequestPaste() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.clipboard != "" && !w.closed {
		w.events <- PasteEvent{Text: w.clipboard}
	}
}

// Move implements Window by recording the call.
func (w *FakeWindow) Move() { w.record("move") }

// Resize implements Window by recording the call with its edges.
func (w *FakeWindow) Resize(edges Edge) { w.record(fmt.Sprintf("resize %d", edges)) }

// SetMaximized implements Window by recording the call.
func (w *FakeWindow) SetMaximized(maximized bool) {
	w.record(map[bool]string{true: "maximize", false: "unmaximize"}[maximized])
}

// Minimize implements Window by recording the call.
func (w *FakeWindow) Minimize() { w.record("minimize") }

// ShowMenu implements Window by recording the call with its position.
func (w *FakeWindow) ShowMenu(x, y int) { w.record(fmt.Sprintf("menu %d %d", x, y)) }

// SetCursor implements Window by keeping the cursor.
func (w *FakeWindow) SetCursor(c Cursor) {
	w.mu.Lock()
	w.cursor = c
	w.mu.Unlock()
}

// Cursor returns the cursor last given to SetCursor.
func (w *FakeWindow) Cursor() Cursor {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cursor
}

func (w *FakeWindow) record(call string) {
	w.mu.Lock()
	w.calls = append(w.calls, call)
	w.mu.Unlock()
}

// Calls returns the window management calls made so far, oldest first, such
// as "move", "resize 9", "maximize", "unmaximize", "minimize" and "menu 4 5".
func (w *FakeWindow) Calls() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.calls...)
}

// Close implements Window; it is idempotent.
func (w *FakeWindow) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		w.closed = true
		close(w.events)
	}
}

// Send queues ev for the application, or returns ErrFakeClosed.
func (w *FakeWindow) Send(ev Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrFakeClosed
	}
	w.events <- ev
	return nil
}

// Title returns the last title set.
func (w *FakeWindow) Title() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.title
}

// Presented returns every image shown so far, oldest first.
func (w *FakeWindow) Presented() []*image.RGBA {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]*image.RGBA(nil), w.presented...)
}

// Invalidations returns how many times Invalidate was called.
func (w *FakeWindow) Invalidations() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.invalidated
}

// Closed reports whether Close was called.
func (w *FakeWindow) Closed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}
