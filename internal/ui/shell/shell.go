// Package shell runs a platform window around a page view: it owns the event
// loop, the bitmap the view draws into and the presentation of each frame.
package shell

import (
	"errors"
	"image"

	"viewer/internal/ui/pageview"
	"viewer/internal/ui/paint"
	"viewer/internal/ui/platform"
)

// ErrNoSize is returned when the event stream ends before the window reports
// a size.
var ErrNoSize = errors.New("shell: window closed before it had a size")

// NewView builds the view once the window's size is known. invalidate asks
// the shell for a redraw and may be called from any goroutine.
type NewView func(size image.Point, invalidate func()) (*pageview.View, error)

// Run drives win until the user closes it or its event stream ends, creating the view from newView on
// the first ResizeEvent. It closes the window and the view before returning.
func Run(win platform.Window, newView NewView) error {
	defer win.Close()
	var (
		view   *pageview.View
		canvas *paint.Canvas
	)
	defer func() {
		if view != nil {
			view.Close()
		}
	}()
	for ev := range win.Events() {
		switch ev := ev.(type) {
		case platform.CloseEvent:
			return nil
		case platform.FrameEvent:
			if view != nil {
				view.Draw(canvas)
				win.Present(canvas.Img)
			}
		case platform.ResizeEvent:
			size := image.Pt(ev.Size.W, ev.Size.H)
			canvas = paint.New(size.X, size.Y)
			if view == nil {
				var err error
				if view, err = newView(size, win.Invalidate); err != nil {
					return err
				}
			}
			view.HandleEvent(ev)
			win.Invalidate()
		default:
			if view != nil && view.HandleEvent(ev) {
				win.Invalidate()
			}
		}
	}
	if view == nil {
		return ErrNoSize
	}
	return nil
}
