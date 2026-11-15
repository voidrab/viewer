package desktop

import (
	"image"
	"image/color"
	"slices"
	"testing"

	"viewer/internal/ui/platform"
)

func TestParseURIList(t *testing.T) {
	list := "# comment\r\nfile:///tmp/a%20b.pdf\r\nfile://localhost/x.pdf\nfile://here/y.pdf\n" +
		"file://elsewhere/z.pdf\nhttp://example.org/w.pdf\nfile:\n%zz\n\n"
	want := []string{"/tmp/a b.pdf", "/x.pdf", "/y.pdf"}
	if got := ParseURIList(list, "here"); !slices.Equal(got, want) {
		t.Errorf("ParseURIList = %q, want %q", got, want)
	}
}

func TestCopyBGRASwapsRedAndBlue(t *testing.T) {
	src := image.NewRGBA(image.Rect(5, 5, 7, 7)) // Bounds start at (5, 5).
	src.SetRGBA(5, 5, color.RGBA{1, 2, 3, 4})
	src.SetRGBA(6, 6, color.RGBA{10, 20, 30, 40})
	dst := make([]byte, 2*12) // stride wider than the image: 12 bytes per row
	CopyBGRA(dst, 12, src)
	if got := dst[0:4]; got[0] != 3 || got[1] != 2 || got[2] != 1 || got[3] != 4 {
		t.Errorf("first pixel = %v", got)
	}
	if got := dst[12+4 : 12+8]; got[0] != 30 || got[1] != 20 || got[2] != 10 || got[3] != 40 {
		t.Errorf("last pixel = %v", got)
	}
	if dst[8] != 0 { // padding is untouched
		t.Errorf("padding written: %v", dst[8:12])
	}
}

func TestResolveKeysym(t *testing.T) {
	for _, tc := range []struct {
		sym uint32
		key platform.Key
		r   rune
	}{
		{0xff0d, platform.KeyEnter, 0},
		{0xff9c, platform.KeyEnd, 0},
		{'a', platform.KeyNone, 'a'},
		{0xe4, platform.KeyNone, 'ä'},
		{0xffb7, platform.KeyNone, '7'},
		{0xffab, platform.KeyNone, '+'},
		{0x1000000 + 0x263a, platform.KeyNone, '☺'},
		{0x20ac, platform.KeyNone, '€'},
		{0x1b9, platform.KeyNone, 'š'},
		{0x6c1, platform.KeyNone, 'а'},
		{0x7e1, platform.KeyNone, 'α'},
		{0xffe1, platform.KeyNone, 0}, // Shift_L types nothing
	} {
		if k, r := ResolveKeysym(tc.sym); k != tc.key || r != tc.r {
			t.Errorf("ResolveKeysym(%#x) = %v %q, want %v %q", tc.sym, k, r, tc.key, tc.r)
		}
	}
}

func TestRuneKeysymInvertsKeysymRune(t *testing.T) {
	for _, r := range []rune{'A', 'Ä', 'Š', 'Я', 'Ω', '☺'} {
		if got := KeysymRune(RuneKeysym(r)); got != r {
			t.Errorf("KeysymRune(RuneKeysym(%q)) = %q", r, got)
		}
	}
}
