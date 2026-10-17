//go:build windows

package daemon

import (
	"errors"
	"os"
)

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }

func statfs(string) (uint64, uint64, error) {
	return 0, 0, errors.New("not supported on this system")
}
