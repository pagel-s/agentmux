package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

var ErrIsolationCredentialLeak = errors.New("selected credential appeared in the private workspace; retained patch and snapshot publication are prohibited")

// CheckIsolationCredentialLeak inspects the exact bounded snapshot selection
// before it can be copied into retained diffs or object stores. The private
// worktree is quarantined on failure. This checks literal selected values, not
// arbitrary encodings or all possible credentials.
func CheckIsolationCredentialLeak(ctx context.Context, id, repository string) error {
	r := isolationFromContext(ctx)
	if r == nil {
		return nil
	}
	r.mu.Lock()
	var values []string
	for _, selected := range r.secrets {
		values = append(values, selected...)
	}
	r.mu.Unlock()
	if len(values) == 0 {
		return nil
	}
	canonical, err := filepath.EvalSymlinks(repository)
	if err != nil || canonical != repository || !pathWithin(r.run, canonical) || canonical == r.run {
		return errors.New("credential inspection requires the exact selected private worktree")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	paths, err := privateSelectedPaths(ctx, repository)
	if err != nil {
		return errors.New("credential inspection could not enumerate the bounded private selection")
	}
	root, err := os.OpenRoot(repository)
	if err != nil {
		return errors.New("credential inspection could not open the private selection")
	}
	defer root.Close()
	scanner := &credentialScanner{}
	for _, value := range values {
		if value != "" {
			scanner.needles = append(scanner.needles, []byte(value))
			scanner.window = max(scanner.window, len(value)-1)
		}
	}
	usage := SnapshotSelection{}
	for _, path := range paths {
		scanner.tail = nil
		if err := writePrivateSelected(ctx, root, path, scanner, sha256.New(), &usage); err != nil {
			if errors.Is(err, ErrIsolationCredentialLeak) {
				return ErrIsolationCredentialLeak
			}
			return errors.New("credential inspection could not verify the exact bounded private selection")
		}
	}
	return nil
}

type credentialScanner struct {
	needles [][]byte
	tail    []byte
	window  int
}

func (s *credentialScanner) Write(data []byte) (int, error) {
	combined := make([]byte, 0, len(s.tail)+len(data))
	combined = append(combined, s.tail...)
	combined = append(combined, data...)
	for _, needle := range s.needles {
		if bytes.Contains(combined, needle) {
			return 0, ErrIsolationCredentialLeak
		}
	}
	keep := min(s.window, len(combined))
	s.tail = append(s.tail[:0], combined[len(combined)-keep:]...)
	return len(data), nil
}

// isolationCredentialWriter checks bytes while they are copied into Git's
// private import stream, closing the gap between pre-export scan and capture.
// It retains enough unforwarded bytes to recognize a value split across writes.
func isolationCredentialWriter(ctx context.Context, path string, out io.Writer) (io.Writer, func() error, error) {
	var needles [][]byte
	window := 0
	for _, value := range selectedIsolationCredentials(ctx) {
		if value != "" {
			needles = append(needles, []byte(value))
			window = max(window, len(value)-1)
		}
	}
	if len(needles) == 0 {
		return out, func() error { return nil }, nil
	}
	for _, needle := range needles {
		if bytes.Contains([]byte(path), needle) {
			return nil, nil, ErrIsolationCredentialLeak
		}
	}
	guard := &credentialGuardWriter{out: out, needles: needles, window: window}
	return guard, guard.Flush, nil
}

type credentialGuardWriter struct {
	out     io.Writer
	needles [][]byte
	pending []byte
	window  int
	failed  error
}

func (g *credentialGuardWriter) Write(data []byte) (int, error) {
	if g.failed != nil {
		return 0, g.failed
	}
	combined := make([]byte, 0, len(g.pending)+len(data))
	combined = append(combined, g.pending...)
	combined = append(combined, data...)
	for _, needle := range g.needles {
		if bytes.Contains(combined, needle) {
			g.failed = ErrIsolationCredentialLeak
			return 0, g.failed
		}
	}
	count := max(0, len(combined)-g.window)
	if count > 0 {
		if n, err := g.out.Write(combined[:count]); err != nil || n != count {
			if err == nil {
				err = io.ErrShortWrite
			}
			g.failed = err
			return 0, err
		}
	}
	g.pending = append(g.pending[:0], combined[count:]...)
	return len(data), nil
}
func (g *credentialGuardWriter) Flush() error {
	if g.failed != nil {
		return g.failed
	}
	if len(g.pending) > 0 {
		n, err := g.out.Write(g.pending)
		if err != nil {
			return err
		}
		if n != len(g.pending) {
			return io.ErrShortWrite
		}
		g.pending = nil
	}
	return nil
}
