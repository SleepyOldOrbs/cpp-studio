package story

import (
	"context"
	"fmt"
	"sync"
	"time"

	"cpp-studio/internal/jobs"
	"cpp-studio/internal/wav"
)

type ReserveEngineFunc func(ctx context.Context, name string) (func(), bool)

// SynthesizeFunc speaks one script line through the audio engine while the
// manager holds the engine's reservation; voiceID selects a stored cloned
// voice ("" means the studio default). nil disables voice_mode "fixed".
type SynthesizeFunc func(ctx context.Context, text string, voiceID string) ([]byte, error)

// ScriptRequest is what a script writer needs: the subject, the pacing
// target, the cast to write for, and — depending on Mode — either the
// grounded facts to cite or the premise and style to invent from.
type ScriptRequest struct {
	Subject       string
	Mode          string
	Premise       string
	Style         string
	TargetSeconds int
	Facts         []FactCard
	Cast          []CastMember
}

// ScriptFunc writes the story: a title plus script lines whose fact_ids
// must cite the request's facts in grounded mode. nil falls back to the
// deterministic fixture script.
type ScriptFunc func(ctx context.Context, req ScriptRequest) (string, []ScriptLine, error)

type ManagerOptions struct {
	RootDir       string
	ReserveEngine ReserveEngineFunc
	Synthesize    SynthesizeFunc
	Script        ScriptFunc
	StageDelay    time.Duration
	Now           func() time.Time
	// Jobs, when set, mirrors every story job into the gateway-wide job
	// registry so /v1/jobs lists and cancels stories alongside other async
	// work. The story manager remains the source of truth.
	Jobs *jobs.Registry
}

type Manager struct {
	mu            sync.Mutex
	store         *Store
	reserveEngine ReserveEngineFunc
	synthesize    SynthesizeFunc
	script        ScriptFunc
	stageDelay    time.Duration
	now           func() time.Time
	counter       int
	activeID      string
	jobs          map[string]*job
	registry      *jobs.Registry
	// edits serializes take-room mutations per story. Retake, EditLine and
	// Render each load the manifest, change it and save it back; without
	// this, two retakes both read one take, both mint "take-002", and one
	// overwrites the other's audio and manifest entry.
	edits   map[string]*sync.Mutex
	editsMu sync.Mutex
}

// editLock returns the mutation lock for one story, creating it on first
// use. Locks are keyed by story id and outlive individual requests.
func (m *Manager) editLock(storyID string) *sync.Mutex {
	m.editsMu.Lock()
	defer m.editsMu.Unlock()
	if lock, ok := m.edits[storyID]; ok {
		return lock
	}
	lock := &sync.Mutex{}
	m.edits[storyID] = lock
	return lock
}

type job struct {
	id      string
	req     NormalizedRequest
	cancel  context.CancelFunc
	release func()
	status  StatusResponse
}

func NewManager(opts ManagerOptions) *Manager {
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	stageDelay := opts.StageDelay
	if stageDelay == 0 {
		stageDelay = 25 * time.Millisecond
	}
	return &Manager{
		store:         NewStore(opts.RootDir),
		reserveEngine: opts.ReserveEngine,
		synthesize:    opts.Synthesize,
		script:        opts.Script,
		stageDelay:    stageDelay,
		now:           now,
		jobs:          make(map[string]*job),
		registry:      opts.Jobs,
		edits:         make(map[string]*sync.Mutex),
	}
}

