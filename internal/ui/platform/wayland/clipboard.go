package wayland

import (
	"io"
	"os"
	"sync"
	"time"

	"viewer/internal/ui/platform"
)

// Opcodes and events of wl_data_device_manager, wl_data_device, wl_data_source
// and wl_data_offer.
const (
	dataManagerCreateSource = 0
	dataManagerGetDevice    = 1

	deviceSetSelection = 1
	deviceDataOffer    = 0
	deviceSelection    = 5

	sourceOffer   = 0
	sourceDestroy = 1
	sourceSend    = 1
	sourceCancel  = 2

	offerReceive = 1
	offerDestroy = 2
	offerOffer   = 0
)

// pipe is replaced by tests to force its failure.
var pipe = os.Pipe

// textMimes are the clipboard formats the client offers and accepts for text,
// best first.
var textMimes = []string{"text/plain;charset=utf-8", "text/plain", "UTF8_STRING", "STRING"}

// maxPaste bounds how much clipboard text is read, and pasteTimeout how long
// the program that holds it has to supply it.
const (
	maxPaste     = 8 << 20
	pasteTimeout = 2 * time.Second
)

// clipboard is the seat's selection: the offer other programs hold, which can
// be pasted, and the source the client holds, which they can paste from.
type clipboard struct {
	mu     sync.Mutex
	device uint32              // the wl_data_device, or zero
	source uint32              // the client's wl_data_source, or zero
	text   string              // what the source supplies
	offer  uint32              // the current selection offered by another program
	mimes  map[uint32][]string // the formats each offer lists
}

// onDataDevice handles the data device's events: offers announced, the
// selection naming which one is the clipboard's, and drags, which onDrag
// handles.
func (b *Backend) onDataDevice(opcode uint16, d *Decoder) {
	cb := &b.clip
	switch opcode {
	case deviceDataOffer:
		id := d.Uint()
		b.c.Handle(id, func(opcode uint16, d *Decoder) {
			switch opcode {
			case offerOffer:
				mime := d.String()
				cb.mu.Lock()
				cb.mimes[id] = append(cb.mimes[id], mime)
				cb.mu.Unlock()
			case offerAction:
				b.onOfferAction(id, d)
			}
		})
	case deviceSelection:
		id := d.Uint()
		cb.mu.Lock()
		old := cb.offer
		cb.offer = id
		delete(cb.mimes, old)
		cb.mu.Unlock()
		if old != 0 {
			b.c.Send(old, offerDestroy, nil)
		}
	default:
		b.onDrag(opcode, d)
	}
}

// SetClipboard implements platform.Window.
func (w *window) SetClipboard(text string) { w.b.setClipboard(text) }

func (b *Backend) setClipboard(text string) {
	cb := &b.clip
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.device == 0 || b.dataManager == 0 {
		return
	}
	old := cb.source
	src := b.c.NewID(b.onDataSource)
	cb.source, cb.text = src, text
	b.c.Send(b.dataManager, dataManagerCreateSource, func(e *Encoder) { e.Uint(src) })
	for _, m := range textMimes {
		b.c.Send(src, sourceOffer, func(e *Encoder) { e.String(m) })
	}
	b.c.Send(cb.device, deviceSetSelection, func(e *Encoder) { e.Uint(src).Uint(b.in.lastSerial()) })
	if old != 0 {
		b.c.Send(old, sourceDestroy, nil)
	}
}

// onDataSource handles requests for the client's clipboard text and the
// compositor taking the selection away.
func (b *Backend) onDataSource(opcode uint16, d *Decoder) {
	cb := &b.clip
	switch opcode {
	case sourceSend:
		_, fd := d.String(), d.FD()
		cb.mu.Lock()
		text := cb.text
		cb.mu.Unlock()
		f := os.NewFile(uintptr(fd), "clipboard")
		// The reader may be slow, so the dispatching goroutine does not wait.
		go func() {
			defer f.Close()
			f.SetWriteDeadline(time.Now().Add(pasteTimeout))
			io.WriteString(f, text)
		}()
	case sourceCancel:
		cb.mu.Lock()
		src := cb.source
		cb.source, cb.text = 0, ""
		cb.mu.Unlock()
		if src != 0 {
			b.c.Send(src, sourceDestroy, nil)
		}
	}
}

// RequestPaste implements platform.Window.
func (w *window) RequestPaste() { w.b.requestPaste(w) }

func (b *Backend) requestPaste(w *window) {
	cb := &b.clip
	cb.mu.Lock()
	offer, mimes, own := cb.offer, cb.mimes[cb.offer], cb.source != 0
	text := cb.text
	cb.mu.Unlock()
	if own { // the client's own copy needs no round trip
		w.emit(platform.PasteEvent{Text: text})
		return
	}
	mime := ""
	for _, m := range textMimes {
		for _, have := range mimes {
			if have == m && mime == "" {
				mime = m
			}
		}
	}
	if offer == 0 || mime == "" {
		return
	}
	r, wr, err := pipe()
	if err != nil {
		return
	}
	b.c.Send(offer, offerReceive, func(e *Encoder) { e.String(mime).FD(int(wr.Fd())) })
	wr.Close()
	go func() {
		defer r.Close()
		r.SetReadDeadline(time.Now().Add(pasteTimeout))
		data, err := io.ReadAll(io.LimitReader(r, maxPaste))
		if err == nil && len(data) > 0 {
			w.emit(platform.PasteEvent{Text: string(data)})
		}
	}()
}
