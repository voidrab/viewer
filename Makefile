# Viewer: build, test and run. Plain go + npm; the Wails CLI is not needed.

BIN      ?= bin/viewer
PREFIX   ?= /usr/local
DEV_PORT ?= 9245
APP_ID   := com.voidrab.gopdfrab.Viewer

APPS_DIR  = $(DESTDIR)$(PREFIX)/share/applications
ICONS_DIR = $(DESTDIR)$(PREFIX)/share/icons/hicolor

# Wails' Linux backend calls a few X11 helpers GTK4 marks deprecated; the
# warnings are upstream noise, not ours. Appended to Go's defaults (-O2 -g),
# and only for targets that compile, so `sudo make install` never needs go.
build dev test-go cover: export CGO_CFLAGS = $(shell go env CGO_CFLAGS) -Wno-deprecated-declarations

GO_RELEASE_FLAGS := -tags production -trimpath -ldflags "-s -w"

.PHONY: all build frontend dev test test-go test-frontend cover install uninstall default clean

all: test build

## build: frontend bundle + stripped release binary in bin/
build: frontend
	go build $(GO_RELEASE_FLAGS) -o $(BIN) .
	@ls -lh $(BIN) | awk '{print "built " $$9 " (" $$5 ")"}'

frontend: frontend/node_modules
	cd frontend && npm run build

frontend/node_modules: frontend/package-lock.json
	cd frontend && npm ci
	@touch $@

## dev: run against the Vite dev server, so frontend edits reload live
dev: frontend/node_modules
	@cd frontend && npx vite --host 127.0.0.1 --port $(DEV_PORT) --strictPort & \
	trap 'kill $$!' EXIT; \
	FRONTEND_DEVSERVER_URL=http://127.0.0.1:$(DEV_PORT) go run . $(ARGS)

## test: Go and frontend tests, each held to 100% coverage
test: test-go test-frontend

test-go:
	go vet ./...
	go test -race -coverprofile=coverage.out ./internal/...
	@go tool cover -func=coverage.out | awk '/^total:/ { \
		if ($$3 != "100.0%") { print "FAIL: Go coverage " $$3 ", want 100.0%"; exit 1 } \
		print "Go coverage " $$3 }'

test-frontend: frontend/node_modules
	cd frontend && npx vitest run --coverage

## cover: open the Go HTML coverage report
cover: test-go
	go tool cover -html=coverage.out

## install: install the built binary, desktop entry and icon system-wide
##          (make build && sudo make install; PREFIX=~/.local for one user)
install:
	@test -x $(BIN) || { echo "$(BIN) is missing: run 'make build' first, without sudo"; exit 1; }
	install -Dm755 $(BIN) $(DESTDIR)$(PREFIX)/bin/viewer
	install -Dm644 build/linux/$(APP_ID).desktop $(APPS_DIR)/$(APP_ID).desktop
	install -Dm644 build/appicon.png $(ICONS_DIR)/256x256/apps/$(APP_ID).png
	@if [ -z "$(DESTDIR)" ]; then \
		update-desktop-database -q $(APPS_DIR) 2>/dev/null || true; \
		gtk-update-icon-cache -q -t $(ICONS_DIR) 2>/dev/null || true; \
	fi

uninstall:
	rm -f $(DESTDIR)$(PREFIX)/bin/viewer $(APPS_DIR)/$(APP_ID).desktop \
		$(ICONS_DIR)/256x256/apps/$(APP_ID).png
	@if [ -z "$(DESTDIR)" ]; then update-desktop-database -q $(APPS_DIR) 2>/dev/null || true; fi

## default: make Viewer your default PDF application (run as yourself, not root)
default:
	xdg-mime default $(APP_ID).desktop application/pdf
	@echo "default for application/pdf: $$(xdg-mime query default application/pdf)"

clean:
	rm -rf bin coverage.out frontend/coverage
	find frontend/dist -mindepth 1 ! -name .gitkeep -delete
