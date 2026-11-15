//go:build !windows

package x11

import (
	"image"
	"slices"
	"testing"
	"time"

	"viewrab/internal/ui/platform"
)

// xdnd delivers the XDND message typ to w, with data as its values.
func xdnd(f *fakeX, w *window, typ string, data ...uint32) {
	f.event(clientMessage(w.id, f.atom(typ), data...))
}

// sourceMessages returns the XDND messages of type typ sent to source, as
// their five data values.
func sourceMessages(f *fakeX, source uint32, typ string) [][5]uint32 {
	var out [][5]uint32
	for _, r := range f.requests(opSendEvent) {
		if r.u32(0) == source && r.body[8] == evClientMessage && u32At(r.body, 16) == f.atom(typ) {
			var m [5]uint32
			for i := range m {
				m[i] = u32At(r.body, 20+4*i)
			}
			out = append(out, m)
		}
	}
	return out
}

// dropServer has the fake server translate window positions to the root by
// an offset of (100, 200), and answer requests for dropped files with list,
// or refuse them when list is empty.
func dropServer(f *fakeX, list string) {
	f.set(func(f *fakeX) {
		f.translate = func(src, dst uint32, x, y int) (int, int, uint32) {
			if dst == fakeRoot {
				return x + 100, y + 200, 0
			}
			return x, y, 0
		}
		f.handle = func(f *fakeX, r request) bool {
			if r.op != opConvertSelection {
				return false
			}
			requestor, selection, target, prop := r.u32(0), r.u32(4), r.u32(8), r.u32(12)
			if list == "" {
				prop = atomNone
			} else {
				f.setProp(requestor, prop, target, 8, []byte(list))
			}
			f.event(selNotify(requestor, selection, target, prop))
			return true
		}
	})
}

const dragSource = 0x6000

func TestXdndDropOfFiles(t *testing.T) {
	override(t, &hostname, func() (string, error) { return "here", nil })
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{})
	dropServer(f, "file:///tmp/a.pdf\r\nfile://here/b.pdf\r\nfile://far/c.pdf\r\n")
	uris := f.atom(uriListMimeName)
	xdnd(f, w, "XdndEnter", dragSource, 5<<24, f.atom("text/plain"), uris)
	xdnd(f, w, "XdndPosition", dragSource, 0, 140<<16|250, 0, f.atom("XdndActionCopy"))
	if d := want[platform.DragEvent](t, w); d.Kind != platform.DragEnter || d.X != 40 || d.Y != 50 || d.Own {
		t.Errorf("enter = %+v", d)
	}
	xdnd(f, w, "XdndPosition", dragSource, 0, 150<<16|260, 0, 0)
	if d := want[platform.DragEvent](t, w); d.Kind != platform.DragMove || d.X != 50 || d.Y != 60 {
		t.Errorf("move = %+v", d)
	}
	if n := len(f.requests(opTranslateCoordinates)); n != 1 {
		t.Errorf("translated %d times", n)
	}
	f.waitRequest(func(request) bool { return len(sourceMessages(f, dragSource, "XdndStatus")) == 2 })
	status := sourceMessages(f, dragSource, "XdndStatus")
	if len(status) != 2 || status[0] != [5]uint32{w.id, 3, 0, 0, f.atom("XdndActionCopy")} {
		t.Errorf("status %v", status)
	}
	xdnd(f, w, "XdndDrop", dragSource, 0, 77)
	d := want[platform.DropEvent](t, w)
	if !slices.Equal(d.Paths, []string{"/tmp/a.pdf", "/b.pdf"}) || d.X != 50 || d.Own {
		t.Errorf("drop = %+v", d)
	}
	r := f.waitRequest(func(r request) bool { return r.op == opConvertSelection })
	if r.u32(0) != w.id || r.u32(4) != f.atom("XdndSelection") || r.u32(8) != uris || r.u32(16) != 77 {
		t.Errorf("conversion %v", r.body)
	}
	f.waitRequest(func(request) bool { return len(sourceMessages(f, dragSource, "XdndFinished")) == 1 })
	if fin := sourceMessages(f, dragSource, "XdndFinished"); fin[0] != [5]uint32{w.id, 1, f.atom("XdndActionCopy")} {
		t.Errorf("finished %v", fin)
	}
}

