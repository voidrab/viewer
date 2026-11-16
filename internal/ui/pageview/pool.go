package pageview

import (
	"container/list"
	"context"
	"image"
	"runtime"
	"sync"
)

// PoolOptions tunes a Pool. The zero value is usable.
type PoolOptions struct {
	// Workers is the number of renders in flight at once; zero means half the
	// CPUs, at least one.
	Workers int
	// PerView is the number of renders of one view in flight at once; zero
	// means one. A document renders one page at a time, so more help only
	// views whose Options.Render renders elsewhere, such as in other
	// processes that each have the document open.
	PerView int
	// CacheBytes bounds the memory of all cached page bitmaps. Zero means a
	// budget that follows the views' viewports: viewportScreens viewports'
	// worth of pixels, between minBudget and maxBudget.
	CacheBytes int
}

// viewportScreens, minBudget and maxBudget size the cache budget when it
// follows the viewports. The budget is viewportScreens times the 4-byte
// pixels of the largest viewport, clamped to the range from minBudget to
// maxBudget. The factor keeps rendering ahead within half the budget, as want
// requires. minBudget leaves room for the previews of a small window, and
// maxBudget bounds the budget on a large screen.
const (
	viewportScreens = 16
	minBudget       = 32 << 20
	maxBudget       = 128 << 20
)

// Pool renders parts of pages for any number of views on a few workers and
// keeps their bitmaps under one memory budget. Work is ordered by priority:
// what is in the active view first, nearest its middle, then what lies just
// beyond it, then the background views. A view has at most PerView renders
// running, one unless the options say otherwise, because the pages of one
// document render one after another whatever the number of workers, and work
// that is no longer wanted is dropped.
type Pool struct {
	mu     sync.Mutex
	cond   *sync.Cond
	closed bool
	wg     sync.WaitGroup

	jobs    []*job // waiting to run
	nextID  int
	perView int // the renders of one view that may run at once

	budget int
	fixed  bool        // the budget was given, rather than following the viewports
	areas  map[int]int // the viewport of each view, in pixels
	cache  map[cacheKey]*entry
	lru    *list.List // front is most recently used
	bytes  int
	byPage map[viewPage][]*entry // the cached bitmaps of each page
	seq    int                   // counts stores, to tell the newest
}

// cacheKey identifies a rendered bitmap: part of a page of a view.
type cacheKey struct {
	view int
	k    key
}

// viewPage identifies a page of a view.
type viewPage struct{ view, page int }

type entry struct {
	key  cacheKey
	img  *image.RGBA
	elem *list.Element
	seq  int // when it was stored
}

// penalty is added to the priority of every job of an inactive view, and to a
// part beyond the viewport, which puts what is in view in the active view
// first, then the rest of the active view and the background views by distance.
const penalty = 1000

// job is the render of one part of a page.
type job struct {
	v       *View
	k       key
	base    int // how far the part is from the middle of its view; lower runs first
	ctx     context.Context
	cancel  context.CancelFunc
	started bool
}

// NewPool starts a pool and its workers. Close stops them.
func NewPool(opts PoolOptions) *Pool {
	if opts.Workers <= 0 {
		opts.Workers = max(1, runtime.NumCPU()/2)
	}
	p := &Pool{
		perView: max(1, opts.PerView),
		budget:  opts.CacheBytes, fixed: opts.CacheBytes > 0, areas: map[int]int{},
		cache: map[cacheKey]*entry{}, lru: list.New(), byPage: map[viewPage][]*entry{},
	}
	if !p.fixed {
		p.budget = minBudget
	}
	p.cond = sync.NewCond(&p.mu)
	for range opts.Workers {
		p.wg.Add(1)
		go p.work()
	}
	return p
}

// Close drops all work and bitmaps and stops the workers once their current
// renders finish.
func (p *Pool) Close() {
	p.mu.Lock()
	p.closed = true
	for _, j := range p.jobs {
		j.cancel()
	}
	p.jobs = nil
	p.clearCache()
	p.cond.Broadcast()
	p.mu.Unlock()
	p.wg.Wait()
}

func (p *Pool) clearCache() {
	p.cache, p.byPage, p.bytes = map[cacheKey]*entry{}, map[viewPage][]*entry{}, 0
	p.lru.Init()
}

