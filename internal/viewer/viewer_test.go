package viewer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/voidrab/gopdfrab"

	"viewer/internal/pdfa"
	"viewer/internal/testpdf"
)

type fakeDialogs struct {
	open        []string
	save        string
	err         error
	lastSuggest string
}

func (f *fakeDialogs) OpenPDFs() ([]string, error) { return f.open, f.err }

func (f *fakeDialogs) SavePDF(suggested string) (string, error) {
	f.lastSuggest = suggested
	return f.save, f.err
}

// fakeWindows records the windows the Service asks for; "+" marks a window
// created with files to open.
type fakeWindows struct{ created, focused []string }

func (f *fakeWindows) New(name string, opening bool) {
	if opening {
		name += "+"
	}
	f.created = append(f.created, name)
}
func (f *fakeWindows) Focus(name string) { f.focused = append(f.focused, name) }

type event struct {
	name string
	data Targeted
}

// recorder collects emitted events; the Service emits synchronously.
type recorder struct{ events []event }

func (r *recorder) emit(name string, data any) {
	r.events = append(r.events, event{name, data.(Targeted)})
}

// windowKey carries the calling window's name in test contexts, as Wails
// does for bound calls.
type windowKey struct{}

func in(window string) context.Context {
	return context.WithValue(context.Background(), windowKey{}, window)
}

func windowOf(ctx context.Context) string {
	name, _ := ctx.Value(windowKey{}).(string)
	return name
}

type fixture struct {
	*Service
	dialogs *fakeDialogs
	windows *fakeWindows
	events  *recorder
}

func newService() fixture {
	f := fixture{dialogs: &fakeDialogs{}, windows: &fakeWindows{}, events: &recorder{}}
	f.Service = New(f.dialogs, f.windows, f.events.emit, windowOf)
	f.after = func(_ time.Duration, fn func()) { fn() } // no waiting in tests
	return f
}

func TestLaunchWithoutFilesOpensAnEmptyWindow(t *testing.T) {
	s := newService()
	s.Launch(nil, "/")
	s.NewWindow()
	if !slices.Equal(s.windows.created, []string{"window-1", "window-2"}) {
		t.Fatalf("created %v", s.windows.created)
	}
	if f := s.InitialFiles(in("window-1")); f == nil || len(f) != 0 {
		t.Fatalf("InitialFiles = %#v; want an empty list", f)
	}
}

func TestLaunchOpensFilesInOneNewWindow(t *testing.T) {
	s := newService()
	s.Launch([]string{"a.pdf", "/abs/b.pdf"}, "/work")
	if !slices.Equal(s.windows.created, []string{"window-1+"}) {
		t.Fatalf("created %v; want one window that opens files", s.windows.created)
	}
	want := []string{"/work/a.pdf", "/abs/b.pdf"}
	if f := s.InitialFiles(in("window-1")); !slices.Equal(f, want) {
		t.Errorf("InitialFiles = %v; want %v, resolved against the launch dir", f, want)
	}
	if f := s.InitialFiles(in("window-1")); len(f) != 0 {
		t.Errorf("InitialFiles returned %v twice", f)
	}
}

func TestLaunchAddsTabsToTheActiveWindow(t *testing.T) {
	s := newService()
	s.Launch(nil, "/")

	// Before the window's frontend has started, files wait for it.
	s.Launch([]string{"/early.pdf"}, "/")
	if len(s.events.events) != 0 {
		t.Fatalf("events %v before the window was ready", s.events.events)
	}
	if f := s.InitialFiles(in("window-1")); !slices.Equal(f, []string{"/early.pdf"}) {
		t.Fatalf("InitialFiles = %v", f)
	}

	// Once it has, they go to it as an event.
	s.Launch([]string{"/late.pdf"}, "/")
	if len(s.events.events) != 1 {
		t.Fatalf("events %v", s.events.events)
	}
	ev := s.events.events[0]
	if ev.name != EventOpenFiles || ev.data.Window != "window-1" || !slices.Equal(ev.data.Data.([]string), []string{"/late.pdf"}) {
		t.Fatalf("event %+v", ev)
	}
	if !slices.Equal(s.windows.created, []string{"window-1"}) ||
		!slices.Equal(s.windows.focused, []string{"window-1", "window-1"}) {
		t.Fatalf("created %v, focused %v; want one window, raised for each launch", s.windows.created, s.windows.focused)
	}
}

