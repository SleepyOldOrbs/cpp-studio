//go:build !windows

package models

import (
	"os"
	"path/filepath"
	"syscall"
)

func diskFreeSpace(path string) (uint64, error) {
	existing := path
	for {
		if _, err := os.Stat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		existing = parent
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(existing, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}
