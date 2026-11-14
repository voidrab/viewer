package wayland

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// Event is one received message: the object it is for, its opcode and its
// still-encoded arguments.
type Event struct {
	Object uint32
	Opcode uint16
	args   []byte
}

// Conn is one end of a Wayland socket. Send is safe for concurrent use;
// Recv must not be called from more than one goroutine at a time.
type Conn struct {
	sock *net.UnixConn

	wmu sync.Mutex

	in []byte // received bytes not yet framed into events

	fmu sync.Mutex // guards fds, which Close also touches
	fds []int      // received descriptors not yet claimed by a decoder
}

// Dial connects to the compositor named by $WAYLAND_DISPLAY (default
// wayland-0) in $XDG_RUNTIME_DIR; an absolute display name is used as is.
func Dial() (*Conn, error) {
	name := os.Getenv("WAYLAND_DISPLAY")
	if name == "" {
		name = "wayland-0"
	}
	if !filepath.IsAbs(name) {
		dir := os.Getenv("XDG_RUNTIME_DIR")
		if dir == "" {
			return nil, errors.New("wayland: XDG_RUNTIME_DIR is not set")
		}
		name = filepath.Join(dir, name)
	}
	sock, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: name, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("wayland: %w", err)
	}
	return NewConn(sock), nil
}

// NewConn wraps an established unix socket.
func NewConn(sock *net.UnixConn) *Conn { return &Conn{sock: sock} }

// Close closes the socket and any descriptors received but never claimed.
func (c *Conn) Close() error {
	c.fmu.Lock()
	for _, fd := range c.fds {
		syscall.Close(fd)
	}
	c.fds = nil
	c.fmu.Unlock()
	return c.sock.Close()
}

// Send writes one request built by build, passing along any descriptors it
// added with Encoder.FD.
func (c *Conn) Send(obj uint32, opcode uint16, build func(*Encoder)) error {
	var e Encoder
	if build != nil {
		build(&e)
	}
	msg, err := frame(obj, opcode, e.buf)
	if err != nil {
		return err
	}
	var oob []byte
	if len(e.fds) > 0 {
		oob = syscall.UnixRights(e.fds...)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, _, err = c.sock.WriteMsgUnix(msg, oob, nil)
	return err
}

// Recv blocks for the next event. It returns io.EOF when the peer closed the
// connection between messages.
func (c *Conn) Recv() (Event, error) {
	for {
		if ev, ok, err := c.frameEvent(); err != nil || ok {
			return ev, err
		}
		buf := make([]byte, 4096)
		oob := make([]byte, syscall.CmsgSpace(4*28))
		n, oobn, _, _, err := c.sock.ReadMsgUnix(buf, oob)
		c.takeFDs(oob[:oobn])
		if n > 0 {
			c.in = append(c.in, buf[:n]...)
		}
		if err != nil {
			if err == io.EOF && len(c.in) > 0 {
				err = io.ErrUnexpectedEOF
			}
			return Event{}, err
		}
	}
}

// frameEvent cuts one complete event off the receive buffer.
func (c *Conn) frameEvent() (Event, bool, error) {
	if len(c.in) < headerSize {
		return Event{}, false, nil
	}
	size := int(order.Uint32(c.in[4:]) >> 16)
	if size < headerSize {
		return Event{}, false, ErrBadMessage
	}
	if len(c.in) < size {
		return Event{}, false, nil
	}
	ev := Event{
		Object: order.Uint32(c.in),
		Opcode: uint16(order.Uint32(c.in[4:])),
		args:   append([]byte(nil), c.in[headerSize:size]...),
	}
	c.in = c.in[size:]
	return ev, true, nil
}

// takeFDs queues the descriptors carried in oob. Malformed control data is
// ignored, so a decoder that needs a missing descriptor reports an error.
func (c *Conn) takeFDs(oob []byte) {
	msgs, err := syscall.ParseSocketControlMessage(oob)
	if err != nil {
		return
	}
	for _, m := range msgs {
		// ParseUnixRights indexes past the end of its result for a data
		// length that is not a multiple of four, so such data is skipped.
		if m.Header.Level != syscall.SOL_SOCKET || m.Header.Type != syscall.SCM_RIGHTS || len(m.Data)%4 != 0 {
			continue
		}
		fds, _ := syscall.ParseUnixRights(&m)
		c.fmu.Lock()
		c.fds = append(c.fds, fds...)
		c.fmu.Unlock()
	}
}

// Decode returns a decoder over ev's arguments that draws descriptors from
// this connection.
func (c *Conn) Decode(ev Event) *Decoder {
	return &Decoder{buf: ev.args, next: c.nextFD}
}

func (c *Conn) nextFD() (int, error) {
	c.fmu.Lock()
	defer c.fmu.Unlock()
	if len(c.fds) == 0 {
		return -1, errors.New("wayland: no file descriptor received with the message")
	}
	fd := c.fds[0]
	c.fds = c.fds[1:]
	return fd, nil
}
