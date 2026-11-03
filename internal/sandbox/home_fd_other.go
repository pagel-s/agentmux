//go:build !linux

package sandbox

import "errors"

func anonymousHomeSource(file HomeFile) (string, error) {
	return "", errors.New("anonymous credential mounts require Linux sealed memfd support")
}
