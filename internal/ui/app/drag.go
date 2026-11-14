package app

import (
	"fmt"
	"image"
	"image/color"
	"path/filepath"
	"slices"
	"strings"

	"viewer/internal/ui/paint"
	"viewer/internal/ui/platform"
	"viewer/internal/ui/theme"
	"viewer/internal/ui/widget"
)

// Dragging tabs: a tab dragged along the tab bar reorders the tabs, as the tab
// bar does on its own. Dragged off the bar it becomes a drag the compositor
// carries, which can drop on the tab bar or the pages of another window, to
// move the tab there, or back on this window's tab bar; dropped away from
// every window, it opens in a new window. Dragging the logo or the tab bar's
// empty part carries every tab, merging the window into the one it is dropped
// on. A tab's document moves with it, so nothing is read again.

// dragPad is the room left and right of a drag icon's label, in pixels.
const dragPad = 12

// beginDrag begins a drag of tab i, or of every tab when i is -1, with the
// pointer at at, in window coordinates.
func (a *App) beginDrag(i int, at image.Point) {
	if i >= len(a.docs) || len(a.docs) == 0 {
		return
	}
	d := &tabDrag{from: a, docs: slices.Clone(a.docs), whole: i < 0}
	if !d.whole {
		d.docs = []*document{a.docs[i]}
	}
	icon, hotspot := a.dragIcon(i, at)
	if !a.win.StartDrag(icon, hotspot) {
		return
	}
	a.drag = d
	a.s.setDrag(d)
	a.tabs.Lifted = i
	// The compositor takes the pointer, and the button's release with it.
	a.capture = sectionNone
}

// dragIcon returns the image a drag of tab i, or of every tab when i is -1,
// shows under the pointer, and the point in it that is at the pointer: a tab
// keeps the place it was grabbed at.
func (a *App) dragIcon(i int, at image.Point) (*image.RGBA, image.Point) {
	label := fmt.Sprintf("%d tabs", len(a.docs))
	if len(a.docs) == 1 {
		label = "1 tab"
	}
	w, h := a.face.Width(label)+2*dragPad, theme.TabBarHeight
	hotspot := image.Pt(w/2, h/2)
	if i >= 0 {
		_, tabs, _, _ := a.layout()
		r := a.tabs.TabRect(i).Add(tabs.Min)
		label, w = a.docs[i].name, r.Dx()
		hotspot = at.Sub(r.Min)
		hotspot.X, hotspot.Y = min(max(hotspot.X, 0), w-1), min(max(hotspot.Y, 0), h-1)
	}
	p := a.opts.Palette
	c := paint.New(w, h)
	c.FillRound(c.Bounds(), theme.Radius, p.Canvas)
	c.StrokeRound(c.Bounds(), theme.Radius, 1, p.Accent)
	a.face.Draw(c, dragPad, (h-a.face.Height())/2, a.face.Fit(label, w-2*dragPad), p.Text)
	return c.Img, hotspot
}

// dropGap returns where tabs dropped at p would go: the gap in the tab bar
// under p, or after the last tab elsewhere, and whether the drop is wanted.
// The window takes files anywhere, the tabs of another window anywhere, and
// one of its own tabs only on its tab bar.
func (a *App) dropGap(p image.Point, own bool) (gap int, onBar, ok bool) {
	_, tabs, _, _ := a.layout()
	gap, onBar = len(a.docs), p.In(tabs)
	if onBar {
		gap = a.tabs.GapAt(p.X - tabs.Min.X)
	}
	if !own {
		return gap, false, true
	}
	d := a.s.currentDrag()
	switch {
	case d == nil:
		return gap, onBar, false
	case d.from == a:
		return gap, onBar, onBar && !d.whole
	}
	return gap, onBar, true
}

// dragOver shows where a drag over the window would drop: a mark between
// tabs on the tab bar, or a tint over the pages.
func (a *App) dragOver(ev platform.DragEvent) {
	a.clearDropMarks()
	if ev.Kind != platform.DragLeave {
		gap, onBar, ok := a.dropGap(image.Pt(int(ev.X), int(ev.Y)), ev.Own)
		switch {
		case ok && onBar:
			a.tabs.DropGap = gap
		case ok:
			a.dropTint = true
		}
	}
	a.win.Invalidate()
}

