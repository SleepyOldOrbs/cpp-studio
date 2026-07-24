package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cpp-studio/internal/config"
	"cpp-studio/internal/demo"
	"cpp-studio/internal/engine"
	"cpp-studio/internal/jobs"
	"cpp-studio/internal/library"
	"cpp-studio/internal/lifecycle"
	"cpp-studio/internal/models"
	"cpp-studio/internal/story"
	"cpp-studio/internal/voice"
	"cpp-studio/internal/wav"
)

type router struct {
	cfg        config.Config
	manager    *lifecycle.Manager
	client     *http.Client
	engines    engine.Invoker
	stories    *story.Manager
	voices     *voice.Store
	catalog    models.Manifest
	modelsRoot string
	jobs       *jobs.Registry
	library    *library.Store
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
		cfg:     cfg,
		manager: manager,
		client:  http.DefaultClient,
		engines: engine.NewRunner(cfg.Engines, manager),
		voices:  voice.NewStore(""),
		jobs:    jobs.NewRegistry(),
		library: library.NewStore(""),
	}
	storyOptions := story.ManagerOptions{
		ReserveEngine: r.reserveEngine,
		Synthesize:    r.synthesizeSpeech,
		Jobs:          r.jobs,
	}
	// With a llama engine configured, stories are written by the model;
	// without one (CI, pure-fixture setups) the deterministic fixture
	// script keeps the pipeline runnable.
	if _, ok := cfg.Engines["llama"]; ok {
		storyOptions.Script = r.writeStoryScript
	}
	r.stories = story.NewManager(storyOptions)

	// The model manifest is optional: a config without a models block (CI,
	// fixture setups) simply serves an empty catalog rather than failing.
	if cfg.Models != nil && cfg.Models.Manifest != "" {
		if manifest, err := models.Load(cfg.Models.Manifest); err == nil {
			r.catalog = manifest
			r.modelsRoot = cfg.Models.Root
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", r.handleRoot)
	mux.HandleFunc("/demo", r.handleDemoRedirect)
	mux.Handle("/demo/", http.StripPrefix("/demo/", demo.Handler()))
	mux.HandleFunc("/health", r.handleHealth)
	mux.HandleFunc("/v1/models/catalog", r.handleModelsCatalog)
	mux.HandleFunc("/v1/engines/", r.handleEngines)
	mux.HandleFunc("/v1/gpu", r.handleGPU)
	mux.HandleFunc("/v1/jobs", r.handleJobs)
	mux.HandleFunc("/v1/jobs/", r.handleJob)
	mux.HandleFunc("/v1/library", r.handleLibrary)
	mux.HandleFunc("/v1/library/", r.handleLibraryItem)
	mux.HandleFunc("/v1/chat/completions", r.handleChatCompletions)
	mux.HandleFunc("/v1/images/generations", r.handleImageGenerations)
	mux.HandleFunc("/v1/images/descriptions", r.handleImageDescriptions)
	mux.HandleFunc("/v1/stories", r.handleStories)
	mux.HandleFunc("/v1/stories/draft", r.handleStoryDraft)
	mux.HandleFunc("/v1/stories/", r.handleStory)
	mux.HandleFunc("/v1/audio/speech", r.handleSpeech)
	mux.HandleFunc("/v1/audio/transcriptions", r.handleTranscriptions)
	mux.HandleFunc("/v1/voice", r.handleVoice)
	mux.HandleFunc("/v1/voices", r.handleVoices)
	mux.HandleFunc("/v1/voices/design", r.handleVoiceDesign)
	mux.HandleFunc("/v1/voices/", r.handleVoiceClone)
	return mux
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
// without a models block serves an empty catalog rather than erroring.
func (r *router) handleModelsCatalog(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodGet) {
		return
	}
	statuses := r.catalog.Statuses(r.modelsRoot)
	if statuses == nil {
		statuses = []models.Status{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"root":   r.modelsRoot,
		"models": statuses,
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

// handleGPU reports overall GPU memory via nvidia-smi when available, so the
// Engines tab can show the VRAM effect of profile changes. Machines without
// nvidia-smi report available:false rather than erroring.
func (r *router) handleGPU(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodGet) {
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=name,memory.total,memory.used", "--format=csv,noheader,nounits").Output()
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"available": false})
		return
	}
	type gpuInfo struct {
		Name     string `json:"name"`
		TotalMiB int    `json:"totalMiB"`
		UsedMiB  int    `json:"usedMiB"`
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
	_ = json.NewEncoder(w).Encode(map[string]any{"available": len(gpus) > 0, "gpus": gpus})
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
	engineCfg, ok := r.engine("audio")
	if !ok {
		return nil, &engine.Error{Kind: engine.KindNotConfigured, Message: `engine "audio" is not configured`}
	}
	if engineCfg.Mode != "server" {
		spec := engine.SpeechVoiceSpec(text, clonedVoice)
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
		release, ok := r.engines.Reserve("audio")
		if !ok {
			return nil, &engine.Error{Kind: engine.KindBusy, Message: `engine "audio" is busy`}
		}
		defer release()
	}
	upstreamURL, ok := inferEngineURL(engineCfg.HealthURL, "/v1/audio/speech")
	if !ok {
		return nil, &engine.Error{Kind: engine.KindNotConfigured, Message: `engine "audio" healthUrl must end in /health to infer /v1/audio/speech`}
	}
	refPath := engineCfg.DefaultVoiceRef
	refText := engineCfg.DefaultVoiceText
	if clonedVoice != nil {
		refPath = clonedVoice.RefWAVPath
		refText = clonedVoice.RefText
	}
	if refPath == "" {
		return nil, &engine.Error{Kind: engine.KindNotConfigured, Message: `server-mode engine "audio" needs defaultVoiceRef configured`}
	}
	payload, err := json.Marshal(audioServerSpeechRequest{
		Model:         audioServerModelID,
		Input:         text,
		VoiceRef:      filepath.ToSlash(refPath),
		ReferenceText: refText,
	})
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
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("audio upstream request failed: %v", err)}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, engine.MaxSpeechOutputBytes+1))
	if err != nil {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("read audio upstream response: %v", err)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("audio upstream returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))}
	}
	if int64(len(data)) > engine.MaxSpeechOutputBytes {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: "audio upstream produced an oversized WAV"}
	}
	if err := wav.ValidateBytes(data); err != nil {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("audio upstream produced an invalid WAV: %v", err)}
	}
	r.manager.MarkSuccess("audio")
	return data, nil
}

