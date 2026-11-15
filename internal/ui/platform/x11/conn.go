package x11

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// Packet types the server sends: the first byte of an error or a reply; any
// other value is an event's code, with the top bit set for an event that
// another client sent.
const (
	packetError = 0
	packetReply = 1

	// genericEvent is the one event longer than 32 bytes.
	genericEvent = 35
)

// Error is an error the server reported for a request.
type Error struct {
	Code   byte
	Major  byte
	Minor  uint16
	Value  uint32
	Serial uint64
}

func (e *Error) Error() string {
	return fmt.Sprintf("x11: error %d for request %d.%d (value %#x)", e.Code, e.Major, e.Minor, e.Value)
}

// ErrClosed is returned for requests on a closed connection.
var ErrClosed = errors.New("x11: connection closed")

// Format is a pixmap format the server supports.
type Format struct {
	Depth, BitsPerPixel, ScanlinePad byte
}

// Visual describes how pixel values become colours.
type Visual struct {
	ID                           uint32
	Class                        byte
	Depth                        byte
	RedMask, GreenMask, BlueMask uint32
}

// Screen is one of the server's screens.
type Screen struct {
	Root          uint32
	Colormap      uint32
	Width, Height int
	RootVisual    uint32
	RootDepth     byte
	Visuals       []Visual
}

// Setup is what the server reports on connecting.
type Setup struct {
	Vendor         string
	ResourceBase   uint32
	ResourceMask   uint32
	MaxRequest     int // in bytes
	ImageByteOrder byte
	MinKeycode     byte
	MaxKeycode     byte
	Formats        []Format
	Screens        []Screen
}

// Conn is a connection to an X server. Requests may be issued from any
// goroutine. A goroutine reads everything the server sends: it hands replies
// to the requests waiting for them and queues events, which Next returns, so
// an event handler may itself wait for replies.
type Conn struct {
	sock  net.Conn
	Setup Setup

	wmu     sync.Mutex
	seq     uint64 // the sequence number of the last request sent
	syncSeq uint64 // that of the last request with a reply

	mu      sync.Mutex
	cond    *sync.Cond
	pending []*cookie // requests awaiting replies, oldest first
	seen    uint64    // the latest sequence number the server reported
	events  [][]byte
	err     error // why reading ended
	idNext  uint32
	errs    func(*Error)
	done    chan struct{}
}

// cookie is a request that awaits its reply. ch is nil for a request whose
// reply is discarded.
type cookie struct {
	seq uint64
	ch  chan result
}

type result struct {
	reply []byte
	err   error
}

// syncEvery bounds how many requests go out without one that has a reply.
// Replies and events carry only the low 16 bits of a sequence number, so a
// request with a reply is slipped in before the count could wrap.
const syncEvery = 1 << 15

// Display is a parsed DISPLAY: where the server listens, and its number.
type Display struct {
	Network, Address string
	Abstract         string // a Linux abstract socket to try first, or empty
	Number           int
	Screen           int
}

// ParseDisplay parses a DISPLAY of the form [host]:number[.screen]. An empty
// host, or "unix", names the local server's socket; a leading '/' names a
// socket path directly, as macOS writes it.
func ParseDisplay(s string) (Display, error) {
	i := strings.LastIndexByte(s, ':')
	if i < 0 {
		return Display{}, fmt.Errorf("x11: bad DISPLAY %q", s)
	}
	host, rest := s[:i], s[i+1:]
	scr := 0
	if j := strings.IndexByte(rest, '.'); j >= 0 {
		n, err := strconv.Atoi(rest[j+1:])
		if err != nil {
			return Display{}, fmt.Errorf("x11: bad DISPLAY %q", s)
		}
		scr, rest = n, rest[:j]
	}
	num, err := strconv.Atoi(rest)
	if err != nil || num < 0 {
		return Display{}, fmt.Errorf("x11: bad DISPLAY %q", s)
	}
	d := Display{Number: num, Screen: scr}
	switch {
	case strings.HasPrefix(host, "/"):
		d.Network, d.Address = "unix", host+":"+strconv.Itoa(num)
	case host == "" || host == "unix":
		path := "/tmp/.X11-unix/X" + strconv.Itoa(num)
		d.Network, d.Address, d.Abstract = "unix", path, "@"+path
	default:
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]") // an IPv6 address
		d.Network, d.Address = "tcp", net.JoinHostPort(host, strconv.Itoa(6000+num))
	}
	return d, nil
}

// dial is replaced by tests.
var dial = net.Dial