func (a *App) clearDropMarks() {
	a.tabs.DropGap, a.dropTint = -1, false
}

// dropped carries out a drop: files open in new tabs, and tabs move here, or
// to their new place on the tab bar of their own window.
func (a *App) dropped(ev platform.DropEvent) {
	a.clearDropMarks()
	a.win.Invalidate()
	a.s.focus(a)
	if !ev.Own {
		a.dropFiles(ev.Paths)
		return
	}
	gap, _, ok := a.dropGap(image.Pt(int(ev.X), int(ev.Y)), true)
	d := a.s.takeDrag(nil)
	switch {
	case !ok || d == nil:
	case d.from == a:
		if from := slices.Index(a.docs, d.docs[0]); from >= 0 {
			a.move(from, min(gap-boolInt(gap > from), len(a.docs)-1))
		}
	default:
		a.s.transfer(d, a, gap)
	}
}

// boolInt returns 1 for true and 0 for false.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// dropFiles opens the PDFs among paths, telling of the files that are not.
func (a *App) dropFiles(paths []string) {
	var pdfs []string
	for _, p := range paths {
		if strings.EqualFold(filepath.Ext(p), ".pdf") {
			pdfs = append(pdfs, p)
		}
	}
	if len(pdfs) == 0 {
		a.say(widget.MessageWarn, "Only PDF files can be opened")
		return
	}
	a.Open(pdfs...)
}

// dragEnded ends the drag of this window's tabs. A tab dropped away from
// every window, rather than cancelled over one, opens in a new window; a
// whole window dropped there stays as it is.
func (a *App) dragEnded(ev platform.DragEndEvent) {
	d := a.drag
	a.drag, a.tabs.Lifted = nil, -1
	a.win.Invalidate()
	if d == nil || ev.Dropped || a.s.takeDrag(d) == nil || !ev.Outside || d.whole {
		return
	}
	docs := a.takeOut(d.docs)
	if len(docs) == 0 {
		return
	}
	if err := a.s.openWindow(docs, nil); err != nil {
		a.adopt(docs, len(a.docs))
		a.say(widget.MessageWarn, "Could not open a window: "+err.Error())
		return
	}
	if len(a.docs) == 0 {
		a.quitting = true // it moved out entirely
	}
}

// takeOut removes the tabs of docs that the window still has, and returns
// their documents, which it no longer shows, in tab order. A check running on
// one of them stops, and a search or page number being typed in one ends.
func (a *App) takeOut(docs []*document) []*document {
	cur := a.current()
	var out []*document
	kept := a.docs[:0:0]
	for _, d := range a.docs {
		if !slices.Contains(docs, d) {
			kept = append(kept, d)
			continue
		}
		out = append(out, d)
		if a.job != nil && a.job.doc == d {
			a.job.cancel()
			d.result = nil
		}
		if a.findDoc == d {
			a.closeFind()
		}
		if a.pageDoc == d {
			a.endPageEdit(false)
		}
	}
	a.docs = kept
	a.active = slices.Index(kept, cur)
	if a.active < 0 {
		a.active = len(kept) - 1
	}
	a.afterChange()
	return out
}

// adopt adds docs, which another window gave up, as tabs from the gap at,
// showing the first. Before the window has a size, they wait for one.
func (a *App) adopt(docs []*document, at int) {
	if a.size == (platform.Size{}) {
		a.pendingDocs = append(a.pendingDocs, docs...)
		return
	}
	p := a.opts.Palette
	for _, d := range docs {
		d.owner.Store(a)
		d.view.SetColors(p.Canvas, p.Selection)
	}
	at = min(max(at, 0), len(a.docs))
	a.docs = slices.Insert(a.docs, at, docs...)
	a.active = at
	a.afterChange()
}

// tint washes r with the accent colour and outlines it, to show that a drop
// there is wanted.
func tint(c *paint.Canvas, r image.Rectangle, accent color.NRGBA) {
	wash := accent
	wash.A = 0x22
	c.Fill(r, wash)
	c.Stroke(r, 2, accent)
}
