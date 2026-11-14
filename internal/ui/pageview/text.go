package pageview

import (
	"context"
	"image"
	"image/color"
	"math"
	"strings"
	"time"

	"github.com/voidrab/gopdfrab"

	"viewer/internal/ui/paint"
	"viewer/internal/ui/textlayer"
)

// pageData is the text and the links of one page.
type pageData struct {
	text  *textlayer.Text
	links []gopdfrab.Link
}

// caret is a place between characters: before character pos of a page's text.
type caret struct{ page, pos int }

func (a caret) before(b caret) bool {
	return a.page < b.page || a.page == b.page && a.pos < b.pos
}

// textState is the selection and the mouse press that may be growing it.
type textState struct {
	anchor, focus caret // the selection runs between them, in either order
	selecting     bool  // a press is dragging the focus
	pressed       *gopdfrab.Link
	pressAt       image.Point
	moved         bool
	clickAt       image.Point
	clickAtTime   time.Time
	clicks        int
}

// Interaction thresholds.
const (
	dragSlop   = 4                      // pixels the pointer may move and still be a click
	multiClick = 400 * time.Millisecond // the longest gap between clicks of a double click
	autoscroll = 30                     // the most a drag past the view's edge scrolls per move
	linkMargin = 6                      // pixels of the viewport edge a followed destination leaves above it
	allOfAPage = math.MaxInt            // a caret position that stands for the end of the text
)

// loaded returns the data of page i if it has been read.
func (v *View) loaded(i int) *pageData {
	v.dataMu.Lock()
	defer v.dataMu.Unlock()
	return v.datas[i]
}

// ensureData returns the data of page i, or nil while it is being read, which
// it starts if it has not been. The view is invalidated when it arrives.
func (v *View) ensureData(i int) *pageData {
	v.dataMu.Lock()
	defer v.dataMu.Unlock()
	if d := v.datas[i]; d != nil {
		return d
	}
	if !v.loading[i] {
		v.loading[i] = true
		v.loadWG.Add(1)
		go v.load(i)
	}
	return nil
}

func (v *View) load(i int) {
	defer v.loadWG.Done()
	d := v.read(v.loadCtx, i)
	v.dataMu.Lock()
	delete(v.loading, i)
	cancelled := v.loadCtx.Err() != nil
	if !cancelled {
		v.datas[i] = d
	}
	v.dataMu.Unlock()
	// A page with neither text nor links has nothing new to draw.
	if !cancelled && (len(d.text.Chars) > 0 || len(d.links) > 0) {
		v.invalidate()
	}
}

// read extracts the text and links of page i. A page that fails to give either
// has none of it.
func (v *View) read(ctx context.Context, i int) *pageData {
	glyphs, _ := v.glyphs(ctx, i)
	links, _ := v.links(ctx, i)
	return &pageData{text: textlayer.Build(glyphs), links: links}
}

// dataNow returns the data of page i, reading it now if it has not been.
func (v *View) dataNow(i int) *pageData {
	if d := v.loaded(i); d != nil {
		return d
	}
	d := v.read(context.Background(), i)
	v.dataMu.Lock()
	defer v.dataMu.Unlock()
	if v.datas[i] == nil { // a background read may have got there first
		v.datas[i] = d
	}
	return v.datas[i]
}

// scaleOf returns how many viewport pixels a point of page i takes, along x
// and y, which differ a little because pixel sizes are whole numbers.
func (v *View) scaleOf(i int) (sx, sy float64) {
	vr := v.lay.ViewRect(i)
	return float64(vr.Dx()) / v.sizes[i].W, float64(vr.Dy()) / v.sizes[i].H
}

// toPage returns the point of page i, in points, under viewport point p.
func (v *View) toPage(i int, p image.Point) (x, y float64) {
	vr := v.lay.ViewRect(i)
	sx, sy := v.scaleOf(i)
	return float64(p.X-vr.Min.X) / sx, float64(p.Y-vr.Min.Y) / sy
}

