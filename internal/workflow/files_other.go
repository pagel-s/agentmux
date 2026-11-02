//go:build !unix

package workflow

import (
	"errors"
	"os"
)

// Fail closed until a platform-safe nofollow/nonblocking opener is validated.
// Current published workflow platforms are Linux and macOS.
func openRegularFile(path string) (*os.File, error) {
	return nil, errors.New("secure workflow file reads require a supported Unix platform")
}

func openWorkflowDirectory(path string) (*os.File, error) {
	return nil, errors.New("durable workflow directory writes require a supported Unix platform")
}
