package app

import (
	"context"
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/voidrab/gopdfrab"

	"viewrab/internal/pdfa"
	"viewrab/internal/testpdf"
	"viewrab/internal/ui/instance"
	"viewrab/internal/ui/pageview"
	"viewrab/internal/ui/platform"
	"viewrab/internal/ui/text"
	"viewrab/internal/ui/theme"
	"viewrab/internal/ui/widget"
)

// fixture wraps an App on a fake window of a known size, with its time and
// system dependencies replaced.
type fixture struct {
	t      *testing.T
	a      *App
	win    *platform.FakeWindow
	dir    string
	now    time.Time
	timers []func()
	picks  [][]string
	seen   int // the window management calls already looked at
}

const winW, winH = 900, 600

func newFixture(t *testing.T) *fixture {
	t.Helper()
	var plat platform.Fake
	w, err := plat.NewWindow(platform.WindowOptions{Size: platform.Size{W: winW, H: winH}})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, win: w.(*platform.FakeWindow), dir: t.TempDir(), now: time.Unix(1000, 0)}
	a, err := New(w, Options{
		Now:       func() time.Time { return f.now },
		AfterFunc: func(d time.Duration, fn func()) { f.timers = append(f.timers, fn) },
		Inspect:   func(*gopdfrab.Document) pdfa.Info { return pdfa.Info{Claimed: "PDF/A-1b"} },
	})
	if err != nil {
		t.Fatal(err)
	}
	f.a = a
	t.Cleanup(a.closeAll)
	return f
}

// pdf writes a PDF of n pages and returns its path.
func (f *fixture) pdf(name string, pages int) string {
	f.t.Helper()
	sizes := make([][2]int, pages)
	for i := range sizes {
		sizes[i] = [2]int{200, 300}
	}
	return testpdf.Write(f.t, f.dir, name, testpdf.BuildPages(sizes))
}

// start sizes the window, which opens anything that was waiting for it.
func (f *fixture) start() {
	f.a.Handle(platform.ResizeEvent{Size: platform.Size{W: winW, H: winH}, Scale: 1})
}

// open starts the app and opens the files.
func (f *fixture) open(paths ...string) {
	f.t.Helper()
	f.start()
	f.a.Open(paths...)
}

func (f *fixture) names() []string {
	var out []string
	for _, d := range f.a.docs {
		out = append(out, d.name)
	}
	return out
}

func (f *fixture) key(r rune, mods platform.Modifiers) {
	f.a.Handle(platform.KeyEvent{Rune: r, Down: true, Mods: mods})
}

func (f *fixture) special(k platform.Key, mods platform.Modifiers) {
	f.a.Handle(platform.KeyEvent{Key: k, Down: true, Mods: mods})
}

func (f *fixture) pointer(kind platform.PointerKind, b platform.Button, x, y int) {
	f.a.Handle(platform.PointerEvent{Kind: kind, Button: b, X: float64(x), Y: float64(y)})
}

func (f *fixture) click(x, y int) {
	f.pointer(platform.Move, platform.None, x, y)
	f.pointer(platform.Press, platform.Left, x, y)
	f.pointer(platform.Release, platform.Left, x, y)
}

// clickButton clicks a toolbar button.
func (f *fixture) clickButton(id widget.ButtonID) {
	r := f.a.toolbar.ButtonRect(id)
	f.click((r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2)
}

// tabPoint returns the window coordinates of the middle of tab i.
func (f *fixture) tabPoint(i int) (x, y int) {
	_, tabs, _, _ := f.a.layout()
	r := f.a.tabs.TabRect(i).Add(tabs.Min)
	return (r.Min.X + r.Max.X) / 2, (r.Min.Y + r.Max.Y) / 2
}

func TestFilesGivenBeforeTheWindowHasASizeOpenWhenItGetsOne(t *testing.T) {
	f := newFixture(t)
	f.a.Open(f.pdf("a.pdf", 2)) // no size yet: waits
	if len(f.a.docs) != 0 || len(f.a.pending) != 1 {
		t.Fatalf("docs %d, pending %d", len(f.a.docs), len(f.a.pending))
	}
	f.start()
	if got := f.names(); len(got) != 1 || got[0] != "a.pdf" || len(f.a.pending) != 0 {
		t.Errorf("after the first resize: %v, pending %d", got, len(f.a.pending))
	}
	// A view sized to the content area, below the toolbar and tab bar.
	_, _, content, _ := f.a.layout()
	if vp := f.a.docs[0].view.Layout().Viewport(); vp != image.Pt(content.Dx(), content.Dy()) || content.Min.Y != theme.ToolbarHeight+theme.TabBarHeight {
		t.Errorf("viewport %v for content %v", vp, content)
	}
}

func TestOpenReportsFailuresAndSwitchesToOpenFiles(t *testing.T) {
	f := newFixture(t)
	a := f.pdf("a.pdf", 1)
	b := f.pdf("b.pdf", 1)
	notPDF := testpdf.Write(t, f.dir, "notes.pdf", []byte("this is not a PDF"))
	f.open(a, filepath.Join(f.dir, "missing.pdf"), notPDF, b)
	if got := f.names(); len(got) != 2 || got[0] != "a.pdf" || got[1] != "b.pdf" {
		t.Fatalf("opened %v", got)
	}
	if f.a.messageKind != widget.MessageBad || f.a.message == "" {
		t.Errorf("message %q kind %d, want a failure", f.a.message, f.a.messageKind)
	}
	// Opening a file that is open switches to it instead of opening it twice.
	f.a.Open(a)
	if len(f.a.docs) != 2 || f.a.active != 0 {
		t.Errorf("docs %d, active %d", len(f.a.docs), f.a.active)
	}
	// A relative path resolves against the working directory and switches to
	// the tab already open.
	cwd, _ := os.Getwd()
	defer os.Chdir(cwd)
	os.Chdir(f.dir)
	f.a.Open("b.pdf")
	if len(f.a.docs) != 2 || f.a.active != 1 {
		t.Errorf("a relative path opened a copy: docs %d active %d", len(f.a.docs), f.a.active)
	}
}

func TestDrawPresentsTheWholeWindow(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("doc.pdf", 3))
	f.a.Handle(platform.FrameEvent{})
	shots := f.win.Presented()
	if len(shots) == 0 {
		t.Fatal("nothing was presented")
	}
	img := shots[len(shots)-1]
	if img.Rect.Dx() != winW || img.Rect.Dy() != winH {
		t.Fatalf("presented %v", img.Rect)
	}
	pal := theme.Light()
	px := func(x, y int) [3]uint8 { c := img.RGBAAt(x, y); return [3]uint8{c.R, c.G, c.B} }
	want := func(c [3]uint8) [3]uint8 { return c }
	if got := px(winW-5, 10); got != want([3]uint8{pal.Chrome.R, pal.Chrome.G, pal.Chrome.B}) {
		t.Errorf("toolbar background = %v", got)
	}
	if got := px(winW-5, winH-10); got != want([3]uint8{pal.Chrome.R, pal.Chrome.G, pal.Chrome.B}) {
		t.Errorf("status background = %v", got)
	}
	if f.win.Title() != "doc.pdf — Viewrab" {
		t.Errorf("title = %q", f.win.Title())
	}
	// The status line shows the file's name, pages, size and version on the
	// left, and the conformance it claims and the zoom on the right; the
	// toolbar shows the page and the zoom preset.
	st := f.a.status
	if !strings.HasPrefix(st.Info, "doc.pdf · 3 pages · ") || st.Claimed != "PDF/A-1b" || len(st.Right) != 1 || !strings.HasSuffix(st.Right[0], "%") {
		t.Errorf("status info %q claimed %q right %v", st.Info, st.Claimed, st.Right)
	}
	if tb := f.a.toolbar; tb.Page != 1 || tb.Pages != 3 || tb.ZoomText != "150%" || tb.NoDocument {
		t.Errorf("toolbar page %d / %d zoom %q", tb.Page, tb.Pages, tb.ZoomText)
	}
	// A second frame does not change the title.
	f.a.Handle(platform.FrameEvent{})
	// Before the window has a size, a frame draws nothing.
	g := newFixture(t)
	g.a.Handle(platform.FrameEvent{})
	if len(g.win.Presented()) != 0 {
		t.Error("a frame was presented before the window had a size")
	}
}