// Dial connects to the server named by $DISPLAY, authorising with the
// user's Xauthority file.
func Dial() (*Conn, error) {
	name := os.Getenv("DISPLAY")
	if name == "" {
		return nil, errors.New("x11: DISPLAY is not set")
	}
	d, err := ParseDisplay(name)
	if err != nil {
		return nil, err
	}
	var sock net.Conn
	if d.Abstract != "" {
		sock, err = dial("unix", d.Abstract)
	}
	if sock == nil {
		if sock, err = dial(d.Network, d.Address); err != nil {
			return nil, fmt.Errorf("x11: %w", err)
		}
	}
	authName, authData := findAuth(authFile(), d.Number)
	c, err := NewConn(sock, authName, authData)
	if err != nil {
		sock.Close()
		return nil, err
	}
	if d.Screen >= len(c.Setup.Screens) {
		c.Close()
		return nil, fmt.Errorf("x11: display has no screen %d", d.Screen)
	}
	// The chosen screen goes first; the client uses only that one.
	c.Setup.Screens[0], c.Setup.Screens[d.Screen] = c.Setup.Screens[d.Screen], c.Setup.Screens[0]
	return c, nil
}

// NewConn performs the connection setup on sock, which it then reads
// from until Close.
func NewConn(sock net.Conn, authName string, authData []byte) (*Conn, error) {
	// The setup request has no length field: the byte order, a pad byte, the
	// protocol version and the lengths of the authorisation's two parts.
	r := &req{b: []byte{'l', 0}}
	r.u16(11).u16(0).u16(uint16(len(authName))).u16(uint16(len(authData))).u16(0)
	r.bytes([]byte(authName)).bytes(authData)
	if _, err := sock.Write(r.b); err != nil {
		return nil, fmt.Errorf("x11: setup: %w", err)
	}
	head := make([]byte, 8)
	if _, err := io.ReadFull(sock, head); err != nil {
		return nil, fmt.Errorf("x11: setup: %w", err)
	}
	body := make([]byte, 4*int(u16At(head, 6)))
	if _, err := io.ReadFull(sock, body); err != nil {
		return nil, fmt.Errorf("x11: setup: %w", err)
	}
	if head[0] != 1 {
		reason := strings.TrimRight(string(body), "\x00")
		if head[0] == 0 && int(head[1]) <= len(body) {
			reason = string(body[:head[1]])
		}
		return nil, fmt.Errorf("x11: the server refused the connection: %s", reason)
	}
	c := &Conn{sock: sock, done: make(chan struct{})}
	c.cond = sync.NewCond(&c.mu)
	if err := c.Setup.parse(body); err != nil {
		return nil, err
	}
	if len(c.Setup.Screens) == 0 {
		return nil, errors.New("x11: the server has no screens")
	}
	go c.read()
	return c, nil
}

// parse reads the body of a successful setup reply.
func (s *Setup) parse(b []byte) error {
	d := &reader{b: b}
	d.skip(4) // release number
	s.ResourceBase, s.ResourceMask = d.u32(), d.u32()
	d.skip(4) // motion buffer size
	vendorLen := int(d.u16())
	s.MaxRequest = 4 * int(d.u16())
	screens, formats := int(d.u8()), int(d.u8())
	s.ImageByteOrder = d.u8()
	d.skip(3) // bitmap bit order, scanline unit and pad
	s.MinKeycode, s.MaxKeycode = d.u8(), d.u8()
	d.skip(4)
	s.Vendor = d.str(vendorLen)
	d.skip(pad4(vendorLen) - vendorLen)
	for range formats {
		s.Formats = append(s.Formats, Format{Depth: d.u8(), BitsPerPixel: d.u8(), ScanlinePad: d.u8()})
		d.skip(5)
	}
	for range screens {
		var sc Screen
		sc.Root, sc.Colormap = d.u32(), d.u32()
		d.skip(12) // white and black pixels, current input masks
		sc.Width, sc.Height = int(d.u16()), int(d.u16())
		d.skip(8) // millimetres, installed maps
		sc.RootVisual = d.u32()
		d.skip(2) // backing stores, save unders
		sc.RootDepth = d.u8()
		depths := int(d.u8())
		for range depths {
			depth := d.u8()
			d.skip(1)
			n := int(d.u16())
			d.skip(4)
			for range n {
				v := Visual{ID: d.u32(), Class: d.u8(), Depth: depth}
				d.skip(3)
				v.RedMask, v.GreenMask, v.BlueMask = d.u32(), d.u32(), d.u32()
				d.skip(4)
				sc.Visuals = append(sc.Visuals, v)
			}
		}
		s.Screens = append(s.Screens, sc)
	}
	if d.err != nil {
		return fmt.Errorf("x11: setup: %w", d.err)
	}
	return nil
}

// Close closes the connection. Waiting requests fail, and Next returns the
// queued events and then an error.
func (c *Conn) Close() error {
	err := c.sock.Close()
	<-c.done
	return err
}

// Done is closed once the connection has ended.
func (c *Conn) Done() <-chan struct{} { return c.done }

// OnError sets the function that receives the errors of requests whose
// replies nobody waits for. It runs on the reading goroutine.
func (c *Conn) OnError(f func(*Error)) {
	c.mu.Lock()
	c.errs = f
	c.mu.Unlock()
}

// NewID allocates a resource ID.
func (c *Conn) NewID() uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	mask := c.Setup.ResourceMask
	inc := mask & -mask
	c.idNext += inc
	return c.Setup.ResourceBase | (c.idNext & mask)
}

