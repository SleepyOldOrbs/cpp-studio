package voice

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cpp-studio/internal/wav"
)

func validWAVBytes() []byte {
	return []byte{
		'R', 'I', 'F', 'F',
		0x24, 0x00, 0x00, 0x00,
		'W', 'A', 'V', 'E',
		'f', 'm', 't', ' ',
	}
}

func TestStoreSaveListLoadDelete(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "voices"))

	clone, err := store.Save("James", "the reference words", validWAVBytes(), false)
	if err != nil {
		t.Fatalf("save voice: %v", err)
	}
	if clone.ID == "" || !strings.HasPrefix(clone.ID, "voice_") {
		t.Fatalf("unexpected voice id %q", clone.ID)
	}
	if clone.Name != "James" || clone.Transcript != "the reference words" {
		t.Fatalf("unexpected clone %+v", clone)
	}
	if clone.CreatedAt.IsZero() {
		t.Fatalf("expected created timestamp, got %+v", clone)
	}

	loaded, ok, err := store.Load(clone.ID)
	if err != nil || !ok {
		t.Fatalf("load voice: ok=%v err=%v", ok, err)
	}
	if loaded.Name != "James" || loaded.Transcript != "the reference words" {
		t.Fatalf("unexpected loaded clone %+v", loaded)
	}

	refPath, err := store.ReferencePath(clone.ID)
	if err != nil {
		t.Fatalf("reference path: %v", err)
	}
	if !filepath.IsAbs(refPath) {
		t.Fatalf("expected absolute reference path, got %q", refPath)
	}
	data, err := os.ReadFile(refPath)
	if err != nil {
		t.Fatalf("read reference wav: %v", err)
	}
	if string(data[:4]) != "RIFF" {
		t.Fatalf("unexpected reference wav bytes %q", data)
	}

	second, err := store.Save("Other", "other words", validWAVBytes(), false)
	if err != nil {
		t.Fatalf("save second voice: %v", err)
	}
	clones, err := store.List()
	if err != nil {
		t.Fatalf("list voices: %v", err)
	}
	if len(clones) != 2 {
		t.Fatalf("expected 2 voices, got %+v", clones)
	}

	if err := store.Delete(clone.ID); err != nil {
		t.Fatalf("delete voice: %v", err)
	}
	clones, err = store.List()
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(clones) != 1 || clones[0].ID != second.ID {
		t.Fatalf("expected only second voice after delete, got %+v", clones)
	}

	if _, ok, _ := store.Load(clone.ID); ok {
		t.Fatalf("expected deleted voice to be gone")
	}
}

func TestStoreProtectedVoiceRefusesDeletion(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "voices"))

	clone, err := store.Save("Cox", "protected reference words", validWAVBytes(), true)
	if err != nil {
		t.Fatalf("save protected voice: %v", err)
	}
	if !clone.Protected {
		t.Fatalf("expected protected clone, got %+v", clone)
	}

	loaded, ok, err := store.Load(clone.ID)
	if err != nil || !ok || !loaded.Protected {
		t.Fatalf("expected protected flag to persist, got ok=%v err=%v clone=%+v", ok, err, loaded)
	}

	if err := store.Delete(clone.ID); !errors.Is(err, ErrProtected) {
		t.Fatalf("expected ErrProtected, got %v", err)
	}
	if _, ok, _ := store.Load(clone.ID); !ok {
		t.Fatalf("expected protected voice to survive the delete attempt")
	}
}

func TestStoreListEmptyRoot(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "missing"))
	clones, err := store.List()
	if err != nil {
		t.Fatalf("list voices: %v", err)
	}
	if len(clones) != 0 {
		t.Fatalf("expected no voices, got %+v", clones)
	}
}

func TestStoreSaveValidation(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "voices"))

	tests := []struct {
		name       string
		voiceName  string
		transcript string
		wav        []byte
		want       string
	}{
		{name: "missing name", voiceName: "", transcript: "words", wav: validWAVBytes(), want: "name is required"},
		{name: "long name", voiceName: strings.Repeat("n", MaxVoiceNameChars+1), transcript: "words", wav: validWAVBytes(), want: "name cannot exceed"},
		{name: "missing transcript", voiceName: "James", transcript: "", wav: validWAVBytes(), want: "transcript is required"},
		{name: "long transcript", voiceName: "James", transcript: strings.Repeat("t", MaxVoiceTranscriptLen+1), wav: validWAVBytes(), want: "transcript cannot exceed"},
		{name: "invalid wav", voiceName: "James", transcript: "words", wav: []byte("not a wav"), want: "reference wav"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := store.Save(tt.voiceName, tt.transcript, tt.wav, false); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}

	if clones, err := store.List(); err != nil || len(clones) != 0 {
		t.Fatalf("expected no voices after rejected saves, got %v %v", clones, err)
	}
}

func TestStoreRejectsBadIDs(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "voices"))
	for _, id := range []string{"", "../escape", `bad\slash`, "bad/slash", "bad id"} {
		if _, ok, err := store.Load(id); ok || err != nil {
			t.Fatalf("Load(%q): expected not found without error, got ok=%v err=%v", id, ok, err)
		}
		if _, err := store.ReferencePath(id); err == nil {
			t.Fatalf("ReferencePath(%q): expected error", id)
		}
		if err := store.Delete(id); err == nil {
			t.Fatalf("Delete(%q): expected error", id)
		}
	}
}

func TestStorePersistsPCMReferenceAnalysisAndLazilyUpgradesOldManifest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "voices")
	store := NewStore(root)
	clone, err := store.Save("Measured", "reference words", wav.SyntheticTone(wav.ToneSampleRate), false)
	if err != nil {
		t.Fatal(err)
	}
	if clone.Analysis == nil || clone.Analysis.DurationSeconds != 1 || clone.Analysis.SampleRate != wav.ToneSampleRate || clone.Analysis.BitsPerSample != 16 || clone.Analysis.ContentSHA256 == "" {
		t.Fatalf("analysis missing from new clone: %+v", clone.Analysis)
	}

	manifestPath := filepath.Join(root, clone.ID, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(data, &legacy); err != nil {
		t.Fatal(err)
	}
	delete(legacy, "analysis")
	data, _ = json.MarshalIndent(legacy, "", "  ")
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, ok, err := store.Load(clone.ID)
	if err != nil || !ok || loaded.Analysis == nil || loaded.Analysis.Method != "pcm-heuristic-v1" {
		t.Fatalf("legacy voice was not lazily analyzed: ok=%v err=%v clone=%+v", ok, err, loaded)
	}
	persisted, err := os.ReadFile(manifestPath)
	if err != nil || !strings.Contains(string(persisted), `"analysis"`) {
		t.Fatalf("lazy analysis was not persisted: err=%v manifest=%s", err, persisted)
	}
}
