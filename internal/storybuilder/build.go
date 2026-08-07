package storybuilder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"cpp-studio/internal/wav"
)

var (
	ErrDialogueBuildBusy    = errors.New("another Story Builder dialogue build is active")
	ErrDialogueEngineBusy   = errors.New("engine \"audio\" is busy")
	ErrNoDialogueToBuild    = errors.New("no retryable dialogue to build")
	ErrDialogueBuildMissing = errors.New("Story Builder dialogue build not found")
	ErrDialogueBuildStopped = errors.New("Story Builder dialogue build is not active")
)

type DialogueBuildStatus string

const (
	DialogueBuildQueued    DialogueBuildStatus = "queued"
	DialogueBuildRunning   DialogueBuildStatus = "running"
	DialogueBuildComplete  DialogueBuildStatus = "complete"
	DialogueBuildFailed    DialogueBuildStatus = "failed"
	DialogueBuildCancelled DialogueBuildStatus = "cancelled"
)

type DialogueBuild struct {
	ID           string              `json:"id"`
	ProjectID    string              `json:"project_id"`
	Status       DialogueBuildStatus `json:"status"`
	StatusURL    string              `json:"status_url"`
	CancelURL    string              `json:"cancel_url"`
	ActiveClipID string              `json:"active_clip_id,omitempty"`
	Completed    int                 `json:"completed"`
	Total        int                 `json:"total"`
	Progress     float64             `json:"progress"`
	Error        string              `json:"error,omitempty"`
}

type ReserveDialogueEngineFunc func(context.Context, string) (func(), bool)
type SynthesizeDialogueFunc func(context.Context, DialogueSynthesisInput) ([]byte, error)

type DialogueBuildManagerOptions struct {
	Store         *Store
	ReserveEngine ReserveDialogueEngineFunc
	Synthesize    SynthesizeDialogueFunc
	Now           func() time.Time
}

type DialogueBuildManager struct {
	mu            sync.Mutex
	store         *Store
	reserveEngine ReserveDialogueEngineFunc
	synthesize    SynthesizeDialogueFunc
	now           func() time.Time
	activeBuildID string
	builds        map[string]DialogueBuild
	latest        map[string]string
	cancels       map[string]context.CancelFunc
}

func NewDialogueBuildManager(options DialogueBuildManagerOptions) *DialogueBuildManager {
	now := options.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &DialogueBuildManager{
		store: options.Store, reserveEngine: options.ReserveEngine, synthesize: options.Synthesize,
		now: now, builds: make(map[string]DialogueBuild), latest: make(map[string]string),
		cancels: make(map[string]context.CancelFunc),
	}
}

