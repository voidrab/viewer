package widget

import (
	"image"
	"testing"

	"viewrab/internal/ui/paint"
	"viewrab/internal/ui/platform"
	"viewrab/internal/ui/text"
	"viewrab/internal/ui/theme"
)

var aboutWindow = image.Rect(0, 0, 900, 600)

func newAbout(t *testing.T) *About {
	t.Helper()
	face := func(size float64, bold bool) *text.Face {
		f, err := text.Mono().Face(size, bold)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	return NewAbout(palette, face(theme.FontSize, false), face(theme.SmallFontSize, false), face(theme.FontSize+2, true))
}

// aboutPointer sends a in window a pointer event of kind at p.
func aboutPointer(a *About, kind platform.PointerKind, p image.Point) (string, bool) {
	return a.HandlePointer(platform.PointerEvent{Kind: kind, Button: platform.Left, X: float64(p.X), Y: float64(p.Y)}, aboutWindow)
}

// aboutClick clicks a at p and returns the URI it opened, if any.
func aboutClick(a *About, p image.Point) string {
	aboutPointer(a, platform.Move, p)
	aboutPointer(a, platform.Press, p)
	uri, _ := aboutPointer(a, platform.Release, p)
	return uri
}

// centre returns the middle of r.
func centre(r image.Rectangle) image.Point { return r.Min.Add(r.Max).Div(2) }

// linkAt returns the middle of the link to uri.
func linkAt(t *testing.T, a *About, uri string) image.Point {
	t.Helper()
	r := a.LinkRect(uri, aboutWindow)
	if r.Empty() {
		t.Fatalf("no link to %s", uri)
	}
	return centre(r)
}

func TestAboutIsCentredInItsWindow(t *testing.T) {
	a := newAbout(t)
	r := a.Rect(aboutWindow)
	if r.Dx() != aboutWidth || centre(r).Sub(centre(aboutWindow)).X > 1 || !r.In(aboutWindow) {
		t.Errorf("card at %v in %v", r, aboutWindow)
	}
	l := a.layout(aboutWindow)
	if !l.close.In(r) || !l.logo.In(r) {
		t.Errorf("close %v, logo %v outside the card %v", l.close, l.logo, r)
	}
	for _, s := range l.spans {
		if !s.r.In(r) {
			t.Errorf("%q at %v outside the card %v", s.text, s.r, r)
		}
	}
	// A narrow window narrows the card; one too small keeps its corner inside.
	if r := a.Rect(image.Rect(0, 0, 400, 600)); r.Dx() != 400-2*aboutMargin {
		t.Errorf("card %v in a 400 wide window", r)
	}
	if r := a.Rect(image.Rect(10, 20, 40, 50)); r.Min != image.Pt(10, 20) {
		t.Errorf("card %v in a tiny window", r)
	}
}

func TestAboutShowsTheVersion(t *testing.T) {
	a := newAbout(t)
	text := func() string { return a.rows()[1].spans[0].text }
	if got := text(); got != "Development build" {
		t.Errorf("no version: %q", got)
	}
	a.Version = "v1.2.0"
	if got := text(); got != "Version v1.2.0" {
		t.Errorf("with a version: %q", got)
	}
}

func TestAboutDraw(t *testing.T) {
	a := newAbout(t)
	c := paint.New(900, 600)
	c.Fill(c.Bounds(), palette.Canvas)
	a.Draw(c, aboutWindow)
	if got := c.Img.RGBAAt(5, 5); got != nrgba(palette.Canvas) {
		t.Error("a closed dialog drew")
	}
	a.Show()
	for _, p := range []theme.Palette{theme.Light(), theme.Dark()} {
		a.Palette = p
		c.Fill(c.Bounds(), p.Canvas)
		aboutPointer(a, platform.Move, centre(a.layout(aboutWindow).close))
		a.Draw(c, aboutWindow)
		if got := c.Img.RGBAAt(5, 5); got == nrgba(p.Canvas) {
			t.Error("the window behind the dialog is not dimmed")
		}
		aboutPointer(a, platform.Move, linkAt(t, a, WebsiteURL))
		a.Draw(c, aboutWindow)
	}
}

func TestAboutLinks(t *testing.T) {
	a := newAbout(t)
	a.Show()
	for _, uri := range []string{WebsiteURL, LicenseURL} {
		p := linkAt(t, a, uri)
		aboutPointer(a, platform.Move, p)
		if !a.OverLink() {
			t.Errorf("not over the link to %s", uri)
		}
		if got := aboutClick(a, p); got != uri || !a.Open {
			t.Errorf("clicking the link to %s opened %q, open %v", uri, got, a.Open)
		}
	}
	// Plain text, a release away from its press and a right click open nothing.
	card := a.Rect(aboutWindow)
	if got := aboutClick(a, card.Min.Add(image.Pt(4, 4))); got != "" || a.OverLink() {
		t.Errorf("a click on the card opened %q", got)
	}
	aboutPointer(a, platform.Press, linkAt(t, a, WebsiteURL))
	if got, _ := aboutPointer(a, platform.Release, linkAt(t, a, LicenseURL)); got != "" {
		t.Errorf("a release on another link opened %q", got)
	}
	p := linkAt(t, a, WebsiteURL)
	aboutPointer(a, platform.Press, p)
	if got, _ := a.HandlePointer(platform.PointerEvent{Kind: platform.Release, Button: platform.Right, X: float64(p.X), Y: float64(p.Y)}, aboutWindow); got != "" {
		t.Errorf("a right release opened %q", got)
	}
	// Leaving the window ends the hover; a closed dialog is over no link.
	aboutPointer(a, platform.Move, p)
	aboutPointer(a, platform.Leave, p)
	if a.OverLink() {
		t.Error("still over a link after leaving")
	}
	aboutPointer(a, platform.Move, p)
	a.Close()
	if a.OverLink() {
		t.Error("a closed dialog is over a link")
	}
	if !a.LinkRect("https://example.com", aboutWindow).Empty() {
		t.Error("a link to a page the dialog does not name")
	}
	if (aboutLayout{}).link(0) != "" {
		t.Error("a link where there is none")
	}
}

func TestAboutCloses(t *testing.T) {
	a := newAbout(t)
	if _, taken := aboutPointer(a, platform.Press, image.Pt(5, 5)); taken {
		t.Error("a closed dialog took an event")
	}
	// The close button closes it.
	a.Show()
	if aboutClick(a, centre(a.layout(aboutWindow).close)); a.Open {
		t.Error("the close button left it open")
	}
	// A press outside the card closes it, and the release is taken too.
	a.Show()
	if _, taken := aboutPointer(a, platform.Press, image.Pt(5, 5)); !taken || a.Open {
		t.Errorf("a press outside: taken %v, open %v", taken, a.Open)
	}
	if _, taken := aboutPointer(a, platform.Release, image.Pt(5, 5)); !taken {
		t.Error("the release of the closing press was not taken")
	}
	if _, taken := aboutPointer(a, platform.Release, image.Pt(5, 5)); taken {
		t.Error("a later release was taken")
	}
	// Escape closes it; other keys and releases do not.
	a.Show()
	a.HandleKey(platform.KeyEvent{Key: platform.KeyEscape})
	a.HandleKey(platform.KeyEvent{Rune: 'x', Down: true})
	if !a.Open {
		t.Error("a release or another key closed it")
	}
	a.HandleKey(platform.KeyEvent{Key: platform.KeyEscape, Down: true})
	if a.Open {
		t.Error("Escape left it open")
	}
}
