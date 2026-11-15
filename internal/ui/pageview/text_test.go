package pageview

import (
	"context"
	"errors"
	"image"
	"image/color"
	"strings"
	"testing"
	"time"

	"github.com/voidrab/gopdfrab"

	"viewrab/internal/ui/paint"
	"viewrab/internal/ui/platform"
)

// line lays out s as glyphs 6 wide and 12 tall from (x, y) points; a space
// leaves a gap and shows no glyph.
func line(x, y float64, s string) []gopdfrab.Glyph {
	var out []gopdfrab.Glyph
	for _, r := range s {
		if r != ' ' {
			out = append(out, gopdfrab.Glyph{Rune: r, Box: gopdfrab.Rect{X0: x, Y0: y, X1: x + 6, Y1: y + 12}})
		}
		x += 6
	}
	return out
}

// textView is a view of n 100x100 pages, each with the text
// "Hello world / Second line" and the links in links, in a 124x300 viewport,
// where a point of a page is a pixel. Its clock is manual.
func textView(t *testing.T, n int, links []gopdfrab.Link) (*View, *manualClock, chan struct{}) {
	t.Helper()
	v, inv := newView(t, openDoc(t, n), image.Pt(124, 300), Options{})
	v.glyphs = func(context.Context, int) ([]gopdfrab.Glyph, error) {
		return append(line(10, 10, "Hello world"), line(10, 30, "Second line")...), nil
	}
	v.links = func(context.Context, int) ([]gopdfrab.Link, error) { return links, nil }
	clk := &manualClock{t: time.Unix(1000, 0)}
	v.now, v.after = clk.now, clk.after
	return v, clk, inv
}

// at returns the viewport pixel of point (x, y) of page i.
func at(v *View, i int, x, y float64) image.Point {
	r := v.lay.ViewRect(i)
	return image.Pt(r.Min.X+int(x), r.Min.Y+int(y))
}

// ready draws until the text of page i has been read.
func ready(t *testing.T, v *View, c *paint.Canvas, i int) {
	t.Helper()
	waitFor(t, "the text to be read", func() bool {
		v.Draw(c)
		return v.loaded(i) != nil
	})
}

func press(v *View, p image.Point)   { v.HandlePointer(ptr(platform.Press, platform.Left, p.X, p.Y)) }
func release(v *View, p image.Point) { v.HandlePointer(ptr(platform.Release, platform.Left, p.X, p.Y)) }
func move(v *View, p image.Point)    { v.HandlePointer(ptr(platform.Move, platform.None, p.X, p.Y)) }

func TestDraggingOverTextSelectsIt(t *testing.T) {
	v, _, _ := textView(t, 1, nil)
	c := paint.New(124, 300)
	ready(t, v, c, 0)
	start := at(v, 0, 11, 16) // on the H
	press(v, start)
	if !v.text.selecting {
		t.Fatal("pressing on text did not start a selection")
	}
	move(v, at(v, 0, 11, 16)) // within the slop: nothing yet
	move(v, at(v, 0, 68, 16)) // in the right part of the l of world
	if got := v.SelectedText(); got != "Hello worl" {
		t.Errorf("selected %q, want %q", got, "Hello worl")
	}
	move(v, at(v, 0, 90, 40)) // below and right: into the second line
	if got := v.SelectedText(); got != "Hello world\nSecond line" {
		t.Errorf("selected %q", got)
	}
	release(v, at(v, 0, 90, 40))
	if v.text.selecting || !v.HasSelection() {
		t.Error("releasing ended the selection too")
	}
	// It is drawn over the page, and a click elsewhere removes it.
	cv := paint.New(124, 300)
	v.Draw(cv)
	px := cv.Img.RGBAAt(at(v, 0, 20, 16).X, at(v, 0, 20, 16).Y)
	if px == paper {
		t.Errorf("the selected text is not highlighted: %v", px)
	}
	press(v, at(v, 0, 90, 90)) // empty paper
	release(v, at(v, 0, 90, 90))
	if v.HasSelection() {
		t.Error("clicking on empty paper kept the selection")
	}
}

