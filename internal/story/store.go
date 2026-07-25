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

// takePath is where one line's take lives: lines/<line-id>/<take-id>.wav.
// Both ids are validated the same way story ids are, so a crafted id cannot
// walk out of the story directory.
func (s *Store) takePath(storyID, lineID, takeID string) (string, error) {
	if err := validateStoryID(storyID); err != nil {
		return "", NewError(CodeNotFound, "story not found")
	}
	if err := validateStoryID(lineID); err != nil {
		return "", NewError(CodeLineNotFound, "invalid line id")
	}
	if err := validateStoryID(takeID); err != nil {
		return "", NewError(CodeTakeNotFound, "invalid take id")
	}
	return filepath.Join(s.rootDir, storyID, "lines", lineID, takeID+".wav"), nil
}

// SaveTake writes one take beside its story and returns the URL it serves
// from. Takes are written directly rather than through the tmp+rename dance:
// a take is only ever referenced by a manifest that is written afterwards,
// so a half-written take is unreachable rather than corrupting.
func (s *Store) SaveTake(storyID, lineID, takeID string, audio []byte) (string, error) {
	if len(audio) > MaxGeneratedWAVBytes {
		return "", fmt.Errorf("take is %d bytes, max is %d bytes", len(audio), MaxGeneratedWAVBytes)
	}
	if err := wav.ValidateBytes(audio); err != nil {
		return "", fmt.Errorf("take wav: %w", err)
	}
	path, err := s.takePath(storyID, lineID, takeID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create take dir: %w", err)
	}
	if err := os.WriteFile(path, audio, 0o644); err != nil {
		return "", fmt.Errorf("write take: %w", err)
	}
	return TakeURL(storyID, lineID, takeID), nil
}

// LoadTake reads a stored take back for re-rendering.
func (s *Store) LoadTake(storyID, lineID, takeID string) ([]byte, error) {
	path, err := s.takePath(storyID, lineID, takeID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, NewError(CodeTakeNotFound, "take audio not found")
	}
	if err != nil {
		return nil, fmt.Errorf("read take: %w", err)
	}
	return data, nil
}

// SaveRender publishes a new render revision and points story.wav at it.
// The revision file is the archive; story.wav is the copy every existing
// reader already knows how to fetch.
func (s *Store) SaveRender(storyID string, revision int, audio []byte) error {
	if err := validateStoryID(storyID); err != nil {
		return NewError(CodeNotFound, "story not found")
	}
	if revision < 1 {
		return fmt.Errorf("render revision must be positive")
	}
	if len(audio) > MaxGeneratedWAVBytes {
		return fmt.Errorf("render is %d bytes, max is %d bytes", len(audio), MaxGeneratedWAVBytes)
	}
	if err := wav.ValidateBytes(audio); err != nil {
		return fmt.Errorf("render wav: %w", err)
	}
	dir := filepath.Join(s.rootDir, storyID, "renders")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create renders dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, renderFileName(revision)), audio, 0o644); err != nil {
		return fmt.Errorf("write render: %w", err)
	}
	return writeFileAtomic(filepath.Join(s.rootDir, storyID, StoryArtifactName), audio)
}

// SaveManifest replaces the manifest of a story that already exists. This is
// the one mutable file in a story directory: takes and renders only ever
// accumulate, so replacing the manifest is what makes an edit visible.
func (s *Store) SaveManifest(manifest Manifest) error {
	if err := validateStoryID(manifest.ID); err != nil {
		return NewError(CodeNotFound, "story not found")
	}
	dir := filepath.Join(s.rootDir, manifest.ID)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return NewError(CodeNotFound, "story not found")
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	return writeFileAtomic(filepath.Join(dir, "manifest.json"), append(data, '\n'))
}

// writeFileAtomic replaces a file through a temp file in the same directory,
// so a reader sees either the old bytes or the new ones.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	return nil
}

func renderFileName(revision int) string {
	return fmt.Sprintf("render-%03d.wav", revision)
}

// TakeURL and RenderURL are the routes the console fetches audio from.
func TakeURL(storyID, lineID, takeID string) string {
	return fmt.Sprintf("/v1/stories/%s/artifact/lines/%s/%s.wav", storyID, lineID, takeID)
}

func RenderURL(storyID string, revision int) string {
	return fmt.Sprintf("/v1/stories/%s/artifact/renders/%s", storyID, renderFileName(revision))
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

// ArtifactPath resolves a whitelisted story artifact. The whitelist is
// story.wav, one take (lines/<line-id>/<take-id>.wav), and one render
// revision (renders/render-NNN.wav) — every id validated the same way a
// story id is, so nothing crafted escapes the story directory.
func (s *Store) ArtifactPath(id string, segments ...string) (string, error) {
	if err := validateStoryID(id); err != nil {
		return "", NewError(CodeInvalidArtifactRequest, "invalid story id")
	}
	rel, err := artifactRelPath(segments)
	if err != nil {
		return "", err
	}
	path := filepath.Join(append([]string{s.rootDir, id}, rel...)...)
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

// artifactRelPath turns the URL tail after /artifact/ into a relative path
// inside the story directory, refusing anything not on the whitelist.
func artifactRelPath(segments []string) ([]string, error) {
	switch {
	case len(segments) == 1 && segments[0] == StoryArtifactName:
		return []string{StoryArtifactName}, nil

	case len(segments) == 3 && segments[0] == "lines":
		lineID := segments[1]
		takeFile := segments[2]
		if err := validateStoryID(lineID); err != nil {
			return nil, NewError(CodeInvalidArtifactRequest, "invalid line id")
		}
		takeID := strings.TrimSuffix(takeFile, ".wav")
		if takeID == takeFile {
			return nil, NewError(CodeUnsupportedArtifact, "unsupported story artifact")
		}
		if err := validateStoryID(takeID); err != nil {
			return nil, NewError(CodeInvalidArtifactRequest, "invalid take id")
		}
		return []string{"lines", lineID, takeID + ".wav"}, nil

	case len(segments) == 2 && segments[0] == "renders":
		name := segments[1]
		if !strings.HasPrefix(name, "render-") || !strings.HasSuffix(name, ".wav") {
			return nil, NewError(CodeUnsupportedArtifact, "unsupported story artifact")
		}
		digits := strings.TrimSuffix(strings.TrimPrefix(name, "render-"), ".wav")
		if digits == "" {
			return nil, NewError(CodeUnsupportedArtifact, "unsupported story artifact")
		}
		for _, r := range digits {
			if r < '0' || r > '9' {
				return nil, NewError(CodeUnsupportedArtifact, "unsupported story artifact")
			}
		}
		return []string{"renders", name}, nil

	default:
		return nil, NewError(CodeUnsupportedArtifact, "unsupported story artifact")
	}
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
