package wayland

import (
	"fmt"
	"image"

	"golang.org/x/sys/unix"
)

// shmFormatARGB8888 is wl_shm's 32-bit premultiplied format. On little-endian
// hosts its bytes are B, G, R, A.
const shmFormatARGB8888 = 0

// memfdCreate is replaced by tests to force a failure.
var memfdCreate = unix.MemfdCreate

// shmMem is anonymous shared memory the compositor can map: a memfd and its
// mapping in this process.
type shmMem struct {
	fd   int
	data []byte
}

// newShmMem creates size bytes of shared memory.
func newShmMem(size int) (*shmMem, error) {
	fd, err := memfdCreate("viewer-shm", unix.MFD_CLOEXEC)
	if err != nil {
		return nil, fmt.Errorf("wayland: memfd_create: %w", err)
	}
	if err := unix.Ftruncate(fd, int64(size)); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("wayland: ftruncate: %w", err)
	}
	data, err := unix.Mmap(fd, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("wayland: mmap: %w", err)
	}
	return &shmMem{fd: fd, data: data}, nil
}

// free unmaps the memory and closes the descriptor.
func (m *shmMem) free() {
	unix.Munmap(m.data)
	unix.Close(m.fd)
	m.data, m.fd = nil, -1
}

// copyARGB converts src, premultiplied RGBA, into dst as ARGB8888, which only
// needs the red and blue bytes swapped. dst must hold the whole image and
// stride is its row length in bytes.
func copyARGB(dst []byte, stride int, src *image.RGBA) {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	for y := range h {
		s := src.Pix[y*src.Stride:][:4*w]
		d := dst[y*stride:][:4*w]
		for i := 0; i < len(s); i += 4 {
			d[i], d[i+1], d[i+2], d[i+3] = s[i+2], s[i+1], s[i], s[i+3]
		}
	}
}
