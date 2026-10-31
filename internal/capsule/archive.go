package capsule

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

type volume struct {
	Name, Source, Target string
	Bytes                int64
}
type stats struct {
	Bytes   int64
	Entries int
	Digest  string
}

func regular(info fs.FileInfo) bool {
	if !info.Mode().IsRegular() {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Nlink == 1
}

// inspect hashes file bytes through a rooted descriptor and rejects links,
// special files and mutations observed during the scan. Inputs are never
// followed through a symlink to an owner's files outside the selected tree.
func inspect(dir string, maxBytes int64, maxEntries int) (stats, error) {
	return inspectExcept(dir, maxBytes, maxEntries, "")
}
func inspectExcept(dir string, maxBytes int64, maxEntries int, skip string) (stats, error) {
	return inspectContext(context.Background(), dir, maxBytes, maxEntries, skip)
}
func inspectContext(ctx context.Context, dir string, maxBytes int64, maxEntries int, skip string) (stats, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return stats{}, err
	}
	defer root.Close()
	h := sha256.New()
	var out stats
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if name == "." {
			return nil
		}
		if name == skip {
			if !entry.IsDir() {
				return errors.New("capsule excluded generation is not a directory")
			}
			return fs.SkipDir
		}
		out.Entries++
		if out.Entries > maxEntries {
			return errors.New("capsule entry quota exceeded")
		}
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if !info.IsDir() && !regular(info) {
			return errors.New("capsule permits only regular unlinked files and directories")
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00", name, info.Mode().Perm()&0700, info.Size())
		if info.IsDir() {
			return nil
		}
		if info.Size() < 0 || info.Size() > maxBytes-out.Bytes {
			return errors.New("capsule byte quota exceeded")
		}
		out.Bytes += info.Size()
		f, err := root.Open(name)
		if err != nil {
			return err
		}
		defer f.Close()
		opened, err := f.Stat()
		if err != nil || !regular(opened) || !os.SameFile(info, opened) {
			return errors.New("capsule input changed during inspection")
		}
		n, err := io.Copy(h, io.LimitReader(contextReader{ctx: ctx, reader: f}, info.Size()+1))
		if err != nil || n != info.Size() {
			return errors.New("capsule input changed during inspection")
		}
		after, err := f.Stat()
		if err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
			return errors.New("capsule input changed during inspection")
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	out.Digest = hex.EncodeToString(h.Sum(nil))
	return out, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func writeArchive(w io.Writer, volumes []volume, maxEntries int) error {
	tw := tar.NewWriter(w)
	entries := 0
	for _, v := range volumes {
		if _, err := inspect(v.Source, v.Bytes, maxEntries); err != nil {
			return err
		}
		root, err := os.OpenRoot(v.Source)
		if err != nil {
			return err
		}
		err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if name == "." {
				return nil
			}
			entries++
			if entries > maxEntries {
				return errors.New("capsule entry quota exceeded")
			}
			info, err := root.Lstat(name)
			if err != nil {
				return err
			}
			if !info.IsDir() && !regular(info) {
				return errors.New("capsule special file rejected")
			}
			header := &tar.Header{Name: path.Join(v.Name, name), Mode: int64(info.Mode().Perm() & 0700), Typeflag: tar.TypeReg, Size: info.Size(), Format: tar.FormatUSTAR}
			if info.IsDir() {
				header.Typeflag = tar.TypeDir
				header.Size = 0
				header.Name += "/"
			}
			if err = tw.WriteHeader(header); err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			f, err := root.Open(name)
			if err != nil {
				return err
			}
			defer f.Close()
			opened, err := f.Stat()
			if err != nil || !regular(opened) || !os.SameFile(info, opened) {
				return errors.New("capsule file replaced during export")
			}
			_, err = io.CopyN(tw, f, info.Size())
			return err
		})
		root.Close()
		if err != nil {
			return err
		}
	}
	return tw.Close()
}

func validName(name string) bool {
	return name != "" && len(name) <= 4096 && name != "." && !strings.ContainsAny(name, "\\\x00") && !strings.HasPrefix(name, "/") && path.Clean(name) == name && !strings.HasPrefix(name, "../")
}

// receiveArchive writes only to fresh host-owned staging roots. It accepts no
// links, devices, sparse/PAX metadata, duplicate entries or implicit parent
// directories. Both data bytes and filesystem entry count are bounded.
func receiveArchive(r io.Reader, base string, volumes []volume, maxEntries int) error {
	var maxBytes int64
	byName := map[string]volume{}
	used := map[string]int64{}
	seen := map[string]bool{}
	for _, v := range volumes {
		maxBytes += v.Bytes
		byName[v.Name] = v
		seen[v.Name] = true
		if err := os.MkdirAll(filepath.Join(base, filepath.FromSlash(v.Name)), 0700); err != nil {
			return err
		}
	}
	// USTAR names have at most 255 bytes; every metadata record is bounded by
	// this wire envelope even if archive/tar consumes a hidden extension.
	limited := &io.LimitedReader{R: r, N: maxBytes + int64(maxEntries)*2048 + 1024}
	tr := tar.NewReader(limited)
	root, err := os.OpenRoot(base)
	if err != nil {
		return err
	}
	defer root.Close()
	entries := 0
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		entries++
		if entries > maxEntries {
			return errors.New("capsule entry quota exceeded")
		}
		name := strings.TrimSuffix(header.Name, "/")
		if !validName(name) || seen[name] || len(header.PAXRecords) != 0 || header.Linkname != "" || (header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir) {
			return errors.New("capsule archive entry rejected")
		}
		var v volume
		found := false
		for key, candidate := range byName {
			if strings.HasPrefix(name, key+"/") {
				v = candidate
				found = true
				break
			}
		}
		if !found || !seen[path.Dir(name)] {
			return errors.New("capsule archive path rejected")
		}
		if header.Size < 0 || header.Size > v.Bytes-used[v.Name] {
			return errors.New("capsule byte quota exceeded")
		}
		used[v.Name] += header.Size
		mode := os.FileMode(header.Mode) & 0700
		if header.Typeflag == tar.TypeDir {
			if header.Size != 0 {
				return errors.New("capsule directory data rejected")
			}
			if err = root.Mkdir(name, 0700); err != nil {
				return err
			}
		} else {
			f, e := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600|mode&0100)
			if e != nil {
				return e
			}
			n, e := io.CopyN(f, tr, header.Size)
			if e == nil && n == header.Size {
				e = f.Sync()
			}
			closeErr := f.Close()
			if e != nil {
				return e
			}
			if closeErr != nil {
				return closeErr
			}
		}
		seen[name] = true
	}
	return nil
}
