package app

import (
	"context"
	"image"
	"slices"
	"strings"
	"testing"

	"viewer/internal/pdfa"
	"viewer/internal/ui/platform"
	"viewer/internal/ui/theme"
	"viewer/internal/ui/widget"
)

// barPoint returns the window coordinates of the tab bar's gap before tab i,
// or after the last tab for i past them.
func (f *fixture) barPoint(i int) (x, y int) {
	_, tabs, _, _ := f.a.layout()
	if i < len(f.a.docs) {
		r := f.a.tabs.TabRect(i).Add(tabs.Min)
		return r.Min.X + 2, r.Min.Y + r.Dy()/2
	}
	r := f.a.tabs.NewTabRect().Add(tabs.Min)
	return r.Min.X, r.Min.Y + r.Dy()/2
}

// dragOut presses on tab i and drags it off the tab bar, which begins a drag.
func (f *fixture) dragOut(i int) {
	f.t.Helper()
	f.a.Handle(platform.FrameEvent{})
	x, y := f.tabPoint(i)
	f.pointer(platform.Move, platform.None, x, y)
	f.pointer(platform.Press, platform.Left, x, y)
	f.pointer(platform.Move, platform.None, x, y+100)
	if f.a.drag == nil {
		f.t.Fatal("no drag began")
	}
}

func (f *fixture) dragAt(kind platform.DragKind, own bool, x, y int) {
	f.a.Handle(platform.DragEvent{Kind: kind, X: float64(x), Y: float64(y), Own: own})
}

func (f *fixture) dropAt(own bool, x, y int, paths ...string) {
	f.a.Handle(platform.DropEvent{X: float64(x), Y: float64(y), Own: own, Paths: paths})
}

func TestFilesDroppedOnTheWindowOpen(t *testing.T) {
	f := newFixture(t)
	f.start()
	a, b := f.pdf("a.pdf", 1), f.pdf("B.PDF", 1)
	// A drag of files over the empty window tints it, until it leaves.
	c := f.content()
	f.dragAt(platform.DragEnter, false, c.Min.X+50, c.Min.Y+50)
	if !f.a.dropTint {
		t.Error("a drag of files does not tint the window")
	}
	f.a.Handle(platform.FrameEvent{})
	shots := f.win.Presented()
	accent := theme.Light().Accent
	if got := shots[len(shots)-1].RGBAAt(c.Min.X, c.Min.Y+40); got != nrgbaOf(accent) {
		t.Errorf("the tint's edge is %v, want the accent", got)
	}
	f.dragAt(platform.DragLeave, false, 0, 0)
	if f.a.dropTint {
		t.Error("the tint stays after the drag left")
	}
	// The PDFs among the files dropped open; the others are left out.
	f.dragAt(platform.DragEnter, false, c.Min.X+50, c.Min.Y+50)
	f.dropAt(false, c.Min.X+50, c.Min.Y+50, a, f.dir+"/notes.txt", b)
	if got := f.names(); !slices.Equal(got, []string{"a.pdf", "B.PDF"}) || f.a.dropTint {
		t.Errorf("after the drop: %v, tint %v", got, f.a.dropTint)
	}
	// A drop with no PDFs among the files shows a status line warning.
	f.dropAt(false, c.Min.X+50, c.Min.Y+50, f.dir+"/notes.txt")
	if f.a.messageKind != widget.MessageWarn || !strings.Contains(f.a.message, "Only PDF") {
		t.Errorf("message %q", f.a.message)
	}
}