func TestNoDocumentShowsAHintAndDisablesTheToolbar(t *testing.T) {
	f := newFixture(t)
	f.start()
	f.a.Handle(platform.FrameEvent{})
	shots := f.win.Presented()
	img := shots[len(shots)-1]
	// The hint is drawn as ink in the middle of the empty content area.
	ink := false
	for y := winH/2 - 30; y < winH/2+30 && !ink; y++ {
		for x := winW/2 - 150; x < winW/2+150; x++ {
			if c := img.RGBAAt(x, y); c != img.RGBAAt(5, y) {
				ink = true
				break
			}
		}
	}
	if !ink {
		t.Error("the empty window shows no hint")
	}
	if !f.a.toolbar.NoDocument {
		t.Error("the toolbar shows the document controls with nothing open")
	}
	if f.win.Title() != "Viewrab" {
		t.Errorf("title = %q", f.win.Title())
	}
	// Buttons that need a document do nothing without one.
	for _, id := range []widget.ButtonID{widget.ButtonPrev, widget.ButtonNext, widget.ButtonZoomOut, widget.ButtonZoomIn, widget.ButtonZoomMode, widget.ButtonPDFA} {
		f.a.toolbarAction(widget.Action{Kind: widget.ActionButton, Index: int(id)})
	}
	f.key('x', 0) // a key with no document to receive it
	f.a.Handle(platform.ScrollEvent{DY: 5})
}

func TestTabShortcuts(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 1), f.pdf("b.pdf", 1), f.pdf("c.pdf", 1))
	if f.a.active != 2 {
		t.Fatalf("active = %d, want the last opened", f.a.active)
	}
	f.key('1', platform.Ctrl)
	if f.a.active != 0 {
		t.Errorf("Ctrl+1 -> %d", f.a.active)
	}
	f.special(platform.KeyTab, platform.Ctrl)
	if f.a.active != 1 {
		t.Errorf("Ctrl+Tab -> %d", f.a.active)
	}
	f.special(platform.KeyPageDown, platform.Ctrl)
	f.special(platform.KeyPageDown, platform.Ctrl) // wraps around
	if f.a.active != 0 {
		t.Errorf("Ctrl+PgDn twice -> %d, want 0", f.a.active)
	}
	f.special(platform.KeyTab, platform.Ctrl|platform.Shift)
	if f.a.active != 2 {
		t.Errorf("Ctrl+Shift+Tab from the first -> %d, want 2", f.a.active)
	}
	f.special(platform.KeyPageUp, platform.Ctrl)
	if f.a.active != 1 {
		t.Errorf("Ctrl+PgUp -> %d", f.a.active)
	}
	f.key('9', platform.Ctrl) // there is no ninth tab
	if f.a.active != 1 {
		t.Errorf("Ctrl+9 -> %d", f.a.active)
	}
	// Ctrl+W closes the active tab.
	f.key('w', platform.Ctrl)
	if got := f.names(); len(got) != 2 || got[0] != "a.pdf" || got[1] != "c.pdf" || f.a.active != 1 {
		t.Errorf("after Ctrl+W: %v active %d", got, f.a.active)
	}
}

func TestClosingTabsKeepsTheRightOneActive(t *testing.T) {
	cases := []struct {
		name          string
		active, close int
		wantActive    int
		remaining     []string
	}{
		{"close before the active tab", 2, 0, 1, []string{"b.pdf", "c.pdf"}},
		{"close the active tab in the middle", 1, 1, 1, []string{"a.pdf", "c.pdf"}},
		{"close the active last tab", 2, 2, 1, []string{"a.pdf", "b.pdf"}},
		{"close after the active tab", 0, 2, 0, []string{"a.pdf", "b.pdf"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.open(f.pdf("a.pdf", 1), f.pdf("b.pdf", 1), f.pdf("c.pdf", 1))
			f.a.active = tc.active
			f.a.closeTab(tc.close)
			got := f.names()
			if f.a.active != tc.wantActive || len(got) != len(tc.remaining) {
				t.Fatalf("active %d, docs %v", f.a.active, got)
			}
			for i := range got {
				if got[i] != tc.remaining[i] {
					t.Errorf("docs %v, want %v", got, tc.remaining)
				}
			}
		})
	}
	f := newFixture(t)
	f.open(f.pdf("only.pdf", 1))
	f.a.closeTab(5)  // past the last tab
	f.a.closeTab(-1) // a negative index
	f.a.closeTab(0)
	if len(f.a.docs) != 0 || f.a.active != -1 || f.a.current() != nil {
		t.Errorf("closing the only tab: docs %d active %d", len(f.a.docs), f.a.active)
	}
	// With no documents open, the layout gives the tab bar no space.
	if _, tabs, _, _ := f.a.layout(); !tabs.Empty() {
		t.Error("the tab bar stayed after the last tab closed")
	}
}

