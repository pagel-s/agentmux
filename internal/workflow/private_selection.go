package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const privateSelectionDirectory = "agentmux-selections"

type privateWorktreeSelection struct {
	Version         int    `json:"version"`
	Repository      string `json:"repository"`
	Store           string `json:"store"`
	IgnoreSHA256    string `json:"ignore_sha256"`
	SelectionSHA256 string `json:"selection_sha256"`
}

// This sibling is outside the worker's worktree and filesystem mount. It lets
// standalone verification restore the starting selection without trusting any
// claim made by writable .git metadata. Saved reports carry their own pin.
func savePrivateWorktreeSelection(ctx context.Context, storage snapshotStorage, identity string) error {
	v := privateWorktreeSelection{1, storage.Source, storage.Store, storage.IgnoreSHA, identity}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeGuardedPrivateMetadata(ctx, storage.Source+".selection.json", append(data, '\n'), true)
}

func restorePrivateWorktreeSelection(ctx context.Context, storage snapshotStorage) error {
	data, err := ReadRegularFile(storage.Source+".selection.json", 32<<10)
	if err != nil {
		return errors.New("private worktree lacks its external starting path selection")
	}
	var v privateWorktreeSelection
	if err := DecodeJSON(data, &v); err != nil || v.Version != 1 || v.Repository != storage.Source || v.Store != storage.Store || v.IgnoreSHA256 != storage.IgnoreSHA || !digestID(v.SelectionSHA256) {
		return errors.New("private worktree external starting path selection is invalid")
	}
	if _, err := readPrivateSelection(ctx, storage, v.SelectionSHA256); err != nil {
		return err
	}
	rememberPrivateSelection(storage, v.SelectionSHA256, false)
	return nil
}

type privateSelectionKey struct{ repository, store string }

// Identities live in controller context and saved snapshots/verifications. The
// worker may remove a manifest, but cannot replace its pinned bytes unnoticed.
// Key by store as well as repository: child watch/lab caches are independent.
type privateSelectionRegistry struct {
	sync.Mutex
	identities map[privateSelectionKey][]string
}

func newPrivateSelectionRegistry() *privateSelectionRegistry {
	return &privateSelectionRegistry{identities: map[privateSelectionKey][]string{}}
}

func privateSelectionIdentities(storage snapshotStorage) []string {
	if storage.Selections == nil {
		return nil
	}
	storage.Selections.Lock()
	defer storage.Selections.Unlock()
	return append([]string(nil), storage.Selections.identities[privateSelectionKey{storage.Source, storage.Store}]...)
}

func currentPrivateSelection(storage snapshotStorage) string {
	identities := privateSelectionIdentities(storage)
	if len(identities) == 1 {
		return identities[0]
	}
	return ""
}

func rememberPrivateSelection(storage snapshotStorage, identity string, replace bool) {
	if storage.Selections == nil {
		return
	}
	storage.Selections.Lock()
	defer storage.Selections.Unlock()
	key := privateSelectionKey{storage.Source, storage.Store}
	if replace {
		storage.Selections.identities[key] = []string{identity}
		return
	}
	for _, old := range storage.Selections.identities[key] {
		if identity == old {
			return
		}
	}
	storage.Selections.identities[key] = append(storage.Selections.identities[key], identity)
	sort.Strings(storage.Selections.identities[key])
}

