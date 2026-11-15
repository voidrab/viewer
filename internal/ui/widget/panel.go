package widget

import (
	"fmt"
	"image"
	"image/color"
	"path/filepath"
	"strconv"
	"strings"

	"viewrab/internal/pdfa"
	"viewrab/internal/ui/icon"
	"viewrab/internal/ui/paint"
	"viewrab/internal/ui/platform"
	"viewrab/internal/ui/text"
	"viewrab/internal/ui/theme"
)

// PanelWidth is the width of the PDF/A panel in a window wide enough for it.
const PanelWidth = 340

// Panel geometry, in pixels.
const (
	panelPad     = 14
	panelTop     = 10
	panelControl = 26 // the height of buttons and the profile drop-down
	panelClose   = 24
	panelMark    = 16 // the verdict's tick or cross
	panelIndent  = 16 // of an issue's pages and messages
	panelLead    = 2  // between the lines of a paragraph
	issuePad     = 6  // above and below an issue's summary
	chevronSize  = 12
	// MaxPageLinks is how many pages an issue links to before the rest are
	// only counted.
	MaxPageLinks = 12
)

// PanelActionKind says what the user asked of a Panel.
type PanelActionKind int

// Panel actions.
const (
	PanelNone     PanelActionKind = iota
	PanelClose                    // close the panel
	PanelVerify                   // verify the document against the chosen profile
	PanelConvert                  // convert it to the chosen profile
	PanelCancel                   // stop the verification or conversion running
	PanelPage                     // go to page Page, counting from 1
	PanelOpen                     // open the file at Path
	PanelProfiles                 // show the profiles to choose from
)

// PanelAction is the result of a pointer event handled by a Panel.
type PanelAction struct {
	Kind PanelActionKind
	Page int
	Path string
}

// PanelResult is what the panel shows below its controls for one document:
// the check running, or the outcome of the last one. The application keeps
// one per document and replaces it with each run.
type PanelResult struct {
	// Busy, when not empty, says what is running, such as "Checking against
	// PDF/A-1b…".
	Busy string
	// Error is why the last run failed.
	Error string
	// Label is the display label of the profile the result is for.
	Label string
	// Report is the outcome of a verification and Conversion that of a
	// conversion; at most one is set.
	Report     *pdfa.Report
	Conversion *pdfa.ConvertReport

	expanded map[int]bool // the issues opened to show their details
}

// Panel is the PDF/A panel at the right of the content: the document's name
// and facts, a choice of profile, buttons to verify and convert, and the
// result of the last run.
type Panel struct {
	Palette theme.Palette
	Face    *text.Face

	// Name is the document's file name and Meta a line of facts about it.
	Name, Meta string
	// Profiles are the labels of the profiles offered, and Profile the index
	// of the one chosen.
	Profiles []string
	Profile  int
	// Running reports that a check is running, for this document or another;
	// only one runs at a time, so the buttons that start one are disabled.
	Running bool
	// Result is the active document's, or nil before its first run.
	Result *PanelResult

	scroll       int
	hover, press target
}

// NewPanel returns a panel using palette and face, offering profiles.
func NewPanel(palette theme.Palette, face *text.Face, profiles []string) *Panel {
	return &Panel{Palette: palette, Face: face, Profiles: profiles}
}

// targetKind is what clicking an item of the panel does.
type targetKind uint8

const (
	toNone targetKind = iota
	toClose
	toProfile
	toVerify
	toConvert
	toCancel
	toIssue
	toPage
	toOpen
)

// target is what an item does when clicked: a kind, with the index of the
// profile, issue or page, or the path to open.
type target struct {
	kind targetKind
	n    int
	path string
}

// itemStyle is how an item is drawn.
type itemStyle uint8

const (
	styleText    itemStyle = iota // a line of text
	styleButton                   // a bordered button
	stylePrimary                  // a button in the accent colour
	styleSelect                   // the profile drop-down
	styleLink                     // a page link in the accent colour
	styleRow                      // the clickable band of an issue's summary
	styleIcon                     // an icon, washed when hovered if clickable
	styleRule                     // a dividing line
)

// item is one laid-out part of the panel, in content coordinates: those of
// the panel scrolled to its top.
type item struct {
	r        image.Rectangle
	style    itemStyle
	text     string
	col      color.NRGBA
	icon     icon.Icon
	target   target
	disabled bool
}

