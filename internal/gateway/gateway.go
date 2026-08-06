package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"math"
	mrand "math/rand/v2"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"cpp-studio/internal/audiobook"
	"cpp-studio/internal/config"
	"cpp-studio/internal/demo"
	"cpp-studio/internal/engine"
	"cpp-studio/internal/gguf"
	"cpp-studio/internal/jobs"
	"cpp-studio/internal/library"
	"cpp-studio/internal/lifecycle"
	"cpp-studio/internal/models"
	"cpp-studio/internal/story"
	"cpp-studio/internal/storybuilder"
	"cpp-studio/internal/voice"
	"cpp-studio/internal/wav"
)

type router struct {
	mux                  *http.ServeMux
	cfg                  config.Config
	manager              *lifecycle.Manager
	client               *http.Client
	engines              engine.Invoker
	stories              *story.Manager
	storyBuilderProjects *storybuilder.Store
	voices               *voice.Store
	catalog              models.Manifest
	modelsRoot           string
	installer            *models.Installer
	jobs                 *jobs.Registry
	library              *library.Store
	audiobooks           audiobook.Service

	// encodersMu guards the one-time probe of what the operator's ffmpeg can
	// encode. The binary does not change under a running gateway.
	encodersMu sync.Mutex
	encoders   map[string]bool

	// verifyMu guards the verify-all state: deep-check results overlay the
	// catalog's fast stat states until the next gateway restart.
	verifyMu      sync.Mutex
	verifyStates  map[string]string
	verifyRunning bool

	// discoveryMu guards the optional fixed-command audio.cpp discovery cache.
	// The cache key incorporates configured command paths and file identities,
	// so replacing a runtime causes the next catalog read to refresh it.
	discoveryMu     sync.Mutex
	discoveryConfig *models.DiscoveryConfig
	discoveryRunner models.CommandRunner
	discoveryLast   models.DiscoveryResult

	// gpuQuery is the nvidia-smi seam, swappable in tests. gpuMu guards a
	// short-lived snapshot so the variants listing (hit on every panel
	// init and after every switch) does not shell out each time.
	gpuQuery gpuQueryFunc
	gpuMu    sync.Mutex
	gpuAt    time.Time
	gpuLast  []gpuInfo
	gpuErr   error

	// ggufMu guards the header-read cache for byom fit preflight, keyed by
	// path and revalidated against size+mtime.
	ggufMu    sync.Mutex
	ggufCache map[string]ggufCacheEntry
}

const (
	defaultChatTimeout          = 120 * time.Second
	maxJSONBodyBytes            = 64 * 1024
	maxTranscriptionUploadBytes = 32 * 1024 * 1024
	maxChatReplyBytes           = 1024 * 1024
)

// NewRouter builds the cpp-studio gateway HTTP routes.
func NewRouter(cfg config.Config, manager *lifecycle.Manager) http.Handler {
	r := &router{
		cfg:                  cfg,
		manager:              manager,
		client:               http.DefaultClient,
		engines:              engine.NewRunner(cfg.Engines, manager),
		voices:               voice.NewStore(""),
		storyBuilderProjects: storybuilder.NewStore(""),
		jobs:                 jobs.NewRegistry(),
		library:              library.NewStore(""),
		gpuQuery:             defaultGPUQuery,
		ggufCache:            map[string]ggufCacheEntry{},
	}
	if whisperCfg, ok := cfg.Engines["whisper"]; ok && whisperVADConfigured(whisperCfg) {
		r.voices = voice.NewStoreWithOptions("", voice.StoreOptions{AnalyzeVAD: func(wavBytes []byte) (time.Duration, error) {
			segments, err := r.transcribeSegments(context.Background(), wavBytes)
			if err != nil {
				return 0, err
			}
			return spokenSegmentDuration(segments), nil
		}})
	}
	storyOptions := story.ManagerOptions{
		ReserveEngine:        r.reserveEngine,
		Synthesize:           r.synthesizeSpeech,
		SynthesisFingerprint: synthesisFingerprint(cfg.Engines["audio"]),
		Jobs:                 r.jobs,
	}
	// With a llama engine configured, stories are written by the model;
	// without one (CI, pure-fixture setups) the deterministic fixture
	// script keeps the pipeline runnable.
	if _, ok := cfg.Engines["llama"]; ok {
		storyOptions.Script = r.writeStoryScript
	}
	// Delivery exports need the operator's own ffmpeg. Without it the story
	// manager has no transcoder and the export route says so.
	if _, ok := cfg.Engines["ffmpeg"]; ok {
		storyOptions.Transcode = r.transcodeAudio
		storyOptions.Measure = r.measureLoudness
	}
	r.stories = story.NewManager(storyOptions)
	audiobookOptions := audiobook.ManagerOptions{
		ReserveEngine: r.reserveEngine,
		Synthesize:    r.synthesizeAudiobook,
		ResolveEngine: r.resolveAudiobookEngine,
		ResolveVoice:  r.resolveAudiobookVoice,
		Jobs:          r.jobs,
	}
	if whisperConfig, ok := cfg.Engines["whisper"]; ok {
		verifierIdentity := fullSynthesisFingerprint(whisperConfig)
		audiobookOptions.Verify = func(ctx context.Context, source string, wavBytes []byte) (audiobook.Verification, error) {
			_ = source
			transcript, err := r.transcribe(ctx, wavBytes)
			return audiobook.Verification{Transcript: transcript, VerifierIdentity: verifierIdentity}, err
		}
	}
	r.audiobooks = audiobook.NewManager(audiobookOptions)

	// The model manifest is optional: a config without a models block (CI,
	// fixture setups) simply serves an empty catalog rather than failing.
	if cfg.Models != nil && cfg.Models.Manifest != "" {
		if manifest, err := models.Load(cfg.Models.Manifest); err == nil {
			r.catalog = manifest
			r.modelsRoot = cfg.Models.Root
		}
	}
	if cfg.Models != nil && cfg.Models.Discovery != nil {
		discovery := cfg.Models.Discovery
		timeout := 10 * time.Second
		if discovery.TimeoutSeconds > 0 {
			timeout = time.Duration(discovery.TimeoutSeconds) * time.Second
		}
		r.discoveryConfig = &models.DiscoveryConfig{
			PythonCommand:   discovery.PythonCommand,
			ManagerScript:   discovery.ManagerScript,
			AudioCLI:        discovery.AudioCLI,
			WorkingDir:      discovery.WorkingDir,
			AllowedPackages: append([]string(nil), discovery.AllowedPackages...),
			Timeout:         timeout,
		}
		r.discoveryRunner = models.ExecCommandRunner{WorkingDir: discovery.WorkingDir}
		r.installer = models.NewInstaller(r.catalog, r.modelsRoot, discovery.AllowedPackages)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", r.handleRoot)
	mux.HandleFunc("/demo", r.handleDemoRedirect)
	mux.Handle("/demo/", http.StripPrefix("/demo/", demo.Handler()))
	mux.HandleFunc("/health", r.handleHealth)
	mux.HandleFunc("/v1/models/catalog", r.handleModelsCatalog)
	mux.HandleFunc("/v1/models/verify", r.handleModelsVerify)
	mux.HandleFunc("/v1/models/", r.handleModel)
	mux.HandleFunc("/v1/engines/", r.handleEngines)
	mux.HandleFunc("/v1/gpu", r.handleGPU)
	mux.HandleFunc("/v1/jobs", r.handleJobs)
	mux.HandleFunc("/v1/jobs/", r.handleJob)
	mux.HandleFunc("/v1/library", r.handleLibrary)
	mux.HandleFunc("/v1/library/", r.handleLibraryItem)
	mux.HandleFunc("/v1/audiobooks", r.handleAudiobooks)
	mux.HandleFunc("/v1/audiobooks/preview", r.handleAudiobookPreview)
	mux.HandleFunc("/v1/audiobooks/preview-document", r.handleAudiobookDocumentPreview)
	mux.HandleFunc("/v1/audiobooks/benchmark", r.handleAudiobookBenchmark)
	mux.HandleFunc("/v1/audiobooks/benchmark/results", r.handleAudiobookBenchmarkResults)
	mux.HandleFunc("/v1/audiobooks/benchmark/results/", r.handleAudiobookBenchmarkResult)
	mux.HandleFunc("/v1/audiobooks/", r.handleAudiobook)
	mux.HandleFunc("/v1/chat/completions", r.handleChatCompletions)
	mux.HandleFunc("/v1/images/generations", r.handleImageGenerations)
	mux.HandleFunc("/v1/images/descriptions", r.handleImageDescriptions)
	mux.HandleFunc("/v1/stories", r.handleStories)
	mux.HandleFunc("/v1/stories/draft", r.handleStoryDraft)
	mux.HandleFunc("/v1/stories/", r.handleStory)
	mux.HandleFunc("/v1/story-builder-projects", r.handleStoryBuilderProjects)
	mux.HandleFunc("/v1/story-builder-projects/", r.handleStoryBuilderProject)
	mux.HandleFunc("/v1/audio/speech", r.handleSpeech)
	mux.HandleFunc("/v1/audio/transcriptions", r.handleTranscriptions)
	mux.HandleFunc("/v1/audio/diarization", r.handleDiarization)
	mux.HandleFunc("/v1/audio/import", r.handleAudioImport)
	mux.HandleFunc("/v1/audio/formats", r.handleAudioFormats)
	mux.HandleFunc("/v1/audio/decode", r.handleAudioDecode)
	mux.HandleFunc("/v1/audio/encode", r.handleAudioEncode)
	mux.HandleFunc("/v1/voice", r.handleVoice)
	mux.HandleFunc("/v1/voices", r.handleVoices)
	mux.HandleFunc("/v1/voices/design", r.handleVoiceDesign)
	mux.HandleFunc("/v1/voices/", r.handleVoiceClone)
	mux.HandleFunc("/v1/character-voices/", r.handleCharacterVoice)
	r.mux = mux
	return r
}

func (r *router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mux.ServeHTTP(w, req)
}

func (r *router) handleRoot(w http.ResponseWriter, req *http.Request) {
	if req.URL.Path != "/" {
		http.NotFound(w, req)
		return
	}
	if !requireMethod(w, req, http.MethodGet) {
		return
	}
	http.Redirect(w, req, "/demo/", http.StatusFound)
}

func (r *router) handleDemoRedirect(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodGet) {
		return
	}
	http.Redirect(w, req, "/demo/", http.StatusFound)
}

func (r *router) handleHealth(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodGet) {
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(r.manager.Health()); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
	}
}

// handleModelsCatalog reports the model manifest with each model's live on-disk
// status (present / missing / size-mismatch), powering the Models tab. A config
// without a models block serves an empty catalog rather than erroring. Deep
// verification results from the last verify run overlay the fast stat states.
func (r *router) handleModelsCatalog(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodGet) {
		return
	}
	statuses := r.catalog.Statuses(r.modelsRoot)
	if statuses == nil {
		statuses = []models.Status{}
	}
	r.verifyMu.Lock()
	for i := range statuses {
		// Only overlay when the fast stat still agrees the model looks intact;
		// a file that changed since verification must show its stat state.
		if deep, ok := r.verifyStates[statuses[i].ID]; ok && statuses[i].State == models.StatePresent {
			statuses[i].State = deep
		}
	}
	verifying := r.verifyRunning
	r.verifyMu.Unlock()
	discovery := r.modelDiscovery(req.Context())
	allowedPackages := map[string]bool{}
	if r.discoveryConfig != nil {
		for _, id := range r.discoveryConfig.AllowedPackages {
			allowedPackages[id] = true
		}
	}
	health := r.manager.Health()
	benchmarkStatus, benchmarkReason, benchmarkCurrent := "", "", false
	if results, err := r.audiobooks.ListBenchmarkResults(req.Context()); err == nil && len(results) > 0 {
		benchmarkStatus = results[0].Status
		benchmarkCurrent = !results[0].IdentityChanged && results[0].Status == "complete"
		if results[0].IdentityChanged {
			benchmarkReason = results[0].IdentityChangeReason
		} else if results[0].Status != "complete" {
			benchmarkReason = results[0].Error
		}
	}
	for i := range statuses {
		statuses[i].Installable = allowedPackages[statuses[i].PackageID] && statuses[i].HasImmutableInstallMetadata()
		if r.discoveryConfig != nil {
			packageKnown := discovery.HasPackage(statuses[i].PackageID)
			loaderAvailable := discovery.HasLoader(statuses[i].Family)
			statuses[i].PackageKnown = &packageKnown
			statuses[i].LoaderAvailable = &loaderAvailable
		}
		_, statuses[i].Configured = r.cfg.Engines[statuses[i].Engine]
		if engineHealth, ok := health.Engines[statuses[i].Engine]; ok {
			statuses[i].Healthy = engineHealth.Ready
		}
		if statuses[i].Engine == audiobook.DramaBoxEngineID {
			statuses[i].BenchmarkCurrent = benchmarkCurrent
			statuses[i].BenchmarkStatus = benchmarkStatus
			statuses[i].BenchmarkReason = benchmarkReason
		}
		if statuses[i].Present && strings.EqualFold(filepath.Ext(statuses[i].AbsPath), ".gguf") {
			inspection := &models.GGUFInspection{FileBytes: statuses[i].ActualBytes}
			if info, err := r.ggufInfo(statuses[i].AbsPath); err != nil {
				inspection.Error = err.Error()
			} else {
				inspection.Version = info.Version
				inspection.Architecture = info.Architecture
				inspection.ExpertCount = info.ExpertCount
				inspection.SizeLabel = info.SizeLabel
			}
			statuses[i].GGUF = inspection
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"root":            r.modelsRoot,
		"models":          statuses,
		"verifying":       verifying,
		"runtimeIdentity": discovery.RuntimeIdentity,
		"discoveryError":  discovery.DiscoveryError,
	})
}

func (r *router) modelDiscovery(ctx context.Context) models.DiscoveryResult {
	if r.discoveryConfig == nil {
		return models.DiscoveryResult{}
	}
	identity := models.DiscoveryRuntimeIdentity(*r.discoveryConfig)
	r.discoveryMu.Lock()
	defer r.discoveryMu.Unlock()
	if r.discoveryLast.RuntimeIdentity == identity && !r.discoveryLast.DiscoveredAt.IsZero() &&
		(r.discoveryLast.DiscoveryError == "" || time.Since(r.discoveryLast.DiscoveredAt) < 30*time.Second) {
		return r.discoveryLast
	}
	r.discoveryLast = models.Discover(ctx, *r.discoveryConfig, r.catalog, r.discoveryRunner)
	return r.discoveryLast
}

// handleModel exposes the two-step, server-authoritative install contract.
// The route contributes only a tracked model id and an opaque confirmation;
// no command, source, destination, or package argument comes from the client.
func (r *router) handleModel(w http.ResponseWriter, req *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(req.URL.Path, "/v1/models/"), "/")
	parts := strings.Split(tail, "/")
	if len(parts) == 3 && parts[0] != "" && parts[1] == "install" && parts[2] == "preview" {
		if !requireMethod(w, req, http.MethodPost) {
			return
		}
		if r.installer == nil {
			writeJSONError(w, http.StatusConflict, "model installation is not configured")
			return
		}
		preview, err := r.installer.Preview(parts[0])
		if err != nil {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(preview)
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "install" {
		if !requireMethod(w, req, http.MethodPost) {
			return
		}
		if r.installer == nil {
			writeJSONError(w, http.StatusConflict, "model installation is not configured")
			return
		}
		var body struct {
			ConfirmationID string `json:"confirmationId"`
		}
		req.Body = http.MaxBytesReader(w, req.Body, maxJSONBodyBytes)
		decoder := json.NewDecoder(req.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || strings.TrimSpace(body.ConfirmationID) == "" {
			writeJSONError(w, http.StatusBadRequest, "a confirmationId-only JSON body is required")
			return
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			writeJSONError(w, http.StatusBadRequest, "request must contain one JSON object")
			return
		}
		task, err := r.installer.Begin(parts[0], body.ConfirmationID)
		if err != nil {
			status := http.StatusConflict
			if strings.Contains(err.Error(), "unknown model id") {
				status = http.StatusNotFound
			}
			writeJSONError(w, status, err.Error())
			return
		}
		id := fmt.Sprintf("model_install_%d", time.Now().UTC().UnixNano())
		r.jobs.TrackCancellable(id, "model_install", task.Cancel)
		go r.runModelInstall(id, task)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"id": id, "statusUrl": "/v1/jobs/" + id})
		return
	}
	http.NotFound(w, req)
}

func (r *router) runModelInstall(id string, task *models.InstallTask) {
	status, err := task.Run(func(update models.InstallProgress) {
		fraction := 0.02
		detail := update.Phase
		switch update.Phase {
		case "downloading":
			if update.ExpectedBytes > 0 {
				fraction = 0.05 + 0.75*float64(update.Downloaded)/float64(update.ExpectedBytes)
			}
			detail = fmt.Sprintf("downloading %d/%d bytes", update.Downloaded, update.ExpectedBytes)
		case "verifying":
			fraction = 0.82
		case "promoting":
			fraction = 0.92
		case "refreshing_catalog":
			fraction = 0.97
		}
		r.jobs.Update(id, fraction, detail)
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			r.jobs.MarkCancelled(id)
		} else {
			r.jobs.Fail(id, err.Error())
		}
		return
	}
	r.verifyMu.Lock()
	if r.verifyStates == nil {
		r.verifyStates = map[string]string{}
	}
	r.verifyStates[status.ID] = status.State
	r.verifyMu.Unlock()
	r.jobs.Complete(id, map[string]string{"modelId": status.ID, "state": status.State, "ready": strconv.FormatBool(status.State == models.StateVerified)})
}

// handleModelsVerify starts a deep verification of every model — full sha256
// for checksummed files, exhaustive walks for directory models — as a tracked
// job. Results overlay the catalog until the gateway restarts.
func (r *router) handleModelsVerify(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodPost) {
		return
	}
	if len(r.catalog.Models) == 0 {
		writeJSONError(w, http.StatusBadRequest, "no model manifest configured")
		return
	}
	r.verifyMu.Lock()
	if r.verifyRunning {
		r.verifyMu.Unlock()
		writeJSONError(w, http.StatusConflict, "a verification run is already in progress")
		return
	}
	r.verifyRunning = true
	r.verifyMu.Unlock()

	id := fmt.Sprintf("verify_%s", time.Now().UTC().Format("20060102_150405"))
	ctx, cancel := context.WithCancel(context.Background())
	r.jobs.Track(id, "verify", cancel)

	go func() {
		defer func() {
			r.verifyMu.Lock()
			r.verifyRunning = false
			r.verifyMu.Unlock()
		}()
		statuses, err := r.catalog.VerifyAll(ctx, r.modelsRoot, func(done, total int, modelID string) {
			r.jobs.Update(id, float64(done)/float64(total), "verifying "+modelID)
		})
		result := map[string]string{}
		states := make(map[string]string, len(statuses))
		counts := map[string]int{}
		for _, s := range statuses {
			states[s.ID] = s.State
			counts[s.State]++
			if s.State == models.StateCorrupt {
				result["corrupt"] = strings.TrimSpace(result["corrupt"] + " " + s.ID)
			}
		}
		r.verifyMu.Lock()
		r.verifyStates = states
		r.verifyMu.Unlock()
		if err != nil {
			if ctx.Err() != nil {
				r.jobs.MarkCancelled(id)
			} else {
				r.jobs.Fail(id, err.Error())
			}
			return
		}
		result["verified"] = strconv.Itoa(counts[models.StateVerified])
		result["total"] = strconv.Itoa(len(statuses))
		r.jobs.Complete(id, result)
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":        id,
		"statusUrl": "/v1/jobs/" + id,
	})
}

