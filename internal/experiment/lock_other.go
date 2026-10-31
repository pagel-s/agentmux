//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd

package experiment

import "errors"

func lockOutput(string) (func(), error) {
	return nil, errors.New("experiment runtime requires Unix file locking")
}
