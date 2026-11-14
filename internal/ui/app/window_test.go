package app

import (
	"fmt"
	"image"
	"slices"
	"testing"
	"time"

	"viewer/internal/ui/platform"
	"viewer/internal/ui/widget"
)

// calls returns the window management calls made since the last call to it.
func (f *fixture) calls() []string {
	all := f.win.Calls()
	out := all[f.seen:]
	f.seen = len(all)
	return out
}

func TestWindowControlsDriveTheWindow(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 1))
	f.a.Handle(platform.FrameEvent{})
	if !f.a.toolbar.Controls {
		t.Fatal("an undecorated window shows no window controls")
	}
	f.clickButton(widget.ButtonMinimize)
	f.clickButton(widget.ButtonMaximize)
	if got := f.calls(); !slices.Equal(got, []string{"minimize", "maximize"}) {
		t.Errorf("calls = %q", got)
	}
	// Once maximised, the same control restores, and shows that it will.
	f.a.Handle(platform.StateEvent{Maximized: true})
	f.a.Handle(platform.FrameEvent{})
	if !f.a.toolbar.Maximized {
		t.Error("the toolbar does not know the window is maximised")
	}
	f.clickButton(widget.ButtonMaximize)
	if got := f.calls(); !slices.Equal(got, []string{"unmaximize"}) {
		t.Errorf("calls = %q", got)
	}
	// The close control quits, like the compositor's close.
	r := f.a.toolbar.ButtonRect(widget.ButtonCloseWindow)
	x, y := mid(r)
	f.pointer(platform.Press, platform.Left, x, y)
	if !f.a.Handle(platform.PointerEvent{Kind: platform.Release, Button: platform.Left, X: float64(x), Y: float64(y)}) {
		t.Error("the close control did not quit")
	}
	// A compositor that decorates the window draws the controls itself.
	g := newFixture(t)
	g.start()
	g.a.Handle(platform.StateEvent{Decorated: true})
	g.a.Handle(platform.FrameEvent{})
	if g.a.toolbar.Controls {
		t.Error("a decorated window shows its own window controls")
	}
}

func mid(r image.Rectangle) (int, int) { return (r.Min.X + r.Max.X) / 2, (r.Min.Y + r.Max.Y) / 2 }

// emptyTitle is a point on the toolbar's empty part: between the zoom
// controls and the find field.
func (f *fixture) emptyTitle() (int, int) {
	zoom, find := f.a.toolbar.ButtonRect(widget.ButtonZoomIn), f.a.toolbar.ButtonRect(widget.ButtonFind)
	return (zoom.Max.X + find.Min.X) / 2, zoom.Min.Y + zoom.Dy()/2
}

func TestTheToolbarIsATitleBar(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 1))
	f.a.Handle(platform.FrameEvent{})
	x, y := f.emptyTitle()

	// Dragging moves the window. The compositor then has the pointer, so no
	// section keeps it: the next press goes where it is.
	f.pointer(platform.Press, platform.Left, x, y)
	f.pointer(platform.Move, platform.None, x+20, y)
	if got := f.calls(); !slices.Equal(got, []string{"move"}) {
		t.Errorf("drag: calls = %q", got)
	}
	if f.a.capture != sectionNone {
		t.Errorf("after the move, section %d has the pointer", f.a.capture)
	}

	// Two presses in quick succession maximise, or restore once maximised.
	f.now = f.now.Add(time.Hour)
	f.click(x, y)
	f.now = f.now.Add(200 * time.Millisecond)
	f.click(x, y)
	f.a.Handle(platform.StateEvent{Maximized: true})
	f.now = f.now.Add(200 * time.Millisecond)
	f.click(x, y) // a third press starts afresh
	f.now = f.now.Add(200 * time.Millisecond)
	f.click(x, y)
	if got := f.calls(); !slices.Equal(got, []string{"maximize", "unmaximize"}) {
		t.Errorf("double clicks: calls = %q", got)
	}
	// Presses too far apart are two single clicks.
	f.now = f.now.Add(time.Second)
	f.click(x, y)
	f.now = f.now.Add(time.Second)
	f.click(x, y)
	if got := f.calls(); len(got) != 0 {
		t.Errorf("slow clicks: calls = %q", got)
	}

	// A right click opens the window menu where the pointer is.
	f.pointer(platform.Press, platform.Right, x, y)
	if got := f.calls(); !slices.Equal(got, []string{fmt.Sprintf("menu %d %d", x, y)}) {
		t.Errorf("right click: calls = %q", got)
	}
	if f.a.capture != sectionNone {
		t.Errorf("after the menu, section %d has the pointer", f.a.capture)
	}
}

