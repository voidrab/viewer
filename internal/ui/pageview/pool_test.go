package pageview

import (
	"context"
	"image"
	"image/color"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/voidrab/gopdfrab"

	"viewrab/internal/testpdf"
	"viewrab/internal/ui/paint"
	"viewrab/internal/ui/platform"
)

// tracker records which parts of pages are rendered, in which order, and lets a
// test hold chosen renders back.
type tracker struct {
	mu      sync.Mutex
	order   []key
	views   []*View       // the view of each entry of order
	gate    chan struct{} // renders of blocked parts wait for it to close
	block   func(k key) bool
	seen    chan key // every render as it starts
	blocked chan key // every render that waits for the gate, as it starts to
	fill    func(k key) color.RGBA
}

func newTracker(block func(k key) bool) *tracker {
	return &tracker{gate: make(chan struct{}), block: block, seen: make(chan key, 256), blocked: make(chan key, 256)}
}

// render is the renderFunc of a view whose pages are v.pages.
func (tr *tracker) render(v *View) renderFunc {
	return func(ctx context.Context, p *gopdfrab.Page, scale float64, region image.Rectangle) (*image.RGBA, error) {
		page := 0
		for i, q := range v.pages {
			if q == p {
				page = i
			}
		}
		k := key{page, int(math.Round(scale * 72)), region}
		tr.mu.Lock()
		tr.order = append(tr.order, k)
		tr.views = append(tr.views, v)
		tr.mu.Unlock()
		tr.seen <- k
		if tr.block != nil && tr.block(k) {
			tr.blocked <- k
			<-tr.gate
		}
		img := image.NewRGBA(image.Rect(0, 0, region.Dx(), region.Dy()))
		if tr.fill != nil {
			c := tr.fill(k)
			for i := 0; i < len(img.Pix); i += 4 {
				img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
			}
		}
		return img, nil
	}
}

func (tr *tracker) started() []key {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return append([]key(nil), tr.order...)
}

// startedParts returns the started renders other than the previews.
func (tr *tracker) startedParts() []key {
	var out []key
	for _, k := range tr.started() {
		if k.dpi != previewDPI {
			out = append(out, k)
		}
	}
	return out
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
	v.now = (&autoClock{}).now
	t.Cleanup(v.Close)
	return v, inv
}

