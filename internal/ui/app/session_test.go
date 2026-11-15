package app

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/voidrab/gopdfrab"

	"viewrab/internal/pdfa"
	"viewrab/internal/testpdf"
	"viewrab/internal/ui/instance"
	"viewrab/internal/ui/platform"
	"viewrab/internal/ui/text"
	"viewrab/internal/ui/theme"
	"viewrab/internal/ui/widget"
)

// multi is a session of windows that run their own event loops on fake
// windows, as Run runs them.
type multi struct {
	t    *testing.T
	plat platform.Fake
	s    *session
	dir  string
	// newWindowErr, when set, makes NewWindow fail.
	newWindowErr error
}

func newMulti(t *testing.T) *multi {
	t.Helper()
	m := &multi{t: t, dir: t.TempDir()}
	m.s = newSession(Options{
		Inspect: func(*gopdfrab.Document) pdfa.Info { return pdfa.Info{} },
		NewWindow: func() (platform.Window, error) {
			if m.newWindowErr != nil {
				return nil, m.newWindowErr
			}
			return m.plat.NewWindow(platform.WindowOptions{Size: platform.Size{W: winW, H: winH}})
		},
	}.withDefaults())
	t.Cleanup(func() {
		for _, a := range m.s.windows() {
			a.post(func() { a.quitting = true })
		}
		m.s.wg.Wait()
	})
	return m
}

// window opens a window showing paths and runs it.
func (m *multi) window(paths ...string) (*App, *platform.FakeWindow) {
	m.t.Helper()
	w, _ := m.plat.NewWindow(platform.WindowOptions{Size: platform.Size{W: winW, H: winH}})
	a, err := m.s.newApp(w)
	if err != nil {
		m.t.Fatal(err)
	}
	a.pending = paths
	m.s.start(a)
	m.waitNames(a, names(paths)...)
	return a, w.(*platform.FakeWindow)
}

func (m *multi) pdf(name string) string {
	return testpdf.Write(m.t, m.dir, name, testpdf.BuildPages([][2]int{{200, 300}}))
}

func names(paths []string) []string {
	var out []string
	for _, p := range paths {
		out = append(out, filepath.Base(p))
	}
	return out
}

// on runs f on a's event loop and returns its result.
func on[T any](t *testing.T, a *App, f func() T) T {
	t.Helper()
	got := make(chan T, 1)
	if !a.post(func() { got <- f() }) {
		t.Fatal("the window has closed")
	}
	return <-got
}

// onPoint draws a so that its widgets are laid out, then returns the point
// that at returns, both on a's event loop.
func onPoint(t *testing.T, a *App, at func() (int, int)) (int, int) {
	t.Helper()
	p := on(t, a, func() [2]int {
		a.draw()
		x, y := at()
		return [2]int{x, y}
	})
	return p[0], p[1]
}

// tabNames returns the names of a's tabs.
func (m *multi) tabNames(a *App) []string {
	return on(m.t, a, func() []string {
		var out []string
		for _, d := range a.docs {
			out = append(out, d.name)
		}
		return out
	})
}

// waitNames waits until a's tabs are those named.
func (m *multi) waitNames(a *App, want ...string) {
	m.t.Helper()
	waitFor(m.t, "tabs "+strings.Join(want, ", "), func() bool { return slices.Equal(m.tabNames(a), want) })
}

// opened waits until the program has opened n windows, and returns the last
// of them and its App.
func (m *multi) opened(n int) (*App, *platform.FakeWindow) {
	m.t.Helper()
	var a *App
	var w *platform.FakeWindow
	waitFor(m.t, "a new window", func() bool {
		ws := m.plat.Windows()
		if len(ws) < n {
			return false
		}
		w = ws[n-1]
		for _, x := range m.s.windows() {
			if x.win == platform.Window(w) {
				a = x
			}
		}
		return a != nil
	})
	return a, w
}

// closed returns how many of the fake windows have closed.
func (m *multi) closed() int {
	n := 0
	for _, w := range m.plat.Windows() {
		if w.Closed() {
			n++
		}
	}
	return n
}

// dragOut drags tab i of a off its bar, which begins a drag.
func (m *multi) dragOut(a *App, w *platform.FakeWindow, i int) {
	m.t.Helper()
	x, y := onPoint(m.t, a, func() (int, int) { return (&fixture{a: a}).tabPoint(i) })
	w.Send(platform.PointerEvent{Kind: platform.Move, X: float64(x), Y: float64(y)})
	w.Send(platform.PointerEvent{Kind: platform.Press, Button: platform.Left, X: float64(x), Y: float64(y)})
	w.Send(platform.PointerEvent{Kind: platform.Move, X: float64(x), Y: float64(y + 100)})
	waitFor(m.t, "the drag to begin", func() bool { return on(m.t, a, func() bool { return a.drag != nil }) })
}

