package icon

import (
	"bytes"
	_ "embed"
	"image"
	"image/png"
	"sync"

	"golang.org/x/image/draw"
)

//go:embed gopher.png
var gopherPNG []byte

var (
	logoMu    sync.Mutex
	logoSrc   image.Image
	logoSizes = map[int]*image.RGBA{}
)

// Logo returns the app's gopher icon, size device pixels square and premultiplied for
// paint.Canvas.DrawImage. Each size is scaled once and then reused, so the
// result must not be modified.
func Logo(size int) *image.RGBA {
	logoMu.Lock()
	defer logoMu.Unlock()
	if img := logoSizes[size]; img != nil {
		return img
	}
	if logoSrc == nil {
		// The image is part of the binary and known to decode.
		logoSrc, _ = png.Decode(bytes.NewReader(gopherPNG))
	}
	img := image.NewRGBA(image.Rect(0, 0, max(size, 0), max(size, 0)))
	draw.CatmullRom.Scale(img, img.Rect, logoSrc, logoSrc.Bounds(), draw.Src, nil)
	logoSizes[size] = img
	return img
}
