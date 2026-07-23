package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	stdpng "image/png"
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
	"cpp-studio/internal/engine"
	"cpp-studio/internal/lifecycle"
	"cpp-studio/internal/story"
	"cpp-studio/internal/wav"
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
	body := rec.Body.String()
	if !strings.Contains(body, "cpp-studio local studio") {
		t.Fatalf("expected demo HTML, got %q", body)
	}
	if !strings.Contains(body, "/v1/images/generations") {
		t.Fatalf("expected image route marker, got %q", body)
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

func TestStoryCreateStatusArtifactAndList(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var create story.CreateResponse
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/stories", strings.NewReader(validStoryRequestJSON()))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&create); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if create.ID == "" || create.StatusURL != "/v1/stories/"+create.ID {
		t.Fatalf("unexpected create response %+v", create)
	}

	status := waitGatewayStoryStatus(t, router, create.ID, story.StatusComplete)
	if status.Manifest == nil {
		t.Fatalf("expected completed manifest, got %+v", status)
	}
	if status.Manifest.Subject != "how stars are born" {
		t.Fatalf("unexpected story subject %q", status.Manifest.Subject)
	}
	if len(status.Manifest.FactCards) != 9 {
		t.Fatalf("expected fixture fact cards, got %+v", status.Manifest.FactCards)
	}
	if len(status.Manifest.Script) < 10 {
		t.Fatalf("expected richer fixture script, got %d lines", len(status.Manifest.Script))
	}
	if status.Manifest.DurationSeconds != 90 {
		t.Fatalf("expected 90 second fixture story, got %d", status.Manifest.DurationSeconds)
	}
	for i, line := range status.Manifest.Script {
		if len(line.FactIDs) == 0 {
			t.Fatalf("script line %d has no fact ids", i)
		}
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/stories/"+create.ID+"/artifact/story.wav", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected artifact status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "audio/wav") {
		t.Fatalf("expected audio/wav content type, got %q", contentType)
	}
	if body := rec.Body.String(); len(body) < 12 || body[:4] != "RIFF" || body[8:12] != "WAVE" {
		t.Fatalf("expected WAV body, got %q", body)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/stories/"+create.ID+"/artifact/manifest.json", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected unknown artifact status 404, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/stories", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected list status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var list story.ListResponse
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode story list: %v", err)
	}
	if len(list.Stories) != 1 || list.Stories[0].ID != create.ID {
		t.Fatalf("unexpected story list %+v", list)
	}
}

func TestStoryScriptedByLlamaWithCastVoices(t *testing.T) {
	t.Chdir(t.TempDir())
	scriptJSON := `{"title": "The Llama Tale", "script": [
{"speaker_id": "narrator", "text": "An opening line.", "fact_ids": ["fact-1"]},
{"speaker_id": "nova", "text": "A question?", "fact_ids": ["fact-2"]},
{"speaker_id": "dr-lumen", "text": "An answer.", "fact_ids": ["fact-3"]},
{"speaker_id": "narrator", "text": "A closing line.", "fact_ids": ["fact-1"]}
]}`
	var sawScriptPrompt bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body chatCompletionRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		if len(body.Messages) > 0 && strings.Contains(body.Messages[0].Content, "audio stories as dialogue scripts") {
			sawScriptPrompt = true
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": scriptJSON}}},
		})
	}))
	defer upstream.Close()

	cfg := testConfig(map[string]config.EngineConfig{
		"llama": {Command: "llama-server", HealthURL: upstream.URL + "/health"},
		"audio": helperEngine("speech-tone-require-voice"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	// Store a voice for the cast.
	var createBody bytes.Buffer
	writer := multipart.NewWriter(&createBody)
	_ = writer.WriteField("name", "Story Voice")
	_ = writer.WriteField("transcript", "story voice reference words")
	part, err := writer.CreateFormFile("file", "reference.wav")
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
	req := httptest.NewRequest(http.MethodPost, "/v1/voices", &createBody)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected voice create status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var voiceCreated voiceCloneSummary
	if err := json.NewDecoder(rec.Body).Decode(&voiceCreated); err != nil {
		t.Fatalf("decode voice create response: %v", err)
	}

	// Unknown cast voices are rejected up front.
	badBody := strings.Replace(validStoryRequestJSON(), `"sources"`, `"cast_voices": {"narrator": "voice_20990101_000000_ffffff"}, "sources"`, 1)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/stories", strings.NewReader(badBody))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected unknown cast voice status 400, got %d: %s", rec.Code, rec.Body.String())
	}

	// Submit with every speaker on the stored voice; the audio helper fails
	// any line missing --voice-ref, proving per-line cast synthesis.
	storyBody := strings.Replace(validStoryRequestJSON(), `"voice_mode": "placeholder"`, `"voice_mode": "fixed"`, 1)
	storyBody = strings.Replace(storyBody, `"sources"`, `"cast_voices": {"narrator": "`+voiceCreated.ID+`", "nova": "`+voiceCreated.ID+`", "dr-lumen": "`+voiceCreated.ID+`"}, "sources"`, 1)
	var create story.CreateResponse
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/stories", strings.NewReader(storyBody))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&create); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	status := waitGatewayStoryStatus(t, router, create.ID, story.StatusComplete)
	if status.Manifest == nil {
		t.Fatalf("expected completed manifest, got %+v", status)
	}
	if status.Manifest.Title != "The Llama Tale" {
		t.Fatalf("expected llama-written title, got %q", status.Manifest.Title)
	}
	if len(status.Manifest.Script) != 4 || status.Manifest.Script[0].Text != "An opening line." {
		t.Fatalf("expected llama-written script, got %+v", status.Manifest.Script)
	}
	if !sawScriptPrompt {
		t.Fatalf("expected the story script prompt to reach llama")
	}
	for _, member := range status.Manifest.Cast {
		if member.VoiceID != voiceCreated.ID {
			t.Fatalf("cast %s: expected voice %q, got %q", member.ID, voiceCreated.ID, member.VoiceID)
		}
	}
}

