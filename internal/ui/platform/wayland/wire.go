//go:build !windows

// Package wayland is a pure-Go Wayland client: the wire protocol over a unix
// socket and the handful of interfaces a software-rendered window needs.
package wayland

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Framing constants of the wire format.
const (
	headerSize = 8
	// MaxMessage is the largest message the protocol can frame: the size
	// field is 16 bits and counts the header.
	MaxMessage = 1<<16 - 1
)

var order = binary.NativeEndian

// Errors reported by the wire layer.
var (
	ErrShortMessage = errors.New("wayland: message too short for its arguments")
	ErrTooLarge     = errors.New("wayland: message exceeds 65535 bytes")
	ErrBadMessage   = errors.New("wayland: malformed message header")
)

// Encoder builds the argument payload of one request.
type Encoder struct {
	buf []byte
	fds []int
}

func (e *Encoder) word(v uint32) { e.buf = order.AppendUint32(e.buf, v) }

// Uint appends an unsigned integer, object ID or new_id.
func (e *Encoder) Uint(v uint32) *Encoder { e.word(v); return e }

// Int appends a signed integer.
func (e *Encoder) Int(v int32) *Encoder { e.word(uint32(v)); return e }

// Fixed appends a signed 24.8 fixed-point number.
func (e *Encoder) Fixed(v float64) *Encoder { e.word(uint32(int32(math.Round(v * 256)))); return e }

// String appends a NUL-terminated, 4-byte padded string.
func (e *Encoder) String(s string) *Encoder {
	e.word(uint32(len(s) + 1))
	e.buf = append(e.buf, s...)
	e.buf = append(e.buf, 0)
	e.pad()
	return e
}

// Array appends a length-prefixed, 4-byte padded byte array.
func (e *Encoder) Array(b []byte) *Encoder {
	e.word(uint32(len(b)))
	e.buf = append(e.buf, b...)
	e.pad()
	return e
}

// FD queues fd to be passed out of band with the message. It adds no payload
// bytes.
func (e *Encoder) FD(fd int) *Encoder { e.fds = append(e.fds, fd); return e }

func (e *Encoder) pad() {
	e.buf = append(e.buf, make([]byte, padded(len(e.buf))-len(e.buf))...)
}

// Decoder reads the arguments of one received event. The first failure is
// sticky: later reads return zero values and Err reports it.
type Decoder struct {
	buf  []byte
	next func() (int, error)
	err  error
}

// Err returns the first decoding error.
func (d *Decoder) Err() error { return d.err }

func (d *Decoder) take(n int) []byte {
	if d.err != nil {
		return nil
	}
	if n < 0 || n > len(d.buf) {
		d.err = ErrShortMessage
		return nil
	}
	b := d.buf[:n]
	d.buf = d.buf[n:]
	return b
}

// Uint reads an unsigned integer, object ID or new_id.
func (d *Decoder) Uint() uint32 {
	b := d.take(4)
	if b == nil {
		return 0
	}
	return order.Uint32(b)
}

// Int reads a signed integer.
func (d *Decoder) Int() int32 { return int32(d.Uint()) }

// Fixed reads a signed 24.8 fixed-point number.
func (d *Decoder) Fixed() float64 { return float64(d.Int()) / 256 }

// padded is n rounded up to a multiple of four.
func padded(n int) int { return (n + 3) &^ 3 }

func (d *Decoder) bytes() []byte {
	n := int(d.Uint())
	b := d.take(padded(n))
	if b == nil {
		return nil
	}
	return b[:n]
}

// String reads a string; an empty (null) string yields "".
func (d *Decoder) String() string {
	b := d.bytes()
	if len(b) == 0 {
		return ""
	}
	return string(b[:len(b)-1])
}

// Array reads a byte array, copying it out of the message.
func (d *Decoder) Array() []byte { return append([]byte(nil), d.bytes()...) }

// FD takes the next file descriptor received with the message.
func (d *Decoder) FD() int {
	if d.err != nil {
		return -1
	}
	fd, err := d.next()
	if err != nil {
		d.err = err
		return -1
	}
	return fd
}

// header packs a message's second header word.
func header(size int, opcode uint16) uint32 { return uint32(size)<<16 | uint32(opcode) }

// frame returns the full wire message for an object's request.
func frame(obj uint32, opcode uint16, payload []byte) ([]byte, error) {
	size := headerSize + len(payload)
	if size > MaxMessage {
		return nil, fmt.Errorf("%w: %d", ErrTooLarge, size)
	}
	out := order.AppendUint32(nil, obj)
	out = order.AppendUint32(out, header(size, opcode))
	return append(out, payload...), nil
}
