package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cpp-studio/internal/config"
	"cpp-studio/internal/lifecycle"
)

func TestHealth(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"llama": {Command: "llama-server"},
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("expected JSON content type, got %q", contentType)
	}

	var health lifecycle.GatewayHealth
	if err := json.NewDecoder(rec.Body).Decode(&health); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if health.Status == "" {
		t.Fatalf("expected health status")
	}
	if _, ok := health.Engines["llama"]; !ok {
		t.Fatalf("expected llama engine in health: %+v", health)
	}
}

func TestDemoRoute(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": {Command: "audio-helper"},
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/demo/", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "cpp-studio voice loop") {
		t.Fatalf("expected demo HTML, got %q", body)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/demo/" {
		t.Fatalf("expected root redirect to /demo/, got %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestChatCompletionsProxy(t *testing.T) {
	var gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected upstream path %q", req.URL.Path)
		}
		if req.Method != http.MethodPost {
			t.Errorf("unexpected upstream method %q", req.Method)
		}
		if contentType := req.Header.Get("Content-Type"); contentType != "application/json" {
			t.Errorf("unexpected upstream content type %q", contentType)
		}
		data, err := io.ReadAll(req.Body)
		if err != nil {
			t.Errorf("read upstream body: %v", err)
		}
		gotBody = string(data)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test"}`))
	}))
	defer upstream.Close()

	cfg := testConfig(map[string]config.EngineConfig{
		"llama": {Command: "llama-server", HealthURL: upstream.URL + "/health"},
	})
	manager := lifecycle.NewManager(cfg)
	router := NewRouter(cfg, manager)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d: %s", rec.Code, rec.Body.String())
	}
	if gotBody != `{"messages":[]}` {
		t.Fatalf("unexpected proxied body %q", gotBody)
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("expected upstream content type to be copied, got %q", contentType)
	}
	if strings.TrimSpace(rec.Body.String()) != `{"id":"chatcmpl-test"}` {
		t.Fatalf("unexpected proxy body %q", rec.Body.String())
	}
	if manager.Health().Engines["llama"].LastSuccessAt == nil {
		t.Fatalf("expected llama lastSuccessAt to be recorded")
	}
}

func TestMissingEngineError(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": {Command: "audio-helper"},
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{}`))
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "llama") {
		t.Fatalf("expected missing llama detail, got %s", rec.Body.String())
	}
}

func TestMethodEnforcement(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"llama": {Command: "llama-server"},
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected status 405, got %d: %s", rec.Code, rec.Body.String())
	}
	if allow := rec.Header().Get("Allow"); allow != http.MethodGet {
		t.Fatalf("expected Allow GET, got %q", allow)
	}
}

func TestSpeechSuccess(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech"),
	})
	manager := lifecycle.NewManager(cfg)
	router := NewRouter(cfg, manager)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"hello","voice":"default","format":"wav"}`))
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "audio/wav") {
		t.Fatalf("expected audio/wav content type, got %q", contentType)
	}
	if rec.Body.String() != "RIFFtestWAVE" {
		t.Fatalf("unexpected wav bytes %q", rec.Body.String())
	}
	if manager.Health().Engines["audio"].LastSuccessAt == nil {
		t.Fatalf("expected audio lastSuccessAt to be recorded")
	}
}

