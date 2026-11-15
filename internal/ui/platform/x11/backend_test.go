package x11

import (
	"errors"
	"image"
	"image/color"
	"net"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"viewer/internal/ui/platform"
)

// withWM makes the fake server look managed by a window manager that
// supports the hints named.
func withWM(hints ...string) func(*fakeX) {
	return func(f *fakeX) {
		check := f.atom("_NET_SUPPORTING_WM_CHECK")
		f.setProp(fakeRoot, check, atomWindow, 32, propU32(0x777))
		f.setProp(0x777, check, atomWindow, 32, propU32(0x777))
		var atoms []uint32
		for _, h := range hints {
			atoms = append(atoms, f.atom(h))
		}
		f.setProp(fakeRoot, f.atom("_NET_SUPPORTED"), atomAtom, 32, propU32(atoms...))
	}
}

// atomWindow is the predefined WINDOW atom, which the fake window manager's
// check property has.
const atomWindow = 33

func TestOpenReadsTheServer(t *testing.T) {
	f, b := startBackend(t, withWM("_NET_WM_MOVERESIZE"))
	if b.visual != 0x21 || b.depth != 24 || b.colormap != 0x20 || !b.pixels.bgrx() {
		t.Errorf("visual %#x depth %d colormap %#x", b.visual, b.depth, b.colormap)
	}
	if b.argb != 0x22 || b.argbColormap == 0 || !b.argbPixels.alpha {
		t.Errorf("argb %#x", b.argb)
	}
	if !b.shm.present || !b.shape.present || !b.render.present || b.argbPict != argbFormat.id {
		t.Errorf("extensions shm %v shape %v render %v pict %#x", b.shm.present, b.shape.present, b.render.present, b.argbPict)
	}
	if b.noWM || !b.supports("_NET_WM_MOVERESIZE") || b.supports("_GTK_SHOW_WINDOW_MENU") || !b.clientDecorated() {
		t.Errorf("wm %v %v", b.noWM, b.wm)
	}
	if len(f.requests(opCreateWindow)) != 1 { // the selection window
		t.Errorf("windows made: %d", len(f.requests(opCreateWindow)))
	}
}

func TestDecoratedByAWindowManagerThatCannotMoveOnRequest(t *testing.T) {
	f, b := startBackend(t, withWM())
	if b.noWM || b.clientDecorated() {
		t.Fatalf("noWM %v, client decorated %v", b.noWM, b.clientDecorated())
	}
	w := openWindowState(t, f, b)
	if !w.decorated {
		t.Error("window not decorated")
	}
	if _, ok := f.prop(w.id, f.atom("_MOTIF_WM_HINTS")); ok {
		t.Error("decorations turned off")
	}
}

// openWindowState opens a window and takes its StateEvent, ResizeEvent and
// FrameEvent.
func openWindowState(t *testing.T, f *fakeX, b *Backend) *window {
	t.Helper()
	pw, err := b.NewWindow(platform.WindowOptions{Size: platform.Size{W: 40, H: 30}})
	if err != nil {
		t.Fatal(err)
	}
	w := pw.(*window)
	t.Cleanup(w.Close)
	f.event(mapEv(w.id))
	if s := want[platform.StateEvent](t, w); s.Decorated != w.decorated {
		t.Errorf("state = %+v", s)
	}
	want[platform.ResizeEvent](t, w)
	want[platform.FrameEvent](t, w)
	return w
}

func TestWindowManagerCheck(t *testing.T) {
	// A root check property naming a window that does not name itself is
	// left over from a manager that has gone.
	_, b := startBackend(t, func(f *fakeX) {
		check := f.atom("_NET_SUPPORTING_WM_CHECK")
		f.setProp(fakeRoot, check, atomWindow, 32, propU32(0x777))
		f.setProp(0x777, check, atomWindow, 32, propU32(0x778))
	})
	if !b.noWM {
		t.Error("stale check window taken for a manager")
	}
	_, b = startBackend(t, nil)
	if !b.noWM || !b.clientDecorated() {
		t.Error("no manager not noticed")
	}
}