// bigView is a view of one 1000x1000 pt page, zoomed so that it is rendered in
// tiles, in a viewport of vp.
func bigView(t *testing.T, pool *Pool, vp image.Point, tr *tracker) *View {
	t.Helper()
	doc, err := gopdfrab.OpenBytes(testpdf.BuildPages([][2]int{{1000, 1000}}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { doc.Close() })
	v, err := New(doc, vp, Options{Pool: pool}, func() {})
	if err != nil {
		t.Fatal(err)
	}
	v.render = tr.render(v)
	v.now = (&autoClock{}).now
	v.lay.SetZoom(2) // 2000x2000 pixels: more than wholePage
	t.Cleanup(v.Close)
	return v
}

// waitIdle waits until the pool has nothing waiting or running.
func waitIdle(t *testing.T, pool *Pool) {
	t.Helper()
	waitFor(t, "the pool to go idle", func() bool {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		return len(pool.jobs) == 0
	})
	// Renders taken from the queue may still be running; the sleep lets them
	// finish.
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
	tr := newTracker(func(k key) bool { // only the first page render blocks
		block := false
		if k.dpi != previewDPI {
			once.Do(func() { block = true })
		}
		return block
	})
	// Open the gate even if the test fails, so Close does not wait forever.
	var open sync.Once
	t.Cleanup(func() { open.Do(func() { close(tr.gate) }) })
	v, _ := sharedView(t, pool, 3, tr)
	c := paint.New(124, 400)

	v.Draw(c) // old level: one render starts and blocks, the others wait behind it
	first := <-tr.blocked
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

	for _, k := range tr.startedParts() {
		if k.dpi == oldDPI && k != first {
			t.Errorf("an old-level render was started after the zoom: %+v", k)
		}
	}
	// The blocked old-level render finished, but its result was not kept.
	pool.mu.Lock()
	defer pool.mu.Unlock()
	for ck := range pool.cache {
		if ck.k.dpi == oldDPI {
			t.Errorf("a bitmap of the old zoom level was cached: %+v", ck)
		}
	}
	if len(pool.cache) == 0 {
		t.Error("nothing was rendered at the new zoom level")
	}
}

func TestPartsRenderNearestTheMiddleFirst(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1})
	t.Cleanup(pool.Close)
	hold := newTracker(func(key) bool { return true }) // keeps the one worker busy
	blocker, _ := sharedView(t, pool, 1, hold)
	blocker.Draw(paint.New(124, 400))
	<-hold.seen

	tr := newTracker(nil)
	v, _ := sharedView(t, pool, 5, tr)
	v.lay.ScrollBy(image.Pt(0, 130))
	v.Draw(paint.New(124, 400))
	dpi := dpiFor(v.lay.Scale())
	plan := v.plan(dpi, image.Rectangle{Max: v.lay.Viewport()}, true)
	base := map[key]int{}
	for _, w := range plan.wanted {
		base[w.k] = w.base
	}
	close(hold.gate)
	waitFor(t, "the parts to render", func() bool { return len(tr.startedParts()) >= len(plan.wanted) })

	prev := -1
	for _, k := range tr.startedParts() {
		b, ok := base[k]
		if !ok {
			t.Fatalf("an unplanned part was rendered: %+v", k)
		}
		if b < prev {
			t.Fatalf("a part %d from the middle was rendered after one %d away: %v", b, prev, tr.startedParts())
		}
		prev = b
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
			waitFor(t, "both views to render", func() bool { return len(tr.started()) == 4 })
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
	renderFor := func(idx int) renderFunc {
		return func(_ context.Context, _ *gopdfrab.Page, _ float64, region image.Rectangle) (*image.RGBA, error) {
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
	a.render, b.render = renderFor(0), renderFor(1)
	a.Draw(paint.New(124, 400))
	b.Draw(paint.New(124, 400))
	waitFor(t, "all parts to render", func() bool {
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
	plan := v.plan(dpiFor(v.lay.Scale()), image.Rectangle{Max: v.lay.Viewport()}, true)
	waitFor(t, "every wanted part to be cached", func() bool {
		for _, w := range plan.wanted {
			if !pool.has(v, w.k) {
				return false
			}
		}
		return true
	})
	before := len(tr.started())
	v.Prefetch() // already cached: nothing more to render
	time.Sleep(50 * time.Millisecond)
	if after := len(tr.started()); after != before {
		t.Errorf("a second Prefetch rendered %d more parts", after-before)
	}
	// Drawing then finds the bitmaps ready.
	v.Draw(paint.New(124, 400))
	if got := len(tr.started()); got != before {
		t.Errorf("drawing a prefetched view rendered %d more parts", got-before)
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
	pool.submit(v, testKey(0, 72), 0) // ignored once closed
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
	pool.store(a, testKey(0, 72), img())
	pool.store(b, testKey(0, 72), img())
	pool.store(a, testKey(1, 72), img()) // over budget: the oldest, a's page 0, goes
	_, aGone := pool.cache[cacheKey{a.id, testKey(0, 72)}]
	_, bKept := pool.cache[cacheKey{b.id, testKey(0, 72)}]
	n := len(pool.cache)
	pool.mu.Unlock()
	if aGone || !bKept || n != 2 {
		t.Errorf("a's page 0 kept: %v, b's page 0 kept: %v, %d bitmaps", aGone, bKept, n)
	}
	if pool.hasPage(a, 0) || pool.standIns(a, 0, 144) != nil {
		t.Error("an evicted page is still known")
	}
	// Storing a part again replaces it instead of counting twice.
	pool.mu.Lock()
	pool.store(b, testKey(0, 72), img())
	bytes := pool.bytes
	pool.mu.Unlock()
	if bytes != 200 {
		t.Errorf("accounted %d bytes for two bitmaps of 100", bytes)
	}
}

func TestStandInsAreTheNewestResolutionOfThePage(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1})
	t.Cleanup(pool.Close)
	v, _ := sharedView(t, pool, 1, newTracker(nil))
	img := func(n int) *image.RGBA { return image.NewRGBA(image.Rect(0, 0, n, n)) }
	small, big, bigToo := img(2), img(4), img(6)
	left, right := key{page: 0, dpi: 144, rect: image.Rect(0, 0, 4, 4)}, key{page: 0, dpi: 144, rect: image.Rect(4, 0, 8, 4)}
	pool.mu.Lock()
	pool.store(v, testKey(0, 72), small)
	pool.store(v, left, big)
	pool.store(v, right, bigToo)
	pool.mu.Unlock()
	// At a new zoom the newest resolution stands in, in all its parts.
	got := pool.standIns(v, 0, 288)
	if len(got) != 2 || got[0].img == small || got[1].img == small {
		t.Errorf("stand-ins at 288 dpi = %v, want the two 144 dpi parts", got)
	}
	// At the newest resolution the older one stands in.
	if got := pool.standIns(v, 0, 144); len(got) != 1 || got[0].img != small {
		t.Errorf("stand-ins at 144 dpi = %v, want the 72 dpi bitmap", got)
	}
	if pool.standIns(v, 0, 72) == nil {
		t.Error("no stand-in at 72 dpi although other resolutions exist")
	}
	if pool.standIns(v, 5, 72) != nil || pool.hasPage(v, 5) {
		t.Error("a page never rendered offers a stand-in")
	}
	if !pool.hasPage(v, 0) {
		t.Error("a rendered page is not known")
	}
}

func TestNewPoolDefaults(t *testing.T) {
	for _, opts := range []PoolOptions{{}, {Workers: -3, CacheBytes: -1}} {
		p := NewPool(opts)
		if p.fixed || p.budget != minBudget {
			t.Errorf("budget = %d, fixed %v for %+v", p.budget, p.fixed, opts)
		}
		p.Close()
	}
}

func TestBudgetFollowsTheLargestViewport(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1})
	t.Cleanup(pool.Close)
	a, _ := sharedView(t, pool, 1, newTracker(nil))
	if got := pool.budgetBytes(); got != minBudget {
		t.Errorf("small viewport: budget = %d, want the least, %d", got, minBudget)
	}
	a.HandleEvent(platform.ResizeEvent{Size: platform.Size{W: 1000, H: 800}})
	if got, want := pool.budgetBytes(), viewportScreens*4*1000*800; got != want {
		t.Errorf("1000x800: budget = %d, want %d", got, want)
	}
	b, _ := sharedView(t, pool, 1, newTracker(nil))
	b.HandleEvent(platform.ResizeEvent{Size: platform.Size{W: 4000, H: 3000}})
	if got := pool.budgetBytes(); got != maxBudget {
		t.Errorf("huge viewport: budget = %d, want the most, %d", got, maxBudget)
	}
	b.Close()
	if got, want := pool.budgetBytes(), viewportScreens*4*1000*800; got != want {
		t.Errorf("after the huge view closed: budget = %d, want %d", got, want)
	}
}

func TestShrinkingTheBudgetEvictsTheOldest(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1})
	t.Cleanup(pool.Close)
	v, _ := sharedView(t, pool, 3, newTracker(nil))
	v.HandleEvent(platform.ResizeEvent{Size: platform.Size{W: 2000, H: 1000}})
	big := func() *image.RGBA { return image.NewRGBA(image.Rect(0, 0, 2000, 3000)) } // 24 MB
	pool.mu.Lock()
	for i := range 3 {
		pool.store(v, testKey(i, 72), big())
	}
	pool.mu.Unlock()
	v.HandleEvent(platform.ResizeEvent{Size: platform.Size{W: 100, H: 100}}) // down to minBudget
	if pool.has(v, testKey(0, 72)) || !pool.has(v, testKey(2, 72)) {
		t.Error("a smaller viewport did not evict the oldest bitmaps, or evicted the newest")
	}
	if pool.bytes > minBudget {
		t.Errorf("%d bytes cached, over the budget of %d", pool.bytes, minBudget)
	}
}

