// Command nativeview opens a PDF in a pure-Go window: no webview, no cgo. It
// is the native viewer under construction and only runs on Wayland for now.
package main

import (
	"fmt"
	"image"
	"os"
	"path/filepath"

	"github.com/voidrab/gopdfrab"

	"viewer/internal/ui/pageview"
	"viewer/internal/ui/platform"
	"viewer/internal/ui/platform/wayland"
	"viewer/internal/ui/shell"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: nativeview file.pdf")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "nativeview:", err)
		os.Exit(1)
	}
}

func run(path string) error {
	doc, err := gopdfrab.Open(path)
	if err != nil {
		return err
	}
	defer doc.Close()
	be, err := wayland.New()
	if err != nil {
		return err
	}
	defer be.Close()
	win, err := be.NewWindow(platform.WindowOptions{
		Title:   filepath.Base(path),
		AppID:   "com.voidrab.gopdfrab.Viewer",
		Size:    platform.Size{W: 900, H: 700},
		MinSize: platform.Size{W: 360, H: 240},
	})
	if err != nil {
		return err
	}
	return shell.Run(win, func(size image.Point, invalidate func()) (*pageview.View, error) {
		return pageview.New(doc, size, pageview.Options{}, invalidate)
	})
}