func TestLaunchFollowsFocus(t *testing.T) {
	s := newService()
	s.NewWindow()
	s.NewWindow()
	s.InitialFiles(in("window-1"))
	s.WindowFocused("window-1")
	s.WindowFocused("closed-long-ago") // ignored
	s.Launch([]string{"/a.pdf"}, "/")
	if got := s.events.events[0].data.Window; got != "window-1" {
		t.Fatalf("files went to %q; want the last focused window", got)
	}

	s.WindowClosed("window-1")
	s.Launch([]string{"/b.pdf"}, "/")
	if got := s.windows.created[len(s.windows.created)-1]; got != "window-3+" {
		t.Fatalf("created %v; want a new window once the active one closed", s.windows.created)
	}
}

func TestLaunchSwitchesToAnOpenFile(t *testing.T) {
	s := newService()
	path := testpdf.Sample(t, "a.pdf")
	s.Launch([]string{path}, "/")
	// Launched again while the window has yet to open it: just raise it.
	s.Launch([]string{path}, "/")
	s.InitialFiles(in("window-1"))
	doc, err := s.Open(in("window-1"), path)
	if err != nil {
		t.Fatal(err)
	}
	// Launched again once a tab shows it: switch to that tab.
	s.Launch([]string{path}, "/")
	if !slices.Equal(s.windows.created, []string{"window-1+"}) ||
		!slices.Equal(s.windows.focused, []string{"window-1", "window-1"}) {
		t.Fatalf("created %v, focused %v; want one window, raised twice", s.windows.created, s.windows.focused)
	}
	if len(s.events.events) != 1 || s.events.events[0].name != EventShow || s.events.events[0].data.Data != doc.ID {
		t.Fatalf("events %v; want one %s for document %d", s.events.events, EventShow, doc.ID)
	}
}

func TestProfiles(t *testing.T) {
	if p := newService().Profiles(); len(p) == 0 || p[0].Name != "pdfa1b" {
		t.Errorf("Profiles() = %v", p)
	}
}

func TestOpen(t *testing.T) {
	s := newService()
	path := testpdf.Sample(t, "my report.pdf")

	doc, err := s.Open(in("w"), path)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	if doc.ID != 1 || doc.Path != path || doc.Name != "my report.pdf" || doc.Size != st.Size() || doc.Note != "" {
		t.Fatalf("Open = %+v", doc)
	}
	if doc.Info.Title != "Sample" || doc.Info.Version != "1.4" {
		t.Fatalf("Info = %+v", doc.Info)
	}
	if doc.URL != "/doc/1/my%20report.pdf" {
		t.Fatalf("URL = %q", doc.URL)
	}

	// A second tab on the same file gets its own document.
	again, err := s.Open(in("w"), path)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID == doc.ID || again.URL == doc.URL || s.doc(doc.ID) == nil {
		t.Fatal("a second open did not give a separate document")
	}
}

func TestOpenRelativePath(t *testing.T) {
	path := testpdf.Sample(t, "rel.pdf")
	t.Chdir(filepath.Dir(path))
	doc, err := newService().Open(in("w"), "rel.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(doc.Path) {
		t.Fatalf("Path = %q; want it made absolute", doc.Path)
	}
}

func TestOpenErrors(t *testing.T) {
	s := newService()
	dir := t.TempDir()

	if _, err := s.Open(in("w"), filepath.Join(dir, "missing.pdf")); err == nil ||
		!strings.Contains(err.Error(), "missing.pdf") || !strings.Contains(err.Error(), "no such file") {
		t.Errorf("missing file err = %v", err)
	}
	if _, err := s.Open(in("w"), dir); err == nil || !strings.Contains(err.Error(), "folder") {
		t.Errorf("directory err = %v", err)
	}
	notPDF := testpdf.Write(t, dir, "notes.pdf", []byte("just text"))
	if _, err := s.Open(in("w"), notPDF); !errors.Is(err, gopdfrab.ErrNotPDF) {
		t.Errorf("not a PDF err = %v", err)
	}
	if len(s.docs) != 0 {
		t.Error("a failed open recorded a document")
	}
}

func TestOpenUnreadableStillOpens(t *testing.T) {
	// A PDF header with no readable structure: gopdfrab gives up, but the
	// renderer may still cope, so the document opens with a note.
	path := testpdf.Write(t, t.TempDir(), "broken.pdf", []byte("%PDF-1.4\ngarbage\n"))
	doc, err := newService().Open(in("w"), path)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Note == "" {
		t.Fatalf("Open = %+v; want a note explaining the missing info", doc)
	}
}

