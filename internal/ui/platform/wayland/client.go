package wayland

import (
	"fmt"
	"sync"
)

// Object IDs and opcodes of wl_display, wl_registry and wl_callback.
const (
	displayID = 1

	displaySync        = 0
	displayGetRegistry = 1
	displayError       = 0
	displayDeleteID    = 1

	registryBind         = 0
	registryGlobal       = 0
	registryGlobalRemove = 1

	callbackDone = 0
)

// firstServerID is where the server's own object IDs begin; the client
// allocates below it.
const firstServerID = 0xff000000

// Handler receives the events of one object. It runs on the dispatching
// goroutine and must not block.
type Handler func(opcode uint16, d *Decoder)

// Global is an interface the compositor advertises.
type Global struct {
	Name      uint32
	Interface string
	Version   uint32
}

// ProtocolError is a fatal error the compositor reported on an object.
type ProtocolError struct {
	Object  uint32
	Code    uint32
	Message string
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("wayland: protocol error %d on object %d: %s", e.Code, e.Object, e.Message)
}

// Client tracks the objects of one connection and routes events to their
// handlers. Dispatch must be driven by a single goroutine; Send and NewID may
// be called from any goroutine.
type Client struct {
	conn *Conn

	mu       sync.Mutex
	next     uint32
	free     []uint32
	handlers map[uint32]Handler

	fatal error

	registry uint32
	globals  map[string]Global
}

// NewClient starts a client on conn. The caller owns conn.
func NewClient(conn *Conn) *Client {
	c := &Client{conn: conn, next: displayID + 1, handlers: map[uint32]Handler{}, globals: map[string]Global{}}
	c.handlers[displayID] = c.onDisplay
	return c
}

// Close closes the underlying connection.
func (c *Client) Close() error { return c.conn.Close() }

// NewID allocates an object ID and routes the object's events to h, which may
// be nil for an object that never receives any.
func (c *Client) NewID(h Handler) uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var id uint32
	if n := len(c.free); n > 0 {
		id, c.free = c.free[n-1], c.free[:n-1]
	} else {
		id, c.next = c.next, c.next+1
	}
	c.handlers[id] = h
	return id
}

// Send issues a request on object obj.
func (c *Client) Send(obj uint32, opcode uint16, build func(*Encoder)) error {
	return c.conn.Send(obj, opcode, build)
}

// Dispatch reads one event and runs its handler. It returns a *ProtocolError
// when the compositor reports one.
func (c *Client) Dispatch() error {
	ev, err := c.conn.Recv()
	if err != nil {
		return err
	}
	c.mu.Lock()
	h := c.handlers[ev.Object]
	c.mu.Unlock()
	if h == nil {
		return nil // a destroyed object may still receive events
	}
	d := c.conn.Decode(ev)
	return c.handle(h, ev, d)
}

// handle runs h and turns a decode failure or a reported protocol error into
// the error Dispatch returns.
func (c *Client) handle(h Handler, ev Event, d *Decoder) error {
	h(ev.Opcode, d)
	if err := d.Err(); err != nil {
		return fmt.Errorf("wayland: event %d of object %d: %w", ev.Opcode, ev.Object, err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.fatal
	c.fatal = nil
	return err
}

func (c *Client) onDisplay(opcode uint16, d *Decoder) {
	switch opcode {
	case displayError:
		obj, code, msg := d.Uint(), d.Uint(), d.String()
		c.mu.Lock()
		c.fatal = &ProtocolError{Object: obj, Code: code, Message: msg}
		c.mu.Unlock()
	case displayDeleteID:
		id := d.Uint()
		c.mu.Lock()
		delete(c.handlers, id)
		if id < firstServerID {
			c.free = append(c.free, id)
		}
		c.mu.Unlock()
	}
}

// Roundtrip returns once the compositor has processed every request sent so
// far, dispatching events meanwhile.
func (c *Client) Roundtrip() error {
	done := false
	cb := c.NewID(func(uint16, *Decoder) { done = true })
	if err := c.Send(displayID, displaySync, func(e *Encoder) { e.Uint(cb) }); err != nil {
		return err
	}
	for !done {
		if err := c.Dispatch(); err != nil {
			return err
		}
	}
	return nil
}

// LoadGlobals fetches the registry and records every advertised global.
func (c *Client) LoadGlobals() error {
	c.registry = c.NewID(func(opcode uint16, d *Decoder) {
		switch opcode {
		case registryGlobal:
			g := Global{Name: d.Uint(), Interface: d.String(), Version: d.Uint()}
			c.globals[g.Interface] = g
		case registryGlobalRemove:
			name := d.Uint()
			for k, g := range c.globals {
				if g.Name == name {
					delete(c.globals, k)
				}
			}
		}
	})
	if err := c.Send(displayID, displayGetRegistry, func(e *Encoder) { e.Uint(c.registry) }); err != nil {
		return err
	}
	return c.Roundtrip()
}

// Global returns the advertised global for iface.
func (c *Client) Global(iface string) (Global, bool) {
	g, ok := c.globals[iface]
	return g, ok
}

// Bind binds iface at the lower of want and the compositor's version, routing
// its events to h. It returns the object ID and the version in effect.
func (c *Client) Bind(iface string, want uint32, h Handler) (id, version uint32, err error) {
	g, ok := c.globals[iface]
	if !ok {
		return 0, 0, fmt.Errorf("wayland: compositor does not offer %s", iface)
	}
	version = min(want, g.Version)
	id = c.NewID(h)
	err = c.Send(c.registry, registryBind, func(e *Encoder) {
		e.Uint(g.Name).String(iface).Uint(version).Uint(id)
	})
	return id, version, err
}