// handleEngines routes the engine-control surface:
//
//	GET  /v1/engines/profiles          -> the configured VRAM profiles
//	POST /v1/engines/profiles/{name}   -> apply a profile (start members, stop the rest)
//	POST /v1/engines/{name}/{action}   -> start | stop | reload one engine
//
// Only server-mode engines hold VRAM between requests, so control acts on
// them; subprocess engines are reported but never started or stopped.
func (r *router) handleEngines(w http.ResponseWriter, req *http.Request) {
	rest := strings.TrimPrefix(req.URL.Path, "/v1/engines/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")

	if parts[0] == "profiles" {
		switch {
		case len(parts) == 1:
			if !requireMethod(w, req, http.MethodGet) {
				return
			}
			profiles := r.cfg.Profiles
			if profiles == nil {
				profiles = map[string][]string{}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"profiles": profiles})
			return
		case len(parts) == 2:
			if !requireMethod(w, req, http.MethodPost) {
				return
			}
			r.applyProfile(w, req, parts[1])
			return
		}
		http.NotFound(w, req)
		return
	}

	if len(parts) != 2 {
		http.NotFound(w, req)
		return
	}
	// The model pickers: list an engine's variants, or switch to one. A
	// switch restarts a running server with the chosen args — the busy
	// state on the console covers the model load.
	if parts[1] == "variants" {
		if !requireMethod(w, req, http.MethodGet) {
			return
		}
		r.writeVariantListing(w, req, parts[0])
		return
	}
	if parts[1] == "variant" {
		if !requireMethod(w, req, http.MethodPost) {
			return
		}
		var body struct {
			ID     string `json:"id"`
			Remedy string `json:"remedy"`
		}
		req.Body = http.MaxBytesReader(w, req.Body, maxJSONBodyBytes)
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || strings.TrimSpace(body.ID) == "" {
			writeJSONError(w, http.StatusBadRequest, "body must be {\"id\": \"<variant>\"}")
			return
		}
		var extra []string
		if body.Remedy != "" {
			args, ok := remedyArgs[body.Remedy]
			if !ok {
				writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("unknown remedy %q", body.Remedy))
				return
			}
			if err := r.checkRemedyApplies(parts[0], body.ID, body.Remedy); err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			extra = args
		}
		// Story production scripts through the same llama the chat model
		// picker restarts; a swap mid-production would fail the job.
		if parts[0] == "llama" && r.stories != nil && r.stories.Active() {
			writeJSONError(w, http.StatusConflict, "a story production is running; switching the chat model would restart the engine writing it")
			return
		}
		if err := r.manager.SetVariant(req.Context(), parts[0], body.ID, extra...); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if parts[0] == "llama" {
			go r.warmupLlama()
		}
		r.writeVariantListing(w, req, parts[0])
		return
	}
	if !requireMethod(w, req, http.MethodPost) {
		return
	}
	name, action := parts[0], parts[1]
	engineCfg, ok := r.engine(name)
	if !ok {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("unknown engine %q", name))
		return
	}
	if !isServerEngine(engineCfg) {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("engine %q is not a server engine; subprocess engines hold no VRAM and need no control", name))
		return
	}

	var err error
	switch action {
	case "start":
		err = r.manager.Start(req.Context(), name)
	case "stop":
		err = r.manager.Stop(req.Context(), name)
	case "reload":
		if err = r.manager.Stop(req.Context(), name); err == nil {
			err = r.manager.Start(req.Context(), name)
		}
	default:
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("unknown engine action %q", action))
		return
	}
	if err != nil {
		writeJSONError(w, http.StatusConflict, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(r.manager.Health())
}

// applyProfile starts the profile's server-mode members and stops every other
// server-mode engine, so the resident VRAM footprint matches the named set.
// Failures are collected rather than aborting: a profile apply should leave
// the system as close to the requested shape as it can get.
func (r *router) applyProfile(w http.ResponseWriter, req *http.Request, name string) {
	members, ok := r.cfg.Profiles[name]
	if !ok {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("unknown profile %q", name))
		return
	}
	inProfile := make(map[string]bool, len(members))
	for _, member := range members {
		inProfile[member] = true
	}

	var failures []string
	// Stop first so VRAM is free before new residents load.
	for engineName, engineCfg := range r.cfg.Engines {
		if !isServerEngine(engineCfg) || inProfile[engineName] {
			continue
		}
		if err := r.manager.Stop(req.Context(), engineName); err != nil {
			failures = append(failures, fmt.Sprintf("stop %s: %v", engineName, err))
		}
	}
	for _, engineName := range members {
		if !isServerEngine(r.cfg.Engines[engineName]) {
			continue
		}
		if err := r.manager.Start(req.Context(), engineName); err != nil && !strings.Contains(err.Error(), "already started") {
			failures = append(failures, fmt.Sprintf("start %s: %v", engineName, err))
		}
	}

	w.Header().Set("Content-Type", "application/json")
	status := http.StatusOK
	if len(failures) > 0 {
		status = http.StatusMultiStatus
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"profile":  name,
		"failures": failures,
		"health":   r.manager.Health(),
	})
}

// isServerEngine reports whether an engine runs as a resident server. Mode ""
// defaults to server, matching the lifecycle manager's interpretation.
func isServerEngine(cfg config.EngineConfig) bool {
	return cfg.Mode == "" || cfg.Mode == "server"
}

// gpuInfo is one GPU's memory picture, as nvidia-smi reports it.
type gpuInfo struct {
	Name     string `json:"name"`
	TotalMiB int    `json:"totalMiB"`
	UsedMiB  int    `json:"usedMiB"`
}

// gpuQueryFunc is the seam between the gateway and nvidia-smi, swappable
// in tests for canned memory pictures.
type gpuQueryFunc func(ctx context.Context) ([]gpuInfo, error)

// defaultGPUQuery is what NewRouter wires in; tests swap it for canned
// memory pictures and restore it on cleanup.
var defaultGPUQuery gpuQueryFunc = queryNvidiaSMI

func queryNvidiaSMI(ctx context.Context) ([]gpuInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=name,memory.total,memory.used", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil, err
	}
	var gpus []gpuInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) != 3 {
			continue
		}
		total, err1 := strconv.Atoi(strings.TrimSpace(fields[1]))
		used, err2 := strconv.Atoi(strings.TrimSpace(fields[2]))
		if err1 != nil || err2 != nil {
			continue
		}
		gpus = append(gpus, gpuInfo{Name: strings.TrimSpace(fields[0]), TotalMiB: total, UsedMiB: used})
	}
	if len(gpus) == 0 {
		return nil, fmt.Errorf("nvidia-smi output had no parseable GPUs")
	}
	return gpus, nil
}

// gpuSnapshot serves a briefly-cached memory picture. Two seconds is long
// enough to make the listing's double-hit (render, then post-switch
// resync) free, and short enough to stay honest across profile changes.
func (r *router) gpuSnapshot(ctx context.Context) ([]gpuInfo, error) {
	r.gpuMu.Lock()
	defer r.gpuMu.Unlock()
	if time.Since(r.gpuAt) < 2*time.Second {
		return r.gpuLast, r.gpuErr
	}
	r.gpuLast, r.gpuErr = r.gpuQuery(ctx)
	r.gpuAt = time.Now()
	return r.gpuLast, r.gpuErr
}

// handleGPU reports overall GPU memory via nvidia-smi when available, so the
// Engines tab can show the VRAM effect of profile changes. Machines without
// nvidia-smi report available:false rather than erroring.
func (r *router) handleGPU(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodGet) {
		return
	}
	gpus, err := r.gpuSnapshot(req.Context())
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"available": false})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"available": len(gpus) > 0, "gpus": gpus})
}

// ggufCacheEntry memoizes one model file's header reading; the walk skims
// real metadata and should not rerun on every listing.
type ggufCacheEntry struct {
	size  int64
	mtime time.Time
	info  gguf.Info
	err   error
}

func (r *router) ggufInfo(path string) (gguf.Info, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return gguf.Info{}, err
	}
	r.ggufMu.Lock()
	entry, ok := r.ggufCache[path]
	r.ggufMu.Unlock()
	if ok && entry.size == stat.Size() && entry.mtime.Equal(stat.ModTime()) {
		return entry.info, entry.err
	}
	info, err := gguf.ReadInfo(path)
	r.ggufMu.Lock()
	r.ggufCache[path] = ggufCacheEntry{size: stat.Size(), mtime: stat.ModTime(), info: info, err: err}
	r.ggufMu.Unlock()
	return info, err
}

// Fit preflight: whether a byom model's weights leave room to actually
// serve. The numbers are calibrated for the -c 8192 context the byomArgs
// templates use — KV cache plus compute buffers plus CUDA context land
// between 1.5 and 2.5 GiB for 4B-35B models, so weights plus headroom is
// a promise and weights plus floor is the bare survival line. Between the
// two is "tight": it should load, but expect context pressure.
const (
	fitHeadroomMiB = 2560
	fitFloorMiB    = 1280
)

func fitVerdict(fileMiB, freeMiB int) string {
	switch {
	case fileMiB+fitHeadroomMiB <= freeMiB:
		return "fits"
	case fileMiB+fitFloorMiB <= freeMiB:
		return "tight"
	default:
		return "too_big"
	}
}

type fitRemedy struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type fitInfo struct {
	Verdict  string      `json:"verdict"`
	Detail   string      `json:"detail"`
	Remedies []fitRemedy `json:"remedies"`
}

// variantView is a variant on the wire, optionally decorated with byom
// fit data. Engines without a byom directory serve plain VariantInfo.
type variantView struct {
	lifecycle.VariantInfo
	Bytes int64    `json:"bytes,omitempty"`
	Fit   *fitInfo `json:"fit,omitempty"`
}

// remedyArgs maps the remedy contract's ids to the flags they stand for.
// Remedies are server-defined by design: the switch route accepts a name
// from this table, never client-supplied arguments.
var remedyArgs = map[string][]string{
	"cpu-moe": {"--cpu-moe"},
}

const cpuMoeLabel = "Load with experts on CPU (--cpu-moe)"

func mib(bytes int64) int { return int(bytes >> 20) }

func gib(bytes int64) string { return fmt.Sprintf("%.1f GiB", float64(bytes)/(1<<30)) }