func TestDocumentsLiveWhileATabShowsThem(t *testing.T) {
	s := newService()
	a := testpdf.Sample(t, "a.pdf")
	docA, _ := s.Open(in("one"), a)
	docB, _ := s.Open(in("one"), a)
	docC, _ := s.Open(in("two"), a)

	s.Release(in("one"), docA.ID)
	if s.doc(docA.ID) != nil {
		t.Error("a released document is still open")
	}
	if s.doc(docB.ID) == nil {
		t.Error("releasing one tab's document dropped another tab's")
	}

	// A document shown by two windows survives the closing of one of them.
	s.win("two").docs[docB.ID] = true
	s.WindowClosed("one")
	if s.doc(docB.ID) == nil || s.doc(docC.ID) == nil {
		t.Error("closing window one dropped documents window two shows")
	}
	s.WindowClosed("two")
	s.WindowClosed("never-opened")
	if len(s.docs) != 0 || len(s.wins) != 0 {
		t.Errorf("after closing every window: docs %v, windows %v", slices.Collect(maps.Keys(s.docs)), slices.Collect(maps.Keys(s.wins)))
	}
}

func TestWindowClosedDropsPendingFiles(t *testing.T) {
	s := newService()
	s.Launch([]string{"/a.pdf"}, "/")
	s.WindowClosed("window-1")
	if f := s.InitialFiles(in("window-1")); len(f) != 0 {
		t.Fatalf("InitialFiles = %v after close", f)
	}
}

func TestPickFiles(t *testing.T) {
	s := newService()
	s.dialogs.open = []string{"/a.pdf", "/b.pdf"}
	if got, err := s.PickFiles(); err != nil || !slices.Equal(got, s.dialogs.open) {
		t.Fatalf("PickFiles = %v, %v", got, err)
	}
	s.dialogs.open = nil
	if got, err := s.PickFiles(); err != nil || got == nil || len(got) != 0 {
		t.Errorf("cancelled PickFiles = %#v, %v; want an empty list", got, err)
	}
	s.dialogs.err = errors.New("no portal")
	if _, err := s.PickFiles(); !errors.Is(err, s.dialogs.err) {
		t.Errorf("dialog failure err = %v", err)
	}
}

func TestHandleDrop(t *testing.T) {
	cases := []struct {
		name  string
		paths []string
		want  string // event name, or "" for none
		data  any
	}{
		{"opens the pdfs", []string{"/x/readme.txt", "/x/a.pdf", "/x/B.PDF"}, EventOpenFiles, []string{"/x/a.pdf", "/x/B.PDF"}},
		{"rejects non-pdfs", []string{"/x/a.png"}, EventError, "Only PDF files can be opened."},
		{"ignores empty drop", nil, "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newService()
			s.HandleDrop("target", c.paths)
			if c.want == "" {
				if len(s.events.events) != 0 {
					t.Fatalf("events = %v; want none", s.events.events)
				}
				return
			}
			if len(s.events.events) != 1 {
				t.Fatalf("events = %v; want one %s", s.events.events, c.want)
			}
			ev := s.events.events[0]
			if ev.name != c.want || ev.data.Window != "target" {
				t.Fatalf("event %+v; want %s to the drop window", ev, c.want)
			}
			if got, ok := ev.data.Data.([]string); ok {
				if !slices.Equal(got, c.data.([]string)) {
					t.Fatalf("files %v; want %v", got, c.data)
				}
			} else if ev.data.Data != c.data {
				t.Fatalf("data %v; want %v", ev.data.Data, c.data)
			}
		})
	}
}

