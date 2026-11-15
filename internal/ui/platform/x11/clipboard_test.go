package x11

import (
	"strings"
	"testing"
	"time"

	"viewrab/internal/ui/platform"
)

// selRequest builds a SelectionRequest from requestor for selection as
// target into prop.
func selRequest(owner, requestor, selection, target, prop uint32) []byte {
	return xev(evSelectionRequest, func(b []byte) {
		order.PutUint32(b[4:], 7)
		order.PutUint32(b[8:], owner)
		order.PutUint32(b[12:], requestor)
		order.PutUint32(b[16:], selection)
		order.PutUint32(b[20:], target)
		order.PutUint32(b[24:], prop)
	})
}

// selNotify builds a SelectionNotify to requestor.
func selNotify(requestor, selection, target, prop uint32) []byte {
	return xev(evSelectionNotify, func(b []byte) {
		order.PutUint32(b[8:], requestor)
		order.PutUint32(b[12:], selection)
		order.PutUint32(b[16:], target)
		order.PutUint32(b[20:], prop)
	})
}

// notified returns the property of the n-th SelectionNotify sent to
// requestor, waiting until it is sent.
func notified(t *testing.T, f *fakeX, requestor uint32, n int) uint32 {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		var got []request
		for _, r := range f.requests(opSendEvent) {
			if r.u32(0) == requestor && r.body[8] == evSelectionNotify {
				got = append(got, r)
			}
		}
		if len(got) >= n {
			return u32At(got[n-1].body, 28)
		}
	}
	t.Fatal("no SelectionNotify")
	return 0
}

func TestClipboardOwnerAnswersRequests(t *testing.T) {
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{})
	clip := f.atom("CLIPBOARD")
	w.SetClipboard("hé ✓")
	f.mu.Lock()
	owner := f.owners[clip]
	f.mu.Unlock()
	if owner != b.selWin {
		t.Fatalf("owner %#x", owner)
	}
	const other = 0x5000
	n := 0
	ask := func(selection, target, prop uint32) uint32 {
		n++
		f.event(selRequest(b.selWin, other, selection, target, prop))
		return notified(t, f, other, n)
	}
	val := func(prop uint32) property { p, _ := f.prop(other, prop); return p }
	p := f.atom("P")
	if got := ask(clip, f.atom("TARGETS"), p); got != p || len(val(p).u32s()) != 6 || val(p).u32s()[1] != atomString {
		t.Errorf("TARGETS: %v %v", got, val(p).u32s())
	}
	if ask(clip, f.atom("UTF8_STRING"), p); string(val(p).value) != "hé ✓" || val(p).typ != f.atom("UTF8_STRING") {
		t.Errorf("UTF8_STRING: %q", val(p).value)
	}
	if ask(clip, atomString, atomNone); string(val(atomString).value) != "h\xe9 ?" {
		t.Errorf("STRING into the target: %q", val(atomString).value)
	}
	if got := ask(clip, f.atom("image/png"), p); got != atomNone {
		t.Errorf("unknown target answered with %d", got)
	}
	if got := ask(f.atom("PRIMARY"), f.atom("UTF8_STRING"), p); got != atomNone {
		t.Errorf("PRIMARY answered with %d", got)
	}
	// Another program taking the clipboard ends the ownership; a clear about
	// another selection or owner does not.
	f.event(xev(evSelectionClear, func(e []byte) { order.PutUint32(e[8:], b.selWin); order.PutUint32(e[12:], f.atom("PRIMARY")) }))
	f.event(xev(evSelectionClear, func(e []byte) { order.PutUint32(e[8:], 0x1); order.PutUint32(e[12:], clip) }))
	settle(t, f, w)
	if !owns(b) {
		t.Error("ownership lost to an unrelated clear")
	}
	f.event(xev(evSelectionClear, func(e []byte) { order.PutUint32(e[8:], b.selWin); order.PutUint32(e[12:], clip) }))
	settle(t, f, w)
	if got := ask(clip, f.atom("UTF8_STRING"), p); got != atomNone || owns(b) {
		t.Errorf("answered after losing the clipboard: %d", got)
	}
}

