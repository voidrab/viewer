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
- Drag tabs to reorder them, onto another window to move them there, or
  out of every window to open them in a new one; drag the app icon (or the
  empty part of the tab bar) onto another window to merge the two
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

## Native build (work in progress)

A second, pure-Go viewer is replacing the webview and pdf.js: no cgo, no Node,
no WebKit. It renders with gopdfrab's rasterizer and talks to Wayland directly.

```sh
make build-native               # bin/viewer-native, a static binary
sudo make install-native        # as `viewer`, with the same desktop entry and icon
make install-native PREFIX=~/.local NAME=viewer-native   # beside the other viewer
```

Needs a Wayland session (no X11 yet) and, for Open/Save dialogs,
xdg-desktop-portal. Same look as the webview build, with the same features:
tabs, zoom (fit width/page, 50-300%), page field, text selection and copy,
find, links (highlighted and hand cursor on hover; external ones via
`xdg-open`), the PDF/A panel, and a light/dark toggle that follows the desktop
until you pick (kept in `~/.config/viewer/theme`).

- One process runs every window; later launches add a tab to the last-used
  window. Ctrl+N opens another window, Ctrl+Q closes all.
- Drop PDFs on a window to open them. Drag tabs to reorder them, onto another
  window to move them (the document is kept), or off every window for a new
  one. Dragging the logo moves all tabs.
- Dragging on text selects it; elsewhere it pans, as does the middle button.
  Ctrl+wheel zooms around the pointer.
- The toolbar is the title bar: drag to move, double click to maximise, right
  click for the window menu.

| Shortcut                  | Action                          |
| ------------------------- | ------------------------------- |
| Ctrl+O                    | Open (into new tabs)            |
| Ctrl+T / Ctrl+W           | New tab / close tab             |
| Ctrl+Tab, Ctrl+PgDn/PgUp  | Next / previous tab             |
| Ctrl+N / Ctrl+Q           | New window / close every window |
| ← / →                     | Previous / next page            |
| Ctrl+F, Enter/Shift+Enter | Find, next / previous match     |
| Ctrl+A / Ctrl+C           | Select all / copy               |
| Ctrl+= / Ctrl+− / Ctrl+0  | Zoom in / out / fit width       |
| Esc                       | Close find, drop-down or panel; clear selection |

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
