package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
)

// Structural copying is not enough: regular files such as alternates or an
// include-bearing config can redirect Git without containing any symlinks.
// Inspect the anchored controller copy before Git can read it, and repeat the
// same policy check before publishing the command's result.
func (q *privateGitQuarantine) validatePolicy() (err error) {
	defer func() {
		if err != nil {
			err = &privateMetadataValidationError{original: err}
		}
	}()
	root, err := openPrivateMetadataRoot(q.git)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := listPrivateMetadata(q.ctx, root, defaultPrivateMetadataLimits()); err != nil {
		return err
	}
	if !q.initial {
		marker, err := validatePrivateRootPolicy(q.ctx, root, q.expectedIgnore)
		if err != nil {
			return err
		}
		if q.expectedIgnore == "" {
			// Low-level callers without a recorded snapshot still pin the first
			// admitted controller copy across execution and publication.
			q.expectedIgnore = marker.IgnoreSHA256
		}
		return nil
	}
	// init has no marker or raw attributes yet. The destination was admitted
	// empty, and only its requested format/type and safe Git metadata may be
	// published before those controller-authored files are installed.
	if err := rejectPrivateMetadataRedirects(root); err != nil {
		return err
	}
	if q.initialMarker.ObjectFormat != "sha1" && q.initialMarker.ObjectFormat != "sha256" {
		return errors.New("invalid private Git initialization format")
	}
	config, err := readPrivatePolicyFile(q.ctx, root, "config", 1<<20)
	if err != nil {
		return err
	}
	return validatePrivateGitConfigData(config, q.initialMarker)
}

func rejectPrivateMetadataRedirects(root *os.Root) error {
	for _, path := range []string{"objects/info/alternates", "objects/info/http-alternates", "commondir", "config.worktree"} {
		if _, err := root.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return errors.New("private Git store contains redirected metadata")
		}
	}
	return nil
}

func validatePrivateRootPolicy(ctx context.Context, root *os.Root, expectedIgnore string) (marker privateStoreMarker, err error) {
	if err := rejectPrivateMetadataRedirects(root); err != nil {
		return marker, err
	}
	data, err := readPrivatePolicyFile(ctx, root, privateStoreMarkerName, 4096)
	if err != nil {
		return marker, err
	}
	if err = DecodeJSON(data, &marker); err != nil {
		return marker, err
	}
	if marker.Version != 1 || marker.ObjectFormat != "sha1" && marker.ObjectFormat != "sha256" || !digestID(marker.IgnoreSHA256) {
		return marker, errors.New("invalid private Git store marker")
	}
	if expectedIgnore != "" && marker.IgnoreSHA256 != expectedIgnore {
		return marker, errors.New("private Git ignore policy differs from its pinned identity")
	}
	attributes, err := readPrivatePolicyFile(ctx, root, "info/attributes", 4096)
	if err != nil || string(attributes) != privateRawAttributes {
		return marker, errors.New("private raw checkout attributes changed or became unavailable")
	}
	ignore, err := readPrivatePolicyFile(ctx, root, "info/exclude", 2<<20)
	if err != nil {
		return marker, err
	}
	digest := sha256.Sum256(ignore)
	if hex.EncodeToString(digest[:]) != marker.IgnoreSHA256 {
		return marker, errors.New("private Git ignore policy changed")
	}
	config, err := readPrivatePolicyFile(ctx, root, "config", 1<<20)
	if err != nil {
		return marker, err
	}
	return marker, validatePrivateGitConfigData(config, marker)
}

func readPrivatePolicyFile(ctx context.Context, root *os.Root, path string, bound int64) ([]byte, error) {
	if err := privateSelectedParents(root, path); err != nil {
		return nil, err
	}
	info, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || privateFileHasMultipleLinks(info) || info.Size() < 0 || info.Size() > bound {
		return nil, errors.New("private Git policy file is not an independent bounded regular file")
	}
	f, err := privateRootOpenRegular(root, path, info)
	if err != nil {
		return nil, err
	}
	var data bytes.Buffer
	read, copyErr := copyPrivateMetadataBytes(ctx, &data, f, info.Size())
	after, statErr := f.Stat()
	closeErr := f.Close()
	pathAfter, pathErr := root.Lstat(path)
	if err := errors.Join(copyErr, statErr, closeErr, pathErr); err != nil {
		return nil, err
	}
	if read != info.Size() || !os.SameFile(info, pathAfter) || after.Size() != info.Size() || after.Mode() != info.Mode() || !after.ModTime().Equal(info.ModTime()) || pathAfter.Size() != info.Size() || pathAfter.Mode() != info.Mode() || !pathAfter.ModTime().Equal(info.ModTime()) {
		return nil, errors.New("private Git policy file changed while reading")
	}
	return data.Bytes(), nil
}
