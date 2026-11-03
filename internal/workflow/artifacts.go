// Package workflow implements owner-initiated, revision-bound work on Git projects.
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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const maxPatchBytes = 32 << 20

// Git's object writes can run concurrently, but worktree name allocation and
// snapshot ref updates share metadata. Waits honor the caller's cancellation.
var metadataPermit = make(chan struct{}, 1)

func mutateMetadata(ctx context.Context, action func() error) error {
	select {
	case metadataPermit <- struct{}{}:
		defer func() { <-metadataPermit }()
	case <-ctx.Done():
		return ctx.Err()
	}
	return action()
}

// Snapshot pins the source's committed and uncommitted, nonignored contents.
// It contains no authentication files other than any the owner has put in Git's
// selected project contents. Ignored, untracked files are not copied.
type Snapshot struct {
	Repository      string `json:"repository"`
	Store           string `json:"store,omitempty"`
	IgnoreSHA256    string `json:"ignore_sha256,omitempty"`
	SelectionSHA256 string `json:"selection_sha256,omitempty"`
	BaseCommit      string `json:"base_commit"`
	Commit          string `json:"commit"`
	Tree            string `json:"tree"`
	CreatedAt       string `json:"created_at"`
	PatchSHA256     string `json:"patch_sha256"`
	ContentSHA256   string `json:"content_sha256"`
	SelectedFiles   int    `json:"selected_files,omitempty"`
	SelectedBytes   int64  `json:"selected_bytes,omitempty"`
}