func (m *DialogueBuildManager) Start(ctx context.Context, projectID string, revision int) (DialogueBuild, error) {
	if m == nil || m.store == nil || m.synthesize == nil {
		return DialogueBuild{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return DialogueBuild{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeBuildID != "" {
		return DialogueBuild{}, ErrDialogueBuildBusy
	}
	candidates, err := m.store.DialogueBuildCandidates(projectID, revision)
	if err != nil {
		return DialogueBuild{}, err
	}
	if len(candidates) == 0 {
		return DialogueBuild{}, ErrNoDialogueToBuild
	}
	buildCtx, cancel := context.WithCancel(context.Background())
	release := func() {}
	if m.reserveEngine != nil {
		var ok bool
		release, ok = m.reserveEngine(buildCtx, "audio")
		if !ok {
			cancel()
			return DialogueBuild{}, ErrDialogueEngineBusy
		}
	}
	id, err := newTimelineID("build", m.now())
	if err != nil {
		cancel()
		release()
		return DialogueBuild{}, err
	}
	statusURL := "/v1/story-builder-projects/" + projectID + "/builds/" + id
	build := DialogueBuild{
		ID: id, ProjectID: projectID, Status: DialogueBuildQueued, StatusURL: statusURL,
		CancelURL: statusURL + "/cancel",
		Total:     len(candidates),
	}
	m.builds[id] = build
	m.latest[projectID] = id
	m.cancels[id] = cancel
	m.activeBuildID = id
	go m.run(buildCtx, build, candidates, release)
	return build, nil
}

func (m *DialogueBuildManager) Status(projectID, buildID string) (DialogueBuild, bool) {
	if m == nil {
		return DialogueBuild{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	build, ok := m.builds[buildID]
	if !ok || build.ProjectID != projectID {
		return DialogueBuild{}, false
	}
	return build, true
}

func (m *DialogueBuildManager) Latest(projectID string) (DialogueBuild, bool) {
	if m == nil {
		return DialogueBuild{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id := m.latest[projectID]
	build, ok := m.builds[id]
	return build, ok
}

func (m *DialogueBuildManager) Cancel(projectID, buildID string) (DialogueBuild, error) {
	if m == nil {
		return DialogueBuild{}, ErrDialogueBuildMissing
	}
	m.mu.Lock()
	build, ok := m.builds[buildID]
	if !ok || build.ProjectID != projectID {
		m.mu.Unlock()
		return DialogueBuild{}, ErrDialogueBuildMissing
	}
	cancel, active := m.cancels[buildID]
	if !active || (build.Status != DialogueBuildQueued && build.Status != DialogueBuildRunning) {
		m.mu.Unlock()
		return DialogueBuild{}, ErrDialogueBuildStopped
	}
	m.mu.Unlock()
	cancel()
	return build, nil
}

func (m *DialogueBuildManager) run(ctx context.Context, build DialogueBuild, candidates []DialogueBuildClip, release func()) {
	defer release()
	for _, selected := range candidates {
		if ctx.Err() != nil {
			m.cancelled(build.ID)
			return
		}
		m.update(build.ID, func(status *DialogueBuild) {
			status.Status = DialogueBuildRunning
			status.ActiveClipID = selected.ClipID
		})
		input, err := m.store.BeginDialogueBuild(build.ProjectID, selected.ClipID)
		if err != nil {
			m.fail(build.ID, err)
			return
		}
		audio, err := m.synthesize(ctx, input)
		if ctx.Err() != nil {
			if _, cancelErr := m.store.CancelDialogueBuild(build.ProjectID, input.ClipID); cancelErr != nil {
				m.fail(build.ID, cancelErr)
				return
			}
			m.cancelled(build.ID)
			return
		}
		if err == nil {
			_, err = m.store.CompleteDialogueBuild(build.ProjectID, input, audio)
		}
		if err != nil {
			if _, persistErr := m.store.FailDialogueBuild(build.ProjectID, input.ClipID, err.Error()); persistErr != nil {
				err = persistErr
			}
			m.fail(build.ID, err)
			return
		}
		m.update(build.ID, func(status *DialogueBuild) {
			status.Completed++
			status.Progress = float64(status.Completed) / float64(status.Total)
			status.ActiveClipID = ""
		})
	}
	m.mu.Lock()
	status := m.builds[build.ID]
	status.Status = DialogueBuildComplete
	status.ActiveClipID = ""
	status.Progress = 1
	m.builds[build.ID] = status
	m.activeBuildID = ""
	delete(m.cancels, build.ID)
	m.mu.Unlock()
}

func (m *DialogueBuildManager) update(id string, apply func(*DialogueBuild)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := m.builds[id]
	apply(&status)
	m.builds[id] = status
}

func (m *DialogueBuildManager) fail(id string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := m.builds[id]
	status.Status = DialogueBuildFailed
	status.ActiveClipID = ""
	status.Error = boundedBuildError(err.Error())
	m.builds[id] = status
	m.activeBuildID = ""
	delete(m.cancels, id)
}

func (m *DialogueBuildManager) cancelled(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := m.builds[id]
	status.Status = DialogueBuildCancelled
	status.ActiveClipID = ""
	status.Error = ""
	m.builds[id] = status
	m.activeBuildID = ""
	delete(m.cancels, id)
}

// DialogueBuildClip identifies one clip selected for a build pass.
type DialogueBuildClip struct {
	ClipID     string
	StartMS    int64
	TrackOrder int
}

// DialogueSynthesisInput is the current spoken content and performance identity
// frozen when one clip enters the building state.
type DialogueSynthesisInput struct {
	ClipID           string
	Text             string
	ActorVoiceID     string
	Direction        string
	VoiceFingerprint string
}

// DialogueBuildCandidates returns retryable dialogue in timeline order. A
// building clip is retryable because it can remain after a failed status save.
func (s *Store) DialogueBuildCandidates(id string, revision int) ([]DialogueBuildClip, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, ok, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}
	if revision != project.Revision {
		return nil, ErrConflict
	}
	candidates := make([]DialogueBuildClip, 0)
	for _, track := range project.Tracks {
		if track.Type != TrackTypeDialogue {
			continue
		}
		for _, clip := range track.Clips {
			if clip.Type != ClipTypeDialogue || (clip.Status != DialogueStatusStale && clip.Status != DialogueStatusFailed && clip.Status != DialogueStatusBuilding) {
				continue
			}
			candidates = append(candidates, DialogueBuildClip{
				ClipID: clip.ID, StartMS: clip.StartMS, TrackOrder: track.Order,
			})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].StartMS != candidates[j].StartMS {
			return candidates[i].StartMS < candidates[j].StartMS
		}
		return candidates[i].TrackOrder < candidates[j].TrackOrder
	})
	return candidates, nil
}

// BeginDialogueBuild durably marks one eligible clip as the active build clip.
func (s *Store) BeginDialogueBuild(id, clipID string) (DialogueSynthesisInput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, clip, err := s.dialogueBuildClip(id, clipID)
	if err != nil {
		return DialogueSynthesisInput{}, err
	}
	if clip.Status != DialogueStatusStale && clip.Status != DialogueStatusFailed && clip.Status != DialogueStatusBuilding {
		return DialogueSynthesisInput{}, ErrConflict
	}
	direction := ""
	if s.resolveCharacterVoice != nil {
		identity, ok, err := s.resolveCharacterVoice(clip.CharacterVoiceID)
		if err != nil {
			return DialogueSynthesisInput{}, err
		}
		if !ok {
			return DialogueSynthesisInput{}, ErrCharacterVoiceNotFound
		}
		direction = identity.Direction
	}
	input := DialogueSynthesisInput{
		ClipID: clip.ID, Text: clip.Text, ActorVoiceID: clip.ActorVoiceID,
		Direction: direction, VoiceFingerprint: clip.VoiceFingerprint,
	}
	clip.Status = DialogueStatusBuilding
	clip.BuildError = ""
	if _, err := s.saveDialogueBuildProject(project); err != nil {
		return DialogueSynthesisInput{}, err
	}
	return input, nil
}

// CompleteDialogueBuild validates and publishes one immutable project-owned take.
func (s *Store) CompleteDialogueBuild(id string, input DialogueSynthesisInput, audio []byte) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := wav.ValidateBytes(audio); err != nil {
		return Project{}, fmt.Errorf("validate generated dialogue: %w", err)
	}
	duration, err := wav.Duration(audio)
	if err != nil || duration <= 0 {
		return Project{}, fmt.Errorf("read generated dialogue duration: %v", err)
	}
	project, clip, err := s.dialogueBuildClip(id, input.ClipID)
	if err != nil {
		return Project{}, err
	}
	if clip.Status != DialogueStatusBuilding || clip.Text != input.Text || clip.ActorVoiceID != input.ActorVoiceID ||
		clip.VoiceFingerprint != input.VoiceFingerprint {
		return Project{}, ErrConflict
	}
	sourceID, err := newTimelineID("take", s.now())
	if err != nil {
		return Project{}, err
	}
	takesDir := filepath.Join(s.rootDir, id, "takes")
	if err := os.MkdirAll(takesDir, 0o755); err != nil {
		return Project{}, fmt.Errorf("create project takes directory: %w", err)
	}
	takePath := filepath.Join(takesDir, sourceID+".wav")
	if err := s.writeFileAtomic(takePath, audio); err != nil {
		return Project{}, fmt.Errorf("write generated dialogue take: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.Remove(takePath)
		}
	}()

	durationMS := duration.Milliseconds()
	clip.SourceID = sourceID
	clip.SourceDurationMS = durationMS
	clip.SourceInMS = 0
	clip.SourceOutMS = durationMS
	if clip.DurationMS > durationMS {
		clip.DurationMS = durationMS
		clip.SourceOutMS = durationMS
	} else {
		clip.SourceOutMS = clip.DurationMS
	}
	clip.Status = DialogueStatusReady
	clip.BuildError = ""
	clip.MediaError = ""
	if err := validateTracks(project.Tracks, project.TimelineDurationMS); err != nil {
		return Project{}, err
	}
	project, err = s.saveDialogueBuildProject(project)
	if err != nil {
		return Project{}, err
	}
	published = true
	return project, nil
}

// FailDialogueBuild durably records a failed active clip for a later retry.
func (s *Store) FailDialogueBuild(id, clipID, detail string) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, clip, err := s.dialogueBuildClip(id, clipID)
	if err != nil {
		return Project{}, err
	}
	if clip.Status != DialogueStatusBuilding {
		return Project{}, ErrConflict
	}
	clip.Status = DialogueStatusFailed
	clip.BuildError = boundedBuildError(detail)
	return s.saveDialogueBuildProject(project)
}

// CancelDialogueBuild durably returns the active clip to the retryable stale state.
func (s *Store) CancelDialogueBuild(id, clipID string) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	project, clip, err := s.dialogueBuildClip(id, clipID)
	if err != nil {
		return Project{}, err
	}
	if clip.Status != DialogueStatusBuilding {
		return Project{}, ErrConflict
	}
	clip.Status = DialogueStatusStale
	clip.BuildError = ""
	return s.saveDialogueBuildProject(project)
}

// DialogueAudioPath resolves only the validated current take of a ready clip.
func (s *Store) DialogueAudioPath(id, clipID string) (string, error) {
	project, ok, err := s.Get(id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNotFound
	}
	for _, track := range project.Tracks {
		for _, clip := range track.Clips {
			if clip.ID != clipID || clip.Type != ClipTypeDialogue || clip.Status != DialogueStatusReady || !validTimelineID(clip.SourceID) {
				continue
			}
			path := filepath.Join(s.rootDir, id, "takes", clip.SourceID+".wav")
			if err := wav.ValidateFile(path); err != nil {
				return "", ErrProjectMediaNotFound
			}
			return path, nil
		}
	}
	return "", ErrProjectMediaNotFound
}

func (s *Store) dialogueBuildClip(id, clipID string) (Project, *TimelineClip, error) {
	if !validTimelineID(clipID) {
		return Project{}, nil, ErrNotFound
	}
	project, ok, err := s.Get(id)
	if err != nil {
		return Project{}, nil, err
	}
	if !ok {
		return Project{}, nil, ErrNotFound
	}
	for trackIndex := range project.Tracks {
		for clipIndex := range project.Tracks[trackIndex].Clips {
			clip := &project.Tracks[trackIndex].Clips[clipIndex]
			if clip.ID == clipID && clip.Type == ClipTypeDialogue {
				return project, clip, nil
			}
		}
	}
	return Project{}, nil, ErrNotFound
}

func (s *Store) saveDialogueBuildProject(project Project) (Project, error) {
	project.Revision++
	project.UpdatedAt = s.now()
	data, err := encodeProject(project)
	if err == nil {
		err = s.writeFileAtomic(filepath.Join(s.rootDir, project.ID, manifestName), data)
	}
	if err != nil {
		return Project{}, fmt.Errorf("save Story Builder dialogue build: %w", err)
	}
	return project, nil
}

func boundedBuildError(detail string) string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return "dialogue synthesis failed"
	}
	const maxRunes = 1000
	runes := []rune(detail)
	if len(runes) > maxRunes {
		detail = string(runes[:maxRunes])
	}
	return detail
}
