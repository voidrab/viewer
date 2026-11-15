package x11

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"
)

// request is one request the fake server received.
type request struct {
	op, data byte
	body     []byte // the request after its four-byte header
	seq      uint16
	fds      []int
}

func (r request) u32(off int) uint32 { return u32At(r.body, off) }
func (r request) u16(off int) uint16 { return u16At(r.body, off) }

// fakeX is a scripted X server on the far end of a socket pair. It answers
// the requests the backend makes on starting and keeps window properties;
// a test's handle function sees each request first and may answer it
// itself.
type fakeX struct {
	t    *testing.T
	sock *net.UnixConn

	wmu sync.Mutex // serialises writes

	mu        sync.Mutex
	seq       uint16
	atoms     map[string]uint32
	atomNames map[uint32]string
	props     map[[2]uint32]property
	owners    map[uint32]uint32 // selection -> owner
	log       []request
	logCh     chan request // if set, every request is also sent here

	// What the server reports.
	exts     map[string]extension
	keysyms  []uint32 // keysyms for the key codes from minKeycode, per code
	per      int
	modmap   [8][]byte
	fontless bool // ListFonts finds no cursor font
	formats  []renderFormat
	shmMinor uint16
	grab     byte // GrabPointer's status
	pointer  [2]int16
	// translate answers TranslateCoordinates, given the source, destination
	// and point; nil answers with the point unchanged and no child.
	translate func(src, dst uint32, x, y int) (int, int, uint32)

	handle func(f *fakeX, r request) bool
	done   chan struct{}
}

// renderFormat is a RENDER picture format the server lists.
type renderFormat struct {
	id    uint32
	depth byte
	// shifts and masks of red, green, blue and alpha
	shifts, masks [4]uint16
}

// argbFormat is the format the backend looks for.
var argbFormat = renderFormat{id: 0x99, depth: 32, shifts: [4]uint16{16, 8, 0, 24}, masks: [4]uint16{0xff, 0xff, 0xff, 0xff}}

// setupOpts shapes the server's setup reply.
type setupOpts struct {
	refuse     string // refuse the connection with this reason
	byteOrder  byte
	maxReq     uint16 // in four-byte units
	rootVisual uint32
	rootDepth  byte
	visuals    []Visual
	formats    []Format
	screens    int
}

// defaultSetup is a common 24-bit TrueColor screen with a 32-bit ARGB
// visual.
func defaultSetup() setupOpts {
	return setupOpts{
		maxReq:     65535,
		rootVisual: 0x21,
		rootDepth:  24,
		visuals: []Visual{
			{ID: 0x21, Class: visualTrueColor, Depth: 24, RedMask: 0xff0000, GreenMask: 0xff00, BlueMask: 0xff},
			{ID: 0x22, Class: visualTrueColor, Depth: 32, RedMask: 0xff0000, GreenMask: 0xff00, BlueMask: 0xff},
		},
		formats: []Format{{Depth: 1, BitsPerPixel: 1, ScanlinePad: 32}, {Depth: 24, BitsPerPixel: 32, ScanlinePad: 32}, {Depth: 32, BitsPerPixel: 32, ScanlinePad: 32}},
		screens: 1,
	}
}

// fakeRoot is the root window of the fake server's screen.
const fakeRoot = 0x100

// socketPair returns two connected unix sockets.
func socketPair(t *testing.T) (a, b *net.UnixConn) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(fd int) *net.UnixConn {
		f := os.NewFile(uintptr(fd), "sock")
		defer f.Close()
		c, err := net.FileConn(f)
		if err != nil {
			t.Fatal(err)
		}
		return c.(*net.UnixConn)
	}
	return mk(fds[0]), mk(fds[1])
}

// newFakeX starts a fake server and returns it with the client's end of the
// socket, before the connection setup.
func newFakeX(t *testing.T, opts setupOpts) (*fakeX, *net.UnixConn) {
	t.Helper()
	client, server := socketPair(t)
	f := &fakeX{
		t: t, sock: server,
		atoms: map[string]uint32{}, atomNames: map[uint32]string{},
		props: map[[2]uint32]property{}, owners: map[uint32]uint32{},
		exts: map[string]extension{
			"MIT-SHM": {present: true, major: 130, firstEvent: 65},
			"SHAPE":   {present: true, major: 129, firstEvent: 64},
			"RENDER":  {present: true, major: 139},
		},
		per:      2,
		formats:  []renderFormat{{id: 0x98, depth: 24}, argbFormat},
		shmMinor: 2,
		done:     make(chan struct{}),
	}
	// Key codes 8 to 15: a, b, Return, Escape, Shift_L, Num_Lock, KP_1, ISO_Level3_Shift.
	f.keysyms = []uint32{'a', 'A', 'b', 'B', 0xff0d, 0, 0xff1b, 0, 0xffe1, 0, ksNumLock, 0, 0xff9c, 0xffb1, ksLevel3, 0}
	f.modmap[0] = []byte{12} // Shift
	f.modmap[4] = []byte{13} // Mod2: Num_Lock
	f.modmap[7] = []byte{15} // Mod5: ISO_Level3_Shift
	go f.serve(opts)
	t.Cleanup(func() { server.Close(); <-f.done })
	return f, client
}

