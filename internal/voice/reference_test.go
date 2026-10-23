package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cpp-studio/internal/engine"
	"cpp-studio/internal/wav"
)

func TestAssignedCharacterReferenceIsIsolatedAndUsedForSynthesis(t *testing.T) {
	store := NewStore(t.TempDir())
	original := wav.SyntheticTone(1600)
	actor, err := store.Save("Actor", "original words", original, false)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.CreateCharacterVoice(actor.ID, "First", "quiet")
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := store.CreateCharacterVoice(actor.ID, "Sibling", "loud")
	if err != nil {
		t.Fatal(err)
	}
	beforeFirst, _, _ := store.ResolveCharacterSynthesisIdentity(first.ID)
	beforeSibling, _, _ := store.ResolveCharacterSynthesisIdentity(sibling.ID)
	originalPath, _, _ := store.ResolveReference(actor.ID)
	_, err = store.SaveCharacterPreview(first.ID, "preview", original, first.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	assigned := wav.SyntheticTone(3200)
	if err := store.AssignReference(first.ID, "character words", assigned, nil); err != nil {
		t.Fatal(err)
	}
	path, transcript, err := store.ResolveReference(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if path == originalPath || transcript != "character words" || !bytes.Equal(data, assigned) {
		t.Fatal("character override not resolved")
	}
	for _, id := range []string{actor.ID, sibling.ID} {
		path, transcript, err := store.ResolveReference(id)
		if err != nil || path != originalPath || transcript != "original words" {
			t.Fatalf("changed unrelated voice %s: %v", id, err)
		}
	}
	firstNow, _, _ := store.LoadCharacterVoice(first.ID)
	if firstNow.Preview != nil {
		t.Fatal("stale character preview retained")
	}
	afterFirst, _, _ := store.ResolveCharacterSynthesisIdentity(first.ID)
	afterSibling, _, _ := store.ResolveCharacterSynthesisIdentity(sibling.ID)
	if !afterFirst.ReferenceReassigned || afterSibling.ReferenceReassigned {
		t.Fatal("incorrect retained take reuse boundary")
	}
	if beforeFirst.Fingerprint == afterFirst.Fingerprint || beforeSibling.Fingerprint != afterSibling.Fingerprint {
		t.Fatal("incorrect synthesis invalidation")
	}
	authoring := CharacterAuthoring{Store: store, Speak: func(_ context.Context, _ engine.SynthesisRequest, ref *engine.Voice) ([]byte, error) {
		if ref.RefWAVPath != path || ref.RefText != "character words" {
			t.Fatalf("preview used wrong reference: %+v", ref)
		}
		return original, nil
	}}
	if _, err := authoring.GeneratePreview(context.Background(), first.ID, "sample"); err != nil {
		t.Fatal(err)
	}
	if err := store.AssignReference(actor.ID, "new actor words", wav.SyntheticTone(4800), nil); err != nil {
		t.Fatal(err)
	}
	_, firstText, _ := store.ResolveReference(first.ID)
	_, siblingText, _ := store.ResolveReference(sibling.ID)
	if firstText != "character words" || siblingText != "new actor words" {
		t.Fatal("actor assignment replaced a character override or missed an inheriting sibling")
	}
	oldAudio, _ := os.ReadFile(originalPath)
	if !bytes.Equal(original, oldAudio) {
		t.Fatal("original WAV overwritten")
	}
	backups, _ := filepath.Glob(filepath.Join(store.rootDir, actor.ID, "reference-before-*.json"))
	if len(backups) != 1 {
		t.Fatal("original manifest not retained")
	}
}

func TestReferenceAssignmentFailurePreservesCurrentReference(t *testing.T) {
	store := NewStore(t.TempDir())
	audio := wav.SyntheticTone(1600)
	actor, err := store.Save("Actor", "original", audio, false)
	if err != nil {
		t.Fatal(err)
	}
	path, _, _ := store.ResolveReference(actor.ID)
	if err := store.AssignReference(actor.ID, "invalid", []byte("bad wav"), nil); err == nil {
		t.Fatal("invalid WAV accepted")
	}
	store.writeFileAtomic = func(name string, data []byte) error {
		if filepath.Base(name) == "manifest.json" {
			return errors.New("publish failure")
		}
		return writeVoiceFileAtomic(name, data)
	}
	if err := store.AssignReference(actor.ID, "new", wav.SyntheticTone(3200), nil); err == nil {
		t.Fatal("publication failure ignored")
	}
	current, text, err := store.ResolveReference(actor.ID)
	if err != nil || current != path || text != "original" {
		t.Fatal("failed assignment changed the reference")
	}
}

func TestFailedActorAssignmentPreservesInheritedPreviews(t *testing.T) {
	for _, stage := range []string{"backup", "wav", "manifest"} {
		t.Run(stage, func(t *testing.T) {
			store := NewStore(t.TempDir())
			audio := wav.SyntheticTone(1600)
			actor, err := store.Save("Actor", "original words", audio, false)
			if err != nil {
				t.Fatal(err)
			}
			child, err := store.CreateCharacterVoice(actor.ID, "Child", "calm")
			if err != nil {
				t.Fatal(err)
			}
			child, err = store.SaveCharacterPreview(child.ID, "sample", audio, child.UpdatedAt)
			if err != nil {
				t.Fatal(err)
			}
			manifest := store.characterManifestPath(child.ID)
			before, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			previewPath, err := store.CharacterPreviewPath(child.ID)
			if err != nil {
				t.Fatal(err)
			}
			store.writeFileAtomic = func(path string, data []byte) error {
				name := filepath.Base(path)
				if (stage == "backup" && strings.HasPrefix(name, "reference-before-")) ||
					(stage == "wav" && strings.HasSuffix(name, ".wav")) ||
					(stage == "manifest" && name == "manifest.json") {
					return errors.New("injected publication failure")
				}
				return writeVoiceFileAtomic(path, data)
			}
			if err := store.AssignReference(actor.ID, "replacement", wav.SyntheticTone(3200), nil); err == nil {
				t.Fatal("expected failure")
			}
			_, transcript, err := store.ResolveReference(actor.ID)
			if err != nil || transcript != "original words" {
				t.Fatalf("old reference: %q %v", transcript, err)
			}
			after, err := os.ReadFile(manifest)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("failed assignment changed child manifest")
			}
			// Verify the same state survives reopening the store, not just in-memory reads.
			reopened := NewStore(store.rootDir)
			saved, _, err := reopened.LoadCharacterVoice(child.ID)
			if err != nil || saved.Preview == nil || !saved.UpdatedAt.Equal(child.UpdatedAt) {
				t.Fatalf("child preview/revision changed: %+v %v", saved, err)
			}
			currentPath, err := reopened.CharacterPreviewPath(child.ID)
			if err != nil || currentPath != previewPath {
				t.Fatalf("preview inaccessible: %s %v", currentPath, err)
			}
		})
	}
}

func TestCharacterReferenceRevisionReadDoesNotMigrateActor(t *testing.T) {
	store := NewStore(t.TempDir())
	actor, err := store.Save("Actor", "original", wav.SyntheticTone(1600), false)
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.CreateCharacterVoice(actor.ID, "Child", "calm")
	if err != nil {
		t.Fatal(err)
	}
	// Legacy actors have no analysis. Reading a child must not trigger a
	// migration write that could overwrite a concurrent actor assignment.
	actor.Analysis = nil
	data, err := json.Marshal(actor)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.rootDir, actor.ID, "manifest.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	store.writeFileAtomic = func(string, []byte) error {
		t.Error("character read attempted a manifest write")
		return errors.New("unexpected write")
	}
	if _, ok, err := store.LoadCharacterVoice(child.ID); err != nil || !ok {
		t.Fatalf("load legacy actor's child: %v %v", ok, err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, after) {
		t.Fatal("character read changed actor manifest")
	}
}

func TestCommittedActorReferenceInvalidatesInheritedPreviewRevisions(t *testing.T) {
	store := NewStore(t.TempDir())
	audio := wav.SyntheticTone(1600)
	actor, err := store.Save("Actor", "original", audio, false)
	if err != nil {
		t.Fatal(err)
	}
	var children []CharacterVoice
	for _, name := range []string{"Previewed", "In flight", "Override"} {
		child, err := store.CreateCharacterVoice(actor.ID, name, "calm")
		if err != nil {
			t.Fatal(err)
		}
		if name == "Override" {
			if err := store.AssignReference(child.ID, "own reference", audio, nil); err != nil {
				t.Fatal(err)
			}
			child, _, err = store.LoadCharacterVoice(child.ID)
			if err != nil {
				t.Fatal(err)
			}
		}
		if name != "In flight" {
			child, err = store.SaveCharacterPreview(child.ID, "sample", audio, child.UpdatedAt)
			if err != nil {
				t.Fatal(err)
			}
		}
		children = append(children, child)
	}
	if err := store.AssignReference(actor.ID, "replacement", wav.SyntheticTone(3200), nil); err != nil {
		t.Fatal(err)
	}
	store = NewStore(store.rootDir)
	for _, before := range children {
		after, _, err := store.LoadCharacterVoice(before.ID)
		if err != nil {
			t.Fatal(err)
		}
		if before.Reference != nil {
			if after.Preview == nil || !after.UpdatedAt.Equal(before.UpdatedAt) {
				t.Fatal("changed independent character preview")
			}
			continue
		}
		if after.Preview != nil || !after.UpdatedAt.After(before.UpdatedAt) {
			t.Fatal("inherited preview/revision was not invalidated")
		}
		if _, err := store.CharacterPreviewPath(before.ID); !errors.Is(err, ErrCharacterPreviewNotFound) {
			t.Fatalf("stale preview is accessible: %v", err)
		}
		if _, err := store.SaveCharacterPreview(before.ID, "old in-flight result", audio, before.UpdatedAt); !errors.Is(err, ErrCharacterVoiceChanged) {
			t.Fatalf("stale synthesis accepted: %v", err)
		}
		if _, err := store.SaveCharacterPreview(before.ID, "fresh result", audio, after.UpdatedAt); err != nil {
			t.Fatal(err)
		}
		if _, err := NewStore(store.rootDir).CharacterPreviewPath(before.ID); err != nil {
			t.Fatalf("fresh preview inaccessible after reopen: %v", err)
		}
	}
}

func TestCreateClonedCharacterPublishesOwnReferenceAtomically(t *testing.T) {
	store := NewStore(t.TempDir())
	original := wav.SyntheticTone(1600)
	actor, err := store.Save("Actor", "actor words", original, false)
	if err != nil {
		t.Fatal(err)
	}
	audio := wav.SyntheticTone(3200)
	child, err := store.CreateClonedCharacterVoice(actor.ID, "Child", "child words", audio, nil)
	if err != nil {
		t.Fatal(err)
	}
	path, transcript, err := store.ResolveReference(child.ID)
	if err != nil || transcript != "child words" {
		t.Fatalf("reference: %s %v", transcript, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(data, audio) {
		t.Fatal("child recording missing")
	}
	_, actorTranscript, _ := store.ResolveReference(actor.ID)
	if actorTranscript != "actor words" {
		t.Fatal("parent changed")
	}
	// A failed file write must not publish a child that falls back to its actor.
	store.writeFileAtomic = func(string, []byte) error { return errors.New("disk full") }
	if _, err := store.CreateClonedCharacterVoice(actor.ID, "Failed", "new words", audio, nil); err == nil {
		t.Fatal("expected write failure")
	}
	for _, input := range []struct {
		transcript string
		audio      []byte
	}{{"", audio}, {"words", []byte("bad WAV")}} {
		if _, err := store.CreateClonedCharacterVoice(actor.ID, "Invalid", input.transcript, input.audio, nil); err == nil {
			t.Fatal("expected validation error")
		}
	}
	children, err := store.ListCharacterVoices(actor.ID)
	if err != nil || len(children) != 1 || children[0].ID != child.ID {
		t.Fatalf("partial child published: %+v %v", children, err)
	}
}

func TestRegeneratedReferenceRetainsProvenanceForSameRecording(t *testing.T) {
	store := NewStore(t.TempDir())
	audio := wav.SyntheticTone(1600)
	source := &CloneSource{Name: "Original recording", Speaker: "B", StartSec: 3, EndSec: 9}
	actor, err := store.SaveWithSource("Actor", "old words", audio, false, source)
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.CreateCharacterVoice(actor.ID, "Child", "natural")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{actor.ID, child.ID, child.ID} {
		if err := store.AssignReference(id, "regenerated words", audio, nil); err != nil {
			t.Fatal(err)
		}
	}
	savedActor, _, _ := store.Load(actor.ID)
	savedChild, _, _ := store.LoadCharacterVoice(child.ID)
	if savedActor.Source == nil || *savedActor.Source != *source || savedChild.Reference.Source == nil || *savedChild.Reference.Source != *source {
		t.Fatal("regeneration lost reference provenance")
	}
	if err := store.AssignReference(child.ID, "different words", wav.SyntheticTone(3200), nil); err != nil {
		t.Fatal(err)
	}
	savedChild, _, _ = store.LoadCharacterVoice(child.ID)
	if savedChild.Reference.Source != nil {
		t.Fatal("new recording retained unrelated provenance")
	}
}
