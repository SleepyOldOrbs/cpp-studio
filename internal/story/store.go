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

// A story being produced lives in a work-in-progress directory: the same
// dot-prefix that hides Save's temp dance from List, but named and long-
// lived. Takes are written into it the moment they are made, so a failure
// mid-production loses the remainder of the session, not the recordings
// already on disk. FinalizeWIP renames the whole directory into place, which
// is the single moment the story starts to exist — take room and all.
func (s *Store) wipDir(id string) string {
	return filepath.Join(s.rootDir, "."+id+".wip")
}

// BeginWIP claims the work-in-progress directory for a production run.
func (s *Store) BeginWIP(id string) error {
	if err := validateStoryID(id); err != nil {
		return fmt.Errorf("invalid story id")
	}
	if _, err := os.Stat(filepath.Join(s.rootDir, id)); err == nil {
		return fmt.Errorf("story %s already exists", id)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat story dir: %w", err)
	}
	if err := os.MkdirAll(s.wipDir(id), 0o755); err != nil {
		return fmt.Errorf("create wip story dir: %w", err)
	}
	return nil
}

// DiscardWIP removes a production run's directory, takes and all. It is
// called for the explicit discard route, and for cancels that recorded
// nothing; a cancel or failure with takes on disk keeps its directory —
// that work is what a resume picks up.
func (s *Store) DiscardWIP(id string) error {
	if err := validateStoryID(id); err != nil {
		return fmt.Errorf("invalid story id")
	}
	if err := os.RemoveAll(s.wipDir(id)); err != nil {
		return fmt.Errorf("discard wip story dir: %w", err)
	}
	return nil
}

// SaveManifestWIP records production progress in the work-in-progress
// directory. It is written after every take lands — take file first, then
// the manifest naming it — so a crash at any point leaves a manifest that
// only names takes that exist, and a resume trusts what it reads.
func (s *Store) SaveManifestWIP(manifest Manifest) error {
	if err := validateStoryID(manifest.ID); err != nil {
		return fmt.Errorf("invalid story id")
	}
	wip := s.wipDir(manifest.ID)
	if info, err := os.Stat(wip); err != nil || !info.IsDir() {
		return fmt.Errorf("story %s has no work in progress", manifest.ID)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	return writeFileAtomic(filepath.Join(wip, "manifest.json"), append(data, '\n'))
}

// LoadWIP reads the manifest of an interrupted production, if one exists.
func (s *Store) LoadWIP(id string) (Manifest, bool, error) {
	if err := validateStoryID(id); err != nil {
		return Manifest{}, false, NewError(CodeNotFound, "story not found")
	}
	data, err := os.ReadFile(filepath.Join(s.wipDir(id), "manifest.json"))
	if os.IsNotExist(err) {
		return Manifest{}, false, nil
	}
	if err != nil {
		return Manifest{}, false, fmt.Errorf("read wip manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, false, fmt.Errorf("decode wip manifest: %w", err)
	}
	return manifest, true, nil
}

// ListWIP enumerates interrupted productions: every work-in-progress
// directory that carries a manifest. Directories without one — a crash
// before the first save, an unrelated dot-dir — are not listable work.
func (s *Store) ListWIP() ([]Manifest, error) {
	entries, err := os.ReadDir(s.rootDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read stories dir: %w", err)
	}
	var manifests []Manifest
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".wip") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "."), ".wip")
		manifest, ok, err := s.LoadWIP(id)
		if err != nil || !ok {
			continue
		}
		manifests = append(manifests, manifest)
	}
	return manifests, nil
}

