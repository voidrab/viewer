package widget

import (
	"image"
	"slices"
	"strings"
	"testing"

	"viewrab/internal/pdfa"
	"viewrab/internal/ui/paint"
	"viewrab/internal/ui/platform"
	"viewrab/internal/ui/text"
	"viewrab/internal/ui/theme"
)

var panelSize = image.Pt(PanelWidth, 600)

func newPanel(t *testing.T) *Panel {
	t.Helper()
	face, err := text.Mono().Face(theme.FontSize, false)
	if err != nil {
		t.Fatal(err)
	}
	p := NewPanel(palette, face, []string{"PDF/A-1b", "PDF/A-2b", "PDF structure"})
	p.Name, p.Meta = "report.pdf", "3 pages · 12.0 KB · PDF 1.7"
	return p
}

// items lays the panel out at its usual size.
func (p *Panel) items() []item {
	items, _ := p.layout(panelSize.X)
	return items
}

// find returns the first item with target t, failing the test without one.
func find(t *testing.T, p *Panel, tg target) item {
	t.Helper()
	for _, it := range p.items() {
		if it.target == tg {
			return it
		}
	}
	t.Fatalf("no item with target %+v", tg)
	return item{}
}

// has reports whether the panel shows a line of text containing s.
func has(p *Panel, s string) bool {
	return slices.ContainsFunc(p.items(), func(it item) bool { return strings.Contains(it.text, s) })
}

// texts returns every text the panel shows, joined by spaces.
func texts(p *Panel) string {
	var out []string
	for _, it := range p.items() {
		if it.text != "" {
			out = append(out, it.text)
		}
	}
	return strings.Join(out, " ")
}

// clickItem clicks the middle of the item with target tg, in a panel scrolled
// to its top.
func clickItem(t *testing.T, p *Panel, tg target) PanelAction {
	t.Helper()
	x, y := mid(find(t, p, tg).r.Sub(image.Pt(0, p.scroll)))
	p.HandlePointer(ptr(platform.Move, platform.None, x, y), panelSize)
	p.HandlePointer(ptr(platform.Press, platform.Left, x, y), panelSize)
	return p.HandlePointer(ptr(platform.Release, platform.Left, x, y), panelSize)
}

func TestPanelShowsTheDocumentAndControls(t *testing.T) {
	p := newPanel(t)
	for _, s := range []string{"PDF/A", "report.pdf", "3 pages", "verify", "convert…", "PDF/A-1b"} {
		if !has(p, s) {
			t.Errorf("the panel does not show %q: %s", s, texts(p))
		}
	}
	if has(p, "cancel") || has(p, "PDF/A-2b") {
		t.Errorf("the panel shows %s, with nothing running and PDF/A-1b chosen", texts(p))
	}
	// The profile drop-down, showing the chosen profile, and the buttons
	// share one row that spans the content.
	drop, verify, conv := find(t, p, target{kind: toProfile}), find(t, p, target{kind: toVerify}), find(t, p, target{kind: toConvert})
	if drop.r.Min.X != panelPad || conv.r.Max.X != PanelWidth-panelPad || drop.r.Min.Y != verify.r.Min.Y ||
		drop.r.Max.X > verify.r.Min.X || verify.r.Max.X > conv.r.Min.X {
		t.Errorf("row: drop-down %v, verify %v, convert %v", drop.r, verify.r, conv.r)
	}
	p.Profile = 2
	if !has(p, "PDF structure") {
		t.Errorf("the drop-down does not show the chosen profile: %s", texts(p))
	}
	// Without a name, meta or profiles, those parts are left out.
	bare := NewPanel(palette, p.Face, nil)
	if got := texts(bare); got != "PDF/A verify convert…" {
		t.Errorf("a bare panel shows %q", got)
	}
}

