package main

import "viewrab/internal/ui/platform"

// backend is a windowing system the viewer can run on.
type backend interface {
	platform.Platform
	Close() error
}
