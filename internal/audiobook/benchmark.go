package audiobook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cpp-studio/internal/engine"
	"cpp-studio/internal/wav"
)

const (
	BenchmarkSchemaVersion = "dramabox-benchmark.v1"
	benchmarkSeed          = Seed(424242)
)

const BenchmarkFixture = `The Moon is Earth's only natural satellite. Its average distance from Earth is about 384,400 kilometres, and its gravity drives most ocean tides.

A solar day on Earth is approximately 24 hours. A sidereal day, measured against distant stars, is about 23 hours, 56 minutes, and 4 seconds.

Water freezes at 0 degrees Celsius and boils at 100 degrees Celsius at standard atmospheric pressure. Those temperatures change when pressure changes.

The International Space Station travels in low Earth orbit at roughly 28,000 kilometres per hour. It completes an orbit in about 90 minutes.
`

type BenchmarkRequest struct {
	Backend     string             `json:"backend"`
	IncludeCUDA bool               `json:"includeCuda,omitempty"`
	VoiceID     string             `json:"voiceId,omitempty"`
	Direction   string             `json:"direction,omitempty"`
	PromptSpec  DramaBoxPromptSpec `json:"promptSpec,omitempty"`
	Options     json.RawMessage    `json:"options,omitempty"`
}

type BenchmarkCaseResult struct {
	ID                   string          `json:"id"`
	Status               string          `json:"status"`
	Detail               string          `json:"detail,omitempty"`
	RequestedSeed        Seed            `json:"requestedSeed,omitempty"`
	ActualSeed           *Seed           `json:"actualSeed,omitempty"`
	SeedStatus           string          `json:"seedStatus,omitempty"`
	SynthesisMS          float64         `json:"synthesisMs,omitempty"`
	VerificationMS       float64         `json:"verificationMs,omitempty"`
	AssemblyMS           float64         `json:"assemblyMs,omitempty"`
	TotalMS              float64         `json:"totalMs,omitempty"`
	AudioDurationSeconds float64         `json:"audioDurationSeconds,omitempty"`
	RealTimeFactor       float64         `json:"realTimeFactor,omitempty"`
	PerformanceLabel     string          `json:"performanceLabel,omitempty"`
	OutputBytes          int             `json:"outputBytes,omitempty"`
	WAVFormat            *wav.Format     `json:"wavFormat,omitempty"`
	Sections             int             `json:"sections,omitempty"`
	ArtifactFile         string          `json:"artifactFile,omitempty"`
	Fidelity             *FidelityReport `json:"fidelity,omitempty"`
	Error                string          `json:"error,omitempty"`
}

type BenchmarkResult struct {
	SchemaVersion           string                `json:"schemaVersion"`
	ID                      string                `json:"id"`
	Status                  string                `json:"status"`
	CreatedAt               time.Time             `json:"createdAt"`
	CompletedAt             time.Time             `json:"completedAt,omitempty"`
	Backend                 string                `json:"backend"`
	FixtureSHA256           string                `json:"fixtureSha256"`
	FixtureWords            int                   `json:"fixtureWords"`
	ProfileFingerprint      string                `json:"profileFingerprint"`
	IdentityChanged         bool                  `json:"identityChanged"`
	IdentityChangeReason    string                `json:"identityChangeReason,omitempty"`
	Engine                  EngineIdentity        `json:"engine"`
	Voice                   VoiceIdentity         `json:"voice"`
	Direction               string                `json:"direction"`
	PromptSpec              DramaBoxPromptSpec    `json:"promptSpec"`
	Options                 SynthesisOptions      `json:"options"`
	Cases                   []BenchmarkCaseResult `json:"cases"`
	ColdRTF                 float64               `json:"coldRtf,omitempty"`
	WarmRTF                 float64               `json:"warmRtf,omitempty"`
	ProjectedChapterSeconds float64               `json:"projectedChapterSeconds,omitempty"`
	PeakVRAMMiB             *float64              `json:"peakVramMiB,omitempty"`
	ResidentVRAMMiB         *float64              `json:"residentVramMiB,omitempty"`
	Error                   string                `json:"error,omitempty"`
	Disclaimer              string                `json:"disclaimer"`
}

