// Package pdfa adapts gopdfrab's PDF/A verification and conversion to plain,
// JSON-friendly values the viewer's frontend can render directly.
package pdfa

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/voidrab/gopdfrab"
)

// maxMessages caps the distinct messages kept per issue group, so a document
// with thousands of identical violations still yields a compact report.
const maxMessages = 5

// ErrUnknownProfile is returned for a profile name Profiles does not list.
var ErrUnknownProfile = errors.New("unknown profile")

// Error is a failure from this package: Error returns a sentence fit to show
// the user, and Unwrap the cause, so errors.Is still matches gopdfrab's
// sentinel errors.
type Error struct {
	Msg string
	Err error
}

func (e *Error) Error() string { return e.Msg }
func (e *Error) Unwrap() error { return e.Err }

// Profile names a verification target the viewer offers.
type Profile struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	profile *gopdfrab.Profile
}

var profiles = []Profile{
	{"pdfa1b", "PDF/A-1b", gopdfrab.PDFA1B},
	{"pdfa2b", "PDF/A-2b", gopdfrab.PDFA2B},
	{"pdf", "PDF structure", gopdfrab.PDF},
}

// Profiles returns the profiles the viewer offers, the default first.
func Profiles() []Profile { return slices.Clone(profiles) }

func lookup(name string) (*gopdfrab.Profile, error) {
	for _, p := range profiles {
		if p.Name == name {
			return p.profile, nil
		}
	}
	return nil, fmt.Errorf("%w %q", ErrUnknownProfile, name)
}

// Info is what the viewer shows about a document before any verification.
// Fields gopdfrab cannot read are left empty rather than failing the open.
// It deliberately skips the page count: walking the page tree is slow on
// large files, and the renderer reports it anyway.
type Info struct {
	Version string `json:"version"`
	Title   string `json:"title"`
	// Claimed is the PDF/A conformance the file declares, e.g. "PDF/A-1b",
	// or empty when it declares none.
	Claimed string `json:"claimed"`
}

// Inspect reads a document's basic properties.
func Inspect(path string) (Info, error) {
	doc, err := gopdfrab.Open(path)
	if err != nil {
		return Info{}, explain(err)
	}
	defer doc.Close()

	var info Info
	info.Version, _ = doc.Version()
	if meta, err := doc.Metadata(); err == nil {
		info.Title = meta["Title"]
	}
	if part, level, err := doc.ClaimedConformance(); err == nil && part != "" {
		info.Claimed = "PDF/A-" + part + strings.ToLower(level)
	}
	return info, nil
}

// Issue groups every report of one check.
type Issue struct {
	// Clause is the ISO clause and subclause, e.g. "6.3.4/1".
	Clause      string `json:"clause"`
	Description string `json:"description"`
	Count       int    `json:"count"`
	// Pages lists the distinct 1-based pages affected, ascending; 0 stands
	// for the document as a whole.
	Pages []int `json:"pages"`
	// Messages holds up to maxMessages distinct messages, in report order.
	Messages []string `json:"messages"`
}

// Report is the outcome of a verification.
type Report struct {
	Valid  bool    `json:"valid"`
	Total  int     `json:"total"`
	Issues []Issue `json:"issues"`
	Millis int64   `json:"millis"`
}

// Verify checks the document at path against the named profile.
func Verify(ctx context.Context, path, profile string) (Report, error) {
	p, err := lookup(profile)
	if err != nil {
		return Report{}, explain(err)
	}
	start := time.Now()
	res, err := gopdfrab.VerifyContext(ctx, path, p, gopdfrab.Options{})
	if err != nil {
		return Report{}, explain(err)
	}
	return newReport(res, time.Since(start)), nil
}

func newReport(res gopdfrab.Result, took time.Duration) Report {
	return Report{
		Valid:  res.Valid,
		Total:  res.Count(),
		Issues: group(res),
		Millis: took.Milliseconds(),
	}
}

