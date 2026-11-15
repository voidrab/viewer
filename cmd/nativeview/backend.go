package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"viewrab/internal/ui/platform"
	"viewrab/internal/ui/platform/wayland"
	"viewrab/internal/ui/platform/x11"
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

// openBackend connects to the desktop's windowing system. VIEWRAB_BACKEND
// names one, "wayland" or "x11". Otherwise Wayland is tried when
// WAYLAND_DISPLAY is set or DISPLAY is not, and X11 when DISPLAY is set. X11
// also serves a Wayland desktop through Xwayland.
//
// When Wayland fails and X11 then connects, the Wayland error is written to
// warn, since the window then runs through Xwayland.
func openBackend(getenv func(string) string, warn io.Writer) (backend, error) {
	switch name := strings.ToLower(getenv("VIEWRAB_BACKEND")); name {
	case "wayland":
		return newWayland()
	case "x11":
		return newX11()
	case "":
	default:
		return nil, fmt.Errorf("unknown VIEWRAB_BACKEND %q: want wayland or x11", name)
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
