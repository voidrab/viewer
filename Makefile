# Viewrab: build, test, package and install. Plain go; no cgo.

BIN      ?= bin/viewrab
PREFIX   ?= /usr/local
# The command name the binary is installed under.
NAME     ?= viewrab
APP_ID   := com.voidrab.Viewrab
# The architecture to build for; build, build-windows and the dist targets
# take it. Without Go, as when installing from a release archive, it is
# empty, which install does not need.
ARCH     ?= $(shell go env GOARCH 2>/dev/null)
ARCH     := $(ARCH)

APPS_DIR     = $(DESTDIR)$(PREFIX)/share/applications
ICONS_DIR    = $(DESTDIR)$(PREFIX)/share/icons/hicolor
METAINFO_DIR = $(DESTDIR)$(PREFIX)/share/metainfo
LICENSES_DIR = $(DESTDIR)$(PREFIX)/share/licenses/$(NAME)

# The version shown in the About dialog and by --version: the nearest tag, or
# the commit. A build from a source archive, which has no git history, has to
# be given one (make build VERSION=v1.2.0).
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null)
VERSION  := $(VERSION)
LDFLAGS  := -s -w -X main.version=$(VERSION)
GO_FLAGS := -trimpath -ldflags "$(LDFLAGS)"
# The dist targets' sub-makes take both as they are, rather than ask again.
export VERSION ARCH

# The version in file names, without the v, and the numeric one Windows
# shows in the .exe's properties (0.0.0 for a build no tag names); the
# installer reads it back from the .exe.
DIST_VERSION := $(patsubst v%,%,$(VERSION))
WIN_VERSION  := $(or $(shell echo '$(VERSION)' | sed -nE 's/^v([0-9]+(\.[0-9]+){0,3}).*/\1/p'),0.0.0)

DIST    := dist
WINRES  := go run github.com/tc-hib/go-winres@v0.3.3

.PHONY: all build build-windows build-web serve-web winres check-licenses test test-windows \
	cover dist dist-linux dist-windows dist-src install uninstall default clean

all: test build

## build: static, stripped binary in bin/
build:
	CGO_ENABLED=0 GOARCH=$(ARCH) go build $(GO_FLAGS) -o $(BIN) ./cmd/nativeview
	@ls -lh $(BIN) | awk '{print "built " $$9 " (" $$5 ")"}'

## build-windows: bin/viewrab.exe, a Windows GUI program with no console,
##                with its icon, manifest and version information
build-windows: winres
	GOOS=windows GOARCH=$(ARCH) CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS) -H windowsgui" -o $(BIN).exe ./cmd/nativeview
	@ls -lh $(BIN).exe | awk '{print "built " $$9 " (" $$5 ")"}'

