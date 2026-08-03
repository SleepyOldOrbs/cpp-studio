//go:build windows

package models

import (
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
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
	pointer, err := syscall.UTF16PtrFromString(existing)
	if err != nil {
		return 0, err
	}
	var available uint64
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GetDiskFreeSpaceExW")
	result, _, callErr := proc.Call(uintptr(unsafe.Pointer(pointer)), uintptr(unsafe.Pointer(&available)), 0, 0)
	if result == 0 {
		return 0, callErr
	}
	return available, nil
}