func TestCtrlNOpensAnotherWindowAndLaunchesGoToTheLastFocused(t *testing.T) {
	m := newMulti(t)
	first, fw := m.window(m.pdf("a.pdf"))
	fw.Send(platform.KeyEvent{Rune: 'n', Down: true, Mods: platform.Ctrl})
	second, sw := m.opened(2)
	// A launch goes to the window opened last, until another is focused.
	b := m.pdf("b.pdf")
	m.s.launch(instance.Request{Paths: []string{b}, Token: "t1"})
	m.waitNames(second, "b.pdf")
	fw.Send(platform.FocusEvent{Focused: true})
	sw.Send(platform.FocusEvent{}) // losing the focus changes nothing
	waitFor(t, "the focus", func() bool { return m.s.target() == first })
	c := m.pdf("c.pdf")
	m.s.launch(instance.Request{Paths: []string{c}, Token: "t2"})
	m.waitNames(first, "a.pdf", "c.pdf")
	// A file already open is switched to in its own window, which comes
	// forward, while the rest open in the focused one.
	d := m.pdf("d.pdf")
	m.s.launch(instance.Request{Paths: []string{b, d}, Token: "t3"})
	m.waitNames(first, "a.pdf", "c.pdf", "d.pdf")
	waitFor(t, "both windows to come forward", func() bool {
		return slices.Contains(sw.Activations(), "t3") && slices.Contains(fw.Activations(), "t3")
	})
	// A launch without files brings the focused window forward.
	m.s.launch(instance.Request{Token: "t4"})
	waitFor(t, "the window to come forward", func() bool { return slices.Contains(fw.Activations(), "t4") })

	// Ctrl+Q closes every window.
	sw.Send(platform.KeyEvent{Rune: 'q', Down: true, Mods: platform.Ctrl})
	select {
	case <-m.s.empty:
	case <-time.After(5 * time.Second):
		t.Fatal("Ctrl+Q left windows open")
	}
	if !fw.Closed() || !sw.Closed() || m.s.target() != nil {
		t.Error("a window is still open")
	}
	// With every window closed, a launch has nowhere to go.
	m.s.launch(instance.Request{Paths: []string{b}})
}

func TestWindowsThatCannotOpenAreReported(t *testing.T) {
	m := newMulti(t)
	f := &fixture{t: t}
	m.newWindowErr = errors.New("no compositor")
	a, err := m.s.newApp(func() platform.Window { w, _ := m.plat.NewWindow(platform.WindowOptions{}); return w }())
	if err != nil {
		t.Fatal(err)
	}
	f.a = a
	f.key('n', platform.Ctrl)
	if !strings.Contains(a.message, "no compositor") {
		t.Errorf("message %q", a.message)
	}
	// A window whose App cannot be made is closed again.
	m.newWindowErr = nil
	orig := newFace
	defer func() { newFace = orig }()
	newFace = func(float64) (*text.Face, error) { return nil, errors.New("no font") }
	if err := m.s.openWindow(nil, nil); err == nil {
		t.Error("openWindow succeeded without a font")
	}
	ws := m.plat.Windows()
	if !ws[len(ws)-1].Closed() {
		t.Error("the window was left open")
	}
	a.closeAll()
}

func TestATabDraggedOutsideOpensInANewWindow(t *testing.T) {
	m := newMulti(t)
	first, fw := m.window(m.pdf("a.pdf"), m.pdf("b.pdf"))
	m.dragOut(first, fw, 1)
	fw.Send(platform.DragEndEvent{Outside: true})
	second, sw := m.opened(2)
	m.waitNames(second, "b.pdf")
	m.waitNames(first, "a.pdf")
	waitFor(t, "the new window's title", func() bool { return sw.Title() == "b.pdf — Viewrab" })
	// Its only tab dragged out too, the window moves to a new one and closes.
	m.dragOut(second, sw, 0)
	sw.Send(platform.DragEndEvent{Outside: true})
	waitFor(t, "the window to close", sw.Closed)
	third, _ := m.opened(3)
	m.waitNames(third, "b.pdf")
}

func TestTabsMoveBetweenWindows(t *testing.T) {
	m := newMulti(t)
	first, fw := m.window(m.pdf("a.pdf"), m.pdf("b.pdf"))
	second, sw := m.window(m.pdf("c.pdf"))
	// A tab dropped on another window's tab bar goes where it was dropped.
	m.dragOut(first, fw, 0)
	x, y := onPoint(t, second, func() (int, int) { return (&fixture{a: second}).barPoint(0) })
	sw.Send(platform.DragEvent{Kind: platform.DragEnter, X: float64(x), Y: float64(y), Own: true})
	waitFor(t, "the drop mark", func() bool { return on(t, second, func() bool { return second.tabs.DropGap == 0 }) })
	sw.Send(platform.DropEvent{X: float64(x), Y: float64(y), Own: true})
	m.waitNames(second, "a.pdf", "c.pdf")
	m.waitNames(first, "b.pdf")
	fw.Send(platform.DragEndEvent{Dropped: true})
	if d := on(t, second, func() *document { return second.current() }); d.name != "a.pdf" || d.owner.Load() != second {
		t.Errorf("the moved tab is not the active one of its new window: %q", d.name)
	}
	// The logo carries every tab of a window; dropped on another window's
	// pages, they go after its tabs, and the window they left closes.
	// Events and posted functions travel apart, so wait until the first drag
	// has ended; otherwise it is taken for the beginning of the next.
	waitFor(t, "the first drag to end", func() bool { return on(t, first, func() bool { return first.drag == nil }) })
	fw.Send(platform.PointerEvent{Kind: platform.Press, Button: platform.Left, X: 18, Y: theme.ToolbarHeight / 2})
	fw.Send(platform.PointerEvent{Kind: platform.Move, X: 40, Y: theme.ToolbarHeight / 2})
	waitFor(t, "the drag to begin", func() bool { return on(t, first, func() bool { return first.drag != nil }) })
	sw.Send(platform.DropEvent{X: winW / 2, Y: winH / 2, Own: true})
	m.waitNames(second, "a.pdf", "c.pdf", "b.pdf")
	waitFor(t, "the emptied window to close", fw.Closed)
}

