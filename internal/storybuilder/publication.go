package storybuilder

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// artifactPublication keeps domain validation and manifest construction with
// the owning caller while concentrating artifact staging and rollback here.
type artifactPublication struct {
	FinalPath   string
	TempPattern string
	Replace     bool
	Stage       func(string) error
	Validate    func(string) error
	Record      func() error
}

// publishArtifact publishes one artifact and its manifest record as a single
// transaction. The bool reports whether the manifest was committed; it can be
// true with a cleanup error after a successful replacement.
func publishArtifact(spec artifactPublication) (published bool, returnErr error) {
	if spec.FinalPath == "" || spec.Stage == nil || spec.Record == nil {
		return false, errors.New("invalid Story Builder artifact publication")
	}
	directory := filepath.Dir(spec.FinalPath)
	pattern := spec.TempPattern
	if pattern == "" {
		pattern = "." + filepath.Base(spec.FinalPath) + ".staging-*"
	}
	tmp, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return false, fmt.Errorf("stage Story Builder artifact: %w", err)
	}
	stagedPath := tmp.Name()
	defer func() {
		if err := os.Remove(stagedPath); err != nil && !os.IsNotExist(err) {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove Story Builder artifact staging file %q: %w", stagedPath, err))
		}
	}()
	if err := tmp.Close(); err != nil {
		return false, fmt.Errorf("stage Story Builder artifact: %w", err)
	}
	if err := os.Remove(stagedPath); err != nil {
		return false, fmt.Errorf("stage Story Builder artifact: %w", err)
	}
	if err := spec.Stage(stagedPath); err != nil {
		return false, err
	}
	if spec.Validate != nil {
		if err := spec.Validate(stagedPath); err != nil {
			return false, err
		}
	}

	backupPath := ""
	if _, err := os.Stat(spec.FinalPath); err == nil {
		if !spec.Replace {
			return false, ErrConflict
		}
		backup, createErr := os.CreateTemp(directory, "."+filepath.Base(spec.FinalPath)+".backup-*")
		if createErr != nil {
			return false, fmt.Errorf("stage existing Story Builder artifact: %w", createErr)
		}
		backupPath = backup.Name()
		if closeErr := backup.Close(); closeErr != nil {
			_ = os.Remove(backupPath)
			return false, fmt.Errorf("stage existing Story Builder artifact: %w", closeErr)
		}
		if err := os.Remove(backupPath); err != nil {
			return false, fmt.Errorf("stage existing Story Builder artifact: %w", err)
		}
		if err := os.Rename(spec.FinalPath, backupPath); err != nil {
			return false, fmt.Errorf("stage existing Story Builder artifact: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("inspect Story Builder artifact: %w", err)
	}

	restore := func() error {
		if err := os.Remove(spec.FinalPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove unpublished Story Builder artifact: %w", err)
		}
		if backupPath != "" {
			if err := os.Rename(backupPath, spec.FinalPath); err != nil {
				return fmt.Errorf("restore Story Builder artifact backup %q: %w", backupPath, err)
			}
		}
		return nil
	}
	if err := os.Rename(stagedPath, spec.FinalPath); err != nil {
		return false, errors.Join(fmt.Errorf("publish Story Builder artifact: %w", err), restore())
	}
	if err := spec.Record(); err != nil {
		return false, errors.Join(err, restore())
	}
	published = true
	if backupPath != "" {
		if err := os.Remove(backupPath); err != nil {
			return true, fmt.Errorf("Story Builder artifact was published but backup cleanup failed at %q: %w", backupPath, err)
		}
	}
	return true, nil
}
