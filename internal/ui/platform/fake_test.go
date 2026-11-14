package platform

import (
	"errors"
	"image"
	"testing"
)

func TestFakeWindowLifecycle(t *testing.T) {
	var f Fake
	win, err := f.NewWindow(WindowOptions{Title: "a", Size: Size{300, 200}})
	if err != nil {
		t.Fatal(err)
	}
	w := win.(*FakeWindow)
	if got := f.Windows(); len(got) != 1 || got[0] != w {
		t.Fatalf("Windows = %v", got)
	}
	if ev := <-w.Events(); ev != (ResizeEvent{Size: Size{300, 200}, Scale: 1}) {
		t.Errorf("first event = %#v", ev)
	}
	if ev := <-w.Events(); ev != (FrameEvent{}) {
		t.Errorf("second event = %#v", ev)
	}

	w.SetTitle("b")
	if w.Title() != "b" {
		t.Errorf("Title = %q", w.Title())
	}

	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Pix[0] = 9
	w.Present(img)
	img.Pix[0] = 1
	if p := w.Presented(); len(p) != 1 || p[0].Pix[0] != 9 {
		t.Errorf("Presented did not keep a copy: %v", p)
	}

	w.Invalidate()
	if ev := <-w.Events(); ev != (FrameEvent{}) || w.Invalidations() != 1 {
		t.Errorf("Invalidate: %#v, %d", ev, w.Invalidations())
	}

	if err := w.Send(KeyEvent{Key: KeyEnter, Down: true}); err != nil {
		t.Fatal(err)
	}
	if ev := (<-w.Events()).(KeyEvent); ev.Key != KeyEnter {
		t.Errorf("key = %#v", ev)
	}

	w.Close()
	w.Close()
	if !w.Closed() {
		t.Error("Closed = false")
	}
	if _, ok := <-w.Events(); ok {
		t.Error("Events not closed")
	}
	if err := w.Send(CloseEvent{}); !errors.Is(err, ErrFakeClosed) {
		t.Errorf("Send after close = %v", err)
	}
	w.Invalidate() // counted, but queues nothing on a closed window
	if w.Invalidations() != 2 {
		t.Errorf("Invalidations = %d", w.Invalidations())
	}
}

func TestFakeNewWindowError(t *testing.T) {
	boom := errors.New("boom")
	f := Fake{Err: boom}
	if _, err := f.NewWindow(WindowOptions{}); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
}

// TestEventsImplementEvent verifies that every event type satisfies Event.
func TestEventsImplementEvent(t *testing.T) {
	evs := []Event{ResizeEvent{}, FrameEvent{}, CloseEvent{}, FocusEvent{}, PointerEvent{}, ScrollEvent{}, KeyEvent{}, PasteEvent{}, StateEvent{}, DragEvent{}, DropEvent{}, DragEndEvent{}}
	for _, e := range evs {
		e.isEvent()
	}
}

func TestFakeWindowRecordsActivations(t *testing.T) {
	var f Fake
	win, _ := f.NewWindow(WindowOptions{})
	w := win.(*FakeWindow)
	w.Activate("a")
	w.Activate("")
	if got := w.Activations(); len(got) != 2 || got[0] != "a" || got[1] != "" {
		t.Errorf("activations = %q", got)
	}
}

func TestFakeClipboardRoundTrips(t *testing.T) {
	var f Fake
	win, _ := f.NewWindow(WindowOptions{})
	w := win.(*FakeWindow)
	<-w.Events() // the initial resize
	<-w.Events() // and frame
	w.RequestPaste()
	select {
	case ev := <-w.Events():
		t.Fatalf("an empty clipboard pasted %#v", ev)
	default:
	}
	w.SetClipboard("text")
	if w.Clipboard() != "text" {
		t.Errorf("clipboard = %q", w.Clipboard())
	}
	w.RequestPaste()
	if ev := <-w.Events(); ev != (PasteEvent{Text: "text"}) {
		t.Errorf("paste = %#v", ev)
	}
	w.Close()
	w.RequestPaste() // a closed window gets no event
}

func TestFakeWindowRecordsWindowManagement(t *testing.T) {
	var f Fake
	win, _ := f.NewWindow(WindowOptions{})
	w := win.(*FakeWindow)
	w.Move()
	w.Resize(EdgeBottom | EdgeRight)
	w.SetMaximized(true)
	w.SetMaximized(false)
	w.Minimize()
	w.ShowMenu(4, 5)
	want := []string{"move", "resize 10", "maximize", "unmaximize", "minimize", "menu 4 5"}
	if got := w.Calls(); len(got) != len(want) {
		t.Fatalf("calls = %q", got)
	} else {
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("call %d = %q, want %q", i, got[i], want[i])
			}
		}
	}
	icon := image.NewRGBA(image.Rect(0, 0, 2, 2))
	if !w.StartDrag(icon, image.Pt(3, 4)) || w.DragIcon() != icon {
		t.Error("StartDrag did not begin the drag with its icon")
	}
	w.NoDrag = true
	if w.StartDrag(nil, image.Point{}) {
		t.Error("StartDrag began a drag with NoDrag set")
	}
	if got := w.Calls(); got[len(got)-1] != "drag 3 4" {
		t.Errorf("calls = %q", got)
	}
	w.SetCursor(CursorResizeSE)
	if w.Cursor() != CursorResizeSE {
		t.Errorf("cursor = %d", w.Cursor())
	}
}

func TestResizeCursor(t *testing.T) {
	for edges, want := range map[Edge]Cursor{
		EdgeTop: CursorResizeN, EdgeBottom: CursorResizeS, EdgeLeft: CursorResizeW, EdgeRight: CursorResizeE,
		EdgeTop | EdgeLeft: CursorResizeNW, EdgeTop | EdgeRight: CursorResizeNE,
		EdgeBottom | EdgeLeft: CursorResizeSW, EdgeBottom | EdgeRight: CursorResizeSE,
		0: CursorDefault, EdgeTop | EdgeBottom: CursorDefault,
	} {
		if got := ResizeCursor(edges); got != want {
			t.Errorf("ResizeCursor(%d) = %d, want %d", edges, got, want)
		}
	}
}