func TestVerify(t *testing.T) {
	s := newService()
	ctx := context.Background()
	if _, err := s.Verify(ctx, 1, "pdfa1b"); !errors.Is(err, ErrNoDocument) {
		t.Fatalf("Verify of an unknown document err = %v", err)
	}
	doc, err := s.Open(in("w"), testpdf.Sample(t, "a.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := s.Verify(ctx, doc.ID, "pdfa1b")
	if err != nil || rep.Valid || len(rep.Issues) == 0 {
		t.Fatalf("Verify = %+v, %v; want issues", rep, err)
	}
	if _, err := s.Verify(ctx, doc.ID, "bogus"); !errors.Is(err, pdfa.ErrUnknownProfile) {
		t.Fatalf("bogus profile err = %v", err)
	}
}

func TestConvert(t *testing.T) {
	ctx := context.Background()
	s := newService()
	if _, err := s.Convert(ctx, 1, "pdfa1b"); !errors.Is(err, ErrNoDocument) {
		t.Fatalf("Convert of an unknown document err = %v", err)
	}

	src := testpdf.Sample(t, "in.pdf")
	doc, err := s.Open(in("w"), src)
	if err != nil {
		t.Fatal(err)
	}

	// Cancelled save dialog: nothing written, no error.
	cr, err := s.Convert(ctx, doc.ID, "pdfa1b")
	if cr != nil || err != nil {
		t.Fatalf("cancelled Convert = %+v, %v", cr, err)
	}
	if s.dialogs.lastSuggest != strings.TrimSuffix(src, ".pdf")+".pdfa.pdf" {
		t.Fatalf("suggested %q", s.dialogs.lastSuggest)
	}

	s.dialogs.save = filepath.Join(t.TempDir(), "out.pdf")
	cr, err = s.Convert(ctx, doc.ID, "pdfa1b")
	if err != nil || cr == nil || !cr.Report.Valid || cr.Output != s.dialogs.save {
		t.Fatalf("Convert = %+v, %v", cr, err)
	}
	if _, err := os.Stat(s.dialogs.save); err != nil {
		t.Fatalf("output not written: %v", err)
	}

	s.dialogs.save = filepath.Join(t.TempDir(), "missing", "out.pdf")
	if _, err := s.Convert(ctx, doc.ID, "pdfa1b"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Convert into a missing directory err = %v", err)
	}

	s.dialogs.err = errors.New("no portal")
	if _, err := s.Convert(ctx, doc.ID, "pdfa1b"); !errors.Is(err, s.dialogs.err) {
		t.Fatalf("dialog failure err = %v", err)
	}
}

func get(s *Service, target string, header http.Header) *http.Response {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	maps.Copy(req.Header, header)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w.Result()
}

func TestServeHTTP(t *testing.T) {
	s := newService()
	a := testpdf.Sample(t, "a.pdf")
	b := testpdf.Write(t, t.TempDir(), "b.pdf", append(testpdf.Build("B"), '\n'))
	docA, _ := s.Open(in("one"), a)
	docB, _ := s.Open(in("two"), b)

	for _, c := range []struct {
		doc  *Document
		path string
	}{{docA, a}, {docB, b}} {
		want, _ := os.ReadFile(c.path)
		res := get(s.Service, c.doc.URL, nil)
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusOK || !bytes.Equal(body, want) {
			t.Fatalf("%s: status %d, %d bytes; want 200 and its own file", c.doc.URL, res.StatusCode, len(body))
		}
		if ct := res.Header.Get("Content-Type"); ct != "application/pdf" {
			t.Errorf("Content-Type = %q", ct)
		}
		if cc := res.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("Cache-Control = %q", cc)
		}
	}

	// ServeHTTP accepts the path with or without the route prefix.
	if res := get(s.Service, "/1/a.pdf", nil); res.StatusCode != http.StatusOK {
		t.Errorf("prefix-stripped path: status %d", res.StatusCode)
	}

	res := get(s.Service, docA.URL, http.Header{"Range": {"bytes=0-4"}})
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusPartialContent || string(body) != "%PDF-" {
		t.Fatalf("range: status %d body %q", res.StatusCode, body)
	}

	for _, target := range []string{"/doc/99/x.pdf", "/doc/abc/x.pdf", "/doc/", "/doc"} {
		if res := get(s.Service, target, nil); res.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d; want 404", target, res.StatusCode)
		}
	}
}

