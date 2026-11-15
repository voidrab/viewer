package x11

import (
	"fmt"
	"image"
	"math/bits"

	"golang.org/x/sys/unix"
)

// MIT-SHM minor opcodes.
const (
	shmQueryVersion = 0
	shmDetach       = 2
	shmPutImage     = 3
	shmAttachFd     = 6
)

// imageFormatZPixmap is PutImage's format of whole pixels.
const imageFormatZPixmap = 2

// pixelFormat is how a 32-bit pixel of a visual holds its colour: the shift
// of each 8-bit channel, and whether the server wants the bytes most
// significant first.
type pixelFormat struct {
	r, g, b, a uint // a is only used for a visual with alpha
	alpha      bool
	msb        bool
}

// newPixelFormat returns the layout of v's pixels, and false if any channel
// is not eight bits wide.
func newPixelFormat(v Visual, byteOrder byte) (pixelFormat, bool) {
	shift := func(mask uint32) (uint, bool) {
		s := uint(bits.TrailingZeros32(mask))
		return s, mask>>s == 0xff
	}
	r, ok1 := shift(v.RedMask)
	g, ok2 := shift(v.GreenMask)
	b, ok3 := shift(v.BlueMask)
	pf := pixelFormat{r: r, g: g, b: b, msb: byteOrder == 1}
	if v.Depth == 32 {
		// Alpha is the byte no colour channel takes.
		rest := ^(v.RedMask | v.GreenMask | v.BlueMask)
		a, ok := shift(rest)
		pf.a, pf.alpha = a, ok
		ok1 = ok1 && ok
	}
	return pf, ok1 && ok2 && ok3
}

// bgrx reports whether the format stores blue, green, red, then alpha or a
// spare byte, in memory order. For such a format, convert needs only to swap
// the red and blue bytes.
func (f pixelFormat) bgrx() bool {
	return !f.msb && f.r == 16 && f.g == 8 && f.b == 0 && (!f.alpha || f.a == 24)
}

// convert writes src, premultiplied RGBA, into dst in this format, stride
// bytes a row.
func (f pixelFormat) convert(dst []byte, stride int, src *image.RGBA) {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	if f.bgrx() {
		for y := range h {
			s := src.Pix[y*src.Stride:][:4*w]
			d := dst[y*stride:][:4*w]
			for i := 0; i < len(s); i += 4 {
				d[i], d[i+1], d[i+2], d[i+3] = s[i+2], s[i+1], s[i], s[i+3]
			}
		}
		return
	}
	for y := range h {
		s := src.Pix[y*src.Stride:][:4*w]
		d := dst[y*stride:][:4*w]
		for i := 0; i < len(s); i += 4 {
			p := uint32(s[i])<<f.r | uint32(s[i+1])<<f.g | uint32(s[i+2])<<f.b
			if f.alpha {
				p |= uint32(s[i+3]) << f.a
			}
			if f.msb {
				d[i], d[i+1], d[i+2], d[i+3] = byte(p>>24), byte(p>>16), byte(p>>8), byte(p)
			} else {
				d[i], d[i+1], d[i+2], d[i+3] = byte(p), byte(p>>8), byte(p>>16), byte(p>>24)
			}
		}
	}
}

// memfdCreate is replaced by tests to force a failure.
var memfdCreate = unix.MemfdCreate

// shmSeg is shared memory attached to the server as a segment.
type shmSeg struct {
	id   uint32
	data []byte
}

// newShmSeg creates size bytes of shared memory and attaches them to the
// server. The descriptor is closed once the server holds it.
func (b *Backend) newShmSeg(size int) (*shmSeg, error) {
	fd, err := memfdCreate("viewer-shm", unix.MFD_CLOEXEC)
	if err != nil {
		return nil, fmt.Errorf("x11: memfd_create: %w", err)
	}
	defer unix.Close(fd)
	if err := unix.Ftruncate(fd, int64(size)); err != nil {
		return nil, fmt.Errorf("x11: ftruncate: %w", err)
	}
	data, err := unix.Mmap(fd, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("x11: mmap: %w", err)
	}
	s := &shmSeg{id: b.c.NewID(), data: data}
	if err := b.c.Send(newReq(b.shm.major, shmAttachFd).u32(s.id).u8(0).pad(3).fd(fd)); err != nil {
		unix.Munmap(data)
		return nil, err
	}
	return s, nil
}

// free detaches the segment and unmaps it.
func (s *shmSeg) free(b *Backend) {
	b.c.Send(newReq(b.shm.major, shmDetach).u32(s.id))
	unix.Munmap(s.data)
	s.data = nil
}

// putImage draws pix, w by h pixels of 4 bytes, into drawable at (0, 0),
// split into strips that each fit in a request.
func (b *Backend) putImage(drawable, gc uint32, depth byte, w, h int, pix []byte) error {
	const header = 24
	rows := max(1, (b.c.Setup.MaxRequest-header)/(4*w))
	for y := 0; y < h; y += rows {
		n := min(rows, h-y)
		strip := pix[4*w*y : 4*w*(y+n)]
		r := &req{b: append(make([]byte, 0, header+len(strip)), opPutImage, imageFormatZPixmap, 0, 0)}
		r.u32(drawable).u32(gc).u16(uint16(w)).u16(uint16(n)).
			i16(0).i16(y).u8(0).u8(depth).pad(2).bytes(strip)
		if err := b.c.Send(r); err != nil {
			return err
		}
	}
	return nil
}
