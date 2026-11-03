package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	StoragePrivate         = "private"
	StorageShared          = "shared"
	privateStoreMarkerName = "agentmux-private-store.json"
	privateRawAttributes   = "* -text -filter -ident -working-tree-encoding\n"
)

type snapshotStorageContextKey struct{}
type snapshotStorage struct {
	Policy, Store, Source, IgnoreSHA string
	Selections                       *privateSelectionRegistry
}
type privateStoreMarker struct {
	Version      int    `json:"version"`
	ObjectFormat string `json:"object_format"`
	Bare         bool   `json:"bare"`
	IgnoreSHA256 string `json:"ignore_sha256"`
}

type privateStoreLeaseContextKey struct{}
type privateStorePermit struct {
	ch   chan struct{}
	refs int
}

var privateStorePermits = struct {
	sync.Mutex
	entries map[string]*privateStorePermit
}{entries: map[string]*privateStorePermit{}}

// Serialize controller writes and metadata validation per private store. Git
// publishes temporary pack/index files while writing; a concurrent status read
// must not mistake that ordinary transition for a damaged or redirected store.
func leasePrivateStore(ctx context.Context, store string) (context.Context, func(), error) {
	held, _ := ctx.Value(privateStoreLeaseContextKey{}).(map[string]bool)
	if held[store] {
		return ctx, func() {}, nil
	}
	privateStorePermits.Lock()
	entry := privateStorePermits.entries[store]
	if entry == nil {
		entry = &privateStorePermit{ch: make(chan struct{}, 1)}
		privateStorePermits.entries[store] = entry
	}
	entry.refs++
	privateStorePermits.Unlock()
	releaseRef := func() {
		privateStorePermits.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(privateStorePermits.entries, store)
		}
		privateStorePermits.Unlock()
	}
	select {
	case entry.ch <- struct{}{}:
	case <-ctx.Done():
		releaseRef()
		return ctx, func() {}, ctx.Err()
	}
	copyHeld := map[string]bool{}
	for key, value := range held {
		copyHeld[key] = value
	}
	copyHeld[store] = true
	var once sync.Once
	return context.WithValue(ctx, privateStoreLeaseContextKey{}, copyHeld), func() { once.Do(func() { <-entry.ch; releaseRef() }) }, nil
}

func snapshotStorageFromContext(ctx context.Context) *snapshotStorage {
	v, _ := ctx.Value(snapshotStorageContextKey{}).(*snapshotStorage)
	return v
}

func SnapshotStoragePolicy(ctx context.Context) string {
	if storage := snapshotStorageFromContext(ctx); storage != nil && storage.Policy == StoragePrivate {
		return StoragePrivate
	}
	return StorageShared
}

func SnapshotObjectStore(ctx context.Context) string {
	if storage := snapshotStorageFromContext(ctx); storage != nil && storage.Policy == StoragePrivate {
		return storage.Store
	}
	return ""
}

func SnapshotIgnoreSHA256(ctx context.Context) string {
	if storage := snapshotStorageFromContext(ctx); storage != nil && storage.Policy == StoragePrivate {
		return storage.IgnoreSHA
	}
	return ""
}

func snapshotSelectionSHA256(ctx context.Context) string {
	if storage := snapshotStorageFromContext(ctx); storage != nil {
		return currentPrivateSelection(*storage)
	}
	return ""
}

// PrepareSnapshotStorage creates a private, independent object store. Source
// Git is queried with read-only plumbing; no source object, ref, index, config
// or worktree metadata is created. Shared is an explicit legacy opt-out.
func PrepareSnapshotStorage(ctx context.Context, policy, repository, storeDir string) (context.Context, error) {
	if containsIsolationCredential(ctx, []byte(storeDir)) {
		return ctx, ErrIsolationCredentialLeak
	}
	if policy == StorageShared {
		return context.WithValue(ctx, snapshotStorageContextKey{}, &snapshotStorage{Policy: StorageShared}), nil
	}
	if policy != "" && policy != StoragePrivate {
		return ctx, errors.New("snapshot storage must be private or shared")
	}
	var inherited *snapshotStorage
	var inheritErr error
	if inherited, inheritErr = privateStorageForRepository(ctx, repository); inheritErr != nil {
		return ctx, inheritErr
	}
	repo, err := repositoryRoot(ctx, repository)
	if err != nil {
		return ctx, err
	}
	path, err := newOutsidePath(repo, storeDir)
	if err != nil {
		return ctx, err
	}
	format, err := gitText(ctx, repo, nil, "rev-parse", "--show-object-format")
	if err != nil {
		return ctx, err
	}
	if err = initPrivateGit(ctx, path, format, true); err != nil {
		return ctx, err
	}
	ignore, err := sourceIgnorePolicy(ctx, repo)
	if err != nil {
		return ctx, err
	}
	ignoreSHA, err := setPrivateIgnorePolicy(ctx, path, ignore)
	if err != nil {
		return ctx, err
	}
	storage := &snapshotStorage{Policy: StoragePrivate, Store: path, Source: repo, IgnoreSHA: ignoreSHA, Selections: newPrivateSelectionRegistry()}
	if inherited != nil {
		for _, identity := range privateSelectionIdentities(*inherited) {
			if err := copyPrivateSelection(ctx, *inherited, *storage, identity); err != nil {
				return ctx, err
			}
		}
	}
	prepared := context.WithValue(ctx, snapshotStorageContextKey{}, storage)
	base, err := gitText(prepared, repo, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return ctx, err
	}
	if err = importPrivateBase(prepared, repo, path, base); err != nil {
		return ctx, err
	}
	return prepared, nil
}

// SnapshotContext binds existing private evidence without creating stores or
// importing source history. Legacy snapshots keep their original semantics.
func SnapshotContext(ctx context.Context, s Snapshot) (context.Context, error) {
	if s.Store == "" {
		return ctx, nil
	}
	marker, err := validatePrivateStore(ctx, s.Store)
	if err != nil {
		return ctx, err
	}
	if !digestID(s.IgnoreSHA256) || marker.IgnoreSHA256 != s.IgnoreSHA256 {
		return ctx, errors.New("private snapshot ignore policy differs from its recorded identity")
	}
	if !filepath.IsAbs(s.Repository) {
		return ctx, errors.New("private snapshot repository must be absolute")
	}
	if !digestID(s.SelectionSHA256) {
		return ctx, errors.New("private snapshot lacks its pinned path selection")
	}
	registry := newPrivateSelectionRegistry()
	if existing := snapshotStorageFromContext(ctx); existing != nil && existing.Selections != nil {
		registry = existing.Selections
	}
	storage := &snapshotStorage{Policy: StoragePrivate, Store: s.Store, Source: s.Repository, IgnoreSHA: s.IgnoreSHA256, Selections: registry}
	if _, err := readPrivateSelection(ctx, *storage, s.SelectionSHA256); err != nil {
		return ctx, err
	}
	rememberPrivateSelection(*storage, s.SelectionSHA256, false)
	return context.WithValue(ctx, snapshotStorageContextKey{}, storage), nil
}

