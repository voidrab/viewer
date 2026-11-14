package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// The desktop's colour scheme is read from the settings portal: the
// org.freedesktop.appearance namespace's color-scheme key, which is 1 when the
// user prefers dark, 2 when they prefer light, and 0 when they have no
// preference.
const (
	settingsIface   = "org.freedesktop.portal.Settings"
	appearance      = "org.freedesktop.appearance"
	colorSchemeKey  = "color-scheme"
	settingChanged  = settingsIface + ".SettingChanged"
	schemeDark      = 1
	themeFileName   = "theme"
	themeDarkValue  = "dark"
	themeLightValue = "light"
)

// themes decides the window's theme: the user's own choice if they made one
// with the theme switch, or else the desktop's. A choice is kept in a file in
// the user's configuration directory; switching back to the desktop's theme
// forgets it, so that the window follows the desktop again.
type themes struct {
	path string // the file the choice is kept in, or "" for nowhere

	mu     sync.Mutex
	system bool // the desktop prefers dark
	known  bool // the desktop's preference could be read
}

// newThemes returns themes keeping the user's choice under the configuration
// directory dir, typically from os.UserConfigDir; "" keeps it nowhere.
func newThemes(dir string) *themes {
	t := &themes{}
	if dir != "" {
		t.path = filepath.Join(dir, "viewer", themeFileName)
	}
	return t
}

// chosen returns the theme the user chose, and whether they chose one.
func (t *themes) chosen() (dark, ok bool) {
	if t.path == "" {
		return false, false
	}
	b, err := os.ReadFile(t.path)
	if err != nil {
		return false, false
	}
	switch strings.TrimSpace(string(b)) {
	case themeDarkValue:
		return true, true
	case themeLightValue:
		return false, true
	}
	return false, false
}

// setSystem records the desktop's preference, and whether it is known.
func (t *themes) setSystem(dark, known bool) {
	t.mu.Lock()
	t.system, t.known = dark, known
	t.mu.Unlock()
}

// dark returns the theme to show: the user's choice, or else the desktop's,
// light when neither is known.
func (t *themes) dark() bool {
	if dark, ok := t.chosen(); ok {
		return dark
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.system
}

// save keeps the user's choice of theme, or forgets it if it is the desktop's.
func (t *themes) save(dark bool) error {
	if t.path == "" {
		return nil
	}
	t.mu.Lock()
	follows := t.known && dark == t.system
	t.mu.Unlock()
	if follows {
		if err := os.Remove(t.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(t.path), 0o755); err != nil {
		return err
	}
	value := themeLightValue
	if dark {
		value = themeDarkValue
	}
	return os.WriteFile(t.path, []byte(value+"\n"), 0o644)
}

// systemChanged records a change of the desktop's preference and reports
// whether the window should follow it: when the user has not chosen a theme
// of their own.
func (t *themes) systemChanged(dark bool) bool {
	t.setSystem(dark, true)
	_, chosen := t.chosen()
	return !chosen
}

// schemeIsDark decodes a color-scheme value as the portal returns it, a
// variant holding a uint32 (or, from the older Read method, a variant inside
// a variant). It reports whether the scheme is dark, and whether the value had
// that shape.
func schemeIsDark(v any) (dark, ok bool) {
	for {
		inner, isVariant := v.(dbus.Variant)
		if !isVariant {
			break
		}
		v = inner.Value()
	}
	n, ok := v.(uint32)
	return n == schemeDark, ok
}

// readScheme asks the settings portal for the desktop's colour scheme. It
// reports whether the scheme is dark, and whether it could be read.
// It waits at most schemeTimeout, since the window does not open until then.
func readScheme(conn *dbus.Conn) (dark, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), schemeTimeout)
	defer cancel()
	obj := conn.Object(portalName, portalPath)
	var v dbus.Variant
	if err := obj.CallWithContext(ctx, settingsIface+".ReadOne", 0, appearance, colorSchemeKey).Store(&v); err != nil {
		// Portals before version 2 only have the deprecated Read.
		if err := obj.CallWithContext(ctx, settingsIface+".Read", 0, appearance, colorSchemeKey).Store(&v); err != nil {
			return false, false
		}
	}
	return schemeIsDark(v)
}

// schemeTimeout bounds the wait for the desktop's colour scheme at startup.
const schemeTimeout = 300 * time.Millisecond

// watchScheme reads the desktop's colour scheme into t and then follows its
// changes, sending the theme to show on out whenever the window should follow
// one. It keeps a session bus connection open for as long as the program
// runs; without a bus or a portal, the theme stays as it is.
func watchScheme(t *themes, out chan<- bool) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return
	}
	t.setSystem(readScheme(conn))
	if conn.AddMatchSignal(
		dbus.WithMatchObjectPath(portalPath),
		dbus.WithMatchInterface(settingsIface),
		dbus.WithMatchMember("SettingChanged"),
		dbus.WithMatchArg(0, appearance),
		dbus.WithMatchArg(1, colorSchemeKey),
	) != nil {
		conn.Close()
		return
	}
	signals := make(chan *dbus.Signal, 4)
	conn.Signal(signals)
	go func() {
		for sig := range signals {
			if sig.Name != settingChanged || len(sig.Body) != 3 {
				continue
			}
			if ns, _ := sig.Body[0].(string); ns != appearance {
				continue
			}
			if key, _ := sig.Body[1].(string); key != colorSchemeKey {
				continue
			}
			if dark, ok := schemeIsDark(sig.Body[2]); ok && t.systemChanged(dark) {
				out <- dark
			}
		}
	}()
}
