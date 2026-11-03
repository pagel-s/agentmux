package workflow

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const privateMetadataDirectoryBatch = 128

type privateMetadataLimits struct {
	entries, pathBytes int
	bytes              int64
}

func defaultPrivateMetadataLimits() privateMetadataLimits {
	return privateMetadataLimits{entries: 4*MaxSnapshotFiles + 1024, pathBytes: 64 << 20, bytes: 4 * MaxSnapshotBytes}
}

type privateMetadataBudget struct {
	limits                privateMetadataLimits
	discovered, pathBytes int
	bytes                 int64
}

func (b *privateMetadataBudget) discover(path string) error {
	if b.discovered >= b.limits.entries || len(path) > b.limits.pathBytes-b.pathBytes {
		return errors.New("private metadata exceeds its entry or path-name bound")
	}
	b.discovered++
	b.pathBytes += len(path)
	return nil
}

// Charge names when the kernel yields them, before retaining a directory's
// sorted list. Readdirnames(-1) and filepath.WalkDir allocate the whole list
// before a caller can enforce its own entry limit.
func privateMetadataDirectoryNames(ctx context.Context, root *os.Root, path string, before os.FileInfo, budget *privateMetadataBudget) ([]string, error) {
	dir, err := privateRootOpenDirectory(root, path, before)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	names := []string{}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := dir.Readdirnames(privateMetadataDirectoryBatch)
		for _, name := range batch {
			if name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
				return nil, errors.New("invalid private metadata entry")
			}
			relative := name
			if path != "." {
				relative = filepath.Join(path, name)
			}
			if containsIsolationCredential(ctx, []byte(relative)) {
				return nil, ErrIsolationCredentialLeak
			}
			if err := budget.discover(relative); err != nil {
				return nil, err
			}
			names = append(names, name)
		}
		if err != nil {
			if err != io.EOF {
				return nil, err
			}
			break
		}
	}
	after, err := dir.Stat()
	if err != nil {
		return nil, err
	}
	current, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, after) || !os.SameFile(before, current) || after.Mode() != before.Mode() || current.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) || !current.ModTime().Equal(before.ModTime()) {
		return nil, errors.New("private metadata directory changed while enumerating")
	}
	sort.Strings(names)
	return names, nil
}

func listPrivateMetadata(ctx context.Context, root *os.Root, limits privateMetadataLimits) ([]privateMetadataEntry, error) {
	budget := privateMetadataBudget{limits: limits}
	if err := budget.discover("."); err != nil {
		return nil, err
	}
	entries := []privateMetadataEntry{}
	var visit func(string) error
	visit = func(path string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if containsIsolationCredential(ctx, []byte(path)) {
			return ErrIsolationCredentialLeak
		}
		if err := privateSelectedParents(root, path); err != nil {
			return err
		}
		info, err := root.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() && !info.Mode().IsRegular() {
			return errors.New("private metadata contains a symlink or special file")
		}
		if !info.IsDir() {
			if privateFileHasMultipleLinks(info) {
				return errors.New("private metadata contains a shared inode")
			}
			if info.Size() < 0 || info.Size() > limits.bytes-budget.bytes {
				return errors.New("private metadata exceeds its byte bound")
			}
			budget.bytes += info.Size()
		}
		entries = append(entries, privateMetadataEntry{path: path, directory: info.IsDir(), mode: info.Mode().Perm(), info: info})
		if !info.IsDir() {
			return nil
		}
		names, err := privateMetadataDirectoryNames(ctx, root, path, info, &budget)
		if err != nil {
			return err
		}
		for _, name := range names {
			child := name
			if path != "." {
				child = filepath.Join(path, name)
			}
			if err := visit(child); err != nil {
				return err
			}
		}
		return nil
	}
	err := visit(".")
	return entries, err
}
