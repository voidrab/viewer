package x11

import (
	"os"
	"sync/atomic"
	"testing"
	"time"

	"viewer/internal/ui/platform"
)

func TestMain(m *testing.M) {
	// Frames follow each other as fast as the fake server answers.
	frameInterval = 0
	os.Exit(m.Run())
}

// override sets *p to v for the rest of the test.
func override[T any](t *testing.T, p *T, v T) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

// xev builds a 32-byte event of the given code, with fields written at
// their offsets by set.
func xev(code byte, set func(b []byte)) []byte {
	b := make([]byte, 32)
	b[0] = code
	if set != nil {
		set(b)
	}
	return b
}

// inputEv builds a key, button or motion event.
func inputEv(code, detail byte, t, win uint32, rootX, rootY, x, y int, state uint16) []byte {
	return xev(code, func(b []byte) {
		b[1] = detail
		order.PutUint32(b[4:], t)
		order.PutUint32(b[8:], fakeRoot)
		order.PutUint32(b[12:], win)
		order.PutUint16(b[20:], uint16(rootX))
		order.PutUint16(b[22:], uint16(rootY))
		order.PutUint16(b[24:], uint16(x))
		order.PutUint16(b[26:], uint16(y))
		order.PutUint16(b[28:], state)
	})
}

// crossEv builds an EnterNotify or LeaveNotify.
func crossEv(code, detail byte, win uint32, x, y int, state uint16, mode byte) []byte {
	b := inputEv(code, detail, 5, win, x, y, x, y, state)
	b[30] = mode
	return b
}

// focusEv builds a FocusIn or FocusOut.
func focusEv(code, detail byte, win uint32, mode byte) []byte {
	return xev(code, func(b []byte) {
		b[1] = detail
		order.PutUint32(b[4:], win)
		b[8] = mode
	})
}

// mapEv, configureEv and propertyEv build the structure and property
// events of window win.
func mapEv(win uint32) []byte {
	return xev(evMapNotify, func(b []byte) { order.PutUint32(b[4:], win); order.PutUint32(b[8:], win) })
}

func configureEv(win uint32, w, h int) []byte {
	return xev(evConfigureNotify, func(b []byte) {
		order.PutUint32(b[8:], win)
		order.PutUint16(b[20:], uint16(w))
		order.PutUint16(b[22:], uint16(h))
	})
}

func propertyEv(win, atom uint32, deleted bool) []byte {
	return xev(evPropertyNotify, func(b []byte) {
		order.PutUint32(b[4:], win)
		order.PutUint32(b[8:], atom)
		order.PutUint32(b[12:], 9)
		if deleted {
			b[16] = 1
		}
	})
}

// want returns the next event of w, failing unless it is an E.
func want[E platform.Event](t *testing.T, w *window) E {
	t.Helper()
	select {
	case ev, ok := <-w.events:
		if !ok {
			t.Fatalf("events closed, want %T", *new(E))
		}
		e, ok := ev.(E)
		if !ok {
			t.Fatalf("got %T %+v, want %T", ev, ev, *new(E))
		}
		return e
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %T", *new(E))
	}
	panic("unreachable")
}

// quiet fails if w has an event once the backend has handled every event
// the fake server sent before.
func quiet(t *testing.T, f *fakeX, w *window) {
	t.Helper()
	settle(t, f, w)
	select {
	case ev := <-w.events:
		t.Fatalf("unexpected %T %+v", ev, ev)
	default:
	}
}

// pings numbers the pings settle sends.
var pings atomic.Uint32

// settle returns once the backend has handled the events the fake server
// sent before: it pings w and waits for the answer, which the backend sends
// only after the events ahead of the ping.
func settle(t *testing.T, f *fakeX, w *window) {
	t.Helper()
	n := pings.Add(1) + 1000
	f.event(clientMessage(w.id, f.atom("WM_PROTOCOLS"), f.atom("_NET_WM_PING"), n))
	f.waitRequest(func(r request) bool {
		return r.op == opSendEvent && len(r.body) >= 40 && r.body[8] == evClientMessage && u32At(r.body, 24) == n
	})
}

// openWindow opens a window on b and has the fake server map it, returning
// it once its first frame is asked for.
func openWindow(t *testing.T, f *fakeX, b *Backend, opts platform.WindowOptions) *window {
	t.Helper()
	if opts.Size == (platform.Size{}) {
		opts.Size = platform.Size{W: 40, H: 30}
	}
	pw, err := b.NewWindow(opts)
	if err != nil {
		t.Fatal(err)
	}
	w := pw.(*window)
	t.Cleanup(w.Close)
	f.event(mapEv(w.id))
	want[platform.ResizeEvent](t, w)
	want[platform.FrameEvent](t, w)
	return w
}
