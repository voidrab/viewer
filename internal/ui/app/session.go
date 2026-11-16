package app

import (
	"cmp"
	"errors"
	"path/filepath"
	"slices"
	"sync"

	"viewrab/internal/ui/instance"
	"viewrab/internal/ui/pageview"
	"viewrab/internal/ui/platform"
	"viewrab/internal/ui/theme"
)

// errNoWindows is reported when the platform cannot open another window.
var errNoWindows = errors.New("several windows are not available here")

// session is what the windows of one run share: the options, the render pool,
// which window launches go to, and the drag of tabs between windows. Each
// window runs its own event loop; they reach one another by posting work to
// each other's loop.
type session struct {
	opts Options
	pool *pageview.Pool

	mu     sync.Mutex
	apps   []*App // the open windows, oldest first
	active *App   // the window launches go to: the last one focused or opened
	drag   *tabDrag
	empty  chan struct{} // closed when the last window has closed
	ended  bool

	wg sync.WaitGroup // the windows' event loops
}

// tabDrag is a drag of tabs: one, or every tab of a window when whole is set.
type tabDrag struct {
	from  *App
	docs  []*document
	whole bool
}

func newSession(opts Options) *session {
	return &session{opts: opts, pool: pageview.NewPool(poolOptions(opts)), empty: make(chan struct{})}
}

// poolOptions returns the options of the pool that renders for the windows:
// the defaults, unless pages render elsewhere, where every render may run at
// once, those of one document included.
func poolOptions(opts Options) pageview.PoolOptions {
	if opts.Render == nil {
		return pageview.PoolOptions{}
	}
	n := max(1, opts.RenderWorkers)
	return pageview.PoolOptions{Workers: n, PerView: n}
}

// options returns the options a new window starts with: those of the run,
// with the palette the windows now have.
func (s *session) options() Options {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opts
}

// setPalette records the palette the windows now have.
func (s *session) setPalette(p theme.Palette) {
	s.mu.Lock()
	s.opts.Palette = p
	s.mu.Unlock()
}

// newApp makes a window of the session on win, which becomes the window
// launches go to until another is focused.
func (s *session) newApp(win platform.Window) (*App, error) {
	a, err := newApp(s, win)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.apps = append(s.apps, a)
	s.active = a
	s.mu.Unlock()
	return a, nil
}

// start runs a's event loop.
func (s *session) start(a *App) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		a.loop()
	}()
}

// remove forgets the closed window a. With the last window gone, the pool is
// closed and the session has ended.
func (s *session) remove(a *App) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := slices.Index(s.apps, a)
	if i < 0 {
		return
	}
	s.apps = slices.Delete(s.apps, i, i+1)
	if s.active == a {
		s.active = nil
		if n := len(s.apps); n > 0 {
			s.active = s.apps[n-1]
		}
	}
	if s.drag != nil && s.drag.from == a {
		s.drag = nil
	}
	if len(s.apps) == 0 && !s.ended {
		s.ended = true
		s.pool.Close()
		close(s.empty)
	}
}

// windows returns the open windows.
func (s *session) windows() []*App {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.apps)
}

// focus makes a the window launches go to.
func (s *session) focus(a *App) {
	s.mu.Lock()
	if slices.Contains(s.apps, a) {
		s.active = a
	}
	s.mu.Unlock()
}

// target returns the window launches go to, or nil when none is open.
func (s *session) target() *App {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

// openWindow opens another window, showing docs, which it takes over, and
// opening paths in it.
func (s *session) openWindow(docs []*document, paths []string) error {
	if s.opts.NewWindow == nil {
		return errNoWindows
	}
	win, err := s.opts.NewWindow()
	if err != nil {
		return err
	}
	a, err := s.newApp(win)
	if err != nil {
		win.Close()
		return err
	}
	for _, d := range docs {
		d.owner.Store(a)
	}
	a.pendingDocs, a.pending = docs, paths
	s.start(a)
	return nil
}

// each runs f on the event loop of every window but except.
func (s *session) each(except *App, f func(*App)) {
	for _, a := range s.windows() {
		if a != except {
			go a.post(func() { f(a) })
		}
	}
}

// launch carries out a later launch's request: a file already open is
// switched to in its window, and the others open as tabs in the window
// launches go to. Each window concerned is brought forward.
func (s *session) launch(req instance.Request) {
	var order []*App
	byWindow := map[*App][]string{}
	for _, p := range req.Paths {
		abs, _ := filepath.Abs(p)
		abs = cmp.Or(abs, p)
		a := s.showing(abs)
		if a == nil {
			a = s.target()
		}
		if _, ok := byWindow[a]; !ok {
			order = append(order, a)
		}
		byWindow[a] = append(byWindow[a], abs)
	}
	if len(order) == 0 {
		order = []*App{s.target()}
	}
	for _, a := range order {
		if a == nil {
			continue // every window has closed
		}
		paths := byWindow[a]
		a.post(func() {
			a.Open(paths...)
			a.win.Activate(req.Token)
		})
	}
}

// showing returns the window with a tab showing path, or nil.
func (s *session) showing(path string) *App {
	for _, a := range s.windows() {
		has := make(chan bool, 1)
		if !a.post(func() { has <- a.find(path) >= 0 }) {
			continue
		}
		select {
		case ok := <-has:
			if ok {
				return a
			}
		case <-a.done:
		}
	}
	return nil
}

// setDrag records the drag of tabs under way.
func (s *session) setDrag(d *tabDrag) {
	s.mu.Lock()
	s.drag = d
	s.mu.Unlock()
}

// currentDrag returns the drag of tabs under way, or nil.
func (s *session) currentDrag() *tabDrag {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.drag
}

// takeDrag returns the drag of tabs under way, if it is d or d is nil, and
// forgets it, so that each drag is taken once.
func (s *session) takeDrag(d *tabDrag) *tabDrag {
	s.mu.Lock()
	defer s.mu.Unlock()
	got := s.drag
	if got == nil || d != nil && got != d {
		return nil
	}
	s.drag = nil
	return got
}

// transfer moves the tabs of drag d from their window to the gap at in window
// to. The window they leave closes if they were all it had.
func (s *session) transfer(d *tabDrag, to *App, at int) {
	from := d.from
	go from.post(func() {
		docs := from.takeOut(d.docs)
		if len(docs) == 0 {
			return
		}
		for _, x := range docs {
			x.owner.Store(to)
		}
		go func() {
			if !to.post(func() { to.adopt(docs, at) }) {
				for _, x := range docs {
					x.close() // the window closed while they were on their way
				}
			}
		}()
		if len(from.docs) == 0 {
			from.quitting = true
		}
	})
}
