//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package workflow

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Keep the coordination inode in place: unlinking a flock file allows another
// process to lock a new inode while an older waiter still uses the old one.
// The kernel releases the actual lock if this process exits or is killed.
func lockApplication(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		f.Close()
		return nil, errors.New("application lock must be a private regular file")
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another application or recovery is active: %w", err)
	}
	// Release by closing this descriptor, never LOCK_UN: an admitted Git writer
	// inherits the same open-file description and must keep the lock if its
	// owner is killed before that writer finishes.
	return f, nil
}

func applicationLockBusy(err error) bool {
	return errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN)
}
