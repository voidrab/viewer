//go:build xlive

// Live tests against the X server named by $DISPLAY. They open real windows
// briefly, so they run only when asked for:
//
//	go test -tags xlive -run Live ./internal/ui/platform/x11/
package x11

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"sync"
	"testing"
	"time"
	"viewer/internal/ui/platform/desktop"

	"viewer/internal/ui/platform"
)

// liveBackend opens a backend on the real server and fails the test on any
// X error it reports.
func liveBackend(t *testing.T) *Backend {
	t.Helper()
	b, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var mu sync.Mutex
	b.c.OnError(func(e *Error) {
		mu.Lock()
		defer mu.Unlock()
		t.Errorf("X error: %v", e)
	})
	t.Cleanup(func() { b.Close() })
	return b
}

// liveWindow opens a window and waits for its first frame request.
func liveWindow(t *testing.T, b *Backend, title string) *window {
	t.Helper()
	pw, err := b.NewWindow(platform.WindowOptions{Title: title, AppID: "viewer.livetest", Size: platform.Size{W: 320, H: 200}, MinSize: platform.Size{W: 100, H: 80}})
	if err != nil {
		t.Fatalf("NewWindow: %v", err)
	}
	w := pw.(*window)
	t.Cleanup(w.Close)
	waitFor[platform.FrameEvent](t, w)
	return w
}

// waitFor returns the next event of type E from w, discarding others.
func waitFor[E platform.Event](t *testing.T, w *window) E {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-w.events:
			if !ok {
				t.Fatalf("window closed waiting for %T", *new(E))
			}
			if e, ok := ev.(E); ok {
				return e
			}
		case <-timeout:
			var zero E
			t.Fatalf("timed out waiting for %T", zero)
			return zero
		}
	}
}

// present fills w with c, answering a frame request.
func present(w *window, c color.RGBA) {
	w.mu.Lock()
	size := w.size
	w.mu.Unlock()
	img := image.NewRGBA(image.Rect(0, 0, size.W, size.H))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	w.Present(img)
}

// pixel reads the pixel at (x, y) of window back from the server.
func pixel(t *testing.T, b *Backend, window uint32, x, y int) (r, g, bl byte) {
	t.Helper()
	rep, err := b.c.Call(newReq(73, imageFormatZPixmap).u32(window).i16(x).i16(y).u16(1).u16(1).u32(0xffffffff))
	if err != nil {
		t.Fatalf("GetImage: %v", err)
	}
	p := u32At(rep, 32)
	return byte(p >> b.pixels.r), byte(p >> b.pixels.g), byte(p >> b.pixels.b)
}

func TestLivePresent(t *testing.T) {
	b := liveBackend(t)
	t.Logf("vendor %q, shm %v, shape %v, render %v, argb %#x, wm %v, client decorated %v",
		b.c.Setup.Vendor, b.shm.present, b.shape.present, b.render.present, b.argb, !b.noWM, b.clientDecorated())
	w := liveWindow(t, b, "live present ✓")
	for i, c := range []color.RGBA{{200, 30, 40, 255}, {10, 220, 60, 255}} {
		present(w, c)
		b.c.Sync()
		if r, g, bl := pixel(t, b, w.id, 10, 10); r != c.R || g != c.G || bl != c.B {
			t.Errorf("frame %d: pixel = %d,%d,%d, want %d,%d,%d", i, r, g, bl, c.R, c.G, c.B)
		}
		w.Invalidate()
		waitFor[platform.FrameEvent](t, w)
	}
	p, err := b.c.getProperty(w.id, b.atom("_NET_WM_NAME"), false, 100)
	if err != nil || string(p.value) != "live present ✓" {
		t.Errorf("_NET_WM_NAME = %q, %v", p.value, err)
	}
}

func TestLivePresentWithoutShm(t *testing.T) {
	b := liveBackend(t)
	b.shm.present = false
	w := liveWindow(t, b, "live putimage")
	present(w, color.RGBA{1, 2, 250, 255})
	b.c.Sync()
	if r, g, bl := pixel(t, b, w.id, 300, 190); r != 1 || g != 2 || bl != 250 {
		t.Errorf("pixel = %d,%d,%d", r, g, bl)
	}
}

func TestLiveClipboardBetweenClients(t *testing.T) {
	a, b := liveBackend(t), liveBackend(t)
	wa, wb := liveWindow(t, a, "clip a"), liveWindow(t, b, "clip b")
	for _, text := range []string{"héllo ✓ wörld", strings.Repeat("0123456789abcdef", 1<<16)} {
		wa.SetClipboard(text)
		a.c.Sync()
		wb.RequestPaste()
		got := waitFor[platform.PasteEvent](t, wb)
		if got.Text != text {
			t.Errorf("pasted %d bytes %.40q, want %d bytes %.40q", len(got.Text), got.Text, len(text), text)
		}
	}
	// The other way round, and the owner pasting its own text.
	wb.SetClipboard("from b")
	b.c.Sync()
	wa.RequestPaste()
	if got := waitFor[platform.PasteEvent](t, wa); got.Text != "from b" {
		t.Errorf("pasted %q", got.Text)
	}
	wb.RequestPaste()
	if got := waitFor[platform.PasteEvent](t, wb); got.Text != "from b" {
		t.Errorf("own paste %q", got.Text)
	}
}

