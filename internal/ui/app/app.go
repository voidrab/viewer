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
	"sync/atomic"
	"time"

	"github.com/voidrab/gopdfrab"

	"viewrab/internal/pdfa"
	"viewrab/internal/ui/instance"
	"viewrab/internal/ui/pageview"
	"viewrab/internal/ui/paint"
	"viewrab/internal/ui/platform"
	"viewrab/internal/ui/text"
	"viewrab/internal/ui/theme"
	"viewrab/internal/ui/widget"
)

// messageTime is how long a status message stays up.
const messageTime = 4 * time.Second

// Options configures an App. The zero value is usable; the function fields
// exist so tests can replace the parts that touch the system.
type Options struct {
	// Palette is the colour scheme to start with; the zero value means the
	// light one, or the dark one if Dark is set.
	Palette theme.Palette
	Dark    bool
	// SaveTheme, if not nil, is called when the user switches the theme,
	// with whether it is now dark, so that the choice can be kept.
	SaveTheme func(dark bool)
	// Themes, if not nil, delivers changes of the desktop's theme, true for
	// dark, which the window follows.
	Themes <-chan bool
	// PickFiles asks the user for files to open. Nil means the platform
	// provides no file dialog.
	PickFiles func() ([]string, error)
	// SaveFile asks the user where to save a converted copy, suggesting
	// suggested, and returns "" if they choose nowhere. Nil means the platform
	// provides no save dialog, and copies are saved at the suggested path.
	SaveFile func(suggested string) (string, error)
	// Verify, Convert and Inspect default to the pdfa package's.
	Verify  func(ctx context.Context, path, profile string) (pdfa.Report, error)
	Convert func(ctx context.Context, in, out, profile string) (pdfa.ConvertReport, error)
	Inspect func(doc *gopdfrab.Document) pdfa.Info
	// Now and AfterFunc default to the time package's.
	Now       func() time.Time
	AfterFunc func(d time.Duration, f func())
	// OpenURI opens the address of a link the user follows; nil means links
	// to addresses do nothing.
	OpenURI func(uri string)
	// Requests, if not nil, delivers requests from later launches of the
	// program to open files, which are opened in new tabs of the window last
	// focused.
	Requests <-chan instance.Request
	// NewWindow opens another window, for Ctrl+N and for tabs dragged out of
	// every window. Nil means the program has a single window.
	NewWindow func() (platform.Window, error)
	// Version is the program's version, shown in the About dialog; empty
	// means a development build.
	Version string
}