// group folds a result's issues into one Issue per check, in clause order,
// in a single pass over the issues.
func group(res gopdfrab.Result) []Issue {
	byCheck := res.IssuesByCheck()
	out := []Issue{}
	for _, c := range res.Checks() {
		reports := byCheck[c]
		clause, sub := reports[0].Clause()
		is := Issue{
			Clause:      fmt.Sprintf("%s/%d", clause, sub),
			Description: c.Description(),
			Count:       len(reports),
			Pages:       make([]int, 0, len(reports)),
			Messages:    []string{},
		}
		for _, r := range reports {
			is.Pages = append(is.Pages, r.Page())
			for _, m := range r.Messages() {
				if len(is.Messages) < maxMessages && !slices.Contains(is.Messages, m) {
					is.Messages = append(is.Messages, m)
				}
			}
		}
		slices.Sort(is.Pages)
		is.Pages = slices.Clip(slices.Compact(is.Pages))
		out = append(out, is)
	}
	return out
}

// ConvertReport is the outcome of a conversion.
type ConvertReport struct {
	Output     string `json:"output"`
	Iterations int    `json:"iterations"`
	// Report is the verdict on the converted output; its issues are the
	// residual ones no repair could fix.
	Report Report `json:"report"`
	// Rasterized lists pages rebuilt as images, so no longer text or vectors.
	Rasterized []int `json:"rasterized"`
	// Lost describes content that could not be carried over at all, including
	// features the rasterizer dropped.
	Lost []string `json:"lost"`
}

// Convert rewrites the document at in towards the named profile and saves the
// result to out. The save is atomic, so out may name the input itself.
func Convert(ctx context.Context, in, out, profile string) (ConvertReport, error) {
	p, err := lookup(profile)
	if err != nil {
		return ConvertReport{}, explain(err)
	}
	start := time.Now()
	cr, err := gopdfrab.ConvertContext(ctx, in, p, gopdfrab.Options{})
	if err != nil {
		return ConvertReport{}, explain(err)
	}
	defer cr.Close()
	if err := cr.Save(out); err != nil {
		return ConvertReport{}, explain(err)
	}
	return ConvertReport{
		Output:     out,
		Iterations: cr.Iterations,
		Report:     newReport(cr.Result, time.Since(start)),
		Rasterized: append([]int{}, cr.RasterizedPages...),
		Lost:       losses(cr.LostObjects, cr.RasterDrops),
	}, nil
}

// losses describes lost objects and dropped raster features, one line each.
func losses(lost []gopdfrab.PDFError, drops []gopdfrab.RasterDrop) []string {
	out := []string{}
	for _, l := range lost {
		out = append(out, pageLabel(l.Page())+strings.Join(l.Messages(), "; "))
	}
	for _, d := range drops {
		for _, f := range d.Features {
			out = append(out, pageLabel(d.Page)+"not drawn: "+f)
		}
	}
	return out
}

func pageLabel(page int) string {
	if page == 0 {
		return "document: "
	}
	return fmt.Sprintf("page %d: ", page)
}

// DefaultOutputPath suggests where to save a conversion of in, following the
// gopdfrab CLI's naming: report.pdf becomes report.pdfa.pdf, or
// report.fixed.pdf for the structural profile.
func DefaultOutputPath(in, profile string) string {
	suffix := ".pdfa.pdf"
	if profile == "pdf" {
		suffix = ".fixed.pdf"
	}
	return strings.TrimSuffix(in, filepath.Ext(in)) + suffix
}

// explain wraps err in an *Error carrying a sentence for the user.
func explain(err error) error {
	return &Error{Msg: describe(err), Err: err}
}

func describe(err error) string {
	switch {
	case errors.Is(err, gopdfrab.ErrNotPDF):
		return "This file is not a PDF document."
	case errors.Is(err, gopdfrab.ErrDamaged):
		return "This PDF is too damaged to read its structure."
	case errors.Is(err, gopdfrab.ErrPasswordRequired):
		return "This PDF is password protected."
	case errors.Is(err, gopdfrab.ErrEncrypted):
		return "This PDF uses an unsupported encryption scheme."
	case errors.Is(err, gopdfrab.ErrUnresolvableGraph):
		return "The PDF's objects could not be resolved, so no output was produced."
	case errors.Is(err, context.Canceled):
		return "Cancelled."
	default:
		return err.Error()
	}
}