func TestDragSelectsBackwardsAndAcrossLines(t *testing.T) {
	v, _, _ := textView(t, 1, nil)
	c := paint.New(124, 300)
	ready(t, v, c, 0)
	press(v, at(v, 0, 70, 36)) // in "Second", the end of "line"
	move(v, at(v, 0, 11, 16))  // up to the H
	if got := v.SelectedText(); !strings.HasPrefix(got, "Hello world\nSecond") {
		t.Errorf("selected %q", got)
	}
	release(v, at(v, 0, 11, 16))
}

func TestDoubleAndTripleClickSelectAWordAndALine(t *testing.T) {
	v, clk, _ := textView(t, 1, nil)
	c := paint.New(124, 300)
	ready(t, v, c, 0)
	p := at(v, 0, 70, 16) // in "world"
	for i, want := range []string{"", "world", "Hello world"} {
		press(v, p)
		release(v, p)
		if got := v.SelectedText(); got != want {
			t.Errorf("after %d clicks: %q, want %q", i+1, got, want)
		}
		clk.t = clk.t.Add(100 * time.Millisecond)
	}
	// A fourth click starts over, and so does one that comes late or far.
	press(v, p)
	release(v, p)
	if v.text.clicks != 1 {
		t.Errorf("click count after four clicks = %d, want 1", v.text.clicks)
	}
	clk.t = clk.t.Add(time.Second)
	press(v, p)
	release(v, p)
	press(v, at(v, 0, 12, 16))
	release(v, at(v, 0, 12, 16))
	if v.text.clicks != 1 {
		t.Errorf("a click far from the last counted as a repeat: %d", v.text.clicks)
	}
}

func TestSelectAllEscapeAndCopyAcrossPages(t *testing.T) {
	v, _, _ := textView(t, 3, nil)
	if v.HasSelection() || v.SelectedText() != "" {
		t.Error("a new view has a selection")
	}
	if v.HandleEvent(platform.KeyEvent{Down: true, Key: platform.KeyEscape}) {
		t.Error("Escape without a selection was handled")
	}
	if !v.HandleEvent(platform.KeyEvent{Down: true, Rune: 'a', Mods: platform.Ctrl}) || !v.HasSelection() {
		t.Fatal("Ctrl+A did not select")
	}
	page := "Hello world\nSecond line"
	if got, want := v.SelectedText(), page+"\n\n"+page+"\n\n"+page; got != want {
		t.Errorf("selected %q, want %q", got, want)
	}
	// Drawn on every visible page.
	c := paint.New(124, 300)
	ready(t, v, c, 0)
	v.Draw(c)
	if p := at(v, 0, 20, 16); c.Img.RGBAAt(p.X, p.Y) == paper {
		t.Error("select-all left the first page unhighlighted")
	}
	if !v.HandleEvent(platform.KeyEvent{Down: true, Key: platform.KeyEscape}) || v.HasSelection() {
		t.Error("Escape did not clear the selection")
	}
}

func TestClickingALinkFollowsIt(t *testing.T) {
	var opened []string
	v, _, _ := textView(t, 6, []gopdfrab.Link{
		{Box: gopdfrab.Rect{X0: 10, Y0: 10, X1: 40, Y1: 22}, URI: "https://example.com", Page: -1}, // over "Hello"
		{Box: gopdfrab.Rect{X0: 5, Y0: 60, X1: 50, Y1: 80}, Page: 2, Y: 30},                        // over no text
	})
	v.opts.OpenURI = func(u string) { opened = append(opened, u) }
	c := paint.New(124, 300)
	ready(t, v, c, 0)

	p := at(v, 0, 20, 16)
	press(v, p)
	release(v, p)
	if len(opened) != 1 || opened[0] != "https://example.com" {
		t.Fatalf("opened %v", opened)
	}
	// Dragging from the link is a selection, not a click.
	press(v, p)
	move(v, at(v, 0, 60, 16))
	release(v, at(v, 0, 60, 16))
	if len(opened) != 1 {
		t.Errorf("a drag from a link followed it: %v", opened)
	}
	// A link over no text goes to its page, scrolling to the position it names.
	q := at(v, 0, 20, 70)
	press(v, q)
	release(v, q)
	// The follow scrolls by the destination's 30 points less linkMargin, so
	// page 2's top ends 30-linkMargin pixels above where GoTo left it.
	if got := v.lay.ViewRect(2).Min.Y; got != 12-(30-linkMargin) {
		t.Errorf("page 2 starts at %d, want %d", got, 12-(30-linkMargin))
	}
	// Dragging the page from a link over no text pans it and follows nothing.
	v.lay.GoTo(0)
	ready(t, v, c, 0)
	press(v, q)
	move(v, q.Add(image.Pt(0, -30)))
	release(v, q.Add(image.Pt(0, -30)))
	if v.lay.Scroll().Y != 30 || v.lay.Scroll().X != 0 {
		t.Errorf("dragging from the link did not pan by 30: scroll %v", v.lay.Scroll())
	}
	// No opener, and a destination off the end, do nothing.
	v.opts.OpenURI = nil
	v.follow(gopdfrab.Link{URI: "https://x.org", Page: -1})
	v.follow(gopdfrab.Link{Page: 99})
	v.follow(gopdfrab.Link{Page: -1})
}

