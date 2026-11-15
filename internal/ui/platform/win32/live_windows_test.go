package win32

// Tests of the backend on a live desktop: Windows, or Wine. They open real
// windows, and drive them with posted messages rather than real input, so
// that they neither move the pointer nor type into other programs.

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"viewrab/internal/ui/platform"
)

var (
	procGetPixel      = gdi32.NewProc("GetPixel")
	procGetWindowText = user32.NewProc("GetWindowTextW")
)

// liveBackend starts a backend for the test, closed at its end.
func liveBackend(t *testing.T) *Backend {
	t.Helper()
	b, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

// open opens a window of size at (x, y) on the screen, and waits until it
// has drawn its first frame.
func open(t *testing.T, b *Backend, x, y int, size platform.Size) *window {
	t.Helper()
	pw, err := b.NewWindow(platform.WindowOptions{Title: "live", Size: size, MinSize: platform.Size{W: 100, H: 100},
		Icon: image.NewRGBA(image.Rect(0, 0, 32, 32))})
	if err != nil {
		t.Fatal(err)
	}
	w := pw.(*window)
	b.call(func() {
		procSetWindowPos.Call(w.hwnd, 0, uintptr(x), uintptr(y), 0, 0, swpNoSize|swpNoZOrder)
	})
	waitFor(t, w, func(ev platform.Event) bool { _, ok := ev.(platform.FrameEvent); return ok })
	return w
}

// fill is the colour test frames are filled with.
var fill = color.RGBA{0x20, 0x80, 0xe0, 0xff}

// waitFor reads w's events until match accepts one, answering frames with a
// frame of fill, and returns it.
func waitFor(t *testing.T, w *window, match func(platform.Event) bool) platform.Event {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-w.Events():
			if !ok {
				t.Fatal("the window closed")
			}
			if _, frame := ev.(platform.FrameEvent); frame {
				w.mu.Lock()
				size := w.size
				w.mu.Unlock()
				img := image.NewRGBA(image.Rect(0, 0, size.W, size.H))
				draw.Draw(img, img.Rect, image.NewUniform(fill), image.Point{}, draw.Src)
				w.Present(img)
			}
			if match(ev) {
				return ev
			}
		case <-timeout:
			t.Fatal("timed out waiting for an event")
		}
	}
}

// post posts a message to w and waits until the UI thread has handled it.
func post(w *window, message uint32, wp, lp uintptr) {
	procPostMessage.Call(w.hwnd, uintptr(message), wp, lp)
	w.b.call(func() {}) // queued behind the message
}

func TestLiveWindowShowsItsFrames(t *testing.T) {
	b := liveBackend(t)
	w := open(t, b, 50, 50, platform.Size{W: 320, H: 240})
	w.mu.Lock()
	size := w.size
	w.mu.Unlock()
	if size != (platform.Size{W: 320, H: 240}) {
		t.Errorf("the client area is %v, want the whole window of 320x240", size)
	}
	// The frame shows once it has been blitted.
	deadline := time.Now().Add(2 * time.Second)
	var got uintptr
	for time.Now().Before(deadline) {
		b.call(func() {
			hdc, _, _ := procGetDC.Call(w.hwnd)
			got, _, _ = procGetPixel.Call(hdc, 10, 10)
			procReleaseDC.Call(w.hwnd, hdc)
		})
		if got == uintptr(fill.B)<<16|uintptr(fill.G)<<8|uintptr(fill.R) {
			break
		}
		w.Invalidate()
		waitFor(t, w, func(ev platform.Event) bool { _, ok := ev.(platform.FrameEvent); return ok })
	}
	if want := uintptr(fill.B)<<16 | uintptr(fill.G)<<8 | uintptr(fill.R); got != want {
		t.Errorf("the window shows %#06x, want %#06x", got, want)
	}
	w.SetTitle("Grüße")
	var title string
	b.call(func() {
		buf := make([]uint16, 64)
		n, _, _ := procGetWindowText.Call(w.hwnd, uintptr(unsafe.Pointer(&buf[0])), 64)
		title = string(utf16.Decode(buf[:n]))
	})
	if title != "Grüße" {
		t.Errorf("the title is %q", title)
	}
}

