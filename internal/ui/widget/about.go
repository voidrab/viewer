package widget

import (
	"image"
	"image/color"

	"viewrab/internal/ui/icon"
	"viewrab/internal/ui/paint"
	"viewrab/internal/ui/platform"
	"viewrab/internal/ui/text"
	"viewrab/internal/ui/theme"
)

// The pages the About dialog links to.
const (
	WebsiteURL = "https://github.com/voidrab/viewrab"
	LicenseURL = "https://www.gnu.org/licenses/agpl-3.0.html"
)

// About dialog geometry, in pixels.
const (
	aboutWidth  = 440 // the card, at most
	aboutNarrow = 280 // the card, at least
	aboutMargin = 16  // the least room around the card
	aboutPad    = 24  // inside the card, above and below its content
	aboutLogo   = 96
	aboutClose  = 26 // the close button
	aboutRadius = 10
)

// aboutScrim dims the window behind the dialog.
var aboutScrim = color.NRGBA{0, 0, 0, 96}

// aboutStyle is how a span of the dialog's text is drawn.
type aboutStyle int

const (
	aboutTitle aboutStyle = iota // the program's name
	aboutBody                    // the version and description
	aboutMuted                   // the copyright and licence notice
	aboutLink                    // a link, in the accent colour and underlined
)

// aboutSpan is a run of text in one style; a link has the URI it opens.
type aboutSpan struct {
	text  string
	style aboutStyle
	uri   string
}

// aboutRow is a centred line of spans, gap pixels below the one before it.
type aboutRow struct {
	gap   int
	spans []aboutSpan
}

// aboutPlaced is a span with where it is drawn.
type aboutPlaced struct {
	aboutSpan
	r image.Rectangle
}

// aboutLayout is where everything in the dialog goes, in window coordinates.
type aboutLayout struct {
	card, close, logo image.Rectangle
	spans             []aboutPlaced
}

// aboutNothing and aboutCloseIt are the hit values of no target and of the
// close button. A link's hit value is its index from 0, in the order the links
// are laid out.
const (
	aboutNothing = -1
	aboutCloseIt = -2
)

// About is the dialog that names the program, its version, its website and
// its licence. While it is open it is drawn over everything else, on a dimmed
// window, and takes every pointer and key event. Its coordinates are the
// window's.
type About struct {
	Palette           theme.Palette
	Face, Small, Bold *text.Face
	// Version is the program's version, or empty for a development build.
	Version string
	// Open is set while the dialog shows.
	Open bool

	hover, press int  // the target under the pointer, and the one pressed
	swallow      bool // the release of the press that closed the dialog is still to come
}

// NewAbout returns a closed About dialog drawing its text with face, small
// and bold.
func NewAbout(palette theme.Palette, face, small, bold *text.Face) *About {
	return &About{Palette: palette, Face: face, Small: small, Bold: bold, hover: aboutNothing, press: aboutNothing}
}

// Show opens the dialog.
func (a *About) Show() { a.Open, a.hover, a.press = true, aboutNothing, aboutNothing }

// Close closes the dialog.
func (a *About) Close() { a.Open, a.hover, a.press = false, aboutNothing, aboutNothing }

// OverLink reports whether the pointer is on one of the open dialog's links.
func (a *About) OverLink() bool { return a.Open && a.hover >= 0 }

// rows returns the dialog's text, below the logo.
func (a *About) rows() []aboutRow {
	return []aboutRow{
		{16, []aboutSpan{{"Viewrab", aboutTitle, ""}}},
		{10, []aboutSpan{{versionText(a.Version), aboutBody, ""}}},
		{10, []aboutSpan{{"A fast, small and simple PDF viewer", aboutBody, ""}}},
		{2, []aboutSpan{{"with PDF/A validation", aboutBody, ""}}},
		{12, []aboutSpan{{"Website", aboutLink, WebsiteURL}}},
		{14, []aboutSpan{{"© 2026 The Viewrab authors", aboutMuted, ""}}},
		{10, []aboutSpan{{"This program comes with absolutely no warranty.", aboutMuted, ""}}},
		{2, []aboutSpan{
			{"See the ", aboutMuted, ""},
			{"GNU AGPL, version 3", aboutLink, LicenseURL},
			{" for details.", aboutMuted, ""},
		}},
	}
}

// versionText returns the version line of the About dialog. A release
// version, which begins with "v" and a digit, is shown as "Version v1.2.0".
// Any other value, such as the commit of a build from an untagged tree, is
// shown as a development build.
func versionText(version string) string {
	switch {
	case version == "":
		return "Development build"
	case len(version) > 1 && version[0] == 'v' && version[1] >= '0' && version[1] <= '9':
		return "Version " + version
	}
	return "Development build (" + version + ")"
}

// face returns the font face that a span of style is drawn in.
func (a *About) face(style aboutStyle) *text.Face {
	switch style {
	case aboutTitle:
		return a.Bold
	case aboutMuted:
		return a.Small
	}
	return a.Face
}

