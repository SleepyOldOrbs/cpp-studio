// Package storybuilder owns durable Story Builder Project state.
package storybuilder

import (
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
)

const (
	DefaultRootDir            = "out/story-builder-projects"
	MaxNameLength             = 120
	MaxTracks                 = 128
	MaxClips                  = 2048
	DefaultTimelineDurationMS = 30000
	manifestName              = "project.json"
)

var (
	ErrInvalid  = errors.New("invalid Story Builder Project")
	ErrNotFound = errors.New("Story Builder Project not found")
	ErrConflict = errors.New("Story Builder Project revision conflict")
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
)

// TimelineClip is timing metadata. Audio-backed clips keep nondestructive
// source offsets; silence clips deliberately have no media source.
type TimelineClip struct {
	ID               string   `json:"id"`
	Type             ClipType `json:"type"`
	Label            string   `json:"label"`
	StartMS          int64    `json:"start_ms"`
	DurationMS       int64    `json:"duration_ms"`
	SourceID         string   `json:"source_id,omitempty"`
	SourceDurationMS int64    `json:"source_duration_ms,omitempty"`
	SourceInMS       int64    `json:"source_in_ms,omitempty"`
	SourceOutMS      int64    `json:"source_out_ms,omitempty"`
}

type Track struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Type             TrackType      `json:"type"`
	Order            int            `json:"order"`
	Muted            bool           `json:"muted"`
	CharacterVoiceID string         `json:"character_voice_id,omitempty"`
	Clips            []TimelineClip `json:"clips"`
}

type ProjectUpdate struct {
	Name               string  `json:"name"`
	Revision           int     `json:"revision"`
	TimelineDurationMS int64   `json:"timeline_duration_ms,omitempty"`
	Tracks             []Track `json:"tracks"`
}

// Project is one separately saved Story Builder production.
type Project struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Revision           int       `json:"revision"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
	TimelineDurationMS int64     `json:"timeline_duration_ms"`
	Tracks             []Track   `json:"tracks"`
}

type StoreOptions struct {
	WriteFileAtomic func(path string, data []byte) error
}

type Store struct {
	mu              sync.Mutex
	rootDir         string
	now             func() time.Time
	writeFileAtomic func(path string, data []byte) error
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
		rootDir:         rootDir,
		now:             func() time.Time { return time.Now().UTC() },
		writeFileAtomic: write,
	}
}

func (s *Store) Create(name string) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	name, err := validateName(name)
	if err != nil {
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
		project := Project{ID: id, Name: name, Revision: 1, CreatedAt: now, UpdatedAt: now, TimelineDurationMS: DefaultTimelineDurationMS, Tracks: []Track{}}
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
		published := false
		defer func() {
			if !published {
				_ = os.RemoveAll(stagingDir)
			}
		}()
		data, err := encodeProject(project)
		if err != nil {
			return Project{}, err
		}
		if err := os.WriteFile(filepath.Join(stagingDir, manifestName), data, 0o644); err != nil {
			return Project{}, fmt.Errorf("write Story Builder Project: %w", err)
		}
		if err := os.Rename(stagingDir, finalDir); err != nil {
			return Project{}, fmt.Errorf("publish Story Builder Project: %w", err)
		}
		published = true
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
	if project.TimelineDurationMS == 0 {
		project.TimelineDurationMS = minimumTimelineDurationMS(project.Tracks)
	}
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
	if err := validateTracks(update.Tracks, timelineDurationMS); err != nil {
		return Project{}, err
	}
	if update.Revision != project.Revision {
		return Project{}, ErrConflict
	}
	project.Name = name
	project.TimelineDurationMS = timelineDurationMS
	project.Tracks = cloneTracks(update.Tracks)
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
		if track.Type != TrackTypeDialogue && track.CharacterVoiceID != "" {
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
			if hasSource {
				if clip.Type == ClipTypeSilence || !validTimelineID(clip.SourceID) || clip.SourceDurationMS <= 0 ||
					clip.SourceInMS < 0 || clip.SourceOutMS <= clip.SourceInMS || clip.SourceOutMS > clip.SourceDurationMS ||
					clip.DurationMS != clip.SourceOutMS-clip.SourceInMS {
					return ErrInvalid
				}
			}
			switch clip.Type {
			case ClipTypeSilence:
			case ClipTypeDialogue:
				if track.Type != TrackTypeDialogue || strings.TrimSpace(track.CharacterVoiceID) == "" {
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
		cloned[i].Clips = append([]TimelineClip(nil), tracks[i].Clips...)
		for j := range cloned[i].Clips {
			cloned[i].Clips[j].Label = strings.TrimSpace(cloned[i].Clips[j].Label)
			cloned[i].Clips[j].SourceID = strings.TrimSpace(cloned[i].Clips[j].SourceID)
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