func TestStoryDraftThenProduceEditedScript(t *testing.T) {
	t.Chdir(t.TempDir())
	scriptJSON := `{"title": "Draft Title", "script": [
{"speaker_id": "narrator", "text": "Draft opening.", "fact_ids": ["fact-1"]},
{"speaker_id": "nova", "text": "Draft question?", "fact_ids": ["fact-2"]},
{"speaker_id": "dr-lumen", "text": "Draft answer.", "fact_ids": ["fact-3"]},
{"speaker_id": "narrator", "text": "Draft close.", "fact_ids": ["fact-1"]}
]}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": scriptJSON}}},
		})
	}))
	defer upstream.Close()

	cfg := testConfig(map[string]config.EngineConfig{
		"llama": {Command: "llama-server", HealthURL: upstream.URL + "/health"},
		"audio": helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/stories/draft", strings.NewReader(validStoryRequestJSON()))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected draft status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var draft story.DraftResponse
	if err := json.NewDecoder(rec.Body).Decode(&draft); err != nil {
		t.Fatalf("decode draft: %v", err)
	}
	if draft.Title != "Draft Title" || len(draft.Script) != 4 || len(draft.FactCards) < 8 {
		t.Fatalf("unexpected draft %+v", draft)
	}

	// Edit a line and produce with the edited script.
	draft.Script[0].Text = "An edited opening line."
	edited, err := json.Marshal(draft.Script)
	if err != nil {
		t.Fatalf("marshal edited script: %v", err)
	}
	produceBody := strings.Replace(validStoryRequestJSON(), `"sources"`, `"title": "Edited Title", "script": `+string(edited)+`, "sources"`, 1)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/stories", strings.NewReader(produceBody))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected produce status 202, got %d: %s", rec.Code, rec.Body.String())
	}
	var create story.CreateResponse
	if err := json.NewDecoder(rec.Body).Decode(&create); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	status := waitGatewayStoryStatus(t, router, create.ID, story.StatusComplete)
	if status.Manifest == nil || status.Manifest.Title != "Edited Title" {
		t.Fatalf("expected edited title, got %+v", status.Manifest)
	}
	if status.Manifest.Script[0].Text != "An edited opening line." {
		t.Fatalf("expected edited line to survive production, got %+v", status.Manifest.Script[0])
	}
}

func TestStoryValidationError(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	body := `{
  "subject": "how stars are born",
  "source_mode": "curated",
  "voice_mode": "placeholder",
  "sources": [
    {"title":"one","url":"https://example.test/one"},
    {"title":"two","excerpt":"two excerpt"},
    {"title":"three","excerpt":"three excerpt"}
  ]
}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/stories", strings.NewReader(body))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Error story.StoryError `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if envelope.Error.Code != story.CodeMissingSourceExcerpt {
		t.Fatalf("expected missing excerpt code, got %+v", envelope.Error)
	}
}

