package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestThemesFollowTheDesktopUntilTheUserChooses(t *testing.T) {
	dir := t.TempDir()
	th := newThemes(dir)
	// Nothing known: light.
	if th.dark() {
		t.Error("dark with nothing known")
	}
	// The desktop prefers dark: so does the window, and follows its changes.
	th.setSystem(true, true)
	if !th.dark() {
		t.Error("not dark with a dark desktop")
	}
	if !th.systemChanged(false) || th.dark() {
		t.Error("did not follow the desktop to light")
	}
	// Choosing dark, against the light desktop, is kept and no longer
	// follows the desktop.
	if err := th.save(true); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "viewer", "theme")); err != nil || string(b) != "dark\n" {
		t.Errorf("kept %q, %v", b, err)
	}
	if !th.dark() || th.systemChanged(false) {
		t.Error("the choice did not hold against the desktop")
	}
	// Choosing light against a dark desktop is kept too.
	th.setSystem(true, true)
	if err := th.save(false); err != nil {
		t.Fatal(err)
	}
	if th.dark() {
		t.Error("the light choice did not hold")
	}
	// Saving the desktop's theme forgets the choice; saving it again changes
	// nothing.
	if err := th.save(true); err != nil {
		t.Fatal(err)
	}
	if err := th.save(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "viewer", "theme")); !os.IsNotExist(err) {
		t.Errorf("the choice was not forgotten: %v", err)
	}
	if !th.systemChanged(true) {
		t.Error("the window does not follow the desktop again")
	}
}

func TestThemesWithNowhereToKeepTheChoice(t *testing.T) {
	th := newThemes("")
	if err := th.save(true); err != nil {
		t.Fatal(err)
	}
	if _, ok := th.chosen(); ok {
		t.Error("a choice was kept with nowhere to keep it")
	}
}

func TestThemesIgnoreAnUnreadableChoice(t *testing.T) {
	dir := t.TempDir()
	th := newThemes(dir)
	path := filepath.Join(dir, "viewer", "theme")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("purple"), 0o644)
	if _, ok := th.chosen(); ok {
		t.Error("an unknown theme counts as a choice")
	}
	// A configuration directory that cannot be written reports the error.
	blocked := newThemes(filepath.Join(path, "below-a-file"))
	if err := blocked.save(true); err == nil {
		t.Error("saving under a file did not fail")
	}
	// A choice that cannot be removed reports the error too.
	ro := newThemes(dir)
	ro.path = filepath.Join(dir, "viewer") // a directory that is not empty
	ro.setSystem(true, true)
	if err := ro.save(true); err == nil {
		t.Error("forgetting a choice that cannot be removed did not fail")
	}
}

func TestSchemeIsDark(t *testing.T) {
	for _, tc := range []struct {
		v        any
		dark, ok bool
	}{
		{uint32(1), true, true},
		{uint32(2), false, true},
		{uint32(0), false, true},
		{dbus.MakeVariant(uint32(1)), true, true},
		{dbus.MakeVariant(dbus.MakeVariant(uint32(1))), true, true},
		{dbus.MakeVariant("dark"), false, false},
		{nil, false, false},
	} {
		if dark, ok := schemeIsDark(tc.v); dark != tc.dark || ok != tc.ok {
			t.Errorf("schemeIsDark(%v) = %v, %v", tc.v, dark, ok)
		}
	}
}
