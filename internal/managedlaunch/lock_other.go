//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd

package managedlaunch

import "errors"

func lock(string) (func(), error) {
	return nil, errors.New("managed tmux launch requires Unix file locking")
}