func TestOpenPicksAnotherVisualWhenTheRootsWillNotDo(t *testing.T) {
	fx, sock := newFakeX(t, func() setupOpts {
		o := defaultSetup()
		o.rootVisual, o.rootDepth = 0x23, 16
		o.visuals = append([]Visual{{ID: 0x23, Class: visualTrueColor, Depth: 16, RedMask: 0xf800, GreenMask: 0x7e0, BlueMask: 0x1f}}, o.visuals...)
		o.formats = append(o.formats, Format{Depth: 16, BitsPerPixel: 16})
		return o
	}())
	c, err := NewConn(sock, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.visual != 0x21 || b.colormap == 0x20 {
		t.Errorf("visual %#x colormap %#x", b.visual, b.colormap)
	}
	if len(fx.requests(opCreateColormap)) != 2 { // the window's and the icon's
		t.Errorf("colormaps made: %d", len(fx.requests(opCreateColormap)))
	}
}

func TestOpenFailures(t *testing.T) {
	open := func(o setupOpts, f func(*fakeX)) error {
		fx, sock := newFakeX(t, o)
		if f != nil {
			f(fx)
		}
		c, err := NewConn(sock, "", nil)
		if err != nil {
			return err
		}
		b, err := Open(c)
		if err == nil {
			b.Close()
		} else {
			c.Close()
		}
		return err
	}
	noVisual := defaultSetup()
	noVisual.visuals = []Visual{{ID: 0x21, Class: 3, Depth: 24}} // PseudoColor
	if err := open(noVisual, nil); !errors.Is(err, ErrVisual) {
		t.Errorf("no TrueColor: %v", err)
	}
	oddMasks := defaultSetup()
	oddMasks.visuals = []Visual{{ID: 0x21, Class: visualTrueColor, Depth: 24, RedMask: 0x3ff00000, GreenMask: 0xffc00, BlueMask: 0x3ff}}
	if err := open(oddMasks, nil); !errors.Is(err, ErrVisual) {
		t.Errorf("10-bit channels: %v", err)
	}
	failOn := func(op byte, minor int) func(*fakeX) {
		return func(f *fakeX) {
			f.handle = func(f *fakeX, r request) bool {
				if r.op == op && (minor < 0 || int(r.data) == minor) {
					f.fail(r, 8, 0)
					return true
				}
				return false
			}
		}
	}
	for _, tc := range []struct {
		name string
		f    func(*fakeX)
	}{
		{"atoms", failOn(opInternAtom, -1)},
		{"extensions", failOn(opQueryExtension, -1)},
		{"keyboard", failOn(opGetKeyboardMapping, -1)},
		{"modifiers", failOn(opGetModifierMapping, -1)},
	} {
		if err := open(defaultSetup(), tc.f); err == nil {
			t.Errorf("%s: Open succeeded", tc.name)
		}
	}
	// Sends that fail: on a connection already closed, the colormap made for
	// a visual other than the root's, or for the icon's; and a selection
	// window too large for the server's requests.
	closedFirst := func(o setupOpts) error {
		_, sock := newFakeX(t, o)
		c, err := NewConn(sock, "", nil)
		if err != nil {
			return err
		}
		sock.Close()
		<-c.Done()
		_, err = Open(c)
		return err
	}
	other := defaultSetup()
	other.rootVisual = 0x99
	for _, o := range []setupOpts{other, defaultSetup()} {
		if err := closedFirst(o); err == nil {
			t.Error("Open on a closed connection succeeded")
		}
	}
	small := defaultSetup()
	small.maxReq = 9 // 36 bytes: every request but CreateWindow fits
	if err := open(small, nil); !errors.Is(err, ErrTooLarge) {
		t.Errorf("selection window: %v", err)
	}
}

func TestExtensionsTheBackendCannotUse(t *testing.T) {
	// MIT-SHM below 1.2, RENDER without a 32-bit ARGB format, and requests
	// for their versions refused.
	_, b := startBackend(t, func(f *fakeX) {
		f.shmMinor = 1
		f.formats = []renderFormat{{id: 1, depth: 24}, {id: 2, depth: 32, shifts: [4]uint16{0, 8, 16, 24}, masks: [4]uint16{0xff, 0xff, 0xff, 0xff}}}
	})
	if b.shm.present || b.render.present {
		t.Errorf("shm %v render %v", b.shm.present, b.render.present)
	}
	_, b = startBackend(t, func(f *fakeX) {
		f.handle = func(f *fakeX, r request) bool {
			if r.op == 130 || r.op == 139 { // MIT-SHM and RENDER
				f.fail(r, 1, 0)
				return true
			}
			return false
		}
	})
	if b.shm.present || b.render.present {
		t.Errorf("refused versions: shm %v render %v", b.shm.present, b.render.present)
	}
	// RENDER refusing only the format list.
	_, b = startBackend(t, func(f *fakeX) {
		f.handle = func(f *fakeX, r request) bool {
			if r.op == 139 && r.data == renderQueryPictFormats {
				f.fail(r, 1, 0)
				return true
			}
			return false
		}
	})
	if b.render.present {
		t.Error("render without formats")
	}
	// A TCP connection cannot pass descriptors.
	_, b = startBackend(t, func(f *fakeX) {})
	if !b.shm.present {
		t.Fatal("shm missing")
	}
}

func TestXKBDetectableRepeat(t *testing.T) {
	f, _ := startBackend(t, func(f *fakeX) {
		f.exts["XKEYBOARD"] = extension{present: true, major: 135}
		f.handle = func(f *fakeX, r request) bool {
			if r.op == 135 {
				if r.data == xkbUseExtension {
					f.reply(r, 1, nil)
				} else {
					f.reply(r, 0, nil)
				}
				return true
			}
			return false
		}
	})
	var minors []byte
	for _, r := range f.requests(135) {
		minors = append(minors, r.data)
	}
	if !slices.Equal(minors, []byte{xkbUseExtension, xkbPerClientFlags}) {
		t.Errorf("XKB requests %v", minors)
	}
	// A server whose XKB refuses the client's version.
	f, _ = startBackend(t, func(f *fakeX) {
		f.exts["XKEYBOARD"] = extension{present: true, major: 135}
		f.handle = func(f *fakeX, r request) bool {
			if r.op == 135 {
				f.reply(r, 0, nil)
				return true
			}
			return false
		}
	})
	if n := len(f.requests(135)); n != 1 {
		t.Errorf("XKB requests after refusal: %d", n)
	}
}

func TestNewWindowSetsPropertiesAndMaps(t *testing.T) {
	override(t, &hostname, func() (string, error) { return "box", nil })
	t.Setenv("DESKTOP_STARTUP_ID", "launch_TIME42")
	f, b := startBackend(t, nil)
	icon := image.NewRGBA(image.Rect(0, 0, 2, 1))
	icon.Set(1, 0, color.NRGBA{R: 255, A: 128})
	w := openWindow(t, f, b, platform.WindowOptions{Title: "Tést ✓", AppID: "app.id", Size: platform.Size{W: 50, H: 40}, MinSize: platform.Size{W: 20, H: 10}, Icon: icon})
	str := func(name string, atom uint32) string {
		if atom == 0 {
			atom = f.atom(name)
		}
		p, _ := f.prop(w.id, atom)
		return string(p.value)
	}
	if got := str("", atomWMName); got != "T\xe9st ?" {
		t.Errorf("WM_NAME = %q", got)
	}
	if got := str("_NET_WM_NAME", 0); got != "Tést ✓" {
		t.Errorf("_NET_WM_NAME = %q", got)
	}
	if got := str("", atomWMClass); got != "app.id\x00app.id\x00" {
		t.Errorf("WM_CLASS = %q", got)
	}
	if got := str("WM_CLIENT_MACHINE", 0); got != "box" {
		t.Errorf("WM_CLIENT_MACHINE = %q", got)
	}
	if got := str("_NET_STARTUP_ID", 0); got != "launch_TIME42" {
		t.Errorf("_NET_STARTUP_ID = %q", got)
	}
	hints, _ := f.prop(w.id, atomWMNormal)
	if v := hints.u32s(); len(v) != 18 || v[0] != 1<<4 || v[5] != 20 || v[6] != 10 {
		t.Errorf("WM_NORMAL_HINTS = %v", v)
	}
	ic, _ := f.prop(w.id, f.atom("_NET_WM_ICON"))
	if v := ic.u32s(); !slices.Equal(v, []uint32{2, 1, 0, 0x80ff0000}) {
		t.Errorf("_NET_WM_ICON = %#x", v)
	}
	if p, _ := f.prop(w.id, f.atom("_MOTIF_WM_HINTS")); !slices.Equal(p.u32s(), []uint32{2, 0, 0, 0, 0}) {
		t.Errorf("_MOTIF_WM_HINTS = %v", p.u32s())
	}
	protocols, _ := f.prop(w.id, f.atom("WM_PROTOCOLS"))
	if v := protocols.u32s(); !slices.Equal(v, []uint32{f.atom("WM_DELETE_WINDOW"), f.atom("_NET_WM_PING")}) {
		t.Errorf("WM_PROTOCOLS = %v", v)
	}
	// Mapping ended the launch's startup notification.
	r := f.waitRequest(func(r request) bool {
		return r.op == opSendEvent && u32At(r.body, 16) == f.atom("_NET_STARTUP_INFO_BEGIN")
	})
	if got := string(r.body[20:40]); got != `remove: ID="launch_T` {
		t.Errorf("startup message begins %q", got)
	}
	// Later windows carry no startup ID.
	w2 := openWindow(t, f, b, platform.WindowOptions{})
	if _, ok := f.prop(w2.id, f.atom("_NET_STARTUP_ID")); ok {
		t.Error("second window has the startup ID")
	}
	w.SetTitle("new")
	settle(t, f, w)
	if got := str("_NET_WM_NAME", 0); got != "new" {
		t.Errorf("SetTitle: %q", got)
	}
}

func TestNewWindowFails(t *testing.T) {
	f, b := startBackend(t, nil)
	// A CreateWindow too large for the server, then properties that are: an
	// icon of 40 kB.
	b.c.Setup.MaxRequest = 44
	if _, err := b.NewWindow(platform.WindowOptions{}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("window: %v", err)
	}
	b.c.Setup.MaxRequest = 1000
	icon := image.NewRGBA(image.Rect(0, 0, 100, 100))
	if _, err := b.NewWindow(platform.WindowOptions{Icon: icon}); !errors.Is(err, ErrTooLarge) {
		t.Errorf("icon: %v", err)
	}
	b.c.Sync()
	if n := len(f.requests(opDestroyWindow)); n != 1 {
		t.Errorf("windows destroyed: %d", n)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.windows) != 0 {
		t.Errorf("windows kept: %d", len(b.windows))
	}
}

func TestWindowLifecycle(t *testing.T) {
	f, b := startBackend(t, nil)
	pw, _ := b.NewWindow(platform.WindowOptions{Size: platform.Size{W: 40, H: 30}})
	w := pw.(*window)
	// Before mapping: no frames, and configures only record the size.
	w.Invalidate()
	f.event(configureEv(w.id, 40, 30))
	f.event(configureEv(w.id, 44, 33))
	f.event(xev(evExpose, func(b []byte) { order.PutUint32(b[4:], w.id) }))
	settle(t, f, w)
	if len(w.events) != 0 {
		t.Fatalf("events before mapping: %d", len(w.events))
	}
	f.event(mapEv(w.id))
	if r := want[platform.ResizeEvent](t, w); r.Size != (platform.Size{W: 44, H: 33}) || r.Scale != 1 {
		t.Errorf("resize = %+v", r)
	}
	want[platform.FrameEvent](t, w)
	f.event(mapEv(w.id)) // mapped again after minimising: nothing new
	f.event(configureEv(w.id, 44, 33))
	quiet(t, f, w)
	f.event(configureEv(w.id, 60, 50))
	if r := want[platform.ResizeEvent](t, w); r.Size != (platform.Size{W: 60, H: 50}) {
		t.Errorf("resize = %+v", r)
	}
	// The frame asked for before the resize is still waiting, so the resize
	// asks for another once it is answered.
	w.Present(image.NewRGBA(image.Rect(0, 0, 44, 33))) // the old size: dropped
	want[platform.FrameEvent](t, w)
	// Events for unknown windows are ignored.
	f.event(mapEv(0x999))
	f.event(configureEv(0x999, 1, 1))
	f.event(xev(evExpose, func(b []byte) { order.PutUint32(b[4:], 0x999) }))
	f.event(xev(evMappingNotify, func(b []byte) { b[4] = 2 }))
	f.event(xev(60, nil)) // an event the backend does not know
	quiet(t, f, w)
	w.Close()
	w.Close()
	if _, ok := <-w.events; ok {
		t.Error("events open after Close")
	}
	r := f.waitRequest(func(r request) bool { return r.op == opDestroyWindow && r.u32(0) == w.id })
	_ = r
	// Calls on a closed window do nothing.
	w.Invalidate()
	w.Present(image.NewRGBA(image.Rect(0, 0, 60, 50)))
}

func TestStateEventsFollowNetWMState(t *testing.T) {
	f, b := startBackend(t, withWM("_NET_WM_MOVERESIZE"))
	w := openWindow(t, f, b, platform.WindowOptions{})
	state := f.atom("_NET_WM_STATE")
	set := func(atoms ...string) {
		var v []uint32
		for _, a := range atoms {
			v = append(v, f.atom(a))
		}
		f.setProp(w.id, state, atomAtom, 32, propU32(v...))
		f.event(propertyEv(w.id, state, false))
	}
	set("_NET_WM_STATE_MAXIMIZED_VERT", "_NET_WM_STATE_MAXIMIZED_HORZ")
	if s := want[platform.StateEvent](t, w); !s.Maximized || s.Decorated {
		t.Errorf("state = %+v", s)
	}
	set("_NET_WM_STATE_MAXIMIZED_VERT", "_NET_WM_STATE_MAXIMIZED_HORZ", "_NET_WM_STATE_FOCUSED")
	quiet(t, f, w) // no change
	set("_NET_WM_STATE_MAXIMIZED_VERT")
	if s := want[platform.StateEvent](t, w); s.Maximized {
		t.Errorf("half maximised = %+v", s)
	}
	set("_NET_WM_STATE_FULLSCREEN")
	if s := want[platform.StateEvent](t, w); !s.Maximized {
		t.Errorf("full screen = %+v", s)
	}
	// A state that cannot be read leaves the state as it was.
	f.set(func(f *fakeX) {
		f.handle = func(f *fakeX, r request) bool {
			if r.op == opGetProperty && r.u32(4) == state {
				f.fail(r, 3, 0)
				return true
			}
			return false
		}
	})
	f.event(propertyEv(w.id, state, false))
	quiet(t, f, w)
}

func TestCloseRequestAndPing(t *testing.T) {
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{})
	protocols := f.atom("WM_PROTOCOLS")
	f.event(clientMessage(w.id, protocols, f.atom("WM_DELETE_WINDOW")))
	want[platform.CloseEvent](t, w)
	f.event(clientMessage(w.id, protocols, 12345)) // a protocol the window does not speak
	f.event(clientMessage(0x999, protocols, f.atom("WM_DELETE_WINDOW")))
	quiet(t, f, w)
	r := f.waitRequest(func(r request) bool { return r.op == opSendEvent && u32At(r.body, 24) >= 1000 })
	if r.u32(0) != fakeRoot || u32At(r.body, 12) != fakeRoot {
		t.Errorf("pong sent to %#x about %#x", r.u32(0), u32At(r.body, 12))
	}
}

