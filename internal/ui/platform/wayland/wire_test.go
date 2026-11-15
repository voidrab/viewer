//go:build !windows

package wayland

import (
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

// pair returns the two ends of a connected socket as Conns.
func pair(t *testing.T) (a, b *Conn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(fd int) *Conn {
		f := os.NewFile(uintptr(fd), "sock")
		defer f.Close()
		c, err := net.FileConn(f)
		if err != nil {
			t.Fatal(err)
		}
		return NewConn(c.(*net.UnixConn))
	}
	a, b = mk(fds[0]), mk(fds[1])
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}

func TestRoundTrip(t *testing.T) {
	a, b := pair(t)
	err := a.Send(7, 3, func(e *Encoder) {
		e.Uint(42).Int(-5).Fixed(1.5).String("héllo").String("").Array([]byte{1, 2, 3})
	})
	if err != nil {
		t.Fatal(err)
	}
	ev, err := b.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if ev.Object != 7 || ev.Opcode != 3 {
		t.Fatalf("event = %+v", ev)
	}
	d := b.Decode(ev)
	if d.Uint() != 42 || d.Int() != -5 || d.Fixed() != 1.5 || d.String() != "héllo" || d.String() != "" {
		t.Error("scalar or string mismatch")
	}
	if got := d.Array(); string(got) != "\x01\x02\x03" {
		t.Errorf("Array = %v", got)
	}
	if d.Err() != nil {
		t.Errorf("Err = %v", d.Err())
	}
	// Reading past the end is a sticky error and yields zero values.
	if d.Uint() != 0 || !errors.Is(d.Err(), ErrShortMessage) {
		t.Errorf("overrun: %v", d.Err())
	}
	if d.String() != "" || d.Array() != nil && len(d.Array()) != 0 || d.FD() != -1 {
		t.Error("reads after an error returned data")
	}
}

func TestSendNilBuilderAndSplitMessages(t *testing.T) {
	a, b := pair(t)
	if err := a.Send(1, 0, nil); err != nil {
		t.Fatal(err)
	}
	// Two messages written back to back are framed separately.
	a.Send(2, 1, func(e *Encoder) { e.Uint(9) })
	a.Send(3, 2, nil)
	for _, want := range []Event{{Object: 1}, {Object: 2, Opcode: 1}, {Object: 3, Opcode: 2}} {
		ev, err := b.Recv()
		if err != nil || ev.Object != want.Object || ev.Opcode != want.Opcode {
			t.Fatalf("Recv = %+v, %v; want %+v", ev, err, want)
		}
	}
}

func TestFileDescriptorPassing(t *testing.T) {
	a, b := pair(t)
	f, err := os.CreateTemp(t.TempDir(), "fd")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	f.WriteString("shm")
	if err := a.Send(5, 0, func(e *Encoder) { e.FD(int(f.Fd())).Uint(11) }); err != nil {
		t.Fatal(err)
	}
	ev, _ := b.Recv()
	d := b.Decode(ev)
	fd := d.FD()
	got := os.NewFile(uintptr(fd), "recv")
	defer got.Close()
	buf := make([]byte, 3)
	if _, err := got.ReadAt(buf, 0); err != nil || string(buf) != "shm" {
		t.Errorf("received fd content = %q, %v", buf, err)
	}
	if d.Uint() != 11 || d.Err() != nil {
		t.Errorf("payload after fd: %v", d.Err())
	}
	// No second descriptor was sent.
	if d.FD() != -1 || d.Err() == nil {
		t.Error("FD without a descriptor succeeded")
	}
}

func TestClose(t *testing.T) {
	a, b := pair(t)
	f, _ := os.Open(os.DevNull)
	a.Send(1, 0, func(e *Encoder) { e.FD(int(f.Fd())) })
	f.Close()
	if _, err := b.Recv(); err != nil { // leaves the descriptor unclaimed
		t.Fatal(err)
	}
	b.Close()
	if len(b.fds) != 0 {
		t.Error("Close kept unclaimed descriptors")
	}
	a.Close()
}

func TestRecvEOF(t *testing.T) {
	a, b := pair(t)
	a.Close()
	if _, err := b.Recv(); err != io.EOF {
		t.Errorf("Recv on a closed peer = %v", err)
	}
}

func TestRecvTruncated(t *testing.T) {
	a, b := pair(t)
	// A header promising 16 bytes, then the peer vanishes.
	a.sock.Write(append(order.AppendUint32(nil, 1), order.AppendUint32(nil, header(16, 0))...))
	a.Close()
	if _, err := b.Recv(); err != io.ErrUnexpectedEOF {
		t.Errorf("Recv = %v", err)
	}
}

func TestRecvBadHeader(t *testing.T) {
	a, b := pair(t)
	a.sock.Write(append(order.AppendUint32(nil, 1), order.AppendUint32(nil, header(4, 0))...))
	if _, err := b.Recv(); !errors.Is(err, ErrBadMessage) {
		t.Errorf("Recv = %v", err)
	}
}

// TestMalformedControlMessages verifies that takeFDs neither panics on nor
// queues descriptors from control data the kernel would never produce.
func TestMalformedControlMessages(t *testing.T) {
	_, b := pair(t)
	oob := make([]byte, syscall.CmsgSpace(4))
	h := (*syscall.Cmsghdr)(unsafe.Pointer(&oob[0]))
	h.Level, h.Type = syscall.SOL_SOCKET, syscall.SCM_RIGHTS

	h.Len = 1 // shorter than its own header
	b.takeFDs(oob)
	h.SetLen(syscall.CmsgLen(3)) // not a whole number of descriptors
	b.takeFDs(oob)
	h.Type = syscall.SCM_CREDENTIALS // some other control message
	b.takeFDs(oob)
	if len(b.fds) != 0 {
		t.Errorf("queued descriptors from malformed data: %v", b.fds)
	}
}

func TestSendTooLarge(t *testing.T) {
	a, _ := pair(t)
	err := a.Send(1, 0, func(e *Encoder) { e.Array(make([]byte, MaxMessage)) })
	if !errors.Is(err, ErrTooLarge) {
		t.Errorf("Send = %v", err)
	}
}

func TestSendAfterClose(t *testing.T) {
	a, _ := pair(t)
	a.Close()
	if err := a.Send(1, 0, nil); err == nil {
		t.Error("Send on a closed connection succeeded")
	}
}

func TestStringBoundsAndPadding(t *testing.T) {
	var e Encoder
	e.String("abc") // 4 bytes with NUL, no padding needed
	e.String("abcd")
	if len(e.buf)%4 != 0 {
		t.Fatalf("payload not word aligned: %d", len(e.buf))
	}
	d := Decoder{buf: e.buf}
	if d.String() != "abc" || d.String() != "abcd" || d.Err() != nil {
		t.Errorf("strings: %v", d.Err())
	}
	// A length that overruns the payload is an error, not a panic.
	bad := Decoder{buf: order.AppendUint32(nil, 1<<30)}
	if bad.String() != "" || bad.Err() == nil {
		t.Error("oversized string accepted")
	}
	huge := Decoder{buf: order.AppendUint32(nil, 0xFFFFFFFF)}
	if huge.Array() != nil && len(huge.Array()) != 0 || huge.Err() == nil {
		t.Error("negative length accepted")
	}
}

func TestDialEnvironment(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	if _, err := Dial(); err == nil || !strings.Contains(err.Error(), "XDG_RUNTIME_DIR") {
		t.Errorf("Dial without a runtime dir = %v", err)
	}
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	if _, err := Dial(); err == nil { // wayland-0 does not exist there
		t.Error("Dial without a compositor succeeded")
	}
	// An absolute socket path is used as is, and a listener answers it.
	path := dir + "/sock"
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	t.Setenv("WAYLAND_DISPLAY", path)
	c, err := Dial()
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}
