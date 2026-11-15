package wayland

import (
	"fmt"

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
	fd, err := memfdCreate("viewrab-shm", unix.MFD_CLOEXEC)
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
