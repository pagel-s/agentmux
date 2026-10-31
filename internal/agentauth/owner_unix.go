//go:build unix

package agentauth

import (
	"os"
	"syscall"
)

func ownedByController(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid()) && stat.Nlink == 1
}