// keycodeFor returns a key code that types r without modifiers.
func keycodeFor(t *testing.T, b *Backend, r rune) byte {
	km := b.in.keymap
	for code := int(km.min); code <= int(b.c.Setup.MaxKeycode); code++ {
		if _, got := km.lookup(byte(code), 0); got == r {
			return byte(code)
		}
	}
	t.Fatalf("no key types %q", r)
	return 0
}

// synth sends w a synthetic input event of the given code.
func synth(b *Backend, w *window, code, detail byte, state uint16, x, y int) {
	ev := make([]byte, 32)
	ev[0], ev[1] = code, detail
	order.PutUint32(ev[4:], 1)
	order.PutUint32(ev[8:], b.root)
	order.PutUint32(ev[12:], w.id)
	order.PutUint16(ev[24:], uint16(x))
	order.PutUint16(ev[26:], uint16(y))
	order.PutUint16(ev[28:], state)
	ev[30] = 1
	b.c.Send(sendEvent(w.id, 0, ev))
}

func TestLiveSyntheticInput(t *testing.T) {
	b := liveBackend(t)
	w := liveWindow(t, b, "live input")
	a := keycodeFor(t, b, 'a')
	synth(b, w, evKeyPress, a, stateShift, 0, 0)
	if k := waitFor[platform.KeyEvent](t, w); k.Rune != 'A' || !k.Down || k.Mods != platform.Shift {
		t.Errorf("key = %+v", k)
	}
	synth(b, w, evButtonPress, 1, stateControl, 12, 34)
	if p := waitFor[platform.PointerEvent](t, w); p.Kind != platform.Press || p.Button != platform.Left || p.X != 12 || p.Y != 34 || p.Mods != platform.Ctrl {
		t.Errorf("press = %+v", p)
	}
	synth(b, w, evButtonPress, 5, 0, 5, 6)
	if s := waitFor[platform.ScrollEvent](t, w); s.DY != scrollStep || s.X != 5 {
		t.Errorf("scroll = %+v", s)
	}
}

func TestLiveXdndDrop(t *testing.T) {
	b := liveBackend(t)
	w := liveWindow(t, b, "live drop")
	src, err := Dial()
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	atoms, _ := src.internAtoms([]string{"XdndEnter", "XdndPosition", "XdndDrop", "XdndStatus", "XdndFinished", "XdndSelection", desktop.URIListMime, "XdndActionCopy"})
	sw := src.NewID()
	root := src.Setup.Screens[0].Root
	src.Send(newReq(opCreateWindow, 0).u32(sw).u32(root).i16(-10).i16(-10).u16(1).u16(1).u16(0).u16(2).u32(0).u32(0))
	src.Send(newReq(opSetSelectionOwner, 0).u32(sw).u32(atoms["XdndSelection"]).u32(0))
	msg := func(typ string, data ...uint32) {
		src.Send(sendEvent(w.id, 0, clientMessage(w.id, atoms[typ], data...)))
	}
	// Where the window is on the root, to drop at (40, 50) in it.
	ox, oy, _, _ := src.translate(w.id, root, 0, 0)
	msg("XdndEnter", sw, 5<<24, atoms[desktop.URIListMime])
	msg("XdndPosition", sw, 0, uint32(ox+40)<<16|uint32(oy+50), 0, atoms["XdndActionCopy"])
	if d := waitFor[platform.DragEvent](t, w); d.Kind != platform.DragEnter || d.X != 40 || d.Y != 50 || d.Own {
		t.Errorf("drag = %+v", d)
	}
	msg("XdndDrop", sw, 0, 0)
	// Play the source: answer the request for the files, and expect status
	// and finished messages.
	deadline := time.Now().Add(5 * time.Second)
	var status, finished bool
	for time.Now().Before(deadline) && !finished {
		ev, err := src.Next()
		if err != nil {
			t.Fatal(err)
		}
		switch ev[0] &^ 0x80 {
		case evClientMessage:
			switch u32At(ev, 8) {
			case atoms["XdndStatus"]:
				status = u32At(ev, 16)&1 == 1
			case atoms["XdndFinished"]:
				finished = true
				if u32At(ev, 16)&1 != 1 {
					t.Error("drop not taken")
				}
			}
		case evSelectionRequest:
			requestor, target, prop := u32At(ev, 12), u32At(ev, 20), u32At(ev, 24)
			src.Send(changeProperty(propReplace, requestor, prop, target, 8, []byte("file:///tmp/a%20b.pdf\r\nhttp://x/y\r\n")))
			reply := make([]byte, 32)
			reply[0] = evSelectionNotify
			copy(reply[4:], ev[4:8])
			copy(reply[8:], ev[12:28])
			src.Send(sendEvent(requestor, 0, reply))
		}
	}
	if !status || !finished {
		t.Errorf("status %v, finished %v", status, finished)
	}
	if d := waitFor[platform.DropEvent](t, w); len(d.Paths) != 1 || d.Paths[0] != "/tmp/a b.pdf" || d.X != 40 {
		t.Errorf("drop = %+v", d)
	}
}