func TestImageGenerationSuccess(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"sd": helperEngine("image-require-size"),
	})
	manager := lifecycle.NewManager(cfg)
	router := NewRouter(cfg, manager)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"prompt":"a small cabin","size":"512x512","response_format":"b64_json"}`))
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("expected JSON content type, got %q", contentType)
	}

	var response imageGenerationResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode image generation response: %v", err)
	}
	if response.Created <= 0 {
		t.Fatalf("expected created timestamp, got %d", response.Created)
	}
	if len(response.Data) != 1 || response.Data[0].B64JSON == "" {
		t.Fatalf("expected one b64_json image, got %+v", response)
	}
	image, err := base64.StdEncoding.DecodeString(response.Data[0].B64JSON)
	if err != nil {
		t.Fatalf("decode b64_json: %v", err)
	}
	if !bytes.Equal(image, validPNGBytes()) {
		t.Fatalf("unexpected png bytes %v", image)
	}
	if manager.Health().Engines["sd"].LastSuccessAt == nil {
		t.Fatalf("expected sd lastSuccessAt to be recorded")
	}
}

func TestImageGenerationMissingEngine(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"prompt":"a small cabin"}`))
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "sd") {
		t.Fatalf("expected missing sd detail, got %s", rec.Body.String())
	}
}

func TestImageGenerationBadRequest(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "missing prompt",
			body: `{"size":"512x512","response_format":"b64_json"}`,
			want: "prompt",
		},
		{
			name: "unsupported response format",
			body: `{"prompt":"a small cabin","response_format":"url"}`,
			want: "response_format",
		},
		{
			name: "invalid size",
			body: `{"prompt":"a small cabin","size":"wide"}`,
			want: "WIDTHxHEIGHT",
		},
		{
			name: "oversized size",
			body: `{"prompt":"a small cabin","size":"4096x4096"}`,
			want: "at most",
		},
		{
			name: "zero n",
			body: `{"prompt":"a small cabin","n":0}`,
			want: "n=1",
		},
		{
			name: "multiple n",
			body: `{"prompt":"a small cabin","n":2}`,
			want: "n=1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig(map[string]config.EngineConfig{
				"sd": helperEngine("image"),
			})
			router := NewRouter(cfg, lifecycle.NewManager(cfg))

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(tt.body))
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.want) {
				t.Fatalf("expected %q detail, got %s", tt.want, rec.Body.String())
			}
		})
	}
}

func TestImageGenerationRejectsInvalidGeneratedPNGAndMarksFailure(t *testing.T) {
	tests := []struct {
		name   string
		helper string
	}{
		{name: "bad signature", helper: "image-invalid"},
		{name: "corrupt image data", helper: "image-corrupt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig(map[string]config.EngineConfig{
				"sd": helperEngine(tt.helper),
			})
			manager := lifecycle.NewManager(cfg)
			router := NewRouter(cfg, manager)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"prompt":"a small cabin"}`))
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadGateway {
				t.Fatalf("expected status 502, got %d: %s", rec.Code, rec.Body.String())
			}
			health := manager.Health().Engines["sd"]
			if health.Status != lifecycle.StatusCrashed {
				t.Fatalf("expected crashed health status, got %+v", health)
			}
			if !strings.Contains(health.LastError, "invalid PNG") {
				t.Fatalf("expected invalid PNG health detail, got %+v", health)
			}
			if health.LastSuccessAt != nil {
				t.Fatalf("invalid image output should not mark success: %+v", health)
			}
		})
	}
}

func TestImageGenerationBusy(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"sd": helperEngine("image-slow"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"prompt":"a small cabin"}`))
		router.ServeHTTP(rec, req)
		done <- rec
	}()

	time.Sleep(50 * time.Millisecond)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"prompt":"a small cabin"}`))
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %d: %s", rec.Code, rec.Body.String())
	}
	first := <-done
	if first.Code != http.StatusOK {
		t.Fatalf("expected first request to complete, got %d: %s", first.Code, first.Body.String())
	}
}

func TestImageGenerationSubprocessFailure(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"sd": helperEngine("fail"),
	})
	manager := lifecycle.NewManager(cfg)
	router := NewRouter(cfg, manager)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"prompt":"a small cabin"}`))
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected status 502, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "partial stdout") || !strings.Contains(rec.Body.String(), "failure stderr") {
		t.Fatalf("expected stdout and stderr detail, got %s", rec.Body.String())
	}
	health := manager.Health().Engines["sd"]
	if health.Status != lifecycle.StatusCrashed || !strings.Contains(health.LastError, "failure stderr") {
		t.Fatalf("expected crashed health detail, got %+v", health)
	}
}

