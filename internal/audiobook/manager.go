package audiobook

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"cpp-studio/internal/engine"
	"cpp-studio/internal/jobs"
	"cpp-studio/internal/wav"
)

const DefaultRootDir = "out/audiobooks"

const (
	// DefaultEngineID preserves the original audiobook narrator when a
	// caller does not opt into an expressive backend.
	DefaultEngineID = engine.DefaultSpeechEngineID
	// DramaBoxEngineID is the only expressive engine accepted by the
	// audiobook contract in this release.
	DramaBoxEngineID = engine.DramaBoxSpeechEngineID
	// MaxDirectionRunes bounds user-authored delivery direction before it is
	// repeated for every narration chunk.
	MaxDirectionRunes = 500
	// DefaultDramaBoxDirection keeps factual narration restrained when the
	// user selects DramaBox without writing custom direction.
	DefaultDramaBoxDirection = "Warm, measured documentary narration with clear diction, restrained emotion, and thoughtful pauses."
)

// ArtifactName is the stitched narration file inside an audiobook's dir.
const ArtifactName = "book.wav"

var (
	ErrProductionNotInterrupted = errors.New("audiobook production is not interrupted")
	ErrSynthesisIdentityChanged = errors.New("audiobook synthesis identity changed")
	ErrProductionActive         = errors.New("audiobook production is active")
)

// chunkGap is the silence between narrated chunks; paragraph pacing comes
// from chunk boundaries, so the gap stays short.
const chunkGap = 400 * time.Millisecond

const sectionCrossfade = 50 * time.Millisecond

// artifactPad is the lead/trail silence around the finished narration.
const artifactPad = 300 * time.Millisecond

// ReserveEngineFunc reserves the audio engine for the whole narration run.
type ReserveEngineFunc func(ctx context.Context, name string) (func(), bool)

// SynthesizeFunc speaks one typed section request. VoiceID "" means the
// engine's default or, for DramaBox, its supported text-only mode.
type SynthesizeFunc func(ctx context.Context, request SynthesisRequest) ([]byte, error)

type ManagerOptions struct {
	RootDir       string
	ReserveEngine ReserveEngineFunc
	Synthesize    SynthesizeFunc
	ResolveEngine ResolveEngineFunc
	ResolveVoice  ResolveVoiceFunc
	Verify        VerifyFunc
	// SeedSource supplies full-range DramaBox section seeds. Nil uses
	// crypto/rand.Reader; tests may inject deterministic or failing readers.
	SeedSource io.Reader
	Jobs       *jobs.Registry
	Now        func() time.Time
}

// Request carries narration intent. NormalizeRequest and Submit validate and
// normalize its engine and direction before any job starts.
type Request struct {
	Title     string
	Text      string
	VoiceID   string
	EngineID  string
	Direction string
	// OptionsJSON is the curated/advanced option object supplied by HTTP.
	// It never accepts a seed; NormalizeRequest resolves it into Options.
	OptionsJSON  string
	Options      SynthesisOptions
	Verification VerificationMode
}

type narrationUnit struct {
	text    string
	seed    Seed
	section *Section
	audio   string
}

type Manager struct {
	mu            sync.Mutex
	rootDir       string
	store         *Store
	reserveEngine ReserveEngineFunc
	synthesize    SynthesizeFunc
	resolveEngine ResolveEngineFunc
	resolveVoice  ResolveVoiceFunc
	verify        VerifyFunc
	seedSource    io.Reader
	registry      *jobs.Registry
	now           func() time.Time
	counter       int
	activeID      string
	cancels       map[string]context.CancelFunc
}