// A laboratory integrates into the original project but retains its own child
// execution store. Source observations write only into that run's private cache,
// never the parent laboratory's store or the source repository object database.
func ReportSnapshotContext(ctx context.Context, r Report) (context.Context, error) {
	source := SourceSnapshot(r)
	if r.Snapshot.Store != "" {
		source.Store = r.Snapshot.Store
		source.IgnoreSHA256 = r.Snapshot.IgnoreSHA256
	}
	prepared, err := SnapshotContext(ctx, source)
	if err != nil {
		return ctx, err
	}
	bind := func(snapshot *Snapshot) error {
		if snapshot == nil || snapshot.Store == "" {
			return nil
		}
		_, err := SnapshotContext(prepared, *snapshot)
		return err
	}
	for _, candidate := range r.Candidates {
		if err := bind(candidate.StartingSnapshot); err != nil {
			return ctx, err
		}
		if err := bind(candidate.FinalSnapshot); err != nil {
			return ctx, err
		}
	}
	for _, step := range r.Steps {
		if err := bind(step.StartingSnapshot); err != nil {
			return ctx, err
		}
		if err := bind(step.FinalSnapshot); err != nil {
			return ctx, err
		}
	}
	if r.Storage == StoragePrivate && r.Worktree != "" {
		storage := *snapshotStorageFromContext(prepared)
		storage.Source, storage.Store = r.Worktree, filepath.Join(r.Worktree, ".git")
		if _, err := readPrivateSelection(prepared, storage, source.SelectionSHA256); err != nil {
			return ctx, err
		}
		rememberPrivateSelection(storage, source.SelectionSHA256, false)
	}
	if err := bind(r.FinalSnapshot); err != nil {
		return ctx, err
	}
	return prepared, nil
}

func SnapshotContextForRepository(ctx context.Context, repository string) (context.Context, error) {
	v, err := privateStorageForRepository(ctx, repository)
	if err != nil || v == nil {
		return ctx, err
	}
	return context.WithValue(ctx, snapshotStorageContextKey{}, v), nil
}

func SnapshotObjectRepository(s Snapshot) string {
	if s.Store != "" {
		return s.Store
	}
	return s.Repository
}

func SnapshotDiff(ctx context.Context, initial, final Snapshot) ([]byte, error) {
	repo := SnapshotObjectRepository(final)
	if err := validateSnapshot(ctx, repo, initial); err != nil {
		return nil, err
	}
	if err := validateSnapshot(ctx, repo, final); err != nil {
		return nil, err
	}
	return snapshotGitOutput(ctx, repo, final.Store != "", nil, "diff", "--binary", "--full-index", "--no-ext-diff", "--no-textconv", initial.Tree, final.Tree, "--")
}

func privateStorageForRepository(ctx context.Context, repository string) (*snapshotStorage, error) {
	configured := snapshotStorageFromContext(ctx)
	if configured != nil && configured.Policy == StorageShared {
		return nil, nil
	}
	abs, err := filepath.Abs(repository)
	if err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	if configured != nil && configured.Policy == StoragePrivate && (abs == configured.Source || pathWithin(configured.Source, abs)) {
		if marker, err := validatePrivateStore(ctx, configured.Store); err != nil {
			return nil, err
		} else if configured.IgnoreSHA != "" && marker.IgnoreSHA256 != configured.IgnoreSHA {
			return nil, errors.New("private snapshot ignore policy changed")
		}
		return configured, nil
	}
	root, matched, err := privateRepositoryRoot(ctx, abs)
	if err != nil {
		return nil, err
	}
	if matched {
		marker, err := validatePrivateStore(ctx, filepath.Join(root, ".git"))
		if err != nil {
			return nil, err
		}
		if configured != nil && configured.IgnoreSHA != "" && marker.IgnoreSHA256 != configured.IgnoreSHA {
			return nil, errors.New("private worktree ignore policy differs from its pinned source policy")
		}
		registry := newPrivateSelectionRegistry()
		if configured != nil && configured.Selections != nil {
			registry = configured.Selections
		}
		storage := &snapshotStorage{Policy: StoragePrivate, Store: filepath.Join(root, ".git"), Source: root, IgnoreSHA: marker.IgnoreSHA256, Selections: registry}
		if len(privateSelectionIdentities(*storage)) == 0 {
			if err := restorePrivateWorktreeSelection(ctx, *storage); err != nil {
				return nil, err
			}
		}
		return storage, nil
	}
	if configured != nil && configured.Policy == StoragePrivate {
		return nil, errors.New("private worktree metadata is missing or no longer identifies its own repository")
	}
	return nil, nil
}

