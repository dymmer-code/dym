//go:build !windows

package daemon

import (
	"os"
	"syscall"
)

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }

func statfs(path string) (total, free uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	size := uint64(st.Bsize)
	return uint64(st.Blocks) * size, uint64(st.Bfree) * size, nil
}