// uriListMimeName is the atom name of the file list format.
const uriListMimeName = "text/uri-list"

func TestXdndDropsWithoutFiles(t *testing.T) {
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{})
	uris := f.atom(uriListMimeName)
	finished := func(n int) [5]uint32 {
		f.waitRequest(func(request) bool { return len(sourceMessages(f, dragSource, "XdndFinished")) == n })
		return sourceMessages(f, dragSource, "XdndFinished")[n-1]
	}
	drop := func() {
		xdnd(f, w, "XdndEnter", dragSource, 5<<24, uris)
		xdnd(f, w, "XdndPosition", dragSource, 0, 0, 0, 0)
		want[platform.DragEvent](t, w)
		xdnd(f, w, "XdndDrop", dragSource, 0, 0)
	}
	for i, list := range []string{"http://example.org/a.pdf", ""} {
		dropServer(f, list)
		drop()
		if d := want[platform.DragEvent](t, w); d.Kind != platform.DragLeave {
			t.Errorf("drop %d: %+v", i, d)
		}
		if fin := finished(i + 1); fin[1] != 0 {
			t.Errorf("drop %d taken", i)
		}
	}
	// The files cannot be read.
	dropServer(f, "file:///a.pdf")
	f.set(func(f *fakeX) {
		old := f.handle
		f.handle = func(f *fakeX, r request) bool {
			if r.op == opGetProperty && r.u32(4) == f.atom("XdndSelection") {
				f.fail(r, 3, 0)
				return true
			}
			return old(f, r)
		}
	})
	drop()
	want[platform.DragEvent](t, w)
	finished(3)
}

func TestXdndRefusesOtherData(t *testing.T) {
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{})
	dropServer(f, "")
	// More than three types, listed on the source window, none of them files.
	f.setProp(dragSource, f.atom("XdndTypeList"), atomAtom, 32, propU32(f.atom("text/plain"), f.atom("a"), f.atom("b"), f.atom("c")))
	xdnd(f, w, "XdndEnter", dragSource, 5<<24|1, f.atom(uriListMimeName))
	xdnd(f, w, "XdndPosition", dragSource, 0, 0, 0, 0)
	xdnd(f, w, "XdndDrop", dragSource, 0, 0)
	f.waitRequest(func(request) bool { return len(sourceMessages(f, dragSource, "XdndFinished")) == 1 })
	if s := sourceMessages(f, dragSource, "XdndStatus"); len(s) != 1 || s[0][1] != 2 || s[0][4] != 0 {
		t.Errorf("status %v", s)
	}
	quiet(t, f, w)
	// When the type list cannot be read, the three types in the message are
	// used.
	f.set(func(f *fakeX) {
		f.handle = func(f *fakeX, r request) bool {
			if r.op == opGetProperty && r.u32(4) == f.atom("XdndTypeList") {
				f.fail(r, 3, 0)
				return true
			}
			return false
		}
	})
	xdnd(f, w, "XdndEnter", dragSource, 5<<24|1, f.atom(uriListMimeName))
	xdnd(f, w, "XdndPosition", dragSource, 0, 0, 0, 0)
	if d := want[platform.DragEvent](t, w); d.Kind != platform.DragEnter {
		t.Errorf("enter = %+v", d)
	}
}