// withDefaults returns opts with the zero fields that have defaults set.
func (opts Options) withDefaults() Options {
	if opts.Palette == (theme.Palette{}) {
		opts.Palette = theme.Of(opts.Dark)
	}
	if opts.Verify == nil {
		opts.Verify = pdfa.Verify
	}
	if opts.Convert == nil {
		opts.Convert = pdfa.Convert
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
	return opts
}

// close releases the document's view and file.
func (d *document) close() {
	d.view.Close()
	d.doc.Close()
}

// newFace and newView are replaced by tests to force their failures.
var (
	newFace = func(size float64, bold bool) (*text.Face, error) { return text.Mono().Face(size, bold) }
	newView = pageview.New
)

// document is one open PDF, shown in one tab.
type document struct {
	// owner is the window whose tab shows the document, which its view asks
	// to redraw. It changes when the tab moves to another window.
	owner            atomic.Pointer[App]
	path, name       string
	bytes            int64
	version, claimed string
	doc              *gopdfrab.Document
	view             *pageview.View
	// result is what the PDF/A panel shows for the document, or nil before
	// its first check.
	result *widget.PanelResult
}

// section is the part of the window the pointer works on.
type section int

const (
	sectionNone section = iota
	sectionToolbar
	sectionTabs
	sectionContent
	sectionPanel
	sectionFrame // the window's edges, which resize it
)

// invalidate asks the window showing d to redraw.
func (d *document) invalidate() {
	if a := d.owner.Load(); a != nil {
		a.win.Invalidate()
	}
}

// App is one of the viewer's windows.
type App struct {
	s    *session
	win  platform.Window
	opts Options

	face *text.Face

	// pool renders the pages of every document of every window, under one
	// memory budget.
	pool *pageview.Pool

	toolbar  *widget.Toolbar
	tabs     *widget.TabBar
	status   *widget.StatusBar
	menu     *widget.Menu // the zoom, profile or application drop-down's list
	menuKind menuKind     // which of them it is
	about    *widget.About

	docs   []*document
	active int

	size    platform.Size
	canvas  *paint.Canvas
	pending []string // files to open once the window has a size
	// pendingDocs are documents moved here from another window before this
	// one had a size, which it shows once it has one.
	pendingDocs []*document

	capture section // the section a pressed pointer button belongs to
	hover   section

	title        string // the window title last set
	message      string
	messageKind  widget.MessageKind
	messageUntil time.Time

	findBar *widget.FindBar
	findDoc *document // the document searched while findBar.Open is set

	pageDoc   *document // the document whose page number is being typed
	hintHover bool      // the pointer is over the empty state's open button

	panel     *widget.Panel
	panelOpen bool      // the PDF/A panel is wanted; it shows with a document
	panelDoc  *document // the document it showed last
	profiles  []pdfa.Profile
	job       *job // the verification or conversion running, or nil

	// The window as the compositor shows it: decorated by it, or with the
	// toolbar as its title bar; maximised or not.
	decorated, maximized bool
	cursor               platform.Cursor // the cursor last set
	titlePress           time.Time       // when the title bar was last pressed
	quitting             bool            // the window's close control was clicked

	picking bool          // a file dialog is open
	inbox   chan func()   // work finished off the event loop, run on it
	done    chan struct{} // closed when the event loop has ended

	drag     *tabDrag // the drag of this window's tabs under way, or nil
	dropTint bool     // whether a drop on the content area is wanted
}

// New returns an App for win, the only window of its own session.
func New(win platform.Window, opts Options) (*App, error) {
	return newSession(opts.withDefaults()).newApp(win)
}

// newApp returns an App for win in session s.
func newApp(s *session, win platform.Window) (*App, error) {
	opts := s.options()
	face, err := newFace(theme.FontSize, false)
	if err != nil {
		return nil, err
	}
	small, err := newFace(theme.SmallFontSize, false)
	if err != nil {
		return nil, err
	}
	bold, err := newFace(theme.FontSize+2, true)
	if err != nil {
		return nil, err
	}
	a := &App{s: s, win: win, opts: opts, face: face, pool: s.pool, active: -1, inbox: make(chan func(), 64), done: make(chan struct{})}
	a.toolbar = widget.NewToolbar(opts.Palette, face)
	a.menu = widget.NewMenu(opts.Palette, face)
	for _, p := range pageview.ZoomPresets {
		a.toolbar.ZoomChoices = append(a.toolbar.ZoomChoices, p.Label)
	}
	a.tabs = widget.NewTabBar(opts.Palette, face)
	a.status = widget.NewStatusBar(opts.Palette, small)
	a.findBar = widget.NewFindBar(opts.Palette, face)
	a.profiles = pdfa.Profiles()
	var labels []string
	for _, p := range a.profiles {
		labels = append(labels, p.Label)
	}
	a.panel = widget.NewPanel(opts.Palette, face, labels)
	a.about = widget.NewAbout(opts.Palette, face, small, bold)
	a.about.Version = opts.Version
	return a, nil
}

// Run shows paths in a new App on win and handles events until every window
// of the program has closed: win and those opened with opts.NewWindow. It
// returns after closing every document.
func Run(win platform.Window, paths []string, opts Options) error {
	s := newSession(opts.withDefaults())
	a, err := s.newApp(win)
	if err != nil {
		s.pool.Close()
		win.Close()
		return err
	}
	a.pending = append(a.pending, paths...)
	s.start(a)
	requests, themes := opts.Requests, opts.Themes
	for {
		select {
		case <-s.empty:
			s.wg.Wait()
			return nil
		case dark, ok := <-themes:
			if !ok {
				themes = nil // a closed channel would be ready for ever
				break
			}
			s.each(nil, func(a *App) { a.setTheme(dark) })
		case req, ok := <-requests:
			if !ok {
				requests = nil
				break
			}
			s.launch(req)
		}
	}
}

// loop handles the window's events, and the work posted to it, until the
// window closes, and then closes its documents.
func (a *App) loop() {
	defer close(a.done)
	defer a.closeAll()
	defer a.win.Close()
	events := a.win.Events()
	for {
		select {
		case ev, ok := <-events:
			if !ok || a.Handle(ev) {
				return
			}
		case f := <-a.inbox:
			f()
			if a.quitting {
				return
			}
			a.win.Invalidate()
		}
	}
}

// post runs f on the window's event loop, and reports whether it will: not
// once the window has closed.
func (a *App) post(f func()) bool {
	select {
	case <-a.done:
		return false // the inbox may have room, but nothing reads it
	default:
	}
	select {
	case a.inbox <- f:
		return true
	case <-a.done:
		return false
	}
}

// bounds returns the whole window, in window coordinates.
func (a *App) bounds() image.Rectangle { return image.Rect(0, 0, a.size.W, a.size.H) }

// layout returns the rectangles of the parts of the window as it is now.
func (a *App) layout() (toolbar, tabs, content, status image.Rectangle) {
	return a.layoutFor(len(a.docs) > 0)
}

// panelRect returns where the PDF/A panel is, which is empty when it is not
// showing: right of the content, beside the pages.
func (a *App) panelRect() image.Rectangle {
	_, _, content, _ := a.layout()
	if !a.showsPanel(len(a.docs) > 0) {
		return image.Rectangle{}
	}
	return image.Rect(content.Max.X, content.Min.Y, a.size.W, content.Max.Y)
}

// showsPanel reports whether the panel shows, with documents open or not.
func (a *App) showsPanel(docs bool) bool { return a.panelOpen && docs }

// layoutFor returns the rectangles of the parts of the window, with documents
// open or not. The tab bar appears with the first document, as does the panel
// if it is open; the panel takes the right of the content.
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
	right := w
	if a.showsPanel(tabBar) {
		right = w - min(widget.PanelWidth, w/2)
	}
	content = image.Rect(0, y, right, bottom)
	status = image.Rect(0, bottom, w, max(bottom, h))
	return
}

