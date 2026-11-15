// Command nativeview opens PDFs in a pure-Go window: no webview and no cgo,
// on Wayland or X11. Every argument opens in its own tab, in the window last
// used by the instance already running if there is one.
package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"viewrab/internal/ui/app"
	"viewrab/internal/ui/icon"
	"viewrab/internal/ui/instance"
	"viewrab/internal/ui/platform"
)

func main() {
	tuneGC()
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "nativeview:", err)
		os.Exit(1)
	}
}

// gcPercent is the garbage collector's target when the environment sets none.
// Nearly all of the heap is page bitmaps and decoded images, which the
// collector does not scan, so collecting four times as often as Go's default
// costs little time, while the heap may outgrow what is live by a quarter
// rather than double it.
const gcPercent = 25

// tuneGC sets gcPercent, unless GOGC or GOMEMLIMIT says how to collect.
func tuneGC() {
	if os.Getenv("GOGC") == "" && os.Getenv("GOMEMLIMIT") == "" {
		debug.SetGCPercent(gcPercent)
	}
}

// saveTheme returns the function that keeps the user's choice of theme,
// reporting on standard error a choice that could not be kept.
func saveTheme(t *themes) func(dark bool) {
	return func(dark bool) {
		if err := t.save(dark); err != nil {
			fmt.Fprintln(os.Stderr, "nativeview: keeping the theme:", err)
		}
	}
}

func run(paths []string) error {
	// A second launch hands its files to the running window and ends. Without
	// a usable socket the program simply runs on its own.
	var requests <-chan instance.Request
	srv, forwarded, err := instance.Claim(instance.DefaultPath("viewrab-native"), instance.Request{
		Paths: paths,
		Token: activationToken(),
	})
	if forwarded {
		return nil
	}
	if err == nil {
		defer srv.Close()
		requests = srv.Requests()
	}
	// The theme follows the desktop's, unless the user switched to the other
	// one; it is read before the window opens so that it opens in it.
	dir, _ := os.UserConfigDir()
	theme := newThemes(dir)
	themeChanges := make(chan bool, 1)
	watchScheme(theme, themeChanges)
	dark := theme.dark()
	be, err := openBackend(os.Getenv, os.Stderr)
	if err != nil {
		return err
	}
	defer be.Close()
	newWindow := func() (platform.Window, error) {
		return be.NewWindow(platform.WindowOptions{
			Title: "Viewrab",
			AppID: "com.voidrab.gopdfrab.Viewrab",
			Size:  platform.Size{W: 1100, H: 800},
			// Wide enough for the toolbar and its window controls.
			MinSize: platform.Size{W: 600, H: 360},
			Icon:    icon.Logo(64),
		})
	}
	win, err := newWindow()
	if err != nil {
		return err
	}
	return app.Run(win, paths, app.Options{
		PickFiles: pickFiles, SaveFile: saveFile, Requests: requests, OpenURI: openURI,
		Dark: dark, Themes: themeChanges, SaveTheme: saveTheme(theme), NewWindow: newWindow,
	})
}
