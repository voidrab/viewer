package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// The file the user's choice is kept in, and the values it holds.
const (
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
		t.path = filepath.Join(dir, "viewrab", themeFileName)
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