func TestClipboardClaimRetriesWithTheCurrentTime(t *testing.T) {
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{})
	f.event(inputEv(evKeyPress, 8, 99, w.id, 0, 0, 0, 0, 0))
	want[platform.KeyEvent](t, w)
	// The fake server ignores a claim with a time, so the backend retries it
	// with CurrentTime, 0.
	f.set(func(f *fakeX) {
		f.handle = func(f *fakeX, r request) bool { return r.op == opSetSelectionOwner && r.u32(8) != 0 }
	})
	w.SetClipboard("x")
	var times []uint32
	for _, r := range f.requests(opSetSelectionOwner) {
		times = append(times, r.u32(8))
	}
	if len(times) != 2 || times[0] != 99 || times[1] != 0 {
		t.Errorf("claims at %v", times)
	}
	// A server that never grants it, and one that fails the check.
	f.set(func(f *fakeX) {
		f.handle = func(f *fakeX, r request) bool {
			if r.op == opGetSelectionOwner {
				f.fail(r, 3, 0)
				return true
			}
			return r.op == opSetSelectionOwner
		}
	})
	w.SetClipboard("y")
	if n := len(f.requests(opSetSelectionOwner)); n != 3 {
		t.Errorf("claims after a failed check: %d", n)
	}
}

func TestClipboardSendsLargeTextInPieces(t *testing.T) {
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{})
	b.c.Setup.MaxRequest = 164 // pieces of 100 bytes
	text := strings.Repeat("0123456789", 25)
	w.SetClipboard(text)
	const other = 0x5000
	p := f.atom("P")
	f.event(selRequest(b.selWin, other, f.atom("CLIPBOARD"), f.atom("UTF8_STRING"), p))
	notified(t, f, other, 1)
	if v, _ := f.prop(other, p); v.typ != f.atom("INCR") || v.u32s()[0] != 250 {
		t.Fatalf("INCR announcement %+v", v)
	}
	for i := range 4 {
		f.event(propertyEv(other, p, true))
		f.waitRequest(func(request) bool { return countPieces(f, other) == i+1 })
	}
	var got string
	for _, r := range f.requests(opChangeProperty) {
		if r.u32(0) == other && r.u32(8) == f.atom("UTF8_STRING") {
			got += string(r.body[20 : 20+r.u32(16)])
		}
	}
	if got != text {
		t.Errorf("pieces make %q", got)
	}
	// The transfer is over: further deletions are ignored.
	f.event(propertyEv(other, p, true))
	settle(t, f, w)
	if n := countPieces(f, other); n != 4 {
		t.Errorf("%d pieces sent", n)
	}
}

// countPieces counts the pieces of text written to window.
func countPieces(f *fakeX, window uint32) int {
	n := 0
	for _, r := range f.requests(opChangeProperty) {
		if r.u32(0) == window && r.u32(8) == f.atom("UTF8_STRING") {
			n++
		}
	}
	return n
}

// consumed waits until the backend has read, and so deleted, window's
// property prop.
func consumed(t *testing.T, f *fakeX, window, prop uint32) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if _, ok := f.prop(window, prop); !ok {
			return
		}
	}
	t.Fatal("property never read")
}

// pasteServer has the fake server play the clipboard's owner: it answers
// each ConvertSelection with answer, which returns the property's type and
// value, or ok false to refuse.
func pasteServer(f *fakeX, answer func(target uint32) (typ uint32, value []byte, ok bool)) {
	f.set(func(f *fakeX) {
		f.handle = func(f *fakeX, r request) bool {
			if r.op != opConvertSelection {
				return false
			}
			requestor, selection, target, prop := r.u32(0), r.u32(4), r.u32(8), r.u32(12)
			typ, value, ok := answer(target)
			if !ok {
				prop = atomNone
			} else {
				f.setProp(requestor, prop, typ, 8, value)
			}
			f.event(selNotify(requestor, selection, target, prop))
			return true
		}
	})
}

func TestPaste(t *testing.T) {
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{})
	pasteServer(f, func(target uint32) (uint32, []byte, bool) {
		return target, []byte("pasted ✓"), target == f.atom("UTF8_STRING")
	})
	w.RequestPaste()
	if p := want[platform.PasteEvent](t, w); p.Text != "pasted ✓" {
		t.Errorf("paste %q", p.Text)
	}
	// An owner with Latin-1 only.
	pasteServer(f, func(target uint32) (uint32, []byte, bool) {
		return atomString, []byte("caf\xe9"), target == atomString
	})
	w.RequestPaste()
	if p := want[platform.PasteEvent](t, w); p.Text != "café" {
		t.Errorf("Latin-1 paste %q", p.Text)
	}
	// An owner with no text, or none to give.
	pasteServer(f, func(uint32) (uint32, []byte, bool) { return 0, nil, false })
	w.RequestPaste()
	quiet(t, f, w)
	pasteServer(f, func(target uint32) (uint32, []byte, bool) { return target, nil, true })
	w.RequestPaste()
	quiet(t, f, w)
	// The program's own text needs no owner.
	w.SetClipboard("mine")
	w.RequestPaste()
	if p := want[platform.PasteEvent](t, w); p.Text != "mine" {
		t.Errorf("own paste %q", p.Text)
	}
}