func NewManager(opts ManagerOptions) *Manager {
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	rootDir := opts.RootDir
	if rootDir == "" {
		rootDir = DefaultRootDir
	}
	seedSource := opts.SeedSource
	if seedSource == nil {
		seedSource = rand.Reader
	}
	resolveEngine := opts.ResolveEngine
	if resolveEngine == nil {
		resolveEngine = func(_ context.Context, engineID string) (EngineIdentity, error) {
			return EngineIdentity{ID: engineID, Mode: "subprocess", ModelID: engineID, Fingerprint: engineID}, nil
		}
	}
	resolveVoice := opts.ResolveVoice
	if resolveVoice == nil {
		resolveVoice = func(_ context.Context, voiceID string) (VoiceIdentity, error) {
			if voiceID == "" {
				voiceID = "default"
			}
			return VoiceIdentity{ID: voiceID, Fingerprint: voiceID}, nil
		}
	}
	manager := &Manager{
		rootDir:       rootDir,
		store:         NewStore(rootDir),
		reserveEngine: opts.ReserveEngine,
		synthesize:    opts.Synthesize,
		resolveEngine: resolveEngine,
		resolveVoice:  resolveVoice,
		verify:        opts.Verify,
		seedSource:    seedSource,
		registry:      opts.Jobs,
		now:           now,
		cancels:       make(map[string]context.CancelFunc),
	}
	_ = manager.store.RecoverInterrupted()
	return manager
}

// Preview resolves exactly the engine, voice, direction, and effective options
// Submit will use, without planning seeds, reserving an engine, or creating work.
func (m *Manager) Preview(ctx context.Context, req Request) (ResolvedRequest, error) {
	normalized, err := NormalizeRequest(req)
	if err != nil {
		return ResolvedRequest{}, err
	}
	return m.resolveRequest(ctx, normalized)
}

func (m *Manager) resolveRequest(ctx context.Context, req Request) (ResolvedRequest, error) {
	resolvedEngine, err := m.resolveEngine(ctx, req.EngineID)
	if err != nil {
		return ResolvedRequest{}, fmt.Errorf("resolve audiobook engine %q: %w", req.EngineID, err)
	}
	resolvedVoice, err := m.resolveVoice(ctx, req.VoiceID)
	if err != nil {
		return ResolvedRequest{}, requestErrorf("resolve audiobook voice %q: %v", req.VoiceID, err)
	}
	return ResolvedRequest{Request: req, Engine: resolvedEngine, Voice: resolvedVoice}, nil
}

// RequestError identifies invalid audiobook intent. HTTP callers map these
// errors to 400, while active-job and reservation conflicts remain 409.
type RequestError struct {
	message string
}

func (e *RequestError) Error() string { return e.message }

func requestErrorf(format string, args ...any) error {
	return &RequestError{message: fmt.Sprintf(format, args...)}
}

// IsRequestError reports whether err is safe to classify as invalid input.
func IsRequestError(err error) bool {
	var requestErr *RequestError
	return errors.As(err, &requestErr)
}

// NormalizeRequest applies the public engine/direction policy without
// touching the document. It is shared by the HTTP boundary and Manager so
// direct callers receive the same validation.
func NormalizeRequest(req Request) (Request, error) {
	req.EngineID = strings.ToLower(strings.TrimSpace(req.EngineID))
	if req.EngineID == "" {
		req.EngineID = DefaultEngineID
	}
	if req.EngineID != DefaultEngineID && req.EngineID != DramaBoxEngineID {
		return Request{}, requestErrorf("audiobook engine must be audio or dramabox, got %q", req.EngineID)
	}
	req.Direction = strings.TrimSpace(req.Direction)
	if utf8.RuneCountInString(req.Direction) > MaxDirectionRunes {
		return Request{}, requestErrorf("audiobook direction must be at most %d characters", MaxDirectionRunes)
	}
	if req.EngineID == DefaultEngineID && req.Direction != "" {
		return Request{}, requestErrorf("audiobook direction is only supported with dramabox")
	}
	if req.EngineID == DramaBoxEngineID && req.Direction == "" {
		req.Direction = DefaultDramaBoxDirection
	}
	if req.Options.Seed != 0 {
		return Request{}, requestErrorf("audiobook section seeds are assigned by the server")
	}
	if strings.TrimSpace(req.OptionsJSON) != "" && req.Options != (SynthesisOptions{}) {
		return Request{}, requestErrorf("provide synthesis options as either typed values or JSON, not both")
	}
	var err error
	if strings.TrimSpace(req.OptionsJSON) != "" || req.Options == (SynthesisOptions{}) {
		req.Options, err = engine.ResolveSynthesisOptions(req.EngineID, req.OptionsJSON)
	} else {
		err = engine.ValidateSynthesisOptions(req.EngineID, req.Options)
	}
	if err != nil {
		return Request{}, requestErrorf("invalid synthesis options: %v", err)
	}
	req.OptionsJSON = ""
	if req.Verification == "" {
		req.Verification = VerificationModeAuto
	}
	if req.Verification != VerificationModeAuto && req.Verification != VerificationModeRequired && req.Verification != VerificationModeOff {
		return Request{}, requestErrorf("audiobook verification must be auto, required, or off")
	}
	if req.EngineID == DefaultEngineID {
		req.Verification = VerificationModeOff
	}
	return req, nil
}

