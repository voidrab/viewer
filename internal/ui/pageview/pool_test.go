package pageview

import (
	"context"
	"image"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/voidrab/gopdfrab"

	"viewer/internal/ui/paint"
)

// tracker records which pages are rendered, at which dpi, in which order, and
// lets a test hold chosen renders back.
type tracker struct {
	mu    sync.Mutex
	order []key
	views []*View       // the view of each entry of order
	gate  chan struct{} // renders of blocked pages wait for it to close
	block func(k key) bool
	seen  chan key
}

func newTracker(block func(k key) bool) *tracker {
	return &tracker{gate: make(chan struct{}), block: block, seen: make(chan key, 64)}
}

// render is the renderFunc of a view whose pages are v.pages.
func (tr *tracker) render(v *View) renderFunc {
	return func(ctx context.Context, p *gopdfrab.Page, scale float64) (*image.RGBA, error) {
		page := 0
		for i, q := range v.pages {
			if q == p {
				page = i
			}
		}
		k := key{page, int(math.Round(scale * 72))}
		tr.mu.Lock()
		tr.order = append(tr.order, k)
		tr.views = append(tr.views, v)
		tr.mu.Unlock()
		tr.seen <- k
		if tr.block != nil && tr.block(k) {
			<-tr.gate
		}
		return image.NewRGBA(image.Rect(0, 0, 2, 2)), nil
	}
}

func (tr *tracker) started() []key {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return append([]key(nil), tr.order...)
}

// viewOf returns the view of the i-th render started.
func (tr *tracker) viewOf(i int) *View {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.views[i]
}

func sharedView(t *testing.T, pool *Pool, pages int, tr *tracker) (*View, chan struct{}) {
	t.Helper()
	inv := make(chan struct{}, 256)
	v, err := New(openDoc(t, pages), image.Pt(124, 400), Options{Pool: pool}, func() { inv <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	v.render = tr.render(v)
	t.Cleanup(v.Close)
	return v, inv
}

// waitIdle waits until the pool has nothing waiting or running.
func waitIdle(t *testing.T, pool *Pool) {
	t.Helper()
	waitFor(t, "the pool to go idle", func() bool {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		return len(pool.jobs) == 0
	})
	// A render may still be running when nothing waits; let it land.
	time.Sleep(30 * time.Millisecond)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestZoomDropsRendersOfTheOldLevel(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1})
	t.Cleanup(pool.Close)
	var once sync.Once
	tr := newTracker(func(k key) bool { // only the first render blocks, whichever page the pool picks
		block := false
		once.Do(func() { block = true })
		return block
	})
	// Open the gate even if the test fails, so Close does not wait forever.
	var open sync.Once
	t.Cleanup(func() { open.Do(func() { close(tr.gate) }) })
	v, _ := sharedView(t, pool, 3, tr)
	c := paint.New(124, 400)

	v.Draw(c) // old level: page 0 starts and blocks, pages 1 and 2 wait behind it
	first := <-tr.seen
	oldDPI := first.dpi
	v.ZoomIn()
	v.Draw(c) // new level: the waiting old-level renders are dropped
	for k := range v.inflight {
		if k.dpi == oldDPI {
			t.Errorf("a request for the old zoom level is still wanted: %+v", k)
		}
	}
	open.Do(func() { close(tr.gate) })
	waitFor(t, "the new level to render", func() bool { return inflightCount(v) == 0 })
	waitIdle(t, pool)

	for _, k := range tr.started() {
		if k.dpi == oldDPI && k != first {
			t.Errorf("an old-level render was started after the zoom: %+v", k)
		}
	}
	// The blocked old-level render finished, but its result was not kept.
	pool.mu.Lock()
	defer pool.mu.Unlock()
	for ck := range pool.cache {
		if ck.dpi == oldDPI {
			t.Errorf("a bitmap of the old zoom level was cached: %+v", ck)
		}
	}
	if len(pool.cache) == 0 {
		t.Error("nothing was rendered at the new zoom level")
	}
}

func TestPagesRenderNearestTheMiddleFirst(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1})
	t.Cleanup(pool.Close)
	hold := newTracker(func(key) bool { return true }) // keeps the one worker busy
	blocker, _ := sharedView(t, pool, 1, hold)
	blocker.Draw(paint.New(124, 400))
	<-hold.seen

	tr := newTracker(nil)
	v, _ := sharedView(t, pool, 5, tr)
	v.lay.ScrollBy(image.Pt(0, 130)) // the middle of the viewport is now in page 1 or 2
	v.Draw(paint.New(124, 400))
	close(hold.gate)
	waitFor(t, "the pages to render", func() bool { return len(tr.started()) >= 3 })

	got := tr.started()
	mid := v.lay.Current()
	prev := -1
	for _, k := range got {
		if d := abs(k.page - mid); d < prev {
			t.Fatalf("page %d (distance %d) rendered after a page %d further away: %v", k.page, d, prev, got)
		} else {
			prev = d
		}
	}
	if got[0].page != mid {
		t.Errorf("the first render was page %d, not the middle page %d", got[0].page, mid)
	}
}

