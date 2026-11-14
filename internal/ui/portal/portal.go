// Package portal asks the desktop's file chooser, through the
// xdg-desktop-portal D-Bus service, for files to open. It holds the protocol
// logic; the D-Bus connection itself is supplied as a Bus.
package portal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
)

// ErrFailed is returned when the portal reports that the dialog failed.
var ErrFailed = errors.New("the file dialog failed")

// Response is the outcome the portal reports for a request.
type Response struct {
	// Code is 0 for success, 1 when the user cancelled and 2 for any other
	// failure.
	Code uint32
	// URIs are the chosen files as file:// URIs.
	URIs []string
}

// Bus is the part of a D-Bus session connection the file chooser needs.
type Bus interface {
	// Sender returns the connection's unique name, such as ":1.42".
	Sender() string
	// Subscribe delivers the Response signal of the request at path. cancel
	// stops the delivery and must be called when the request is over.
	Subscribe(path string) (responses <-chan Response, cancel func(), err error)
	// OpenFile calls org.freedesktop.portal.FileChooser.OpenFile and returns
	// the object path of the request it started.
	OpenFile(parent, title string, options map[string]any) (request string, err error)
}

// FilterSpec is one pattern of a file filter: a glob (Kind 0) or a MIME type
// (Kind 1).
type FilterSpec struct {
	Kind    uint32
	Pattern string
}

// Filter is a named group of patterns, in the shape the portal's a(sa(us))
// filter option takes.
type Filter struct {
	Name  string
	Specs []FilterSpec
}

// newToken makes the token that names a request; tests replace it.
var newToken = func() string {
	var b [8]byte
	rand.Read(b[:]) // never fails: the documented behaviour of crypto/rand
	return "viewer" + hex.EncodeToString(b[:])
}

// requestPath returns the object path the portal gives the request that
// sender makes with handle token (see the portal's Request documentation).
func requestPath(sender, token string) string {
	sender = strings.ReplaceAll(strings.TrimPrefix(sender, ":"), ".", "_")
	return "/org/freedesktop/portal/desktop/request/" + sender + "/" + token
}

// PickPDFs shows the desktop's file chooser, titled title, for one or more PDF
// files and returns the paths chosen. It returns no paths and no error when the
// user cancels, and ctx.Err() when ctx ends first.
func PickPDFs(ctx context.Context, bus Bus, title string) ([]string, error) {
	token := newToken()
	path := requestPath(bus.Sender(), token)
	// Subscribe first: the response may arrive before OpenFile returns.
	responses, cancel, err := bus.Subscribe(path)
	if err != nil {
		return nil, err
	}
	defer func() { cancel() }()

	request, err := bus.OpenFile("", title, map[string]any{
		"handle_token": token,
		"multiple":     true,
		"filters": []Filter{{
			Name:  "PDF documents",
			Specs: []FilterSpec{{Kind: 1, Pattern: "application/pdf"}, {Kind: 0, Pattern: "*.pdf"}},
		}},
	})
	if err != nil {
		return nil, err
	}
	// An older portal may not honour the token: follow the path it returned.
	if request != path {
		cancel()
		if responses, cancel, err = bus.Subscribe(request); err != nil {
			cancel = func() {}
			return nil, err
		}
	}

	select {
	case resp := <-responses:
		switch resp.Code {
		case 0:
			return filePaths(resp.URIs), nil
		case 1:
			return nil, nil
		}
		return nil, ErrFailed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// filePaths converts file:// URIs to paths, skipping any that are not local
// files.
func filePaths(uris []string) []string {
	var out []string
	for _, s := range uris {
		u, err := url.Parse(s)
		if err != nil || u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") || u.Path == "" {
			continue
		}
		out = append(out, u.Path)
	}
	return out
}
