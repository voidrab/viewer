// Package desktop holds what the windowing backends share of the Linux
// desktop's conventions: keysyms, the text/uri-list format of dragged files,
// the limits on clipboard transfers, and the BGRA pixel layout servers take.
package desktop

import (
	"image"
	"net/url"
	"strings"
	"time"
)

// URIListMime is the format files are dragged in: a list of URIs, one per
// line.
const URIListMime = "text/uri-list"

// MaxPaste bounds how much clipboard text, or dropped data, is read, and
// PasteTimeout bounds how long the program that holds it has to supply it.
const (
	MaxPaste     = 8 << 20
	PasteTimeout = 2 * time.Second
)

// ParseURIList returns the local paths of the file URIs in list, a
// text/uri-list, given the name of this host: comments, other schemes and
// files on other hosts are left out.
func ParseURIList(list, host string) []string {
	var paths []string
	for _, line := range strings.Split(list, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil || u.Scheme != "file" || u.Path == "" {
			continue
		}
		if u.Host == "" || u.Host == "localhost" || u.Host == host {
			paths = append(paths, u.Path)
		}
	}
	return paths
}

// CopyBGRA copies src, premultiplied RGBA, into dst in the blue, green, red,
// alpha byte order of Wayland's ARGB8888 and of the usual 32-bit X11 pixel on
// little-endian hosts. dst must hold the whole image, and stride is the length
// of a row of dst in bytes.
func CopyBGRA(dst []byte, stride int, src *image.RGBA) {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	for y := range h {
		s := src.Pix[y*src.Stride:][:4*w]
		d := dst[y*stride:][:4*w]
		for i := 0; i < len(s); i += 4 {
			d[i], d[i+1], d[i+2], d[i+3] = s[i+2], s[i+1], s[i], s[i+3]
		}
	}
}
