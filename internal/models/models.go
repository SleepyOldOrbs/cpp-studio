// Package models is the declarative registry of the model files the engines
// need. A tracked manifest (models.json) lists each model's identity, engine,
// relative path, expected size, checksum, and provenance; this package loads it
// and reports live on-disk status so the console can show what is present,
// missing, or mismatched — the backbone for the models/downloads surface and,
// later, bring-your-own-model.
package models

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// Model is one entry in the manifest. Path is relative to the manifest root so
// the file is portable across machines. Directory models (multi-file model
// folders) declare their expected total Bytes and Files count; single files
// declare Bytes and, for checksum verification, SHA256.
type Model struct {
	ID          string `json:"id"`
	Engine      string `json:"engine"`
	Family      string `json:"family"`
	Path        string `json:"path"`
	Bytes       int64  `json:"bytes,omitempty"`
	Files       int    `json:"files,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Source      string `json:"source,omitempty"`
	License     string `json:"license,omitempty"`
	Description string `json:"description,omitempty"`
}

// Manifest is the whole registry.
type Manifest struct {
	Models []Model `json:"models"`
}

// State classifies a model's on-disk status.
const (
	StatePresent      = "present"       // exists and matches expected size (and file count for dirs)
	StateMissing      = "missing"       // not on disk
	StateSizeMismatch = "size-mismatch" // exists but wrong size or file count vs manifest
	StateCorrupt      = "corrupt"       // deep verification ran and did not match
	StateUnverified   = "unverified"    // present, but manifest declares nothing to check
	StateVerified     = "verified"      // deep verification ran and passed
)

// Status is a model plus its resolved on-disk reality.
type Status struct {
	Model
	AbsPath     string `json:"absPath"`
	Present     bool   `json:"present"`
	ActualBytes int64  `json:"actualBytes"`
	ActualFiles int    `json:"actualFiles,omitempty"`
	State       string `json:"state"`
}

// Load reads and validates a manifest file.
func Load(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("parse model manifest: %w", err)
	}
	seen := make(map[string]bool, len(m.Models))
	for i, mod := range m.Models {
		if mod.ID == "" {
			return Manifest{}, fmt.Errorf("model %d: id is required", i)
		}
		if seen[mod.ID] {
			return Manifest{}, fmt.Errorf("duplicate model id %q", mod.ID)
		}
		seen[mod.ID] = true
		if mod.Path == "" {
			return Manifest{}, fmt.Errorf("model %q: path is required", mod.ID)
		}
	}
	return m, nil
}

// Statuses resolves each model against root and reports its on-disk state. It
// only stats files (fast); checksum verification is the explicit opt-in Verify.
func (m Manifest) Statuses(root string) []Status {
	out := make([]Status, 0, len(m.Models))
	for _, mod := range m.Models {
		out = append(out, stat(mod, root))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Engine != out[j].Engine {
			return out[i].Engine < out[j].Engine
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func stat(mod Model, root string) Status {
	abs := mod.Path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, mod.Path)
	}
	s := Status{Model: mod, AbsPath: abs}
	info, err := os.Stat(abs)
	if err != nil {
		s.State = StateMissing
		return s
	}
	s.Present = true
	if info.IsDir() {
		total, count, err := walkDir(abs)
		if err != nil {
			s.State = StateSizeMismatch
			return s
		}
		s.ActualBytes = total
		s.ActualFiles = count
	} else {
		s.ActualBytes = info.Size()
	}
	switch {
	case mod.Bytes <= 0:
		s.State = StateUnverified
	case s.ActualBytes != mod.Bytes,
		mod.Files > 0 && s.ActualFiles != mod.Files:
		s.State = StateSizeMismatch
	default:
		s.State = StatePresent
	}
	return s
}

// walkDir totals a directory model's bytes and file count.
func walkDir(root string) (int64, int, error) {
	var total int64
	var count int
	err := filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		count++
		return nil
	})
	return total, count, err
}

// Verify deep-checks one model: files with a manifest sha256 are fully
// hashed; directory models are exhaustively walked against their expected
// total bytes and file count. Passing yields StateVerified, a mismatch
// StateCorrupt (checksum) or the stat states. It reads whole files, so
// callers gate it behind explicit intent; ctx aborts a long hash.
func (m Manifest) Verify(ctx context.Context, id, root string) (Status, error) {
	for _, mod := range m.Models {
		if mod.ID != id {
			continue
		}
		return verify(ctx, mod, root)
	}
	return Status{}, fmt.Errorf("unknown model id %q", id)
}

func verify(ctx context.Context, mod Model, root string) (Status, error) {
	s := stat(mod, root)
	if !s.Present {
		return s, nil
	}
	if mod.SHA256 != "" {
		sum, err := sha256File(ctx, s.AbsPath)
		if err != nil {
			return s, err
		}
		if sum != mod.SHA256 {
			s.State = StateCorrupt
		} else {
			s.State = StateVerified
		}
		return s, nil
	}
	// Without a checksum, the exhaustive walk/stat comparison is the deep
	// check: byte totals (and file counts for directories) must match.
	if s.State == StatePresent {
		s.State = StateVerified
	}
	return s, nil
}

// VerifyAll deep-checks every model, reporting progress before each one.
// Cancellation applies between models and inside file hashing.
func (m Manifest) VerifyAll(ctx context.Context, root string, progress func(done, total int, id string)) ([]Status, error) {
	out := make([]Status, 0, len(m.Models))
	for i, mod := range m.Models {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if progress != nil {
			progress(i, len(m.Models), mod.ID)
		}
		s, err := verify(ctx, mod, root)
		if err != nil {
			return out, fmt.Errorf("verify %s: %w", mod.ID, err)
		}
		out = append(out, s)
	}
	return out, nil
}

func sha256File(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 4*1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