func TestLiveKeys(t *testing.T) {
	b := liveBackend(t)
	w := open(t, b, 50, 50, platform.Size{W: 300, H: 200})
	post(w, wmKeyDown, vkLeft, 1)
	ev := waitFor(t, w, func(ev platform.Event) bool { _, ok := ev.(platform.KeyEvent); return ok })
	if k := ev.(platform.KeyEvent); k.Key != platform.KeyLeft || !k.Down {
		t.Errorf("Left reported %+v", k)
	}
	post(w, wmKeyUp, vkLeft, 0xc0000001)
	ev = waitFor(t, w, func(ev platform.Event) bool { _, ok := ev.(platform.KeyEvent); return ok })
	if k := ev.(platform.KeyEvent); k.Key != platform.KeyLeft || k.Down {
		t.Errorf("the release of Left reported %+v", k)
	}
	post(w, wmChar, 'ß', 1)
	ev = waitFor(t, w, func(ev platform.Event) bool { _, ok := ev.(platform.KeyEvent); return ok })
	if k := ev.(platform.KeyEvent); k.Rune != 'ß' || !k.Down {
		t.Errorf("typing ß reported %+v", k)
	}
}

func TestLivePointerAndWheel(t *testing.T) {
	b := liveBackend(t)
	w := open(t, b, 50, 50, platform.Size{W: 300, H: 200})
	isPointer := func(kind platform.PointerKind) func(platform.Event) bool {
		return func(ev platform.Event) bool { p, ok := ev.(platform.PointerEvent); return ok && p.Kind == kind }
	}
	post(w, wmMouseMove, 0, packPoint(30, 40))
	if p := waitFor(t, w, isPointer(platform.Enter)).(platform.PointerEvent); p.X != 30 || p.Y != 40 {
		t.Errorf("entered at (%v, %v), want (30, 40)", p.X, p.Y)
	}
	waitFor(t, w, isPointer(platform.Move))
	post(w, wmLButtonDown, 1, packPoint(31, 41))
	if p := waitFor(t, w, isPointer(platform.Press)).(platform.PointerEvent); p.Button != platform.Left {
		t.Errorf("pressed %v, want the left button", p.Button)
	}
	post(w, wmLButtonUp, 0, packPoint(31, 41))
	waitFor(t, w, isPointer(platform.Release))
	// The wheel reports in screen coordinates.
	origin := point{}
	b.call(func() { procClientToScreen.Call(w.hwnd, uintptr(unsafe.Pointer(&origin))) })
	turn := int16(-wheelDelta)
	post(w, wmMouseWheel, uintptr(uint16(turn))<<16, packPoint(origin.X+5, origin.Y+6))
	ev := waitFor(t, w, func(ev platform.Event) bool { _, ok := ev.(platform.ScrollEvent); return ok })
	if s := ev.(platform.ScrollEvent); s.DY != scrollStep || s.X != 5 || s.Y != 6 {
		t.Errorf("the wheel reported %+v", s)
	}
}

func TestLiveDropFiles(t *testing.T) {
	b := liveBackend(t)
	w := open(t, b, 50, 50, platform.Size{W: 300, H: 200})
	paths := []string{`C:\a.pdf`, `C:\dir\b ü.pdf`}
	// A DROPFILES: the offset of the names, the point, whether it is in the
	// non-client area, and whether the names are wide; then the names.
	var names []uint16
	for _, p := range paths {
		names = append(append(names, utf16.Encode([]rune(p))...), 0)
	}
	names = append(names, 0)
	h, _, _ := procGlobalAlloc.Call(gmemMoveable, uintptr(20+2*len(names)))
	mem, _, _ := procGlobalLock.Call(h)
	head := unsafe.Slice((*uint32)(ptr(mem)), 5)
	head[0], head[1], head[2], head[3], head[4] = 20, 12, 34, 0, 1
	copy(unsafe.Slice((*uint16)(ptr(mem+20)), len(names)), names)
	procGlobalUnlock.Call(h)
	post(w, wmDropFiles, h, 0)
	ev := waitFor(t, w, func(ev platform.Event) bool { _, ok := ev.(platform.DropEvent); return ok })
	d := ev.(platform.DropEvent)
	if d.Own || d.X != 12 || d.Y != 34 || len(d.Paths) != 2 || d.Paths[0] != paths[0] || d.Paths[1] != paths[1] {
		t.Errorf("the drop reported %+v", d)
	}
}

