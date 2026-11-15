package portal

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

// fakeBus is a Bus whose requests are answered from a script.
type fakeBus struct {
	sender    string
	subErrs   []error // the error each Subscribe returns, in turn
	subs      []string
	cancelled int
	open      func(parent, title string, options map[string]any) (string, error)
	response  *Response
	opened    map[string]any
	parent    string
	title     string
	method    string // "save" after a SaveFile call
}

func (b *fakeBus) Sender() string { return b.sender }

func (b *fakeBus) Subscribe(path string) (<-chan Response, func(), error) {
	b.subs = append(b.subs, path)
	if n := len(b.subs) - 1; n < len(b.subErrs) && b.subErrs[n] != nil {
		return nil, func() {}, b.subErrs[n]
	}
	ch := make(chan Response, 1)
	if b.response != nil {
		ch <- *b.response
	}
	return ch, func() { b.cancelled++ }, nil
}

func (b *fakeBus) OpenFile(parent, title string, options map[string]any) (string, error) {
	b.parent, b.title, b.opened = parent, title, options
	return b.open(parent, title, options)
}

// SaveFile records its call like OpenFile, under the method name "save".
func (b *fakeBus) SaveFile(parent, title string, options map[string]any) (string, error) {
	b.method = "save"
	return b.OpenFile(parent, title, options)
}

// pick runs PickPDFs against bus with a fixed token.
func pick(t *testing.T, bus *fakeBus) ([]string, error) {
	t.Helper()
	orig := newToken
	newToken = func() string { return "tok" }
	defer func() { newToken = orig }()
	return PickPDFs(context.Background(), bus, "Open PDF")
}

const wantPath = "/org/freedesktop/portal/desktop/request/1_42/tok"

func answering(resp Response) *fakeBus {
	return &fakeBus{
		sender: ":1.42", response: &resp,
		open: func(string, string, map[string]any) (string, error) { return wantPath, nil },
	}
}

func TestPickReturnsTheChosenFiles(t *testing.T) {
	bus := answering(Response{URIs: []string{"file:///home/me/a%20b.pdf", "file://localhost/tmp/c.pdf"}})
	got, err := pick(t, bus)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/home/me/a b.pdf", "/tmp/c.pdf"}; !reflect.DeepEqual(got, want) {
		t.Errorf("paths = %v, want %v", got, want)
	}
	// It subscribed to the predicted path before opening the dialog, and
	// cancelled the subscription afterwards.
	if len(bus.subs) != 1 || bus.subs[0] != wantPath || bus.cancelled != 1 {
		t.Errorf("subscriptions %v, cancelled %d", bus.subs, bus.cancelled)
	}
	if bus.parent != "" || bus.title != "Open PDF" {
		t.Errorf("parent %q title %q", bus.parent, bus.title)
	}
	// The request names its token, allows several files and filters to PDFs.
	if bus.opened["handle_token"] != "tok" || bus.opened["multiple"] != true {
		t.Errorf("options = %v", bus.opened)
	}
	filters, ok := bus.opened["filters"].([]Filter)
	if !ok || len(filters) != 1 || filters[0].Name != "PDF documents" || len(filters[0].Specs) != 2 ||
		filters[0].Specs[0] != (FilterSpec{Kind: 1, Pattern: "application/pdf"}) {
		t.Errorf("filters = %#v", bus.opened["filters"])
	}
}

func TestPickReportsCancellationAndFailure(t *testing.T) {
	got, err := pick(t, answering(Response{Code: 1}))
	if got != nil || err != nil {
		t.Errorf("a cancelled dialog = %v, %v; want nothing at all", got, err)
	}
	if _, err := pick(t, answering(Response{Code: 2})); !errors.Is(err, ErrFailed) {
		t.Errorf("a failed dialog = %v", err)
	}
	// A success with nothing usable chosen is an empty, not failed, result.
	got, err = pick(t, answering(Response{URIs: []string{"https://example.com/x.pdf"}}))
	if got != nil || err != nil {
		t.Errorf("no local files = %v, %v", got, err)
	}
}

func TestPickReportsBusErrors(t *testing.T) {
	boom := errors.New("bus down")
	bus := answering(Response{})
	bus.subErrs = []error{boom}
	if _, err := pick(t, bus); !errors.Is(err, boom) {
		t.Errorf("subscribe failure = %v", err)
	}
	bus = answering(Response{})
	bus.open = func(string, string, map[string]any) (string, error) { return "", boom }
	if _, err := pick(t, bus); !errors.Is(err, boom) || bus.cancelled != 1 {
		t.Errorf("open failure = %v, cancelled %d", err, bus.cancelled)
	}
}

