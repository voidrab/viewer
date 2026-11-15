//go:build !windows

package x11

import (
	"slices"
	"strings"
	"testing"

	"viewrab/internal/ui/platform"
)

// rootMessages returns the client messages of type typ sent to the root, as
// their window and data.
func rootMessages(f *fakeX, typ string) [][6]uint32 {
	a := f.atom(typ)
	var out [][6]uint32
	for _, r := range f.requests(opSendEvent) {
		if r.u32(0) == fakeRoot && r.body[8] == evClientMessage && u32At(r.body, 16) == a {
			m := [6]uint32{u32At(r.body, 12)}
			for i := range 5 {
				m[1+i] = u32At(r.body, 20+4*i)
			}
			out = append(out, m)
		}
	}
	return out
}

func TestMoveResizeAndMenuGoToTheWindowManager(t *testing.T) {
	f, b := startBackend(t, withWM("_NET_WM_MOVERESIZE", "_GTK_SHOW_WINDOW_MENU"))
	w := openWindow(t, f, b, platform.WindowOptions{})
	f.event(inputEv(evButtonPress, 1, 50, w.id, 300, 400, 30, 40, 0))
	want[platform.PointerEvent](t, w)
	w.Move()
	w.Resize(platform.EdgeBottom | platform.EdgeRight)
	w.Resize(platform.EdgeTop | platform.EdgeBottom) // no side or corner: nothing
	w.ShowMenu(5, 6)
	b.c.Sync()
	moves := rootMessages(f, "_NET_WM_MOVERESIZE")
	if len(moves) != 2 || moves[0] != [6]uint32{w.id, 300, 400, moveResizeMove, 1, sourceApp} || moves[1][3] != 4 {
		t.Errorf("moves %v", moves)
	}
	menus := rootMessages(f, "_GTK_SHOW_WINDOW_MENU")
	if len(menus) != 1 || menus[0][2] != 275 || menus[0][3] != 366 {
		t.Errorf("menus %v", menus)
	}
	if n := len(f.requests(opUngrabPointer)); n != 3 {
		t.Errorf("pointer released %d times", n)
	}
}

func TestMoveAndMenuWithoutSupportDoNothing(t *testing.T) {
	f, b := startBackend(t, withWM())
	w := openWindowState(t, f, b)
	w.Move()
	w.ShowMenu(1, 1)
	b.c.Sync()
	if n := len(rootMessages(f, "_NET_WM_MOVERESIZE")) + len(rootMessages(f, "_GTK_SHOW_WINDOW_MENU")); n != 0 {
		t.Errorf("%d messages sent", n)
	}
}

func TestMaximizeMinimizeAndActivate(t *testing.T) {
	f, b := startBackend(t, nil)
	w := openWindow(t, f, b, platform.WindowOptions{})
	f.event(inputEv(evKeyPress, 8, 77, w.id, 0, 0, 0, 0, 0))
	want[platform.KeyEvent](t, w)
	w.SetMaximized(true)
	w.SetMaximized(false)
	w.Minimize()
	w.Activate("")
	w.Activate("host-1-launch_TIME1234")
	w.Activate("no-time_TIMEx")
	b.c.Sync()
	vert, horz := f.atom("_NET_WM_STATE_MAXIMIZED_VERT"), f.atom("_NET_WM_STATE_MAXIMIZED_HORZ")
	if got := rootMessages(f, "_NET_WM_STATE"); len(got) != 2 || got[0] != [6]uint32{w.id, 1, vert, horz, sourceApp} || got[1][1] != 0 {
		t.Errorf("states %v", got)
	}
	if got := rootMessages(f, "WM_CHANGE_STATE"); len(got) != 1 || got[0][1] != iconicState {
		t.Errorf("minimise %v", got)
	}
	var times []uint32
	for _, m := range rootMessages(f, "_NET_ACTIVE_WINDOW") {
		times = append(times, m[2])
	}
	if !slices.Equal(times, []uint32{77, 1234, 77}) {
		t.Errorf("activation times %v", times)
	}
	// Each token's launch is ended.
	if n := len(rootMessages(f, "_NET_STARTUP_INFO_BEGIN")); n != 2 {
		t.Errorf("startup removals %d", n)
	}
}

func TestStartupMessagesAreSplitAndQuoted(t *testing.T) {
	f, b := startBackend(t, nil)
	b.sendStartup("remove: ID=" + quoteStartup(`a "quoted" \ id that runs past twenty bytes`))
	b.c.Sync()
	var msg []byte
	for _, r := range f.requests(opSendEvent) {
		if r.body[8] == evClientMessage && r.body[9] == 8 {
			msg = append(msg, r.body[20:40]...)
		}
	}
	text, _, _ := strings.Cut(string(msg), "\x00")
	if text != `remove: ID="a \"quoted\" \\ id that runs past twenty bytes"` {
		t.Errorf("message %q", text)
	}
	begins, rest := rootMessages(f, "_NET_STARTUP_INFO_BEGIN"), rootMessages(f, "_NET_STARTUP_INFO")
	if len(begins) != 1 || len(rest) != 2 {
		t.Errorf("%d begin and %d further pieces", len(begins), len(rest))
	}
}

func TestSetCursor(t *testing.T) {
	f, b := startBackend(t, func(f *fakeX) { f.formats = nil }) // no themes: the font
	w := openWindow(t, f, b, platform.WindowOptions{})
	w.SetCursor(platform.CursorDefault) // the cursor it has
	w.SetCursor(platform.CursorPointer)
	w.SetCursor(platform.CursorPointer)
	w.SetCursor(platform.CursorResizeNE)
	b.c.Sync()
	var set []uint32
	for _, r := range f.requests(opChangeWindowAttribute) {
		if r.u32(0) == w.id && r.u32(4) == cwCursor {
			set = append(set, r.u32(8))
		}
	}
	glyphs := f.requests(opCreateGlyphCursor)
	if len(set) != 2 || len(glyphs) != 2 || set[0] != glyphs[0].u32(0) || glyphs[0].u16(12) != 60 || glyphs[1].u16(12) != 136 {
		t.Errorf("cursors set %v from %d glyphs", set, len(glyphs))
	}
	if n := len(f.requests(opOpenFont)); n != 1 {
		t.Errorf("font opened %d times", n)
	}
}
