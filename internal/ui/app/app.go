// Package app is the viewer's window: it owns the open documents and wires the
// toolbar, tab bar, page view and status bar to one platform window.
package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"time"

	"github.com/voidrab/gopdfrab"

	"viewer/internal/pdfa"
	"viewer/internal/ui/instance"
	"viewer/internal/ui/pageview"
	"viewer/internal/ui/paint"
	"viewer/internal/ui/platform"
	"viewer/internal/ui/text"
	"viewer/internal/ui/theme"
	"viewer/internal/ui/widget"
)

// messageTime is how long a status message stays up.
const messageTime = 4 * time.Second

// Options configures an App. The zero value is usable; the function fields
// exist so tests can replace the parts that touch the system.
type Options struct {
	// Palette is the colour scheme; the zero value means the light one.
	Palette theme.Palette
	// PickFiles asks the user for files to open. Nil means the platform
	// provides no file dialog.
	PickFiles func() ([]string, error)
	// Verify and Inspect default to the pdfa package's.
	Verify  func(ctx context.Context, path, profile string) (pdfa.Report, error)
	Inspect func(path string) (pdfa.Info, error)
	// Now and AfterFunc default to the time package's.
	Now       func() time.Time
	AfterFunc func(d time.Duration, f func())
	// OpenURI opens the address of a link the user follows; nil means links
	// to addresses do nothing.
	OpenURI func(uri string)
	// Requests, if not nil, delivers requests from later launches of the
	// program to open files here, which are opened in new tabs.
	Requests <-chan instance.Request
}

// close releases the document's view and file.
func (d *document) close() {
	d.view.Close()
	d.doc.Close()
}

// newFace and newView are replaced by tests to force their failures.
var (
	newFace = func(size float64) (*text.Face, error) { return text.Mono().Face(size, false) }
	newView = pageview.New
)

// document is one open PDF, shown in one tab.
type document struct {
	path, name string
	bytes      int64
	claimed    string
	doc        *gopdfrab.Document
	view       *pageview.View
}

// section is the part of the window the pointer works on.
type section int

const (
	sectionNone section = iota
	sectionToolbar
	sectionTabs
	sectionContent
)

// App is the viewer's single window.
type App struct {
	win  platform.Window
	opts Options

	face *text.Face

	// pool renders the pages of every document, under one memory budget.
	pool *pageview.Pool

	toolbar *widget.Toolbar
	tabs    *widget.TabBar
	status  *widget.StatusBar

	docs   []*document
	active int

	size    platform.Size
	canvas  *paint.Canvas
	pending []string // files to open once the window has a size

	capture section // the section a pressed pointer button belongs to
	hover   section

	title        string // the window title last set
	message      string
	messageKind  widget.MessageKind
	messageUntil time.Time

	findBar  *widget.FindBar
	findOpen bool      // the find bar is showing
	findDoc  *document // the document it searches

	picking bool        // a file dialog is open
	inbox   chan func() // work finished off the event loop, run on it
}

