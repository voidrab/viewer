// Package viewer is the Go side of the PDF viewer: it routes files to
// windows, serves document bytes to the frontend's renderer, and runs PDF/A
// verification and conversion.
//
// One process hosts every window, and each window shows documents in tabs.
// Go decides which window a file goes to; the window's frontend owns its
// tabs. Each open document has an ID, and the frontend names the document it
// acts on, so verify or convert always runs on the file a tab shows.
//
// The package does not depend on Wails. The desktop wiring in package main
// supplies native dialogs, windows and an event emitter through the Dialogs,
// Windows, Emitter and WindowOf types, so the whole package runs under plain
// go test.
package viewer

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/voidrab/gopdfrab"

	"viewer/internal/pdfa"
)

// Route is where documents are served; mount the Service there.
const Route = "/doc"

// Names of the events the Service emits. Each is sent as a Targeted payload.
const (
	// EventOpenFiles carries paths ([]string) to open, each in a tab.
	EventOpenFiles = "files:open"
	// EventShow carries the ID (int) of an open document to switch to.
	EventShow = "document:show"
	// EventError carries a message for the user.
	EventError = "document:error"
)

// ErrNoDocument is returned for a document ID that is not open.
var ErrNoDocument = errors.New("no document is open")

// Dialogs shows native file dialogs. Each returns nothing, and no error,
// when the user cancels.
type Dialogs interface {
	OpenPDFs() ([]string, error)
	SavePDF(suggested string) (string, error)
}

// Windows creates and raises app windows by name. New's opening reports
// whether the window starts with files to load, so it can show progress from
// its first paint.
type Windows interface {
	New(name string, opening bool)
	Focus(name string)
}

// Emitter delivers an event to the frontend. Events reach every window.
type Emitter func(name string, data any)

// WindowOf names the window a bound call came from, or "" for none.
type WindowOf func(ctx context.Context) string

// Targeted is the payload of every event the Service emits: events reach all
// windows, and each window ignores those addressed to another.
type Targeted struct {
	Window string `json:"window"`
	Data   any    `json:"data"`
}