func TestStoryBusyAndAudioReservation(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var create story.CreateResponse
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/stories", strings.NewReader(validStoryRequestJSON()))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&create); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/stories", strings.NewReader(validStoryRequestJSON()))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected busy story status 429, got %d: %s", rec.Code, rec.Body.String())
	}

	waitGatewayStoryActive(t, router, create.ID)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"hello","format":"wav"}`))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected direct speech to see reserved audio status 429, got %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/stories/"+create.ID+"/cancel", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected cancel status 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestStoryCreateReturnsBusyWhenAudioIsAlreadyReserved(t *testing.T) {
	t.Chdir(t.TempDir())
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
	req := httptest.NewRequest(http.MethodPost, "/v1/stories", strings.NewReader(validStoryRequestJSON()))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected story create to return 429 while audio is busy, got %d: %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Error story.StoryError `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode story error: %v", err)
	}
	if envelope.Error.Code != story.CodeEngineBusy {
		t.Fatalf("expected engine_busy error, got %+v", envelope.Error)
	}

	first := <-done
	if first.Code != http.StatusOK {
		t.Fatalf("expected direct speech to complete, got %d: %s", first.Code, first.Body.String())
	}
}

func TestStoryMalformedIDReturnsNotFoundEnvelope(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	for _, tt := range []struct {
		name   string
		method string
		path   string
	}{
		{name: "status", method: http.MethodGet, path: "/v1/stories/bad..id"},
		{name: "cancel", method: http.MethodPost, path: "/v1/stories/bad..id/cancel"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, nil)
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("expected status 404, got %d: %s", rec.Code, rec.Body.String())
			}
			var envelope struct {
				Error story.StoryError `json:"error"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&envelope); err != nil {
				t.Fatalf("decode story error: %v", err)
			}
			if envelope.Error.Code != story.CodeNotFound {
				t.Fatalf("expected not_found error, got %+v", envelope.Error)
			}
		})
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
		case "speech-tone":
			runToneSpeechHelper(helperArgs)
		case "design":
			runDesignHelper(helperArgs)
		case "speech-require-voice":
			runVoiceRequiredSpeechHelper(helperArgs)
		case "speech-tone-require-voice":
			runVoiceRequiredToneSpeechHelper(helperArgs)
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
	if err := file.Truncate(engine.MaxSpeechOutputBytes + 1); err != nil {
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

// runVoiceRequiredSpeechHelper fails unless the run carried a cloned voice:
// --voice-ref must point at a readable file and --reference-text must be
// non-empty.
func runVoiceRequiredSpeechHelper(args []string) {
	ref := helperArg(args, "--voice-ref")
	refText := helperArg(args, "--reference-text")
	if ref == "" || refText == "" {
		fmt.Fprintf(os.Stderr, "missing cloned voice args voice-ref=%q reference-text=%q\n", ref, refText)
		os.Exit(2)
	}
	if _, err := os.Stat(ref); err != nil {
		fmt.Fprintf(os.Stderr, "voice-ref not readable: %v\n", err)
		os.Exit(2)
	}
	runSpeechHelper(args)
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

// runDesignHelper mimics the VoiceDesign task: it fails unless both the
// instruction and sample text arrived, then writes a minimal WAV.
func runDesignHelper(args []string) {
	instruct := helperArg(args, "--instruct")
	text := helperArg(args, "--text")
	out := helperArg(args, "--out")
	if instruct == "" || text == "" || out == "" {
		fmt.Fprintf(os.Stderr, "missing design args instruct=%q text=%q out=%q\n", instruct, text, out)
		os.Exit(2)
	}
	if err := os.WriteFile(out, []byte("RIFFtestWAVE"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

// runVoiceRequiredToneSpeechHelper is runVoiceRequiredSpeechHelper but with
// decodable tone output, for flows that stitch the result.
func runVoiceRequiredToneSpeechHelper(args []string) {
	ref := helperArg(args, "--voice-ref")
	refText := helperArg(args, "--reference-text")
	if ref == "" || refText == "" {
		fmt.Fprintf(os.Stderr, "missing cloned voice args voice-ref=%q reference-text=%q\n", ref, refText)
		os.Exit(2)
	}
	runToneSpeechHelper(args)
}

// runToneSpeechHelper writes one second of decodable synthetic tone, for
// tests that assert on the produced audio rather than raw bytes.
func runToneSpeechHelper(args []string) {
	out := helperArg(args, "--out")
	if out == "" {
		fmt.Fprintln(os.Stderr, "missing --out")
		os.Exit(2)
	}
	if err := os.WriteFile(out, wav.SyntheticTone(wav.ToneSampleRate), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
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

func TestVoiceLoopWithAudioUpload(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected upstream path %q", req.URL.Path)
		}
		var body chatCompletionRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Errorf("decode upstream chat request: %v", err)
		}
		if len(body.Messages) != 2 || body.Messages[0].Role != "system" || body.Messages[0].Content != voiceSystemPrompt || body.Messages[1].Content != "transcribed text" {
			t.Errorf("unexpected upstream chat messages %+v", body.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"assistant says hi"}}]}`))
	}))
	defer upstream.Close()

	cfg := testConfig(map[string]config.EngineConfig{
		"whisper": helperEngine("transcribe"),
		"audio":   helperEngine("speech"),
		"llama":   {Command: "llama-server", HealthURL: upstream.URL + "/health"},
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
	req := httptest.NewRequest(http.MethodPost, "/v1/voice", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Transcript  string `json:"transcript"`
		Reply       string `json:"reply"`
		AudioFormat string `json:"audio_format"`
		AudioB64    string `json:"audio_b64"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode voice response: %v", err)
	}
	if response.Transcript != "transcribed text" {
		t.Fatalf("unexpected transcript %q", response.Transcript)
	}
	if response.Reply != "assistant says hi" {
		t.Fatalf("unexpected reply %q", response.Reply)
	}
	if response.AudioFormat != "wav" {
		t.Fatalf("unexpected audio format %q", response.AudioFormat)
	}
	audio, err := base64.StdEncoding.DecodeString(response.AudioB64)
	if err != nil {
		t.Fatalf("decode audio_b64: %v", err)
	}
	if string(audio) != "RIFFtestWAVE" {
		t.Fatalf("unexpected wav bytes %q", audio)
	}
	health := manager.Health().Engines
	for _, name := range []string{"whisper", "llama", "audio"} {
		if health[name].LastSuccessAt == nil {
			t.Fatalf("expected %s lastSuccessAt to be recorded", name)
		}
	}
}

func TestVoiceLoopSendsHistoryUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body chatCompletionRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Errorf("decode upstream chat request: %v", err)
		}
		if len(body.Messages) != 4 ||
			body.Messages[0].Role != "system" ||
			body.Messages[1].Role != "user" || body.Messages[1].Content != "earlier question" ||
			body.Messages[2].Role != "assistant" || body.Messages[2].Content != "earlier answer" ||
			body.Messages[3].Role != "user" || body.Messages[3].Content != "follow-up" {
			t.Errorf("unexpected upstream chat messages %+v", body.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"contextual reply"}}]}`))
	}))
	defer upstream.Close()

	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech"),
		"llama": {Command: "llama-server", HealthURL: upstream.URL + "/health"},
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("message", "follow-up")
	_ = writer.WriteField("history", `[{"role":"user","text":"earlier question"},{"role":"assistant","text":"earlier answer"}]`)
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/voice", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVoiceLoopRejectsBadHistory(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	tests := []struct {
		name    string
		history string
	}{
		{name: "not json", history: "not-json"},
		{name: "bad role", history: `[{"role":"system","text":"sneaky"}]`},
		{name: "empty text", history: `[{"role":"user","text":"  "}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			_ = writer.WriteField("message", "hello")
			_ = writer.WriteField("history", tt.history)
			if err := writer.Close(); err != nil {
				t.Fatalf("close multipart writer: %v", err)
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/voice", &body)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestTranscriptionsViaWhisperServer(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/inference" {
			t.Errorf("unexpected upstream path %q", req.URL.Path)
		}
		if err := req.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("parse upstream multipart: %v", err)
		}
		if req.FormValue("response_format") != "json" {
			t.Errorf("expected response_format json, got %q", req.FormValue("response_format"))
		}
		if _, _, err := req.FormFile("file"); err != nil {
			t.Errorf("expected file field: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":" served transcript\n with newlines\n"}`))
	}))
	defer upstream.Close()

	cfg := testConfig(map[string]config.EngineConfig{
		"whisper": {Command: "whisper-server", Mode: "server", HealthURL: upstream.URL + "/health"},
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

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
	var response struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode transcription response: %v", err)
	}
	if response.Text != "served transcript with newlines" {
		t.Fatalf("unexpected transcript %q", response.Text)
	}
}

