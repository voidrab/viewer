package wayland

import (
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"viewer/internal/ui/platform"
)

// clipSetup is a window on a compositor with a seat and a data device manager,
// recording what the client asks of it.
type clipSetup struct {
	srv     *server
	win     platform.Window
	surface uint32
	f       *fakeWM

	mu        sync.Mutex
	manager   uint32
	seat      uint32
	pointer   uint32
	device    uint32
	sources   []uint32            // created, in order
	mimes     map[uint32][]string // offered by each source
	selection []selectionRequest
	destroyed []uint32 // sources and offers the client destroyed
	received  []receiveRequest
	log       chan string
	// extra, if set, sees each request first, and handles it by returning
	// true. It runs with mu held.
	extra func(ev Event, d *Decoder) bool
}

type selectionRequest struct{ source, serial uint32 }
type receiveRequest struct {
	offer uint32
	mime  string
	fd    int
}

func newClipSetup(t *testing.T, withManager bool) *clipSetup {
	t.Helper()
	return newClipSetupVersion(t, withManager, 3)
}

// newClipSetupVersion is newClipSetup with a data device manager of version.
func newClipSetupVersion(t *testing.T, withManager bool, version uint32) *clipSetup {
	t.Helper()
	cs := &clipSetup{mimes: map[uint32][]string{}, log: make(chan string, 64)}
	a, b := pair(t)
	f := &fakeWM{t: t, log: make(chan string, 256), ids: map[string]uint32{},
		poolMem: map[uint32][]byte{}, bufMem: map[uint32][]byte{}, bufSize: map[uint32][3]int32{}}
	f.afterBind = func(s *server, iface string, id uint32) {
		cs.mu.Lock()
		defer cs.mu.Unlock()
		switch iface {
		case "wl_seat":
			cs.seat = id
			s.send(id, seatCapabilites, func(e *Encoder) { e.Uint(seatCapPointer) })
		case "wl_data_device_manager":
			cs.manager = id
		}
	}
	globals := append(append([]Global(nil), wmGlobals...), Global{Name: 4, Interface: "wl_seat", Version: 5})
	if withManager {
		globals = append(globals, Global{Name: 5, Interface: "wl_data_device_manager", Version: version})
	}
	base := f.handler(globals)
	cs.srv = serve(t, b, func(s *server, ev Event, d *Decoder) {
		cs.mu.Lock()
		defer cs.mu.Unlock()
		if cs.extra != nil && cs.extra(ev, d) {
			return
		}
		switch {
		case cs.seat != 0 && ev.Object == cs.seat && ev.Opcode == seatGetPointer:
			cs.pointer = d.Uint()
		case cs.manager != 0 && ev.Object == cs.manager:
			switch ev.Opcode {
			case dataManagerGetDevice:
				cs.device = d.Uint()
				cs.log <- "get_data_device"
			case dataManagerCreateSource:
				cs.sources = append(cs.sources, d.Uint())
				cs.log <- "create_source"
			}
		case cs.device != 0 && ev.Object == cs.device && ev.Opcode == deviceSetSelection:
			cs.selection = append(cs.selection, selectionRequest{d.Uint(), d.Uint()})
			cs.log <- "set_selection"
		case ev.Opcode == sourceOffer && cs.isSource(ev.Object):
			cs.mimes[ev.Object] = append(cs.mimes[ev.Object], d.String())
		case ev.Opcode == sourceDestroy && cs.isSource(ev.Object):
			cs.destroyed = append(cs.destroyed, ev.Object)
			cs.log <- "destroy_source"
		case ev.Object >= firstServerID && ev.Opcode == offerDestroy:
			cs.destroyed = append(cs.destroyed, ev.Object)
			cs.log <- "destroy_offer"
		case ev.Object >= firstServerID && ev.Opcode == offerReceive:
			mime, fd := d.String(), d.FD()
			cs.received = append(cs.received, receiveRequest{ev.Object, mime, fd})
			cs.log <- "receive"
		default:
			cs.mu.Unlock()
			base(s, ev, d)
			cs.mu.Lock()
		}
	})
	be, err := Open(a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { be.Close() })
	win, err := be.NewWindow(platform.WindowOptions{Size: platform.Size{W: 100, H: 100}})
	if err != nil {
		t.Fatal(err)
	}
	f.wait("commit")
	f.configure(cs.srv, 100, 100, 1)
	next(t, win)
	next(t, win)
	cs.win, cs.f = win, f
	f.mu.Lock()
	cs.surface = f.surface
	f.mu.Unlock()
	return cs
}

func (cs *clipSetup) isSource(id uint32) bool {
	for _, s := range cs.sources {
		if s == id {
			return true
		}
	}
	return false
}

func (cs *clipSetup) wait(what string) {
	cs.f.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case got := <-cs.log:
			if got == what {
				return
			}
		case <-timeout:
			cs.f.t.Fatalf("timed out waiting for %q", what)
		}
	}
}

// pointerEnter gives the client the serial it will cite.
func (cs *clipSetup) pointerEnter(serial uint32) {
	cs.mu.Lock()
	p := cs.pointer
	cs.mu.Unlock()
	cs.srv.send(p, pointerEnter, func(e *Encoder) { e.Uint(serial).Uint(cs.surface).Fixed(1).Fixed(1) })
	cs.f.t.Helper()
	if ev := next(cs.f.t, cs.win); ev.(platform.PointerEvent).Kind != platform.Enter {
		cs.f.t.Fatalf("event %#v", ev)
	}
}

