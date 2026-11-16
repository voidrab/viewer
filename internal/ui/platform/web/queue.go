package web

import (
	"sync"

	"viewrab/internal/ui/platform"
)

// queue carries a window's events from the browser's callbacks to its Events
// channel. A callback runs on the browser's event loop and must not block, so
// posting never waits: events are kept, in order, until the application
// takes them.
type queue struct {
	out  chan platform.Event
	wake chan struct{}
	done chan struct{}

	mu      sync.Mutex
	pending []platform.Event
	closed  bool
}

// newQueue returns a queue and starts the goroutine that delivers its events.
func newQueue() *queue {
	q := &queue{
		out:  make(chan platform.Event, 64),
		wake: make(chan struct{}, 1),
		done: make(chan struct{}),
	}
	go q.pump()
	return q
}

// post queues ev for delivery; after close it does nothing.
func (q *queue) post(ev platform.Event) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.pending = append(q.pending, ev)
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// close drops the events not yet taken and, once the delivering goroutine has
// stopped, closes the Events channel. It may be called more than once.
func (q *queue) close() {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	q.pending = nil
	q.mu.Unlock()
	close(q.done)
}

// pump delivers the queued events until the queue closes.
func (q *queue) pump() {
	defer close(q.out)
	for {
		select {
		case <-q.done:
			return
		case <-q.wake:
		}
		q.mu.Lock()
		batch := q.pending
		q.pending = nil
		q.mu.Unlock()
		for _, ev := range batch {
			select {
			case q.out <- ev:
			case <-q.done:
				return
			}
		}
	}
}

// frames paces FrameEvents as the Window contract asks: at most one is
// outstanding, from when it is sent until Present answers it, and requests
// made meanwhile are answered by a single later one. Its methods return
// whether to ask the browser for an animation frame, which is answered by
// calling fire.
type frames struct {
	mu        sync.Mutex
	scheduled bool // an animation frame is asked for
	waiting   bool // a FrameEvent awaits its Present
	wanted    bool // a frame was asked for while one was outstanding
}

// invalidate records a request for a frame.
func (f *frames) invalidate() (schedule bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.scheduled || f.waiting {
		f.wanted = true
		return false
	}
	f.scheduled = true
	return true
}

// fire records that the animation frame came, after which a FrameEvent is
// sent.
func (f *frames) fire() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scheduled, f.waiting, f.wanted = false, true, false
}

// presented records that Present answered the outstanding FrameEvent.
func (f *frames) presented() (schedule bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.waiting = false
	if !f.wanted {
		return false
	}
	f.wanted, f.scheduled = false, true
	return true
}
