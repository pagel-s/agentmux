//go:build unix

package claude

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func ownedByCurrentUser(st os.FileInfo) bool {
	s, ok := st.Sys().(*syscall.Stat_t)
	return ok && s.Uid == uint32(os.Geteuid())
}

func helperLock(ctx context.Context, path, marker, session string, interval time.Duration) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			if err := os.WriteFile(path+".session", []byte(session), 0600); err != nil {
				_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
				_ = f.Close()
				return nil, err
			}
			return func() { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN); _ = f.Close() }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			_ = f.Close()
			return nil, err
		}
		// A duplicate SessionStart for the same conversation does not create a
		// second poller. A changed session waits for the previous poller to retire.
		current, _ := os.ReadFile(marker)
		if string(current) != session {
			_ = f.Close()
			return nil, nil
		}
		previous, _ := os.ReadFile(path + ".session")
		if string(previous) == session {
			_ = f.Close()
			return nil, nil
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, errors.New("previous Claude native bridge has not retired")
		}
		if err := wait(ctx, interval); err != nil {
			_ = f.Close()
			return nil, err
		}
	}
}