func TestPanelButtons(t *testing.T) {
	p := newPanel(t)
	for tg, want := range map[target]PanelActionKind{
		{kind: toClose}: PanelClose, {kind: toVerify}: PanelVerify, {kind: toConvert}: PanelConvert,
	} {
		if a := clickItem(t, p, tg); a.Kind != want {
			t.Errorf("clicking %+v = %+v, want %d", tg, a, want)
		}
	}
	// The profile drop-down asks for the profiles to be shown, and ItemRect
	// finds it.
	if a := clickItem(t, p, target{kind: toProfile}); a != (PanelAction{Kind: PanelProfiles}) {
		t.Errorf("clicking the profile drop-down = %+v", a)
	}
	if got := p.ItemRect(PanelAction{Kind: PanelProfiles}, PanelWidth); got != find(t, p, target{kind: toProfile}).r {
		t.Errorf("ItemRect of the drop-down = %v", got)
	}
	p.Profile = 1
	// While something runs, the controls that start a run are disabled, and
	// the result being worked on can be cancelled.
	p.Running = true
	p.Result = &PanelResult{Busy: "Checking against PDF/A-2b…"}
	for _, tg := range []target{{kind: toVerify}, {kind: toConvert}, {kind: toProfile}} {
		if a := clickItem(t, p, tg); a != (PanelAction{}) {
			t.Errorf("a disabled %+v = %+v", tg, a)
		}
	}
	if p.Profile != 1 {
		t.Error("a disabled profile could be chosen")
	}
	if !has(p, "Checking against PDF/A-2b…") {
		t.Error("the busy text is not shown")
	}
	if a := clickItem(t, p, target{kind: toCancel}); a.Kind != PanelCancel {
		t.Errorf("cancel = %+v", a)
	}
}

func TestPanelClicksNeedAPressAndReleaseOnTheSameItem(t *testing.T) {
	p := newPanel(t)
	vx, vy := mid(find(t, p, target{kind: toVerify}).r)
	cx, cy := mid(find(t, p, target{kind: toConvert}).r)
	p.HandlePointer(ptr(platform.Press, platform.Left, vx, vy), panelSize)
	if a := p.HandlePointer(ptr(platform.Release, platform.Left, cx, cy), panelSize); a != (PanelAction{}) {
		t.Errorf("press on one, release on another = %+v", a)
	}
	p.HandlePointer(ptr(platform.Press, platform.Right, vx, vy), panelSize)
	if a := p.HandlePointer(ptr(platform.Release, platform.Right, vx, vy), panelSize); a != (PanelAction{}) {
		t.Errorf("right click = %+v", a)
	}
	// A press outside the panel's area finds nothing, even where an item
	// would be if the panel were larger.
	p.HandlePointer(ptr(platform.Press, platform.Left, vx, vy), image.Pt(PanelWidth, 10))
	if a := p.HandlePointer(ptr(platform.Release, platform.Left, vx, vy), image.Pt(PanelWidth, 10)); a != (PanelAction{}) {
		t.Errorf("a click beyond the panel = %+v", a)
	}
	// Hover follows the pointer and ends when it leaves.
	p.HandlePointer(ptr(platform.Enter, platform.None, vx, vy), panelSize)
	if p.hover.kind != toVerify {
		t.Errorf("hover = %+v", p.hover)
	}
	p.HandlePointer(ptr(platform.Leave, platform.None, 0, 0), panelSize)
	if p.hover.kind != toNone {
		t.Errorf("hover after leaving = %+v", p.hover)
	}
}

// failing is a report with two issues: one on the document and many pages,
// one on a single page.
func failing() *pdfa.Report {
	many := []int{0}
	for i := 1; i <= MaxPageLinks+3; i++ {
		many = append(many, 100+i)
	}
	return &pdfa.Report{Total: 20, Millis: 42, Issues: []pdfa.Issue{
		{Clause: "6.1.13/1", Description: "A long description of what the document does wrong, which takes more than one line", Count: 19, Pages: many, Messages: []string{"first message", "second message"}},
		{Clause: "6.3.4/1", Description: "Fonts are not embedded", Count: 1, Pages: []int{2}},
	}}
}

