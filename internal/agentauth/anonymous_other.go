//go:build !linux

package agentauth

import (
	"errors"
	"os"
)

func anonymousCredential(data []byte) (*os.File, string, error) {
	return nil, "", errors.New("anonymous native authentication requires Linux sealed memfd support")
}
