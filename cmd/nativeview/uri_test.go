package main

import (
	"slices"
	"testing"
)

func TestOpenURIOpensOnlySafeSchemes(t *testing.T) {
	var opened []string
	old := launch
	launch = func(uri string) { opened = append(opened, uri) }
	defer func() { launch = old }()
	for _, uri := range []string{
		"https://example.com/a?b=c", "http://example.com", "mailto:someone@example.com",
		"file:///etc/passwd", "file:///C:/Windows/System32/calc.exe", "javascript:alert(1)",
		"ssh://host", "relative/path", "", "%zz",
	} {
		openURI(uri)
	}
	want := []string{"https://example.com/a?b=c", "http://example.com", "mailto:someone@example.com"}
	if !slices.Equal(opened, want) {
		t.Errorf("opened %q, want %q", opened, want)
	}
}
