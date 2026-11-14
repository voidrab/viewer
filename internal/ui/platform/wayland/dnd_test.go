package wayland

import (
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"viewer/internal/ui/platform"
)

// dndSetup is a clipSetup whose compositor also records the requests of drag
// and drop, each as a line such as "accept 4278190081 7 text/uri-list".
type dndSetup struct {
	*clipSetup
	reqs []string // guarded by mu
	icon uint32   // the drag icon's surface
}

func newDnDSetup(t *testing.T, version uint32) *dndSetup {
	t.Helper()
	ds := &dndSetup{clipSetup: newClipSetupVersion(t, true, version)}
	ds.wait("get_data_device")
	ds.f.mu.Lock()
	compositor := ds.f.ids["wl_compositor"]
	ds.f.mu.Unlock()
	ds.mu.Lock()
	defer ds.mu.Unlock()
	ds.extra = func(ev Event, d *Decoder) bool {
		var line string
		switch {
		case ev.Object == compositor:
			ds.icon = d.Uint()
			line = "icon"
		case ev.Object == ds.device && ev.Opcode == deviceStartDrag:
			line = fmt.Sprintf("start_drag %d %d %d %d", d.Uint(), d.Uint(), d.Uint(), d.Uint())
		case ds.isSource(ev.Object) && ev.Opcode == sourceSetActions:
			line = fmt.Sprintf("source_actions %d", d.Uint())
		case ds.icon != 0 && ev.Object == ds.icon:
			switch ev.Opcode {
			case surfaceAttach:
				d.Uint() // the buffer, which is not checked
				line = fmt.Sprintf("icon_attach %d %d", d.Int(), d.Int())
			case surfaceDamageBuffer:
				line = "icon_damage"
			case surfaceCommit:
				line = "icon_commit"
			case surfaceDestroy:
				line = "icon_destroy"
			}
		case ev.Object >= firstServerID && ev.Opcode == offerAccept:
			serial, n := d.Uint(), d.Uint()
			mime := "<null>"
			if n > 0 {
				b := d.take(int(padded(int(n))))
				mime = string(b[:n-1])
			}
			line = fmt.Sprintf("accept %d %s", serial, mime)
		case ev.Object >= firstServerID && ev.Opcode == offerSetActions:
			line = fmt.Sprintf("offer_actions %d %d", d.Uint(), d.Uint())
		case ev.Object >= firstServerID && ev.Opcode == offerFinish:
			line = "finish"
		default:
			return false
		}
		ds.reqs = append(ds.reqs, line)
		ds.log <- line
		return true
	}
	return ds
}

// requests returns the drag and drop requests recorded so far, and forgets
// them.
func (ds *dndSetup) requests() []string {
	ds.mu.Lock()
	defer ds.mu.Unlock()
	r := ds.reqs
	ds.reqs = nil
	return r
}

// offer has another program announce offer, listing mimes.
func (ds *dndSetup) offer(id uint32, mimes ...string) {
	ds.srv.send(ds.device, deviceDataOffer, func(e *Encoder) { e.Uint(id) })
	for _, m := range mimes {
		ds.srv.send(id, offerOffer, func(e *Encoder) { e.String(m) })
	}
}

func (ds *dndSetup) enter(serial, surface, offer uint32, x, y float64) {
	ds.srv.send(ds.device, deviceEnter, func(e *Encoder) { e.Uint(serial).Uint(surface).Fixed(x).Fixed(y).Uint(offer) })
}

func (ds *dndSetup) motion(x, y float64) {
	ds.srv.send(ds.device, deviceMotion, func(e *Encoder) { e.Uint(0).Fixed(x).Fixed(y) })
}

func (ds *dndSetup) event(opcode uint16) { ds.srv.send(ds.device, opcode, nil) }

// sync waits until the client has handled every event sent so far: a pointer
// entry sent after them arrives last.
func (ds *dndSetup) sync() {
	ds.f.t.Helper()
	ds.pointerEnter(1)
}

func wantEvent[T comparable](t *testing.T, win platform.Window, ev T) {
	t.Helper()
	if got, ok := next(t, win).(T); !ok || got != ev {
		t.Errorf("event %#v, want %#v", got, ev)
	}
}

