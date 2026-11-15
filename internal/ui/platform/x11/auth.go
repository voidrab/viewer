package x11

import (
	"bufio"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// Address families of Xauthority entries.
const (
	familyLocal = 256
	familyWild  = 65535
)

// cookieName is the only authorisation scheme the client speaks.
const cookieName = "MIT-MAGIC-COOKIE-1"

// authEntry is one record of an Xauthority file.
type authEntry struct {
	family  uint16
	address string
	number  string
	name    string
	data    []byte
}

// readAuthEntries parses an Xauthority file. Each record is a big-endian
// 16-bit family followed by four fields, each a big-endian 16-bit length and
// that many bytes. A truncated last record ends the list.
func readAuthEntries(r io.Reader) []authEntry {
	br := bufio.NewReader(r)
	field := func() ([]byte, bool) {
		var n uint16
		if binary.Read(br, binary.BigEndian, &n) != nil {
			return nil, false
		}
		b := make([]byte, n)
		_, err := io.ReadFull(br, b)
		return b, err == nil
	}
	var out []authEntry
	for {
		var fam uint16
		if binary.Read(br, binary.BigEndian, &fam) != nil {
			return out
		}
		addr, ok1 := field()
		num, ok2 := field()
		name, ok3 := field()
		data, ok4 := field()
		if !ok1 || !ok2 || !ok3 || !ok4 {
			return out
		}
		out = append(out, authEntry{family: fam, address: string(addr), number: string(num), name: string(name), data: data})
	}
}

// authFile returns the path of the user's Xauthority file.
func authFile() string {
	if p := os.Getenv("XAUTHORITY"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".Xauthority")
}

// hostname is replaced by tests.
var hostname = os.Hostname

// host returns the name of this host, or an empty string.
func host() string {
	h, _ := hostname()
	return h
}

// findAuth returns the MIT-MAGIC-COOKIE-1 for display number on the local
// host, from the Xauthority file at path. It returns an empty name and no
// data when there is none, in which case the client connects without
// authorisation, which a server that relies on other means accepts.
func findAuth(path string, number int) (name string, data []byte) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil
	}
	defer f.Close()
	host, _ := hostname()
	num := strconv.Itoa(number)
	for _, e := range readAuthEntries(f) {
		if e.name != cookieName || e.number != "" && e.number != num {
			continue
		}
		if e.family == familyWild || e.family == familyLocal && e.address == host {
			return e.name, e.data
		}
	}
	return "", nil
}