func TestSpeechBusy(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech-slow"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"hello","format":"wav"}`))
		router.ServeHTTP(rec, req)
		done <- rec
	}()

	time.Sleep(50 * time.Millisecond)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"hello","format":"wav"}`))
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %d: %s", rec.Code, rec.Body.String())
	}
	first := <-done
	if first.Code != http.StatusOK {
		t.Fatalf("expected first request to complete, got %d: %s", first.Code, first.Body.String())
	}
}

func TestSpeechRejectsInvalidGeneratedWAVAndMarksFailure(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech-invalid"),
	})
	manager := lifecycle.NewManager(cfg)
	router := NewRouter(cfg, manager)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"hello","format":"wav"}`))
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected status 502, got %d: %s", rec.Code, rec.Body.String())
	}
	health := manager.Health().Engines["audio"]
	if health.Status != lifecycle.StatusCrashed {
		t.Fatalf("expected crashed health status, got %+v", health)
	}
	if !strings.Contains(health.LastError, "invalid WAV") {
		t.Fatalf("expected invalid WAV health detail, got %+v", health)
	}
	if health.LastSuccessAt != nil {
		t.Fatalf("invalid speech output should not mark success: %+v", health)
	}
}

func TestSpeechSuccessClearsPreviousFailure(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "flaky-state")
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": {
			Command:               os.Args[0],
			Args:                  []string{"-test.run=TestGatewayHelperProcess", "--", "speech-flaky", marker},
			RequestTimeoutSeconds: 5,
			Mode:                  "subprocess",
		},
	})
	manager := lifecycle.NewManager(cfg)
	router := NewRouter(cfg, manager)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"hello","format":"wav"}`))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected first request status 502, got %d: %s", rec.Code, rec.Body.String())
	}
	if manager.Health().Engines["audio"].Status != lifecycle.StatusCrashed {
		t.Fatalf("expected first request to mark crashed")
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"hello","format":"wav"}`))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected second request status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	health := manager.Health().Engines["audio"]
	if health.Status != lifecycle.StatusReady || !health.Ready || health.LastError != "" || health.LastSuccessAt == nil {
		t.Fatalf("expected success to restore ready health, got %+v", health)
	}
}

func TestSpeechRejectsOversizedGeneratedWAV(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech-large"),
	})
	manager := lifecycle.NewManager(cfg)
	router := NewRouter(cfg, manager)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"hello","format":"wav"}`))
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected status 502, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "oversized") {
		t.Fatalf("expected oversized detail, got %s", rec.Body.String())
	}
	if manager.Health().Engines["audio"].Status != lifecycle.StatusCrashed {
		t.Fatalf("expected oversized output to mark crashed")
	}
}

func TestTranscriptionSuccess(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"whisper": helperEngine("transcribe"),
	})
	manager := lifecycle.NewManager(cfg)
	router := NewRouter(cfg, manager)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "sample.wav")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write(validWAVBytes()); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var response transcriptionResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode transcription response: %v", err)
	}
	if response.Text != "transcribed text" {
		t.Fatalf("unexpected transcription text %q", response.Text)
	}
	if response.DurationMS < 0 {
		t.Fatalf("unexpected negative duration %d", response.DurationMS)
	}
	if manager.Health().Engines["whisper"].LastSuccessAt == nil {
		t.Fatalf("expected whisper lastSuccessAt to be recorded")
	}
}

func TestTranscriptionBusy(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"whisper": helperEngine("transcribe-slow"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		req := multipartRequest(t, "sample.wav", validWAVBytes())
		router.ServeHTTP(rec, req)
		done <- rec
	}()

	time.Sleep(50 * time.Millisecond)
	rec := httptest.NewRecorder()
	req := multipartRequest(t, "sample.wav", validWAVBytes())
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %d: %s", rec.Code, rec.Body.String())
	}
	first := <-done
	if first.Code != http.StatusOK {
		t.Fatalf("expected first request to complete, got %d: %s", first.Code, first.Body.String())
	}
}

func TestTranscriptionRejectsUnsupportedAudio(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"whisper": helperEngine("transcribe"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "sample.txt")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write([]byte("not a wav")); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "WAV") {
		t.Fatalf("expected WAV validation detail, got %s", rec.Body.String())
	}
}

func TestSubprocessFailure(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("fail"),
	})
	manager := lifecycle.NewManager(cfg)
	router := NewRouter(cfg, manager)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"hello","format":"wav"}`))
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected status 502, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "partial stdout") || !strings.Contains(rec.Body.String(), "failure stderr") {
		t.Fatalf("expected stdout and stderr detail, got %s", rec.Body.String())
	}
	health := manager.Health().Engines["audio"]
	if health.Status != lifecycle.StatusCrashed || !strings.Contains(health.LastError, "failure stderr") {
		t.Fatalf("expected crashed health detail, got %+v", health)
	}
}