// Handle applies one event and reports whether the application should quit.
func (a *App) Handle(ev platform.Event) (quit bool) {
	switch ev := ev.(type) {
	case platform.CloseEvent:
		return true
	case platform.FocusEvent:
		if ev.Focused {
			a.s.focus(a)
		}
	case platform.DragEvent:
		a.dragOver(ev)
	case platform.DropEvent:
		a.dropped(ev)
	case platform.DragEndEvent:
		a.dragEnded(ev)
	case platform.ResizeEvent:
		a.resize(ev.Size)
	case platform.FrameEvent:
		a.draw()
	case platform.PointerEvent:
		a.pointer(ev)
	case platform.StateEvent:
		a.decorated, a.maximized = ev.Decorated, ev.Maximized
		a.win.Invalidate()
	case platform.ScrollEvent:
		if r := a.panelRect(); image.Pt(int(ev.X), int(ev.Y)).In(r) {
			a.panel.Scroll(ev.DY*panelScrollSpeed, r.Size())
			a.win.Invalidate()
			break
		}
		o := a.origin(sectionContent)
		ev.X, ev.Y = ev.X-float64(o.X), ev.Y-float64(o.Y)
		if d := a.current(); d != nil && d.view.HandleEvent(ev) {
			a.win.Invalidate()
		}
	case platform.KeyEvent:
		return a.key(ev)
	case platform.PasteEvent:
		if a.findBar.Open {
			a.findAction(a.findBar.Paste(ev.Text))
			a.win.Invalidate()
		}
	}
	return a.quitting
}

// current returns the active document, or nil.
func (a *App) current() *document {
	if a.active < 0 || a.active >= len(a.docs) {
		return nil
	}
	return a.docs[a.active]
}

func (a *App) resize(size platform.Size) {
	// The canvas is made when drawing, at the size then, so that a burst of
	// resizes while the window is dragged allocates one canvas, not one each.
	a.size = size
	a.toolbar.Layout(size.W)
	if docs := a.pendingDocs; len(docs) > 0 {
		a.pendingDocs = nil
		a.adopt(docs, 0)
	}
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
	pal := a.opts.Palette
	d := &document{path: path, name: filepath.Base(path), bytes: st.Size(), doc: doc}
	d.owner.Store(a)
	// Inspect runs before newView, so the properties are read before any
	// render of doc starts.
	info := a.opts.Inspect(doc)
	d.version, d.claimed = info.Version, info.Claimed
	d.view, err = newView(doc, image.Pt(content.Dx(), content.Dy()), pageview.Options{
		Pool: a.pool, OpenURI: a.opts.OpenURI, Gap: theme.PageGap, Margin: theme.PageMargin,
		Background: pal.Canvas, Selection: pal.Selection,
	}, d.invalidate)
	if err != nil {
		doc.Close()
		return nil, err
	}
	// The first pages are rendered in the background, so that the tab is ready
	// when it is shown.
	d.view.Prefetch()
	return d, nil
}

// closeTab closes the document in tab i.
func (a *App) closeTab(i int) {
	if i < 0 || i >= len(a.docs) {
		return
	}
	a.drop(a.docs[i])
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

// drop closes d, stopping its check if one is running.
func (a *App) drop(d *document) {
	if a.job != nil && a.job.doc == d {
		a.job.cancel()
	}
	d.close()
}

// closeAll closes every document, those still to be shown too, and takes
// the window out of its session.
func (a *App) closeAll() {
	for _, d := range append(a.docs, a.pendingDocs...) {
		a.drop(d)
	}
	a.docs, a.pendingDocs, a.active = nil, nil, -1
	a.s.remove(a)
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

// setTheme switches the window to the dark palette or the light one, which
// windows opened later start with too.
func (a *App) setTheme(dark bool) {
	p := theme.Of(dark)
	a.s.setPalette(p)
	a.opts.Palette = p
	a.toolbar.Palette, a.tabs.Palette, a.status.Palette = p, p, p
	a.findBar.Palette, a.panel.Palette, a.menu.Palette, a.about.Palette = p, p, p, p
	for _, d := range a.docs {
		d.view.SetColors(p.Canvas, p.Selection)
	}
	a.win.Invalidate()
}

// toggleTheme switches every window between the light and the dark theme,
// and passes the new choice to SaveTheme if it is set.
func (a *App) toggleTheme() {
	dark := !a.opts.Palette.Dark
	a.setTheme(dark)
	a.s.each(a, func(b *App) { b.setTheme(dark) })
	if a.opts.SaveTheme != nil {
		a.opts.SaveTheme(dark)
	}
}