func TestActiveViewsRenderBeforeBackgroundOnes(t *testing.T) {
	for name, swap := range map[string]bool{"as asked": false, "after switching tabs": true} {
		t.Run(name, func(t *testing.T) {
			pool := NewPool(PoolOptions{Workers: 1})
			t.Cleanup(pool.Close)
			hold := newTracker(func(key) bool { return true })
			blocker, _ := sharedView(t, pool, 1, hold)
			blocker.Draw(paint.New(124, 400))
			<-hold.seen // the single worker is busy

			// One tracker for both views, so the start order is global.
			tr := newTracker(nil)
			a, _ := sharedView(t, pool, 1, tr)
			b, _ := sharedView(t, pool, 1, tr)
			b.SetActive(false)
			b.Draw(paint.New(124, 400)) // the background view asks first
			a.Draw(paint.New(124, 400))
			want := a
			if swap {
				a.SetActive(false)
				b.SetActive(true)
				want = b
			}
			close(hold.gate)
			waitFor(t, "both views to render", func() bool { return len(tr.started()) == 2 })
			if got := tr.viewOf(0); got != want {
				t.Errorf("the first render was the other view's: want view %d, got view %d", want.id, got.id)
			}
		})
	}
}

func TestOnlyOneRenderPerViewRunsAtATime(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 4})
	t.Cleanup(pool.Close)
	var perView [2]atomic.Int32
	var maxPer, total, maxTotal atomic.Int32
	renderFor := func(idx int, v *View) renderFunc {
		return func(context.Context, *gopdfrab.Page, float64) (*image.RGBA, error) {
			n := perView[idx].Add(1)
			for {
				m := maxPer.Load()
				if n <= m || maxPer.CompareAndSwap(m, n) {
					break
				}
			}
			tot := total.Add(1)
			for {
				m := maxTotal.Load()
				if tot <= m || maxTotal.CompareAndSwap(m, tot) {
					break
				}
			}
			time.Sleep(25 * time.Millisecond)
			total.Add(-1)
			perView[idx].Add(-1)
			return image.NewRGBA(image.Rect(0, 0, 2, 2)), nil
		}
	}
	a, _ := sharedView(t, pool, 3, newTracker(nil))
	b, _ := sharedView(t, pool, 3, newTracker(nil))
	a.render, b.render = renderFor(0, a), renderFor(1, b)
	a.Draw(paint.New(124, 400))
	b.Draw(paint.New(124, 400))
	waitFor(t, "all pages to render", func() bool {
		return inflightCount(a) == 0 && inflightCount(b) == 0
	})
	waitIdle(t, pool)
	if maxPer.Load() != 1 {
		t.Errorf("up to %d renders of one view ran at once", maxPer.Load())
	}
	if maxTotal.Load() != 2 {
		t.Errorf("up to %d renders ran at once across two views, want 2", maxTotal.Load())
	}
}

func TestPrefetchRendersWithoutDrawing(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1})
	t.Cleanup(pool.Close)
	tr := newTracker(nil)
	v, inv := sharedView(t, pool, 2, tr)
	v.SetActive(false)
	v.Prefetch()
	waitInv(t, inv)
	waitFor(t, "both pages to be cached", func() bool {
		return pool.has(v, key{0, dpiFor(v.lay.Scale())}) && pool.has(v, key{1, dpiFor(v.lay.Scale())})
	})
	before := len(tr.started())
	v.Prefetch() // already cached: nothing more to render
	time.Sleep(50 * time.Millisecond)
	if after := len(tr.started()); after != before {
		t.Errorf("a second Prefetch rendered %d more pages", after-before)
	}
	// Drawing then finds the bitmaps ready.
	c := paint.New(124, 400)
	v.Draw(c)
	if got := len(tr.started()); got != before {
		t.Errorf("drawing a prefetched view rendered %d more pages", got-before)
	}
}

