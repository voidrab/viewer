package wayland

import (
	"errors"
	"image"
	"image/color"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"viewer/internal/ui/platform"
)

// fakeWM is a compositor that implements just enough of wl_compositor,
// wl_shm and xdg-shell to run a window, and logs each request it handles.
type fakeWM struct {
	t   *testing.T
	log chan string

	mu         sync.Mutex
	ids        map[string]uint32 // bound interface -> object id
	surface    uint32
	xdgSurface uint32
	toplevel   uint32
	title      string
	appID      string
	minSize    [2]int32
	acked      uint32
	pongs      []uint32
	pools      int
	poolMem    map[uint32][]byte // pool id -> its mapping
	bufMem     map[uint32][]byte // buffer id -> pool mapping
	bufSize    map[uint32][3]int32
	attached   uint32
	destroyed  []string

	// afterBind, if set, runs after the compositor handles a bind.
	afterBind func(s *server, iface string, id uint32)
}

var wmGlobals = []Global{
	{Name: 1, Interface: "wl_compositor", Version: 4},
	{Name: 2, Interface: "wl_shm", Version: 1},
	{Name: 3, Interface: "xdg_wm_base", Version: 2},
}

func (f *fakeWM) handler(globals []Global) func(*server, Event, *Decoder) {
	return compositor(globals, func(s *server, g Global, version, id uint32) {
		f.mu.Lock()
		f.ids[g.Interface] = id
		f.mu.Unlock()
		f.log <- "bind " + g.Interface
		if f.afterBind != nil {
			f.afterBind(s, g.Interface, id)
		}
	}, f.request)
}

func (f *fakeWM) request(s *server, ev Event, d *Decoder) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch ev.Object {
	case f.ids["wl_compositor"]:
		f.surface = d.Uint()
		f.log <- "create_surface"
	case f.ids["xdg_wm_base"]:
		switch ev.Opcode {
		case wmBaseGetXdgSurface:
			f.xdgSurface = d.Uint()
		case wmBasePong:
			f.pongs = append(f.pongs, d.Uint())
			f.log <- "pong"
		}
	case f.xdgSurface:
		switch ev.Opcode {
		case xdgSurfaceGetToplevel:
			f.toplevel = d.Uint()
		case xdgSurfaceAckConfigure:
			f.acked = d.Uint()
			f.log <- "ack"
		case xdgSurfaceDestroy:
			f.destroyed = append(f.destroyed, "xdg_surface")
		}
	case f.toplevel:
		switch ev.Opcode {
		case toplevelSetTitle:
			f.title = d.String()
			f.log <- "title"
		case toplevelSetAppID:
			f.appID = d.String()
		case toplevelSetMinSize:
			f.minSize = [2]int32{d.Int(), d.Int()}
		case toplevelDestroy:
			f.destroyed = append(f.destroyed, "toplevel")
		}
	case f.ids["wl_shm"]:
		pool, fd, size := d.Uint(), d.FD(), int(d.Int())
		mem, err := unix.Mmap(fd, 0, size, unix.PROT_READ, unix.MAP_SHARED)
		if err != nil {
			f.t.Errorf("fake compositor mmap: %v", err)
		}
		unix.Close(fd)
		f.pools++
		f.poolMem[pool] = mem
		f.log <- "create_pool"
	case f.surface:
		switch ev.Opcode {
		case surfaceAttach:
			f.attached = d.Uint()
		case surfaceCommit:
			f.log <- "commit"
		case surfaceDestroy:
			f.destroyed = append(f.destroyed, "surface")
		}
	default:
		if mem, ok := f.poolMem[ev.Object]; ok && ev.Opcode == poolCreateBuffer {
			id, _, w, h, stride := d.Uint(), d.Int(), d.Int(), d.Int(), d.Int()
			f.bufMem[id], f.bufSize[id] = mem, [3]int32{w, h, stride}
			f.log <- "create_buffer"
		} else if _, ok := f.bufMem[ev.Object]; ok && ev.Opcode == bufferDestroy {
			f.destroyed = append(f.destroyed, "buffer")
			f.log <- "destroy_buffer"
		}
	}
}

