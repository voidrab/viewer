//go:build !windows

package x11

import (
	"errors"
	"testing"

	"viewrab/internal/ui/platform"
)

func TestOpenWithoutAnARGBVisual(t *testing.T) {
	o := defaultSetup()
	o.visuals = o.visuals[:1]
	_, sock := newFakeX(t, o)
	c, err := NewConn(sock, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.argb != 0 {
		t.Errorf("argb visual %#x", b.argb)
	}
}

func TestOddEventsAndCachedCursors(t *testing.T) {
	f, b := startBackend(t, func(f *fakeX) {
		// A format list that claims more formats than it holds.
		f.handle = func(f *fakeX, r request) bool {
			if r.op == 139 && r.data == renderQueryPictFormats {
				f.reply(r, 0, (&req{}).u32(5).pad(20).b)
				return true
			}
			return false
		}
	})
	if b.render.present {
		t.Error("render kept without an ARGB format")
	}
	w := openWindow(t, f, b, platform.WindowOptions{})
	if w.Events() != (<-chan platform.Event)(w.events) {
		t.Error("Events is not the event channel")
	}
	f.event(xev(65, nil)) // a ShmCompletion
	f.event(inputEv(evKeyPress, 8, 0, w.id, 0, 0, 0, 0, 0))
	want[platform.KeyEvent](t, w)
	if b.in.now() != 0 {
		t.Errorf("time %d from an event without one", b.in.now())
	}
	quiet(t, f, w)
	if a, c := b.cursors.get(b, platform.CursorPointer), b.cursors.get(b, platform.CursorPointer); a != c || a == 0 {
		t.Errorf("cursors %#x %#x", a, c)
	}
}

func TestRequestsOnAClosedConnection(t *testing.T) {
	f, c := startFake(t, defaultSetup())
	f.sock.Close()
	<-c.Done()
	if _, err := c.Call(newReq(opGetInputFocus, 0)); !errors.Is(err, ErrClosed) {
		t.Errorf("Call = %v", err)
	}
	// Send also fails when it must first write the sync request that keeps
	// the sequence count from wrapping.
	c.seq += syncEvery
	if err := c.Send(newReq(opMapWindow, 0)); !errors.Is(err, ErrClosed) {
		t.Errorf("Send = %v", err)
	}
}
