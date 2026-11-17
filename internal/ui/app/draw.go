package app

import (
	"fmt"
	"image"
	"strings"

	"viewrab/internal/ui/icon"
	"viewrab/internal/ui/pageview"
	"viewrab/internal/ui/theme"

	"viewrab/internal/ui/paint"
	"viewrab/internal/ui/platform"
	"viewrab/internal/ui/widget"
)

// humanSize formats a byte count for display, with one decimal below 10:
// "512 B", "1.5 KB", "270 KB".
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	size, suffix := float64(n), ""
	for _, s := range []string{"KB", "MB", "GB"} {
		size /= unit
		suffix = s
		if size < unit {
			break
		}
	}
	if size < 10 {
		return fmt.Sprintf("%.1f %s", size, suffix)
	}
	return fmt.Sprintf("%.0f %s", size, suffix)
}

// statusInfo is the line about document d in the status bar: its name, page
// count, size and PDF version.
func statusInfo(d *document) string {
	parts := []string{d.name, fmt.Sprintf("%d pages", d.view.Pages()), humanSize(d.bytes)}
	if d.version != "" {
		parts = append(parts, "pdf "+d.version)
	}
	return strings.Join(parts, " · ")
}

// chrome brings the widgets in line with the current state.
func (a *App) chrome() {
	d := a.current()
	tb := a.toolbar
	a.tabs.Titles = a.tabs.Titles[:0]
	for _, x := range a.docs {
		a.tabs.Titles = append(a.tabs.Titles, x.name)
	}
	a.tabs.Active = a.active
	for i, x := range a.docs {
		x.view.SetActive(i == a.active)
	}

	tb.NoDocument = d == nil // which hides the controls that need one
	if d == nil {
		a.endPageEdit(false)
		if a.menuKind != menuApp {
			a.menu.Close() // the zoom and profile menus need a document
		}
		tb.Page, tb.Pages, tb.ZoomText = 0, 0, pageview.DefaultZoom.Label
		a.status.Info, a.status.Claimed, a.status.Right = "", "", nil
		a.setTitle("Viewrab")
	} else {
		page, pages := d.view.Page(), d.view.Pages()
		tb.Page, tb.Pages = page+1, pages
		tb.ZoomText = d.view.ZoomLabel()
		tb.Disabled[widget.ButtonPrev] = page <= 0
		tb.Disabled[widget.ButtonNext] = page >= pages-1
		a.status.Info, a.status.Claimed = statusInfo(d), d.claimed
		a.status.Right = append(a.status.Right[:0], d.view.ZoomPercent())
		a.setTitle(d.name + " — Viewrab")
	}
	if a.findBar.Open && a.findDoc != d {
		a.closeFind() // A search belongs to the tab it began in.
	}
	if a.toolbar.EditingPage && a.pageDoc != d {
		a.endPageEdit(false) // A page number being typed is discarded too.
	}
	a.syncFind()
	tb.On[widget.ButtonZoomMode] = a.menu.Open && a.menuKind == menuZoom
	tb.On[widget.ButtonMenu] = a.menu.Open && a.menuKind == menuApp
	tb.On[widget.ButtonPDFA] = a.panelOpen
	tb.Controls, tb.Maximized = !a.decorated, a.maximized
	tb.Layout(a.size.W)
	a.panelChrome(d)
	a.tabs.Layout(a.size.W)

	if a.message != "" && !a.opts.Now().Before(a.messageUntil) {
		a.message = ""
	}
	a.status.Message, a.status.Kind = a.message, a.messageKind
}

// syncFind brings the find field's count in line with its search.
func (a *App) syncFind() {
	if a.findBar.Open {
		a.findBar.Current, a.findBar.Total, a.findBar.Searching = a.findDoc.view.FindStatus()
	}
}

// panelChrome brings the PDF/A panel in line with the document d, the active
// one or nil.
func (a *App) panelChrome(d *document) {
	p := a.panel
	if d != a.panelDoc {
		a.panelDoc = d
		p.ScrollToTop()
	}
	p.Running = a.job != nil
	p.Name, p.Meta, p.Result = "", "", nil
	if d != nil {
		p.Name, p.Meta, p.Result = d.name, panelMeta(d), d.result
	}
}

// setTitle sets the window title when it changes.
func (a *App) setTitle(title string) {
	if title != a.title {
		a.title = title
		a.win.SetTitle(title)
	}
}

