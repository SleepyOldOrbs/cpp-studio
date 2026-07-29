package story

import (
	"context"
	"fmt"
	"sort"
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
	// Transcode, when set, enables delivery exports (MP3/Opus) of render
	// revisions. nil means the optional ffmpeg engine is not configured.
	Transcode TranscodeFunc
	// Measure, when set, masters every render: speakers are levelled against
	// each other and the finished piece is placed at the delivery target.
	// nil leaves renders exactly as they were stitched.
	Measure MeasureFunc
	// SynthesisFingerprint names the synthesis configuration behind
	// Synthesize — engine binary, args, models — and is stamped on every
	// take. A resume keeps only takes whose fingerprint matches it; empty
	// means unfingerprinted, which never matches, so a resume under an
	// unknown configuration re-synthesizes rather than splices.
	SynthesisFingerprint string
	StageDelay           time.Duration
	Now                  func() time.Time
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
	transcode     TranscodeFunc
	measure       MeasureFunc
	fingerprint   string
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
		transcode:     opts.Transcode,
		measure:       opts.Measure,
		fingerprint:   opts.SynthesisFingerprint,
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
	if err != nil {
		return StatusResponse{}, false, err
	}
	if !ok {
		// Not stored, not running: an interrupted production still reports —
		// its work-in-progress manifest says what exists and how far the
		// synthesis got, which is what a resume decision needs to see.
		wip, wipOk, wipErr := m.store.LoadWIP(id)
		if wipErr != nil || !wipOk {
			return StatusResponse{}, false, wipErr
		}
		recorded := 0
		for _, line := range wip.Script {
			if line.CurrentTake != "" {
				recorded++
			}
		}
		progress := 0.0
		if len(wip.Script) > 0 {
			progress = float64(recorded) / float64(len(wip.Script))
		}
		return StatusResponse{
			ID:       wip.ID,
			Status:   StatusInterrupted,
			Stage:    StatusInterrupted,
			Progress: progress,
			Manifest: &wip,
		}, true, nil
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
	summaries, err := m.store.List()
	if err != nil {
		return nil, err
	}
	// Interrupted productions list alongside finished stories so the
	// console can offer to resume or discard them. The one currently being
	// produced is running, not interrupted, so it is skipped.
	wips, err := m.store.ListWIP()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	active := m.activeID
	m.mu.Unlock()
	for _, wip := range wips {
		if wip.ID == active {
			continue
		}
		summaries = append(summaries, Summary{
			ID:              wip.ID,
			Subject:         wip.Subject,
			Mode:            wip.Mode,
			Title:           wip.Title,
			Status:          StatusInterrupted,
			CreatedAt:       wip.CreatedAt,
			DurationSeconds: wip.DurationSeconds,
		})
	}
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].CreatedAt.After(summaries[j].CreatedAt)
	})
	return summaries, nil
}

// Resume finishes an interrupted production: the work-in-progress manifest
// is read back, lines whose takes were made by the current synthesis
// configuration are kept, and everything else is synthesized again. It is
// a story job like any other — same single-active gate, same engine
// reservation, same status lifecycle, same id.
func (m *Manager) Resume(ctx context.Context, id string) (CreateResponse, error) {
	if m.synthesize == nil {
		return CreateResponse{}, NewError(CodeSynthesisFailure, "no audio engine is configured for synthesis")
	}
	m.mu.Lock()
	if m.activeID != "" {
		m.mu.Unlock()
		return CreateResponse{}, NewError(CodeStoryBusy, "another story job is already active")
	}
	m.mu.Unlock()

	manifest, ok, err := m.store.LoadWIP(id)
	if err != nil {
		return CreateResponse{}, NewError(CodeStoreFailure, err.Error())
	}
	if !ok {
		return CreateResponse{}, NewError(CodeNotFound, "no interrupted production with this id")
	}
	// A placeholder story has no takes to keep and costs nothing to make;
	// resubmitting is the honest path for it.
	if manifest.VoiceMode != "fixed" {
		return CreateResponse{}, NewError(CodeNotResumable, "only fixed-voice productions resume; submit the story again instead")
	}

	m.mu.Lock()
	if m.activeID != "" {
		m.mu.Unlock()
		return CreateResponse{}, NewError(CodeStoryBusy, "another story job is already active")
	}
	var release func()
	if m.reserveEngine != nil {
		var reserved bool
		release, reserved = m.reserveEngine(ctx, "audio")
		if !reserved {
			m.mu.Unlock()
			return CreateResponse{}, NewError(CodeEngineBusy, "engine \"audio\" is busy")
		}
	}
	jobCtx, cancel := context.WithCancel(context.Background())
	j := &job{
		id:      id,
		cancel:  cancel,
		release: release,
		status: StatusResponse{
			ID:           id,
			Status:       StatusQueued,
			Stage:        StatusQueued,
			Progress:     0,
			RetryAfterMS: DefaultRetryAfterMillis,
		},
	}
	m.jobs[id] = j
	m.activeID = id
	if m.registry != nil {
		m.registry.Track(id, "story", func() { _, _ = m.Cancel(id) })
	}
	m.mu.Unlock()

	go m.resumeRun(jobCtx, j, manifest)
	return CreateResponse{
		ID:        id,
		Status:    StatusQueued,
		StatusURL: "/v1/stories/" + id,
	}, nil
}