func TestTabsGivenToAClosedWindowAreClosed(t *testing.T) {
	m := newMulti(t)
	first, _ := m.window(m.pdf("a.pdf"), m.pdf("b.pdf"))
	second, sw := m.window()
	sw.Close()
	<-second.done
	d := &tabDrag{from: first, docs: on(t, first, func() []*document { return first.docs[:1] })}
	m.s.transfer(d, second, 0)
	m.waitNames(first, "b.pdf")
	// A drag taken by nothing, or of tabs the window no longer has, moves
	// nothing.
	m.s.transfer(&tabDrag{from: first, docs: d.docs}, second, 0)
	m.waitNames(first, "b.pdf")
	// A window that closes is no longer asked what it shows, and a drag it
	// began is forgotten.
	m.s.setDrag(&tabDrag{from: first})
	if got := m.s.showing("/nowhere.pdf"); got != nil {
		t.Errorf("showing = %v", got)
	}
	on(t, first, func() bool { first.quitting = true; return true })
	<-first.done
	if m.s.currentDrag() != nil || m.s.showing("/nowhere.pdf") != nil {
		t.Error("the closed window is still known")
	}
	if first.post(func() {}) {
		t.Error("a closed window took work")
	}
}

func TestShowingAsksEveryWindow(t *testing.T) {
	m := newMulti(t)
	a := m.pdf("a.pdf")
	first, _ := m.window(a)
	second, _ := m.window()
	if got := m.s.showing(a); got != first {
		t.Errorf("showing = %v, want the first window", got)
	}
	m.s.focus(second)
	m.s.remove(second) // as if it closed: the window launches go to falls back
	m.s.remove(second)
	if m.s.target() != first {
		t.Error("launches go to a closed window")
	}
	m.s.focus(second) // a closed window is not focused
	if m.s.target() != first {
		t.Error("a closed window took the focus")
	}
	m.s.apps = append(m.s.apps, second) // so that the cleanup closes it
}

func TestTheThemeSwitchesEveryWindow(t *testing.T) {
	m := newMulti(t)
	first, fw := m.window(m.pdf("a.pdf"))
	second, _ := m.window()
	x, y := onPoint(t, first, func() (int, int) { return mid(first.toolbar.ButtonRect(widget.ButtonTheme)) })
	fw.Send(platform.PointerEvent{Kind: platform.Move, X: float64(x), Y: float64(y)})
	fw.Send(platform.PointerEvent{Kind: platform.Press, Button: platform.Left, X: float64(x), Y: float64(y)})
	fw.Send(platform.PointerEvent{Kind: platform.Release, Button: platform.Left, X: float64(x), Y: float64(y)})
	waitFor(t, "the other window to turn dark", func() bool {
		return on(t, second, func() bool { return second.opts.Palette.Dark })
	})
	if !m.s.options().Palette.Dark {
		t.Error("a window opened now would not start dark")
	}
}

func TestPostGivesUpWhenTheWindowClosesMeanwhile(t *testing.T) {
	f := newFixture(t)
	// Nothing runs the window's loop, so its inbox fills; work posted then
	// waits until the window closes.
	for f.a.post(func() {}) && len(f.a.inbox) < cap(f.a.inbox) {
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		close(f.a.done)
	}()
	if f.a.post(func() {}) {
		t.Error("work was taken by a window that closed")
	}
}

func TestShowingSkipsAWindowThatClosesBeforeAnswering(t *testing.T) {
	f := newFixture(t)
	// The window takes the question but closes without running it.
	go func() {
		for len(f.a.inbox) == 0 {
			time.Sleep(time.Millisecond)
		}
		close(f.a.done)
	}()
	if got := f.a.s.showing("/a.pdf"); got != nil {
		t.Errorf("showing = %v", got)
	}
	// Once closed, it is not asked.
	if got := f.a.s.showing("/a.pdf"); got != nil {
		t.Errorf("showing = %v", got)
	}
}
