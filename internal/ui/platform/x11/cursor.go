package x11

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"viewer/internal/ui/platform"
)

// RENDER minor opcodes.
const (
	renderQueryVersion     = 0
	renderQueryPictFormats = 1
	renderCreatePicture    = 4
	renderFreePicture      = 7
	renderCreateCursor     = 27
)

// atomResourceManager is the predefined atom of the root's resource
// database.
const atomResourceManager = 23

// cursorNames lists the names each cursor goes by in cursor themes, the CSS
// name first. cursorGlyphs gives the glyph of each cursor in the core cursor
// font.
var (
	cursorNames = map[platform.Cursor][]string{
		platform.CursorDefault:  {"default", "left_ptr"},
		platform.CursorPointer:  {"pointer", "hand2", "hand1"},
		platform.CursorResizeN:  {"n-resize", "top_side"},
		platform.CursorResizeS:  {"s-resize", "bottom_side"},
		platform.CursorResizeW:  {"w-resize", "left_side"},
		platform.CursorResizeE:  {"e-resize", "right_side"},
		platform.CursorResizeNW: {"nw-resize", "top_left_corner"},
		platform.CursorResizeNE: {"ne-resize", "top_right_corner"},
		platform.CursorResizeSW: {"sw-resize", "bottom_left_corner"},
		platform.CursorResizeSE: {"se-resize", "bottom_right_corner"},
	}
	cursorGlyphs = map[platform.Cursor]uint16{
		platform.CursorDefault:  68,
		platform.CursorPointer:  60,
		platform.CursorResizeN:  138,
		platform.CursorResizeS:  16,
		platform.CursorResizeW:  70,
		platform.CursorResizeE:  96,
		platform.CursorResizeNW: 134,
		platform.CursorResizeNE: 136,
		platform.CursorResizeSW: 12,
		platform.CursorResizeSE: 14,
	}
)

// cursors are the pointer images, made the first time each is shown: from
// the user's cursor theme with RENDER, or else from the core cursor font.
type cursors struct {
	mu    sync.Mutex
	made  map[platform.Cursor]uint32
	theme string
	size  int
	font  uint32 // the opened cursor font, or zero
}

// init reads the cursor theme and size from the environment or, failing
// that, the X resources, as libXcursor does.
func (cs *cursors) init(b *Backend) {
	cs.made = map[platform.Cursor]uint32{}
	cs.theme, cs.size = "default", 24
	p, _ := b.c.getProperty(b.root, atomResourceManager, false, 1<<20)
	res := parseResources(string(p.value))
	if t := res["Xcursor.theme"]; t != "" {
		cs.theme = t
	}
	if n, err := strconv.Atoi(res["Xcursor.size"]); err == nil && n > 0 {
		cs.size = n
	}
	if t := os.Getenv("XCURSOR_THEME"); t != "" {
		cs.theme = t
	}
	if n, err := strconv.Atoi(os.Getenv("XCURSOR_SIZE")); err == nil && n > 0 {
		cs.size = n
	}
}

// parseResources reads the lines "name: value" of a resource database.
func parseResources(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

// get returns the cursor for c, making it the first time. Zero, the
// parent's cursor, stands in for one that cannot be made.
func (cs *cursors) get(b *Backend, c platform.Cursor) uint32 {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if id, ok := cs.made[c]; ok {
		return id
	}
	id := cs.fromTheme(b, c)
	if id == 0 {
		id = cs.fromFont(b, c)
	}
	cs.made[c] = id
	return id
}

// fromFont makes c from the core cursor font. The caller holds cs.mu.
func (cs *cursors) fromFont(b *Backend, c platform.Cursor) uint32 {
	glyph, ok := cursorGlyphs[c]
	if !ok {
		return 0
	}
	if cs.font == 0 {
		// A server may lack the font, so it is looked for first.
		const name = "cursor"
		rep, err := b.c.Call(newReq(opListFonts, 0).u16(1).u16(uint16(len(name))).bytes([]byte(name)))
		if err != nil || u16At(rep, 8) == 0 {
			return 0
		}
		cs.font = b.c.NewID()
		b.c.Send(newReq(opOpenFont, 0).u32(cs.font).u16(uint16(len(name))).pad(2).bytes([]byte(name)))
	}
	id := b.c.NewID()
	// Black on white, as the font's cursors are drawn.
	b.c.Send(newReq(opCreateGlyphCursor, 0).u32(id).u32(cs.font).u32(cs.font).u16(glyph).u16(glyph + 1).
		u16(0).u16(0).u16(0).u16(0xffff).u16(0xffff).u16(0xffff))
	return id
}

// fromTheme makes c from the user's cursor theme, or returns zero. The
// caller holds cs.mu.
func (cs *cursors) fromTheme(b *Backend, c platform.Cursor) uint32 {
	if !b.render.present {
		return 0
	}
	for _, name := range cursorNames[c] {
		if f := findCursorFile(cs.theme, name); f != "" {
			if img, ok := loadXcursor(f, cs.size); ok {
				return b.uploadCursor(img)
			}
		}
	}
	return 0
}

// cursorPath returns the directories cursor themes live in.
func cursorPath() []string {
	if p := os.Getenv("XCURSOR_PATH"); p != "" {
		return filepath.SplitList(p)
	}
	home, _ := os.UserHomeDir()
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	return []string{filepath.Join(dataHome, "icons"), filepath.Join(home, ".icons"), "/usr/share/icons", "/usr/share/pixmaps"}
}

// findCursorFile returns the file of the cursor called name in theme or the
// themes it inherits from, or an empty string.
func findCursorFile(theme, name string) string {
	seen := map[string]bool{}
	var find func(theme string) string
	find = func(theme string) string {
		if seen[theme] || len(seen) > 16 {
			return ""
		}
		seen[theme] = true
		var inherits []string
		for _, dir := range cursorPath() {
			f := filepath.Join(dir, theme, "cursors", name)
			if st, err := os.Stat(f); err == nil && !st.IsDir() {
				return f
			}
			inherits = append(inherits, themeInherits(filepath.Join(dir, theme, "index.theme"))...)
		}
		for _, t := range inherits {
			if f := find(t); f != "" {
				return f
			}
		}
		return ""
	}
	return find(theme)
}

// themeInherits returns the themes an index.theme names in Inherits.
func themeInherits(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok && strings.TrimSpace(k) == "Inherits" {
			return strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' || r == ' ' })
		}
	}
	return nil
}

