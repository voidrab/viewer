package testpdf

import (
	"bytes"
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
