package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"viewrab/internal/pdfa"
	"viewrab/internal/ui/platform"
	"viewrab/internal/ui/widget"
)

// panelClick clicks the panel's control that asks for act.
func (f *fixture) panelClick(act widget.PanelAction) {
	f.t.Helper()
	f.a.Handle(platform.FrameEvent{}) // lay the panel out as it now is
	pr := f.a.panelRect()
	r := f.a.panel.ItemRect(act, pr.Dx())
	if r.Empty() {
		f.t.Fatalf("the panel shows no control for %+v", act)
	}
	x, y := mid(r.Add(pr.Min))
	f.click(x, y)
}

func TestThePDFAButtonTogglesThePanel(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 2))
	before := f.a.current().view.Layout().Viewport()
	f.clickButton(widget.ButtonPDFA)
	f.a.Handle(platform.FrameEvent{})
	pr := f.a.panelRect()
	if !f.a.panelOpen || !f.a.toolbar.On[widget.ButtonPDFA] || pr.Dx() != widget.PanelWidth || pr.Max.X != winW {
		t.Fatalf("open %v, button on %v, panel %v", f.a.panelOpen, f.a.toolbar.On[widget.ButtonPDFA], pr)
	}
	// The pages make room for it.
	if vp := f.a.current().view.Layout().Viewport(); vp.X != before.X-widget.PanelWidth {
		t.Errorf("viewport %v, was %v", vp, before)
	}
	// It shows the document, with its facts.
	p := f.a.panel
	if p.Name != "a.pdf" || !strings.Contains(p.Meta, "2 pages") || !strings.Contains(p.Meta, "claims PDF/A-1b") {
		t.Errorf("panel name %q meta %q", p.Name, p.Meta)
	}
	// Its close button, Escape and the toolbar button each close it.
	f.panelClick(widget.PanelAction{Kind: widget.PanelClose})
	if f.a.panelOpen {
		t.Error("the close button left the panel open")
	}
	f.clickButton(widget.ButtonPDFA)
	f.special(platform.KeyEscape, 0)
	if f.a.panelOpen {
		t.Error("Escape left the panel open")
	}
	f.clickButton(widget.ButtonPDFA)
	f.clickButton(widget.ButtonPDFA)
	if f.a.panelOpen {
		t.Error("the toolbar button left the panel open")
	}
	// Escape with nothing to close does nothing.
	f.special(platform.KeyEscape, 0)
	// With no document, there is no panel to show, however it was left.
	f.clickButton(widget.ButtonPDFA)
	f.key('w', platform.Ctrl)
	f.a.Handle(platform.FrameEvent{})
	if !f.a.panelRect().Empty() || f.a.panel.Name != "" {
		t.Errorf("a panel with no document: %v %q", f.a.panelRect(), f.a.panel.Name)
	}
}

func TestEscapeClearsTheSelectionBeforeClosingThePanel(t *testing.T) {
	f := textFixture(t)
	f.a.setPanel(true)
	f.key('a', platform.Ctrl) // select all
	f.special(platform.KeyEscape, 0)
	if !f.a.panelOpen {
		t.Error("the Escape that cleared the selection closed the panel")
	}
	f.special(platform.KeyEscape, 0)
	if f.a.panelOpen {
		t.Error("the next Escape left the panel open")
	}
}

func TestTheWheelOverThePanelScrollsIt(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 12))
	f.a.setPanel(true)
	d := f.a.current()
	d.result = &widget.PanelResult{Label: "PDF/A-1b", Report: &pdfa.Report{Total: 40, Issues: manyIssues(40)}}
	f.a.Handle(platform.FrameEvent{})
	pr := f.a.panelRect()
	top := f.a.panel.ItemRect(widget.PanelAction{Kind: widget.PanelVerify}, pr.Dx())
	scroll := d.view.Layout().Scroll()
	f.a.Handle(platform.ScrollEvent{DY: 10, X: float64(pr.Min.X + 20), Y: float64(pr.Min.Y + 20)})
	if d.view.Layout().Scroll() != scroll {
		t.Error("the wheel over the panel scrolled the pages")
	}
	// The panel moved: its Verify button is no longer where it was.
	x, y := mid(top.Add(pr.Min))
	f.pointer(platform.Move, platform.None, x, y)
	f.pointer(platform.Press, platform.Left, x, y)
	f.pointer(platform.Release, platform.Left, x, y)
	if f.a.job != nil {
		t.Error("the scrolled panel still had Verify under the pointer")
	}
	// Switching documents shows the panel from its top.
	f.open(f.pdf("b.pdf", 1))
	f.a.Handle(platform.FrameEvent{})
	f.panelClick(widget.PanelAction{Kind: widget.PanelVerify})
	f.drain()
}

