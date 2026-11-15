package win32

import (
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf16"
)

// pdfFilter is the file type list of the file dialogs, as OPENFILENAME takes
// it: pairs of a description and a pattern, each ending in a NUL, and the
// list in another.
const pdfFilter = "PDF documents (*.pdf)\x00*.pdf\x00All files (*.*)\x00*.*\x00\x00"

// chosenFiles decodes the paths an open dialog with OFN_ALLOWMULTISELECT
// wrote into its buffer: NUL-separated, ending in two NULs, and either a
// single full path or a directory followed by the names chosen in it.
func chosenFiles(buf []uint16) []string {
	var parts []string
	for len(buf) > 0 && buf[0] != 0 {
		end := slices.Index(buf, 0)
		if end < 0 {
			end = len(buf)
		}
		parts = append(parts, string(utf16.Decode(buf[:end])))
		buf = buf[min(end+1, len(buf)):]
	}
	if len(parts) < 2 {
		return parts
	}
	paths := make([]string, len(parts)-1)
	for i, name := range parts[1:] {
		paths[i] = filepath.Join(parts[0], name)
	}
	return paths
}

// chosenFirst decodes buf up to its first NUL. A save dialog has already
// added the default extension, ".pdf", to a name typed without one.
func chosenFirst(buf []uint16) string {
	if i := slices.Index(buf, 0); i >= 0 {
		buf = buf[:i]
	}
	return string(utf16.Decode(buf))
}

// dialogBuffer returns the buffer a file dialog writes its result into, of n
// code units, holding initial, the name it suggests, cut to fit.
func dialogBuffer(n int, initial string) []uint16 {
	buf := make([]uint16, n)
	units := utf16.Encode([]rune(strings.ReplaceAll(initial, "\x00", "")))
	copy(buf[:n-1], units)
	return buf
}