func TestToolbarButtonsDriveTheDocument(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 4))
	view := f.a.docs[0].view
	f.a.Handle(platform.FrameEvent{}) // brings the toolbar state up to date

	if !f.a.toolbar.Disabled[widget.ButtonPrev] || f.a.toolbar.Disabled[widget.ButtonNext] {
		t.Errorf("on the first page: prev disabled %v, next disabled %v", f.a.toolbar.Disabled[widget.ButtonPrev], f.a.toolbar.Disabled[widget.ButtonNext])
	}
	f.clickButton(widget.ButtonNext)
	if view.Page() != 1 {
		t.Errorf("Next -> page %d", view.Page())
	}
	f.a.Handle(platform.FrameEvent{})
	f.clickButton(widget.ButtonPrev)
	if view.Page() != 0 {
		t.Errorf("Prev -> page %d", view.Page())
	}
	f.a.Handle(platform.FrameEvent{})
	// On the last page Next is disabled.
	view.GoToPage(3)
	f.a.Handle(platform.FrameEvent{})
	if !f.a.toolbar.Disabled[widget.ButtonNext] {
		t.Error("Next is enabled on the last page")
	}

	before := view.Layout().Scale()
	f.clickButton(widget.ButtonZoomIn)
	if view.Layout().Scale() <= before {
		t.Error("Zoom in did not zoom in")
	}
	f.clickButton(widget.ButtonZoomOut)
	f.clickButton(widget.ButtonZoomOut)
	if view.Layout().Scale() >= before {
		t.Error("Zoom out did not zoom out")
	}
	view.Layout().SetMode(0)
	f.a.Handle(platform.FrameEvent{})
	f.clickButton(widget.ButtonZoomMode)
	if view.ZoomLabel() == "100%" || view.ZoomLabel() == "" {
		t.Errorf("zoom label %q", view.ZoomLabel())
	}
}

func TestToolbarOpenFindAndPDFA(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 1))
	f.a.Handle(platform.FrameEvent{})
	f.clickButton(widget.ButtonOpen)
	if f.a.messageKind != widget.MessageWarn { // no PickFiles option is set
		t.Errorf("Open without a dialog: kind %d message %q", f.a.messageKind, f.a.message)
	}
	f.clickButton(widget.ButtonFind)
	if !f.a.findBar.Open {
		t.Error("the Find button did not open the find bar")
	}
	f.key('n', platform.Ctrl)
	if f.a.message != "Could not open a window: "+errNoWindows.Error() {
		t.Errorf("Ctrl+N message %q", f.a.message)
	}
}

// drain runs the next function that a background goroutine handed back through
// inbox. It fails the test if none arrives within five seconds.
func (f *fixture) drain() {
	f.t.Helper()
	select {
	case fn := <-f.a.inbox:
		fn()
	case <-time.After(5 * time.Second):
		f.t.Fatal("no background work came back")
	}
}

func TestPickOpensWhatTheDialogReturns(t *testing.T) {
	f := newFixture(t)
	b := f.pdf("picked.pdf", 1)
	f.a.opts.PickFiles = func() ([]string, error) { return []string{b}, nil }
	f.open(f.pdf("a.pdf", 1))
	f.key('o', platform.Ctrl)
	f.drain()
	if got := f.names(); len(got) != 2 || got[1] != "picked.pdf" {
		t.Errorf("after Ctrl+O: %v", got)
	}
	f.key('t', platform.Ctrl) // already open: just switches to it
	f.drain()
	f.a.Handle(platform.FrameEvent{})
	// The new-tab button asks for files too.
	n := f.a.tabs.NewTabRect()
	_, tabs, _, _ := f.a.layout()
	f.click(tabs.Min.X+(n.Min.X+n.Max.X)/2, tabs.Min.Y+n.Dy()/2)
	f.drain()
	if len(f.a.docs) != 2 {
		t.Errorf("docs = %d", len(f.a.docs))
	}
	f.a.opts.PickFiles = func() ([]string, error) { return nil, errors.New("dialog failed") }
	f.key('o', platform.Ctrl)
	f.drain()
	if f.a.message != "dialog failed" || f.a.messageKind != widget.MessageBad {
		t.Errorf("message %q kind %d", f.a.message, f.a.messageKind)
	}
}

func TestOnlyOneFileDialogIsOpenAtATime(t *testing.T) {
	f := newFixture(t)
	release := make(chan struct{})
	calls := make(chan struct{}, 4)
	f.a.opts.PickFiles = func() ([]string, error) {
		calls <- struct{}{}
		<-release
		return nil, nil // the user cancelled
	}
	f.start()
	f.key('o', platform.Ctrl)
	f.key('o', platform.Ctrl) // while the first is still open
	f.a.toolbarAction(widget.Action{Kind: widget.ActionButton, Index: int(widget.ButtonOpen)})
	<-calls
	close(release)
	f.drain()
	select {
	case <-calls:
		t.Error("a second dialog was opened while the first was open")
	default:
	}
	// Once it has closed, another can be opened.
	f.a.opts.PickFiles = func() ([]string, error) { return nil, nil }
	f.key('o', platform.Ctrl)
	f.drain()
}

func TestControlQQuits(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 1))
	if !f.a.Handle(platform.KeyEvent{Rune: 'q', Down: true, Mods: platform.Ctrl}) {
		t.Error("Ctrl+Q did not quit")
	}
	if f.a.Handle(platform.KeyEvent{Rune: 'q', Down: true}) {
		t.Error("a plain q quit")
	}
	if f.a.Handle(platform.KeyEvent{Rune: 'q', Mods: platform.Ctrl}) {
		t.Error("releasing Ctrl+Q quit")
	}
}

func TestMessagesExpire(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 1))
	f.a.say(widget.MessageOK, "hello")
	if len(f.timers) != 1 {
		t.Fatalf("%d timers scheduled", len(f.timers))
	}
	f.a.Handle(platform.FrameEvent{})
	if f.a.status.Message != "hello" {
		t.Errorf("message not shown: %q", f.a.status.Message)
	}
	f.now = f.now.Add(messageTime + time.Second)
	f.timers[0]() // the timer asks for a redraw
	f.a.Handle(platform.FrameEvent{})
	if f.a.status.Message != "" || f.a.message != "" {
		t.Errorf("message still shown: %q", f.a.status.Message)
	}
}

