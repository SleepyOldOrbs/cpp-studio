package audiobook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"cpp-studio/internal/wav"
)

var (
	ErrProductionNotFound = errors.New("audiobook production not found")
	ErrStoreCorrupt       = errors.New("audiobook store is corrupt")
)

const (
	manifestFileName = "manifest.json"
	sourceFileName   = "source.txt"
	sectionsDirName  = "sections"
)

// Store owns durable audiobook production and publication state.
type Store struct {
	rootDir string
}

func NewStore(rootDir string) *Store {
	if rootDir == "" {
		rootDir = DefaultRootDir
	}
	return &Store{rootDir: rootDir}
}

func (s *Store) wipDir(id string) string {
	return filepath.Join(s.rootDir, "."+id+".wip")
}

func (s *Store) IDExists(id string) (bool, error) {
	if err := validateBookID(id); err != nil {
		return false, err
	}
	for _, path := range []string{filepath.Join(s.rootDir, id), s.wipDir(id)} {
		if _, err := os.Stat(path); err == nil {
			return true, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	return false, nil
}

type stagedProduction struct {
	id   string
	path string
}

// StageInitial writes a complete source/identity/section plan into a private,
// unpublished directory. The caller either publishes it after reservation or
// aborts it; errors clean it up before returning.
func (s *Store) StageInitial(manifest Manifest, source string) (*stagedProduction, error) {
	if err := validateInitialManifest(manifest, source); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(s.rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("create audiobooks dir: %w", err)
	}
	for _, path := range []string{filepath.Join(s.rootDir, manifest.ID), s.wipDir(manifest.ID)} {
		if _, err := os.Stat(path); err == nil {
			return nil, fmt.Errorf("audiobook %s already exists", manifest.ID)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("stat audiobook path: %w", err)
		}
	}
	path, err := os.MkdirTemp(s.rootDir, "."+manifest.ID+".staging-")
	if err != nil {
		return nil, fmt.Errorf("create audiobook staging dir: %w", err)
	}
	staged := &stagedProduction{id: manifest.ID, path: path}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(path)
		}
	}()
	if err := os.Chmod(path, 0o700); err != nil {
		return nil, fmt.Errorf("protect audiobook staging dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(path, sourceFileName), []byte(source), 0o600); err != nil {
		return nil, fmt.Errorf("write canonical audiobook source: %w", err)
	}
	if err := os.Mkdir(filepath.Join(path, sectionsDirName), 0o700); err != nil {
		return nil, fmt.Errorf("create audiobook sections dir: %w", err)
	}
	if err := writeManifest(filepath.Join(path, manifestFileName), manifest); err != nil {
		return nil, err
	}
	committed = true
	return staged, nil
}

func (s *Store) AbortInitial(staged *stagedProduction) {
	if staged == nil || staged.path == "" {
		return
	}
	_ = os.RemoveAll(staged.path)
}

// PublishInitial makes a fully staged production discoverable with one rename.
func (s *Store) PublishInitial(staged *stagedProduction) error {
	if staged == nil || validateBookID(staged.id) != nil {
		return fmt.Errorf("invalid staged audiobook")
	}
	if filepath.Dir(staged.path) != filepath.Clean(s.rootDir) || !strings.HasPrefix(filepath.Base(staged.path), "."+staged.id+".staging-") {
		return fmt.Errorf("invalid audiobook staging path")
	}
	if err := os.Rename(staged.path, s.wipDir(staged.id)); err != nil {
		return fmt.Errorf("publish audiobook work in progress: %w", err)
	}
	staged.path = ""
	return nil
}

func validateInitialManifest(manifest Manifest, source string) error {
	if err := validateBookID(manifest.ID); err != nil {
		return err
	}
	if manifest.SchemaVersion != CurrentManifestSchemaVersion || manifest.Status != ProductionStatusSynthesizing {
		return fmt.Errorf("initial audiobook manifest must be schema %d and synthesizing", CurrentManifestSchemaVersion)
	}
	if manifest.SourceFile != sourceFileName || manifest.SynthesisIdentity == nil || manifest.SynthesisFingerprint == "" {
		return fmt.Errorf("initial audiobook manifest is missing source or synthesis identity")
	}
	sourceSum := sha256.Sum256([]byte(source))
	if manifest.SourceSHA256 != hex.EncodeToString(sourceSum[:]) || manifest.SynthesisIdentity.SourceSHA256 != manifest.SourceSHA256 || manifest.SynthesisIdentity.Fingerprint != manifest.SynthesisFingerprint {
		return fmt.Errorf("initial audiobook source or synthesis identity does not match")
	}
	if len(manifest.Sections) == 0 || manifest.Chunks != len(manifest.Sections) {
		return fmt.Errorf("initial audiobook manifest needs a complete section plan")
	}
	for i, section := range manifest.Sections {
		if section.ID != fmt.Sprintf("section-%04d", i+1) || section.Status != SectionStatusPending || section.CheckpointFingerprint == "" {
			return fmt.Errorf("initial audiobook section %d is incomplete", i+1)
		}
		if section.StartByte < 0 || section.EndByte <= section.StartByte || section.EndByte > int64(len(source)) {
			return fmt.Errorf("initial audiobook section %s has an invalid source range", section.ID)
		}
		textSum := sha256.Sum256([]byte(source[section.StartByte:section.EndByte]))
		if section.TextSHA256 != hex.EncodeToString(textSum[:]) {
			return fmt.Errorf("initial audiobook section %s source hash does not match", section.ID)
		}
		if section.AudioFile != filepath.ToSlash(filepath.Join(sectionsDirName, section.ID+".wav")) {
			return fmt.Errorf("initial audiobook section %s has an invalid audio path", section.ID)
		}
	}
	return nil
}

func (s *Store) SaveSectionWIP(id, sectionID string, audio []byte) (string, error) {
	if err := validateBookID(id); err != nil || !validSectionID(sectionID) {
		return "", fmt.Errorf("invalid audiobook section")
	}
	if err := wav.ValidateBytes(audio); err != nil {
		return "", fmt.Errorf("section wav: %w", err)
	}
	path := filepath.Join(s.wipDir(id), sectionsDirName, sectionID+".wav")
	if err := writeFileAtomic(path, audio); err != nil {
		return "", fmt.Errorf("write section wav: %w", err)
	}
	return path, nil
}

func validSectionID(id string) bool {
	if len(id) != len("section-0000") || !strings.HasPrefix(id, "section-") {
		return false
	}
	for _, r := range id[len("section-"):] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (s *Store) SaveManifestWIP(manifest Manifest) error {
	if err := validateBookID(manifest.ID); err != nil {
		return err
	}
	if info, err := os.Stat(s.wipDir(manifest.ID)); err != nil || !info.IsDir() {
		return fmt.Errorf("audiobook %s has no work in progress", manifest.ID)
	}
	return writeManifestAtomic(filepath.Join(s.wipDir(manifest.ID), manifestFileName), manifest)
}

func (s *Store) bookWIPPath(id string) string {
	return filepath.Join(s.wipDir(id), ArtifactName)
}

func (s *Store) LoadWIP(id string) (Manifest, bool, error) {
	if err := validateBookID(id); err != nil {
		return Manifest{}, false, err
	}
	return loadManifest(filepath.Join(s.wipDir(id), manifestFileName))
}

func (s *Store) ListWIP() ([]Manifest, error) {
	entries, err := os.ReadDir(s.rootDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read audiobooks dir: %w", err)
	}
	var manifests []Manifest
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || !strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".wip") {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "."), ".wip")
		manifest, ok, err := s.LoadWIP(id)
		if err == nil && ok {
			manifests = append(manifests, manifest)
		}
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].CreatedAt.After(manifests[j].CreatedAt) })
	return manifests, nil
}