// builder lays out the panel's items from the top down.
type builder struct {
	p      *Panel
	items  []item
	x0, x1 int // the content's left and right edges
	y      int
}

func (b *builder) add(it item) { b.items = append(b.items, it) }

// lineHeight is the height of a line of a paragraph, with its leading.
func (b *builder) lineHeight() int { return b.p.Face.Height() + panelLead }

// text adds a line of text with its top-left corner at (x, y).
func (b *builder) text(x, y int, s string, col color.NRGBA) {
	b.add(item{r: image.Rect(x, y, x+b.p.Face.Width(s), y+b.p.Face.Height()), text: s, col: col})
}

// paragraph adds s wrapped to the content's width less indent.
func (b *builder) paragraph(s string, col color.NRGBA, indent int) {
	for _, line := range wrap(b.p.Face, s, b.x1-b.x0-indent) {
		b.text(b.x0+indent, b.y, line, col)
		b.y += b.lineHeight()
	}
}

// button adds a button at x on the current line and returns the x after it.
func (b *builder) button(x int, label string, style itemStyle, t target, disabled bool) int {
	w := b.p.Face.Width(label) + 16
	b.add(item{r: image.Rect(x, b.y, x+w, b.y+panelControl), style: style, text: label, target: t, disabled: disabled})
	return x + w + 6
}

// buttonWidth returns the width of a button labelled label, with the gap
// after it.
func (b *builder) buttonWidth(label string) int { return b.p.Face.Width(label) + 16 + 6 }

// verdict adds a tick or a cross and s beside it, in the OK or Bad colour.
func (b *builder) verdict(ok bool, s string) {
	mark, col := icon.Close, b.p.Palette.Bad
	if ok {
		mark, col = icon.Check, b.p.Palette.OK
	}
	h := b.p.Face.Height()
	b.add(item{r: image.Rect(b.x0, b.y+(h-panelMark)/2, b.x0+panelMark, b.y+(h+panelMark)/2), style: styleIcon, icon: mark, col: col})
	b.paragraph(s, col, panelMark+6)
}

// rule adds a dividing line across the content.
func (b *builder) rule() {
	b.add(item{r: image.Rect(b.x0, b.y, b.x1, b.y+1), style: styleRule})
	b.y++
}

// The labels of the panel's buttons, in lower case.
const (
	verifyLabel  = "verify"
	convertLabel = "convert…"
	cancelLabel  = "cancel"
)

// layout lays out the panel at width w and returns its items and the height
// of its content.
func (p *Panel) layout(w int) ([]item, int) {
	pal := p.Palette
	b := &builder{p: p, x0: panelPad, x1: w - panelPad, y: panelTop}
	h := p.Face.Height()

	b.text(b.x0, b.y+(panelClose-h)/2, "PDF/A", pal.Muted)
	b.add(item{r: image.Rect(b.x1-panelClose+4, b.y, b.x1+4, b.y+panelClose), style: styleIcon, icon: icon.Close, col: pal.Muted, target: target{kind: toClose}})
	b.y += panelClose + 6
	if p.Name != "" {
		b.text(b.x0, b.y, p.Face.Fit(p.Name, b.x1-b.x0), pal.Text)
		b.y += b.lineHeight()
	}
	if p.Meta != "" {
		b.paragraph(p.Meta, pal.Muted, 0)
	}
	b.y += 10

	// The profile drop-down and the buttons share one row: the drop-down
	// takes what the buttons leave.
	res := p.Result
	busy := res != nil && res.Busy != ""
	right := b.buttonWidth(verifyLabel) + b.buttonWidth(convertLabel)
	if busy {
		right += b.buttonWidth(cancelLabel)
	}
	x := b.x0
	if len(p.Profiles) > 0 {
		r := image.Rect(x, b.y, max(b.x1-right, x+panelControl), b.y+panelControl)
		label := p.Face.Fit(p.Profiles[min(max(p.Profile, 0), len(p.Profiles)-1)], r.Dx()-12-chevronSize-8)
		b.add(item{r: r, style: styleSelect, text: label, target: target{kind: toProfile}, disabled: p.Running})
		x = r.Max.X + 6
	}
	x = b.button(x, verifyLabel, stylePrimary, target{kind: toVerify}, p.Running)
	x = b.button(x, convertLabel, styleButton, target{kind: toConvert}, p.Running)
	if busy {
		b.button(x, cancelLabel, styleButton, target{kind: toCancel}, false)
	}
	b.y += panelControl + 14

	switch {
	case res == nil:
	case res.Busy != "":
		b.paragraph(res.Busy, pal.Muted, 0)
	case res.Error != "":
		b.verdict(false, res.Error)
	case res.Conversion != nil:
		b.conversion(res)
	case res.Report != nil:
		b.report(res)
	}
	return b.items, b.y + panelPad
}

