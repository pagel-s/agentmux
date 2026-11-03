//go:build unix

package workflow

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func privateFileHasMultipleLinks(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Nlink > 1
}

func privateRootOpenDirectory(root *os.Root, path string, before os.FileInfo) (*os.File, error) {
	f, err := root.OpenFile(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !before.IsDir() || !opened.IsDir() || !os.SameFile(before, opened) {
		f.Close()
		return nil, errors.New("private directory changed while opening")
	}
	return f, nil
}

func privateRootMkdir(root *os.Root, path string, mode os.FileMode) error {
	if path == "." {
		return nil
	}
	if err := privateSelectedParents(root, path); err != nil {
		return err
	}
	if err := root.Mkdir(path, mode); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("private directory publication was redirected")
	}
	f, err := privateRootOpenDirectory(root, path, info)
	if err != nil {
		return err
	}
	err = f.Chmod(mode)
	return errors.Join(err, f.Close())
}

func privateRootReplaceFrom(ctx context.Context, root *os.Root, path string, in io.Reader, size int64, mode os.FileMode) error {
	if err := privateSelectedParents(root, path); err != nil {
		return err
	}
	parent := filepath.Dir(path)
	info, err := root.Lstat(parent)
	if err != nil {
		return err
	}
	directory, err := privateRootOpenDirectory(root, parent, info)
	if err != nil {
		return err
	}
	defer directory.Close()
	temporary := ".agentmux-publish-" + randomRunID()
	fd, err := unix.Openat(int(directory.Fd()), temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	defer unix.Unlinkat(int(directory.Fd()), temporary, 0)
	f := os.NewFile(uintptr(fd), temporary)
	read, copyErr := copyPrivateMetadataBytes(ctx, f, in, size)
	if copyErr == nil && read != size {
		copyErr = errors.New("private publication source size changed")
	}
	if copyErr == nil {
		copyErr = f.Chmod(mode)
	}
	if copyErr == nil {
		copyErr = f.Sync()
	}
	closeErr := f.Close()
	if err = errors.Join(copyErr, closeErr); err != nil {
		return err
	}
	if err = unix.Renameat(int(directory.Fd()), temporary, int(directory.Fd()), filepath.Base(path)); err != nil {
		return err
	}
	return directory.Sync()
}

func privateRootOpenRegular(root *os.Root, path string, before os.FileInfo) (*os.File, error) {
	f, err := root.OpenFile(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !before.Mode().IsRegular() || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || opened.Mode() != before.Mode() || opened.Size() != before.Size() || !opened.ModTime().Equal(before.ModTime()) {
		f.Close()
		return nil, errors.New("selected private file changed while opening")
	}
	return f, nil
}

// Opening the parent through Root first bounds every ancestor. Readlinkat
// reads the final symlink itself and never follows its potentially outside target.
func privateRootReadlink(root *os.Root, path string) (string, error) {
	parent, err := root.Open(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	defer parent.Close()
	info, err := parent.Stat()
	if err != nil || !info.IsDir() {
		return "", errors.New("private symlink parent is not a directory")
	}
	buffer := make([]byte, 256)
	for len(buffer) <= 1<<16 {
		n, err := unix.Readlinkat(int(parent.Fd()), filepath.Base(path), buffer)
		if err != nil {
			return "", err
		}
		if n < len(buffer) {
			return string(buffer[:n]), nil
		}
		buffer = make([]byte, len(buffer)*2)
	}
	return "", errors.New("selected symlink target exceeds 64 KiB")
}