// LoadDurableWIP validates the immutable source, synthesis identity, section
// plan, and checkpoints before returning any source text to orchestration.
func (s *Store) LoadDurableWIP(id string) (Manifest, string, error) {
	manifest, ok, err := s.LoadWIP(id)
	if err != nil {
		return Manifest{}, "", err
	}
	if !ok {
		return Manifest{}, "", ErrProductionNotFound
	}
	source, err := os.ReadFile(filepath.Join(s.wipDir(id), sourceFileName))
	if err != nil {
		return Manifest{}, "", fmt.Errorf("%w: read canonical source: %v", ErrStoreCorrupt, err)
	}
	if err := validateDurableManifest(manifest, string(source)); err != nil {
		return Manifest{}, "", fmt.Errorf("%w: %v", ErrStoreCorrupt, err)
	}
	return manifest, string(source), nil
}

func validateDurableManifest(manifest Manifest, source string) error {
	if err := validateBookID(manifest.ID); err != nil {
		return err
	}
	if manifest.SchemaVersion != CurrentManifestSchemaVersion || manifest.SynthesisIdentity == nil {
		return fmt.Errorf("unsupported or missing manifest identity")
	}
	if manifest.SourceFile != sourceFileName || manifest.SynthesisFingerprint == "" {
		return fmt.Errorf("missing canonical source identity")
	}
	sourceSum := sha256.Sum256([]byte(source))
	if got := hex.EncodeToString(sourceSum[:]); got != manifest.SourceSHA256 || got != manifest.SynthesisIdentity.SourceSHA256 {
		return fmt.Errorf("canonical source hash does not match")
	}
	if manifest.SynthesisIdentity.Fingerprint != manifest.SynthesisFingerprint || len(manifest.Sections) == 0 || manifest.Chunks != len(manifest.Sections) {
		return fmt.Errorf("synthesis identity or section table does not match")
	}
	for i, section := range manifest.Sections {
		if section.ID != fmt.Sprintf("section-%04d", i+1) || section.StartByte < 0 || section.EndByte <= section.StartByte || section.EndByte > int64(len(source)) {
			return fmt.Errorf("section %d has an invalid identity or source range", i+1)
		}
		textSum := sha256.Sum256([]byte(source[section.StartByte:section.EndByte]))
		if section.TextSHA256 != hex.EncodeToString(textSum[:]) {
			return fmt.Errorf("section %s source hash does not match", section.ID)
		}
		if section.AudioFile != filepath.ToSlash(filepath.Join(sectionsDirName, section.ID+".wav")) {
			return fmt.Errorf("section %s audio path is invalid", section.ID)
		}
		if section.CheckpointFingerprint != sectionCheckpointFingerprint(manifest.SynthesisFingerprint, section) {
			return fmt.Errorf("section %s checkpoint does not match", section.ID)
		}
	}
	return nil
}

