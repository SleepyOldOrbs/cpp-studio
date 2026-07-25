package story

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cpp-studio/internal/wav"
)

type Store struct {
	rootDir string
}

func NewStore(rootDir string) *Store {
	if rootDir == "" {
		rootDir = DefaultRootDir
	}
	return &Store{rootDir: rootDir}
}

func (s *Store) Save(manifest Manifest, audio []byte) error {
	if manifest.ID == "" {
		return fmt.Errorf("story id is required")
	}
	if err := validateStoryID(manifest.ID); err != nil {
		return err
	}
	if len(audio) > MaxGeneratedWAVBytes {
		return fmt.Errorf("generated wav is %d bytes, max is %d bytes", len(audio), MaxGeneratedWAVBytes)
	}
	if err := wav.ValidateBytes(audio); err != nil {
		return fmt.Errorf("generated wav: %w", err)
	}
	if err := os.MkdirAll(s.rootDir, 0o755); err != nil {
		return fmt.Errorf("create stories dir: %w", err)
	}

	finalDir := filepath.Join(s.rootDir, manifest.ID)
	if _, err := os.Stat(finalDir); err == nil {
		return fmt.Errorf("story %s already exists", manifest.ID)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat story dir: %w", err)
	}

	tmpDir := filepath.Join(s.rootDir, "."+manifest.ID+".tmp")
	_ = os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Errorf("create temp story dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := os.WriteFile(filepath.Join(tmpDir, StoryArtifactName), audio, 0o644); err != nil {
		return fmt.Errorf("write story wav: %w", err)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "manifest.json"), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := os.Rename(tmpDir, finalDir); err != nil {
		return fmt.Errorf("finalize story dir: %w", err)
	}
	return nil
}

func (s *Store) Load(id string) (Manifest, bool, error) {
	if err := validateStoryID(id); err != nil {
		return Manifest{}, false, NewError(CodeNotFound, "story not found")
	}
	path := filepath.Join(s.rootDir, id, "manifest.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Manifest{}, false, nil
	}
	if err != nil {
		return Manifest{}, false, fmt.Errorf("read manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, false, fmt.Errorf("decode manifest: %w", err)
	}
	return manifest, true, nil
}

func (s *Store) List() ([]Summary, error) {
	entries, err := os.ReadDir(s.rootDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read stories dir: %w", err)
	}
	summaries := make([]Summary, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		manifest, ok, err := s.Load(entry.Name())
		if err != nil || !ok {
			continue
		}
		summaries = append(summaries, Summary{
			ID:              manifest.ID,
			Subject:         manifest.Subject,
			Mode:            manifest.Mode,
			Title:           manifest.Title,
			Status:          manifest.Status,
			CreatedAt:       manifest.CreatedAt,
			DurationSeconds: manifest.DurationSeconds,
			ArtifactURL:     manifest.Audio.URL,
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].CreatedAt.After(summaries[j].CreatedAt)
	})
	return summaries, nil
}

func (s *Store) ArtifactPath(id string, filename string) (string, error) {
	if err := validateStoryID(id); err != nil {
		return "", NewError(CodeInvalidArtifactRequest, "invalid story id")
	}
	if filename != StoryArtifactName {
		return "", NewError(CodeUnsupportedArtifact, "unsupported story artifact")
	}
	path := filepath.Join(s.rootDir, id, filename)
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return "", NewError(CodeArtifactNotFound, "story artifact not found")
	}
	if err != nil {
		return "", err
	}
	defer file.Close()
	if err := wav.ValidateHeader(file); err != nil {
		return "", NewError(CodeArtifactNotFound, "story artifact is not a valid WAV")
	}
	return path, nil
}

func (s *Store) Delete(id string) error {
	if err := validateStoryID(id); err != nil {
		return NewError(CodeNotFound, "story not found")
	}
	if err := os.RemoveAll(filepath.Join(s.rootDir, id)); err != nil {
		return fmt.Errorf("delete story dir: %w", err)
	}
	return nil
}

func validateStoryID(id string) error {
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") || filepath.IsAbs(id) {
		return fmt.Errorf("invalid story id")
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("invalid story id")
	}
	return nil
}