// cursorImage is one image of an Xcursor file: premultiplied ARGB pixels,
// one 32-bit value each, and the hotspot.
type cursorImage struct {
	w, h, xhot, yhot int
	pix              []uint32
}

// xcursorImage is the chunk type of an image in an Xcursor file.
const xcursorImage = 0xfffd0002

// loadXcursor reads the image of the nominal size nearest size from an
// Xcursor file; of an animated cursor, its first frame.
func loadXcursor(path string, size int) (cursorImage, bool) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) < 16 || string(data[:4]) != "Xcur" {
		return cursorImage{}, false
	}
	ntoc := int(u32At(data, 12))
	best, bestPos := -1, 0
	for i := range ntoc {
		off := 16 + 12*i
		if off+12 > len(data) {
			break
		}
		typ, sub, pos := u32At(data, off), int(u32At(data, off+4)), int(u32At(data, off+8))
		if typ != xcursorImage {
			continue
		}
		if best < 0 || abs(sub-size) < abs(best-size) {
			best, bestPos = sub, pos
		}
	}
	if best < 0 || bestPos+36 > len(data) {
		return cursorImage{}, false
	}
	d := data[bestPos:]
	img := cursorImage{w: int(u32At(d, 16)), h: int(u32At(d, 20)), xhot: int(u32At(d, 24)), yhot: int(u32At(d, 28))}
	n := img.w * img.h
	if img.w <= 0 || img.h <= 0 || img.w > 1024 || img.h > 1024 || 36+4*n > len(d) {
		return cursorImage{}, false
	}
	img.pix = make([]uint32, n)
	for i := range img.pix {
		img.pix[i] = u32At(d, 36+4*i)
	}
	return img, true
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// renderARGB starts RENDER and returns its 32-bit ARGB picture format, or
// zero.
func (b *Backend) renderARGB() uint32 {
	m := b.render.major
	if _, err := b.c.Call(newReq(m, renderQueryVersion).u32(0).u32(11)); err != nil {
		return 0
	}
	rep, err := b.c.Call(newReq(m, renderQueryPictFormats))
	if err != nil {
		return 0
	}
	n := int(u32At(rep, 8))
	for i := range n {
		f := 32 + 28*i
		if f+28 > len(rep) {
			break
		}
		// Direct, depth 32, with alpha at 24, red at 16, green at 8, blue at 0.
		if rep[f+4] == 1 && rep[f+5] == 32 &&
			u16At(rep, f+8) == 16 && u16At(rep, f+10) == 0xff &&
			u16At(rep, f+12) == 8 && u16At(rep, f+14) == 0xff &&
			u16At(rep, f+16) == 0 && u16At(rep, f+18) == 0xff &&
			u16At(rep, f+20) == 24 && u16At(rep, f+22) == 0xff {
			return u32At(rep, f)
		}
	}
	return 0
}

// uploadCursor makes a cursor of img with RENDER, by way of a 32-bit pixmap
// and a picture of it.
func (b *Backend) uploadCursor(img cursorImage) uint32 {
	c := b.c
	pix, gc, pict, id := c.NewID(), c.NewID(), c.NewID(), c.NewID()
	data := make([]byte, 4*len(img.pix))
	for i, p := range img.pix {
		if b.c.Setup.ImageByteOrder == 1 {
			p = p>>24 | p>>8&0xff00 | p<<8&0xff0000 | p<<24
		}
		order.PutUint32(data[4*i:], p)
	}
	m := b.render.major
	c.Send(newReq(opCreatePixmap, 32).u32(pix).u32(b.root).u16(uint16(img.w)).u16(uint16(img.h)))
	c.Send(newReq(opCreateGC, 0).u32(gc).u32(pix).u32(0))
	b.putImage(pix, gc, 32, img.w, img.h, data)
	c.Send(newReq(m, renderCreatePicture).u32(pict).u32(pix).u32(b.argbPict).u32(0))
	c.Send(newReq(m, renderCreateCursor).u32(id).u32(pict).u16(uint16(img.xhot)).u16(uint16(img.yhot)))
	c.Send(newReq(m, renderFreePicture).u32(pict))
	c.Send(newReq(opFreeGC, 0).u32(gc))
	c.Send(newReq(opFreePixmap, 0).u32(pix))
	return id
}
