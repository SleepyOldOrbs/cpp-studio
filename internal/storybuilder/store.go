// Package storybuilder owns durable Story Builder Project state.
package storybuilder

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"cpp-studio/internal/story"
	"cpp-studio/internal/wav"
)

const (
	DefaultRootDir            = "out/story-builder-projects"
	MaxNameLength             = 120
	MaxTracks                 = 128
	MaxClips                  = 2048
	MaxDialogueTextLength     = 4000
	DefaultTimelineDurationMS = 30000
	manifestName              = "project.json"
)

var (
	ErrInvalid                = errors.New("invalid Story Builder Project")
	ErrNotFound               = errors.New("Story Builder Project not found")
	ErrConflict               = errors.New("Story Builder Project revision conflict")
	ErrVoiceConflict          = errors.New("Dialogue Track already contains dialogue for a different Character Voice")
	ErrCharacterVoiceNotFound = errors.New("Character Voice not found")
	ErrLibraryAudioNotFound   = errors.New("Library audio not found")
	ErrIncompatibleMedia      = errors.New("Library audio is not compatible with the target track")
	ErrProjectMediaNotFound   = errors.New("project media not found")
	ErrStoryNotFound          = errors.New("retained Story not found")
	ErrStoryMappingRequired   = errors.New("every Story speaker requires a Character Voice mapping")
	ErrRenderNotReady         = errors.New("audible dialogue is not ready to render")
	ErrRenderNotFound         = errors.New("Story Builder render not found")
	ErrExportUnavailable      = errors.New("Story Builder export encoder is unavailable")
	ErrExportNotFound         = errors.New("Story Builder export not found")
	ErrUnsupportedExport      = errors.New("unsupported Story Builder export format")
)

type TrackType string

const (
	TrackTypeDialogue TrackType = "dialogue"
	TrackTypeSFX      TrackType = "sfx"
	TrackTypeMusic    TrackType = "music"
)

type ClipType string

const (
	ClipTypeSilence  ClipType = "silence"
	ClipTypeDialogue ClipType = "dialogue"
	ClipTypeSFX      ClipType = "sfx"
	ClipTypeMusic    ClipType = "music"
)

type MediaRole string

const (
	MediaRoleSFX     MediaRole = "sfx"
	MediaRoleMusic   MediaRole = "music"
	MediaRoleUtility MediaRole = "utility"
)

type DialogueStatus string

const (
	DialogueStatusStale    DialogueStatus = "stale"
	DialogueStatusBuilding DialogueStatus = "building"
	DialogueStatusReady    DialogueStatus = "ready"
	DialogueStatusFailed   DialogueStatus = "failed"
)

// TimelineClip is timing metadata. Audio-backed clips keep nondestructive
// source offsets; silence clips deliberately have no media source.
type TimelineClip struct {
	ID                  string         `json:"id"`
	Type                ClipType       `json:"type"`
	Label               string         `json:"label"`
	StartMS             int64          `json:"start_ms"`
	DurationMS          int64          `json:"duration_ms"`
	SourceID            string         `json:"source_id,omitempty"`
	SourceDurationMS    int64          `json:"source_duration_ms,omitempty"`
	SourceInMS          int64          `json:"source_in_ms,omitempty"`
	SourceOutMS         int64          `json:"source_out_ms,omitempty"`
	SourceLibraryItemID string         `json:"source_library_item_id,omitempty"`
	SourceLibraryName   string         `json:"source_library_name,omitempty"`
	SourceMediaRole     MediaRole      `json:"source_media_role,omitempty"`
	MediaError          string         `json:"media_error,omitempty"`
	Text                string         `json:"text,omitempty"`
	Status              DialogueStatus `json:"status,omitempty"`
	CharacterVoiceID    string         `json:"character_voice_id,omitempty"`
	ActorVoiceID        string         `json:"actor_voice_id,omitempty"`
	VoiceFingerprint    string         `json:"voice_fingerprint,omitempty"`
	BuildError          string         `json:"build_error,omitempty"`
	SourceStoryID       string         `json:"source_story_id,omitempty"`
	SourceStoryLineID   string         `json:"source_story_line_id,omitempty"`
	SourceStoryTakeID   string         `json:"source_story_take_id,omitempty"`
}

type Track struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Type             TrackType      `json:"type"`
	Order            int            `json:"order"`
	Muted            bool           `json:"muted"`
	CharacterVoiceID string         `json:"character_voice_id,omitempty"`
	ActorVoiceID     string         `json:"actor_voice_id,omitempty"`
	VoiceFingerprint string         `json:"voice_fingerprint,omitempty"`
	Clips            []TimelineClip `json:"clips"`
}

type VoiceIdentity struct {
	CharacterVoiceID string
	ActorVoiceID     string
	Direction        string
	Fingerprint      string
}

type CharacterVoiceResolver func(id string) (VoiceIdentity, bool, error)

type LibraryAudio struct {
	ID        string
	Name      string
	MediaRole MediaRole
	Data      []byte
}

type LibraryAudioResolver func(id string) (LibraryAudio, bool, error)

