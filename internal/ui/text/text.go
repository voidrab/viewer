// Package text measures and draws text in the viewer's monospace UI font.
package text

import (
	_ "embed"
	"errors"
	"image"
	"image/color"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"viewer/internal/ui/paint"
)

// The embedded Liberation Mono fonts are licensed under the SIL Open Font
// License 1.1; see fonts/OFL.txt.
var (
	//go:embed fonts/LiberationMono-Regular.ttf
	monoRegular []byte
	//go:embed fonts/LiberationMono-Bold.ttf
	monoBold []byte
)

// ErrSize is returned for a font size that is not positive.
var ErrSize = errors.New("text: font size must be positive")

// parseFont and newFace are replaced by tests to force their failures.
var (
	parseFont = opentype.Parse
	newFace   = opentype.NewFace
)

// Family is a regular and a bold face of one typeface, and the sized Faces
// made from them.
type Family struct {
	regular, bold []byte

	mu    sync.Mutex
	fonts map[bool]*opentype.Font
	faces map[faceKey]*Face
}

type faceKey struct {
	size float64
	bold bool
}

// Mono returns the monospace family the UI is set in.
func Mono() *Family {
	return &Family{regular: monoRegular, bold: monoBold}
}

// Face returns the face at size pixels, bold or regular, creating it on first
// use.
func (fam *Family) Face(size float64, bold bool) (*Face, error) {
	if !(size > 0) { // also rejects NaN
		return nil, ErrSize
	}
	fam.mu.Lock()
	defer fam.mu.Unlock()
	key := faceKey{size, bold}
	if f := fam.faces[key]; f != nil {
		return f, nil
	}
	if fam.fonts == nil {
		fam.fonts, fam.faces = map[bool]*opentype.Font{}, map[faceKey]*Face{}
	}
	f := fam.fonts[bold]
	if f == nil {
		data := fam.regular
		if bold {
			data = fam.bold
		}
		var err error
		if f, err = parseFont(data); err != nil {
			return nil, err
		}
		fam.fonts[bold] = f
	}
	face, err := newFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return nil, err
	}
	m := face.Metrics()
	out := &Face{face: face, ascent: m.Ascent.Ceil(), descent: m.Descent.Ceil()}
	fam.faces[key] = out
	return out, nil
}

// Face is a typeface at one size.
type Face struct {
	face            font.Face
	ascent, descent int
}

// Ascent returns the pixels from the baseline up to the top of a line.
func (f *Face) Ascent() int { return f.ascent }

// Height returns the height in pixels of a line of text.
func (f *Face) Height() int { return f.ascent + f.descent }

// Width returns the advance in pixels of s.
func (f *Face) Width(s string) int { return font.MeasureString(f.face, s).Ceil() }

// Draw paints s in col with the top-left of its line at (x, y), within the
// canvas's clip.
func (f *Face) Draw(c *paint.Canvas, x, y int, s string, col color.NRGBA) {
	w := f.Width(s)
	if s == "" || w == 0 || col.A == 0 {
		return
	}
	line := image.NewRGBA(image.Rect(0, 0, w, f.Height()))
	d := font.Drawer{
		Dst: line, Src: image.NewUniform(col), Face: f.face,
		Dot: fixed.P(0, f.ascent),
	}
	d.DrawString(s)
	c.DrawImage(image.Pt(x, y), line)
}

// Fit returns s shortened with a trailing ellipsis so that it is at most
// maxWidth pixels wide, or s itself if it already fits. When even the
// ellipsis does not fit, Fit returns "".
func (f *Face) Fit(s string, maxWidth int) string {
	if f.Width(s) <= maxWidth {
		return s
	}
	const ellipsis = "…"
	room := maxWidth - f.Width(ellipsis)
	if room < 0 {
		return ""
	}
	runes := []rune(s)
	for n := len(runes); n > 0; n-- {
		if f.Width(string(runes[:n])) <= room {
			return string(runes[:n]) + ellipsis
		}
	}
	return ellipsis
}
