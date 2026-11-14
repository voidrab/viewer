# Viewer

A fast, small and simple PDF viewer with built-in PDF/A validation and
conversion. Pure Go: no cgo or Node. It renders with
[gopdfrab](https://github.com/voidrab/gopdfrab)'s rasterizer and talks to
Wayland directly.

## TODO

- Support for X11, Windows and macOS

## Features

- Tabs, zoom (fit width/page, 50-300%), page field, text selection and copy,
  find, and links (highlighted, hand cursor on hover; external ones open with
  `xdg-open`)
- PDF/A panel: verify against PDF/A-1b, PDF/A-2b or plain PDF structure, jump
  to the pages an issue is on, and convert to a conformant copy
- One process runs every window; later launches add a tab to the last-used
  window. Drop PDFs on a window to open them, drag tabs to reorder them, move
  them between windows, or tear them off into a new one
- Light on memory: one render pool under a bitmap budget, tiled zoom, files
  read by byte range
- Console-style UI with the toolbar as the title bar, Adwaita Mono type, and a
  light/dark theme that follows the desktop until you pick one (kept in
  `~/.config/viewer/theme`)

## Requirements

Go 1.24+, a Wayland session and, for the Open/Save dialogs,
xdg-desktop-portal.

## Usage

```sh
make build                      # bin/viewer, a static binary
sudo make install               # with desktop entry and icon
make default                    # make Viewer your default PDF application
make test                       # Go tests; fails below 100% coverage
viewer a.pdf b.pdf              # each file opens in its own tab
```

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
| Ctrl+wheel                | Zoom around the pointer         |
| Esc                       | Close find, drop-down or panel; clear selection |

Dragging on text selects it; elsewhere it pans, as does the middle button.

## Layout

```
cmd/nativeview          entry point: file dialogs (portal), theme, single instance
internal/ui             app, widgets, page view, text layer, Wayland platform
internal/pdfa           gopdfrab adapter producing PDF/A reports
internal/testpdf        builds small valid PDFs for tests
build/                  app icon and Linux desktop entry
```
