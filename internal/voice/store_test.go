package voice

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestStoreUsesOptionalVADAndDisclosesFallback(t *testing.T) {
	audio := wav.SyntheticTone(wav.ToneSampleRate)
	withVAD := NewStoreWithOptions(filepath.Join(t.TempDir(), "voices"), StoreOptions{
		AnalyzeVAD: func([]byte) (time.Duration, error) { return 750 * time.Millisecond, nil },
	})
	clone, err := withVAD.Save("VAD", "words", audio, false)
	if err != nil {
		t.Fatal(err)
	}
	if clone.Analysis.VADStatus != "used" || clone.Analysis.Method != "configured-vad+pcm-v1" || clone.Analysis.UsableSpeechSeconds != 0.75 {
		t.Fatalf("configured VAD was not retained: %+v", clone.Analysis)
	}

	fallback := NewStoreWithOptions(filepath.Join(t.TempDir(), "voices"), StoreOptions{
		AnalyzeVAD: func([]byte) (time.Duration, error) { return 0, errors.New("vad unavailable") },
	})
	clone, err = fallback.Save("Fallback", "words", audio, false)
	if err != nil {
		t.Fatal(err)
	}
	if clone.Analysis.VADStatus != "failed" || clone.Analysis.Method != "pcm-heuristic-v1" || clone.Analysis.VADError != "vad unavailable" || len(clone.Analysis.Warnings) == 0 {
		t.Fatalf("failed VAD fallback was not disclosed: %+v", clone.Analysis)
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

func TestUserCanAuthorCharacterVoicesBeneathOneActorVoice(t *testing.T) {
	root := filepath.Join(t.TempDir(), "voices")
	store := NewStore(root)
	actor, err := store.Save("Mara", "reference words", validWAVBytes(), false)
	if err != nil {
		t.Fatalf("save Actor Voice: %v", err)
	}

	keeper, err := store.CreateCharacterVoice(actor.ID, "Weathered keeper", "elderly, low pitch")
	if err != nil {
		t.Fatalf("create first Character Voice: %v", err)
	}
	guide, err := store.CreateCharacterVoice(actor.ID, "Harbour guide", "young adult, upbeat")
	if err != nil {
		t.Fatalf("create second Character Voice: %v", err)
	}
	if keeper.ID == guide.ID || keeper.ActorVoiceID != actor.ID || keeper.CreatedAt.IsZero() || keeper.UpdatedAt.IsZero() {
		t.Fatalf("unexpected Character Voices: keeper=%+v guide=%+v", keeper, guide)
	}
	if _, err := os.Stat(filepath.Join(root, ".characters", keeper.ID, referenceWAVName)); !os.IsNotExist(err) {
		t.Fatalf("Character Voice duplicated Actor Voice reference: %v", err)
	}

	characters, err := NewStore(root).ListCharacterVoices(actor.ID)
	if err != nil {
		t.Fatalf("list Character Voices: %v", err)
	}
	if len(characters) != 2 {
		t.Fatalf("expected two Character Voices, got %+v", characters)
	}

	updated, err := store.UpdateCharacterVoice(keeper.ID, "Weathered lighthouse keeper", "elderly, whisper")
	if err != nil {
		t.Fatalf("edit Character Voice: %v", err)
	}
	if updated.Name != "Weathered lighthouse keeper" || updated.Direction != "elderly, whisper" ||
		updated.ActorVoiceID != actor.ID || !updated.CreatedAt.Equal(keeper.CreatedAt) || !updated.UpdatedAt.After(keeper.UpdatedAt) {
		t.Fatalf("unexpected updated Character Voice: %+v", updated)
	}

	if err := store.Delete(actor.ID); !errors.Is(err, ErrActorHasCharacters) {
		t.Fatalf("delete Actor Voice with children error = %v, want ErrActorHasCharacters", err)
	}
	if err := store.DeleteCharacterVoice(keeper.ID); err != nil {
		t.Fatalf("delete Character Voice: %v", err)
	}
	if _, ok, err := store.LoadCharacterVoice(keeper.ID); err != nil || ok {
		t.Fatalf("deleted Character Voice still loads: ok=%v err=%v", ok, err)
	}
	remaining, err := store.ListCharacterVoices(actor.ID)
	if err != nil || len(remaining) != 1 || remaining[0].ID != guide.ID {
		t.Fatalf("unexpected remaining Character Voices: %+v err=%v", remaining, err)
	}
}

func TestCharacterVoiceValidationRejectsInvalidParentsAndFields(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "voices"))
	actor, err := store.Save("Mara", "reference words", validWAVBytes(), false)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name      string
		actorID   string
		voiceName string
		direction string
	}{
		{name: "missing parent", actorID: "voice_missing", voiceName: "Keeper", direction: "elderly"},
		{name: "traversal parent", actorID: "../outside", voiceName: "Keeper", direction: "elderly"},
		{name: "missing name", actorID: actor.ID, direction: "elderly"},
		{name: "long name", actorID: actor.ID, voiceName: strings.Repeat("n", MaxCharacterVoiceNameChars+1), direction: "elderly"},
		{name: "missing direction", actorID: actor.ID, voiceName: "Keeper"},
		{name: "long direction", actorID: actor.ID, voiceName: "Keeper", direction: strings.Repeat("d", MaxCharacterVoiceDirectionChars+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := store.CreateCharacterVoice(tt.actorID, tt.voiceName, tt.direction); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	characters, err := store.ListCharacterVoices(actor.ID)
	if err != nil || len(characters) != 0 {
		t.Fatalf("rejected Character Voices left partial records: %+v err=%v", characters, err)
	}
}

func TestCharacterVoiceLimitsCountUnicodeCharacters(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "voices"))
	actor, err := store.Save("Mara", "reference words", validWAVBytes(), false)
	if err != nil {
		t.Fatal(err)
	}

	character, err := store.CreateCharacterVoice(
		actor.ID,
		strings.Repeat("é", MaxCharacterVoiceNameChars),
		strings.Repeat("灯", MaxCharacterVoiceDirectionChars),
	)
	if err != nil {
		t.Fatalf("valid Unicode Character Voice fields were rejected: %v", err)
	}
	if _, err := store.SaveCharacterPreview(
		character.ID,
		strings.Repeat("声", MaxCharacterPreviewTextChars),
		wav.SyntheticTone(160),
		character.UpdatedAt,
	); err != nil {
		t.Fatalf("valid Unicode preview text was rejected: %v", err)
	}
	if _, err := store.UpdateCharacterVoice(character.ID, strings.Repeat("é", MaxCharacterVoiceNameChars+1), "guarded"); err == nil {
		t.Fatal("overlong Unicode Character Voice name was accepted")
	}
}