// modelArgPath digs the model file out of a configured variant's args, so
// the fit check can credit back the VRAM the current model will free on
// swap. llama-specific flag knowledge stays here in the gateway, next to
// the llama-specific URL inference.
func modelArgPath(args []string) string {
	for i, arg := range args {
		if (arg == "-m" || arg == "--model") && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// writeVariantListing serves an engine's variants — plain for engines
// without a byom directory (byte-identical to the pre-byom payload), and
// decorated with size, fit and the active remedy when byom entries exist.
func (r *router) writeVariantListing(w http.ResponseWriter, req *http.Request, name string) {
	variants, ok := r.manager.Variants(name)
	if !ok {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("engine %q has no variants", name))
		return
	}
	hasByom := false
	for _, v := range variants {
		if v.ModelPath != "" {
			hasByom = true
			break
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if !hasByom {
		_ = json.NewEncoder(w).Encode(map[string]any{"variants": variants})
		return
	}
	views := r.decorateByomVariants(req.Context(), name, variants)
	activeRemedy := ""
	if remedy := r.manager.Health().Engines[name].Remedy; remedy != "" {
		for id, args := range remedyArgs {
			if strings.Join(args, " ") == remedy {
				activeRemedy = id
				break
			}
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"variants": views, "activeRemedy": activeRemedy})
}

// checkRemedyApplies enforces that a remedy is only accepted where the
// listing could have offered it: cpu-moe needs a byom model whose header
// says mixture-of-experts. The GPU verdict is deliberately not re-checked
// — VRAM moves between listing and click, and a button the UI honestly
// offered must not bounce.
func (r *router) checkRemedyApplies(engineName, id, remedy string) error {
	if !strings.HasPrefix(id, "byom:") {
		return fmt.Errorf("remedies apply only to byom models")
	}
	engineCfg, ok := r.engine(engineName)
	if !ok || engineCfg.ByomDir == "" {
		return fmt.Errorf("engine %q has no byom directory", engineName)
	}
	fname := strings.TrimPrefix(id, "byom:")
	if fname == "" || fname != filepath.Base(fname) || strings.ContainsAny(fname, `/\`) {
		return fmt.Errorf("byom model name %q is invalid", fname)
	}
	info, err := r.ggufInfo(filepath.Join(engineCfg.ByomDir, fname))
	if err != nil {
		return fmt.Errorf("cannot read %q to confirm the remedy applies: %v", fname, err)
	}
	if remedy == "cpu-moe" && info.ExpertCount == 0 {
		return fmt.Errorf("%q is not a mixture-of-experts model; --cpu-moe would do nothing", fname)
	}
	return nil
}

// decorateByomVariants attaches size and fit to synthesized entries. The
// free-VRAM basis is the best GPU's headline free memory plus the running
// engine's current model file size — its VRAM frees the moment the swap
// stops it. Per-process attribution would be more precise but is
// unreliable under Windows WDDM, so the file size stands in.
func (r *router) decorateByomVariants(ctx context.Context, name string, variants []lifecycle.VariantInfo) []variantView {
	views := make([]variantView, len(variants))
	for i, v := range variants {
		views[i] = variantView{VariantInfo: v}
	}
	gpus, gpuErr := r.gpuSnapshot(ctx)
	freeMiB, gpuName := 0, ""
	for _, gpu := range gpus {
		if free := gpu.TotalMiB - gpu.UsedMiB; free > freeMiB {
			freeMiB, gpuName = free, gpu.Name
		}
	}
	if gpuErr == nil {
		health := r.manager.Health().Engines[name]
		if health.PID != 0 {
			activePath := ""
			for _, v := range variants {
				if v.Active {
					activePath = v.ModelPath
					break
				}
			}
			if activePath == "" {
				engineCfg, _ := r.engine(name)
				activePath = modelArgPath(engineCfg.Variants[health.Variant].Args)
			}
			if activePath != "" {
				if stat, err := os.Stat(activePath); err == nil {
					freeMiB += mib(stat.Size())
				}
			}
		}
	}
	for i := range views {
		path := views[i].ModelPath
		if path == "" {
			continue
		}
		stat, err := os.Stat(path)
		if err != nil {
			continue
		}
		views[i].Bytes = stat.Size()
		fit := &fitInfo{Remedies: []fitRemedy{}}
		info, infoErr := r.ggufInfo(path)
		label := info.SizeLabel
		if label == "" && infoErr == nil {
			label = info.Architecture
		}
		if label != "" {
			label += " · "
		}
		if gpuErr != nil {
			fit.Verdict = "no_gpu_info"
			fit.Detail = fmt.Sprintf("%s%s weights; nvidia-smi is unavailable, so fit cannot be judged", label, gib(stat.Size()))
		} else {
			fit.Verdict = fitVerdict(mib(stat.Size()), freeMiB)
			fit.Detail = fmt.Sprintf("%sneeds ~%s (%s weights + ~%s KV/compute at this context); ~%s free on %s",
				label, gib(stat.Size()+int64(fitHeadroomMiB)<<20), gib(stat.Size()),
				gib(int64(fitHeadroomMiB)<<20), gib(int64(freeMiB)<<20), gpuName)
			if fit.Verdict == "too_big" && infoErr == nil && info.ExpertCount > 0 {
				fit.Remedies = append(fit.Remedies, fitRemedy{ID: "cpu-moe", Label: cpuMoeLabel})
			}
		}
		views[i].Fit = fit
	}
	return views
}

// handleJobs lists every tracked async job, newest first.
func (r *router) handleJobs(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodGet) {
		return
	}
	list := r.jobs.List()
	if list == nil {
		list = []jobs.Job{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jobs": list})
}

// handleJob serves GET /v1/jobs/{id} and POST /v1/jobs/{id}/cancel.
func (r *router) handleJob(w http.ResponseWriter, req *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(req.URL.Path, "/v1/jobs/"), "/")
	parts := strings.Split(rest, "/")
	switch {
	case len(parts) == 1 && parts[0] != "":
		if !requireMethod(w, req, http.MethodGet) {
			return
		}
		job, ok := r.jobs.Get(parts[0])
		if !ok {
			writeJSONError(w, http.StatusNotFound, "job not found")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(job)
	case len(parts) == 2 && parts[1] == "cancel":
		if !requireMethod(w, req, http.MethodPost) {
			return
		}
		job, err := r.jobs.Cancel(parts[0])
		if err != nil {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(job)
	default:
		http.NotFound(w, req)
	}
}

// maxLibraryUploadBytes bounds the JSON body of a library save: the base64
// payload (~1.34x the raw artifact cap) plus metadata headroom.
const maxLibraryUploadBytes = 96 * 1024 * 1024

// handleLibrary serves GET /v1/library (list) and POST /v1/library (save).
func (r *router) handleLibrary(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		items, err := r.library.List()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if items == nil {
			items = []library.Item{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
	case http.MethodPost:
		var body struct {
			Kind    string            `json:"kind"`
			Name    string            `json:"name"`
			DataB64 string            `json:"data_b64"`
			Meta    map[string]string `json:"meta"`
		}
		req.Body = http.MaxBytesReader(w, req.Body, maxLibraryUploadBytes)
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON request: %v", err))
			return
		}
		data, err := base64.StdEncoding.DecodeString(body.DataB64)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("decode data_b64: %v", err))
			return
		}
		item, err := r.library.Save(body.Kind, body.Name, data, body.Meta)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(item)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleLibraryItem serves GET /v1/library/{id}/artifact and DELETE /v1/library/{id}.
func (r *router) handleLibraryItem(w http.ResponseWriter, req *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(req.URL.Path, "/v1/library/"), "/")
	parts := strings.Split(rest, "/")
	switch {
	case len(parts) == 2 && parts[1] == "artifact":
		if !requireMethod(w, req, http.MethodGet) {
			return
		}
		path, _, err := r.library.ArtifactPath(parts[0])
		if err != nil {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		http.ServeFile(w, req, path)
	case len(parts) == 1 && parts[0] != "":
		if !requireMethod(w, req, http.MethodDelete) {
			return
		}
		if err := r.library.Delete(parts[0]); err != nil {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.NotFound(w, req)
	}
}

// handleAudiobooks serves GET /v1/audiobooks (finished narrations) and
// POST /v1/audiobooks (multipart upload: file + optional voice/title), which
// starts a narration job trackable at /v1/jobs/{id}.
func (r *router) handleAudiobooks(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		books, err := r.audiobooks.List()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		finished := make([]audiobook.Manifest, 0, len(books))
		type interruptedAudiobook struct {
			audiobook.Manifest
			ResumeAvailable bool   `json:"resumeAvailable"`
			ResumeConflict  string `json:"resumeConflict,omitempty"`
		}
		interrupted := make([]interruptedAudiobook, 0)
		for _, book := range books {
			if book.Status == audiobook.ProductionStatusInterrupted {
				item := interruptedAudiobook{Manifest: book, ResumeAvailable: true}
				if err := r.audiobooks.CanResume(req.Context(), book.ID); err != nil {
					item.ResumeAvailable = false
					item.ResumeConflict = err.Error()
				}
				interrupted = append(interrupted, item)
			} else {
				finished = append(finished, book)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"audiobooks": finished, "interrupted": interrupted})
	case http.MethodPost:
		req.Body = http.MaxBytesReader(w, req.Body, audiobook.MaxDocumentBytes+1024*1024)
		file, header, err := req.FormFile("file")
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "multipart field file is required")
			return
		}
		defer file.Close()
		if header == nil || header.Filename == "" {
			writeJSONError(w, http.StatusBadRequest, "uploaded file must include a filename")
			return
		}
		data, err := io.ReadAll(io.LimitReader(file, audiobook.MaxDocumentBytes+1))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("read uploaded file: %v", err))
			return
		}
		title := strings.TrimSpace(req.FormValue("title"))
		if title == "" {
			title = strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
		}
		voiceID := strings.TrimSpace(req.FormValue("voice"))
		promptSpec, err := decodeDramaBoxPromptSpec(req.FormValue("promptSpec"))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		bookRequest, err := audiobook.NormalizeRequest(audiobook.Request{
			Title:                title,
			VoiceID:              voiceID,
			EngineID:             req.FormValue("engine"),
			Direction:            req.FormValue("direction"),
			PromptSpec:           promptSpec,
			AcceptPromptWarnings: strings.EqualFold(req.FormValue("acceptPromptWarnings"), "true"),
			OptionsJSON:          req.FormValue("options"),
			Verification:         audiobook.VerificationMode(strings.TrimSpace(req.FormValue("verification"))),
		})
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		id, chunks, err := r.audiobooks.Create(req.Context(), audiobook.DocumentRequest{
			Filename: header.Filename, Data: data, Request: bookRequest,
		})
		if err != nil {
			var engineErr *engine.Error
			if errors.As(err, &engineErr) {
				writeEngineError(w, err)
				return
			}
			writeAudiobookError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		response := map[string]any{
			"id":        id,
			"chunks":    chunks,
			"statusUrl": "/v1/jobs/" + id,
		}
		if bookRequest.EngineID == audiobook.DramaBoxEngineID {
			response["sections"] = chunks
		}
		_ = json.NewEncoder(w).Encode(response)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func decodeDramaBoxPromptSpec(raw string) (audiobook.DramaBoxPromptSpec, error) {
	if strings.TrimSpace(raw) == "" {
		return audiobook.DramaBoxPromptSpec{}, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var spec audiobook.DramaBoxPromptSpec
	if err := decoder.Decode(&spec); err != nil {
		return audiobook.DramaBoxPromptSpec{}, fmt.Errorf("invalid promptSpec: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return audiobook.DramaBoxPromptSpec{}, fmt.Errorf("invalid promptSpec: expected one JSON object")
	}
	return spec, nil
}

type audiobookPreviewRequest struct {
	EngineID             string                       `json:"engine"`
	VoiceID              string                       `json:"voice"`
	Direction            string                       `json:"direction"`
	PromptSpec           audiobook.DramaBoxPromptSpec `json:"promptSpec"`
	SourceText           string                       `json:"sourceText"`
	AcceptPromptWarnings bool                         `json:"acceptPromptWarnings"`
	Options              json.RawMessage              `json:"options"`
	Verification         audiobook.VerificationMode   `json:"verification"`
}

// handleAudiobookPreview resolves the complete effective request without
// planning seeds, creating durable state, reserving an engine, or invoking it.
func (r *router) handleAudiobookPreview(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodPost) {
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxJSONBodyBytes)
	var body audiobookPreviewRequest
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid audiobook preview request: "+err.Error())
		return
	}
	optionsJSON := ""
	if len(body.Options) > 0 {
		optionsJSON = string(body.Options)
	}
	resolved, err := r.audiobooks.Preview(req.Context(), audiobook.Request{
		EngineID:             body.EngineID,
		VoiceID:              strings.TrimSpace(body.VoiceID),
		Direction:            body.Direction,
		PromptSpec:           body.PromptSpec,
		Text:                 body.SourceText,
		AcceptPromptWarnings: body.AcceptPromptWarnings,
		OptionsJSON:          optionsJSON,
		Verification:         body.Verification,
	})
	if err != nil {
		writeAudiobookPreviewError(w, err)
		return
	}
	writeAudiobookPreview(w, resolved)
}

// handleAudiobookDocumentPreview extracts the complete uploaded document and
// applies the production section policy, so prompt lint and exact section
// previews cannot miss text beyond a browser-side sample.
func (r *router) handleAudiobookDocumentPreview(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodPost) {
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, audiobook.MaxDocumentBytes+1024*1024)
	file, header, err := req.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "multipart field file is required")
		return
	}
	defer file.Close()
	if header == nil || header.Filename == "" {
		writeJSONError(w, http.StatusBadRequest, "uploaded file must include a filename")
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, audiobook.MaxDocumentBytes+1))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("read uploaded file: %v", err))
		return
	}
	promptSpec, err := decodeDramaBoxPromptSpec(req.FormValue("promptSpec"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	resolved, err := r.audiobooks.PreviewDocument(req.Context(), audiobook.DocumentRequest{
		Filename: header.Filename,
		Data:     data,
		Request: audiobook.Request{
			VoiceID:              strings.TrimSpace(req.FormValue("voice")),
			EngineID:             req.FormValue("engine"),
			Direction:            req.FormValue("direction"),
			PromptSpec:           promptSpec,
			AcceptPromptWarnings: strings.EqualFold(req.FormValue("acceptPromptWarnings"), "true"),
			OptionsJSON:          req.FormValue("options"),
			Verification:         audiobook.VerificationMode(strings.TrimSpace(req.FormValue("verification"))),
		},
	})
	if err != nil {
		writeAudiobookPreviewError(w, err)
		return
	}
	writeAudiobookPreview(w, resolved)
}

func writeAudiobookPreviewError(w http.ResponseWriter, err error) {
	var engineErr *engine.Error
	if errors.As(err, &engineErr) {
		writeEngineError(w, err)
	} else if audiobook.IsRequestError(err) {
		writeJSONError(w, http.StatusBadRequest, err.Error())
	} else {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeAudiobookPreview(w http.ResponseWriter, resolved audiobook.ResolvedRequest) {

	var previewOptions any = map[string]any{}
	var mapping any = map[string]any{}
	if resolved.Request.EngineID == audiobook.DramaBoxEngineID {
		previewOptions = resolved.Request.Options
		mapping = engine.DescribeSynthesisMapping(resolved.Engine.Mode)
	}
	promptEvaluation := audiobook.PromptEvaluation{Warnings: []audiobook.PromptWarning{}, Normalizations: []audiobook.PromptNormalization{}}
	promptSections := []audiobook.PromptSectionPreview{}
	if resolved.Request.EngineID == audiobook.DramaBoxEngineID {
		var err error
		promptEvaluation, err = audiobook.EvaluateDramaBoxPrompt(resolved.Request.PromptSpec, resolved.Request.Text)
		if err != nil {
			writeAudiobookPreviewError(w, err)
			return
		}
		promptSections, err = audiobook.PreviewDramaBoxPromptSections(resolved.Request.PromptSpec, resolved.Request.Text)
		if err != nil {
			writeAudiobookPreviewError(w, err)
			return
		}
		if len(promptSections) != 1 {
			// The section list is the byte-for-byte production request. Avoid
			// presenting a whole-document prompt that production never sends.
			promptEvaluation.GeneratedPrompt = ""
		}
	}
	response := map[string]any{
		"engine":                   resolved.Engine.ID,
		"engineFingerprint":        resolved.Engine.Fingerprint,
		"model":                    resolved.Engine.ModelID,
		"voice":                    resolved.Voice.ID,
		"voiceFingerprint":         resolved.Voice.Fingerprint,
		"voiceReferenceSha256":     resolved.Voice.ReferenceSHA256,
		"voiceUsableSpeechSeconds": resolved.Voice.UsableSpeechSeconds,
		"voiceFitnessMethod":       resolved.Voice.FitnessMethod,
		"voiceFitnessWarnings":     resolved.Voice.FitnessWarnings,
		"direction":                resolved.Request.Direction,
		"prompt":                   promptEvaluation,
		"promptSections":           promptSections,
		"promptPolicyVersion":      audiobook.CurrentPromptPolicyVersion,
		"speakerPhrases":           audiobook.SpeakerPhrases(),
		"deliveryPresets":          audiobook.DeliveryPresets(),
		"options":                  previewOptions,
		"seedPolicy":               "one server-assigned positive 31-bit seed per section (DramaBox release-0.5 int-parser compatibility)",
		"verification":             resolved.Request.Verification,
		"transport": map[string]any{
			"mode":    resolved.Engine.Mode,
			"mapping": mapping,
		},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(response)
}

func (r *router) handleAudiobookBenchmark(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodPost) {
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxJSONBodyBytes)
	var body audiobook.BenchmarkRequest
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid DramaBox benchmark request: "+err.Error())
		return
	}
	id, err := r.audiobooks.StartBenchmark(req.Context(), body)
	if err != nil {
		writeAudiobookError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": id, "statusUrl": "/v1/jobs/" + id,
		"resultUrl": "/v1/audiobooks/benchmark/results/" + id,
	})
}

func (r *router) handleAudiobookBenchmarkResult(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodGet) {
		return
	}
	id := strings.Trim(strings.TrimPrefix(req.URL.Path, "/v1/audiobooks/benchmark/results/"), "/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, req)
		return
	}
	result, err := r.audiobooks.BenchmarkResult(req.Context(), id)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func (r *router) handleAudiobookBenchmarkResults(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodGet) {
		return
	}
	results, err := r.audiobooks.ListBenchmarkResults(req.Context())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
}

func writeAudiobookError(w http.ResponseWriter, err error) {
	var engineErr *engine.Error
	if errors.As(err, &engineErr) {
		writeEngineError(w, err)
		return
	}
	status := http.StatusInternalServerError
	switch {
	case audiobook.IsRequestError(err):
		status = http.StatusBadRequest
	case errors.Is(err, audiobook.ErrProductionNotFound):
		status = http.StatusNotFound
	case errors.Is(err, audiobook.ErrVerificationUnavailable):
		status = http.StatusServiceUnavailable
	case errors.Is(err, audiobook.ErrProductionNotInterrupted),
		errors.Is(err, audiobook.ErrSynthesisIdentityChanged),
		errors.Is(err, audiobook.ErrProductionActive),
		errors.Is(err, audiobook.ErrStoreCorrupt),
		strings.Contains(err.Error(), "already narrating"), strings.Contains(err.Error(), "is busy"):
		status = http.StatusConflict
	}
	writeJSONError(w, status, err.Error())
}

// handleAudiobook serves durable production state and lifecycle actions.
func (r *router) handleAudiobook(w http.ResponseWriter, req *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(req.URL.Path, "/v1/audiobooks/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) == 1 && parts[0] != "" {
		switch req.Method {
		case http.MethodGet:
			manifest, ok, err := r.audiobooks.Status(parts[0])
			if err != nil {
				writeAudiobookError(w, err)
				return
			}
			if !ok {
				writeJSONError(w, http.StatusNotFound, "audiobook production not found")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(manifest)
		case http.MethodDelete:
			if err := r.audiobooks.Delete(parts[0]); err != nil {
				writeAudiobookError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Allow", "GET, DELETE")
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "resume" {
		if !requireMethod(w, req, http.MethodPost) {
			return
		}
		sections, err := r.audiobooks.Resume(req.Context(), parts[0])
		if err != nil {
			writeAudiobookError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": parts[0], "chunks": sections, "sections": sections, "statusUrl": "/v1/jobs/" + parts[0]})
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "restart" {
		if !requireMethod(w, req, http.MethodPost) {
			return
		}
		id, sections, err := r.audiobooks.Restart(req.Context(), parts[0])
		if err != nil {
			writeAudiobookError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "chunks": sections, "sections": sections, "statusUrl": "/v1/jobs/" + id})
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "discard" {
		if !requireMethod(w, req, http.MethodPost) {
			return
		}
		if err := r.audiobooks.Discard(parts[0]); err != nil {
			writeAudiobookError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": parts[0], "discarded": true})
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "verification" {
		if !requireMethod(w, req, http.MethodGet) {
			return
		}
		path, err := r.audiobooks.VerificationPath(parts[0])
		if err != nil {
			writeAudiobookError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		http.ServeFile(w, req, path)
		return
	}
	if len(parts) == 4 && parts[0] != "" && parts[1] == "sections" && parts[2] != "" && parts[3] == "retry" {
		if !requireMethod(w, req, http.MethodPost) {
			return
		}
		req.Body = http.MaxBytesReader(w, req.Body, maxJSONBodyBytes)
		var body struct {
			Mode audiobook.RetryMode `json:"mode"`
		}
		decoder := json.NewDecoder(req.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid audiobook retry request: "+err.Error())
			return
		}
		jobID, err := r.audiobooks.RetrySection(req.Context(), parts[0], parts[2], body.Mode)
		if err != nil {
			writeAudiobookError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": jobID, "statusUrl": "/v1/jobs/" + jobID})
		return
	}
	if len(parts) == 6 && parts[0] != "" && parts[1] == "sections" && parts[2] != "" && parts[3] == "attempts" && parts[4] != "" && parts[5] == "select" {
		if !requireMethod(w, req, http.MethodPost) {
			return
		}
		jobID, err := r.audiobooks.SelectAttempt(req.Context(), parts[0], parts[2], parts[4])
		if err != nil {
			writeAudiobookError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": jobID, "statusUrl": "/v1/jobs/" + jobID})
		return
	}
	if len(parts) == 6 && parts[0] != "" && parts[1] == "sections" && parts[2] != "" && parts[3] == "attempts" && parts[4] != "" && parts[5] == "audio" {
		if !requireMethod(w, req, http.MethodGet) {
			return
		}
		path, err := r.audiobooks.AttemptPath(parts[0], parts[2], parts[4])
		if err != nil {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		http.ServeFile(w, req, path)
		return
	}
	if len(parts) != 3 || parts[0] == "" || parts[1] != "artifact" {
		http.NotFound(w, req)
		return
	}
	if !requireMethod(w, req, http.MethodGet) {
		return
	}
	path, err := r.audiobooks.ArtifactPath(parts[0], parts[2])
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	http.ServeFile(w, req, path)
}

func (r *router) handleChatCompletions(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodPost) {
		return
	}

	engineCfg, ok := r.engine("llama")
	if !ok {
		writeJSONError(w, http.StatusServiceUnavailable, `engine "llama" is not configured`)
		return
	}
	upstreamURL, ok := inferChatCompletionsURL(engineCfg.HealthURL)
	if !ok {
		writeJSONError(w, http.StatusServiceUnavailable, `engine "llama" healthUrl must end in /health to infer /v1/chat/completions`)
		return
	}

	ctx, cancel := context.WithTimeout(req.Context(), engine.RequestTimeout(engineCfg, defaultChatTimeout))
	defer cancel()

	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, req.Body)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	upstreamReq.ContentLength = req.ContentLength
	upstreamReq.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(upstreamReq)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, fmt.Sprintf("llama upstream request failed: %v", err))
		return
	}
	defer resp.Body.Close()

	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		r.manager.MarkSuccess("llama")
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (r *router) handleSpeech(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodPost) {
		return
	}

	var body speechRequest
	req.Body = http.MaxBytesReader(w, req.Body, maxJSONBodyBytes)
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON request: %v", err))
		return
	}
	if strings.TrimSpace(body.Input) == "" {
		writeJSONError(w, http.StatusBadRequest, "input is required")
		return
	}
	if body.Format != "" && body.Format != "wav" {
		writeJSONError(w, http.StatusBadRequest, "only wav format is supported")
		return
	}
	clonedVoice, err := r.resolveVoice(body.Voice)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	audio, err := r.speak(req.Context(), body.Input, clonedVoice, false)
	if err != nil {
		writeEngineError(w, err)
		return
	}

	w.Header().Set("Content-Type", "audio/wav")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(padSpeech(audio))
}

// audioServerModelID is the model id a server-mode audio engine must expose
// for speech (see docs/CONFIG.md).
const audioServerModelID = "tts"

// speak synthesizes text with the selected cloned voice (nil = default)
// through the audio engine: the resident audiocpp_server when audio is
// mode "server" (model stays loaded between requests), the subprocess CLI
// otherwise. reserved skips taking the engine's slot for callers that
// already hold it (story jobs reserve audio for the whole run).
func (r *router) speak(ctx context.Context, text string, clonedVoice *engine.Voice, reserved bool) ([]byte, error) {
	return r.speakWithEngine(ctx, engine.DefaultSpeechEngineID, text, clonedVoice, reserved)
}

// speakWithEngine synthesizes through one configured speech backend. The
// normal audio server retains its required default voice reference, while
// DramaBox supports both text-only requests and explicit stored clones.
func (r *router) speakWithEngine(ctx context.Context, engineName string, text string, clonedVoice *engine.Voice, reserved bool) ([]byte, error) {
	return r.speakSynthesis(ctx, engine.SynthesisRequest{Text: text, EngineID: engineName}, clonedVoice, reserved)
}

func (r *router) speakSynthesis(ctx context.Context, request engine.SynthesisRequest, clonedVoice *engine.Voice, reserved bool) ([]byte, error) {
	engineName := request.EngineID
	engineCfg, ok := r.engine(engineName)
	if !ok {
		return nil, &engine.Error{Kind: engine.KindNotConfigured, Message: fmt.Sprintf("engine %q is not configured", engineName)}
	}
	if engineCfg.Mode != "server" {
		spec := engine.SpeechVoiceSpecForRequest(request, clonedVoice)
		var res engine.Result
		var err error
		if reserved {
			res, err = r.engines.RunReserved(ctx, spec)
		} else {
			res, err = r.engines.Run(ctx, spec)
		}
		if err != nil {
			return nil, err
		}
		return res.Output, nil
	}

	if !reserved {
		release, ok := r.engines.Reserve(engineName)
		if !ok {
			return nil, &engine.Error{Kind: engine.KindBusy, Message: fmt.Sprintf("engine %q is busy", engineName)}
		}
		defer release()
	}
	upstreamURL, ok := inferEngineURL(engineCfg.HealthURL, "/v1/audio/speech")
	if !ok {
		return nil, &engine.Error{Kind: engine.KindNotConfigured, Message: fmt.Sprintf("engine %q healthUrl must end in /health to infer /v1/audio/speech", engineName)}
	}
	refPath := engineCfg.DefaultVoiceRef
	if clonedVoice != nil {
		refPath = clonedVoice.RefWAVPath
	}
	if refPath == "" && !engine.SpeechEngineAllowsTextOnly(engineName) {
		return nil, &engine.Error{Kind: engine.KindNotConfigured, Message: fmt.Sprintf("server-mode engine %q needs defaultVoiceRef configured", engineName)}
	}
	var defaultVoice *engine.Voice
	if engineCfg.DefaultVoiceRef != "" || engineCfg.DefaultVoiceText != "" {
		defaultVoice = &engine.Voice{RefWAVPath: engineCfg.DefaultVoiceRef, RefText: engineCfg.DefaultVoiceText}
	}
	payload, err := engine.MarshalSpeechServerRequest(audioServerModelID, request, clonedVoice, defaultVoice)
	if err != nil {
		return nil, &engine.Error{Kind: engine.KindInternal, Message: fmt.Sprintf("encode speech request: %v", err)}
	}

	ctx, cancel := context.WithTimeout(ctx, engine.RequestTimeout(engineCfg, engine.DefaultSpeechTimeout))
	defer cancel()
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(payload))
	if err != nil {
		return nil, &engine.Error{Kind: engine.KindInternal, Message: err.Error()}
	}
	upstreamReq.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(upstreamReq)
	if err != nil {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("%s upstream request failed: %v", engineName, err)}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, engine.MaxSpeechOutputBytes+1))
	if err != nil {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("read %s upstream response: %v", engineName, err)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("%s upstream returned status %d: %s", engineName, resp.StatusCode, strings.TrimSpace(string(data)))}
	}
	if int64(len(data)) > engine.MaxSpeechOutputBytes {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("%s upstream produced an oversized WAV", engineName)}
	}
	if err := wav.ValidateBytes(data); err != nil {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("%s upstream produced an invalid WAV: %v", engineName, err)}
	}
	r.manager.MarkSuccess(engineName)
	return data, nil
}

type audioServerSpeechRequest struct {
	Model             string         `json:"model"`
	Input             string         `json:"input"`
	VoiceRef          string         `json:"voice_ref,omitempty"`
	ReferenceText     string         `json:"reference_text,omitempty"`
	Seed              engine.Seed    `json:"seed,omitempty"`
	NumInferenceSteps int            `json:"num_inference_steps,omitempty"`
	GuidanceScale     float64        `json:"guidance_scale,omitempty"`
	Options           map[string]any `json:"options,omitempty"`
}

const (
	// speechLeadPad / speechTrailPad wrap every spoken reply in silence:
	// playback devices swallow the first fraction of a second of a clip
	// (Bluetooth wake-up, autoplay ramp-in), which listeners hear as
	// clipped speech.
	speechLeadPad  = 250 * time.Millisecond
	speechTrailPad = 250 * time.Millisecond
)

// padSpeech is best-effort: audio the wav package cannot decode is returned
// unchanged rather than failing the request.
func padSpeech(audio []byte) []byte {
	padded, err := wav.PadSilence(audio, speechLeadPad, speechTrailPad)
	if err != nil {
		return audio
	}
	return padded
}

func (r *router) handleImageGenerations(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodPost) {
		return
	}

	var body imageGenerationRequest
	req.Body = http.MaxBytesReader(w, req.Body, maxJSONBodyBytes)
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON request: %v", err))
		return
	}
	if strings.TrimSpace(body.Prompt) == "" {
		writeJSONError(w, http.StatusBadRequest, "prompt is required")
		return
	}
	if body.ResponseFormat != "" && body.ResponseFormat != "b64_json" {
		writeJSONError(w, http.StatusBadRequest, "only b64_json response_format is supported")
		return
	}
	if body.N != nil && *body.N != 1 {
		writeJSONError(w, http.StatusBadRequest, "only n=1 is supported")
		return
	}
	width, height, err := parseImageSize(body.Size)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Resolve the seed here, once: absent or negative rolls a fresh one.
	// Downstream everything works with a concrete value, and the response
	// reports it, so every image is reproducible by construction.
	seed := rollImageSeed()
	if body.Seed != nil && *body.Seed >= 0 {
		seed = *body.Seed
	}

	png, err := r.generateImage(req.Context(), body.Prompt, width, height, seed)
	if err != nil {
		writeEngineError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(imageGenerationResponse{
		Created: time.Now().Unix(),
		Data: []imageGenerationData{
			{B64JSON: base64.StdEncoding.EncodeToString(png)},
		},
		Seed: seed,
	})
}

// rollImageSeed picks a fresh positive seed. Zero is avoided so a rolled
// seed can never look like "unset" to anything downstream.
func rollImageSeed() int64 {
	return mrand.Int64N(math.MaxInt32-1) + 1
}

// generateImage produces a PNG for the prompt. Subprocess sd crosses the
// engine seam (sd-cli reloads the model each run); server-mode sd posts to
// the resident sd-server's native async route, POST /sdcpp/v1/img_gen, and
// polls the job it returns. The native route is used rather than the
// OpenAI-compatible one because it accepts a seed — the whole point — and
// the same purpose-built-body discipline applies (the OpenAI route once
// crashed on a stray "n":null; only send fields the server defines).
func (r *router) generateImage(ctx context.Context, prompt string, width, height int, seed int64) ([]byte, error) {
	engineCfg, ok := r.engine("sd")
	if !ok {
		return nil, &engine.Error{Kind: engine.KindNotConfigured, Message: `engine "sd" is not configured`}
	}
	if engineCfg.Mode != "server" {
		res, err := r.engines.Run(ctx, engine.ImageSpec(prompt, width, height, seed))
		if err != nil {
			return nil, err
		}
		return res.Output, nil
	}

	upstreamURL, ok := inferSDURL(engineCfg.HealthURL, "/sdcpp/v1/img_gen")
	if !ok {
		return nil, &engine.Error{Kind: engine.KindNotConfigured, Message: `engine "sd" healthUrl must be an absolute http(s) URL to infer /sdcpp/v1/img_gen`}
	}

	upstreamBody := struct {
		Prompt       string `json:"prompt"`
		Width        int    `json:"width,omitempty"`
		Height       int    `json:"height,omitempty"`
		Seed         int64  `json:"seed"`
		OutputFormat string `json:"output_format"`
	}{Prompt: prompt, Seed: seed, OutputFormat: "png"}
	if width > 0 && height > 0 {
		upstreamBody.Width = width
		upstreamBody.Height = height
	}
	payload, err := json.Marshal(upstreamBody)
	if err != nil {
		return nil, &engine.Error{Kind: engine.KindInternal, Message: fmt.Sprintf("encode image request: %v", err)}
	}

	ctx, cancel := context.WithTimeout(ctx, engine.RequestTimeout(engineCfg, engine.DefaultImageTimeout))
	defer cancel()
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(payload))
	if err != nil {
		return nil, &engine.Error{Kind: engine.KindInternal, Message: err.Error()}
	}
	upstreamReq.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(upstreamReq)
	if err != nil {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("sd upstream request failed: %v", err)}
	}
	submitted, err := readBoundedJSON(resp, maxImageUpstreamBytes)
	if err != nil {
		return nil, err
	}
	var job struct {
		PollURL string `json:"poll_url"`
	}
	if err := json.Unmarshal(submitted, &job); err != nil || job.PollURL == "" {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("sd upstream returned no job to poll: %s", strings.TrimSpace(string(submitted)))}
	}
	pollURL, ok := inferSDURL(engineCfg.HealthURL, job.PollURL)
	if !ok {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: "sd upstream returned an unusable poll url"}
	}

	// Poll until the job lands somewhere terminal; the request context
	// bounds the whole wait, so a wedged job becomes a timeout rather than
	// a stuck request.
	for {
		select {
		case <-ctx.Done():
			return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: "sd generation timed out"}
		case <-time.After(250 * time.Millisecond):
		}
		pollReq, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
		if err != nil {
			return nil, &engine.Error{Kind: engine.KindInternal, Message: err.Error()}
		}
		pollResp, err := r.client.Do(pollReq)
		if err != nil {
			return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("sd job poll failed: %v", err)}
		}
		pollBody, err := readBoundedJSON(pollResp, maxImageUpstreamBytes)
		if err != nil {
			return nil, err
		}
		var status struct {
			Status string `json:"status"`
			Result *struct {
				Images []struct {
					B64JSON string `json:"b64_json"`
				} `json:"images"`
			} `json:"result"`
			Error *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(pollBody, &status); err != nil {
			return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("decode sd job status: %v", err)}
		}
		switch status.Status {
		case "queued", "generating":
			continue
		case "completed":
			if status.Result == nil || len(status.Result.Images) == 0 || status.Result.Images[0].B64JSON == "" {
				return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: "sd job completed with no image data"}
			}
			pngBytes, err := base64.StdEncoding.DecodeString(status.Result.Images[0].B64JSON)
			if err != nil {
				return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("decode sd upstream image: %v", err)}
			}
			if err := engine.ValidatePNGBytes(pngBytes); err != nil {
				return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("sd upstream produced invalid PNG: %v", err)}
			}
			r.manager.MarkSuccess("sd")
			return pngBytes, nil
		default:
			message := "sd job " + status.Status
			if status.Error != nil && status.Error.Message != "" {
				message += ": " + status.Error.Message
			}
			return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: message}
		}
	}
}

// readBoundedJSON drains a bounded upstream response and hands back its
// bytes, treating non-2xx statuses as engine failures with the body as the
// explanation.
func readBoundedJSON(resp *http.Response, limit int64) ([]byte, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("read sd upstream response: %v", err)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("sd upstream returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
	}
	return body, nil
}

const (
	// maxImageDescriptionBodyBytes bounds the JSON body carrying a base64
	// PNG for description.
	maxImageDescriptionBodyBytes = 24 * 1024 * 1024
	// maxImageUpstreamBytes bounds the sd-server response we read: a base64
	// PNG (~1.34x the raw cap) wrapped in JSON, so it must exceed
	// engine.MaxImageOutputBytes with room for the encoding overhead.
	maxImageUpstreamBytes = 64 * 1024 * 1024
	// maxDescribeImageDimension caps described images. Unlike generation,
	// description accepts photos and screenshots, so the cap is looser than
	// the SD limit; the browser additionally downscales before uploading.
	maxDescribeImageDimension = 4096
)

// visionInstruction is the only text the vision model sees alongside the
// image. Descriptions must come from the pixels: the generation prompt is
// deliberately never part of this request, so the model cannot crib from it.
const visionInstruction = "Describe what you see in this image in two or three short sentences of plain conversational text, with no markdown or lists."

// handleImageDescriptions runs true vision-to-speech: the uploaded PNG goes
// to the vision engine with a generic instruction, and the resulting
// description is spoken with the requested cloned voice (or the default).
func (r *router) handleImageDescriptions(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodPost) {
		return
	}

	var body imageDescriptionRequest
	req.Body = http.MaxBytesReader(w, req.Body, maxImageDescriptionBodyBytes)
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON request: %v", err))
		return
	}
	if strings.TrimSpace(body.ImageB64) == "" {
		writeJSONError(w, http.StatusBadRequest, "image_b64 is required")
		return
	}
	imageBytes, err := base64.StdEncoding.DecodeString(body.ImageB64)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "image_b64 must be base64 PNG data")
		return
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(imageBytes))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "image_b64 must decode to a PNG image")
		return
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxDescribeImageDimension || cfg.Height > maxDescribeImageDimension {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("image must be at most %dx%d pixels", maxDescribeImageDimension, maxDescribeImageDimension))
		return
	}
	clonedVoice, err := r.resolveVoice(body.Voice)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	description, err := r.describeImage(req.Context(), imageBytes)
	if err != nil {
		writeEngineError(w, err)
		return
	}

	speech, err := r.speak(req.Context(), description, clonedVoice, false)
	if err != nil {
		writeEngineError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(imageDescriptionResponse{
		Description: description,
		AudioFormat: "wav",
		AudioB64:    base64.StdEncoding.EncodeToString(padSpeech(speech)),
	})
}

// describeImage asks the resident vision server what the PNG shows. The
// request carries the image and visionInstruction only.
func (r *router) describeImage(ctx context.Context, imageBytes []byte) (string, error) {
	engineCfg, ok := r.engine("vision")
	if !ok {
		return "", &engine.Error{Kind: engine.KindNotConfigured, Message: `engine "vision" is not configured`}
	}
	upstreamURL, ok := inferChatCompletionsURL(engineCfg.HealthURL)
	if !ok {
		return "", &engine.Error{Kind: engine.KindNotConfigured, Message: `engine "vision" healthUrl must end in /health to infer /v1/chat/completions`}
	}

	payload, err := json.Marshal(visionChatRequest{
		Model: "default",
		Messages: []visionChatMessage{
			{
				Role: "user",
				Content: []visionContentPart{
					{Type: "text", Text: visionInstruction},
					{Type: "image_url", ImageURL: &visionImageURL{URL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes)}},
				},
			},
		},
	})
	if err != nil {
		return "", &engine.Error{Kind: engine.KindInternal, Message: fmt.Sprintf("encode vision request: %v", err)}
	}

	ctx, cancel := context.WithTimeout(ctx, engine.RequestTimeout(engineCfg, defaultChatTimeout))
	defer cancel()
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(payload))
	if err != nil {
		return "", &engine.Error{Kind: engine.KindInternal, Message: err.Error()}
	}
	upstreamReq.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(upstreamReq)
	if err != nil {
		return "", &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("vision upstream request failed: %v", err)}
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxChatReplyBytes))
	if err != nil {
		return "", &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("read vision upstream response: %v", err)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("vision upstream returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))}
	}

	description, err := extractChatReply(respBody)
	if err != nil {
		return "", &engine.Error{Kind: engine.KindEngineFailure, Message: err.Error()}
	}
	if description == "" {
		return "", &engine.Error{Kind: engine.KindEngineFailure, Message: "vision engine returned no description"}
	}
	r.manager.MarkSuccess("vision")
	return description, nil
}

func (r *router) handleTranscriptions(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodPost) {
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxTranscriptionUploadBytes)

	data, ok := readUploadedWAV(w, req)
	if !ok {
		return
	}

	started := time.Now()
	if req.URL.Query().Get("format") == "segments" {
		segments, err := r.transcribeSegments(req.Context(), data)
		if err != nil {
			writeEngineError(w, err)
			return
		}
		var full strings.Builder
		for _, s := range segments {
			if full.Len() > 0 {
				full.WriteByte(' ')
			}
			full.WriteString(s.Text)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"text":        full.String(),
			"duration_ms": time.Since(started).Milliseconds(),
			"segments":    segments,
		})
		return
	}

	text, err := r.transcribe(req.Context(), data)
	if err != nil {
		writeEngineError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(transcriptionResponse{
		Text:       strings.TrimSpace(text),
		DurationMS: time.Since(started).Milliseconds(),
	})
}

// maxDiarizationUploadBytes bounds the diarization WAV: clustering needs the
// whole recording in one piece (chunking would break speaker identity across
// chunks), and 64 MB holds ~35 minutes of 16 kHz mono — beyond the
// Extractor's 30-minute editor cap.
const maxDiarizationUploadBytes = 64 * 1024 * 1024

// speakerLabel maps a cluster index to the console's tag alphabet: A, B, ...
func speakerLabel(cluster int) string {
	if cluster < 0 || cluster >= 26 {
		return fmt.Sprintf("S%d", cluster)
	}
	return string(rune('A' + cluster))
}

// handleDiarization prefers audio.cpp's CUDA Sortformer engine for compatible
// 16 kHz mono recordings. Sherpa remains the fallback for explicit speaker
// counts, incompatible WAVs, and recordings beyond Sortformer's fixed graph.
func (r *router) handleDiarization(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodPost) {
		return
	}
	_, sortformerConfigured := r.engine("diarize")
	_, sherpaConfigured := r.engine("diarize-sherpa")
	if !sortformerConfigured && !sherpaConfigured {
		writeJSONError(w, http.StatusServiceUnavailable, `neither engine "diarize" (Sortformer) nor "diarize-sherpa" is configured; see docs/CONFIG.md`)
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxDiarizationUploadBytes)
	data, ok := readUploadedWAV(w, req)
	if !ok {
		return
	}

	numSpeakers := 0
	if v := req.URL.Query().Get("speakers"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 26 {
			writeJSONError(w, http.StatusBadRequest, "speakers must be an integer between 1 and 26")
			return
		}
		numSpeakers = n
	}
	format, _, err := wav.Decode(data)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid WAV: %v", err))
		return
	}
	duration, err := wav.Duration(data)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid WAV: %v", err))
		return
	}

	sortformerCompatible := engine.CanUseSortformer(format, duration, numSpeakers)
	provider := engine.DiarizationProviderSherpa
	var spec engine.Spec
	if sortformerCompatible && sortformerConfigured {
		provider = engine.DiarizationProviderSortformer
		spec = engine.SortformerDiarizationSpec(data, duration)
	} else {
		if !sherpaConfigured {
			writeJSONError(w, http.StatusServiceUnavailable, `this recording requires engine "diarize-sherpa"; Sortformer accepts 16 kHz mono PCM WAV, at most 120 seconds, without an explicit speaker count`)
			return
		}
		spec = engine.SherpaDiarizationSpec(data, numSpeakers)
	}

	started := time.Now()
	result, err := r.engines.Run(req.Context(), spec)
	if err != nil {
		writeEngineError(w, err)
		return
	}
	spans, err := provider.ParseDiarization(result.Stdout)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	type spanOut struct {
		Start   float64 `json:"start"`
		End     float64 `json:"end"`
		Speaker string  `json:"speaker"`
	}
	out := make([]spanOut, 0, len(spans))
	for _, s := range spans {
		out = append(out, spanOut{Start: s.Start, End: s.End, Speaker: speakerLabel(s.Speaker)})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"duration_ms": time.Since(started).Milliseconds(),
		"provider":    provider,
		"spans":       out,
	})
}

// maxImportRequestBytes bounds the import request itself — a JSON object
// holding one URL, nothing more. The response can be enormous; the request
// cannot.
const maxImportRequestBytes = 8 * 1024

type audioImportRequest struct {
	URL string `json:"url"`
}

// storyExportRequest asks for a delivery encoding of one render revision.
// Revision 0 means the latest, which is what the console always wants.
type storyExportRequest struct {
	Format   string `json:"format"`
	Bitrate  string `json:"bitrate,omitempty"`
	Revision int    `json:"revision,omitempty"`
}

// validateImportURL keeps the importer to what a person can paste in a
// browser. yt-dlp happily accepts local paths, batch files, and anything
// starting with "-" is argv-parsed as a flag, so the scheme check is the
// gate: http(s), with a host, or nothing runs.
func validateImportURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("url is required")
	}
	if len(trimmed) > 2048 {
		return "", fmt.Errorf("url must be at most 2048 characters")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("url is not a valid URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("url must be http or https")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("url must include a host")
	}
	return trimmed, nil
}

// handleAudioImport fetches the audio behind a URL through the optional
// user-supplied "ytdlp" binary and streams it back for the Extractor to
// decode. The bytes are never stored: importing is the front door of the
// Extractor, not a downloader — what the user chooses to keep is saved
// through the existing clip and voice flows, which is also where the consent
// question belongs.
func (r *router) handleAudioImport(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodPost) {
		return
	}
	if _, ok := r.engine("ytdlp"); !ok {
		writeJSONError(w, http.StatusServiceUnavailable, `engine "ytdlp" is not configured; see docs/CONFIG.md for the URL importer setup`)
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxImportRequestBytes)
	var body audioImportRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid import request body")
		return
	}
	sourceURL, err := validateImportURL(body.URL)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	result, err := r.engines.Run(req.Context(), engine.ImportAudioSpec(sourceURL))
	if err != nil {
		writeEngineError(w, err)
		return
	}
	contentType, ok := engine.SniffAudioContentType(result.Output)
	if !ok {
		writeJSONError(w, http.StatusBadGateway, "yt-dlp returned a file the browser cannot decode as audio")
		return
	}

	// The title rides in a header rather than the body so the response stays
	// raw audio the browser can hand straight to decodeAudioData. Percent
	// encoding keeps non-ASCII titles legal in a header value.
	title := firstLine(string(result.Stdout))
	if title == "" {
		title = "Imported audio"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(result.Output)))
	w.Header().Set("X-Import-Title", url.PathEscape(title))
	w.Header().Set("Access-Control-Expose-Headers", "X-Import-Title")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Output)
}

// transcodeAudio is the story manager's TranscodeFunc: it runs the operator's
// ffmpeg over real files, so a half-hour recording is never held in memory.
func (r *router) transcodeAudio(ctx context.Context, inPath string, outPath string, formatID string, bitrate string) error {
	format, ok := engine.LookupAudioFormat(formatID)
	if !ok {
		return story.NewError(story.CodeUnsupportedArtifact, fmt.Sprintf("unsupported export format %q", formatID))
	}
	if err := engine.ValidateBitrate(bitrate); err != nil {
		return story.NewError(story.CodeInvalidRequest, err.Error())
	}
	// A configured ffmpeg is not necessarily an ffmpeg that can make this
	// format. Ask before spending the job, not after.
	available, err := r.audioEncoders(ctx)
	if err != nil {
		return story.NewError(story.CodeExportUnavailable, err.Error())
	}
	if !available[format.Encoder] {
		return story.NewError(story.CodeExportUnavailable, fmt.Sprintf("this ffmpeg build has no %s encoder, so it cannot make %s", format.Encoder, format.Label))
	}
	if _, err := r.engines.Run(ctx, engine.TranscodeSpec(inPath, outPath, format, bitrate)); err != nil {
		return story.NewError(story.CodeStoreFailure, err.Error())
	}
	return nil
}

// handleAudioDecode converts an upload the browser could not decode into a
// WAV it can. This is the Extractor's other front door, and unlike the URL
// importer it has to take the file itself, so the upload streams to a temp
// file rather than being read into memory: the whole point is that these
// are the large and awkward files.
func (r *router) handleAudioDecode(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodPost) {
		return
	}
	if _, ok := r.engine("ffmpeg"); !ok {
		writeJSONError(w, http.StatusServiceUnavailable, `engine "ffmpeg" is not configured; see docs/CONFIG.md for the decode setup`)
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, engine.MaxDecodeUploadBytes)

	upload, header, err := req.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "multipart field file is required")
		return
	}
	defer upload.Close()

	// Keep the original extension: ffmpeg sniffs content, but some formats
	// are far easier to demux when the container is named.
	inPath, cleanupIn, err := spoolUpload(upload, filepath.Ext(header.Filename))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer cleanupIn()

	outFile, err := os.CreateTemp("", "cpp-studio-decoded-*.wav")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("create decode output: %v", err))
		return
	}
	outPath := outFile.Name()
	_ = outFile.Close()
	defer os.Remove(outPath)

	if _, err := r.engines.Run(req.Context(), engine.DecodeAudioSpec(inPath, outPath)); err != nil {
		writeEngineError(w, err)
		return
	}
	decoded, err := os.Open(outPath)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("read decoded audio: %v", err))
		return
	}
	defer decoded.Close()
	info, err := decoded.Stat()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("stat decoded audio: %v", err))
		return
	}

	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.Header().Set("X-Decoded-From", url.PathEscape(header.Filename))
	w.Header().Set("Access-Control-Expose-Headers", "X-Decoded-From")
	w.WriteHeader(http.StatusOK)
	// Streamed rather than buffered: a half-hour decode is well over a
	// hundred megabytes and there is no reason for it to sit in memory.
	_, _ = io.Copy(w, decoded)
}

// handleAudioEncode is the decode route's mirror: a clip the browser
// already holds, returned in a delivery format through the operator's own
// ffmpeg. It is what makes "Save MP3" work anywhere audio exists — voice
// replies, spoken previews, recordings — not just on story render
// revisions, whose exports stay on the story route because they are
// recorded against a revision.
func (r *router) handleAudioEncode(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodPost) {
		return
	}
	if _, ok := r.engine("ffmpeg"); !ok {
		writeJSONError(w, http.StatusServiceUnavailable, `engine "ffmpeg" is not configured; see docs/CONFIG.md for the export setup`)
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, engine.MaxDecodeUploadBytes)

	upload, header, err := req.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "multipart field file is required")
		return
	}
	defer upload.Close()
	format, ok := engine.LookupAudioFormat(strings.TrimSpace(req.FormValue("format")))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "format must be mp3 or opus")
		return
	}
	bitrate := strings.TrimSpace(req.FormValue("bitrate"))
	if bitrate == "" {
		bitrate = format.DefaultBitrate
	}

	inPath, cleanupIn, err := spoolUpload(upload, filepath.Ext(header.Filename))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer cleanupIn()

	outFile, err := os.CreateTemp("", "cpp-studio-encoded-*."+format.ID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("create encode output: %v", err))
		return
	}
	outPath := outFile.Name()
	_ = outFile.Close()
	defer os.Remove(outPath)

	if err := r.transcodeAudio(req.Context(), inPath, outPath, format.ID, bitrate); err != nil {
		var storyErr *story.StoryError
		if errors.As(err, &storyErr) {
			status := http.StatusBadRequest
			if storyErr.Code == story.CodeExportUnavailable {
				status = http.StatusServiceUnavailable
			}
			writeJSONError(w, status, storyErr.Message)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	encoded, err := os.Open(outPath)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("read encoded audio: %v", err))
		return
	}
	defer encoded.Close()
	info, err := encoded.Stat()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("stat encoded audio: %v", err))
		return
	}

	w.Header().Set("Content-Type", story.ArtifactContentType(outPath))
	w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, encoded)
}

// spoolUpload writes a multipart file to disk without holding it in memory,
// returning the path and a cleanup func.
func spoolUpload(upload io.Reader, ext string) (string, func(), error) {
	if ext == "" || len(ext) > 12 {
		ext = ".bin"
	}
	file, err := os.CreateTemp("", "cpp-studio-upload-*"+ext)
	if err != nil {
		return "", func() {}, fmt.Errorf("create upload temp file: %v", err)
	}
	path := file.Name()
	cleanup := func() { os.Remove(path) }
	if _, err := io.Copy(file, upload); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, fmt.Errorf("save upload: %v", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("close upload temp file: %v", err)
	}
	return path, cleanup, nil
}

// measureLoudness is the story manager's MeasureFunc: it hands a clip to
// ffmpeg's loudnorm analysis and reads back a BS.1770 measurement. The clip
// is written to a temp file rather than piped so ffmpeg can seek it, which
// the two-pass analysis needs.
func (r *router) measureLoudness(ctx context.Context, audio []byte) (story.Loudness, error) {
	file, err := os.CreateTemp("", "cpp-studio-loudness-*.wav")
	if err != nil {
		return story.Loudness{}, fmt.Errorf("create temp file to measure: %v", err)
	}
	path := file.Name()
	defer os.Remove(path)
	if _, err := file.Write(audio); err != nil {
		_ = file.Close()
		return story.Loudness{}, fmt.Errorf("write temp file to measure: %v", err)
	}
	if err := file.Close(); err != nil {
		return story.Loudness{}, fmt.Errorf("close temp file to measure: %v", err)
	}

	result, err := r.engines.Run(ctx, engine.LoudnessSpec(path))
	if err != nil {
		return story.Loudness{}, err
	}
	measured, err := engine.ParseLoudness(result.Stderr)
	if err != nil {
		return story.Loudness{}, err
	}
	return story.Loudness{
		IntegratedLUFS: measured.Integrated,
		TruePeakDBTP:   measured.TruePeak,
		RangeLU:        measured.Range,
	}, nil
}

// audioEncoders reports what the configured ffmpeg can encode, probing once
// and caching: the answer cannot change while the binary does not.
func (r *router) audioEncoders(ctx context.Context) (map[string]bool, error) {
	r.encodersMu.Lock()
	defer r.encodersMu.Unlock()
	if r.encoders != nil {
		return r.encoders, nil
	}
	if _, ok := r.engine("ffmpeg"); !ok {
		return nil, fmt.Errorf(`engine "ffmpeg" is not configured; see docs/CONFIG.md for the export setup`)
	}
	result, err := r.engines.Run(ctx, engine.EncodersSpec())
	if err != nil {
		return nil, fmt.Errorf("could not ask ffmpeg what it can encode: %v", err)
	}
	r.encoders = engine.ParseEncoders(result.Stdout)
	return r.encoders, nil
}

// handleAudioFormats reports which delivery formats this machine can
// actually produce, so the console offers only what will work.
func (r *router) handleAudioFormats(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodGet) {
		return
	}
	type formatOut struct {
		engine.AudioFormat
		Available bool `json:"available"`
	}
	available := map[string]bool{}
	if _, ok := r.engine("ffmpeg"); ok {
		if found, err := r.audioEncoders(req.Context()); err == nil {
			available = found
		}
	}
	out := make([]formatOut, 0, len(engine.AudioFormats))
	for _, format := range engine.AudioFormats {
		out = append(out, formatOut{AudioFormat: format, Available: available[format.Encoder]})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"formats": out})
}

// firstLine is yt-dlp's --print output: the title, ahead of whatever progress
// chatter follows it.
func firstLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// transcriptSegment is one timestamped span of speech. Speaker is first-class
// from day one: the Extractor's manual tagging fills it now, and automatic
// diarization (a future config-gated engine) fills it later — same field,
// same UI, less manual labour.
type transcriptSegment struct {
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Text    string  `json:"text"`
	Speaker string  `json:"speaker"`
}

// transcribeSegments asks the resident whisper-server for verbose_json and
// returns clean timestamped segments. Timestamped output needs the resident
// server; subprocess whisper (-nt) deliberately strips timestamps.
func (r *router) transcribeSegments(ctx context.Context, wavBytes []byte) ([]transcriptSegment, error) {
	engineCfg, ok := r.engine("whisper")
	if !ok {
		return nil, &engine.Error{Kind: engine.KindNotConfigured, Message: `engine "whisper" is not configured`}
	}
	if engineCfg.Mode != "server" {
		return nil, &engine.Error{Kind: engine.KindNotConfigured, Message: `segment transcription needs the "whisper" engine in server mode`}
	}
	if err := wav.ValidateBytes(wavBytes); err != nil {
		return nil, &engine.Error{Kind: engine.KindInvalidInput, Message: err.Error()}
	}
	upstreamURL, ok := inferEngineURL(engineCfg.HealthURL, "/inference")
	if !ok {
		return nil, &engine.Error{Kind: engine.KindNotConfigured, Message: `engine "whisper" healthUrl must end in /health to infer /inference`}
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "input.wav")
	if err != nil {
		return nil, &engine.Error{Kind: engine.KindInternal, Message: fmt.Sprintf("encode transcription request: %v", err)}
	}
	if _, err := part.Write(wavBytes); err != nil {
		return nil, &engine.Error{Kind: engine.KindInternal, Message: fmt.Sprintf("encode transcription request: %v", err)}
	}
	if err := writer.WriteField("response_format", "verbose_json"); err != nil {
		return nil, &engine.Error{Kind: engine.KindInternal, Message: fmt.Sprintf("encode transcription request: %v", err)}
	}
	if err := writer.Close(); err != nil {
		return nil, &engine.Error{Kind: engine.KindInternal, Message: fmt.Sprintf("encode transcription request: %v", err)}
	}

	ctx, cancel := context.WithTimeout(ctx, engine.RequestTimeout(engineCfg, engine.DefaultTranscriptionTimeout))
	defer cancel()
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, &body)
	if err != nil {
		return nil, &engine.Error{Kind: engine.KindInternal, Message: err.Error()}
	}
	upstreamReq.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := r.client.Do(upstreamReq)
	if err != nil {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("whisper upstream request failed: %v", err)}
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxChatReplyBytes))
	if err != nil {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("read whisper upstream response: %v", err)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("whisper upstream returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))}
	}

	var parsed struct {
		Segments []struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
			Text  string  `json:"text"`
		} `json:"segments"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("decode whisper upstream response: %v", err)}
	}
	segments := make([]transcriptSegment, 0, len(parsed.Segments))
	for _, s := range parsed.Segments {
		text := strings.TrimSpace(s.Text)
		if text == "" {
			continue
		}
		segments = append(segments, transcriptSegment{Start: s.Start, End: s.End, Text: text})
	}
	r.manager.MarkSuccess("whisper")
	return segments, nil
}

func whisperVADConfigured(cfg config.EngineConfig) bool {
	if cfg.Mode != "server" {
		return false
	}
	args := cfg.Args
	if len(cfg.Variants) > 0 {
		variant, ok := cfg.Variants[cfg.DefaultVariant]
		if !ok {
			return false
		}
		args = variant.Args
	}
	for _, arg := range args {
		if arg == "--vad" {
			return true
		}
	}
	return false
}

func spokenSegmentDuration(segments []transcriptSegment) time.Duration {
	ranges := append([]transcriptSegment(nil), segments...)
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].Start < ranges[j].Start })
	start, end, total := 0.0, 0.0, 0.0
	haveRange := false
	for _, segment := range ranges {
		if segment.End <= segment.Start || segment.Start < 0 {
			continue
		}
		if !haveRange {
			start, end, haveRange = segment.Start, segment.End, true
			continue
		}
		if segment.Start <= end {
			if segment.End > end {
				end = segment.End
			}
			continue
		}
		total += end - start
		start, end = segment.Start, segment.End
	}
	if haveRange {
		total += end - start
	}
	return time.Duration(total * float64(time.Second))
}