// BuildDramaBoxPrompt places delivery direction outside a single quoted
// source passage. Double-quote punctuation is changed to apostrophes so
// neither source nor direction can close that passage early; all source
// words remain unchanged.
func BuildDramaBoxPrompt(direction, chunk string) string {
	direction = strings.Join(strings.Fields(normalizePromptQuotes(direction)), " ")
	chunk = normalizePromptQuotes(chunk)
	return direction + ` "` + chunk + `"`
}

func normalizePromptQuotes(value string) string {
	return strings.NewReplacer(`"`, `'`, "“", `'`, "”", `'`).Replace(value)
}

// Submit validates, chunks, and starts a narration job. One audiobook runs
// at a time: narration monopolizes the audio engine for minutes.
func (m *Manager) Submit(ctx context.Context, req Request) (string, int, error) {
	if m.synthesize == nil {
		return "", 0, requestErrorf("audiobooks need a configured speech engine")
	}
	var err error
	req, err = NormalizeRequest(req)
	if err != nil {
		return "", 0, err
	}
	if req.EngineID == DramaBoxEngineID && req.Verification == VerificationModeRequired && m.verify == nil {
		return "", 0, requestErrorf("required audiobook verification needs a configured Whisper engine")
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "Untitled audiobook"
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeID != "" {
		return "", 0, fmt.Errorf("another audiobook is already narrating")
	}
	resolved, err := m.resolveRequest(ctx, req)
	if err != nil {
		return "", 0, err
	}
	identity := buildSynthesisIdentity(req, resolved.Engine, resolved.Voice)

	var units []narrationUnit
	var initial *Manifest
	id, err := m.nextID()
	if err != nil {
		return "", 0, err
	}
	if req.EngineID == DramaBoxEngineID {
		sections, err := planDramaBoxSections(req.Text, m.seedSource)
		if err != nil {
			return "", 0, fmt.Errorf("plan DramaBox sections: %w", err)
		}
		sections = prepareSectionCheckpoints(identity, sections)
		createdAt := m.now()
		options := req.Options
		manifest := Manifest{
			SchemaVersion: CurrentManifestSchemaVersion, ID: id, Title: title,
			VoiceID: req.VoiceID, EngineID: req.EngineID, Direction: req.Direction,
			Chunks: len(sections), CreatedAt: createdAt,
			ArtifactURL: "/v1/audiobooks/" + id + "/artifact/" + ArtifactName,
			Status:      ProductionStatusSynthesizing, SourceFile: sourceFileName,
			SourceSHA256: identity.SourceSHA256, SynthesisFingerprint: identity.Fingerprint,
			SectionPolicyVersion: identity.SectionPolicyVersion, PromptPolicyVersion: identity.PromptPolicyVersion,
			Sections: sections, ResolvedOptions: &options, SynthesisIdentity: &identity,
			Verification: &VerificationSummary{Mode: req.Verification, Status: VerificationStatusPending, ReportFile: verificationFileName},
		}
		initial = &manifest
		units = make([]narrationUnit, len(sections))
		for i := range sections {
			units[i] = narrationUnit{text: req.Text[sections[i].StartByte:sections[i].EndByte], seed: sections[i].Seed, section: &manifest.Sections[i]}
		}
	} else {
		chunks := Chunk(req.Text, DefaultChunkChars)
		units = make([]narrationUnit, len(chunks))
		for i, chunk := range chunks {
			units[i] = narrationUnit{text: chunk}
		}
	}
	if len(units) == 0 {
		return "", 0, requestErrorf("document contains no narratable text")
	}
	if len(units) > MaxChunks {
		return "", 0, requestErrorf("document needs %d chunks, max is %d; narrate it in parts", len(units), MaxChunks)
	}

	var staged *stagedProduction
	if initial != nil {
		staged, err = m.store.StageInitial(*initial, req.Text)
		if err != nil {
			return "", 0, fmt.Errorf("stage audiobook: %w", err)
		}
	}
	var release func()
	if m.reserveEngine != nil {
		var ok bool
		release, ok = m.reserveEngine(ctx, req.EngineID)
		if !ok {
			m.store.AbortInitial(staged)
			return "", 0, fmt.Errorf("engine %q is busy", req.EngineID)
		}
	}
	if staged != nil {
		if err := m.store.PublishInitial(staged); err != nil {
			if release != nil {
				release()
			}
			m.store.AbortInitial(staged)
			return "", 0, err
		}
	}
	jobCtx, cancel := context.WithCancel(context.Background())
	m.activeID = id
	m.cancels[id] = cancel
	if m.registry != nil {
		m.registry.Track(id, "audiobook", cancel)
	}

	go m.run(jobCtx, id, title, req, identity, resolved.Voice, units, initial, release)
	return id, len(units), nil
}

func (m *Manager) nextID() (string, error) {
	timestamp := m.now().Format("20060102_150405")
	for {
		m.counter++
		id := fmt.Sprintf("book_%s_%03d", timestamp, m.counter)
		exists, err := m.store.IDExists(id)
		if err != nil {
			return "", fmt.Errorf("check audiobook id: %w", err)
		}
		if !exists {
			return id, nil
		}
	}
}

// Resume continues one durable interrupted DramaBox production under exactly
// its frozen engine, voice, option, prompt, and section identities.
func (m *Manager) Resume(ctx context.Context, id string) (int, error) {
	if m.synthesize == nil {
		return 0, requestErrorf("audiobooks need a configured speech engine")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeID != "" {
		return 0, fmt.Errorf("another audiobook is already narrating")
	}
	manifest, source, err := m.store.LoadDurableWIP(id)
	if err != nil {
		return 0, err
	}
	if manifest.Status != ProductionStatusInterrupted {
		return 0, ErrProductionNotInterrupted
	}
	if manifest.EngineID != DramaBoxEngineID || manifest.ResolvedOptions == nil {
		return 0, fmt.Errorf("only durable DramaBox productions can resume")
	}
	req, err := NormalizeRequest(Request{
		Title: manifest.Title, Text: source, VoiceID: manifest.VoiceID,
		EngineID: manifest.EngineID, Direction: manifest.Direction, Options: *manifest.ResolvedOptions,
		Verification: manifestVerificationMode(manifest),
	})
	if err != nil {
		return 0, fmt.Errorf("stored audiobook request: %w", err)
	}
	if req.Verification == VerificationModeRequired && m.verify == nil {
		return 0, requestErrorf("required audiobook verification needs a configured Whisper engine")
	}
	resolved, err := m.resolveRequest(ctx, req)
	if err != nil {
		return 0, err
	}
	identity := buildSynthesisIdentity(req, resolved.Engine, resolved.Voice)
	if identity.Fingerprint != manifest.SynthesisFingerprint {
		return 0, fmt.Errorf("%w: Restart creates a new production under the current engine and voice", ErrSynthesisIdentityChanged)
	}
	units := make([]narrationUnit, len(manifest.Sections))
	for i := range manifest.Sections {
		section := &manifest.Sections[i]
		trustedPath, trusted, err := m.store.TrustedSectionWIP(manifest, *section)
		if err != nil {
			return 0, err
		}
		if !trusted {
			section.Status = SectionStatusPending
			section.AudioSHA256 = ""
			section.DurationMS = 0
			section.TranscriptFile = ""
			section.Attempts = nil
		}
		units[i] = narrationUnit{
			text: source[section.StartByte:section.EndByte], seed: section.Seed,
			section: section, audio: trustedPath,
		}
	}
	var release func()
	if m.reserveEngine != nil {
		var ok bool
		release, ok = m.reserveEngine(ctx, req.EngineID)
		if !ok {
			return 0, fmt.Errorf("engine %q is busy", req.EngineID)
		}
	}
	manifest.Status = ProductionStatusSynthesizing
	if err := m.store.SaveManifestWIP(manifest); err != nil {
		if release != nil {
			release()
		}
		return 0, err
	}
	jobCtx, cancel := context.WithCancel(context.Background())
	m.activeID = id
	m.cancels[id] = cancel
	if m.registry != nil {
		m.registry.Track(id, "audiobook", cancel)
	}
	go m.run(jobCtx, id, manifest.Title, req, identity, resolved.Voice, units, &manifest, release)
	return len(units), nil
}

// Restart creates a separate production from a hash-valid interrupted source.
// The original WIP remains untouched and current resolvers define the new identity.
func (m *Manager) Restart(ctx context.Context, id string) (string, int, error) {
	manifest, source, err := m.store.LoadDurableWIP(id)
	if err != nil {
		return "", 0, err
	}
	if manifest.Status != ProductionStatusInterrupted {
		return "", 0, ErrProductionNotInterrupted
	}
	if manifest.ResolvedOptions == nil {
		return "", 0, fmt.Errorf("stored audiobook options are missing")
	}
	return m.Submit(ctx, Request{
		Title: manifest.Title, Text: source, VoiceID: manifest.VoiceID,
		EngineID: manifest.EngineID, Direction: manifest.Direction, Options: *manifest.ResolvedOptions,
		Verification: manifestVerificationMode(manifest),
	})
}

func manifestVerificationMode(manifest Manifest) VerificationMode {
	if manifest.Verification == nil || manifest.Verification.Mode == "" {
		return VerificationModeAuto
	}
	return manifest.Verification.Mode
}

// Discard explicitly removes an inactive interrupted WIP and its evidence.
func (m *Manager) Discard(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeID == id {
		return ErrProductionActive
	}
	manifest, _, err := m.store.LoadDurableWIP(id)
	if err != nil {
		return err
	}
	if manifest.Status != ProductionStatusInterrupted {
		return ErrProductionNotInterrupted
	}
	return m.store.DiscardWIP(id)
}

func (m *Manager) run(ctx context.Context, id, title string, req Request, identity SynthesisIdentity, resolvedVoice VoiceIdentity, units []narrationUnit, initial *Manifest, release func()) {
	defer func() {
		if release != nil {
			release()
		}
		m.mu.Lock()
		if m.activeID == id {
			m.activeID = ""
		}
		delete(m.cancels, id)
		m.mu.Unlock()
	}()

	if err := os.MkdirAll(m.rootDir, 0o755); err != nil {
		m.finishRunError(id, initial, "create audiobook staging dir: "+err.Error(), false)
		return
	}
	clipDir := ""
	if initial == nil {
		var err error
		clipDir, err = os.MkdirTemp(m.rootDir, "."+id+".clips-")
		if err != nil {
			m.finishRunError(id, initial, "create audiobook staging dir: "+err.Error(), false)
			return
		}
		defer os.RemoveAll(clipDir)
	}
	clipPaths := make([]string, 0, len(units))
	for i, unit := range units {
		if ctx.Err() != nil {
			m.finishRunError(id, initial, "audiobook narration was cancelled", true)
			return
		}
		if m.registry != nil {
			detail := fmt.Sprintf("narrating chunk %d/%d", i+1, len(units))
			if req.EngineID != DefaultEngineID {
				detail += " with " + req.EngineID
			}
			m.registry.Update(id, float64(i)/float64(len(units)), detail)
		}
		if unit.audio != "" {
			clipPaths = append(clipPaths, unit.audio)
			continue
		}
		text := unit.text
		if req.EngineID == DramaBoxEngineID {
			text = BuildDramaBoxPrompt(req.Direction, unit.text)
		}
		options := req.Options
		options.Seed = unit.seed
		clip, err := m.synthesize(ctx, SynthesisRequest{
			Text:     text,
			VoiceID:  req.VoiceID,
			EngineID: req.EngineID,
			Options:  options,
			Voice:    resolvedVoice.Reference,
		})
		if err != nil {
			if ctx.Err() != nil {
				m.finishRunError(id, initial, "audiobook narration was cancelled", true)
				return
			}
			m.finishRunError(id, initial, fmt.Sprintf("narrate chunk %d/%d with %s: %v", i+1, len(units), req.EngineID, err), false)
			return
		}
		clipPath := filepath.Join(clipDir, fmt.Sprintf("chunk-%04d.wav", i+1))
		if unit.section != nil {
			clipPath, err = m.store.SaveSectionWIP(id, unit.section.ID, clip)
		} else {
			err = os.WriteFile(clipPath, clip, 0o600)
		}
		if err != nil {
			m.finishRunError(id, initial, fmt.Sprintf("stage chunk %d/%d with %s: %v", i+1, len(units), req.EngineID, err), false)
			return
		}
		if unit.section != nil {
			audioSum := sha256.Sum256(clip)
			unit.section.Status = SectionStatusSynthesized
			unit.section.AudioSHA256 = hex.EncodeToString(audioSum[:])
			if duration, durationErr := wav.Duration(clip); durationErr == nil {
				unit.section.DurationMS = duration.Milliseconds()
			}
			attemptOptions := options
			unit.section.Attempts = []Attempt{{
				ID: "attempt-0001", Seed: unit.seed,
				CheckpointFingerprint: unit.section.CheckpointFingerprint,
				AudioFile:             unit.section.AudioFile, AudioSHA256: unit.section.AudioSHA256,
				Selected: true, CreatedAt: m.now(), Options: &attemptOptions,
			}}
			if err := m.store.SaveManifestWIP(*initial); err != nil {
				m.finishRunError(id, initial, "checkpoint audiobook section: "+err.Error(), false)
				return
			}
		}
		clipPaths = append(clipPaths, clipPath)
	}
	if initial != nil && !m.verifySections(ctx, id, req, initial, units, clipPaths) {
		return
	}

	if m.registry != nil {
		m.registry.Update(id, 0.97, "stitching")
	}
	if initial != nil {
		initial.Status = ProductionStatusStitching
		if err := m.store.SaveManifestWIP(*initial); err != nil {
			m.finishRunError(id, initial, "checkpoint audiobook stitching: "+err.Error(), false)
			return
		}
		bookPath := m.store.bookWIPPath(id)
		if err := wav.AssembleFiles(bookPath, clipPaths, sectionCrossfade, artifactPad, artifactPad); err != nil {
			m.finishRunError(id, initial, "assemble narration: "+err.Error(), false)
			return
		}
		manifest := *initial
		manifest.Status = ProductionStatusComplete
		if duration, err := wav.DurationFile(bookPath); err == nil {
			manifest.DurationSeconds = int(duration.Round(time.Second) / time.Second)
		}
		if ctx.Err() != nil {
			m.finishRunError(id, initial, "audiobook narration was cancelled", true)
			return
		}
		if err := m.store.FinalizeWIPFile(manifest); err != nil {
			m.finishRunError(id, initial, "save audiobook: "+err.Error(), false)
			return
		}
		if m.registry != nil {
			m.registry.Complete(id, map[string]string{"artifactUrl": manifest.ArtifactURL, "title": manifest.Title, "engine": manifest.EngineID})
		}
		return
	}
	gaps := make([]time.Duration, len(clipPaths))
	for i := range gaps {
		gaps[i] = chunkGap
	}
	stitched, err := wav.ConcatenateGapsFrom(len(clipPaths), gaps, func(i int) ([]byte, error) {
		return os.ReadFile(clipPaths[i])
	})
	if err != nil {
		m.finishRunError(id, initial, "stitch narration: "+err.Error(), false)
		return
	}
	manifest := Manifest{
		ID:                   id,
		Title:                title,
		VoiceID:              req.VoiceID,
		EngineID:             req.EngineID,
		Direction:            req.Direction,
		Chunks:               len(units),
		CreatedAt:            m.now(),
		SynthesisFingerprint: identity.Fingerprint,
		SourceSHA256:         identity.SourceSHA256,
		SectionPolicyVersion: identity.SectionPolicyVersion,
		PromptPolicyVersion:  identity.PromptPolicyVersion,
		SynthesisIdentity:    &identity,
	}
	if req.EngineID == DramaBoxEngineID {
		options := req.Options
		manifest.ResolvedOptions = &options
	}
	if duration, err := wav.Duration(stitched); err == nil {
		manifest.DurationSeconds = int(duration.Round(time.Second) / time.Second)
	}
	if padded, err := wav.PadSilence(stitched, artifactPad, artifactPad); err == nil {
		stitched = padded
	}
	manifest.ArtifactURL = "/v1/audiobooks/" + id + "/artifact/" + ArtifactName

	if ctx.Err() != nil {
		m.finishRunError(id, initial, "audiobook narration was cancelled", true)
		return
	}
	err = m.save(manifest, stitched)
	if err != nil {
		m.finishRunError(id, initial, "save audiobook: "+err.Error(), false)
		return
	}
	if m.registry != nil {
		m.registry.Complete(id, map[string]string{"artifactUrl": manifest.ArtifactURL, "title": manifest.Title, "engine": manifest.EngineID})
	}
}

func (m *Manager) verifySections(ctx context.Context, id string, req Request, manifest *Manifest, units []narrationUnit, clipPaths []string) bool {
	aggregate := FidelityAggregate{Mode: req.Verification, Status: VerificationStatusPending}
	finish := func(status VerificationStatus, message string) bool {
		aggregate.Status = status
		aggregate.Error = message
		if err := m.store.SaveFidelityAggregateWIP(id, aggregate); err != nil {
			m.finishRunError(id, manifest, "save audiobook verification: "+err.Error(), false)
			return false
		}
		manifest.Verification = &VerificationSummary{
			Mode: aggregate.Mode, Status: aggregate.Status,
			VerifiedSections: aggregate.VerifiedSections, FlaggedSections: aggregate.FlaggedSections,
			ReportFile: verificationFileName, Error: aggregate.Error,
		}
		if err := m.store.SaveManifestWIP(*manifest); err != nil {
			m.finishRunError(id, manifest, "checkpoint audiobook verification: "+err.Error(), false)
			return false
		}
		return true
	}
	if req.Verification == VerificationModeOff {
		return finish(VerificationStatusSkipped, "")
	}
	if m.verify == nil {
		return finish(VerificationStatusUnavailable, "Whisper verification is not configured")
	}
	manifest.Status = ProductionStatusVerifying
	if err := m.store.SaveManifestWIP(*manifest); err != nil {
		m.finishRunError(id, manifest, "checkpoint audiobook verification: "+err.Error(), false)
		return false
	}
	for i := range units {
		if ctx.Err() != nil {
			m.finishRunError(id, manifest, "audiobook verification was cancelled", true)
			return false
		}
		if m.registry != nil {
			m.registry.Update(id, 0.9+0.06*float64(i)/float64(len(units)), fmt.Sprintf("verifying section %d/%d", i+1, len(units)))
		}
		audio, err := os.ReadFile(clipPaths[i])
		if err != nil {
			m.finishRunError(id, manifest, fmt.Sprintf("read section %d for verification: %v", i+1, err), false)
			return false
		}
		verification, err := m.verify(ctx, units[i].text, audio)
		if err != nil {
			if ctx.Err() != nil {
				m.finishRunError(id, manifest, "audiobook verification was cancelled", true)
				return false
			}
			message := fmt.Sprintf("verify section %d/%d: %v", i+1, len(units), err)
			if req.Verification == VerificationModeRequired {
				if !finish(VerificationStatusUnavailable, message) {
					return false
				}
				m.finishRunError(id, manifest, message+"; Resume after repairing Whisper", false)
				return false
			}
			return finish(VerificationStatusUnavailable, message)
		}
		if verification.VerifierIdentity == "" {
			verification.VerifierIdentity = "whisper"
		}
		report := evaluateFidelity(*units[i].section, units[i].text, verification, m.now())
		transcriptFile, reportFile, err := m.store.SaveVerificationWIP(id, units[i].section.ID, verification, report)
		if err != nil {
			m.finishRunError(id, manifest, "save section verification: "+err.Error(), false)
			return false
		}
		section := units[i].section
		section.TranscriptFile = transcriptFile
		section.VerificationFile = reportFile
		section.Status = report.Status
		for attemptIndex := range section.Attempts {
			if section.Attempts[attemptIndex].Selected {
				section.Attempts[attemptIndex].TranscriptFile = transcriptFile
				section.Attempts[attemptIndex].VerificationFile = reportFile
			}
		}
		aggregate.Sections = append(aggregate.Sections, report)
		aggregate.VerifiedSections++
		if report.Status == SectionStatusFlagged {
			aggregate.FlaggedSections++
		}
		if err := m.store.SaveManifestWIP(*manifest); err != nil {
			m.finishRunError(id, manifest, "checkpoint section verification: "+err.Error(), false)
			return false
		}
	}
	status := VerificationStatusPassed
	if aggregate.FlaggedSections > 0 {
		status = VerificationStatusFlagged
	}
	return finish(status, "")
}

func (m *Manager) finishRunError(id string, manifest *Manifest, message string, cancelled bool) {
	if manifest != nil {
		manifest.Status = ProductionStatusInterrupted
		if err := m.store.SaveManifestWIP(*manifest); err != nil {
			message += "; persist interrupted state: " + err.Error()
		}
	}
	if m.registry == nil {
		return
	}
	if cancelled {
		m.registry.MarkCancelled(id)
		return
	}
	m.registry.Fail(id, message)
}

func (m *Manager) save(manifest Manifest, audio []byte) error {
	if err := wav.ValidateBytes(audio); err != nil {
		return fmt.Errorf("stitched narration: %w", err)
	}
	if err := os.MkdirAll(m.rootDir, 0o755); err != nil {
		return fmt.Errorf("create audiobooks dir: %w", err)
	}
	tmpDir := filepath.Join(m.rootDir, "."+manifest.ID+".tmp")
	_ = os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Errorf("create temp audiobook dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	if err := os.WriteFile(filepath.Join(tmpDir, ArtifactName), audio, 0o644); err != nil {
		return fmt.Errorf("write narration wav: %w", err)
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "manifest.json"), append(encoded, '\n'), 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return os.Rename(tmpDir, filepath.Join(m.rootDir, manifest.ID))
}

// List returns finished and recoverable interrupted audiobooks, newest first.
func (m *Manager) List() ([]Manifest, error) {
	entries, err := os.ReadDir(m.rootDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read audiobooks dir: %w", err)
	}
	out := make([]Manifest, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(m.rootDir, entry.Name(), "manifest.json"))
		if err != nil {
			continue
		}
		var manifest Manifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			continue
		}
		out = append(out, manifest)
	}
	m.mu.Lock()
	activeID := m.activeID
	m.mu.Unlock()
	wips, err := m.store.ListWIP()
	if err != nil {
		return nil, err
	}
	for _, candidate := range wips {
		if candidate.ID == activeID {
			continue
		}
		manifest, _, err := m.store.LoadDurableWIP(candidate.ID)
		if err == nil && manifest.Status == ProductionStatusInterrupted {
			out = append(out, manifest)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// Status loads either a finished production or a hash-valid durable WIP.
func (m *Manager) Status(id string) (Manifest, bool, error) {
	if err := validateBookID(id); err != nil {
		return Manifest{}, false, nil
	}
	if manifest, ok, err := loadManifest(filepath.Join(m.rootDir, id, manifestFileName)); err != nil || ok {
		return manifest, ok, err
	}
	manifest, _, err := m.store.LoadDurableWIP(id)
	if errors.Is(err, ErrProductionNotFound) {
		return Manifest{}, false, nil
	}
	return manifest, err == nil, err
}

// ArtifactPath resolves an audiobook's WAV for serving.
func (m *Manager) ArtifactPath(id, filename string) (string, error) {
	if err := validateBookID(id); err != nil {
		return "", fmt.Errorf("audiobook not found")
	}
	if filename != ArtifactName {
		return "", fmt.Errorf("unsupported audiobook artifact")
	}
	path := filepath.Join(m.rootDir, id, filename)
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("audiobook artifact not found")
	}
	defer file.Close()
	if err := wav.ValidateHeader(file); err != nil {
		return "", fmt.Errorf("audiobook artifact is not a valid WAV")
	}
	return path, nil
}

func validateBookID(id string) error {
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") || filepath.IsAbs(id) {
		return fmt.Errorf("invalid audiobook id")
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return fmt.Errorf("invalid audiobook id")
	}
	return nil
}
