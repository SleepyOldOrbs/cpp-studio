//go:build !windows

package models

import "os"

// A hard link publishes the already-complete staged file atomically and
// fails when destination exists. The validated staging cleanup removes the
// source link after catalog verification.
func promoteNoReplace(source, destination string) error {
	return os.Link(source, destination)
}