## build-web: bin/web, the WebAssembly build with the page that runs it,
##            for any static HTTP server (make serve-web serves it locally).
##            gopdfrab's substitute fonts are left out of the program and
##            served from bin/web/fonts, fetched when a document needs one.
##            The page's script is named with the build's hash, which it
##            adds to every other file's name, so that a browser never mixes
##            the files of two builds.
WEB := bin/web
build-web:
	rm -rf $(WEB) && mkdir -p $(WEB)/fonts
	GOOS=js GOARCH=wasm CGO_ENABLED=0 go build -tags gopdfrab_nofonts $(GO_FLAGS) -o $(WEB)/viewrab.wasm ./cmd/web
	cp "$$(go env GOROOT)/lib/wasm/wasm_exec.js" web/viewrab.js web/worker.js web/memfs.js $(WEB)/
	cp build/appicon.png $(WEB)/icon.png
	fonts="$$(go list -m -f '{{.Dir}}' github.com/voidrab/gopdfrab)/internal/fontsubst/fonts" && \
		install -m644 "$$fonts"/*.ttf.gz "$$fonts"/OFL.txt $(WEB)/fonts/
	v=$$(cat $(WEB)/viewrab.wasm $(WEB)/*.js | sha256sum | cut -c1-12) && size=$$(wc -c < $(WEB)/viewrab.wasm) && \
		sed "s|<script src=\"viewrab.js\">|<script src=\"viewrab.js?v=$$v\" data-size=\"$$size\">|" \
			web/index.html > $(WEB)/index.html
	@grep -q 'viewrab.js?v=' $(WEB)/index.html || { echo "web/index.html: the version was not stamped"; exit 1; }
	@ls -lh $(WEB)/viewrab.wasm | awk '{print "built " $$9 " (" $$5 ")"}'

## serve-web: serve bin/web on http://127.0.0.1:$(PORT)
PORT ?= 8080
serve-web: build-web
	python3 -m http.server -d $(WEB) -b 127.0.0.1 $(PORT)

## winres: the .exe's resources, as .syso files go build links on Windows
winres:
	$(WINRES) make --in build/windows/winres.json --arch amd64,arm64 --out cmd/nativeview/rsrc \
		--product-version $(WIN_VERSION) --file-version $(WIN_VERSION)

## check-licenses: fail when build/licenses, the notices of what gopdfrab
##                 compiles into Viewrab, differs from the required gopdfrab's
check-licenses:
	go mod download github.com/voidrab/gopdfrab
	dir=$$(go list -m -f '{{.Dir}}' github.com/voidrab/gopdfrab) && \
		cmp "$$dir/NOTICE" build/licenses/gopdfrab-NOTICE.txt && \
		cmp "$$dir/internal/fontsubst/fonts/OFL.txt" build/licenses/OFL.txt

## test-windows: vet for Windows, then run the Win32 backend's tests, live
##               ones included, under Wine (WINE=wine, or set it to a runner)
WINE ?= wine
test-windows:
	GOOS=windows go vet ./...
	GOOS=windows go test -c -o bin/win32.test.exe ./internal/ui/platform/win32
	$(WINE) bin/win32.test.exe -test.v

## test: vet and test, held to 100% coverage
test:
	go vet ./...
	GOOS=js GOARCH=wasm go vet ./cmd/web ./internal/ui/platform/web
	go test -race -coverprofile=coverage.out ./internal/...
	@go tool cover -func=coverage.out | awk '/^total:/ { \
		if ($$3 != "100.0%") { print "FAIL: Go coverage " $$3 ", want 100.0%"; exit 1 } \
		print "Go coverage " $$3 }'

## cover: open the HTML coverage report
cover: test
	go tool cover -html=coverage.out

## dist: release archives for Linux and Windows, amd64 and arm64, and the
##       vendored source, in dist/
dist:
	rm -rf $(DIST)
	$(MAKE) dist-linux ARCH=amd64
	$(MAKE) dist-linux ARCH=arm64
	$(MAKE) dist-windows ARCH=amd64
	$(MAKE) dist-windows ARCH=arm64
	$(MAKE) dist-src

# TAR packs a directory of dist/ the same way every time; ZIP zips one, with
# zip or else libarchive's bsdtar.
TAR = tar -C $(DIST) --sort=name --owner=0 --group=0 --numeric-owner -czf
ZIP = $(if $(shell command -v zip),zip -qrX,bsdtar -a -cf)

# D is the directory each dist target packs, named as its archive is.
dist-linux:   D = viewrab-$(DIST_VERSION)-linux-$(ARCH)
dist-windows: D = viewrab-$(DIST_VERSION)-windows-$(ARCH)
dist-src:     D = viewrab-$(DIST_VERSION)

## dist-linux: dist/viewrab-<version>-linux-<arch>.tar.gz, which installs
##             with make install (sudo, or PREFIX=~/.local)
dist-linux:
	rm -rf $(DIST)/$(D) && mkdir -p $(DIST)/$(D)/bin $(DIST)/$(D)/build
	$(MAKE) build BIN=$(DIST)/$(D)/bin/viewrab
	cp -r build/appicon.png build/linux build/licenses $(DIST)/$(D)/build/
	cp Makefile README.md LICENSE $(DIST)/$(D)/
	$(TAR) $(DIST)/$(D).tar.gz $(D)
	rm -rf $(DIST)/$(D)

## dist-windows: dist/viewrab-<version>-windows-<arch>.zip, the portable
##               program; packaging/windows/installers.ps1 turns it into the
##               installer, on Windows
dist-windows:
	rm -rf $(DIST)/$(D) && mkdir -p $(DIST)/$(D)
	$(MAKE) build-windows BIN=$(DIST)/$(D)/viewrab
	cp LICENSE $(DIST)/$(D)/LICENSE.txt
	cp build/licenses/* $(DIST)/$(D)/
	cd $(DIST) && rm -f $(D).zip && $(ZIP) $(D).zip $(D)
	rm -rf $(DIST)/$(D)

## dist-src: dist/viewrab-<version>-vendored.tar.gz, the source with every
##           module it needs, which builds offline (Flatpak, distributions)
dist-src:
	rm -rf $(DIST)/$(D) && mkdir -p $(DIST)
	git archive --prefix=$(D)/ HEAD | tar -x -C $(DIST)
	cd $(DIST)/$(D) && go mod vendor
	$(TAR) $(DIST)/$(D)-vendored.tar.gz $(D)
	rm -rf $(DIST)/$(D)

## install: install the built binary, desktop entry, metainfo, icon and
##          licences system-wide (make build && sudo make install;
##          PREFIX=~/.local for one user)
install:
	@test -x $(BIN) || { echo "$(BIN) is missing: run 'make build' first, without sudo"; exit 1; }
	install -Dm755 $(BIN) $(DESTDIR)$(PREFIX)/bin/$(NAME)
	sed 's|^Exec=viewrab |Exec=$(NAME) |' build/linux/$(APP_ID).desktop \
		| install -Dm644 /dev/stdin $(APPS_DIR)/$(APP_ID).desktop
	sed 's|<binary>viewrab</binary>|<binary>$(NAME)</binary>|' build/linux/$(APP_ID).metainfo.xml \
		| install -Dm644 /dev/stdin $(METAINFO_DIR)/$(APP_ID).metainfo.xml
	install -Dm644 build/appicon.png $(ICONS_DIR)/256x256/apps/$(APP_ID).png
	install -Dm644 -t $(LICENSES_DIR) LICENSE build/licenses/*
	@if [ -z "$(DESTDIR)" ]; then \
		update-desktop-database -q $(APPS_DIR) 2>/dev/null || true; \
		gtk-update-icon-cache -q -t $(ICONS_DIR) 2>/dev/null || true; \
	fi

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/$(NAME) $(APPS_DIR)/$(APP_ID).desktop \
		$(METAINFO_DIR)/$(APP_ID).metainfo.xml $(ICONS_DIR)/256x256/apps/$(APP_ID).png
	rm -rf $(LICENSES_DIR)
	@if [ -z "$(DESTDIR)" ]; then update-desktop-database -q $(APPS_DIR) 2>/dev/null || true; fi

## default: make Viewrab your default PDF application (run as yourself, not root)
default:
	xdg-mime default $(APP_ID).desktop application/pdf
	@echo "default for application/pdf: $$(xdg-mime query default application/pdf)"

clean:
	rm -rf bin $(DIST) coverage.out cmd/nativeview/rsrc_windows_*.syso
