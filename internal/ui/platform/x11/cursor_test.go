//go:build !windows

package x11

import (
	"image"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"viewrab/internal/ui/platform"
)

// xcursorFile encodes an Xcursor file with one image of each nominal size,
// size by size pixels with the hotspot at (1, 2), every pixel p.
func xcursorFile(p uint32, sizes ...int) []byte {
	head := (&req{}).bytes([]byte("Xcur")).u32(16).u32(0x10000).u32(uint32(len(sizes)))
	pos := 16 + 12*len(sizes)
	var chunks []byte
	for _, s := range sizes {
		head.u32(xcursorImage).u32(uint32(s)).u32(uint32(pos + len(chunks)))
		c := (&req{}).u32(36).u32(xcursorImage).u32(uint32(s)).u32(1).u32(uint32(s)).u32(uint32(s)).u32(1).u32(2).u32(0)
		for range s * s {
			c.u32(p)
		}
		chunks = append(chunks, c.b...)
	}
	return append(head.b, chunks...)
}

// cursorTheme lays out cursor themes under a temporary directory, which it
// makes the cursor path: files maps a path inside it to its contents.
func cursorTheme(t *testing.T, files map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	for name, data := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XCURSOR_PATH", dir+string(os.PathListSeparator)+filepath.Join(dir, "missing"))
	return dir
}

func TestCursorFromATheme(t *testing.T) {
	cursorTheme(t, map[string][]byte{
		"mine/index.theme":       []byte("[Icon Theme]\nName=Mine\nInherits = base, other\n"),
		"base/cursors/left_ptr":  xcursorFile(0x80112233, 16, 30, 64),
		"base/cursors/hand2":     []byte("not a cursor"),
		"base/cursors/n-resize/": nil, // a directory, which is no cursor
	})
	t.Setenv("XCURSOR_THEME", "mine")
	t.Setenv("XCURSOR_SIZE", "24")
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{})
	w.SetCursor(platform.CursorPointer) // unreadable in the theme: the font's
	w.SetCursor(platform.CursorResizeNE)
	w.SetCursor(platform.CursorDefault) // from the theme, at size 30
	b.c.Sync()
	render := f.requests(139)
	var minors []byte
	for _, r := range render {
		minors = append(minors, r.data)
	}
	if want := []byte{renderQueryVersion, renderQueryPictFormats, renderCreatePicture, renderCreateCursor, renderFreePicture}; !slices.Equal(minors, want) {
		t.Errorf("RENDER requests %v, want %v", minors, want)
	}
	pix := f.requests(opCreatePixmap)
	if len(pix) != 1 || pix[0].data != 32 || pix[0].u16(8) != 30 {
		t.Errorf("pixmaps %v", pix)
	}
	put := f.requests(opPutImage)
	if len(put) == 0 || u32At(put[len(put)-1].body, 20) != 0x80112233 {
		t.Error("cursor pixels not uploaded")
	}
	if c := render[3]; c.u16(8) != 1 || c.u16(10) != 2 {
		t.Errorf("hotspot %d,%d", c.u16(8), c.u16(10))
	}
	if n := len(f.requests(opCreateGlyphCursor)); n != 2 {
		t.Errorf("font cursors %d", n)
	}
	// Bytes most significant first, for such a server.
	b.c.Setup.ImageByteOrder = 1
	b.uploadCursor(cursorImage{w: 1, h: 1, pix: []uint32{0x11223344}})
	b.c.Sync()
	put = f.requests(opPutImage)
	if got := put[len(put)-1].body[20:24]; !slices.Equal(got, []byte{0x11, 0x22, 0x33, 0x44}) {
		t.Errorf("MSB pixel %x", got)
	}
}

func TestCursorSettingsFromResources(t *testing.T) {
	t.Setenv("XCURSOR_THEME", "")
	t.Setenv("XCURSOR_SIZE", "")
	_, b := startBackend(t, func(f *fakeX) {
		f.setProp(fakeRoot, atomResourceManager, atomString, 8, []byte("Xft.dpi:\t96\nXcursor.theme:\tbreeze\nno colon here\nXcursor.size:\t32\n"))
	})
	if b.cursors.theme != "breeze" || b.cursors.size != 32 {
		t.Errorf("theme %q size %d", b.cursors.theme, b.cursors.size)
	}
	t.Setenv("XCURSOR_SIZE", "huge")
	_, b = startBackend(t, nil)
	if b.cursors.theme != "default" || b.cursors.size != 24 {
		t.Errorf("defaults: theme %q size %d", b.cursors.theme, b.cursors.size)
	}
}

func TestCursorPath(t *testing.T) {
	t.Setenv("XCURSOR_PATH", "")
	t.Setenv("HOME", "/home/u")
	t.Setenv("XDG_DATA_HOME", "")
	want := []string{"/home/u/.local/share/icons", "/home/u/.icons", "/usr/share/icons", "/usr/share/pixmaps"}
	if got := cursorPath(); !slices.Equal(got, want) {
		t.Errorf("cursorPath = %v", got)
	}
	t.Setenv("XDG_DATA_HOME", "/data")
	if got := cursorPath(); got[0] != "/data/icons" {
		t.Errorf("with XDG_DATA_HOME: %v", got)
	}
}