func TestVoiceLoopWithTypedMessage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"typed reply"}}]}`))
	}))
	defer upstream.Close()

	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech"),
		"llama": {Command: "llama-server", HealthURL: upstream.URL + "/health"},
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("message", "hello from the keyboard"); err != nil {
		t.Fatalf("write message field: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/voice", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response struct {
		Transcript string `json:"transcript"`
		Reply      string `json:"reply"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode voice response: %v", err)
	}
	if response.Transcript != "hello from the keyboard" {
		t.Fatalf("unexpected transcript %q", response.Transcript)
	}
	if response.Reply != "typed reply" {
		t.Fatalf("unexpected reply %q", response.Reply)
	}
}

func TestVoiceLoopRejectsEmptyRequest(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/voice", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVoiceLoopChatUnavailable(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"whisper": helperEngine("transcribe"),
		"audio":   helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("message", "hello"); err != nil {
		t.Fatalf("write message field: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/voice", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "llama") {
		t.Fatalf("expected missing llama detail, got %s", rec.Body.String())
	}
}

func TestVoiceCloneLifecycleAndClonedVoiceLoop(t *testing.T) {
	t.Chdir(t.TempDir())
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"cloned reply"}}]}`))
	}))
	defer upstream.Close()

	cfg := testConfig(map[string]config.EngineConfig{
		"whisper": helperEngine("transcribe"),
		"audio":   helperEngine("speech-require-voice"),
		"llama":   {Command: "llama-server", HealthURL: upstream.URL + "/health"},
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	// Create: uploads the reference, transcribes it via whisper, stores the voice.
	var createBody bytes.Buffer
	writer := multipart.NewWriter(&createBody)
	_ = writer.WriteField("name", "Test Voice")
	part, err := writer.CreateFormFile("file", "reference.wav")
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
	req := httptest.NewRequest(http.MethodPost, "/v1/voices", &createBody)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created voiceCloneSummary
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == "" || created.Name != "Test Voice" || created.Transcript != "transcribed text" {
		t.Fatalf("unexpected created voice %+v", created)
	}
	if created.AudioURL != "/v1/voices/"+created.ID+"/audio" {
		t.Fatalf("unexpected audio url %q", created.AudioURL)
	}

	// List includes the new voice.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/voices", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected list status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var list voiceListResponse
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode voice list: %v", err)
	}
	if len(list.Voices) != 1 || list.Voices[0].ID != created.ID {
		t.Fatalf("unexpected voice list %+v", list)
	}

	// The reference WAV is served for playback.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, created.AudioURL, nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected audio status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "audio/wav") {
		t.Fatalf("expected audio/wav content type, got %q", contentType)
	}
	if body := rec.Body.String(); len(body) < 12 || body[:4] != "RIFF" {
		t.Fatalf("expected WAV body, got %q", body)
	}

	// The voice loop speaks with the cloned voice: the speech helper exits
	// non-zero unless --voice-ref/--reference-text arrived.
	var loopBody bytes.Buffer
	writer = multipart.NewWriter(&loopBody)
	_ = writer.WriteField("message", "hello")
	_ = writer.WriteField("voice", created.ID)
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/voice", &loopBody)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected cloned voice loop status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Direct speech accepts the cloned voice too.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"hello","voice":"`+created.ID+`","format":"wav"}`))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected cloned speech status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Unknown voices are rejected up front.
	var badBody bytes.Buffer
	writer = multipart.NewWriter(&badBody)
	_ = writer.WriteField("message", "hello")
	_ = writer.WriteField("voice", "voice_20990101_000000_ffffff")
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/voice", &badBody)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected unknown voice status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "not found") {
		t.Fatalf("expected not found detail, got %s", rec.Body.String())
	}

	// Delete removes the voice from the library.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/v1/voices/"+created.ID, nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected delete status 204, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/voices", nil)
	router.ServeHTTP(rec, req)
	list = voiceListResponse{}
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode voice list after delete: %v", err)
	}
	if len(list.Voices) != 0 {
		t.Fatalf("expected empty voice list after delete, got %+v", list)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/v1/voices/"+created.ID, nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected repeat delete status 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSpeechPadsGeneratedAudioWithSilence(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech-tone"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"hello","format":"wav"}`))
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	duration, err := wav.Duration(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("decode padded speech: %v", err)
	}
	// One second of tone plus 250ms lead and trail padding.
	want := 1500 * time.Millisecond
	if diff := duration - want; diff < -10*time.Millisecond || diff > 10*time.Millisecond {
		t.Fatalf("expected ~%s of padded audio, got %s", want, duration)
	}

	format, pcm, err := wav.Decode(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("decode padded speech: %v", err)
	}
	leadBytes := int(float64(format.SampleRate)*0.25) * int(format.Channels) * int(format.BitsPerSample) / 8
	for i := 0; i < leadBytes; i++ {
		if pcm[i] != 0 {
			t.Fatalf("expected lead silence at byte %d", i)
		}
	}
}

func TestVoiceDesignReturnsPreviewAndReference(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"voicedesign": helperEngine("design"),
	})
	manager := lifecycle.NewManager(cfg)
	router := NewRouter(cfg, manager)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/voices/design", strings.NewReader(`{"description":"Deep gravelly cowboy","model":"qwen3"}`))
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var response voiceDesignResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode design response: %v", err)
	}
	if response.Description != "Deep gravelly cowboy" {
		t.Fatalf("unexpected description %q", response.Description)
	}
	// No llama engine configured: normalization falls back to the raw text.
	if response.EngineInput != "Deep gravelly cowboy" {
		t.Fatalf("expected raw description fallback, got %q", response.EngineInput)
	}
	if response.Transcript == "" {
		t.Fatalf("expected sample transcript, got %+v", response)
	}
	reference, err := base64.StdEncoding.DecodeString(response.ReferenceB64)
	if err != nil {
		t.Fatalf("decode reference_b64: %v", err)
	}
	if string(reference) != "RIFFtestWAVE" {
		t.Fatalf("unexpected reference wav %q", reference)
	}
	if response.PreviewB64 == "" {
		t.Fatalf("expected preview audio, got %+v", response)
	}
	if manager.Health().Engines["voicedesign"].LastSuccessAt == nil {
		t.Fatalf("expected voicedesign lastSuccessAt to be recorded")
	}
}

func TestVoiceDesignModelRouting(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"voicedesign": helperEngine("design"),
		"omnivoice":   helperEngine("design"),
		"voxcpm2":     helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	for _, model := range []string{"qwen3", "omnivoice", "voxcpm2"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/voices/design", strings.NewReader(`{"description":"female, british accent","model":"`+model+`"}`))
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("model %s: expected status 200, got %d: %s", model, rec.Code, rec.Body.String())
		}
		var response voiceDesignResponse
		if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
			t.Fatalf("model %s: decode design response: %v", model, err)
		}
		if response.Model != model {
			t.Fatalf("expected model %s echoed, got %q", model, response.Model)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/voices/design", strings.NewReader(`{"description":"x","model":"elevenlabs"}`))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected unknown model status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVoiceDesignBadRequest(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"voicedesign": helperEngine("design"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "missing description", body: `{}`, want: "description"},
		{name: "oversized description", body: `{"description":"` + strings.Repeat("x", 501) + `"}`, want: "500"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/voices/design", strings.NewReader(tt.body))
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

func TestVoiceDesignMissingEngine(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/voices/design", strings.NewReader(`{"description":"a whispering ghost"}`))
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d: %s", rec.Code, rec.Body.String())
	}
	// The default model is voxcpm2.
	if !strings.Contains(rec.Body.String(), "voxcpm2") {
		t.Fatalf("expected missing voxcpm2 detail, got %s", rec.Body.String())
	}
}

func TestVoiceDesignNormalizesPerModel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body chatCompletionRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Errorf("decode normalizer request: %v", err)
		}
		if len(body.Messages) != 2 || !strings.Contains(body.Messages[0].Content, "voice-design normalizer") {
			t.Errorf("expected normalizer system prompt, got %+v", body.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"prose\": \"A deep gravelly cowboy voice.\", \"attributes\": \"male, middle-aged, low pitch, american accent\"}"}}]}`))
	}))
	defer upstream.Close()

	cfg := testConfig(map[string]config.EngineConfig{
		"llama":       {Command: "llama-server", HealthURL: upstream.URL + "/health"},
		"voicedesign": helperEngine("design"),
		"omnivoice":   helperEngine("design"),
		"voxcpm2":     helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	tests := []struct {
		model string
		want  string
	}{
		{model: "qwen3", want: "A deep gravelly cowboy voice."},
		{model: "voxcpm2", want: "A deep gravelly cowboy voice."},
		{model: "omnivoice", want: "male, middle-aged, low pitch, american accent"},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/voices/design", strings.NewReader(`{"description":"deep gravelly cowboy","model":"`+tt.model+`"}`))
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("model %s: expected status 200, got %d: %s", tt.model, rec.Code, rec.Body.String())
		}
		var response voiceDesignResponse
		if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
			t.Fatalf("model %s: decode design response: %v", tt.model, err)
		}
		if response.EngineInput != tt.want {
			t.Fatalf("model %s: expected engine input %q, got %q", tt.model, tt.want, response.EngineInput)
		}
		if response.Prose != "A deep gravelly cowboy voice." || response.Attributes != "male, middle-aged, low pitch, american accent" {
			t.Fatalf("model %s: unexpected normalized forms %+v", tt.model, response)
		}
	}
}

