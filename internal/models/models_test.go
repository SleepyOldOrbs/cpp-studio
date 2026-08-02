package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "models.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

func TestLoadRejectsDuplicateAndMissingFields(t *testing.T) {
	cases := map[string]string{
		"missing id":    `{"models":[{"engine":"llama","path":"a.gguf"}]}`,
		"missing path":  `{"models":[{"id":"a","engine":"llama"}]}`,
		"duplicate id":  `{"models":[{"id":"a","path":"a"},{"id":"a","path":"b"}]}`,
		"unknown field": `{"models":[{"id":"a","path":"a","nope":1}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeManifest(t, body)); err == nil {
				t.Fatalf("expected error for %s", name)
			}
		})
	}
}

func TestTrackedManifestDeclaresExactDramaBoxPackage(t *testing.T) {
	manifest, err := Load(filepath.Join("..", "..", "models.json"))
	if err != nil {
		t.Fatalf("load tracked model manifest: %v", err)
	}
	for _, model := range manifest.Models {
		if model.ID != "dramabox-q8-0" {
			continue
		}
		if model.Engine != "dramabox" || model.Family != "dramabox" || model.Bytes != 18942803808 {
			t.Fatalf("unexpected DramaBox package identity: %+v", model)
		}
		if model.Path != "audio.cpp/models/DramaBox-GGUF/dramabox-q8_0.gguf" || model.Source == "" || model.License != "LTX-2 Community License" {
			t.Fatalf("missing DramaBox package provenance: %+v", model)
		}
		return
	}
	t.Fatal("tracked manifest is missing DramaBox")
}

func TestStatusesReflectDisk(t *testing.T) {
	root := t.TempDir()
	// present with matching size
	present := filepath.Join(root, "present.gguf")
	if err := os.WriteFile(present, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	// present with wrong size vs manifest
	wrong := filepath.Join(root, "wrong.gguf")
	if err := os.WriteFile(wrong, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	// a directory-based model (TTS engines ship folders, no expected bytes)
	if err := os.Mkdir(filepath.Join(root, "voicedir"), 0o755); err != nil {
		t.Fatal(err)
	}

	m := Manifest{Models: []Model{
		{ID: "present", Engine: "llama", Path: "present.gguf", Bytes: 5},
		{ID: "wrong", Engine: "sd", Path: "wrong.gguf", Bytes: 999},
		{ID: "missing", Engine: "whisper", Path: "nope.bin", Bytes: 10},
		{ID: "voice", Engine: "audio", Path: "voicedir"},
	}}

	byID := map[string]Status{}
	for _, s := range m.Statuses(root) {
		byID[s.ID] = s
	}
	if got := byID["present"].State; got != StatePresent {
		t.Errorf("present: got %q", got)
	}
	if got := byID["wrong"].State; got != StateSizeMismatch {
		t.Errorf("wrong: got %q", got)
	}
	if got := byID["missing"].State; got != StateMissing {
		t.Errorf("missing: got %q", got)
	}
	if s := byID["voice"]; !s.Present || s.State != StateUnverified {
		t.Errorf("voice dir: present=%v state=%q", s.Present, s.State)
	}
}

func TestVerifyChecksums(t *testing.T) {
	root := t.TempDir()
	content := []byte("the model bytes")
	if err := os.WriteFile(filepath.Join(root, "m.gguf"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	good := hex.EncodeToString(sum[:])

	m := Manifest{Models: []Model{{ID: "m", Path: "m.gguf", Bytes: int64(len(content)), SHA256: good}}}
	s, err := m.Verify(context.Background(), "m", root)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if s.State != StateVerified {
		t.Fatalf("expected verified, got %q", s.State)
	}

	bad := Manifest{Models: []Model{{ID: "m", Path: "m.gguf", SHA256: "deadbeef"}}}
	s, err = bad.Verify(context.Background(), "m", root)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if s.State != StateCorrupt {
		t.Fatalf("expected corrupt, got %q", s.State)
	}
}

func TestDirectoryModelsAreSizeChecked(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "voice")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.bin"), []byte("aaaa"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.bin"), []byte("bbbbbb"), 0o600); err != nil {
		t.Fatal(err)
	}

	m := Manifest{Models: []Model{{ID: "voice", Path: "voice", Bytes: 10, Files: 2}}}
	statuses := m.Statuses(root)
	if statuses[0].State != StatePresent || statuses[0].ActualBytes != 10 || statuses[0].ActualFiles != 2 {
		t.Fatalf("expected present 10B/2 files, got %+v", statuses[0])
	}

	// A deep verify on a matching directory reports verified.
	s, err := m.Verify(context.Background(), "voice", root)
	if err != nil {
		t.Fatalf("verify dir: %v", err)
	}
	if s.State != StateVerified {
		t.Fatalf("expected verified dir, got %q", s.State)
	}

	// Deleting a file flips it to size-mismatch.
	if err := os.Remove(filepath.Join(dir, "sub", "b.bin")); err != nil {
		t.Fatal(err)
	}
	if got := m.Statuses(root)[0].State; got != StateSizeMismatch {
		t.Fatalf("expected size-mismatch after deletion, got %q", got)
	}

	// Wrong file count with right bytes also mismatches.
	wrongCount := Manifest{Models: []Model{{ID: "voice", Path: "voice", Bytes: 4, Files: 5}}}
	if got := wrongCount.Statuses(root)[0].State; got != StateSizeMismatch {
		t.Fatalf("expected file-count mismatch, got %q", got)
	}
}

func TestVerifyAllReportsProgressAndCancels(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.bin", "b.bin"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := Manifest{Models: []Model{
		{ID: "a", Path: "a.bin", Bytes: 4},
		{ID: "b", Path: "b.bin", Bytes: 4},
	}}

	var seen []string
	statuses, err := m.VerifyAll(context.Background(), root, func(_, _ int, id string) {
		seen = append(seen, id)
	})
	if err != nil {
		t.Fatalf("verify all: %v", err)
	}
	if len(statuses) != 2 || len(seen) != 2 {
		t.Fatalf("expected 2 results and 2 progress calls, got %d/%d", len(statuses), len(seen))
	}
	for _, s := range statuses {
		if s.State != StateVerified {
			t.Fatalf("expected verified, got %+v", s)
		}
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.VerifyAll(cancelled, root, nil); err == nil {
		t.Fatal("expected cancellation error")
	}
}