func TestFindCursorFileStopsAtInheritanceLoops(t *testing.T) {
	cursorTheme(t, map[string][]byte{
		"a/index.theme": []byte("Inherits=b\n"),
		"b/index.theme": []byte("Comment=no inherits here\nInherits=a\n"),
		"c/index.theme": []byte("Name=c\n"),
	})
	if got := findCursorFile("a", "left_ptr"); got != "" {
		t.Errorf("found %q", got)
	}
	if got := themeInherits("/nonexistent/index.theme"); got != nil {
		t.Errorf("inherits of a missing file %v", got)
	}
	dir := cursorTheme(t, map[string][]byte{"c/index.theme": []byte("Name=c\n")})
	if got := themeInherits(filepath.Join(dir, "c/index.theme")); got != nil {
		t.Errorf("inherits without the key %v", got)
	}
}

func TestLoadXcursorRejectsBadFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(data []byte) string {
		p := filepath.Join(dir, "c")
		os.WriteFile(p, data, 0o644)
		return p
	}
	good := xcursorFile(1, 8)
	big := xcursorFile(1, 8)
	order.PutUint32(big[16+12+16:], 2000) // a width past the limit
	truncatedTOC := slices.Clone(good[:20])
	order.PutUint32(truncatedTOC[12:], 3)
	otherChunk := slices.Clone(good)
	order.PutUint32(otherChunk[16:], 0xfffe0001) // a comment, not an image
	farChunk := slices.Clone(good)
	order.PutUint32(farChunk[24:], 1<<20)
	for name, data := range map[string][]byte{
		"short": []byte("Xcur"), "magic": append([]byte("Xcux"), good[4:]...), "toc": truncatedTOC,
		"no image": otherChunk, "position": farChunk, "size": big, "pixels": good[:len(good)-4],
	} {
		if _, ok := loadXcursor(write(data), 24); ok {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, ok := loadXcursor(filepath.Join(dir, "missing"), 24); ok {
		t.Error("missing file accepted")
	}
	if img, ok := loadXcursor(write(xcursorFile(7, 32, 16)), 20); !ok || img.w != 16 {
		t.Errorf("nearest size: %+v %v", img, ok)
	}
}

func TestCursorsThatCannotBeMade(t *testing.T) {
	t.Setenv("XCURSOR_PATH", t.TempDir())
	f, b := startBackend(t, func(f *fakeX) { f.fontless = true })
	if id := b.cursors.get(b, platform.CursorPointer); id != 0 {
		t.Errorf("cursor without a font: %#x", id)
	}
	if id := b.cursors.get(b, platform.Cursor(99)); id != 0 {
		t.Errorf("unknown cursor: %#x", id)
	}
	f.set(func(f *fakeX) {
		f.fontless = false
		f.handle = func(f *fakeX, r request) bool {
			if r.op == opListFonts {
				f.fail(r, 3, 0)
				return true
			}
			return false
		}
	})
	b.cursors.made = map[platform.Cursor]uint32{}
	if id := b.cursors.get(b, platform.CursorPointer); id != 0 {
		t.Errorf("cursor after a failed font search: %#x", id)
	}
}

func TestPixelFormats(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1, 1))
	copy(src.Pix, []byte{0x11, 0x22, 0x33, 0x80})
	for _, tc := range []struct {
		v     Visual
		order byte
		want  []byte
	}{
		{Visual{Depth: 24, RedMask: 0xff, GreenMask: 0xff00, BlueMask: 0xff0000}, 0, []byte{0x11, 0x22, 0x33, 0}},
		{Visual{Depth: 24, RedMask: 0xff0000, GreenMask: 0xff00, BlueMask: 0xff}, 1, []byte{0, 0x11, 0x22, 0x33}},
		{Visual{Depth: 32, RedMask: 0xff0000, GreenMask: 0xff00, BlueMask: 0xff}, 1, []byte{0x80, 0x11, 0x22, 0x33}},
		{Visual{Depth: 32, RedMask: 0xff0000, GreenMask: 0xff00, BlueMask: 0xff}, 0, []byte{0x33, 0x22, 0x11, 0x80}},
	} {
		pf, ok := newPixelFormat(tc.v, tc.order)
		if !ok {
			t.Fatalf("%+v rejected", tc.v)
		}
		dst := make([]byte, 4)
		pf.convert(dst, 4, src)
		if !slices.Equal(dst, tc.want) {
			t.Errorf("%+v order %d: %x, want %x", tc.v, tc.order, dst, tc.want)
		}
	}
	// A 32-bit visual whose colours leave no whole byte for alpha.
	if _, ok := newPixelFormat(Visual{Depth: 32, RedMask: 0x3ff00000, GreenMask: 0xffc00, BlueMask: 0x3ff}, 0); ok {
		t.Error("30-bit colour accepted")
	}
}