// wantOwnDrop checks that the next event is a drop of the program's own drag
// at (x, y).
func wantOwnDrop(t *testing.T, win platform.Window, x, y float64) {
	t.Helper()
	if ev, ok := next(t, win).(platform.DropEvent); !ok || !ev.Own || ev.X != x || ev.Y != y || ev.Paths != nil {
		t.Errorf("event %#v, want the program's own drop at %v, %v", ev, x, y)
	}
}

func sameLines(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("requests\n%q, want\n%q", got, want)
	}
}

// override sets *v to x until the test and its cleanups end, before the
// backend that reads it starts.
func override[T any](t *testing.T, v *T, x T) {
	old := *v
	*v = x
	t.Cleanup(func() { *v = old })
}

func TestDroppedFilesArriveAsTheirPaths(t *testing.T) {
	override(t, &hostname, func() (string, error) { return "here", nil })
	ds := newDnDSetup(t, 3)
	const offer = firstServerID + 1
	ds.offer(offer, "text/plain", uriListMime)
	ds.enter(7, ds.surface, offer, 10, 20)
	wantEvent(t, ds.win, platform.DragEvent{Kind: platform.DragEnter, X: 10, Y: 20})
	ds.motion(30, 40)
	wantEvent(t, ds.win, platform.DragEvent{Kind: platform.DragMove, X: 30, Y: 40})
	ds.srv.send(offer, offerAction, func(e *Encoder) { e.Uint(actionCopy) })
	ds.event(deviceDrop)
	ds.wait("receive")
	ds.mu.Lock()
	rq := ds.received[0]
	ds.mu.Unlock()
	if rq.offer != offer || rq.mime != uriListMime {
		t.Errorf("receive %+v", rq)
	}
	w := os.NewFile(uintptr(rq.fd), "pipe")
	io.WriteString(w, "# a comment\r\nfile:///tmp/a.pdf\r\nhttp://example.com/b.pdf\nfile://elsewhere/c.pdf\n\nfile://localhost/d%20e.pdf\nfile://here/f.pdf\nfile:\n%zz\n")
	w.Close()
	ev, _ := next(t, ds.win).(platform.DropEvent)
	if ev.X != 30 || ev.Y != 40 || ev.Own || !slices.Equal(ev.Paths, []string{"/tmp/a.pdf", "/d e.pdf", "/f.pdf"}) {
		t.Errorf("drop %#v", ev)
	}
	ds.wait("destroy_offer")
	// The leave that follows the drop has nothing left to end.
	ds.event(deviceLeave)
	ds.sync()
	sameLines(t, ds.requests(), "accept 7 text/uri-list", "offer_actions 1 1", "finish")
}

func TestADropOfNoFilesTellsNothing(t *testing.T) {
	ds := newDnDSetup(t, 3)
	const offer = firstServerID + 1
	ds.offer(offer, uriListMime)
	ds.enter(7, ds.surface, offer, 1, 1)
	wantEvent(t, ds.win, platform.DragEvent{Kind: platform.DragEnter, X: 1, Y: 1})
	ds.event(deviceDrop) // no action was chosen, so there is nothing to finish
	ds.wait("receive")
	ds.mu.Lock()
	rq := ds.received[0]
	ds.mu.Unlock()
	w := os.NewFile(uintptr(rq.fd), "pipe")
	io.WriteString(w, "https://example.com/\n")
	w.Close()
	ds.wait("destroy_offer")
	if !quiet(ds.win, 80*time.Millisecond) {
		t.Error("a drop of no files was reported")
	}
	sameLines(t, ds.requests(), "accept 7 text/uri-list", "offer_actions 1 1")
}

func TestADropGivesUpWhenNoPipeCanBeMade(t *testing.T) {
	override(t, &pipe, func() (*os.File, *os.File, error) { return nil, nil, os.ErrInvalid })
	ds := newDnDSetup(t, 3)
	const offer = firstServerID + 1
	ds.offer(offer, uriListMime)
	ds.enter(7, ds.surface, offer, 1, 1)
	wantEvent(t, ds.win, platform.DragEvent{Kind: platform.DragEnter, X: 1, Y: 1})
	ds.event(deviceDrop)
	ds.wait("destroy_offer")
	if !quiet(ds.win, 80*time.Millisecond) {
		t.Error("a drop was reported without its files")
	}
}