// pixelScale returns the window's device pixels per logical pixel.
func (a *App) pixelScale() float64 {
	if a.scale > 0 {
		return a.scale
	}
	return 1
}

// draw paints the whole window and presents it.
func (a *App) draw() {
	if a.size == (platform.Size{}) {
		return
	}
	if c := a.canvas; c == nil || c.Bounds().Size() != image.Pt(a.size.W, a.size.H) || c.Scale() != a.pixelScale() {
		a.canvas = paint.NewScaled(a.size.W, a.size.H, a.pixelScale())
	}
	a.chrome()
	c := a.canvas
	c.Fill(c.Bounds(), a.opts.Palette.Canvas)
	toolbar, tabs, content, status := a.layout()
	a.toolbar.Draw(c, toolbar.Min)
	if d := a.current(); d != nil {
		a.findBar.Draw(c, a.findRect())
		a.tabs.Draw(c, tabs.Min)
		d.view.DrawIn(c, content)
		if r := a.panelRect(); !r.Empty() {
			a.panel.Draw(c, r)
		}
	} else {
		a.hint(content)
	}
	if a.dropTint {
		tint(c, content, a.opts.Palette.Accent)
	}
	a.status.Draw(c, status)
	a.menu.Draw(c)
	a.about.Draw(c, a.bounds())
	a.win.Present(c.Img)
}

// findRect returns where the find field is, in window coordinates.
func (a *App) findRect() image.Rectangle {
	return a.toolbar.ButtonRect(widget.ButtonFind).Add(a.origin(sectionToolbar))
}

// hintLogo, hintGap and kbdPad set the size of the empty state's logo, the gap
// below it and the padding of the keycaps, in pixels.
const (
	hintLogo = 48
	hintGap  = 8
	kbdPad   = 4
)

// hintLayout returns where the parts of the empty state lie when it is
// centred in r: the logo, the top of each of the two lines of text, and the
// open button.
func (a *App) hintLayout(r image.Rectangle) (logo image.Rectangle, line1, line2 int, button image.Rectangle) {
	lh := a.face.Height() + 4
	bw, bh := a.face.Width(hintButton)+16, lh+6
	total := hintLogo + hintGap + 2*lh + 14 + bh
	y := r.Min.Y + (r.Dy()-total)/2
	cx := r.Min.X + r.Dx()/2
	logo = image.Rect(cx-hintLogo/2, y, cx+hintLogo/2, y+hintLogo)
	line1 = logo.Max.Y + hintGap
	line2 = line1 + lh
	by := line2 + lh + 14
	button = image.Rect(cx-bw/2, by, cx-bw/2+bw, by+bh)
	return
}

// hintTitle, hintKeys and hintButton are the text of the empty state.
const (
	hintTitle  = "no document open"
	hintKeys   = " to open"
	hintButton = "open file…"
)

// hint draws the empty state centred in r: the logo, what to press to open a
// file, and a button that opens one.
func (a *App) hint(r image.Rectangle) {
	p := a.opts.Palette
	c := a.canvas
	logo, y1, y2, button := a.hintLayout(r)
	c.DrawImage(logo.Min, icon.Logo(c.DeviceLen(hintLogo)))
	f := a.face
	f.Draw(c, r.Min.X+(r.Dx()-f.Width(hintTitle))/2, y1, hintTitle, p.Muted)

	// Ctrl and O as keycaps, then the rest of the line.
	keys := []string{"Ctrl", "O"}
	w := f.Width(hintKeys)
	for _, k := range keys {
		w += f.Width(k) + 2*kbdPad + 6
	}
	x := r.Min.X + (r.Dx()-w)/2
	for _, k := range keys {
		kr := image.Rect(x, y2, x+f.Width(k)+2*kbdPad, y2+f.Height()+2)
		c.StrokeRound(kr, 3, 1, p.Border)
		f.Draw(c, kr.Min.X+kbdPad, kr.Min.Y+1, k, p.Muted)
		x = kr.Max.X + 6
	}
	f.Draw(c, x-6, y2+1, hintKeys, p.Faint)

	if a.hintHover {
		c.FillRound(button, theme.Radius, p.Hover)
	}
	c.StrokeRound(button, theme.Radius, 1, p.Border)
	f.Draw(c, button.Min.X+8, button.Min.Y+(button.Dy()-f.Height())/2, hintButton, p.Text)
}
