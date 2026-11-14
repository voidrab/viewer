package pageview

import (
	"context"
	"image"
	"sort"
	"strings"

	"viewer/internal/ui/paint"
	"viewer/internal/ui/textlayer"
)

// findState is a search in progress or done. The pages are searched on a
// background goroutine, so everything in it is guarded by View.dataMu; the
// goroutine only marks that the first match should be shown, and the view
// does it on its own goroutine, where the layout may be changed.
type findState struct {
	cancel   context.CancelFunc
	byPage   map[int][]textlayer.Match // the matches of each page searched so far
	searched int                       // how many pages have been searched
	cur      pageMatch                 // the current match, valid when has is set
	has      bool
	reveal   bool // the current match is to be scrolled into view
}

// pageMatch is a match, on a page.
type pageMatch struct{ page, index int }

// Find searches the document for query, ignoring case, and highlights every
// match. The first one from the top of the view on is made current and scrolled
// to. Matches are found page by page in the background; FindStatus reports its
// progress. An empty query ends the search.
func (v *View) Find(query string) {
	v.ClearFind()
	if strings.TrimSpace(query) == "" {
		return
	}
	ctx, cancel := context.WithCancel(v.loadCtx)
	f := &findState{cancel: cancel, byPage: map[int][]textlayer.Match{}}
	v.dataMu.Lock()
	v.find = f
	v.dataMu.Unlock()
	start, _ := v.lay.Visible()
	v.loadWG.Add(1)
	go v.search(ctx, f, query, start)
}

// search looks for query in every page, from page start to the end and then
// from the first page up to it. It records its results in f, the state of this
// search. A search replaced by a later one keeps writing to its own f, which
// nothing reads.
func (v *View) search(ctx context.Context, f *findState, query string, start int) {
	defer v.loadWG.Done()
	n := len(v.pages)
	for k := 0; k < n; k++ {
		i := (start + k) % n
		d := v.dataCtx(ctx, i)
		if d == nil {
			return // cancelled
		}
		matches := d.text.Find(query)
		v.dataMu.Lock()
		f.searched++
		if len(matches) > 0 {
			f.byPage[i] = matches
			if !f.has {
				f.cur, f.has, f.reveal = pageMatch{i, 0}, true, true
			}
		}
		v.dataMu.Unlock()
		if len(matches) > 0 || k == n-1 {
			v.invalidate()
		}
	}
}

// dataCtx is dataNow that gives up if ctx is done, returning nil.
func (v *View) dataCtx(ctx context.Context, i int) *pageData {
	if d := v.loaded(i); d != nil {
		return d
	}
	d := v.read(ctx, i)
	if ctx.Err() != nil {
		return nil
	}
	v.dataMu.Lock()
	defer v.dataMu.Unlock()
	if v.datas[i] == nil {
		v.datas[i] = d
	}
	return v.datas[i]
}

// ClearFind ends the search and removes its highlights.
func (v *View) ClearFind() {
	v.dataMu.Lock()
	defer v.dataMu.Unlock()
	if v.find != nil {
		v.find.cancel()
	}
	v.find = nil
}

// FindStatus reports the search: which match is current, counting from 1 or 0
// if none, how many were found, and whether pages are still being searched.
func (v *View) FindStatus() (current, total int, searching bool) {
	v.dataMu.Lock()
	defer v.dataMu.Unlock()
	f := v.find
	if f == nil {
		return 0, 0, false
	}
	for _, p := range f.pages() {
		if f.has && p < f.cur.page {
			current += len(f.byPage[p])
		}
		total += len(f.byPage[p])
	}
	if f.has {
		current += f.cur.index + 1
	}
	return current, total, f.searched < len(v.pages)
}

// pages returns the pages with matches, in order. The caller holds v.dataMu.
func (f *findState) pages() []int {
	pages := make([]int, 0, len(f.byPage))
	for p := range f.byPage {
		pages = append(pages, p)
	}
	sort.Ints(pages)
	return pages
}

// FindNext makes the next match current, wrapping from the last to the first,
// and scrolls to it.
func (v *View) FindNext() { v.stepFind(1) }

// FindPrev makes the previous match current.
func (v *View) FindPrev() { v.stepFind(-1) }

func (v *View) stepFind(dir int) {
	v.dataMu.Lock()
	f := v.find
	var all []pageMatch
	if f != nil {
		for _, p := range f.pages() {
			for i := range f.byPage[p] {
				all = append(all, pageMatch{p, i})
			}
		}
	}
	if len(all) == 0 {
		v.dataMu.Unlock()
		return
	}
	at := 0
	for i, m := range all {
		if f.has && m == f.cur {
			at = i
		}
	}
	if f.has {
		at = (at + dir + len(all)) % len(all)
	}
	f.cur, f.has, f.reveal = all[at], true, true
	v.dataMu.Unlock()
	v.revealFind()
	v.invalidate()
}

// revealFind scrolls to the current match if it has not been shown yet.
func (v *View) revealFind() {
	v.dataMu.Lock()
	f := v.find
	if f == nil || !f.reveal || !f.has {
		v.dataMu.Unlock()
		return
	}
	f.reveal = false
	m := f.byPage[f.cur.page][f.cur.index]
	d := v.datas[f.cur.page]
	page := f.cur.page
	v.dataMu.Unlock()
	// A match holds at least one character the page shows, so it has a rectangle.
	rects := d.text.Rects(m.Start, m.End)
	sx, sy := v.scaleOf(page)
	pr := v.lay.PageRect(page)
	vp := v.lay.Viewport()
	v.lay.ScrollTo(image.Pt(pr.Min.X+int(rects[0].X0*sx)-vp.X/2, pr.Min.Y+int(rects[0].Y0*sy)-vp.Y/3))
}

// drawFind paints the matches on page i, whose text is d, with the current one
// in its own colour, over the page in c whose view has its top-left corner at
// origin.
func (v *View) drawFind(c *paint.Canvas, origin image.Point, i int, d *pageData) {
	v.dataMu.Lock()
	var matches []textlayer.Match
	var cur pageMatch
	var has bool
	if f := v.find; f != nil {
		matches, cur, has = f.byPage[i], f.cur, f.has
	}
	v.dataMu.Unlock()
	for k, m := range matches {
		col := v.opts.Match
		if has && cur == (pageMatch{i, k}) {
			col = v.opts.CurrentMatch
		}
		v.highlight(c, origin, i, d.text.Rects(m.Start, m.End), col)
	}
}
