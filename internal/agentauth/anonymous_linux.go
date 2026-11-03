//go:build linux

package agentauth

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func anonymousCredential(data []byte) (*os.File, string, error) {
	fd, err := unix.MemfdCreate("agentmux-native-auth", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return nil, "", errors.New("anonymous native authentication requires sealed Linux memfd support")
	}
	f := os.NewFile(uintptr(fd), "agentmux-native-auth")
	fail := func() (*os.File, string, error) {
		f.Close()
		return nil, "", errors.New("anonymous native authentication could not be sealed")
	}
	if err := f.Chmod(0600); err != nil {
		return fail()
	}
	if _, err := f.Write(data); err != nil {
		return fail()
	}
	if _, err := f.Seek(0, 0); err != nil {
		return fail()
	}
	if _, err := unix.FcntlInt(f.Fd(), unix.F_ADD_SEALS, unix.F_SEAL_WRITE|unix.F_SEAL_GROW|unix.F_SEAL_SHRINK|unix.F_SEAL_SEAL); err != nil {
		return fail()
	}
	return f, fmt.Sprintf("/proc/%d/fd/%d", os.Getpid(), f.Fd()), nil
}
