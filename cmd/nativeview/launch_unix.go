//go:build !windows

package main

import "os/exec"

// startCommand starts name with args without waiting for it; tests replace it.
var startCommand = func(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // so that the finished process does not linger
	return nil
}

// launch opens uri with xdg-open; tests replace it.
var launch = func(uri string) { startCommand("xdg-open", uri) }