func TestTabBarClicksAndDrags(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 1), f.pdf("b.pdf", 1), f.pdf("c.pdf", 1))
	f.a.Handle(platform.FrameEvent{})

	x, y := f.tabPoint(0)
	f.click(x, y)
	if f.a.active != 0 {
		t.Errorf("clicking tab 0 -> active %d", f.a.active)
	}
	// The close button of tab 1.
	_, tabs, _, _ := f.a.layout()
	cr := f.a.tabs.CloseRect(1).Add(tabs.Min)
	f.click((cr.Min.X+cr.Max.X)/2, (cr.Min.Y+cr.Max.Y)/2)
	if got := f.names(); len(got) != 2 || got[1] != "c.pdf" {
		t.Errorf("after closing tab 1: %v", got)
	}
	f.a.Handle(platform.FrameEvent{})

	// Drag the first tab past the last and release it just below the bar. The
	// press keeps the pointer until the button is released, so the drop still
	// reorders.
	f.a.Handle(platform.FrameEvent{})
	x, y = f.tabPoint(0)
	last := f.a.tabs.TabRect(1)
	f.pointer(platform.Press, platform.Left, x, y)
	f.pointer(platform.Move, platform.None, x+30, y)
	below := tabs.Max.Y + 5
	f.pointer(platform.Move, platform.None, tabs.Min.X+last.Max.X-3, below)
	f.pointer(platform.Release, platform.Left, tabs.Min.X+last.Max.X-3, below)
	if got := f.names(); got[0] != "c.pdf" || got[1] != "a.pdf" || f.a.docs[f.a.active].name != "a.pdf" {
		t.Errorf("after the drag: %v, active %q", got, f.a.docs[f.a.active].name)
	}
	// Reordering with indexes outside the tabs does nothing.
	f.a.move(-1, 0)
	f.a.move(0, 9)
	f.a.move(1, 1)
	if got := f.names(); got[0] != "c.pdf" {
		t.Errorf("a bad move changed the order: %v", got)
	}
	// Selecting a tab that does not exist is ignored.
	f.a.tabAction(widget.Action{Kind: widget.ActionSelect, Index: 7})
	f.a.tabAction(widget.Action{Kind: widget.ActionNone})
	f.a.toolbarAction(widget.Action{Kind: widget.ActionSelect})
}

func TestPointerRoutingAndHover(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 2))
	f.a.Handle(platform.FrameEvent{})
	r := f.a.toolbar.ButtonRect(widget.ButtonOpen)
	// Moving the pointer from the toolbar onto the page leaves the toolbar.
	f.pointer(platform.Move, platform.None, r.Min.X+3, r.Min.Y+3)
	if f.a.hover != sectionToolbar {
		t.Errorf("hover = %d", f.a.hover)
	}
	f.pointer(platform.Move, platform.None, 400, 300)
	if f.a.hover != sectionContent {
		t.Errorf("hover = %d", f.a.hover)
	}
	// A press on the page, or on the status line, belongs to no widget.
	f.pointer(platform.Press, platform.Left, 400, 300)
	f.pointer(platform.Release, platform.Left, 400, 300)
	f.click(400, winH-5)
	// The pointer leaving the window ends the hover.
	f.pointer(platform.Leave, platform.None, 0, 0)
	if f.a.hover != sectionNone {
		t.Errorf("hover after leaving = %d", f.a.hover)
	}
	// Events with no section under them, such as beyond the window, are harmless.
	f.pointer(platform.Move, platform.None, winW+50, -20)
	if s := f.a.hit(image.Pt(-1, -1)); s != sectionNone {
		t.Errorf("section outside the window = %d", s)
	}
	if o := f.a.origin(sectionNone); o != (image.Point{}) {
		t.Errorf("origin of nothing = %v", o)
	}
}

func TestScrollAndKeysReachTheView(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 5))
	view := f.a.docs[0].view
	f.a.Handle(platform.ScrollEvent{DY: 10})
	if view.Layout().Scroll().Y == 0 {
		t.Error("the wheel did not scroll")
	}
	y := view.Layout().Scroll().Y
	f.special(platform.KeyPageDown, 0)
	if view.Layout().Scroll().Y <= y {
		t.Error("Page Down did not scroll")
	}
	// A key release is ignored, and so is a key the view does not use.
	f.a.Handle(platform.KeyEvent{Key: platform.KeyDown})
	f.special(platform.KeyTab, 0)
	f.a.Handle(platform.FocusEvent{Focused: true})
	// Ctrl+0 is not an app shortcut, so the view handles it.
	f.key('+', platform.Ctrl)
	f.key('0', platform.Ctrl)
	if view.ZoomLabel() != pageview.DefaultZoom.Label {
		t.Errorf("Ctrl+0 left zoom %q", view.ZoomLabel())
	}
}

func TestResizeRelaysOutEveryDocument(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 1), f.pdf("b.pdf", 1))
	f.a.Handle(platform.ResizeEvent{Size: platform.Size{W: 500, H: 400}, Scale: 1})
	_, _, content, _ := f.a.layout()
	for _, d := range f.a.docs {
		if vp := d.view.Layout().Viewport(); vp != image.Pt(content.Dx(), content.Dy()) || content.Dx() != 500 {
			t.Errorf("%s viewport %v, content %v", d.name, vp, content)
		}
	}
	// In a window too small for the chrome, the content area is empty and the
	// status line does not overlap it.
	f.a.Handle(platform.ResizeEvent{Size: platform.Size{W: 40, H: 30}, Scale: 1})
	f.a.Handle(platform.FrameEvent{})
	_, _, tiny, status := f.a.layout()
	if tiny.Dy() != 0 || status.Min.Y < tiny.Max.Y {
		t.Errorf("tiny layout: content %v status %v", tiny, status)
	}
}

func TestHandleReportsQuit(t *testing.T) {
	f := newFixture(t)
	if f.a.Handle(platform.CloseEvent{}) != true {
		t.Error("a close request did not quit")
	}
	if f.a.Handle(platform.FrameEvent{}) {
		t.Error("a frame quit the app")
	}
}

