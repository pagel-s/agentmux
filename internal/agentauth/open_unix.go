//go:build unix

package agentauth

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Never block on or follow a racing replacement after credential selection.
func openCredentialFile(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("native credential must remain a regular file")
	}
	return f, nil
}