func TestGatewayHelperProcess(t *testing.T) {
	args := os.Args
	for i, arg := range args {
		if arg != "--" || i+1 >= len(args) {
			continue
		}
		mode := args[i+1]
		helperArgs := args[i+2:]
		switch mode {
		case "speech":
			runSpeechHelper(helperArgs)
		case "speech-slow":
			time.Sleep(500 * time.Millisecond)
			runSpeechHelper(helperArgs)
		case "speech-invalid":
			runInvalidSpeechHelper(helperArgs)
		case "speech-flaky":
			runFlakySpeechHelper(helperArgs)
		case "speech-large":
			runLargeSpeechHelper(helperArgs)
		case "image":
			runImageHelper(helperArgs, false)
		case "image-require-size":
			runImageHelper(helperArgs, true)
		case "image-slow":
			time.Sleep(500 * time.Millisecond)
			runImageHelper(helperArgs, false)
		case "image-invalid":
			runInvalidImageHelper(helperArgs)
		case "image-corrupt":
			runCorruptImageHelper(helperArgs)
		case "transcribe":
			runTranscriptionHelper(helperArgs)
		case "transcribe-slow":
			time.Sleep(500 * time.Millisecond)
			runTranscriptionHelper(helperArgs)
		case "fail":
			fmt.Fprintln(os.Stdout, "partial stdout")
			fmt.Fprintln(os.Stderr, "failure stderr")
			os.Exit(7)
		}
	}
}