func TestLiveMaximize(t *testing.T) {
	b := liveBackend(t)
	if b.noWM {
		t.Skip("no window manager")
	}
	w := liveWindow(t, b, "live maximize")
	w.SetMaximized(true)
	if s := waitFor[platform.StateEvent](t, w); !s.Maximized {
		t.Errorf("state = %+v", s)
	}
	if r := waitFor[platform.ResizeEvent](t, w); r.Size.W <= 320 {
		t.Errorf("maximised size = %+v", r.Size)
	}
	w.SetMaximized(false)
	if s := waitFor[platform.StateEvent](t, w); s.Maximized {
		t.Errorf("state = %+v", s)
	}
}

func TestLiveCursors(t *testing.T) {
	b := liveBackend(t)
	w := liveWindow(t, b, "live cursors")
	t.Logf("cursor theme %q size %d", b.cursors.theme, b.cursors.size)
	for c := range cursorShapes {
		w.SetCursor(c)
		if b.cursors.get(b, c) == 0 {
			t.Errorf("cursor %d not made", c)
		}
	}
	b.c.Sync()
	// The core font, as a theme-less server would give.
	b.cursors.made = map[platform.Cursor]uint32{}
	b.render.present = false
	for c := range cursorShapes {
		if b.cursors.get(b, c) == 0 {
			t.Errorf("font cursor %d not made", c)
		}
	}
	b.c.Sync()
}

func TestLiveDragCancel(t *testing.T) {
	b := liveBackend(t)
	w := liveWindow(t, b, "live drag")
	icon := image.NewRGBA(image.Rect(0, 0, 40, 20))
	if !w.StartDrag(icon, image.Pt(5, 5)) {
		t.Skip("the pointer could not be grabbed")
	}
	b.c.Sync()
	synth(b, w, evKeyPress, keycodeForKey(t, b, platform.KeyEscape), 0, 0, 0)
	end := waitFor[platform.DragEndEvent](t, w)
	if end.Dropped {
		t.Errorf("end = %+v", end)
	}
	if b.drag.active() {
		t.Error("drag still active")
	}
}

// keycodeForKey returns a key code of the non-text key k.
func keycodeForKey(t *testing.T, b *Backend, k platform.Key) byte {
	km := b.in.keymap
	for code := int(km.min); code <= int(b.c.Setup.MaxKeycode); code++ {
		if got, _ := km.lookup(byte(code), 0); got == k {
			return byte(code)
		}
	}
	t.Fatalf("no key %d", k)
	return 0
}

func TestLiveKeymapTypesAscii(t *testing.T) {
	b := liveBackend(t)
	var missing []string
	for r := rune(0x21); r < 0x7f; r++ {
		found := false
		km := b.in.keymap
		for code := int(km.min); code <= int(b.c.Setup.MaxKeycode) && !found; code++ {
			for _, st := range []uint16{0, stateShift, km.level3, km.level3 | stateShift} {
				if _, got := km.lookup(byte(code), st); got == r {
					found = true
				}
			}
		}
		if !found {
			missing = append(missing, string(r))
		}
	}
	// Dead keys, such as ^ and ` on a German layout, type nothing alone.
	if len(missing) > 2 {
		t.Errorf("no key types %v", missing)
	}
}

func TestLiveKeymapDump(t *testing.T) {
	b := liveBackend(t)
	km := b.in.keymap
	t.Logf("level3 %#x modeSwitch %#x numLock %#x alt %#x super %#x", km.level3, km.modeSwitch, km.numLock, km.alt, km.super)
	for _, code := range []byte{24, 29, 17, 20, 34, 35, 49, 51, 94} {
		var s []string
		for _, st := range []uint16{0, stateShift, km.level3, km.level3 | stateShift} {
			_, r := km.lookup(code, st)
			s = append(s, fmt.Sprintf("%q", r))
		}
		t.Logf("code %d: syms %x -> %v", code, km.keysyms(code), s)
	}
}