func TestPresentWithSharedMemory(t *testing.T) {
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{Size: platform.Size{W: 4, H: 3}})
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Pix[0], img.Pix[1], img.Pix[2] = 10, 20, 30
	w.Present(img)
	r := f.waitRequest(func(r request) bool { return r.op == 130 && r.data == shmPutImage })
	if r.u32(0) != w.id || r.u16(8) != 4 || r.u16(10) != 3 {
		t.Errorf("ShmPutImage %v", r.body[:12])
	}
	if got := w.seg.data[:4]; got[0] != 30 || got[1] != 20 || got[2] != 10 {
		t.Errorf("pixel = %v", got)
	}
	w.Invalidate()
	want[platform.FrameEvent](t, w)
	// The same size reuses the segment.
	w.Present(img)
	w.Invalidate()
	want[platform.FrameEvent](t, w)
	if n := len(f.requests(130)); n != 4 { // version, attach, two puts
		t.Errorf("MIT-SHM requests: %d", n)
	}
	// A resize replaces it, detaching the old one.
	f.event(configureEv(w.id, 5, 5))
	want[platform.ResizeEvent](t, w)
	w.Present(image.NewRGBA(image.Rect(0, 0, 5, 5)))
	f.waitRequest(func(r request) bool { return r.op == 130 && r.data == shmDetach })
	w.Invalidate()
	want[platform.FrameEvent](t, w)
	// Asking twice while a frame waits brings one frame.
	w.Invalidate()
	w.Invalidate()
	quiet(t, f, w)
}