// resumeRun is run() without the writing half: the script already exists in
// the work-in-progress manifest, so production picks up from synthesis.
func (m *Manager) resumeRun(ctx context.Context, j *job, manifest Manifest) {
	if j.release != nil {
		defer j.release()
	}
	manifest.Status = StatusSynthesizing
	if err := m.store.SaveManifestWIP(manifest); err != nil {
		m.fail(j, NewError(CodeStoreFailure, err.Error()))
		return
	}
	m.produce(ctx, j, manifest, m.now())
}

// Discard abandons an interrupted production, takes and all. The one being
// produced right now cannot be discarded — cancel it instead.
func (m *Manager) Discard(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeID == id {
		return NewError(CodeCannotCancel, "story is being produced; cancel it instead")
	}
	_, ok, err := m.store.LoadWIP(id)
	if err != nil {
		return NewError(CodeStoreFailure, err.Error())
	}
	if !ok {
		return NewError(CodeNotFound, "no interrupted production with this id")
	}
	if err := m.store.DiscardWIP(id); err != nil {
		return NewError(CodeStoreFailure, err.Error())
	}
	return nil
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

	// Production runs in a work-in-progress directory: every take is on
	// disk the moment it is synthesized, so a failure part-way loses the
	// remainder of the session, not the recordings already made. The story
	// itself appears only at FinalizeWIP, complete with its take room.
	if err := m.store.BeginWIP(j.id); err != nil {
		m.fail(j, NewError(CodeStoreFailure, err.Error()))
		return
	}
	// The manifest rides in the directory from the start — status honest —
	// so an interrupted run is a story a resume can read, not a pile of
	// anonymous WAVs.
	manifest.Status = StatusSynthesizing
	if err := m.store.SaveManifestWIP(manifest); err != nil {
		m.fail(j, NewError(CodeStoreFailure, err.Error()))
		return
	}
	m.produce(ctx, j, manifest, createdAt)
}

// produce is the production half of a story job — synthesis onward — shared
// by a fresh run and a resume. Everything it needs rides on the manifest —
// a resumed job has no original request to consult. The manifest it is
// handed is already in the work-in-progress directory; lines that carry a
// valid take are kept, the rest are synthesized.
func (m *Manager) produce(ctx context.Context, j *job, manifest Manifest, createdAt time.Time) {
	// An explicit cancel wants no residue; a failure deliberately leaves
	// the directory behind — its takes are what a future resume picks up.
	discard := func() { _ = m.store.DiscardWIP(j.id) }

	audio := fixtureWAV(manifest.DurationSeconds)
	if manifest.VoiceMode == "fixed" && m.synthesize != nil {
		if !m.synthesizeTakes(ctx, j, &manifest, createdAt) {
			if m.isCancelled(j) {
				discard()
			}
			return
		}
		if !m.advance(ctx, j, StatusStitching, 0.9) {
			discard()
			return
		}
		stitched, err := m.stitchWIPTakes(j.id, manifest.Script)
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
			discard()
			return
		}
		if !m.advance(ctx, j, StatusStitching, 0.9) {
			discard()
			return
		}
	}
	if ctx.Err() != nil || m.isCancelled(j) {
		m.cancelled(j)
		discard()
		return
	}
	// Archive this stitch as revision 1 beside the takes, then publish the
	// whole story atomically. A story that only kept its mix would have to
	// be regenerated whole to fix one bad read; with takes on disk, one
	// line can be replaced.
	if err := m.store.SaveRenderWIP(j.id, 1, audio); err != nil {
		m.fail(j, NewError(CodeStoreFailure, err.Error()))
		return
	}
	manifest.Renders = []Render{{
		Revision:        1,
		CreatedAt:       createdAt.UTC(),
		DurationSeconds: manifest.DurationSeconds,
		Bytes:           len(audio),
		URL:             RenderURL(j.id, 1),
	}}
	manifest.Status = StatusComplete
	if err := m.store.FinalizeWIP(manifest, audio); err != nil {
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
		Scenes:      normalized.Scenes,
		Script:      script,
	}, nil
}

