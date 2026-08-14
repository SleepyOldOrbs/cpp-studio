package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cpp-studio/internal/config"
	"cpp-studio/internal/wav"
)

const (
	maxResidentJSONBytes = 64 * 1024 * 1024
	maxChatReplyBytes    = 1024 * 1024
	audioServerModelID   = "tts"
)

// ChatMessage is one OpenAI-shaped resident chat turn.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// TranscriptSegment is one timestamped span returned by resident Whisper.
type TranscriptSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

func withResidentSpeech(spec Spec, request SynthesisRequest, voice *Voice) Spec {
	spec.speechRequest = &request
	spec.speechVoice = voice
	spec.resident = func(ctx context.Context, runner *Runner, cfg config.EngineConfig) (Result, error) {
		selectedVoice := voice
		if selectedVoice == nil {
			selectedVoice = runner.defaultSpeechVoice(request.EngineID)
		}
		if (selectedVoice == nil || selectedVoice.RefWAVPath == "") && !SpeechEngineAllowsTextOnly(request.EngineID) {
			return Result{}, &Error{Kind: KindNotConfigured, Message: fmt.Sprintf("server-mode engine %q needs defaultVoiceRef configured", request.EngineID)}
		}
		payload, err := MarshalSpeechServerRequest(audioServerModelID, request, voice, runner.defaultSpeechVoice(request.EngineID))
		if err != nil {
			return Result{}, internalError("encode speech request", err)
		}
		endpoint, ok := inferHealthRoute(cfg.HealthURL, "/v1/audio/speech")
		if !ok {
			return Result{}, notConfigured(request.EngineID, "healthUrl must end in /health to infer /v1/audio/speech")
		}
		data, err := runner.do(ctx, cfg, DefaultSpeechTimeout, http.MethodPost, endpoint, "application/json", payload, MaxSpeechOutputBytes+1)
		if err != nil {
			return Result{}, err
		}
		if int64(len(data)) > MaxSpeechOutputBytes {
			return Result{}, engineFailure(request.EngineID + " upstream produced an oversized WAV")
		}
		if err := wav.ValidateBytes(data); err != nil {
			return Result{}, engineFailure(fmt.Sprintf("%s upstream produced an invalid WAV: %v", request.EngineID, err))
		}
		return Result{Output: data}, nil
	}
	return spec
}

func (r *Runner) defaultSpeechVoice(engineName string) *Voice {
	voiceFrom := func(cfg config.EngineConfig) *Voice {
		if cfg.DefaultVoiceRef == "" && cfg.DefaultVoiceText == "" {
			return nil
		}
		return &Voice{RefWAVPath: cfg.DefaultVoiceRef, RefText: cfg.DefaultVoiceText}
	}
	if cfg, ok := r.engines[engineName]; ok {
		if voice := voiceFrom(cfg); voice != nil {
			return voice
		}
	}
	if engineName != DefaultSpeechEngineID {
		if cfg, ok := r.engines[DefaultSpeechEngineID]; ok {
			return voiceFrom(cfg)
		}
	}
	return nil
}

func withResidentTranscription(spec Spec, segments bool) Spec {
	spec.resident = func(ctx context.Context, runner *Runner, cfg config.EngineConfig) (Result, error) {
		if err := wav.ValidateBytes(spec.Input); err != nil {
			return Result{}, &Error{Kind: KindInvalidInput, Message: err.Error()}
		}
		endpoint, ok := inferHealthRoute(cfg.HealthURL, "/inference")
		if !ok {
			return Result{}, notConfigured(spec.Engine, "healthUrl must end in /health to infer /inference")
		}
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", "input.wav")
		if err == nil {
			_, err = part.Write(spec.Input)
		}
		format := "json"
		if segments {
			format = "verbose_json"
		}
		if err == nil {
			err = writer.WriteField("response_format", format)
		}
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return Result{}, internalError("encode transcription request", err)
		}
		data, err := runner.do(ctx, cfg, DefaultTranscriptionTimeout, http.MethodPost, endpoint, writer.FormDataContentType(), body.Bytes(), maxChatReplyBytes)
		if err != nil {
			return Result{}, err
		}
		if segments {
			cleaned, err := parseResidentSegments(data)
			if err != nil {
				return Result{}, err
			}
			encoded, _ := json.Marshal(cleaned)
			return Result{Stdout: encoded}, nil
		}
		var parsed struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(data, &parsed); err != nil {
			return Result{}, engineFailure(fmt.Sprintf("decode whisper upstream response: %v", err))
		}
		return Result{Stdout: []byte(strings.Join(strings.Fields(parsed.Text), " "))}, nil
	}
	return spec
}

// TranscriptionSegmentsSpec requests normalized timestamped Whisper output.
func TranscriptionSegmentsSpec(wavBytes []byte) Spec {
	spec := TranscriptionSpec(wavBytes)
	spec.Label = "whisper segment transcription"
	spec.residentOnly = true
	return withResidentTranscription(spec, true)
}