func privateRepositoryRoot(ctx context.Context, abs string) (string, bool, error) {
	for path := abs; ; path = filepath.Dir(path) {
		store := filepath.Join(path, ".git")
		// The controller's sibling identity survives removal of worker-writable
		// Git metadata. Its presence must never turn into ordinary shared Git
		// semantics merely because the marker or a manifest disappeared.
		external := false
		if configured := snapshotStorageFromContext(ctx); configured == nil || configured.Policy != StorageShared {
			info, err := os.Lstat(path + ".selection.json")
			if err == nil {
				external = true
				if !info.Mode().IsRegular() {
					return "", true, errors.New("private worktree external starting path selection is not a regular file")
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				return "", true, errors.New("private worktree external starting path selection is unavailable")
			}
		}
		storeInfo, storeErr := os.Lstat(store)
		if storeErr == nil && storeInfo.Mode().IsRegular() && !external {
			if path == filepath.Dir(path) {
				return "", false, nil
			}
			continue
		}
		if info, err := os.Lstat(filepath.Join(store, privateStoreMarkerName)); err == nil {
			if !info.Mode().IsRegular() {
				return "", true, errors.New("private store marker is not a regular file")
			}
			marker, err := validatePrivateStore(ctx, store)
			if err != nil {
				return "", true, err
			}
			if marker.Bare {
				return "", true, errors.New("private worktree identifies a bare object store")
			}
			if external {
				storage := snapshotStorage{Source: path, Store: store, IgnoreSHA: marker.IgnoreSHA256}
				if err := restorePrivateWorktreeSelection(ctx, storage); err != nil {
					return "", true, err
				}
			}
			return path, true, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", true, err
		} else if external {
			return "", true, errors.New("private worktree metadata is missing despite its external starting path selection")
		}
		if path == filepath.Dir(path) {
			return "", false, nil
		}
	}
}

func initPrivateGit(ctx context.Context, store, format string, bare bool) error {
	if format != "sha1" && format != "sha256" {
		return errors.New("unsupported private Git object format")
	}
	if !filepath.IsAbs(store) {
		return errors.New("private object store must be absolute")
	}
	if _, err := os.Lstat(store); !errors.Is(err, os.ErrNotExist) {
		return errors.New("private object store destination must not exist")
	}
	if err := os.Mkdir(store, 0700); err != nil {
		return err
	}
	if err := syncParent(store); err != nil {
		return err
	}
	args := []string{"init", "--quiet", "--object-format=" + format, "--template="}
	if bare {
		args = append(args, "--bare", store)
	} else {
		args = append(args, filepath.Dir(store))
	}
	if _, err := privateCommandOutput(ctx, filepath.Dir(store), nil, nil, args...); err != nil {
		return err
	}
	emptyDigest := sha256.Sum256(nil)
	marker := privateStoreMarker{Version: 1, ObjectFormat: format, Bare: bare, IgnoreSHA256: hex.EncodeToString(emptyDigest[:])}
	data, _ := json.Marshal(marker)
	if err := writeGuardedPrivateMetadata(ctx, filepath.Join(store, privateStoreMarkerName), append(data, '\n'), true); err != nil {
		return err
	}
	// Highest-priority attributes make private checkout an exact raw-byte copy.
	if err := mkdirAllDurable(filepath.Join(store, "info"), 0700); err != nil {
		return err
	}
	if err := writeGuardedPrivateMetadata(ctx, filepath.Join(store, "info", "attributes"), []byte(privateRawAttributes), true); err != nil {
		return err
	}
	if err := writeGuardedPrivateMetadata(ctx, filepath.Join(store, "info", "exclude"), nil, true); err != nil {
		return err
	}
	return nil
}

type privateMetadataValidationError struct{ original error }

func (e *privateMetadataValidationError) Error() string {
	return "private Git metadata validation failed"
}
func (e *privateMetadataValidationError) Unwrap() error { return e.original }

func validatePrivateStore(ctx context.Context, store string) (marker privateStoreMarker, err error) {
	defer func() {
		if err != nil {
			err = &privateMetadataValidationError{original: err}
		}
	}()
	leased, release, err := leasePrivateStore(ctx, store)
	if err != nil {
		return marker, err
	}
	defer release()
	ctx = leased
	if err := ctx.Err(); err != nil {
		return marker, err
	}
	abs, err := filepath.Abs(store)
	if err != nil || abs != store {
		return marker, errors.New("private object store must be an absolute clean path")
	}
	resolved, err := filepath.EvalSymlinks(store)
	if err != nil || resolved != store {
		return marker, errors.New("private object store may not be redirected through a symlink")
	}
	root, err := openPrivateMetadataRoot(store)
	if err != nil {
		return marker, err
	}
	defer root.Close()
	if _, err := listPrivateMetadata(ctx, root, defaultPrivateMetadataLimits()); err != nil {
		return marker, err
	}
	return validatePrivateRootPolicy(ctx, root, "")
}

func validatePrivateGitConfig(path string, marker privateStoreMarker) error {
	data, err := ReadRegularFile(path, 1<<20)
	if err != nil {
		return err
	}
	return validatePrivateGitConfigData(data, marker)
}

func validatePrivateGitConfigData(data []byte, marker privateStoreMarker) error {
	section := ""
	allowedCore := map[string]bool{"repositoryformatversion": true, "filemode": true, "bare": true, "logallrefupdates": true, "ignorecase": true, "precomposeunicode": true, "symlinks": true}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") {
				return errors.New("invalid private Git config section")
			}
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			if section != "core" && section != "extensions" && section != "user" {
				return errors.New("private Git config contains an unsupported section")
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)
		if !ok {
			return errors.New("invalid private Git config entry")
		}
		switch section {
		case "core":
			if !allowedCore[key] {
				return errors.New("private Git config contains an unsafe core setting")
			}
			if key == "bare" && (value == "true") != marker.Bare {
				return errors.New("private Git repository type changed")
			}
		case "extensions":
			if key != "objectformat" || value != marker.ObjectFormat {
				return errors.New("private Git object format changed")
			}
		case "user":
			if key != "name" && key != "email" {
				return errors.New("private Git config contains an unsupported user setting")
			}
		default:
			return errors.New("private Git config entry has no allowed section")
		}
	}
	return nil
}

func snapshotGitText(ctx context.Context, repo string, private bool, args ...string) (string, error) {
	b, err := snapshotGitOutput(ctx, repo, private, nil, args...)
	return strings.TrimSuffix(string(b), "\n"), err
}
func snapshotGitOutput(ctx context.Context, repo string, private bool, input []byte, args ...string) ([]byte, error) {
	if !private && input == nil {
		return gitOutput(ctx, repo, nil, args...)
	}
	if private {
		return privateCommandOutput(ctx, repo, privateGitArgs(repo), input, args...)
	}
	return privateCommandOutput(ctx, repo, nil, input, args...)
}
func privateGitArgs(store string) []string {
	args := []string{"--git-dir=" + store}
	if filepath.Base(store) == ".git" {
		args = append(args, "--work-tree="+filepath.Dir(store))
	}
	return args
}

