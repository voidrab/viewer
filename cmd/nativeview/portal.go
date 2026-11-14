package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"

	"viewer/internal/ui/portal"
)

const (
	portalName      = "org.freedesktop.portal.Desktop"
	portalPath      = "/org/freedesktop/portal/desktop"
	chooserOpenFile = "org.freedesktop.portal.FileChooser.OpenFile"
	requestIface    = "org.freedesktop.portal.Request"
	responseSignal  = requestIface + ".Response"
)

// dialogTimeout bounds how long a file dialog may stay open.
const dialogTimeout = 30 * time.Minute

// pickFiles shows the desktop's file chooser and returns the PDFs chosen. It
// connects to the session bus afresh each time, so a dialog never depends on
// the state of an earlier one.
func pickFiles() ([]string, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("no desktop file dialog: %w", err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), dialogTimeout)
	defer cancel()
	return portal.PickPDFs(ctx, dbusBus{conn}, "Open PDF")
}

// dbusBus adapts a godbus connection to portal.Bus.
type dbusBus struct{ conn *dbus.Conn }

func (b dbusBus) Sender() string {
	if names := b.conn.Names(); len(names) > 0 {
		return names[0]
	}
	return ""
}

func (b dbusBus) Subscribe(path string) (<-chan portal.Response, func(), error) {
	match := []dbus.MatchOption{
		dbus.WithMatchObjectPath(dbus.ObjectPath(path)),
		dbus.WithMatchInterface(requestIface),
		dbus.WithMatchMember("Response"),
	}
	if err := b.conn.AddMatchSignal(match...); err != nil {
		return nil, func() {}, err
	}
	signals := make(chan *dbus.Signal, 8)
	b.conn.Signal(signals)
	out := make(chan portal.Response, 1)
	go func() {
		for sig := range signals {
			if sig.Path != dbus.ObjectPath(path) || sig.Name != responseSignal {
				continue
			}
			if resp, ok := parseResponse(sig.Body); ok {
				out <- resp
				return
			}
		}
	}()
	cancel := func() {
		b.conn.RemoveSignal(signals)
		b.conn.RemoveMatchSignal(match...)
		close(signals)
	}
	return out, cancel, nil
}

// parseResponse reads the body of a Request.Response signal: the response code
// and a dictionary of results that holds the chosen URIs.
func parseResponse(body []any) (portal.Response, bool) {
	if len(body) != 2 {
		return portal.Response{}, false
	}
	code, ok := body[0].(uint32)
	if !ok {
		return portal.Response{}, false
	}
	resp := portal.Response{Code: code}
	if results, ok := body[1].(map[string]dbus.Variant); ok {
		if uris, ok := results["uris"].Value().([]string); ok {
			resp.URIs = uris
		}
	}
	return resp, true
}

func (b dbusBus) OpenFile(parent, title string, options map[string]any) (string, error) {
	vars := make(map[string]dbus.Variant, len(options))
	for k, v := range options {
		vars[k] = dbus.MakeVariant(v)
	}
	var request dbus.ObjectPath
	call := b.conn.Object(portalName, portalPath).Call(chooserOpenFile, 0, parent, title, vars)
	if err := call.Store(&request); err != nil {
		return "", errors.Join(errors.New("the desktop has no file dialog"), err)
	}
	return string(request), nil
}