// pageAt returns the page under p, or false over the background.
func (v *View) pageAt(p image.Point) (int, bool) {
	first, last := v.lay.Visible()
	for i := first; i < last; i++ {
		if p.In(v.lay.ViewRect(i)) {
			return i, true
		}
	}
	return 0, false
}

// nearestPage returns the visible page nearest viewport point p vertically.
func (v *View) nearestPage(p image.Point) int {
	first, last := v.lay.Visible()
	best, bestDist := first, math.MaxInt
	for i := first; i < last; i++ {
		vr := v.lay.ViewRect(i)
		d := max(vr.Min.Y-p.Y, p.Y-vr.Max.Y, 0)
		if d < bestDist {
			best, bestDist = i, d
		}
	}
	return best
}

// linkAt returns the link of page i at (x, y), in points, if there is one.
func (d *pageData) linkAt(x, y float64) *gopdfrab.Link {
	for i := len(d.links) - 1; i >= 0; i-- {
		b := d.links[i].Box
		if x >= b.X0 && x <= b.X1 && y >= b.Y0 && y <= b.Y1 {
			return &d.links[i]
		}
	}
	return nil
}

// clickCount returns how many times in a row the pointer has been pressed at p,
// counting presses that are near each other in place and time.
func (v *View) clickCount(p image.Point) int {
	t := &v.text
	now := v.now()
	near := abs(p.X-t.clickAt.X) <= dragSlop && abs(p.Y-t.clickAt.Y) <= dragSlop
	if t.clicks > 0 && near && now.Sub(t.clickAtTime) < multiClick {
		t.clicks = t.clicks%3 + 1
	} else {
		t.clicks = 1
	}
	t.clickAt, t.clickAtTime = p, now
	return t.clicks
}

// pressText handles a left press over the pages that is not on a scrollbar. It
// starts a selection if the press is on text, remembers a link it is on to
// follow on release, and otherwise reports false so that the press drags the
// page. changed reports whether the view needs redrawing.
func (v *View) pressText(p image.Point) (handled, changed bool) {
	t := &v.text
	page, ok := v.pageAt(p)
	if !ok {
		changed = v.clearSelection()
		return false, changed
	}
	d := v.ensureData(page)
	if d == nil {
		return false, v.clearSelection()
	}
	x, y := v.toPage(page, p)
	count := v.clickCount(p)
	t.pressed, t.pressAt, t.moved = d.linkAt(x, y), p, false
	idx, onText := d.text.At(x, y)
	if !onText {
		changed = v.clearSelection()
		return false, changed
	}
	switch count {
	case 2:
		lo, hi := d.text.Word(idx)
		t.anchor, t.focus = caret{page, lo}, caret{page, hi}
	case 3:
		lo, hi := d.text.Line(idx)
		t.anchor, t.focus = caret{page, lo}, caret{page, hi}
	default:
		c := caret{page, d.text.Caret(x, y)}
		t.anchor, t.focus = c, c
	}
	t.selecting = true
	return true, true
}

// dragText moves the end of the selection to the pointer at p.
func (v *View) dragText(p image.Point) bool {
	t := &v.text
	if !t.moved && abs(p.X-t.pressAt.X) <= dragSlop && abs(p.Y-t.pressAt.Y) <= dragSlop {
		return false
	}
	t.moved, t.pressed = true, nil
	// Beyond the view's edge the page scrolls to follow.
	if vp := v.lay.Viewport(); p.Y < 0 {
		v.lay.ScrollBy(image.Pt(0, -min(-p.Y, autoscroll)))
	} else if p.Y > vp.Y {
		v.lay.ScrollBy(image.Pt(0, min(p.Y-vp.Y, autoscroll)))
	}
	page := v.nearestPage(p)
	if d := v.ensureData(page); d != nil {
		x, y := v.toPage(page, p)
		t.focus = caret{page, d.text.Caret(x, y)}
	}
	return true
}