// transcribe produces the transcript for uploaded WAV bytes. Subprocess
// whisper crosses the engine seam; server-mode whisper posts to the resident
// whisper-server's /inference route so the model stays loaded between
// requests (which is what makes live transcription passes fast).
func (r *router) transcribe(ctx context.Context, wavBytes []byte) (string, error) {
	engineCfg, ok := r.engine("whisper")
	if !ok {
		return "", &engine.Error{Kind: engine.KindNotConfigured, Message: `engine "whisper" is not configured`}
	}
	if engineCfg.Mode != "server" {
		res, err := r.engines.Run(ctx, engine.TranscriptionSpec(wavBytes))
		if err != nil {
			return "", err
		}
		return string(res.Stdout), nil
	}

	if err := wav.ValidateBytes(wavBytes); err != nil {
		return "", &engine.Error{Kind: engine.KindInvalidInput, Message: err.Error()}
	}
	upstreamURL, ok := inferEngineURL(engineCfg.HealthURL, "/inference")
	if !ok {
		return "", &engine.Error{Kind: engine.KindNotConfigured, Message: `engine "whisper" healthUrl must end in /health to infer /inference`}
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "input.wav")
	if err != nil {
		return "", &engine.Error{Kind: engine.KindInternal, Message: fmt.Sprintf("encode transcription request: %v", err)}
	}
	if _, err := part.Write(wavBytes); err != nil {
		return "", &engine.Error{Kind: engine.KindInternal, Message: fmt.Sprintf("encode transcription request: %v", err)}
	}
	if err := writer.WriteField("response_format", "json"); err != nil {
		return "", &engine.Error{Kind: engine.KindInternal, Message: fmt.Sprintf("encode transcription request: %v", err)}
	}
	if err := writer.Close(); err != nil {
		return "", &engine.Error{Kind: engine.KindInternal, Message: fmt.Sprintf("encode transcription request: %v", err)}
	}

	ctx, cancel := context.WithTimeout(ctx, engine.RequestTimeout(engineCfg, engine.DefaultTranscriptionTimeout))
	defer cancel()
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, &body)
	if err != nil {
		return "", &engine.Error{Kind: engine.KindInternal, Message: err.Error()}
	}
	upstreamReq.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := r.client.Do(upstreamReq)
	if err != nil {
		return "", &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("whisper upstream request failed: %v", err)}
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxChatReplyBytes))
	if err != nil {
		return "", &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("read whisper upstream response: %v", err)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("whisper upstream returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))}
	}

	var parsed struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("decode whisper upstream response: %v", err)}
	}
	r.manager.MarkSuccess("whisper")
	// whisper-server joins segments with newlines; collapse to one line to
	// match the subprocess -nt output shape.
	return strings.Join(strings.Fields(parsed.Text), " "), nil
}