func TestLiveDragBetweenWindows(t *testing.T) {
	b := liveBackend(t)
	from := open(t, b, 40, 40, platform.Size{W: 300, H: 200})
	to := open(t, b, 400, 40, platform.Size{W: 300, H: 200})
	if from.StartDrag(nil, image.Point{}) {
		t.Fatal("a drag began with no button held")
	}
	post(from, wmLButtonDown, 1, packPoint(10, 10))
	waitFor(t, from, func(ev platform.Event) bool { _, ok := ev.(platform.PointerEvent); return ok })
	icon := image.NewRGBA(image.Rect(0, 0, 40, 20))
	if !from.StartDrag(icon, image.Pt(20, 10)) {
		t.Fatal("the drag did not begin")
	}
	if from.StartDrag(icon, image.Point{}) {
		t.Error("a second drag began while one was under way")
	}
	// The pointer moves to (50, 60) in the client area of to. The message
	// carries that point in from's client coordinates.
	var a, c point
	b.call(func() {
		procClientToScreen.Call(from.hwnd, uintptr(unsafe.Pointer(&a)))
		procClientToScreen.Call(to.hwnd, uintptr(unsafe.Pointer(&c)))
	})
	at := packPoint(c.X-a.X+50, c.Y-a.Y+60)
	post(from, wmMouseMove, 1, at)
	ev := waitFor(t, to, func(ev platform.Event) bool { _, ok := ev.(platform.DragEvent); return ok })
	if d := ev.(platform.DragEvent); d.Kind != platform.DragEnter || !d.Own || d.X != 50 || d.Y != 60 {
		t.Errorf("the drag entered with %+v", d)
	}
	post(from, wmLButtonUp, 0, at)
	ev = waitFor(t, to, func(ev platform.Event) bool { _, ok := ev.(platform.DropEvent); return ok })
	if d := ev.(platform.DropEvent); !d.Own || d.X != 50 || d.Y != 60 {
		t.Errorf("the drop reported %+v", d)
	}
	ev = waitFor(t, from, func(ev platform.Event) bool { _, ok := ev.(platform.DragEndEvent); return ok })
	if e := ev.(platform.DragEndEvent); !e.Dropped || e.Outside {
		t.Errorf("the drag ended with %+v", e)
	}

	// Escape cancels a drag, which ends outside every window when the
	// pointer is away from them.
	post(from, wmLButtonDown, 1, packPoint(10, 10))
	waitFor(t, from, func(ev platform.Event) bool {
		p, ok := ev.(platform.PointerEvent)
		return ok && p.Kind == platform.Press
	})
	if !from.StartDrag(nil, image.Point{}) {
		t.Fatal("the second drag did not begin")
	}
	post(from, wmMouseMove, 1, packPoint(-30000, -30000))
	post(from, wmKeyDown, vkEscape, 1)
	ev = waitFor(t, from, func(ev platform.Event) bool { _, ok := ev.(platform.DragEndEvent); return ok })
	if e := ev.(platform.DragEndEvent); e.Dropped || !e.Outside {
		t.Errorf("the cancelled drag ended with %+v", e)
	}
}

func TestLiveClipboardRoundTrips(t *testing.T) {
	b := liveBackend(t)
	w := open(t, b, 50, 50, platform.Size{W: 300, H: 200})
	// What the clipboard held is put back afterwards.
	w.RequestPaste()
	var saved *platform.PasteEvent
	deadline := time.After(time.Second)
wait:
	for {
		select {
		case ev := <-w.Events():
			if p, ok := ev.(platform.PasteEvent); ok {
				saved = &p
				break wait
			}
		case <-deadline:
			break wait
		}
	}
	if saved != nil {
		t.Cleanup(func() {
			w.SetClipboard(saved.Text)
			b.call(func() {})
		})
	}
	w.SetClipboard("one\ntwo ✓")
	w.RequestPaste()
	ev := waitFor(t, w, func(ev platform.Event) bool { _, ok := ev.(platform.PasteEvent); return ok })
	if p := ev.(platform.PasteEvent); p.Text != "one\ntwo ✓" {
		t.Errorf("pasted %q", p.Text)
	}
}

func TestLiveCloseAndMaximize(t *testing.T) {
	b := liveBackend(t)
	w := open(t, b, 50, 50, platform.Size{W: 300, H: 200})
	post(w, wmClose, 0, 0)
	waitFor(t, w, func(ev platform.Event) bool { _, ok := ev.(platform.CloseEvent); return ok })
	w.SetMaximized(true)
	ev := waitFor(t, w, func(ev platform.Event) bool { _, ok := ev.(platform.StateEvent); return ok })
	if s := ev.(platform.StateEvent); !s.Maximized || s.Decorated {
		t.Errorf("maximising reported %+v", s)
	}
	w.Close()
	w.Close() // a second Close does nothing
	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-w.Events():
			if !ok {
				return
			}
		case <-timeout:
			t.Fatal("Events did not close")
		}
	}
}

func TestLiveSecondBackendIsRefused(t *testing.T) {
	liveBackend(t)
	if _, err := New(); err != ErrRunning {
		t.Errorf("a second New returned %v, want ErrRunning", err)
	}
}
