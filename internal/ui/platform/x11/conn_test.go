package x11

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseDisplay(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want Display
	}{
		{":0", Display{Network: "unix", Address: "/tmp/.X11-unix/X0", Abstract: "@/tmp/.X11-unix/X0"}},
		{"unix:3.1", Display{Network: "unix", Address: "/tmp/.X11-unix/X3", Abstract: "@/tmp/.X11-unix/X3", Number: 3, Screen: 1}},
		{"example.org:10", Display{Network: "tcp", Address: "example.org:6010", Number: 10}},
		{"[::1]:2", Display{Network: "tcp", Address: "[::1]:6002", Number: 2}},
		{"/tmp/launch-x/org.xquartz:0", Display{Network: "unix", Address: "/tmp/launch-x/org.xquartz:0"}},
	} {
		got, err := ParseDisplay(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("ParseDisplay(%q) = %+v, %v; want %+v", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "host", ":x", ":0.x", ":-1"} {
		if _, err := ParseDisplay(bad); err == nil {
			t.Errorf("ParseDisplay(%q) succeeded", bad)
		}
	}
}

// writeAuth writes an Xauthority file of the given entries.
func writeAuth(t *testing.T, entries ...authEntry) string {
	t.Helper()
	var b bytes.Buffer
	field := func(s []byte) {
		binary.Write(&b, binary.BigEndian, uint16(len(s)))
		b.Write(s)
	}
	for _, e := range entries {
		binary.Write(&b, binary.BigEndian, e.family)
		field([]byte(e.address))
		field([]byte(e.number))
		field([]byte(e.name))
		field(e.data)
	}
	p := filepath.Join(t.TempDir(), "Xauthority")
	if err := os.WriteFile(p, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFindAuth(t *testing.T) {
	hostname = func() (string, error) { return "box", nil }
	t.Cleanup(func() { hostname = os.Hostname })
	p := writeAuth(t,
		authEntry{family: familyLocal, address: "other", number: "0", name: cookieName, data: []byte("x")},
		authEntry{family: familyLocal, address: "box", number: "1", name: cookieName, data: []byte("one")},
		authEntry{family: familyLocal, address: "box", number: "0", name: "XDM-AUTHORIZATION-1", data: []byte("y")},
		authEntry{family: familyLocal, address: "box", number: "0", name: cookieName, data: []byte("zero")},
		authEntry{family: familyWild, number: "", name: cookieName, data: []byte("wild")},
	)
	for n, want := range map[int]string{0: "zero", 1: "one", 7: "wild"} {
		if name, data := findAuth(p, n); name != cookieName || string(data) != want {
			t.Errorf("findAuth(%d) = %q %q, want %q", n, name, data, want)
		}
	}
	if name, data := findAuth(filepath.Join(t.TempDir(), "none"), 0); name != "" || data != nil {
		t.Errorf("missing file: %q %q", name, data)
	}
	none := writeAuth(t, authEntry{family: familyLocal, address: "other", name: cookieName})
	if name, _ := findAuth(none, 0); name != "" {
		t.Errorf("no match: %q", name)
	}
}

func TestReadAuthEntriesStopsAtATruncatedRecord(t *testing.T) {
	full := []byte{0, 1, 0, 1, 'a', 0, 1, '0', 0, 1, 'n', 0, 1, 'd'}
	for cut := 1; cut < len(full); cut++ {
		if got := readAuthEntries(bytes.NewReader(full[:cut])); len(got) != 0 {
			t.Errorf("cut at %d: %d entries", cut, len(got))
		}
	}
	if got := readAuthEntries(bytes.NewReader(full)); len(got) != 1 || got[0].address != "a" || string(got[0].data) != "d" {
		t.Errorf("entries = %+v", got)
	}
}

func TestAuthFile(t *testing.T) {
	t.Setenv("XAUTHORITY", "/x/auth")
	if got := authFile(); got != "/x/auth" {
		t.Errorf("authFile = %q", got)
	}
	t.Setenv("XAUTHORITY", "")
	t.Setenv("HOME", "/home/u")
	if got := authFile(); got != "/home/u/.Xauthority" {
		t.Errorf("authFile = %q", got)
	}
	t.Setenv("HOME", "")
	if got := authFile(); got != "" {
		t.Errorf("authFile without a home = %q", got)
	}
}

func TestSetupParsesScreensAndFormats(t *testing.T) {
	_, c := startFake(t, defaultSetup())
	s := c.Setup
	if s.Vendor != "fake" || s.MaxRequest != 4*65535 || s.MinKeycode != 8 || s.MaxKeycode != 15 {
		t.Errorf("setup = %+v", s)
	}
	if len(s.Formats) != 3 || s.Formats[1] != (Format{24, 32, 32}) {
		t.Errorf("formats = %+v", s.Formats)
	}
	if len(s.Screens) != 1 || s.Screens[0].Root != fakeRoot || len(s.Screens[0].Visuals) != 2 || s.Screens[0].Visuals[1].Depth != 32 {
		t.Errorf("screens = %+v", s.Screens)
	}
}

func TestSetupRefused(t *testing.T) {
	o := defaultSetup()
	o.refuse = "Authorization required"
	_, sock := newFakeX(t, o)
	_, err := NewConn(sock, cookieName, []byte("cookie"))
	if err == nil || !strings.Contains(err.Error(), "Authorization required") {
		t.Errorf("err = %v", err)
	}
}

func TestSetupFailures(t *testing.T) {
	// A server that hangs up at each stage of the setup.
	for _, tc := range []struct {
		name  string
		reply []byte
	}{
		{"nothing", nil},
		{"short header", []byte{1, 0, 11}},
		{"short body", []byte{1, 0, 11, 0, 0, 0, 2, 0, 1}},
		{"truncated setup", []byte{1, 0, 11, 0, 0, 0, 1, 0, 0, 0, 0, 0}},
		{"no screens", append([]byte{1, 0, 11, 0, 0, 0, 8, 0}, make([]byte, 32)...)},
		{"other status", append([]byte{2, 0, 11, 0, 0, 0, 1, 0}, "more"...)},
	} {
		client, server := socketPair(t)
		go func() {
			io.ReadFull(server, make([]byte, 12))
			server.Write(tc.reply)
			server.Close()
		}()
		if _, err := NewConn(client, "", nil); err == nil {
			t.Errorf("%s: NewConn succeeded", tc.name)
		}
		client.Close()
	}
	// A socket that cannot be written.
	client, server := socketPair(t)
	server.Close()
	client.Close()
	if _, err := NewConn(client, "", nil); err == nil {
		t.Error("closed socket: NewConn succeeded")
	}
}

func TestCallsGetTheirRepliesInOrderAndErrorsGoToTheirRequests(t *testing.T) {
	f, c := startFake(t, defaultSetup())
	f.handle = func(f *fakeX, r request) bool {
		if r.op == opInternAtom && string(r.body[4:4+r.u16(0)]) == "BAD" {
			f.fail(r, 2, 77)
			return true
		}
		return false
	}
	waits := []func() ([]byte, error){
		c.Start(newReq(opInternAtom, 0).u16(1).pad(2).bytes([]byte("A"))),
		c.Start(newReq(opInternAtom, 0).u16(3).pad(2).bytes([]byte("BAD"))),
		c.Start(newReq(opInternAtom, 0).u16(1).pad(2).bytes([]byte("B"))),
	}
	a, errA := waits[0]()
	_, errBad := waits[1]()
	b, errB := waits[2]()
	var xe *Error
	if errA != nil || errB != nil || !errors.As(errBad, &xe) || xe.Code != 2 || xe.Value != 77 || xe.Major != opInternAtom {
		t.Fatalf("errors: %v %v %v", errA, errBad, errB)
	}
	if !strings.Contains(xe.Error(), "error 2") {
		t.Errorf("Error() = %q", xe.Error())
	}
	if u32At(a, 8) == u32At(b, 8) {
		t.Error("two names got the same atom")
	}
	if _, err := c.internAtoms([]string{"X", "BAD"}); err == nil {
		t.Error("internAtoms hid an error")
	}
}

func TestUnclaimedErrorsGoToTheHandler(t *testing.T) {
	f, c := startFake(t, defaultSetup())
	f.handle = func(f *fakeX, r request) bool {
		if r.op == opMapWindow {
			f.fail(r, 3, r.u32(0))
			return true
		}
		return false
	}
	got := make(chan *Error, 2)
	c.Send(newReq(opMapWindow, 0).u32(1)) // before a handler: dropped
	c.Sync()
	c.OnError(func(e *Error) { got <- e })
	c.Send(newReq(opMapWindow, 0).u32(42))
	c.Sync()
	select {
	case e := <-got:
		if e.Value != 42 || e.Code != 3 {
			t.Errorf("error = %+v", e)
		}
	default:
		t.Error("no error reported")
	}
}

func TestEventsQueueInOrderAndPeekDoesNotTake(t *testing.T) {
	f, c := startFake(t, defaultSetup())
	if _, ok := c.Peek(); ok {
		t.Error("Peek found an event in an empty queue")
	}
	f.event([]byte{evExpose, 0, 0, 0, 1})
	ge := make([]byte, 40)
	ge[0] = genericEvent
	order.PutUint32(ge[4:], 2)
	ge[32] = 9
	f.write(ge)
	c.Sync()
	if ev, ok := c.Peek(); !ok || ev[0] != evExpose {
		t.Fatalf("Peek = %v, %v", ev, ok)
	}
	if ev, err := c.Next(); err != nil || ev[0] != evExpose || ev[4] != 1 {
		t.Errorf("Next = %v, %v", ev, err)
	}
	if ev, err := c.Next(); err != nil || len(ev) != 40 || ev[32] != 9 {
		t.Errorf("generic event = %v, %v", ev, err)
	}
}

func TestSequenceNumbersSurviveWrapping(t *testing.T) {
	_, c := startFake(t, defaultSetup())
	// More requests without replies than 16 bits can count: syncs slip in.
	for range 3 * syncEvery {
		if err := c.Send(newReq(opMapWindow, 0).u32(1)); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := c.Call(newReq(opInternAtom, 0).u16(1).pad(2).bytes([]byte("Z")))
	if err != nil || len(rep) < 32 {
		t.Fatalf("Call after wrapping: %v", err)
	}
}

func TestClosingFailsWaitingCallsAndEndsTheEventStream(t *testing.T) {
	f, c := startFake(t, defaultSetup())
	f.handle = func(f *fakeX, r request) bool {
		if r.op == opGetInputFocus {
			f.sock.Close() // hang up instead of answering
			return true
		}
		return false
	}
	f.event([]byte{evExpose, 0, 0, 0})
	if err := c.Sync(); !errors.Is(err, ErrClosed) {
		t.Errorf("Sync = %v", err)
	}
	<-c.Done()
	if _, err := c.Next(); err != nil {
		t.Errorf("queued event lost: %v", err)
	}
	if _, err := c.Next(); err == nil {
		t.Error("Next after the end returned no error")
	}
	if err := c.Send(newReq(opMapWindow, 0)); !errors.Is(err, ErrClosed) {
		t.Errorf("Send after the end = %v", err)
	}
}

func TestTruncatedReplyEndsTheConnection(t *testing.T) {
	f, c := startFake(t, defaultSetup())
	f.handle = func(f *fakeX, r request) bool {
		p := make([]byte, 32)
		p[0] = packetReply
		order.PutUint16(p[2:], r.seq)
		order.PutUint32(p[4:], 10) // promises 40 more bytes
		f.write(p)
		f.sock.Close()
		return true
	}
	if _, err := c.Call(newReq(opGetInputFocus, 0)); err == nil {
		t.Error("truncated reply accepted")
	}
}

func TestRequestTooLarge(t *testing.T) {
	o := defaultSetup()
	o.maxReq = 16
	_, c := startFake(t, o)
	if err := c.Send(newReq(opMapWindow, 0).pad(100)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("Send = %v", err)
	}
}

func TestNewIDsAreDistinctAndInTheClientsRange(t *testing.T) {
	_, c := startFake(t, defaultSetup())
	a, b := c.NewID(), c.NewID()
	if a == b || a&^0x1fffff != 0x200000 || b&^0x1fffff != 0x200000 {
		t.Errorf("ids %#x %#x", a, b)
	}
}

func TestDial(t *testing.T) {
	t.Setenv("DISPLAY", "")
	if _, err := Dial(); err == nil {
		t.Error("Dial without DISPLAY succeeded")
	}
	t.Setenv("DISPLAY", "nonsense")
	if _, err := Dial(); err == nil {
		t.Error("Dial with a bad DISPLAY succeeded")
	}
	t.Setenv("XAUTHORITY", filepath.Join(t.TempDir(), "none"))
	var dialed []string
	fail := errors.New("no")
	connect := func(t *testing.T, o setupOpts) func(network, addr string) (net.Conn, error) {
		return func(network, addr string) (net.Conn, error) {
			dialed = append(dialed, network+" "+addr)
			if strings.HasPrefix(addr, "@") {
				return nil, fail // no abstract socket: fall back to the path
			}
			_, sock := newFakeX(t, o)
			return sock, nil
		}
	}
	t.Cleanup(func() { dial = net.Dial })

	dial = connect(t, defaultSetup())
	t.Setenv("DISPLAY", ":5")
	c, err := Dial()
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if len(dialed) != 2 || dialed[0] != "unix @/tmp/.X11-unix/X5" || dialed[1] != "unix /tmp/.X11-unix/X5" {
		t.Errorf("dialed %v", dialed)
	}

	// The screen named in DISPLAY comes first.
	o := defaultSetup()
	o.screens = 2
	dial = connect(t, o)
	t.Setenv("DISPLAY", ":5.1")
	if c, err = Dial(); err != nil || c.Setup.Screens[0].Root != fakeRoot+1 {
		t.Errorf("screen 1: %v", err)
	} else {
		c.Close()
	}
	t.Setenv("DISPLAY", ":5.2")
	if _, err = Dial(); err == nil {
		t.Error("missing screen accepted")
	}

	// A refused setup, and a socket that cannot be reached.
	o.refuse = "no"
	dial = connect(t, o)
	if _, err = Dial(); err == nil {
		t.Error("refused setup accepted")
	}
	dial = func(string, string) (net.Conn, error) { return nil, fail }
	t.Setenv("DISPLAY", "host:0")
	if _, err = Dial(); !errors.Is(err, fail) {
		t.Errorf("unreachable: %v", err)
	}
}

func TestReaderReportsShortFields(t *testing.T) {
	d := &reader{b: []byte{1, 2, 3}}
	if d.u16() != 0x0201 || d.err != nil {
		t.Fatal("u16")
	}
	d.u32()
	if d.err != ErrShortReply {
		t.Errorf("err = %v", d.err)
	}
	d = &reader{b: []byte{0xff, 0xff, 'h', 'i'}}
	if d.i16() != -1 || d.str(2) != "hi" {
		t.Error("i16/str")
	}
	d.at(0)
	if d.u8() != 0xff {
		t.Error("at")
	}
}

func TestGetPropertyTooShort(t *testing.T) {
	f, c := startFake(t, defaultSetup())
	f.handle = func(f *fakeX, r request) bool {
		if r.op == opGetProperty {
			f.reply(r, 32, (&req{}).u32(atomCardinal).u32(0).u32(100).b) // claims 100 values, sends none
			return true
		}
		return false
	}
	if _, err := c.getProperty(fakeRoot, 1, false, 4); !errors.Is(err, ErrShortReply) {
		t.Errorf("err = %v", err)
	}
	if got := (property{format: 8, value: []byte{1}}).u32s(); got != nil {
		t.Errorf("u32s of an 8-bit property = %v", got)
	}
}

func TestCallTimesOutNever(t *testing.T) {
	// Sync returns once the server answers, however late.
	f, c := startFake(t, defaultSetup())
	f.handle = func(f *fakeX, r request) bool {
		if r.op == opGetInputFocus {
			go func() { time.Sleep(20 * time.Millisecond); f.reply(r, 0, nil) }()
			return true
		}
		return false
	}
	if err := c.Sync(); err != nil {
		t.Error(err)
	}
}