func (r *router) handleVoice(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodPost) {
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxTranscriptionUploadBytes)

	var wavBytes []byte
	if file, header, err := req.FormFile("file"); err == nil {
		defer file.Close()
		if header == nil || header.Filename == "" {
			writeJSONError(w, http.StatusBadRequest, "multipart field file must include a filename")
			return
		}
		data, err := io.ReadAll(file)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("save uploaded file: %v", err))
			return
		}
		wavBytes = data
	}

	history, err := parseVoiceHistory(req.FormValue("history"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	clonedVoice, err := r.resolveVoice(req.FormValue("voice"))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	loop := voice.Loop{
		Engines:    r.engines,
		Chat:       r.chatOnce,
		Transcribe: r.transcribe,
		Speak: func(ctx context.Context, text string, v *engine.Voice) ([]byte, error) {
			return r.speak(ctx, text, v, false)
		},
	}
	result, err := loop.Run(req.Context(), voice.Request{
		WAV:     wavBytes,
		Message: req.FormValue("message"),
		History: history,
		Voice:   clonedVoice,
	})
	if err != nil {
		writeEngineError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(voiceResponse{
		Transcript:  result.Transcript,
		Reply:       result.Reply,
		AudioFormat: "wav",
		AudioB64:    base64.StdEncoding.EncodeToString(padSpeech(result.Audio)),
	})
}

// resolveVoice maps an optional voice id from a request to the stored cloned
// voice the speech engine should reference; "" and "default" mean the config
// default voice.
func (r *router) resolveVoice(id string) (*engine.Voice, error) {
	id = strings.TrimSpace(id)
	if id == "" || id == "default" {
		return nil, nil
	}
	clone, ok, err := r.voices.Load(id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("voice %q not found", id)
	}
	refPath, err := r.voices.ReferencePath(clone.ID)
	if err != nil {
		return nil, err
	}
	return &engine.Voice{RefWAVPath: refPath, RefText: clone.Transcript}, nil
}

const (
	// maxVoiceDesignInstructChars caps the natural-language voice description.
	maxVoiceDesignInstructChars = 500
	// voiceDesignSampleText is what a freshly designed voice says. It doubles
	// as the cloning reference transcript when the voice is saved, so it is
	// long enough to condition the Base model well.
	voiceDesignSampleText = "Hello there! This is my brand new voice, created from just a written description. I hope you enjoy the way I sound, because I could talk like this all day long."
)

// voiceDesignNormalizePrompt turns whatever the user typed into the two
// input dialects the design engines prefer: a natural prose sentence
// (Qwen3, VoxCPM2) and OmniVoice's strict attribute vocabulary.
const voiceDesignNormalizePrompt = `You are a voice-design normalizer. Convert the user's voice description into two forms and reply with ONLY a JSON object, no markdown, in this exact shape: {"prose": "...", "attributes": "..."}
"prose": one natural English sentence describing the voice (speaker, age, accent, tone, texture, emotion).
"attributes": comma-separated attribute values chosen ONLY from this list, without category names, omitting anything the description does not imply: male, female, child, teenager, young adult, middle-aged, elderly, very low pitch, low pitch, moderate pitch, high pitch, very high pitch, whisper, american accent, british accent, australian accent, canadian accent, indian accent, chinese accent, korean accent, japanese accent, portuguese accent, russian accent.
Example reply: {"prose": "An elderly British man with a deep, refined voice.", "attributes": "male, elderly, low pitch, british accent"}`

// omniAttributeCategories is OmniVoice's instruct vocabulary, keyed by
// attribute value with its category; one value per category may be used.
var omniAttributeCategories = map[string]string{
	"male": "gender", "female": "gender",
	"child": "age", "teenager": "age", "young adult": "age", "middle-aged": "age", "elderly": "age",
	"very low pitch": "pitch", "low pitch": "pitch", "moderate pitch": "pitch", "high pitch": "pitch", "very high pitch": "pitch",
	"whisper":         "style",
	"american accent": "accent", "british accent": "accent", "australian accent": "accent",
	"canadian accent": "accent", "indian accent": "accent", "chinese accent": "accent",
	"korean accent": "accent", "japanese accent": "accent", "portuguese accent": "accent",
	"russian accent": "accent",
}

// sanitizeOmniAttributes reduces a normalizer attribute string to values
// OmniVoice actually accepts: category prefixes are stripped, unknown items
// dropped, and only the first value per category kept. "" means nothing
// usable survived.
func sanitizeOmniAttributes(raw string) string {
	usedCategories := make(map[string]bool)
	var kept []string
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(strings.ToLower(item))
		if colon := strings.LastIndex(item, ":"); colon >= 0 {
			item = strings.TrimSpace(item[colon+1:])
		}
		category, ok := omniAttributeCategories[item]
		if !ok || usedCategories[category] {
			continue
		}
		usedCategories[category] = true
		kept = append(kept, item)
	}
	return strings.Join(kept, ", ")
}

// normalizeVoiceDescription is best-effort: when llama is unavailable or its
// reply does not parse, both forms come back empty and the raw description
// is used instead.
func (r *router) normalizeVoiceDescription(ctx context.Context, description string) (prose string, attributes string) {
	reply, err := r.llamaChat(ctx, []chatMessage{
		{Role: "system", Content: voiceDesignNormalizePrompt},
		{Role: "user", Content: description},
	})
	if err != nil {
		return "", ""
	}
	return parseVoiceDesignForms(reply)
}

// parseVoiceDesignForms extracts the {"prose", "attributes"} object from a
// normalizer reply, tolerating surrounding chatter.
func parseVoiceDesignForms(reply string) (string, string) {
	start := strings.Index(reply, "{")
	end := strings.LastIndex(reply, "}")
	if start < 0 || end <= start {
		return "", ""
	}
	var decoded struct {
		Prose      string `json:"prose"`
		Attributes string `json:"attributes"`
	}
	if err := json.Unmarshal([]byte(reply[start:end+1]), &decoded); err != nil {
		return "", ""
	}
	return strings.TrimSpace(decoded.Prose), strings.TrimSpace(decoded.Attributes)
}

// handleVoiceDesign creates a voice from a natural-language description via
// the selected design engine. The response carries the spoken sample;
// that same WAV is the cloning reference if the user saves the voice, so no
// candidate state lives server-side.
func (r *router) handleVoiceDesign(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodPost) {
		return
	}

	var body voiceDesignRequest
	req.Body = http.MaxBytesReader(w, req.Body, maxJSONBodyBytes)
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON request: %v", err))
		return
	}
	description := strings.TrimSpace(body.Description)
	if description == "" {
		writeJSONError(w, http.StatusBadRequest, "description is required")
		return
	}
	if len(description) > maxVoiceDesignInstructChars {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("description cannot exceed %d characters", maxVoiceDesignInstructChars))
		return
	}
	sampleText := strings.TrimSpace(body.SampleText)
	if sampleText == "" {
		sampleText = voiceDesignSampleText
	}
	if len(sampleText) > maxVoiceHistoryTurnChars {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("sample_text cannot exceed %d characters", maxVoiceHistoryTurnChars))
		return
	}
	model := strings.TrimSpace(body.Model)
	if model == "" {
		model = "voxcpm2"
	}
	if model != "qwen3" && model != "omnivoice" && model != "voxcpm2" {
		writeJSONError(w, http.StatusBadRequest, "model must be qwen3, omnivoice, or voxcpm2")
		return
	}

	// Normalize once, then hand each engine its preferred input dialect:
	// prose sentences for Qwen3/VoxCPM2, strict attributes for OmniVoice.
	prose, attributes := r.normalizeVoiceDescription(req.Context(), description)
	engineInput := description
	var spec engine.Spec
	switch model {
	case "qwen3":
		if prose != "" {
			engineInput = prose
		}
		spec = engine.VoiceDesignSpec(engineInput, sampleText)
	case "omnivoice":
		if cleaned := sanitizeOmniAttributes(attributes); cleaned != "" {
			engineInput = cleaned
		} else if cleaned := sanitizeOmniAttributes(description); cleaned != "" {
			// The user may have typed valid attributes directly.
			engineInput = cleaned
		}
		spec = engine.OmniVoiceDesignSpec(engineInput, sampleText)
	case "voxcpm2":
		if prose != "" {
			engineInput = prose
		}
		spec = engine.VoxCPMDesignSpec(engineInput, sampleText)
	}

	result, err := r.engines.Run(req.Context(), spec)
	if err != nil {
		writeEngineError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(voiceDesignResponse{
		Description: description,
		Model:       model,
		Prose:       prose,
		Attributes:  attributes,
		EngineInput: engineInput,
		Transcript:  sampleText,
		AudioFormat: "wav",
		// The raw (unpadded) WAV is the cloning reference; the padded copy
		// is for listening.
		ReferenceB64: base64.StdEncoding.EncodeToString(result.Output),
		PreviewB64:   base64.StdEncoding.EncodeToString(padSpeech(result.Output)),
	})
}

