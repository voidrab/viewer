//go:build !windows

package instance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// DefaultPath returns where the socket of the program called name lives: in
// the user's runtime directory, or the temporary directory without one.
func DefaultPath(name string) string {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, name+".sock")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d.sock", name, os.Getuid()))
}

// addrInUse reports whether err tells that a socket already exists at the
// path listened on.
func addrInUse(err error) bool { return errors.Is(err, syscall.EADDRINUSE) }
