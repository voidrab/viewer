package main

import (
	"errors"
	"strings"
	"testing"

	"viewrab/internal/ui/platform"
)

// fakeBackend stands in for a connected backend.
type fakeBackend struct{ name string }

func (fakeBackend) NewWindow(platform.WindowOptions) (platform.Window, error) { return nil, nil }
func (fakeBackend) Close() error                                              { return nil }

func TestOpenBackendPicksBySessionAndOverride(t *testing.T) {
	waylandErr, x11Err := errors.New("no wayland"), errors.New("no x11")
	var waylandOK, x11OK bool
	newWayland = func() (backend, error) {
		if !waylandOK {
			return nil, waylandErr
		}
		return fakeBackend{"wayland"}, nil
	}
	newX11 = func() (backend, error) {
		if !x11OK {
			return nil, x11Err
		}
		return fakeBackend{"x11"}, nil
	}
	t.Cleanup(func() {
		newWayland = func() (backend, error) { return nil, nil }
		newX11 = newWayland
	})
	for _, tc := range []struct {
		env              map[string]string
		waylandOK, x11OK bool
		want, warn       string
		err              error
	}{
		{env: map[string]string{"WAYLAND_DISPLAY": "w", "DISPLAY": ":0"}, waylandOK: true, x11OK: true, want: "wayland"},
		{env: map[string]string{"WAYLAND_DISPLAY": "w", "DISPLAY": ":0"}, x11OK: true, want: "x11", warn: "no wayland"},
		{env: map[string]string{"DISPLAY": ":0"}, waylandOK: true, x11OK: true, want: "x11"},
		{env: map[string]string{}, waylandOK: true, want: "wayland"},
		{env: map[string]string{"WAYLAND_DISPLAY": "w", "DISPLAY": ":0"}, err: x11Err},
		{env: map[string]string{"VIEWRAB_BACKEND": "X11", "WAYLAND_DISPLAY": "w"}, waylandOK: true, x11OK: true, want: "x11"},
		{env: map[string]string{"VIEWRAB_BACKEND": "wayland", "DISPLAY": ":0"}, waylandOK: true, x11OK: true, want: "wayland"},
		{env: map[string]string{"VIEWRAB_BACKEND": "mir"}, err: errors.New("unknown")},
	} {
		waylandOK, x11OK = tc.waylandOK, tc.x11OK
		var warn strings.Builder
		be, err := openBackend(func(k string) string { return tc.env[k] }, &warn)
		switch {
		case tc.err != nil:
			if err == nil || !strings.Contains(err.Error(), tc.err.Error()) {
				t.Errorf("%v: err = %v, want %v", tc.env, err, tc.err)
			}
		case err != nil || be.(fakeBackend).name != tc.want:
			t.Errorf("%v: got %v, %v; want %s", tc.env, be, err, tc.want)
		}
		if !strings.Contains(warn.String(), tc.warn) || tc.warn == "" && warn.Len() > 0 {
			t.Errorf("%v: warning %q, want %q", tc.env, warn.String(), tc.warn)
		}
	}
}

func TestActivationTokenPrefersWayland(t *testing.T) {
	t.Setenv("XDG_ACTIVATION_TOKEN", "wl")
	t.Setenv("DESKTOP_STARTUP_ID", "x")
	if got := activationToken(); got != "wl" {
		t.Errorf("token = %q", got)
	}
	t.Setenv("XDG_ACTIVATION_TOKEN", "")
	if got := activationToken(); got != "x" {
		t.Errorf("token = %q", got)
	}
}
