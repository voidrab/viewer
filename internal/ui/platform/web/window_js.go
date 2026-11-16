//go:build js && wasm

package web

import (
	"errors"
	"fmt"
	"image"
	"os"
	"sync"
	"syscall/js"
	"time"

	"viewrab/internal/ui/platform"
)

// Backend opens the one window a page has, on the canvas the page handed to
// the worker.
type Backend struct {
	mu     sync.Mutex
	opened bool
}

// New returns the backend of the page the worker runs for.
func New() *Backend { return &Backend{} }

// ErrOneWindow is returned when a second window is asked for: a page shows
// one.
var ErrOneWindow = errors.New("web: a page has one window")

// Close implements the backend's closing; the page outlives it.
func (b *Backend) Close() error { return nil }

// NewWindow implements platform.Platform, opening the page's window the first
// time it is called.
func (b *Backend) NewWindow(opts platform.WindowOptions) (platform.Window, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.opened {
		return nil, ErrOneWindow
	}
	b.opened = true
	return newWindow(opts), nil
}

// window is the page's window: the worker draws on its canvas, an
// OffscreenCanvas, and the page's thread forwards the canvas's events.
type window struct {
	canvas, ctx js.Value
	q           *queue
	frames      frames
	frameFunc   js.Func
	eventFunc   js.Func

	mu     sync.Mutex
	data   js.Value // the ImageData presented through, of the last size
	paste  pasteState
	closed bool
}

// pasteState matches requests for the clipboard's text with the paste events
// the page forwards: Ctrl+V reaches the program as a key press, and the paste
// event follows it, before or after the program asks.
type pasteState struct {
	text  string
	at    time.Time // when text arrived; zero for none
	asked time.Time // when the program asked with no text at hand; zero for no request
}

// pasteWindow is how far apart a paste event and a request may be and still
// belong together.
const pasteWindow = time.Second

func newWindow(opts platform.WindowOptions) *window {
	canvas := host().Get("canvas")
	w := &window{
		canvas: canvas,
		ctx:    canvas.Call("getContext", "2d", map[string]any{"alpha": false}),
		q:      newQueue(),
	}
	w.SetTitle(opts.Title)
	w.frameFunc = js.FuncOf(func(js.Value, []js.Value) any {
		w.frames.fire()
		w.q.post(platform.FrameEvent{})
		return nil
	})
	// The page has no window frame to resize or controls to draw: the
	// browser's are the window's.
	w.q.post(platform.StateEvent{Maximized: true, Decorated: true})
	w.eventFunc = js.FuncOf(func(_ js.Value, args []js.Value) any {
		w.handle(args[0])
		return nil
	})
	// Setting the handler delivers the messages that arrived before it,
	// the first resize among them.
	host().Call("listen", w.eventFunc)
	return w
}

// decode reads the plain object the page posted: its type, and the fields
// that type uses.
func decode(v js.Value) Message {
	str := func(name string) string {
		if f := v.Get(name); f.Type() == js.TypeString {
			return f.String()
		}
		return ""
	}
	num := func(name string) float64 {
		if f := v.Get(name); f.Type() == js.TypeNumber {
			return f.Float()
		}
		return 0
	}
	flag := func(name string) bool { return v.Get(name).Truthy() }
	m := Message{Type: str("type")}
	mods := func() {
		m.Shift, m.Ctrl, m.Alt = flag("shift"), flag("ctrl"), flag("alt")
		m.Meta, m.AltGraph = flag("meta"), flag("altGraph")
	}
	switch m.Type {
	case "pointerdown", "pointerup", "pointermove", "pointerenter", "pointerleave":
		m.X, m.Y, m.Button = num("x"), num("y"), int(num("button"))
		mods()
	case "wheel":
		m.X, m.Y, m.DX, m.DY = num("x"), num("y"), num("dx"), num("dy")
		m.Mode, m.H = int(num("mode")), int(num("h"))
		mods()
	case "keydown", "keyup":
		m.Key, m.Code = str("key"), str("code")
		mods()
	case "dragenter", "dragover", "dragleave", "drop":
		m.X, m.Y = num("x"), num("y")
	case "resize":
		m.W, m.H = int(num("w")), int(num("h"))
	case "paste", "clipboard":
		m.Text = str("text")
	}
	return m
}