func TestServeHTTPUnreadable(t *testing.T) {
	path := testpdf.Sample(t, "a.pdf")
	s := newService()
	doc, err := s.Open(in("w"), path)
	if err != nil {
		t.Fatal(err)
	}

	if os.Getuid() != 0 { // Root ignores file permissions.
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		if res := get(s.Service, doc.URL, nil); res.StatusCode != http.StatusGone {
			t.Errorf("unreadable file: status %d; want 410", res.StatusCode)
		}
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if res := get(s.Service, doc.URL, nil); res.StatusCode != http.StatusGone {
		t.Errorf("deleted file: status %d; want 410", res.StatusCode)
	}
}

func TestDragMovesTabsBetweenWindows(t *testing.T) {
	s := newService()
	docA, _ := s.Open(in("window-1"), testpdf.Sample(t, "a.pdf"))
	docB, _ := s.Open(in("window-1"), testpdf.Sample(t, "b.pdf"))
	s.InitialFiles(in("window-2"))

	state := map[string]any{"pages": 1.0}
	token := s.StartDrag(in("window-1"), []DragItem{{ID: docA.ID, State: state}, {ID: docB.ID}})
	if ev := s.events.events[0]; ev.name != EventDrag || ev.data.Window != "" ||
		ev.data.Data != (DragState{Token: token, Active: true}) {
		t.Fatalf("start event %+v; want a broadcast of the drag", ev)
	}

	// A drop in the source window reorders its tabs there, so Go returns none
	// and keeps the drag.
	if m := s.Drop(in("window-1"), token); len(m) != 0 {
		t.Fatalf("Drop in the source window = %v", m)
	}
	// The drag's end may reach Go before the target's drop.
	s.EndDrag(token, false)
	if ev := s.events.events[1]; ev.name != EventDrag || ev.data.Data.(DragState).Active {
		t.Fatalf("end event %+v", ev)
	}
	// A document closed since the drag started is left out.
	s.Release(in("window-1"), docB.ID)

	moved := s.Drop(in("window-2"), token)
	if len(moved) != 1 || moved[0].Doc != docA || moved[0].State == nil {
		t.Fatalf("Drop = %+v; want a.pdf with its state", moved)
	}
	if ev := s.events.events[2]; ev.name != EventMovedOut || ev.data.Window != "window-1" ||
		!slices.Equal(ev.data.Data.([]int), []int{docA.ID}) {
		t.Fatalf("moved-out event %+v", ev)
	}
	if m := s.Drop(in("window-3"), token); len(m) != 0 {
		t.Fatalf("a drag was dropped twice: %v", m)
	}

	// The document now belongs to window-2: closing window-1 keeps it.
	s.WindowClosed("window-1")
	if s.doc(docA.ID) == nil {
		t.Fatal("a moved document was released with its old window")
	}
	s.WindowClosed("window-2")
	if s.doc(docA.ID) != nil {
		t.Fatal("a moved document outlived its new window")
	}
}

func TestDropOfAStaleDrag(t *testing.T) {
	s := newService()
	old := s.StartDrag(in("window-1"), nil)
	s.StartDrag(in("window-1"), nil)
	if m := s.Drop(in("window-2"), old); len(m) != 0 {
		t.Fatalf("Drop of a replaced drag = %v", m)
	}
}

func TestDragOutOfAllWindowsDetachesTabs(t *testing.T) {
	s := newService()
	s.Launch(nil, "/")
	a, _ := s.Open(in("window-1"), testpdf.Sample(t, "a.pdf"))
	token := s.StartDrag(in("window-1"), []DragItem{{ID: a.ID, State: "here"}})
	s.EndDrag(token, true)

	if !slices.Equal(s.windows.created, []string{"window-1", "window-2+"}) {
		t.Fatalf("created %v; want a new window for the tab", s.windows.created)
	}
	last := s.events.events[len(s.events.events)-1]
	if last.name != EventMovedOut || last.data.Window != "window-1" || !slices.Equal(last.data.Data.([]int), []int{a.ID}) {
		t.Fatalf("event %+v; want the source told its tab moved out", last)
	}
	tabs := s.InitialTabs(in("window-2"))
	if len(tabs) != 1 || tabs[0].Doc != a || tabs[0].State != "here" {
		t.Fatalf("InitialTabs = %+v", tabs)
	}
	if again := s.InitialTabs(in("window-2")); len(again) != 0 {
		t.Fatalf("InitialTabs returned %v twice", again)
	}
	s.WindowClosed("window-1")
	if s.doc(a.ID) == nil {
		t.Fatal("the detached document was released with its old window")
	}
}

func TestDetachLeavesClaimedAndEmptyDrags(t *testing.T) {
	s := newService()
	s.Launch(nil, "/")
	a, _ := s.Open(in("window-1"), testpdf.Sample(t, "a.pdf"))

	// Another window took the tab before the delay ran out.
	token := s.StartDrag(in("window-1"), []DragItem{{ID: a.ID}})
	var detach func()
	s.after = func(_ time.Duration, fn func()) { detach = fn }
	s.EndDrag(token, true)
	s.Drop(in("window-3"), token)
	detach()

	// Its tabs were closed while dragged.
	token = s.StartDrag(in("window-3"), []DragItem{{ID: a.ID}})
	s.Release(in("window-3"), a.ID)
	s.EndDrag(token, true)
	detach()

	if !slices.Equal(s.windows.created, []string{"window-1"}) {
		t.Fatalf("created %v; want no window for a claimed or empty drag", s.windows.created)
	}
	if _, ok := s.wins["window-2"]; ok {
		t.Fatal("an unused detach window was kept")
	}
	s.Launch([]string{"/b.pdf"}, "/")
	if !slices.Equal(s.windows.created, []string{"window-1"}) {
		t.Fatalf("created %v; launched files should still go to window-1", s.windows.created)
	}
}

func TestDragCancelledTellsEveryWindow(t *testing.T) {
	s := newService()
	s.DragCancelled(true)
	if ev := s.events.events[0]; ev.name != EventDragCancelled || ev.data != (Targeted{"", true}) {
		t.Fatalf("event %+v", ev)
	}
}