// attach gives a view of viewport size vp its identity in the pool.
func (p *Pool) attach(v *View, vp image.Point) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextID++
	v.id = p.nextID
	v.inflight = map[key]*job{}
	p.areas[v.id] = vp.X * vp.Y
	p.fitBudget()
}

// setViewport tells the pool the new viewport size of v.
func (p *Pool) setViewport(v *View, vp image.Point) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.areas[v.id] = vp.X * vp.Y
	p.fitBudget()
}

// fitBudget sets a budget that follows the viewports from the largest of them,
// and evicts what no longer fits. The caller holds p.mu.
func (p *Pool) fitBudget() {
	if p.fixed {
		return
	}
	biggest := 0
	for _, a := range p.areas {
		biggest = max(biggest, a)
	}
	p.budget = min(max(viewportScreens*4*biggest, minBudget), maxBudget)
	p.trim()
}

// budgetBytes returns the budget in effect.
func (p *Pool) budgetBytes() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.budget
}

// detach cancels a view's work and forgets its bitmaps. It returns once no
// render of the view is running, so the document can be closed afterwards.
func (p *Pool) detach(v *View) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancelWhere(v, func(key) bool { return false })
	for e := p.lru.Front(); e != nil; {
		next := e.Next()
		if en := e.Value.(*entry); en.key.view == v.id {
			p.remove(en)
		}
		e = next
	}
	delete(p.areas, v.id)
	p.fitBudget()
	for v.running > 0 {
		p.cond.Wait()
	}
}

// has reports whether part k of v is cached, without touching its recency.
func (p *Pool) has(v *View, k key) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cache[cacheKey{v.id, k}] != nil
}

// hasPage reports whether anything of page is cached.
func (p *Pool) hasPage(v *View, page int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.byPage[viewPage{v.id, page}]) > 0
}

// lookup returns the bitmap of part k of v and marks it recently used.
func (p *Pool) lookup(v *View, k key) *image.RGBA {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.cache[cacheKey{v.id, k}]
	if e == nil {
		return nil
	}
	p.lru.MoveToFront(e.elem)
	return e.img
}

// piece is a cached bitmap with the part of the page it shows.
type piece struct {
	k   key
	img *image.RGBA
}

// standIns returns the bitmaps of page to show in place of parts at dpi that
// are not rendered yet, in the order to draw them: the preview, which covers
// the whole page, then the newest other resolution, which covers what has been
// looked at. It marks them recently used, so they are not evicted while they
// stand in.
func (p *Pool) standIns(v *View, page, dpi int) []piece {
	p.mu.Lock()
	defer p.mu.Unlock()
	list := p.byPage[viewPage{v.id, page}]
	var newest *entry
	for _, e := range list {
		if d := e.key.k.dpi; d != dpi && d != previewDPI && (newest == nil || e.seq > newest.seq) {
			newest = e
		}
	}
	var out []piece
	for _, level := range []int{previewDPI, -1} {
		if level == -1 {
			if newest == nil {
				break
			}
			level = newest.key.k.dpi
		}
		if level == dpi {
			continue
		}
		for _, e := range list {
			if e.key.k.dpi == level {
				p.lru.MoveToFront(e.elem)
				out = append(out, piece{e.key.k, e.img})
			}
		}
	}
	return out
}

// prune drops the bitmaps of page at resolutions other than dpi and the
// preview. With keepOther it keeps the newest of the other resolutions, which
// still stands in for parts at dpi that are not rendered yet.
func (p *Pool) prune(v *View, page, dpi int, keepOther bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	list := append([]*entry(nil), p.byPage[viewPage{v.id, page}]...)
	var newest *entry
	for _, e := range list {
		if keepOther && e.key.k.dpi != dpi && e.key.k.dpi != previewDPI && (newest == nil || e.seq > newest.seq) {
			newest = e
		}
	}
	for _, e := range list {
		d := e.key.k.dpi
		if d != dpi && d != previewDPI && (newest == nil || d != newest.key.k.dpi) {
			p.remove(e)
		}
	}
}

