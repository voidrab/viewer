package wayland

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestShmMemMapsSharedMemory(t *testing.T) {
	m, err := newShmMem(8192)
	if err != nil {
		t.Fatal(err)
	}
	m.data[0], m.data[8191] = 1, 2
	// Reading through the descriptor sees what the mapping wrote.
	buf := make([]byte, 1)
	if _, err := unix.Pread(m.fd, buf, 8191); err != nil || buf[0] != 2 {
		t.Errorf("shared read = %v, %v", buf, err)
	}
	m.free()
	if m.data != nil || m.fd != -1 {
		t.Errorf("free left %v %d", m.data, m.fd)
	}
}

func TestShmMemErrors(t *testing.T) {
	orig := memfdCreate
	memfdCreate = func(string, int) (int, error) { return -1, os.ErrPermission }
	_, err := newShmMem(4096)
	memfdCreate = orig
	if err == nil {
		t.Error("memfd_create failure ignored")
	}
	if _, err := newShmMem(-1); err == nil {
		t.Error("negative size accepted")
	}
	if _, err := newShmMem(0); err == nil { // mmap of zero bytes fails
		t.Error("zero size accepted")
	}
}
