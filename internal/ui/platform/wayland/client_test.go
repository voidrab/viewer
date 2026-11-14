package wayland

import (
	"errors"
	"io"
	"testing"
)

var testGlobals = []Global{
	{Name: 1, Interface: "wl_compositor", Version: 6},
	{Name: 2, Interface: "wl_shm", Version: 1},
	{Name: 3, Interface: "wl_output", Version: 4},
}

func newClient(t *testing.T, fn func(*server, Event, *Decoder)) *Client {
	t.Helper()
	a, b := pair(t)
	serve(t, b, fn)
	return NewClient(a)
}

func TestLoadGlobalsAndBind(t *testing.T) {
	bound := make(chan [3]uint32, 1)
	c := newClient(t, compositor(testGlobals, func(s *server, g Global, version, id uint32) {
		bound <- [3]uint32{g.Name, version, id}
		// Greet the new object, so the handler wiring is exercised.
		s.send(id, 0, func(e *Encoder) { e.Uint(77) })
	}, nil))
	if err := c.LoadGlobals(); err != nil {
		t.Fatal(err)
	}
	if g, ok := c.Global("wl_shm"); !ok || g.Version != 1 {
		t.Errorf("Global(wl_shm) = %+v, %v", g, ok)
	}
	if _, ok := c.Global("zwp_nothing"); ok {
		t.Error("found a global that was not advertised")
	}

	var got uint32
	id, version, err := c.Bind("wl_compositor", 4, func(op uint16, d *Decoder) { got = d.Uint() })
	if err != nil || version != 4 {
		t.Fatalf("Bind = %d, %d, %v", id, version, err)
	}
	if b := <-bound; b != [3]uint32{1, 4, id} {
		t.Errorf("server saw bind %v", b)
	}
	// Ask for more than the server has: the lower version wins.
	if _, v, _ := c.Bind("wl_shm", 9, nil); v != 1 {
		t.Errorf("negotiated version = %d", v)
	}
	if _, _, err := c.Bind("zwp_nothing", 1, nil); err == nil {
		t.Error("Bind of a missing global succeeded")
	}
	if err := c.Roundtrip(); err != nil { // the greeting arrives before the done
		t.Fatal(err)
	}
	if got != 77 {
		t.Errorf("bound object handler saw %d", got)
	}
}

func TestIDsAreRecycledAfterDeleteID(t *testing.T) {
	c := newClient(t, compositor(nil, nil, nil))
	first := c.NewID(nil)
	if first != 2 {
		t.Fatalf("first id = %d", first)
	}
	// The first roundtrip's callback (id 3) is deleted by a delete_id that
	// arrives after its done, so a second roundtrip dispatches it.
	for range 2 {
		if err := c.Roundtrip(); err != nil {
			t.Fatal(err)
		}
	}
	if again := c.NewID(nil); again != 3 {
		t.Errorf("callback id was not recycled: next = %d", again)
	}
}

func TestServerIDsAreNotRecycled(t *testing.T) {
	c := newClient(t, func(*server, Event, *Decoder) {})
	c.onDisplay(displayDeleteID, &Decoder{buf: order.AppendUint32(nil, firstServerID+1)})
	if len(c.free) != 0 {
		t.Errorf("a server-allocated id was queued for reuse: %v", c.free)
	}
}

func TestProtocolError(t *testing.T) {
	c := newClient(t, func(s *server, ev Event, d *Decoder) {
		s.send(displayID, displayError, func(e *Encoder) { e.Uint(5).Uint(2).String("bad request") })
	})
	c.Send(displayID, displaySync, func(e *Encoder) { e.Uint(c.NewID(nil)) })
	err := c.Dispatch()
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Object != 5 || pe.Code != 2 || pe.Message != "bad request" {
		t.Fatalf("Dispatch = %v", err)
	}
	if pe.Error() == "" {
		t.Error("empty error text")
	}
	// The error is reported once.
	if c.fatal != nil {
		t.Error("fatal error not cleared")
	}
}

func TestDispatchIgnoresUnknownObjectsAndReportsBadEvents(t *testing.T) {
	c := newClient(t, func(s *server, ev Event, d *Decoder) {
		s.send(99, 0, nil) // nobody listens on 99
		s.send(2, 0, nil)  // object 2 expects an argument that is missing
	})
	id := c.NewID(func(op uint16, d *Decoder) { d.Uint() })
	c.Send(id, 0, nil)
	if err := c.Dispatch(); err != nil {
		t.Fatalf("event for an unknown object: %v", err)
	}
	if err := c.Dispatch(); !errors.Is(err, ErrShortMessage) {
		t.Errorf("short event = %v", err)
	}
}

func TestRegistryGlobalRemove(t *testing.T) {
	var reg uint32
	c := newClient(t, func(s *server, ev Event, d *Decoder) {
		if ev.Object == displayID && ev.Opcode == displayGetRegistry {
			reg = d.Uint()
			s.send(reg, registryGlobal, func(e *Encoder) { e.Uint(7).String("wl_output").Uint(1) })
			s.send(reg, registryGlobal, func(e *Encoder) { e.Uint(8).String("wl_seat").Uint(1) })
			s.send(reg, registryGlobalRemove, func(e *Encoder) { e.Uint(7) })
			cb := uint32(0)
			_ = cb
		}
		if ev.Object == displayID && ev.Opcode == displaySync {
			cb := d.Uint()
			s.send(cb, callbackDone, func(e *Encoder) { e.Uint(0) })
		}
	})
	if err := c.LoadGlobals(); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Global("wl_output"); ok {
		t.Error("removed global still present")
	}
	if _, ok := c.Global("wl_seat"); !ok {
		t.Error("remaining global lost")
	}
}

func TestSendAndRecvFailures(t *testing.T) {
	a, b := pair(t)
	c := NewClient(a)
	b.Close()
	if err := c.Dispatch(); err != io.EOF {
		t.Errorf("Dispatch on a closed peer = %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
	// With the client's own socket closed, sending fails.
	if err := c.Roundtrip(); err == nil {
		t.Error("Roundtrip on a closed connection succeeded")
	}
	if err := c.LoadGlobals(); err == nil {
		t.Error("LoadGlobals on a closed connection succeeded")
	}
}

// TestRoundtripStopsOnProtocolError checks that Roundtrip surfaces an error
// the compositor sends instead of its done.
func TestRoundtripStopsOnProtocolError(t *testing.T) {
	c := newClient(t, func(s *server, ev Event, d *Decoder) {
		s.send(displayID, displayError, func(e *Encoder) { e.Uint(1).Uint(0).String("nope") })
	})
	var pe *ProtocolError
	if err := c.Roundtrip(); !errors.As(err, &pe) {
		t.Errorf("Roundtrip = %v", err)
	}
}