func TestPresentFallsBackToPutImage(t *testing.T) {
	f, b := startBackend(t, nil)
	override(t, &memfdCreate, func(string, int) (int, error) { return -1, unix.EMFILE })
	w := openWindow(t, f, b, platform.WindowOptions{Size: platform.Size{W: 3, H: 2}})
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	img.Pix[4*5] = 0xff
	w.Present(img)
	r := f.waitRequest(func(r request) bool { return r.op == opPutImage })
	if r.u16(8) != 3 || r.u16(10) != 2 || r.body[17] != 24 {
		t.Errorf("PutImage %v", r.body[:20])
	}
	if b.shm.present {
		t.Error("shm kept after a failure")
	}
	if got := r.body[20+4*5+2]; got != 0xff {
		t.Errorf("red of the last pixel = %#x", got)
	}
}

func TestPutImageSplitsIntoStrips(t *testing.T) {
	o := defaultSetup()
	o.maxReq = 16 // 64 bytes: 24 of header leave room for 10 pixels
	fx, sock := newFakeX(t, o)
	c, _ := NewConn(sock, "", nil)
	defer c.Close()
	b := &Backend{c: c}
	pix := make([]byte, 4*4*5) // 4 by 5
	if err := b.putImage(1, 2, 24, 4, 5, pix); err != nil {
		t.Fatal(err)
	}
	c.Sync()
	var rows []int
	for _, r := range fx.requests(opPutImage) {
		rows = append(rows, int(r.u16(10)))
	}
	if !slices.Equal(rows, []int{2, 2, 1}) {
		t.Errorf("strips of %v rows", rows)
	}
	fx.sock.Close()
	<-c.Done()
	if err := b.putImage(1, 2, 24, 4, 5, pix); err == nil {
		t.Error("putImage on a closed connection succeeded")
	}
}