func TestPanelShowsAVerification(t *testing.T) {
	p := newPanel(t)
	p.Result = &PanelResult{Label: "PDF/A-1b", Report: &pdfa.Report{Valid: true, Millis: 7}}
	if !has(p, "Conforms to PDF/A-1b") || !has(p, "PDF/A-1b · checked in 7 ms") {
		t.Errorf("a valid report shows %s", texts(p))
	}

	p.Result = &PanelResult{Label: "PDF/A-1b", Report: failing()}
	for _, s := range []string{"20 issues in 2 checks", "6.1.13/1", "×19", "6.3.4/1", "Fonts are not embedded"} {
		if !has(p, s) {
			t.Errorf("a failing report does not show %q: %s", s, texts(p))
		}
	}
	// An issue's details show once it is opened, and hide again.
	if has(p, "first message") || has(p, "Where:") {
		t.Error("a closed issue shows its details")
	}
	if a := clickItem(t, p, target{kind: toIssue, n: 0}); a != (PanelAction{}) {
		t.Errorf("opening an issue = %+v", a)
	}
	for _, s := range []string{"Where:", "document,", "p. 101", "p. 112", "and 3 more pages", "first message", "second message"} {
		if !has(p, s) {
			t.Errorf("an open issue does not show %q: %s", s, texts(p))
		}
	}
	if has(p, "p. 113") {
		t.Error("more than MaxPageLinks pages are linked")
	}
	// The page links flow onto more than one line.
	first, last := find(t, p, target{kind: toPage, n: 101}), find(t, p, target{kind: toPage, n: 112})
	if last.r.Min.Y <= first.r.Min.Y {
		t.Errorf("the links did not wrap: %v then %v", first.r, last.r)
	}
	if a := clickItem(t, p, target{kind: toPage, n: 105}); a != (PanelAction{Kind: PanelPage, Page: 105}) {
		t.Errorf("a page link = %+v", a)
	}
	clickItem(t, p, target{kind: toIssue, n: 0})
	if has(p, "first message") {
		t.Error("a closed issue still shows its details")
	}
	// A single page needs no "and more", and no comma.
	clickItem(t, p, target{kind: toIssue, n: 1})
	if !has(p, "p. 2") || has(p, "more pages") {
		t.Errorf("one page shows %s", texts(p))
	}
	// One check with one issue reads in the singular.
	p.Result = &PanelResult{Label: "PDF/A-1b", Report: &pdfa.Report{Total: 1, Issues: []pdfa.Issue{{Clause: "1/1", Count: 1, Pages: []int{1}}}}}
	if !has(p, "1 issue in 1 check") {
		t.Errorf("singular: %s", texts(p))
	}
}

func TestPanelShowsAConversion(t *testing.T) {
	p := newPanel(t)
	p.Result = &PanelResult{Label: "PDF/A-2b", Conversion: &pdfa.ConvertReport{
		Output: "/tmp/out/report.pdfa.pdf", Iterations: 1, Report: pdfa.Report{Valid: true, Millis: 9},
	}}
	for _, s := range []string{"Converted to PDF/A-2b", "Saved as report.pdfa.pdf · 1 pass · 9 ms", "Open converted file"} {
		if !has(p, s) {
			t.Errorf("a conversion does not show %q: %s", s, texts(p))
		}
	}
	if has(p, "Rebuilt") || has(p, "Not carried over") {
		t.Errorf("a clean conversion reports losses: %s", texts(p))
	}
	if a := clickItem(t, p, target{kind: toOpen, path: "/tmp/out/report.pdfa.pdf"}); a != (PanelAction{Kind: PanelOpen, Path: "/tmp/out/report.pdfa.pdf"}) {
		t.Errorf("open = %+v", a)
	}

	p.Result = &PanelResult{Label: "PDF/A-2b", Conversion: &pdfa.ConvertReport{
		Output: "x.pdf", Iterations: 3, Rasterized: []int{2, 5}, Lost: []string{"page 2: not drawn: shading"},
		Report: pdfa.Report{Total: 2, Issues: failing().Issues[1:]},
	}}
	for _, s := range []string{"Converted, but 2 issues remain", "3 passes", "pages 2, 5", "Not carried over:", "page 2: not drawn: shading", "6.3.4/1"} {
		if !has(p, s) {
			t.Errorf("a lossy conversion does not show %q: %s", s, texts(p))
		}
	}
}