func TestHumanSize(t *testing.T) {
	for n, want := range map[int64]string{
		0: "0 B", 1023: "1023 B", 1024: "1.0 KB", 281927: "275 KB", 5 << 20: "5.0 MB", 3 << 30: "3.0 GB", 5 << 40: "5120 GB",
	} {
		if got := humanSize(n); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestNewReportsFaceFailures(t *testing.T) {
	boom := errors.New("boom")
	orig := newFace
	defer func() { newFace = orig }()
	var plat platform.Fake
	w, _ := plat.NewWindow(platform.WindowOptions{Size: platform.Size{W: 100, H: 100}})
	for fail := 1; fail <= 3; fail++ {
		calls := 0
		newFace = func(size float64, bold bool) (*text.Face, error) {
			calls++
			if calls == fail {
				return nil, boom
			}
			return orig(size, bold)
		}
		if _, err := New(w, Options{}); !errors.Is(err, boom) {
			t.Errorf("failure of face %d: %v", fail, err)
		}
	}
}

func TestOpenReportsAViewThatCannotBeBuilt(t *testing.T) {
	f := newFixture(t)
	f.start()
	boom := errors.New("view failed")
	orig := newView
	defer func() { newView = orig }()
	newView = func(pageview.Document, image.Point, pageview.Options, func()) (*pageview.View, error) {
		return nil, boom
	}
	f.a.Open(f.pdf("a.pdf", 1))
	if len(f.a.docs) != 0 || f.a.messageKind != widget.MessageBad {
		t.Errorf("docs %d, message %q", len(f.a.docs), f.a.message)
	}
}

func TestNewAppliesDefaults(t *testing.T) {
	var plat platform.Fake
	w, _ := plat.NewWindow(platform.WindowOptions{Size: platform.Size{W: 400, H: 300}})
	a, err := New(w, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if a.opts.Palette != theme.Light() || a.opts.Verify == nil || a.opts.Inspect == nil || a.opts.Now == nil || a.opts.AfterFunc == nil {
		t.Error("defaults were not applied")
	}
	// The default AfterFunc runs its function after the delay.
	fired := make(chan struct{})
	a.opts.AfterFunc(time.Millisecond, func() { close(fired) })
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("the default AfterFunc never fired")
	}
	if a.opts.Now().IsZero() {
		t.Error("the default clock is zero")
	}
}

// waitFor polls until cond holds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestRunDrivesTheWindow(t *testing.T) {
	var plat platform.Fake
	w, _ := plat.NewWindow(platform.WindowOptions{Size: platform.Size{W: winW, H: winH}})
	fw := w.(*platform.FakeWindow)
	dir := t.TempDir()
	path := testpdf.Write(t, dir, "run.pdf", testpdf.BuildPages([][2]int{{200, 300}, {200, 300}}))

	// A fixture with the same document gives the position of the PDF/A button,
	// and of the Verify button in the panel it opens.
	ref := newFixture(t)
	ref.open(path)
	ref.a.Handle(platform.FrameEvent{})
	btn := ref.a.toolbar.ButtonRect(widget.ButtonPDFA)
	bx, by := (btn.Min.X+btn.Max.X)/2, (btn.Min.Y+btn.Max.Y)/2
	ref.a.setPanel(true)
	ref.a.Handle(platform.FrameEvent{})
	panel := ref.a.panelRect()
	vr := ref.a.panel.ItemRect(widget.PanelAction{Kind: widget.PanelVerify}, panel.Dx()).Add(panel.Min)
	vx, vy := (vr.Min.X+vr.Max.X)/2, (vr.Min.Y+vr.Max.Y)/2

	errc := make(chan error, 1)
	go func() {
		errc <- Run(w, []string{path}, Options{
			Verify: func(context.Context, string, string) (pdfa.Report, error) {
				return pdfa.Report{Valid: true}, nil
			},
		})
	}()
	// The fake window starts with a resize and a frame: the file opens and is drawn.
	waitFor(t, "the first page to be rendered and presented", func() bool {
		for _, img := range fw.Presented() {
			if img.RGBAAt(winW/2, 200) == (color.RGBA{255, 0, 0, 255}) { // the page, rendered
				return true
			}
		}
		return false
	})
	if fw.Title() != "run.pdf — Viewrab" {
		t.Errorf("title = %q", fw.Title())
	}

	// The PDF/A button opens the panel, whose Verify button checks in the
	// background; the result comes back through the event loop and shows in
	// the panel in the OK colour.
	ok := theme.Light().OK
	okPixel := color.RGBA{ok.R, ok.G, ok.B, 255}
	click := func(x, y int) {
		fw.Send(platform.PointerEvent{Kind: platform.Move, X: float64(x), Y: float64(y)})
		fw.Send(platform.PointerEvent{Kind: platform.Press, Button: platform.Left, X: float64(x), Y: float64(y)})
		fw.Send(platform.PointerEvent{Kind: platform.Release, Button: platform.Left, X: float64(x), Y: float64(y)})
	}
	click(bx, by)
	click(vx, vy)
	waitFor(t, "the verification result in the panel", func() bool {
		shots := fw.Presented()
		if len(shots) == 0 {
			return false
		}
		img := shots[len(shots)-1]
		for y := panel.Min.Y; y < panel.Max.Y; y++ {
			for x := panel.Min.X; x < panel.Max.X; x++ {
				if img.RGBAAt(x, y) == okPixel {
					return true
				}
			}
		}
		return false
	})

	fw.Send(platform.CloseEvent{})
	select {
	case err := <-errc:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after the window closed")
	}
	if !fw.Closed() {
		t.Error("the window was left open")
	}
}

func TestRunEndsWhenTheEventStreamEnds(t *testing.T) {
	var plat platform.Fake
	w, _ := plat.NewWindow(platform.WindowOptions{Size: platform.Size{W: 300, H: 200}})
	errc := make(chan error, 1)
	go func() { errc <- Run(w, nil, Options{}) }()
	w.(*platform.FakeWindow).Close()
	select {
	case err := <-errc:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return when the stream ended")
	}
}

func TestRunReportsFailuresToCreateTheApp(t *testing.T) {
	orig := newFace
	defer func() { newFace = orig }()
	boom := errors.New("no font")
	newFace = func(float64, bool) (*text.Face, error) { return nil, boom }
	var plat platform.Fake
	w, _ := plat.NewWindow(platform.WindowOptions{Size: platform.Size{W: 300, H: 200}})
	if err := Run(w, nil, Options{}); !errors.Is(err, boom) {
		t.Errorf("Run = %v", err)
	}
	if !w.(*platform.FakeWindow).Closed() {
		t.Error("the window was left open")
	}
}

// content returns the content area, the page view's place in the window.
func (f *fixture) content() image.Rectangle {
	_, _, content, _ := f.a.layout()
	return content
}

func TestDraggingThePageScrollsIt(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 6))
	c := f.content()
	view := f.a.current().view
	x, y := c.Min.X+c.Dx()/2, c.Min.Y+c.Dy()/2
	f.pointer(platform.Move, platform.None, x, y)
	if s := view.Layout().Scroll(); s != (image.Point{}) {
		t.Fatalf("a new document opened scrolled to %v", s)
	}
	if got := view.ZoomLabel(); got != "150%" {
		t.Fatalf("a new document opened at zoom %q", got)
	}
	f.pointer(platform.Press, platform.Middle, x, y)
	f.pointer(platform.Move, platform.None, x, y-60)
	if got := view.Layout().Scroll().Y; got != 60 {
		t.Fatalf("dragging up by 60 scrolled to %d", got)
	}
	// The drag goes on beyond the content area, into the toolbar.
	f.pointer(platform.Move, platform.None, x, 2)
	if got := view.Layout().Scroll().Y; got <= 60 {
		t.Errorf("dragging out of the content area stopped the drag: scroll %d", got)
	}
	f.pointer(platform.Release, platform.Middle, x, 2)
	at := view.Layout().Scroll()
	f.pointer(platform.Move, platform.None, x, y+100)
	if view.Layout().Scroll() != at {
		t.Error("the page followed the pointer after the button was released")
	}
}