func TestPasteInPieces(t *testing.T) {
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{})
	prop := f.atom("VIEWRAB_SELECTION")
	pasteServer(f, func(uint32) (uint32, []byte, bool) { return f.atom("INCR"), propU32(10), true })
	w.RequestPaste()
	// Once the announcement is read, and so deleted, the pieces come, each
	// once the one before is read.
	reads(t, f, prop, 1)
	consumed(t, f, b.selWin, prop)
	for _, piece := range []string{"hello ", "world", ""} {
		f.setProp(b.selWin, prop, f.atom("UTF8_STRING"), 8, []byte(piece))
		f.event(propertyEv(b.selWin, prop, false))
		consumed(t, f, b.selWin, prop)
	}
	if p := want[platform.PasteEvent](t, w); p.Text != "hello world" {
		t.Errorf("paste %q", p.Text)
	}
	// Notifications about other selections or requestors, deletions, and
	// pieces with no paste under way are ignored.
	f.event(selNotify(0x999, f.atom("CLIPBOARD"), 0, prop))
	f.event(selNotify(b.selWin, f.atom("PRIMARY"), 0, prop))
	f.event(selNotify(b.selWin, f.atom("CLIPBOARD"), 0, prop))
	f.event(propertyEv(b.selWin, prop, false))
	f.event(propertyEv(b.selWin, prop, true))
	quiet(t, f, w)
}

// owns reports whether the program owns the clipboard.
func owns(b *Backend) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.clip.owned
}

// reads waits for the n-th read of the property prop.
func reads(t *testing.T, f *fakeX, prop uint32, n int) {
	t.Helper()
	f.waitRequest(func(request) bool {
		k := 0
		for _, r := range f.requests(opGetProperty) {
			if r.u32(4) == prop {
				k++
			}
		}
		return k >= n
	})
}

// pasting reports whether a paste is under way.
func pasting(b *Backend) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.clip.paste != nil
}

// countConversions counts the ConvertSelection requests.
func countConversions(f *fakeX) int { return len(f.requests(opConvertSelection)) }

func TestPasteFailures(t *testing.T) {
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{})
	prop := f.atom("VIEWRAB_SELECTION")
	failReads := func(after int) {
		reads := 0
		f.set(func(f *fakeX) {
			old := f.handle
			f.handle = func(f *fakeX, r request) bool {
				if r.op == opGetProperty && r.u32(4) == prop {
					if reads++; reads > after {
						f.fail(r, 3, 0)
						return true
					}
				}
				return old(f, r)
			}
		})
	}
	// The text cannot be read.
	pasteServer(f, func(target uint32) (uint32, []byte, bool) { return target, []byte("x"), true })
	failReads(0)
	w.RequestPaste()
	quiet(t, f, w)
	// A piece cannot be read.
	pasteServer(f, func(uint32) (uint32, []byte, bool) { return f.atom("INCR"), propU32(1), true })
	failReads(1)
	w.RequestPaste()
	reads(t, f, prop, 2) // the first case's, and the announcement
	consumed(t, f, b.selWin, prop)
	f.setProp(b.selWin, prop, f.atom("UTF8_STRING"), 8, []byte("x"))
	f.event(propertyEv(b.selWin, prop, false))
	quiet(t, f, w)
	if pasting(b) {
		t.Error("paste still under way")
	}
	// The owner never answers: the paste times out, and one asked for
	// meanwhile is not started.
	var fire func()
	override(t, &after, func(d time.Duration, fn func()) *time.Timer {
		fire = fn
		return time.NewTimer(time.Hour)
	})
	f.set(func(f *fakeX) { f.handle = func(f *fakeX, r request) bool { return r.op == opConvertSelection } })
	w.RequestPaste()
	f.waitRequest(func(request) bool { return countConversions(f) == 3 })
	w.RequestPaste()
	b.c.Sync()
	if n := countConversions(f); n != 3 {
		t.Errorf("conversions %d", n)
	}
	fire()
	fire() // a second expiry finds the paste over
	if pasting(b) {
		t.Error("paste not ended by the timeout")
	}
}