func normalizeBenchmarkRequest(request BenchmarkRequest) (BenchmarkRequest, error) {
	request.Backend = strings.ToLower(strings.TrimSpace(request.Backend))
	if request.Backend == "" {
		request.Backend = "cpu"
	}
	if request.Backend != "cpu" && request.Backend != "cuda" {
		return BenchmarkRequest{}, requestErrorf("benchmark backend must be cpu or cuda")
	}
	if request.Backend == "cuda" && !request.IncludeCUDA {
		return BenchmarkRequest{}, requestErrorf("CUDA benchmarking requires explicit includeCuda=true")
	}
	request.VoiceID = strings.TrimSpace(request.VoiceID)
	return request, nil
}

func benchmarkProfileFingerprint(engine EngineIdentity, voice VoiceIdentity, options SynthesisOptions, direction string, promptSpec DramaBoxPromptSpec, backend, fixtureHash string) string {
	data, _ := json.Marshal(struct {
		Engine              EngineIdentity
		Voice               VoiceIdentity
		Options             SynthesisOptions
		Direction           string
		PromptSpec          DramaBoxPromptSpec
		Backend             string
		FixtureHash         string
		PromptPolicyVersion int
	}{engine, voice, options, direction, promptSpec, backend, fixtureHash, CurrentPromptPolicyVersion})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (m *Manager) StartBenchmark(ctx context.Context, request BenchmarkRequest) (string, error) {
	request, err := normalizeBenchmarkRequest(request)
	if err != nil {
		return "", err
	}
	resolved, err := m.Preview(ctx, Request{
		EngineID: DramaBoxEngineID, VoiceID: request.VoiceID,
		Direction: request.Direction, PromptSpec: request.PromptSpec,
		OptionsJSON: string(request.Options), Verification: VerificationModeAuto,
	})
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeID != "" || m.creating {
		return "", fmt.Errorf("another audiobook is already narrating")
	}
	var release func()
	if m.reserveEngine != nil {
		var ok bool
		release, ok = m.reserveEngine(ctx, DramaBoxEngineID)
		if !ok {
			return "", fmt.Errorf("engine %q is busy", DramaBoxEngineID)
		}
	}
	m.benchmarkCounter++
	id := fmt.Sprintf("benchmark_%s_%03d", m.now().Format("20060102_150405"), m.benchmarkCounter)
	jobCtx, cancel := context.WithCancel(context.Background())
	m.activeID = id
	m.cancels[id] = cancel
	if m.registry != nil {
		m.registry.Track(id, "audiobook-benchmark", func() { _ = m.Cancel(id) })
	}
	fixtureHash := sha256.Sum256([]byte(BenchmarkFixture))
	result := BenchmarkResult{
		SchemaVersion: BenchmarkSchemaVersion, ID: id, Status: "running", CreatedAt: m.now(),
		Backend: request.Backend, FixtureSHA256: hex.EncodeToString(fixtureHash[:]), FixtureWords: len(strings.Fields(BenchmarkFixture)),
		Engine: resolved.Engine, Voice: resolved.Voice, Direction: resolved.Request.Direction,
		PromptSpec: resolved.Request.PromptSpec, Options: resolved.Request.Options,
		Disclaimer: "Technical timing and fidelity evidence does not certify subjective voice quality. Local generation consumes compute and storage.",
	}
	result.ProfileFingerprint = benchmarkProfileFingerprint(result.Engine, result.Voice, result.Options, result.Direction, result.PromptSpec, result.Backend, result.FixtureSHA256)
	if err := m.saveBenchmarkResult(result); err != nil {
		if release != nil {
			release()
		}
		m.activeID = ""
		delete(m.cancels, id)
		return "", err
	}
	go m.runBenchmark(jobCtx, result, release)
	return id, nil
}

func (m *Manager) runBenchmark(ctx context.Context, result BenchmarkResult, release func()) {
	defer func() {
		if release != nil {
			release()
		}
		m.mu.Lock()
		if m.activeID == result.ID {
			m.activeID = ""
		}
		delete(m.cancels, result.ID)
		m.mu.Unlock()
	}()
	fail := func(err error) {
		result.Status = "failed"
		result.Error = err.Error()
		result.CompletedAt = m.now()
		_ = m.saveBenchmarkResult(result)
		if m.registry != nil {
			if errors.Is(err, context.Canceled) {
				m.registry.MarkCancelled(result.ID)
			} else {
				m.registry.Fail(result.ID, err.Error())
			}
		}
	}
	checkpoint := func() error { return m.saveBenchmarkResult(result) }
	paragraph := strings.SplitN(BenchmarkFixture, "\n\n", 2)[0]
	options := result.Options
	options.Seed = benchmarkSeed
	voice := result.Voice.Reference

	if m.registry != nil {
		m.registry.Update(result.ID, 0.1, "cold factual paragraph")
	}
	cold, _, err := m.measureBenchmarkCase(ctx, result.ID, "cpu.cold_text", paragraph, result.PromptSpec, options, voice)
	result.Cases = append(result.Cases, cold)
	if err != nil {
		fail(err)
		return
	}
	result.ColdRTF = cold.RealTimeFactor
	if err := checkpoint(); err != nil {
		fail(err)
		return
	}
	if m.registry != nil {
		m.registry.Update(result.ID, 0.3, "warm factual paragraph")
	}
	warm, warmAudio, err := m.measureBenchmarkCase(ctx, result.ID, "cpu.warm_text", paragraph, result.PromptSpec, options, voice)
	result.Cases = append(result.Cases, warm)
	if err != nil {
		fail(err)
		return
	}
	result.WarmRTF = warm.RealTimeFactor
	result.ProjectedChapterSeconds = warm.RealTimeFactor * 4000 // 10,000 words at 150 wpm.
	if err := checkpoint(); err != nil {
		fail(err)
		return
	}

	if m.registry != nil {
		m.registry.Update(result.ID, 0.55, "native long-form fixture")
	}
	longSource := strings.Repeat(BenchmarkFixture+"\n", 3)
	longCase, err := m.measureLongBenchmarkCase(ctx, result.ID, longSource, result.PromptSpec, options, voice)
	result.Cases = append(result.Cases, longCase)
	if err != nil {
		fail(err)
		return
	}
	if err := checkpoint(); err != nil {
		fail(err)
		return
	}
	result.Cases = append(result.Cases,
		BenchmarkCaseResult{ID: "cpu.mem_saver_off", Status: "profile-required", Detail: "run in a fresh server configured with dramabox.mem_saver=false"},
		BenchmarkCaseResult{ID: "cpu.mem_saver_on", Status: "profile-required", Detail: "run in a fresh server configured with dramabox.mem_saver=true"},
	)
	if result.Backend == "cuda" {
		result.Cases = append(result.Cases, BenchmarkCaseResult{ID: "cuda.explicit", Status: "requested", Detail: "backend was explicitly requested; device metrics must confirm CUDA before drawing fit conclusions"})
	}
	if result.Voice.Reference != nil {
		result.Cases = append(result.Cases, BenchmarkCaseResult{ID: "voice.clone", Status: "complete", Detail: "all synthesis cases used the authorized stored reference"})
	} else {
		result.Cases = append(result.Cases, BenchmarkCaseResult{ID: "voice.clone", Status: "not-requested"})
	}
	result.Cases = append(result.Cases, BenchmarkCaseResult{ID: "recovery.cancel_restart", Status: "external-harness", Detail: "cancel this tracked job and verify persisted evidence after process restart"})

	fidelityCase := BenchmarkCaseResult{ID: "fidelity.asr", Status: "unavailable", Detail: "Whisper verifier is not configured"}
	if m.verify != nil {
		started := time.Now()
		verification, verifyErr := m.verify(ctx, paragraph, warmAudio)
		fidelityCase.VerificationMS = float64(time.Since(started)) / float64(time.Millisecond)
		if verifyErr != nil {
			fidelityCase.Detail = verifyErr.Error()
		} else {
			sum := sha256.Sum256([]byte(paragraph))
			report := evaluateFidelity(Section{ID: "benchmark", TextSHA256: hex.EncodeToString(sum[:])}, paragraph, verification, m.now())
			fidelityCase.Status = "complete"
			fidelityCase.Detail = string(report.Status)
			fidelityCase.Fidelity = &report
		}
	}
	result.Cases = append(result.Cases, fidelityCase)
	result.Status = "complete"
	result.CompletedAt = m.now()
	if err := m.saveBenchmarkResult(result); err != nil {
		fail(err)
		return
	}
	if m.registry != nil {
		m.registry.Complete(result.ID, map[string]string{
			"resultUrl":          "/v1/audiobooks/benchmark/results/" + result.ID,
			"profileFingerprint": result.ProfileFingerprint,
		})
	}
}

func (m *Manager) measureBenchmarkCase(ctx context.Context, benchmarkID, caseID, text string, promptSpec DramaBoxPromptSpec, options SynthesisOptions, voice *engine.Voice) (BenchmarkCaseResult, []byte, error) {
	prompt, err := BuildStructuredDramaBoxPrompt(promptSpec, text)
	if err != nil {
		return BenchmarkCaseResult{ID: caseID, Status: "failed", Error: err.Error()}, nil, err
	}
	request := SynthesisRequest{Text: prompt, EngineID: DramaBoxEngineID, Options: options, Voice: voice}
	result, err := m.invokeSynthesis(ctx, request)
	caseResult := BenchmarkCaseResult{ID: caseID, Status: "failed", RequestedSeed: options.Seed, SeedStatus: "requested"}
	if err != nil {
		caseResult.Error = err.Error()
		return caseResult, nil, err
	}
	if result.ActualSeed != nil {
		caseResult.ActualSeed = result.ActualSeed
		caseResult.SeedStatus = "confirmed"
		if *result.ActualSeed != options.Seed {
			err = fmt.Errorf("benchmark engine reported seed %d, requested %d", uint64(*result.ActualSeed), uint64(options.Seed))
			caseResult.Error = err.Error()
			return caseResult, nil, err
		}
	}
	if err := wav.ValidateBytes(result.Audio); err != nil {
		caseResult.Error = err.Error()
		return caseResult, nil, err
	}
	format, _, _ := wav.Decode(result.Audio)
	duration, _ := wav.Duration(result.Audio)
	caseResult.Status = "complete"
	caseResult.SynthesisMS = float64(result.Elapsed) / float64(time.Millisecond)
	caseResult.TotalMS = caseResult.SynthesisMS
	caseResult.AudioDurationSeconds = duration.Seconds()
	caseResult.OutputBytes = len(result.Audio)
	caseResult.WAVFormat = &format
	if duration > 0 {
		caseResult.RealTimeFactor = result.Elapsed.Seconds() / duration.Seconds()
	}
	caseResult.PerformanceLabel = benchmarkPerformanceLabel(caseResult.RealTimeFactor)
	dir := filepath.Join(m.benchmarkRootDir, benchmarkID)
	filename := strings.ReplaceAll(caseID, ".", "-") + ".wav"
	if err := writeFileAtomic(filepath.Join(dir, filename), result.Audio); err != nil {
		return caseResult, nil, err
	}
	caseResult.ArtifactFile = filename
	return caseResult, result.Audio, nil
}

func (m *Manager) measureLongBenchmarkCase(ctx context.Context, benchmarkID, source string, promptSpec DramaBoxPromptSpec, options SynthesisOptions, voice *engine.Voice) (BenchmarkCaseResult, error) {
	sections, err := PlanDramaBoxSections(source)
	caseResult := BenchmarkCaseResult{ID: "cpu.long_form", Status: "failed", RequestedSeed: options.Seed}
	if err != nil {
		caseResult.Error = err.Error()
		return caseResult, err
	}
	started := time.Now()
	clips := make([][]byte, 0, len(sections))
	for _, section := range sections {
		sectionOptions := options
		sectionOptions.Seed = section.Seed
		measured, audio, synthErr := m.measureBenchmarkCase(ctx, benchmarkID, "long-"+section.ID, source[section.StartByte:section.EndByte], promptSpec, sectionOptions, voice)
		if synthErr != nil {
			caseResult.Error = measured.Error
			return caseResult, synthErr
		}
		clips = append(clips, audio)
		caseResult.SynthesisMS += measured.SynthesisMS
	}
	assemblyStarted := time.Now()
	joined, err := wav.Concatenate(clips, 50*time.Millisecond)
	caseResult.AssemblyMS = float64(time.Since(assemblyStarted)) / float64(time.Millisecond)
	if err != nil {
		caseResult.Error = err.Error()
		return caseResult, err
	}
	duration, _ := wav.Duration(joined)
	format, _, _ := wav.Decode(joined)
	caseResult.Status = "complete"
	caseResult.Sections = len(sections)
	caseResult.TotalMS = float64(time.Since(started)) / float64(time.Millisecond)
	caseResult.AudioDurationSeconds = duration.Seconds()
	caseResult.OutputBytes = len(joined)
	caseResult.WAVFormat = &format
	if duration > 0 {
		caseResult.RealTimeFactor = (caseResult.SynthesisMS / 1000) / duration.Seconds()
	}
	caseResult.PerformanceLabel = benchmarkPerformanceLabel(caseResult.RealTimeFactor)
	filename := "cpu-long-form.wav"
	if err := writeFileAtomic(filepath.Join(m.benchmarkRootDir, benchmarkID, filename), joined); err != nil {
		return caseResult, err
	}
	caseResult.ArtifactFile = filename
	return caseResult, nil
}

func benchmarkPerformanceLabel(rtf float64) string {
	if rtf <= 1 {
		return "interactive"
	}
	if rtf <= 5 {
		return "batch-usable"
	}
	return "overnight"
}

func (m *Manager) saveBenchmarkResult(result BenchmarkResult) error {
	dir := filepath.Join(m.benchmarkRootDir, result.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create benchmark result directory: %w", err)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encode benchmark result: %w", err)
	}
	return writeFileAtomic(filepath.Join(dir, "result.json"), append(data, '\n'))
}