func TestPointerOverContentWithNoDocumentIsIgnored(t *testing.T) {
	f := newFixture(t)
	f.start()
	c := f.content()
	f.pointer(platform.Press, platform.Left, c.Min.X+10, c.Min.Y+10)
	f.pointer(platform.Release, platform.Left, c.Min.X+10, c.Min.Y+10)
}

func TestScrollbarThumbDragsInTheWindow(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 12))
	c := f.content()
	view := f.a.current().view
	x := c.Max.X - 8 // over the vertical scrollbar, inside the window's frame
	f.pointer(platform.Move, platform.None, x, c.Min.Y+4)
	f.pointer(platform.Press, platform.Left, x, c.Min.Y+4)
	f.pointer(platform.Move, platform.None, x, c.Max.Y)
	if got, max := view.Layout().Scroll().Y, view.Layout().DocumentSize().Y-view.Layout().Viewport().Y; got != max {
		t.Errorf("dragging the thumb to the bottom scrolled to %d of %d", got, max)
	}
	f.pointer(platform.Release, platform.Left, x, c.Max.Y)
}

func TestWheelZoomUsesPointerPositionRelativeToTheContent(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 6))
	c := f.content()
	view := f.a.current().view
	view.Layout().ScrollTo(image.Pt(0, 500))
	at := image.Pt(c.Dx()/2, c.Dy()/3) // in content coordinates
	page, pt, ok := view.Layout().HitPage(at)
	if !ok {
		t.Fatal("no page under the pointer")
	}
	f.a.Handle(platform.ScrollEvent{DY: -10, X: float64(c.Min.X + at.X), Y: float64(c.Min.Y + at.Y), Mods: platform.Ctrl})
	got, gpt, ok := view.Layout().HitPage(at)
	if !ok || got != page || gpt.H-pt.H > 1 || pt.H-gpt.H > 1 {
		t.Errorf("under the pointer: page %d at %.1f, was page %d at %.1f", got, gpt.H, page, pt.H)
	}
}

func TestLaterLaunchesOpenTabsAndRaiseTheWindow(t *testing.T) {
	var plat platform.Fake
	w, _ := plat.NewWindow(platform.WindowOptions{Size: platform.Size{W: winW, H: winH}})
	fw := w.(*platform.FakeWindow)
	dir := t.TempDir()
	first := testpdf.Write(t, dir, "first.pdf", testpdf.BuildPages([][2]int{{200, 300}}))
	second := testpdf.Write(t, dir, "second.pdf", testpdf.BuildPages([][2]int{{200, 300}}))
	requests := make(chan instance.Request)
	done := make(chan error, 1)
	go func() { done <- Run(w, []string{first}, Options{Requests: requests}) }()

	requests <- instance.Request{Paths: []string{second}, Token: "launcher-token"}
	waitFor(t, "the window to be activated", func() bool { return len(fw.Activations()) == 1 })
	if got := fw.Activations()[0]; got != "launcher-token" {
		t.Errorf("activated with %q", got)
	}
	waitFor(t, "both tabs", func() bool {
		return fw.Title() == "second.pdf — Viewrab"
	})
	// The window keeps working after the request channel is closed.
	close(requests)
	if err := fw.Send(platform.CloseEvent{}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the close event")
	}
}

// pump hands the app the window's queued events until done reports one.
func (f *fixture) pump(done func(platform.Event) bool) {
	f.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev := <-f.win.Events():
			f.a.Handle(ev)
			if done(ev) {
				return
			}
		case <-timeout:
			f.t.Fatal("the awaited window event never came")
		}
	}
}

// textFixture opens a document whose two pages read "alpha beta" and "beta
// gamma", and returns the fixture.
func textFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	path := testpdf.Write(t, f.dir, "text.pdf", testpdf.BuildText([]string{"alpha beta", "beta gamma"}))
	f.open(path)
	return f
}

// settled waits until the active view's search has finished and returns its
// status.
func settled(t *testing.T, f *fixture) (current, total int) {
	t.Helper()
	view := f.a.current().view
	var searching bool
	waitFor(t, "the search to finish", func() bool {
		current, total, searching = view.FindStatus()
		return !searching
	})
	return current, total
}

func TestCtrlFOpensTheFindBarAndSearches(t *testing.T) {
	f := textFixture(t)
	if f.a.findBar.Open {
		t.Fatal("the find bar starts open")
	}
	f.key('f', platform.Ctrl)
	if !f.a.findBar.Open || f.a.findDoc != f.a.current() {
		t.Fatal("Ctrl+F did not open the find bar")
	}
	for _, r := range "beta" {
		f.key(r, 0)
	}
	if string(f.a.findBar.Text) != "beta" {
		t.Fatalf("typed %q", string(f.a.findBar.Text))
	}
	if cur, total := settled(t, f); cur != 1 || total != 2 {
		t.Fatalf("match %d of %d, want 1 of 2", cur, total)
	}
	f.a.Handle(platform.FrameEvent{}) // the bar shows the count
	if f.a.findBar.Current != 1 || f.a.findBar.Total != 2 {
		t.Errorf("bar shows %d of %d", f.a.findBar.Current, f.a.findBar.Total)
	}
	f.special(platform.KeyEnter, 0)
	if cur, _, _ := f.a.current().view.FindStatus(); cur != 2 {
		t.Errorf("Enter went to match %d, want 2", cur)
	}
	f.special(platform.KeyEnter, platform.Shift)
	if cur, _, _ := f.a.current().view.FindStatus(); cur != 1 {
		t.Errorf("Shift+Enter went to match %d, want 1", cur)
	}
	f.special(platform.KeyBackspace, 0)
	if cur, total := settled(t, f); total != 2 || cur < 1 { // "bet" still matches twice
		t.Errorf("after backspace: %d of %d", cur, total)
	}
	// Escape closes the bar and clears the search.
	f.special(platform.KeyEscape, 0)
	if f.a.findBar.Open {
		t.Error("Escape left the find bar open")
	}
	if _, total, _ := f.a.current().view.FindStatus(); total != 0 {
		t.Errorf("%d matches remain after closing", total)
	}
}