func manyIssues(n int) []pdfa.Issue {
	out := make([]pdfa.Issue, n)
	for i := range out {
		out[i] = pdfa.Issue{Clause: "6.1/1", Description: "An issue", Count: 1, Pages: []int{1}}
	}
	return out
}

func TestVerifyShowsTheReportInThePanel(t *testing.T) {
	f := newFixture(t)
	release := make(chan struct{})
	f.a.opts.Verify = func(_ context.Context, path, profile string) (pdfa.Report, error) {
		if profile != "pdfa2b" || filepath.Base(path) != "a.pdf" {
			t.Errorf("Verify(%q, %q)", path, profile)
		}
		<-release
		return pdfa.Report{Total: 2, Issues: []pdfa.Issue{{Clause: "6.3.4/1", Count: 2, Pages: []int{3}}}}, nil
	}
	f.open(f.pdf("a.pdf", 4))
	f.a.setPanel(true)
	f.a.panel.Profile = 1 // PDF/A-2b
	f.panelClick(widget.PanelAction{Kind: widget.PanelVerify})
	d := f.a.current()
	if d.result == nil || d.result.Busy != "Checking against PDF/A-2b…" {
		t.Fatalf("while checking: %+v", d.result)
	}
	// Only one check runs at a time.
	f.a.verify()
	f.a.convert()
	f.a.Handle(platform.FrameEvent{})
	if !f.a.panel.Running {
		t.Error("the panel does not know a check is running")
	}
	close(release)
	f.drain()
	if d.result.Report == nil || d.result.Label != "PDF/A-2b" || f.a.job != nil {
		t.Fatalf("after checking: %+v, job %v", d.result, f.a.job)
	}
	// The panel showed the result, so the status line says nothing.
	if f.a.message != "" {
		t.Errorf("status message %q", f.a.message)
	}
	// An issue's page link goes to that page.
	f.a.panelAction(widget.PanelAction{Kind: widget.PanelPage, Page: 3})
	if got := d.view.Page(); got != 2 {
		t.Errorf("page = %d, want the third (2)", got)
	}
}

func TestAResultThePanelDoesNotShowIsReportedInTheStatusLine(t *testing.T) {
	for _, tc := range []struct {
		name   string
		report pdfa.Report
		err    error
		want   string
		kind   widget.MessageKind
	}{
		{"valid", pdfa.Report{Valid: true}, nil, "a.pdf: PDF/A-1b: no issues found", widget.MessageOK},
		{"issues", pdfa.Report{Total: 7, Issues: make([]pdfa.Issue, 3)}, nil, "a.pdf: PDF/A-1b: 7 issues in 3 checks", widget.MessageBad},
		{"error", pdfa.Report{}, errors.New("this document is damaged"), "a.pdf: this document is damaged", widget.MessageBad},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.a.opts.Verify = func(context.Context, string, string) (pdfa.Report, error) { return tc.report, tc.err }
			f.open(f.pdf("a.pdf", 1), f.pdf("b.pdf", 1))
			f.a.selectTab(0)
			f.a.setPanel(true)
			f.a.verify()
			f.a.selectTab(1) // the result is for a tab no longer shown
			f.drain()
			if f.a.message != tc.want || f.a.messageKind != tc.kind {
				t.Errorf("message %q kind %d, want %q kind %d", f.a.message, f.a.messageKind, tc.want, tc.kind)
			}
			if tc.err != nil && f.a.docs[0].result.Error != tc.err.Error() {
				t.Errorf("result %+v", f.a.docs[0].result)
			}
		})
	}
}

