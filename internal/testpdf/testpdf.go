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

// BuildPages returns a valid PDF 1.4 document with one page per size, each
// width x height points and filled solid red, so a render of any page is
// recognisable by its colour.
func BuildPages(sizes [][2]int) []byte {
	kids := ""
	for i := range sizes {
		kids += fmt.Sprintf("%d 0 R ", 3+2*i)
	}
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids, len(sizes)),
	}
	for i, s := range sizes {
		content := fmt.Sprintf("1 0 0 rg 0 0 %d %d re f", s[0], s[1])
		objects = append(objects,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Contents %d 0 R /Resources << >> >>", s[0], s[1], 4+2*i),
			fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	}

	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
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
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return b.Bytes()
}

// BuildText returns a valid PDF 1.4 document with one 200x200 page for each
// string in texts, which shows the string in 18 point Helvetica starting 20
// points from the left and 100 up. Every character is 500 units wide, so
// the text takes 9 points a character.
func BuildText(texts []string) []byte {
	kids := ""
	for i := range texts {
		kids += fmt.Sprintf("%d 0 R ", 3+2*i)
	}
	font := 3 + 2*len(texts)
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids, len(texts)),
	}
	for i, s := range texts {
		content := fmt.Sprintf("BT /F1 18 Tf 20 100 Td (%s) Tj ET", s)
		objects = append(objects,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Contents %d 0 R /Resources << /Font << /F1 %d 0 R >> >> >>", 4+2*i, font),
			fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	}
	widths := ""
	for i := 32; i <= 126; i++ {
		widths += "500 "
	}
	objects = append(objects, fmt.Sprintf("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding /FirstChar 32 /LastChar 126 /Widths [%s] >>", widths))

	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
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
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return b.Bytes()
}