func TestAGivenBudgetDoesNotFollowTheViewport(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1, CacheBytes: 1000})
	t.Cleanup(pool.Close)
	v, _ := sharedView(t, pool, 1, newTracker(nil))
	v.HandleEvent(platform.ResizeEvent{Size: platform.Size{W: 1000, H: 800}})
	if got := pool.budgetBytes(); got != 1000 {
		t.Errorf("budget = %d, want the given 1000", got)
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

func TestPagesNearTheViewAreRenderedAhead(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1})
	t.Cleanup(pool.Close)
	tr := newTracker(nil)
	v, _ := sharedView(t, pool, 12, tr)
	v.GoToPage(5)
	v.Draw(paint.New(124, 400))
	waitIdle(t, pool)
	view := image.Rect(0, 0, 124, 400)
	reach := view.Inset(-200) // half a viewport
	got := map[int]bool{}
	for _, k := range tr.startedParts() {
		got[k.page] = true
	}
	for i := range 12 {
		if near := v.lay.ViewRect(i).Overlaps(reach); near != got[i] {
			t.Errorf("page %d at %v: within reach %v, rendered %v", i, v.lay.ViewRect(i), near, got[i])
		}
	}
}

func TestNoReadAheadWhenItWouldNotFitTheBudget(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1})
	t.Cleanup(pool.Close)
	tr := newTracker(nil)
	v, _ := sharedView(t, pool, 12, tr)
	v.GoToPage(5)
	view := image.Rectangle{Max: v.lay.Viewport()}
	dpi := dpiFor(v.lay.Scale())
	seen, all := v.plan(dpi, view, false).bytes, v.plan(dpi, view, true).bytes
	if all <= seen {
		t.Fatalf("reading ahead adds nothing: %d, %d bytes", seen, all)
	}
	// The budget holds the parts in view but not the parts ahead as well, so
	// the two would evict each other without end.
	pool.mu.Lock()
	pool.budget = 2 * seen
	pool.mu.Unlock()
	v.Draw(paint.New(124, 400))
	waitIdle(t, pool)
	for _, k := range tr.startedParts() {
		if !v.lay.ViewRect(k.page).Overlaps(view) {
			t.Errorf("page %d, outside the view, was rendered", k.page)
		}
	}
}

