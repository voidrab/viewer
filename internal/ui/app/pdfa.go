package app

import (
	"context"
	"fmt"
	"image"
	"path/filepath"
	"slices"
	"strings"

	"viewer/internal/pdfa"
	"viewer/internal/ui/widget"
)

// panelScrollSpeed multiplies wheel distances over the PDF/A panel, as the page
// view's scroll speed does over the pages.
const panelScrollSpeed = 5

// job is the PDF/A check running: one at a time, for one document.
type job struct {
	doc    *document
	cancel context.CancelFunc
}

// setPanel shows or hides the PDF/A panel, which changes the room the pages
// have.
func (a *App) setPanel(open bool) {
	a.panelOpen = open
	a.afterChange()
}

// panelMeta is the line of facts the panel shows about d.
func panelMeta(d *document) string {
	pages := "1 page"
	if n := d.view.Pages(); n != 1 {
		pages = fmt.Sprintf("%d pages", n)
	}
	facts := []string{pages, humanSize(d.bytes)}
	if d.version != "" {
		facts = append(facts, "PDF "+d.version)
	}
	if d.claimed != "" {
		facts = append(facts, "claims "+d.claimed)
	}
	return strings.Join(facts, " · ")
}

// panelAction carries out what the panel asked for.
func (a *App) panelAction(act widget.PanelAction) {
	switch act.Kind {
	case widget.PanelClose:
		a.setPanel(false)
	case widget.PanelVerify:
		a.verify()
	case widget.PanelConvert:
		a.convert()
	case widget.PanelCancel:
		if a.job != nil {
			a.job.cancel()
		}
	case widget.PanelPage:
		if d := a.current(); d != nil {
			d.view.GoToPage(act.Page - 1)
		}
	case widget.PanelOpen:
		a.openConverted(act.Path)
	case widget.PanelProfiles:
		r := a.panelRect()
		anchor := a.panel.ItemRect(act, r.Dx()).Add(r.Min).Sub(image.Pt(0, a.panel.ScrollOffset()))
		a.showMenu(menuProfile, a.panel.Profiles, a.panel.Profile, anchor)
	}
}

// profile returns the profile chosen in the panel.
func (a *App) profile() pdfa.Profile { return a.profiles[a.panel.Profile] }

// begin starts a job on the active document, showing busy in its panel, and
// returns the job's context. It returns nil when there is no document or a job
// is already running.
func (a *App) begin(busy string) (*document, context.Context) {
	d := a.current()
	if d == nil || a.job != nil {
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.job = &job{doc: d, cancel: cancel}
	d.result = &widget.PanelResult{Busy: busy}
	a.panel.ScrollToTop()
	a.win.Invalidate()
	return d, ctx
}

// finish ends the job on d with its outcome: res, or err if it failed, unless d
// has been closed. A message in the status line tells of it when the panel is
// not showing d.
func (a *App) finish(d *document, res *widget.PanelResult, err error, summary string) {
	a.job.cancel()
	a.job = nil
	if !slices.Contains(a.docs, d) {
		return // its tab was closed
	}
	if err != nil {
		res = &widget.PanelResult{Error: err.Error()}
		summary = err.Error()
	}
	d.result = res
	if !a.panelOpen || a.current() != d {
		kind := widget.MessageOK
		if err != nil || !resultValid(res) {
			kind = widget.MessageBad
		}
		a.say(kind, d.name+": "+summary)
	}
}

// resultValid reports whether a finished result found the document conformant.
func resultValid(res *widget.PanelResult) bool {
	if res.Conversion != nil {
		return res.Conversion.Report.Valid
	}
	return res.Report.Valid
}

// verify checks the active document against the chosen profile in the
// background.
func (a *App) verify() {
	prof := a.profile()
	d, ctx := a.begin("Checking against " + prof.Label + "…")
	if d == nil {
		return
	}
	go func() {
		report, err := a.opts.Verify(ctx, d.path, prof.Name)
		a.inbox <- func() {
			summary := prof.Label + ": no issues found"
			if !report.Valid {
				summary = fmt.Sprintf("%s: %d issues in %d checks", prof.Label, report.Total, len(report.Issues))
			}
			a.finish(d, &widget.PanelResult{Label: prof.Label, Report: &report}, err, summary)
		}
	}()
}

// convert asks where to save a conformant copy of the active document, then
// converts it there in the background. Choosing nowhere leaves the result the
// panel showed before.
func (a *App) convert() {
	prof := a.profile()
	prev := a.current()
	if prev == nil {
		return
	}
	before := prev.result
	d, ctx := a.begin("Choose where to save the converted copy…")
	if d == nil {
		return
	}
	suggested := pdfa.DefaultOutputPath(d.path, prof.Name)
	go func() {
		out, err := suggested, error(nil)
		if a.opts.SaveFile != nil {
			out, err = a.opts.SaveFile(suggested)
		}
		switch {
		case err != nil:
		case ctx.Err() != nil:
			err = &pdfa.Error{Msg: "Cancelled.", Err: ctx.Err()}
		case out == "":
			a.inbox <- func() {
				a.job.cancel()
				a.job = nil
				if slices.Contains(a.docs, d) {
					d.result = before
				}
			}
			return
		default:
			a.inbox <- func() {
				if slices.Contains(a.docs, d) { // not moved to another window
					d.result = &widget.PanelResult{Busy: "Converting to " + prof.Label + "…"}
				}
			}
			var cr pdfa.ConvertReport
			cr, err = a.opts.Convert(ctx, d.path, out, prof.Name)
			a.inbox <- func() {
				summary := "saved as " + filepath.Base(cr.Output)
				a.finish(d, &widget.PanelResult{Label: prof.Label, Conversion: &cr}, err, summary)
			}
			return
		}
		a.inbox <- func() { a.finish(d, nil, err, "") }
	}()
}

// openConverted opens the converted copy at path. A tab that already shows
// that file, such as one converted in place, reloads to show the new content.
func (a *App) openConverted(path string) {
	i := a.find(path)
	if i < 0 {
		a.Open(path)
		return
	}
	d, err := a.load(path)
	if err != nil {
		a.say(widget.MessageBad, fmt.Sprintf("Could not open %s: %v", filepath.Base(path), err))
		return
	}
	a.drop(a.docs[i])
	a.docs[i], a.active = d, i
	a.afterChange()
}