// handle takes one message from the page.
func (w *window) handle(v js.Value) {
	m := decode(v)
	switch m.Type {
	case "drop":
		files := v.Get("files")
		go func() {
			paths, err := ImportFiles(files)
			if err != nil {
				fmt.Fprintln(os.Stderr, "viewrab: dropping files:", err)
			}
			w.q.post(platform.DropEvent{X: m.X, Y: m.Y, Paths: paths})
		}()
	case "paste":
		w.pasted(m.Text)
	case "clipboard": // the answer to readClipboard
		w.q.post(platform.PasteEvent{Text: m.Text})
	default:
		ev, ok := m.Event()
		if !ok {
			return
		}
		w.q.post(ev)
		if _, resized := ev.(platform.ResizeEvent); resized {
			w.Invalidate()
		}
	}
}

// pasted takes the text of a paste event, delivering it if the program asked
// for it, or keeping it for a request about to come.
func (w *window) pasted(text string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.paste.asked.IsZero() && time.Since(w.paste.asked) < pasteWindow {
		w.paste = pasteState{}
		w.q.post(platform.PasteEvent{Text: text})
		return
	}
	w.paste = pasteState{text: text, at: time.Now()}
}

// Events implements platform.Window.
func (w *window) Events() <-chan platform.Event { return w.q.out }

// Present implements platform.Window, copying img to the canvas, which takes
// its size.
func (w *window) Present(img *image.RGBA) {
	b := img.Bounds()
	w.mu.Lock()
	if w.data.IsUndefined() || w.data.Get("width").Int() != b.Dx() || w.data.Get("height").Int() != b.Dy() {
		w.canvas.Set("width", b.Dx())
		w.canvas.Set("height", b.Dy())
		w.data = w.ctx.Call("createImageData", b.Dx(), b.Dy())
	}
	data := w.data
	w.mu.Unlock()
	pix := img.Pix
	if img.Stride != 4*b.Dx() {
		pix = make([]byte, 4*b.Dx()*b.Dy())
		for y := range b.Dy() {
			copy(pix[y*4*b.Dx():], img.Pix[y*img.Stride:y*img.Stride+4*b.Dx()])
		}
	}
	js.CopyBytesToJS(data.Get("data"), pix[:4*b.Dx()*b.Dy()])
	w.ctx.Call("putImageData", data, 0, 0)
	if w.frames.presented() {
		host().Call("requestFrame", w.frameFunc)
	}
}

// Invalidate implements platform.Window, asking for an animation frame.
func (w *window) Invalidate() {
	if w.frames.invalidate() {
		host().Call("requestFrame", w.frameFunc)
	}
}

// SetTitle implements platform.Window as the page's title.
func (w *window) SetTitle(title string) { toPage("setTitle", title) }

// Activate implements platform.Window: a page cannot bring itself forward.
func (w *window) Activate(string) {}

// SetClipboard implements platform.Window through the page's asynchronous
// clipboard, which it may write after a key press or click.
func (w *window) SetClipboard(text string) { toPage("writeClipboard", text) }

// RequestPaste implements platform.Window with the text of the paste event
// that came with Ctrl+V, or, when none comes, the page's asynchronous
// clipboard, which the browser may ask the user about.
func (w *window) RequestPaste() {
	w.mu.Lock()
	if !w.paste.at.IsZero() && time.Since(w.paste.at) < pasteWindow {
		text := w.paste.text
		w.paste = pasteState{}
		w.mu.Unlock()
		w.q.post(platform.PasteEvent{Text: text})
		return
	}
	w.paste = pasteState{asked: time.Now()}
	w.mu.Unlock()
	go func() {
		time.Sleep(pasteWindow / 4)
		w.mu.Lock()
		still := !w.paste.asked.IsZero()
		w.paste = pasteState{}
		w.mu.Unlock()
		if still {
			toPage("readClipboard", nil)
		}
	}()
}

// Move, Resize, SetMaximized, Minimize and ShowMenu implement platform.Window:
// the browser's window is moved and sized by the browser.
func (w *window) Move()                {}
func (w *window) Resize(platform.Edge) {}
func (w *window) SetMaximized(bool)    {}
func (w *window) Minimize()            {}
func (w *window) ShowMenu(int, int)    {}

// StartDrag implements platform.Window: with one window there is nowhere to
// drag a tab to, so tabs are only reordered along the tab bar.
func (w *window) StartDrag(*image.RGBA, image.Point) bool { return false }

// SetCursor implements platform.Window.
func (w *window) SetCursor(c platform.Cursor) { toPage("setCursor", c.CSSName()) }

// Close implements platform.Window, no longer taking the page's events.
func (w *window) Close() {
	w.mu.Lock()
	closed := w.closed
	w.closed = true
	w.mu.Unlock()
	if closed {
		return
	}
	host().Call("listen", js.Null())
	w.eventFunc.Release()
	w.q.close()
}
