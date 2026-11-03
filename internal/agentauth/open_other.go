//go:build !unix

package agentauth

import (
	"errors"
	"os"
)

func openCredentialFile(path string) (*os.File, error) {
	return nil, errors.New("safe native credential selection is unsupported on this platform")
}