func TestXdndIgnoresStrayMessages(t *testing.T) {
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{})
	dropServer(f, "file:///a.pdf")
	uris := f.atom(uriListMimeName)
	xdnd(f, w, "XdndPosition", dragSource, 0, 0, 0, 0) // before any enter
	xdnd(f, w, "XdndDrop", dragSource, 0, 0)
	xdnd(f, w, "XdndLeave", dragSource)
	xdnd(f, w, "XdndUnknown", dragSource)
	f.event(selNotify(w.id, f.atom("XdndSelection"), uris, uris)) // no drop under way
	quiet(t, f, w)
	xdnd(f, w, "XdndEnter", dragSource, 5<<24, uris)
	xdnd(f, w, "XdndLeave", dragSource) // left before moving: no DragEnter went out
	quiet(t, f, w)
	xdnd(f, w, "XdndEnter", dragSource, 5<<24, uris)
	xdnd(f, w, "XdndPosition", dragSource+1, 0, 0, 0, 0) // another source
	xdnd(f, w, "XdndLeave", dragSource+1)
	xdnd(f, w, "XdndDrop", dragSource+1, 0, 0)
	quiet(t, f, w)
	xdnd(f, w, "XdndPosition", dragSource, 0, 0, 0, 0)
	want[platform.DragEvent](t, w)
	xdnd(f, w, "XdndLeave", dragSource)
	if d := want[platform.DragEvent](t, w); d.Kind != platform.DragLeave {
		t.Errorf("leave = %+v", d)
	}
	// A window whose position cannot be found takes no part.
	f.set(func(f *fakeX) {
		f.handle = func(f *fakeX, r request) bool {
			if r.op == opTranslateCoordinates {
				f.fail(r, 3, 0)
				return true
			}
			return false
		}
	})
	xdnd(f, w, "XdndEnter", dragSource, 5<<24, uris)
	xdnd(f, w, "XdndPosition", dragSource, 0, 0, 0, 0)
	quiet(t, f, w)
}

func TestXdndDropTimesOut(t *testing.T) {
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{})
	timers := make(chan func(), 1)
	override(t, &after, func(d time.Duration, fn func()) *time.Timer {
		timers <- fn
		return time.NewTimer(time.Hour)
	})
	f.set(func(f *fakeX) { f.handle = func(f *fakeX, r request) bool { return r.op == opConvertSelection } })
	uris := f.atom(uriListMimeName)
	xdnd(f, w, "XdndEnter", dragSource, 5<<24, uris)
	xdnd(f, w, "XdndPosition", dragSource, 0, 0, 0, 0)
	want[platform.DragEvent](t, w)
	xdnd(f, w, "XdndDrop", dragSource, 0, 0)
	xdnd(f, w, "XdndDrop", dragSource, 0, 0) // a second drop is ignored
	f.waitRequest(func(r request) bool { return r.op == opConvertSelection })
	fire := <-timers
	settle(t, f, w)
	fire()
	if d := want[platform.DragEvent](t, w); d.Kind != platform.DragLeave {
		t.Errorf("timeout = %+v", d)
	}
	f.waitRequest(func(request) bool { return len(sourceMessages(f, dragSource, "XdndFinished")) == 1 })
	fire() // already over
	if n := len(f.requests(opConvertSelection)); n != 1 {
		t.Errorf("conversions %d", n)
	}
}

// dragWorld has the fake server lay out two windows of the program: a at
// (100, 200) inside a window manager's frame, b at (500, 200) without one,
// with the frame covering root x below 400 and b x from 400 to 800.
func dragWorld(f *fakeX, a, b *window) {
	const frame = 0x8001
	f.set(func(f *fakeX) {
		f.parents[a.id] = frame
		f.pointer = [2]int16{140, 250}
		f.translate = func(src, dst uint32, x, y int) (int, int, uint32) {
			switch {
			case src == a.id && dst == fakeRoot:
				return x + 100, y + 200, 0
			case src == b.id && dst == fakeRoot:
				return x + 500, y + 200, 0
			case x < 400:
				return x, y, frame
			case x < 800:
				return x, y, b.id
			}
			return x, y, 0
		}
	})
}

