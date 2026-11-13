// Package testpdf builds small, well-formed PDF files for tests.
package testpdf

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Build returns a valid one-page PDF 1.4 document titled title. It paints a
// DeviceRGB rectangle and has no OutputIntent or XMP metadata, so it is a
// well-formed PDF that is not PDF/A conformant.
func Build(title string) []byte {
	content := "0 0 1 rg 20 20 160 160 re f"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Contents 4 0 R /Resources << >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		fmt.Sprintf("<< /Title (%s) /Author (testpdf) /Producer (testpdf) >>", title),
	}

	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objects))
	for i, obj := range objects {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	id := "<0123456789abcdef0123456789abcdef>"
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R /Info 5 0 R /ID [%s %s] >>\nstartxref\n%d\n%%%%EOF\n",
		len(objects)+1, id, id, xref)
	return b.Bytes()
}

// Sample writes Build("Sample") as name in a fresh temporary directory and
// returns its path.
func Sample(tb testing.TB, name string) string {
	tb.Helper()
	return Write(tb, tb.TempDir(), name, Build("Sample"))
}

// Write saves data as dir/name and returns the path, failing tb on error.
func Write(tb testing.TB, dir, name string, data []byte) string {
	tb.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		tb.Fatal(err)
	}
	return path
}
