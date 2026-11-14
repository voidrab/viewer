package main

import (
	"errors"
	"os"
	"testing"
)

func TestOpenURIOpensOnlySafeSchemes(t *testing.T) {
	var started [][]string
	old := startCommand
	startCommand = func(name string, args ...string) error {
		started = append(started, append([]string{name}, args...))
		return nil
	}
	defer func() { startCommand = old }()
	for _, uri := range []string{
		"https://example.com/a?b=c", "http://example.com", "mailto:someone@example.com",
		"file:///etc/passwd", "javascript:alert(1)", "ssh://host", "relative/path", "", "%zz",
	} {
		openURI(uri)
	}
	want := [][]string{
		{"xdg-open", "https://example.com/a?b=c"}, {"xdg-open", "http://example.com"}, {"xdg-open", "mailto:someone@example.com"},
	}
	if len(started) != len(want) {
		t.Fatalf("started %v, want %v", started, want)
	}
	for i := range want {
		if started[i][0] != want[i][0] || started[i][1] != want[i][1] {
			t.Errorf("started %v, want %v", started[i], want[i])
		}
	}
	// A failing command is not an error for the caller.
	startCommand = func(string, ...string) error { return errors.New("boom") }
	openURI("https://example.com")
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