type LibraryAudioPlacement struct {
	Revision      int
	TrackID       string
	LibraryItemID string
	StartMS       int64
}

type ProjectUpdate struct {
	Name               string   `json:"name"`
	Revision           int      `json:"revision"`
	TimelineDurationMS int64    `json:"timeline_duration_ms,omitempty"`
	Tracks             []Track  `json:"tracks"`
	RevoiceTrackIDs    []string `json:"revoice_track_ids,omitempty"`
}

type RenderRevision struct {
	Revision   int            `json:"revision"`
	CreatedAt  time.Time      `json:"created_at"`
	DurationMS int64          `json:"duration_ms"`
	Bytes      int            `json:"bytes"`
	URL        string         `json:"url"`
	Master     *story.Master  `json:"master,omitempty"`
	Exports    []RenderExport `json:"exports,omitempty"`
}

type RenderExport struct {
	Format    string    `json:"format"`
	Bitrate   string    `json:"bitrate,omitempty"`
	Bytes     int       `json:"bytes"`
	CreatedAt time.Time `json:"created_at"`
	URL       string    `json:"url"`
}

type MasterRenderFunc func(context.Context, []byte) ([]byte, *story.Master, error)
type TranscodeRenderFunc func(context.Context, string, string, string, string) error

// Project is one separately saved Story Builder production.
type Project struct {
	ID                 string           `json:"id"`
	Name               string           `json:"name"`
	Revision           int              `json:"revision"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
	TimelineDurationMS int64            `json:"timeline_duration_ms"`
	Tracks             []Track          `json:"tracks"`
	Renders            []RenderRevision `json:"renders,omitempty"`
}

// DependentProject identifies a saved project that still needs a Voice for
// future dialogue rebuilds.
type DependentProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type StoreOptions struct {
	WriteFileAtomic       func(path string, data []byte) error
	ResolveCharacterVoice CharacterVoiceResolver
	ResolveLibraryAudio   LibraryAudioResolver
	MasterRender          MasterRenderFunc
	Transcode             TranscodeRenderFunc
}

type Store struct {
	mu                    sync.Mutex
	rootDir               string
	now                   func() time.Time
	writeFileAtomic       func(path string, data []byte) error
	resolveCharacterVoice CharacterVoiceResolver
	resolveLibraryAudio   LibraryAudioResolver
	masterRender          MasterRenderFunc
	transcode             TranscodeRenderFunc
}

func NewStore(rootDir string) *Store {
	return NewStoreWithOptions(rootDir, StoreOptions{})
}

func NewStoreWithOptions(rootDir string, options StoreOptions) *Store {
	if rootDir == "" {
		rootDir = DefaultRootDir
	}
	write := options.WriteFileAtomic
	if write == nil {
		write = writeFileAtomic
	}
	return &Store{
		rootDir:               rootDir,
		now:                   func() time.Time { return time.Now().UTC() },
		writeFileAtomic:       write,
		resolveCharacterVoice: options.ResolveCharacterVoice,
		resolveLibraryAudio:   options.ResolveLibraryAudio,
		masterRender:          options.MasterRender,
		transcode:             options.Transcode,
	}
}

func (s *Store) Create(name string) (Project, error) {
	return s.createProject(name, DefaultTimelineDurationMS, []Track{}, nil)
}

// createProject is the one publication transaction for new projects. Imports
// add validated project-owned takes to the same staged directory before its
// manifest and directory become visible together.
func (s *Store) createProject(name string, timelineDurationMS int64, tracks []Track, readyTakes map[string][]byte) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name, err := validateName(name)
	if err != nil {
		return Project{}, err
	}
	if err := validateTracks(tracks, timelineDurationMS); err != nil {
		return Project{}, err
	}
	if err := os.MkdirAll(s.rootDir, 0o755); err != nil {
		return Project{}, fmt.Errorf("create Story Builder Projects directory: %w", err)
	}

	now := s.now()
	for attempt := 0; attempt < 5; attempt++ {
		id, err := newProjectID(now)
		if err != nil {
			return Project{}, err
		}
		project := Project{ID: id, Name: name, Revision: 1, CreatedAt: now, UpdatedAt: now, TimelineDurationMS: timelineDurationMS, Tracks: tracks}
		finalDir := filepath.Join(s.rootDir, project.ID)
		if _, err := os.Stat(finalDir); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return Project{}, fmt.Errorf("stat Story Builder Project: %w", err)
		}

		stagingDir, err := os.MkdirTemp(s.rootDir, "."+project.ID+".staging-")
		if err != nil {
			return Project{}, fmt.Errorf("stage Story Builder Project: %w", err)
		}
		if len(readyTakes) > 0 {
			takesDir := filepath.Join(stagingDir, "takes")
			if err := os.MkdirAll(takesDir, 0o755); err != nil {
				_ = os.RemoveAll(stagingDir)
				return Project{}, fmt.Errorf("create imported takes directory: %w", err)
			}
			for sourceID, data := range readyTakes {
				if !validTimelineID(sourceID) || wav.ValidateBytes(data) != nil {
					_ = os.RemoveAll(stagingDir)
					return Project{}, ErrInvalid
				}
				if err := os.WriteFile(filepath.Join(takesDir, sourceID+".wav"), data, 0o644); err != nil {
					_ = os.RemoveAll(stagingDir)
					return Project{}, fmt.Errorf("copy retained Story take: %w", err)
				}
			}
		}
		data, err := encodeProject(project)
		if err != nil {
			_ = os.RemoveAll(stagingDir)
			return Project{}, err
		}
		if err := s.writeFileAtomic(filepath.Join(stagingDir, manifestName), data); err != nil {
			_ = os.RemoveAll(stagingDir)
			return Project{}, fmt.Errorf("write Story Builder Project: %w", err)
		}
		if err := os.Rename(stagingDir, finalDir); err != nil {
			_ = os.RemoveAll(stagingDir)
			return Project{}, fmt.Errorf("publish Story Builder Project: %w", err)
		}
		return project, nil
	}
	return Project{}, fmt.Errorf("mint unique Story Builder Project id")
}

func (s *Store) Get(id string) (Project, bool, error) {
	if !validProjectID(id) {
		return Project{}, false, nil
	}
	data, err := os.ReadFile(filepath.Join(s.rootDir, id, manifestName))
	if os.IsNotExist(err) {
		return Project{}, false, nil
	}
	if err != nil {
		return Project{}, false, fmt.Errorf("read Story Builder Project: %w", err)
	}
	var project Project
	if err := json.Unmarshal(data, &project); err != nil {
		return Project{}, false, fmt.Errorf("decode Story Builder Project: %w", err)
	}
	if project.ID != id || project.Revision < 1 {
		return Project{}, false, fmt.Errorf("decode Story Builder Project: invalid manifest identity")
	}
	if err := validateRenderRevisions(project.Renders, id); err != nil {
		return Project{}, false, err
	}
	if project.TimelineDurationMS == 0 {
		project.TimelineDurationMS = minimumTimelineDurationMS(project.Tracks)
	}
	if s.resolveCharacterVoice != nil {
		tracks, err := s.prepareTracks(project.Tracks, project.Tracks, nil)
		if err != nil {
			return Project{}, false, err
		}
		project.Tracks = tracks
	}
	s.markMediaErrors(&project)
	return project, true, nil
}

func minimumTimelineDurationMS(tracks []Track) int64 {
	durationMS := int64(DefaultTimelineDurationMS)
	for _, track := range tracks {
		for _, clip := range track.Clips {
			if clip.StartMS < 0 || clip.DurationMS <= 0 || clip.StartMS > math.MaxInt64-clip.DurationMS {
				continue
			}
			if endMS := clip.StartMS + clip.DurationMS; endMS > durationMS {
				durationMS = endMS
			}
		}
	}
	return durationMS
}

func (s *Store) List() ([]Project, error) {
	entries, err := os.ReadDir(s.rootDir)
	if os.IsNotExist(err) {
		return []Project{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list Story Builder Projects: %w", err)
	}
	projects := make([]Project, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		project, ok, err := s.Get(entry.Name())
		if err != nil {
			return nil, err
		}
		if ok {
			projects = append(projects, project)
		}
	}
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].UpdatedAt.Equal(projects[j].UpdatedAt) {
			return projects[i].ID > projects[j].ID
		}
		return projects[i].UpdatedAt.After(projects[j].UpdatedAt)
	})
	return projects, nil
}

// GuardCharacterVoiceDeletion holds the project mutation boundary while the
// owning Voice Store performs the deletion. A concurrent project save cannot
// add a dependency between the check and deletion.
func (s *Store) GuardCharacterVoiceDeletion(characterVoiceID string, deleteVoice func() error) ([]DependentProject, error) {
	characterVoiceID = strings.TrimSpace(characterVoiceID)
	s.mu.Lock()
	defer s.mu.Unlock()

	dependents, err := s.projectsDependingOnVoice(func(track Track) bool {
		return trackDependsOnCharacterVoice(track, characterVoiceID)
	})
	if err != nil || len(dependents) > 0 {
		return dependents, err
	}
	return dependents, deleteVoice()
}

// GuardActorVoiceDeletion provides the same boundary for direct Actor Voice
// bindings and bindings through one of the Actor's Character Voices.
func (s *Store) GuardActorVoiceDeletion(actorVoiceID string, characterVoiceIDs []string, deleteVoice func() error) ([]DependentProject, error) {
	actorVoiceID = strings.TrimSpace(actorVoiceID)
	characters := characterVoiceIDSet(characterVoiceIDs)
	s.mu.Lock()
	defer s.mu.Unlock()

	dependents, err := s.projectsDependingOnVoice(func(track Track) bool {
		return trackDependsOnActorVoice(track, actorVoiceID, characters)
	})
	if err != nil || len(dependents) > 0 {
		return dependents, err
	}
	return dependents, deleteVoice()
}

func characterVoiceIDSet(ids []string) map[string]struct{} {
	characters := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			characters[id] = struct{}{}
		}
	}
	return characters
}

func trackDependsOnCharacterVoice(track Track, characterVoiceID string) bool {
	if characterVoiceID == "" {
		return false
	}
	if track.CharacterVoiceID == characterVoiceID {
		return true
	}
	for _, clip := range track.Clips {
		if clip.CharacterVoiceID == characterVoiceID {
			return true
		}
	}
	return false
}

func trackDependsOnActorVoice(track Track, actorVoiceID string, characterVoiceIDs map[string]struct{}) bool {
	if actorVoiceID != "" && track.ActorVoiceID == actorVoiceID {
		return true
	}
	if _, ok := characterVoiceIDs[track.CharacterVoiceID]; ok {
		return true
	}
	for _, clip := range track.Clips {
		if actorVoiceID != "" && clip.ActorVoiceID == actorVoiceID {
			return true
		}
		if _, ok := characterVoiceIDs[clip.CharacterVoiceID]; ok {
			return true
		}
	}
	return false
}

func (s *Store) projectsDependingOnVoice(matches func(Track) bool) ([]DependentProject, error) {
	projects, err := s.List()
	if err != nil {
		return nil, err
	}
	dependents := make([]DependentProject, 0)
	for _, project := range projects {
		for _, track := range project.Tracks {
			if matches(track) {
				dependents = append(dependents, DependentProject{ID: project.ID, Name: project.Name})
				break
			}
		}
	}
	sort.Slice(dependents, func(i, j int) bool {
		if dependents[i].Name == dependents[j].Name {
			return dependents[i].ID < dependents[j].ID
		}
		return dependents[i].Name < dependents[j].Name
	})
	return dependents, nil
}

func (s *Store) Update(id string, update ProjectUpdate) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !validProjectID(id) {
		return Project{}, ErrNotFound
	}
	name, err := validateName(update.Name)
	if err != nil {
		return Project{}, err
	}
	project, ok, err := s.Get(id)
	if err != nil {
		return Project{}, err
	}
	if !ok {
		return Project{}, ErrNotFound
	}
	timelineDurationMS := update.TimelineDurationMS
	if timelineDurationMS == 0 {
		timelineDurationMS = project.TimelineDurationMS
	}
	revoiceTrackIDs := make(map[string]struct{}, len(update.RevoiceTrackIDs))
	for _, id := range update.RevoiceTrackIDs {
		id = strings.TrimSpace(id)
		if !validTimelineID(id) {
			return Project{}, ErrInvalid
		}
		if _, exists := revoiceTrackIDs[id]; exists {
			return Project{}, ErrInvalid
		}
		revoiceTrackIDs[id] = struct{}{}
	}
	tracks, err := s.prepareTracks(project.Tracks, update.Tracks, revoiceTrackIDs)
	if err != nil {
		return Project{}, err
	}
	if err := validateTracks(tracks, timelineDurationMS); err != nil {
		return Project{}, err
	}
	if update.Revision != project.Revision {
		return Project{}, ErrConflict
	}
	project.Name = name
	project.TimelineDurationMS = timelineDurationMS
	project.Tracks = tracks
	project.Revision++
	project.UpdatedAt = s.now()
	data, err := encodeProject(project)
	if err != nil {
		return Project{}, err
	}
	if err := s.writeFileAtomic(filepath.Join(s.rootDir, id, manifestName), data); err != nil {
		return Project{}, fmt.Errorf("save Story Builder Project: %w", err)
	}
	return project, nil
}

func (s *Store) PlaceLibraryAudio(id string, placement LibraryAudioPlacement) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !validProjectID(id) {
		return Project{}, ErrNotFound
	}
	if placement.Revision < 1 || !validTimelineID(placement.TrackID) || !validTimelineID(placement.LibraryItemID) || placement.StartMS < 0 {
		return Project{}, ErrInvalid
	}
	project, ok, err := s.Get(id)
	if err != nil {
		return Project{}, err
	}
	if !ok {
		return Project{}, ErrNotFound
	}
	if placement.Revision != project.Revision {
		return Project{}, ErrConflict
	}
	var track *Track
	for i := range project.Tracks {
		if project.Tracks[i].ID == placement.TrackID {
			track = &project.Tracks[i]
			break
		}
	}
	if track == nil || s.resolveLibraryAudio == nil {
		return Project{}, ErrLibraryAudioNotFound
	}
	asset, ok, err := s.resolveLibraryAudio(placement.LibraryItemID)
	if err != nil {
		return Project{}, err
	}
	if !ok || asset.ID != placement.LibraryItemID || !validTimelineID(asset.ID) {
		return Project{}, ErrLibraryAudioNotFound
	}
	asset.Name = strings.TrimSpace(asset.Name)
	if asset.Name == "" || utf8.RuneCountInString(asset.Name) > MaxNameLength {
		return Project{}, ErrInvalid
	}
	clipType := ClipType("")
	switch asset.MediaRole {
	case MediaRoleSFX:
		if track.Type != TrackTypeSFX {
			return Project{}, ErrIncompatibleMedia
		}
		clipType = ClipTypeSFX
	case MediaRoleMusic:
		if track.Type != TrackTypeMusic {
			return Project{}, ErrIncompatibleMedia
		}
		clipType = ClipTypeMusic
	default:
		return Project{}, ErrIncompatibleMedia
	}
	if err := wav.ValidateBytes(asset.Data); err != nil {
		return Project{}, fmt.Errorf("validate Library audio: %w", err)
	}
	duration, err := wav.Duration(asset.Data)
	if err != nil || duration.Milliseconds() <= 0 {
		return Project{}, fmt.Errorf("read Library audio duration: %w", err)
	}
	durationMS := duration.Milliseconds()
	if placement.StartMS > math.MaxInt64-durationMS || placement.StartMS+durationMS > project.TimelineDurationMS {
		return Project{}, ErrInvalid
	}
	clipID, err := newTimelineID("clip", s.now())
	if err != nil {
		return Project{}, err
	}
	sourceID := "media_" + asset.ID
	if !validTimelineID(sourceID) {
		return Project{}, ErrInvalid
	}
	track.Clips = append(track.Clips, TimelineClip{
		ID: clipID, Type: clipType, Label: asset.Name, StartMS: placement.StartMS, DurationMS: durationMS,
		SourceID: sourceID, SourceDurationMS: durationMS, SourceOutMS: durationMS,
		SourceLibraryItemID: asset.ID, SourceLibraryName: asset.Name, SourceMediaRole: asset.MediaRole,
	})
	if err := validateTracks(project.Tracks, project.TimelineDurationMS); err != nil {
		return Project{}, err
	}

	mediaDir := filepath.Join(s.rootDir, id, "media")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		return Project{}, fmt.Errorf("create project media directory: %w", err)
	}
	mediaPath := filepath.Join(mediaDir, sourceID+".wav")
	createdMedia := false
	if existing, readErr := os.ReadFile(mediaPath); readErr == nil {
		if !bytes.Equal(existing, asset.Data) {
			return Project{}, fmt.Errorf("project media identity conflict")
		}
	} else if !os.IsNotExist(readErr) {
		return Project{}, fmt.Errorf("read project media: %w", readErr)
	} else {
		if err := s.writeFileAtomic(mediaPath, asset.Data); err != nil {
			return Project{}, fmt.Errorf("copy project media: %w", err)
		}
		createdMedia = true
	}

	project.Revision++
	project.UpdatedAt = s.now()
	data, err := encodeProject(project)
	if err == nil {
		err = s.writeFileAtomic(filepath.Join(s.rootDir, id, manifestName), data)
	}
	if err != nil {
		if createdMedia {
			_ = os.Remove(mediaPath)
		}
		return Project{}, fmt.Errorf("save Story Builder Project: %w", err)
	}
	return project, nil
}

func (s *Store) MediaPath(id, sourceID string) (string, error) {
	if !validProjectID(id) || !validTimelineID(sourceID) {
		return "", ErrProjectMediaNotFound
	}
	project, ok, err := s.Get(id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNotFound
	}
	referenced := false
	for _, track := range project.Tracks {
		for _, clip := range track.Clips {
			if (clip.Type == ClipTypeSFX || clip.Type == ClipTypeMusic) && clip.SourceID == sourceID {
				referenced = true
				break
			}
		}
	}
	if !referenced {
		return "", ErrProjectMediaNotFound
	}
	path := filepath.Join(s.rootDir, id, "media", sourceID+".wav")
	if err := wav.ValidateFile(path); err != nil {
		return "", ErrProjectMediaNotFound
	}
	return path, nil
}

func (s *Store) markMediaErrors(project *Project) {
	checked := make(map[string]string)
	for trackIndex := range project.Tracks {
		for clipIndex := range project.Tracks[trackIndex].Clips {
			clip := &project.Tracks[trackIndex].Clips[clipIndex]
			directory := "media"
			errorMessage := "project media is missing or unreadable"
			if clip.Type == ClipTypeDialogue {
				if clip.Status != DialogueStatusReady || clip.SourceID == "" {
					continue
				}
				directory = "takes"
				errorMessage = "dialogue take is missing or unreadable"
			} else if clip.Type != ClipTypeSFX && clip.Type != ClipTypeMusic {
				continue
			}
			cacheKey := directory + "/" + clip.SourceID
			errorText, seen := checked[cacheKey]
			if !seen {
				path := filepath.Join(s.rootDir, project.ID, directory, clip.SourceID+".wav")
				data, err := os.ReadFile(path)
				if err == nil {
					var duration time.Duration
					duration, err = wav.Duration(data)
					if err == nil && duration.Milliseconds() != clip.SourceDurationMS {
						err = fmt.Errorf("duration changed")
					}
				}
				if err != nil {
					errorText = errorMessage
				}
				checked[cacheKey] = errorText
			}
			clip.MediaError = errorText
		}
	}
}

func (s *Store) prepareTracks(existing, incoming []Track, revoiceTrackIDs map[string]struct{}) ([]Track, error) {
	tracks := cloneTracks(incoming)
	existingTracks := make(map[string]Track, len(existing))
	usedRevoiceTrackIDs := make(map[string]struct{}, len(revoiceTrackIDs))
	for _, track := range existing {
		existingTracks[track.ID] = track
	}
	for trackIndex := range tracks {
		track := &tracks[trackIndex]
		oldTrack, hadTrack := existingTracks[track.ID]
		if track.Type != TrackTypeDialogue {
			existingClips := make(map[string]TimelineClip, len(oldTrack.Clips))
			if hadTrack {
				for _, clip := range oldTrack.Clips {
					existingClips[clip.ID] = clip
				}
			}
			for clipIndex := range track.Clips {
				clip := &track.Clips[clipIndex]
				if clip.Type == ClipTypeSilence {
					continue
				}
				oldClip, ok := existingClips[clip.ID]
				if !ok || oldClip.Type != clip.Type {
					return nil, ErrInvalid
				}
				clip.SourceID = oldClip.SourceID
				clip.SourceDurationMS = oldClip.SourceDurationMS
				clip.SourceLibraryItemID = oldClip.SourceLibraryItemID
				clip.SourceLibraryName = oldClip.SourceLibraryName
				clip.SourceMediaRole = oldClip.SourceMediaRole
				clip.MediaError = oldClip.MediaError
			}
			continue
		}
		if track.CharacterVoiceID == "" && track.ActorVoiceID != "" &&
			(!hadTrack || oldTrack.CharacterVoiceID != "" || oldTrack.ActorVoiceID != track.ActorVoiceID) {
			// Direct Actor ids are retained for older manifests, but new voice
			// bindings must be selected through a resolvable Character Voice.
			return nil, ErrInvalid
		}
		if hadTrack && oldTrack.CharacterVoiceID != "" && oldTrack.CharacterVoiceID != track.CharacterVoiceID && hasDialogue(oldTrack) {
			if _, allowed := revoiceTrackIDs[track.ID]; !allowed {
				return nil, ErrVoiceConflict
			}
			usedRevoiceTrackIDs[track.ID] = struct{}{}
		}
		identity := VoiceIdentity{
			CharacterVoiceID: track.CharacterVoiceID,
			ActorVoiceID:     track.ActorVoiceID,
			Fingerprint:      track.VoiceFingerprint,
		}
		missingExistingVoice := false
		if track.CharacterVoiceID != "" && s.resolveCharacterVoice != nil {
			resolved, ok, err := s.resolveCharacterVoice(track.CharacterVoiceID)
			if err != nil {
				return nil, err
			}
			if !ok {
				if !hadTrack || oldTrack.CharacterVoiceID != track.CharacterVoiceID {
					return nil, ErrCharacterVoiceNotFound
				}
				missingExistingVoice = true
				identity = VoiceIdentity{
					CharacterVoiceID: oldTrack.CharacterVoiceID,
					ActorVoiceID:     oldTrack.ActorVoiceID,
					Fingerprint:      oldTrack.VoiceFingerprint,
				}
			} else {
				identity = resolved
			}
		}
		track.CharacterVoiceID = strings.TrimSpace(identity.CharacterVoiceID)
		track.ActorVoiceID = strings.TrimSpace(identity.ActorVoiceID)
		track.VoiceFingerprint = strings.TrimSpace(identity.Fingerprint)

		existingClips := make(map[string]TimelineClip, len(oldTrack.Clips))
		if hadTrack {
			for _, clip := range oldTrack.Clips {
				existingClips[clip.ID] = clip
			}
		}
		for clipIndex := range track.Clips {
			clip := &track.Clips[clipIndex]
			if clip.Type != ClipTypeDialogue {
				continue
			}
			// Generated dialogue sources are server-owned. Whole-project edits
			// may preserve the current take, but cannot introduce or replace it.
			clip.SourceID = ""
			clip.SourceDurationMS = 0
			clip.SourceInMS = 0
			clip.SourceOutMS = 0
			clip.MediaError = ""
			clip.Text = strings.TrimSpace(clip.Text)
			if clip.Text == "" {
				clip.Text = clip.Label
			}
			clip.CharacterVoiceID = track.CharacterVoiceID
			clip.ActorVoiceID = track.ActorVoiceID
			clip.VoiceFingerprint = track.VoiceFingerprint
			clip.Status = DialogueStatusStale
			clip.BuildError = ""
			clip.SourceStoryID = ""
			clip.SourceStoryLineID = ""
			clip.SourceStoryTakeID = ""
			if missingExistingVoice {
				clip.Status = DialogueStatusFailed
				clip.BuildError = ErrCharacterVoiceNotFound.Error()
				continue
			}
			if oldClip, ok := existingClips[clip.ID]; ok && oldClip.Type == ClipTypeDialogue &&
				strings.TrimSpace(oldClip.Text) == clip.Text && oldTrack.CharacterVoiceID == track.CharacterVoiceID &&
				oldTrack.ActorVoiceID == track.ActorVoiceID && oldTrack.VoiceFingerprint == track.VoiceFingerprint {
				clip.Status = dialogueStatus(oldClip)
				clip.BuildError = oldClip.BuildError
				clip.SourceID = oldClip.SourceID
				clip.SourceDurationMS = oldClip.SourceDurationMS
				clip.SourceInMS = oldClip.SourceInMS
				clip.SourceOutMS = oldClip.SourceOutMS
				clip.MediaError = oldClip.MediaError
				clip.SourceStoryID = oldClip.SourceStoryID
				clip.SourceStoryLineID = oldClip.SourceStoryLineID
				clip.SourceStoryTakeID = oldClip.SourceStoryTakeID
			}
		}
	}
	if len(usedRevoiceTrackIDs) != len(revoiceTrackIDs) {
		return nil, ErrInvalid
	}
	return tracks, nil
}

func hasDialogue(track Track) bool {
	for _, clip := range track.Clips {
		if clip.Type == ClipTypeDialogue {
			return true
		}
	}
	return false
}

func dialogueStatus(clip TimelineClip) DialogueStatus {
	switch clip.Status {
	case DialogueStatusStale, DialogueStatusBuilding, DialogueStatusReady, DialogueStatusFailed:
		return clip.Status
	}
	if clip.SourceID != "" {
		return DialogueStatusReady
	}
	return DialogueStatusStale
}

func validateTracks(tracks []Track, timelineDurationMS int64) error {
	if len(tracks) > MaxTracks || timelineDurationMS <= 0 {
		return ErrInvalid
	}
	trackIDs := make(map[string]struct{}, len(tracks))
	clipIDs := make(map[string]struct{})
	clipCount := 0
	for index, track := range tracks {
		if !validTimelineID(track.ID) || strings.TrimSpace(track.Name) == "" || utf8.RuneCountInString(track.Name) > MaxNameLength || track.Order != index {
			return ErrInvalid
		}
		if _, exists := trackIDs[track.ID]; exists {
			return ErrInvalid
		}
		trackIDs[track.ID] = struct{}{}
		if track.Type != TrackTypeDialogue && track.Type != TrackTypeSFX && track.Type != TrackTypeMusic {
			return ErrInvalid
		}
		if track.Type != TrackTypeDialogue && (track.CharacterVoiceID != "" || track.ActorVoiceID != "" || track.VoiceFingerprint != "") {
			return ErrInvalid
		}
		clipCount += len(track.Clips)
		if clipCount > MaxClips {
			return ErrInvalid
		}
		for _, clip := range track.Clips {
			if !validTimelineID(clip.ID) || strings.TrimSpace(clip.Label) == "" || utf8.RuneCountInString(clip.Label) > MaxNameLength ||
				clip.StartMS < 0 || clip.DurationMS <= 0 || clip.StartMS > math.MaxInt64-clip.DurationMS ||
				clip.StartMS+clip.DurationMS > timelineDurationMS {
				return ErrInvalid
			}
			if _, exists := clipIDs[clip.ID]; exists {
				return ErrInvalid
			}
			clipIDs[clip.ID] = struct{}{}
			hasSource := clip.SourceID != "" || clip.SourceDurationMS != 0 || clip.SourceInMS != 0 || clip.SourceOutMS != 0
			hasStoryProvenance := clip.SourceStoryID != "" || clip.SourceStoryLineID != "" || clip.SourceStoryTakeID != ""
			if hasSource {
				if clip.Type == ClipTypeSilence || !validTimelineID(clip.SourceID) || clip.SourceDurationMS <= 0 ||
					clip.SourceInMS < 0 || clip.SourceOutMS <= clip.SourceInMS || clip.SourceOutMS > clip.SourceDurationMS ||
					clip.DurationMS != clip.SourceOutMS-clip.SourceInMS {
					return ErrInvalid
				}
			}
			switch clip.Type {
			case ClipTypeSilence:
				if hasSource || clip.SourceLibraryItemID != "" || clip.SourceLibraryName != "" || clip.SourceMediaRole != "" || clip.MediaError != "" ||
					clip.Text != "" || clip.Status != "" || clip.CharacterVoiceID != "" || clip.ActorVoiceID != "" || clip.VoiceFingerprint != "" || clip.BuildError != "" || hasStoryProvenance {
					return ErrInvalid
				}
			case ClipTypeDialogue:
				if track.Type != TrackTypeDialogue || strings.TrimSpace(track.CharacterVoiceID) == "" ||
					strings.TrimSpace(clip.Text) == "" || utf8.RuneCountInString(clip.Text) > MaxDialogueTextLength ||
					clip.SourceLibraryItemID != "" || clip.SourceLibraryName != "" || clip.SourceMediaRole != "" || clip.MediaError != "" ||
					clip.CharacterVoiceID != track.CharacterVoiceID || clip.ActorVoiceID != track.ActorVoiceID ||
					clip.VoiceFingerprint != track.VoiceFingerprint || dialogueStatus(clip) != clip.Status {
					return ErrInvalid
				}
				if hasStoryProvenance && (clip.SourceStoryID == "" || clip.SourceStoryLineID == "" ||
					!validTimelineID(clip.SourceStoryID) || !validTimelineID(clip.SourceStoryLineID) ||
					(clip.SourceStoryTakeID != "" && !validTimelineID(clip.SourceStoryTakeID))) {
					return ErrInvalid
				}
			case ClipTypeSFX:
				if track.Type != TrackTypeSFX || !hasSource || clip.SourceLibraryItemID == "" || clip.SourceLibraryName == "" || clip.SourceMediaRole != MediaRoleSFX ||
					clip.Text != "" || clip.Status != "" || clip.CharacterVoiceID != "" || clip.ActorVoiceID != "" || clip.VoiceFingerprint != "" || clip.BuildError != "" || hasStoryProvenance {
					return ErrInvalid
				}
			case ClipTypeMusic:
				if track.Type != TrackTypeMusic || !hasSource || clip.SourceLibraryItemID == "" || clip.SourceLibraryName == "" || clip.SourceMediaRole != MediaRoleMusic ||
					clip.Text != "" || clip.Status != "" || clip.CharacterVoiceID != "" || clip.ActorVoiceID != "" || clip.VoiceFingerprint != "" || clip.BuildError != "" || hasStoryProvenance {
					return ErrInvalid
				}
			default:
				return ErrInvalid
			}
		}
		orderedClips := append([]TimelineClip(nil), track.Clips...)
		sort.Slice(orderedClips, func(i, j int) bool {
			return orderedClips[i].StartMS < orderedClips[j].StartMS
		})
		for i := 1; i < len(orderedClips); i++ {
			previousEnd := orderedClips[i-1].StartMS + orderedClips[i-1].DurationMS
			if orderedClips[i].StartMS < previousEnd {
				return ErrInvalid
			}
		}
	}
	return nil
}

func validTimelineID(id string) bool {
	if id == "" || len(id) > 120 {
		return false
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func cloneTracks(tracks []Track) []Track {
	cloned := make([]Track, len(tracks))
	copy(cloned, tracks)
	for i := range cloned {
		cloned[i].Name = strings.TrimSpace(cloned[i].Name)
		cloned[i].CharacterVoiceID = strings.TrimSpace(cloned[i].CharacterVoiceID)
		cloned[i].ActorVoiceID = strings.TrimSpace(cloned[i].ActorVoiceID)
		cloned[i].VoiceFingerprint = strings.TrimSpace(cloned[i].VoiceFingerprint)
		cloned[i].Clips = append([]TimelineClip(nil), tracks[i].Clips...)
		for j := range cloned[i].Clips {
			cloned[i].Clips[j].Label = strings.TrimSpace(cloned[i].Clips[j].Label)
			cloned[i].Clips[j].SourceID = strings.TrimSpace(cloned[i].Clips[j].SourceID)
			cloned[i].Clips[j].SourceLibraryItemID = strings.TrimSpace(cloned[i].Clips[j].SourceLibraryItemID)
			cloned[i].Clips[j].SourceLibraryName = strings.TrimSpace(cloned[i].Clips[j].SourceLibraryName)
			cloned[i].Clips[j].Text = strings.TrimSpace(cloned[i].Clips[j].Text)
			cloned[i].Clips[j].CharacterVoiceID = strings.TrimSpace(cloned[i].Clips[j].CharacterVoiceID)
			cloned[i].Clips[j].ActorVoiceID = strings.TrimSpace(cloned[i].Clips[j].ActorVoiceID)
			cloned[i].Clips[j].VoiceFingerprint = strings.TrimSpace(cloned[i].Clips[j].VoiceFingerprint)
		}
	}
	return cloned
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !validProjectID(id) {
		return ErrNotFound
	}
	if _, ok, err := s.Get(id); err != nil {
		return err
	} else if !ok {
		return ErrNotFound
	}
	if err := os.RemoveAll(filepath.Join(s.rootDir, id)); err != nil {
		return fmt.Errorf("delete Story Builder Project: %w", err)
	}
	return nil
}

func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxNameLength {
		return "", ErrInvalid
	}
	return name, nil
}

func validProjectID(id string) bool {
	if id == "" || strings.Contains(id, "..") || strings.ContainsAny(id, `/\`) || filepath.IsAbs(id) {
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

func newProjectID(now time.Time) (string, error) {
	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("mint Story Builder Project id: %w", err)
	}
	return fmt.Sprintf("sbp_%s_%s", now.Format("20060102_150405"), hex.EncodeToString(suffix)), nil
}

func newTimelineID(prefix string, now time.Time) (string, error) {
	suffix := make([]byte, 3)
	if _, err := rand.Read(suffix); err != nil {
		return "", fmt.Errorf("mint timeline id: %w", err)
	}
	return fmt.Sprintf("%s_%d_%s", prefix, now.UnixMilli(), hex.EncodeToString(suffix)), nil
}

func encodeProject(project Project) ([]byte, error) {
	data, err := json.MarshalIndent(project, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode Story Builder Project: %w", err)
	}
	return append(data, '\n'), nil
}

func writeFileAtomic(path string, data []byte) error {
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