func (m *Manager) Submit(ctx context.Context, req CreateRequest) (CreateResponse, error) {
	normalized, err := ValidateCreateRequest(req)
	if err != nil {
		return CreateResponse{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeID != "" {
		return CreateResponse{}, NewError(CodeStoryBusy, "another story job is already active")
	}
	var release func()
	if m.reserveEngine != nil {
		var ok bool
		release, ok = m.reserveEngine(ctx, "audio")
		if !ok {
			return CreateResponse{}, NewError(CodeEngineBusy, "engine \"audio\" is busy")
		}
	}
	m.counter++
	id := fmt.Sprintf("story_%s_%03d", m.now().UTC().Format("20060102_150405"), m.counter)
	jobCtx, cancel := context.WithCancel(context.Background())
	j := &job{
		id:      id,
		req:     normalized,
		cancel:  cancel,
		release: release,
		status: StatusResponse{
			ID:           id,
			Status:       StatusQueued,
			Stage:        StatusQueued,
			Progress:     0,
			Error:        nil,
			ArtifactURL:  nil,
			RetryAfterMS: DefaultRetryAfterMillis,
		},
	}
	m.jobs[id] = j
	m.activeID = id
	if m.registry != nil {
		m.registry.Track(id, "story", func() { _, _ = m.Cancel(id) })
	}

	go m.run(jobCtx, j)

	_ = ctx
	return CreateResponse{
		ID:        id,
		Status:    StatusQueued,
		StatusURL: "/v1/stories/" + id,
	}, nil
}

func (m *Manager) Status(id string) (StatusResponse, bool, error) {
	m.mu.Lock()
	if j, ok := m.jobs[id]; ok {
		status := j.status
		m.mu.Unlock()
		return status, true, nil
	}
	m.mu.Unlock()

	manifest, ok, err := m.store.Load(id)
	if err != nil || !ok {
		return StatusResponse{}, ok, err
	}
	artifactURL := manifest.Audio.URL
	return StatusResponse{
		ID:          manifest.ID,
		Status:      StatusComplete,
		Stage:       StatusComplete,
		Progress:    1,
		Error:       nil,
		ArtifactURL: &artifactURL,
		Manifest:    &manifest,
	}, true, nil
}

func (m *Manager) Cancel(id string) (StatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		if _, exists, err := m.store.Load(id); err != nil {
			return StatusResponse{}, err
		} else if exists {
			return StatusResponse{}, NewError(CodeCannotCancel, "story is already complete")
		}
		return StatusResponse{}, NewError(CodeNotFound, "story not found")
	}
	if j.status.Status == StatusComplete || j.status.Status == StatusFailed || j.status.Status == StatusCancelled {
		return StatusResponse{}, NewError(CodeCannotCancel, "story cannot be cancelled from its current state")
	}
	j.cancel()
	j.status.Status = StatusCancelled
	j.status.Stage = StatusCancelled
	j.status.Progress = 0
	j.status.Error = nil
	j.status.RetryAfterMS = 0
	if m.registry != nil {
		m.registry.MarkCancelled(id)
	}
	return j.status, nil
}

func (m *Manager) List() ([]Summary, error) {
	return m.store.List()
}

// ArtifactPath resolves a whitelisted artifact of a stored story: the
// current mix, one take, or one render revision.
func (m *Manager) ArtifactPath(id string, segments ...string) (string, error) {
	return m.store.ArtifactPath(id, segments...)
}

func (m *Manager) run(ctx context.Context, j *job) {
	if j.release != nil {
		defer j.release()
	}

	// A sketch has no sources to extract, so it never reports that stage.
	if j.req.Mode != ModeSketch {
		if !m.advance(ctx, j, StatusExtractingSources, 0.15) {
			return
		}
	}
	scaffold, err := BuildScaffold(j.req)
	if err != nil {
		m.fail(j, err)
		return
	}
	if !m.advance(ctx, j, StatusPlanning, 0.35) {
		return
	}
	if !m.advance(ctx, j, StatusScripting, 0.55) {
		return
	}
	if ctx.Err() != nil || m.isCancelled(j) {
		m.cancelled(j)
		return
	}

	title, script, err := m.writeScript(ctx, j.req, scaffold)
	if err != nil {
		m.fail(j, err)
		return
	}
	if ctx.Err() != nil || m.isCancelled(j) {
		m.cancelled(j)
		return
	}

	createdAt := m.now()
	manifest, err := AssembleManifest(j.id, j.req, createdAt, scaffold, title, script)
	if err != nil {
		m.fail(j, err)
		return
	}
	audio := fixtureWAV(j.req.TargetSeconds)

	var clips [][]byte
	if j.req.VoiceMode == "fixed" && m.synthesize != nil {
		var ok bool
		clips, ok = m.synthesizeScript(ctx, j, manifest.Script, j.req.CastVoices)
		if !ok {
			return
		}
		if !m.advance(ctx, j, StatusStitching, 0.9) {
			return
		}
		stitched, err := stitchTakes(clips, manifest.Script)
		if err != nil {
			m.fail(j, NewError(CodeSynthesisFailure, "stitch story audio: "+err.Error()))
			return
		}
		if duration, err := wav.Duration(stitched); err == nil {
			manifest.DurationSeconds = int(duration.Round(time.Second) / time.Second)
		}
		// Pad the finished artifact with lead/trail silence so playback
		// devices that swallow the first fraction of a second don't clip the
		// opening line. Duration above stays the speech length.
		if padded, err := wav.PadSilence(stitched, artifactPad, artifactPad); err == nil {
			stitched = padded
		}
		audio = stitched
	} else {
		if !m.advance(ctx, j, StatusSynthesizing, 0.75) {
			return
		}
		if !m.advance(ctx, j, StatusStitching, 0.9) {
			return
		}
	}
	if ctx.Err() != nil || m.isCancelled(j) {
		m.cancelled(j)
		return
	}
	if err := m.store.Save(manifest, audio); err != nil {
		m.fail(j, NewError(CodeStoreFailure, err.Error()))
		return
	}
	// Keep every line's own recording, and archive this stitch as revision 1.
	// A story that only kept its mix would have to be regenerated whole to
	// fix one bad read; with takes on disk, one line can be replaced.
	if err := m.retainFirstRender(&manifest, clips, audio, createdAt); err != nil {
		m.fail(j, NewError(CodeStoreFailure, err.Error()))
		return
	}
	artifactURL := manifest.Audio.URL
	m.mu.Lock()
	if j.status.Status == StatusCancelled {
		if m.activeID == j.id {
			m.activeID = ""
		}
		m.mu.Unlock()
		_ = m.store.Delete(j.id)
		return
	}
	j.status.Status = StatusComplete
	j.status.Stage = StatusComplete
	j.status.Progress = 1
	j.status.ArtifactURL = &artifactURL
	j.status.Manifest = &manifest
	j.status.RetryAfterMS = 0
	if m.activeID == j.id {
		m.activeID = ""
	}
	if m.registry != nil {
		m.registry.Complete(j.id, map[string]string{"artifactUrl": artifactURL, "title": manifest.Title})
	}
	m.mu.Unlock()
}

// lineGap is the silence inserted between spoken script lines.
const (
	lineGap = 350 * time.Millisecond
	// artifactPad is the lead/trail silence around the stitched story WAV.
	artifactPad = 250 * time.Millisecond
)

// writeScript produces the story's title and script: a user-supplied script
// (the draft → edit → produce flow) wins, then the injected ScriptFunc, then
// the deterministic fixture script.
func (m *Manager) writeScript(ctx context.Context, req NormalizedRequest, scaffold Scaffold) (string, []ScriptLine, error) {
	if len(req.Script) > 0 {
		title := req.Title
		if title == "" {
			title = titleForSubject(req.Subject)
		}
		return title, req.Script, nil
	}
	if m.script == nil {
		return titleForRequest(req), FixtureScript(req, scaffold), nil
	}
	return m.script(ctx, ScriptRequest{
		Subject:       req.Subject,
		Mode:          req.Mode,
		Premise:       req.Premise,
		Style:         req.Style,
		TargetSeconds: req.TargetSeconds,
		Facts:         scaffold.Facts,
		Cast:          scaffold.Cast,
	})
}

// Draft writes a story without producing it: validate, scaffold, script —
// no job, no audio reservation, no storage. Safe to run concurrently with
// an active story job.
func (m *Manager) Draft(ctx context.Context, req CreateRequest) (DraftResponse, error) {
	normalized, err := ValidateCreateRequest(req)
	if err != nil {
		return DraftResponse{}, err
	}
	scaffold, err := BuildScaffold(normalized)
	if err != nil {
		return DraftResponse{}, err
	}
	title, script, err := m.writeScript(ctx, normalized, scaffold)
	if err != nil {
		return DraftResponse{}, err
	}
	// Ground the draft exactly as production would, so an edited version
	// that keeps these lines is guaranteed to produce.
	if _, err := AssembleManifest("draft", normalized, m.now(), scaffold, title, script); err != nil {
		return DraftResponse{}, err
	}
	return DraftResponse{
		Subject:     normalized.Subject,
		Mode:        normalized.Mode,
		Title:       title,
		Sources:     scaffold.Sources,
		SourceNotes: scaffold.Notes,
		FactCards:   scaffold.Facts,
		Cast:        scaffold.Cast,
		Script:      script,
	}, nil
}

// synthesizeScript speaks each script line through the injected synthesizer
// with the speaker's assigned voice, walking progress across the
// synthesizing stage. Lines are synthesized grouped by voice — resident TTS
// sessions keep a single-slot voice-prompt cache, so grouping re-conditions
// once per voice instead of once per speaker change — while clips return in
// script order. It reports false when the job was cancelled or failed
// (status already updated).
func (m *Manager) synthesizeScript(ctx context.Context, j *job, script []ScriptLine, castVoices map[string]string) ([][]byte, bool) {
	byVoice := make(map[string][]int)
	var voiceOrder []string
	for i, line := range script {
		voiceID := castVoices[line.SpeakerID]
		if _, seen := byVoice[voiceID]; !seen {
			voiceOrder = append(voiceOrder, voiceID)
		}
		byVoice[voiceID] = append(byVoice[voiceID], i)
	}

	clips := make([][]byte, len(script))
	done := 0
	for _, voiceID := range voiceOrder {
		for _, idx := range byVoice[voiceID] {
			if ctx.Err() != nil || m.isCancelled(j) {
				m.cancelled(j)
				return nil, false
			}
			m.setStage(j, StatusSynthesizing, 0.55+0.3*float64(done)/float64(len(script)))
			clip, err := m.synthesize(ctx, script[idx].Text, voiceID)
			if err != nil {
				m.fail(j, NewError(CodeSynthesisFailure, fmt.Sprintf("synthesize script line %d: %v", idx+1, err)))
				return nil, false
			}
			clips[idx] = clip
			done++
		}
	}
	return clips, true
}

// setStage updates status/progress without the stage-delay pause; synthesis
// has its own natural pacing.
func (m *Manager) setStage(j *job, stage Status, progress float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j.status.Status == StatusCancelled {
		return
	}
	j.status.Status = stage
	j.status.Stage = stage
	j.status.Progress = progress
	j.status.RetryAfterMS = DefaultRetryAfterMillis
	if m.registry != nil {
		m.registry.Update(j.id, progress, string(stage))
	}
}

func (m *Manager) advance(ctx context.Context, j *job, stage Status, progress float64) bool {
	m.mu.Lock()
	if j.status.Status == StatusCancelled {
		if m.activeID == j.id {
			m.activeID = ""
		}
		m.mu.Unlock()
		return false
	}
	j.status.Status = stage
	j.status.Stage = stage
	j.status.Progress = progress
	j.status.RetryAfterMS = DefaultRetryAfterMillis
	if m.registry != nil {
		m.registry.Update(j.id, progress, string(stage))
	}
	m.mu.Unlock()

	timer := time.NewTimer(m.stageDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		m.cancelled(j)
		return false
	case <-timer.C:
		return true
	}
}

func (m *Manager) fail(j *job, err error) {
	storyErr, ok := err.(*StoryError)
	if !ok {
		storyErr = NewError(CodeStoreFailure, err.Error())
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if j.status.Status == StatusCancelled {
		return
	}
	j.status.Status = StatusFailed
	j.status.Stage = StatusFailed
	j.status.Progress = 0
	j.status.Error = storyErr
	j.status.RetryAfterMS = 0
	if m.activeID == j.id {
		m.activeID = ""
	}
	if m.registry != nil {
		m.registry.Fail(j.id, storyErr.Message)
	}
}

func (m *Manager) isCancelled(j *job) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return j.status.Status == StatusCancelled
}

func (m *Manager) cancelled(j *job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j.status.Status = StatusCancelled
	j.status.Stage = StatusCancelled
	j.status.Progress = 0
	j.status.RetryAfterMS = 0
	if m.activeID == j.id {
		m.activeID = ""
	}
	if m.registry != nil {
		m.registry.MarkCancelled(j.id)
	}
}