// report adds the outcome of a verification.
func (b *builder) report(res *PanelResult) {
	r := res.Report
	if r.Valid {
		b.verdict(true, "Conforms to "+res.Label)
	} else {
		b.verdict(false, plural(r.Total, "issue", "issues")+" in "+plural(len(r.Issues), "check", "checks"))
	}
	b.paragraph(fmt.Sprintf("%s · checked in %d ms", res.Label, r.Millis), b.p.Palette.Muted, 0)
	b.issues(res, r.Issues)
}

// conversion adds the outcome of a conversion.
func (b *builder) conversion(res *PanelResult) {
	cr, pal := res.Conversion, b.p.Palette
	if cr.Report.Valid {
		b.verdict(true, "Converted to "+res.Label)
	} else {
		b.verdict(false, "Converted, but "+plural(cr.Report.Total, "issue", "issues")+" remain")
	}
	b.paragraph(fmt.Sprintf("Saved as %s · %s · %d ms", filepath.Base(cr.Output), plural(cr.Iterations, "pass", "passes"), cr.Report.Millis), pal.Muted, 0)
	b.y += 4
	b.button(b.x0, "Open converted file", stylePrimary, target{kind: toOpen, path: cr.Output}, false)
	b.y += panelControl + 8
	if len(cr.Rasterized) > 0 {
		pages := make([]string, len(cr.Rasterized))
		for i, p := range cr.Rasterized {
			pages[i] = strconv.Itoa(p)
		}
		b.paragraph("Rebuilt as images (text no longer selectable): pages "+strings.Join(pages, ", "), pal.Warn, 0)
		b.y += 4
	}
	if len(cr.Lost) > 0 {
		b.paragraph("Not carried over:", pal.Warn, 0)
		for _, l := range cr.Lost {
			b.paragraph(l, pal.Muted, panelIndent)
		}
	}
	b.issues(res, cr.Report.Issues)
}

// issues adds the list of issues, each a summary that opens to show the
// pages it is on and its messages.
func (b *builder) issues(res *PanelResult, issues []pdfa.Issue) {
	if len(issues) == 0 {
		return
	}
	pal, f := b.p.Palette, b.p.Face
	// The descriptions line up in a column right of the widest clause.
	clauses := 0
	for _, is := range issues {
		clauses = max(clauses, f.Width(is.Clause))
	}
	cx := b.x0 + chevronSize + 6
	dx := cx + clauses + 8
	b.y += 10
	b.rule()
	for i, is := range issues {
		open := res.expanded[i]
		count := fmt.Sprintf("×%d", is.Count)
		lines := wrap(f, is.Description, b.x1-f.Width(count)-8-dx)
		top := b.y
		height := len(lines)*b.lineHeight() - panelLead + 2*issuePad
		b.add(item{r: image.Rect(b.x0, top, b.x1, top+height), style: styleRow, target: target{kind: toIssue, n: i}})
		chevron := icon.Next
		if open {
			chevron = icon.Down
		}
		cy := top + issuePad + (f.Height()-chevronSize)/2
		b.add(item{r: image.Rect(b.x0, cy, b.x0+chevronSize, cy+chevronSize), style: styleIcon, icon: chevron, col: pal.Faint})
		b.text(cx, top+issuePad, is.Clause, pal.Muted)
		for k, line := range lines {
			b.text(dx, top+issuePad+k*b.lineHeight(), line, pal.Text)
		}
		b.text(b.x1-f.Width(count), top+issuePad, count, pal.Muted)
		b.y += height
		if open {
			b.pages(is.Pages)
			if len(is.Messages) > 0 {
				b.y += 4
			}
			for _, m := range is.Messages {
				b.paragraph(m, pal.Muted, panelIndent)
			}
			b.y += issuePad
		}
		b.rule()
	}
}