func TestPickFollowsAPortalThatIgnoresTheToken(t *testing.T) {
	bus := answering(Response{URIs: []string{"file:///x.pdf"}})
	bus.open = func(string, string, map[string]any) (string, error) {
		return "/org/freedesktop/portal/desktop/request/1_42/other", nil
	}
	got, err := pick(t, bus)
	if err != nil || len(got) != 1 {
		t.Fatalf("%v, %v", got, err)
	}
	if len(bus.subs) != 2 || bus.subs[1] != "/org/freedesktop/portal/desktop/request/1_42/other" {
		t.Errorf("subscriptions %v", bus.subs)
	}
	if bus.cancelled != 2 { // the first, and the second when done
		t.Errorf("cancelled %d", bus.cancelled)
	}
	// Failing to follow it is reported.
	boom := errors.New("no")
	bad := answering(Response{})
	bad.subErrs = []error{nil, boom}
	bad.open = func(string, string, map[string]any) (string, error) { return "/elsewhere", nil }
	if _, err := pick(t, bad); !errors.Is(err, boom) {
		t.Errorf("resubscribe failure = %v", err)
	}
}

func TestPickStopsWhenTheContextEnds(t *testing.T) {
	bus := &fakeBus{sender: ":1.42", open: func(string, string, map[string]any) (string, error) { return wantPath, nil }}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	orig := newToken
	newToken = func() string { return "tok" }
	defer func() { newToken = orig }()
	if _, err := PickPDFs(ctx, bus, "Open"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
	if bus.cancelled != 1 {
		t.Errorf("the subscription was not cancelled: %d", bus.cancelled)
	}
}

func TestRequestPath(t *testing.T) {
	for sender, want := range map[string]string{
		":1.42":      "/org/freedesktop/portal/desktop/request/1_42/t",
		":1.7.3":     "/org/freedesktop/portal/desktop/request/1_7_3/t",
		"plain":      "/org/freedesktop/portal/desktop/request/plain/t",
		":1.42:more": "/org/freedesktop/portal/desktop/request/1_42:more/t",
	} {
		if got := requestPath(sender, "t"); got != want {
			t.Errorf("requestPath(%q) = %q, want %q", sender, got, want)
		}
	}
}

func TestFilePaths(t *testing.T) {
	got := filePaths([]string{
		"file:///a/b.pdf",
		"file:///with%20space/c%C3%A4.pdf",
		"file://localhost/d.pdf",
		"file://otherhost/e.pdf", // not on this machine
		"https://example.com/f.pdf",
		"file://",     // no path
		"::bad uri::", // unparseable
		"",
	})
	want := []string{"/a/b.pdf", "/with space/cä.pdf", "/d.pdf"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("filePaths = %v, want %v", got, want)
	}
	if filePaths(nil) != nil {
		t.Error("no URIs gave paths")
	}
}

func TestNewTokenIsFreshAndWellFormed(t *testing.T) {
	a, b := newToken(), newToken()
	if a == b || len(a) != len("viewrab")+16 || a[:7] != "viewrab" {
		t.Errorf("tokens %q %q", a, b)
	}
	for _, c := range a[7:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			t.Fatalf("token %q has a non-hex character", a)
		}
	}
}

// save runs SavePDF against bus with a fixed token.
func save(t *testing.T, bus *fakeBus) (string, error) {
	t.Helper()
	orig := newToken
	newToken = func() string { return "tok" }
	defer func() { newToken = orig }()
	return SavePDF(context.Background(), bus, "Save converted PDF", "/home/me/docs/a.pdfa.pdf")
}

func TestSaveReturnsTheChosenPath(t *testing.T) {
	bus := answering(Response{URIs: []string{"file:///home/me/out%20put.pdf"}})
	got, err := save(t, bus)
	if err != nil || got != "/home/me/out put.pdf" {
		t.Fatalf("SavePDF = %q, %v", got, err)
	}
	if bus.method != "save" || bus.title != "Save converted PDF" {
		t.Errorf("method %q title %q", bus.method, bus.title)
	}
	// The dialog suggests the file's name in its folder, the folder as the
	// NUL-terminated bytes the portal takes.
	if bus.opened["handle_token"] != "tok" || bus.opened["current_name"] != "a.pdfa.pdf" ||
		!reflect.DeepEqual(bus.opened["current_folder"], []byte("/home/me/docs\x00")) {
		t.Errorf("options = %v", bus.opened)
	}
	if _, ok := bus.opened["filters"].([]Filter); !ok {
		t.Errorf("filters = %#v", bus.opened["filters"])
	}
}

func TestSaveReportsCancellationAndFailure(t *testing.T) {
	for name, resp := range map[string]Response{
		"cancelled":     {Code: 1},
		"no local file": {URIs: []string{"https://example.com/x.pdf"}},
	} {
		if got, err := save(t, answering(resp)); got != "" || err != nil {
			t.Errorf("%s: SavePDF = %q, %v; want nothing at all", name, got, err)
		}
	}
	if _, err := save(t, answering(Response{Code: 2})); !errors.Is(err, ErrFailed) {
		t.Errorf("a failed dialog = %v", err)
	}
}
