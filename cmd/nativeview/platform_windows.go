package main

import (
	"io"

	"viewrab/internal/ui/platform/win32"
)

// openBackend opens the Win32 backend, the only one Windows has.
func openBackend(func(string) string, io.Writer) (backend, error) { return win32.New() }

// activationToken returns the token a launcher gave this process: none, as
// Windows has no such token.
func activationToken() string { return "" }

// allowActivation lets the running instance, which a launch may hand its
// files to, bring its window to the front.
func allowActivation() { win32.AllowForeground() }

// pickFiles shows the Open dialog and returns the PDFs chosen.
func pickFiles() ([]string, error) { return win32.PickPDFs("Open PDF") }

// saveFile asks with the Save dialog where to save a converted PDF,
// suggesting suggested, and returns the path chosen, or "" if none was.
func saveFile(suggested string) (string, error) {
	return win32.SavePDF("Save converted PDF", suggested)
}

// launch opens uri in the program the user picked for it; tests replace it.
var launch = func(uri string) { win32.Open(uri) }

// watchScheme reads whether the user prefers dark programs into t and then
// follows the choice, sending the theme to show on out whenever the window
// should follow a change.
func watchScheme(t *themes, out chan<- bool) {
	t.setSystem(win32.PrefersDark())
	win32.WatchDarkMode(func(dark bool) {
		if t.systemChanged(dark) {
			out <- dark
		}
	})
}
