//go:build !windows

package main

import (
	"errors"
	"os"
	"testing"
)

func TestLaunchUsesXdgOpen(t *testing.T) {
	var started [][]string
	old := startCommand
	startCommand = func(name string, args ...string) error {
		started = append(started, append([]string{name}, args...))
		return nil
	}
	defer func() { startCommand = old }()
	launch("https://example.com")
	if len(started) != 1 || started[0][0] != "xdg-open" || started[0][1] != "https://example.com" {
		t.Errorf("started %v", started)
	}
	// A failing command is not an error for the caller.
	startCommand = func(string, ...string) error { return errors.New("boom") }
	launch("https://example.com")
}

func TestStartCommandRunsAndReapsAProcess(t *testing.T) {
	if _, err := os.Stat("/bin/true"); err != nil {
		t.Skip("no /bin/true")
	}
	if err := startCommand("/bin/true"); err != nil {
		t.Errorf("starting /bin/true: %v", err)
	}
	if err := startCommand("/nonexistent/program"); err == nil {
		t.Error("starting a missing program succeeded")
	}
}
