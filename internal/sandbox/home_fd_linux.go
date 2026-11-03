//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func anonymousHomeSource(file HomeFile) (string, error) {
	if file.Descriptor == nil {
		return "", errors.New("anonymous home file requires a descriptor")
	}
	info, err := file.Descriptor.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return "", errors.New("anonymous native credential must be a private regular file")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Nlink != 0 || st.Uid != uint32(os.Geteuid()) {
		return "", errors.New("native credential must be an owned anonymous object")
	}
	seals, err := unix.FcntlInt(file.Descriptor.Fd(), unix.F_GET_SEALS, 0)
	required := unix.F_SEAL_WRITE | unix.F_SEAL_GROW | unix.F_SEAL_SHRINK | unix.F_SEAL_SEAL
	if err != nil || seals&required != required {
		return "", errors.New("anonymous native credential requires immutable kernel seals")
	}
	source := fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), file.Descriptor.Fd())
	if file.Source != "" && file.Source != source {
		return "", errors.New("anonymous native credential reference does not match its descriptor")
	}
	return source, nil
}
