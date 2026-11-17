//go:build js && wasm

package web

import (
	"context"
	"errors"
	"image"
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

// paths calls the host's op with args and waits for the paths it promises.
func paths(op string, args ...any) ([]string, error) {
	v, err := Await(host().Call(op, args...))
	if err != nil {
		return nil, err
	}
	return stringsOf(v), nil
}

// ImportFiles copies the browser's files, a FileList or an array of File, into
// the in-memory file system and returns their paths there.
func ImportFiles(files js.Value) ([]string, error) { return paths("importFiles", files) }

// PickPDFs shows the browser's file chooser for PDFs and returns the paths of
// the files chosen, copied into the in-memory file system; none if the user
// cancels.
func PickPDFs() ([]string, error) { return paths("pickFiles") }

// Fetch downloads the file at url into the in-memory file system and returns
// its path there.
func Fetch(url string) (string, error) {
	v, err := Await(host().Call("fetchFile", url))
	if err != nil {
		return "", err
	}
	return v.String(), nil
}

// FetchFont downloads gopdfrab's substitute font file name, which the web
// build leaves out of the program, from the fonts directory next to the
// page. The browser's cache shares it between the workers. Like Await, it
// blocks the calling goroutine.
func FetchFont(name string) ([]byte, error) {
	v, err := Await(host().Call("fetchFont", name))
	if err != nil {
		return nil, err
	}
	return bytesOf(v), nil
}

// bytesOf copies the bytes of a Uint8Array.
func bytesOf(a js.Value) []byte {
	b := make([]byte, a.Length())
	js.CopyBytesToGo(b, a)
	return b
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

// RenderWorkers returns how many render workers the page started for the
// program, zero if it could start none.
func RenderWorkers() int { return host().Get("renderers").Int() }

// RenderPage renders the part region of page, counted from zero, of the
// document at path at scale in one of the page's render workers, and returns
// the pixels gopdfrab's Page.Render returns. Each render worker has its own
// copy of the file. The calling goroutine waits, and the program's worker
// stays free. If ctx is done first, the job is dropped and RenderPage returns
// ctx.Err(); a render already running finishes, and its result is discarded.
func RenderPage(ctx context.Context, path string, page int, scale float64, region image.Rectangle) (*image.RGBA, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	job := host().Call("renderPage", path, page, scale, region.Min.X, region.Min.Y, region.Max.X, region.Max.Y)
	id := job.Get("id")
	// Cancelling drops the job, which settles its promise at once and ends
	// the wait.
	stop := context.AfterFunc(ctx, func() { host().Call("cancelRender", id) })
	defer stop()
	v, err := Await(job.Get("promise"))
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return decodeImage(v), nil
}

// decodeImage reads the image a render worker sends: its rectangle, stride
// and pixels.
func decodeImage(v js.Value) *image.RGBA {
	img := &image.RGBA{
		Rect:   image.Rect(v.Get("x0").Int(), v.Get("y0").Int(), v.Get("x1").Int(), v.Get("y1").Int()),
		Stride: v.Get("stride").Int(),
	}
	img.Pix = bytesOf(v.Get("pix"))
	return img
}

// EncodeImage converts img into the object that a render worker sends back.
// decodeImage reads it.
func EncodeImage(img *image.RGBA) js.Value {
	pix := js.Global().Get("Uint8Array").New(len(img.Pix))
	js.CopyBytesToJS(pix, img.Pix)
	return js.ValueOf(map[string]any{
		"x0": img.Rect.Min.X, "y0": img.Rect.Min.Y, "x1": img.Rect.Max.X, "y1": img.Rect.Max.Y,
		"stride": img.Stride, "pix": pix,
	})
}

// IsRenderer reports whether the program runs as one of the page's render
// workers rather than as the viewer.
func IsRenderer() bool {
	return host().Get("role").String() == "renderer"
}

// RenderJob describes a render that a render worker is asked to perform.
type RenderJob struct {
	ID        int
	Path, Key string // the file, and the version of it the job is for
	Page      int
	Scale     float64
	Region    image.Rectangle
}

// RenderJobs returns a channel of the renders this render worker is asked to
// perform. The worker runs one job at a time: the page sends the next once
// Finish answers the last.
func RenderJobs() <-chan RenderJob {
	jobs := make(chan RenderJob, 16)
	host().Call("onJob", js.FuncOf(func(_ js.Value, args []js.Value) any {
		j := args[0]
		job := RenderJob{
			ID: j.Get("id").Int(), Path: j.Get("path").String(), Key: j.Get("key").String(),
			Page: j.Get("page").Int(), Scale: j.Get("scale").Float(),
			Region: image.Rect(j.Get("x0").Int(), j.Get("y0").Int(), j.Get("x1").Int(), j.Get("y1").Int()),
		}
		// Send from a goroutine, so that the callback does not block.
		go func() { jobs <- job }()
		return nil
	}))
	return jobs
}

// Finish answers render job id with img, or with err if it failed, and
// reports the paths of the files the render worker has closed.
func Finish(id int, img *image.RGBA, err error, dropped []string) {
	drops := make([]any, len(dropped))
	for i, p := range dropped {
		drops[i] = p
	}
	result := map[string]any{"id": id, "dropped": drops}
	if err != nil {
		result["error"] = err.Error()
	} else {
		result["image"] = EncodeImage(img)
	}
	host().Call("finish", result)
}

// Forget removes the file at path from the in-memory file system.
func Forget(path string) { host().Call("forget", path) }

// Started tells the page that the program has started.
func Started() { host().Call("started") }

// Exited tells the page that the program has ended.
func Exited() { host().Call("exited") }

// Failed tells the page that the program could not run, because of err.
func Failed(err error) { host().Call("failed", err.Error()) }