// FinalizeWIP publishes a produced story: the mix and the manifest are
// written beside the takes already in the work-in-progress directory, and
// the directory is renamed into place. A reader therefore sees either no
// story or the complete one — never a story whose takes are still arriving.
func (s *Store) FinalizeWIP(manifest Manifest, audio []byte) error {
	if err := validateStoryID(manifest.ID); err != nil {
		return fmt.Errorf("invalid story id")
	}
	if len(audio) > MaxGeneratedWAVBytes {
		return fmt.Errorf("generated wav is %d bytes, max is %d bytes", len(audio), MaxGeneratedWAVBytes)
	}
	if err := wav.ValidateBytes(audio); err != nil {
		return fmt.Errorf("generated wav: %w", err)
	}
	finalDir := filepath.Join(s.rootDir, manifest.ID)
	if _, err := os.Stat(finalDir); err == nil {
		return fmt.Errorf("story %s already exists", manifest.ID)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat story dir: %w", err)
	}
	wip := s.wipDir(manifest.ID)
	if info, err := os.Stat(wip); err != nil || !info.IsDir() {
		return fmt.Errorf("story %s has no work in progress", manifest.ID)
	}
	if err := os.WriteFile(filepath.Join(wip, StoryArtifactName), audio, 0o644); err != nil {
		return fmt.Errorf("write story wav: %w", err)
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(wip, "manifest.json"), append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	if err := os.Rename(wip, finalDir); err != nil {
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
	return s.takePathIn(filepath.Join(s.rootDir, storyID), lineID, takeID)
}

// takePathIn resolves a take below any story home — final or work in
// progress — with the same id policing either way.
func (s *Store) takePathIn(dir, lineID, takeID string) (string, error) {
	if err := validateStoryID(lineID); err != nil {
		return "", NewError(CodeLineNotFound, "invalid line id")
	}
	if err := validateStoryID(takeID); err != nil {
		return "", NewError(CodeTakeNotFound, "invalid take id")
	}
	return filepath.Join(dir, "lines", lineID, takeID+".wav"), nil
}

// SaveTake writes one take beside its story and returns the URL it serves
// from. Takes are written directly rather than through the tmp+rename dance:
// a take is only ever referenced by a manifest that is written afterwards,
// so a half-written take is unreachable rather than corrupting.
func (s *Store) SaveTake(storyID, lineID, takeID string, audio []byte) (string, error) {
	path, err := s.takePath(storyID, lineID, takeID)
	if err != nil {
		return "", err
	}
	return TakeURL(storyID, lineID, takeID), writeTake(path, audio)
}

// SaveTakeWIP is SaveTake into the work-in-progress directory — where every
// take of a story still being produced goes the moment it is synthesized.
// The URL it reports is the one the take serves from after FinalizeWIP,
// which is when the manifest naming it first becomes visible.
func (s *Store) SaveTakeWIP(storyID, lineID, takeID string, audio []byte) (string, error) {
	if err := validateStoryID(storyID); err != nil {
		return "", NewError(CodeNotFound, "story not found")
	}
	path, err := s.takePathIn(s.wipDir(storyID), lineID, takeID)
	if err != nil {
		return "", err
	}
	return TakeURL(storyID, lineID, takeID), writeTake(path, audio)
}

func writeTake(path string, audio []byte) error {
	if len(audio) > MaxGeneratedWAVBytes {
		return fmt.Errorf("take is %d bytes, max is %d bytes", len(audio), MaxGeneratedWAVBytes)
	}
	if err := wav.ValidateBytes(audio); err != nil {
		return fmt.Errorf("take wav: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create take dir: %w", err)
	}
	if err := os.WriteFile(path, audio, 0o644); err != nil {
		return fmt.Errorf("write take: %w", err)
	}
	return nil
}

// LoadTake reads a stored take back for re-rendering.
func (s *Store) LoadTake(storyID, lineID, takeID string) ([]byte, error) {
	path, err := s.takePath(storyID, lineID, takeID)
	if err != nil {
		return nil, err
	}
	return readTake(path)
}

// HasTakeWIP reports whether a work-in-progress take's audio is actually on
// disk — the resume path's guard against a manifest naming what a tampered
// directory no longer holds.
func (s *Store) HasTakeWIP(storyID, lineID, takeID string) bool {
	if err := validateStoryID(storyID); err != nil {
		return false
	}
	path, err := s.takePathIn(s.wipDir(storyID), lineID, takeID)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// LoadTakeWIP reads a take of a story still being produced, for the stitch
// that happens before the story is finalized.
func (s *Store) LoadTakeWIP(storyID, lineID, takeID string) ([]byte, error) {
	if err := validateStoryID(storyID); err != nil {
		return nil, NewError(CodeNotFound, "story not found")
	}
	path, err := s.takePathIn(s.wipDir(storyID), lineID, takeID)
	if err != nil {
		return nil, err
	}
	return readTake(path)
}

func readTake(path string) ([]byte, error) {
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

// SaveRenderWIP archives a revision inside the work-in-progress directory.
// It skips the story.wav copy SaveRender maintains — FinalizeWIP writes
// that as part of publishing the story.
func (s *Store) SaveRenderWIP(storyID string, revision int, audio []byte) error {
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
	dir := filepath.Join(s.wipDir(storyID), "renders")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create renders dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, renderFileName(revision)), audio, 0o644); err != nil {
		return fmt.Errorf("write render: %w", err)
	}
	return nil
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

// renderExportName is the delivery encoding beside its revision, e.g.
// render-002.mp3 next to render-002.wav.
func renderExportName(revision int, format string) string {
	return fmt.Sprintf("render-%03d.%s", revision, format)
}

// RenderPath is the on-disk WAV of one revision, for callers that hand a
// path to an external tool rather than reading the bytes.
func (s *Store) RenderPath(storyID string, revision int) (string, error) {
	return s.ArtifactPath(storyID, "renders", renderFileName(revision))
}

// ExportPath is where a delivery encoding of one revision lives. The file
// need not exist yet: this is also the destination an encoder writes to.
func (s *Store) ExportPath(storyID string, revision int, format string) (string, error) {
	if err := validateStoryID(storyID); err != nil {
		return "", NewError(CodeNotFound, "story not found")
	}
	if revision < 1 {
		return "", NewError(CodeInvalidRequest, "render revision must be positive")
	}
	if !isExportFormat(format) {
		return "", NewError(CodeUnsupportedArtifact, "unsupported export format")
	}
	dir := filepath.Join(s.rootDir, storyID, "renders")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create renders dir: %w", err)
	}
	return filepath.Join(dir, renderExportName(revision, format)), nil
}

// ExportURL is the route a delivery encoding is served from.
func ExportURL(storyID string, revision int, format string) string {
	return fmt.Sprintf("/v1/stories/%s/artifact/renders/%s", storyID, renderExportName(revision, format))
}

// isExportFormat keeps the artifact whitelist and the export destination
// agreeing on exactly which extensions exist.
func isExportFormat(format string) bool {
	for _, allowed := range exportFormats {
		if format == allowed {
			return true
		}
	}
	return false
}

// exportFormats mirrors engine.AudioFormats. It is duplicated rather than
// imported because the store's job is to police filenames, and it should
// not gain a dependency on the engine package to do it.
var exportFormats = []string{"mp3", "opus"}

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
		exports := make([]Export, 0)
		renders := make([]RenderSummary, 0, len(manifest.Renders))
		for _, render := range manifest.Renders {
			renderExports := make([]Export, 0, len(render.Exports))
			for _, export := range render.Exports {
				export.RenderRevision = render.Revision
				exports = append(exports, export)
				renderExports = append(renderExports, export)
			}
			renders = append(renders, RenderSummary{
				Revision: render.Revision, CreatedAt: render.CreatedAt, DurationSeconds: render.DurationSeconds,
				Bytes: render.Bytes, URL: render.URL, Exports: renderExports,
			})
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
			Exports:         exports,
			Renders:         renders,
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
	// Only the studio's own WAVs get a header check; a delivery encoding is
	// whatever the encoder made of one, and this store has no business
	// second-guessing an MP3 frame.
	if strings.HasSuffix(path, ".wav") {
		if err := wav.ValidateHeader(file); err != nil {
			return "", NewError(CodeArtifactNotFound, "story artifact is not a valid WAV")
		}
	}
	return path, nil
}

// ArtifactContentType names what an artifact path serves as.
func ArtifactContentType(path string) string {
	switch {
	case strings.HasSuffix(path, ".mp3"):
		return "audio/mpeg"
	case strings.HasSuffix(path, ".opus"):
		return "audio/ogg"
	default:
		return "audio/wav"
	}
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
		if !strings.HasPrefix(name, "render-") {
			return nil, NewError(CodeUnsupportedArtifact, "unsupported story artifact")
		}
		// render-NNN.wav is the revision itself; render-NNN.mp3 and
		// render-NNN.opus are its delivery encodings.
		rest := strings.TrimPrefix(name, "render-")
		dot := strings.LastIndex(rest, ".")
		if dot <= 0 {
			return nil, NewError(CodeUnsupportedArtifact, "unsupported story artifact")
		}
		digits, ext := rest[:dot], rest[dot+1:]
		if ext != "wav" && !isExportFormat(ext) {
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
