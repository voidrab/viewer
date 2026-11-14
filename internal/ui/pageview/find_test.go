package pageview

import (
	"context"
	"image"
	"image/color"
	"testing"

	"github.com/voidrab/gopdfrab"

	"viewer/internal/ui/paint"
)

// found waits for the search to finish and returns its status.
func found(t *testing.T, v *View) (current, total int) {
	t.Helper()
	var searching bool
	waitFor(t, "the search to finish", func() bool {
		current, total, searching = v.FindStatus()
		return !searching
	})
	return current, total
}

func TestFindHighlightsEveryMatchAndScrollsToTheFirst(t *testing.T) {
	v, _, inv := textView(t, 3, nil)
	c := paint.New(124, 300)
	v.Find("hello")
	if cur, total := found(t, v); cur != 1 || total != 3 {
		t.Fatalf("match %d of %d, want 1 of 3", cur, total)
	}
	waitInv(t, inv)
	v.Draw(c) // reveals the first match
	// The match, "Hello" at (10, 10) on page 0, is inside the view after
	// the first draw.
	vr := v.lay.ViewRect(0)
	if y := vr.Min.Y + 10; y < 0 || y > 300 {
		t.Fatalf("the first match is at y %d, outside the view", y)
	}
	v.Draw(c)
	cur := at(v, 0, 12, 16)
	other := at(v, 1, 12, 16)
	curPx := c.Img.RGBAAt(cur.X, cur.Y)
	if curPx == paper {
		t.Errorf("the current match is not highlighted: %v", curPx)
	}
	// Match two is on page 1, in the same place, in the plain match colour.
	v.lay.ScrollTo(image.Pt(0, v.lay.PageRect(1).Min.Y-12))
	v.Draw(c)
	other = at(v, 1, 12, 16)
	plain := c.Img.RGBAAt(other.X, other.Y)
	if plain == paper || plain == curPx {
		t.Errorf("a match that is not current is %v, current was %v", plain, curPx)
	}
	// Text that is not a match is untouched: it shows the page as it is.
	rest := at(v, 1, 80, 16)
	if got := c.Img.RGBAAt(rest.X, rest.Y); got != paper && got != red {
		t.Errorf("text outside any match was highlighted: %v", got)
	}
}

func TestFindNextAndPrevWrapAroundTheDocument(t *testing.T) {
	v, _, _ := textView(t, 6, nil)
	v.GoToPage(1) // the search starts here
	if first, _ := v.lay.Visible(); first != 1 {
		t.Fatalf("the first visible page is %d", first)
	}
	v.Find("world")
	if cur, total := found(t, v); cur != 2 || total != 6 {
		t.Fatalf("from page 1: match %d of %d, want 2 of 6", cur, total)
	}
	v.FindNext()
	if cur, _, _ := v.FindStatus(); cur != 3 {
		t.Errorf("next = %d, want 3", cur)
	}
	v.FindPrev()
	v.FindPrev()
	if cur, _, _ := v.FindStatus(); cur != 1 {
		t.Errorf("previous twice = %d, want 1", cur)
	}
	v.FindPrev()
	if cur, _, _ := v.FindStatus(); cur != 6 {
		t.Errorf("previous before the first = %d, want 6 (wrapped)", cur)
	}
	v.FindNext()
	if cur, _, _ := v.FindStatus(); cur != 1 {
		t.Errorf("next past the last = %d, want 1 (wrapped)", cur)
	}
	// Going to a match scrolls it into view.
	v.FindPrev()
	v.Draw(paint.New(124, 300))
	if y := v.lay.ViewRect(5).Min.Y; y > 300 || y+100 < 0 {
		t.Errorf("page 5 is at y %d after going to its match", y)
	}
	// Several matches on one page are stepped through in order.
	v.Find("l")
	found(t, v)
	var seen []int
	for range 6 {
		v.FindNext()
		cur, total, _ := v.FindStatus()
		seen = append(seen, cur)
		if total != 24 {
			t.Fatalf("%d matches of l, want 24", total)
		}
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] != seen[i-1]%24+1 {
			t.Errorf("steps went %v, want consecutive", seen)
		}
	}
}

func TestANewFindReplacesTheOldOne(t *testing.T) {
	v, _, _ := textView(t, 3, nil)
	v.Find("hello")
	v.Find("l") // four to a page
	if _, total := found(t, v); total != 12 {
		t.Errorf("total %d, want 12 for the second query only", total)
	}
	for _, q := range []string{"", "   ", "\t\n"} {
		v.Find(q)
		if cur, total, searching := v.FindStatus(); cur != 0 || total != 0 || searching {
			t.Errorf("Find(%q): %d of %d, searching %v", q, cur, total, searching)
		}
	}
	v.Find("hello")
	found(t, v)
	v.ClearFind()
	if cur, total, _ := v.FindStatus(); cur != 0 || total != 0 {
		t.Errorf("after ClearFind: %d of %d", cur, total)
	}
	v.FindNext() // nothing to step through
	v.FindPrev()
	c := paint.New(124, 300)
	ready(t, v, c, 0)
	p := at(v, 0, 12, 16)
	if c.Img.RGBAAt(p.X, p.Y) != paper {
		t.Error("highlights remain after ClearFind")
	}
}

func TestNoMatchesAndFailingPages(t *testing.T) {
	v, _, inv := textView(t, 2, nil)
	v.glyphs = func(context.Context, int) ([]gopdfrab.Glyph, error) { return nil, context.DeadlineExceeded }
	v.Find("anything")
	if cur, total := found(t, v); cur != 0 || total != 0 {
		t.Errorf("%d of %d on pages without text", cur, total)
	}
	waitInv(t, inv) // the end of a search is announced even with no match
	v.FindNext()
}

func TestClosingCancelsASearch(t *testing.T) {
	v, _, _ := textView(t, 3, nil)
	started := make(chan struct{}, 4)
	v.glyphs = func(ctx context.Context, i int) ([]gopdfrab.Glyph, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	v.Find("x")
	<-started
	v.Close() // returns although the search is under way
	if _, _, searching := v.FindStatus(); searching {
		t.Error("a closed view is still searching")
	}
}

func TestFindColoursAreConfigurable(t *testing.T) {
	v, _, _ := textView(t, 1, nil)
	v.opts.CurrentMatch = color.NRGBA{0, 0, 255, 255}
	v.Find("hello")
	found(t, v)
	c := paint.New(124, 300)
	v.Draw(c)
	p := at(v, 0, 12, 16)
	if got := c.Img.RGBAAt(p.X, p.Y); got.B < 200 || got.R > 60 {
		t.Errorf("current match = %v, want blue", got)
	}
}
