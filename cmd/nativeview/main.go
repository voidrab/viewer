// Command nativeview opens PDFs in a pure-Go window: no webview and no cgo,
// using only the Wayland platform backend. Every argument opens in its own
// tab, in the window of the instance already running if there is one.
package main

import (
	"fmt"
	"os"

	"viewer/internal/ui/app"
	"viewer/internal/ui/instance"
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
	// A second launch hands its files to the running window and ends. Without
	// a usable socket the program simply runs on its own.
	var requests <-chan instance.Request
	srv, forwarded, err := instance.Claim(instance.DefaultPath("viewer-native"), instance.Request{
		Paths: paths,
		Token: os.Getenv("XDG_ACTIVATION_TOKEN"),
	})
	if forwarded {
		return nil
	}
	if err == nil {
		defer srv.Close()
		requests = srv.Requests()
	}
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
	return app.Run(win, paths, app.Options{PickFiles: pickFiles, Requests: requests, OpenURI: openURI})
}
