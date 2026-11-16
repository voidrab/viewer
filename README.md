# Viewrab

A fast, small and simple PDF viewer with built-in PDF/A validation and
conversion. Pure Go: no cgo or Node. It renders with
[gopdfrab](https://github.com/voidrab/gopdfrab)'s rasterizer and talks to
Wayland, X11 or Win32 directly.

## TODO

- Support for macOS

## Features

- Tabs, zoom (fit width/page, 50-300%), page field, text selection and copy,
  find, and links (highlighted, hand cursor on hover; external ones open with
  `xdg-open`, or the default program on Windows)
- PDF/A panel: verify against PDF/A-1b, PDF/A-2b or plain PDF structure, jump
  to the pages an issue is on, and convert to a conformant copy
- One process runs every window; later launches add a tab to the last-used
  window. Drop PDFs on a window to open them, drag tabs to reorder them, move
  them between windows, or tear them off into a new one
- Light on memory: one render pool under a bitmap budget, tiled zoom, files
  read by byte range
- Console-style UI with the toolbar as the title bar, Adwaita Mono type, and a
  light/dark theme that follows the desktop until you pick one (kept in
  `~/.config/viewrab/theme`, or `%AppData%\viewrab\theme` on Windows)

## Requirements

Go 1.24+, a Wayland or X11 session and, for the Open/Save dialogs,
xdg-desktop-portal. Wayland is used where the session offers it, X11
otherwise; `VIEWRAB_BACKEND=x11` or `VIEWRAB_BACKEND=wayland` picks one.

On Windows 10 1803 or later it runs as a plain Win32 program with the common
Open and Save dialogs and follows the system's app mode for its theme.

## Usage

```sh
make build                      # bin/viewrab, a static binary (ARCH=arm64 to cross-build)
make build-windows              # bin/viewrab.exe, for Windows, with icon and version info
make build-web                  # bin/web, the WebAssembly build and its page (make serve-web)
make test-windows               # the Win32 backend's tests, under Wine
sudo make install               # with desktop entry, metainfo, icon and licences
make default                    # make Viewrab your default PDF application
make test                       # Go tests; fails below 100% coverage
make dist                       # release archives for Linux and Windows in dist/
viewrab a.pdf b.pdf             # each file opens in its own tab
viewrab --version
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

## In the browser

`make build-web` builds Viewrab to WebAssembly in `bin/web`, with the page
that runs it; any static HTTP server can serve the directory, and
`make serve-web` serves it on http://127.0.0.1:8080. It is the same viewer,
run in a web worker so that rendering never holds up the page, and drawn on
the page's canvas through an OffscreenCanvas, which current Chrome, Firefox
and Safari have. It differs from the native program in that:

- Files come from the browser: the open button's file chooser, files dropped
  on the page, or `?file=<url>` in the address (each one fetched, so another
  site's file needs CORS). They are held in memory, in the in-memory file
  system of `web/memfs.js`, and never leave the page.
- A converted PDF/A copy is saved among the browser's downloads.
- One window and no tear-off: tabs reorder along the tab bar only. Ctrl+N,
  Ctrl+T and Ctrl+W may be taken by the browser before the page sees them.
- The theme follows the system's, or the choice made with the switch, kept
  in the page's local storage.

## Layout

```
cmd/nativeview          entry point: file dialogs (portal), theme, single instance
cmd/web                 entry point of the WebAssembly build
internal/ui             app, widgets, page view, text layer
internal/ui/platform    the window seam, with the wayland, x11, win32 and web backends
internal/pdfa           gopdfrab adapter producing PDF/A reports
internal/testpdf        builds small valid PDFs for tests
build/                  app icon, Linux desktop entry and metainfo, Windows resources,
                        and the notices of what gopdfrab bundles (make check-licenses)
packaging/              Windows installer, AUR, Scoop, winget and Flatpak templates
web/                    the page of the WebAssembly build, the worker it runs in, and its
                        in-memory file system
.github/workflows       CI, and the release built from a version tag
```

## Releasing

Push a version tag, such as `git tag -a v0.1.0 -m "Viewrab 0.1.0" && git push
origin v0.1.0`. The Release workflow tests, then builds:

- `viewrab-<version>-linux-<arch>.tar.gz` for amd64 and arm64: the static
  binary with its desktop entry, metainfo, icon and licences. Unpacked,
  `sudo make install` (or `PREFIX=~/.local make install`) installs it,
  without Go.
- `viewrab-<version>-windows-<arch>.zip`: the portable program.
- `viewrab-<version>-windows-<arch>-setup.exe`: an Inno Setup installer that
  installs for the current user without administrator rights, adds a Start
  menu entry and offers Viewrab under "Open with" for PDFs. Windows lets
  only the user pick the default PDF program; the installer's last page
  opens that setting.
- `viewrab-<version>-vendored.tar.gz`: the source with every module it needs,
  which builds offline (`make build VERSION=v<version>`).
- `SHA256SUMS`.

It creates a draft release with these files, to check and publish by hand.
Its `packaging` artifact holds the AUR (`viewrab`, `viewrab-bin`), Scoop,
winget and Flatpak manifests filled in for the release;
`packaging/render.sh` makes them from any `SHA256SUMS`. They go to:

- **AUR:** run `makepkg --printsrcinfo > .SRCINFO` next to each PKGBUILD and
  push both to `ssh://aur@aur.archlinux.org/<name>.git`.
- **Scoop:** a bucket repository; `checkver` and `autoupdate` keep it current.
- **winget:** a pull request to `microsoft/winget-pkgs` under
  `manifests/v/Voidrab/Viewrab/<version>/`, for instance with `wingetcreate`.
- **Flathub:** a new-app pull request to `flathub/flathub`. Test the sandbox
  first: files dropped from the host are not yet handed over through the
  document portal.

Windows files are signed when the repository has SignPath set up: the
variables `SIGNPATH_ORGANIZATION_ID`, `SIGNPATH_PROJECT_SLUG` and
`SIGNPATH_POLICY_SLUG`, the secret `SIGNPATH_API_TOKEN`, and in the SignPath
project the artifact configurations `zips` (viewrab.exe inside each zip) and
`installers` (each setup.exe). Unsigned, Windows SmartScreen warns before the
first start.

## Licensing

Viewrab is licensed under GNU AGPL 3.0.

It compiles in the fonts gopdfrab bundles;
[build/licenses](build/licenses) gives their sources and licences.