func TestOwnDragAcrossWindows(t *testing.T) {
	f, b := startBackend(t, nil)
	wa := openWindow(t, f, b, platform.WindowOptions{})
	wb := openWindow(t, f, b, platform.WindowOptions{})
	dragWorld(f, wa, wb)
	icon := image.NewRGBA(image.Rect(0, 0, 8, 4))
	if !wa.StartDrag(icon, image.Pt(2, 1)) {
		t.Fatal("StartDrag failed")
	}
	if wa.StartDrag(nil, image.Point{}) {
		t.Error("a second drag began")
	}
	if d := want[platform.DragEvent](t, wa); d.Kind != platform.DragEnter || d.X != 40 || d.Y != 50 || !d.Own {
		t.Errorf("enter a = %+v", d)
	}
	created := f.requests(opCreateWindow)
	iconWin := created[len(created)-1]
	if iconWin.data != 32 || i16At(iconWin.body, 8) != 138 || i16At(iconWin.body, 10) != 249 {
		t.Errorf("icon window depth %d at %d,%d", iconWin.data, i16At(iconWin.body, 8), i16At(iconWin.body, 10))
	}
	if len(f.requests(129)) != 1 { // SHAPE: no input region
		t.Error("icon takes input")
	}
	// Crossing events during the drag are not the pointer's.
	f.event(crossEv(evLeaveNotify, 0, wa.id, 1, 2, 0, notifyNormal))
	f.events(inputEv(evMotionNotify, 0, 40, wa.id, 300, 260, 0, 0, 0), inputEv(evMotionNotify, 0, 40, wa.id, 520, 260, 0, 0, 0))
	if d := want[platform.DragEvent](t, wa); d.Kind != platform.DragLeave {
		t.Errorf("leave a = %+v", d)
	}
	if d := want[platform.DragEvent](t, wb); d.Kind != platform.DragEnter || d.X != 20 || d.Y != 60 {
		t.Errorf("enter b = %+v", d)
	}
	f.event(inputEv(evMotionNotify, 0, 41, wa.id, 530, 270, 0, 0, 0))
	if d := want[platform.DragEvent](t, wb); d.Kind != platform.DragMove || d.X != 30 {
		t.Errorf("move b = %+v", d)
	}
	f.event(inputEv(evButtonPress, 3, 42, wa.id, 530, 270, 0, 0, 0))   // another button: ignored
	f.event(inputEv(evKeyPress, 8, 42, wa.id, 0, 0, 0, 0, 0))          // a key other than Escape
	f.event(inputEv(evKeyRelease, 11, 42, wa.id, 0, 0, 0, 0, 0))       // Escape's release
	f.event(inputEv(evButtonRelease, 1, 43, wa.id, 540, 270, 0, 0, 0)) // the drop
	if d := want[platform.DragEvent](t, wb); d.Kind != platform.DragMove || d.X != 40 {
		t.Errorf("last move = %+v", d)
	}
	if d := want[platform.DropEvent](t, wb); !d.Own || d.X != 40 || d.Y != 70 {
		t.Errorf("drop = %+v", d)
	}
	if e := want[platform.DragEndEvent](t, wa); !e.Dropped || e.Outside {
		t.Errorf("end = %+v", e)
	}
	settle(t, f, wa)
	if b.drag.active() || len(f.requests(opUngrabPointer)) != 1 || len(f.requests(opUngrabKeyboard)) != 1 {
		t.Error("drag not ended")
	}
	if n := len(f.requests(opDestroyWindow)); n != 1 {
		t.Errorf("icon windows destroyed: %d", n)
	}
	quiet(t, f, wa)
	quiet(t, f, wb)
}

