//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd

package managed

import "errors"

func acquireLock(string) (func(), error) {
	return nil, errors.New("managed runtime requires Unix advisory locks")
}