// New returns an App for win.
func New(win platform.Window, opts Options) (*App, error) {
	if opts.Palette == (theme.Palette{}) {
		opts.Palette = theme.Light()
	}
	if opts.Verify == nil {
		opts.Verify = pdfa.Verify
	}
	if opts.Inspect == nil {
		opts.Inspect = pdfa.Inspect
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.AfterFunc == nil {
		opts.AfterFunc = func(d time.Duration, f func()) { time.AfterFunc(d, f) }
	}
	face, err := newFace(theme.FontSize)
	if err != nil {
		return nil, err
	}
	small, err := newFace(theme.SmallFontSize)
	if err != nil {
		return nil, err
	}
	a := &App{win: win, opts: opts, face: face, pool: pageview.NewPool(pageview.PoolOptions{}), active: -1, inbox: make(chan func(), 16)}
	a.toolbar = widget.NewToolbar(opts.Palette, face)
	a.tabs = widget.NewTabBar(opts.Palette, face)
	a.status = widget.NewStatusBar(opts.Palette, small)
	a.findBar = widget.NewFindBar(opts.Palette, face)
	return a, nil
}

// Run shows paths in a new App on win and handles events until the window
// closes. It returns after closing every document.
func Run(win platform.Window, paths []string, opts Options) error {
	a, err := New(win, opts)
	if err != nil {
		win.Close()
		return err
	}
	defer a.closeAll()
	defer win.Close()
	a.pending = append(a.pending, paths...)
	events, requests := win.Events(), opts.Requests
	for {
		select {
		case ev, ok := <-events:
			if !ok || a.Handle(ev) {
				return nil
			}
		case req, ok := <-requests:
			if !ok {
				requests = nil // a closed channel would be ready for ever
				break
			}
			a.Open(req.Paths...)
			win.Activate(req.Token)
			win.Invalidate()
		case f := <-a.inbox:
			f()
			win.Invalidate()
		}
	}
}

// layout returns the rectangles of the parts of the window as it is now.
func (a *App) layout() (toolbar, tabs, content, status image.Rectangle) {
	return a.layoutFor(len(a.docs) > 0)
}

// layoutFor returns the rectangles of the parts of the window, with the tab
// bar present or not. The tab bar appears with the first document.
func (a *App) layoutFor(tabBar bool) (toolbar, tabs, content, status image.Rectangle) {
	w, h := a.size.W, a.size.H
	y := theme.ToolbarHeight
	toolbar = image.Rect(0, 0, w, y)
	if tabBar {
		tabs = image.Rect(0, y, w, y+theme.TabBarHeight)
		y += theme.TabBarHeight
	}
	// In a window too small for the chrome, the content area is empty and the
	// status line gets the height that is left, so the rectangles never invert.
	bottom := max(y, h-theme.StatusHeight)
	content = image.Rect(0, y, w, bottom)
	status = image.Rect(0, bottom, w, max(bottom, h))
	return
}

// Handle applies one event and reports whether the application should quit.
func (a *App) Handle(ev platform.Event) (quit bool) {
	switch ev := ev.(type) {
	case platform.CloseEvent:
		return true
	case platform.ResizeEvent:
		a.resize(ev.Size)
	case platform.FrameEvent:
		a.draw()
	case platform.PointerEvent:
		a.pointer(ev)
	case platform.ScrollEvent:
		o := a.origin(sectionContent)
		ev.X, ev.Y = ev.X-float64(o.X), ev.Y-float64(o.Y)
		if d := a.current(); d != nil && d.view.HandleEvent(ev) {
			a.win.Invalidate()
		}
	case platform.KeyEvent:
		return a.key(ev)
	case platform.PasteEvent:
		if a.findOpen {
			a.findAction(a.findBar.Paste(ev.Text))
			a.win.Invalidate()
		}
	}
	return false
}

// current returns the active document, or nil.
func (a *App) current() *document {
	if a.active < 0 || a.active >= len(a.docs) {
		return nil
	}
	return a.docs[a.active]
}

func (a *App) resize(size platform.Size) {
	a.size = size
	a.canvas = paint.New(size.W, size.H)
	a.toolbar.Layout(size.W)
	if len(a.pending) > 0 {
		paths := a.pending
		a.pending = nil
		a.Open(paths...)
	}
	a.afterChange()
}

// Open opens each file in its own tab, or switches to the tab already showing
// it. Files that cannot be opened are reported in the status line.
func (a *App) Open(paths ...string) {
	if a.size == (platform.Size{}) {
		a.pending = append(a.pending, paths...)
		return
	}
	for _, p := range paths {
		abs, _ := filepath.Abs(p)
		abs = cmp.Or(abs, p) // Abs fails only when the working directory is gone
		if i := a.find(abs); i >= 0 {
			a.active = i
			continue
		}
		d, err := a.load(abs)
		if err != nil {
			a.say(widget.MessageBad, fmt.Sprintf("Could not open %s: %v", filepath.Base(p), err))
			continue
		}
		a.docs = append(a.docs, d)
		a.active = len(a.docs) - 1
	}
	a.afterChange()
}

func (a *App) find(path string) int {
	for i, d := range a.docs {
		if d.path == path {
			return i
		}
	}
	return -1
}

// load opens path and builds its view.
func (a *App) load(path string) (*document, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, errors.Unwrap(err)
	}
	doc, err := gopdfrab.Open(path)
	if err != nil {
		return nil, err
	}
	// Once the document is added the tab bar is shown, so the view is sized
	// for the layout with it.
	_, _, content, _ := a.layoutFor(true)
	view, err := newView(doc, image.Pt(content.Dx(), content.Dy()), pageview.Options{Pool: a.pool, OpenURI: a.opts.OpenURI, Selection: a.opts.Palette.Selection}, a.win.Invalidate)
	if err != nil {
		doc.Close()
		return nil, err
	}
	d := &document{path: path, name: filepath.Base(path), bytes: st.Size(), doc: doc, view: view}
	if info, err := a.opts.Inspect(path); err == nil {
		d.claimed = info.Claimed
	}
	// The first pages are rendered in the background, so that the tab is ready
	// when it is shown.
	view.Prefetch()
	return d, nil
}

