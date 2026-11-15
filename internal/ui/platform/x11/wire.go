// Package x11 is a pure-Go X11 client: the core protocol over a unix or TCP
// socket, the MIT-SHM, SHAPE and RENDER extensions, and the window manager
// conventions (ICCCM, EWMH, XDND) a software-rendered window needs.
package x11

import (
	"encoding/binary"
	"errors"
)

// order is the byte order the client announces in its setup request, so
// every message in either direction uses it.
var order = binary.LittleEndian

// Errors reported by the wire layer.
var (
	ErrShortReply = errors.New("x11: reply too short for its fields")
	ErrTooLarge   = errors.New("x11: request exceeds the server's maximum length")
)

// pad4 returns n rounded up to a multiple of four.
func pad4(n int) int { return (n + 3) &^ 3 }

// req builds one request: the opcode, a data byte, a length the builder fills
// in, and the request's fields.
type req struct {
	b   []byte
	fds []int
}

// newReq starts a request with the given major opcode and data byte, which is
// the minor opcode of an extension's request.
func newReq(opcode, data byte) *req {
	return &req{b: append(make([]byte, 0, 32), opcode, data, 0, 0)}
}

func (r *req) u8(v byte) *req    { r.b = append(r.b, v); return r }
func (r *req) u16(v uint16) *req { r.b = order.AppendUint16(r.b, v); return r }
func (r *req) u32(v uint32) *req { r.b = order.AppendUint32(r.b, v); return r }
func (r *req) i16(v int) *req    { return r.u16(uint16(int16(v))) }

// pad appends n zero bytes.
func (r *req) pad(n int) *req { r.b = append(r.b, make([]byte, n)...); return r }

// bytes appends b, padded to a multiple of four bytes.
func (r *req) bytes(b []byte) *req {
	r.b = append(r.b, b...)
	return r.pad(pad4(len(b)) - len(b))
}

// fd passes a descriptor along with the request.
func (r *req) fd(fd int) *req { r.fds = append(r.fds, fd); return r }

// finish writes the length field and returns the request's bytes. The length
// counts four-byte units, so the request is padded first.
func (r *req) finish() []byte {
	r.pad(pad4(len(r.b)) - len(r.b))
	order.PutUint16(r.b[2:], uint16(len(r.b)/4))
	return r.b
}

// reader decodes the fields of a reply or event, recording the first read
// past its end.
type reader struct {
	b   []byte
	off int
	err error
}

func (d *reader) take(n int) []byte {
	if d.err != nil || d.off+n > len(d.b) {
		d.err = ErrShortReply
		return make([]byte, n)
	}
	v := d.b[d.off : d.off+n]
	d.off += n
	return v
}

func (d *reader) u8() byte    { return d.take(1)[0] }
func (d *reader) u16() uint16 { return order.Uint16(d.take(2)) }
func (d *reader) u32() uint32 { return order.Uint32(d.take(4)) }
func (d *reader) i16() int    { return int(int16(d.u16())) }
func (d *reader) skip(n int)  { d.take(n) }
func (d *reader) at(off int)  { d.off = off }
func (d *reader) str(n int) string {
	return string(d.take(n))
}

// u16At, u32At and i16At read a field at a fixed offset of a message known to
// be long enough, such as the 32 bytes of an event.
func u16At(b []byte, off int) uint16 { return order.Uint16(b[off:]) }
func u32At(b []byte, off int) uint32 { return order.Uint32(b[off:]) }
func i16At(b []byte, off int) int    { return int(int16(order.Uint16(b[off:]))) }
