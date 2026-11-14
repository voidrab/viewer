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

A second, pure-Go viewer is being built to replace the webview and pdf.js: no
cgo, no Node, no WebKit. It renders with gopdfrab's own rasterizer and talks to
Wayland directly.

```sh
make build-native               # bin/viewer-native, a static binary
sudo make install-native        # as `viewer`, with the same desktop entry and icon
make install-native PREFIX=~/.local NAME=viewer-native   # one user, beside the other viewer
viewer a.pdf b.pdf              # each file opens in its own tab
```

It needs a Wayland session (X11 is not supported yet) and, for the Open and
Save dialogs, a desktop with xdg-desktop-portal. It has tabs, a toolbar,
scrolling, zoom, page navigation, text selection and copy, find, links, and
the PDF/A panel. It looks like the webview build: the same toolbar, icons,
colours, status line and Adwaita Mono type.

- Click the page number to type another one; Enter, or clicking elsewhere,
  goes there, and Escape leaves it. The zoom drop-down offers fit width, fit
  page and fixed zooms from 50% to 300%, counted as in a browser (100% is 96
  dpi), so a page shows at the same size in both builds.
- The sun or moon button at the right of the toolbar switches between the
  light and the dark theme. The window follows the desktop's theme (through
  xdg-desktop-portal's settings) until you switch; the choice is kept in
  `~/.config/viewer/theme`, and switching back to the desktop's theme forgets
  it, so the window follows the desktop again.

- Opening another PDF while it runs adds a tab to the running window (and asks
  the compositor to bring it forward).
- Drag on text to select it (double click a word, triple click a line), Ctrl+C
  to copy. Dragging anywhere else pans the page, as does the middle button;
  the scrollbar thumbs drag and clicking beside them jumps.
- Ctrl+F, or a click on the find field, finds text, starting from the
  selection, with every match highlighted; Enter and Shift+Enter, or the
  arrows in the field, step through them, Ctrl+V pastes, Escape ends it.
- Links go to their page and position, or open http, https and mailto
  addresses with `xdg-open`.
- Ctrl+wheel zooms around the pointer, and holding a key repeats it.
- The toolbar is the title bar, as on the webview build: drag its empty part
  to move the window, double click it to maximise, right click it for the
  compositor's window menu, and use the minimise, maximise and close controls
  at its right end. The window's edges resize it. Where the compositor insists
  on drawing its own title bar, the toolbar leaves the controls to it.
- The shield button opens the PDF/A panel: pick PDF/A-1b, PDF/A-2b or plain
  PDF structure from its drop-down, verify, and open an issue to see its messages and jump to the
  pages it is on. Convert… asks where to save a conformant copy, reports what
  had to be rasterized or dropped, and opens the copy (reloading its tab if it
  is already open). One check runs at a time and can be cancelled.

Use `make default` as usual to make it your PDF application.

| Shortcut              | Action                   |
| --------------------- | ------------------------ |
| Ctrl+O                | Open (into new tabs)     |
| Ctrl+T / Ctrl+W       | New tab / close tab      |
| Ctrl+Tab, Ctrl+PgDn/PgUp | Next / previous tab   |
| Ctrl+N                | New window               |
| ← / →                 | Previous / next page     |
| Ctrl+F, Enter/Shift+Enter | Find, next / previous match |
| Ctrl+A / Ctrl+C       | Select all / copy        |
| Ctrl+= / Ctrl+− / Ctrl+0 | Zoom in / out / fit width |
| Esc                   | Close find or a drop-down, leave the page field, clear the selection, close the PDF/A panel |

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