// Document describes an open document.
type Document struct {
	ID   int    `json:"id"`
	Path string `json:"path"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	// URL is where the frontend fetches the bytes: Route/<ID>/<name>.
	URL  string    `json:"url"`
	Info pdfa.Info `json:"info"`
	// Note gives the reason Info is incomplete when gopdfrab could not read the
	// file, which may still render.
	Note string `json:"note,omitempty"`
}

// window is what the Service tracks per app window.
type window struct {
	docs    map[int]bool // IDs of the documents its tabs show
	ready   bool         // its frontend has started and takes events
	pending []string     // files to open once it is ready
}

// Service is bound to the frontend. Its exported methods are callable from
// JavaScript; it is safe for concurrent use.
type Service struct {
	dialogs  Dialogs
	windows  Windows
	emit     Emitter
	windowOf WindowOf

	mu         sync.Mutex
	docs       map[int]*Document // by ID; only documents some tab shows
	wins       map[string]*window
	active     string // the window launched files go to
	lastDoc    int
	lastWindow int
}

// New returns a Service. Call Launch to open the first window.
func New(dialogs Dialogs, windows Windows, emit Emitter, windowOf WindowOf) *Service {
	return &Service{
		dialogs:  dialogs,
		windows:  windows,
		emit:     emit,
		windowOf: windowOf,
		docs:     map[int]*Document{},
		wins:     map[string]*window{},
	}
}

// win returns the named window's state, creating it on first use. The
// caller holds s.mu.
func (s *Service) win(name string) *window {
	w := s.wins[name]
	if w == nil {
		w = &window{docs: map[int]bool{}}
		s.wins[name] = w
	}
	return w
}

// Launch handles a start of the app: the process's own, or a second one
// whose arguments were forwarded here. A file already open is switched to;
// the rest open as tabs in the most recently focused window, or a new one.
// With no files, an empty window opens. Relative paths are resolved against
// dir, the launch's working directory.
func (s *Service) Launch(files []string, dir string) {
	if len(files) == 0 {
		s.openWindow(nil)
		return
	}
	var open []string
	for _, f := range files {
		if !filepath.IsAbs(f) {
			f = filepath.Join(dir, f)
		}
		if name, id := s.find(f); name != "" {
			if id != 0 {
				s.emit(EventShow, Targeted{name, id})
			}
			s.windows.Focus(name)
		} else {
			open = append(open, f)
		}
	}
	if len(open) > 0 {
		s.deliver(open)
	}
}

// deliver sends files to the active window, or opens a window for them.
func (s *Service) deliver(files []string) {
	s.mu.Lock()
	target, w := s.active, s.wins[s.active]
	switch {
	case w == nil:
		s.mu.Unlock()
		s.openWindow(files)
		return
	case !w.ready:
		w.pending = append(w.pending, files...)
	default:
		s.emit(EventOpenFiles, Targeted{target, files})
	}
	s.mu.Unlock()
	s.windows.Focus(target)
}

// NewWindow opens an empty window.
func (s *Service) NewWindow() { s.openWindow(nil) }

func (s *Service) openWindow(files []string) {
	s.mu.Lock()
	s.lastWindow++
	name := fmt.Sprintf("window-%d", s.lastWindow)
	s.win(name).pending = files
	s.active = name // until another window takes focus
	s.mu.Unlock()
	s.windows.New(name, len(files) > 0)
}

// find returns the name of the window that shows path or has it pending to
// open. If the window already shows path, find also returns the document's
// ID. Otherwise it returns "" and 0.
func (s *Service) find(path string) (string, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, w := range s.wins {
		for id := range w.docs {
			if s.docs[id].Path == path {
				return name, id
			}
		}
		if slices.Contains(w.pending, path) {
			return name, 0
		}
	}
	return "", 0
}

// InitialFiles marks the calling window ready and returns the files pending
// for it. Each file is returned at most once.
func (s *Service) InitialFiles(ctx context.Context) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.win(s.windowOf(ctx))
	w.ready = true
	files := append([]string{}, w.pending...)
	w.pending = nil
	return files
}

// WindowFocused records the window launched files should go to.
func (s *Service) WindowFocused(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wins[name] != nil {
		s.active = name
	}
}

// WindowClosed releases the named window's documents and forgets it.
func (s *Service) WindowClosed(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if w := s.wins[name]; w != nil {
		for id := range w.docs {
			s.drop(w, id)
		}
	}
	delete(s.wins, name)
	if s.active == name {
		s.active = ""
	}
}

// Release tells the Service a tab of the calling window stopped showing
// document id.
func (s *Service) Release(ctx context.Context, id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drop(s.win(s.windowOf(ctx)), id)
}

// drop removes id from w, and forgets the document once no window shows
// it. The caller holds s.mu.
func (s *Service) drop(w *window, id int) {
	delete(w.docs, id)
	for _, other := range s.wins {
		if other.docs[id] {
			return
		}
	}
	delete(s.docs, id)
}

// Profiles lists the verification profiles, the default first.
func (s *Service) Profiles() []pdfa.Profile { return pdfa.Profiles() }

// Open reads the PDF at path for a tab of the calling window.
func (s *Service) Open(ctx context.Context, path string) (*Document, error) {
	abs := path
	if a, err := filepath.Abs(path); err == nil {
		abs = a
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("cannot open %s: %w", filepath.Base(abs), errors.Unwrap(err))
	}
	if st.IsDir() {
		return nil, fmt.Errorf("%s is a folder, not a PDF", filepath.Base(abs))
	}

	doc := &Document{Path: abs, Name: filepath.Base(abs), Size: st.Size()}
	info, err := pdfa.Inspect(abs)
	switch {
	case err == nil:
		doc.Info = info
	case errors.Is(err, gopdfrab.ErrNotPDF):
		return nil, err
	default:
		doc.Note = err.Error()
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastDoc++
	doc.ID = s.lastDoc
	doc.URL = fmt.Sprintf("%s/%d/%s", Route, doc.ID, url.PathEscape(doc.Name))
	s.docs[doc.ID] = doc
	s.win(s.windowOf(ctx)).docs[doc.ID] = true
	return doc, nil
}

// PickFiles asks the user for PDFs to open. It returns none, and no error,
// when the user cancels.
func (s *Service) PickFiles() ([]string, error) {
	files, err := s.dialogs.OpenPDFs()
	return append([]string{}, files...), err
}

// HandleDrop tells the named window to open the PDFs dropped onto it.
func (s *Service) HandleDrop(window string, paths []string) {
	var pdfs []string
	for _, p := range paths {
		if strings.EqualFold(filepath.Ext(p), ".pdf") {
			pdfs = append(pdfs, p)
		}
	}
	switch {
	case len(pdfs) > 0:
		s.emit(EventOpenFiles, Targeted{window, pdfs})
	case len(paths) > 0:
		s.emit(EventError, Targeted{window, "Only PDF files can be opened."})
	}
}

func (s *Service) doc(id int) *Document {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.docs[id]
}

// Verify checks document id against the named profile.
func (s *Service) Verify(ctx context.Context, id int, profile string) (pdfa.Report, error) {
	doc := s.doc(id)
	if doc == nil {
		return pdfa.Report{}, ErrNoDocument
	}
	return pdfa.Verify(ctx, doc.Path, profile)
}

// Convert asks where to save, then converts document id to the named
// profile. It returns nil, and no error, when the user cancels the dialog.
func (s *Service) Convert(ctx context.Context, id int, profile string) (*pdfa.ConvertReport, error) {
	doc := s.doc(id)
	if doc == nil {
		return nil, ErrNoDocument
	}
	out, err := s.dialogs.SavePDF(pdfa.DefaultOutputPath(doc.Path, profile))
	if err != nil || out == "" {
		return nil, err
	}
	cr, err := pdfa.Convert(ctx, doc.Path, out, profile)
	if err != nil {
		return nil, err
	}
	return &cr, nil
}

// ServeHTTP serves a document's bytes at Route/<ID>/<name> and supports range
// requests. The name part of the path is ignored; it only makes the URL
// readable.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, Route), "/")
	idText, _, _ := strings.Cut(rest, "/")
	id, _ := strconv.Atoi(idText) // A malformed ID yields 0, which no document has
	doc := s.doc(id)
	if doc == nil {
		http.Error(w, ErrNoDocument.Error(), http.StatusNotFound)
		return
	}
	f, err := os.Open(doc.Path)
	if err != nil {
		http.Error(w, "document is no longer readable", http.StatusGone)
		return
	}
	defer f.Close()
	var modified time.Time // Zero, which ServeContent ignores, if Stat fails
	if st, err := f.Stat(); err == nil {
		modified = st.ModTime()
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, doc.Name, modified, f)
}
