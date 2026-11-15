# Viewrab: build, test and install. Plain go; no cgo.

BIN      ?= bin/viewrab
PREFIX   ?= /usr/local
# The command name the binary is installed under.
NAME     ?= viewrab
APP_ID   := com.voidrab.gopdfrab.Viewrab

APPS_DIR  = $(DESTDIR)$(PREFIX)/share/applications
ICONS_DIR = $(DESTDIR)$(PREFIX)/share/icons/hicolor

GO_FLAGS := -trimpath -ldflags "-s -w"

.PHONY: all build build-windows test test-windows cover install uninstall default clean

all: test build

## build: static, stripped binary in bin/
build:
	CGO_ENABLED=0 go build $(GO_FLAGS) -o $(BIN) ./cmd/nativeview
	@ls -lh $(BIN) | awk '{print "built " $$9 " (" $$5 ")"}'

## build-windows: bin/viewrab.exe, a Windows GUI program with no console
build-windows:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H windowsgui" -o $(BIN).exe ./cmd/nativeview
	@ls -lh $(BIN).exe | awk '{print "built " $$9 " (" $$5 ")"}'

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
	go test -race -coverprofile=coverage.out ./internal/...
	@go tool cover -func=coverage.out | awk '/^total:/ { \
		if ($$3 != "100.0%") { print "FAIL: Go coverage " $$3 ", want 100.0%"; exit 1 } \
		print "Go coverage " $$3 }'

## cover: open the HTML coverage report
cover: test
	go tool cover -html=coverage.out

## install: install the built binary, desktop entry and icon system-wide
##          (make build && sudo make install; PREFIX=~/.local for one user)
install:
	@test -x $(BIN) || { echo "$(BIN) is missing: run 'make build' first, without sudo"; exit 1; }
	install -Dm755 $(BIN) $(DESTDIR)$(PREFIX)/bin/$(NAME)
	sed 's|^Exec=viewrab %f|Exec=$(NAME) %f|' build/linux/$(APP_ID).desktop \
		| install -Dm644 /dev/stdin $(APPS_DIR)/$(APP_ID).desktop
	install -Dm644 build/appicon.png $(ICONS_DIR)/256x256/apps/$(APP_ID).png
	@if [ -z "$(DESTDIR)" ]; then \
		update-desktop-database -q $(APPS_DIR) 2>/dev/null || true; \
		gtk-update-icon-cache -q -t $(ICONS_DIR) 2>/dev/null || true; \
	fi

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/$(NAME) $(APPS_DIR)/$(APP_ID).desktop \
		$(ICONS_DIR)/256x256/apps/$(APP_ID).png
	@if [ -z "$(DESTDIR)" ]; then update-desktop-database -q $(APPS_DIR) 2>/dev/null || true; fi

## default: make Viewrab your default PDF application (run as yourself, not root)
default:
	xdg-mime default $(APP_ID).desktop application/pdf
	@echo "default for application/pdf: $$(xdg-mime query default application/pdf)"

clean:
	rm -rf bin coverage.out