func TestTheWindowEdgesResizeIt(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 1))
	f.a.Handle(platform.FrameEvent{})
	for _, tc := range []struct {
		x, y   int
		edges  platform.Edge
		cursor platform.Cursor
	}{
		{1, winH / 2, platform.EdgeLeft, platform.CursorResizeW},
		{winW - 2, winH / 2, platform.EdgeRight, platform.CursorResizeE},
		{winW / 2, 0, platform.EdgeTop, platform.CursorResizeN},
		{winW / 2, winH - 1, platform.EdgeBottom, platform.CursorResizeS},
		{2, 2, platform.EdgeTop | platform.EdgeLeft, platform.CursorResizeNW},
		// A corner reaches along its edges.
		{winW - 1, 10, platform.EdgeTop | platform.EdgeRight, platform.CursorResizeNE},
		{10, winH - 1, platform.EdgeBottom | platform.EdgeLeft, platform.CursorResizeSW},
		{winW - 10, winH - 2, platform.EdgeBottom | platform.EdgeRight, platform.CursorResizeSE},
	} {
		f.pointer(platform.Move, platform.None, tc.x, tc.y)
		if f.win.Cursor() != tc.cursor || f.a.hover != sectionFrame {
			t.Errorf("at (%d, %d): cursor %d, hover %d", tc.x, tc.y, f.win.Cursor(), f.a.hover)
		}
		f.pointer(platform.Press, platform.Left, tc.x, tc.y)
		if got := f.calls(); !slices.Equal(got, []string{fmt.Sprintf("resize %d", tc.edges)}) {
			t.Errorf("at (%d, %d): calls = %q, want a resize from %d", tc.x, tc.y, got, tc.edges)
		}
		if f.a.capture != sectionNone {
			t.Errorf("after the resize, section %d has the pointer", f.a.capture)
		}
	}
	// Other buttons on the frame do nothing, and away from it is the default cursor.
	f.pointer(platform.Press, platform.Right, 1, winH/2)
	f.pointer(platform.Release, platform.Right, 1, winH/2)
	f.pointer(platform.Move, platform.None, winW/2, winH/2)
	if got := f.calls(); len(got) != 0 || f.win.Cursor() != platform.CursorDefault {
		t.Errorf("calls = %q, cursor %d", got, f.win.Cursor())
	}
	// Leaving the window from the frame puts the default cursor back.
	f.pointer(platform.Move, platform.None, 1, winH/2)
	f.pointer(platform.Leave, platform.None, 1, winH/2)
	if f.win.Cursor() != platform.CursorDefault {
		t.Errorf("cursor after leaving = %d", f.win.Cursor())
	}
	// A maximised window, or one the compositor decorates, has no frame.
	for _, st := range []platform.StateEvent{{Maximized: true}, {Decorated: true}} {
		f.a.Handle(st)
		if e := f.a.edgeAt(image.Pt(1, 1)); e != 0 {
			t.Errorf("%+v: edges %d at the corner", st, e)
		}
	}
	// Beyond the window there is no frame either.
	f.a.Handle(platform.StateEvent{})
	if e := f.a.edgeAt(image.Pt(-1, winH/2)); e != 0 {
		t.Errorf("edges %d beyond the window", e)
	}
	// While a button is held, for a drag begun inside, the cursor stays.
	f.pointer(platform.Press, platform.Left, winW/2, winH/2)
	f.pointer(platform.Move, platform.None, 1, winH/2)
	if f.win.Cursor() != platform.CursorDefault {
		t.Errorf("cursor during a drag = %d", f.win.Cursor())
	}
	// The pointer coming back into the window holds no button for anyone.
	f.pointer(platform.Enter, platform.None, winW/2, winH/2)
	if f.a.capture != sectionNone {
		t.Errorf("after entering, section %d has the pointer", f.a.capture)
	}
}
