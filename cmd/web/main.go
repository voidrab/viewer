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
	"slices"
	"sync"
	"sync/atomic"

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
	// The heap is mostly bitmaps, which the collector does not scan, so
	// frequent collections cost little.
	debug.SetGCPercent(25)
	if web.IsRenderer() {
		serveRenders()
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "viewrab:", err)
		web.Failed(err)
		return
	}
	web.Exited()
}

func run() error {
	win, err := web.New().NewWindow(platform.WindowOptions{Title: "Viewrab"})
	if err != nil {
		return err
	}
	paths := fetchAll(web.Params("file"))
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
	web.Started()
	opts := app.Options{
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
	}
	// Pages render in the page's render workers, so the viewer's own worker
	// stays free to scroll. Without render workers, the viewer renders the
	// pages itself.
	if n := web.RenderWorkers(); n > 0 {
		opts.Render, opts.RenderWorkers = web.RenderPage, n
	}
	return app.Run(win, paths, opts)
}

// fetchAll fetches the files at urls, all at once, and returns their paths in
// the in-memory file system in the order of urls. A file that cannot be
// fetched is reported on the console and skipped.
func fetchAll(urls []string) []string {
	got := make([]string, len(urls))
	var wg sync.WaitGroup
	for i, url := range urls {
		wg.Go(func() {
			p, err := web.Fetch(url)
			if err != nil {
				fmt.Fprintln(os.Stderr, "viewrab: fetching", url+":", err)
				return
			}
			got[i] = p
		})
	}
	wg.Wait()
	return slices.DeleteFunc(got, func(p string) bool { return p == "" })
}
