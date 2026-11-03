package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type privateGitQuarantineKey struct{}
type privateGitQuarantineCapability struct{ git, worktree string }

// Only controller-created quarantine state grants this capability. Worker argv
// and writable repository metadata cannot request another writable mount.
func PrivateGitQuarantine(ctx context.Context) (string, string) {
	v, _ := ctx.Value(privateGitQuarantineKey{}).(privateGitQuarantineCapability)
	return v.git, v.worktree
}

type privateGitProcess struct {
	*exec.Cmd
	quarantine *privateGitQuarantine
}

func (p *privateGitProcess) Start() error {
	err := p.Cmd.Start()
	if err != nil && p.quarantine != nil {
		p.quarantine.close()
	}
	return err
}
func (p *privateGitProcess) Wait() error {
	err := p.Cmd.Wait()
	if p.quarantine != nil {
		if err == nil && p.quarantine.initialWorktree {
			err = p.quarantine.normalizeInitialConfig()
		}
		if err == nil {
			err = p.quarantine.publish()
		}
		p.quarantine.close()
	}
	return err
}
func (p *privateGitProcess) Run() error {
	if err := p.Start(); err != nil {
		return err
	}
	return p.Wait()
}

type privateGitQuarantine struct {
	ctx             context.Context
	root            *os.Root
	base, git       string
	original        string
	before          string
	initial         bool
	initialMarker   privateStoreMarker
	expectedIgnore  string
	initialWorktree bool
	release         func()
	once            sync.Once
}

func (q *privateGitQuarantine) normalizeInitialConfig() error {
	path := filepath.Join(q.git, "config")
	data, err := ReadRegularFile(path, 1<<20)
	if err != nil {
		return err
	}
	section := ""
	var clean []string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			section = trimmed
		}
		key, _, _ := strings.Cut(trimmed, "=")
		if section == "[core]" && strings.TrimSpace(key) == "worktree" {
			continue
		}
		clean = append(clean, line)
	}
	return replacePrivateFile(path, []byte(strings.Join(clean, "\n")))
}

func (q *privateGitQuarantine) close() {
	q.once.Do(func() { _ = q.root.Close(); _ = os.RemoveAll(q.base); q.release() })
}

func privateMutation(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "init", "fast-import", "index-pack", "commit-tree", "update-ref", "read-tree":
		return true
	}
	return false
}

// Git never writes through worker-controlled objects/refs/config directories.
// Each mutation runs against a fresh independent copy, then Go publishes through
// an anchored os.Root. A late symlink swap therefore refuses publication rather
// than redirecting Git's output into the source or another repository.
func preparePrivateGitQuarantine(ctx context.Context, store string, initial bool) (*privateGitQuarantine, context.Context, error) {
	leased, release, err := leasePrivateStore(ctx, store)
	if err != nil {
		return nil, ctx, err
	}
	ctx = leased
	root, err := openPrivateMetadataRoot(store)
	if err != nil {
		release()
		return nil, ctx, err
	}
	parent := os.TempDir()
	if isolated := isolationFromContext(ctx); isolated != nil {
		parent = isolated.run
	} else if storage := snapshotStorageFromContext(ctx); storage != nil && pathWithin(storage.Source, parent) {
		parent = filepath.Dir(storage.Store)
	}
	if filepath.Base(store) == ".git" && pathWithin(filepath.Dir(store), parent) {
		root.Close()
		release()
		return nil, ctx, errors.New("private Git quarantine must be outside its working tree")
	}
	base, err := os.MkdirTemp(parent, ".private-git-")
	if err != nil {
		root.Close()
		release()
		return nil, ctx, err
	}
	q := &privateGitQuarantine{ctx: ctx, root: root, base: base, git: filepath.Join(base, "git"), original: store, initial: initial, release: release}
	if storage := snapshotStorageFromContext(ctx); storage != nil {
		q.expectedIgnore = storage.IgnoreSHA
	}
	if err = os.Mkdir(q.git, 0700); err != nil {
		q.close()
		return nil, ctx, err
	}
	_, digest, err := walkPrivateMetadata(ctx, root, q.git)
	if err != nil {
		q.close()
		return nil, ctx, err
	}
	q.before = digest
	if initial {
		entries, err := os.ReadDir(q.git)
		if err != nil || len(entries) != 0 {
			q.close()
			return nil, ctx, errors.New("new private Git store changed before initialization")
		}
	} else if err := q.validatePolicy(); err != nil {
		q.close()
		return nil, ctx, err
	}
	return q, ctx, nil
}

