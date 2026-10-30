//go:build !windows

package builderregistration

import (
	"errors"
	"os"
	"syscall"
)

func preserveOwner(f *os.File, info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if os.Geteuid() != 0 {
		if os.Geteuid() != int(stat.Uid) {
			return errors.New("cannot preserve file owner; run as configuration owner or root")
		}
		return nil
	}
	return f.Chown(int(stat.Uid), int(stat.Gid))
}
