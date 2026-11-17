//go:build !windows

package wayland

import (
	"errors"
	"net"
	"sync"
	"syscall"
	"testing"
)

// server is a scripted compositor on the far end of a socket pair. Its
// handler sees every request and answers through the send helpers.
type server struct {
	t    *testing.T
	conn *Conn
	wg   sync.WaitGroup
}

// serve runs fn for each request until the client closes the socket.
func serve(t *testing.T, conn *Conn, fn func(s *server, ev Event, d *Decoder)) *server {
	s := &server{t: t, conn: conn}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			ev, err := conn.Recv()
			if err != nil {
				return
			}
			fn(s, ev, conn.Decode(ev))
		}
	}()
	t.Cleanup(func() { conn.Close(); s.wg.Wait() })
	return s
}

// send sends an event to the client. A client that has hung up, as one does
// when its test ends while the server is still answering a request, is not
// an error.
func (s *server) send(obj uint32, opcode uint16, build func(*Encoder)) {
	s.t.Helper()
	err := s.conn.Send(obj, opcode, build)
	if err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, syscall.EPIPE) && !errors.Is(err, syscall.ECONNRESET) {
		s.t.Errorf("server send: %v", err)
	}
}

// compositor returns a handler that answers the core requests.
// It advertises the globals in reply to wl_display.get_registry, answers
// wl_display.sync with a callback done event and a delete_id event, and
// passes wl_registry.bind requests to onBind.
func compositor(globals []Global, onBind func(s *server, g Global, version, id uint32), other func(s *server, ev Event, d *Decoder)) func(*server, Event, *Decoder) {
	return func(s *server, ev Event, d *Decoder) {
		switch {
		case ev.Object == displayID && ev.Opcode == displayGetRegistry:
			reg := d.Uint()
			for _, g := range globals {
				s.send(reg, registryGlobal, func(e *Encoder) { e.Uint(g.Name).String(g.Interface).Uint(g.Version) })
			}
		case ev.Object == displayID && ev.Opcode == displaySync:
			cb := d.Uint()
			s.send(cb, callbackDone, func(e *Encoder) { e.Uint(0) })
			s.send(displayID, displayDeleteID, func(e *Encoder) { e.Uint(cb) })
		case ev.Opcode == registryBind && ev.Object == 2:
			name, iface, version, id := d.Uint(), d.String(), d.Uint(), d.Uint()
			for _, g := range globals {
				if g.Name == name && g.Interface == iface {
					onBind(s, g, version, id)
				}
			}
		default:
			if other != nil {
				other(s, ev, d)
			}
		}
	}
}
