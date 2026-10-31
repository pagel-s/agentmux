//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd

package research

import "errors"

func lockOutput(string) (func(), error) {
	return nil, errors.New("research runtime requires Unix file locking")
}
