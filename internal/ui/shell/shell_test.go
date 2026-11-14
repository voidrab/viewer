package shell

import (
	"errors"
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/voidrab/gopdfrab"

	"viewer/internal/testpdf"
	"viewer/internal/ui/pageview"
	"viewer/internal/ui/platform"
)

func viewFor(t *testing.T) NewView {
	doc, err := gopdfrab.OpenBytes(testpdf.BuildPages([][2]int{{100, 100}}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { doc.Close() })
	return func(size image.Point, invalidate func()) (*pageview.View, error) {
		return pageview.New(doc, size, pageview.Options{}, invalidate)
	}
}

func openFake(t *testing.T) (*platform.FakeWindow, platform.Window) {
	var f platform.Fake
	w, err := f.NewWindow(platform.WindowOptions{Size: platform.Size{W: 124, H: 124}})
	if err != nil {
		t.Fatal(err)
	}
	return w.(*platform.FakeWindow), w
}

// run starts Run in the background and returns the channel of its result.
func run(w platform.Window, nv NewView) chan error {
	errc := make(chan error, 1)
	go func() { errc <- Run(w, nv) }()
	return errc
}

// waitPresented polls until a presented frame satisfies ok.
func waitPresented(t *testing.T, w *platform.FakeWindow, ok func(*image.RGBA) bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, img := range w.Presented() {
			if ok(img) {
				return
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("no presented frame matched")
}

func TestRunShowsTheRenderedPageAndClosesOnRequest(t *testing.T) {
	fw, w := openFake(t)
	errc := run(w, viewFor(t))
	red := color.RGBA{255, 0, 0, 255}
	// The first frame is blank paper; the background render follows.
	waitPresented(t, fw, func(img *image.RGBA) bool { return img.RGBAAt(62, 62) == red })

	fw.Send(platform.ScrollEvent{DY: 1})        // handled: redraw
	fw.Send(platform.FocusEvent{Focused: true}) // ignored by the view
	fw.Send(platform.ResizeEvent{Size: platform.Size{W: 200, H: 150}, Scale: 1})
	waitPresented(t, fw, func(img *image.RGBA) bool { return img.Rect.Dx() == 200 && img.Rect.Dy() == 150 })
	fw.Send(platform.CloseEvent{})
	if err := <-errc; err != nil {
		t.Errorf("Run = %v", err)
	}
	if !fw.Closed() {
		t.Error("window left open")
	}
}

func TestRunIgnoresEventsBeforeTheFirstSize(t *testing.T) {
	fw, w := openFake(t)
	<-fw.Events() // take the initial resize and frame away
	<-fw.Events()
	errc := run(w, viewFor(t))
	fw.Send(platform.FrameEvent{})
	fw.Send(platform.KeyEvent{Down: true, Key: platform.KeyDown})
	fw.Send(platform.ResizeEvent{Size: platform.Size{W: 124, H: 124}, Scale: 1})
	waitPresented(t, fw, func(*image.RGBA) bool { return true })
	fw.Close() // the stream ends without a CloseEvent
	if err := <-errc; err != nil {
		t.Errorf("Run = %v", err)
	}
}

func TestRunReportsAMissingSize(t *testing.T) {
	fw, w := openFake(t)
	<-fw.Events()
	<-fw.Events()
	fw.Close()
	if err := Run(w, viewFor(t)); !errors.Is(err, ErrNoSize) {
		t.Errorf("Run = %v", err)
	}
}

func TestRunReportsViewErrors(t *testing.T) {
	_, w := openFake(t)
	boom := errors.New("boom")
	err := Run(w, func(image.Point, func()) (*pageview.View, error) { return nil, boom })
	if !errors.Is(err, boom) {
		t.Errorf("Run = %v", err)
	}
}