func validBenchmarkID(id string) bool {
	if !strings.HasPrefix(id, "benchmark_") || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
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

func (m *Manager) BenchmarkResult(ctx context.Context, id string) (BenchmarkResult, error) {
	if !validBenchmarkID(id) {
		return BenchmarkResult{}, fmt.Errorf("benchmark result not found")
	}
	data, err := os.ReadFile(filepath.Join(m.benchmarkRootDir, id, "result.json"))
	if os.IsNotExist(err) {
		return BenchmarkResult{}, fmt.Errorf("benchmark result not found")
	}
	if err != nil {
		return BenchmarkResult{}, err
	}
	var result BenchmarkResult
	if err := json.Unmarshal(data, &result); err != nil || result.ID != id || result.SchemaVersion != BenchmarkSchemaVersion {
		return BenchmarkResult{}, fmt.Errorf("benchmark result is corrupt")
	}
	resolved, resolveErr := m.Preview(ctx, Request{EngineID: DramaBoxEngineID, VoiceID: result.Voice.ID, Direction: result.Direction, PromptSpec: result.PromptSpec, Options: result.Options, Verification: VerificationModeAuto})
	if resolveErr != nil {
		result.IdentityChanged = true
		result.IdentityChangeReason = resolveErr.Error()
		return result, nil
	}
	current := benchmarkProfileFingerprint(resolved.Engine, resolved.Voice, resolved.Request.Options, resolved.Request.Direction, resolved.Request.PromptSpec, result.Backend, result.FixtureSHA256)
	result.IdentityChanged = current != result.ProfileFingerprint
	if result.IdentityChanged {
		result.IdentityChangeReason = "engine, model, voice, prompt, option, backend, or fixture identity changed"
	}
	return result, nil
}

func (m *Manager) ListBenchmarkResults(ctx context.Context) ([]BenchmarkResult, error) {
	entries, err := os.ReadDir(m.benchmarkRootDir)
	if os.IsNotExist(err) {
		return []BenchmarkResult{}, nil
	}
	if err != nil {
		return nil, err
	}
	results := make([]BenchmarkResult, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !validBenchmarkID(entry.Name()) {
			continue
		}
		result, loadErr := m.BenchmarkResult(ctx, entry.Name())
		if loadErr == nil {
			results = append(results, result)
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].CreatedAt.After(results[j].CreatedAt) })
	return results, nil
}
