package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"viewer/internal/ui/platform"
	"viewer/internal/ui/platform/wayland"
	"viewer/internal/ui/platform/x11"
)

// backend is a windowing system the viewer can run on.
type backend interface {
	platform.Platform
	Close() error
}

// Constructors of the backends, replaced by tests.
var (
	newWayland = func() (backend, error) { return wayland.New() }
	newX11     = func() (backend, error) { return x11.New() }
)

// openBackend connects to the desktop's windowing system. VIEWER_BACKEND
// names one, "wayland" or "x11". Otherwise Wayland is tried when
// WAYLAND_DISPLAY is set or DISPLAY is not, and X11 when DISPLAY is set. X11
// also serves a Wayland desktop through Xwayland.
//
// A Wayland session that cannot be used, but whose Xwayland can, is reported
// on warn, since the window then runs through Xwayland.
func openBackend(getenv func(string) string, warn io.Writer) (backend, error) {
	switch name := strings.ToLower(getenv("VIEWER_BACKEND")); name {
	case "wayland":
		return newWayland()
	case "x11":
		return newX11()
	case "":
	default:
		return nil, fmt.Errorf("unknown VIEWER_BACKEND %q: want wayland or x11", name)
	}
	var errs []error
	if getenv("WAYLAND_DISPLAY") != "" || getenv("DISPLAY") == "" {
		be, err := newWayland()
		if err == nil {
			return be, nil
		}
		errs = append(errs, err)
	}
	if getenv("DISPLAY") != "" {
		be, err := newX11()
		if err == nil {
			if len(errs) > 0 {
				fmt.Fprintf(warn, "nativeview: Wayland is unavailable (%v); using X11\n", errs[0])
			}
			return be, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

// activationToken returns the token a launcher gave this process, which lets
// the running instance bring its window forward: Wayland's activation token,
// or X11's startup notification ID.
func activationToken() string {
	if t := os.Getenv("XDG_ACTIVATION_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("DESKTOP_STARTUP_ID")
}