// startFake starts a fake server and connects to it.
func startFake(t *testing.T, opts setupOpts) (*fakeX, *Conn) {
	t.Helper()
	f, sock := newFakeX(t, opts)
	c, err := NewConn(sock, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return f, c
}

// startBackend starts a fake server, connects to it and opens a backend.
func startBackend(t *testing.T, f func(*fakeX)) (*fakeX, *Backend) {
	t.Helper()
	fx, sock := newFakeX(t, defaultSetup())
	if f != nil {
		f(fx)
	}
	c, err := NewConn(sock, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(c)
	if err != nil {
		c.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return fx, b
}

// serve answers the setup and then every request until the socket closes.
func (f *fakeX) serve(opts setupOpts) {
	defer close(f.done)
	head := make([]byte, 12)
	if _, err := io.ReadFull(f.sock, head); err != nil {
		return
	}
	n := pad4(int(u16At(head, 6))) + pad4(int(u16At(head, 8)))
	if _, err := io.ReadFull(f.sock, make([]byte, n)); err != nil {
		return
	}
	f.write(setupReply(opts))
	if opts.refuse != "" {
		return
	}
	var buf []byte
	var fds []int
	for {
		for len(buf) >= 4 {
			size := 4 * int(u16At(buf, 2))
			if size == 0 || len(buf) < size {
				break
			}
			f.mu.Lock()
			f.seq++
			r := request{op: buf[0], data: buf[1], body: bytes.Clone(buf[4:size]), seq: f.seq, fds: fds}
			f.log = append(f.log, r)
			ch, handle := f.logCh, f.handle
			f.mu.Unlock()
			fds = nil
			buf = buf[size:]
			if ch != nil {
				ch <- r
			}
			if handle == nil || !handle(f, r) {
				f.answer(r)
			}
		}
		p := make([]byte, 1<<16)
		oob := make([]byte, syscall.CmsgSpace(4*8))
		n, oobn, _, _, err := f.sock.ReadMsgUnix(p, oob)
		if msgs, err := syscall.ParseSocketControlMessage(oob[:oobn]); err == nil {
			for _, m := range msgs {
				got, _ := syscall.ParseUnixRights(&m)
				fds = append(fds, got...)
			}
		}
		buf = append(buf, p[:max(n, 0)]...)
		if err != nil {
			return
		}
	}
}

// setupReply encodes the server's answer to the setup request.
func setupReply(o setupOpts) []byte {
	if o.refuse != "" {
		r := []byte{0, byte(len(o.refuse)), 11, 0, 0, 0, 0, 0}
		body := make([]byte, pad4(len(o.refuse)))
		copy(body, o.refuse)
		order.PutUint16(r[6:], uint16(len(body)/4))
		return append(r, body...)
	}
	vendor := "fake"
	b := (&req{}).u32(0).u32(0x200000).u32(0x1fffff).u32(0).u16(uint16(len(vendor))).u16(o.maxReq).
		u8(byte(o.screens)).u8(byte(len(o.formats))).u8(o.byteOrder).u8(0).u8(32).u8(32).u8(8).u8(15).pad(4).bytes([]byte(vendor))
	for _, fm := range o.formats {
		b.u8(fm.Depth).u8(fm.BitsPerPixel).u8(fm.ScanlinePad).pad(5)
	}
	depths := map[byte][]Visual{}
	var order_ []byte
	for _, v := range o.visuals {
		if _, ok := depths[v.Depth]; !ok {
			order_ = append(order_, v.Depth)
		}
		depths[v.Depth] = append(depths[v.Depth], v)
	}
	for s := range o.screens {
		b.u32(fakeRoot + uint32(s)).u32(0x20).u32(0xffffff).u32(0).u32(0).u16(1920).u16(1080).u16(500).u16(300).
			u16(1).u16(1).u32(o.rootVisual).u8(0).u8(0).u8(o.rootDepth).u8(byte(len(order_)))
		for _, d := range order_ {
			b.u8(d).pad(1).u16(uint16(len(depths[d]))).pad(4)
			for _, v := range depths[d] {
				b.u32(v.ID).u8(v.Class).u8(8).u16(256).u32(v.RedMask).u32(v.GreenMask).u32(v.BlueMask).pad(4)
			}
		}
	}
	head := []byte{1, 0, 11, 0, 0, 0, 0, 0}
	order.PutUint16(head[6:], uint16(len(b.b)/4))
	return append(head, b.b...)
}

func (f *fakeX) write(b []byte) {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	if _, err := f.sock.Write(b); err != nil && !errors.Is(err, net.ErrClosed) && !errors.Is(err, syscall.EPIPE) {
		f.t.Errorf("fake server write: %v", err)
	}
}

// reply answers r: data is the reply's second byte and fields the bytes from
// offset 8 on, padded to the 32 bytes every reply has at least.
func (f *fakeX) reply(r request, data byte, fields []byte) {
	if len(fields) < 24 {
		fields = append(fields, make([]byte, 24-len(fields))...)
	}
	fields = append(fields, make([]byte, pad4(len(fields))-len(fields))...)
	p := []byte{packetReply, data, 0, 0, 0, 0, 0, 0}
	order.PutUint16(p[2:], r.seq)
	order.PutUint32(p[4:], uint32((len(fields)-24)/4))
	f.write(append(p, fields...))
}

// fail answers r with an error.
func (f *fakeX) fail(r request, code byte, value uint32) {
	p := make([]byte, 32)
	p[0], p[1] = packetError, code
	order.PutUint16(p[2:], r.seq)
	order.PutUint32(p[4:], value)
	p[10] = r.op
	f.write(p)
}

// event sends an event, padded to 32 bytes and stamped with the latest sequence number.
func (f *fakeX) event(ev []byte) {
	f.mu.Lock()
	seq := f.seq
	f.mu.Unlock()
	ev = append(bytes.Clone(ev), make([]byte, max(0, 32-len(ev)))...)
	order.PutUint16(ev[2:], seq)
	f.write(ev)
}

// set changes the server's settings, such as handle, while it runs.
func (f *fakeX) set(change func(f *fakeX)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change(f)
}

// events sends several events in one write, as a server sends events that
// happen together, each stamped with the latest sequence number.
func (f *fakeX) events(evs ...[]byte) {
	f.mu.Lock()
	seq := f.seq
	f.mu.Unlock()
	var all []byte
	for _, ev := range evs {
		ev = bytes.Clone(ev)
		order.PutUint16(ev[2:], seq)
		all = append(all, ev...)
	}
	f.write(all)
}

// atom returns the atom for name, interning it.
func (f *fakeX) atom(name string) uint32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.atomLocked(name)
}

func (f *fakeX) atomLocked(name string) uint32 {
	if a, ok := f.atoms[name]; ok {
		return a
	}
	a := uint32(100 + len(f.atoms))
	f.atoms[name], f.atomNames[a] = a, name
	return a
}

// setProp sets a property, as another client would.
func (f *fakeX) setProp(window, prop uint32, typ uint32, format byte, value []byte) {
	f.mu.Lock()
	f.props[[2]uint32{window, prop}] = property{typ: typ, format: format, value: value}
	f.mu.Unlock()
}

// prop returns a property's value.
func (f *fakeX) prop(window, prop uint32) (property, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.props[[2]uint32{window, prop}]
	return p, ok
}

// requests returns the logged requests with major opcode op.
func (f *fakeX) requests(op byte) []request {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []request
	for _, r := range f.log {
		if r.op == op {
			out = append(out, r)
		}
	}
	return out
}

// waitRequest waits for a request matching ok, logged already or to come.
func (f *fakeX) waitRequest(ok func(request) bool) request {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		log := slices.Clone(f.log)
		f.mu.Unlock()
		for _, r := range log {
			if ok(r) {
				return r
			}
		}
		time.Sleep(time.Millisecond)
	}
	f.t.Fatal("timed out waiting for a request")
	return request{}
}

// answer gives the default answer to r.
func (f *fakeX) answer(r request) {
	switch r.op {
	case opInternAtom:
		name := string(r.body[4 : 4+r.u16(0)])
		f.reply(r, 0, propU32(f.atom(name)))
	case opQueryExtension:
		name := string(r.body[4 : 4+r.u16(0)])
		f.mu.Lock()
		e := f.exts[name]
		f.mu.Unlock()
		present := byte(0)
		if e.present {
			present = 1
		}
		f.reply(r, 0, []byte{present, e.major, e.firstEvent, 0})
	case opGetProperty:
		f.getProperty(r)
	case opChangeProperty:
		window, prop, typ, format, n := r.u32(0), r.u32(4), r.u32(8), r.body[12], int(r.u32(16))
		value := bytes.Clone(r.body[20 : 20+n*int(format/8)])
		f.mu.Lock()
		key := [2]uint32{window, prop}
		if r.data == propAppend {
			value = append(bytes.Clone(f.props[key].value), value...)
		}
		f.props[key] = property{typ: typ, format: format, value: value}
		f.mu.Unlock()
	case opDeleteProperty:
		f.mu.Lock()
		delete(f.props, [2]uint32{r.u32(0), r.u32(4)})
		f.mu.Unlock()
	case opGetKeyboardMapping:
		f.mu.Lock()
		syms := bytes.Clone(propU32(f.keysyms...))
		per := f.per
		f.mu.Unlock()
		f.reply(r, byte(per), append(make([]byte, 24), syms...))
	case opGetModifierMapping:
		per := 0
		for _, m := range f.modmap {
			per = max(per, len(m))
		}
		codes := make([]byte, 8*per)
		for i, m := range f.modmap {
			copy(codes[i*per:], m)
		}
		f.reply(r, byte(per), append(make([]byte, 24), codes...))
	case opGetInputFocus:
		f.reply(r, 0, nil)
	case opSetSelectionOwner:
		f.mu.Lock()
		f.owners[r.u32(4)] = r.u32(0)
		f.mu.Unlock()
	case opGetSelectionOwner:
		f.mu.Lock()
		owner := f.owners[r.u32(0)]
		f.mu.Unlock()
		f.reply(r, 0, propU32(owner))
	case opTranslateCoordinates:
		x, y, child := int(int16(r.u16(8))), int(int16(r.u16(10))), uint32(0)
		f.mu.Lock()
		translate := f.translate
		f.mu.Unlock()
		if translate != nil {
			x, y, child = translate(r.u32(0), r.u32(4), x, y)
		}
		f.reply(r, 1, (&req{}).u32(child).i16(x).i16(y).b)
	case opQueryPointer:
		f.mu.Lock()
		pt := f.pointer
		f.mu.Unlock()
		f.reply(r, 1, (&req{}).u32(fakeRoot).u32(0).i16(int(pt[0])).i16(int(pt[1])).b)
	case opGrabPointer, opGrabKeyboard:
		f.mu.Lock()
		grab := f.grab
		f.mu.Unlock()
		f.reply(r, grab, nil)
	case opListFonts:
		n := uint16(1)
		f.mu.Lock()
		if f.fontless {
			n = 0
		}
		f.mu.Unlock()
		f.reply(r, 0, (&req{}).u16(n).b)
	case 73: // GetImage
		f.reply(r, 24, nil)
	default:
		f.answerExtension(r)
	}
}

// getProperty answers GetProperty from the stored properties.
func (f *fakeX) getProperty(r request) {
	window, prop, del, long := r.u32(0), r.u32(4), r.data == 1, int(r.u32(16))
	f.mu.Lock()
	p, ok := f.props[[2]uint32{window, prop}]
	value := p.value
	after := 0
	if len(value) > 4*long {
		after = len(value) - 4*long
		value = value[:4*long]
	}
	if ok && del && after == 0 {
		delete(f.props, [2]uint32{window, prop})
	}
	f.mu.Unlock()
	if !ok {
		f.reply(r, 0, nil)
		return
	}
	n := len(value)
	if p.format > 0 {
		n /= int(p.format / 8)
	}
	fields := (&req{}).u32(p.typ).u32(uint32(after)).u32(uint32(n)).pad(12).b
	f.reply(r, p.format, append(fields, value...))
}

// answerExtension answers the requests of MIT-SHM and RENDER.
func (f *fakeX) answerExtension(r request) {
	f.mu.Lock()
	shm, render := f.exts["MIT-SHM"], f.exts["RENDER"]
	f.mu.Unlock()
	switch {
	case r.op == shm.major && r.data == shmQueryVersion:
		f.mu.Lock()
		minor := f.shmMinor
		f.mu.Unlock()
		f.reply(r, 0, (&req{}).u16(1).u16(minor).b)
	case r.op == shm.major && r.data == shmAttachFd:
		for _, fd := range r.fds {
			syscall.Close(fd)
		}
	case r.op == render.major && r.data == renderQueryVersion:
		f.reply(r, 0, (&req{}).u32(0).u32(11).b)
	case r.op == render.major && r.data == renderQueryPictFormats:
		f.mu.Lock()
		formats := f.formats
		f.mu.Unlock()
		b := (&req{}).u32(uint32(len(formats))).pad(20)
		for _, fm := range formats {
			b.u32(fm.id).u8(1).u8(fm.depth).pad(2)
			for i := range 4 {
				b.u16(fm.shifts[i]).u16(fm.masks[i])
			}
			b.u32(0)
		}
		f.reply(r, 0, b.b)
	}
}
