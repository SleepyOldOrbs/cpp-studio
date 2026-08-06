package voice

import (
	"context"
	"errors"

	"cpp-studio/internal/engine"
)

// CharacterSpeakFunc is the speech-generation seam used to evaluate one
// Character Voice with its parent Actor Voice reference.
type CharacterSpeakFunc func(context.Context, engine.SynthesisRequest, *engine.Voice) ([]byte, error)

// CharacterAuthoring owns the HTTP-independent Character Voice preview flow.
type CharacterAuthoring struct {
	Store *Store
	Speak CharacterSpeakFunc
}

// GeneratePreview resolves the Character and Actor Voices, synthesizes the
// directed sample, then publishes it only after synthesis succeeds.
func (a CharacterAuthoring) GeneratePreview(ctx context.Context, id, sampleText string) (CharacterVoice, error) {
	if a.Store == nil || a.Speak == nil {
		return CharacterVoice{}, errors.New("Character Voice preview is not configured")
	}
	sampleText, err := validateCharacterPreviewText(sampleText)
	if err != nil {
		return CharacterVoice{}, err
	}
	character, ok, err := a.Store.LoadCharacterVoice(id)
	if err != nil {
		return CharacterVoice{}, err
	}
	if !ok {
		return CharacterVoice{}, ErrCharacterNotFound
	}
	actor, ok, err := a.Store.Load(character.ActorVoiceID)
	if err != nil {
		return CharacterVoice{}, err
	}
	if !ok {
		return CharacterVoice{}, ErrActorVoiceNotFound
	}
	refPath, err := a.Store.ReferencePath(actor.ID)
	if err != nil {
		return CharacterVoice{}, err
	}
	audio, err := a.Speak(ctx, engine.SynthesisRequest{
		Text: sampleText, EngineID: "omnivoice", Direction: character.Direction,
	}, &engine.Voice{RefWAVPath: refPath, RefText: actor.Transcript})
	if err != nil {
		return CharacterVoice{}, err
	}
	return a.Store.SaveCharacterPreview(id, sampleText, audio, character.UpdatedAt)
}
