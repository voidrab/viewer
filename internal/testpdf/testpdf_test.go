package testpdf

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/voidrab/gopdfrab"
)

func TestBuildIsReadable(t *testing.T) {
	doc, err := gopdfrab.OpenBytes(Build("Hello"))
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	n, err := doc.PageCount()
	if err != nil || n != 1 {
		t.Fatalf("PageCount = %d, %v; want 1, nil", n, err)
	}
	meta, err := doc.Metadata()
	if err != nil || meta["Title"] != "Hello" {
		t.Fatalf("Title = %q, %v; want Hello", meta["Title"], err)
	}
	ok, err := doc.IsPDF()
	if err != nil || !ok {
		t.Fatalf("IsPDF = %v, %v; want a structurally valid PDF", ok, err)
	}
}

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	path := Write(t, dir, "a.pdf", []byte("x"))
	if path != filepath.Join(dir, "a.pdf") {
		t.Fatalf("path = %q", path)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, []byte("x")) {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}
}

func TestSample(t *testing.T) {
	path := Sample(t, "s.pdf")
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, Build("Sample")) || filepath.Base(path) != "s.pdf" {
		t.Fatalf("Sample wrote %q (%d bytes), %v", path, len(got), err)
	}
}

type fatalRecorder struct {
	testing.TB
	fatal bool
}

func (f *fatalRecorder) Helper()      {}
func (f *fatalRecorder) Fatal(...any) { f.fatal = true }

func TestWriteFailure(t *testing.T) {
	rec := &fatalRecorder{TB: t}
	Write(rec, filepath.Join(t.TempDir(), "missing"), "a.pdf", nil)
	if !rec.fatal {
		t.Fatal("Write into a missing directory did not fail the test")
	}
}

func TestBuildPagesIsReadable(t *testing.T) {
	doc, err := gopdfrab.OpenBytes(BuildPages([][2]int{{100, 200}, {300, 50}}))
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	if n, err := doc.Pages(); err != nil || n != 2 {
		t.Fatalf("Pages = %d, %v", n, err)
	}
	for i, want := range [][2]float64{{100, 200}, {300, 50}} {
		p, err := doc.Page(i)
		if err != nil {
			t.Fatal(err)
		}
		if w, h := p.Size(); w != want[0] || h != want[1] {
			t.Errorf("page %d is %vx%v, want %v", i, w, h, want)
		}
	}
}

func TestBuildTextHasTheText(t *testing.T) {
	doc, err := gopdfrab.OpenBytes(BuildText([]string{"hello", "second page"}))
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	if n, _ := doc.Pages(); n != 2 {
		t.Fatalf("%d pages, want 2", n)
	}
	p, _ := doc.Page(1)
	glyphs, err := p.Glyphs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got []rune
	for _, g := range glyphs {
		got = append(got, g.Rune)
	}
	if string(got) != "second page" {
		t.Errorf("text %q, want %q", string(got), "second page")
	}
	if w := glyphs[1].Box.X0 - glyphs[0].Box.X0; w < 8.99 || w > 9.01 {
		t.Errorf("a character is %v points wide, want 9", w)
	}
}

func TestBuildLinkHasALink(t *testing.T) {
	doc, err := gopdfrab.OpenBytes(BuildLink())
	if err != nil {
		t.Fatal(err)
	}
	defer doc.Close()
	p, _ := doc.Page(0)
	links, err := p.Links(context.Background())
	if err != nil || len(links) != 1 || links[0].URI != "https://example.com" {
		t.Errorf("links = %+v, err %v", links, err)
	}
}