// TrustedSectionWIP returns a reusable section only when its manifest
// projection, selected attempt, hash, checkpoint, and WAV all agree.
func (s *Store) TrustedSectionWIP(manifest Manifest, section Section) (string, bool, error) {
	if section.Status != SectionStatusSynthesized && section.Status != SectionStatusVerified && section.Status != SectionStatusFlagged {
		return "", false, nil
	}
	if section.AudioSHA256 == "" || len(section.Attempts) == 0 {
		return "", false, nil
	}
	var selected *Attempt
	for i := range section.Attempts {
		if section.Attempts[i].Selected {
			if selected != nil {
				return "", false, nil
			}
			selected = &section.Attempts[i]
		}
	}
	if selected == nil || selected.Seed != section.Seed || selected.CheckpointFingerprint != section.CheckpointFingerprint || selected.AudioFile != section.AudioFile || selected.AudioSHA256 != section.AudioSHA256 {
		return "", false, nil
	}
	path := filepath.Join(s.wipDir(manifest.ID), filepath.FromSlash(section.AudioFile))
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if err := wav.ValidateBytes(data); err != nil {
		return "", false, nil
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != section.AudioSHA256 {
		return "", false, nil
	}
	return path, true, nil
}

// RecoverInterrupted marks unfinished productions found after process startup.
// Corrupt WIPs remain untouched for manual recovery and are never synthesized.
func (s *Store) RecoverInterrupted() error {
	manifests, err := s.ListWIP()
	if err != nil {
		return err
	}
	for _, item := range manifests {
		manifest, _, err := s.LoadDurableWIP(item.ID)
		if err != nil {
			continue
		}
		if manifest.Status != ProductionStatusInterrupted {
			manifest.Status = ProductionStatusInterrupted
			if err := s.SaveManifestWIP(manifest); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) DiscardWIP(id string) error {
	if err := validateBookID(id); err != nil {
		return err
	}
	path := s.wipDir(id)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return ErrProductionNotFound
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: WIP path is not a directory", ErrStoreCorrupt)
	}
	return os.RemoveAll(path)
}

// FinalizeWIP writes the validated book beside its durable source/sections and
// publishes the complete directory atomically.
func (s *Store) FinalizeWIP(manifest Manifest, audio []byte) error {
	if err := validateBookID(manifest.ID); err != nil {
		return err
	}
	if manifest.Status != ProductionStatusComplete {
		return fmt.Errorf("final audiobook manifest must be complete")
	}
	if err := wav.ValidateBytes(audio); err != nil {
		return fmt.Errorf("stitched narration: %w", err)
	}
	wip := s.wipDir(manifest.ID)
	if info, err := os.Stat(wip); err != nil || !info.IsDir() {
		return fmt.Errorf("audiobook %s has no work in progress", manifest.ID)
	}
	finalDir := filepath.Join(s.rootDir, manifest.ID)
	if _, err := os.Stat(finalDir); err == nil {
		return fmt.Errorf("audiobook %s already exists", manifest.ID)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat final audiobook: %w", err)
	}
	if err := writeFileAtomic(filepath.Join(wip, ArtifactName), audio); err != nil {
		return fmt.Errorf("write narration wav: %w", err)
	}
	return s.FinalizeWIPFile(manifest)
}

// FinalizeWIPFile publishes an already streamed and validated book.wav without
// reading it back into a whole-book byte slice.
func (s *Store) FinalizeWIPFile(manifest Manifest) error {
	if err := validateBookID(manifest.ID); err != nil {
		return err
	}
	if manifest.Status != ProductionStatusComplete {
		return fmt.Errorf("final audiobook manifest must be complete")
	}
	wip := s.wipDir(manifest.ID)
	if info, err := os.Stat(wip); err != nil || !info.IsDir() {
		return fmt.Errorf("audiobook %s has no work in progress", manifest.ID)
	}
	if _, err := wav.DurationFile(s.bookWIPPath(manifest.ID)); err != nil {
		return fmt.Errorf("validate streamed narration: %w", err)
	}
	finalDir := filepath.Join(s.rootDir, manifest.ID)
	if _, err := os.Stat(finalDir); err == nil {
		return fmt.Errorf("audiobook %s already exists", manifest.ID)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat final audiobook: %w", err)
	}
	if err := writeManifestAtomic(filepath.Join(wip, manifestFileName), manifest); err != nil {
		return err
	}
	if err := os.Rename(wip, finalDir); err != nil {
		return fmt.Errorf("publish audiobook: %w", err)
	}
	return nil
}

func writeManifest(path string, manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode audiobook manifest: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write audiobook manifest: %w", err)
	}
	return nil
}

func writeManifestAtomic(path string, manifest Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode audiobook manifest: %w", err)
	}
	return writeFileAtomic(path, append(data, '\n'))
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
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
	return os.Rename(tmpPath, path)
}

func loadManifest(path string) (Manifest, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Manifest{}, false, nil
	}
	if err != nil {
		return Manifest{}, false, fmt.Errorf("read audiobook manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, false, fmt.Errorf("decode audiobook manifest: %w", err)
	}
	return manifest, true, nil
}