func TestPressBeforeTheTextIsReadPansInstead(t *testing.T) {
	v, _, _ := textView(t, 1, nil)
	gate := make(chan struct{})
	v.glyphs = func(ctx context.Context, i int) ([]gopdfrab.Glyph, error) {
		<-gate
		return line(10, 10, "Hello"), nil
	}
	press(v, at(v, 0, 11, 16))
	if !v.ptr.pan || v.text.selecting {
		t.Error("a press before the text was read did not pan")
	}
	release(v, at(v, 0, 11, 16))
	close(gate)
}

func TestMiddleButtonPansAnywhere(t *testing.T) {
	v, _, _ := textView(t, 3, nil)
	c := paint.New(124, 300)
	ready(t, v, c, 0)
	p := at(v, 0, 11, 16) // over text, where the left button would select
	v.HandlePointer(ptr(platform.Press, platform.Middle, p.X, p.Y))
	move(v, p.Add(image.Pt(0, -40)))
	if v.lay.Scroll().Y != 40 || v.text.selecting {
		t.Errorf("scroll %v, selecting %v", v.lay.Scroll(), v.text.selecting)
	}
	v.HandlePointer(ptr(platform.Release, platform.Middle, p.X, p.Y))
	if v.ptr.pan {
		t.Error("releasing the middle button did not end the pan")
	}
}

func TestDraggingPastTheEdgeScrollsAndSelectsTheNearestPage(t *testing.T) {
	v, _, _ := textView(t, 4, nil)
	c := paint.New(124, 300)
	ready(t, v, c, 0)
	for i := range 4 {
		v.dataNow(i) // so that the pages the drag reaches have their text
	}
	press(v, at(v, 0, 11, 16))
	move(v, image.Pt(60, 500)) // far below the view
	if v.lay.Scroll().Y == 0 {
		t.Error("dragging below the view did not scroll")
	}
	v.Draw(c)
	if v.text.focus.page == 0 {
		t.Errorf("the selection did not reach the next page: %+v", v.text.focus)
	}
	back := v.lay.Scroll().Y
	move(v, image.Pt(60, -50)) // above the view: scrolls up
	if v.lay.Scroll().Y >= back {
		t.Error("dragging above the view did not scroll up")
	}
	release(v, image.Pt(60, -50))
	// A press in the gap between pages starts nothing.
	v.lay.ScrollTo(image.Point{})
	if _, ok := v.pageAt(image.Pt(60, 5)); ok {
		t.Error("the margin above the first page is part of a page")
	}
	press(v, image.Pt(60, 5))
	if v.text.selecting || !v.ptr.pan {
		t.Error("a press on the background did not pan")
	}
	release(v, image.Pt(60, 5))
}

func TestPagesThatFailToGiveTextHaveNone(t *testing.T) {
	v, _, _ := textView(t, 1, nil)
	boom := errors.New("boom")
	v.glyphs = func(context.Context, int) ([]gopdfrab.Glyph, error) { return nil, boom }
	v.links = func(context.Context, int) ([]gopdfrab.Link, error) { return nil, boom }
	c := paint.New(124, 300)
	ready(t, v, c, 0)
	press(v, at(v, 0, 11, 16))
	if v.text.selecting || v.SelectedText() != "" {
		t.Error("a page without text was selectable")
	}
	release(v, at(v, 0, 11, 16))
}

