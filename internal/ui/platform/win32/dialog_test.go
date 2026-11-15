package win32

import (
	"path/filepath"
	"slices"
	"testing"
	"unicode/utf16"
)

// units encodes parts as a dialog writes them: each ending in a NUL, and the
// list in another.
func units(parts ...string) []uint16 {
	var out []uint16
	for _, p := range parts {
		out = append(append(out, utf16.Encode([]rune(p))...), 0)
	}
	return append(out, 0)
}

func TestChosenFiles(t *testing.T) {
	cases := []struct {
		name string
		buf  []uint16
		want []string
	}{
		{"nothing", units(), nil},
		{"one file", units(`C:\docs\a.pdf`), []string{`C:\docs\a.pdf`}},
		{"several files", units(`C:\docs`, "a.pdf", "b ü.pdf"),
			[]string{filepath.Join(`C:\docs`, "a.pdf"), filepath.Join(`C:\docs`, "b ü.pdf")}},
		{"a buffer filled to its end", utf16.Encode([]rune("abc")), []string{"abc"}},
	}
	for _, c := range cases {
		if got := chosenFiles(c.buf); !slices.Equal(got, c.want) {
			t.Errorf("%s: chosenFiles = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestChosenFirst(t *testing.T) {
	if got := chosenFirst(units(`C:\out\report.pdf`, "rest")); got != `C:\out\report.pdf` {
		t.Errorf("chosenFirst = %q", got)
	}
	if got := chosenFirst(utf16.Encode([]rune("full"))); got != "full" {
		t.Errorf("chosenFirst without a NUL = %q", got)
	}
}

func TestDialogBuffer(t *testing.T) {
	buf := dialogBuffer(4, "ab\x00cdef")
	if want := []uint16{'a', 'b', 'c', 0}; !slices.Equal(buf, want) {
		t.Errorf("dialogBuffer = %v, want %v", buf, want)
	}
}

func TestPDFFilterIsPairsEndingInTwoNULs(t *testing.T) {
	if n := len(pdfFilter); pdfFilter[n-2:] != "\x00\x00" {
		t.Fatalf("the filter %q does not end in two NULs", pdfFilter)
	}
	parts := 0
	for _, r := range pdfFilter[:len(pdfFilter)-1] {
		if r == 0 {
			parts++
		}
	}
	if parts%2 != 0 {
		t.Errorf("the filter has %d parts, not pairs", parts)
	}
}