func TestCloseWaitsForARunningRender(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1})
	t.Cleanup(pool.Close)
	tr := newTracker(func(key) bool { return true })
	v, _ := sharedView(t, pool, 1, tr)
	v.Draw(paint.New(124, 400))
	<-tr.seen // the render is running
	closed := make(chan struct{})
	go func() { v.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned while a render of the view was running")
	case <-time.After(60 * time.Millisecond):
	}
	close(tr.gate)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the render finished")
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if len(pool.cache) != 0 || len(v.inflight) != 0 {
		t.Errorf("a closed view left %d bitmaps and %d requests", len(pool.cache), len(v.inflight))
	}
}

func TestPoolCloseStopsWorkersAndIgnoresLaterWork(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1})
	tr := newTracker(func(key) bool { return true })
	v, _ := sharedView(t, pool, 3, tr)
	v.Draw(paint.New(124, 400))
	<-tr.seen // one render running, the others waiting
	done := make(chan struct{})
	go func() { pool.Close(); close(done) }()
	time.Sleep(30 * time.Millisecond)
	close(tr.gate)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if len(pool.cache) != 0 || len(pool.jobs) != 0 {
		t.Errorf("a closed pool holds %d bitmaps and %d jobs", len(pool.cache), len(pool.jobs))
	}
	pool.mu.Unlock()
	pool.submit(v, key{0, 72}, 0) // ignored once closed
	pool.mu.Lock()
	if len(pool.jobs) != 0 {
		t.Error("a closed pool accepted work")
	}
}

func TestOneBudgetCoversEveryView(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1, CacheBytes: 200})
	t.Cleanup(pool.Close)
	a, _ := sharedView(t, pool, 2, newTracker(nil))
	b, _ := sharedView(t, pool, 2, newTracker(nil))
	img := func() *image.RGBA { return image.NewRGBA(image.Rect(0, 0, 5, 5)) } // 100 bytes
	pool.mu.Lock()
	pool.store(a, key{0, 72}, img())
	pool.store(b, key{0, 72}, img())
	pool.store(a, key{1, 72}, img()) // over budget: the oldest, a's page 0, goes
	_, aGone := pool.cache[cacheKey{a.id, 0, 72}]
	_, bKept := pool.cache[cacheKey{b.id, 0, 72}]
	n := len(pool.cache)
	pool.mu.Unlock()
	if aGone || !bKept || n != 2 {
		t.Errorf("a's page 0 kept: %v, b's page 0 kept: %v, %d bitmaps", aGone, bKept, n)
	}
	if pool.standIn(a, 0) != nil {
		t.Error("an evicted page still offers a stand-in")
	}
	// Storing a page again replaces it instead of counting twice.
	pool.mu.Lock()
	pool.store(b, key{0, 72}, img())
	bytes := pool.bytes
	pool.mu.Unlock()
	if bytes != 200 {
		t.Errorf("accounted %d bytes for two bitmaps of 100", bytes)
	}
}

func TestStandInIsTheNewestBitmapOfThePage(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1})
	t.Cleanup(pool.Close)
	v, _ := sharedView(t, pool, 1, newTracker(nil))
	small, big := image.NewRGBA(image.Rect(0, 0, 2, 2)), image.NewRGBA(image.Rect(0, 0, 4, 4))
	pool.mu.Lock()
	pool.store(v, key{0, 72}, small)
	pool.store(v, key{0, 144}, big)
	pool.mu.Unlock()
	if got := pool.standIn(v, 0); got != big {
		t.Error("the stand-in is not the newest bitmap")
	}
	if pool.standIn(v, 5) != nil {
		t.Error("a page never rendered offers a stand-in")
	}
}

func TestNewPoolDefaults(t *testing.T) {
	for _, opts := range []PoolOptions{{}, {Workers: -3, CacheBytes: -1}} {
		p := NewPool(opts)
		if p.budget != 192<<20 {
			t.Errorf("budget = %d for %+v", p.budget, opts)
		}
		p.Close()
	}
}

func TestSetActiveOnlyActsOnAChange(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1})
	t.Cleanup(pool.Close)
	v, _ := sharedView(t, pool, 1, newTracker(nil))
	v.SetActive(true) // a new view is already active
	pool.mu.Lock()
	if v.inactive {
		t.Error("an active view became inactive")
	}
	pool.mu.Unlock()
	v.SetActive(false)
	v.SetActive(false)
	pool.mu.Lock()
	defer pool.mu.Unlock()
	if !v.inactive {
		t.Error("the view is not inactive")
	}
}
