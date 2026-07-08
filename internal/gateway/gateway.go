package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"cpp-studio/internal/config"
	"cpp-studio/internal/demo"
	"cpp-studio/internal/engine"
	"cpp-studio/internal/lifecycle"
	"cpp-studio/internal/story"
	"cpp-studio/internal/voice"
)

type router struct {
	cfg     config.Config
	manager *lifecycle.Manager
	client  *http.Client
	engines engine.Invoker
	stories *story.Manager
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
	mux.HandleFunc("/v1/voice", r.handleVoice)
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

	result, err := r.engines.Run(req.Context(), engine.SpeechSpec(body.Input))
	if err != nil {
		writeEngineError(w, err)
		return
	}

	w.Header().Set("Content-Type", "audio/wav")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Output)
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

	result, err := r.engines.Run(req.Context(), engine.ImageSpec(body.Prompt, width, height))
	if err != nil {
		writeEngineError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(imageGenerationResponse{
		Created: time.Now().Unix(),
		Data: []imageGenerationData{
			{B64JSON: base64.StdEncoding.EncodeToString(result.Output)},
		},
	})
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

	result, err := r.engines.Run(req.Context(), engine.TranscriptionSpec(data))
	if err != nil {
		writeEngineError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(transcriptionResponse{
		Text:       strings.TrimSpace(string(result.Stdout)),
		DurationMS: result.Elapsed.Milliseconds(),
	})
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

	loop := voice.Loop{Engines: r.engines, Chat: r.chatOnce}
	result, err := loop.Run(req.Context(), voice.Request{
		WAV:     wavBytes,
		Message: req.FormValue("message"),
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
		AudioB64:    base64.StdEncoding.EncodeToString(result.Audio),
	})
}

// chatOnce is the voice loop's ChatFunc: one user message in, the assistant
// reply out, via the llama server's /v1/chat/completions route.
func (r *router) chatOnce(ctx context.Context, message string) (string, error) {
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
		Messages: []chatMessage{{Role: "user", Content: message}},
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

func (r *router) engine(name string) (config.EngineConfig, bool) {
	engineCfg, ok := r.cfg.Engines[name]
	return engineCfg, ok
}

func (r *router) reserveEngine(ctx context.Context, name string) (func(), bool) {
	_ = ctx
	return r.engines.Reserve(name)
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