func TestClosingCancelsReadingText(t *testing.T) {
	v, _, inv := textView(t, 2, nil)
	started := make(chan struct{}, 4)
	// No page render may finish and invalidate the view either.
	v.render = func(ctx context.Context, _ *gopdfrab.Page, _ float64, _ image.Rectangle) (*image.RGBA, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	v.glyphs = func(ctx context.Context, i int) ([]gopdfrab.Glyph, error) {
		started <- struct{}{}
		<-ctx.Done()
		return line(10, 10, "late"), nil
	}
	v.Draw(paint.New(124, 300))
	<-started
	v.Close() // returns although a read is under way: it was cancelled
	if v.loaded(0) != nil {
		t.Error("the text of a cancelled read was kept")
	}
	select {
	case <-inv:
		t.Error("a cancelled read invalidated the view")
	default:
	}
}

func TestSelectedTextReadsPagesNotYetLoaded(t *testing.T) {
	v, _, _ := textView(t, 3, nil)
	v.text.anchor, v.text.focus = caret{0, 6}, caret{1, 5}
	got := v.SelectedText()
	if !strings.HasPrefix(got, "world\nSecond line\n\nHello") || !strings.HasSuffix(got, "Hello") {
		t.Errorf("selected %q", got)
	}
	// Reversed, with the focus first, gives the same.
	v.text.anchor, v.text.focus = caret{1, 5}, caret{0, 6}
	if v.SelectedText() != got {
		t.Error("a backwards selection reads differently")
	}
	// A selection that ends past the last page stops there.
	v.text.focus = caret{9, 0}
	if v.SelectedText() == "" {
		t.Error("a selection to a page past the end read as empty")
	}
}

func TestSelectionColourIsConfigurable(t *testing.T) {
	v, _, _ := textView(t, 1, nil)
	v.opts.Selection = color.NRGBA{255, 0, 0, 255}
	c := paint.New(124, 300)
	ready(t, v, c, 0)
	v.SelectAll()
	v.Draw(c)
	p := at(v, 0, 20, 16)
	if got := c.Img.RGBAAt(p.X, p.Y); got.R < 200 || got.G > 60 {
		t.Errorf("highlight = %v, want red", got)
	}
}

func TestHoveringALinkHighlightsIt(t *testing.T) {
	v, _, _ := textView(t, 1, []gopdfrab.Link{
		{Box: gopdfrab.Rect{X0: 10, Y0: 60, X1: 40, Y1: 80}, URI: "https://example.com", Page: -1},
	})
	c := paint.New(124, 300)
	ready(t, v, c, 0)
	in, out := at(v, 0, 20, 70), at(v, 0, 70, 90)
	px := func() color.NRGBA {
		v.Draw(c)
		return color.NRGBAModel.Convert(c.Img.RGBAAt(in.X, in.Y)).(color.NRGBA)
	}
	plain := px()

	if v.OverLink() {
		t.Fatal("over a link before the pointer moved")
	}
	if !v.HandlePointer(ptr(platform.Move, platform.None, in.X, in.Y)) || !v.OverLink() {
		t.Fatal("moving onto the link did not hover it")
	}
	hover := px()
	if hover == plain {
		t.Error("the hovered link is not highlighted")
	}
	if !v.HandlePointer(ptr(platform.Move, platform.None, out.X, out.Y)) || v.OverLink() {
		t.Error("moving off the link did not end the hover")
	}
	if px() != plain {
		t.Error("the highlight stayed after the pointer left")
	}

	// Following the link leaves nothing behind once the pointer moves off it.
	move(v, in)
	press(v, in)
	release(v, in)
	move(v, out)
	if px() != plain {
		t.Error("a followed link stays coloured")
	}
	move(v, in)
	if !v.HandlePointer(platform.PointerEvent{Kind: platform.Leave}) {
		t.Error("leaving the window over a link did not ask for a redraw")
	}
	if v.OverLink() {
		t.Error("still over a link after the pointer left")
	}
}