// ParseTranscriptSegments decodes the normalized output of TranscriptionSegmentsSpec.
func ParseTranscriptSegments(output []byte) ([]TranscriptSegment, error) {
	var segments []TranscriptSegment
	if err := json.Unmarshal(output, &segments); err != nil {
		return nil, fmt.Errorf("decode transcript segments: %w", err)
	}
	return segments, nil
}

func parseResidentSegments(data []byte) ([]TranscriptSegment, error) {
	var parsed struct {
		Segments []TranscriptSegment `json:"segments"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, engineFailure(fmt.Sprintf("decode whisper upstream response: %v", err))
	}
	out := make([]TranscriptSegment, 0, len(parsed.Segments))
	for _, segment := range parsed.Segments {
		segment.Text = strings.TrimSpace(segment.Text)
		if segment.Text != "" {
			out = append(out, segment)
		}
	}
	return out, nil
}

func withResidentImage(spec Spec, prompt string, width, height int, seed int64) Spec {
	spec.resident = func(ctx context.Context, runner *Runner, cfg config.EngineConfig) (Result, error) {
		endpoint, ok := inferOriginRoute(cfg.HealthURL, "/sdcpp/v1/img_gen")
		if !ok {
			return Result{}, notConfigured(spec.Engine, "healthUrl must be an absolute http(s) URL to infer /sdcpp/v1/img_gen")
		}
		request := struct {
			Prompt       string `json:"prompt"`
			Width        int    `json:"width,omitempty"`
			Height       int    `json:"height,omitempty"`
			Seed         int64  `json:"seed"`
			OutputFormat string `json:"output_format"`
		}{Prompt: prompt, Width: width, Height: height, Seed: seed, OutputFormat: "png"}
		payload, err := json.Marshal(request)
		if err != nil {
			return Result{}, internalError("encode image request", err)
		}
		ctx, cancel := context.WithTimeout(ctx, RequestTimeout(cfg, DefaultImageTimeout))
		defer cancel()
		data, err := runner.doContext(ctx, http.MethodPost, endpoint, "application/json", payload, maxResidentJSONBytes)
		if err != nil {
			return Result{}, err
		}
		var submitted struct {
			PollURL string `json:"poll_url"`
		}
		if json.Unmarshal(data, &submitted) != nil || submitted.PollURL == "" {
			return Result{}, engineFailure("sd upstream returned no job to poll: " + strings.TrimSpace(string(data)))
		}
		pollURL, ok := inferOriginRoute(cfg.HealthURL, submitted.PollURL)
		if !ok {
			return Result{}, engineFailure("sd upstream returned an unusable poll url")
		}
		for {
			select {
			case <-ctx.Done():
				return Result{}, engineFailure("sd generation timed out")
			case <-time.After(250 * time.Millisecond):
			}
			data, err = runner.doContext(ctx, http.MethodGet, pollURL, "", nil, maxResidentJSONBytes)
			if err != nil {
				return Result{}, err
			}
			var status struct {
				Status string `json:"status"`
				Result *struct {
					Images []struct {
						B64JSON string `json:"b64_json"`
					} `json:"images"`
				} `json:"result"`
				Error *struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(data, &status); err != nil {
				return Result{}, engineFailure(fmt.Sprintf("decode sd job status: %v", err))
			}
			switch status.Status {
			case "queued", "generating":
				continue
			case "completed":
				if status.Result == nil || len(status.Result.Images) == 0 || status.Result.Images[0].B64JSON == "" {
					return Result{}, engineFailure("sd job completed with no image data")
				}
				pngBytes, err := base64.StdEncoding.DecodeString(status.Result.Images[0].B64JSON)
				if err != nil {
					return Result{}, engineFailure(fmt.Sprintf("decode sd upstream image: %v", err))
				}
				if err := ValidatePNGBytes(pngBytes); err != nil {
					return Result{}, engineFailure(fmt.Sprintf("sd upstream produced invalid PNG: %v", err))
				}
				return Result{Output: pngBytes}, nil
			default:
				message := "sd job " + status.Status
				if status.Error != nil && status.Error.Message != "" {
					message += ": " + status.Error.Message
				}
				return Result{}, engineFailure(message)
			}
		}
	}
	return spec
}

// ChatSpec invokes a resident llama-compatible chat completion.
func ChatSpec(engineName string, messages []ChatMessage) Spec {
	return chatSpec(engineName, messages, 0)
}

// ChatWarmupSpec is the bounded one-token chat used after a model switch.
func ChatWarmupSpec(engineName string) Spec {
	spec := chatSpec(engineName, []ChatMessage{{Role: "user", Content: "hi"}}, 1)
	spec.Timeout = 5 * time.Minute
	return spec
}

func chatSpec(engineName string, messages []ChatMessage, maxTokens int) Spec {
	spec := Spec{Engine: engineName, Label: engineName + " chat completion", Timeout: 90 * time.Second, residentOnly: true}
	spec.resident = func(ctx context.Context, runner *Runner, cfg config.EngineConfig) (Result, error) {
		endpoint, ok := inferHealthRoute(cfg.HealthURL, "/v1/chat/completions")
		if !ok {
			return Result{}, notConfigured(engineName, "healthUrl must end in /health to infer /v1/chat/completions")
		}
		payload, err := json.Marshal(struct {
			Model     string        `json:"model"`
			Messages  []ChatMessage `json:"messages"`
			MaxTokens int           `json:"max_tokens,omitempty"`
		}{Model: "default", Messages: messages, MaxTokens: maxTokens})
		if err != nil {
			return Result{}, internalError("encode chat request", err)
		}
		data, err := runner.do(ctx, cfg, spec.Timeout, http.MethodPost, endpoint, "application/json", payload, maxChatReplyBytes)
		if err != nil {
			return Result{}, err
		}
		reply, err := parseChatReply(data)
		if err != nil {
			return Result{}, err
		}
		return Result{Stdout: []byte(reply)}, nil
	}
	return spec
}

// ChatProxySpec forwards one OpenAI-shaped request while keeping resident
// transport and status recording inside Engine invocation.
func ChatProxySpec(payload []byte) Spec {
	spec := Spec{Engine: "llama", Label: "llama chat proxy", Timeout: 90 * time.Second, residentOnly: true}
	spec.resident = func(ctx context.Context, runner *Runner, cfg config.EngineConfig) (Result, error) {
		endpoint, ok := inferHealthRoute(cfg.HealthURL, "/v1/chat/completions")
		if !ok {
			return Result{}, notConfigured("llama", "healthUrl must end in /health to infer /v1/chat/completions")
		}
		ctx, cancel := context.WithTimeout(ctx, RequestTimeout(cfg, spec.Timeout))
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return Result{}, internalError("create resident request", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := runner.client.Do(req)
		if err != nil {
			return Result{}, engineFailure(fmt.Sprintf("llama upstream request failed: %v", err))
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxChatReplyBytes))
		if err != nil {
			return Result{}, engineFailure(fmt.Sprintf("read llama upstream response: %v", err))
		}
		return Result{Output: body, StatusCode: resp.StatusCode, ContentType: resp.Header.Get("Content-Type")}, nil
	}
	return spec
}

// VisionSpec invokes a resident multimodal chat server with one PNG.
func VisionSpec(imageBytes []byte, instruction string) Spec {
	spec := Spec{Engine: "vision", Label: "vision image description", Timeout: 90 * time.Second, residentOnly: true}
	spec.resident = func(ctx context.Context, runner *Runner, cfg config.EngineConfig) (Result, error) {
		endpoint, ok := inferHealthRoute(cfg.HealthURL, "/v1/chat/completions")
		if !ok {
			return Result{}, notConfigured("vision", "healthUrl must end in /health to infer /v1/chat/completions")
		}
		request := map[string]any{"model": "default", "messages": []any{map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": instruction}, map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes)}}}}}}
		payload, err := json.Marshal(request)
		if err != nil {
			return Result{}, internalError("encode vision request", err)
		}
		data, err := runner.do(ctx, cfg, spec.Timeout, http.MethodPost, endpoint, "application/json", payload, maxChatReplyBytes)
		if err != nil {
			return Result{}, err
		}
		reply, err := parseChatReply(data)
		if err != nil {
			return Result{}, err
		}
		if reply == "" {
			return Result{}, engineFailure("vision engine returned no description")
		}
		return Result{Stdout: []byte(reply)}, nil
	}
	return spec
}

func parseChatReply(data []byte) (string, error) {
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Text string `json:"text"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", engineFailure(fmt.Sprintf("decode chat response: %v", err))
	}
	if len(parsed.Choices) == 0 {
		return "", engineFailure("chat response has no choices")
	}
	if content := strings.TrimSpace(parsed.Choices[0].Message.Content); content != "" {
		return content, nil
	}
	return strings.TrimSpace(parsed.Choices[0].Text), nil
}

