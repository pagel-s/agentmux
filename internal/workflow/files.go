package workflow

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// ReadRegularFile reads an explicitly selected finite file. It refuses links
// and special files before opening, and the platform opener prevents a racing
// replacement with a FIFO or symlink from blocking or redirecting the read.
func ReadRegularFile(path string, maximum int64) ([]byte, error) {
	if maximum < 1 || maximum > 32<<20 {
		return nil, errors.New("invalid workflow file size limit")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > maximum {
		return nil, fmt.Errorf("workflow input must be a regular file, not a symlink, of at most %d bytes", maximum)
	}
	f, err := openRegularFileMatching(path, before)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) || opened.Size() > maximum {
		return nil, errors.New("workflow input changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(f, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, errors.New("workflow input exceeds its size limit")
	}
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) || int64(len(data)) != after.Size() {
		return nil, errors.New("workflow input changed while reading")
	}
	return data, nil
}

// Match a prior selection without following a replacement link or blocking on
// a replacement FIFO. Callers still check for in-place changes after reading.
func openRegularFileMatching(path string, before os.FileInfo) (*os.File, error) {
	f, err := openRegularFile(path)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || before.Mode() != opened.Mode() || before.Size() != opened.Size() || !before.ModTime().Equal(opened.ModTime()) {
		f.Close()
		return nil, errors.New("selected file changed while opening")
	}
	return f, nil
}
