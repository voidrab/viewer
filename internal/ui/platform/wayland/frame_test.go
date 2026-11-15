//go:build !windows

package wayland

import (
	"image"
	"testing"
	"time"

	"viewrab/internal/ui/platform"
)

// frameDone signals the frame callback the client asked for last.
func (f *fakeWM) frameDone(s *server) {
	f.mu.Lock()
	cb := f.frames[len(f.frames)-1]
	f.mu.Unlock()
	s.send(cb, 1, nil) // an event a callback does not have, which is ignored
	s.send(cb, callbackDone, func(e *Encoder) { e.Uint(0) })
}

// noEvent fails the test if win delivers an event soon.
func noEvent(t *testing.T, win platform.Window, what string) {
	t.Helper()
	select {
	case ev := <-win.Events():
		t.Errorf("%s: got %#v", what, ev)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestFramesWaitForTheCompositor(t *testing.T) {
	_, f, srv, win := openWindow(t, 4, 2)
	f.mu.Lock()
	f.holdFrames = true
	f.mu.Unlock()
	frame := image.NewRGBA(image.Rect(0, 0, 4, 2))

	// A frame is drawn, and the compositor has not shown it yet: requests for
	// more wait, and come to one FrameEvent once it has.
	win.Present(frame)
	f.wait("frame")
	win.Invalidate()
	win.Invalidate()
	noEvent(t, win, "before the compositor was ready")
	f.frameDone(srv)
	if ev := next(t, win); ev != (platform.FrameEvent{}) {
		t.Fatalf("once ready = %#v", ev)
	}
	noEvent(t, win, "after the one frame")

	// While a FrameEvent waits for its Present, a request is kept for after
	// it: the application may have drawn before the change that asked.
	win.Invalidate()
	noEvent(t, win, "with a FrameEvent waiting")
	win.Present(frame)
	f.wait("frame")
	noEvent(t, win, "before the compositor was ready again")
	f.frameDone(srv)
	if ev := next(t, win); ev != (platform.FrameEvent{}) {
		t.Fatalf("the kept request = %#v", ev)
	}

	// A configure while the compositor is busy resizes at once, and draws
	// once it is ready.
	win.Present(frame)
	f.wait("frame")
	f.configure(srv, 8, 6, 9)
	if ev := next(t, win); ev != (platform.ResizeEvent{Size: platform.Size{W: 8, H: 6}, Scale: 1}) {
		t.Fatalf("configure = %#v", ev)
	}
	noEvent(t, win, "a configure before the compositor was ready")
	f.frameDone(srv)
	if ev := next(t, win); ev != (platform.FrameEvent{}) {
		t.Fatalf("after the configure = %#v", ev)
	}
}

func TestADroppedFrameIsRedrawnWhenWanted(t *testing.T) {
	_, f, srv, win := openWindow(t, 4, 2)
	// A configure arrives while a FrameEvent waits, so the application draws
	// at the old size; the window drops that frame and asks for another.
	win.Invalidate()
	next(t, win)
	f.configure(srv, 8, 6, 9)
	next(t, win) // the resize; its frame waits for the one queued
	skipFrame(win)
	if ev := next(t, win); ev != (platform.FrameEvent{}) {
		t.Fatalf("after the dropped frame = %#v", ev)
	}
	// With nothing wanted, a dropped frame asks for nothing.
	skipFrame(win)
	noEvent(t, win, "a dropped frame with nothing wanted")
}

func TestAFrameDroppedFromAFullQueueIsNotWaitedFor(t *testing.T) {
	_, _, _, win := openWindow(t, 4, 2)
	w := win.(*window)
	for range eventQueue {
		w.emit(platform.FocusEvent{})
	}
	win.Invalidate() // the queue is full: the FrameEvent is lost
	for range eventQueue {
		next(t, win)
	}
	// Nothing waits for an answer to the lost frame, so the next request
	// brings one.
	win.Invalidate()
	if ev := next(t, win); ev != (platform.FrameEvent{}) {
		t.Errorf("after the queue drained = %#v", ev)
	}
}
