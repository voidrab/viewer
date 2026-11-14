package wayland

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"viewer/internal/ui/platform"
)

func TestWindowManagementRequestsFollowTheLatestPress(t *testing.T) {
	ss := newSeatSetup(t, seatCapPointer)
	p := <-ss.pointer
	ss.srv.send(p, pointerEnter, func(e *Encoder) { e.Uint(3).Uint(ss.surface).Fixed(1).Fixed(1) })
	next(t, ss.win)
	ss.srv.send(p, pointerButton, func(e *Encoder) { e.Uint(42).Uint(0).Uint(btnLeft).Uint(buttonPressed) })
	next(t, ss.win)
	f := ss.f
	f.mu.Lock()
	seat := f.ids["wl_seat"]
	f.mu.Unlock()

	ss.win.Move()
	f.wait("move")
	ss.win.Resize(platform.EdgeBottom | platform.EdgeRight)
	f.wait("resize")
	ss.win.ShowMenu(10, -2)
	f.wait("menu")
	ss.win.SetMaximized(true)
	f.wait("maximize")
	ss.win.SetMaximized(false)
	f.wait("unmaximize")
	ss.win.Minimize()
	f.wait("minimize")

	f.mu.Lock()
	defer f.mu.Unlock()
	want := []string{
		fmt.Sprintf("move %d 42", seat), fmt.Sprintf("resize %d 42 10", seat), fmt.Sprintf("menu %d 42 10 -2", seat),
		"maximize", "unmaximize", "minimize",
	}
	if !slices.Equal(f.manage, want) {
		t.Errorf("requests = %q\nwant %q", f.manage, want)
	}
}

func TestMovingWithoutASeatAsksForNothing(t *testing.T) {
	_, f, _, win := openWindow(t, 100, 100)
	win.Move()
	win.Resize(platform.EdgeTop)
	win.ShowMenu(1, 1)
	win.Minimize() // needs no seat, and marks the end of the requests
	f.wait("minimize")
	f.mu.Lock()
	defer f.mu.Unlock()
	if !slices.Equal(f.manage, []string{"minimize"}) {
		t.Errorf("requests = %q", f.manage)
	}
}

func TestCursorShapesFollowThePointer(t *testing.T) {
	ss := newSeatSetup(t, seatCapPointer, Global{Name: 11, Interface: "wp_cursor_shape_manager_v1", Version: 1})
	p := <-ss.pointer
	f := ss.f
	// The device was made during Open's roundtrip, before the window.
	f.mu.Lock()
	if f.cursorPointer != p {
		t.Errorf("cursor device made for %d, want the pointer %d", f.cursorPointer, p)
	}
	f.mu.Unlock()

	// Entering shows the window's cursor, the default to begin with.
	ss.srv.send(p, pointerEnter, func(e *Encoder) { e.Uint(5).Uint(ss.surface).Fixed(1).Fixed(1) })
	f.wait("set_shape")
	ss.win.SetCursor(platform.CursorResizeSE)
	f.wait("set_shape")
	// The window keeps its cursor for the next entry, which has its own serial.
	ss.srv.send(p, pointerLeave, func(e *Encoder) { e.Uint(6).Uint(ss.surface) })
	ss.srv.send(p, pointerEnter, func(e *Encoder) { e.Uint(9).Uint(ss.surface).Fixed(1).Fixed(1) })
	f.wait("set_shape")
	// Entering a surface that is not a window sets nothing.
	ss.srv.send(p, pointerEnter, func(e *Encoder) { e.Uint(10).Uint(9999).Fixed(1).Fixed(1) })
	time.Sleep(30 * time.Millisecond)

	f.mu.Lock()
	defer f.mu.Unlock()
	want := [][2]uint32{{5, 1}, {5, 23}, {9, 23}}
	if !slices.Equal(f.shapes, want) {
		t.Errorf("shapes = %v, want %v", f.shapes, want)
	}
}

func TestEveryCursorHasAShape(t *testing.T) {
	for c := platform.CursorDefault; c <= platform.CursorPointer; c++ {
		if cursorShapes[c] == 0 {
			t.Errorf("cursor %d has no shape", c)
		}
	}
}

func TestSetCursorWithoutTheProtocolOnlyRemembersIt(t *testing.T) {
	_, _, _, win := openWindow(t, 100, 100)
	win.SetCursor(platform.CursorResizeN)
	if got := win.(*window).currentCursor(); got != platform.CursorResizeN {
		t.Errorf("cursor = %d", got)
	}
}
