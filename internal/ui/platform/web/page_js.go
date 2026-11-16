//go:build js && wasm

package web

import (
	"errors"
	"syscall/js"
)

// host is the object web/worker.js gives the program: its in-memory file
// system, the canvas and what the page told the worker at the start, and the
// way to the page's thread for what only the page can do.
func host() js.Value { return js.Global().Get("viewrab") }

// toPage asks the page's thread to do op, with arg, without waiting.
func toPage(op string, arg any) { host().Call("toPage", op, arg) }

// Await waits for promise to settle and returns its value, or its reason as
// an error. It blocks the calling goroutine, so it must not be called from a
// JavaScript callback, which would then never return to the event loop.
func Await(promise js.Value) (js.Value, error) {
	type result struct {
		v   js.Value
		err error
	}
	c := make(chan result, 1)
	then := js.FuncOf(func(_ js.Value, args []js.Value) any {
		c <- result{v: arg(args)}
		return nil
	})
	defer then.Release()
	catch := js.FuncOf(func(_ js.Value, args []js.Value) any {
		reason := arg(args)
		msg := "rejected"
		if reason.Truthy() {
			msg = reason.Call("toString").String()
		}
		c <- result{err: errors.New(msg)}
		return nil
	})
	defer catch.Release()
	promise.Call("then", then, catch)
	r := <-c
	return r.v, r.err
}

func arg(args []js.Value) js.Value {
	if len(args) == 0 {
		return js.Undefined()
	}
	return args[0]
}

// stringsOf converts a JavaScript array of strings.
func stringsOf(a js.Value) []string {
	out := make([]string, a.Length())
	for i := range out {
		out[i] = a.Index(i).String()
	}
	return out
}

// ImportFiles copies the browser's files, a FileList or an array of File, into
// the in-memory file system and returns their paths there.
func ImportFiles(files js.Value) ([]string, error) {
	v, err := Await(host().Call("importFiles", files))
	if err != nil {
		return nil, err
	}
	return stringsOf(v), nil
}

// PickPDFs shows the browser's file chooser for PDFs and returns the paths of
// the files chosen, copied into the in-memory file system; none if the user
// cancels.
func PickPDFs() ([]string, error) {
	v, err := Await(host().Call("pickFiles"))
	if err != nil {
		return nil, err
	}
	return stringsOf(v), nil
}

// Fetch downloads the file at url into the in-memory file system and returns
// its path there.
func Fetch(url string) (string, error) {
	v, err := Await(host().Call("fetchFile", url))
	if err != nil {
		return "", err
	}
	return v.String(), nil
}

// Download hands the file at path, in the in-memory file system, to the
// browser to save among the user's downloads.
func Download(path string) { host().Call("download", path) }

// OpenURI opens uri in a new browser tab.
func OpenURI(uri string) { toPage("open", uri) }

// Params returns the values of the page address's query parameter name, as
// absolute addresses when they are addresses.
func Params(name string) []string {
	v := host().Get("params").Get(name)
	if !v.Truthy() {
		return nil
	}
	return stringsOf(v)
}

// PrefersDark reports whether the user's system preferred dark pages when the
// page started.
func PrefersDark() bool { return host().Get("prefersDark").Bool() }

// WatchDark calls f with whether the user's system prefers dark pages each
// time the preference changes.
func WatchDark(f func(dark bool)) {
	host().Call("onDark", js.FuncOf(func(_ js.Value, args []js.Value) any {
		dark := args[0].Bool()
		go f(dark) // f may block, as a send to the application does
		return nil
	}))
}

// Stored returns the value kept under key in the page's local storage, and
// whether one is. The worker cannot reach the storage; it has the copy the
// page gave it at the start, which Store keeps up to date.
func Stored(key string) (string, bool) {
	v := host().Get("storage").Get(key)
	if v.Type() != js.TypeString {
		return "", false
	}
	return v.String(), true
}

// Store keeps value under key in the page's local storage, or forgets the key
// when value is empty. Storage the browser refuses keeps nothing.
func Store(key, value string) { host().Call("store", key, value) }
