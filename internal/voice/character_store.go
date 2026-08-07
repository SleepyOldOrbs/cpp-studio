package voice

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"cpp-studio/internal/wav"
)

const (
	MaxCharacterVoiceNameChars      = 80
	MaxCharacterVoiceDirectionChars = 500
	MaxCharacterPreviewTextChars    = 1000
	characterVoicesDir              = ".characters"
	characterManifestName           = "manifest.json"
)

var (
	ErrActorVoiceNotFound       = errors.New("Actor Voice not found")
	ErrCharacterNotFound        = errors.New("Character Voice not found")
	ErrCharacterPreviewNotFound = errors.New("Character Voice preview not found")
	ErrCharacterVoiceChanged    = errors.New("Character Voice changed while preview was generating; try again")
)

// CharacterVoice is one named performance beneath an Actor Voice. It owns
// direction and replaceable preview metadata, never a copy of the Actor's
// reference WAV.
type CharacterVoice struct {
	ID           string            `json:"id"`
	ActorVoiceID string            `json:"actor_voice_id"`
	Name         string            `json:"name"`
	Direction    string            `json:"direction"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	Preview      *CharacterPreview `json:"preview,omitempty"`
}

// CharacterPreview identifies the latest replaceable evaluation sample.
type CharacterPreview struct {
	SampleText string    `json:"sample_text"`
	UpdatedAt  time.Time `json:"updated_at"`
	FileName   string    `json:"-"`
}

// CharacterSynthesisIdentity is the durable voice choice a production needs
// to decide whether previously generated dialogue still matches the Actor
// Voice reference, transcript, and Character Voice direction.
type CharacterSynthesisIdentity struct {
	CharacterVoiceID string
	ActorVoiceID     string
	Direction        string
	Fingerprint      string
}

// ResolveCharacterSynthesisIdentity returns the current synthesis identity
// without treating display-name or preview changes as speech changes.
func (s *Store) ResolveCharacterSynthesisIdentity(id string) (CharacterSynthesisIdentity, bool, error) {
	character, ok, err := s.LoadCharacterVoice(id)
	if err != nil || !ok {
		return CharacterSynthesisIdentity{}, ok, err
	}
	actor, ok, err := s.Load(character.ActorVoiceID)
	if err != nil {
		return CharacterSynthesisIdentity{}, false, err
	}
	if !ok {
		return CharacterSynthesisIdentity{}, false, ErrActorVoiceNotFound
	}
	referenceIdentity := ""
	if actor.Analysis != nil {
		referenceIdentity = actor.Analysis.ContentSHA256
	}
	if referenceIdentity == "" {
		referenceWAV, err := os.ReadFile(filepath.Join(s.rootDir, actor.ID, referenceWAVName))
		if err != nil {
			return CharacterSynthesisIdentity{}, false, fmt.Errorf("read Actor Voice reference: %w", err)
		}
		referenceHash := sha256.Sum256(referenceWAV)
		referenceIdentity = hex.EncodeToString(referenceHash[:])
	}
	fingerprint := sha256.Sum256([]byte(strings.Join([]string{
		character.ID,
		actor.ID,
		referenceIdentity,
		actor.Transcript,
		character.Direction,
	}, "\x00")))
	return CharacterSynthesisIdentity{
		CharacterVoiceID: character.ID,
		ActorVoiceID:     actor.ID,
		Direction:        character.Direction,
		Fingerprint:      hex.EncodeToString(fingerprint[:]),
	}, true, nil
}

func (s *Store) CreateCharacterVoice(actorVoiceID, name, direction string) (CharacterVoice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok, err := s.Load(actorVoiceID); err != nil {
		return CharacterVoice{}, err
	} else if !ok {
		return CharacterVoice{}, ErrActorVoiceNotFound
	}
	name, direction, err := validateCharacterVoiceFields(name, direction)
	if err != nil {
		return CharacterVoice{}, err
	}
	if err := os.MkdirAll(filepath.Join(s.rootDir, characterVoicesDir), 0o755); err != nil {
		return CharacterVoice{}, fmt.Errorf("create Character Voices directory: %w", err)
	}

	now := time.Now().UTC()
	id, err := newCharacterVoiceID(now)
	if err != nil {
		return CharacterVoice{}, err
	}
	character := CharacterVoice{
		ID: id, ActorVoiceID: actorVoiceID, Name: name, Direction: direction,
		CreatedAt: now, UpdatedAt: now,
	}
	root := filepath.Join(s.rootDir, characterVoicesDir)
	stagingDir, err := os.MkdirTemp(root, "."+id+".tmp-")
	if err != nil {
		return CharacterVoice{}, fmt.Errorf("stage Character Voice: %w", err)
	}
	defer os.RemoveAll(stagingDir)
	if err := writeCharacterManifest(filepath.Join(stagingDir, characterManifestName), character); err != nil {
		return CharacterVoice{}, err
	}
	if err := os.Rename(stagingDir, filepath.Join(root, id)); err != nil {
		return CharacterVoice{}, fmt.Errorf("publish Character Voice: %w", err)
	}
	return character, nil
}

func (s *Store) LoadCharacterVoice(id string) (CharacterVoice, bool, error) {
	if !validCharacterVoiceID(id) {
		return CharacterVoice{}, false, nil
	}
	data, err := os.ReadFile(s.characterManifestPath(id))
	if os.IsNotExist(err) {
		return CharacterVoice{}, false, nil
	}
	if err != nil {
		return CharacterVoice{}, false, fmt.Errorf("read Character Voice: %w", err)
	}
	var character CharacterVoice
	if err := json.Unmarshal(data, &character); err != nil {
		return CharacterVoice{}, false, fmt.Errorf("decode Character Voice: %w", err)
	}
	if character.ID != id || !validCharacterVoiceID(character.ID) || validateVoiceID(character.ActorVoiceID) != nil {
		return CharacterVoice{}, false, fmt.Errorf("decode Character Voice: invalid identity")
	}
	if character.Preview != nil {
		character.Preview.FileName = previewFileName(character.Preview.UpdatedAt)
	}
	return character, true, nil
}

func (s *Store) ListCharacterVoices(actorVoiceID string) ([]CharacterVoice, error) {
	return s.listCharacterVoices(actorVoiceID, true)
}

func (s *Store) listCharacterVoices(actorVoiceID string, requireActor bool) ([]CharacterVoice, error) {
	if requireActor {
		if _, ok, err := s.Load(actorVoiceID); err != nil {
			return nil, err
		} else if !ok {
			return nil, ErrActorVoiceNotFound
		}
	}
	root := filepath.Join(s.rootDir, characterVoicesDir)
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return []CharacterVoice{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list Character Voices: %w", err)
	}
	characters := make([]CharacterVoice, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		character, ok, err := s.LoadCharacterVoice(entry.Name())
		if err != nil {
			return nil, err
		}
		if ok && character.ActorVoiceID == actorVoiceID {
			characters = append(characters, character)
		}
	}
	sort.Slice(characters, func(i, j int) bool {
		if characters[i].CreatedAt.Equal(characters[j].CreatedAt) {
			return characters[i].ID < characters[j].ID
		}
		return characters[i].CreatedAt.Before(characters[j].CreatedAt)
	})
	return characters, nil
}

func (s *Store) UpdateCharacterVoice(id, name, direction string) (CharacterVoice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name, direction, err := validateCharacterVoiceFields(name, direction)
	if err != nil {
		return CharacterVoice{}, err
	}
	character, ok, err := s.LoadCharacterVoice(id)
	if err != nil {
		return CharacterVoice{}, err
	}
	if !ok {
		return CharacterVoice{}, ErrCharacterNotFound
	}
	previousPreview := character.Preview
	if character.Direction != direction {
		character.Preview = nil
	}
	character.Name = name
	character.Direction = direction
	character.UpdatedAt = time.Now().UTC()
	if err := writeCharacterManifestAtomic(s.characterManifestPath(id), character); err != nil {
		return CharacterVoice{}, fmt.Errorf("update Character Voice: %w", err)
	}
	if previousPreview != nil && character.Preview == nil {
		_ = os.Remove(filepath.Join(s.rootDir, characterVoicesDir, id, previewFileName(previousPreview.UpdatedAt)))
	}
	return character, nil
}

func (s *Store) SaveCharacterPreview(id, sampleText string, previewWAV []byte, expectedUpdatedAt time.Time) (CharacterVoice, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sampleText, err := validateCharacterPreviewText(sampleText)
	if err != nil {
		return CharacterVoice{}, err
	}
	if len(previewWAV) > MaxReferenceWAVBytes {
		return CharacterVoice{}, fmt.Errorf("preview wav is %d bytes, max is %d bytes", len(previewWAV), MaxReferenceWAVBytes)
	}
	if err := wav.ValidateBytes(previewWAV); err != nil {
		return CharacterVoice{}, fmt.Errorf("preview wav: %w", err)
	}
	character, ok, err := s.LoadCharacterVoice(id)
	if err != nil {
		return CharacterVoice{}, err
	}
	if !ok {
		return CharacterVoice{}, ErrCharacterNotFound
	}
	if !character.UpdatedAt.Equal(expectedUpdatedAt) {
		return CharacterVoice{}, ErrCharacterVoiceChanged
	}

	previousPreview := character.Preview
	now := time.Now().UTC()
	preview := &CharacterPreview{SampleText: sampleText, UpdatedAt: now, FileName: previewFileName(now)}
	previewPath := filepath.Join(s.rootDir, characterVoicesDir, id, preview.FileName)
	if err := writeVoiceFileAtomic(previewPath, previewWAV); err != nil {
		return CharacterVoice{}, fmt.Errorf("write Character Voice preview: %w", err)
	}
	character.Preview = preview
	character.UpdatedAt = now
	if err := writeCharacterManifestAtomic(s.characterManifestPath(id), character); err != nil {
		_ = os.Remove(previewPath)
		return CharacterVoice{}, fmt.Errorf("publish Character Voice preview: %w", err)
	}
	if previousPreview != nil {
		oldPath := filepath.Join(s.rootDir, characterVoicesDir, id, previewFileName(previousPreview.UpdatedAt))
		if oldPath != previewPath {
			_ = os.Remove(oldPath)
		}
	}
	return character, nil
}

// CharacterPreviewPath returns the latest preview WAV selected by the
// Character Voice manifest.
func (s *Store) CharacterPreviewPath(id string) (string, error) {
	character, ok, err := s.LoadCharacterVoice(id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrCharacterNotFound
	}
	if character.Preview == nil {
		return "", ErrCharacterPreviewNotFound
	}
	path := filepath.Join(s.rootDir, characterVoicesDir, id, previewFileName(character.Preview.UpdatedAt))
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return "", ErrCharacterPreviewNotFound
	}
	if err != nil {
		return "", err
	}
	defer file.Close()
	if err := wav.ValidateHeader(file); err != nil {
		return "", fmt.Errorf("Character Voice preview is not a valid WAV")
	}
	return filepath.Abs(path)
}

func (s *Store) DeleteCharacterVoice(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok, err := s.LoadCharacterVoice(id); err != nil {
		return err
	} else if !ok {
		return ErrCharacterNotFound
	}
	if err := os.RemoveAll(filepath.Join(s.rootDir, characterVoicesDir, id)); err != nil {
		return fmt.Errorf("delete Character Voice: %w", err)
	}
	return nil
}

func (s *Store) characterManifestPath(id string) string {
	return filepath.Join(s.rootDir, characterVoicesDir, id, characterManifestName)
}

func validateCharacterVoiceFields(name, direction string) (string, string, error) {
	name = strings.TrimSpace(name)
	direction = strings.TrimSpace(direction)
	if name == "" {
		return "", "", fmt.Errorf("Character Voice name is required")
	}
	if utf8.RuneCountInString(name) > MaxCharacterVoiceNameChars {
		return "", "", fmt.Errorf("Character Voice name cannot exceed %d characters", MaxCharacterVoiceNameChars)
	}
	if direction == "" {
		return "", "", fmt.Errorf("Character Voice direction is required")
	}
	if utf8.RuneCountInString(direction) > MaxCharacterVoiceDirectionChars {
		return "", "", fmt.Errorf("Character Voice direction cannot exceed %d characters", MaxCharacterVoiceDirectionChars)
	}
	return name, direction, nil
}

func validateCharacterPreviewText(sampleText string) (string, error) {
	sampleText = strings.TrimSpace(sampleText)
	if sampleText == "" {
		return "", fmt.Errorf("preview sample text is required")
	}
	if utf8.RuneCountInString(sampleText) > MaxCharacterPreviewTextChars {
		return "", fmt.Errorf("preview sample text cannot exceed %d characters", MaxCharacterPreviewTextChars)
	}
	return sampleText, nil
}

func validCharacterVoiceID(id string) bool {
	if !strings.HasPrefix(id, "character_") || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") || filepath.IsAbs(id) {
		return false
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func newCharacterVoiceID(now time.Time) (string, error) {
	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("generate Character Voice id: %w", err)
	}
	return fmt.Sprintf("character_%s_%s", now.Format("20060102_150405"), hex.EncodeToString(suffix)), nil
}

func writeCharacterManifest(path string, character CharacterVoice) error {
	data, err := marshalCharacterManifest(character)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write Character Voice: %w", err)
	}
	return nil
}

func writeCharacterManifestAtomic(path string, character CharacterVoice) error {
	data, err := marshalCharacterManifest(character)
	if err != nil {
		return err
	}
	return writeVoiceFileAtomic(path, data)
}

func marshalCharacterManifest(character CharacterVoice) ([]byte, error) {
	data, err := json.MarshalIndent(character, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode Character Voice: %w", err)
	}
	return append(data, '\n'), nil
}

func writeVoiceFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	deadline := time.Now().Add(time.Second)
	for {
		err := os.Rename(tmpPath, path)
		if err == nil {
			return nil
		}
		if runtime.GOOS != "windows" || time.Now().After(deadline) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func previewFileName(updatedAt time.Time) string {
	return "preview-" + updatedAt.UTC().Format("20060102T150405.000000000") + ".wav"
}