func TestClickingTheFindFieldStartsASearch(t *testing.T) {
	f := textFixture(t)
	f.clickButton(widget.ButtonFind)
	if !f.a.findBar.Open {
		t.Fatal("clicking the find field did not start a search")
	}
	f.a.Handle(platform.FrameEvent{})
	if !f.a.findBar.Open {
		t.Error("the find field does not show that it has the keys")
	}
	// A second click stays in the field; Escape ends the search and clears it.
	f.clickButton(widget.ButtonFind)
	f.key('x', 0)
	if !f.a.findBar.Open || string(f.a.findBar.Text) != "x" {
		t.Errorf("after a second click: open %v text %q", f.a.findBar.Open, string(f.a.findBar.Text))
	}
	f.special(platform.KeyEscape, 0)
	if f.a.findBar.Open || len(f.a.findBar.Text) != 0 {
		t.Error("Escape did not end the search")
	}
	// The toolbar's own handling of the field, which the find field normally
	// takes first, starts a search too.
	f.a.toolbarAction(widget.Action{Kind: widget.ActionButton, Index: int(widget.ButtonFind)})
	if !f.a.findBar.Open {
		t.Error("the toolbar's find button did not start a search")
	}
	f.special(platform.KeyEscape, 0)
	// Opening an open bar keeps its text. With no document the bar stays
	// closed.
	f.key('f', platform.Ctrl)
	f.a.findBar.SetText("kept")
	f.key('f', platform.Ctrl)
	if string(f.a.findBar.Text) != "kept" {
		t.Errorf("opening the bar again reset its text to %q", string(f.a.findBar.Text))
	}
	empty := newFixture(t)
	empty.start()
	empty.key('f', platform.Ctrl)
	if empty.a.findBar.Open {
		t.Error("the find bar opened with no document")
	}
}

func TestFindStartsFromTheSelectedText(t *testing.T) {
	f := textFixture(t)
	view := f.a.current().view
	c := f.content()
	waitFor(t, "the page's text", func() bool {
		f.a.Handle(platform.FrameEvent{})
		return view.Layout().Pages() > 0 && selectWord(f, view, c)
	})
	f.key('f', platform.Ctrl)
	if got := string(f.a.findBar.Text); got != "alpha" {
		t.Fatalf("the bar starts with %q, want the selected word", got)
	}
	if _, total := settled(t, f); total != 1 {
		t.Errorf("%d matches of the selected word, want 1", total)
	}
}

// selectWord double-clicks the first word of the first page, reporting whether
// anything came to be selected.
func selectWord(f *fixture, view interface{ HasSelection() bool }, c image.Rectangle) bool {
	f.a.current().view.ClearSelection()
	// The page is centred; "alpha" starts 20 points from its left and 100 from
	// its bottom (82 from its top in a 200 point page), at the page's scale.
	vr := f.a.current().view.Layout().ViewRect(0)
	scale := float64(vr.Dx()) / 200
	p := image.Pt(c.Min.X+vr.Min.X+int(30*scale), c.Min.Y+vr.Min.Y+int(90*scale))
	for range 2 {
		f.pointer(platform.Press, platform.Left, p.X, p.Y)
		f.pointer(platform.Release, platform.Left, p.X, p.Y)
	}
	return view.HasSelection()
}

func TestCopyTakesTheSelectionToTheClipboard(t *testing.T) {
	f := textFixture(t)
	f.key('c', platform.Ctrl)
	if f.a.message != "Nothing is selected" || f.win.Clipboard() != "" {
		t.Errorf("copying nothing: message %q, clipboard %q", f.a.message, f.win.Clipboard())
	}
	view := f.a.current().view
	waitFor(t, "a selection", func() bool {
		f.a.Handle(platform.FrameEvent{})
		return selectWord(f, view, f.content())
	})
	f.key('c', platform.Ctrl)
	if f.win.Clipboard() != "alpha" || f.a.messageKind != widget.MessageOK {
		t.Errorf("clipboard %q, message %q", f.win.Clipboard(), f.a.message)
	}
	// With no document, Ctrl+C does nothing.
	empty := newFixture(t)
	empty.start()
	empty.key('c', platform.Ctrl)
	if empty.win.Clipboard() != "" {
		t.Error("copy with no document set the clipboard")
	}
}

func TestPasteGoesIntoTheFindBar(t *testing.T) {
	f := textFixture(t)
	f.key('v', platform.Ctrl) // no bar: nothing is asked
	f.win.SetClipboard("gamma")
	f.key('f', platform.Ctrl)
	f.key('v', platform.Ctrl)
	f.pump(func(ev platform.Event) bool { _, ok := ev.(platform.PasteEvent); return ok })
	if string(f.a.findBar.Text) != "gamma" {
		t.Fatalf("pasted %q", string(f.a.findBar.Text))
	}
	if _, total := settled(t, f); total != 1 {
		t.Errorf("%d matches of the pasted text, want 1", total)
	}
	// A paste with the bar closed is ignored.
	f.special(platform.KeyEscape, 0)
	f.a.Handle(platform.PasteEvent{Text: "ignored"})
	if string(f.a.findBar.Text) == "gammaignored" {
		t.Error("a paste with the bar closed changed its text")
	}
}

func TestFindBarTakesKeysAndPointerButNotNavigation(t *testing.T) {
	f := textFixture(t)
	f.key('f', platform.Ctrl)
	view := f.a.current().view
	// Keys that move through the document still do.
	f.special(platform.KeyPageDown, 0)
	if view.Layout().Scroll().Y == 0 {
		t.Error("PageDown with the bar open did not scroll")
	}
	// Other keys, such as the arrows that move the caret, do not reach the view.
	before := view.Layout().Scroll()
	f.special(platform.KeyLeft, 0)
	f.special(platform.KeyHome, 0)
	if view.Layout().Scroll() != before {
		t.Error("a caret key scrolled the document")
	}
	// A click on the field's next button steps to the next match, and does
	// not reach the toolbar.
	for _, r := range "a" {
		f.key(r, 0)
	}
	settled(t, f)
	r := f.a.findRect()
	f.click(r.Max.X-10, (r.Min.Y+r.Max.Y)/2)
	if cur, total := settled(t, f); total < 2 || cur != 2 {
		t.Errorf("after clicking next: match %d of %d, want 2", cur, total)
	}
	// A click in the content is for the page, and the search goes on.
	c := f.content()
	f.pointer(platform.Press, platform.Left, c.Min.X+5, c.Min.Y+c.Dy()/2)
	f.pointer(platform.Move, platform.None, c.Min.X+5, c.Min.Y+c.Dy()/2-30)
	f.pointer(platform.Release, platform.Left, c.Min.X+5, c.Min.Y+c.Dy()/2-30)
	if view.Layout().Scroll().Y == before.Y || !f.a.findBar.Open {
		t.Errorf("dragging the page: scrolled %v, find open %v", view.Layout().Scroll().Y != before.Y, f.a.findBar.Open)
	}
}

func TestSwitchingTabsClosesTheFindBar(t *testing.T) {
	f := textFixture(t)
	f.open(f.pdf("other.pdf", 1))
	f.a.selectTab(0)
	f.key('f', platform.Ctrl)
	f.a.selectTab(1)
	f.a.Handle(platform.FrameEvent{})
	if f.a.findBar.Open {
		t.Error("the find bar stayed open on another tab")
	}
}