type audioServerSpeechRequest struct {
	Model         string `json:"model"`
	Input         string `json:"input"`
	VoiceRef      string `json:"voice_ref"`
	ReferenceText string `json:"reference_text,omitempty"`
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

	png, err := r.generateImage(req.Context(), body.Prompt, width, height)
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
	})
}

// generateImage produces a PNG for the prompt. Subprocess sd crosses the
// engine seam (sd-cli reloads the model each run); server-mode sd posts to the
// resident sd-server's OpenAI-compatible /v1/images/generations route so the
// ~2.8GB model stays loaded between requests, mirroring the resident whisper
// and audio proxies. Either way the returned bytes satisfy the same PNG caps.
func (r *router) generateImage(ctx context.Context, prompt string, width, height int) ([]byte, error) {
	engineCfg, ok := r.engine("sd")
	if !ok {
		return nil, &engine.Error{Kind: engine.KindNotConfigured, Message: `engine "sd" is not configured`}
	}
	if engineCfg.Mode != "server" {
		res, err := r.engines.Run(ctx, engine.ImageSpec(prompt, width, height))
		if err != nil {
			return nil, err
		}
		return res.Output, nil
	}

	upstreamURL, ok := inferImageGenerationsURL(engineCfg.HealthURL)
	if !ok {
		return nil, &engine.Error{Kind: engine.KindNotConfigured, Message: `engine "sd" healthUrl must be an absolute http(s) URL to infer /v1/images/generations`}
	}

	// Build the upstream body explicitly rather than reusing
	// imageGenerationRequest: its N field has no omitempty, so an unset N
	// marshals to "n":null, which sd-server's JSON parser fatally rejects
	// (fast-fail / 0xc0000409). Only send the fields sd-server needs.
	upstreamBody := struct {
		Prompt         string `json:"prompt"`
		Size           string `json:"size,omitempty"`
		ResponseFormat string `json:"response_format"`
	}{Prompt: prompt, ResponseFormat: "b64_json"}
	if width > 0 && height > 0 {
		upstreamBody.Size = fmt.Sprintf("%dx%d", width, height)
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
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxImageUpstreamBytes))
	if err != nil {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("read sd upstream response: %v", err)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("sd upstream returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))}
	}

	var parsed struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("decode sd upstream response: %v", err)}
	}
	if len(parsed.Data) == 0 || parsed.Data[0].B64JSON == "" {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: "sd upstream returned no image data"}
	}
	pngBytes, err := base64.StdEncoding.DecodeString(parsed.Data[0].B64JSON)
	if err != nil {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("decode sd upstream image: %v", err)}
	}
	if err := engine.ValidatePNGBytes(pngBytes); err != nil {
		return nil, &engine.Error{Kind: engine.KindEngineFailure, Message: fmt.Sprintf("sd upstream produced invalid PNG: %v", err)}
	}
	r.manager.MarkSuccess("sd")
	return pngBytes, nil
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
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(voiceListResponse{Voices: voiceSummaries(clones)})
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
		clone, err := r.voices.Save(name, transcript, data, req.FormValue("protected") == "true")
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(voiceSummary(clone))
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
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
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