func TestShmSegmentFailures(t *testing.T) {
	_, b := startBackend(t, nil)
	if _, err := b.newShmSeg(0); err == nil { // mmap of zero bytes fails
		t.Error("zero-size segment made")
	}
	if _, err := b.newShmSeg(-1); err == nil { // ftruncate fails
		t.Error("negative size accepted")
	}
	b.c.Close()
	if _, err := b.newShmSeg(64); err == nil {
		t.Error("segment attached on a closed connection")
	}
}

func TestCloseDuringAFrameLeavesTheBufferToIt(t *testing.T) {
	f, b := startBackend(t, nil)
	hold := make(chan request, 1)
	w := openWindow(t, f, b, platform.WindowOptions{Size: platform.Size{W: 2, H: 2}})
	f.set(func(f *fakeX) {
		f.handle = func(f *fakeX, r request) bool {
			if r.op == opGetInputFocus {
				hold <- r // answered once the window is closed
				return true
			}
			return false
		}
	})
	w.Present(image.NewRGBA(image.Rect(0, 0, 2, 2)))
	r := <-hold
	w.Close()
	if w.seg == nil {
		t.Fatal("buffer freed under the frame")
	}
	f.reply(r, 0, nil)
	f.waitRequest(func(r request) bool { return r.op == 130 && r.data == shmDetach })
}

