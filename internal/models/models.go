// Package models is the declarative registry of the model files the engines
// need. A tracked manifest (models.json) lists each model's identity, engine,
// relative path, expected size, checksum, and provenance; this package loads it
// and reports live on-disk status so the console can show what is present,
// missing, or mismatched — the backbone for the models/downloads surface and,
// later, bring-your-own-model.
package models

import (
	"bytes"
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
// the file is portable across machines.
type Model struct {
	ID          string `json:"id"`
	Engine      string `json:"engine"`
	Family      string `json:"family"`
	Path        string `json:"path"`
	Bytes       int64  `json:"bytes,omitempty"`
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
	StatePresent      = "present"       // exists and (if known) matches expected size
	StateMissing      = "missing"       // not on disk
	StateSizeMismatch = "size-mismatch" // exists but wrong size vs manifest
	StateCorrupt      = "corrupt"       // sha256 verified and did not match
	StateUnverified   = "unverified"    // present, no expected size/sha to check
)

// Status is a model plus its resolved on-disk reality.
type Status struct {
	Model
	AbsPath     string `json:"absPath"`
	Present     bool   `json:"present"`
	ActualBytes int64  `json:"actualBytes"`
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
	s.ActualBytes = info.Size()
	switch {
	case mod.Bytes > 0 && info.Size() != mod.Bytes:
		s.State = StateSizeMismatch
	case mod.Bytes > 0:
		s.State = StatePresent
	default:
		s.State = StateUnverified
	}
	return s
}

// Verify computes the sha256 of a present model and compares it to the
// manifest. It reads the whole file, so callers gate it behind explicit intent.
func (m Manifest) Verify(id, root string) (Status, error) {
	for _, mod := range m.Models {
		if mod.ID != id {
			continue
		}
		s := stat(mod, root)
		if !s.Present {
			return s, nil
		}
		if mod.SHA256 == "" {
			return s, nil
		}
		sum, err := sha256File(s.AbsPath)
		if err != nil {
			return s, err
		}
		if sum != mod.SHA256 {
			s.State = StateCorrupt
		} else {
			s.State = StatePresent
		}
		return s, nil
	}
	return Status{}, fmt.Errorf("unknown model id %q", id)
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
