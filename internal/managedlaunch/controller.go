package managedlaunch

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"agentmux/internal/managedconfig"
)

func (l *Launcher) pinController(cfg managedconfig.Config) error {
	executable, err := filepath.EvalSymlinks(l.options.Executable)
	if err != nil {
		return err
	}
	// The workspace registry can grow after Up. Pin even when today's projects
	// do not expose the executable: a later project may contain its source tree.
	// Supervisors and all subsequently launched workers keep the original copy.
	root := l.options.StateDir + ".bin"
	if err := os.Mkdir(root, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return errors.New("controller copy directory must be private and outside the project")
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil || canonical != root {
		return errors.New("controller copy directory must not contain symlink parents")
	}
	for _, a := range cfg.Agents {
		if a.Workdir != "" {
			workspace, e := filepath.EvalSymlinks(a.Workdir)
			if e == nil {
				rel, e := filepath.Rel(workspace, root)
				if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
					return errors.New("state/controller directory must be outside every writable project")
				}
			}
		}
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	path := filepath.Join(root, hex.EncodeToString(digest[:])+".agentmux")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if os.IsExist(err) {
		info, e := os.Lstat(path)
		if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Mode().Perm()&0100 == 0 {
			return errors.New("existing pinned controller is not a private regular executable")
		}
		saved, e := os.ReadFile(path)
		if e != nil || sha256.Sum256(saved) != digest {
			return errors.New("existing pinned controller checksum differs")
		}
	} else if err != nil {
		return err
	} else {
		if _, err = f.Write(data); err != nil {
			f.Close()
			os.Remove(path)
			return err
		}
		if err = f.Close(); err != nil {
			os.Remove(path)
			return err
		}
	}
	l.options.Executable = path
	return nil
}
