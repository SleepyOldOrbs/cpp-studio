package voice

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"cpp-studio/internal/engine"
	"cpp-studio/internal/wav"
)

func TestCharacterAuthoringGeneratesPreviewThroughInjectedSpeech(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "voices"))
	actor, err := store.Save("Avery", "reference words", validWAVBytes(), false)
	if err != nil {
		t.Fatal(err)
	}
	character, err := store.CreateCharacterVoice(actor.ID, "Mara", "weathered and guarded")
	if err != nil {
		t.Fatal(err)
	}

	called := false
	authoring := CharacterAuthoring{
		Store: store,
		Speak: func(_ context.Context, request engine.SynthesisRequest, actorVoice *engine.Voice) ([]byte, error) {
			called = true
			if request.EngineID != "omnivoice" || request.Text != "Keep the lamp lit." || request.Direction != character.Direction {
				t.Fatalf("unexpected synthesis request: %+v", request)
			}
			if actorVoice == nil || actorVoice.RefText != actor.Transcript || actorVoice.RefWAVPath == "" {
				t.Fatalf("missing Actor Voice reference: %+v", actorVoice)
			}
			return wav.SyntheticTone(160), nil
		},
	}
	previewed, err := authoring.GeneratePreview(context.Background(), character.ID, "Keep the lamp lit.")
	if err != nil {
		t.Fatalf("generate Character Voice preview: %v", err)
	}
	if !called || previewed.Preview == nil || previewed.Preview.SampleText != "Keep the lamp lit." {
		t.Fatalf("preview was not durably published: called=%v character=%+v", called, previewed)
	}
}

func TestCharacterAuthoringDoesNotPublishFailedSynthesis(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "voices"))
	actor, err := store.Save("Avery", "reference words", validWAVBytes(), false)
	if err != nil {
		t.Fatal(err)
	}
	character, err := store.CreateCharacterVoice(actor.ID, "Mara", "weathered and guarded")
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("speech busy")
	authoring := CharacterAuthoring{
		Store: store,
		Speak: func(context.Context, engine.SynthesisRequest, *engine.Voice) ([]byte, error) {
			return nil, wantErr
		},
	}
	if _, err := authoring.GeneratePreview(context.Background(), character.ID, "Keep the lamp lit."); !errors.Is(err, wantErr) {
		t.Fatalf("generate error = %v, want %v", err, wantErr)
	}
	loaded, ok, err := store.LoadCharacterVoice(character.ID)
	if err != nil || !ok || loaded.Preview != nil {
		t.Fatalf("failed synthesis partially published a preview: ok=%v err=%v character=%+v", ok, err, loaded)
	}
}

func TestCharacterAuthoringRejectsPreviewWhenDirectionChangesDuringSynthesis(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "voices"))
	actor, err := store.Save("Avery", "reference words", validWAVBytes(), false)
	if err != nil {
		t.Fatal(err)
	}
	character, err := store.CreateCharacterVoice(actor.ID, "Mara", "quiet and guarded")
	if err != nil {
		t.Fatal(err)
	}
	authoring := CharacterAuthoring{
		Store: store,
		Speak: func(context.Context, engine.SynthesisRequest, *engine.Voice) ([]byte, error) {
			if _, err := store.UpdateCharacterVoice(character.ID, character.Name, "urgent and breathless"); err != nil {
				t.Fatalf("direction change during synthesis: %v", err)
			}
			return wav.SyntheticTone(160), nil
		},
	}
	if _, err := authoring.GeneratePreview(context.Background(), character.ID, "Keep the lamp lit."); !errors.Is(err, ErrCharacterVoiceChanged) {
		t.Fatalf("generate error = %v, want ErrCharacterVoiceChanged", err)
	}
	loaded, ok, err := store.LoadCharacterVoice(character.ID)
	if err != nil || !ok || loaded.Direction != "urgent and breathless" || loaded.Preview != nil {
		t.Fatalf("stale preview overwrote direction change: ok=%v err=%v character=%+v", ok, err, loaded)
	}
}
