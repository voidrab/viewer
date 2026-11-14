package app

import (
	"fmt"
	"image"

	"viewer/internal/ui/widget"
)

// humanSize formats a byte count for display.
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
	return fmt.Sprintf("%.1f %s", size, suffix)
}

// chrome brings the widgets in line with the current state.
func (a *App) chrome() {
	d := a.current()
	tb := a.toolbar
	titles := make([]string, len(a.docs))
	for i, x := range a.docs {
		titles[i] = x.name
	}
	a.tabs.Titles, a.tabs.Active = titles, a.active

	if d == nil {
		tb.PageText, tb.ZoomText = "", "Fit width"
		for _, id := range []widget.ButtonID{
			widget.ButtonPrev, widget.ButtonNext, widget.ButtonZoomOut, widget.ButtonZoomMode,
			widget.ButtonZoomIn, widget.ButtonFind, widget.ButtonPDFA,
		} {
			tb.Disabled[id] = true
		}
		a.status.Info, a.status.Right = "", nil
		a.setTitle("Viewer")
	} else {
		page, pages := d.view.Page(), d.view.Pages()
		tb.PageText = fmt.Sprintf("%d / %d", page+1, pages)
		tb.ZoomText = d.view.ZoomLabel()
		for _, id := range []widget.ButtonID{widget.ButtonZoomOut, widget.ButtonZoomMode, widget.ButtonZoomIn, widget.ButtonFind, widget.ButtonPDFA} {
			tb.Disabled[id] = false
		}
		tb.Disabled[widget.ButtonPrev] = page <= 0
		tb.Disabled[widget.ButtonNext] = page >= pages-1
		a.status.Info = d.name + "  " + humanSize(d.bytes)
		a.status.Right = a.status.Right[:0]
		if d.claimed != "" {
			a.status.Right = append(a.status.Right, d.claimed)
		}
		a.status.Right = append(a.status.Right, tb.ZoomText, tb.PageText)
		a.setTitle(d.name + " — Viewer")
	}
	tb.Layout(a.size.W)
	a.tabs.Layout(a.size.W)

	if a.message != "" && !a.opts.Now().Before(a.messageUntil) {
		a.message = ""
	}
	a.status.Message, a.status.Kind = a.message, a.messageKind
}

// setTitle sets the window title when it changes.
func (a *App) setTitle(title string) {
	if title != a.title {
		a.title = title
		a.win.SetTitle(title)
	}
}

// draw paints the whole window and presents it.
func (a *App) draw() {
	if a.canvas == nil {
		return
	}
	a.chrome()
	c := a.canvas
	c.Fill(c.Bounds(), a.palette.Canvas)
	toolbar, tabs, content, status := a.layout()
	a.toolbar.Draw(c, toolbar.Min)
	if d := a.current(); d != nil {
		a.tabs.Draw(c, tabs.Min)
		d.view.DrawIn(c, content)
	} else {
		a.hint(content)
	}
	a.status.Draw(c, status)
	a.win.Present(c.Img)
}

// hint tells the user how to open something, centred in r.
func (a *App) hint(r image.Rectangle) {
	for i, line := range []string{"No document open", "Press Ctrl+O, or start with file names"} {
		col := a.palette.Faint
		if i == 0 {
			col = a.palette.Muted
		}
		y := r.Min.Y + r.Dy()/2 - a.face.Height() + i*(a.face.Height()+6)
		a.face.Draw(a.canvas, r.Min.X+(r.Dx()-a.face.Width(line))/2, y, line, col)
	}
}
