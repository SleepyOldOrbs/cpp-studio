//go:build windows

package models

import "syscall"

// promoteNoReplace uses MoveFile without the replace-existing flag. Staging
// and destination share the models volume, so the final name appears as one
// move and an externally-created destination is never overwritten.
func promoteNoReplace(source, destination string) error {
	from, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	to, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return syscall.MoveFile(from, to)
}