func TestWindowsRefuseWhatTheyCannotOpen(t *testing.T) {
	ds := newDnDSetup(t, 3)
	const pictures, files, again = firstServerID + 1, firstServerID + 2, firstServerID + 3
	// A drag of no offer, or of an image, is refused, and neither its motion
	// nor its drop tells the window anything.
	ds.enter(5, ds.surface, 0, 1, 1)
	ds.motion(2, 2)
	ds.event(deviceDrop)
	ds.event(deviceLeave)
	ds.offer(pictures, "image/png")
	ds.enter(6, ds.surface, pictures, 1, 1)
	ds.motion(2, 2)
	ds.event(deviceDrop)
	ds.wait("destroy_offer")
	// Neither is a drag over a surface the program does not know.
	ds.offer(files, uriListMime)
	ds.enter(7, 999, files, 1, 1)
	ds.event(deviceLeave)
	ds.wait("destroy_offer")
	// A drag that enters again without leaving first ends the first offer.
	ds.offer(files, uriListMime)
	ds.enter(8, ds.surface, files, 1, 1)
	wantEvent(t, ds.win, platform.DragEvent{Kind: platform.DragEnter, X: 1, Y: 1})
	ds.offer(again, uriListMime)
	ds.enter(9, ds.surface, again, 3, 4)
	ds.wait("destroy_offer")
	wantEvent(t, ds.win, platform.DragEvent{Kind: platform.DragEnter, X: 3, Y: 4})
	ds.event(deviceLeave)
	wantEvent(t, ds.win, platform.DragEvent{Kind: platform.DragLeave, X: 3, Y: 4})
	ds.wait("destroy_offer")
	sameLines(t, ds.requests(), "accept 6 <null>", "accept 7 <null>",
		"accept 8 text/uri-list", "offer_actions 1 1", "accept 9 text/uri-list", "offer_actions 1 1")
}

func TestAnOldDataDeviceNegotiatesNoActions(t *testing.T) {
	ds := newDnDSetup(t, 2)
	const offer = firstServerID + 1
	ds.offer(offer, ownMime)
	ds.enter(7, ds.surface, offer, 1, 1)
	wantEvent(t, ds.win, platform.DragEvent{Kind: platform.DragEnter, X: 1, Y: 1, Own: true})
	ds.srv.send(offer, offerAction, func(e *Encoder) { e.Uint(actionMove) })
	ds.event(deviceDrop)
	wantOwnDrop(t, ds.win, 1, 1)
	ds.pointerEnter(3)
	if !ds.win.StartDrag(nil, image.Point{}) {
		t.Fatal("no drag began")
	}
	start := fmt.Sprintf("start_drag %d %d 0 3", ds.newSource(), ds.surface)
	ds.wait(start)
	sameLines(t, ds.requests(), "accept 7 "+ownMime, start)
}

// newSource waits for the client to make a source, and returns it.
func (ds *dndSetup) newSource() uint32 {
	ds.f.t.Helper()
	ds.wait("create_source")
	ds.mu.Lock()
	defer ds.mu.Unlock()
	return ds.sources[len(ds.sources)-1]
}