func TestATabDraggedBackOntoItsBarMoves(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 1), f.pdf("b.pdf", 1), f.pdf("c.pdf", 1))
	f.dragOut(0)
	calls := f.calls()
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "drag ") {
		t.Errorf("calls %q", calls)
	}
	icon := f.win.DragIcon()
	if icon == nil || icon.Rect.Dy() != theme.TabBarHeight || icon.Rect.Dx() != f.a.tabs.TabRect(0).Dx() {
		t.Errorf("drag icon %v", icon)
	}
	if f.a.tabs.Lifted != 0 || f.a.capture != sectionNone {
		t.Errorf("lifted %d, capture %d", f.a.tabs.Lifted, f.a.capture)
	}
	// A tab dragged over its own pages is not a drop. Over the bar it marks
	// the gap.
	c := f.content()
	f.dragAt(platform.DragEnter, true, c.Min.X+50, c.Min.Y+50)
	if f.a.dropTint || f.a.tabs.DropGap != -1 {
		t.Errorf("over its own pages: tint %v, gap %d", f.a.dropTint, f.a.tabs.DropGap)
	}
	x, y := f.barPoint(3)
	f.dragAt(platform.DragMove, true, x, y)
	if f.a.tabs.DropGap != 3 {
		t.Errorf("gap %d, want 3", f.a.tabs.DropGap)
	}
	f.dropAt(true, x, y)
	if got := f.names(); !slices.Equal(got, []string{"b.pdf", "c.pdf", "a.pdf"}) || f.a.current().name != "a.pdf" {
		t.Errorf("after the drop: %v", got)
	}
	f.a.Handle(platform.DragEndEvent{Dropped: true})
	if f.a.drag != nil || f.a.tabs.Lifted != -1 {
		t.Error("the drag did not end")
	}
	// A tab dropped left of where it was moves there. Dropped on its own
	// pages, it stays.
	f.dragOut(2)
	x, y = f.barPoint(0)
	f.dropAt(true, x, y)
	if got := f.names(); !slices.Equal(got, []string{"a.pdf", "b.pdf", "c.pdf"}) {
		t.Errorf("after the drop to the left: %v", got)
	}
	f.dragOut(2)
	f.dropAt(true, c.Min.X+50, c.Min.Y+50)
	f.a.Handle(platform.DragEndEvent{Dropped: true})
	if got := f.names(); !slices.Equal(got, []string{"a.pdf", "b.pdf", "c.pdf"}) {
		t.Errorf("after the drop on its pages: %v", got)
	}
	// Once the drag has ended, a drop of the window's own tabs does
	// nothing, and a drag of nothing marks no gap.
	f.dropAt(true, x, y)
	f.dragAt(platform.DragEnter, true, x, y)
	f.a.Handle(platform.DragEndEvent{Outside: true})
	if f.a.tabs.DropGap != -1 {
		t.Errorf("a drag of nothing marks gap %d", f.a.tabs.DropGap)
	}
}

func TestDraggingEveryTab(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 1), f.pdf("b.pdf", 1))
	f.a.Handle(platform.FrameEvent{})
	// The logo carries every tab, with an icon that counts them.
	logo := image.Pt(18, theme.ToolbarHeight/2)
	f.pointer(platform.Press, platform.Left, logo.X, logo.Y)
	f.pointer(platform.Move, platform.None, logo.X+20, logo.Y)
	if f.a.drag == nil || !f.a.drag.whole || len(f.a.drag.docs) != 2 {
		t.Fatalf("drag %+v", f.a.drag)
	}
	if icon := f.win.DragIcon(); icon.Rect.Dx() != f.a.face.Width("2 tabs")+2*dragPad {
		t.Errorf("icon %v", icon.Rect)
	}
	// A window's logo drag marks no gap on its own tab bar, and a drag that
	// ends outside every window leaves its tabs in place.
	x, y := f.barPoint(0)
	f.dragAt(platform.DragMove, true, x, y)
	if f.a.tabs.DropGap != -1 {
		t.Errorf("a window's own tabs mark gap %d", f.a.tabs.DropGap)
	}
	f.a.Handle(platform.DragEndEvent{Outside: true})
	if got := f.names(); len(got) != 2 || f.a.s.currentDrag() != nil {
		t.Errorf("after the drag: %v", got)
	}
	// The empty part of the tab bar also carries every tab. With one tab
	// left, the icon reads "1 tab".
	f.a.closeTab(1)
	f.a.Handle(platform.FrameEvent{})
	_, tabs, _, _ := f.a.layout()
	ex := tabs.Min.X + f.a.tabs.NewTabRect().Max.X + 50
	f.pointer(platform.Move, platform.None, ex, y)
	f.pointer(platform.Press, platform.Left, ex, y)
	f.pointer(platform.Move, platform.None, ex+30, y)
	if f.a.drag == nil || !f.a.drag.whole {
		t.Fatal("the tab bar's empty part began no drag")
	}
	if icon := f.win.DragIcon(); icon.Rect.Dx() != f.a.face.Width("1 tab")+2*dragPad {
		t.Errorf("icon %v", icon.Rect)
	}
	// A drag the window cannot begin leaves things as they are, as does a drag
	// of a tab that does not exist.
	f.a.Handle(platform.DragEndEvent{})
	f.win.NoDrag = true
	f.dragOutFails(0)
	f.a.beginDrag(5, image.Point{})
	if f.a.drag != nil {
		t.Error("a drag began of a tab that does not exist")
	}
}

