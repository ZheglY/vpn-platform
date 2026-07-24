//go:build !windows

package staging

import (
	"errors"
	"os"
	"syscall"
)

func fileOwner(info os.FileInfo) (int, int, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, errors.New("unsupported file metadata")
	}
	return int(stat.Uid), int(stat.Gid), nil
}
