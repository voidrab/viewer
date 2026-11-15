package main

import "net/url"

// openURI opens the address of a link in the desktop's default program for it.
// Only addresses of the kinds a document may sensibly link to are opened:
// a link must not be able to start an arbitrary program.
func openURI(uri string) {
	u, err := url.Parse(uri)
	if err != nil {
		return
	}
	switch u.Scheme {
	case "http", "https", "mailto":
		launch(uri)
	}
}
