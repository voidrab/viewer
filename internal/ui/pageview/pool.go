package pageview

import (
	"container/list"
	"context"
	"image"
	"runtime"
	"sync"

	"viewer/internal/ui/layout"
)

// PoolOptions tunes a Pool. The zero value is usable.
type PoolOptions struct {
	// Workers is the number of renders in flight at once; zero means half the
	// CPUs, at least one.
	Workers int
	// CacheBytes bounds the memory of all cached page bitmaps; zero means
	// 192 MiB.
	CacheBytes int
}

// Pool renders pages for any number of views on a few workers and keeps their
// bitmaps under one memory budget. Work is ordered by priority: the page in the
// middle of the active view first, then its neighbours, then the background
// views. A view has at most one render running, because the pages of one
// document render one after another whatever the number of workers, and work
// for a zoom level that is no longer wanted is dropped.
type Pool struct {
	mu     sync.Mutex
	cond   *sync.Cond
	closed bool
	wg     sync.WaitGroup

	jobs   []*job // waiting to run
	nextID int

	budget int
	cache  map[cacheKey]*entry
	lru    *list.List // front is most recently used
	bytes  int
	latest map[viewPage]int // dpi of the newest bitmap of each page
}

// cacheKey identifies a rendered bitmap: a page of a view at a whole dpi.
type cacheKey struct{ view, page, dpi int }

// viewPage identifies a page of a view.
type viewPage struct{ view, page int }

type entry struct {
	key  cacheKey
	img  *image.RGBA
	elem *list.Element
}

// penalty is added to the priority of every job of an inactive view, which
// puts the active view's work first whatever the distance of its pages.
const penalty = 1000

// job is one page render.
type job struct {
	v       *View
	k       key
	base    int // how far the page is from the middle of its view
	ctx     context.Context
	cancel  context.CancelFunc
	started bool
}

// NewPool starts a pool and its workers. Close stops them.
func NewPool(opts PoolOptions) *Pool {
	if opts.Workers <= 0 {
		opts.Workers = max(1, runtime.NumCPU()/2)
	}
	if opts.CacheBytes <= 0 {
		opts.CacheBytes = 192 << 20
	}
	p := &Pool{
		budget: opts.CacheBytes,
		cache:  map[cacheKey]*entry{}, lru: list.New(), latest: map[viewPage]int{},
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
	p.cache, p.latest, p.bytes = map[cacheKey]*entry{}, map[viewPage]int{}, 0
	p.lru.Init()
}

// attach gives a view its identity in the pool.
func (p *Pool) attach(v *View) {
	p.mu.Lock()
	p.nextID++
	v.id = p.nextID
	v.inflight = map[key]*job{}
	p.mu.Unlock()
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
	for v.running {
		p.cond.Wait()
	}
}

// has reports whether page k of v is cached, without touching its recency.
func (p *Pool) has(v *View, k key) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cache[cacheKey{v.id, k.page, k.dpi}] != nil
}

// tooBig reports whether the bitmaps of pages [lo, hi) of lay, at its scale,
// would take more than half the budget, leaving the rest to other views.
func (p *Pool) tooBig(lay *layout.Layout, lo, hi int) bool {
	need := 0
	for i := lo; i < hi; i++ {
		r := lay.ViewRect(i)
		need += 4 * r.Dx() * r.Dy()
	}
	return need > p.budget/2
}

// lookup returns the bitmap of page k of v and marks it recently used.
func (p *Pool) lookup(v *View, k key) *image.RGBA {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.cache[cacheKey{v.id, k.page, k.dpi}]
	if e == nil {
		return nil
	}
	p.lru.MoveToFront(e.elem)
	return e.img
}

// standIn returns the newest bitmap of a page at any scale, to show in place
// of the one being rendered.
func (p *Pool) standIn(v *View, page int) *image.RGBA {
	p.mu.Lock()
	defer p.mu.Unlock()
	e := p.cache[cacheKey{v.id, page, p.latest[viewPage{v.id, page}]}]
	if e == nil {
		return nil
	}
	return e.img
}

// submit asks for page k of v to be rendered, base being how far the page is
// from the middle of the view. A render already asked for keeps its place but
// takes the new base.
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
// views that have no render running, or nil. The caller holds p.mu.
func (p *Pool) pick() *job {
	best := -1
	bestPrio := 0
	for i, j := range p.jobs {
		if j.v.running {
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
		j.v.running = true
		p.mu.Unlock()

		img, err := j.v.render(j.ctx, j.v.pages[j.k.page], float64(j.k.dpi)/72)

		p.mu.Lock()
		j.v.running = false
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

// store caches img for page k of v and evicts the least recently used bitmaps,
// of any view, beyond the budget. The caller holds p.mu.
func (p *Pool) store(v *View, k key, img *image.RGBA) {
	ck := cacheKey{v.id, k.page, k.dpi}
	if old := p.cache[ck]; old != nil {
		p.remove(old)
	}
	e := &entry{key: ck, img: img}
	e.elem = p.lru.PushFront(e)
	p.cache[ck] = e
	p.bytes += len(img.Pix)
	p.latest[viewPage{v.id, k.page}] = k.dpi
	for p.bytes > p.budget && p.lru.Len() > 1 {
		p.remove(p.lru.Back().Value.(*entry))
	}
}

// remove drops one cached bitmap. The caller holds p.mu.
func (p *Pool) remove(e *entry) {
	p.lru.Remove(e.elem)
	delete(p.cache, e.key)
	p.bytes -= len(e.img.Pix)
	vp := viewPage{e.key.view, e.key.page}
	if p.latest[vp] == e.key.dpi {
		delete(p.latest, vp)
	}
}
