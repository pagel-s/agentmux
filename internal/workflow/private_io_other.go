//go:build !unix

package workflow

import (
	"context"
	"errors"
	"io"
	"os"
)

func privateFileHasMultipleLinks(info os.FileInfo) bool { return true }
func privateRootOpenRegular(root *os.Root, path string, before os.FileInfo) (*os.File, error) {
	return nil, errors.New("private snapshot file guarantees are unsupported on this platform")
}
func privateRootReadlink(root *os.Root, path string) (string, error) {
	return "", errors.New("private snapshot symlink guarantees are unsupported on this platform")
}

func privateRootOpenDirectory(root *os.Root, path string, before os.FileInfo) (*os.File, error) {
	return nil, errors.New("private directory guarantees are unsupported on this platform")
}
func privateRootMkdir(root *os.Root, path string, mode os.FileMode) error {
	return errors.New("private directory guarantees are unsupported on this platform")
}
func privateRootReplaceFrom(ctx context.Context, root *os.Root, path string, in io.Reader, size int64, mode os.FileMode) error {
	return errors.New("private publication guarantees are unsupported on this platform")
}
