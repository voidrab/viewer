// Package textlayer turns the glyphs of a page into text that can be selected,
// copied and searched: lines in reading order, with the spaces and line breaks
// the page leaves out. It is geometry and string handling only.
package textlayer

import (
	"strings"
	"unicode"

	"github.com/voidrab/gopdfrab"
)

// Rect is a rectangle in points from the top-left corner of the displayed page.
type Rect struct{ X0, Y0, X1, Y1 float64 }

// glyph is a character with its box.
type glyph struct {
	r   rune
	box Rect
}

// Char is one character of a page's text, or a space or line break the layer
// added where the page shows none.
type Char struct {
	R    rune
	Box  Rect // for an added space the gap it stands for, for a line break the end of the line
	Line int  // the index of its line
	Real bool // the page shows it, as opposed to the layer adding it
}

// Text is the text of one page.
type Text struct {
	Chars []Char
	lines []span // the character range and bounds of each line
}

// span is a line: the characters [start, end), without its line break, and the
// rectangle that holds them.
type span struct {
	start, end int
	box        Rect
}

// Tuning of how glyphs are grouped, as fractions of a line's height.
const (
	sameLine  = 0.5  // the share of the shorter box that must overlap vertically
	backstep  = 0.5  // how far left a glyph may start and still continue its line
	wordSpace = 0.2  // a gap wider than this is a space
	slop      = 0.15 // how far outside a glyph a point still hits it
)

// Build makes the text of a page from its glyphs, in the order the page shows
// them. Glyphs with no character are left out. A glyph goes on the current line
// if it overlaps it vertically and does not start well to its left, and
// otherwise starts a new line.
func Build(glyphs []gopdfrab.Glyph) *Text {
	t := &Text{}
	var line []glyph
	flush := func() {
		if len(line) > 0 {
			t.addLine(line)
			line = line[:0]
		}
	}
	var bounds Rect
	for _, in := range glyphs {
		if in.Rune == 0 || unicode.IsControl(in.Rune) {
			continue
		}
		g := glyph{in.Rune, Rect(in.Box)}
		if len(line) > 0 {
			last := line[len(line)-1].box
			h := bounds.Y1 - bounds.Y0
			if overlap(bounds, g.box) < sameLine*min(h, g.box.Y1-g.box.Y0) || g.box.X0 < last.X0-backstep*h {
				flush()
			}
		}
		if len(line) == 0 {
			bounds = g.box
		} else {
			bounds = union(bounds, g.box)
		}
		line = append(line, g)
	}
	flush()
	return t
}

// overlap returns how far the vertical extents of a and b overlap.
func overlap(a, b Rect) float64 { return min(a.Y1, b.Y1) - max(a.Y0, b.Y0) }

func union(a, b Rect) Rect {
	return Rect{min(a.X0, b.X0), min(a.Y0, b.Y0), max(a.X1, b.X1), max(a.Y1, b.Y1)}
}

// addLine appends a line of glyphs, with a space where two of them are apart,
// after the line break that ends the line before it.
func (t *Text) addLine(glyphs []glyph) {
	index := len(t.lines)
	box := glyphs[0].box
	for _, g := range glyphs[1:] {
		box = union(box, g.box)
	}
	if index > 0 {
		prev := t.lines[index-1].box
		t.Chars = append(t.Chars, Char{R: '\n', Box: Rect{prev.X1, prev.Y0, prev.X1, prev.Y1}, Line: index - 1})
	}
	start := len(t.Chars)
	h := box.Y1 - box.Y0
	for i, g := range glyphs {
		if i > 0 {
			prev := glyphs[i-1]
			if gap := g.box.X0 - prev.box.X1; gap > wordSpace*h && !unicode.IsSpace(prev.r) && !unicode.IsSpace(g.r) {
				t.Chars = append(t.Chars, Char{R: ' ', Box: Rect{prev.box.X1, box.Y0, g.box.X0, box.Y1}, Line: index})
			}
		}
		t.Chars = append(t.Chars, Char{R: g.r, Box: g.box, Line: index, Real: true})
	}
	t.lines = append(t.lines, span{start, len(t.Chars), box})
}

// String returns all of the text.
func (t *Text) String() string { return t.Slice(0, len(t.Chars)) }

// Slice returns the text of the characters [lo, hi), which are clamped to the
// text.
func (t *Text) Slice(lo, hi int) string {
	lo, hi = t.clamp(lo, hi)
	var b strings.Builder
	for _, c := range t.Chars[lo:hi] {
		b.WriteRune(c.R)
	}
	return b.String()
}

func (t *Text) clamp(lo, hi int) (int, int) {
	lo, hi = max(lo, 0), min(hi, len(t.Chars))
	return lo, max(lo, hi)
}