// dragOutFails drags tab i off the bar, which begins no drag.
func (f *fixture) dragOutFails(i int) {
	f.t.Helper()
	x, y := f.tabPoint(i)
	f.pointer(platform.Move, platform.None, x, y)
	f.pointer(platform.Press, platform.Left, x, y)
	f.pointer(platform.Move, platform.None, x, y+100)
	f.pointer(platform.Release, platform.Left, x, y+100)
	if f.a.drag != nil || f.a.tabs.Lifted != -1 {
		f.t.Error("a drag began")
	}
}

func TestATabDraggedOutsideWithoutMoreWindowsStays(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 1), f.pdf("b.pdf", 1))
	// A drag cancelled over a window keeps its tab where it was.
	f.dragOut(1)
	f.a.Handle(platform.DragEndEvent{})
	if got := f.names(); len(got) != 2 {
		t.Errorf("after a cancel: %v", got)
	}
	// A tab dragged away from every window opens in a new window. Where no
	// window can be opened, the tab comes back and a warning is shown.
	f.dragOut(0)
	f.a.Handle(platform.DragEndEvent{Outside: true})
	if got := f.names(); !slices.Equal(got, []string{"b.pdf", "a.pdf"}) {
		t.Errorf("after the drag: %v", got)
	}
	if !strings.Contains(f.a.message, errNoWindows.Error()) {
		t.Errorf("message %q", f.a.message)
	}
	// Tabs closed while dragged are not taken out.
	f.dragOut(0)
	f.a.closeTab(0)
	f.a.Handle(platform.DragEndEvent{Outside: true})
	if got := f.names(); !slices.Equal(got, []string{"a.pdf"}) {
		t.Errorf("after closing the dragged tab: %v", got)
	}
}

func TestTakingOutATabEndsWhatWorksOnIt(t *testing.T) {
	f := textFixture(t)
	other := f.pdf("other.pdf", 1)
	f.a.Open(other)
	f.a.selectTab(0)
	text := f.a.docs[0]
	// A check that runs on the tab stops, and its result is dropped.
	started := make(chan context.Context, 1)
	f.a.opts.Verify = func(ctx context.Context, _, _ string) (pdfa.Report, error) {
		started <- ctx
		<-ctx.Done()
		return pdfa.Report{}, ctx.Err()
	}
	f.a.verify()
	ctx := <-started
	// The search in it and the page number being typed end as well.
	f.key('f', platform.Ctrl)
	f.a.startPageEdit(image.Point{})
	out := f.a.takeOut([]*document{text})
	if len(out) != 1 || out[0] != text || ctx.Err() == nil || text.result != nil {
		t.Errorf("taken %v, check context %v, result %v", out, ctx.Err(), text.result)
	}
	if f.a.findBar.Open || f.a.toolbar.EditingPage || f.a.current().name != "other.pdf" {
		t.Errorf("find open %v, editing %v", f.a.findBar.Open, f.a.toolbar.EditingPage)
	}
	f.drain() // runs the check's last step, which ends the job
	if f.a.job != nil {
		t.Error("the check did not end")
	}
	// The tab comes back where it is put, in this window's colours, and shows.
	f.a.setTheme(true)
	f.a.adopt(out, 7)
	if got := f.names(); !slices.Equal(got, []string{"other.pdf", "text.pdf"}) || f.a.active != 1 || text.owner.Load() != f.a {
		t.Errorf("after adopting: %v, active %d", got, f.a.active)
	}
	// Taking out the last tab leaves none active.
	f.a.takeOut(slices.Clone(f.a.docs))
	if f.a.active != -1 || f.a.current() != nil {
		t.Errorf("active %d", f.a.active)
	}
}

func TestAdoptedTabsWaitForTheWindowsSize(t *testing.T) {
	f := newFixture(t)
	g := newFixture(t)
	g.open(g.pdf("a.pdf", 1), g.pdf("b.pdf", 1))
	docs := g.a.takeOut(slices.Clone(g.a.docs))
	f.a.adopt(docs[:1], 0)
	if len(f.a.docs) != 0 || len(f.a.pendingDocs) != 1 {
		t.Fatalf("before a size: %d docs, %d waiting", len(f.a.docs), len(f.a.pendingDocs))
	}
	f.start()
	if got := f.names(); !slices.Equal(got, []string{"a.pdf"}) {
		t.Errorf("after the size: %v", got)
	}
	// Those still waiting when the window closes are closed with it.
	f.a.pendingDocs = docs[1:]
	f.a.closeAll()
	if f.a.pendingDocs != nil {
		t.Error("the waiting documents were kept")
	}
}