// releaseText ends a selection drag, and follows the link the press began on if
// the pointer did not move.
func (v *View) releaseText() bool {
	t := &v.text
	was, link := t.selecting, t.pressed
	t.selecting, t.pressed = false, nil
	if link != nil && !t.moved {
		v.follow(*link)
		return true
	}
	return was
}

// follow goes where link goes: to a page of the document, or to an address.
func (v *View) follow(l gopdfrab.Link) {
	switch {
	case l.URI != "":
		if v.opts.OpenURI != nil {
			v.opts.OpenURI(l.URI)
		}
	case l.Page >= 0 && l.Page < len(v.pages):
		v.lay.GoTo(l.Page)
		_, sy := v.scaleOf(l.Page)
		v.lay.ScrollBy(image.Pt(0, int(l.Y*sy)-linkMargin))
	}
}

// HasSelection reports whether any text is selected.
func (v *View) HasSelection() bool { return v.text.anchor != v.text.focus }

// ClearSelection removes the selection, and reports whether there was one.
func (v *View) ClearSelection() bool { return v.clearSelection() }

func (v *View) clearSelection() bool {
	had := v.HasSelection()
	v.text.anchor, v.text.focus = caret{}, caret{}
	return had
}

// SelectAll selects the text of every page.
func (v *View) SelectAll() {
	v.text.anchor, v.text.focus = caret{}, caret{len(v.pages) - 1, allOfAPage}
}

// rangeOn returns the characters of page i, whose text has n, that are
// selected, or false if there are none.
func (t *textState) rangeOn(page, n int) (lo, hi int, ok bool) {
	a, b := t.anchor, t.focus
	if b.before(a) {
		a, b = b, a
	}
	if a == b || page < a.page || page > b.page {
		return 0, 0, false
	}
	lo, hi = 0, n
	if page == a.page {
		lo = min(a.pos, n)
	}
	if page == b.page {
		hi = min(b.pos, n)
	}
	return lo, hi, lo < hi
}

// SelectedText returns the selected text, with the text of different pages
// separated by a blank line. It reads the text of any page of the selection
// that has not been read, which can take a moment for a long selection.
func (v *View) SelectedText() string {
	a, b := v.text.anchor, v.text.focus
	if b.before(a) {
		a, b = b, a
	}
	if a == b {
		return ""
	}
	var parts []string
	for page := a.page; page <= b.page && page < len(v.pages); page++ {
		text := v.dataNow(page).text
		if lo, hi, ok := v.text.rangeOn(page, len(text.Chars)); ok {
			parts = append(parts, strings.TrimRight(text.Slice(lo, hi), "\n"))
		}
	}
	return strings.Join(parts, "\n\n")
}

// highlight paints rects, in points of page i, over the page in c, whose view
// has its top-left corner at origin.
func (v *View) highlight(c *paint.Canvas, origin image.Point, i int, rects []textlayer.Rect, col color.NRGBA) {
	vr := v.lay.ViewRect(i).Add(origin)
	sx, sy := v.scaleOf(i)
	for _, r := range rects {
		c.Fill(image.Rect(
			vr.Min.X+int(math.Floor(r.X0*sx)), vr.Min.Y+int(math.Floor(r.Y0*sy)),
			vr.Min.X+int(math.Ceil(r.X1*sx)), vr.Min.Y+int(math.Ceil(r.Y1*sy)),
		), col)
	}
}

// drawText paints the selection over the visible pages whose text is read.
func (v *View) drawText(c *paint.Canvas, origin image.Point) {
	first, last := v.lay.Visible()
	for i := first; i < last; i++ {
		d := v.ensureData(i)
		if d == nil {
			continue
		}
		if lo, hi, ok := v.text.rangeOn(i, len(d.text.Chars)); ok {
			v.highlight(c, origin, i, d.text.Rects(lo, hi), v.opts.Selection)
		}
	}
}