func TestOwnDragEndsOutsideOrCancelled(t *testing.T) {
	f, b := startBackend(t, nil)
	wa := openWindow(t, f, b, platform.WindowOptions{})
	wb := openWindow(t, f, b, platform.WindowOptions{})
	dragWorld(f, wa, wb)
	// No icon, dropped away from every window.
	wa.StartDrag(nil, image.Point{})
	want[platform.DragEvent](t, wa)
	f.event(inputEv(evMotionNotify, 0, 40, wa.id, 900, 10, 0, 0, 0))
	want[platform.DragEvent](t, wa) // leave
	f.event(inputEv(evButtonRelease, 1, 41, wa.id, 900, 10, 0, 0, 0))
	if e := want[platform.DragEndEvent](t, wa); e.Dropped || !e.Outside {
		t.Errorf("outside = %+v", e)
	}
	// Escape over a window, then over none.
	b.argb, b.shape.present = 0, false // an opaque icon that takes input
	wa.StartDrag(image.NewRGBA(image.Rect(0, 0, 2, 2)), image.Point{})
	want[platform.DragEvent](t, wa)
	f.event(inputEv(evKeyPress, 11, 50, wa.id, 0, 0, 0, 0, 0))
	if d := want[platform.DragEvent](t, wa); d.Kind != platform.DragLeave {
		t.Errorf("cancel leave = %+v", d)
	}
	if e := want[platform.DragEndEvent](t, wa); e.Dropped || e.Outside {
		t.Errorf("cancel over a window = %+v", e)
	}
	created := f.requests(opCreateWindow)
	if icon := created[len(created)-1]; icon.data != 24 || len(f.requests(129)) != 0 {
		t.Errorf("opaque icon depth %d", icon.data)
	}
	f.set(func(f *fakeX) { f.pointer = [2]int16{900, 10} })
	wa.StartDrag(nil, image.Point{})
	f.event(inputEv(evKeyPress, 11, 51, wa.id, 0, 0, 0, 0, 0))
	if e := want[platform.DragEndEvent](t, wa); !e.Outside {
		t.Errorf("cancel outside = %+v", e)
	}
	// Ending or cancelling a drag that is over does nothing.
	b.drag.cancel(b)
	b.drag.onPointer(b, evButtonRelease, 0, 0)
	quiet(t, f, wa)
}

func TestStartDragFailures(t *testing.T) {
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{})
	f.set(func(f *fakeX) { f.grab = 1 }) // AlreadyGrabbed
	if w.StartDrag(nil, image.Point{}) {
		t.Error("drag began without the pointer")
	}
	f.set(func(f *fakeX) {
		f.grab = 0
		f.handle = func(f *fakeX, r request) bool {
			if r.op == opGrabPointer {
				f.fail(r, 3, 0)
				return true
			}
			return false
		}
	})
	if w.StartDrag(nil, image.Point{}) {
		t.Error("drag began after a refused grab")
	}
	// The pointer's place, the window tree and the windows' places all
	// unknown: the drag goes on over no window.
	f.set(func(f *fakeX) {
		f.parents[w.id] = w.id // a tree that never reaches the root
		f.handle = func(f *fakeX, r request) bool {
			switch r.op {
			case opQueryPointer, opTranslateCoordinates:
				f.fail(r, 3, 0)
				return true
			}
			return false
		}
	})
	if !w.StartDrag(nil, image.Point{}) {
		t.Fatal("StartDrag failed")
	}
	if len(b.drag.spots) != 0 {
		t.Errorf("spots %v", b.drag.spots)
	}
	f.event(inputEv(evButtonRelease, 1, 60, w.id, 0, 0, 0, 0, 0))
	if e := want[platform.DragEndEvent](t, w); !e.Outside {
		t.Errorf("end = %+v", e)
	}
	// A tree that cannot be read.
	f.set(func(f *fakeX) {
		f.handle = func(f *fakeX, r request) bool {
			if r.op == opQueryTree {
				f.fail(r, 3, 0)
				return true
			}
			return false
		}
	})
	if b.topLevel(w.id) != 0 {
		t.Error("top level of an unreadable tree")
	}
}