func openPrivateMetadataRoot(path string) (*os.Root, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 || before.Mode().Perm()&0077 != 0 {
		return nil, errors.New("private metadata root must be a private directory")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	opened, err := root.Lstat(".")
	if err != nil || !os.SameFile(before, opened) {
		root.Close()
		return nil, errors.New("private metadata root changed while opening")
	}
	return root, nil
}

type privateMetadataEntry struct {
	path      string
	directory bool
	mode      os.FileMode
	info      os.FileInfo
}

func walkPrivateMetadata(ctx context.Context, root *os.Root, destination string) ([]privateMetadataEntry, string, error) {
	entries, err := listPrivateMetadata(ctx, root, defaultPrivateMetadataLimits())
	if err != nil {
		return nil, "", err
	}
	h := sha256.New()
	for _, entry := range entries {
		relative, info := entry.path, entry.info
		if err := ctx.Err(); err != nil {
			return entries, "", err
		}
		if err := privateSelectedParents(root, relative); err != nil {
			return entries, "", err
		}
		current, err := root.Lstat(relative)
		if err != nil {
			return entries, "", err
		}
		if !os.SameFile(info, current) || current.Mode() != info.Mode() || current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) {
			return entries, "", errors.New("private metadata changed before copying")
		}
		bodySize := info.Size()
		if entry.directory {
			bodySize = 0
		}
		// Length-frame bodies so file bytes cannot impersonate the next
		// metadata entry's header in the change-detection digest.
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00", relative, info.Mode(), bodySize)
		if entry.directory {
			if destination != "" && relative != "." {
				if err := os.Mkdir(filepath.Join(destination, relative), info.Mode().Perm()); err != nil {
					return entries, "", err
				}
			}
			continue
		}
		f, err := privateRootOpenRegular(root, relative, info)
		if err != nil {
			return entries, "", err
		}
		var output *os.File
		writer := io.Writer(h)
		if destination != "" {
			output, err = os.OpenFile(filepath.Join(destination, relative), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				f.Close()
				return entries, "", err
			}
			writer = io.MultiWriter(h, output)
		}
		guarded, flush, guardErr := isolationCredentialWriter(ctx, relative, writer)
		if guardErr != nil {
			f.Close()
			if output != nil {
				output.Close()
			}
			return entries, "", guardErr
		}
		read, copyErr := copyPrivateMetadataBytes(ctx, guarded, f, info.Size())
		if copyErr == nil {
			copyErr = flush()
		}
		after, statErr := f.Stat()
		closeErr := f.Close()
		if output != nil {
			if copyErr == nil {
				copyErr = output.Chmod(info.Mode().Perm())
			}
			if copyErr == nil {
				copyErr = output.Sync()
			}
			copyErr = errors.Join(copyErr, output.Close())
		}
		pathAfter, pathErr := root.Lstat(relative)
		if err = errors.Join(copyErr, statErr, closeErr, pathErr); err != nil {
			return entries, "", err
		}
		if read != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) || !os.SameFile(info, pathAfter) || pathAfter.Mode() != info.Mode() || pathAfter.Size() != info.Size() || !pathAfter.ModTime().Equal(info.ModTime()) {
			return entries, "", errors.New("private metadata changed while copying")
		}
		if err := privateSelectedParents(root, relative); err != nil {
			return entries, "", err
		}
	}
	return entries, hex.EncodeToString(h.Sum(nil)), nil
}