// submit asks for part k of v to be rendered, with base as its priority: how
// far it is from the middle of the view, or lower to run sooner. A render
// already asked for keeps its place but takes the new base.
func (p *Pool) submit(v *View, k key, base int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	if j := v.inflight[k]; j != nil {
		j.base = base
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{v: v, k: k, base: base, ctx: ctx, cancel: cancel}
	v.inflight[k] = j
	p.jobs = append(p.jobs, j)
	p.cond.Signal()
}

// cancelStale drops the work of v that keep does not want.
func (p *Pool) cancelStale(v *View, keep func(key) bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancelWhere(v, keep)
}

// cancelWhere cancels the jobs of v that keep rejects. One that is running
// is told to stop, and its result is dropped.
// The caller holds p.mu.
func (p *Pool) cancelWhere(v *View, keep func(key) bool) {
	for k, j := range v.inflight {
		if keep(k) {
			continue
		}
		j.cancel()
		delete(v.inflight, k)
		if !j.started {
			for i, q := range p.jobs {
				if q == j {
					p.jobs = append(p.jobs[:i], p.jobs[i+1:]...)
					break
				}
			}
		}
	}
}

// setActive says whether v is the view being looked at. Jobs of a view that is
// not active wait behind those of the active view.
func (p *Pool) setActive(v *View, active bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if v.inactive != active {
		return // no change
	}
	v.inactive = !active
	p.cond.Broadcast()
}

// pick removes and returns the waiting job with the lowest priority among the
// views with fewer than perView renders running, or nil. The caller holds
// p.mu.
func (p *Pool) pick() *job {
	best := -1
	bestPrio := 0
	for i, j := range p.jobs {
		if j.v.running >= p.perView {
			continue
		}
		prio := j.base
		if j.v.inactive {
			prio += penalty
		}
		if best < 0 || prio < bestPrio {
			best, bestPrio = i, prio
		}
	}
	if best < 0 {
		return nil
	}
	j := p.jobs[best]
	p.jobs = append(p.jobs[:best], p.jobs[best+1:]...)
	return j
}

// work runs jobs until the pool closes.
func (p *Pool) work() {
	defer p.wg.Done()
	for {
		p.mu.Lock()
		var j *job
		for {
			if p.closed {
				p.mu.Unlock()
				return
			}
			if j = p.pick(); j != nil {
				break
			}
			p.cond.Wait()
		}
		j.started = true
		j.v.running++
		p.mu.Unlock()

		img, err := j.v.render(j.ctx, j.v.pages[j.k.page], float64(j.k.dpi)/72, j.k.rect)

		p.mu.Lock()
		j.v.running--
		// Only a render that is still wanted may keep its result: a job that
		// was cancelled has been removed from the view's inflight map.
		wanted := j.v.inflight[j.k] == j && err == nil && !p.closed
		if j.v.inflight[j.k] == j {
			delete(j.v.inflight, j.k)
		}
		if wanted {
			p.store(j.v, j.k, img)
		}
		j.cancel()
		p.cond.Broadcast() // the view may run again; a detach may be waiting
		p.mu.Unlock()
		if wanted {
			j.v.invalidate()
		}
	}
}

// store caches img for part k of v and evicts the least recently used bitmaps,
// of any view, beyond the budget. The caller holds p.mu.
func (p *Pool) store(v *View, k key, img *image.RGBA) {
	ck := cacheKey{v.id, k}
	if old := p.cache[ck]; old != nil {
		p.remove(old)
	}
	p.seq++
	e := &entry{key: ck, img: img, seq: p.seq}
	e.elem = p.lru.PushFront(e)
	p.cache[ck] = e
	vp := viewPage{v.id, k.page}
	p.byPage[vp] = append(p.byPage[vp], e)
	p.bytes += len(img.Pix)
	p.trim()
}

// trim evicts the least recently used bitmaps beyond the budget, keeping at
// least the newest. The caller holds p.mu.
func (p *Pool) trim() {
	for p.bytes > p.budget && p.lru.Len() > 1 {
		p.remove(p.lru.Back().Value.(*entry))
	}
}

// remove drops one cached bitmap. The caller holds p.mu.
func (p *Pool) remove(e *entry) {
	p.lru.Remove(e.elem)
	delete(p.cache, e.key)
	p.bytes -= len(e.img.Pix)
	vp := viewPage{e.key.view, e.key.k.page}
	list := p.byPage[vp]
	for i, q := range list {
		if q == e {
			list = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(list) == 0 {
		delete(p.byPage, vp)
	} else {
		p.byPage[vp] = list
	}
}