func runInvalidSpeechHelper(args []string) {
	out := helperArg(args, "--out")
	if out == "" {
		fmt.Fprintln(os.Stderr, "missing --out")
		os.Exit(2)
	}
	if err := os.WriteFile(out, []byte("not a wav"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func runFlakySpeechHelper(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "missing marker path")
		os.Exit(2)
	}
	out := helperArg(args, "--out")
	if out == "" {
		fmt.Fprintln(os.Stderr, "missing --out")
		os.Exit(2)
	}
	marker := args[0]
	if _, err := os.Stat(marker); os.IsNotExist(err) {
		if err := os.WriteFile(marker, []byte("failed-once"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := os.WriteFile(out, []byte("not a wav"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	runSpeechHelper(args)
}

func runLargeSpeechHelper(args []string) {
	out := helperArg(args, "--out")
	if out == "" {
		fmt.Fprintln(os.Stderr, "missing --out")
		os.Exit(2)
	}
	file, err := os.Create(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if _, err := file.Write(validWAVBytes()); err != nil {
		_ = file.Close()
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := file.Truncate(maxSpeechOutputBytes + 1); err != nil {
		_ = file.Close()
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := file.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func runSpeechHelper(args []string) {
	text := helperArg(args, "--text")
	out := helperArg(args, "--out")
	if text == "" || out == "" {
		fmt.Fprintf(os.Stderr, "missing speech args text=%q out=%q\n", text, out)
		os.Exit(2)
	}
	if err := os.WriteFile(out, []byte("RIFFtestWAVE"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Fprintln(os.Stdout, "speech ok")
	os.Exit(0)
}

func runInvalidImageHelper(args []string) {
	out := helperArg(args, "--output")
	if out == "" {
		fmt.Fprintln(os.Stderr, "missing --output")
		os.Exit(2)
	}
	if err := os.WriteFile(out, []byte("not a png"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func runCorruptImageHelper(args []string) {
	out := helperArg(args, "--output")
	if out == "" {
		fmt.Fprintln(os.Stderr, "missing --output")
		os.Exit(2)
	}
	data := validPNGBytes()
	if err := os.WriteFile(out, data[:33], 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func runImageHelper(args []string, requireSize bool) {
	prompt := helperArg(args, "--prompt")
	out := helperArg(args, "--output")
	if prompt == "" || out == "" {
		fmt.Fprintf(os.Stderr, "missing image args prompt=%q output=%q\n", prompt, out)
		os.Exit(2)
	}
	if requireSize {
		width := helperArg(args, "--width")
		height := helperArg(args, "--height")
		if width != "512" || height != "512" {
			fmt.Fprintf(os.Stderr, "missing image size width=%q height=%q\n", width, height)
			os.Exit(2)
		}
	}
	if err := os.WriteFile(out, validPNGBytes(), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Fprintln(os.Stdout, "image ok")
	os.Exit(0)
}

func runTranscriptionHelper(args []string) {
	path := helperArg(args, "-f")
	if path == "" {
		fmt.Fprintln(os.Stderr, "missing -f")
		os.Exit(2)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(data) == 0 {
		fmt.Fprintln(os.Stderr, "empty input")
		os.Exit(2)
	}
	fmt.Fprintln(os.Stdout, "transcribed text")
	os.Exit(0)
}

func helperArg(args []string, key string) string {
	for i, arg := range args {
		if arg == key && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func helperEngine(mode string) config.EngineConfig {
	return config.EngineConfig{
		Command:               os.Args[0],
		Args:                  []string{"-test.run=TestGatewayHelperProcess", "--", mode},
		RequestTimeoutSeconds: 5,
	}
}

func TestCommandContextUsesRouteDefaultTimeout(t *testing.T) {
	ctx, cancel := commandContext(context.Background(), config.EngineConfig{}, 10*time.Millisecond)
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatalf("expected deadline")
	}
	if until := time.Until(deadline); until <= 0 || until > time.Second {
		t.Fatalf("unexpected default deadline %s", until)
	}
}

func validWAVBytes() []byte {
	return []byte{
		'R', 'I', 'F', 'F',
		0x24, 0x00, 0x00, 0x00,
		'W', 'A', 'V', 'E',
		'f', 'm', 't', ' ',
	}
}

func validPNGBytes() []byte {
	return []byte{
		0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n',
		0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R',
		0x00, 0x00, 0x00, 0x01,
		0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00,
		0x90, 0x77, 0x53, 0xde,
		0x00, 0x00, 0x00, 0x10, 'I', 'D', 'A', 'T',
		0x78, 0x9c, 0x62, 0xfa, 0xff, 0xff, 0x3f, 0x20,
		0x00, 0x00, 0xff, 0xff, 0x06, 0x06, 0x03, 0x00,
		0xb7, 0x66, 0x11, 0x21,
		0x00, 0x00, 0x00, 0x00, 'I', 'E', 'N', 'D',
		0xae, 0x42, 0x60, 0x82,
	}
}

func multipartRequest(t *testing.T, filename string, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func testConfig(engines map[string]config.EngineConfig) config.Config {
	return config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8765},
		Engines: engines,
	}
}
