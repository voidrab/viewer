package pdfa

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab"

	"viewrab/internal/testpdf"
)

func sample(t *testing.T) string { return testpdf.Sample(t, "doc.pdf") }

func TestProfiles(t *testing.T) {
	got := Profiles()
	if got[0].Name != "pdfa1b" {
		t.Fatalf("Profiles() = %v; want pdfa1b first", got)
	}
	for _, p := range got {
		if _, err := lookup(p.Name); err != nil {
			t.Errorf("listed profile %q does not resolve: %v", p.Name, err)
		}
		if p.Label == "" {
			t.Errorf("profile %q has no label", p.Name)
		}
	}
	got[0].Name = "mutated"
	if Profiles()[0].Name != "pdfa1b" {
		t.Fatal("Profiles returned its internal slice")
	}
}

func TestLookupUnknown(t *testing.T) {
	if _, err := lookup("nope"); !errors.Is(err, ErrUnknownProfile) {
		t.Fatalf("lookup(nope) err = %v; want ErrUnknownProfile", err)
	}
}

// inspect opens the document at path, returns its Info, and closes the
// document.
func inspect(t *testing.T, path string) Info {
	t.Helper()
	doc, err := gopdfrab.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	return Inspect(doc)
}

func TestInspect(t *testing.T) {
	info := inspect(t, sample(t))
	want := Info{Version: "1.4", Title: "Sample"}
	if info != want {
		t.Fatalf("Inspect = %+v; want %+v", info, want)
	}
}

func TestInspectClaimedConformance(t *testing.T) {
	src := sample(t)
	out := filepath.Join(t.TempDir(), "out.pdf")
	if _, err := Convert(context.Background(), src, out, "pdfa2b"); err != nil {
		t.Fatal(err)
	}
	if info := inspect(t, out); info.Claimed != "PDF/A-2b" {
		t.Fatalf("Claimed = %q; want PDF/A-2b", info.Claimed)
	}
}

func TestVerifyNonConformant(t *testing.T) {
	rep, err := Verify(context.Background(), sample(t), "pdfa1b")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Valid || rep.Total == 0 || len(rep.Issues) == 0 {
		t.Fatalf("Verify = %+v; want an invalid report with issues", rep)
	}
	sum := 0
	for _, is := range rep.Issues {
		sum += is.Count
		if !strings.Contains(is.Clause, "/") || is.Description == "" || len(is.Messages) == 0 {
			t.Errorf("incomplete issue %+v", is)
		}
		if !slices.IsSorted(is.Pages) {
			t.Errorf("pages not sorted: %v", is.Pages)
		}
	}
	if sum != rep.Total {
		t.Fatalf("issue counts sum to %d; Total = %d", sum, rep.Total)
	}
}

func TestVerifyStructureValid(t *testing.T) {
	rep, err := Verify(context.Background(), sample(t), "pdf")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Valid || rep.Total != 0 || rep.Issues == nil || len(rep.Issues) != 0 {
		t.Fatalf("Verify(pdf) = %+v; want valid with an empty, non-nil issue list", rep)
	}
}

func TestVerifyErrors(t *testing.T) {
	if _, err := Verify(context.Background(), sample(t), "nope"); !errors.Is(err, ErrUnknownProfile) || err.Error() != `unknown profile "nope"` {
		t.Errorf("unknown profile err = %v", err)
	}
	missing := filepath.Join(t.TempDir(), "missing.pdf")
	if _, err := Verify(context.Background(), missing, "pdfa1b"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file err = %v; want ErrNotExist", err)
	}
}

func TestConvert(t *testing.T) {
	for _, profile := range []string{"pdfa1b", "pdfa2b"} {
		t.Run(profile, func(t *testing.T) {
			src := sample(t)
			out := DefaultOutputPath(src, profile)
			cr, err := Convert(context.Background(), src, out, profile)
			if err != nil {
				t.Fatal(err)
			}
			if cr.Output != out || cr.Iterations < 1 || !cr.Report.Valid {
				t.Fatalf("Convert = %+v", cr)
			}
			if cr.Rasterized == nil || cr.Lost == nil {
				t.Fatal("Rasterized and Lost must be non-nil for JSON")
			}
			rep, err := Verify(context.Background(), out, profile)
			if err != nil || !rep.Valid {
				t.Fatalf("re-verify of output = %+v, %v; want valid", rep, err)
			}
		})
	}
}