func TestCharacterVoicePreviewIsReplaceableEvaluationData(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "voices"))
	actor, err := store.Save("Mara", "reference words", validWAVBytes(), false)
	if err != nil {
		t.Fatal(err)
	}
	character, err := store.CreateCharacterVoice(actor.ID, "Keeper", "elderly, low pitch")
	if err != nil {
		t.Fatal(err)
	}

	first, err := store.SaveCharacterPreview(character.ID, "First sample", wav.SyntheticTone(160), character.UpdatedAt)
	if err != nil {
		t.Fatalf("save first preview: %v", err)
	}
	firstPath, err := store.CharacterPreviewPath(first.ID)
	if err != nil {
		t.Fatalf("first preview path: %v", err)
	}
	if first.Preview == nil || first.Preview.SampleText != "First sample" {
		t.Fatalf("missing first preview metadata: %+v", first)
	}

	time.Sleep(time.Millisecond)
	second, err := store.SaveCharacterPreview(character.ID, "Replacement sample", wav.SyntheticTone(320), first.UpdatedAt)
	if err != nil {
		t.Fatalf("replace preview: %v", err)
	}
	secondPath, err := store.CharacterPreviewPath(second.ID)
	if err != nil {
		t.Fatalf("replacement preview path: %v", err)
	}
	if secondPath == firstPath || second.Preview == nil || second.Preview.SampleText != "Replacement sample" {
		t.Fatalf("preview was not replaced: first=%q second=%+v path=%q", firstPath, second, secondPath)
	}
	if _, err := os.Stat(firstPath); !os.IsNotExist(err) {
		t.Fatalf("superseded preview remains addressable: %v", err)
	}

	redirected, err := store.UpdateCharacterVoice(character.ID, "Keeper", "elderly, whisper")
	if err != nil {
		t.Fatal(err)
	}
	if redirected.Preview != nil {
		t.Fatalf("direction change retained stale preview: %+v", redirected.Preview)
	}
	if _, err := store.CharacterPreviewPath(character.ID); !errors.Is(err, ErrCharacterPreviewNotFound) {
		t.Fatalf("preview path after direction change error = %v, want ErrCharacterPreviewNotFound", err)
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
