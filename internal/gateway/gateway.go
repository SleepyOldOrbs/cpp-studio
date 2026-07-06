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
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"cpp-studio/internal/config"
	"cpp-studio/internal/demo"
	"cpp-studio/internal/lifecycle"
	"cpp-studio/internal/story"
)

type router struct {
	cfg     config.Config
	manager *lifecycle.Manager
	client  *http.Client
	busy    map[string]chan struct{}
	stories *story.Manager
}

const (
	defaultChatTimeout          = 120 * time.Second
	defaultTranscriptionTimeout = 120 * time.Second
	defaultSpeechTimeout        = 180 * time.Second
	defaultImageTimeout         = 300 * time.Second
	maxJSONBodyBytes            = 64 * 1024
	maxTranscriptionUploadBytes = 32 * 1024 * 1024
	maxSpeechOutputBytes        = 32 * 1024 * 1024
	maxImageOutputBytes         = 32 * 1024 * 1024
	maxImageDimension           = 2048
	maxImagePixels              = maxImageDimension * maxImageDimension
	maxSubprocessLogBytes       = 1024 * 1024
)

// NewRouter builds the cpp-studio gateway HTTP routes.
func NewRouter(cfg config.Config, manager *lifecycle.Manager) http.Handler {
	r := &router{
		cfg:     cfg,
		manager: manager,
		client:  http.DefaultClient,
		busy:    make(map[string]chan struct{}, len(cfg.Engines)),
	}
	for name := range cfg.Engines {
		r.busy[name] = make(chan struct{}, 1)
	}
	r.stories = story.NewManager(story.ManagerOptions{
		ReserveEngine: r.reserveEngine,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/", r.handleRoot)
	mux.HandleFunc("/demo", r.handleDemoRedirect)
	mux.Handle("/demo/", http.StripPrefix("/demo/", demo.Handler()))
	mux.HandleFunc("/health", r.handleHealth)
	mux.HandleFunc("/v1/chat/completions", r.handleChatCompletions)
	mux.HandleFunc("/v1/images/generations", r.handleImageGenerations)
	mux.HandleFunc("/v1/stories", r.handleStories)
	mux.HandleFunc("/v1/stories/", r.handleStory)
	mux.HandleFunc("/v1/audio/speech", r.handleSpeech)
	mux.HandleFunc("/v1/audio/transcriptions", r.handleTranscriptions)
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

func (r *router) handleChatCompletions(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodPost) {
		return
	}

	engine, ok := r.engine("llama")
	if !ok {
		writeJSONError(w, http.StatusServiceUnavailable, `engine "llama" is not configured`)
		return
	}
	upstreamURL, ok := inferChatCompletionsURL(engine.HealthURL)
	if !ok {
		writeJSONError(w, http.StatusServiceUnavailable, `engine "llama" healthUrl must end in /health to infer /v1/chat/completions`)
		return
	}

	ctx, cancel := requestContext(req.Context(), engine, defaultChatTimeout)
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

	engine, ok := r.engine("audio")
	if !ok {
		writeJSONError(w, http.StatusServiceUnavailable, `engine "audio" is not configured`)
		return
	}
	release, ok := r.acquire("audio")
	if !ok {
		writeJSONError(w, http.StatusTooManyRequests, `engine "audio" is busy`)
		return
	}
	defer release()

	out, err := os.CreateTemp("", "cpp-studio-speech-*.wav")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("create temp output: %v", err))
		return
	}
	outPath := out.Name()
	if err := out.Close(); err != nil {
		_ = os.Remove(outPath)
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("close temp output: %v", err))
		return
	}
	defer os.Remove(outPath)

	stdout, stderr, _, err := runEngineCommand(req.Context(), engine, defaultSpeechTimeout, "--text", body.Input, "--out", outPath)
	if err != nil {
		message := commandFailure("audio speech command failed", err, stdout, stderr)
		r.manager.MarkFailure("audio", lifecycle.StatusCrashed, message)
		writeJSONError(w, http.StatusBadGateway, message)
		return
	}
	if err := validateWAVFile(outPath); err != nil {
		message := fmt.Sprintf("audio speech command produced invalid WAV: %v", err)
		r.manager.MarkFailure("audio", lifecycle.StatusCrashed, message)
		writeJSONError(w, http.StatusBadGateway, message)
		return
	}
	if err := validateFileSize(outPath, maxSpeechOutputBytes, "generated wav"); err != nil {
		message := fmt.Sprintf("audio speech command produced oversized WAV: %v", err)
		r.manager.MarkFailure("audio", lifecycle.StatusCrashed, message)
		writeJSONError(w, http.StatusBadGateway, message)
		return
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		message := fmt.Sprintf("read generated wav: %v", err)
		r.manager.MarkFailure("audio", lifecycle.StatusCrashed, message)
		writeJSONError(w, http.StatusBadGateway, message)
		return
	}

	w.Header().Set("Content-Type", "audio/wav")
	r.manager.MarkSuccess("audio")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
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

	engine, ok := r.engine("sd")
	if !ok {
		writeJSONError(w, http.StatusServiceUnavailable, `engine "sd" is not configured`)
		return
	}
	release, ok := r.acquire("sd")
	if !ok {
		writeJSONError(w, http.StatusTooManyRequests, `engine "sd" is busy`)
		return
	}
	defer release()

	out, err := os.CreateTemp("", "cpp-studio-image-*.png")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("create temp output: %v", err))
		return
	}
	outPath := out.Name()
	if err := out.Close(); err != nil {
		_ = os.Remove(outPath)
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("close temp output: %v", err))
		return
	}
	defer os.Remove(outPath)

	engineArgs := []string{"--prompt", body.Prompt, "--output", outPath}
	if width > 0 && height > 0 {
		engineArgs = append(engineArgs, "--width", strconv.Itoa(width), "--height", strconv.Itoa(height))
	}
	stdout, stderr, _, err := runEngineCommand(req.Context(), engine, defaultImageTimeout, engineArgs...)
	if err != nil {
		message := commandFailure("sd image generation command failed", err, stdout, stderr)
		r.manager.MarkFailure("sd", lifecycle.StatusCrashed, message)
		writeJSONError(w, http.StatusBadGateway, message)
		return
	}
	if err := validateFileSize(outPath, maxImageOutputBytes, "generated png"); err != nil {
		message := fmt.Sprintf("sd image generation command produced oversized PNG: %v", err)
		r.manager.MarkFailure("sd", lifecycle.StatusCrashed, message)
		writeJSONError(w, http.StatusBadGateway, message)
		return
	}
	if err := validatePNGFile(outPath); err != nil {
		message := fmt.Sprintf("sd image generation command produced invalid PNG: %v", err)
		r.manager.MarkFailure("sd", lifecycle.StatusCrashed, message)
		writeJSONError(w, http.StatusBadGateway, message)
		return
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		message := fmt.Sprintf("read generated png: %v", err)
		r.manager.MarkFailure("sd", lifecycle.StatusCrashed, message)
		writeJSONError(w, http.StatusBadGateway, message)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	r.manager.MarkSuccess("sd")
	_ = json.NewEncoder(w).Encode(imageGenerationResponse{
		Created: time.Now().Unix(),
		Data: []imageGenerationData{
			{B64JSON: base64.StdEncoding.EncodeToString(data)},
		},
	})
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

func (r *router) handleTranscriptions(w http.ResponseWriter, req *http.Request) {
	if !requireMethod(w, req, http.MethodPost) {
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxTranscriptionUploadBytes)

	file, header, err := req.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "multipart field file is required")
		return
	}
	defer file.Close()
	if header == nil || header.Filename == "" {
		writeJSONError(w, http.StatusBadRequest, "multipart field file must include a filename")
		return
	}

	engine, ok := r.engine("whisper")
	if !ok {
		writeJSONError(w, http.StatusServiceUnavailable, `engine "whisper" is not configured`)
		return
	}
	release, ok := r.acquire("whisper")
	if !ok {
		writeJSONError(w, http.StatusTooManyRequests, `engine "whisper" is busy`)
		return
	}
	defer release()

	in, err := os.CreateTemp("", "cpp-studio-transcription-*")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("create temp input: %v", err))
		return
	}
	inPath := in.Name()
	defer os.Remove(inPath)

	if _, err := io.Copy(in, file); err != nil {
		_ = in.Close()
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("save uploaded file: %v", err))
		return
	}
	if err := in.Close(); err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("close temp input: %v", err))
		return
	}
	if err := validateWAVFile(inPath); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	stdout, stderr, elapsed, err := runEngineCommand(req.Context(), engine, defaultTranscriptionTimeout, "-f", inPath)
	if err != nil {
		message := commandFailure("whisper transcription command failed", err, stdout, stderr)
		r.manager.MarkFailure("whisper", lifecycle.StatusCrashed, message)
		writeJSONError(w, http.StatusBadGateway, message)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	r.manager.MarkSuccess("whisper")
	_ = json.NewEncoder(w).Encode(transcriptionResponse{
		Text:       strings.TrimSpace(string(stdout)),
		DurationMS: elapsed.Milliseconds(),
	})
}

