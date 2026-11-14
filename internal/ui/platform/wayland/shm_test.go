package wayland

import (
	"image"
	"image/color"
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

func TestCopyARGBSwapsRedAndBlue(t *testing.T) {
	src := image.NewRGBA(image.Rect(5, 5, 7, 7)) // Bounds start at (5, 5).
	src.SetRGBA(5, 5, color.RGBA{1, 2, 3, 4})
	src.SetRGBA(6, 6, color.RGBA{10, 20, 30, 40})
	dst := make([]byte, 2*12) // stride wider than the image: 12 bytes per row
	copyARGB(dst, 12, src)
	if got := dst[0:4]; got[0] != 3 || got[1] != 2 || got[2] != 1 || got[3] != 4 {
		t.Errorf("first pixel = %v", got)
	}
	if got := dst[12+4 : 12+8]; got[0] != 30 || got[1] != 20 || got[2] != 10 || got[3] != 40 {
		t.Errorf("last pixel = %v", got)
	}
	if dst[8] != 0 { // padding is untouched
		t.Errorf("padding written: %v", dst[8:12])
	}
}
