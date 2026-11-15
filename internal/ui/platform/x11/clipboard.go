package x11

import (
	"time"
	"unicode/utf8"
	"viewer/internal/ui/platform/desktop"

	"viewer/internal/ui/platform"
)

// after is replaced by tests.
var after = time.AfterFunc

// clipboard is the CLIPBOARD selection: the text the program holds, when it
// owns the selection, the transfers of it under way to programs that paste
// it in pieces, and a paste in progress.
type clipboard struct {
	owned bool
	text  string

	// sending are the INCR transfers to other programs, by the window and
	// property each is written to.
	sending map[[2]uint32]*transfer

	// paste is the paste in progress, or nil.
	paste *paste
}

// transfer is clipboard text being sent in pieces.
type transfer struct {
	typ  uint32
	data []byte
}

// paste is a request for the clipboard's text on behalf of a window.
type paste struct {
	w      *window
	target uint32
	incr   bool   // the text comes in pieces
	data   []byte // the pieces so far
	timer  *time.Timer
}

// textTargets are the formats offered for the clipboard's text, best first.
var textTargets = []string{"UTF8_STRING", "text/plain;charset=utf-8", "TEXT", "text/plain"}

// SetClipboard implements platform.Window.
func (w *window) SetClipboard(text string) { w.b.setClipboard(text) }

func (b *Backend) setClipboard(text string) {
	b.mu.Lock()
	b.clip.owned, b.clip.text = true, text
	b.mu.Unlock()
	// The server ignores a claim older than the current owner's, which the
	// time of the latest event here can be; the current time then claims it.
	clip := b.atom("CLIPBOARD")
	for _, t := range []uint32{b.in.now(), 0} {
		b.c.Send(newReq(opSetSelectionOwner, 0).u32(b.selWin).u32(clip).u32(t))
		rep, err := b.c.Call(newReq(opGetSelectionOwner, 0).u32(clip))
		if err != nil || u32At(rep, 8) == b.selWin {
			return
		}
	}
}

// onSelectionClear handles another program taking the clipboard.
func (b *Backend) onSelectionClear(ev []byte) {
	if u32At(ev, 8) != b.selWin || u32At(ev, 12) != b.atom("CLIPBOARD") {
		return
	}
	b.mu.Lock()
	b.clip.owned, b.clip.text = false, ""
	b.mu.Unlock()
}

// notifySelection tells requestor the outcome of its request for target from
// selection: the data is in prop, or, when prop is zero, the request was
// refused.
func (b *Backend) notifySelection(t, requestor, selection, target, prop uint32) {
	ev := make([]byte, 32)
	ev[0] = evSelectionNotify
	order.PutUint32(ev[4:], t)
	order.PutUint32(ev[8:], requestor)
	order.PutUint32(ev[12:], selection)
	order.PutUint32(ev[16:], target)
	order.PutUint32(ev[20:], prop)
	b.c.Send(sendEvent(requestor, 0, ev))
}

// onSelectionRequest answers another program asking for the clipboard's
// text, or for the formats it is offered in.
func (b *Backend) onSelectionRequest(ev []byte) {
	t, requestor, selection, target, prop := u32At(ev, 4), u32At(ev, 12), u32At(ev, 16), u32At(ev, 20), u32At(ev, 24)
	if prop == atomNone {
		prop = target // as obsolete clients ask
	}
	b.mu.Lock()
	owned, text := b.clip.owned, b.clip.text
	b.mu.Unlock()
	if selection != b.atom("CLIPBOARD") || !owned {
		b.notifySelection(t, requestor, selection, target, atomNone)
		return
	}
	var typ uint32
	var data []byte
	format := byte(8)
	switch target {
	case b.atom("TARGETS"):
		typ, format = atomAtom, 32
		atoms := []uint32{b.atom("TARGETS"), atomString}
		for _, n := range textTargets {
			atoms = append(atoms, b.atom(n))
		}
		data = propU32(atoms...)
	case b.atom("UTF8_STRING"), b.atom("text/plain;charset=utf-8"):
		typ, data = target, []byte(text)
	case atomString, b.atom("TEXT"), b.atom("text/plain"):
		typ, data = atomString, latin1(text)
	default:
		b.notifySelection(t, requestor, selection, target, atomNone)
		return
	}
	if chunk := b.chunkSize(); len(data) > chunk {
		// Too large for one request: announce the size with INCR, and send the
		// text a piece at a time as the requestor deletes each.
		b.mu.Lock()
		if b.clip.sending == nil {
			b.clip.sending = map[[2]uint32]*transfer{}
		}
		b.clip.sending[[2]uint32{requestor, prop}] = &transfer{typ: typ, data: data}
		b.mu.Unlock()
		b.c.Send(changeAttrs(requestor, (&windowAttrs{}).set(cwEventMask, maskPropertyChange)))
		b.c.Send(changeProperty(propReplace, requestor, prop, b.atom("INCR"), 32, propU32(uint32(len(data)))))
	} else {
		b.c.Send(changeProperty(propReplace, requestor, prop, typ, format, data))
	}
	b.notifySelection(t, requestor, selection, target, prop)
}