func (r *router) engine(name string) (config.EngineConfig, bool) {
	engine, ok := r.cfg.Engines[name]
	return engine, ok
}

func (r *router) acquire(name string) (func(), bool) {
	lock, ok := r.busy[name]
	if !ok {
		return func() {}, true
	}
	select {
	case lock <- struct{}{}:
		return func() { <-lock }, true
	default:
		return nil, false
	}
}

func (r *router) reserveEngine(ctx context.Context, name string) (func(), bool) {
	_ = ctx
	return r.acquire(name)
}

func inferChatCompletionsURL(healthURL string) (string, bool) {
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

	parsed.Path = strings.TrimSuffix(parsed.Path, "/health") + "/v1/chat/completions"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), true
}

func runEngineCommand(ctx context.Context, engine config.EngineConfig, fallback time.Duration, extraArgs ...string) ([]byte, []byte, time.Duration, error) {
	ctx, cancel := commandContext(ctx, engine, fallback)
	defer cancel()

	args := append([]string{}, engine.Args...)
	args = append(args, extraArgs...)
	cmd := exec.CommandContext(ctx, engine.Command, args...)
	cmd.Dir = engine.WorkingDir

	stdout := newLimitedBuffer(maxSubprocessLogBytes)
	stderr := newLimitedBuffer(maxSubprocessLogBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	started := time.Now()
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), time.Since(started), err
}