func voiceSummaries(clones []voice.Clone) []voiceCloneSummary {
	summaries := make([]voiceCloneSummary, 0, len(clones))
	for _, clone := range clones {
		summaries = append(summaries, voiceSummary(clone))
	}
	return summaries
}

func voiceSummary(clone voice.Clone) voiceCloneSummary {
	return voiceCloneSummary{
		ID:         clone.ID,
		Name:       clone.Name,
		Transcript: clone.Transcript,
		CreatedAt:  clone.CreatedAt,
		Protected:  clone.Protected,
		AudioURL:   "/v1/voices/" + clone.ID + "/audio",
	}
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
		if !requireMethod(w, req, http.MethodGet) {
			return
		}
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
	if len(parts) == 3 && parts[0] != "" && parts[1] == "artifact" && parts[2] != "" {
		if !requireMethod(w, req, http.MethodGet) {
			return
		}
		path, err := r.stories.ArtifactPath(parts[0], parts[2])
		if err != nil {
			writeStoryErrorFromError(w, err)
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		http.ServeFile(w, req, path)
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

// storyScriptSystemPrompt instructs llama to write grounded audio stories
// as dialogue scripts. The fixture chat server keys on the phrase "audio
// stories as dialogue scripts" to return its canned script.
const storyScriptSystemPrompt = `You write short, factual audio stories as dialogue scripts. Reply with ONLY a JSON object, no markdown, in this exact shape: {"title": "...", "script": [{"speaker_id": "...", "text": "...", "fact_ids": ["fact-1"]}]}
Rules:
%s- Every line must cite at least one fact id from the provided fact list in fact_ids, and may only state things those cited facts support. Do not invent information.
- text is plain spoken language: one to three short sentences, no markdown, no stage directions, no emojis.
- Give the story a beginning, a middle, and an ending line that lands.`

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

// writeStoryScript is the story manager's ScriptFunc: facts in, a grounded
// script out of llama, with one retry that feeds the validation error back.
func (r *router) writeStoryScript(ctx context.Context, req story.ScriptRequest) (string, []story.ScriptLine, error) {
	lines := req.TargetSeconds / 7
	if lines < 8 {
		lines = 8
	}
	if lines > 40 {
		lines = 40
	}
	var prompt strings.Builder
	fmt.Fprintf(&prompt, "Subject: %s\nTarget length: about %d spoken lines (%d seconds of audio).\nFacts:\n", req.Subject, lines, req.TargetSeconds)
	for _, fact := range req.Facts {
		if fact.Conflicting {
			continue
		}
		fmt.Fprintf(&prompt, "%s: %s\n", fact.ID, fact.Claim)
	}

	messages := []chatMessage{
		{Role: "system", Content: fmt.Sprintf(storyScriptSystemPrompt, storyCastRules(req.Cast))},
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
		return "", nil, story.NewError(story.CodeGroundingFailure, "story scripting failed: "+retryErr.Error())
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
	for i, line := range decoded.Script {
		if !speakers[line.SpeakerID] {
			return "", nil, fmt.Errorf("script line %d uses unknown speaker %q", i+1, line.SpeakerID)
		}
		if strings.TrimSpace(line.Text) == "" {
			return "", nil, fmt.Errorf("script line %d has no text", i+1)
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

// inferImageGenerationsURL derives sd-server's request route from its
// healthUrl's origin. Unlike the /health-suffixed engines, sd-server has no
// /health route (readiness is polled at /v1/models), so the path is taken from
// the origin rather than by stripping a /health suffix.
func inferImageGenerationsURL(healthURL string) (string, bool) {
	if healthURL == "" {
		return "", false
	}
	parsed, err := url.Parse(healthURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: "/v1/images/generations"}).String(), true
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
	case story.CodeNotFound, story.CodeArtifactNotFound, story.CodeUnsupportedArtifact, story.CodeInvalidArtifactRequest:
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
}

type imageGenerationResponse struct {
	Created int64                 `json:"created"`
	Data    []imageGenerationData `json:"data"`
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

type voiceCloneSummary struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Transcript string    `json:"transcript"`
	CreatedAt  time.Time `json:"created_at"`
	Protected  bool      `json:"protected,omitempty"`
	AudioURL   string    `json:"audio_url"`
}

type voiceListResponse struct {
	Voices []voiceCloneSummary `json:"voices"`
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
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