func copyPrivateMetadataBytes(ctx context.Context, out io.Writer, in io.Reader, size int64) (int64, error) {
	var read int64
	buffer := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return read, err
		}
		n, err := in.Read(buffer)
		if n > 0 {
			read += int64(n)
			if read > size {
				return read, errors.New("private metadata grew while copying")
			}
			if _, e := out.Write(buffer[:n]); e != nil {
				return read, e
			}
		}
		if err != nil {
			if err == io.EOF {
				return read, nil
			}
			return read, err
		}
	}
}

func (q *privateGitQuarantine) publish() error {
	if err := q.validatePolicy(); err != nil {
		return err
	}
	guardContext := context.WithValue(q.ctx, privateGitQuarantineKey{}, privateGitQuarantineCapability{git: q.git})
	if err := inspectIsolationGitObjects(guardContext, q.git, []string{"--git-dir=" + q.git}, nil, true); err != nil {
		return err
	}
	beforeEntries, current, err := walkPrivateMetadata(q.ctx, q.root, "")
	if err != nil {
		return err
	}
	if current != q.before {
		return errors.New("private metadata changed during Git operation; quarantine was not published")
	}
	quarantined, err := os.OpenRoot(q.git)
	if err != nil {
		return err
	}
	defer quarantined.Close()
	entries, expected, err := walkPrivateMetadata(q.ctx, quarantined, "")
	if err != nil {
		return err
	}
	if err := q.validatePolicy(); err != nil {
		return err
	}
	// Object graph first, then refs/index/HEAD. A crash must not publish a ref
	// before the object files needed to resolve it have requested persistence.
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.directory != b.directory {
			return a.directory
		}
		if a.directory {
			return len(a.path) < len(b.path)
		}
		ap := strings.HasPrefix(filepath.ToSlash(a.path), "objects/")
		bp := strings.HasPrefix(filepath.ToSlash(b.path), "objects/")
		if ap != bp {
			return ap
		}
		return a.path < b.path
	})
	seen := map[string]bool{}
	for _, entry := range entries {
		seen[entry.path] = true
		if entry.path == "." {
			continue
		}
		if err := q.ctx.Err(); err != nil {
			return err
		}
		if entry.directory {
			if err := privateRootMkdir(q.root, entry.path, entry.mode); err != nil {
				return err
			}
			continue
		}
		info, err := quarantined.Lstat(entry.path)
		if err != nil {
			return err
		}
		f, err := privateRootOpenRegular(quarantined, entry.path, info)
		if err != nil {
			return err
		}
		err = privateRootReplaceFrom(q.ctx, q.root, entry.path, f, info.Size(), entry.mode)
		closeErr := f.Close()
		if err = errors.Join(err, closeErr); err != nil {
			return err
		}
	}
	for _, entry := range beforeEntries {
		if entry.directory || seen[entry.path] {
			continue
		}
		if err := q.root.Remove(entry.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	sort.Slice(beforeEntries, func(i, j int) bool { return len(beforeEntries[i].path) > len(beforeEntries[j].path) })
	for _, entry := range beforeEntries {
		if !entry.directory || seen[entry.path] || entry.path == "." {
			continue
		}
		if err := q.root.Remove(entry.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	_, observed, err := walkPrivateMetadata(q.ctx, q.root, "")
	if err != nil {
		return err
	}
	if observed != expected {
		return errors.New("private metadata changed during safe publication")
	}
	currentRoot, err := os.Lstat(q.original)
	opened, openedErr := q.root.Lstat(".")
	if err != nil || openedErr != nil || !currentRoot.IsDir() || currentRoot.Mode()&os.ModeSymlink != 0 || !os.SameFile(currentRoot, opened) {
		return errors.New("private metadata root was redirected during publication")
	}
	return nil
}