func TestAProgramDragDroppedOnOneOfItsWindows(t *testing.T) {
	ds := newDnDSetup(t, 3)
	ds.pointerEnter(11)
	icon := image.NewRGBA(image.Rect(0, 0, 4, 2))
	icon.Pix[0], icon.Pix[2], icon.Pix[3] = 10, 30, 255
	if !ds.win.StartDrag(icon, image.Pt(3, 1)) {
		t.Fatal("no drag began")
	}
	if ds.win.StartDrag(nil, image.Point{}) {
		t.Error("a second drag began while the first went on")
	}
	src := ds.newSource()
	ds.wait("icon_commit")
	ds.mu.Lock()
	icon2 := ds.icon
	if got := ds.mimes[src]; !slices.Equal(got, []string{ownMime}) {
		t.Errorf("the source offers %q", got)
	}
	ds.mu.Unlock()
	sameLines(t, ds.requests(), "source_actions 2", "icon",
		fmt.Sprintf("start_drag %d %d %d 11", src, ds.surface, icon2), "icon_attach -3 -1", "icon_damage", "icon_commit")
	ds.f.mu.Lock()
	var mem []byte
	for _, m := range ds.f.bufMem {
		mem = m
	}
	ds.f.mu.Unlock()
	if mem[0] != 30 || mem[2] != 10 || mem[3] != 255 {
		t.Errorf("icon pixels % x", mem[:4])
	}

	// The drag comes back over the window, which takes it.
	const offer = firstServerID + 1
	ds.offer(offer, ownMime)
	ds.enter(12, ds.surface, offer, 5, 6)
	wantEvent(t, ds.win, platform.DragEvent{Kind: platform.DragEnter, X: 5, Y: 6, Own: true})
	ds.srv.send(offer, offerAction, func(e *Encoder) { e.Uint(actionMove) })
	ds.event(deviceDrop)
	wantOwnDrop(t, ds.win, 5, 6)
	ds.wait("destroy_offer")
	// The compositor asks for the data, which nothing needs, and then ends the
	// drag as dropped.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	ds.srv.send(src, sourceSend, func(e *Encoder) { e.String(ownMime).FD(int(w.Fd())) })
	w.Close()
	r.SetReadDeadline(time.Now().Add(5 * time.Second))
	if data, err := io.ReadAll(r); err != nil || len(data) != 0 {
		t.Errorf("the drag's data: %q, %v", data, err)
	}
	r.Close()
	ds.srv.send(src, sourceFinished, nil)
	wantEvent(t, ds.win, platform.DragEndEvent{Dropped: true})
	ds.wait("destroy_source")
	ds.f.wait("destroy_buffer")
	sameLines(t, ds.requests(), "accept 12 "+ownMime, "offer_actions 2 2", "finish", "icon_destroy")
	// The source is gone: a late event for it changes nothing.
	ds.srv.send(src, sourceCancel, nil)
	if !quiet(ds.win, 80*time.Millisecond) {
		t.Error("a late cancel was reported")
	}
}

func TestACancelledDragTellsWhetherItEndedOutside(t *testing.T) {
	var at atomic.Int64 // seconds
	override(t, &now, func() time.Time { return time.Unix(at.Load(), 0) })
	ds := newDnDSetup(t, 3)
	// Without memory for the icon, the drag goes on without one.
	oldMemfd := memfdCreate
	memfdCreate = func(string, int) (int, error) { return -1, errors.New("no memory") }
	ds.pointerEnter(2)
	if !ds.win.StartDrag(image.NewRGBA(image.Rect(0, 0, 2, 2)), image.Point{}) {
		t.Fatal("no drag began")
	}
	src := ds.newSource()
	ds.wait("icon_commit")
	memfdCreate = oldMemfd
	end := func(want platform.DragEndEvent) {
		t.Helper()
		ds.srv.send(src, sourceCancel, nil)
		if got := next(t, ds.win); got != want {
			t.Errorf("end %#v, want %#v", got, want)
		}
		ds.wait("destroy_source")
	}
	// Away from every window, the cancel is outside.
	end(platform.DragEndEvent{Outside: true})
	ds.requests()

	// Over a window, it is not.
	again := func() {
		t.Helper()
		ds.pointerEnter(3)
		if !ds.win.StartDrag(nil, image.Point{}) {
			t.Fatal("no drag began")
		}
		src = ds.newSource()
		ds.wait(fmt.Sprintf("start_drag %d %d 0 3", src, ds.surface))
	}
	again()
	const offer = firstServerID + 1
	ds.offer(offer, ownMime)
	ds.enter(4, ds.surface, offer, 1, 1)
	wantEvent(t, ds.win, platform.DragEvent{Kind: platform.DragEnter, X: 1, Y: 1, Own: true})
	end(platform.DragEndEvent{})

	// Nor is it just after the drag left a window, as the leave that the end
	// of a drag brings comes first on some compositors; but it is once the
	// drag has been away a while.
	for _, wait := range []int64{0, int64(leaveGrace/time.Second) + 1} {
		again()
		ds.offer(offer, ownMime)
		ds.enter(4, ds.surface, offer, 1, 1)
		wantEvent(t, ds.win, platform.DragEvent{Kind: platform.DragEnter, X: 1, Y: 1, Own: true})
		ds.event(deviceLeave)
		wantEvent(t, ds.win, platform.DragEvent{Kind: platform.DragLeave, X: 1, Y: 1, Own: true})
		at.Add(wait)
		end(platform.DragEndEvent{Outside: wait > 0})
	}
}

func TestNoDragWithoutADataDevice(t *testing.T) {
	cs := newClipSetup(t, false)
	if cs.win.StartDrag(nil, image.Point{}) {
		t.Error("a drag began without a data device")
	}
}