// pages adds where an issue is: "document" for page 0, then links to the
// numbered pages, up to MaxPageLinks of them, flowed across the lines.
func (b *builder) pages(pages []int) {
	type span struct {
		text string
		t    target
	}
	spans := []span{}
	var numbered []int
	for _, p := range pages {
		if p == 0 {
			spans = append(spans, span{text: "document"})
		} else {
			numbered = append(numbered, p)
		}
	}
	for _, p := range numbered[:min(len(numbered), MaxPageLinks)] {
		spans = append(spans, span{text: fmt.Sprintf("p. %d", p), t: target{kind: toPage, n: p}})
	}
	if more := len(numbered) - MaxPageLinks; more > 0 {
		spans = append(spans, span{text: "and " + plural(more, "more page", "more pages")})
	}
	f, pal := b.p.Face, b.p.Palette
	left := b.x0 + panelIndent
	x := left
	place := func(s string) int {
		if x > left && x+f.Width(s) > b.x1 {
			x = left
			b.y += b.lineHeight()
		}
		at := x
		x += f.Width(s)
		return at
	}
	b.text(place("Where: "), b.y, "Where: ", pal.Muted)
	for i, s := range spans {
		label := s.text
		if i < len(spans)-1 {
			label += ","
		}
		at := place(label)
		if s.t.kind == toPage {
			b.add(item{r: image.Rect(at, b.y, at+f.Width(s.text), b.y+f.Height()), style: styleLink, text: s.text, target: s.t})
			if label != s.text {
				b.text(at+f.Width(s.text), b.y, ",", pal.Muted)
			}
		} else {
			b.text(at, b.y, label, pal.Muted)
		}
		x += f.Width(" ")
	}
	b.y += b.lineHeight()
}

// plural returns n followed by one or many, as n calls for.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// wrap breaks s into lines at most width pixels wide, at spaces where it can
// and inside words too long for a line of their own. It returns at least one
// line, which is empty for an empty s.
func wrap(f *text.Face, s string, width int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(s) {
		if line != "" && f.Width(line+" "+word) <= width {
			line += " " + word
			continue
		}
		if line != "" {
			lines = append(lines, line)
		}
		// A word wider than a line fills whole lines with as much as fits,
		// and at least one rune, so the loop always ends.
		rs := []rune(word)
		for f.Width(string(rs)) > width && len(rs) > 1 {
			n := 1
			for n < len(rs)-1 && f.Width(string(rs[:n+1])) <= width {
				n++
			}
			lines = append(lines, string(rs[:n]))
			rs = rs[n:]
		}
		line = string(rs)
	}
	return append(lines, line)
}

// maxScroll returns the largest scroll offset for content of height h shown
// in a panel of height view.
func maxScroll(h, view int) int { return max(0, h-view) }

// Draw paints the panel into r.
func (p *Panel) Draw(c *paint.Canvas, r image.Rectangle) {
	pal := p.Palette
	c.Fill(r, pal.Chrome)
	c.Fill(image.Rect(r.Min.X, r.Min.Y, r.Min.X+1, r.Max.Y), pal.Border)
	items, h := p.layout(r.Dx())
	p.scroll = min(p.scroll, maxScroll(h, r.Dy()))
	c.PushClip(r)
	defer c.PopClip()
	off := r.Min.Sub(image.Pt(0, p.scroll))
	for _, it := range items {
		p.drawItem(c, it, it.r.Add(off))
	}
}

// drawItem paints it at r, where it lies on the canvas.
func (p *Panel) drawItem(c *paint.Canvas, it item, r image.Rectangle) {
	pal := p.Palette
	hovered := it.target.kind != toNone && !it.disabled && p.hover == it.target
	wash := func() {
		switch {
		case hovered && p.press == it.target:
			c.FillRound(r, theme.Radius, pal.Active)
		case hovered:
			c.FillRound(r, theme.Radius, pal.Hover)
		}
	}
	centred := func(col color.NRGBA) {
		p.Face.Draw(c, r.Min.X+(r.Dx()-p.Face.Width(it.text))/2, r.Min.Y+(r.Dy()-p.Face.Height())/2, it.text, col)
	}
	switch it.style {
	case styleText:
		p.Face.Draw(c, r.Min.X, r.Min.Y, it.text, it.col)
	case styleRule:
		c.Fill(r, pal.Border)
	case styleRow:
		wash()
	case styleIcon:
		wash()
		icon.Draw(c, r.Inset(r.Dx()/8), it.icon, it.col, 2)
	case styleLink:
		p.Face.Draw(c, r.Min.X, r.Min.Y, it.text, pal.Accent)
		if hovered {
			c.Fill(image.Rect(r.Min.X, r.Max.Y-1, r.Max.X, r.Max.Y), pal.Accent)
		}
	case stylePrimary, styleButton, styleSelect:
		// A primary button is outlined and labelled in the accent colour.
		fg, border := pal.Text, pal.Border
		if it.style == stylePrimary {
			fg, border = pal.Accent, pal.Accent
		}
		if it.disabled {
			fg, border = dim(fg), dim(border)
		}
		wash()
		c.StrokeRound(r, theme.Radius, 1, border)
		if it.style != styleSelect {
			centred(fg)
			return
		}
		p.Face.Draw(c, r.Min.X+8, r.Min.Y+(r.Dy()-p.Face.Height())/2, it.text, fg)
		cy := r.Min.Y + (r.Dy()-chevronSize)/2
		icon.Draw(c, image.Rect(r.Max.X-6-chevronSize, cy, r.Max.X-6, cy+chevronSize), icon.Chevron, pal.Muted, 2)
	}
}

