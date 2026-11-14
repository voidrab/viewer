package main

import (
	"net/url"
	"os/exec"
)

// startCommand starts name with args without waiting for it; tests replace it.
var startCommand = func(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // so that the finished process does not linger
	return nil
}

// openURI opens the address of a link in the desktop's default program for it.
// Only addresses of the kinds a document may sensibly link to are opened:
// a link must not be able to start an arbitrary program.
func openURI(uri string) {
	u, err := url.Parse(uri)
	if err != nil {
		return
	}
	switch u.Scheme {
	case "http", "https", "mailto":
		startCommand("xdg-open", uri)
	}
}