// offerFrom has another program offer text in mimes as the selection.
func (cs *clipSetup) offerFrom(id uint32, mimes ...string) {
	cs.mu.Lock()
	dev := cs.device
	cs.mu.Unlock()
	cs.srv.send(dev, deviceDataOffer, func(e *Encoder) { e.Uint(id) })
	for _, m := range mimes {
		cs.srv.send(id, offerOffer, func(e *Encoder) { e.String(m) })
	}
	cs.srv.send(dev, deviceSelection, func(e *Encoder) { e.Uint(id) })
}

func TestSetClipboardOffersTextAndServesIt(t *testing.T) {
	cs := newClipSetup(t, true)
	cs.wait("get_data_device")
	cs.pointerEnter(42)
	cs.win.SetClipboard("copied text")
	cs.wait("set_selection")
	cs.mu.Lock()
	src := cs.sources[0]
	if len(cs.selection) != 1 || cs.selection[0] != (selectionRequest{src, 42}) {
		t.Errorf("selection = %+v, want source %d with serial 42", cs.selection, src)
	}
	if got := cs.mimes[src]; len(got) != len(textMimes) || got[0] != "text/plain;charset=utf-8" {
		t.Errorf("offered %v, want %v", got, textMimes)
	}
	cs.mu.Unlock()

	// Another program pastes: the compositor passes a pipe, and gets the text.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cs.srv.send(src, sourceSend, func(e *Encoder) { e.String("text/plain").FD(int(w.Fd())) })
	w.Close()
	r.SetReadDeadline(time.Now().Add(5 * time.Second))
	got, err := io.ReadAll(r)
	if err != nil || string(got) != "copied text" {
		t.Errorf("pasted %q, %v", got, err)
	}

	// A second copy replaces the first source, which is destroyed.
	cs.win.SetClipboard("second")
	cs.wait("destroy_source")
	cs.mu.Lock()
	if len(cs.sources) != 2 || cs.destroyed[0] != src {
		t.Errorf("sources %v, destroyed %v", cs.sources, cs.destroyed)
	}
	second := cs.sources[1]
	cs.mu.Unlock()

	// The compositor taking the selection away ends the second source.
	cs.srv.send(second, sourceCancel, nil)
	cs.wait("destroy_source")
	cs.srv.send(second, sourceCancel, nil) // a repeat has nothing left to destroy
}

func TestPasteFromAnotherProgram(t *testing.T) {
	cs := newClipSetup(t, true)
	cs.wait("get_data_device")
	const offer1, offer2 = firstServerID + 1, firstServerID + 2

	// Nothing is offered yet, and an offer without a text format is no use.
	cs.win.RequestPaste()
	cs.offerFrom(offer1, "image/png")
	cs.pointerEnter(3)
	cs.win.RequestPaste()
	if !quiet(cs.win, 80*time.Millisecond) {
		t.Fatal("a paste event came without text on offer")
	}

	// A later selection replaces it, and the old offer is destroyed.
	cs.offerFrom(offer2, "STRING", "text/plain")
	cs.wait("destroy_offer")
	cs.pointerEnter(4)
	cs.win.RequestPaste()
	cs.wait("receive")
	cs.mu.Lock()
	rq := cs.received[0]
	cs.mu.Unlock()
	if rq.offer != offer2 || rq.mime != "text/plain" {
		t.Errorf("receive = %+v, want offer %#x in the better of its formats, text/plain", rq, offer2)
	}
	w := os.NewFile(uintptr(rq.fd), "pipe")
	io.WriteString(w, "from elsewhere")
	w.Close()
	if ev := next(t, cs.win); ev != (platform.PasteEvent{Text: "from elsewhere"}) {
		t.Errorf("event = %#v", ev)
	}

	// An empty answer, and a cleared selection, give nothing.
	cs.win.RequestPaste()
	cs.wait("receive")
	cs.mu.Lock()
	rq = cs.received[1]
	cs.mu.Unlock()
	unix.Close(rq.fd)
	if !quiet(cs.win, 100*time.Millisecond) {
		t.Error("an empty clipboard produced a paste event")
	}
	cs.srv.send(cs.device, deviceSelection, func(e *Encoder) { e.Uint(0) })
	cs.wait("destroy_offer")
	cs.win.RequestPaste()
	if !quiet(cs.win, 80*time.Millisecond) {
		t.Error("a paste event came after the selection was cleared")
	}
}

func TestPastingYourOwnCopyNeedsNoRoundTrip(t *testing.T) {
	cs := newClipSetup(t, true)
	cs.wait("get_data_device")
	cs.win.SetClipboard("mine")
	cs.wait("set_selection")
	cs.win.RequestPaste()
	if ev := next(t, cs.win); ev != (platform.PasteEvent{Text: "mine"}) {
		t.Errorf("event = %#v", ev)
	}
}

func TestNoClipboardWithoutADataDeviceManager(t *testing.T) {
	cs := newClipSetup(t, false)
	cs.win.SetClipboard("nowhere to put it")
	cs.win.RequestPaste()
	if !quiet(cs.win, 80*time.Millisecond) {
		t.Error("a paste event came from a compositor without a clipboard")
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if len(cs.sources) != 0 || len(cs.selection) != 0 {
		t.Errorf("requests sent without a manager: %v %v", cs.sources, cs.selection)
	}
}

func TestPasteGivesUpWhenNoPipeCanBeMade(t *testing.T) {
	cs := newClipSetup(t, true)
	cs.wait("get_data_device")
	cs.offerFrom(firstServerID+1, "text/plain")
	cs.pointerEnter(5) // delivered after the selection above, so it has been handled
	old := pipe
	pipe = func() (*os.File, *os.File, error) { return nil, nil, os.ErrInvalid }
	defer func() { pipe = old }()
	cs.win.RequestPaste()
	if !quiet(cs.win, 80*time.Millisecond) {
		t.Error("a paste event came although no pipe could be made")
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if len(cs.received) != 0 {
		t.Errorf("a receive request went out without a pipe: %+v", cs.received)
	}
}
