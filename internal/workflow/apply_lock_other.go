//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package workflow

import (
	"errors"
	"os"
)

func lockApplication(path string) (*os.File, error) {
	return nil, errors.New("crash-safe application locks require a supported Unix platform")
}

func applicationLockBusy(err error) bool { return false }