// handleVoices lists the cloned voices or creates one. Creation uploads a
// reference WAV; when no transcript is supplied, whisper transcribes the
// reference first, so the clone is grounded in what the reference actually
// says.
func (r *router) handleVoices(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		clones, err := r.voices.List()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		summaries, err := r.actorVoiceSummaries(clones)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(voiceListResponse{Voices: summaries})
	case http.MethodPost:
		req.Body = http.MaxBytesReader(w, req.Body, voice.MaxReferenceWAVBytes)
		data, ok := readUploadedWAV(w, req)
		if !ok {
			return
		}
		name := strings.TrimSpace(req.FormValue("name"))
		if name == "" {
			name = "Voice " + time.Now().Format("15:04:05")
		}
		transcript := strings.TrimSpace(req.FormValue("transcript"))
		if transcript == "" {
			text, err := r.transcribe(req.Context(), data)
			if err != nil {
				writeEngineError(w, err)
				return
			}
			transcript = strings.TrimSpace(text)
			if transcript == "" {
				writeJSONError(w, http.StatusBadRequest, "transcription of the reference wav returned no text; supply a transcript")
				return
			}
		}
		clone, err := r.voices.SaveWithSource(name, transcript, data, req.FormValue("protected") == "true", parseCloneSource(req))
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(actorVoiceSummaryOf(clone))
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleVoiceClone serves one cloned voice: DELETE /v1/voices/{id} removes
// it, GET /v1/voices/{id}/audio streams the reference WAV for playback.
func (r *router) handleVoiceClone(w http.ResponseWriter, req *http.Request) {
	tail := strings.TrimPrefix(req.URL.Path, "/v1/voices/")
	parts := strings.Split(tail, "/")
	if len(parts) == 1 && parts[0] != "" {
		if !requireMethod(w, req, http.MethodDelete) {
			return
		}
		_, ok, err := r.voices.Load(parts[0])
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !ok {
			writeJSONError(w, http.StatusNotFound, "voice not found")
			return
		}
		if err := r.voices.Delete(parts[0]); err != nil {
			if errors.Is(err, voice.ErrProtected) {
				writeJSONError(w, http.StatusForbidden, err.Error())
				return
			}
			if errors.Is(err, voice.ErrActorHasCharacters) {
				writeJSONError(w, http.StatusConflict, err.Error())
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "characters" {
		r.handleActorCharacterVoices(w, req, parts[0])
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "audio" {
		if !requireMethod(w, req, http.MethodGet) {
			return
		}
		path, err := r.voices.ReferencePath(parts[0])
		if err != nil {
			writeJSONError(w, http.StatusNotFound, "voice reference not found")
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		http.ServeFile(w, req, path)
		return
	}
	http.NotFound(w, req)
}

func (r *router) actorVoiceSummaries(actors []voice.Clone) ([]actorVoiceSummary, error) {
	summaries := make([]actorVoiceSummary, 0, len(actors))
	for _, actor := range actors {
		characters, err := r.voices.ListCharacterVoices(actor.ID)
		if err != nil {
			return nil, err
		}
		summary := actorVoiceSummaryOf(actor)
		summary.CharacterVoices = characterVoiceSummaries(characters)
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

func actorVoiceSummaryOf(actor voice.Clone) actorVoiceSummary {
	return actorVoiceSummary{
		Kind:       "actor_voice",
		ID:         actor.ID,
		Name:       actor.Name,
		Transcript: actor.Transcript,
		CreatedAt:  actor.CreatedAt,
		Protected:  actor.Protected,
		Source:     actor.Source,
		Analysis:   actor.Analysis,
		AudioURL:   "/v1/voices/" + actor.ID + "/audio",
	}
}

type characterVoiceWriteRequest struct {
	Name      string `json:"name"`
	Direction string `json:"direction"`
}

type characterVoicePreviewRequest struct {
	SampleText string `json:"sample_text"`
}

func (r *router) handleActorCharacterVoices(w http.ResponseWriter, req *http.Request, actorVoiceID string) {
	switch req.Method {
	case http.MethodGet:
		characters, err := r.voices.ListCharacterVoices(actorVoiceID)
		if err != nil {
			writeCharacterVoiceError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"character_voices": characterVoiceSummaries(characters)})
	case http.MethodPost:
		var body characterVoiceWriteRequest
		if err := decodeCharacterVoiceJSON(w, req, &body); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		character, err := r.voices.CreateCharacterVoice(actorVoiceID, body.Name, body.Direction)
		if err != nil {
			writeCharacterVoiceError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(characterVoiceSummaryOf(character))
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (r *router) handleCharacterVoice(w http.ResponseWriter, req *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(req.URL.Path, "/v1/character-voices/"), "/")
	parts := strings.Split(tail, "/")
	if len(parts) == 1 && parts[0] != "" {
		id := parts[0]
		switch req.Method {
		case http.MethodGet:
			character, ok, err := r.voices.LoadCharacterVoice(id)
			if err != nil {
				writeCharacterVoiceError(w, err)
				return
			}
			if !ok {
				writeCharacterVoiceError(w, voice.ErrCharacterNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(characterVoiceSummaryOf(character))
		case http.MethodPut:
			var body characterVoiceWriteRequest
			if err := decodeCharacterVoiceJSON(w, req, &body); err != nil {
				writeJSONError(w, http.StatusBadRequest, err.Error())
				return
			}
			character, err := r.voices.UpdateCharacterVoice(id, body.Name, body.Direction)
			if err != nil {
				writeCharacterVoiceError(w, err)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(characterVoiceSummaryOf(character))
		case http.MethodDelete:
			if err := r.voices.DeleteCharacterVoice(id); err != nil {
				writeCharacterVoiceError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Allow", "GET, PUT, DELETE")
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "preview" {
		if !requireMethod(w, req, http.MethodPost) {
			return
		}
		r.handleCharacterVoicePreview(w, req, parts[0])
		return
	}
	if len(parts) == 3 && parts[0] != "" && parts[1] == "preview" && parts[2] == "audio" {
		if !requireMethod(w, req, http.MethodGet) {
			return
		}
		path, err := r.voices.CharacterPreviewPath(parts[0])
		if err != nil {
			writeCharacterVoiceError(w, err)
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		http.ServeFile(w, req, path)
		return
	}
	http.NotFound(w, req)
}

func (r *router) handleCharacterVoicePreview(w http.ResponseWriter, req *http.Request, id string) {
	var body characterVoicePreviewRequest
	if err := decodeCharacterVoiceJSON(w, req, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	authoring := voice.CharacterAuthoring{
		Store: r.voices,
		Speak: func(ctx context.Context, request engine.SynthesisRequest, actor *engine.Voice) ([]byte, error) {
			return r.speakSynthesis(ctx, request, actor, false)
		},
	}
	character, err := authoring.GeneratePreview(req.Context(), id, body.SampleText)
	if err != nil {
		var engineErr *engine.Error
		if errors.As(err, &engineErr) {
			writeEngineError(w, err)
			return
		}
		writeCharacterVoiceError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(characterVoiceSummaryOf(character))
}

func decodeCharacterVoiceJSON(w http.ResponseWriter, req *http.Request, body any) error {
	req.Body = http.MaxBytesReader(w, req.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil {
		return fmt.Errorf("invalid JSON request: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("invalid JSON request: expected one object")
	}
	return nil
}

func writeCharacterVoiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, voice.ErrActorVoiceNotFound), errors.Is(err, voice.ErrCharacterNotFound), errors.Is(err, voice.ErrCharacterPreviewNotFound):
		writeJSONError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, voice.ErrCharacterVoiceChanged):
		writeJSONError(w, http.StatusConflict, err.Error())
	default:
		writeJSONError(w, http.StatusBadRequest, err.Error())
	}
}

func characterVoiceSummaries(characters []voice.CharacterVoice) []characterVoiceSummary {
	summaries := make([]characterVoiceSummary, 0, len(characters))
	for _, character := range characters {
		summaries = append(summaries, characterVoiceSummaryOf(character))
	}
	return summaries
}

func characterVoiceSummaryOf(character voice.CharacterVoice) characterVoiceSummary {
	summary := characterVoiceSummary{
		ID: character.ID, ActorVoiceID: character.ActorVoiceID, Name: character.Name,
		Direction: character.Direction, CreatedAt: character.CreatedAt, UpdatedAt: character.UpdatedAt,
		Preview: character.Preview,
	}
	if character.Preview != nil {
		summary.PreviewAudioURL = "/v1/character-voices/" + character.ID + "/preview/audio"
	}
	return summary
}

// parseCloneSource reads the optional provenance fields off a clone upload.
// The Extractor knows the recording, the seconds, and the speaker at the
// moment it mints a voice; a hand-uploaded reference knows none of it, so
// every field is optional and an empty source is left off entirely.
func parseCloneSource(req *http.Request) *voice.CloneSource {
	name := strings.TrimSpace(req.FormValue("source_name"))
	speaker := strings.TrimSpace(req.FormValue("source_speaker"))
	start, _ := strconv.ParseFloat(req.FormValue("source_start_sec"), 64)
	end, _ := strconv.ParseFloat(req.FormValue("source_end_sec"), 64)
	if name == "" && speaker == "" {
		return nil
	}
	if len(name) > 200 {
		name = name[:200]
	}
	if len(speaker) > 40 {
		speaker = speaker[:40]
	}
	return &voice.CloneSource{Name: name, StartSec: start, EndSec: end, Speaker: speaker}
}

const (
	// maxVoiceHistoryTurns caps how many prior turns one voice request may
	// carry; older turns should be dropped client-side.
	maxVoiceHistoryTurns = 40
	// maxVoiceHistoryTurnChars caps one turn's text.
	maxVoiceHistoryTurnChars = 4000
)

// parseVoiceHistory decodes the optional multipart "history" field: a JSON
// array of {"role","text"} turns, oldest first.
func parseVoiceHistory(raw string) ([]voice.Turn, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var history []voice.Turn
	if err := json.Unmarshal([]byte(raw), &history); err != nil {
		return nil, fmt.Errorf("history must be a JSON array of {role, text} turns: %v", err)
	}
	if len(history) > maxVoiceHistoryTurns {
		return nil, fmt.Errorf("history cannot exceed %d turns", maxVoiceHistoryTurns)
	}
	for i, turn := range history {
		if turn.Role != "user" && turn.Role != "assistant" {
			return nil, fmt.Errorf("history turn %d role must be user or assistant", i+1)
		}
		if strings.TrimSpace(turn.Text) == "" {
			return nil, fmt.Errorf("history turn %d text cannot be empty", i+1)
		}
		if len(turn.Text) > maxVoiceHistoryTurnChars {
			return nil, fmt.Errorf("history turn %d text cannot exceed %d characters", i+1, maxVoiceHistoryTurnChars)
		}
	}
	return history, nil
}

// voiceSystemPrompt shapes voice-loop replies for the speech engine: the
// reply is spoken aloud, so markdown and essay-length answers degrade into
// unlistenable audio.
const voiceSystemPrompt = "You are a voice assistant. Your reply will be spoken aloud, so answer conversationally in at most three short sentences of plain text, with no markdown, lists, or emojis."

// chatOnce is the voice loop's ChatFunc: the prior turns plus one user
// message in, the assistant reply out, via the llama server's
// /v1/chat/completions route.
func (r *router) chatOnce(ctx context.Context, history []voice.Turn, message string) (string, error) {
	messages := make([]chatMessage, 0, len(history)+2)
	messages = append(messages, chatMessage{Role: "system", Content: voiceSystemPrompt})
	for _, turn := range history {
		messages = append(messages, chatMessage{Role: turn.Role, Content: turn.Text})
	}
	messages = append(messages, chatMessage{Role: "user", Content: message})
	return r.llamaChat(ctx, messages)
}

// llamaChat sends one chat completion to the resident llama server.
// warmupTimeout is generous on purpose: the warmup exists to absorb the
// first-request page-in of a large model off slow storage, which can take
// minutes — exactly the cost the regular request timeout would otherwise
// turn into an empty first reply.
const warmupTimeout = 5 * time.Minute

// warmupLlama nudges a freshly switched chat model through one tiny
// completion so the weights page in now, not under the user's first
// message. Fire-and-forget best effort: a stopped engine refuses the
// connection, a mid-warmup restart severs it, and both are fine — the
// next real request simply pays the cost the warmup would have.
func (r *router) warmupLlama() {
	engineCfg, ok := r.engine("llama")
	if !ok {
		return
	}
	upstreamURL, ok := inferChatCompletionsURL(engineCfg.HealthURL)
	if !ok {
		return
	}
	payload, err := json.Marshal(chatCompletionRequest{
		Model:     "default",
		Messages:  []chatMessage{{Role: "user", Content: "hi"}},
		MaxTokens: 1,
	})
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), warmupTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxChatReplyBytes))
}

func (r *router) llamaChat(ctx context.Context, messages []chatMessage) (string, error) {
	engineCfg, ok := r.engine("llama")
	if !ok {
		return "", &engine.Error{Kind: engine.KindNotConfigured, Message: `engine "llama" is not configured`}
	}
	upstreamURL, ok := inferChatCompletionsURL(engineCfg.HealthURL)
	if !ok {
		return "", &engine.Error{Kind: engine.KindNotConfigured, Message: `engine "llama" healthUrl must end in /health to infer /v1/chat/completions`}
	}

	payload, err := json.Marshal(chatCompletionRequest{
		Model:    "default",
		Messages: messages,
	})
	if err != nil {
		return "", &engine.Error{Kind: engine.KindInternal, Message: fmt.Sprintf("encode chat request: %v", err)}
	}

	ctx, cancel := context.WithTimeout(ctx, engine.RequestTimeout(engineCfg, defaultChatTimeout))
	defer cancel()
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL, bytes.NewReader(payload))
	if err != nil {
		return "", &engine.Error{Kind: engine.KindInternal, Message: err.Error()}
	}
	upstreamReq.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(upstreamReq)
	if err != nil {
		return "", &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("llama upstream request failed: %v", err)}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxChatReplyBytes))
	if err != nil {
		return "", &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("read llama upstream response: %v", err)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("llama upstream returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
	}

	reply, err := extractChatReply(body)
	if err != nil {
		return "", &engine.Error{Kind: engine.KindEngineFailure, Message: err.Error()}
	}
	r.manager.MarkSuccess("llama")
	return reply, nil
}

func extractChatReply(body []byte) (string, error) {
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Text string `json:"text"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decode llama chat response: %v", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("llama chat response has no choices")
	}
	if content := strings.TrimSpace(parsed.Choices[0].Message.Content); content != "" {
		return content, nil
	}
	return strings.TrimSpace(parsed.Choices[0].Text), nil
}

type storyBuilderProjectCreateRequest struct {
	Name string `json:"name"`
}

type storyBuilderProjectUpdateRequest struct {
	Name     string                `json:"name"`
	Revision int                   `json:"revision"`
	Tracks   *[]storybuilder.Track `json:"tracks"`
}

func (r *router) handleStoryBuilderProjects(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		projects, err := r.storyBuilderProjects.List()
		if err != nil {
			writeStoryBuilderProjectError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"projects": projects})
	case http.MethodPost:
		var body storyBuilderProjectCreateRequest
		if err := decodeStoryBuilderProjectRequest(w, req, &body); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		project, err := r.storyBuilderProjects.Create(body.Name)
		if err != nil {
			writeStoryBuilderProjectError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(project)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (r *router) handleStoryBuilderProject(w http.ResponseWriter, req *http.Request) {
	id := strings.TrimPrefix(req.URL.Path, "/v1/story-builder-projects/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, req)
		return
	}
	switch req.Method {
	case http.MethodGet:
		project, ok, err := r.storyBuilderProjects.Get(id)
		if err != nil {
			writeStoryBuilderProjectError(w, err)
			return
		}
		if !ok {
			writeStoryBuilderProjectError(w, storybuilder.ErrNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(project)
	case http.MethodPut:
		var body storyBuilderProjectUpdateRequest
		if err := decodeStoryBuilderProjectRequest(w, req, &body); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if body.Revision < 1 {
			writeJSONError(w, http.StatusBadRequest, "revision must be positive")
			return
		}
		if body.Tracks == nil {
			writeJSONError(w, http.StatusBadRequest, "tracks is required")
			return
		}
		project, err := r.storyBuilderProjects.Update(id, storybuilder.ProjectUpdate{Name: body.Name, Revision: body.Revision, Tracks: *body.Tracks})
		if err != nil {
			writeStoryBuilderProjectError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(project)
	case http.MethodDelete:
		if err := r.storyBuilderProjects.Delete(id); err != nil {
			writeStoryBuilderProjectError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func decodeStoryBuilderProjectRequest(w http.ResponseWriter, req *http.Request, body any) error {
	req.Body = http.MaxBytesReader(w, req.Body, maxJSONBodyBytes)
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(body); err != nil {
		return fmt.Errorf("invalid JSON request: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("invalid JSON request: expected one object")
	}
	return nil
}

func writeStoryBuilderProjectError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storybuilder.ErrInvalid):
		writeJSONError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, storybuilder.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, storybuilder.ErrConflict):
		writeJSONError(w, http.StatusConflict, err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, err.Error())
	}
}

func (r *router) handleStories(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		stories, err := r.stories.List()
		if err != nil {
			writeStoryError(w, http.StatusInternalServerError, story.CodeStoreFailure, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(story.ListResponse{Stories: stories})
	case http.MethodPost:
		var body story.CreateRequest
		req.Body = http.MaxBytesReader(w, req.Body, story.MaxRequestBodyBytes)
		decoder := json.NewDecoder(req.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil {
			writeStoryError(w, http.StatusBadRequest, story.CodeInvalidRequest, fmt.Sprintf("invalid JSON request: %v", err))
			return
		}
		if !r.validateStoryCastVoices(w, body) {
			return
		}
		response, err := r.stories.Submit(req.Context(), body)
		if err != nil {
			writeStoryErrorFromError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(response)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeStoryError(w, http.StatusMethodNotAllowed, story.CodeInvalidRequest, "method not allowed")
	}
}

// validateStoryCastVoices rejects requests naming voices that do not exist;
// a missing voice failing mid-synthesis would waste the whole run.
func (r *router) validateStoryCastVoices(w http.ResponseWriter, body story.CreateRequest) bool {
	check := func(label string, voiceID string) bool {
		if strings.TrimSpace(voiceID) == "" {
			return true
		}
		if _, err := r.resolveVoice(voiceID); err != nil {
			writeStoryError(w, http.StatusBadRequest, story.CodeInvalidRequest, fmt.Sprintf("%s: %v", label, err))
			return false
		}
		return true
	}
	for speakerID, voiceID := range body.CastVoices {
		if !check("cast_voices["+speakerID+"]", voiceID) {
			return false
		}
	}
	for i, member := range body.Cast {
		if !check(fmt.Sprintf("cast[%d].voice_id", i), member.VoiceID) {
			return false
		}
	}
	return true
}

// handleStoryDraft writes a story without producing audio: the fast half of
// the draft → edit → produce flow.
func (r *router) handleStoryDraft(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodPost) {
		return
	}
	var body story.CreateRequest
	req.Body = http.MaxBytesReader(w, req.Body, story.MaxRequestBodyBytes)
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeStoryError(w, http.StatusBadRequest, story.CodeInvalidRequest, fmt.Sprintf("invalid JSON request: %v", err))
		return
	}
	if !r.validateStoryCastVoices(w, body) {
		return
	}
	draft, err := r.stories.Draft(req.Context(), body)
	if err != nil {
		writeStoryErrorFromError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(draft)
}

func (r *router) handleStory(w http.ResponseWriter, req *http.Request) {
	tail := strings.TrimPrefix(req.URL.Path, "/v1/stories/")
	parts := strings.Split(tail, "/")
	if len(parts) == 1 && parts[0] != "" {
		switch req.Method {
		case http.MethodGet:
			status, ok, err := r.stories.Status(parts[0])
			if err != nil {
				writeStoryErrorFromError(w, err)
				return
			}
			if !ok {
				writeStoryError(w, http.StatusNotFound, story.CodeNotFound, "story not found")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(status)
		case http.MethodDelete:
			if err := r.stories.Delete(parts[0]); err != nil {
				writeStoryErrorFromError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Allow", "GET, DELETE")
			writeStoryError(w, http.StatusMethodNotAllowed, story.CodeInvalidRequest, "method not allowed")
		}
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "cancel" {
		if !requireMethod(w, req, http.MethodPost) {
			return
		}
		status, err := r.stories.Cancel(parts[0])
		if err != nil {
			writeStoryErrorFromError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(status)
		return
	}
	// An interrupted production either resumes — kept takes stay, the rest
	// are synthesized under the current engine fingerprint — or is
	// discarded, takes and all.
	if len(parts) == 2 && parts[0] != "" && parts[1] == "resume" {
		if !requireMethod(w, req, http.MethodPost) {
			return
		}
		response, err := r.stories.Resume(req.Context(), parts[0])
		if err != nil {
			writeStoryErrorFromError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(response)
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "discard" {
		if !requireMethod(w, req, http.MethodPost) {
			return
		}
		if err := r.stories.Discard(parts[0]); err != nil {
			writeStoryErrorFromError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": parts[0], "discarded": true})
		return
	}
	// A scene audition is an ephemeral stitch of the scene's current takes:
	// nothing mastered, nothing stored, so it is computed per request and
	// must not be cached.
	if len(parts) == 4 && parts[0] != "" && parts[1] == "scenes" && parts[2] != "" && parts[3] == "audition.wav" {
		if !requireMethod(w, req, http.MethodGet) {
			return
		}
		audio, err := r.stories.Audition(parts[0], parts[2])
		if err != nil {
			writeStoryErrorFromError(w, err)
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(audio)
		return
	}
	// Artifacts are story.wav, one take (lines/<line>/<take>.wav), or one
	// render revision (renders/render-NNN.wav); the store owns the whitelist.
	if len(parts) >= 3 && parts[0] != "" && parts[1] == "artifact" && parts[2] != "" {
		if !requireMethod(w, req, http.MethodGet) {
			return
		}
		path, err := r.stories.ArtifactPath(parts[0], parts[2:]...)
		if err != nil {
			writeStoryErrorFromError(w, err)
			return
		}
		w.Header().Set("Content-Type", story.ArtifactContentType(path))
		http.ServeFile(w, req, path)
		return
	}
	if len(parts) == 2 && parts[0] != "" && parts[1] == "export" {
		if !requireMethod(w, req, http.MethodPost) {
			return
		}
		req.Body = http.MaxBytesReader(w, req.Body, story.MaxRequestBodyBytes)
		var body storyExportRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil && err != io.EOF {
			writeStoryError(w, http.StatusBadRequest, story.CodeInvalidRequest, "invalid export request body")
			return
		}
		format, ok := engine.LookupAudioFormat(strings.TrimSpace(body.Format))
		if !ok {
			writeStoryError(w, http.StatusBadRequest, story.CodeUnsupportedArtifact, "format must be mp3 or opus")
			return
		}
		bitrate := strings.TrimSpace(body.Bitrate)
		if bitrate == "" {
			bitrate = format.DefaultBitrate
		}
		manifest, export, err := r.stories.Export(req.Context(), parts[0], body.Revision, format.ID, bitrate)
		if err != nil {
			writeStoryErrorFromError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"export": export, "manifest": manifest})
		return
	}
	// The take room: retake one line, edit its production settings, or
	// publish a new render revision from the current takes.
	if len(parts) == 2 && parts[0] != "" && parts[1] == "render" {
		if !requireMethod(w, req, http.MethodPost) {
			return
		}
		manifest, render, err := r.stories.Render(req.Context(), parts[0])
		if err != nil {
			writeStoryErrorFromError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"render": render, "manifest": manifest})
		return
	}
	if len(parts) == 4 && parts[0] != "" && parts[1] == "lines" && parts[2] != "" && parts[3] == "takes" {
		if !requireMethod(w, req, http.MethodPost) {
			return
		}
		manifest, take, err := r.stories.Retake(req.Context(), parts[0], parts[2])
		if err != nil {
			writeStoryErrorFromError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"take": take, "manifest": manifest})
		return
	}
	if len(parts) == 3 && parts[0] != "" && parts[1] == "lines" && parts[2] != "" {
		if !requireMethod(w, req, http.MethodPatch) {
			return
		}
		req.Body = http.MaxBytesReader(w, req.Body, story.MaxRequestBodyBytes)
		var patch story.LinePatch
		if err := json.NewDecoder(req.Body).Decode(&patch); err != nil {
			writeStoryError(w, http.StatusBadRequest, story.CodeInvalidRequest, "invalid line patch body")
			return
		}
		manifest, err := r.stories.EditLine(parts[0], parts[2], patch)
		if err != nil {
			writeStoryErrorFromError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"manifest": manifest})
		return
	}
	http.NotFound(w, req)
}

func (r *router) engine(name string) (config.EngineConfig, bool) {
	engineCfg, ok := r.cfg.Engines[name]
	return engineCfg, ok
}

func (r *router) reserveEngine(ctx context.Context, name string) (func(), bool) {
	_ = ctx
	return r.engines.Reserve(name)
}

func (r *router) resolveAudiobookEngine(_ context.Context, engineID string) (audiobook.EngineIdentity, error) {
	engineCfg, ok := r.engine(engineID)
	if !ok {
		return audiobook.EngineIdentity{}, &engine.Error{
			Kind:    engine.KindNotConfigured,
			Message: fmt.Sprintf("audiobook engine %q is not configured; add it to config or choose another narrator", engineID),
		}
	}
	mode := engineCfg.Mode
	if mode == "" {
		mode = "server"
	}
	runtimeIdentity := fullSynthesisFingerprint(engineCfg)
	modelID, fingerprint := r.catalogAudiobookIdentity(engineID, runtimeIdentity)
	return audiobook.EngineIdentity{
		ID:              engineID,
		Family:          engineID,
		Mode:            mode,
		ModelID:         modelID,
		RuntimeIdentity: runtimeIdentity,
		Fingerprint:     fingerprint,
	}, nil
}

func (r *router) catalogAudiobookIdentity(engineID, runtimeIdentity string) (string, string) {
	tracked := make([]models.Model, 0)
	for _, model := range r.catalog.Models {
		if model.Engine == engineID {
			tracked = append(tracked, model)
		}
	}
	if len(tracked) == 0 {
		return audioServerModelID, runtimeIdentity
	}
	sort.Slice(tracked, func(i, j int) bool { return tracked[i].ID < tracked[j].ID })
	h := sha256.New()
	_, _ = h.Write([]byte(runtimeIdentity + "\x00"))
	ids := make([]string, 0, len(tracked))
	for _, model := range tracked {
		ids = append(ids, model.ID)
		encoded, _ := json.Marshal(model)
		_, _ = h.Write(encoded)
		_, _ = h.Write([]byte{0})
		path := model.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(r.modelsRoot, path)
		}
		if info, err := os.Stat(path); err == nil {
			_, _ = h.Write([]byte(fmt.Sprintf("%d:%d\x00", info.Size(), info.ModTime().UnixNano())))
		}
	}
	return strings.Join(ids, ","), hex.EncodeToString(h.Sum(nil))
}

func (r *router) resolveAudiobookVoice(_ context.Context, voiceID string) (audiobook.VoiceIdentity, error) {
	if voiceID == "" || voiceID == "default" {
		return audiobook.VoiceIdentity{ID: "default", Fingerprint: "default"}, nil
	}
	clone, ok, err := r.voices.Load(voiceID)
	if err != nil {
		return audiobook.VoiceIdentity{}, err
	}
	if !ok {
		return audiobook.VoiceIdentity{}, fmt.Errorf("voice %q not found", voiceID)
	}
	resolved, err := r.resolveVoice(voiceID)
	if err != nil {
		return audiobook.VoiceIdentity{}, err
	}
	file, err := os.Open(resolved.RefWAVPath)
	if err != nil {
		return audiobook.VoiceIdentity{}, fmt.Errorf("open voice reference: %w", err)
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, io.LimitReader(file, voice.MaxReferenceWAVBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		return audiobook.VoiceIdentity{}, fmt.Errorf("hash voice reference: %w", copyErr)
	}
	if closeErr != nil {
		return audiobook.VoiceIdentity{}, fmt.Errorf("close voice reference: %w", closeErr)
	}
	referenceSHA := hex.EncodeToString(h.Sum(nil))
	identityHash := sha256.Sum256([]byte(voiceID + "\x00" + referenceSHA + "\x00" + resolved.RefText))
	usableSpeech, fitnessMethod := 0.0, "analysis unavailable"
	warnings := []string(nil)
	ineligibleReason := "reference analysis is unavailable; re-save or inspect the voice"
	if clone.Analysis != nil {
		usableSpeech = clone.Analysis.UsableSpeechSeconds
		fitnessMethod = clone.Analysis.Method
		warnings = append(warnings, clone.Analysis.Warnings...)
		if clone.Analysis.Fitness == "unsupported" {
			ineligibleReason = "reference format is unsupported for usable-speech analysis"
		} else if usableSpeech < 10 {
			ineligibleReason = fmt.Sprintf("requires at least 10 seconds of usable speech; measured %.1f seconds via %s", usableSpeech, fitnessMethod)
		} else {
			ineligibleReason = ""
		}
	}
	return audiobook.VoiceIdentity{
		ID: voiceID, Fingerprint: hex.EncodeToString(identityHash[:]), ReferenceSHA256: referenceSHA,
		UsableSpeechSeconds: usableSpeech, FitnessMethod: fitnessMethod, FitnessWarnings: warnings,
		DramaBoxIneligibleReason: ineligibleReason, Reference: resolved,
	}, nil
}

// synthesizeSpeech is the story pipeline's SynthesizeFunc. It speaks without
// re-reserving: the story manager already holds the audio slot for the whole
// job. voiceID selects a stored cloned voice; "" keeps the studio default.
func (r *router) synthesizeSpeech(ctx context.Context, text string, voiceID string) ([]byte, error) {
	clonedVoice, err := r.resolveVoice(voiceID)
	if err != nil {
		return nil, err
	}
	return r.speak(ctx, text, clonedVoice, true)
}

// synthesizeAudiobook routes each prepared chunk through the engine selected
// on the book request. The manager already holds that engine's reservation.
func (r *router) synthesizeAudiobook(ctx context.Context, request audiobook.SynthesisRequest) ([]byte, error) {
	return r.speakSynthesis(ctx, request, request.Voice, true)
}

// synthesisFingerprint names the audio synthesis configuration for take
// provenance: the engine's command, args and default voice, plus the size
// and mtime of any of those that are files on disk — the binary, a server
// config, a model path — hashed together. A resume keeps only takes whose
// fingerprint matches the configuration running now, so a swapped model or
// rebuilt engine re-synthesizes rather than splices. Timeouts and health
// probing stay out: a changed timeout does not change audio.
// Over-invalidation costs a re-synthesis; under-invalidation publishes an
// episode spoken by two different engines, which is why file identity is
// included deliberately.
func synthesisFingerprint(cfg config.EngineConfig) string {
	full := fullSynthesisFingerprint(cfg)
	if full == "" {
		return ""
	}
	return full[:16]
}

func fullSynthesisFingerprint(cfg config.EngineConfig) string {
	if cfg.Command == "" {
		return ""
	}
	h := sha256.New()
	add := func(s string) {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	addFileIdentity := func(path string) {
		if !filepath.IsAbs(path) && cfg.WorkingDir != "" {
			path = filepath.Join(cfg.WorkingDir, path)
		}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			add(fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano()))
		}
	}
	add(cfg.Command)
	addFileIdentity(cfg.Command)
	add(cfg.Mode)
	add(cfg.WorkingDir)
	for _, arg := range cfg.Args {
		add(arg)
		addFileIdentity(arg)
	}
	add(cfg.DefaultVoiceRef)
	addFileIdentity(cfg.DefaultVoiceRef)
	add(cfg.DefaultVoiceText)
	return hex.EncodeToString(h.Sum(nil))
}

// storyScriptSystemPrompt instructs llama to write grounded audio stories
// as dialogue scripts. The fixture chat server keys on the phrase "audio
// stories as dialogue scripts" to return its canned script.
const storyScriptSystemPrompt = `You write short, factual audio stories as dialogue scripts. Reply with ONLY a JSON object, no markdown, in this exact shape: {"title": "...", "script": [{"speaker_id": "...", "text": "...", "fact_ids": ["fact-1"]}]}
Rules:
%s- Every line must cite at least one fact id from the provided fact list in fact_ids, and may only state things those cited facts support. Do not invent information.
- text is plain spoken language: one to three short sentences, no markdown, no stage directions, no emojis.
- Give the story a beginning, a middle, and an ending line that lands.`

// storySketchSystemPrompt is the anti-grounding prompt: invention is the
// point, and there are no facts to cite. The fixture chat server keys on the
// phrase "comedy sketch scripts" to return its canned sketch.
const storySketchSystemPrompt = `You write comedy sketch scripts as dialogue. Reply with ONLY a JSON object, no markdown, in this exact shape: {"title": "...", "script": [{"speaker_id": "...", "text": "..."}]}
Rules:
%s- Invent freely. There are no sources and nothing to cite: the premise is yours to play with.
- text is what the performer says out loud: one to three short sentences, no markdown, no stage directions, no character names as prefixes, no emojis.
- Write for the ear. Characters interrupt, misunderstand each other, and talk past each other.
- Build to something: a running joke that escalates, and a final line that lands the sketch.`

// storyCastRules renders the dynamic speaker rules for the script prompt.
func storyCastRules(cast []story.CastMember) string {
	ids := make([]string, 0, len(cast))
	for _, member := range cast {
		ids = append(ids, member.ID)
	}
	var rules strings.Builder
	fmt.Fprintf(&rules, "- speaker_id must be exactly one of: %s.\n", strings.Join(ids, ", "))
	for _, member := range cast {
		if member.Role != "" {
			fmt.Fprintf(&rules, "- %s (%s): %s.\n", member.ID, member.DisplayName, member.Role)
		}
	}
	return rules.String()
}

// writeStoryScript is the story manager's ScriptFunc: facts (or a premise)
// in, a script out of llama, with one retry that feeds the validation error
// back.
func (r *router) writeStoryScript(ctx context.Context, req story.ScriptRequest) (string, []story.ScriptLine, error) {
	lines := req.TargetSeconds / 7
	if lines < 8 {
		lines = 8
	}
	if lines > 40 {
		lines = 40
	}
	systemPrompt := storyScriptSystemPrompt
	if req.Mode == story.ModeSketch {
		systemPrompt = storySketchSystemPrompt
	}

	var prompt strings.Builder
	if req.Mode == story.ModeSketch {
		fmt.Fprintf(&prompt, "Premise: %s\n", req.Subject)
		if req.Premise != "" {
			fmt.Fprintf(&prompt, "Detail: %s\n", req.Premise)
		}
		if req.Style != "" {
			fmt.Fprintf(&prompt, "Style: %s\n", req.Style)
		}
		fmt.Fprintf(&prompt, "Target length: about %d spoken lines (%d seconds of audio).\n", lines, req.TargetSeconds)
	} else {
		fmt.Fprintf(&prompt, "Subject: %s\nTarget length: about %d spoken lines (%d seconds of audio).\nFacts:\n", req.Subject, lines, req.TargetSeconds)
		for _, fact := range req.Facts {
			if fact.Conflicting {
				continue
			}
			fmt.Fprintf(&prompt, "%s: %s\n", fact.ID, fact.Claim)
		}
	}

	messages := []chatMessage{
		{Role: "system", Content: fmt.Sprintf(systemPrompt, storyCastRules(req.Cast))},
		{Role: "user", Content: prompt.String()},
	}
	title, script, err := r.requestStoryScript(ctx, messages, req)
	if err == nil {
		return title, script, nil
	}
	// One retry with the failure explained; models usually fix cited ids
	// and speaker names when told what was wrong.
	retry := append(messages, chatMessage{Role: "user", Content: "Your previous reply was rejected: " + err.Error() + ". Reply again with ONLY the corrected JSON object."})
	title, script, retryErr := r.requestStoryScript(ctx, retry, req)
	if retryErr != nil {
		code := story.CodeGroundingFailure
		if req.Mode == story.ModeSketch {
			code = story.CodeInvalidScript
		}
		return "", nil, story.NewError(code, "story scripting failed: "+retryErr.Error())
	}
	return title, script, nil
}

// requestStoryScript runs one llama round and lightly validates the script
// shape; the manager's grounding validator remains the final gate.
func (r *router) requestStoryScript(ctx context.Context, messages []chatMessage, req story.ScriptRequest) (string, []story.ScriptLine, error) {
	reply, err := r.llamaChat(ctx, messages)
	if err != nil {
		return "", nil, err
	}
	start := strings.Index(reply, "{")
	end := strings.LastIndex(reply, "}")
	if start < 0 || end <= start {
		return "", nil, fmt.Errorf("reply contained no JSON object")
	}
	var decoded struct {
		Title  string             `json:"title"`
		Script []story.ScriptLine `json:"script"`
	}
	if err := json.Unmarshal([]byte(reply[start:end+1]), &decoded); err != nil {
		return "", nil, fmt.Errorf("reply was not valid script JSON: %v", err)
	}
	if strings.TrimSpace(decoded.Title) == "" {
		return "", nil, fmt.Errorf("reply had no title")
	}
	if len(decoded.Script) < 4 {
		return "", nil, fmt.Errorf("script had %d lines, need at least 4", len(decoded.Script))
	}
	speakers := make(map[string]bool, len(req.Cast))
	for _, member := range req.Cast {
		speakers[member.ID] = true
	}
	validFacts := make(map[string]bool, len(req.Facts))
	for _, fact := range req.Facts {
		if !fact.Conflicting {
			validFacts[fact.ID] = true
		}
	}
	sketch := req.Mode == story.ModeSketch
	for i, line := range decoded.Script {
		if !speakers[line.SpeakerID] {
			return "", nil, fmt.Errorf("script line %d uses unknown speaker %q", i+1, line.SpeakerID)
		}
		if strings.TrimSpace(line.Text) == "" {
			return "", nil, fmt.Errorf("script line %d has no text", i+1)
		}
		if sketch {
			// There are no fact cards behind a sketch, so any ids the model
			// invented out of habit are dropped rather than rejected.
			decoded.Script[i].FactIDs = nil
			continue
		}
		if len(line.FactIDs) == 0 {
			return "", nil, fmt.Errorf("script line %d cites no fact ids", i+1)
		}
		for _, id := range line.FactIDs {
			if !validFacts[id] {
				return "", nil, fmt.Errorf("script line %d cites unknown fact id %q", i+1, id)
			}
		}
	}
	return strings.TrimSpace(decoded.Title), decoded.Script, nil
}

// readUploadedWAV pulls the multipart "file" field into memory, writing the
// HTTP error itself when the upload is unusable.
func readUploadedWAV(w http.ResponseWriter, req *http.Request) ([]byte, bool) {
	file, header, err := req.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "multipart field file is required")
		return nil, false
	}
	defer file.Close()
	if header == nil || header.Filename == "" {
		writeJSONError(w, http.StatusBadRequest, "multipart field file must include a filename")
		return nil, false
	}
	data, err := io.ReadAll(file)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("save uploaded file: %v", err))
		return nil, false
	}
	return data, true
}

// slashPath normalizes a configured path for the resident audio server's
// JSON API, which wants forward slashes whatever the host looks like.
//
// This deliberately does not use filepath.ToSlash: that converts the *host's*
// separator, so on Linux it leaves a Windows-style config path untouched and
// the engine receives C:\voices\ref.wav. The config is the same file on every
// platform, so the conversion has to be too.
func slashPath(path string) string {
	return strings.ReplaceAll(path, `\`, "/")
}

// inferEngineURL derives a server engine's request route from its healthUrl,
// e.g. http://127.0.0.1:8733/health -> http://127.0.0.1:8733<path>.
func inferEngineURL(healthURL string, path string) (string, bool) {
	if healthURL == "" {
		return "", false
	}
	parsed, err := url.Parse(healthURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	if !strings.HasSuffix(parsed.Path, "/health") {
		return "", false
	}

	parsed.Path = strings.TrimSuffix(parsed.Path, "/health") + path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), true
}

func inferChatCompletionsURL(healthURL string) (string, bool) {
	return inferEngineURL(healthURL, "/v1/chat/completions")
}

// inferSDURL derives an sd-server route from its healthUrl's origin. Unlike
// the /health-suffixed engines, sd-server has no /health route (readiness is
// polled at /v1/models), so the path is taken from the origin rather than by
// stripping a /health suffix.
func inferSDURL(healthURL string, path string) (string, bool) {
	if healthURL == "" {
		return "", false
	}
	parsed, err := url.Parse(healthURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: path}).String(), true
}

func parseImageSize(size string) (int, int, error) {
	size = strings.TrimSpace(size)
	if size == "" {
		return 0, 0, nil
	}
	widthText, heightText, ok := strings.Cut(size, "x")
	if !ok {
		return 0, 0, fmt.Errorf("size must be formatted as WIDTHxHEIGHT")
	}
	width, err := strconv.Atoi(widthText)
	if err != nil || width <= 0 {
		return 0, 0, fmt.Errorf("size width must be a positive integer")
	}
	height, err := strconv.Atoi(heightText)
	if err != nil || height <= 0 {
		return 0, 0, fmt.Errorf("size height must be a positive integer")
	}
	if err := engine.ValidateImageDimensions(width, height); err != nil {
		return 0, 0, err
	}
	return width, height, nil
}

func requireMethod(w http.ResponseWriter, req *http.Request, method string) bool {
	if req.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	return false
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// writeEngineError maps failures crossing the engine seam (and the voice
// loop built on it) to HTTP status codes.
func writeEngineError(w http.ResponseWriter, err error) {
	if errors.Is(err, voice.ErrNoInput) {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	var engErr *engine.Error
	if errors.As(err, &engErr) {
		writeJSONError(w, engineHTTPStatus(engErr.Kind), engErr.Message)
		return
	}
	writeJSONError(w, http.StatusBadGateway, err.Error())
}

func engineHTTPStatus(kind engine.FailureKind) int {
	switch kind {
	case engine.KindNotConfigured:
		return http.StatusServiceUnavailable
	case engine.KindBusy:
		return http.StatusTooManyRequests
	case engine.KindInternal:
		return http.StatusInternalServerError
	case engine.KindInvalidInput:
		return http.StatusBadRequest
	default:
		return http.StatusBadGateway
	}
}

func writeStoryErrorFromError(w http.ResponseWriter, err error) {
	var storyErr *story.StoryError
	if errors.As(err, &storyErr) {
		writeStoryError(w, storyHTTPStatus(storyErr.Code), storyErr.Code, storyErr.Message)
		return
	}
	writeStoryError(w, http.StatusInternalServerError, story.CodeStoreFailure, err.Error())
}

func writeStoryError(w http.ResponseWriter, status int, code story.ErrorCode, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]*story.StoryError{
		"error": story.NewError(code, message),
	})
}

func storyHTTPStatus(code story.ErrorCode) int {
	switch code {
	case story.CodeStoryBusy, story.CodeEngineBusy:
		return http.StatusTooManyRequests
	case story.CodeNotFound, story.CodeSceneNotFound, story.CodeArtifactNotFound, story.CodeUnsupportedArtifact, story.CodeInvalidArtifactRequest:
		return http.StatusNotFound
	case story.CodeCannotCancel:
		return http.StatusConflict
	case story.CodeStoreFailure:
		return http.StatusInternalServerError
	default:
		return http.StatusBadRequest
	}
}

type speechRequest struct {
	Input  string `json:"input"`
	Voice  string `json:"voice"`
	Format string `json:"format"`
}

type imageGenerationRequest struct {
	Prompt         string `json:"prompt"`
	Size           string `json:"size"`
	ResponseFormat string `json:"response_format"`
	N              *int   `json:"n"`
	// Seed pins the noise a generation starts from. Absent or negative
	// means "roll a fresh one" — the gateway always resolves it to a
	// concrete value and reports it back, so the repeated-clicks-same-image
	// trap is gone and a good image can still be reproduced on purpose.
	Seed *int64 `json:"seed,omitempty"`
}

type imageGenerationResponse struct {
	Created int64                 `json:"created"`
	Data    []imageGenerationData `json:"data"`
	// Seed is the concrete seed this image was generated with.
	Seed int64 `json:"seed"`
}

type imageGenerationData struct {
	B64JSON string `json:"b64_json"`
}

type transcriptionResponse struct {
	Text       string `json:"text"`
	DurationMS int64  `json:"duration_ms"`
}

type voiceDesignRequest struct {
	Description string `json:"description"`
	SampleText  string `json:"sample_text"`
	// Model picks the design engine: "qwen3" (default), "omnivoice", or
	// "voxcpm2".
	Model string `json:"model"`
}

type voiceDesignResponse struct {
	Description string `json:"description"`
	Model       string `json:"model"`
	// Prose and Attributes are the normalized forms of the description;
	// EngineInput is the one actually sent to the design engine.
	Prose        string `json:"prose,omitempty"`
	Attributes   string `json:"attributes,omitempty"`
	EngineInput  string `json:"engine_input"`
	Transcript   string `json:"transcript"`
	AudioFormat  string `json:"audio_format"`
	ReferenceB64 string `json:"reference_b64"`
	PreviewB64   string `json:"preview_b64"`
}

type imageDescriptionRequest struct {
	ImageB64 string `json:"image_b64"`
	Voice    string `json:"voice"`
}

type imageDescriptionResponse struct {
	Description string `json:"description"`
	AudioFormat string `json:"audio_format"`
	AudioB64    string `json:"audio_b64"`
}

type visionChatRequest struct {
	Model    string              `json:"model"`
	Messages []visionChatMessage `json:"messages"`
}

type visionChatMessage struct {
	Role    string              `json:"role"`
	Content []visionContentPart `json:"content"`
}

type visionContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *visionImageURL `json:"image_url,omitempty"`
}

type visionImageURL struct {
	URL string `json:"url"`
}

type actorVoiceSummary struct {
	Kind            string                   `json:"kind"`
	ID              string                   `json:"id"`
	Name            string                   `json:"name"`
	Transcript      string                   `json:"transcript"`
	CreatedAt       time.Time                `json:"created_at"`
	Protected       bool                     `json:"protected,omitempty"`
	Source          *voice.CloneSource       `json:"source,omitempty"`
	Analysis        *voice.ReferenceAnalysis `json:"analysis,omitempty"`
	AudioURL        string                   `json:"audio_url"`
	CharacterVoices []characterVoiceSummary  `json:"character_voices"`
}

type characterVoiceSummary struct {
	ID              string                  `json:"id"`
	ActorVoiceID    string                  `json:"actor_voice_id"`
	Name            string                  `json:"name"`
	Direction       string                  `json:"direction"`
	CreatedAt       time.Time               `json:"created_at"`
	UpdatedAt       time.Time               `json:"updated_at"`
	Preview         *voice.CharacterPreview `json:"preview,omitempty"`
	PreviewAudioURL string                  `json:"preview_audio_url,omitempty"`
}

type voiceListResponse struct {
	Voices []actorVoiceSummary `json:"voices"`
}

type voiceResponse struct {
	Transcript  string `json:"transcript"`
	Reply       string `json:"reply"`
	AudioFormat string `json:"audio_format"`
	AudioB64    string `json:"audio_b64"`
}

type chatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	// MaxTokens caps generation; only the warmup ping sets it, so every
	// other request serializes exactly as before.
	MaxTokens int `json:"max_tokens,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