func TestPanelShowsAnError(t *testing.T) {
	p := newPanel(t)
	p.Result = &PanelResult{Error: "This PDF is password protected."}
	if !has(p, "This PDF is password protected.") {
		t.Errorf("error: %s", texts(p))
	}
	// An empty result shows nothing below the buttons.
	p.Result = &PanelResult{}
	if got, want := len(p.items()), len(newPanel(t).items()); got != want {
		t.Errorf("an empty result added %d items", got-want)
	}
}

func TestPanelScrolls(t *testing.T) {
	p := newPanel(t)
	p.Result = &PanelResult{Label: "PDF/A-1b", Report: failing()}
	p.Result.expanded = map[int]bool{0: true, 1: true}
	small := image.Pt(PanelWidth, 120)
	_, h := p.layout(PanelWidth)
	p.Scroll(50, small)
	if p.scroll != 50 {
		t.Errorf("scroll = %d", p.scroll)
	}
	p.Scroll(1e6, small)
	if p.scroll != h-small.Y {
		t.Errorf("scroll = %d, want the bottom %d", p.scroll, h-small.Y)
	}
	p.Scroll(-1e6, small)
	if p.scroll != 0 {
		t.Errorf("scroll = %d, want the top", p.scroll)
	}
	// Clicks land on what is shown, scrolled.
	p.Scroll(1e6, small)
	if a := clickItem(t, p, target{kind: toPage, n: 2}); a.Page != 2 {
		t.Errorf("a click on the scrolled panel = %+v", a)
	}
	// Drawing into a taller panel brings the scroll back within the content.
	p.Draw(paint.New(PanelWidth, 2000), image.Rect(0, 0, PanelWidth, 2000))
	if p.scroll != 0 {
		t.Errorf("scroll after a tall draw = %d", p.scroll)
	}
	p.Scroll(30, small)
	p.ScrollToTop()
	if p.scroll != 0 {
		t.Errorf("scroll after ScrollToTop = %d", p.scroll)
	}
}

func TestPanelDraws(t *testing.T) {
	p := newPanel(t)
	p.Result = &PanelResult{Label: "PDF/A-1b", Report: failing()}
	p.Result.expanded = map[int]bool{0: true}
	draw := func() *paint.Canvas {
		c := paint.New(PanelWidth+20, 700)
		p.Draw(c, image.Rect(20, 0, PanelWidth+20, 700))
		return c
	}
	c := draw()
	if got := c.Img.RGBAAt(20, 5); got != nrgba(palette.Border) {
		t.Errorf("left border = %v", got)
	}
	if got := c.Img.RGBAAt(PanelWidth+18, 5); got != nrgba(palette.Chrome) {
		t.Errorf("background = %v", got)
	}
	if got := c.Img.RGBAAt(5, 5); got.A != 0 {
		t.Errorf("drew outside its rectangle: %v", got)
	}
	// Verify is outlined, and labelled, in the accent colour.
	verify := find(t, p, target{kind: toVerify})
	if inkIn(c, verify.r.Add(image.Pt(20, 0)), palette.Accent).Empty() {
		t.Error("verify is not drawn in the accent colour")
	}
	if anyInk(c, verify.r.Add(image.Pt(20, 0))).Empty() {
		t.Error("verify drew nothing")
	}
	// Every clickable item changes when hovered, and again when pressed.
	for _, tg := range []target{
		{kind: toVerify}, {kind: toConvert}, {kind: toClose}, {kind: toProfile},
		{kind: toIssue, n: 1}, {kind: toPage, n: 101},
	} {
		it := find(t, p, tg)
		idle := draw()
		x, y := mid(it.r)
		p.HandlePointer(ptr(platform.Move, platform.None, x, y), panelSize)
		hovered := draw()
		p.HandlePointer(ptr(platform.Press, platform.Left, x, y), panelSize)
		pressed := draw()
		p.HandlePointer(ptr(platform.Release, platform.Left, 0, 0), panelSize)
		p.HandlePointer(ptr(platform.Leave, platform.None, 0, 0), panelSize)
		r := it.r.Add(image.Pt(20, 0))
		if sumAlpha(idle, r) == sumAlpha(hovered, r) && sumRed(idle, r) == sumRed(hovered, r) {
			t.Errorf("%+v looks the same hovered", tg)
		}
		if tg.kind != toPage && sumRed(hovered, r) == sumRed(pressed, r) {
			t.Errorf("%+v looks the same pressed", tg)
		}
	}
	// Disabled controls look different.
	p.Running = true
	dim := draw()
	vr := verify.r.Add(image.Pt(20, 0))
	if sumRed(dim, vr) == sumRed(c, vr) {
		t.Error("a disabled verify button looks enabled")
	}
	conv := find(t, p, target{kind: toConvert}).r.Add(image.Pt(20, 0))
	if sumRed(dim, conv) == sumRed(c, conv) {
		t.Error("a disabled convert button looks enabled")
	}
}

