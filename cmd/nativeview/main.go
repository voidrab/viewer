// Command nativeview opens PDFs in a pure-Go window: no webview and no cgo,
// using only the Wayland platform backend. Every argument opens in its own
// tab.
package main

import (
	"fmt"
	"os"

	"viewer/internal/ui/app"
	"viewer/internal/ui/platform"
	"viewer/internal/ui/platform/wayland"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "nativeview:", err)
		os.Exit(1)
	}
}

func run(paths []string) error {
	be, err := wayland.New()
	if err != nil {
		return err
	}
	defer be.Close()
	win, err := be.NewWindow(platform.WindowOptions{
		Title:   "Viewer",
		AppID:   "com.voidrab.gopdfrab.Viewer",
		Size:    platform.Size{W: 1100, H: 800},
		MinSize: platform.Size{W: 480, H: 360},
	})
	if err != nil {
		return err
	}
	return app.Run(win, paths, app.Options{})
}
