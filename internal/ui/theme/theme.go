// Package theme defines the colours and sizes the viewer's interface is drawn
// with, in a light and a dark palette.
package theme

import "image/color"

// Palette is the set of colours the UI is drawn with.
type Palette struct {
	Chrome    color.NRGBA // toolbar, tab bar and status line
	Canvas    color.NRGBA // behind the pages, and the active tab
	Raised    color.NRGBA // inputs and panels
	Border    color.NRGBA
	Text      color.NRGBA
	Muted     color.NRGBA
	Faint     color.NRGBA
	Hover     color.NRGBA // a translucent wash over a hovered control
	Active    color.NRGBA // a translucent wash over a pressed control
	Accent    color.NRGBA
	OK        color.NRGBA
	Bad       color.NRGBA
	Warn      color.NRGBA
	Selection color.NRGBA // translucent text selection
	// Dark is set in the dark palette.
	Dark bool
}

// Of returns the dark palette if dark is set, or else the light one.
func Of(dark bool) Palette {
	if dark {
		return Dark()
	}
	return Light()
}

// Light returns the light palette.
func Light() Palette {
	return Palette{
		Chrome: rgb(0xf3f3f4), Canvas: rgb(0xe2e2e5), Raised: rgb(0xffffff), Border: rgb(0xdadadf),
		Text: rgb(0x1f2024), Muted: rgb(0x55575f), Faint: rgb(0x85878f),
		Hover: color.NRGBA{0, 0, 0, 15}, Active: color.NRGBA{0, 0, 0, 26},
		Accent: rgb(0x3b72d9), OK: rgb(0x3f8f4e), Bad: rgb(0xc44b4b), Warn: rgb(0xa6741c),
		Selection: color.NRGBA{59, 114, 217, 71},
	}
}

// Dark returns the dark palette.
func Dark() Palette {
	return Palette{
		Chrome: rgb(0x1b1c1f), Canvas: rgb(0x121315), Raised: rgb(0x24252a), Border: rgb(0x2c2d32),
		Text: rgb(0xe2e4e9), Muted: rgb(0xa6a9b2), Faint: rgb(0x767981),
		Hover: color.NRGBA{255, 255, 255, 15}, Active: color.NRGBA{255, 255, 255, 26},
		Accent: rgb(0x74ade8), OK: rgb(0xa1c181), Bad: rgb(0xe07a7f), Warn: rgb(0xdec184),
		Selection: color.NRGBA{116, 173, 232, 77}, Dark: true,
	}
}

// rgb converts 0xRRGGBB to an opaque colour.
func rgb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}
}

// CloseHover is the red behind a hovered close-window control, in either
// palette.
var CloseHover = rgb(0xd9534f)

// Sizes, in pixels at scale 1, as in the webview build's style sheet.
const (
	FontSize      = 13.0
	SmallFontSize = 12.5
	ToolbarHeight = 36
	TabBarHeight  = 32
	StatusHeight  = 28
	// PageGap is the space between pages and PageMargin the space around
	// them, which pdf.js leaves too.
	PageGap    = 10
	PageMargin = 20
	// Radius rounds the corners of buttons, fields and hover marks.
	Radius = 4
)