// configure sends the toplevel and xdg_surface configure pair.
func (f *fakeWM) configure(s *server, w, h int32, serial uint32) {
	f.mu.Lock()
	top, xdg := f.toplevel, f.xdgSurface
	f.mu.Unlock()
	s.send(top, toplevelConfigure, func(e *Encoder) { e.Int(w).Int(h).Array(nil) })
	s.send(xdg, xdgSurfaceConfigure, func(e *Encoder) { e.Uint(serial) })
}

// wait blocks until the compositor logged want, skipping other entries.
func (f *fakeWM) wait(want string) {
	f.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case got := <-f.log:
			if got == want {
				return
			}
		case <-timeout:
			f.t.Fatalf("timed out waiting for %q", want)
		}
	}
}

// wmSetup opens a backend against a fakeWM.
func wmSetup(t *testing.T, globals []Global) (*Backend, *fakeWM, *server) {
	t.Helper()
	a, b := pair(t)
	f := &fakeWM{t: t, log: make(chan string, 256), ids: map[string]uint32{},
		poolMem: map[uint32][]byte{}, bufMem: map[uint32][]byte{}, bufSize: map[uint32][3]int32{}}
	srv := serve(t, b, f.handler(globals))
	be, err := Open(a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { be.Close() })
	return be, f, srv
}

func next(t *testing.T, w platform.Window) platform.Event {
	t.Helper()
	select {
	case ev := <-w.Events():
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a window event")
		return nil
	}
}