// synthesizeTakes speaks each script line through the injected synthesizer
// with the speaker's assigned voice, walking progress across the
// synthesizing stage. Every take is written to the work-in-progress
// directory the moment it is made — take file first, then the manifest
// naming it — so nothing accumulates in memory and an interruption keeps
// everything already performed. Lines whose existing take was made by this
// same synthesis configuration for these same words are kept, which is what
// lets a resume skip straight to the first line that still needs work.
// Lines are synthesized grouped by voice — resident TTS sessions keep a
// single-slot voice-prompt cache, so grouping re-conditions once per voice
// instead of once per speaker change. It reports false when the job was
// cancelled or failed (status already updated).
func (m *Manager) synthesizeTakes(ctx context.Context, j *job, manifest *Manifest, createdAt time.Time) bool {
	script := manifest.Script
	byVoice := make(map[string][]int)
	var voiceOrder []string
	for i, line := range script {
		voiceID := castVoiceFor(manifest.Cast, line.SpeakerID)
		if _, seen := byVoice[voiceID]; !seen {
			voiceOrder = append(voiceOrder, voiceID)
		}
		byVoice[voiceID] = append(byVoice[voiceID], i)
	}

	done := 0
	for _, voiceID := range voiceOrder {
		for _, idx := range byVoice[voiceID] {
			if ctx.Err() != nil || m.isCancelled(j) {
				m.cancelled(j)
				return false
			}
			m.setStage(j, StatusSynthesizing, 0.55+0.3*float64(done)/float64(len(script)))
			if m.takeStillValid(manifest.ID, script[idx], voiceID) {
				done++
				continue
			}
			clip, err := m.synthesize(ctx, script[idx].Text, voiceID)
			if err != nil {
				m.fail(j, NewError(CodeSynthesisFailure, fmt.Sprintf("synthesize script line %d: %v", idx+1, err)))
				return false
			}
			take, err := m.storeTakeWIP(manifest.ID, script[idx].ID, "take-001", clip, voiceID, script[idx].Text, createdAt)
			if err != nil {
				m.fail(j, NewError(CodeStoreFailure, err.Error()))
				return false
			}
			script[idx].Takes = []Take{take}
			script[idx].CurrentTake = take.ID
			if err := m.store.SaveManifestWIP(*manifest); err != nil {
				m.fail(j, NewError(CodeStoreFailure, err.Error()))
				return false
			}
			done++
		}
	}
	return true
}

// takeStillValid reports whether a line's current take can be kept by this
// run: same words, same voice, the same synthesis fingerprint, and audio
// actually on disk. Line id + text alone is not a sufficient key — an
// engine or model change between runs would splice an inconsistent episode
// together silently, which is exactly what the fingerprint exists to
// refuse. An empty fingerprint on either side never matches, so an
// unfingerprinted configuration always re-synthesizes rather than splices.
func (m *Manager) takeStillValid(storyID string, line ScriptLine, voiceID string) bool {
	if line.CurrentTake == "" {
		return false
	}
	take := takeByID(line.Takes, line.CurrentTake)
	if take == nil {
		return false
	}
	if take.Text != line.Text || take.VoiceID != voiceID {
		return false
	}
	if take.Fingerprint == "" || take.Fingerprint != m.fingerprint {
		return false
	}
	// The manifest is written after the take, so this should always hold;
	// checking anyway turns a tampered or half-lost directory into one
	// re-synthesized line instead of a stitch failure a resume cannot get
	// past.
	return m.store.HasTakeWIP(storyID, line.ID, line.CurrentTake)
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