func requestContext(ctx context.Context, engine config.EngineConfig, fallback time.Duration) (context.Context, context.CancelFunc) {
	return commandContext(ctx, engine, fallback)
}

func commandContext(ctx context.Context, engine config.EngineConfig, fallback time.Duration) (context.Context, context.CancelFunc) {
	if engine.RequestTimeoutSeconds <= 0 {
		return context.WithTimeout(ctx, fallback)
	}
	return context.WithTimeout(ctx, time.Duration(engine.RequestTimeoutSeconds)*time.Second)
}

func validateWAVFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open uploaded audio: %v", err)
	}
	defer file.Close()

	header := make([]byte, 12)
	if _, err := io.ReadFull(file, header); err != nil {
		return fmt.Errorf("unsupported audio file: expected WAV RIFF header")
	}
	if string(header[0:4]) != "RIFF" || string(header[8:12]) != "WAVE" {
		return fmt.Errorf("unsupported audio file: expected WAV RIFF header")
	}
	return nil
}

func validatePNGFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open generated image: %v", err)
	}
	defer file.Close()

	header := make([]byte, 8)
	if _, err := io.ReadFull(file, header); err != nil {
		return fmt.Errorf("unsupported image file: expected PNG signature")
	}
	pngSignature := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	if !bytes.Equal(header, pngSignature) {
		return fmt.Errorf("unsupported image file: expected PNG signature")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind generated image: %v", err)
	}
	cfg, err := png.DecodeConfig(file)
	if err != nil {
		return fmt.Errorf("decode PNG metadata: %v", err)
	}
	if err := validateImageDimensions(cfg.Width, cfg.Height); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind generated image: %v", err)
	}
	if _, err := png.Decode(file); err != nil {
		return fmt.Errorf("decode PNG image: %v", err)
	}
	return nil
}

func validateFileSize(path string, maxBytes int64, label string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %v", label, err)
	}
	if info.Size() > maxBytes {
		return fmt.Errorf("%s is %d bytes, max is %d bytes", label, info.Size(), maxBytes)
	}
	return nil
}

func commandFailure(prefix string, err error, stdout []byte, stderr []byte) string {
	parts := []string{fmt.Sprintf("%s: %v", prefix, err)}
	if out := strings.TrimSpace(string(stdout)); out != "" {
		parts = append(parts, "stdout: "+out)
	}
	if out := strings.TrimSpace(string(stderr)); out != "" {
		parts = append(parts, "stderr: "+out)
	}
	return strings.Join(parts, "; ")
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
	if err := validateImageDimensions(width, height); err != nil {
		return 0, 0, err
	}
	return width, height, nil
}

func validateImageDimensions(width int, height int) error {
	if width <= 0 || height <= 0 {
		return fmt.Errorf("image dimensions must be positive")
	}
	if width > maxImageDimension || height > maxImageDimension {
		return fmt.Errorf("image dimensions must be at most %dx%d", maxImageDimension, maxImageDimension)
	}
	if width > maxImagePixels/height {
		return fmt.Errorf("image dimensions must contain at most %d pixels", maxImagePixels)
	}
	return nil
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

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int64
	truncated bool
}

func newLimitedBuffer(limit int64) *limitedBuffer {
	return &limitedBuffer{limit: limit}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if int64(b.buf.Len()) < b.limit {
		remaining := b.limit - int64(b.buf.Len())
		if int64(len(p)) > remaining {
			_, _ = b.buf.Write(p[:remaining])
			b.truncated = true
			return len(p), nil
		}
		_, _ = b.buf.Write(p)
		return len(p), nil
	}
	b.truncated = true
	return len(p), nil
}

func (b *limitedBuffer) Bytes() []byte {
	if !b.truncated {
		return b.buf.Bytes()
	}
	out := append([]byte{}, b.buf.Bytes()...)
	out = append(out, []byte("\n[truncated]")...)
	return out
}
