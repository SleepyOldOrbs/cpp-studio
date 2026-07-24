package models

import (
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
	s, err := m.Verify("m", root)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if s.State != StatePresent {
		t.Fatalf("expected present, got %q", s.State)
	}

	bad := Manifest{Models: []Model{{ID: "m", Path: "m.gguf", SHA256: "deadbeef"}}}
	s, err = bad.Verify("m", root)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if s.State != StateCorrupt {
		t.Fatalf("expected corrupt, got %q", s.State)
	}
}