func TestBackendCloseEndsWindows(t *testing.T) {
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{})
	f.sock.Close()
	if _, ok := <-w.events; ok {
		t.Error("window events still open")
	}
	b.Close()
}

func TestWindowEventsDropWhenTheApplicationStalls(t *testing.T) {
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{})
	for range eventQueue + 5 {
		w.emit(platform.CloseEvent{})
	}
	if len(w.events) != eventQueue {
		t.Errorf("queued %d", len(w.events))
	}
	_ = f
}

func TestNewWithoutDisplay(t *testing.T) {
	t.Setenv("DISPLAY", "")
	if _, err := New(); err == nil {
		t.Error("New without DISPLAY succeeded")
	}
	t.Setenv("DISPLAY", ":7")
	t.Setenv("XAUTHORITY", "/nonexistent")
	override(t, &dial, func(network, addr string) (net.Conn, error) {
		if strings.HasPrefix(addr, "@") {
			return nil, errors.New("no")
		}
		o := defaultSetup()
		o.visuals = nil
		_, sock := newFakeX(t, o)
		return sock, nil
	})
	if _, err := New(); !errors.Is(err, ErrVisual) {
		t.Errorf("New on a screen without visuals: %v", err)
	}
	override(t, &dial, func(network, addr string) (net.Conn, error) {
		_, sock := newFakeX(t, defaultSetup())
		return sock, nil
	})
	b, err := New()
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
}
