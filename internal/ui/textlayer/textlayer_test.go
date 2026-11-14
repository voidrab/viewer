package textlayer

import (
	"reflect"
	"testing"

	"github.com/voidrab/gopdfrab"
)

// run lays out s as glyphs 10 wide and 20 tall from (x, y); a space in s
// leaves a gap but, like most PDFs, shows no glyph.
func run(x, y float64, s string) []gopdfrab.Glyph {
	var out []gopdfrab.Glyph
	for _, r := range s {
		if r != ' ' {
			out = append(out, gopdfrab.Glyph{Rune: r, Box: box(x, y, x+10, y+20)})
		}
		x += 10
	}
	return out
}

// box returns the gopdfrab.Rect with corners (x0, y0) and (x1, y1).
func box(x0, y0, x1, y1 float64) gopdfrab.Rect {
	return gopdfrab.Rect{X0: x0, Y0: y0, X1: x1, Y1: y1}
}

func join(parts ...[]gopdfrab.Glyph) []gopdfrab.Glyph {
	var out []gopdfrab.Glyph
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func sample() *Text {
	return Build(join(run(10, 10, "Hello world"), run(10, 40, "Second line")))
}

func TestBuildAddsSpacesAndLineBreaks(t *testing.T) {
	if got, want := sample().String(), "Hello world\nSecond line"; got != want {
		t.Errorf("text = %q, want %q", got, want)
	}
	// Added characters are marked, and have the gap they stand for as their box.
	text := sample()
	space := text.Chars[5]
	if space.R != ' ' || space.Real || space.Box != (Rect{60, 10, 70, 30}) {
		t.Errorf("space = %+v", space)
	}
	if nl := text.Chars[11]; nl.R != '\n' || nl.Real || nl.Line != 0 {
		t.Errorf("line break = %+v", nl)
	}
}

func TestBuildLeavesOutGlyphsWithNoCharacter(t *testing.T) {
	g := join(run(0, 0, "a"), []gopdfrab.Glyph{{Rune: 0, Box: box(10, 0, 20, 20)}, {Rune: '\t', Box: box(10, 0, 20, 20)}}, run(10, 0, "b"))
	if got := Build(g).String(); got != "ab" {
		t.Errorf("text = %q, want %q", got, "ab")
	}
	if got := Build(nil).String(); got != "" {
		t.Errorf("empty page text = %q", got)
	}
}

func TestBuildStartsALineWhenTheTextJumpsBack(t *testing.T) {
	// Same height, but the second run starts well left of where the first ended:
	// two columns, shown one after the other.
	text := Build(join(run(200, 10, "right"), run(10, 10, "left")))
	if got := text.String(); got != "right\nleft" {
		t.Errorf("text = %q", got)
	}
	// A small step back, as kerning makes, stays on the line.
	text = Build([]gopdfrab.Glyph{{Rune: 'A', Box: box(0, 0, 10, 20)}, {Rune: 'V', Box: box(8, 0, 18, 20)}})
	if got := text.String(); got != "AV" {
		t.Errorf("kerned text = %q", got)
	}
	// A superscript, shorter and higher but overlapping, stays on the line.
	text = Build([]gopdfrab.Glyph{{Rune: 'x', Box: box(0, 10, 10, 30)}, {Rune: '2', Box: box(10, 6, 16, 18)}})
	if got := text.String(); got != "x2" {
		t.Errorf("text with a superscript = %q", got)
	}
}

func TestSliceIsClamped(t *testing.T) {
	text := sample()
	if got := text.Slice(-5, 5); got != "Hello" {
		t.Errorf("Slice(-5, 5) = %q", got)
	}
	if got := text.Slice(12, 999); got != "Second line" {
		t.Errorf("Slice(12, 999) = %q", got)
	}
	if got := text.Slice(8, 3); got != "" {
		t.Errorf("a backwards range gave %q", got)
	}
}

func TestAtHitsOnlyShownCharacters(t *testing.T) {
	text := sample()
	if i, ok := text.At(15, 20); !ok || i != 0 {
		t.Errorf("At inside H = %d, %v", i, ok)
	}
	if i, ok := text.At(118, 20); !ok || text.Chars[i].R != 'd' {
		t.Errorf("At right part of the last d = %d, %v", i, ok)
	}
	if _, ok := text.At(65, 20); ok {
		t.Error("the gap between the words was a hit")
	}
	if _, ok := text.At(15, 35); ok {
		t.Error("the space between the lines was a hit")
	}
	if _, ok := text.At(500, 20); ok {
		t.Error("a point far right of the line was a hit")
	}
	if _, ok := text.At(15, 500); ok {
		t.Error("a point far below the text was a hit")
	}
}

func TestCaretFindsTheNearestGap(t *testing.T) {
	text := sample()
	for _, tc := range []struct {
		x, y float64
		want int
		why  string
	}{
		{12, 20, 0, "left half of the first glyph"},
		{18, 20, 1, "right half of the first glyph"},
		{-50, 20, 0, "left of the line"},
		{900, 20, 11, "right of the first line"},
		{12, 45, 12, "start of the second line"},
		{12, -99, 0, "above everything"},
		{900, 999, 23, "below everything, past the end"},
		{12, 36, 12, "nearer the second line than the first"},
	} {
		if got := text.Caret(tc.x, tc.y); got != tc.want {
			t.Errorf("Caret(%v, %v) = %d, want %d (%s)", tc.x, tc.y, got, tc.want, tc.why)
		}
	}
	if got := Build(nil).Caret(5, 5); got != 0 {
		t.Errorf("Caret on an empty page = %d", got)
	}
}

func TestWordAndLine(t *testing.T) {
	text := sample()
	if lo, hi := text.Word(7); text.Slice(lo, hi) != "world" {
		t.Errorf("Word(7) = %q", text.Slice(lo, hi))
	}
	if lo, hi := text.Word(5); text.Slice(lo, hi) != " " {
		t.Errorf("Word over a space = %q", text.Slice(lo, hi))
	}
	if lo, hi := text.Word(14); text.Slice(lo, hi) != "Second" {
		t.Errorf("Word(14) = %q", text.Slice(lo, hi))
	}
	if lo, hi := text.Line(14); text.Slice(lo, hi) != "Second line" {
		t.Errorf("Line(14) = %q", text.Slice(lo, hi))
	}
	if lo, hi := text.Line(3); text.Slice(lo, hi) != "Hello world" {
		t.Errorf("Line(3) = %q", text.Slice(lo, hi))
	}
	for _, i := range []int{-1, 999} {
		if lo, hi := text.Word(i); lo != 0 || hi != 0 {
			t.Errorf("Word(%d) = %d, %d", i, lo, hi)
		}
		if lo, hi := text.Line(i); lo != 0 || hi != 0 {
			t.Errorf("Line(%d) = %d, %d", i, lo, hi)
		}
	}
}

func TestRectsCoverEachLineOfARange(t *testing.T) {
	text := sample()
	got := text.Rects(3, 15) // "lo world", the break, "Sec"
	want := []Rect{{40, 10, 120, 30}, {10, 40, 40, 60}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rects = %+v, want %+v", got, want)
	}
	if got := text.Rects(4, 4); len(got) != 0 {
		t.Errorf("an empty range has rects %+v", got)
	}
	if got := text.Rects(11, 12); len(got) != 0 {
		t.Errorf("a line break alone has rects %+v", got)
	}
}

func TestFindIgnoresCaseAndWhiteSpace(t *testing.T) {
	text := Build(join(run(10, 10, "The cat sat"), run(10, 40, "on the mat")))
	for _, tc := range []struct {
		query string
		want  []Match
	}{
		{"the", []Match{{0, 3}, {15, 18}}},
		{"CAT  sat", []Match{{4, 11}}},
		{"sat on", []Match{{8, 14}}}, // across the line break
		{" mat ", []Match{{19, 22}}},
		{"dog", nil},
		{"", nil},
		{"   ", nil},
		{"the cat sat on the mat and more", nil},
	} {
		got := text.Find(tc.query)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("Find(%q) = %v, want %v", tc.query, got, tc.want)
		}
	}
	// Matches do not overlap.
	if got := Build(run(0, 0, "aaaa")).Find("aa"); len(got) != 2 {
		t.Errorf("Find(aa) in aaaa = %v, want two", got)
	}
}