// chunkSize is how much of a property one request may write.
func (b *Backend) chunkSize() int { return b.c.Setup.MaxRequest - 64 }

// latin1 converts text to Latin-1, with '?' for what it lacks.
func latin1(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 0xff {
			r = '?'
		}
		out = append(out, byte(r))
	}
	return out
}

// fromLatin1 converts Latin-1 text to UTF-8.
func fromLatin1(b []byte) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		out = utf8.AppendRune(out, rune(c))
	}
	return string(out)
}

// clipProperty advances the INCR transfers: a requestor deleting the property
// asks for the next piece of text, and a new value of the paste property is
// a piece of the text being pasted.
func (b *Backend) clipProperty(window, prop uint32, deleted bool) {
	b.mu.Lock()
	key := [2]uint32{window, prop}
	tr := b.clip.sending[key]
	p := b.clip.paste
	b.mu.Unlock()
	switch {
	case tr != nil && deleted:
		n := min(len(tr.data), b.chunkSize())
		piece := tr.data[:n]
		tr.data = tr.data[n:]
		b.c.Send(changeProperty(propReplace, window, prop, tr.typ, 8, piece))
		if n == 0 {
			// The empty piece that ends the transfer has gone out.
			b.mu.Lock()
			delete(b.clip.sending, key)
			b.mu.Unlock()
			b.c.Send(changeAttrs(window, (&windowAttrs{}).set(cwEventMask, 0)))
		}
	case p != nil && p.incr && !deleted && window == b.selWin && prop == b.atom("VIEWER_SELECTION"):
		v, err := b.c.getProperty(b.selWin, prop, true, desktop.MaxPaste)
		if err != nil {
			b.endPaste(p, nil)
			return
		}
		if len(v.value) == 0 {
			b.endPaste(p, p.data)
			return
		}
		p.timer.Reset(desktop.PasteTimeout)
		if len(p.data)+len(v.value) <= desktop.MaxPaste {
			p.data = append(p.data, v.value...)
		}
	}
}

// RequestPaste implements platform.Window.
func (w *window) RequestPaste() { w.b.requestPaste(w) }

func (b *Backend) requestPaste(w *window) {
	b.mu.Lock()
	owned, text := b.clip.owned, b.clip.text
	busy := b.clip.paste != nil
	b.mu.Unlock()
	if owned { // the program's own copy needs no round trip
		w.emit(platform.PasteEvent{Text: text})
		return
	}
	if busy {
		return
	}
	p := &paste{w: w, target: b.atom("UTF8_STRING")}
	b.mu.Lock()
	b.clip.paste = p
	p.timer = after(desktop.PasteTimeout, func() { b.endPaste(p, nil) })
	b.mu.Unlock()
	b.convert(p.target)
}

// convert asks the clipboard's owner for its text as target.
func (b *Backend) convert(target uint32) {
	b.c.Send(newReq(opConvertSelection, 0).u32(b.selWin).u32(b.atom("CLIPBOARD")).u32(target).
		u32(b.atom("VIEWER_SELECTION")).u32(b.in.now()))
}

// endPaste ends paste p, delivering data as its text unless it is empty.
func (b *Backend) endPaste(p *paste, data []byte) {
	b.mu.Lock()
	if b.clip.paste != p {
		b.mu.Unlock()
		return
	}
	b.clip.paste = nil
	b.mu.Unlock()
	p.timer.Stop()
	if len(data) == 0 {
		return
	}
	text := string(data)
	if p.target == atomString {
		text = fromLatin1(data)
	}
	p.w.emit(platform.PasteEvent{Text: text})
}

// onSelectionNotify handles the owner's answer to a request for a
// selection: the clipboard's text, or the files of a drop.
func (b *Backend) onSelectionNotify(ev []byte) {
	requestor, selection, prop := u32At(ev, 8), u32At(ev, 12), u32At(ev, 20)
	if selection == b.atom("XdndSelection") {
		b.drop.onSelection(b, requestor, prop)
		return
	}
	b.mu.Lock()
	p := b.clip.paste
	b.mu.Unlock()
	if requestor != b.selWin || selection != b.atom("CLIPBOARD") || p == nil {
		return
	}
	if prop == atomNone {
		// An owner that refuses UTF-8 may still have Latin-1.
		if p.target != atomString {
			p.target = atomString
			b.convert(atomString)
			return
		}
		b.endPaste(p, nil)
		return
	}
	v, err := b.c.getProperty(b.selWin, prop, true, desktop.MaxPaste)
	switch {
	case err != nil:
		b.endPaste(p, nil)
	case v.typ == b.atom("INCR"):
		// Deleting the property, as getProperty did, asks for the pieces.
		p.incr = true
	default:
		b.endPaste(p, v.value)
	}
}
