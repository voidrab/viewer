package instance

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func socket(t *testing.T) string {
	t.Helper()
	// Unix socket paths are short; the test's own directory name can be long.
	dir, err := os.MkdirTemp("", "inst")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s.sock")
}

func recv(t *testing.T, s *Server) Request {
	t.Helper()
	select {
	case r := <-s.Requests():
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("no request arrived")
		return Request{}
	}
}

func TestFirstLaunchClaimsAndLaterOnesForward(t *testing.T) {
	path := socket(t)
	first, forwarded, err := Claim(path, Request{})
	if err != nil || forwarded || first == nil {
		t.Fatalf("first launch: %v, forwarded %v, err %v", first, forwarded, err)
	}
	defer first.Close()

	cwd, _ := os.Getwd()
	srv, forwarded, err := Claim(path, Request{Paths: []string{"a.pdf", "/abs/b.pdf"}, Token: "tok"})
	if err != nil || !forwarded || srv != nil {
		t.Fatalf("second launch: %v, forwarded %v, err %v", srv, forwarded, err)
	}
	got := recv(t, first)
	want := Request{Paths: []string{filepath.Join(cwd, "a.pdf"), "/abs/b.pdf"}, Token: "tok"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("request = %+v, want %+v", got, want)
	}
}

func TestAStaleSocketIsReplaced(t *testing.T) {
	path := socket(t)
	// A socket file left by an instance that died: nothing listens on it.
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the stale socket is gone: %v", err)
	}
	srv, forwarded, err := Claim(path, Request{})
	if err != nil || forwarded || srv == nil {
		t.Fatalf("claim over a stale socket: %v, forwarded %v, err %v", srv, forwarded, err)
	}
	defer srv.Close()
	if _, forwarded, _ := Claim(path, Request{Paths: []string{"/x.pdf"}}); !forwarded {
		t.Error("the new instance did not receive a forwarded request")
	}
	recv(t, srv)
}

func TestClosingRemovesTheSocket(t *testing.T) {
	path := socket(t)
	srv, _, err := Claim(path, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the socket is still there: %v", err)
	}
}

func TestClaimReportsErrors(t *testing.T) {
	// A directory that does not exist cannot hold the socket.
	if _, _, err := Claim(filepath.Join(t.TempDir(), "no", "such", "dir.sock"), Request{}); err == nil {
		t.Error("a socket in a missing directory was claimed")
	}
	// A path that is held by something that is not an instance, and cannot be
	// removed because its directory cannot be written, is an error too.
	dir := t.TempDir()
	path := filepath.Join(dir, "s.sock")
	ln, _ := net.Listen("unix", path)
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if os.Getuid() != 0 {
		if _, _, err := Claim(path, Request{}); err == nil {
			t.Error("a stale socket in a read-only directory was replaced")
		}
	}
}

func TestMalformedAndSilentClientsDoNotHarmTheServer(t *testing.T) {
	path := socket(t)
	srv, _, err := Claim(path, Request{})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	bad, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	bad.Write([]byte("not json\n"))
	bad.Close()
	// A good request still goes through afterwards.
	if _, forwarded, _ := Claim(path, Request{Paths: []string{"/ok.pdf"}}); !forwarded {
		t.Fatal("a good request was not accepted after a bad one")
	}
	if r := recv(t, srv); r.Paths[0] != "/ok.pdf" {
		t.Errorf("request = %+v", r)
	}
}

func TestSendFailsAgainstAnImpostor(t *testing.T) {
	path := socket(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			var req Request
			json.NewDecoder(c).Decode(&req)
			c.Write([]byte("what?\n"))
			c.Close()
		}
	}()
	if err := send(path, Request{}); err == nil {
		t.Error("an unexpected reply was taken for an acknowledgement")
	}
	// One that hangs up without a word fails too.
	ln2path := socket(t)
	ln2, _ := net.Listen("unix", ln2path)
	defer ln2.Close()
	go func() {
		c, _ := ln2.Accept()
		if c != nil {
			// Read the request first, so the client's write succeeds and
			// it is the missing reply that fails it.
			var req Request
			json.NewDecoder(c).Decode(&req)
			c.Close()
		}
	}()
	if err := send(ln2path, Request{}); err == nil {
		t.Error("a hang-up was taken for an acknowledgement")
	}
}

func TestDefaultPath(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	if got := DefaultPath("viewer"); got != "/run/user/1000/viewer.sock" {
		t.Errorf("path = %q", got)
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	if got := DefaultPath("viewer"); filepath.Dir(got) != os.TempDir() || filepath.Ext(got) != ".sock" {
		t.Errorf("fallback path = %q", got)
	}
}

func TestClaimFailsWithoutAWorkingDirectoryForRelativePaths(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "gone")
	if err := os.Mkdir(gone, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(gone)
	if err := os.Remove(gone); err != nil {
		t.Skip("cannot remove the working directory here")
	}
	if _, _, err := Claim(socket(t), Request{Paths: []string{"relative.pdf"}}); err == nil {
		t.Error("a relative path was made absolute without a working directory")
	}
}

func TestSendFailsWhenTheInstanceHangsUpMidRequest(t *testing.T) {
	path := socket(t)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	// More than the socket buffers hold, so the write fails once the peer is gone.
	huge := make([]byte, 8<<20)
	for i := range huge {
		huge[i] = 'x'
	}
	if err := send(path, Request{Paths: []string{string(huge)}}); err == nil {
		t.Error("a request to a closed connection was reported as sent")
	}
}