func TestSanitizeOmniAttributes(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "category prefixes stripped",
			raw:  "gender: male, age: elderly, pitch: low pitch, style: whisper, english accent: british accent",
			want: "male, elderly, low pitch, whisper, british accent",
		},
		{
			name: "clean values pass through",
			raw:  "female, young adult, high pitch, british accent",
			want: "female, young adult, high pitch, british accent",
		},
		{
			name: "unknown items dropped",
			raw:  "male, gravelly, cowboy drawl, low pitch",
			want: "male, low pitch",
		},
		{
			name: "one value per category",
			raw:  "male, female, elderly, child",
			want: "male, elderly",
		},
		{name: "nothing usable", raw: "a booming radio announcer", want: ""},
		{name: "case insensitive", raw: "Male, British Accent", want: "male, british accent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeOmniAttributes(tt.raw); got != tt.want {
				t.Fatalf("sanitizeOmniAttributes(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseVoiceDesignForms(t *testing.T) {
	tests := []struct {
		name       string
		reply      string
		prose      string
		attributes string
	}{
		{
			name:       "clean json",
			reply:      `{"prose": "A warm voice.", "attributes": "female, low pitch"}`,
			prose:      "A warm voice.",
			attributes: "female, low pitch",
		},
		{
			name:       "surrounded by chatter",
			reply:      "Sure! Here you go:\n{\"prose\": \"A warm voice.\", \"attributes\": \"female\"}\nHope that helps.",
			prose:      "A warm voice.",
			attributes: "female",
		},
		{name: "no json", reply: "I cannot help with that."},
		{name: "broken json", reply: `{"prose": "unterminated`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prose, attributes := parseVoiceDesignForms(tt.reply)
			if prose != tt.prose || attributes != tt.attributes {
				t.Fatalf("parseVoiceDesignForms(%q) = %q, %q; want %q, %q", tt.reply, prose, attributes, tt.prose, tt.attributes)
			}
		})
	}
}

