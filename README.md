# Viewer

A fast, small and simple PDF viewer with built-in PDF/A validation and
conversion. Go + [Wails v3](https://v3.wails.io) for the desktop shell,
[pdf.js](https://mozilla.github.io/pdf.js/) for rendering, and
[gopdfrab](https://github.com/voidrab/gopdfrab) for PDF/A.

- Opens PDFs from the toolbar, the command line (`viewer file.pdf`) or by
  dropping them on the window, with a slim progress bar while loading
- Tabs, in one process: opening another PDF (e.g. from the file manager)
  adds a tab to the running app's last-used window, or switches to the tab
  already showing it; Ctrl+N still opens a separate window
- Light on memory: pdf.js loads only with the first document, only the active
  tab keeps rendered pages, only the three most recently used tabs keep their
  document open (others reopen on demand), files are read by byte range, and
  a hidden window drops its rendered pages
- Lazy page rendering, text selection, links, find (Ctrl+F), zoom presets and
  Ctrl+wheel zoom
- PDF/A panel: verify against PDF/A-1b, PDF/A-2b or plain PDF structure, jump
  to the pages an issue is on, and convert to a conformant copy, with a report
  of anything the conversion had to rasterize or drop
- Minimal, console-style UI: a Zed-like icon bar that doubles as the title
  bar (macOS keeps its native traffic lights), a status line for file info,
  zoom and messages, and monospace type throughout
- Follows the system light/dark theme

## Requirements

- Go 1.24+ and Node 20+
- Linux: GTK 4 and WebKitGTK 6.0 (Arch: `pacman -S gtk4 webkitgtk-6.0`;
  Debian/Ubuntu: `apt install libgtk-4-dev libwebkitgtk-6.0-dev`)

The Wails CLI is not required; everything builds with `go` and `npm`.

## Usage

```sh
make build          # bin/viewer, a single self-contained binary
sudo make install   # system-wide into /usr/local (after make build)
make default        # make Viewer your default PDF application
make dev ARGS=a.pdf # run with live frontend reload
make test           # Go + frontend tests; fails below 100% coverage
```

| Shortcut              | Action                   |
| --------------------- | ------------------------ |
| Ctrl+O                | Open (into new tabs)     |
| Ctrl+T / Ctrl+W       | New tab / close tab      |
| Ctrl+Tab, Ctrl+PgDn/PgUp | Next / previous tab   |
| Ctrl+N                | New window               |
| ← / →                 | Previous / next page     |
| Ctrl+F, Enter/Shift+Enter | Find, next / previous match |
| Ctrl+= / Ctrl+− / Ctrl+0 | Zoom in / out / fit width |
| Esc                   | Close the PDF/A panel    |

## Layout

```
main.go                 Wails wiring only: window, native dialogs, file drop
internal/viewer         the service the frontend calls; serves the open PDF at /doc
internal/pdfa           gopdfrab adapter producing JSON-friendly reports
internal/testpdf        builds small valid PDFs for tests
frontend/src            api.js (Go bridge), document.js (pdf.js), app.js (UI), report.js
frontend/test           vitest + happy-dom suites
build/                  app icon and Linux desktop entry
```

The PDF bytes go from Go to pdf.js over the app's internal asset server
(`/doc/<rev>/<name>`, with range support), not through the JS bridge, so
large files open quickly. `main.go` is the only code outside the coverage
gate: it needs a running window system and holds no logic.