func privateGitCommand(ctx context.Context, repo string, prefix []string, args ...string) (*privateGitProcess, context.CancelFunc, error) {
	if len(prefix) == 0 && len(args) > 0 && args[0] != "init" {
		if err := rejectPrivatePromisorSource(ctx, repo); err != nil {
			return nil, func() {}, err
		}
	}
	child, cancel := context.WithTimeout(ctx, 45*time.Second)
	var quarantine *privateGitQuarantine
	if privateMutation(args) && (len(prefix) > 0 || args[0] == "init") {
		store, worktree := repo, ""
		initial := args[0] == "init"
		if initial {
			store = args[len(args)-1]
			if !slices.Contains(args, "--bare") {
				worktree, store = store, filepath.Join(store, ".git")
			}
		} else if filepath.Base(store) == ".git" {
			worktree = filepath.Dir(store)
		}
		var err error
		quarantine, child, err = preparePrivateGitQuarantine(child, store, initial)
		if err != nil {
			cancel()
			return nil, func() {}, err
		}
		quarantine.initialWorktree = initial && worktree != ""
		if initial {
			quarantine.initialMarker = privateStoreMarker{Bare: worktree == ""}
			for _, arg := range args {
				if strings.HasPrefix(arg, "--object-format=") {
					quarantine.initialMarker.ObjectFormat = strings.TrimPrefix(arg, "--object-format=")
				}
			}
		}
		repo, prefix = quarantine.git, []string{"--git-dir=" + quarantine.git}
		if worktree != "" {
			prefix = append(prefix, "--work-tree="+worktree)
		}
		if initial && worktree == "" {
			args = append([]string(nil), args...)
			args[len(args)-1] = quarantine.git
		}
		child = context.WithValue(child, privateGitQuarantineKey{}, privateGitQuarantineCapability{git: quarantine.git, worktree: worktree})
	}
	// Private metadata remains writable by its worker, but cannot redefine
	// controller path selection or raw checkout modes/types. In particular,
	// ignorecase=true can hide an untracked FILE.TXT beside tracked file.txt
	// even on a case-sensitive filesystem, defeating exact-content checks.
	argv := []string{"--no-replace-objects", "-C", repo, "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "core.ignorecase=false", "-c", "core.precomposeunicode=false", "-c", "core.filemode=true", "-c", "core.symlinks=true", "-c", "core.autocrlf=false", "-c", "core.safecrlf=false", "-c", "core.excludesFile=" + os.DevNull, "-c", "commit.gpgsign=false", "-c", "user.name=Agentmux", "-c", "user.email=agentmux@localhost", "-c", "gc.auto=0", "-c", "maintenance.auto=false"}
	argv = append(argv, prefix...)
	argv = append(argv, args...)
	cmd := exec.CommandContext(child, "git", argv...)
	configureGitProcess(cmd)
	cmd.Env = cleanGitEnvironment()
	cmd.Env = append(cmd.Env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	cmd.WaitDelay = 2 * time.Second
	if err := wrapGitProcess(child, cmd); err != nil {
		cancel()
		if quarantine != nil {
			quarantine.close()
		}
		return nil, func() {}, err
	}
	cleanup := func() {
		cancel()
		if quarantine != nil {
			quarantine.close()
		}
	}
	return &privateGitProcess{Cmd: cmd, quarantine: quarantine}, cleanup, nil
}
func privateCommandOutput(ctx context.Context, repo string, prefix []string, input []byte, args ...string) ([]byte, error) {
	cmd, cancel, err := privateGitCommand(ctx, repo, prefix, args...)
	if err != nil {
		return nil, err
	}
	defer cancel()
	cmd.Stdin = bytes.NewReader(input)
	var out, diagnostic limitedOutput
	cmd.Stdout = &out
	cmd.Stderr = &diagnostic
	err = cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("private git %s: %w: %s", args[0], err, diagnostic.String())
	}
	if out.exceeded || diagnostic.exceeded {
		return nil, errors.New("private Git output exceeds size limit")
	}
	return out.Bytes(), nil
}

// importPrivateBase imports precisely the base commit, its trees and blobs,
// without copying history, alternates, hardlinks, refs or configuration.
func importPrivateBase(ctx context.Context, source, store, commit string) error {
	if !objectID(commit) {
		return errors.New("invalid private base identity")
	}
	if err := rejectPrivatePromisorSource(ctx, source); err != nil {
		return err
	}
	if _, err := privateCommandOutput(ctx, store, privateGitArgs(store), nil, "cat-file", "-e", commit+"^{commit}"); err == nil {
		return nil
	}
	root, err := gitText(ctx, source, nil, "rev-parse", commit+"^{tree}")
	if err != nil {
		return err
	}
	listing, err := gitOutput(ctx, source, nil, "ls-tree", "-r", "-t", "-z", commit, "--")
	if err != nil {
		return err
	}
	objects := map[string]bool{commit: true, root: true}
	blobSet := map[string]int64{}
	files := 0
	for _, record := range bytes.Split(listing, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		meta, _, ok := bytes.Cut(record, []byte{'\t'})
		fields := strings.Fields(string(meta))
		if !ok || len(fields) != 3 || !objectID(fields[2]) {
			return errors.New("malformed private base tree")
		}
		if fields[0] == "160000" || fields[1] != "tree" && fields[1] != "blob" {
			return errors.New("private snapshots do not support gitlinks")
		}
		objects[fields[2]] = true
		if fields[1] == "blob" {
			files++
			blobSet[fields[2]]++
		}
		if files > MaxSnapshotFiles {
			return errors.New("private base tree exceeds selected file limit")
		}
	}
	ids := make([]string, 0, len(objects))
	for id := range objects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	checks, err := privateCommandOutput(ctx, source, nil, []byte(strings.Join(ids, "\n")+"\n"), "cat-file", "--batch-check=%(objectname) %(objecttype) %(objectsize)")
	if err != nil {
		return err
	}
	var total int64
	for _, line := range strings.Split(strings.TrimSpace(string(checks)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return errors.New("private base object is unavailable")
		}
		size, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil || size < 0 {
			return errors.New("invalid private base object size")
		}
		if count := blobSet[fields[0]]; count > 0 {
			if size > (MaxSnapshotBytes-total)/count {
				return errors.New("private base contents exceed 512 MiB")
			}
			total += size * count
		}
	}
	if err := copyPrivateObjects(ctx, source, store, ids); err != nil {
		return err
	}
	shallow := []byte(commit + "\n")
	if old, err := ReadRegularFile(filepath.Join(store, "shallow"), 1<<20); err == nil {
		if !bytes.Contains(append([]byte{'\n'}, old...), []byte("\n"+commit+"\n")) {
			shallow = append(old, shallow...)
		} else {
			shallow = old
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := writeGuardedPrivateMetadata(ctx, filepath.Join(store, "shallow"), shallow, false); err != nil {
		return err
	}
	_, err = privateCommandOutput(ctx, store, privateGitArgs(store), nil, "update-ref", "refs/agentmux/bases/"+commit, commit)
	return err
}

func copyPrivateObjects(ctx context.Context, source, target string, ids []string) error {
	if err := inspectIsolationGitObjects(ctx, source, nil, ids, false); err != nil {
		return err
	}
	pack, packCancel, err := privateGitCommand(ctx, source, nil, "pack-objects", "--stdout", "--threads=1")
	if err != nil {
		return err
	}
	defer packCancel()
	unpack, unpackCancel, err := privateGitCommand(ctx, target, privateGitArgs(target), "index-pack", "--stdin")
	if err != nil {
		return err
	}
	defer unpackCancel()
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer reader.Close()
	defer writer.Close()
	pack.Stdin = strings.NewReader(strings.Join(ids, "\n") + "\n")
	pack.Stdout = writer
	var packDiagnostic, unpackDiagnostic, unpackOutput limitedOutput
	pack.Stderr = &packDiagnostic
	unpack.Stdin = reader
	unpack.Stdout = &unpackOutput
	unpack.Stderr = &unpackDiagnostic
	if err = unpack.Start(); err != nil {
		return err
	}
	// Children own the pipe endpoints. Keeping a parent read descriptor open
	// could strand the pack writer if index-pack exits before consuming input.
	_ = reader.Close()
	if err = pack.Start(); err != nil {
		unpackCancel()
		_ = unpack.Wait()
		return err
	}
	_ = writer.Close()
	packErr := pack.Wait()
	unpackErr := unpack.Wait()
	if packErr != nil || unpackErr != nil {
		return fmt.Errorf("copy independent private objects: %w: %s %s", errors.Join(packErr, unpackErr), packDiagnostic.String(), unpackDiagnostic.String())
	}
	if packDiagnostic.exceeded || unpackDiagnostic.exceeded || unpackOutput.exceeded {
		return errors.New("private object transfer exceeded diagnostic bound")
	}
	return nil
}

func privateContentState(ctx context.Context, repo string, storage snapshotStorage, selection *SnapshotSelection) (string, string, error) {
	if err := rejectPrivatePromisorSource(ctx, repo); err != nil {
		return "", "", err
	}
	leased, release, err := leasePrivateStore(ctx, storage.Store)
	if err != nil {
		return "", "", err
	}
	defer release()
	ctx = leased
	if marker, err := validatePrivateStore(ctx, storage.Store); err != nil {
		return "", "", err
	} else if storage.IgnoreSHA != "" && marker.IgnoreSHA256 != storage.IgnoreSHA {
		return "", "", errors.New("private fingerprint ignore policy differs from its pinned identity")
	}
	paths, err := privateSelectedPaths(ctx, repo)
	if err != nil {
		return "", "", err
	}
	root, err := os.OpenRoot(repo)
	if err != nil {
		return "", "", err
	}
	defer root.Close()
	// Admit the entire selection before Git can create a blob. Repeat exact
	// bounds while streaming because unrelated writers do not share this lock.
	if _, err := measurePrivateSelectedPaths(ctx, root, paths); err != nil {
		return "", "", err
	}
	ref := "refs/agentmux/fingerprints/" + randomRunID()
	cmd, cancel, err := privateGitCommand(ctx, storage.Store, privateGitArgs(storage.Store), "fast-import", "--quiet")
	if err != nil {
		return "", "", err
	}
	defer cancel()
	var diagnostic, output limitedOutput
	cmd.Stderr = &diagnostic
	cmd.Stdout = &output
	input, err := cmd.StdinPipe()
	if err != nil {
		return "", "", err
	}
	if err = cmd.Start(); err != nil {
		input.Close()
		return "", "", err
	}
	h := sha256.New()
	usage := SnapshotSelection{}
	writeErr := func() error {
		if _, err := fmt.Fprintf(input, "feature done\ncommit %s\ncommitter Agentmux <agentmux@localhost> 0 +0000\ndata 0\n\ndeleteall\n", ref); err != nil {
			return err
		}
		for _, path := range paths {
			if err := writePrivateSelected(ctx, root, path, input, h, &usage); err != nil {
				return err
			}
		}
		_, err := io.WriteString(input, "\ndone\n")
		return err
	}()
	closeErr := input.Close()
	if writeErr != nil || closeErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if err = errors.Join(writeErr, closeErr, waitErr); err != nil {
		return "", "", fmt.Errorf("private raw snapshot: %w: %s", err, diagnostic.String())
	}
	if diagnostic.exceeded || output.exceeded {
		return "", "", errors.New("private snapshot output exceeded its bound")
	}
	tree, err := privateCommandOutput(ctx, storage.Store, privateGitArgs(storage.Store), nil, "rev-parse", "--verify", ref+"^{tree}")
	if err != nil {
		return "", "", err
	}
	if _, err = privateCommandOutput(ctx, storage.Store, privateGitArgs(storage.Store), nil, "update-ref", "-d", ref); err != nil {
		return "", "", err
	}
	if selection != nil {
		*selection = usage
	}
	if err := pinPrivateSelection(ctx, storage, paths); err != nil {
		return "", "", err
	}
	return strings.TrimSpace(string(tree)), hex.EncodeToString(h.Sum(nil)), nil
}

func measurePrivateSelectedPaths(ctx context.Context, root *os.Root, paths []string) (SnapshotSelection, error) {
	usage := SnapshotSelection{}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return usage, err
		}
		info, err := privateSelectedInfo(root, path)
		if err != nil {
			return usage, err
		}
		if info == nil {
			continue
		}
		size := info.Size()
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := privateRootReadlink(root, path)
			if err != nil {
				return usage, err
			}
			size = int64(len(target))
		} else if !info.Mode().IsRegular() {
			return usage, errors.New("private snapshot selection is not a regular file or symlink")
		}
		usage.Files++
		if usage.Files > MaxSnapshotFiles || size > MaxSnapshotBytes-usage.Bytes {
			return usage, errors.New("private snapshot selection exceeds its file/byte bounds")
		}
		usage.Bytes += size
	}
	return usage, nil
}