func TestAPageZoomedFarInIsRenderedInTilesInView(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1})
	t.Cleanup(pool.Close)
	tr := newTracker(nil)
	v := bigView(t, pool, image.Pt(300, 300), tr)
	v.Draw(paint.New(300, 300))
	waitIdle(t, pool)
	parts := tr.startedParts()
	if len(parts) == 0 {
		t.Fatal("nothing was rendered")
	}
	for _, k := range parts {
		if k.rect.Dx() > tileSize || k.rect.Dy() > tileSize || k.rect.Dx()*k.rect.Dy() >= 2000*2000 {
			t.Errorf("rendered a part of %v, more than a tile", k.rect)
		}
		// Only tiles near the viewport, at the page's top-left corner.
		if k.rect.Min.X > 2*tileSize/2+300 || k.rect.Min.Y > 2*tileSize/2+300 {
			t.Errorf("rendered tile %v far from the viewport", k.rect)
		}
	}
	if len(parts) >= 4 {
		t.Errorf("rendered %d tiles for a page of 4 with a small viewport at one corner", len(parts))
	}
	// Scrolling to the far corner asks for the tiles there instead.
	v.lay.ScrollTo(image.Pt(math.MaxInt32, math.MaxInt32))
	v.Draw(paint.New(300, 300))
	waitIdle(t, pool)
	var far bool
	for _, k := range tr.startedParts() {
		far = far || (k.rect.Min.X > 0 && k.rect.Min.Y > 0)
	}
	if !far {
		t.Error("the tile at the far corner was not rendered")
	}
}

func TestTilesAreDrawnWhereTheyBelongWithoutSeams(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1})
	t.Cleanup(pool.Close)
	colour := func(k key) color.RGBA {
		if k.dpi == previewDPI {
			return color.RGBA{9, 9, 9, 255}
		}
		return color.RGBA{uint8(40 + k.rect.Min.X/8), uint8(40 + k.rect.Min.Y/8), 200, 255}
	}
	tr := newTracker(nil)
	tr.fill = colour
	// A viewport that shows all four tiles of the page.
	v := bigView(t, pool, image.Pt(2100, 2100), tr)
	v.lay.SetZoom(2)
	c := paint.New(2100, 2100)
	v.Draw(c)
	dpi := dpiFor(v.lay.Scale())
	px := v.pixelSize(0, dpi)
	waitFor(t, "every tile to be cached", func() bool {
		for _, r := range parts(px) {
			if !pool.has(v, key{0, dpi, r}) {
				return false
			}
		}
		return true
	})
	v.Draw(c)
	vr := v.lay.ViewRect(0)
	for _, r := range parts(px) {
		want := colour(key{0, dpi, r})
		at := place(vr, r, px)
		// Every pixel of the tile, corners included, has its colour.
		for _, p := range []image.Point{at.Min, at.Max.Sub(image.Pt(1, 1)), at.Min.Add(at.Size().Div(2))} {
			if got := c.Img.RGBAAt(p.X, p.Y); got != want {
				t.Errorf("pixel %v of tile %v = %v, want %v", p, r, got, want)
			}
		}
	}
	// Neighbouring tiles share their edges: no pixel of the page is left as
	// paper or background.
	for y := vr.Min.Y; y < vr.Max.Y; y += 7 {
		for x := vr.Min.X; x < vr.Max.X; x += 7 {
			if got := c.Img.RGBAAt(x, y); got == paper || got.B != 200 {
				t.Fatalf("pixel (%d,%d) of the page is %v: a seam", x, y, got)
			}
		}
	}
}