func (r *Runner) do(ctx context.Context, cfg config.EngineConfig, fallback time.Duration, method, endpoint, contentType string, payload []byte, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout(cfg, fallback))
	defer cancel()
	return r.doContext(ctx, method, endpoint, contentType, payload, limit)
}

func (r *Runner) doContext(ctx context.Context, method, endpoint, contentType string, payload []byte, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, internalError("create resident request", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, engineFailure(fmt.Sprintf("resident upstream request failed: %v", err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, engineFailure(fmt.Sprintf("read resident upstream response: %v", err))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, engineFailure(fmt.Sprintf("resident upstream returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(data))))
	}
	return data, nil
}

func inferHealthRoute(healthURL, route string) (string, bool) {
	parsed, err := url.Parse(healthURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || !strings.HasSuffix(parsed.Path, "/health") {
		return "", false
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/health") + route
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), true
}

func inferOriginRoute(healthURL, route string) (string, bool) {
	parsed, err := url.Parse(healthURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: route}).String(), true
}

func notConfigured(engineName, detail string) error {
	return &Error{Kind: KindNotConfigured, Message: fmt.Sprintf("engine %q %s", engineName, detail)}
}
func internalError(prefix string, err error) error {
	return &Error{Kind: KindInternal, Message: fmt.Sprintf("%s: %v", prefix, err)}
}
func engineFailure(message string) error { return &Error{Kind: KindEngineFailure, Message: message} }
