//go:build !linux

package main

import "unsafe"

// releaseWindowRef does nothing: the reference leak it works around is in
// Wails's GTK backend only.
func releaseWindowRef(unsafe.Pointer) {}
