package workflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Syncing file contents alone does not make a newly created or renamed record
// discoverable after a machine interruption. Persist its directory entry too.
// Filesystems which cannot honor this request are refused rather than silently
// advertising a durable record. This cannot make stronger promises than the
// filesystem, kernel and storage hardware provide for fsync.
func syncDirectory(path string) error {
	f, err := openWorkflowDirectory(path)
	if err != nil {
		return fmt.Errorf("open workflow directory for durability: %w", err)
	}
	info, err := f.Stat()
	if err != nil || !info.IsDir() {
		f.Close()
		return errors.New("workflow durability target must be a directory")
	}
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(syncErr, closeErr); err != nil {
		return fmt.Errorf("persist workflow directory entry: %w", err)
	}
	return nil
}

func syncParent(path string) error { return syncDirectory(filepath.Dir(path)) }

func mkdirAllDurable(path string, mode os.FileMode) error {
	if !filepath.IsAbs(path) {
		return errors.New("durable workflow directory path must be absolute")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	missing := []string{}
	for current := abs; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("durable workflow directory ancestry must not contain a symlink or special file")
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		missing = append(missing, current)
	}
	if err := os.MkdirAll(abs, mode); err != nil {
		return err
	}
	for _, created := range missing {
		info, err := os.Lstat(created)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("created workflow directory changed before persistence")
		}
		if err := syncDirectory(created); err != nil {
			return err
		}
		if err := syncParent(created); err != nil {
			return err
		}
	}
	return nil
}