func TestAPageWithNothingCachedIsPreviewedFirst(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1})
	t.Cleanup(pool.Close)
	tr := newTracker(nil)
	v, _ := sharedView(t, pool, 1, tr)
	v.Draw(paint.New(124, 400))
	waitIdle(t, pool)
	got := tr.started()
	if len(got) != 2 || got[0].dpi != previewDPI || got[1].dpi == previewDPI {
		t.Fatalf("renders = %+v, want the preview and then the page", got)
	}
	if got[0].rect.Dx() >= got[1].rect.Dx() {
		t.Errorf("the preview %v is not smaller than the page %v", got[0].rect, got[1].rect)
	}
	// With something cached for the page, a new zoom level needs no preview.
	v.ZoomIn()
	v.Draw(paint.New(124, 400))
	waitIdle(t, pool)
	if n := len(tr.started()); n != 3 {
		t.Errorf("%d renders after zooming, want 3: no new preview", n)
	}
}

func TestPartsPlacementAndSize(t *testing.T) {
	small := image.Pt(1000, 1000)
	if p := parts(small); len(p) != 1 || p[0] != image.Rect(0, 0, 1000, 1000) {
		t.Errorf("a small page is %v, want one whole part", p)
	}
	big := image.Pt(2500, 2100)
	p := parts(big)
	if len(p) != 3*3 {
		t.Fatalf("a large page has %d parts, want 9", len(p))
	}
	area := 0
	for _, r := range p {
		area += r.Dx() * r.Dy()
	}
	if area != big.X*big.Y || p[len(p)-1].Max != big {
		t.Errorf("parts cover %d pixels ending at %v, want %d ending at %v", area, p[len(p)-1].Max, big.X*big.Y, big)
	}
	// place scales parts in proportion, and neighbouring parts share edges.
	vr := image.Rect(10, 20, 10+300, 20+250)
	a, b := place(vr, p[0], big), place(vr, p[1], big)
	if a.Max.X != b.Min.X || a.Min != vr.Min {
		t.Errorf("neighbouring parts at %v and %v do not meet at the page's corner %v", a, b, vr.Min)
	}
	if last := place(vr, p[len(p)-1], big); last.Max != vr.Max {
		t.Errorf("the last part ends at %v, the page at %v", last.Max, vr.Max)
	}
}

func TestPruneKeepsWhatStillStandsIn(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1})
	t.Cleanup(pool.Close)
	v, _ := sharedView(t, pool, 2, newTracker(nil))
	img := func() *image.RGBA { return image.NewRGBA(image.Rect(0, 0, 2, 2)) }
	fill := func() {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		for _, dpi := range []int{72, 100, previewDPI, 144} {
			pool.store(v, testKey(0, dpi), img())
		}
		pool.store(v, testKey(1, 72), img())
	}
	dpis := func() map[int]bool {
		pool.mu.Lock()
		defer pool.mu.Unlock()
		out := map[int]bool{}
		for _, e := range pool.byPage[viewPage{v.id, 0}] {
			out[e.key.k.dpi] = true
		}
		return out
	}
	fill()
	// With parts at 144 still missing, prune keeps the newest other resolution,
	// 100, and the preview, and drops 72.
	pool.prune(v, 0, 144, true)
	if got := dpis(); len(got) != 3 || !got[144] || !got[previewDPI] || !got[100] {
		t.Errorf("after pruning with stand-ins: %v", got)
	}
	// With everything at 144 rendered, only it and the preview stay.
	fill()
	pool.prune(v, 0, 144, false)
	if got := dpis(); len(got) != 2 || !got[144] || !got[previewDPI] {
		t.Errorf("after pruning without stand-ins: %v", got)
	}
	// Other pages are not touched.
	if !pool.hasPage(v, 1) {
		t.Error("pruning one page dropped another")
	}
}

// manualClock is a clock the test moves, with timers that fire when it says.
type manualClock struct {
	t      time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	d       time.Duration
	f       func()
	stopped bool
}

func (t *fakeTimer) Stop() bool { t.stopped = true; return true }

func (c *manualClock) now() time.Time { return c.t }

func (c *manualClock) after(d time.Duration, f func()) stopper {
	t := &fakeTimer{d: d, f: f}
	c.timers = append(c.timers, t)
	return t
}

func (c *manualClock) last() *fakeTimer { return c.timers[len(c.timers)-1] }