func TestConvertErrors(t *testing.T) {
	ctx := context.Background()
	src := sample(t)
	if _, err := Convert(ctx, src, src+".out", "nope"); !errors.Is(err, ErrUnknownProfile) {
		t.Errorf("unknown profile err = %v", err)
	}
	notPDF := testpdf.Write(t, t.TempDir(), "x.pdf", []byte("hello"))
	if _, err := Convert(ctx, notPDF, notPDF+".out", "pdfa1b"); !errors.Is(err, gopdfrab.ErrNotPDF) {
		t.Errorf("not a PDF err = %v", err)
	}
	badOut := filepath.Join(t.TempDir(), "no", "such", "dir", "out.pdf")
	if _, err := Convert(ctx, src, badOut, "pdfa1b"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("saving into a missing directory err = %v; want ErrNotExist", err)
	}
}

func TestLosses(t *testing.T) {
	if got := losses(nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("losses(nil, nil) = %#v; want empty, non-nil", got)
	}

	// Real PDFError values come only from a verification, so borrow a
	// document-level one and check it is described with its messages.
	res, err := gopdfrab.Verify(sample(t), gopdfrab.PDFA1B)
	if err != nil {
		t.Fatal(err)
	}
	var docLevel gopdfrab.PDFError
	found := false
	for _, is := range res.Issues {
		if is.IsDocumentLevel() && len(is.Messages()) > 0 {
			docLevel, found = is, true
			break
		}
	}
	if !found {
		t.Fatal("fixture has no document-level issue to borrow")
	}
	drops := []gopdfrab.RasterDrop{{Page: 2, Features: []string{"shading", "JBIG2 image"}}}
	got := losses([]gopdfrab.PDFError{docLevel}, drops)
	want := []string{
		"document: " + strings.Join(docLevel.Messages(), "; "),
		"page 2: not drawn: shading",
		"page 2: not drawn: JBIG2 image",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("losses = %q; want %q", got, want)
	}
}

func TestGroupCapsMessages(t *testing.T) {
	// Repeat a real result's issues so every check reports more messages than
	// the cap, then confirm grouping still keeps at most maxMessages each.
	res, err := gopdfrab.VerifyBytes(testpdf.Build("x"), gopdfrab.PDFA1B)
	if err != nil {
		t.Fatal(err)
	}
	many := gopdfrab.Result{Type: res.Type}
	for range maxMessages + 3 {
		many.Issues = append(many.Issues, res.Issues...)
	}
	for _, is := range group(many) {
		if len(is.Messages) > maxMessages {
			t.Fatalf("issue %s keeps %d messages; cap is %d", is.Clause, len(is.Messages), maxMessages)
		}
	}
}

func TestPageLabel(t *testing.T) {
	if got := pageLabel(0); got != "document: " {
		t.Errorf("pageLabel(0) = %q", got)
	}
	if got := pageLabel(7); got != "page 7: " {
		t.Errorf("pageLabel(7) = %q", got)
	}
}

func TestDefaultOutputPath(t *testing.T) {
	cases := []struct{ in, profile, want string }{
		{"/a/report.pdf", "pdfa1b", "/a/report.pdfa.pdf"},
		{"/a/report.PDF", "pdfa2b", "/a/report.pdfa.pdf"},
		{"/a/report", "pdfa1b", "/a/report.pdfa.pdf"},
		{"/a/report.pdf", "pdf", "/a/report.fixed.pdf"},
	}
	for _, c := range cases {
		if got := DefaultOutputPath(c.in, c.profile); got != c.want {
			t.Errorf("DefaultOutputPath(%q, %q) = %q; want %q", c.in, c.profile, got, c.want)
		}
	}
}

func TestExplain(t *testing.T) {
	wrap := func(err error) error { return fmt.Errorf("open: %w", err) }
	cases := []struct {
		err  error
		want string
	}{
		{wrap(gopdfrab.ErrNotPDF), "not a PDF"},
		{wrap(gopdfrab.ErrDamaged), "damaged"},
		{wrap(gopdfrab.ErrPasswordRequired), "password"},
		{wrap(gopdfrab.ErrEncrypted), "encryption"},
		{wrap(gopdfrab.ErrUnresolvableGraph), "no output"},
		{wrap(context.Canceled), "Cancelled"},
		{errors.New("disk on fire"), "disk on fire"},
	}
	for _, c := range cases {
		err := explain(c.err)
		if !strings.Contains(err.Error(), c.want) || !errors.Is(err, c.err) {
			t.Errorf("explain(%v) = %q; want it to mention %q and wrap the cause", c.err, err, c.want)
		}
	}
}