func TestCancelStopsTheCheck(t *testing.T) {
	f := newFixture(t)
	f.a.opts.Verify = func(ctx context.Context, _, _ string) (pdfa.Report, error) {
		<-ctx.Done()
		return pdfa.Report{}, &pdfa.Error{Msg: "Cancelled.", Err: ctx.Err()}
	}
	f.open(f.pdf("a.pdf", 1))
	f.a.setPanel(true)
	f.panelClick(widget.PanelAction{Kind: widget.PanelVerify})
	f.panelClick(widget.PanelAction{Kind: widget.PanelCancel})
	f.drain()
	if d := f.a.current(); d.result.Error != "Cancelled." || f.a.job != nil {
		t.Errorf("result %+v, job %v", d.result, f.a.job)
	}
	// With nothing running, Cancel does nothing.
	f.a.panelAction(widget.PanelAction{Kind: widget.PanelCancel})
}

func TestClosingATabStopsItsCheck(t *testing.T) {
	f := newFixture(t)
	f.a.opts.Verify = func(ctx context.Context, _, _ string) (pdfa.Report, error) {
		<-ctx.Done()
		return pdfa.Report{}, ctx.Err()
	}
	f.open(f.pdf("a.pdf", 1))
	f.a.verify()
	f.key('w', platform.Ctrl)
	f.drain()
	if f.a.job != nil || f.a.message != "" {
		t.Errorf("job %v, message %q", f.a.job, f.a.message)
	}
	// With no document, the panel's actions have nothing to act on.
	f.a.verify()
	f.a.convert()
	f.a.panelAction(widget.PanelAction{Kind: widget.PanelPage, Page: 1})
	if f.a.job != nil {
		t.Error("a check started with no document")
	}
}

func TestConvertSavesWhereTheUserChooses(t *testing.T) {
	f := newFixture(t)
	out := filepath.Join(f.dir, "chosen.pdf")
	var suggested, savedTo string
	f.a.opts.SaveFile = func(s string) (string, error) { suggested = s; return out, nil }
	f.a.opts.Convert = func(_ context.Context, in, o, profile string) (pdfa.ConvertReport, error) {
		savedTo = o
		if filepath.Base(in) != "a.pdf" || profile != "pdfa1b" {
			t.Errorf("Convert(%q, %q, %q)", in, o, profile)
		}
		return pdfa.ConvertReport{Output: o, Report: pdfa.Report{Valid: true}}, nil
	}
	in := f.pdf("a.pdf", 1)
	f.open(in)
	f.a.setPanel(true)
	f.panelClick(widget.PanelAction{Kind: widget.PanelConvert})
	f.drain() // the choice made: converting
	if got := f.a.current().result.Busy; got != "Converting to PDF/A-1b…" {
		t.Errorf("while converting: %q", got)
	}
	f.drain()
	if suggested != filepath.Join(f.dir, "a.pdfa.pdf") || savedTo != out {
		t.Errorf("suggested %q, saved to %q", suggested, savedTo)
	}
	if r := f.a.current().result; r.Conversion == nil || r.Conversion.Output != out || !resultValid(r) {
		t.Errorf("result %+v", r)
	}
}

func TestConvertWithoutADialogSavesBesideTheFile(t *testing.T) {
	f := newFixture(t)
	var savedTo string
	f.a.opts.Convert = func(_ context.Context, _, o, _ string) (pdfa.ConvertReport, error) {
		savedTo = o
		return pdfa.ConvertReport{Output: o, Report: pdfa.Report{Total: 1, Issues: make([]pdfa.Issue, 1)}}, nil
	}
	f.open(f.pdf("a.pdf", 1), f.pdf("b.pdf", 1))
	f.a.panel.Profile = 2 // the structural profile
	f.a.selectTab(0)
	f.a.convert()
	f.drain()
	f.drain()
	if savedTo != filepath.Join(f.dir, "a.fixed.pdf") {
		t.Errorf("saved to %q", savedTo)
	}
	// The panel was closed, so the status line tells, as a failure: issues remain.
	if f.a.message != "a.pdf: saved as a.fixed.pdf" || f.a.messageKind != widget.MessageBad {
		t.Errorf("message %q kind %d", f.a.message, f.a.messageKind)
	}
}

