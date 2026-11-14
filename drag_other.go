//go:build !linux

package main

// watchDragCancels does nothing: other platforms' webviews fire dragend for
// cancelled drags themselves.
func watchDragCancels(func(byUser bool)) {}