// layout places the dialog in window: a card centred in it, as wide as
// aboutWidth allows, with the close button in its top-right corner, then the
// logo and the rows of text, each centred. A window too small for the card
// keeps the card's top-left corner inside it, and the card is cut off.
func (a *About) layout(window image.Rectangle) aboutLayout {
	rows := a.rows()
	h := aboutPad + aboutLogo + aboutPad
	for _, row := range rows {
		h += row.gap + a.rowHeight(row)
	}
	w := max(min(aboutWidth, window.Dx()-2*aboutMargin), aboutNarrow)
	x := window.Min.X + max((window.Dx()-w)/2, 0)
	y := window.Min.Y + max((window.Dy()-h)/2, 0)
	var l aboutLayout
	l.card = image.Rect(x, y, x+w, y+h)
	l.close = image.Rect(l.card.Max.X-8-aboutClose, y+8, l.card.Max.X-8, y+8+aboutClose)
	cx := x + w/2
	l.logo = image.Rect(cx-aboutLogo/2, y+aboutPad, cx+aboutLogo/2, y+aboutPad+aboutLogo)
	y = l.logo.Max.Y
	for _, row := range rows {
		y += row.gap
		rh := a.rowHeight(row)
		width := 0
		for _, s := range row.spans {
			width += a.face(s.style).Width(s.text)
		}
		sx := cx - width/2
		for _, s := range row.spans {
			f := a.face(s.style)
			sw := f.Width(s.text)
			// Each span sits on the row's baseline.
			top := y + a.rowAscent(row) - f.Ascent()
			l.spans = append(l.spans, aboutPlaced{s, image.Rect(sx, top, sx+sw, top+f.Height())})
			sx += sw
		}
		y += rh
	}
	return l
}

// rowAscent returns the tallest ascent of row's faces.
func (a *About) rowAscent(row aboutRow) int {
	asc := 0
	for _, s := range row.spans {
		asc = max(asc, a.face(s.style).Ascent())
	}
	return asc
}

// rowHeight returns the ascent of row plus the deepest descent of its faces.
func (a *About) rowHeight(row aboutRow) int {
	desc := 0
	for _, s := range row.spans {
		f := a.face(s.style)
		desc = max(desc, f.Height()-f.Ascent())
	}
	return a.rowAscent(row) + desc
}

// Rect returns where the dialog's card is in window.
func (a *About) Rect(window image.Rectangle) image.Rectangle { return a.layout(window).card }

// LinkRect returns where the link to uri is in window, or an empty rectangle
// if the dialog has no such link.
func (a *About) LinkRect(uri string, window image.Rectangle) image.Rectangle {
	for _, s := range a.layout(window).spans {
		if s.uri == uri {
			return s.r
		}
	}
	return image.Rectangle{}
}

// hit returns the target at p: a link's number, aboutCloseIt, or
// aboutNothing.
func (l aboutLayout) hit(p image.Point) int {
	if p.In(l.close) {
		return aboutCloseIt
	}
	n := 0
	for _, s := range l.spans {
		if s.uri == "" {
			continue
		}
		if p.In(s.r.Inset(-2)) {
			return n
		}
		n++
	}
	return aboutNothing
}

// link returns the URI of link n.
func (l aboutLayout) link(n int) string {
	for _, s := range l.spans {
		if s.uri == "" {
			continue
		}
		if n == 0 {
			return s.uri
		}
		n--
	}
	return ""
}

// Draw paints the dialog over window if it is open.
func (a *About) Draw(c *paint.Canvas, window image.Rectangle) {
	if !a.Open {
		return
	}
	p := a.Palette
	l := a.layout(window)
	c.Fill(window, aboutScrim)
	c.Shadow(l.card, aboutRadius, 2, menuShadow)
	c.FillRound(l.card, aboutRadius, p.Raised)
	c.StrokeRound(l.card, aboutRadius, 1, p.Border)
	c.PushClip(l.card)
	defer c.PopClip()

	fg := p.Muted
	if a.hover == aboutCloseIt {
		c.FillRound(l.close, theme.Radius, p.Hover)
		fg = p.Text
	}
	icon.Draw(c, l.close.Inset((aboutClose-toolIcon)/2), icon.Close, fg, 1.6)
	c.DrawImage(l.logo.Min, icon.Logo(aboutLogo))

	link := 0
	for _, s := range l.spans {
		col := p.Text
		switch s.style {
		case aboutTitle, aboutLink:
			col = p.Accent
		case aboutMuted:
			col = p.Muted
		}
		a.face(s.style).Draw(c, s.r.Min.X, s.r.Min.Y, s.text, col)
		if s.style == aboutLink {
			if link == a.hover {
				col = p.Text
			}
			base := s.r.Min.Y + a.face(s.style).Ascent() + 2
			c.Fill(image.Rect(s.r.Min.X, base, s.r.Max.X, base+1), col)
			link++
		}
	}
}

// HandlePointer applies ev to the dialog, which is laid out in window. While
// the dialog is open it takes every event: a click on a link returns its URI,
// a click on the close button closes the dialog, and a press outside the card
// closes it too. It also takes the release of the press that closed it. It
// returns false when the event is not the dialog's.
func (a *About) HandlePointer(ev platform.PointerEvent, window image.Rectangle) (uri string, taken bool) {
	if !a.Open {
		if a.swallow && ev.Kind == platform.Release {
			a.swallow = false
			return "", true
		}
		return "", false
	}
	l := a.layout(window)
	pt := image.Pt(int(ev.X), int(ev.Y))
	at := l.hit(pt)
	switch ev.Kind {
	case platform.Move, platform.Enter:
		a.hover = at
	case platform.Leave:
		a.hover = aboutNothing
	case platform.Press:
		if !pt.In(l.card) {
			a.Close()
			a.swallow = true
			break
		}
		a.press = at
	case platform.Release:
		pressed := a.press
		a.press = aboutNothing
		if ev.Button != platform.Left || pressed == aboutNothing || pressed != at {
			break
		}
		if at == aboutCloseIt {
			a.Close()
			break
		}
		return l.link(at), true
	}
	return "", true
}

// HandleKey applies a key press while the dialog is open: Escape closes it.
func (a *About) HandleKey(ev platform.KeyEvent) {
	if ev.Down && ev.Key == platform.KeyEscape {
		a.Close()
	}
}