// closeTab closes the document in tab i.
func (a *App) closeTab(i int) {
	if i < 0 || i >= len(a.docs) {
		return
	}
	a.docs[i].close()
	a.docs = append(a.docs[:i], a.docs[i+1:]...)
	if len(a.docs) == 0 {
		a.active = -1
	} else {
		if a.active > i {
			a.active--
		}
		a.active = min(a.active, len(a.docs)-1)
	}
	a.afterChange()
}

// closeAll closes every document.
func (a *App) closeAll() {
	for _, d := range a.docs {
		d.close()
	}
	a.docs, a.active = nil, -1
	a.pool.Close()
}

// afterChange relays out what a change of tabs affects and asks for a frame.
func (a *App) afterChange() {
	a.tabs.Layout(a.size.W)
	_, _, content, _ := a.layout()
	for _, d := range a.docs {
		d.view.HandleEvent(platform.ResizeEvent{Size: platform.Size{W: content.Dx(), H: content.Dy()}, Scale: 1})
	}
	a.win.Invalidate()
}

// say shows a message in the status line for a few seconds.
func (a *App) say(kind widget.MessageKind, msg string) {
	a.message, a.messageKind = msg, kind
	a.messageUntil = a.opts.Now().Add(messageTime)
	a.opts.AfterFunc(messageTime, a.win.Invalidate)
	a.win.Invalidate()
}

// move reorders the tabs: the tab at from goes to position to.
func (a *App) move(from, to int) {
	if from < 0 || from >= len(a.docs) || to < 0 || to >= len(a.docs) || from == to {
		return
	}
	d := a.docs[from]
	cur := a.current()
	a.docs = append(a.docs[:from], a.docs[from+1:]...)
	a.docs = append(a.docs[:to], append([]*document{d}, a.docs[to:]...)...)
	for i, x := range a.docs {
		if x == cur {
			a.active = i
		}
	}
	a.afterChange()
}

// pick asks for files to open and opens them.
func (a *App) pick() {
	if a.opts.PickFiles == nil {
		a.say(widget.MessageWarn, "No file dialog yet: give the files on the command line")
		return
	}
	if a.picking {
		return // a dialog is already open
	}
	a.picking = true
	// The dialog blocks until the user answers, so it runs off the event loop,
	// which keeps repainting meanwhile. The result is opened back on the event
	// loop through inbox.
	go func() {
		paths, err := a.opts.PickFiles()
		a.inbox <- func() {
			a.picking = false
			if err != nil {
				a.say(widget.MessageBad, err.Error())
				return
			}
			a.Open(paths...)
		}
	}()
}

// verify checks the active document against PDF/A-1b in the background and
// reports the outcome in the status line.
func (a *App) verify() {
	d := a.current()
	if d == nil {
		return
	}
	a.say(widget.MessageInfo, "Verifying "+d.name+" against PDF/A-1b…")
	go func() {
		report, err := a.opts.Verify(context.Background(), d.path, "pdfa1b")
		a.inbox <- func() {
			switch {
			case err != nil:
				a.say(widget.MessageBad, err.Error())
			case report.Valid:
				a.say(widget.MessageOK, "PDF/A-1b: no issues found")
			default:
				a.say(widget.MessageBad, fmt.Sprintf("PDF/A-1b: %d issues in %d checks", report.Total, len(report.Issues)))
			}
		}
	}()
}