func TestWrap(t *testing.T) {
	face, _ := text.Mono().Face(theme.FontSize, false)
	ch := face.Width("x")
	for _, tc := range []struct {
		s     string
		width int
		want  []string
	}{
		{"", 10 * ch, []string{""}},
		{"short", 10 * ch, []string{"short"}},
		{"one two three four", 10 * ch, []string{"one two", "three four"}},
		{"  spaced   out  ", 20 * ch, []string{"spaced out"}},
		{"abcdefghijklmnopqrstuvwxyz", 10 * ch, []string{"abcdefghij", "klmnopqrst", "uvwxyz"}},
		{"to abcdefghijkl end", 5 * ch, []string{"to", "abcde", "fghij", "kl", "end"}},
		{"ab", 0, []string{"a", "b"}}, // too narrow for anything: a rune a line
	} {
		if got := wrap(face, tc.s, tc.width); !slices.Equal(got, tc.want) {
			t.Errorf("wrap(%q, %d chars) = %q, want %q", tc.s, tc.width/ch, got, tc.want)
		}
	}
}

func TestPlural(t *testing.T) {
	if got := plural(1, "pass", "passes"); got != "1 pass" {
		t.Errorf("one = %q", got)
	}
	if got := plural(0, "pass", "passes"); got != "0 passes" {
		t.Errorf("none = %q", got)
	}
}

func TestPanelItemRect(t *testing.T) {
	p := newPanel(t)
	if got, want := p.ItemRect(PanelAction{Kind: PanelVerify}, PanelWidth), find(t, p, target{kind: toVerify}).r; got != want {
		t.Errorf("verify at %v, want %v", got, want)
	}
	// An action the panel shows no control for has no rectangle: no action at
	// all, a cancel with nothing running, or a page with no link shown.
	for _, act := range []PanelAction{{}, {Kind: PanelCancel}, {Kind: PanelPage, Page: 3}} {
		if r := p.ItemRect(act, PanelWidth); !r.Empty() {
			t.Errorf("%+v at %v", act, r)
		}
	}
}

func TestPanelScrollOffset(t *testing.T) {
	p := newPanel(t)
	p.Result = &PanelResult{Label: "PDF/A-1b", Report: failing()}
	p.Result.expanded = map[int]bool{0: true, 1: true}
	p.Scroll(40, image.Pt(PanelWidth, 120))
	if p.ScrollOffset() != 40 {
		t.Errorf("ScrollOffset = %d after scrolling 40", p.ScrollOffset())
	}
}