func TestRapidZoomWaitsUntilItSettles(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1})
	t.Cleanup(pool.Close)
	tr := newTracker(nil)
	v, inv := sharedView(t, pool, 1, tr)
	clk := &manualClock{t: time.Unix(1000, 0)}
	v.now, v.after = clk.now, clk.after
	c := paint.New(124, 400)
	dpis := func() map[int]bool {
		out := map[int]bool{}
		for _, k := range tr.startedParts() {
			out[k.dpi] = true
		}
		return out
	}

	v.Draw(c)
	waitIdle(t, pool)
	first := dpiFor(v.lay.Scale())

	// A single step, long after the last, renders at once.
	clk.t = clk.t.Add(time.Second)
	v.ZoomIn()
	v.Draw(c)
	waitIdle(t, pool)
	second := dpiFor(v.lay.Scale())
	if !dpis()[second] || len(clk.timers) != 0 {
		t.Fatalf("a single zoom step was not rendered at once: %v, %d timers", dpis(), len(clk.timers))
	}

	// Steps in quick succession wait, showing what there is, until quiet.
	clk.t = clk.t.Add(50 * time.Millisecond)
	v.ZoomIn()
	v.Draw(c)
	third := dpiFor(v.lay.Scale())
	if len(clk.timers) != 1 || clk.last().d != settleDelay {
		t.Fatalf("timers after a rapid step: %d, wait %v", len(clk.timers), clk.last().d)
	}
	clk.t = clk.t.Add(30 * time.Millisecond)
	v.ZoomIn()
	v.Draw(c)
	fourth := dpiFor(v.lay.Scale())
	clk.t = clk.t.Add(10 * time.Millisecond)
	v.Draw(c) // still waiting, with the remaining time
	if !clk.timers[0].stopped || !clk.timers[1].stopped || clk.last().d != settleDelay-10*time.Millisecond {
		t.Errorf("timers not replaced as the wait went on: %+v", clk.timers)
	}
	waitIdle(t, pool)
	if dpis()[third] || dpis()[fourth] || inflightCount(v) != 0 {
		t.Fatalf("renders were started while zooming: %v", dpis())
	}

	// The timer invalidates the view; once the wait is over the next draw
	// renders the zoom the user stopped at, and only that.
	clk.last().f()
	waitInv(t, inv)
	clk.t = clk.t.Add(settleDelay)
	v.Draw(c)
	waitIdle(t, pool)
	if !dpis()[fourth] || dpis()[third] {
		t.Errorf("after the wait: rendered %v, want %d and not %d", dpis(), fourth, third)
	}
	_ = first
	// Closing the view cancels a pending timer.
	clk.t = clk.t.Add(time.Second)
	v.ZoomIn()
	clk.t = clk.t.Add(time.Millisecond)
	v.ZoomIn()
	v.Draw(c)
	pending := clk.last()
	v.Close()
	if !pending.stopped {
		t.Error("Close left the settle timer running")
	}
}

func TestStandInsDrawThePreviewUnderTheNewestResolution(t *testing.T) {
	pool := NewPool(PoolOptions{Workers: 1})
	t.Cleanup(pool.Close)
	v, _ := sharedView(t, pool, 1, newTracker(nil))
	img := func(n int) *image.RGBA { return image.NewRGBA(image.Rect(0, 0, n, n)) }
	preview, old, newer := img(1), img(2), img(3)
	pool.mu.Lock()
	pool.store(v, testKey(0, 72), old)
	pool.store(v, testKey(0, 144), newer)
	pool.store(v, testKey(0, previewDPI), preview)
	pool.mu.Unlock()
	got := pool.standIns(v, 0, 288)
	if len(got) != 2 || got[0].img != preview || got[1].img != newer {
		t.Errorf("stand-ins at 288 dpi = %v, want the preview then the 144 dpi bitmap", got)
	}
	// At the preview's own resolution it does not stand in for itself.
	if got := pool.standIns(v, 0, previewDPI); len(got) != 1 || got[0].img != newer {
		t.Errorf("stand-ins at the preview's dpi = %v, want only the newest other", got)
	}
}

func TestDefaultTimerFiresAndStops(t *testing.T) {
	fired := make(chan struct{}, 1)
	afterFunc(time.Millisecond, func() { fired <- struct{}{} })
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("the timer did not fire")
	}
	if tm := afterFunc(time.Hour, func() {}); !tm.Stop() {
		t.Error("a pending timer could not be stopped")
	}
}
