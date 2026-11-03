package workflow

import (
	"bufio"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
)

// inspectIsolationGitObjects reads raw immutable object bytes without importing
// them. The guard includes commit messages and binary tree names, not just blobs.
// A second check of quarantine objects prevents a racing source replacement from
// publishing bytes different from those observed before the pack transfer.
func inspectIsolationGitObjects(ctx context.Context, repository string, prefix []string, ids []string, all bool) error {
	values := selectedIsolationCredentials(ctx)
	if len(values) == 0 {
		return nil
	}
	args := []string{"cat-file", "--batch"}
	if all {
		args = append(args, "--batch-all-objects")
	}
	cmd, cancel, err := privateGitCommand(ctx, repository, prefix, args...)
	if err != nil {
		return errors.New("credential inspection could not open imported Git objects")
	}
	defer cancel()
	if !all {
		cmd.Stdin = strings.NewReader(strings.Join(ids, "\n") + "\n")
	}
	var diagnostic limitedOutput
	cmd.Stderr = &diagnostic
	output, err := cmd.StdoutPipe()
	if err != nil {
		return errors.New("credential inspection could not read imported Git objects")
	}
	if err := cmd.Start(); err != nil {
		return errors.New("credential inspection could not start imported-object validation")
	}
	reader := bufio.NewReaderSize(output, 64<<10)
	var inspected int
	var total int64
	scan := func() error {
		for {
			header, err := reader.ReadSlice('\n')
			if err == io.EOF && len(header) == 0 {
				break
			}
			if err != nil {
				return errors.New("imported Git object header is malformed or exceeds its bound")
			}
			fields := strings.Fields(string(header))
			if len(fields) != 3 || !objectID(fields[0]) || fields[1] != "commit" && fields[1] != "tree" && fields[1] != "blob" && fields[1] != "tag" {
				return errors.New("imported Git object identity or type is invalid")
			}
			if !all && (inspected >= len(ids) || fields[0] != ids[inspected]) {
				return errors.New("imported Git objects differ from the selected graph")
			}
			size, err := strconv.ParseInt(fields[2], 10, 64)
			if err != nil || size < 0 || size > MaxSnapshotBytes || size > 4*MaxSnapshotBytes-total || inspected >= 4*MaxSnapshotFiles+1024 {
				return errors.New("imported Git object bytes exceed their credential-inspection bound")
			}
			total += size
			scanner := &credentialScanner{}
			for _, value := range values {
				scanner.needles = append(scanner.needles, []byte(value))
				scanner.window = max(scanner.window, len(value)-1)
			}
			if _, err := io.CopyN(scanner, reader, size); err != nil {
				if errors.Is(err, ErrIsolationCredentialLeak) {
					return ErrIsolationCredentialLeak
				}
				return errors.New("imported Git object bytes could not be verified")
			}
			terminator, err := reader.ReadByte()
			if err != nil || terminator != '\n' {
				return errors.New("imported Git object framing is invalid")
			}
			inspected++
		}
		if !all && inspected != len(ids) {
			return errors.New("selected imported Git object graph is incomplete")
		}
		return nil
	}()
	if scan != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil && !errors.Is(scan, ErrIsolationCredentialLeak) {
		return ctx.Err()
	}
	if scan != nil {
		return scan
	}
	if waitErr != nil || diagnostic.exceeded {
		return errors.New("imported Git object validation did not complete")
	}
	return nil
}