// Capture makes a new private destination containing snapshot.json and
// changes.patch. It leaves the source working directory and index untouched.
// A dedicated refs/agentmux/snapshots reference retains the snapshot against Git
// garbage collection. No source branch is moved.
func Capture(ctx context.Context, repository, destination string) (Snapshot, error) {
	if private, err := privateStorageForRepository(ctx, repository); err != nil {
		return Snapshot{}, err
	} else if private != nil {
		return capturePrivate(ctx, repository, destination, *private)
	}
	var s Snapshot
	repo, err := repositoryRoot(ctx, repository)
	if err != nil {
		return s, err
	}
	dest, err := newOutsidePath(repo, destination)
	if err != nil {
		return s, err
	}
	if err = os.Mkdir(dest, 0700); err != nil {
		return s, fmt.Errorf("new snapshot directory: %w", err)
	}
	if err = syncParent(dest); err != nil {
		return s, err
	}
	base, err := gitText(ctx, repo, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return s, err
	}
	var selection SnapshotSelection
	tree, content, err := contentStateSelection(ctx, repo, &selection)
	if err != nil {
		return s, err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	env := []string{"GIT_AUTHOR_NAME=Agentmux", "GIT_AUTHOR_EMAIL=agentmux@localhost", "GIT_COMMITTER_NAME=Agentmux", "GIT_COMMITTER_EMAIL=agentmux@localhost"}
	commit, err := gitText(ctx, repo, env, "-c", "commit.gpgsign=false", "commit-tree", tree, "-p", base, "-m", "Agentmux workflow snapshot")
	if err != nil {
		return s, err
	}
	patch, err := gitOutput(ctx, repo, nil, "diff", "--binary", "--full-index", "--no-ext-diff", "--no-textconv", base, commit, "--")
	if err != nil {
		return s, err
	}
	again, againContent, err := contentState(ctx, repo)
	if err != nil {
		return s, err
	}
	if again != tree || againContent != content {
		return s, errors.New("repository changed during capture; request a new snapshot")
	}
	currentBase, err := gitText(ctx, repo, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return s, err
	}
	if currentBase != base {
		return s, errors.New("repository HEAD changed during capture; request a new snapshot")
	}
	if err = mutateMetadata(ctx, func() error {
		_, e := gitOutput(ctx, repo, nil, "update-ref", "refs/agentmux/snapshots/"+commit, commit)
		return e
	}); err != nil {
		return s, err
	}
	digest := sha256.Sum256(patch)
	s = Snapshot{Repository: repo, BaseCommit: base, Commit: commit, Tree: tree, CreatedAt: now, PatchSHA256: hex.EncodeToString(digest[:]), ContentSHA256: content, SelectedFiles: selection.Files, SelectedBytes: selection.Bytes}
	if err = writeNewPrivate(filepath.Join(dest, "changes.patch"), patch); err != nil {
		return s, err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return s, err
	}
	if err = writeNewPrivate(filepath.Join(dest, "snapshot.json"), append(data, '\n')); err != nil {
		return s, err
	}
	return s, nil
}

// CreateWorktree checks out the pinned contents in a detached worktree. It
// never checks out or resets the source directory, and never invokes Git hooks.
func CreateWorktree(ctx context.Context, s Snapshot, path string) error {
	if s.Store != "" {
		return createPrivateWorktree(ctx, s, path)
	}
	repo, err := repositoryRoot(ctx, s.Repository)
	if err != nil {
		return err
	}
	if err = validateSnapshot(ctx, repo, s); err != nil {
		return err
	}
	dest, err := newOutsidePath(repo, path)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(dest); !errors.Is(err, os.ErrNotExist) {
		return errors.New("worktree destination must not exist")
	}
	// Precreate privately. Git accepts an existing empty directory.
	if err = os.Mkdir(dest, 0700); err != nil {
		return err
	}
	if err = syncParent(dest); err != nil {
		return err
	}
	err = mutateMetadata(ctx, func() error {
		_, e := gitOutput(ctx, repo, nil, "worktree", "add", "--detach", "--", dest, s.Commit)
		return e
	})
	if err != nil {
		return err
	}
	_, content, err := contentState(ctx, dest)
	if err != nil {
		return err
	}
	if content != s.ContentSHA256 {
		return errors.New("Git checkout changed selected file bytes or modes; filters or checkout conversions cannot reproduce this snapshot exactly")
	}
	return nil
}

// ExportPatch writes the candidate's complete current changes relative to its
// starting snapshot, including new nonignored files. Existing outputs are refused.
func ExportPatch(ctx context.Context, s Snapshot, worktree, output string) error {
	repo, err := repositoryRoot(ctx, worktree)
	if err != nil {
		return err
	}
	if err = validateSnapshot(ctx, repo, s); err != nil {
		return err
	}
	path, err := newOutsidePath(repo, output)
	if err != nil {
		return err
	}
	tree, err := contentTree(ctx, repo)
	if err != nil {
		return err
	}
	var patch []byte
	if private, privateErr := privateStorageForRepository(ctx, repo); privateErr != nil {
		return privateErr
	} else if private != nil {
		patch, err = privateCommandOutput(ctx, private.Store, privateGitArgs(private.Store), nil, "diff", "--binary", "--full-index", "--no-ext-diff", "--no-textconv", s.Tree, tree, "--")
	} else {
		patch, err = gitOutput(ctx, repo, nil, "diff", "--binary", "--full-index", "--no-ext-diff", "--no-textconv", s.Commit, tree, "--")
	}
	if err != nil {
		return err
	}
	again, err := contentTree(ctx, repo)
	if err != nil {
		return err
	}
	if again != tree {
		return errors.New("candidate changed during patch export")
	}
	return writeNewPrivate(path, patch)
}

func validateSnapshot(ctx context.Context, repo string, s Snapshot) error {
	if !objectID(s.Commit) || !objectID(s.Tree) || !objectID(s.BaseCommit) {
		return errors.New("invalid snapshot object identity")
	}
	if len(s.ContentSHA256) != 64 {
		return errors.New("snapshot lacks an exact-content digest; capture a new snapshot")
	}
	if s.Store != "" {
		if marker, err := validatePrivateStore(ctx, s.Store); err != nil {
			return err
		} else if !digestID(s.IgnoreSHA256) || marker.IgnoreSHA256 != s.IgnoreSHA256 {
			return errors.New("private snapshot ignore policy differs from recorded evidence")
		}
		if _, err := readPrivateSelection(ctx, snapshotStorage{Store: s.Store}, s.SelectionSHA256); err != nil {
			return err
		}
		repo = s.Store
	}
	tree, err := snapshotGitText(ctx, repo, s.Store != "", "rev-parse", "--verify", s.Commit+"^{tree}")
	if err != nil {
		return err
	}
	if tree != s.Tree {
		return errors.New("snapshot commit does not match its recorded tree")
	}
	return nil
}

func objectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func repositoryRoot(ctx context.Context, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("repository is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	if root, matched, err := privateRepositoryRoot(ctx, abs); matched || err != nil {
		return root, err
	}
	if storage := snapshotStorageFromContext(ctx); storage != nil && storage.Policy == StoragePrivate && abs != storage.Source && !pathWithin(storage.Source, abs) {
		return "", errors.New("private worktree metadata is missing or redirected")
	}
	root, err := gitText(ctx, abs, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err == nil {
		if storage := snapshotStorageFromContext(ctx); storage != nil && storage.Policy == StoragePrivate && root != storage.Source {
			return "", errors.New("private snapshot source repository identity changed")
		}
	}
	return root, err
}

func newOutsidePath(repo, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("output path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", fmt.Errorf("output parent must already exist: %w", err)
	}
	abs = filepath.Join(parent, filepath.Base(abs))
	rel, err := filepath.Rel(repo, abs)
	if err != nil {
		return "", err
	}
	if rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("workflow output must be outside the repository")
	}
	return abs, nil
}

func contentTree(ctx context.Context, repo string) (string, error) {
	tree, _, err := contentState(ctx, repo)
	return tree, err
}

// contentState retains Git tree identity for patches and separately hashes raw
// selected file bytes. Git clean/EOL filters must not conceal changed evidence.
func contentState(ctx context.Context, repo string) (string, string, error) {
	return contentStateSelection(ctx, repo, nil)
}

func contentStateSelection(ctx context.Context, repo string, selection *SnapshotSelection) (string, string, error) {
	if private, err := privateStorageForRepository(ctx, repo); err != nil {
		return "", "", err
	} else if private != nil {
		return privateContentState(ctx, repo, *private, selection)
	}
	// Admission happens before git add can write new blobs into the source
	// object store. The raw fingerprint below checks the same limits again,
	// because unrelated writers do not participate in this observation.
	if _, err := snapshotUsageRoot(ctx, repo); err != nil {
		return "", "", err
	}
	f, err := os.CreateTemp("", "agentmux-index-*")
	if err != nil {
		return "", "", err
	}
	path := f.Name()
	if err = f.Close(); err != nil {
		return "", "", err
	}
	if err = os.Remove(path); err != nil {
		return "", "", err
	}
	defer os.Remove(path)
	defer os.Remove(path + ".lock")
	env := []string{"GIT_INDEX_FILE=" + path}
	if _, err = gitOutput(ctx, repo, env, "read-tree", "HEAD"); err != nil {
		return "", "", err
	}
	if _, err = gitOutput(ctx, repo, env, "add", "--all", "--", "."); err != nil {
		return "", "", err
	}
	// A newly tracked path may now match .gitignore. Starting from HEAD alone
	// would silently drop it; retain every existing source-index tracked path.
	tracked, err := gitOutput(ctx, repo, nil, "ls-files", "--cached", "-z")
	if err != nil {
		return "", "", err
	}
	var selected bytes.Buffer
	for _, path := range bytes.Split(tracked, []byte{0}) {
		if len(path) == 0 {
			continue
		}
		if _, e := os.Lstat(filepath.Join(repo, string(path))); e == nil {
			selected.Write(path)
			selected.WriteByte(0)
		} else if !errors.Is(e, os.ErrNotExist) {
			return "", "", e
		}
	}
	if selected.Len() > 0 {
		list, err := os.CreateTemp("", "agentmux-paths-*")
		if err != nil {
			return "", "", err
		}
		name := list.Name()
		defer os.Remove(name)
		if _, err = list.Write(selected.Bytes()); err != nil {
			list.Close()
			return "", "", err
		}
		if err = list.Close(); err != nil {
			return "", "", err
		}
		if _, err = gitOutput(ctx, repo, env, "add", "--force", "--pathspec-from-file="+name, "--pathspec-file-nul"); err != nil {
			return "", "", err
		}
	}
	entries, err := gitOutput(ctx, repo, env, "ls-files", "--stage", "-z")
	if err != nil {
		return "", "", err
	}
	for _, entry := range bytes.Split(entries, []byte{0}) {
		if bytes.HasPrefix(entry, []byte("160000 ")) {
			return "", "", errors.New("workflow snapshots do not support submodules or embedded Git repositories; select a repository without gitlinks")
		}
	}
	tree, err := gitText(ctx, repo, env, "write-tree")
	if err != nil {
		return "", "", err
	}
	digest, err := rawContentDigest(ctx, repo, entries, selection)
	return tree, digest, err
}

func rawContentDigest(ctx context.Context, repo string, entries []byte, selection *SnapshotSelection) (string, error) {
	h := sha256.New()
	usage := SnapshotSelection{}
	var length [8]byte
	for _, entry := range bytes.Split(entries, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		_, path, ok := bytes.Cut(entry, []byte{'\t'})
		if !ok {
			return "", errors.New("malformed Git index entry")
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		usage.Files++
		if usage.Files > MaxSnapshotFiles {
			return "", fmt.Errorf("selected snapshot contains more than %d files", MaxSnapshotFiles)
		}
		name := filepath.Join(repo, string(path))
		info, err := os.Lstat(name)
		if err != nil {
			return "", err
		}
		binary.BigEndian.PutUint64(length[:], uint64(len(path)))
		h.Write(length[:])
		h.Write(path)
		mode := byte(0)
		if info.Mode()&os.ModeSymlink != 0 {
			mode = 2
		} else if info.Mode().IsRegular() {
			mode = 1
			if info.Mode().Perm()&0111 != 0 {
				mode = 3
			}
		} else {
			return "", errors.New("selected project content is not a file or symlink")
		}
		h.Write([]byte{mode})
		if mode == 2 {
			target, err := os.Readlink(name)
			if err != nil {
				return "", err
			}
			if int64(len(target)) > MaxSnapshotBytes-usage.Bytes {
				return "", errors.New("selected snapshot contents exceed 512 MiB")
			}
			usage.Bytes += int64(len(target))
			binary.BigEndian.PutUint64(length[:], uint64(len(target)))
			h.Write(length[:])
			h.Write([]byte(target))
			continue
		}
		if info.Size() > MaxSnapshotBytes-usage.Bytes {
			return "", errors.New("selected snapshot contents exceed 512 MiB")
		}
		usage.Bytes += info.Size()
		binary.BigEndian.PutUint64(length[:], uint64(info.Size()))
		h.Write(length[:])
		f, err := openRegularFileMatching(name, info)
		if err != nil {
			return "", err
		}
		buffer := make([]byte, 64<<10)
		read := int64(0)
		for {
			if err = ctx.Err(); err != nil {
				break
			}
			n, e := f.Read(buffer)
			if n > 0 {
				read += int64(n)
				if read > info.Size() {
					err = errors.New("selected file grew during fingerprint")
					break
				}
				h.Write(buffer[:n])
			}
			if e != nil {
				if e != io.EOF {
					err = e
				}
				break
			}
		}
		end, e := f.Stat()
		f.Close()
		if err != nil {
			return "", err
		}
		if e != nil {
			return "", e
		}
		pathEnd, pathErr := os.Lstat(name)
		if pathErr != nil {
			return "", pathErr
		}
		if read != info.Size() || end.Size() != info.Size() || !end.ModTime().Equal(info.ModTime()) || !os.SameFile(info, pathEnd) || pathEnd.Mode() != info.Mode() || pathEnd.Size() != info.Size() || !pathEnd.ModTime().Equal(info.ModTime()) {
			return "", errors.New("selected file changed during fingerprint")
		}
	}
	if selection != nil {
		*selection = usage
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CurrentTree returns the selected committed, modified and nonignored new file
// contents without modifying the working directory or its real index.
func CurrentTree(ctx context.Context, repository string) (string, error) {
	repo, err := repositoryRoot(ctx, repository)
	if err != nil {
		return "", err
	}
	return contentTree(ctx, repo)
}

func gitText(ctx context.Context, repo string, env []string, args ...string) (string, error) {
	b, err := gitOutput(ctx, repo, env, args...)
	// Git terminates records with a newline. Other whitespace may be part of a
	// legitimate repository path, so do not TrimSpace a path result.
	return strings.TrimSuffix(string(b), "\n"), err
}

type limitedOutput struct {
	bytes.Buffer
	exceeded bool
}

func (b *limitedOutput) Write(p []byte) (int, error) {
	n := len(p)
	room := maxPatchBytes - b.Len()
	if len(p) > room {
		b.exceeded = true
		p = p[:room]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}

func gitOutput(ctx context.Context, repo string, env []string, args ...string) ([]byte, error) {
	if SnapshotStoragePolicy(ctx) == StoragePrivate {
		if err := rejectPrivatePromisorSource(ctx, repo); err != nil {
			return nil, err
		}
	}
	return gitOutputCommand(ctx, repo, env, args...)
}

// Effective config reads cannot trigger lazy object retrieval. Refuse promisor
// repositories before object-reading plumbing, including on Git versions predating
// GIT_NO_LAZY_FETCH. No transport or source metadata mutation is permitted.
func rejectPrivatePromisorSource(ctx context.Context, repo string) error {
	_, err := gitOutputCommand(ctx, repo, nil, "config", "--includes", "--get-regexp", `^(extensions\.partialclone|remote\..*\.promisor)$`)
	if err == nil {
		return errors.New("private snapshots do not support partial-clone or promisor repositories")
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil
	}
	return err
}

func privateGitDenyEnvironment(entry string) bool {
	switch entry {
	case "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_NO_LAZY_FETCH=1", "GIT_ALLOW_PROTOCOL=", "GIT_NO_REPLACE_OBJECTS=1":
		return true
	}
	return false
}

func gitOutputCommand(ctx context.Context, repo string, env []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	argv := append([]string{"-C", repo, "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", argv...)
	configureGitProcess(cmd)
	cmd.Env = cleanGitEnvironment()
	cmd.Env = append(cmd.Env, env...)
	if err := wrapGitProcess(ctx, cmd); err != nil {
		return nil, err
	}
	cmd.WaitDelay = 2 * time.Second
	var stdout, stderr limitedOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	if stdout.exceeded || stderr.exceeded {
		return nil, errors.New("Git output exceeds workflow size limit")
	}
	return stdout.Bytes(), nil
}

func writeNewPrivate(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return err
	}
	return syncParent(path)
}
