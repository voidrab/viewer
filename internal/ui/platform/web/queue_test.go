package web

import (
	"testing"
	"time"

	"viewrab/internal/ui/platform"
)

// next returns the next event q delivers, failing the test if none comes.
func next(t *testing.T, q *queue) platform.Event {
	t.Helper()
	select {
	case ev, ok := <-q.out:
		if !ok {
			t.Fatal("events closed")
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
	}
	return nil
}

func TestQueueKeepsOrderWithoutBlocking(t *testing.T) {
	q := newQueue()
	defer q.close()
	// Far more than the channel holds, posted with nobody reading: posting
	// never waits.
	const n = 1000
	for i := range n {
		q.post(platform.PointerEvent{X: float64(i)})
	}
	for i := range n {
		if ev := next(t, q).(platform.PointerEvent); ev.X != float64(i) {
			t.Fatalf("event %d has X %v", i, ev.X)
		}
	}
}

func TestQueueClose(t *testing.T) {
	q := newQueue()
	q.post(platform.FrameEvent{})
	next(t, q)
	// Events left over when it closes are dropped, mid-batch included.
	for range 100 {
		q.post(platform.FrameEvent{})
	}
	q.close()
	q.close() // a second close does nothing
	q.post(platform.FrameEvent{})
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-q.out:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("events not closed")
		}
	}
}

func TestQueueCloseWhileDelivering(t *testing.T) {
	q := newQueue()
	// More than the channel holds, so that the pump waits to deliver when
	// the queue closes.
	for range cap(q.out) + 10 {
		q.post(platform.FrameEvent{})
	}
	for len(q.out) < cap(q.out) {
		time.Sleep(time.Millisecond)
	}
	q.close()
	n := 0
	for range q.out {
		n++
	}
	if n > cap(q.out) {
		t.Errorf("%d events delivered after close, want at most %d", n, cap(q.out))
	}
}

func TestFrames(t *testing.T) {
	var f frames
	if !f.invalidate() {
		t.Fatal("first invalidate asks for no frame")
	}
	if f.invalidate() {
		t.Fatal("invalidate with a frame asked for asks again")
	}
	f.fire()
	// The request made before the frame came is answered by it.
	if f.presented() {
		t.Fatal("present asks for a frame nobody wanted")
	}
	if !f.invalidate() {
		t.Fatal("invalidate after present asks for no frame")
	}
	f.fire()
	if f.invalidate() {
		t.Fatal("invalidate before present asks for a frame")
	}
	if f.invalidate() {
		t.Fatal("second invalidate before present asks for a frame")
	}
	if !f.presented() {
		t.Fatal("present asks for no frame though one was wanted")
	}
	if f.invalidate() {
		t.Fatal("invalidate with a frame asked for asks again")
	}
}
