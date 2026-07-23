package voice

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cpp-studio/internal/wav"
)

const (
	DefaultVoicesRootDir  = "out/voices"
	MaxReferenceWAVBytes  = 32 * 1024 * 1024
	MaxVoiceNameChars     = 80
	MaxVoiceTranscriptLen = 4000
	referenceWAVName      = "ref.wav"
)

// Clone is one stored cloned voice: a reference WAV on disk plus the
// transcript the TTS engine conditions on. Protected voices refuse
// deletion — for curated voices that should survive library housekeeping.
type Clone struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Transcript string    `json:"transcript"`
	CreatedAt  time.Time `json:"created_at"`
	Protected  bool      `json:"protected,omitempty"`
}

// ErrProtected reports a deletion attempt on a protected voice.
var ErrProtected = errors.New("voice is protected and cannot be deleted")

// Store persists cloned voices, one directory per voice holding ref.wav and
// manifest.json, in the same shape as the story store.
type Store struct {
	rootDir string
}

func NewStore(rootDir string) *Store {
	if rootDir == "" {
		rootDir = DefaultVoicesRootDir
	}
	return &Store{rootDir: rootDir}
}

// Save validates and persists a new cloned voice, returning it with a fresh
// ID. The name and transcript arrive already trimmed by the caller.
// Protected voices refuse later deletion.
func (s *Store) Save(name string, transcript string, refWAV []byte, protected bool) (Clone, error) {
	if name == "" {
		return Clone{}, fmt.Errorf("voice name is required")
	}
	if len(name) > MaxVoiceNameChars {
		return Clone{}, fmt.Errorf("voice name cannot exceed %d characters", MaxVoiceNameChars)
	}
	if transcript == "" {
		return Clone{}, fmt.Errorf("voice transcript is required")
	}
	if len(transcript) > MaxVoiceTranscriptLen {
		return Clone{}, fmt.Errorf("voice transcript cannot exceed %d characters", MaxVoiceTranscriptLen)
	}
	if len(refWAV) > MaxReferenceWAVBytes {
		return Clone{}, fmt.Errorf("reference wav is %d bytes, max is %d bytes", len(refWAV), MaxReferenceWAVBytes)
	}
	if err := wav.ValidateBytes(refWAV); err != nil {
		return Clone{}, fmt.Errorf("reference wav: %w", err)
	}
	if err := os.MkdirAll(s.rootDir, 0o755); err != nil {
		return Clone{}, fmt.Errorf("create voices dir: %w", err)
	}

	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		return Clone{}, fmt.Errorf("generate voice id: %w", err)
	}
	clone := Clone{
		ID:         fmt.Sprintf("voice_%s_%s", time.Now().UTC().Format("20060102_150405"), hex.EncodeToString(suffix)),
		Name:       name,
		Transcript: transcript,
		CreatedAt:  time.Now().UTC(),
		Protected:  protected,
	}

	tmpDir := filepath.Join(s.rootDir, "."+clone.ID+".tmp")
	_ = os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return Clone{}, fmt.Errorf("create temp voice dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.WriteFile(filepath.Join(tmpDir, referenceWAVName), refWAV, 0o644); err != nil {
		return Clone{}, fmt.Errorf("write reference wav: %w", err)
	}
	data, err := json.MarshalIndent(clone, "", "  ")
	if err != nil {
		return Clone{}, fmt.Errorf("encode voice manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "manifest.json"), append(data, '\n'), 0o644); err != nil {
		return Clone{}, fmt.Errorf("write voice manifest: %w", err)
	}
	if err := os.Rename(tmpDir, filepath.Join(s.rootDir, clone.ID)); err != nil {
		return Clone{}, fmt.Errorf("finalize voice dir: %w", err)
	}
	return clone, nil
}

func (s *Store) Load(id string) (Clone, bool, error) {
	if err := validateVoiceID(id); err != nil {
		return Clone{}, false, nil
	}
	data, err := os.ReadFile(filepath.Join(s.rootDir, id, "manifest.json"))
	if os.IsNotExist(err) {
		return Clone{}, false, nil
	}
	if err != nil {
		return Clone{}, false, fmt.Errorf("read voice manifest: %w", err)
	}
	var clone Clone
	if err := json.Unmarshal(data, &clone); err != nil {
		return Clone{}, false, fmt.Errorf("decode voice manifest: %w", err)
	}
	return clone, true, nil
}

func (s *Store) List() ([]Clone, error) {
	entries, err := os.ReadDir(s.rootDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read voices dir: %w", err)
	}
	clones := make([]Clone, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		clone, ok, err := s.Load(entry.Name())
		if err != nil || !ok {
			continue
		}
		clones = append(clones, clone)
	}
	sort.Slice(clones, func(i, j int) bool {
		return clones[i].CreatedAt.After(clones[j].CreatedAt)
	})
	return clones, nil
}

// ReferencePath returns the absolute path of a voice's reference WAV, for
// handing to the speech engine and for serving playback.
func (s *Store) ReferencePath(id string) (string, error) {
	if err := validateVoiceID(id); err != nil {
		return "", err
	}
	path := filepath.Join(s.rootDir, id, referenceWAVName)
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return "", fmt.Errorf("voice reference not found")
	}
	if err != nil {
		return "", err
	}
	defer file.Close()
	if err := wav.ValidateHeader(file); err != nil {
		return "", fmt.Errorf("voice reference is not a valid WAV")
	}
	return filepath.Abs(path)
}

func (s *Store) Delete(id string) error {
	if err := validateVoiceID(id); err != nil {
		return fmt.Errorf("voice not found")
	}
	if clone, ok, err := s.Load(id); err != nil {
		return err
	} else if ok && clone.Protected {
		return ErrProtected
	}
	if err := os.RemoveAll(filepath.Join(s.rootDir, id)); err != nil {
		return fmt.Errorf("delete voice dir: %w", err)
	}
	return nil
}

func validateVoiceID(id string) error {
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") || filepath.IsAbs(id) {
		return fmt.Errorf("invalid voice id")
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("invalid voice id")
	}
	return nil
}