func privateSelectedPaths(ctx context.Context, repo string) ([]string, error) {
	head, err := gitOutput(ctx, repo, nil, "ls-tree", "-r", "-z", "HEAD", "--")
	if err != nil {
		return nil, err
	}
	// HEAD and the source index always participate, including tracked paths
	// covered by ignore rules. Enumerate new files against the pinned private
	// ignore policy rather than the source's mutable info/exclude or HOME.
	cached, err := gitOutput(ctx, repo, nil, "ls-files", "--stage", "-z")
	if err != nil {
		return nil, err
	}
	storage, err := privateStorageForRepository(ctx, repo)
	if err != nil {
		return nil, err
	}
	head, err = privateTrackedPathNames(head, false)
	if err != nil {
		return nil, err
	}
	cached, err = privateTrackedPathNames(cached, true)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	paths := []string{}
	pathBytes := 0
	add := func(list []byte) error {
		for _, p := range bytes.Split(list, []byte{0}) {
			if len(p) == 0 || seen[string(p)] {
				continue
			}
			path := string(p)
			if strings.HasSuffix(path, "/") {
				return errors.New("private snapshots do not support embedded Git repositories")
			}
			if !validPrivateSelectedPath(path) {
				return errors.New("invalid private snapshot path")
			}
			if len(paths) >= 2*MaxSnapshotFiles || len(path)+1 > maxPatchBytes-pathBytes {
				return errors.New("private snapshot path enumeration exceeded its entry/name-byte bounds")
			}
			seen[path] = true
			paths = append(paths, path)
			pathBytes += len(path) + 1
		}
		return nil
	}
	var others []byte
	if storage == nil {
		// Explicit shared storage retains its legacy selection policy.
		others, err = gitOutput(ctx, repo, nil, "ls-files", "--others", "--exclude-standard", "-z")
	} else {
		for _, identity := range privateSelectionIdentities(*storage) {
			paths, readErr := readPrivateSelection(ctx, *storage, identity)
			if readErr != nil {
				return nil, readErr
			}
			if err := add(paths); err != nil {
				return nil, err
			}
		}
		others, err = privateUntrackedPaths(ctx, repo, *storage)
	}
	if err != nil {
		return nil, err
	}
	for _, list := range [][]byte{head, cached, others} {
		if err := add(list); err != nil {
			return nil, err
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func writePrivateSelected(ctx context.Context, root *os.Root, path string, out io.Writer, h hash.Hash, usage *SnapshotSelection) error {
	guarded, flush, guardErr := isolationCredentialWriter(ctx, path, out)
	if guardErr != nil {
		return guardErr
	}
	out = guarded
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := privateSelectedInfo(root, path)
	if err != nil {
		return err
	}
	if info == nil {
		return nil
	}
	mode, rawMode := uint32(0100644), byte(1)
	var target string
	if info.Mode()&os.ModeSymlink != 0 {
		mode, rawMode = 0120000, 2
		target, err = privateRootReadlink(root, path)
		if err != nil {
			return err
		}
	} else if info.Mode().IsRegular() {
		if info.Mode().Perm()&0111 != 0 {
			mode, rawMode = 0100755, 3
		}
	} else {
		return errors.New("private snapshot selection is not a regular file or symlink")
	}
	size := info.Size()
	if rawMode == 2 {
		size = int64(len(target))
	}
	usage.Files++
	if usage.Files > MaxSnapshotFiles || size > MaxSnapshotBytes-usage.Bytes {
		return errors.New("private snapshot selection exceeds its file/byte bounds")
	}
	usage.Bytes += size
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(path)))
	h.Write(length[:])
	h.Write([]byte(path))
	h.Write([]byte{rawMode})
	binary.BigEndian.PutUint64(length[:], uint64(size))
	h.Write(length[:])
	if _, err := fmt.Fprintf(out, "M %o inline %s\ndata %d\n", mode, quoteGitPath(path), size); err != nil {
		return err
	}
	if rawMode == 2 {
		if _, err = io.WriteString(io.MultiWriter(out, h), target); err != nil {
			return err
		}
		after, e := root.Lstat(path)
		again, e2 := privateRootReadlink(root, path)
		if e != nil || e2 != nil || !os.SameFile(info, after) || again != target {
			return errors.New("selected symlink changed during private fingerprint")
		}
	} else {
		f, err := privateRootOpenRegular(root, path, info)
		if err != nil {
			return err
		}
		var read int64
		buf := make([]byte, 64<<10)
		for {
			if err = ctx.Err(); err != nil {
				break
			}
			n, e := f.Read(buf)
			if n > 0 {
				read += int64(n)
				if read > size {
					err = errors.New("selected file grew during private fingerprint")
					break
				}
				if _, err = io.MultiWriter(out, h).Write(buf[:n]); err != nil {
					break
				}
			}
			if e != nil {
				if e != io.EOF {
					err = e
				}
				break
			}
		}
		end, statErr := f.Stat()
		closeErr := f.Close()
		after, pathErr := root.Lstat(path)
		if err != nil {
			return err
		}
		if statErr != nil || closeErr != nil || pathErr != nil {
			return errors.Join(statErr, closeErr, pathErr)
		}
		if read != size || end.Size() != size || !end.ModTime().Equal(info.ModTime()) || !os.SameFile(info, after) || after.Mode() != info.Mode() || after.Size() != size || !after.ModTime().Equal(info.ModTime()) {
			return errors.New("selected file changed during private fingerprint")
		}
	}
	if err = privateSelectedParents(root, path); err != nil {
		return err
	}
	if _, err = io.WriteString(out, "\n"); err != nil {
		return err
	}
	return flush()
}

func privateSelectedParents(root *os.Root, path string) error {
	parent := filepath.Dir(path)
	if parent == "." {
		return nil
	}
	parts := strings.Split(filepath.ToSlash(parent), "/")
	current := ""
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("private snapshot selection traverses a symlink or non-directory ancestor")
		}
	}
	return nil
}