func readPrivateSelection(ctx context.Context, storage snapshotStorage, identity string) ([]byte, error) {
	if !digestID(identity) {
		return nil, errors.New("private path selection has no valid pinned identity")
	}
	root, err := openPrivateMetadataRoot(storage.Store)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	data, err := readPrivatePolicyFile(ctx, root, filepath.Join(privateSelectionDirectory, identity), maxPatchBytes)
	if err != nil {
		return nil, errors.New("pinned private path selection is unavailable")
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != identity || len(data) > 0 && data[len(data)-1] != 0 {
		return nil, errors.New("pinned private path selection changed")
	}
	previous, count := "", 0
	var records [][]byte
	if len(data) > 0 {
		records = bytes.Split(data[:len(data)-1], []byte{0})
	}
	for _, path := range records {
		value := string(path)
		if count > 0 && value <= previous || !validPrivateSelectedPath(value) {
			return nil, errors.New("pinned private path selection is malformed")
		}
		previous = value
		count++
		if count > 2*MaxSnapshotFiles {
			return nil, errors.New("pinned private path selection exceeds its bound")
		}
	}
	return data, nil
}

func storePrivateSelection(ctx context.Context, storage snapshotStorage, data []byte) (string, error) {
	if len(data) > maxPatchBytes || containsIsolationCredential(ctx, data) {
		return "", errors.New("private path selection exceeds its bound or contains selected credentials")
	}
	digest := sha256.Sum256(data)
	identity := hex.EncodeToString(digest[:])
	root, err := openPrivateMetadataRoot(storage.Store)
	if err != nil {
		return "", err
	}
	defer root.Close()
	path := filepath.Join(privateSelectionDirectory, identity)
	if _, err := root.Lstat(path); err == nil {
		if _, err := readPrivateSelection(ctx, storage, identity); err != nil {
			return "", err
		}
		return identity, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := privateRootMkdir(root, privateSelectionDirectory, 0700); err != nil {
		return "", err
	}
	if err := privateRootReplaceFrom(ctx, root, path, bytes.NewReader(data), int64(len(data)), 0600); err != nil {
		return "", err
	}
	return identity, nil
}

func pinPrivateSelection(ctx context.Context, storage snapshotStorage, paths []string) error {
	var data bytes.Buffer
	for _, path := range paths {
		data.WriteString(path)
		data.WriteByte(0)
	}
	identity, err := storePrivateSelection(ctx, storage, data.Bytes())
	if err == nil {
		rememberPrivateSelection(storage, identity, true)
	}
	return err
}

func copyPrivateSelection(ctx context.Context, source, target snapshotStorage, identity string) error {
	data, err := readPrivateSelection(ctx, source, identity)
	if err != nil {
		return err
	}
	if _, err = storePrivateSelection(ctx, target, data); err != nil {
		return err
	}
	rememberPrivateSelection(target, identity, false)
	return nil
}

func validPrivateSelectedPath(path string) bool {
	if path == "" || strings.IndexByte(path, 0) >= 0 || filepath.IsAbs(path) || filepath.Clean(path) != path || path == ".." || strings.HasPrefix(path, "../") {
		return false
	}
	for _, component := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.EqualFold(component, ".git") {
			return false
		}
	}
	return true
}

// HEAD/index may add tracked names, including force-added ignored files. They
// cannot remove a pinned name, and unsupported gitlinks/directories fail closed.
func privateTrackedPathNames(data []byte, index bool) ([]byte, error) {
	var names bytes.Buffer
	for _, record := range bytes.Split(data, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		meta, path, ok := bytes.Cut(record, []byte{'\t'})
		fields := strings.Fields(string(meta))
		if !ok || len(fields) != 3 || fields[0] != "100644" && fields[0] != "100755" && fields[0] != "120000" {
			return nil, errors.New("private snapshots do not support gitlinks or directory index entries")
		}
		if index && (!objectID(fields[1]) || fields[2] != "0" && fields[2] != "1" && fields[2] != "2" && fields[2] != "3") || !index && (fields[1] != "blob" || !objectID(fields[2])) {
			return nil, errors.New("private tracked path metadata is malformed")
		}
		names.Write(path)
		names.WriteByte(0)
	}
	return names.Bytes(), nil
}

// Enumerate against a fresh controller-owned empty index. A worker's gitlink,
// sparse entry or tracked directory must never suppress ordinary descendants.
// The anchored metadata copy retains only the separately pinned ignore policy;
// it is discarded, never published back to the worker repository.
func privateUntrackedPaths(ctx context.Context, repo string, storage snapshotStorage) ([]byte, error) {
	q, child, err := preparePrivateGitQuarantine(ctx, storage.Store, false)
	if err != nil {
		return nil, err
	}
	defer q.close()
	if err := os.Remove(filepath.Join(q.git, "index")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	worktree := repo
	if runtime := isolationFromContext(ctx); runtime != nil && repo == runtime.source {
		worktree = "" // the source capability mounts this exact input read-only
	}
	child = context.WithValue(child, privateGitQuarantineKey{}, privateGitQuarantineCapability{git: q.git, worktree: worktree})
	return privateCommandOutput(child, q.git, []string{"--git-dir=" + q.git, "--work-tree=" + repo}, nil, "ls-files", "--others", "--exclude-standard", "--full-name", "-z", "--", ":(top)")
}