// at returns the target of the clickable item at p, in content coordinates,
// or the zero target.
func (p *Panel) at(items []item, pt image.Point) target {
	for i := len(items) - 1; i >= 0; i-- {
		if it := items[i]; it.target.kind != toNone && !it.disabled && pt.In(it.r) {
			return it.target
		}
	}
	return target{}
}

// HandlePointer applies a pointer event, given in the panel's own coordinates
// for a panel of the given size, and returns what it asks for. Opening an issue
// is handled here and returns PanelNone.
func (p *Panel) HandlePointer(ev platform.PointerEvent, size image.Point) PanelAction {
	items, _ := p.layout(size.X)
	pt := image.Pt(int(ev.X), int(ev.Y))
	t := target{}
	if pt.In(image.Rectangle{Max: size}) {
		t = p.at(items, pt.Add(image.Pt(0, p.scroll)))
	}
	switch ev.Kind {
	case platform.Move, platform.Enter:
		p.hover = t
	case platform.Leave:
		p.hover = target{}
	case platform.Press:
		if ev.Button == platform.Left {
			p.press = t
		}
	case platform.Release:
		pressed := p.press
		p.press = target{}
		if ev.Button == platform.Left && pressed.kind != toNone && pressed == t {
			return p.activate(t)
		}
	}
	return PanelAction{}
}

// activate carries out a click on an item with target t.
func (p *Panel) activate(t target) PanelAction {
	switch t.kind {
	case toIssue:
		if p.Result.expanded == nil {
			p.Result.expanded = map[int]bool{}
		}
		p.Result.expanded[t.n] = !p.Result.expanded[t.n]
	}
	return actionOf(t)
}

// actionOf returns what clicking an item with target t asks of the
// application: nothing for the targets the panel handles itself.
func actionOf(t target) PanelAction {
	switch t.kind {
	case toClose:
		return PanelAction{Kind: PanelClose}
	case toVerify:
		return PanelAction{Kind: PanelVerify}
	case toConvert:
		return PanelAction{Kind: PanelConvert}
	case toCancel:
		return PanelAction{Kind: PanelCancel}
	case toPage:
		return PanelAction{Kind: PanelPage, Page: t.n}
	case toOpen:
		return PanelAction{Kind: PanelOpen, Path: t.path}
	case toProfile:
		return PanelAction{Kind: PanelProfiles}
	}
	return PanelAction{}
}

// ItemRect returns where the control that asks for act is, in the coordinates
// of a panel of the given width scrolled to its top, or an empty rectangle
// when the panel shows none. Like Toolbar.ButtonRect, it lets a caller find a
// control to point at.
func (p *Panel) ItemRect(act PanelAction, width int) image.Rectangle {
	items, _ := p.layout(width)
	for _, it := range items {
		if act.Kind != PanelNone && actionOf(it.target) == act {
			return it.r
		}
	}
	return image.Rectangle{}
}

// Scroll moves the panel's content up by dy pixels, for a panel of the given
// size, within its content.
func (p *Panel) Scroll(dy float64, size image.Point) {
	_, h := p.layout(size.X)
	p.scroll = max(0, min(p.scroll+int(dy), maxScroll(h, size.Y)))
}

// ScrollOffset returns how far the panel's content is scrolled up.
func (p *Panel) ScrollOffset() int { return p.scroll }

// ScrollToTop shows the top of the panel's content.
func (p *Panel) ScrollToTop() { p.scroll = 0 }