// The selection is a union of HEAD, index and current untracked paths. An old
// tracked leaf may therefore now be a directory, or have an absent or regular
// file ancestor after a deletion or directory-to-file replacement. Such leaves
// have no current bytes. Symlink ancestors remain an error: traversing one
// could select data outside the project or alias another selected path.
func privateSelectedInfo(root *os.Root, path string) (os.FileInfo, error) {
	info, err := privateCurrentPathInfo(root, path)
	if err == nil && info != nil && info.IsDir() {
		return nil, nil
	}
	return info, err
}

func privateCurrentPathInfo(root *os.Root, path string) (os.FileInfo, error) {
	parent := filepath.Dir(path)
	if parent != "." {
		current := ""
		for _, part := range strings.Split(filepath.ToSlash(parent), "/") {
			current = filepath.Join(current, part)
			info, err := root.Lstat(current)
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil
			}
			if err != nil {
				return nil, err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return nil, errors.New("private snapshot selection traverses a symlink ancestor")
			}
			if info.Mode().IsRegular() {
				return nil, nil
			}
			if !info.IsDir() {
				return nil, errors.New("private snapshot selection traverses a non-directory ancestor")
			}
		}
	}
	info, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return info, nil
}

func quoteGitPath(path string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, v := range []byte(path) {
		switch v {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteByte(v)
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			if v < 32 || v >= 127 {
				fmt.Fprintf(&b, "\\%03o", v)
			} else {
				b.WriteByte(v)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func capturePrivate(ctx context.Context, repository, destination string, storage snapshotStorage) (Snapshot, error) {
	var s Snapshot
	leased, release, leaseErr := leasePrivateStore(ctx, storage.Store)
	if leaseErr != nil {
		return s, leaseErr
	}
	defer release()
	ctx = leased
	repo, err := repositoryRoot(ctx, repository)
	if err != nil {
		return s, err
	}
	dest, err := newOutsidePath(repo, destination)
	if err != nil {
		return s, err
	}
	if err = os.Mkdir(dest, 0700); err != nil {
		return s, err
	}
	if err = syncParent(dest); err != nil {
		return s, err
	}
	base, err := gitText(ctx, repo, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return s, err
	}
	if err = importPrivateBase(ctx, repo, storage.Store, base); err != nil {
		return s, err
	}
	var selection SnapshotSelection
	tree, raw, err := privateContentState(ctx, repo, storage, &selection)
	if err != nil {
		return s, err
	}
	env := []string{"GIT_AUTHOR_NAME=Agentmux", "GIT_AUTHOR_EMAIL=agentmux@localhost", "GIT_COMMITTER_NAME=Agentmux", "GIT_COMMITTER_EMAIL=agentmux@localhost"}
	cmd, cancel, err := privateGitCommand(ctx, storage.Store, privateGitArgs(storage.Store), "commit-tree", tree, "-p", base, "-m", "Agentmux private workflow snapshot")
	if err != nil {
		return s, err
	}
	defer cancel()
	cmd.Env = append(cmd.Env, env...)
	var commitOutput, diagnostic limitedOutput
	cmd.Stdout = &commitOutput
	cmd.Stderr = &diagnostic
	if err = cmd.Run(); err != nil {
		return s, fmt.Errorf("private snapshot commit: %w: %s", err, diagnostic.String())
	}
	commit := strings.TrimSpace(commitOutput.String())
	patch, err := privateCommandOutput(ctx, storage.Store, privateGitArgs(storage.Store), nil, "diff", "--binary", "--full-index", "--no-ext-diff", "--no-textconv", base, commit, "--")
	if err != nil {
		return s, err
	}
	again, againRaw, err := privateContentState(ctx, repo, storage, nil)
	if err != nil {
		return s, err
	}
	head, headErr := gitText(ctx, repo, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if headErr != nil {
		return s, headErr
	}
	if again != tree || againRaw != raw || head != base {
		return s, errors.New("repository changed during private capture; request a new snapshot")
	}
	if _, err = privateCommandOutput(ctx, storage.Store, privateGitArgs(storage.Store), nil, "update-ref", "refs/agentmux/snapshots/"+commit, commit); err != nil {
		return s, err
	}
	if err = syncPrivateStore(ctx, storage.Store); err != nil {
		return s, err
	}
	digest := sha256.Sum256(patch)
	s = Snapshot{Repository: repo, Store: storage.Store, IgnoreSHA256: storage.IgnoreSHA, SelectionSHA256: currentPrivateSelection(storage), BaseCommit: base, Commit: commit, Tree: tree, CreatedAt: timestamp(), PatchSHA256: hex.EncodeToString(digest[:]), ContentSHA256: raw, SelectedFiles: selection.Files, SelectedBytes: selection.Bytes}
	if err = writeGuardedPrivateMetadata(ctx, filepath.Join(dest, "changes.patch"), patch, true); err != nil {
		return s, err
	}
	data, _ := json.MarshalIndent(s, "", "  ")
	if err = writeGuardedPrivateMetadata(ctx, filepath.Join(dest, "snapshot.json"), append(data, '\n'), true); err != nil {
		return s, err
	}
	return s, nil
}

func createPrivateWorktree(ctx context.Context, s Snapshot, path string) error {
	leased, release, leaseErr := leasePrivateStore(ctx, s.Store)
	if leaseErr != nil {
		return leaseErr
	}
	defer release()
	ctx = leased
	marker, err := validatePrivateStore(ctx, s.Store)
	if err != nil {
		return err
	}
	if err = validateSnapshot(ctx, s.Store, s); err != nil {
		return err
	}
	// The source snapshot, rather than mutable fork metadata, supplies the
	// expected ignore identity for every subsequent Git mutation.
	ctx, err = SnapshotContext(ctx, s)
	if err != nil {
		return err
	}
	dest, err := newOutsidePath(s.Repository, path)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(dest); !errors.Is(err, os.ErrNotExist) {
		return errors.New("private worktree destination must not exist")
	}
	if err = os.Mkdir(dest, 0700); err != nil {
		return err
	}
	if err = syncParent(dest); err != nil {
		return err
	}
	store := filepath.Join(dest, ".git")
	if err = initPrivateGit(ctx, store, marker.ObjectFormat, false); err != nil {
		return err
	}
	ignore, err := ReadRegularFile(filepath.Join(s.Store, "info", "exclude"), 2<<20)
	if err != nil {
		return err
	}
	if _, err = setPrivateIgnorePolicy(ctx, store, ignore); err != nil {
		return err
	}
	childStorage := snapshotStorage{Policy: StoragePrivate, Store: store, Source: dest, IgnoreSHA: marker.IgnoreSHA256, Selections: snapshotStorageFromContext(ctx).Selections}
	if err := copyPrivateSelection(ctx, *snapshotStorageFromContext(ctx), childStorage, s.SelectionSHA256); err != nil {
		return err
	}
	objects, err := privateCommandOutput(ctx, s.Store, privateGitArgs(s.Store), nil, "rev-list", "--objects", "--no-object-names", s.Commit)
	if err != nil {
		return err
	}
	ids := strings.Fields(string(objects))
	if len(ids) > 4*MaxSnapshotFiles+1024 {
		return errors.New("private starting object graph exceeds its bound")
	}
	for _, id := range ids {
		if !objectID(id) {
			return errors.New("invalid private starting object")
		}
	}
	if err = copyPrivateObjects(ctx, s.Store, store, ids); err != nil {
		return err
	}
	if shallow, readErr := ReadRegularFile(filepath.Join(s.Store, "shallow"), 1<<20); readErr == nil {
		if err = writeGuardedPrivateMetadata(ctx, filepath.Join(store, "shallow"), shallow, true); err != nil {
			return err
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	if _, err = privateCommandOutput(ctx, store, privateGitArgs(store), nil, "update-ref", "--no-deref", "HEAD", s.Commit); err != nil {
		return err
	}
	if _, err = privateCommandOutput(ctx, store, privateGitArgs(store), nil, "read-tree", "--reset", "-u", s.Commit); err != nil {
		return err
	}
	child := context.WithValue(ctx, snapshotStorageContextKey{}, &childStorage)
	tree, raw, err := privateContentState(child, dest, childStorage, nil)
	if err != nil {
		return err
	}
	if tree != s.Tree || raw != s.ContentSHA256 {
		return errors.New("private checkout did not reproduce exact snapshot bytes and modes")
	}
	if err := syncPrivateStore(child, store); err != nil {
		return err
	}
	return savePrivateWorktreeSelection(child, childStorage, s.SelectionSHA256)
}

// Publish no snapshot record before its private object graph and metadata have
// requested persistence. fsync still relies on the filesystem/hardware contract.
func syncPrivateStore(ctx context.Context, store string) error {
	leased, release, err := leasePrivateStore(ctx, store)
	if err != nil {
		return err
	}
	defer release()
	ctx = leased
	if _, err = validatePrivateStore(ctx, store); err != nil {
		return err
	}
	root, err := openPrivateMetadataRoot(store)
	if err != nil {
		return err
	}
	defer root.Close()
	entries, err := listPrivateMetadata(ctx, root, defaultPrivateMetadataLimits())
	if err != nil {
		return err
	}
	dirs := []privateMetadataEntry{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.directory {
			dirs = append(dirs, entry)
			continue
		}
		if err := privateSelectedParents(root, entry.path); err != nil {
			return err
		}
		f, err := privateRootOpenRegular(root, entry.path, entry.info)
		if err != nil {
			return err
		}
		if err := errors.Join(f.Sync(), f.Close()); err != nil {
			return err
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i].path) > len(dirs[j].path) })
	for _, entry := range dirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := privateSelectedParents(root, entry.path); err != nil {
			return err
		}
		f, err := privateRootOpenDirectory(root, entry.path, entry.info)
		if err != nil {
			return err
		}
		if err := errors.Join(f.Sync(), f.Close()); err != nil {
			return err
		}
	}
	return syncParent(store)
}

func replacePrivateFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".agentmux-private-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err = errors.Join(err, closeErr); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	return syncParent(path)
}

func setPrivateIgnorePolicy(ctx context.Context, store string, patterns []byte) (string, error) {
	if len(patterns) > 2<<20 {
		return "", errors.New("private ignore policy exceeds 2 MiB")
	}
	if containsIsolationCredential(ctx, patterns) {
		return "", ErrIsolationCredentialLeak
	}
	var marker privateStoreMarker
	if err := DecodeFile(filepath.Join(store, privateStoreMarkerName), &marker); err != nil {
		return "", err
	}
	digest := sha256.Sum256(patterns)
	value := hex.EncodeToString(digest[:])
	marker.IgnoreSHA256 = value
	data, err := json.Marshal(marker)
	if err != nil {
		return "", err
	}
	if containsIsolationCredential(ctx, data) {
		return "", ErrIsolationCredentialLeak
	}
	if err := writeGuardedPrivateMetadata(ctx, filepath.Join(store, "info", "exclude"), patterns, false); err != nil {
		return "", err
	}
	if err = writeGuardedPrivateMetadata(ctx, filepath.Join(store, privateStoreMarkerName), append(data, '\n'), false); err != nil {
		return "", err
	}
	return value, nil
}

func writeGuardedPrivateMetadata(ctx context.Context, path string, data []byte, exclusive bool) error {
	if containsIsolationCredential(ctx, []byte(path)) || containsIsolationCredential(ctx, data) {
		return ErrIsolationCredentialLeak
	}
	if exclusive {
		return writeNewPrivate(path, data)
	}
	return replacePrivateFile(path, data)
}

// Pin the owner's effective global and repository-local ignore rules before
// native work starts. Project .gitignore files retain their normal higher
// priority. Private forks inherit this data, not the owner's home/config files.
func sourceIgnorePolicy(ctx context.Context, repo string) ([]byte, error) {
	if root, matched, err := privateRepositoryRoot(ctx, repo); err != nil {
		return nil, err
	} else if matched {
		return ReadRegularFile(filepath.Join(root, ".git", "info", "exclude"), 2<<20)
	}
	global, err := gitText(ctx, repo, nil, "config", "--path", "--get", "core.excludesfile")
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			return nil, err
		}
		global = ""
	}
	if global == "" {
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			home, e := os.UserHomeDir()
			if e != nil {
				return nil, e
			}
			base = filepath.Join(home, ".config")
		}
		global = filepath.Join(base, "git", "ignore")
	}
	if !filepath.IsAbs(global) {
		global = filepath.Join(repo, global)
	}
	local, err := gitText(ctx, repo, nil, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	if err != nil {
		return nil, err
	}
	parts := [][]byte{}
	for _, path := range []string{global, local} {
		resolved, e := filepath.EvalSymlinks(path)
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return nil, e
		}
		data, e := ReadRegularFile(resolved, 1<<20)
		if e != nil {
			return nil, e
		}
		data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
		if len(data) == 0 {
			continue
		}
		parts = append(parts, data)
	}
	var result []byte
	for _, data := range parts {
		result = append(result, data...)
		if len(result) > 0 && result[len(result)-1] != '\n' {
			result = append(result, '\n')
		}
	}
	if len(result) > 2<<20 {
		return nil, errors.New("combined source ignore policy exceeds 2 MiB")
	}
	return result, nil
}