func TestFirstLineOfASelection(t *testing.T) {
	long := strings.Repeat("é", 150)
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"one line", "one line"},
		{"  padded \nsecond", "padded"},
		{long, strings.Repeat("é", 100)},
	} {
		if got := firstLine(tc.in); got != tc.want {
			t.Errorf("firstLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFindActionsWithoutADocumentAreIgnored(t *testing.T) {
	f := newFixture(t)
	f.start()
	f.a.findAction(widget.FindEdited)
	f.a.findAction(widget.FindNext)
	f.a.closeFind()
}

func TestSelectAllSeedsTheFindBarWithItsFirstLine(t *testing.T) {
	f := textFixture(t)
	f.key('a', platform.Ctrl)
	f.key('f', platform.Ctrl)
	if got := string(f.a.findBar.Text); got != "alpha beta" {
		t.Errorf("the bar starts with %q, want the first line of the selection", got)
	}
}

func TestRenderOptionRendersThePagesOfEachPath(t *testing.T) {
	var plat platform.Fake
	w, _ := plat.NewWindow(platform.WindowOptions{Size: platform.Size{W: winW, H: winH}})
	type call struct {
		path string
		page int
	}
	calls := make(chan call, 64)
	a, err := New(w, Options{
		Inspect: func(*gopdfrab.Document) pdfa.Info { return pdfa.Info{} },
		Render: func(_ context.Context, path string, page int, _ float64, region image.Rectangle) (*image.RGBA, error) {
			calls <- call{path, page}
			return image.NewRGBA(image.Rect(0, 0, region.Dx(), region.Dy())), nil
		},
		RenderWorkers: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.closeAll)
	path := testpdf.Write(t, t.TempDir(), "a.pdf", testpdf.BuildPages([][2]int{{200, 300}}))
	a.Handle(platform.ResizeEvent{Size: platform.Size{W: winW, H: winH}, Scale: 1})
	a.Open(path)
	a.Handle(platform.FrameEvent{})
	select {
	case c := <-calls:
		if c.path != a.docs[0].path || c.page != 0 {
			t.Errorf("rendered page %d of %q, want page 0 of %q", c.page, c.path, a.docs[0].path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no page rendered through Options.Render")
	}
}

func TestPoolOptions(t *testing.T) {
	render := func(context.Context, string, int, float64, image.Rectangle) (*image.RGBA, error) { return nil, nil }
	for _, tc := range []struct {
		opts Options
		want pageview.PoolOptions
	}{
		{Options{}, pageview.PoolOptions{}},
		{Options{RenderWorkers: 3}, pageview.PoolOptions{}},
		{Options{Render: render}, pageview.PoolOptions{Workers: 1}},
		{Options{Render: render, RenderWorkers: 3}, pageview.PoolOptions{Workers: 3}},
	} {
		if got := poolOptions(tc.opts); got != tc.want {
			t.Errorf("poolOptions(Render set %v, RenderWorkers %d) = %+v, want %+v",
				tc.opts.Render != nil, tc.opts.RenderWorkers, got, tc.want)
		}
	}
}

// wordAt returns the window point over the word "alpha" of the text fixture.
func wordAt(f *fixture) image.Point {
	c := f.content()
	vr := f.a.current().view.Layout().ViewRect(0)
	scale := float64(vr.Dx()) / 200
	return image.Pt(c.Min.X+vr.Min.X+int(30*scale), c.Min.Y+vr.Min.Y+int(90*scale))
}

func TestATextCursorShowsOverText(t *testing.T) {
	f := textFixture(t)
	p := wordAt(f)
	waitFor(t, "the text cursor over text", func() bool {
		f.a.Handle(platform.FrameEvent{})
		f.pointer(platform.Move, platform.None, p.X, p.Y)
		return f.win.Cursor() == platform.CursorText
	})
	// Off the text, on the page's blank part, the cursor is the default one.
	vr := f.a.current().view.Layout().ViewRect(0).Add(f.content().Min)
	f.pointer(platform.Move, platform.None, vr.Min.X+5, vr.Min.Y+5)
	if f.win.Cursor() != platform.CursorDefault {
		t.Errorf("cursor off the text = %d", f.win.Cursor())
	}
}

func TestTheContextMenuCopiesAndSelectsAll(t *testing.T) {
	f := textFixture(t)
	view := f.a.current().view
	p := wordAt(f)
	waitFor(t, "the text read", func() bool {
		f.a.Handle(platform.FrameEvent{})
		return selectWord(f, view, f.content())
	})
	view.ClearSelection()
	// A right press opens the menu at the pointer, with Copy disabled while
	// nothing is selected, and leaves no section holding the pointer.
	f.pointer(platform.Press, platform.Right, p.X, p.Y)
	if !f.a.menu.Open || f.a.menuKind != menuContext || f.a.capture != sectionNone {
		t.Fatalf("menu open %v, kind %d, capture %d", f.a.menu.Open, f.a.menuKind, f.a.capture)
	}
	if r := f.a.menu.Rect(); r.Min != p {
		t.Errorf("menu at %v, want its corner at %v", r.Min, p)
	}
	if !f.a.menu.Disabled[contextCopy] || f.a.menu.Disabled[contextSelectAll] {
		t.Errorf("disabled = %v", f.a.menu.Disabled)
	}
	if f.win.Cursor() != platform.CursorDefault {
		t.Errorf("cursor over the menu = %d", f.win.Cursor())
	}
	f.pointer(platform.Release, platform.Right, p.X, p.Y)
	if !f.a.menu.Open {
		t.Fatal("the release of the right press closed the menu")
	}
	// Select All selects the document's text.
	r := f.a.menu.Rect()
	row := func(i int) image.Point {
		return image.Pt(r.Min.X+r.Dx()/2, r.Min.Y+4+i*26+13)
	}
	sel := row(contextSelectAll)
	f.click(sel.X, sel.Y)
	if f.a.menu.Open || !view.HasSelection() {
		t.Fatalf("after Select All: open %v, selection %v", f.a.menu.Open, view.HasSelection())
	}
	// Copy, now enabled, takes it to the clipboard.
	f.pointer(platform.Press, platform.Right, p.X, p.Y)
	f.pointer(platform.Release, platform.Right, p.X, p.Y)
	if f.a.menu.Disabled[contextCopy] {
		t.Error("Copy is disabled with text selected")
	}
	r = f.a.menu.Rect()
	cp := row(contextCopy)
	f.click(cp.X, cp.Y)
	if f.win.Clipboard() == "" || f.a.messageKind != widget.MessageOK {
		t.Errorf("clipboard %q, message %q", f.win.Clipboard(), f.a.message)
	}
	// Other menus show no shortcuts and nothing disabled.
	f.a.openZoomMenu()
	if f.a.menu.Shortcuts != nil || f.a.menu.Disabled != nil {
		t.Error("the zoom menu kept the context menu's shortcuts")
	}
}
