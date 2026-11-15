package win32

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"viewrab/internal/ui/platform"
)

// className is the window class of every window the backend makes.
const className = "ViewrabWindow"

// eventQueue is how many events a window buffers before it starts dropping
// the newest, which keeps a stalled application from blocking the UI thread.
const eventQueue = 1024

// Backend is a platform.Platform that opens Win32 windows. A Win32 window
// belongs to the thread that made it and receives its messages there, so
// every window is made and handled on one thread, the UI thread, which the
// backend locks a goroutine to. Calls from other goroutines are queued to it.
type Backend struct {
	tid      uint32  // the UI thread
	instance uintptr // the module, for the window class
	runner   uintptr // a message-only window that runs the queued functions
	arrow    uintptr // the default cursor

	mu      sync.Mutex
	queue   []func()
	windows map[uintptr]*window
	// creating is the window CreateWindowEx is making, which receives
	// messages before CreateWindowEx returns its handle.
	creating *window
	closed   bool
	done     chan struct{}

	drag drag
}

// current is the running backend, the one the window procedure serves. Only
// one runs at a time, as the window class it registers is the program's.
var (
	current   atomic.Pointer[Backend]
	wndProcCB = sync.OnceValue(func() uintptr { return syscall.NewCallback(wndProc) })
	classOnce sync.Once
	classErr  error
)

// ErrRunning is returned by New while another backend runs.
var ErrRunning = errors.New("win32: a backend is already running")

// New starts the UI thread and returns the backend that runs on it.
// Backend.Close ends it.
func New() (*Backend, error) {
	b := &Backend{windows: map[uintptr]*window{}, done: make(chan struct{})}
	if !current.CompareAndSwap(nil, b) {
		return nil, ErrRunning
	}
	ready := make(chan error, 1)
	go b.run(ready)
	if err := <-ready; err != nil {
		current.Store(nil)
		return nil, err
	}
	return b, nil
}

// run is the UI thread: it registers the window class, makes the runner
// window and then dispatches messages until Close.
func (b *Backend) run(ready chan<- error) {
	runtime.LockOSThread()
	defer close(b.done)
	b.tid = windows.GetCurrentThreadId()
	b.instance, _, _ = procGetModuleHandle.Call(0)
	b.arrow, _, _ = procLoadCursor.Call(0, idcArrow)
	classOnce.Do(func() {
		wc := wndClassEx{
			wndProc:   wndProcCB(),
			instance:  b.instance,
			cursor:    b.arrow,
			className: utf16Ptr(className),
		}
		wc.size = uint32(unsafe.Sizeof(wc))
		if r, _, err := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			classErr = fmt.Errorf("win32: registering the window class: %w", err)
		}
	})
	if classErr != nil {
		ready <- classErr
		return
	}
	r, _, err := procCreateWindowEx.Call(0, uintptr(unsafe.Pointer(utf16Ptr(className))), 0, 0,
		0, 0, 0, 0, hwndMessage, 0, b.instance, 0)
	if r == 0 {
		ready <- fmt.Errorf("win32: creating the message window: %w", err)
		return
	}
	b.runner = r
	ready <- nil
	var m msg
	for {
		r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	procDestroyWindow.Call(b.runner)
}

// Close destroys the windows left open and ends the UI thread.
func (b *Backend) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	open := make([]*window, 0, len(b.windows))
	for _, w := range b.windows {
		open = append(open, w)
	}
	b.mu.Unlock()
	for _, w := range open {
		w.Close()
	}
	b.post(func() { procPostQuitMessage.Call(0) })
	<-b.done
	current.CompareAndSwap(b, nil)
	return nil
}

// post queues f to run on the UI thread, or runs it at once when called
// there. Queued functions run in order, also while a modal loop, such as a
// move or a menu, holds the thread, since the runner window's messages are
// dispatched there too.
func (b *Backend) post(f func()) {
	if windows.GetCurrentThreadId() == b.tid {
		f()
		return
	}
	b.mu.Lock()
	b.queue = append(b.queue, f)
	first := len(b.queue) == 1
	b.mu.Unlock()
	if first {
		procPostMessage.Call(b.runner, wmRun, 0, 0)
	}
}

// call runs f on the UI thread and waits for it to finish.
func (b *Backend) call(f func()) {
	done := make(chan struct{})
	b.post(func() {
		defer close(done)
		f()
	})
	<-done
}

// runQueued runs the functions queued for the UI thread, one at a time. Each
// is taken off the queue before it runs, so a function that enters a modal
// loop lets the rest run from within that loop.
func (b *Backend) runQueued() {
	for {
		b.mu.Lock()
		if len(b.queue) == 0 {
			b.mu.Unlock()
			return
		}
		f := b.queue[0]
		b.queue = b.queue[1:]
		b.mu.Unlock()
		f()
	}
}

// windowFor returns the window with handle hwnd. A window being made is
// bound to its handle at its first message.
func (b *Backend) windowFor(hwnd uintptr) *window {
	b.mu.Lock()
	defer b.mu.Unlock()
	if w, ok := b.windows[hwnd]; ok {
		return w
	}
	if w := b.creating; w != nil && hwnd != b.runner {
		w.hwnd = hwnd
		b.windows[hwnd] = w
		b.creating = nil
		return w
	}
	return nil
}

// wndProc is the window procedure of every window of the class.
func wndProc(hwnd, message, wp, lp uintptr) uintptr {
	if b := current.Load(); b != nil {
		if hwnd == b.runner {
			if message == wmRun {
				b.runQueued()
				return 0
			}
		} else if w := b.windowFor(hwnd); w != nil {
			if r, ok := w.handle(uint32(message), wp, lp); ok {
				return r
			}
		}
	}
	r, _, _ := procDefWindowProc.Call(hwnd, message, wp, lp)
	return r
}

// NewWindow opens a window. Its first ResizeEvent and FrameEvent arrive once
// it is shown.
func (b *Backend) NewWindow(opts platform.WindowOptions) (platform.Window, error) {
	w := &window{b: b, events: make(chan platform.Event, eventQueue), minSize: opts.MinSize,
		cursor: b.arrow}
	var err error
	b.call(func() { err = w.create(opts) })
	if err != nil {
		return nil, err
	}
	return w, nil
}
