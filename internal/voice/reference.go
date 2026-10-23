package voice

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cpp-studio/internal/wav"
)

// SavedReference is an explicitly assigned recording. Characters without one
// continue to inherit their actor's reference.
type SavedReference struct {
	File       string             `json:"file"`
	Transcript string             `json:"transcript"`
	Source     *CloneSource       `json:"source,omitempty"`
	Analysis   *ReferenceAnalysis `json:"analysis"`
}

// ResolveReference returns one consistent reference/transcript snapshot.
func (s *Store) ResolveReference(id string) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resolveReference(id)
}

func (s *Store) resolveReference(id string) (string, string, error) {
	if strings.HasPrefix(id, "character_") {
		character, ok, err := s.LoadCharacterVoice(id)
		if err != nil {
			return "", "", err
		}
		if !ok {
			return "", "", ErrCharacterNotFound
		}
		if character.Reference != nil {
			path, err := referenceFilePath(filepath.Join(s.rootDir, characterVoicesDir, id), character.Reference.File)
			return path, character.Reference.Transcript, err
		}
		id = character.ActorVoiceID
	}
	actor, ok, err := s.load(id)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", ErrActorVoiceNotFound
	}
	file := referenceWAVName
	if actor.Reference != nil {
		file = actor.Reference.File
	}
	path, err := referenceFilePath(filepath.Join(s.rootDir, id), file)
	return path, actor.Transcript, err
}

func referenceFilePath(dir, name string) (string, error) {
	if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, `/\:`) || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid reference filename")
	}
	path := filepath.Join(dir, name)
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	if err := wav.ValidateHeader(file); err != nil {
		return "", err
	}
	return filepath.Abs(path)
}

// AssignReference publishes a new reference without overwriting earlier WAVs
// or manifests. A character assignment affects only that character.
func (s *Store) AssignReference(id, transcript string, audio []byte, source *CloneSource) error {
	transcript = strings.TrimSpace(transcript)
	if transcript == "" || len(transcript) > MaxVoiceTranscriptLen {
		return fmt.Errorf("a reference transcript of at most %d bytes is required", MaxVoiceTranscriptLen)
	}
	if len(audio) > MaxReferenceWAVBytes {
		return fmt.Errorf("reference wav is too large")
	}
	if err := wav.ValidateBytes(audio); err != nil {
		return fmt.Errorf("reference wav: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	ref := &SavedReference{File: fmt.Sprintf("reference-%d.wav", now.UnixNano()), Transcript: transcript, Source: source, Analysis: analyzeReference(audio, s.vad)}
	var manifest string
	var updated any
	if strings.HasPrefix(id, "character_") {
		character, ok, err := s.LoadCharacterVoice(id)
		if err != nil {
			return err
		}
		if !ok {
			return ErrCharacterNotFound
		}
		previous := character.Reference
		if previous == nil {
			if actor, ok, err := s.load(character.ActorVoiceID); err == nil && ok {
				previous = &SavedReference{Source: actor.Source, Analysis: actor.Analysis}
			}
		}
		if ref.Source == nil && previous != nil && previous.Analysis != nil && previous.Analysis.ContentSHA256 == ref.Analysis.ContentSHA256 {
			ref.Source = previous.Source
		}
		character.Reference, character.Preview, character.UpdatedAt = ref, nil, now
		manifest, updated = s.characterManifestPath(id), character
	} else {
		actor, ok, err := s.load(id)
		if err != nil {
			return err
		}
		if !ok {
			return ErrActorVoiceNotFound
		}
		if actor.Protected {
			return fmt.Errorf("protected Actor Voice reference cannot be changed")
		}
		// Re-transcribing the same recording must not discard its provenance.
		if ref.Source == nil && actor.Analysis != nil && actor.Analysis.ContentSHA256 == ref.Analysis.ContentSHA256 {
			ref.Source = actor.Source
		}
		actor.Reference, actor.Transcript, actor.Source, actor.Analysis = ref, transcript, ref.Source, ref.Analysis
		// Inherited previews no longer describe this actor's new recording.
		// Update their revision even without a preview to reject in-flight work.
		characters, err := s.listCharacterVoices(id, false)
		if err != nil {
			return err
		}
		for _, character := range characters {
			if character.Reference != nil {
				continue
			}
			character.Preview, character.UpdatedAt = nil, now
			if err := writeCharacterManifestAtomic(s.characterManifestPath(character.ID), character); err != nil {
				return err
			}
		}
		manifest, updated = filepath.Join(s.rootDir, id, "manifest.json"), actor
	}
	previous, err := os.ReadFile(manifest)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(updated, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(manifest)
	if err := s.writeFileAtomic(filepath.Join(dir, fmt.Sprintf("reference-before-%d.json", now.UnixNano())), previous); err != nil {
		return err
	}
	if err := s.writeFileAtomic(filepath.Join(dir, ref.File), audio); err != nil {
		return err
	}
	return s.writeFileAtomic(manifest, append(data, '\n'))
}