func TestImageDescriptionUsesVisionAndClonedVoice(t *testing.T) {
	t.Chdir(t.TempDir())
	var gotPayload []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected upstream path %q", req.URL.Path)
		}
		data, err := io.ReadAll(req.Body)
		if err != nil {
			t.Errorf("read vision request: %v", err)
		}
		gotPayload = data
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"a tiny red square on a plain background"}}]}`))
	}))
	defer upstream.Close()

	cfg := testConfig(map[string]config.EngineConfig{
		"vision": {Command: "llama-server", HealthURL: upstream.URL + "/health"},
		"audio":  helperEngine("speech-require-voice"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	// Store a voice with a supplied transcript so no whisper engine is needed.
	var createBody bytes.Buffer
	writer := multipart.NewWriter(&createBody)
	_ = writer.WriteField("name", "Narrator")
	_ = writer.WriteField("transcript", "narrator reference words")
	part, err := writer.CreateFormFile("file", "reference.wav")
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
	req := httptest.NewRequest(http.MethodPost, "/v1/voices", &createBody)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected voice create status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created voiceCloneSummary
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode voice create response: %v", err)
	}

	imageB64 := base64.StdEncoding.EncodeToString(validPNGBytes())
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/images/descriptions", strings.NewReader(`{"image_b64":"`+imageB64+`","voice":"`+created.ID+`"}`))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected description status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var response imageDescriptionResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode description response: %v", err)
	}
	if response.Description != "a tiny red square on a plain background" {
		t.Fatalf("unexpected description %q", response.Description)
	}
	audio, err := base64.StdEncoding.DecodeString(response.AudioB64)
	if err != nil {
		t.Fatalf("decode audio_b64: %v", err)
	}
	if string(audio) != "RIFFtestWAVE" {
		t.Fatalf("unexpected wav bytes %q", audio)
	}

	// True vision: the upstream request carries the generic instruction and
	// the image, and nothing else.
	var visionPayload visionChatRequest
	if err := json.Unmarshal(gotPayload, &visionPayload); err != nil {
		t.Fatalf("decode vision payload: %v", err)
	}
	if len(visionPayload.Messages) != 1 || visionPayload.Messages[0].Role != "user" {
		t.Fatalf("expected one user message, got %+v", visionPayload.Messages)
	}
	parts := visionPayload.Messages[0].Content
	if len(parts) != 2 {
		t.Fatalf("expected text and image parts, got %+v", parts)
	}
	if parts[0].Type != "text" || parts[0].Text != visionInstruction {
		t.Fatalf("expected only the generic vision instruction, got %+v", parts[0])
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL == nil || !strings.HasPrefix(parts[1].ImageURL.URL, "data:image/png;base64,") {
		t.Fatalf("expected base64 PNG image part, got %+v", parts[1])
	}
}

func TestImageDescriptionMissingVisionEngine(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	imageB64 := base64.StdEncoding.EncodeToString(validPNGBytes())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/descriptions", strings.NewReader(`{"image_b64":"`+imageB64+`"}`))
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "vision") {
		t.Fatalf("expected missing vision detail, got %s", rec.Body.String())
	}
}

func TestImageDescriptionBadRequest(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"vision": {Command: "llama-server", HealthURL: "http://127.0.0.1:1/health"},
		"audio":  helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "missing image", body: `{}`, want: "image_b64"},
		{name: "not base64", body: `{"image_b64":"not-base64!!"}`, want: "base64"},
		{name: "not png", body: `{"image_b64":"` + base64.StdEncoding.EncodeToString([]byte("plain text")) + `"}`, want: "PNG"},
		{name: "oversized dimensions", body: `{"image_b64":"` + base64.StdEncoding.EncodeToString(widePNGBytes(t, 4097)) + `"}`, want: "at most"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/images/descriptions", strings.NewReader(tt.body))
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

func TestVoiceCloneProtectedRefusesDeletion(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("name", "Cox")
	_ = writer.WriteField("transcript", "protected reference words")
	_ = writer.WriteField("protected", "true")
	part, err := writer.CreateFormFile("file", "reference.wav")
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
	req := httptest.NewRequest(http.MethodPost, "/v1/voices", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created voiceCloneSummary
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if !created.Protected {
		t.Fatalf("expected protected summary, got %+v", created)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/v1/voices/"+created.ID, nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected delete status 403, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "protected") {
		t.Fatalf("expected protected detail, got %s", rec.Body.String())
	}

	// Still listed and usable afterwards.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/voices", nil)
	router.ServeHTTP(rec, req)
	var list voiceListResponse
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode voice list: %v", err)
	}
	if len(list.Voices) != 1 || !list.Voices[0].Protected {
		t.Fatalf("expected protected voice to survive, got %+v", list)
	}
}

func TestVoiceCloneCreateAcceptsSuppliedTranscript(t *testing.T) {
	t.Chdir(t.TempDir())
	// No whisper engine configured: a supplied transcript must skip transcription.
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("name", "Manual")
	_ = writer.WriteField("transcript", "hand-written reference words")
	part, err := writer.CreateFormFile("file", "reference.wav")
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
	req := httptest.NewRequest(http.MethodPost, "/v1/voices", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected create status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created voiceCloneSummary
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Transcript != "hand-written reference words" {
		t.Fatalf("unexpected transcript %q", created.Transcript)
	}
}

func TestVoiceCloneCreateRejectsInvalidWAV(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{
		"whisper": helperEngine("transcribe"),
		"audio":   helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("transcript", "words")
	part, err := writer.CreateFormFile("file", "reference.wav")
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
	req := httptest.NewRequest(http.MethodPost, "/v1/voices", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "reference wav") {
		t.Fatalf("expected reference wav detail, got %s", rec.Body.String())
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

// widePNGBytes encodes a real 1-pixel-tall PNG of the given width, for
// exercising the description dimension cap.
func widePNGBytes(t *testing.T, width int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := stdpng.Encode(&buf, image.NewGray(image.Rect(0, 0, width, 1))); err != nil {
		t.Fatalf("encode wide png: %v", err)
	}
	return buf.Bytes()
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

func validStoryRequestJSON() string {
	return `{
  "subject": "how stars are born",
  "target_seconds": 90,
  "source_mode": "curated",
  "voice_mode": "placeholder",
  "sources": [
    {
      "id": "src-1",
      "title": "NASA Science: Star Basics",
      "url": "https://science.nasa.gov/universe/stars/",
      "excerpt": "Stars form inside molecular clouds of gas and dust. Cold cloud conditions help gas clump into denser pockets. As clumps gain mass, gravity can make them collapse."
    },
    {
      "id": "src-2",
      "title": "NASA Webb: Fiery Hourglass",
      "url": "https://science.nasa.gov/missions/webb/nasas-webb-catches-fiery-hourglass-as-new-star-forms/",
      "excerpt": "A forming protostar gathers material from its surrounding molecular cloud. Falling material spirals inward and forms an accretion disk. The disk feeds material onto the protostar."
    },
    {
      "id": "src-3",
      "title": "NASA Hubble: Planet-Forming Disks",
      "url": "https://science.nasa.gov/missions/hubble/hubbles-album-of-planet-forming-disks/",
      "excerpt": "Some falling material forms a rotating disk around the protostar. Jets from magnetic poles are part of star formation. Jets help carry away angular momentum so material can continue collecting."
    }
  ]
}`
}

func waitGatewayStoryStatus(t *testing.T, router http.Handler, id string, want story.Status) story.StatusResponse {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/stories/"+id, nil)
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status response 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var status story.StatusResponse
		if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
			t.Fatalf("decode story status: %v", err)
		}
		if status.Status == want {
			return status
		}
		if status.Status == story.StatusFailed {
			t.Fatalf("story failed: %+v", status.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for story status %s", want)
	return story.StatusResponse{}
}

func waitGatewayStoryActive(t *testing.T, router http.Handler, id string) {
	t.Helper()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/stories/"+id, nil)
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status response 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var status story.StatusResponse
		if err := json.NewDecoder(rec.Body).Decode(&status); err != nil {
			t.Fatalf("decode story status: %v", err)
		}
		if status.Status != story.StatusQueued && status.Status != story.StatusComplete {
			return
		}
		if status.Status == story.StatusFailed {
			t.Fatalf("story failed: %+v", status.Error)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for active story")
}

func testConfig(engines map[string]config.EngineConfig) config.Config {
	return config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8765},
		Engines: engines,
	}
}
