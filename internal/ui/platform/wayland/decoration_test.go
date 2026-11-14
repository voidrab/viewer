package wayland

import (
	"testing"
	"time"

	"viewer/internal/ui/platform"
)

var decorationGlobals = append(append([]Global(nil), wmGlobals...), Global{Name: 9, Interface: "zxdg_decoration_manager_v1", Version: 1})

func TestClientSideDecorationIsRequestedWhenOffered(t *testing.T) {
	be, f, _ := wmSetup(t, decorationGlobals)
	win, err := be.NewWindow(platform.WindowOptions{Size: platform.Size{W: 100, H: 100}})
	if err != nil {
		t.Fatal(err)
	}
	f.wait("get_toplevel_decoration")
	f.wait("set_mode")
	f.wait("commit") // the mode is set before the first commit
	f.mu.Lock()
	if f.decorationMode != decorationModeClient {
		t.Errorf("mode = %d, want client-side (%d)", f.decorationMode, decorationModeClient)
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

// configureStates sends a toplevel configure with the given states, and the
// xdg_surface configure that applies it.
func (f *fakeWM) configureStates(s *server, w, h int32, serial uint32, states ...uint32) {
	f.mu.Lock()
	top, xdg := f.toplevel, f.xdgSurface
	f.mu.Unlock()
	var arr []byte
	for _, st := range states {
		arr = order.AppendUint32(arr, st)
	}
	s.send(top, toplevelConfigure, func(e *Encoder) { e.Int(w).Int(h).Array(arr) })
	s.send(xdg, xdgSurfaceConfigure, func(e *Encoder) { e.Uint(serial) })
}

func TestACompositorThatDecoratesIsReported(t *testing.T) {
	be, f, srv := wmSetup(t, decorationGlobals)
	win, _ := be.NewWindow(platform.WindowOptions{Size: platform.Size{W: 100, H: 100}})
	f.wait("commit")
	f.mu.Lock()
	deco := f.decoration
	f.mu.Unlock()
	srv.send(deco, decorationConfigure, func(e *Encoder) { e.Uint(decorationModeServer) })
	srv.send(deco, 7, nil) // an event this client does not use
	f.configure(srv, 100, 100, 1)
	if ev := next(t, win); ev != (platform.StateEvent{Decorated: true}) {
		t.Errorf("first event = %#v, want a decorated state", ev)
	}
	if ev := next(t, win); ev != (platform.ResizeEvent{Size: platform.Size{W: 100, H: 100}, Scale: 1}) {
		t.Errorf("then %#v, want the resize", ev)
	}
	next(t, win) // the frame
	// Client-side again: the state goes back to the zero one.
	srv.send(deco, decorationConfigure, func(e *Encoder) { e.Uint(decorationModeClient) })
	f.configure(srv, 100, 100, 2)
	if ev := next(t, win); ev != (platform.StateEvent{}) {
		t.Errorf("after the switch back: %#v", ev)
	}
}

func TestMaximizedAndFullscreenStatesAreReportedWhenTheyChange(t *testing.T) {
	_, f, srv, win := openWindow(t, 100, 100) // starts in the zero state: no StateEvent
	f.configureStates(srv, 200, 100, 6, toplevelStateMaximized, 4)
	if ev := next(t, win); ev != (platform.StateEvent{Maximized: true}) {
		t.Errorf("maximised: %#v", ev)
	}
	next(t, win) // resize
	next(t, win) // frame
	skipFrame(win)
	// Full screen counts as maximised, so nothing changes.
	f.configureStates(srv, 200, 100, 7, toplevelStateFullscreen)
	if ev := next(t, win); ev != (platform.ResizeEvent{Size: platform.Size{W: 200, H: 100}, Scale: 1}) {
		t.Errorf("full screen: %#v, want only the resize", ev)
	}
	next(t, win)
	skipFrame(win)
	f.configureStates(srv, 100, 100, 8, 4) // activated only
	if ev := next(t, win); ev != (platform.StateEvent{}) {
		t.Errorf("restored: %#v", ev)
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

func TestActivateAsksTheCompositorToRaiseTheWindow(t *testing.T) {
	globals := append(append([]Global(nil), wmGlobals...), Global{Name: 10, Interface: "xdg_activation_v1", Version: 1})
	be, f, _ := wmSetup(t, globals)
	win, err := be.NewWindow(platform.WindowOptions{Size: platform.Size{W: 100, H: 100}})
	if err != nil {
		t.Fatal(err)
	}
	f.wait("commit")
	win.Activate("") // an empty token asks the compositor for nothing
	win.Activate("token-1")
	f.wait("activate")
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.activations) != 1 || f.activations[0] != (activation{"token-1", f.surface}) {
		t.Errorf("activations = %+v, want token-1 for surface %d", f.activations, f.surface)
	}
}

func TestActivateDoesNothingWithoutTheProtocol(t *testing.T) {
	be, f, _ := wmSetup(t, wmGlobals)
	win, _ := be.NewWindow(platform.WindowOptions{Size: platform.Size{W: 100, H: 100}})
	f.wait("commit")
	win.Activate("token")
	time.Sleep(30 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.activations) != 0 {
		t.Errorf("activation requested from a compositor without the protocol: %+v", f.activations)
	}
}