// send writes one request, registering ck, when it is not nil, to receive
// its reply.
func (c *Conn) send(r *req, ck *cookie) error {
	b := r.finish()
	if max := c.Setup.MaxRequest; max > 0 && len(b) > max {
		return ErrTooLarge
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if ck == nil && c.seq-c.syncSeq >= syncEvery {
		// Send a GetInputFocus, whose reply is discarded, so that a request
		// with a reply is sent before the count can wrap.
		if err := c.write(newReq(43, 0), &cookie{}); err != nil {
			return err
		}
	}
	return c.write(r, ck)
}

// write sends r and registers ck. The caller holds c.wmu.
func (c *Conn) write(r *req, ck *cookie) error {
	b := r.finish()
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return ErrClosed
	}
	c.seq++
	if ck != nil {
		ck.seq = c.seq
		c.syncSeq = c.seq
		c.pending = append(c.pending, ck)
	}
	c.mu.Unlock()
	var err error
	if uc, ok := c.sock.(*net.UnixConn); ok && len(r.fds) > 0 {
		_, _, err = uc.WriteMsgUnix(b, syscall.UnixRights(r.fds...), nil)
	} else {
		_, err = c.sock.Write(b)
	}
	return err
}

// Send issues a request that has no reply.
func (c *Conn) Send(r *req) error { return c.send(r, nil) }

// Call issues a request and waits for its reply, which it returns whole:
// the 32-byte header and any further data.
func (c *Conn) Call(r *req) ([]byte, error) { return c.Start(r)() }

// Start issues a request with a reply and returns the function that waits
// for it, so several requests can be in flight at once.
func (c *Conn) Start(r *req) func() ([]byte, error) {
	ck := &cookie{ch: make(chan result, 1)}
	if err := c.send(r, ck); err != nil {
		return func() ([]byte, error) { return nil, err }
	}
	return func() ([]byte, error) {
		res := <-ck.ch
		return res.reply, res.err
	}
}

// Sync waits until the server has handled every request sent so far.
func (c *Conn) Sync() error {
	_, err := c.Call(newReq(43, 0))
	return err
}

// read receives everything the server sends until the connection ends.
func (c *Conn) read() {
	defer close(c.done)
	for {
		p, err := c.readPacket()
		if err != nil {
			c.mu.Lock()
			c.err = err
			pending := c.pending
			c.pending = nil
			c.cond.Broadcast()
			c.mu.Unlock()
			for _, ck := range pending {
				if ck.ch != nil {
					ck.ch <- result{err: ErrClosed}
				}
			}
			return
		}
		c.route(p)
	}
}

// readPacket reads one error, reply or event.
func (c *Conn) readPacket() ([]byte, error) {
	p := make([]byte, 32)
	if _, err := io.ReadFull(c.sock, p); err != nil {
		return nil, err
	}
	if p[0] == packetReply || p[0]&0x7f == genericEvent {
		extra := make([]byte, 4*int(u32At(p, 4)))
		if _, err := io.ReadFull(c.sock, extra); err != nil {
			return nil, err
		}
		p = append(p, extra...)
	}
	return p, nil
}

// route hands a packet to the request it answers, or queues it as an event.
func (c *Conn) route(p []byte) {
	c.mu.Lock()
	// The sequence number in a packet is the low 16 bits of the latest
	// request the server has handled, which is never older than the one
	// last seen.
	seq := c.seen&^0xffff | uint64(u16At(p, 2))
	if seq < c.seen {
		seq += 1 << 16
	}
	c.seen = seq
	if p[0] != packetError && p[0] != packetReply {
		c.events = append(c.events, p)
		c.cond.Signal()
		c.mu.Unlock()
		return
	}
	// Replies come in order, so every request older than this one that is
	// still waiting got its answer already or never gets one.
	var ck *cookie
	for len(c.pending) > 0 && c.pending[0].seq <= seq {
		if c.pending[0].seq == seq {
			ck = c.pending[0]
		}
		c.pending = c.pending[1:]
	}
	errs := c.errs
	c.mu.Unlock()
	var res result
	if p[0] == packetError {
		e := &Error{Code: p[1], Value: u32At(p, 4), Minor: u16At(p, 8), Major: p[10], Serial: seq}
		if ck == nil || ck.ch == nil {
			if errs != nil {
				errs(e)
			}
			return
		}
		res.err = e
	} else {
		res.reply = p
	}
	if ck != nil && ck.ch != nil {
		ck.ch <- res
	}
}

// Next waits for the next event and returns it, 32 bytes or, for a generic
// event, more. Once the connection has ended and every event has been taken,
// it returns the reason it ended.
func (c *Conn) Next() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.events) == 0 && c.err == nil {
		c.cond.Wait()
	}
	if len(c.events) == 0 {
		return nil, c.err
	}
	ev := c.events[0]
	c.events = c.events[1:]
	return ev, nil
}

// Peek returns the queued event that Next would return, without waiting.
func (c *Conn) Peek() ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.events) == 0 {
		return nil, false
	}
	return c.events[0], true
}
