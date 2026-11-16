//go:build js && wasm

// Command web is Viewrab for browsers, built to WebAssembly: the same viewer
// as the native program, run in a web worker that web/viewrab.js starts for
// the page web/index.html and drawn on the page's canvas. Files
// are opened from the browser's file chooser, dropped on the page or named by
// the page's ?file= parameters, which are fetched; converted copies are saved
// among the user's downloads.
package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"sync/atomic"
	"syscall/js"

	"viewrab/internal/pdfa"
	"viewrab/internal/ui/app"
	"viewrab/internal/ui/platform"
	"viewrab/internal/ui/platform/web"
)

// version is the program's version. The build sets it with
// -ldflags "-X main.version=…".
var version string

// themeKey is the local storage key the user's choice of theme is kept under.
const themeKey = "viewrab-theme"

func main() {
	// As in the native program: the heap is nearly all bitmaps the collector
	// does not scan, so collecting often costs little.
	debug.SetGCPercent(25)
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "viewrab:", err)
		host().Call("failed", err.Error())
		return
	}
	host().Call("exited")
}

func host() js.Value { return js.Global().Get("viewrab") }

func run() error {
	win, err := web.New().NewWindow(platform.WindowOptions{Title: "Viewrab"})
	if err != nil {
		return err
	}
	// Files the address names are fetched while the window opens; the viewer
	// reports one that cannot be opened.
	var paths []string
	for _, url := range web.Params("file") {
		p, err := web.Fetch(url)
		if err != nil {
			fmt.Fprintln(os.Stderr, "viewrab: fetching", url+":", err)
			continue
		}
		paths = append(paths, p)
	}
	// system is the system's preference, which the watch below updates
	// while SaveTheme reads it.
	var system atomic.Bool
	system.Store(web.PrefersDark())
	dark := system.Load()
	switch v, _ := web.Stored(themeKey); v {
	case "dark":
		dark = true
	case "light":
		dark = false
	}
	themes := make(chan bool, 1)
	web.WatchDark(func(d bool) {
		system.Store(d)
		if _, chosen := web.Stored(themeKey); !chosen {
			themes <- d
		}
	})
	host().Call("started")
	return app.Run(win, paths, app.Options{
		PickFiles: web.PickPDFs,
		OpenURI:   web.OpenURI,
		Dark:      dark,
		Themes:    themes,
		SaveTheme: func(d bool) {
			// Choosing the system's theme forgets the choice, so that the
			// page follows the system again.
			switch {
			case d == system.Load():
				web.Store(themeKey, "")
			case d:
				web.Store(themeKey, "dark")
			default:
				web.Store(themeKey, "light")
			}
		},
		// With no save dialog, a copy is saved next to its original in the
		// in-memory file system, and from there among the downloads.
		Convert: func(ctx context.Context, in, out, profile string) (pdfa.ConvertReport, error) {
			cr, err := pdfa.Convert(ctx, in, out, profile)
			if err == nil {
				web.Download(cr.Output)
			}
			return cr, err
		},
		Version: version,
	})
}