// openWindow opens and configures a window of the given size.
func openWindow(t *testing.T, w, h int32) (*Backend, *fakeWM, *server, platform.Window) {
	be, f, srv := wmSetup(t, wmGlobals)
	win, err := be.NewWindow(platform.WindowOptions{
		Title: "t", AppID: "app.id", Size: platform.Size{W: 100, H: 100}, MinSize: platform.Size{W: 20, H: 10},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.wait("commit") // the initial commit
	f.configure(srv, w, h, 5)
	if ev := next(t, win); ev != (platform.ResizeEvent{Size: platform.Size{W: int(w), H: int(h)}, Scale: 1}) {
		t.Fatalf("first event = %#v", ev)
	}
	if ev := next(t, win); ev != (platform.FrameEvent{}) {
		t.Fatalf("second event = %#v", ev)
	}
	return be, f, srv, win
}

func TestWindowConfigureAndProperties(t *testing.T) {
	_, f, _, _ := openWindow(t, 300, 200)
	f.wait("ack")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.acked != 5 || f.title != "t" || f.appID != "app.id" || f.minSize != [2]int32{20, 10} {
		t.Errorf("compositor saw ack %d title %q app %q min %v", f.acked, f.title, f.appID, f.minSize)
	}
}

func TestPresentCopiesPixelsAndReusesBuffers(t *testing.T) {
	_, f, srv, win := openWindow(t, 4, 2)
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	img.SetRGBA(0, 0, color.RGBA{1, 2, 3, 255})
	img.SetRGBA(3, 1, color.RGBA{10, 20, 30, 255})
	win.Present(img)
	f.wait("create_pool")
	f.wait("create_buffer")
	f.wait("commit")

	f.mu.Lock()
	if f.pools != 1 || f.attached == 0 {
		t.Fatalf("pools %d attached %d", f.pools, f.attached)
	}
	buf := f.attached
	mem, size := f.bufMem[buf], f.bufSize[buf]
	f.mu.Unlock()
	if size != [3]int32{4, 2, 16} {
		t.Errorf("buffer geometry = %v", size)
	}
	if got := mem[0:4]; got[0] != 3 || got[1] != 2 || got[2] != 1 || got[3] != 255 {
		t.Errorf("first pixel = %v", got)
	}
	if got := mem[16+12 : 16+16]; got[0] != 30 || got[1] != 20 || got[2] != 10 {
		t.Errorf("last pixel = %v", got)
	}

	// While the compositor holds the buffer a second frame needs another one.
	win.Present(img)
	f.wait("create_buffer")
	f.wait("commit")
	// After a release, the buffer is reused instead of a new pool.
	srv.send(buf, bufferRelease, nil)
	time.Sleep(50 * time.Millisecond) // let the dispatcher process the release
	win.Present(img)
	f.wait("commit")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.attached != buf {
		t.Errorf("attached %d, want the released buffer %d", f.attached, buf)
	}
}

func TestPresentIgnoresWrongSizeAndUnconfigured(t *testing.T) {
	be, f, _ := wmSetup(t, wmGlobals)
	win, _ := be.NewWindow(platform.WindowOptions{Size: platform.Size{W: 4, H: 2}})
	f.wait("commit")
	win.Present(image.NewRGBA(image.Rect(0, 0, 4, 2))) // not yet configured
	_, f2, _, win2 := openWindow(t, 4, 2)
	win2.Present(image.NewRGBA(image.Rect(0, 0, 9, 9))) // wrong size
	time.Sleep(50 * time.Millisecond)
	f.mu.Lock()
	f2.mu.Lock()
	defer f.mu.Unlock()
	defer f2.mu.Unlock()
	if f.pools != 0 || f2.pools != 0 {
		t.Errorf("a rejected frame allocated memory: %d %d", f.pools, f2.pools)
	}
}

func TestResizeRetiresOldBuffers(t *testing.T) {
	_, f, srv, win := openWindow(t, 4, 2)
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	win.Present(img)
	f.wait("create_buffer")
	f.wait("commit")
	f.mu.Lock()
	buf := f.attached
	f.mu.Unlock()

	// A zero-size configure keeps the size; a real one resizes.
	f.configure(srv, 0, 0, 6)
	if ev := next(t, win); ev != (platform.ResizeEvent{Size: platform.Size{W: 4, H: 2}, Scale: 1}) {
		t.Errorf("zero configure = %#v", ev)
	}
	next(t, win)
	f.configure(srv, 8, 6, 7)
	if ev := next(t, win); ev != (platform.ResizeEvent{Size: platform.Size{W: 8, H: 6}, Scale: 1}) {
		t.Errorf("resize = %#v", ev)
	}
	next(t, win)
	// The old buffer is busy, so it survives until the compositor releases it.
	srv.send(buf, bufferRelease, nil)
	f.wait("destroy_buffer")
	win.Present(image.NewRGBA(image.Rect(0, 0, 8, 6)))
	f.wait("create_buffer")
}

func TestCloseRequestPingTitleInvalidate(t *testing.T) {
	_, f, srv, win := openWindow(t, 4, 2)
	f.mu.Lock()
	top, wm := f.toplevel, f.ids["xdg_wm_base"]
	f.mu.Unlock()

	srv.send(wm, wmBasePing, func(e *Encoder) { e.Uint(77) })
	f.wait("pong")
	f.mu.Lock()
	if len(f.pongs) != 1 || f.pongs[0] != 77 {
		t.Errorf("pongs = %v", f.pongs)
	}
	f.mu.Unlock()

	srv.send(top, toplevelClose, nil)
	if ev := next(t, win); ev != (platform.CloseEvent{}) {
		t.Errorf("close request = %#v", ev)
	}
	win.SetTitle("renamed")
	f.wait("title")
	f.mu.Lock()
	if f.title != "renamed" {
		t.Errorf("title = %q", f.title)
	}
	f.mu.Unlock()
	win.Invalidate()
	if ev := next(t, win); ev != (platform.FrameEvent{}) {
		t.Errorf("Invalidate = %#v", ev)
	}

	win.Close()
	win.Close()
	if _, ok := <-win.Events(); ok {
		t.Error("Events still open after Close")
	}
	win.Invalidate() // no panic on a closed window
	win.Present(image.NewRGBA(image.Rect(0, 0, 4, 2)))
	// The compositor sees the three objects destroyed, children first.
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mu.Lock()
		got := append([]string(nil), f.destroyed...)
		f.mu.Unlock()
		if len(got) == 3 {
			if got[0] != "toplevel" || got[1] != "xdg_surface" || got[2] != "surface" {
				t.Errorf("destroyed = %v", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("destroyed = %v", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestOpenRequiresEveryGlobal(t *testing.T) {
	for skip := range wmGlobals {
		a, b := pair(t)
		var some []Global
		for i, g := range wmGlobals {
			if i != skip {
				some = append(some, g)
			}
		}
		f := &fakeWM{t: t, log: make(chan string, 64), ids: map[string]uint32{}}
		serve(t, b, f.handler(some))
		if _, err := Open(a); !errors.Is(err, ErrNoGlobal) {
			t.Errorf("without %s: Open = %v", wmGlobals[skip].Interface, err)
		}
	}
	a, b := pair(t)
	b.Close()
	if _, err := Open(a); err == nil {
		t.Error("Open on a dead connection succeeded")
	}
}

func TestNewConnectsThroughTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("WAYLAND_DISPLAY", "wayland-test")
	if _, err := New(); err == nil {
		t.Error("New without a compositor succeeded")
	}
	l, err := net.Listen("unix", dir+"/wayland-test")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	// First a compositor that hangs up at once, then a working one.
	go func() {
		c, _ := l.Accept()
		c.Close()
	}()
	if _, err := New(); err == nil {
		t.Error("New against a compositor that hangs up succeeded")
	}
	f := &fakeWM{t: t, log: make(chan string, 256), ids: map[string]uint32{}}
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		serve(t, NewConn(c.(*net.UnixConn)), f.handler(wmGlobals))
	}()
	be, err := New()
	if err != nil {
		t.Fatal(err)
	}
	be.Close()
}

func TestNewWindowFailsOnDeadConnection(t *testing.T) {
	be, _, _ := wmSetup(t, wmGlobals)
	be.c.conn.sock.Close()
	if _, err := be.NewWindow(platform.WindowOptions{}); err == nil {
		t.Error("NewWindow on a closed socket succeeded")
	}
}

func TestPresentSurvivesAllocationFailure(t *testing.T) {
	_, f, _, win := openWindow(t, 4, 2)
	orig := memfdCreate
	memfdCreate = func(string, int) (int, error) { return -1, os.ErrPermission }
	win.Present(image.NewRGBA(image.Rect(0, 0, 4, 2)))
	memfdCreate = orig
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pools != 0 {
		t.Error("a pool was created despite the allocation failure")
	}
}

func TestBackendCloseEndsWindowsAndIgnoresUnknownEvents(t *testing.T) {
	be, f, srv, win := openWindow(t, 4, 2)
	f.mu.Lock()
	xdg, top, wm := f.xdgSurface, f.toplevel, f.ids["xdg_wm_base"]
	f.mu.Unlock()
	srv.send(xdg, 9, nil) // an xdg_surface event this client does not know
	srv.send(wm, 9, nil)
	// Events are dispatched in the order they arrive, so receiving the close
	// event means the unknown events sent before it have been dispatched.
	srv.send(top, toplevelClose, nil)
	if ev := next(t, win); ev != (platform.CloseEvent{}) {
		t.Fatalf("event = %#v", ev)
	}
	if err := be.Close(); err != nil {
		t.Fatal(err)
	}
	for {
		if _, ok := <-win.Events(); !ok {
			break
		}
	}
	win.Close() // already ended by the backend
}

func TestEventQueueDropsWhenFull(t *testing.T) {
	_, _, _, win := openWindow(t, 4, 2)
	for range eventQueue + 10 {
		win.Invalidate() // never drained: must not block
	}
}

func TestCloseDestroysBuffers(t *testing.T) {
	_, f, _, win := openWindow(t, 4, 2)
	win.Present(image.NewRGBA(image.Rect(0, 0, 4, 2)))
	f.wait("create_buffer")
	win.Close()
	f.wait("destroy_buffer")
}

// TestShutdownIsIdempotent verifies that calling shutdown twice on a window
// neither panics nor closes the Events channel twice.
func TestShutdownIsIdempotent(t *testing.T) {
	_, _, _, win := openWindow(t, 4, 2)
	w := win.(*window)
	w.shutdown()
	w.shutdown()
}
