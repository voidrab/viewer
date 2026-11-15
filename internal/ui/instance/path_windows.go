package instance

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// DefaultPath returns where the socket of the program called name lives: in
// the user's own temporary directory.
func DefaultPath(name string) string {
	return filepath.Join(os.TempDir(), name+".sock")
}

// addrInUse reports whether err tells that a socket already exists at the
// path listened on.
func addrInUse(err error) bool { return errors.Is(err, windows.WSAEADDRINUSE) }