// At returns the index of the character shown at (x, y), allowing a little
// room around each, or false if the point is over no text.
func (t *Text) At(x, y float64) (int, bool) {
	for _, l := range t.lines {
		h := l.box.Y1 - l.box.Y0
		if y < l.box.Y0-slop*h || y > l.box.Y1+slop*h || x < l.box.X0-slop*h || x > l.box.X1+slop*h {
			continue
		}
		for i := l.start; i < l.end; i++ {
			c := t.Chars[i]
			if c.Real && x >= c.Box.X0-slop*h && x <= c.Box.X1+slop*h && y >= c.Box.Y0-slop*h && y <= c.Box.Y1+slop*h {
				return i, true
			}
		}
	}
	return 0, false
}

// Caret returns the position between characters nearest (x, y): the line
// nearest vertically, and in it the gap nearest horizontally. A caret c lies
// before Chars[c], so the carets run from 0 to len(Chars). It returns 0 for a
// page with no text.
func (t *Text) Caret(x, y float64) int {
	if len(t.lines) == 0 {
		return 0
	}
	best, bestDist := 0, -1.0
	for i, l := range t.lines {
		d := 0.0
		if y < l.box.Y0 {
			d = l.box.Y0 - y
		} else if y > l.box.Y1 {
			d = y - l.box.Y1
		}
		if bestDist < 0 || d < bestDist {
			best, bestDist = i, d
		}
	}
	l := t.lines[best]
	for i := l.start; i < l.end; i++ {
		if c := t.Chars[i].Box; x < (c.X0+c.X1)/2 {
			return i
		}
	}
	return l.end
}

// Word returns the characters [lo, hi) of the word at index i, which are the
// letters and digits around it, or just the character if it is neither.
func (t *Text) Word(i int) (lo, hi int) {
	if i < 0 || i >= len(t.Chars) {
		return 0, 0
	}
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	lo, hi = i, i+1
	if !word(t.Chars[i].R) {
		return lo, hi
	}
	for lo > 0 && word(t.Chars[lo-1].R) {
		lo--
	}
	for hi < len(t.Chars) && word(t.Chars[hi].R) {
		hi++
	}
	return lo, hi
}

// Line returns the characters [lo, hi) of the line holding index i, without its
// line break.
func (t *Text) Line(i int) (lo, hi int) {
	if i < 0 || i >= len(t.Chars) {
		return 0, 0
	}
	l := t.lines[t.Chars[i].Line]
	return l.start, l.end
}

// Rects returns the rectangles that cover the characters [lo, hi): one for
// each line they run over, so that a selection can be drawn.
func (t *Text) Rects(lo, hi int) []Rect {
	lo, hi = t.clamp(lo, hi)
	var out []Rect
	for _, l := range t.lines {
		a, b := max(lo, l.start), min(hi, l.end)
		if a >= b {
			continue
		}
		box := t.Chars[a].Box
		for _, c := range t.Chars[a+1 : b] {
			box = union(box, c.Box)
		}
		out = append(out, box)
	}
	return out
}

// Match is the characters [Start, End) that a search found.
type Match struct{ Start, End int }

// Find returns the places the text contains query, ignoring case, and treating
// a run of white space in the query, a line break in the text and a space the
// layer added alike. An empty query finds nothing.
func (t *Text) Find(query string) []Match {
	q := fold([]rune(query))
	if len(q) == 0 {
		return nil
	}
	hay := make([]rune, len(t.Chars))
	for i, c := range t.Chars {
		hay[i] = c.R
	}
	hay = foldKeep(hay)
	var out []Match
	for i := 0; i+len(q) <= len(hay); {
		if equal(hay[i:i+len(q)], q) {
			out = append(out, Match{i, i + len(q)})
			i += len(q)
			continue
		}
		i++
	}
	return out
}

func equal(a, b []rune) bool {
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// fold lowercases rs and collapses runs of white space in it to one space,
// trimming it at both ends.
func fold(rs []rune) []rune {
	out := make([]rune, 0, len(rs))
	for _, r := range rs {
		if unicode.IsSpace(r) {
			if len(out) > 0 && out[len(out)-1] != ' ' {
				out = append(out, ' ')
			}
			continue
		}
		out = append(out, unicode.ToLower(r))
	}
	if n := len(out); n > 0 && out[n-1] == ' ' {
		out = out[:n-1]
	}
	return out
}

// foldKeep lowercases rs and turns white space into spaces, keeping the length
// so that positions in the result are positions in rs.
func foldKeep(rs []rune) []rune {
	for i, r := range rs {
		if unicode.IsSpace(r) {
			rs[i] = ' '
		} else {
			rs[i] = unicode.ToLower(r)
		}
	}
	return rs
}
