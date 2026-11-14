package wayland

import (
	"testing"
	"time"

	"viewer/internal/ui/platform"
)

func TestServerSideDecorationIsRequestedWhenOffered(t *testing.T) {
	globals := append(append([]Global(nil), wmGlobals...), Global{Name: 9, Interface: "zxdg_decoration_manager_v1", Version: 1})
	be, f, _ := wmSetup(t, globals)
	win, err := be.NewWindow(platform.WindowOptions{Size: platform.Size{W: 100, H: 100}})
	if err != nil {
		t.Fatal(err)
	}
	f.wait("get_toplevel_decoration")
	f.wait("set_mode")
	f.wait("commit") // the mode is set before the first commit
	f.mu.Lock()
	if f.decorationMode != decorationModeServer {
		t.Errorf("mode = %d, want server-side (%d)", f.decorationMode, decorationModeServer)
	}
	f.mu.Unlock()

	// Closing the window destroys the decoration object before the toplevel.
	win.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mu.Lock()
		got := append([]string(nil), f.destroyed...)
		f.mu.Unlock()
		if len(got) >= 2 {
			if got[0] != "decoration" || got[1] != "toplevel" {
				t.Errorf("destroyed %v, want the decoration first", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("destroyed = %v", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestNoDecorationRequestWithoutTheManager(t *testing.T) {
	be, f, _ := wmSetup(t, wmGlobals)
	if be.decoration != 0 {
		t.Fatalf("decoration manager bound without being offered: %d", be.decoration)
	}
	win, _ := be.NewWindow(platform.WindowOptions{Size: platform.Size{W: 100, H: 100}})
	f.wait("commit")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.decoration != 0 {
		t.Error("a decoration was requested from a compositor without the protocol")
	}
	_ = win
}