func TestConvertDialogOutcomes(t *testing.T) {
	f := newFixture(t)
	f.open(f.pdf("a.pdf", 1))
	d := f.a.current()
	earlier := &widget.PanelResult{Label: "PDF/A-1b", Report: &pdfa.Report{Valid: true}}
	d.result = earlier

	// Choosing nowhere leaves the earlier result in place.
	f.a.opts.SaveFile = func(string) (string, error) { return "", nil }
	f.a.convert()
	f.drain()
	if d.result != earlier || f.a.job != nil {
		t.Errorf("after choosing nowhere: %+v, job %v", d.result, f.a.job)
	}
	// A dialog that fails shows why.
	f.a.opts.SaveFile = func(string) (string, error) { return "", errors.New("no dialog") }
	f.a.convert()
	f.drain()
	if d.result.Error != "no dialog" {
		t.Errorf("after a failed dialog: %+v", d.result)
	}
	// Cancelling while the dialog is open cancels the conversion.
	release := make(chan struct{})
	f.a.opts.SaveFile = func(string) (string, error) { <-release; return "/tmp/x.pdf", nil }
	f.a.convert()
	f.a.panelAction(widget.PanelAction{Kind: widget.PanelCancel})
	close(release)
	f.drain()
	if d.result.Error != "Cancelled." {
		t.Errorf("after cancelling: %+v", d.result)
	}
}

func TestOpenConvertedOpensOrReloadsTheCopy(t *testing.T) {
	f := newFixture(t)
	a, b := f.pdf("a.pdf", 1), f.pdf("b.pdf", 2)
	f.open(a)
	f.a.setPanel(true)
	// A copy not open yet opens in a new tab.
	f.a.panelAction(widget.PanelAction{Kind: widget.PanelOpen, Path: b})
	if got := f.names(); len(got) != 2 || f.a.active != 1 {
		t.Fatalf("tabs %v, active %d", got, f.a.active)
	}
	// One that is open, such as a copy converted in place, reloads in its tab.
	old := f.a.docs[0]
	f.a.selectTab(1)
	f.a.panelAction(widget.PanelAction{Kind: widget.PanelOpen, Path: a})
	if got := f.names(); len(got) != 2 || got[0] != "a.pdf" || f.a.active != 0 || f.a.docs[0] == old {
		t.Errorf("tabs %v, active %d, reloaded %v", got, f.a.active, f.a.docs[0] != old)
	}
	// A copy that cannot be read any more is reported, and its tab kept.
	if err := writeFile(a, "not a pdf"); err != nil {
		t.Fatal(err)
	}
	f.a.panelAction(widget.PanelAction{Kind: widget.PanelOpen, Path: a})
	if len(f.a.docs) != 2 || f.a.messageKind != widget.MessageBad || !strings.HasPrefix(f.a.message, "Could not open a.pdf") {
		t.Errorf("tabs %v, message %q", f.names(), f.a.message)
	}
}

func TestPanelMetaListsWhatIsKnown(t *testing.T) {
	f := newFixture(t)
	f.a.opts.Inspect = func(string) (pdfa.Info, error) { return pdfa.Info{Version: "1.7"}, nil }
	f.open(f.pdf("a.pdf", 1))
	if got := panelMeta(f.a.current()); !strings.HasPrefix(got, "1 page · ") || !strings.HasSuffix(got, " · PDF 1.7") {
		t.Errorf("meta = %q", got)
	}
}

// writeFile replaces the file at path with content by writing a new file and
// renaming it into place, as saving a converted copy does. The old file is
// renamed aside and removed first, which Windows allows while a document
// holds it open, as it does not allow renaming over it; either way the old
// file stays intact for a document still reading it.
func writeFile(path, content string) error {
	tmp, old := path+".tmp", path+".old"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	if err := os.Rename(path, old); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return os.Remove(old)
}
