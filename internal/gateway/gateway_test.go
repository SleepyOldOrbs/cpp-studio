package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	stdpng "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cpp-studio/internal/audiobook"
	"cpp-studio/internal/config"
	"cpp-studio/internal/engine"
	"cpp-studio/internal/lifecycle"
	"cpp-studio/internal/models"
	"cpp-studio/internal/story"
	"cpp-studio/internal/storybuilder"
	"cpp-studio/internal/voice"
	"cpp-studio/internal/wav"
)

func TestStoryBuilderProjectLifecycleThroughGateway(t *testing.T) {
	root := t.TempDir()
	newRouter := func() *router {
		cfg := testConfig(nil)
		r := NewRouter(cfg, lifecycle.NewManager(cfg)).(*router)
		r.storyBuilderProjects = storybuilder.NewStore(root)
		return r
	}
	r := newRouter()

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/story-builder-projects", strings.NewReader(`{"name":"The Lantern at Crow Point"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body.String())
	}
	var created storybuilder.Project
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created project: %v", err)
	}
	if created.ID == "" || created.Name != "The Lantern at Crow Point" || created.Revision != 1 || created.TimelineDurationMS != storybuilder.DefaultTimelineDurationMS {
		t.Fatalf("unexpected created project: %+v", created)
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/story-builder-projects", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Projects []storybuilder.Project `json:"projects"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil || len(list.Projects) != 1 || list.Projects[0].ID != created.ID {
		t.Fatalf("unexpected project list: %+v, err %v", list, err)
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v1/story-builder-projects/"+created.ID, strings.NewReader(`{
		"name":"Lantern final edit",
		"revision":1,
		"tracks":[
			{"id":"dialogue_mara","name":"Mara","type":"dialogue","order":0,"muted":false,"clips":[]},
			{"id":"foley","name":"Foley","type":"sfx","order":1,"muted":true,"clips":[
				{"id":"pause_1","type":"silence","label":"Hold for thunder","start_ms":1250,"duration_ms":800}
			]},
			{"id":"score","name":"Score","type":"music","order":2,"muted":false,"clips":[]}
		]
	}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("rename status = %d: %s", rec.Code, rec.Body.String())
	}
	var renamed storybuilder.Project
	if err := json.NewDecoder(rec.Body).Decode(&renamed); err != nil || renamed.Name != "Lantern final edit" || renamed.Revision != 2 ||
		len(renamed.Tracks) != 3 || renamed.Tracks[1].Clips[0].Type != storybuilder.ClipTypeSilence {
		t.Fatalf("unexpected renamed project: %+v, err %v", renamed, err)
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v1/story-builder-projects/"+created.ID, strings.NewReader(`{"name":"legacy rename","revision":2}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("track-omitting save status = %d, want 400: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v1/story-builder-projects/"+created.ID, strings.NewReader(`{"name":"stale overwrite","revision":1,"tracks":[]}`)))
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale save status = %d, want 409: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v1/story-builder-projects/"+created.ID, strings.NewReader(`{
		"name":"corrupt attempt","revision":2,
		"tracks":[{"id":"bad","name":"Bad","type":"sfx","order":0,"muted":false,
		"clips":[{"id":"bad_clip","type":"silence","label":"Bad","start_ms":-1,"duration_ms":100}]}]
	}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid timeline status = %d, want 400: %s", rec.Code, rec.Body.String())
	}

	// Rebuilding the router simulates a Gateway restart while retaining the
	// same purpose-built Story Builder Project store.
	r = newRouter()
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/story-builder-projects/"+created.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("reopen status = %d: %s", rec.Code, rec.Body.String())
	}
	var reopened storybuilder.Project
	if err := json.NewDecoder(rec.Body).Decode(&reopened); err != nil || reopened.Name != renamed.Name || reopened.Revision != renamed.Revision || len(reopened.Tracks) != 3 ||
		!reopened.CreatedAt.Equal(renamed.CreatedAt) || !reopened.UpdatedAt.Equal(renamed.UpdatedAt) {
		t.Fatalf("unexpected reopened project: %+v, err %v", reopened, err)
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/v1/story-builder-projects/"+created.ID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d: %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/story-builder-projects/"+created.ID, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deleted project status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestStoryBuilderTimelineTimingThroughGateway(t *testing.T) {
	cfg := testConfig(nil)
	r := NewRouter(cfg, lifecycle.NewManager(cfg)).(*router)
	r.storyBuilderProjects = storybuilder.NewStore(t.TempDir())

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/story-builder-projects", strings.NewReader(`{"name":"Exact timing"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body.String())
	}
	var created storybuilder.Project
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created project: %v", err)
	}

	validUpdate := fmt.Sprintf(`{
		"name":"Exact timing","revision":%d,"timeline_duration_ms":30000,
		"tracks":[
			{"id":"foley_a","name":"Foley A","type":"sfx","order":0,"muted":false,"clips":[
				{"id":"step_1","type":"silence","label":"First step","start_ms":101,"duration_ms":333},
				{"id":"step_2","type":"silence","label":"Second step","start_ms":434,"duration_ms":266}
			]},
			{"id":"foley_b","name":"Foley B","type":"sfx","order":1,"muted":false,"clips":[
				{"id":"rain","type":"silence","label":"Rain","start_ms":250,"duration_ms":400}
			]}
		]
	}`, created.Revision)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v1/story-builder-projects/"+created.ID, strings.NewReader(validUpdate)))
	if rec.Code != http.StatusOK {
		t.Fatalf("valid timing status = %d: %s", rec.Code, rec.Body.String())
	}
	var saved storybuilder.Project
	if err := json.NewDecoder(rec.Body).Decode(&saved); err != nil {
		t.Fatalf("decode saved project: %v", err)
	}
	if saved.Tracks[0].Clips[0].StartMS != 101 || saved.Tracks[0].Clips[0].DurationMS != 333 ||
		saved.Tracks[1].Clips[0].StartMS != 250 {
		t.Fatalf("millisecond timing drifted: %+v", saved.Tracks)
	}

	overlappingUpdate := fmt.Sprintf(`{
		"name":"Exact timing","revision":%d,
		"tracks":[{"id":"foley_a","name":"Foley A","type":"sfx","order":0,"muted":false,"clips":[
			{"id":"step_1","type":"silence","label":"First step","start_ms":101,"duration_ms":333},
			{"id":"step_2","type":"silence","label":"Overlapping step","start_ms":433,"duration_ms":266}
		]}]
	}`, saved.Revision)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v1/story-builder-projects/"+created.ID, strings.NewReader(overlappingUpdate)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("same-track overlap status = %d, want 400: %s", rec.Code, rec.Body.String())
	}

	outOfBoundsUpdate := fmt.Sprintf(`{
		"name":"Exact timing","revision":%d,"timeline_duration_ms":30000,
		"tracks":[{"id":"foley_a","name":"Foley A","type":"sfx","order":0,"muted":false,"clips":[
			{"id":"late","type":"silence","label":"Past project end","start_ms":29900,"duration_ms":101}
		]}]
	}`, saved.Revision)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v1/story-builder-projects/"+created.ID, strings.NewReader(outOfBoundsUpdate)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("project-bound status = %d, want 400: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/story-builder-projects/"+created.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("reload status = %d: %s", rec.Code, rec.Body.String())
	}
	var reloaded storybuilder.Project
	if err := json.NewDecoder(rec.Body).Decode(&reloaded); err != nil {
		t.Fatalf("decode reloaded project: %v", err)
	}
	if reloaded.Revision != saved.Revision || len(reloaded.Tracks) != 2 || reloaded.Tracks[0].Clips[1].StartMS != 434 {
		t.Fatalf("rejected overlap changed durable timing: %+v", reloaded)
	}
}

func TestStoryBuilderAudioTrimBoundsThroughGateway(t *testing.T) {
	cfg := testConfig(nil)
	r := NewRouter(cfg, lifecycle.NewManager(cfg)).(*router)
	r.storyBuilderProjects = storybuilder.NewStore(t.TempDir())

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/story-builder-projects", strings.NewReader(`{"name":"Trimmed dialogue"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", rec.Code, rec.Body.String())
	}
	var created storybuilder.Project
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created project: %v", err)
	}

	validUpdate := fmt.Sprintf(`{
		"name":"Trimmed dialogue","revision":%d,
		"tracks":[{"id":"mara","name":"Mara","type":"dialogue","order":0,"muted":false,"character_voice_id":"voice_mara","clips":[
			{"id":"line_1","type":"dialogue","label":"Keep the lamp low","start_ms":125,"duration_ms":700,
			 "source_id":"take_1","source_duration_ms":1200,"source_in_ms":200,"source_out_ms":900}
		]}]
	}`, created.Revision)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v1/story-builder-projects/"+created.ID, strings.NewReader(validUpdate)))
	if rec.Code != http.StatusOK {
		t.Fatalf("valid trim status = %d: %s", rec.Code, rec.Body.String())
	}
	var saved storybuilder.Project
	if err := json.NewDecoder(rec.Body).Decode(&saved); err != nil {
		t.Fatalf("decode saved project: %v", err)
	}
	clip := saved.Tracks[0].Clips[0]
	if clip.SourceInMS != 200 || clip.SourceOutMS != 900 || clip.DurationMS != 700 {
		t.Fatalf("trim bounds drifted: %+v", clip)
	}

	invalidUpdate := fmt.Sprintf(`{
		"name":"Trimmed dialogue","revision":%d,
		"tracks":[{"id":"mara","name":"Mara","type":"dialogue","order":0,"muted":false,"character_voice_id":"voice_mara","clips":[
			{"id":"line_1","type":"dialogue","label":"Keep the lamp low","start_ms":125,"duration_ms":1100,
			 "source_id":"take_1","source_duration_ms":1200,"source_in_ms":200,"source_out_ms":1300}
		]}]
	}`, saved.Revision)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v1/story-builder-projects/"+created.ID, strings.NewReader(invalidUpdate)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("out-of-bounds trim status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

func TestStoryBuilderProjectGatewayRejectsInvalidRequests(t *testing.T) {
	cfg := testConfig(nil)
	r := NewRouter(cfg, lifecycle.NewManager(cfg)).(*router)
	r.storyBuilderProjects = storybuilder.NewStore(t.TempDir())

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{name: "empty name", method: http.MethodPost, path: "/v1/story-builder-projects", body: `{"name":" "}`, status: http.StatusBadRequest},
		{name: "unknown create field", method: http.MethodPost, path: "/v1/story-builder-projects", body: `{"name":"x","path":"C:\\\\outside"}`, status: http.StatusBadRequest},
		{name: "collection method", method: http.MethodDelete, path: "/v1/story-builder-projects", status: http.StatusMethodNotAllowed},
		{name: "traversal id", method: http.MethodGet, path: "/v1/story-builder-projects/bad..id", status: http.StatusNotFound},
		{name: "nested path", method: http.MethodGet, path: "/v1/story-builder-projects/project/manifest", status: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body)))
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.status, rec.Body.String())
			}
		})
	}
}

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

func TestVoiceConversionUsesSourceAndTargetWAVs(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		engine.VoiceConversionEngineID: {Command: "audiocpp-cli"},
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg)).(*router)
	fake := engine.NewFake()
	fake.Handle(engine.VoiceConversionEngineID, func(spec engine.Spec) (engine.Result, error) {
		args := spec.BuildArgs(spec.InputPath, "converted.wav")
		if len(args) != 6 || args[0] != "--audio" || args[2] != "--voice-ref" {
			t.Fatalf("unexpected conversion args %v", args)
		}
		for _, path := range []string{args[1], args[3]} {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read staged WAV %q: %v", path, err)
			}
			if err := wav.ValidateBytes(data); err != nil {
				t.Fatalf("staged input %q is not WAV: %v", path, err)
			}
		}
		return engine.Result{Output: validWAVBytes()}, nil
	})
	router.engines = fake

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for field, filename := range map[string]string{"source": "performance.wav", "target": "voice.wav"} {
		part, err := writer.CreateFormFile(field, filename)
		if err != nil {
			t.Fatalf("create %s part: %v", field, err)
		}
		if _, err := part.Write(validWAVBytes()); err != nil {
			t.Fatalf("write %s part: %v", field, err)
		}
	}
	_ = writer.WriteField("model", "chatterbox-q8-0")
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/conversions", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "audio/wav" {
		t.Fatalf("expected audio/wav, got %q", got)
	}
	if err := wav.ValidateBytes(rec.Body.Bytes()); err != nil {
		t.Fatalf("response is not WAV: %v", err)
	}
}

func TestMusicGenerationUsesCuratedACEFieldsAndSourceWAV(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		engine.MusicEngineID: {Command: "audiocpp-cli"},
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg)).(*router)
	fake := engine.NewFake()
	fake.Handle(engine.MusicEngineID, func(spec engine.Spec) (engine.Result, error) {
		if spec.InputPath == "" {
			t.Fatal("expected staged source WAV")
		}
		if err := wav.ValidateFile(spec.InputPath); err != nil {
			t.Fatalf("source is not WAV: %v", err)
		}
		args := strings.Join(spec.BuildArgs(spec.InputPath, "song.wav"), " ")
		for _, want := range []string{"--task-route cover", "--text brighter acoustic cover", "--lyrics new words", "--duration-seconds 30", "--seed 7", "--num-inference-steps 8", "--guidance-scale 1.25", "--audio " + spec.InputPath, "--out song.wav"} {
			if !strings.Contains(args, want) {
				t.Fatalf("music args %q missing %q", args, want)
			}
		}
		return engine.Result{Output: validWAVBytes()}, nil
	})
	router.engines = fake

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("source", "source.wav")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(validWAVBytes())
	for key, value := range map[string]string{
		"model": "ace-step-turbo-q8-0", "route": "cover", "prompt": "brighter acoustic cover",
		"lyrics": "new words", "duration": "30", "seed": "7", "steps": "8", "guidance": "1.25",
	} {
		_ = writer.WriteField(key, value)
	}
	_ = writer.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/music", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-Music-Model") != "ace-step-turbo-q8-0" {
		t.Fatalf("missing model identity header: %v", rec.Header())
	}
	if err := wav.ValidateBytes(rec.Body.Bytes()); err != nil {
		t.Fatalf("response is not WAV: %v", err)
	}
}

func TestMusicGenerationRequiresSourceForEditRoutes(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{engine.MusicEngineID: {Command: "audiocpp-cli"}})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))
	for _, route := range []string{"lego", "extract", "cover", "cover-nofsq", "repaint"} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("route", route)
		_ = writer.WriteField("prompt", "test edit")
		if route == "repaint" {
			_ = writer.WriteField("repaint_start", "1")
			_ = writer.WriteField("repaint_end", "2")
		}
		_ = writer.Close()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/audio/music", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "source") {
			t.Fatalf("route %s accepted without source: %d %s", route, rec.Code, rec.Body.String())
		}
	}
}

func TestMusicGenerationRejectsFieldsUnsupportedByRoute(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{engine.MusicEngineID: {Command: "audiocpp-cli"}})
	router := NewRouter(cfg, lifecycle.NewManager(cfg)).(*router)
	fake := engine.NewFake()
	runs := 0
	fake.Handle(engine.MusicEngineID, func(spec engine.Spec) (engine.Result, error) {
		runs++
		return engine.Result{Output: validWAVBytes()}, nil
	})
	router.engines = fake

	tests := []struct {
		name       string
		fields     map[string]string
		withSource bool
		want       string
	}{
		{name: "text source", fields: map[string]string{"route": "text2music", "prompt": "new song"}, withSource: true, want: "source is not supported"},
		{name: "cover track name", fields: map[string]string{"route": "cover", "prompt": "new cover", "track_name": "vocals"}, withSource: true, want: "track_name is only supported"},
		{name: "text repaint field", fields: map[string]string{"route": "text2music", "prompt": "new song", "repaint_strength": "0.5"}, want: "repaint fields are only supported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			if test.withSource {
				part, err := writer.CreateFormFile("source", "source.wav")
				if err != nil {
					t.Fatal(err)
				}
				_, _ = part.Write(validWAVBytes())
			}
			for key, value := range test.fields {
				_ = writer.WriteField(key, value)
			}
			_ = writer.Close()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/audio/music", &body)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), test.want) {
				t.Fatalf("expected 400 containing %q, got %d %s", test.want, rec.Code, rec.Body.String())
			}
		})
	}
	if runs != 0 {
		t.Fatalf("engine ran %d times for rejected requests", runs)
	}
}

func TestMusicSourceAnalysisReturnsStructuredMetadata(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{engine.MusicEngineID: {Command: "audiocpp-cli"}})
	router := NewRouter(cfg, lifecycle.NewManager(cfg)).(*router)
	fake := engine.NewFake()
	fake.Handle(engine.MusicEngineID, func(spec engine.Spec) (engine.Result, error) {
		args := strings.Join(spec.BuildArgs(spec.InputPath, ""), " ")
		if !strings.Contains(args, "--task-route analyze") || !strings.Contains(args, "--seed 1234") {
			t.Fatalf("unexpected analysis args: %s", args)
		}
		return engine.Result{Stdout: []byte("loader ready\ntext_output={\"caption\":\"warm jazz trio\",\"bpm\":92,\"keyscale\":\"C minor\",\"timesignature\":\"4/4\"}\n")}, nil
	})
	router.engines = fake

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("source", "source.wav")
	_, _ = part.Write(validWAVBytes())
	_ = writer.WriteField("seed", "-1")
	_ = writer.Close()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/music/analyze", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"caption":"warm jazz trio"`) || !strings.Contains(rec.Body.String(), `"bpm":92`) {
		t.Fatalf("unexpected analysis response: %d %s", rec.Code, rec.Body.String())
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

// fakeSDServer implements the native async sd-server API: img_gen accepts a
// job and the jobs route reports it generating once before completing, so
// the gateway's poll loop is actually exercised.
func fakeSDServer(t *testing.T, onBody func(raw []byte)) *httptest.Server {
	t.Helper()
	var polls int
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.URL.Path == "/sdcpp/v1/img_gen":
			raw, _ := io.ReadAll(req.Body)
			onBody(raw)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"id":"job-1","status":"queued","poll_url":"/sdcpp/v1/jobs/job-1"}`))
		case req.URL.Path == "/sdcpp/v1/jobs/job-1":
			w.Header().Set("Content-Type", "application/json")
			polls++
			if polls == 1 {
				_, _ = w.Write([]byte(`{"id":"job-1","status":"generating","result":null,"error":null}`))
				return
			}
			_, _ = w.Write([]byte(fmt.Sprintf(`{"id":"job-1","status":"completed","result":{"output_format":"png","images":[{"index":0,"b64_json":%q}]},"error":null}`, base64.StdEncoding.EncodeToString(validPNGBytes()))))
		default:
			t.Errorf("unexpected upstream path %q", req.URL.Path)
			http.NotFound(w, req)
		}
	}))
}

func TestImageGenerationViaResidentSDServer(t *testing.T) {
	var upstreamBody []byte
	upstream := fakeSDServer(t, func(raw []byte) { upstreamBody = raw })
	defer upstream.Close()

	cfg := testConfig(map[string]config.EngineConfig{
		"sd": {Command: "sd-server", Mode: "server", HealthURL: upstream.URL + "/v1/models"},
	})
	manager := lifecycle.NewManager(cfg)
	router := NewRouter(cfg, manager)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"prompt":"a small cabin","size":"512x512","response_format":"b64_json","seed":1234}`))
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// The upstream body is purpose-built: exactly the fields the native
	// route defines, with the client's seed passed through. The old OpenAI
	// route fatally rejected stray fields ("n":null), so the same
	// discipline is guarded here.
	var sent struct {
		Prompt       string `json:"prompt"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		Seed         *int64 `json:"seed"`
		OutputFormat string `json:"output_format"`
	}
	if err := json.Unmarshal(upstreamBody, &sent); err != nil {
		t.Fatalf("decode upstream body: %v", err)
	}
	if sent.Prompt != "a small cabin" || sent.Width != 512 || sent.Height != 512 {
		t.Fatalf("unexpected upstream request: %s", upstreamBody)
	}
	if sent.Seed == nil || *sent.Seed != 1234 {
		t.Fatalf("expected the client seed passed through, got %s", upstreamBody)
	}
	if sent.OutputFormat != "png" {
		t.Fatalf("expected png output_format, got %s", upstreamBody)
	}
	if bytes.Contains(upstreamBody, []byte(`"n"`)) || bytes.Contains(upstreamBody, []byte("response_format")) {
		t.Fatalf("upstream body must only carry native-route fields: %s", upstreamBody)
	}

	var response imageGenerationResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode image generation response: %v", err)
	}
	if len(response.Data) != 1 || response.Data[0].B64JSON == "" {
		t.Fatalf("expected one b64_json image, got %+v", response)
	}
	if response.Seed != 1234 {
		t.Fatalf("expected the seed reported back, got %d", response.Seed)
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

func TestImageGenerationRollsASeedWhenAbsent(t *testing.T) {
	var upstreamBody []byte
	upstream := fakeSDServer(t, func(raw []byte) { upstreamBody = raw })
	defer upstream.Close()

	cfg := testConfig(map[string]config.EngineConfig{
		"sd": {Command: "sd-server", Mode: "server", HealthURL: upstream.URL + "/v1/models"},
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"prompt":"a small cabin","size":"512x512"}`))
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var sent struct {
		Seed *int64 `json:"seed"`
	}
	if err := json.Unmarshal(upstreamBody, &sent); err != nil {
		t.Fatalf("decode upstream body: %v", err)
	}
	// No seed in the request: the gateway rolls a concrete positive one —
	// never -1, which would let the server randomise invisibly and make
	// the image unreproducible.
	if sent.Seed == nil || *sent.Seed <= 0 {
		t.Fatalf("expected a rolled positive seed, got %s", upstreamBody)
	}
	var response imageGenerationResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode image generation response: %v", err)
	}
	if response.Seed != *sent.Seed {
		t.Fatalf("response seed %d should match the one sent upstream %d", response.Seed, *sent.Seed)
	}
}

func TestImageGenerationResidentSDServerFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Error(w, "diffusion failed", http.StatusInternalServerError)
	}))
	defer upstream.Close()

	cfg := testConfig(map[string]config.EngineConfig{
		"sd": {Command: "sd-server", Mode: "server", HealthURL: upstream.URL + "/v1/models"},
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"prompt":"a small cabin","size":"512x512"}`))
	router.ServeHTTP(rec, req)

	if rec.Code < 500 {
		t.Fatalf("expected 5xx on upstream failure, got %d: %s", rec.Code, rec.Body.String())
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

func TestCompletedProductionsCanBeDeletedFromLibrary(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech-tone"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	storyCreate := httptest.NewRecorder()
	router.ServeHTTP(storyCreate, httptest.NewRequest(http.MethodPost, "/v1/stories", strings.NewReader(validStoryRequestJSON())))
	if storyCreate.Code != http.StatusAccepted {
		t.Fatalf("create story: %d %s", storyCreate.Code, storyCreate.Body.String())
	}
	var createdStory story.CreateResponse
	if err := json.NewDecoder(storyCreate.Body).Decode(&createdStory); err != nil {
		t.Fatalf("decode story: %v", err)
	}
	waitGatewayStoryStatus(t, router, createdStory.ID, story.StatusComplete)

	storyDelete := httptest.NewRecorder()
	router.ServeHTTP(storyDelete, httptest.NewRequest(http.MethodDelete, "/v1/stories/"+createdStory.ID, nil))
	if storyDelete.Code != http.StatusNoContent {
		t.Fatalf("delete story: %d %s", storyDelete.Code, storyDelete.Body.String())
	}
	storyStatus := httptest.NewRecorder()
	router.ServeHTTP(storyStatus, httptest.NewRequest(http.MethodGet, "/v1/stories/"+createdStory.ID, nil))
	if storyStatus.Code != http.StatusNotFound {
		t.Fatalf("deleted story status: %d %s", storyStatus.Code, storyStatus.Body.String())
	}

	bookCreate := postAudiobook(t, router, nil)
	if bookCreate.Code != http.StatusAccepted {
		t.Fatalf("create audiobook: %d %s", bookCreate.Code, bookCreate.Body.String())
	}
	var createdBook struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(bookCreate.Body).Decode(&createdBook); err != nil {
		t.Fatalf("decode audiobook: %v", err)
	}
	if job := waitGatewayAudiobookJob(t, router, createdBook.ID); job.Status != "complete" {
		t.Fatalf("audiobook job: %+v", job)
	}

	var bookDelete *httptest.ResponseRecorder
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		bookDelete = httptest.NewRecorder()
		router.ServeHTTP(bookDelete, httptest.NewRequest(http.MethodDelete, "/v1/audiobooks/"+createdBook.ID, nil))
		if bookDelete.Code != http.StatusConflict {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if bookDelete.Code != http.StatusNoContent {
		t.Fatalf("delete audiobook: %d %s", bookDelete.Code, bookDelete.Body.String())
	}
	bookStatus := httptest.NewRecorder()
	router.ServeHTTP(bookStatus, httptest.NewRequest(http.MethodGet, "/v1/audiobooks/"+createdBook.ID, nil))
	if bookStatus.Code != http.StatusNotFound {
		t.Fatalf("deleted audiobook status: %d %s", bookStatus.Code, bookStatus.Body.String())
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
	var voiceCreated actorVoiceSummary
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

func TestSketchModeWritesUnsourcedComedy(t *testing.T) {
	t.Chdir(t.TempDir())
	// The model answers with no citations, and with one invented fact id to
	// prove sketch mode drops rather than rejects it.
	scriptJSON := `{"title": "The Apology Shop", "script": [
{"speaker_id": "narrator", "text": "The shop was quiet, as apology shops tend to be."},
{"speaker_id": "nova", "text": "I would like to return this apology, please."},
{"speaker_id": "dr-lumen", "text": "Was it not sincere enough, madam?", "fact_ids": ["fact-1"]},
{"speaker_id": "nova", "text": "It was far too sincere. It made the dog cry."}
]}`
	var sawSketchPrompt, sawGroundedPrompt bool
	var sketchPrompt string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body chatCompletionRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		if len(body.Messages) > 0 {
			if strings.Contains(body.Messages[0].Content, "comedy sketch scripts") {
				sawSketchPrompt = true
			}
			if strings.Contains(body.Messages[0].Content, "audio stories as dialogue scripts") {
				sawGroundedPrompt = true
			}
		}
		if len(body.Messages) > 1 {
			sketchPrompt = body.Messages[1].Content
		}
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
	req := httptest.NewRequest(http.MethodPost, "/v1/stories", strings.NewReader(validSketchRequestJSON()))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d: %s", rec.Code, rec.Body.String())
	}
	var create story.CreateResponse
	if err := json.NewDecoder(rec.Body).Decode(&create); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	status := waitGatewayStoryStatus(t, router, create.ID, story.StatusComplete)
	if status.Manifest == nil {
		t.Fatalf("expected completed manifest, got %+v", status)
	}
	if !sawSketchPrompt {
		t.Fatalf("expected the sketch prompt to reach llama")
	}
	if sawGroundedPrompt {
		t.Fatalf("sketch mode must not send the grounded story prompt")
	}
	if !strings.Contains(sketchPrompt, "A customer wants to return an apology") {
		t.Fatalf("expected the premise in the user prompt, got %q", sketchPrompt)
	}
	if !strings.Contains(sketchPrompt, "1960s BBC radio comedy") {
		t.Fatalf("expected the style in the user prompt, got %q", sketchPrompt)
	}
	if status.Manifest.Mode != story.ModeSketch {
		t.Fatalf("expected a sketch manifest, got mode %q", status.Manifest.Mode)
	}
	if len(status.Manifest.FactCards) != 0 || len(status.Manifest.Sources) != 0 {
		t.Fatalf("expected no facts or sources, got %d facts, %d sources", len(status.Manifest.FactCards), len(status.Manifest.Sources))
	}
	if status.Manifest.Title != "The Apology Shop" || len(status.Manifest.Script) != 4 {
		t.Fatalf("unexpected sketch script %+v", status.Manifest)
	}
	for i, line := range status.Manifest.Script {
		if len(line.FactIDs) != 0 {
			t.Fatalf("sketch line %d kept invented fact ids %v", i, line.FactIDs)
		}
	}
}

func TestSketchModeRejectsSourcelessGroundedStory(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{"audio": helperEngine("speech")})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	// The same body without the sketch mode is still an invalid story.
	body := strings.Replace(validSketchRequestJSON(), `"mode": "sketch"`, `"mode": "grounded"`, 1)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/stories", strings.NewReader(body))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), string(story.CodeInsufficientSources)) {
		t.Fatalf("expected insufficient_sources, got %s", rec.Body.String())
	}

	// And an unknown mode is refused outright.
	body = strings.Replace(validSketchRequestJSON(), `"mode": "sketch"`, `"mode": "documentary"`, 1)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/stories", strings.NewReader(body))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), string(story.CodeUnsupportedMode)) {
		t.Fatalf("expected unsupported_mode, got %s", rec.Body.String())
	}
}

func TestTakeRoomRetakeEditAndRender(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{"audio": helperEngine("speech-tone")})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	// Produce a sketch with real per-line synthesis so takes land on disk.
	body := strings.Replace(validSketchRequestJSON(), `"voice_mode": "placeholder"`, `"voice_mode": "fixed"`, 1)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/stories", strings.NewReader(body))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d: %s", rec.Code, rec.Body.String())
	}
	var create story.CreateResponse
	if err := json.NewDecoder(rec.Body).Decode(&create); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	status := waitGatewayStoryStatus(t, router, create.ID, story.StatusComplete)
	if status.Manifest == nil || len(status.Manifest.Script) == 0 {
		t.Fatalf("expected a produced manifest, got %+v", status)
	}
	lineID := status.Manifest.Script[0].ID
	if lineID == "" {
		t.Fatalf("produced line has no id: %+v", status.Manifest.Script[0])
	}

	// Each take is servable over the artifact route.
	takeURL := status.Manifest.Script[0].Takes[0].URL
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, takeURL, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected take audio at %s, got %d: %s", takeURL, rec.Code, rec.Body.String())
	}
	if body := rec.Body.Bytes(); len(body) < 12 || string(body[:4]) != "RIFF" {
		t.Fatalf("take route did not serve WAV bytes")
	}

	// Retake that line.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/stories/"+create.ID+"/lines/"+lineID+"/takes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected retake status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var retake struct {
		Take     story.Take     `json:"take"`
		Manifest story.Manifest `json:"manifest"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&retake); err != nil {
		t.Fatalf("decode retake response: %v", err)
	}
	if retake.Take.ID != "take-002" || retake.Manifest.Script[0].CurrentTake != "take-002" {
		t.Fatalf("unexpected retake result %+v", retake)
	}

	// Go back to the first take through the line patch route.
	rec = httptest.NewRecorder()
	patch := httptest.NewRequest(http.MethodPatch, "/v1/stories/"+create.ID+"/lines/"+lineID, strings.NewReader(`{"current_take": "take-001", "gap_after_ms": 250}`))
	router.ServeHTTP(rec, patch)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected patch status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var patched struct {
		Manifest story.Manifest `json:"manifest"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&patched); err != nil {
		t.Fatalf("decode patch response: %v", err)
	}
	if patched.Manifest.Script[0].CurrentTake != "take-001" || patched.Manifest.Script[0].GapAfterMS != 250 {
		t.Fatalf("patch did not apply: %+v", patched.Manifest.Script[0])
	}

	// Render a new revision from the current takes.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/stories/"+create.ID+"/render", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected render status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var rendered struct {
		Render story.Render `json:"render"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&rendered); err != nil {
		t.Fatalf("decode render response: %v", err)
	}
	if rendered.Render.Revision != 2 {
		t.Fatalf("expected revision 2, got %d", rendered.Render.Revision)
	}
	// Both revisions stay fetchable: a published render is immutable.
	for _, url := range []string{
		"/v1/stories/" + create.ID + "/artifact/renders/render-001.wav",
		rendered.Render.URL,
	} {
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected %s to be served, got %d", url, rec.Code)
		}
	}

	t.Run("path traversal is refused", func(t *testing.T) {
		for _, url := range []string{
			"/v1/stories/" + create.ID + "/artifact/manifest.json",
			"/v1/stories/" + create.ID + "/artifact/renders/render-001.txt",
			"/v1/stories/" + create.ID + "/artifact/lines/" + lineID + "/take-001.mp3",
		} {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
			if rec.Code == http.StatusOK {
				t.Fatalf("expected %s to be refused", url)
			}
		}
	})
}

// ffmpegHelperEngine points the ffmpeg engine at the test helper process,
// which answers the encoder probe and performs a stand-in transcode.
func ffmpegHelperEngine() config.EngineConfig {
	return config.EngineConfig{
		Command:               os.Args[0],
		Args:                  []string{"-test.run=TestGatewayHelperProcess", "--", "ffmpeg"},
		RequestTimeoutSeconds: 20,
	}
}

func TestStoryExportToDeliveryFormat(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{
		"audio":  helperEngine("speech-tone"),
		"ffmpeg": ffmpegHelperEngine(),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	// Only formats this ffmpeg can actually encode are offered.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/audio/formats", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected formats status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var formats struct {
		Formats []struct {
			ID        string `json:"id"`
			Available bool   `json:"available"`
		} `json:"formats"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&formats); err != nil {
		t.Fatalf("decode formats: %v", err)
	}
	if len(formats.Formats) != 2 {
		t.Fatalf("expected mp3 and opus, got %+v", formats.Formats)
	}
	for _, format := range formats.Formats {
		if !format.Available {
			t.Fatalf("%s should be available with this ffmpeg: %+v", format.ID, format)
		}
	}

	// Produce a story so there is a render revision to encode.
	body := strings.Replace(validSketchRequestJSON(), `"voice_mode": "placeholder"`, `"voice_mode": "fixed"`, 1)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/stories", strings.NewReader(body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected status 202, got %d: %s", rec.Code, rec.Body.String())
	}
	var create story.CreateResponse
	if err := json.NewDecoder(rec.Body).Decode(&create); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	waitGatewayStoryStatus(t, router, create.ID, story.StatusComplete)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/stories/"+create.ID+"/export", strings.NewReader(`{"format":"mp3"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected export status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var exported struct {
		Export   story.Export   `json:"export"`
		Manifest story.Manifest `json:"manifest"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&exported); err != nil {
		t.Fatalf("decode export response: %v", err)
	}
	if exported.Export.Format != "mp3" || exported.Export.Bitrate != "128k" {
		t.Fatalf("unexpected export %+v", exported.Export)
	}
	if exported.Export.Bytes <= 0 {
		t.Fatalf("export has no bytes: %+v", exported.Export)
	}
	// The export hangs off the revision it encodes, not the story.
	last := exported.Manifest.Renders[len(exported.Manifest.Renders)-1]
	if len(last.Exports) != 1 || last.Exports[0].Format != "mp3" {
		t.Fatalf("export was not recorded against the revision: %+v", last)
	}

	// And it serves with the right content type.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, exported.Export.URL, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected the export to be served, got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "audio/mpeg" {
		t.Fatalf("expected audio/mpeg, got %q", got)
	}
	if !strings.HasPrefix(rec.Body.String(), "ID3") {
		t.Fatalf("export route did not serve the encoded bytes")
	}

	t.Run("re-exporting replaces rather than accumulates", func(t *testing.T) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/stories/"+create.ID+"/export", strings.NewReader(`{"format":"mp3","bitrate":"64k"}`)))
		if rec.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var again struct {
			Manifest story.Manifest `json:"manifest"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&again); err != nil {
			t.Fatalf("decode: %v", err)
		}
		last := again.Manifest.Renders[len(again.Manifest.Renders)-1]
		if len(last.Exports) != 1 || last.Exports[0].Bitrate != "64k" {
			t.Fatalf("expected one mp3 export at the new bitrate, got %+v", last.Exports)
		}
	})

	t.Run("bad requests are refused", func(t *testing.T) {
		for _, payload := range []string{`{"format":"flac"}`, `{"format":"mp3","bitrate":"9k"}`, `{"format":"mp3","revision":99}`} {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/stories/"+create.ID+"/export", strings.NewReader(payload)))
			if rec.Code == http.StatusOK {
				t.Fatalf("expected %s to be refused", payload)
			}
		}
	})
}

func TestAudioDecodeConvertsWhatTheBrowserCannot(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{"ffmpeg": ffmpegHelperEngine()})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "round-the-horne.wma")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	// Deliberately not a WAV: the whole point is the formats the browser
	// refuses.
	if _, err := part.Write([]byte("\x30\x26\xb2\x75 not audio the browser knows")); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/decode", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "audio/wav" {
		t.Fatalf("expected audio/wav, got %q", got)
	}
	if got := rec.Body.Bytes(); len(got) < 12 || string(got[:4]) != "RIFF" {
		t.Fatalf("decode did not return WAV bytes")
	}
	name, err := url.PathUnescape(rec.Header().Get("X-Decoded-From"))
	if err != nil || name != "round-the-horne.wma" {
		t.Fatalf("expected the source filename back, got %q (%v)", name, err)
	}

	t.Run("a missing file is refused", func(t *testing.T) {
		var empty bytes.Buffer
		w := multipart.NewWriter(&empty)
		_ = w.WriteField("notafile", "x")
		_ = w.Close()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/audio/decode", &empty)
		req.Header.Set("Content-Type", w.FormDataContentType())
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400, got %d", rec.Code)
		}
	})

	t.Run("method not allowed", func(t *testing.T) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/audio/decode", nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected status 405, got %d", rec.Code)
		}
	})
}

func TestEngineVariantRoutes(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"whisper": {
			Command:        "whisper-server",
			Mode:           "server",
			DefaultVariant: "large",
			Variants: map[string]config.EngineVariant{
				"large": {Label: "large-v3 (best)", Args: []string{"-m", "large.bin"}},
				"turbo": {Args: []string{"-m", "turbo.bin"}},
			},
		},
		"audio": helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/engines/whisper/variants", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 listing variants, got %d: %s", rec.Code, rec.Body.String())
	}
	var listing struct {
		Variants []lifecycle.VariantInfo `json:"variants"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&listing); err != nil {
		t.Fatalf("decode variants: %v", err)
	}
	if len(listing.Variants) != 2 || !listing.Variants[0].Active || listing.Variants[0].ID != "large" {
		t.Fatalf("unexpected variants %+v", listing.Variants)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/engines/whisper/variant", strings.NewReader(`{"id":"turbo"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 switching variant, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&listing); err != nil {
		t.Fatalf("decode variants after switch: %v", err)
	}
	if !listing.Variants[1].Active || listing.Variants[0].Active {
		t.Fatalf("expected turbo active after the switch, got %+v", listing.Variants)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/engines/whisper/variant", strings.NewReader(`{"id":"nope"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an unknown variant, got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/engines/audio/variants", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for an engine without variants, got %d", rec.Code)
	}
}

// ggufHeaderBytes hand-crafts the minimal GGUF header the fit preflight
// reads: architecture plus, for MoE models, an expert count.
func ggufHeaderBytes(arch string, expertCount uint32) []byte {
	var kvs bytes.Buffer
	writeStr := func(s string) {
		_ = binary.Write(&kvs, binary.LittleEndian, uint64(len(s)))
		kvs.WriteString(s)
	}
	kvCount := uint64(1)
	writeStr("general.architecture")
	_ = binary.Write(&kvs, binary.LittleEndian, uint32(8)) // string
	writeStr(arch)
	if expertCount > 0 {
		kvCount++
		writeStr(arch + ".expert_count")
		_ = binary.Write(&kvs, binary.LittleEndian, uint32(4)) // uint32
		_ = binary.Write(&kvs, binary.LittleEndian, expertCount)
	}
	var out bytes.Buffer
	out.WriteString("GGUF")
	_ = binary.Write(&out, binary.LittleEndian, uint32(3))
	_ = binary.Write(&out, binary.LittleEndian, uint64(0))
	_ = binary.Write(&out, binary.LittleEndian, kvCount)
	out.Write(kvs.Bytes())
	return out.Bytes()
}

// byomLlamaEngine returns a llama engine whose byomDir is a temp dir
// seeded with a MoE model, a dense model, and a file that is not GGUF at
// all (the size-only degradation path).
func byomLlamaEngine(t *testing.T) config.EngineConfig {
	t.Helper()
	dir := t.TempDir()
	seed := map[string][]byte{
		"moe.gguf":   ggufHeaderBytes("qwen35moe", 8),
		"dense.gguf": ggufHeaderBytes("gemma3", 0),
		"junk.gguf":  []byte("not a gguf at all"),
	}
	for name, data := range seed {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("seed byom file: %v", err)
		}
	}
	return config.EngineConfig{
		Command:        "llama-server",
		Mode:           "server",
		DefaultVariant: "base",
		Variants: map[string]config.EngineVariant{
			"base": {Label: "studio default", Args: []string{"-m", "base.gguf"}},
		},
		ByomDir:  dir,
		ByomArgs: []string{"-m", "{model}"},
	}
}

func cannedGPU(t *testing.T, freeMiB int, fail bool) {
	t.Helper()
	previous := defaultGPUQuery
	defaultGPUQuery = func(ctx context.Context) ([]gpuInfo, error) {
		if fail {
			return nil, fmt.Errorf("no nvidia-smi here")
		}
		return []gpuInfo{{Name: "Test GPU", TotalMiB: 16000, UsedMiB: 16000 - freeMiB}}, nil
	}
	t.Cleanup(func() { defaultGPUQuery = previous })
}

type fitListing struct {
	Variants []struct {
		ID     string `json:"id"`
		Label  string `json:"label"`
		Active bool   `json:"active"`
		Bytes  int64  `json:"bytes"`
		Fit    *struct {
			Verdict  string `json:"verdict"`
			Detail   string `json:"detail"`
			Remedies []struct {
				ID    string `json:"id"`
				Label string `json:"label"`
			} `json:"remedies"`
		} `json:"fit"`
	} `json:"variants"`
	ActiveRemedy *string `json:"activeRemedy"`
}

func listLlamaVariants(t *testing.T, router http.Handler) fitListing {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/engines/llama/variants", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 listing variants, got %d: %s", rec.Code, rec.Body.String())
	}
	var listing fitListing
	if err := json.NewDecoder(rec.Body).Decode(&listing); err != nil {
		t.Fatalf("decode variants: %v", err)
	}
	return listing
}

func TestFitVerdictBoundaries(t *testing.T) {
	cases := []struct {
		fileMiB, freeMiB int
		want             string
	}{
		{fileMiB: 1000, freeMiB: 3560, want: "fits"},
		{fileMiB: 1000, freeMiB: 3559, want: "tight"},
		{fileMiB: 1000, freeMiB: 2280, want: "tight"},
		{fileMiB: 1000, freeMiB: 2279, want: "too_big"},
		{fileMiB: 15000, freeMiB: 12000, want: "too_big"},
		{fileMiB: 0, freeMiB: 0, want: "too_big"},
	}
	for _, tc := range cases {
		if got := fitVerdict(tc.fileMiB, tc.freeMiB); got != tc.want {
			t.Fatalf("fitVerdict(%d, %d) = %q, want %q", tc.fileMiB, tc.freeMiB, got, tc.want)
		}
	}
}

func TestEngineVariantRoutesListByomModelsWithFit(t *testing.T) {
	engineCfg := byomLlamaEngine(t)

	// Tiny files need ~2.5 GiB of free VRAM to earn "fits"; scarce free
	// memory pushes every entry to too_big, and only the MoE model gets a
	// remedy for it.
	cannedGPU(t, 1000, false)
	cfg := testConfig(map[string]config.EngineConfig{"llama": engineCfg})
	listing := listLlamaVariants(t, NewRouter(cfg, lifecycle.NewManager(cfg)))
	if len(listing.Variants) != 4 {
		t.Fatalf("expected base + three byom entries, got %+v", listing.Variants)
	}
	base := listing.Variants[0]
	if base.ID != "base" || !base.Active || base.Fit != nil || base.Bytes != 0 {
		t.Fatalf("expected the configured variant undecorated, got %+v", base)
	}
	dense, junk, moe := listing.Variants[1], listing.Variants[2], listing.Variants[3]
	if dense.ID != "byom:dense.gguf" || junk.ID != "byom:junk.gguf" || moe.ID != "byom:moe.gguf" {
		t.Fatalf("unexpected byom order %+v", listing.Variants[1:])
	}
	if dense.Fit == nil || dense.Fit.Verdict != "too_big" || len(dense.Fit.Remedies) != 0 {
		t.Fatalf("unexpected dense fit %+v", dense.Fit)
	}
	if junk.Fit == nil || junk.Fit.Verdict != "too_big" || len(junk.Fit.Remedies) != 0 || junk.Bytes == 0 {
		t.Fatalf("expected the unparseable file judged on size alone, got %+v", junk)
	}
	if moe.Fit == nil || moe.Fit.Verdict != "too_big" || len(moe.Fit.Remedies) != 1 || moe.Fit.Remedies[0].ID != "cpu-moe" {
		t.Fatalf("expected the MoE model offered cpu-moe, got %+v", moe.Fit)
	}
	if listing.ActiveRemedy == nil || *listing.ActiveRemedy != "" {
		t.Fatalf("expected an empty activeRemedy, got %+v", listing.ActiveRemedy)
	}

	// Plenty of room: everything fits and no remedies are offered.
	cannedGPU(t, 8000, false)
	listing = listLlamaVariants(t, NewRouter(cfg, lifecycle.NewManager(cfg)))
	moe = listing.Variants[3]
	if moe.Fit == nil || moe.Fit.Verdict != "fits" || len(moe.Fit.Remedies) != 0 {
		t.Fatalf("expected fits with no remedy, got %+v", moe.Fit)
	}

	// The in-between band is honest about being tight.
	cannedGPU(t, 2000, false)
	listing = listLlamaVariants(t, NewRouter(cfg, lifecycle.NewManager(cfg)))
	if got := listing.Variants[1].Fit.Verdict; got != "tight" {
		t.Fatalf("expected tight, got %q", got)
	}

	// No nvidia-smi: sizes still listed, fit honestly unknown.
	cannedGPU(t, 0, true)
	listing = listLlamaVariants(t, NewRouter(cfg, lifecycle.NewManager(cfg)))
	moe = listing.Variants[3]
	if moe.Fit == nil || moe.Fit.Verdict != "no_gpu_info" || len(moe.Fit.Remedies) != 0 || moe.Bytes == 0 {
		t.Fatalf("expected no_gpu_info with sizes, got %+v", moe)
	}
}

func TestEngineVariantRoutesKeepNonByomPayloadUnchanged(t *testing.T) {
	cannedGPU(t, 1000, false)
	cfg := testConfig(map[string]config.EngineConfig{
		"whisper": {
			Command:        "whisper-server",
			Mode:           "server",
			DefaultVariant: "large",
			Variants: map[string]config.EngineVariant{
				"large": {Label: "large-v3 (best)", Args: []string{"-m", "large.bin"}},
				"turbo": {Args: []string{"-m", "turbo.bin"}},
			},
		},
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/engines/whisper/variants", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, key := range []string{"bytes", "fit", "activeRemedy"} {
		if strings.Contains(body, key) {
			t.Fatalf("expected the non-byom payload untouched, found %q in %s", key, body)
		}
	}
}

func TestVariantSwitchAppliesAServerDefinedRemedy(t *testing.T) {
	cannedGPU(t, 1000, false)
	cfg := testConfig(map[string]config.EngineConfig{"llama": byomLlamaEngine(t)})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/engines/llama/variant",
		strings.NewReader(`{"id":"byom:moe.gguf","remedy":"cpu-moe"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 applying a remedy, got %d: %s", rec.Code, rec.Body.String())
	}
	var listing fitListing
	if err := json.NewDecoder(rec.Body).Decode(&listing); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if listing.ActiveRemedy == nil || *listing.ActiveRemedy != "cpu-moe" {
		t.Fatalf("expected activeRemedy cpu-moe, got %+v", listing.ActiveRemedy)
	}
	if !listing.Variants[3].Active {
		t.Fatalf("expected the MoE byom variant active, got %+v", listing.Variants)
	}
}

func TestVariantSwitchRejectsRemedyMisuse(t *testing.T) {
	cannedGPU(t, 1000, false)
	cfg := testConfig(map[string]config.EngineConfig{"llama": byomLlamaEngine(t)})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	cases := []struct {
		name string
		body string
		want string
	}{
		{"unknown remedy", `{"id":"byom:moe.gguf","remedy":"turbo-mode"}`, "unknown remedy"},
		{"remedy on a configured variant", `{"id":"base","remedy":"cpu-moe"}`, "apply only to byom"},
		{"cpu-moe on a dense model", `{"id":"byom:dense.gguf","remedy":"cpu-moe"}`, "not a mixture-of-experts"},
		{"cpu-moe on an unparseable model", `{"id":"byom:junk.gguf","remedy":"cpu-moe"}`, "cannot read"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/engines/llama/variant", strings.NewReader(tc.body)))
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("expected 400 containing %q, got %d: %s", tc.want, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestVariantSwitchWarmsTheChatModel(t *testing.T) {
	cannedGPU(t, 1000, false)
	warmed := make(chan chatCompletionRequest, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, req)
			return
		}
		var body chatCompletionRequest
		_ = json.NewDecoder(req.Body).Decode(&body)
		select {
		case warmed <- body:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": "."}}},
		})
	}))
	defer upstream.Close()

	llama := byomLlamaEngine(t)
	llama.HealthURL = upstream.URL + "/health"
	cfg := testConfig(map[string]config.EngineConfig{"llama": llama})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/engines/llama/variant", strings.NewReader(`{"id":"byom:dense.gguf"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 switching variant, got %d: %s", rec.Code, rec.Body.String())
	}

	select {
	case body := <-warmed:
		if body.MaxTokens != 1 || len(body.Messages) != 1 || body.Messages[0].Role != "user" {
			t.Fatalf("unexpected warmup request %+v", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("expected a warmup completion after the switch")
	}
}

func TestVariantSwitchRefusedWhileAStoryProductionIsActive(t *testing.T) {
	t.Chdir(t.TempDir())
	cannedGPU(t, 1000, false)

	scriptJSON := `{"title": "The Llama Tale", "script": [
{"speaker_id": "narrator", "text": "An opening line.", "fact_ids": ["fact-1"]},
{"speaker_id": "nova", "text": "A question?", "fact_ids": ["fact-2"]},
{"speaker_id": "dr-lumen", "text": "An answer.", "fact_ids": ["fact-3"]},
{"speaker_id": "narrator", "text": "A closing line.", "fact_ids": ["fact-1"]}
]}`
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		<-release
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": scriptJSON}}},
		})
	}))
	defer upstream.Close()

	llama := byomLlamaEngine(t)
	llama.HealthURL = upstream.URL + "/health"
	cfg := testConfig(map[string]config.EngineConfig{
		"llama": llama,
		"whisper": {
			Command:        "whisper-server",
			Mode:           "server",
			DefaultVariant: "large",
			Variants: map[string]config.EngineVariant{
				"large": {Args: []string{"-m", "large.bin"}},
				"turbo": {Args: []string{"-m", "turbo.bin"}},
			},
		},
		"audio": helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/stories", strings.NewReader(validStoryRequestJSON())))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 submitting the story, got %d: %s", rec.Code, rec.Body.String())
	}
	var created story.CreateResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	// The production is holding the pipeline (blocked in scripting), so a
	// chat model swap is refused — it would restart the engine writing it.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/engines/llama/variant", strings.NewReader(`{"id":"byom:dense.gguf"}`)))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "story production is running") {
		t.Fatalf("expected 409 while a production runs, got %d: %s", rec.Code, rec.Body.String())
	}

	// Other engines are not the story's scriptwriter; whisper still swaps.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/engines/whisper/variant", strings.NewReader(`{"id":"turbo"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 switching whisper mid-production, got %d: %s", rec.Code, rec.Body.String())
	}

	close(release)
	waitGatewayStoryStatus(t, router, created.ID, story.StatusComplete)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/engines/llama/variant", strings.NewReader(`{"id":"byom:dense.gguf"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 after the production finished, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAudioEncodeSavesDeliveryFormats(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{"ffmpeg": ffmpegHelperEngine()})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	encode := func(format string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", "clip.wav")
		if err != nil {
			t.Fatalf("create multipart file: %v", err)
		}
		if _, err := part.Write([]byte("RIFF fake wav bytes")); err != nil {
			t.Fatalf("write multipart file: %v", err)
		}
		_ = writer.WriteField("format", format)
		if err := writer.Close(); err != nil {
			t.Fatalf("close multipart writer: %v", err)
		}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/audio/encode", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		router.ServeHTTP(rec, req)
		return rec
	}

	rec := encode("mp3")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "audio/mpeg" {
		t.Fatalf("expected audio/mpeg, got %q", got)
	}
	if rec.Body.Len() == 0 {
		t.Fatalf("expected encoded bytes back")
	}

	if rec := encode("flac"); rec.Code != http.StatusBadRequest {
		t.Fatalf("expected an unknown format to be refused, got %d", rec.Code)
	}
}

func TestAudioEncodeWithoutFfmpegIsUnavailable(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{"audio": helperEngine("speech")})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "clip.wav")
	_, _ = part.Write([]byte("RIFF"))
	_ = writer.WriteField("format", "mp3")
	_ = writer.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/encode", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAudioDecodeWithoutFfmpegIsUnavailable(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{"audio": helperEngine("speech")})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "x.wma")
	_, _ = part.Write([]byte("anything"))
	_ = writer.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/decode", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestStoryExportWithoutFfmpegIsUnavailable(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{"audio": helperEngine("speech")})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/audio/formats", nil))
	var formats struct {
		Formats []struct {
			Available bool `json:"available"`
		} `json:"formats"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&formats); err != nil {
		t.Fatalf("decode formats: %v", err)
	}
	for _, format := range formats.Formats {
		if format.Available {
			t.Fatalf("no ffmpeg is configured, so nothing should be available: %+v", formats.Formats)
		}
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/stories/story_20260101_000000_001/export", strings.NewReader(`{"format":"mp3"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), string(story.CodeExportUnavailable)) {
		t.Fatalf("expected export_unavailable, got %s", rec.Body.String())
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

func TestSpeechRoutesToSelectedModelEngine(t *testing.T) {
	dir := t.TempDir()
	defaultRef := filepath.Join(dir, "default.wav")
	if err := os.WriteFile(defaultRef, validWAVBytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	audioCfg := helperEngine("fail")
	audioCfg.DefaultVoiceRef = defaultRef
	audioCfg.DefaultVoiceText = "default reference words"
	cfg := testConfig(map[string]config.EngineConfig{
		"audio":     audioCfg,
		"voxcpm2":   helperEngine("speech-require-voice"),
		"omnivoice": helperEngine("speech"),
	})
	manager := lifecycle.NewManager(cfg)
	router := NewRouter(cfg, manager)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"hello","model":"voxcpm2","format":"wav"}`))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected selected model status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if manager.Health().Engines["voxcpm2"].LastSuccessAt == nil {
		t.Fatalf("expected voxcpm2 to serve the request")
	}
	if manager.Health().Engines["audio"].LastSuccessAt != nil {
		t.Fatalf("default audio engine must not serve a voxcpm2 request")
	}
}

func TestSpeechRejectsUnknownModelEngine(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{"audio": helperEngine("speech")})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"hello","model":"not-a-model","format":"wav"}`))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected unknown model status 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestVoiceLoopRoutesToSelectedSpeechModel(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"selected model reply"}}]}`))
	}))
	defer upstream.Close()
	cfg := testConfig(map[string]config.EngineConfig{
		"audio":   helperEngine("fail"),
		"voxcpm2": helperEngine("speech"),
		"llama":   {Command: "llama-server", HealthURL: upstream.URL + "/health"},
	})
	manager := lifecycle.NewManager(cfg)
	router := NewRouter(cfg, manager)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("message", "hello")
	_ = writer.WriteField("speech_model", "voxcpm2")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/voice", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected selected voice-loop model status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if manager.Health().Engines["voxcpm2"].LastSuccessAt == nil {
		t.Fatalf("expected voxcpm2 to speak the voice-loop reply")
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
		case "speech-tone-slow":
			time.Sleep(500 * time.Millisecond)
			runToneSpeechHelper(helperArgs)
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
		case "diarize-sortformer":
			if helperArg(helperArgs, "--audio") == "" || helperArg(helperArgs, "--session-option") != "session_len_sec=30" {
				fmt.Fprintln(os.Stderr, "missing Sortformer audio or graph-window argument")
				os.Exit(2)
			}
			fmt.Fprintln(os.Stdout, `speaker_turns=[{"start_sample":1280,"end_sample":126720,"speaker_id":"SPEAKER_00","confidence":0.8},{"start_sample":135680,"end_sample":227840,"speaker_id":"SPEAKER_01","confidence":0.9}]`)
			os.Exit(0)
		case "diarize-sherpa":
			// Mimics sherpa-onnx: config lines and noise on stdout around
			// the speaker spans.
			fmt.Fprintln(os.Stdout, "OfflineSpeakerDiarizationConfig(...)")
			fmt.Fprintln(os.Stdout, "Started")
			fmt.Fprintln(os.Stdout, "0.031 -- 6.578 speaker_00")
			fmt.Fprintln(os.Stdout, "8.401 -- 14.408 speaker_01")
			fmt.Fprintln(os.Stdout, "15.877 -- 21.327 speaker_00")
			os.Exit(0)
		case "diarize-sherpa-five":
			found := false
			for _, arg := range helperArgs {
				found = found || arg == "--clustering.num-clusters=5"
			}
			if !found {
				fmt.Fprintln(os.Stderr, "missing pinned five-speaker cluster count")
				os.Exit(2)
			}
			fmt.Fprintln(os.Stdout, "0.031 -- 6.578 speaker_00")
			fmt.Fprintln(os.Stdout, "8.401 -- 14.408 speaker_01")
			fmt.Fprintln(os.Stdout, "15.877 -- 21.327 speaker_00")
			os.Exit(0)
		case "ffmpeg":
			runFFmpegHelper(helperArgs)
		case "import":
			runImportHelper(helperArgs, "ID3\x03")
		case "import-not-audio":
			runImportHelper(helperArgs, "<!doctype html>")
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

// runFFmpegHelper stands in for the operator's ffmpeg: it answers the
// encoder probe and performs a stand-in transcode over the real paths, so
// the file-path engine mode is exercised rather than the byte seam.
func runFFmpegHelper(args []string) {
	for _, arg := range args {
		if arg == "-encoders" {
			fmt.Fprint(os.Stdout, " V..... = Video\n A....D libmp3lame           MP3\n A....D libopus              Opus\n")
			os.Exit(0)
		}
	}
	in := helperArg(args, "-i")
	codec := helperArg(args, "-c:a")
	out := args[len(args)-1]
	if in == "" || codec == "" || strings.HasPrefix(out, "-") {
		fmt.Fprintln(os.Stderr, "missing -i/-c:a/output")
		os.Exit(2)
	}
	source, err := os.ReadFile(in)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	// A pcm_s16le run is a decode: whatever went in, a WAV comes out.
	if codec == "pcm_s16le" {
		if err := os.WriteFile(out, validWAVBytes(), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	if err := os.WriteFile(out, append([]byte("ID3\x03\x00\x00\x00"), source[:len(source)/8]...), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

// runImportHelper stands in for yt-dlp: it echoes the source title the way
// --print does and writes prefix-tagged bytes to -o, so the gateway's
// container sniffing is exercised for real.
func runImportHelper(args []string, prefix string) {
	out := helperArg(args, "-o")
	if out == "" {
		fmt.Fprintln(os.Stderr, "missing -o")
		os.Exit(2)
	}
	url := args[len(args)-1]
	if !strings.HasPrefix(url, "http") {
		fmt.Fprintf(os.Stderr, "expected a URL argument, got %q\n", url)
		os.Exit(2)
	}
	fmt.Fprintln(os.Stdout, "Round the Horne — Series 3, Episode 1")
	if err := os.WriteFile(out, []byte(prefix+strings.Repeat("x", 64)), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
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

// The resident audio server wants forward slashes in the voice reference
// whatever host the gateway runs on. filepath.ToSlash cannot do this job: it
// converts the host separator, so on Linux it silently passes a Windows
// config path straight through. This test fails on every platform if that
// regresses, which the integration test could not.
func TestSlashPathIsHostIndependent(t *testing.T) {
	tests := map[string]string{
		`C:\voices\default.wav`:      "C:/voices/default.wav",
		`\\server\share\ref.wav`:     "//server/share/ref.wav",
		"/home/james/voices/ref.wav": "/home/james/voices/ref.wav",
		"already/slashed.wav":        "already/slashed.wav",
		"":                           "",
	}
	for input, want := range tests {
		if got := slashPath(input); got != want {
			t.Errorf("slashPath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestVoiceCloneRecordsProvenance(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{"whisper": helperEngine("transcribe")})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("name", "Round the Horne B")
	_ = writer.WriteField("transcript", "a reference line of speech")
	_ = writer.WriteField("source_name", "round-the-horne-s3e1.mp3")
	_ = writer.WriteField("source_speaker", "B")
	_ = writer.WriteField("source_start_sec", "412.50")
	_ = writer.WriteField("source_end_sec", "424.25")
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
		t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created actorVoiceSummary
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Source == nil {
		t.Fatalf("expected provenance on the created voice, got %+v", created)
	}
	if created.Source.Name != "round-the-horne-s3e1.mp3" || created.Source.Speaker != "B" {
		t.Fatalf("unexpected provenance %+v", created.Source)
	}
	if created.Source.StartSec != 412.50 || created.Source.EndSec != 424.25 {
		t.Fatalf("unexpected provenance times %+v", created.Source)
	}

	// It survives a reload: provenance is stored, not just echoed.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/voices", nil))
	var list voiceListResponse
	if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(list.Voices) != 1 || list.Voices[0].Source == nil || list.Voices[0].Source.Speaker != "B" {
		t.Fatalf("provenance did not persist: %+v", list.Voices)
	}

	t.Run("a hand-uploaded reference carries none", func(t *testing.T) {
		var plain bytes.Buffer
		w := multipart.NewWriter(&plain)
		_ = w.WriteField("name", "Hand upload")
		_ = w.WriteField("transcript", "another reference line")
		p, _ := w.CreateFormFile("file", "reference.wav")
		_, _ = p.Write(validWAVBytes())
		_ = w.Close()

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/voices", &plain)
		req.Header.Set("Content-Type", w.FormDataContentType())
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("expected status 201, got %d: %s", rec.Code, rec.Body.String())
		}
		var plainClone actorVoiceSummary
		if err := json.NewDecoder(rec.Body).Decode(&plainClone); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if plainClone.Source != nil {
			t.Fatalf("expected no invented provenance, got %+v", plainClone.Source)
		}
	})
}

func TestAudioImportFetchesThroughYtdlp(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{"ytdlp": helperEngine("import")})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/import", strings.NewReader(`{"url": "https://example.com/episode"}`))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "audio/mpeg" {
		t.Fatalf("expected sniffed audio/mpeg, got %q", got)
	}
	// The title survives the round trip, percent-encoded so a non-ASCII
	// title stays a legal header value.
	title, err := url.PathUnescape(rec.Header().Get("X-Import-Title"))
	if err != nil {
		t.Fatalf("decode title header: %v", err)
	}
	if title != "Round the Horne — Series 3, Episode 1" {
		t.Fatalf("unexpected title %q", title)
	}
	if !strings.HasPrefix(rec.Body.String(), "ID3") {
		t.Fatalf("expected the fetched bytes, got %q", rec.Body.String()[:8])
	}
}

func TestAudioImportRejectsBadRequests(t *testing.T) {
	t.Run("no engine configured", func(t *testing.T) {
		cfg := testConfig(map[string]config.EngineConfig{"audio": helperEngine("speech")})
		router := NewRouter(cfg, lifecycle.NewManager(cfg))
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/audio/import", strings.NewReader(`{"url": "https://example.com/x"}`))
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected status 503, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	cfg := testConfig(map[string]config.EngineConfig{"ytdlp": helperEngine("import")})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	// Anything that is not an http(s) URL is refused before yt-dlp runs:
	// local paths, other schemes, and argv that would parse as a flag.
	for _, body := range []string{
		`{"url": ""}`,
		`{"url": "file:///C:/Windows/win.ini"}`,
		`{"url": "C:\\Windows\\win.ini"}`,
		`{"url": "--batch-file=urls.txt"}`,
		`{"url": "https://"}`,
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/audio/import", strings.NewReader(body))
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected status 400 for %s, got %d: %s", body, rec.Code, rec.Body.String())
		}
	}

	t.Run("method not allowed", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/audio/import", nil)
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected status 405, got %d", rec.Code)
		}
	})
}

func TestAudioImportRejectsUndecodableDownload(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{"ytdlp": helperEngine("import-not-audio")})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/import", strings.NewReader(`{"url": "https://example.com/not-audio"}`))
	router.ServeHTTP(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("expected a non-audio download to fail, got 200")
	}
	if !strings.Contains(strings.ToLower(rec.Body.String()), "audio") {
		t.Fatalf("expected an explanation mentioning audio, got %s", rec.Body.String())
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

func TestTranscriptionSegmentsViaWhisperServer(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if err := req.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("parse upstream multipart: %v", err)
		}
		if req.FormValue("response_format") != "verbose_json" {
			t.Errorf("expected verbose_json, got %q", req.FormValue("response_format"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"a b","segments":[
			{"id":0,"start":0.0,"end":2.5,"text":" Hello there. "},
			{"id":1,"start":2.5,"end":4.0,"text":"  "},
			{"id":2,"start":4.0,"end":6.0,"text":" Second thought."}
		]}`))
	}))
	defer upstream.Close()

	cfg := testConfig(map[string]config.EngineConfig{
		"whisper": {Command: "whisper-server", Mode: "server", HealthURL: upstream.URL + "/health"},
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "sample.wav")
	_, _ = part.Write(validWAVBytes())
	_ = writer.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions?format=segments", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Text     string `json:"text"`
		Segments []struct {
			Start   float64 `json:"start"`
			End     float64 `json:"end"`
			Text    string  `json:"text"`
			Speaker string  `json:"speaker"`
		} `json:"segments"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The whitespace-only segment is dropped; text is trimmed; speaker is
	// present (empty) as the first-class slot diarization fills later.
	if len(resp.Segments) != 2 {
		t.Fatalf("expected 2 segments, got %+v", resp.Segments)
	}
	if resp.Segments[0].Text != "Hello there." || resp.Segments[0].End != 2.5 {
		t.Fatalf("unexpected first segment: %+v", resp.Segments[0])
	}
	if resp.Segments[1].Start != 4.0 || resp.Segments[1].Speaker != "" {
		t.Fatalf("unexpected second segment: %+v", resp.Segments[1])
	}
	if resp.Text != "Hello there. Second thought." {
		t.Fatalf("unexpected joined text: %q", resp.Text)
	}
}

func TestTranscriptionSegmentsNeedServerMode(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"whisper": {Command: "whisper-cli", Mode: "subprocess"},
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "sample.wav")
	_, _ = part.Write(validWAVBytes())
	_ = writer.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions?format=segments", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for subprocess whisper, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDiarizationRoute(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"diarize":        helperEngine("diarize-sortformer"),
		"diarize-sherpa": helperEngine("diarize-sherpa"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "sample.wav")
	_, _ = part.Write(wav.SyntheticTone(28 * wav.ToneSampleRate))
	_ = writer.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/diarization", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Provider string `json:"provider"`
		Spans    []struct {
			Start   float64 `json:"start"`
			End     float64 `json:"end"`
			Speaker string  `json:"speaker"`
		} `json:"spans"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Provider != "sortformer" {
		t.Fatalf("provider = %q, want sortformer", resp.Provider)
	}
	if len(resp.Spans) != 2 {
		t.Fatalf("expected 2 spans, got %+v", resp.Spans)
	}
	if resp.Spans[0].Speaker != "A" || resp.Spans[1].Speaker != "B" {
		t.Fatalf("cluster labels wrong: %+v", resp.Spans)
	}
	if resp.Spans[1].Start != 8.48 || resp.Spans[1].End != 14.24 {
		t.Fatalf("span timing wrong: %+v", resp.Spans[1])
	}
}

func TestDiarizationRouteExplicitSpeakerCountUsesSherpa(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"diarize":        helperEngine("diarize-sortformer"),
		"diarize-sherpa": helperEngine("diarize-sherpa-five"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "sample.wav")
	_, _ = part.Write(wav.SyntheticTone(28 * wav.ToneSampleRate))
	_ = writer.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/diarization?speakers=5", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Provider string `json:"provider"`
		Spans    []any  `json:"spans"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Provider != "sherpa-onnx" || len(resp.Spans) != 3 {
		t.Fatalf("unexpected fallback response: %+v", resp)
	}
}

func TestDiarizationRouteLongRecordingUsesSherpa(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"diarize":        helperEngine("diarize-sortformer"),
		"diarize-sherpa": helperEngine("diarize-sherpa"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "sample.wav")
	_, _ = part.Write(wav.SyntheticTone(121 * wav.ToneSampleRate))
	_ = writer.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/diarization", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Provider != "sherpa-onnx" {
		t.Fatalf("provider = %q, want sherpa-onnx", resp.Provider)
	}
}

func TestDiarizationRouteIncompatibleSampleRateUsesSherpa(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"diarize":        helperEngine("diarize-sortformer"),
		"diarize-sherpa": helperEngine("diarize-sherpa"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	data := wav.Encode(
		wav.Format{Channels: 1, SampleRate: 24000, BitsPerSample: 16},
		make([]byte, 24_000*2),
	)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "sample.wav")
	_, _ = part.Write(data)
	_ = writer.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/diarization", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Provider != "sherpa-onnx" {
		t.Fatalf("provider = %q, want sherpa-onnx", resp.Provider)
	}
}

func TestDiarizationRouteWithoutEngine(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{"llama": {Command: "llama-server"}})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "sample.wav")
	_, _ = part.Write(validWAVBytes())
	_ = writer.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/diarization", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 without diarize engine, got %d", rec.Code)
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
	var created actorVoiceSummary
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.ID == "" || created.Name != "Test Voice" || created.Transcript != "transcribed text" {
		t.Fatalf("unexpected created voice %+v", created)
	}
	if created.AudioURL != "/v1/voices/"+created.ID+"/audio" {
		t.Fatalf("unexpected audio url %q", created.AudioURL)
	}
	if created.Analysis == nil || created.Analysis.ContentSHA256 == "" || created.Analysis.Fitness != "unsupported" {
		t.Fatalf("voice response omitted persisted reference fitness: %+v", created.Analysis)
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

func TestCharacterVoiceAuthoringAndPreviewThroughGateway(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"omnivoice": helperEngine("design"),
	})
	r := NewRouter(cfg, lifecycle.NewManager(cfg)).(*router)
	r.voices = voice.NewStore(filepath.Join(t.TempDir(), "voices"))
	actor, err := r.voices.Save("Mara", "reference words", validWAVBytes(), false)
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/voices/"+actor.ID+"/characters", strings.NewReader(`{"name":"Weathered keeper","direction":"elderly, low pitch"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create Character Voice status = %d: %s", rec.Code, rec.Body.String())
	}
	var character voice.CharacterVoice
	if err := json.NewDecoder(rec.Body).Decode(&character); err != nil {
		t.Fatal(err)
	}
	if character.ActorVoiceID != actor.ID || character.Name != "Weathered keeper" || character.ID == "" {
		t.Fatalf("unexpected Character Voice: %+v", character)
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/voices/"+actor.ID+"/characters", strings.NewReader(`{"name":"Harbour guide","direction":"young adult, upbeat"}`)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create sibling status = %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/voices", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list Actor Voices status = %d: %s", rec.Code, rec.Body.String())
	}
	var grouped voiceListResponse
	if err := json.NewDecoder(rec.Body).Decode(&grouped); err != nil {
		t.Fatal(err)
	}
	if len(grouped.Voices) != 1 || grouped.Voices[0].Kind != "actor_voice" || len(grouped.Voices[0].CharacterVoices) != 2 {
		t.Fatalf("Character Voices were not grouped beneath Actor Voice: %+v", grouped.Voices)
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v1/character-voices/"+character.ID, strings.NewReader(`{"name":"Weathered lighthouse keeper","direction":"elderly, whisper"}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"direction":"elderly, whisper"`) {
		t.Fatalf("edit Character Voice status = %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/character-voices/"+character.ID+"/preview", strings.NewReader(`{"sample_text":"Keep the lamp lit."}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("preview Character Voice status = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"preview_audio_url":"/v1/character-voices/`+character.ID+`/preview/audio"`) {
		t.Fatalf("preview response missing replaceable audio URL: %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/character-voices/"+character.ID+"/preview", strings.NewReader(`{"sample_text":"Replacement line."}`)))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"sample_text":"Replacement line."`) {
		t.Fatalf("replace Character Voice preview status = %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/character-voices/"+character.ID+"/preview/audio", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "audio/wav" || !bytes.HasPrefix(rec.Body.Bytes(), []byte("RIFF")) {
		t.Fatalf("preview audio status = %d type=%q bytes=%q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Bytes())
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/v1/voices/"+actor.ID, nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("Actor Voice with children delete status = %d, want 409: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/v1/character-voices/"+character.ID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete Character Voice status = %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCharacterVoiceGatewayRejectsInvalidAndBusyRequestsWithoutPartialRecords(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"omnivoice": helperEngine("design"),
	})
	r := NewRouter(cfg, lifecycle.NewManager(cfg)).(*router)
	r.voices = voice.NewStore(filepath.Join(t.TempDir(), "voices"))
	actor, err := r.voices.Save("Mara", "reference words", validWAVBytes(), false)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		status int
	}{
		{name: "missing parent", method: http.MethodPost, path: "/v1/voices/voice_missing/characters", body: `{"name":"Keeper","direction":"elderly"}`, status: http.StatusNotFound},
		{name: "unknown field", method: http.MethodPost, path: "/v1/voices/" + actor.ID + "/characters", body: `{"name":"Keeper","direction":"elderly","path":"outside"}`, status: http.StatusBadRequest},
		{name: "overlong direction", method: http.MethodPost, path: "/v1/voices/" + actor.ID + "/characters", body: `{"name":"Keeper","direction":"` + strings.Repeat("d", voice.MaxCharacterVoiceDirectionChars+1) + `"}`, status: http.StatusBadRequest},
		{name: "missing Character Voice", method: http.MethodPut, path: "/v1/character-voices/character_missing", body: `{"name":"Keeper","direction":"elderly"}`, status: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(test.method, test.path, strings.NewReader(test.body)))
			if rec.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, test.status, rec.Body.String())
			}
		})
	}
	characters, err := r.voices.ListCharacterVoices(actor.ID)
	if err != nil || len(characters) != 0 {
		t.Fatalf("invalid requests left partial Character Voices: %+v err=%v", characters, err)
	}

	character, err := r.voices.CreateCharacterVoice(actor.ID, "Keeper", "elderly, low pitch")
	if err != nil {
		t.Fatal(err)
	}
	fake := engine.NewFake()
	fake.Handle("omnivoice", func(engine.Spec) (engine.Result, error) {
		return engine.Result{Output: wav.SyntheticTone(160)}, nil
	})
	r.engines = fake
	release, ok := fake.Reserve("omnivoice")
	if !ok {
		t.Fatal("reserve fixture OmniVoice")
	}
	defer release()

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/character-voices/"+character.ID+"/preview", strings.NewReader(`{"sample_text":"Keep the lamp lit."}`)))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("busy preview status = %d, want 429: %s", rec.Code, rec.Body.String())
	}
	loaded, ok, err := r.voices.LoadCharacterVoice(character.ID)
	if err != nil || !ok || loaded.Preview != nil {
		t.Fatalf("busy preview partially updated Character Voice: ok=%v err=%v voice=%+v", ok, err, loaded)
	}
}

func TestCharacterVoiceGatewayRejectsStalePreviewAfterDirectionChange(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"omnivoice": helperEngine("design"),
	})
	r := NewRouter(cfg, lifecycle.NewManager(cfg)).(*router)
	r.voices = voice.NewStore(filepath.Join(t.TempDir(), "voices"))
	actor, err := r.voices.Save("Mara", "reference words", validWAVBytes(), false)
	if err != nil {
		t.Fatal(err)
	}
	character, err := r.voices.CreateCharacterVoice(actor.ID, "Keeper", "quiet and guarded")
	if err != nil {
		t.Fatal(err)
	}
	fake := engine.NewFake()
	fake.Handle("omnivoice", func(engine.Spec) (engine.Result, error) {
		if _, err := r.voices.UpdateCharacterVoice(character.ID, character.Name, "urgent and breathless"); err != nil {
			t.Fatalf("direction change during synthesis: %v", err)
		}
		return engine.Result{Output: wav.SyntheticTone(160)}, nil
	})
	r.engines = fake

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/character-voices/"+character.ID+"/preview", strings.NewReader(`{"sample_text":"Keep the lamp lit."}`)))
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "changed while preview was generating") {
		t.Fatalf("stale preview status = %d, want 409 conflict: %s", rec.Code, rec.Body.String())
	}
	loaded, ok, err := r.voices.LoadCharacterVoice(character.ID)
	if err != nil || !ok || loaded.Direction != "urgent and breathless" || loaded.Preview != nil {
		t.Fatalf("stale preview overwrote direction change: ok=%v err=%v character=%+v", ok, err, loaded)
	}
}

func TestWhisperVADConfigurationAndSpokenDuration(t *testing.T) {
	without := config.EngineConfig{Mode: "server", Args: []string{"--model", "whisper.bin"}}
	with := config.EngineConfig{Mode: "server", Args: []string{"--vad", "--vad-model", "silero.bin"}}
	variant := config.EngineConfig{Mode: "server", DefaultVariant: "quality", Variants: map[string]config.EngineVariant{
		"quality": {Args: []string{"--vad"}},
	}}
	if whisperVADConfigured(without) || !whisperVADConfigured(with) || !whisperVADConfigured(variant) {
		t.Fatalf("VAD configuration detection was not exact")
	}
	duration := spokenSegmentDuration([]transcriptSegment{
		{Start: 2, End: 4}, {Start: 0, End: 1.5}, {Start: 1, End: 3}, {Start: -1, End: 20},
	})
	if duration != 4*time.Second {
		t.Fatalf("overlapping VAD spans counted as %v", duration)
	}
}

func TestSpeechViaResidentAudioServer(t *testing.T) {
	t.Chdir(t.TempDir())
	var gotRequests []audioServerSpeechRequest
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/audio/speech" {
			t.Errorf("unexpected upstream path %q", req.URL.Path)
		}
		var body audioServerSpeechRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Errorf("decode speech request: %v", err)
		}
		gotRequests = append(gotRequests, body)
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wav.SyntheticTone(wav.ToneSampleRate))
	}))
	defer upstream.Close()

	cfg := testConfig(map[string]config.EngineConfig{
		"audio": {
			Command:          "audiocpp_server",
			Mode:             "server",
			HealthURL:        upstream.URL + "/health",
			DefaultVoiceRef:  `C:\voices\default.wav`,
			DefaultVoiceText: "default reference words",
		},
	})
	manager := lifecycle.NewManager(cfg)
	router := NewRouter(cfg, manager)

	// Default voice speech.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"hello resident","format":"wav"}`))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	duration, err := wav.Duration(rec.Body.Bytes())
	if err != nil {
		t.Fatalf("decode response wav: %v", err)
	}
	// One second of tone plus the 500ms of padding.
	if duration < 1400*time.Millisecond || duration > 1600*time.Millisecond {
		t.Fatalf("unexpected padded duration %s", duration)
	}
	if len(gotRequests) != 1 {
		t.Fatalf("expected one upstream request, got %d", len(gotRequests))
	}
	if gotRequests[0].Model != "tts" || gotRequests[0].Input != "hello resident" {
		t.Fatalf("unexpected upstream request %+v", gotRequests[0])
	}
	if gotRequests[0].VoiceRef != "C:/voices/default.wav" || gotRequests[0].ReferenceText != "default reference words" {
		t.Fatalf("expected forward-slashed default voice, got %+v", gotRequests[0])
	}

	// Cloned voice speech routes the stored reference through.
	var createBody bytes.Buffer
	writer := multipart.NewWriter(&createBody)
	_ = writer.WriteField("name", "Resident Clone")
	_ = writer.WriteField("transcript", "clone reference words")
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
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/voices", &createBody)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected voice create status 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created actorVoiceSummary
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode voice create response: %v", err)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"hello clone","voice":"`+created.ID+`","format":"wav"}`))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected cloned speech status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	last := gotRequests[len(gotRequests)-1]
	if !strings.HasSuffix(last.VoiceRef, "/ref.wav") || strings.Contains(last.VoiceRef, `\`) {
		t.Fatalf("expected forward-slashed clone reference path, got %q", last.VoiceRef)
	}
	if last.ReferenceText != "clone reference words" {
		t.Fatalf("expected clone transcript, got %+v", last)
	}
	if manager.Health().Engines["audio"].LastSuccessAt == nil {
		t.Fatalf("expected audio lastSuccessAt to be recorded")
	}
}

func TestImageDescriptionSpeaksViaResidentAudioServer(t *testing.T) {
	var speechHits int
	audioUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/audio/speech" {
			t.Errorf("unexpected audio path %q", req.URL.Path)
		}
		speechHits++
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wav.SyntheticTone(160))
	}))
	defer audioUpstream.Close()
	visionUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"a described scene"}}]}`))
	}))
	defer visionUpstream.Close()

	cfg := testConfig(map[string]config.EngineConfig{
		"vision": {Command: "llama-server", HealthURL: visionUpstream.URL + "/health"},
		"audio": {
			Command:         "audiocpp_server",
			Mode:            "server",
			HealthURL:       audioUpstream.URL + "/health",
			DefaultVoiceRef: `C:\voices\default.wav`,
		},
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	imageB64 := base64.StdEncoding.EncodeToString(validPNGBytes())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/images/descriptions", strings.NewReader(`{"image_b64":"`+imageB64+`"}`))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if speechHits != 1 {
		t.Fatalf("expected description to speak through the resident server, got %d hits", speechHits)
	}
}

func TestSpeechResidentServerFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"model exploded"}}`))
	}))
	defer upstream.Close()

	cfg := testConfig(map[string]config.EngineConfig{
		"audio": {
			Command:         "audiocpp_server",
			Mode:            "server",
			HealthURL:       upstream.URL + "/health",
			DefaultVoiceRef: `C:\voices\default.wav`,
		},
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"hello","format":"wav"}`))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("expected status 502, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "model exploded") {
		t.Fatalf("expected upstream error detail, got %s", rec.Body.String())
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
	var created actorVoiceSummary
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
	var created actorVoiceSummary
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
	var created actorVoiceSummary
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

func validSketchRequestJSON() string {
	return `{
  "subject": "a shop that only sells apologies",
  "mode": "sketch",
  "premise": "A customer wants to return an apology that did not fit.",
  "style": "1960s BBC radio comedy: fast, silly, groan-worthy puns.",
  "target_seconds": 60,
  "voice_mode": "placeholder"
}`
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

func TestModelsCatalog(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "chat.gguf"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "models.json")
	if err := os.WriteFile(manifestPath, []byte(`{"models":[
		{"id":"chat","engine":"llama","path":"chat.gguf","bytes":5},
		{"id":"art","engine":"sd","path":"missing.safetensors","bytes":100}
	]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig(map[string]config.EngineConfig{"llama": {Command: "llama-server"}})
	cfg.Models = &config.ModelsConfig{Manifest: manifestPath, Root: root}
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models/catalog", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Root   string `json:"root"`
		Models []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"models"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Root != root {
		t.Errorf("root: got %q", resp.Root)
	}
	state := map[string]string{}
	for _, m := range resp.Models {
		state[m.ID] = m.State
	}
	if state["chat"] != "present" {
		t.Errorf("chat state: got %q", state["chat"])
	}
	if state["art"] != "missing" {
		t.Errorf("art state: got %q", state["art"])
	}
}

type gatewayDiscoveryRunner struct{ calls int }

func (r *gatewayDiscoveryRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	r.calls++
	joined := strings.Join(args, " ")
	switch {
	case strings.HasSuffix(joined, "list --json"):
		return []byte(`[{"id":"dramabox_q8_0","family":"dramabox"}]`), nil
	case strings.Contains(joined, "info dramabox_q8_0 --json"):
		return []byte(`{"id":"dramabox_q8_0"}`), nil
	case joined == "--list-loaders --json":
		return []byte(`{"loaders":{"dramabox":{"tasks":{"tts":["offline"]}}}}`), nil
	default:
		return nil, fmt.Errorf("unexpected discovery command: %s", joined)
	}
}

func TestModelsCatalogMergesDistinctCachedReadinessStates(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "models.json")
	manifest := `{"models":[{"id":"dramabox","engine":"dramabox","family":"dramabox","path":"missing.gguf","bytes":5,"sha256":"` + strings.Repeat("0", 64) + `","license":"test","packageId":"dramabox_q8_0","revision":"fixed","downloadUrl":"https://example.invalid/model.gguf"}]}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(map[string]config.EngineConfig{"dramabox": {Command: "audiocpp_server"}})
	cfg.Models = &config.ModelsConfig{Manifest: manifestPath, Root: root, Discovery: &config.ModelDiscoveryConfig{
		PythonCommand: "python", ManagerScript: "manager.py", AudioCLI: "audiocpp_cli", AllowedPackages: []string{"dramabox_q8_0"},
	}}
	router := NewRouter(cfg, lifecycle.NewManager(cfg)).(*router)
	runner := &gatewayDiscoveryRunner{}
	router.discoveryRunner = runner
	for attempt := 0; attempt < 2; attempt++ {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models/catalog", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("catalog: %d %s", rec.Code, rec.Body.String())
		}
		var response struct {
			DiscoveryError string `json:"discoveryError"`
			Models         []struct {
				Installable, PackageKnown, LoaderAvailable, Present, Configured, Healthy bool
			} `json:"models"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
			t.Fatal(err)
		}
		if response.DiscoveryError != "" || len(response.Models) != 1 {
			t.Fatalf("unexpected catalog: %+v", response)
		}
		model := response.Models[0]
		if !model.Installable || !model.PackageKnown || !model.LoaderAvailable || !model.Configured || model.Present || model.Healthy {
			t.Fatalf("readiness states were conflated: %+v", model)
		}
	}
	if runner.calls != 3 {
		t.Fatalf("discovery was not cached: %d calls", runner.calls)
	}
}

func TestModelInstallHTTPRequiresOpaqueFreshConfirmation(t *testing.T) {
	payload := []byte("small verified model")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payload) }))
	defer server.Close()
	sum := sha256.Sum256(payload)
	root := t.TempDir()
	model := models.Model{
		ID: "dramabox", Engine: "dramabox", Family: "dramabox", Path: "models/dramabox.gguf", Bytes: int64(len(payload)),
		SHA256: hex.EncodeToString(sum[:]), License: "test", PackageID: "dramabox_q8_0", Revision: "fixed", DownloadURL: server.URL + "/model.gguf",
	}
	cfg := testConfig(map[string]config.EngineConfig{"dramabox": {Command: "audiocpp_server"}})
	router := NewRouter(cfg, lifecycle.NewManager(cfg)).(*router)
	router.installer = models.NewInstaller(models.Manifest{Models: []models.Model{model}}, root, []string{model.PackageID})
	router.installer.Client = server.Client()
	router.installer.Free = func(string) (uint64, error) { return 1 << 40, nil }

	previewRec := httptest.NewRecorder()
	router.ServeHTTP(previewRec, httptest.NewRequest(http.MethodPost, "/v1/models/dramabox/install/preview", nil))
	if previewRec.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", previewRec.Code, previewRec.Body.String())
	}
	var preview models.InstallPreview
	if err := json.NewDecoder(previewRec.Body).Decode(&preview); err != nil || preview.ConfirmationID == "" {
		t.Fatalf("preview decode: %+v %v", preview, err)
	}
	injected := httptest.NewRecorder()
	router.ServeHTTP(injected, httptest.NewRequest(http.MethodPost, "/v1/models/dramabox/install", strings.NewReader(`{"confirmationId":"`+preview.ConfirmationID+`","source":"https://evil.invalid"}`)))
	if injected.Code != http.StatusBadRequest {
		t.Fatalf("browser-controlled install input accepted: %d", injected.Code)
	}

	installRec := httptest.NewRecorder()
	router.ServeHTTP(installRec, httptest.NewRequest(http.MethodPost, "/v1/models/dramabox/install", strings.NewReader(`{"confirmationId":"`+preview.ConfirmationID+`"}`)))
	if installRec.Code != http.StatusAccepted {
		t.Fatalf("install: %d %s", installRec.Code, installRec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(installRec.Body).Decode(&created)
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		job, ok := router.jobs.Get(created.ID)
		if ok && job.Status == "complete" {
			break
		}
		if ok && job.Status == "failed" {
			t.Fatalf("install failed: %+v", job)
		}
		time.Sleep(time.Millisecond)
	}
	if data, err := os.ReadFile(filepath.Join(root, model.Path)); err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("installed artifact: %v", err)
	}
	replay := httptest.NewRecorder()
	router.ServeHTTP(replay, httptest.NewRequest(http.MethodPost, "/v1/models/dramabox/install", strings.NewReader(`{"confirmationId":"`+preview.ConfirmationID+`"}`)))
	if replay.Code != http.StatusConflict {
		t.Fatalf("confirmation replay accepted: %d", replay.Code)
	}
}

func TestEngineControlRoutes(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"llama": {Command: "llama-server", Mode: "server", HealthURL: "http://127.0.0.1:1/health"},
		"sd":    {Command: "sd-cli", Mode: "subprocess"},
	})
	cfg.Profiles = map[string][]string{"art": {"sd"}, "chat": {"llama"}}
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	do := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec
	}

	if rec := do(http.MethodPost, "/v1/engines/nope/start"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown engine: got %d", rec.Code)
	}
	if rec := do(http.MethodPost, "/v1/engines/sd/start"); rec.Code != http.StatusBadRequest {
		t.Errorf("subprocess engine control: got %d", rec.Code)
	}
	if rec := do(http.MethodPost, "/v1/engines/llama/nonsense"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown action: got %d", rec.Code)
	}
	// Stop on a never-started server engine is a clean no-op.
	if rec := do(http.MethodPost, "/v1/engines/llama/stop"); rec.Code != http.StatusOK {
		t.Errorf("stop: got %d: %s", rec.Code, rec.Body.String())
	}

	rec := do(http.MethodGet, "/v1/engines/profiles")
	if rec.Code != http.StatusOK {
		t.Fatalf("profiles: got %d", rec.Code)
	}
	var profiles struct {
		Profiles map[string][]string `json:"profiles"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&profiles); err != nil {
		t.Fatalf("decode profiles: %v", err)
	}
	if len(profiles.Profiles) != 2 || profiles.Profiles["art"][0] != "sd" {
		t.Errorf("unexpected profiles: %+v", profiles.Profiles)
	}

	if rec := do(http.MethodPost, "/v1/engines/profiles/nope"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown profile: got %d", rec.Code)
	}
	// "art" contains only a subprocess engine; applying it stops the (never
	// started) llama server and starts nothing — a clean success.
	rec = do(http.MethodPost, "/v1/engines/profiles/art")
	if rec.Code != http.StatusOK {
		t.Fatalf("apply art: got %d: %s", rec.Code, rec.Body.String())
	}
	var applied struct {
		Profile  string   `json:"profile"`
		Failures []string `json:"failures"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&applied); err != nil {
		t.Fatalf("decode apply: %v", err)
	}
	if applied.Profile != "art" || len(applied.Failures) != 0 {
		t.Errorf("unexpected apply result: %+v", applied)
	}
}

func TestLibrarySaveListServeDelete(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{"llama": {Command: "llama-server"}})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	body := fmt.Sprintf(`{"kind":"audio","name":"My take","data_b64":%q,"meta":{"voice":"cox"}}`,
		base64.StdEncoding.EncodeToString(validWAVBytes()))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/library", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("save: got %d: %s", rec.Code, rec.Body.String())
	}
	var item struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&item); err != nil || item.ID == "" {
		t.Fatalf("decode save response: %v %q", err, item.ID)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/library", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "My take") {
		t.Fatalf("list: got %d: %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/library/"+item.ID+"/artifact", nil))
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), validWAVBytes()) {
		t.Fatalf("artifact: got %d, %d bytes", rec.Code, rec.Body.Len())
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/v1/library/"+item.ID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: got %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/library/"+item.ID+"/artifact", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("artifact after delete: got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/library", strings.NewReader(`{"kind":"video","name":"x","data_b64":"aGk="}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad kind: got %d", rec.Code)
	}
}

func TestJobsSurfaceTracksStories(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var create story.CreateResponse
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/stories", strings.NewReader(validStoryRequestJSON())))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit story: got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&create); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	waitGatewayStoryStatus(t, router, create.ID, story.StatusComplete)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+create.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("job status: got %d: %s", rec.Code, rec.Body.String())
	}
	var job struct {
		Kind   string            `json:"kind"`
		Status string            `json:"status"`
		Result map[string]string `json:"result"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	if job.Kind != "story" || job.Status != "complete" || job.Result["artifactUrl"] == "" {
		t.Fatalf("unexpected job: %+v", job)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/jobs", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), create.ID) {
		t.Fatalf("jobs list: got %d: %s", rec.Code, rec.Body.String())
	}

	// Cancelling a finished job is a conflict.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/jobs/"+create.ID+"/cancel", nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("cancel finished: got %d", rec.Code)
	}
}

func TestJobsCancelDelegatesToStory(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech-slow"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	storyBody := strings.Replace(validStoryRequestJSON(), `"voice_mode": "placeholder"`, `"voice_mode": "fixed"`, 1)
	var create story.CreateResponse
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/stories", strings.NewReader(storyBody)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit story: got %d: %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&create); err != nil {
		t.Fatalf("decode create: %v", err)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/jobs/"+create.ID+"/cancel", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel: got %d: %s", rec.Code, rec.Body.String())
	}

	status := waitGatewayStoryStatus(t, router, create.ID, story.StatusCancelled)
	if status.Status != story.StatusCancelled {
		t.Fatalf("story not cancelled: %+v", status)
	}
}

func TestAudiobookUploadNarratesAndServes(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech-tone"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "mybook.txt")
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	_, _ = part.Write([]byte("The lighthouse stood alone. It had guided ships for a century.\n\nEvery night the keeper climbed the stairs."))
	_ = writer.WriteField("title", "The Keeper")
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audiobooks", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit: got %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID        string `json:"id"`
		Chunks    int    `json:"chunks"`
		StatusURL string `json:"statusUrl"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Chunks < 2 || created.StatusURL != "/v1/jobs/"+created.ID {
		t.Fatalf("unexpected create response: %+v", created)
	}

	deadline := time.Now().Add(10 * time.Second)
	var job struct {
		Status string            `json:"status"`
		Error  string            `json:"error"`
		Result map[string]string `json:"result"`
	}
	for time.Now().Before(deadline) {
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created.ID, nil))
		if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
			t.Fatalf("decode job: %v", err)
		}
		if job.Status == "complete" || job.Status == "failed" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if job.Status != "complete" {
		t.Fatalf("narration did not complete: %+v", job)
	}
	if job.Result["title"] != "The Keeper" {
		t.Fatalf("unexpected result: %+v", job.Result)
	}
	if job.Result["engine"] != audiobook.DefaultEngineID {
		t.Fatalf("expected default audiobook engine provenance, got %+v", job.Result)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, job.Result["artifactUrl"], nil))
	if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		t.Fatalf("artifact: got %d, %d bytes", rec.Code, rec.Body.Len())
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/audiobooks", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "The Keeper") {
		t.Fatalf("list: got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAudiobookDramaBoxSubprocessRoutesAndRecordsProvenance(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{
		"audio":    helperEngine("speech-tone"),
		"dramabox": helperEngine("speech-tone"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := postAudiobook(t, router, map[string]string{
		"engine":    audiobook.DramaBoxEngineID,
		"direction": "Quiet, precise documentary delivery.",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("submit: got %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	job := waitGatewayAudiobookJob(t, router, created.ID)
	if job.Status != "complete" || job.Result["engine"] != audiobook.DramaBoxEngineID {
		t.Fatalf("unexpected job: %+v", job)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/audiobooks", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"engine":"dramabox"`) || !strings.Contains(rec.Body.String(), `"direction":"Quiet, precise documentary delivery."`) || !strings.Contains(rec.Body.String(), `"synthesisIdentity"`) {
		t.Fatalf("missing DramaBox manifest provenance: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAudiobookDramaBoxResidentServerSupportsTextOnlyAndClone(t *testing.T) {
	t.Chdir(t.TempDir())
	type capturedSpeech struct {
		body audioServerSpeechRequest
		raw  string
	}
	requests := make(chan capturedSpeech, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		data, err := io.ReadAll(req.Body)
		if err != nil {
			t.Errorf("read upstream request: %v", err)
		}
		var body audioServerSpeechRequest
		if err := json.Unmarshal(data, &body); err != nil {
			t.Errorf("decode upstream request: %v", err)
		}
		requests <- capturedSpeech{body: body, raw: string(data)}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wav.SyntheticTone(1600))
	}))
	defer upstream.Close()

	cfg := testConfig(map[string]config.EngineConfig{
		"dramabox": {
			Command:   "audiocpp_server",
			Mode:      "server",
			HealthURL: upstream.URL + "/health",
		},
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := postAudiobook(t, router, map[string]string{
		"engine":    "dramabox",
		"direction": "Measured.",
		"options":   `{"num_inference_steps":12,"guidance_scale":3.5}`,
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("text-only submit: got %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&created)
	waitGatewayAudiobookJob(t, router, created.ID)
	textOnly := <-requests
	if textOnly.body.VoiceRef != "" || textOnly.body.ReferenceText != "" {
		t.Fatalf("text-only DramaBox unexpectedly sent a reference: %+v", textOnly.body)
	}
	if strings.Contains(textOnly.raw, "voice_ref") || strings.Contains(textOnly.raw, "reference_text") {
		t.Fatalf("text-only DramaBox must omit reference fields, got %s", textOnly.raw)
	}
	if !strings.Contains(textOnly.raw, `"seed":"`) || textOnly.body.NumInferenceSteps != 12 || textOnly.body.GuidanceScale != 3.5 {
		t.Fatalf("typed top-level options were not forwarded: %s", textOnly.raw)
	}
	if textOnly.body.Options["audio_chunk_threshold_sec"] != float64(45) || textOnly.body.Options["audio_chunk_duration_sec"] != float64(37) || textOnly.body.Options["cross_fade_duration_sec"] != 0.05 {
		t.Fatalf("complete nested defaults were not forwarded: %s", textOnly.raw)
	}

	var voiceBody bytes.Buffer
	writer := multipart.NewWriter(&voiceBody)
	_ = writer.WriteField("name", "Book Clone")
	_ = writer.WriteField("transcript", "reference words")
	part, _ := writer.CreateFormFile("file", "reference.wav")
	_, _ = part.Write(wav.SyntheticTone(10 * wav.ToneSampleRate))
	_ = writer.Close()
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/voices", &voiceBody)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create clone: got %d: %s", rec.Code, rec.Body.String())
	}
	var clone actorVoiceSummary
	_ = json.NewDecoder(rec.Body).Decode(&clone)

	rec = postAudiobook(t, router, map[string]string{"engine": "dramabox", "voice": clone.ID})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("clone submit: got %d: %s", rec.Code, rec.Body.String())
	}
	_ = json.NewDecoder(rec.Body).Decode(&created)
	waitGatewayAudiobookJob(t, router, created.ID)
	cloned := <-requests
	if !strings.HasSuffix(cloned.body.VoiceRef, "/ref.wav") || cloned.body.ReferenceText != "reference words" {
		t.Fatalf("DramaBox clone reference was not forwarded: %+v", cloned.body)
	}
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/audiobooks", nil))
	var listed struct {
		Audiobooks []audiobook.Manifest `json:"audiobooks"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&listed); err != nil || len(listed.Audiobooks) < 1 {
		t.Fatalf("decode audiobook identities: %v, body=%s", err, rec.Body.String())
	}
	identity := listed.Audiobooks[0].SynthesisIdentity
	if identity == nil || identity.Voice.ID != clone.ID || len(identity.Voice.ReferenceSHA256) != 64 || len(identity.Voice.Fingerprint) != 64 {
		t.Fatalf("stored clone identity is incomplete: %+v", identity)
	}
}

func TestAudiobookPreviewReturnsCompleteEffectiveOptionsWithoutStartingWork(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{
		"audio":    helperEngine("speech-tone"),
		"dramabox": helperEngine("speech-tone"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audiobooks/preview", strings.NewReader(`{
		"engine":"dramabox",
		"promptSpec":{"speakerPhrase":"A woman","deliveryPreset":"clear-explainer"},
		"sourceText":"Ada wrote \"three\" in the ledger.",
		"options":{"guidance_scale":3.25}
	}`))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: got %d: %s", rec.Code, rec.Body.String())
	}
	var preview struct {
		Engine              string                     `json:"engine"`
		EngineFingerprint   string                     `json:"engineFingerprint"`
		Model               string                     `json:"model"`
		Voice               string                     `json:"voice"`
		VoiceFingerprint    string                     `json:"voiceFingerprint"`
		Options             audiobook.SynthesisOptions `json:"options"`
		SeedPolicy          string                     `json:"seedPolicy"`
		Transport           map[string]json.RawMessage `json:"transport"`
		Prompt              audiobook.PromptEvaluation `json:"prompt"`
		PromptPolicyVersion int                        `json:"promptPolicyVersion"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if preview.Engine != "dramabox" || preview.Model != audioServerModelID || preview.Voice != "default" {
		t.Fatalf("preview identities wrong: %+v", preview)
	}
	if len(preview.EngineFingerprint) != 64 || preview.VoiceFingerprint != "default" {
		t.Fatalf("preview did not expose frozen runtime identities: %+v", preview)
	}
	if preview.Options.GuidanceScale != 3.25 || preview.Options.NumInferenceSteps != 30 || preview.Options.AudioChunkThresholdSec != 45 || preview.Options.Seed != 0 {
		t.Fatalf("preview options are not complete and seed-independent: %+v", preview.Options)
	}
	if preview.SeedPolicy == "" || len(preview.Transport["mapping"]) == 0 {
		t.Fatalf("preview omitted seed/transport semantics: %+v", preview)
	}
	if preview.PromptPolicyVersion != audiobook.CurrentPromptPolicyVersion || preview.Prompt.GeneratedPrompt != `A woman speaks clearly at a steady pace, with precise diction and restrained emphasis, "Ada wrote 'three' in the ledger."` {
		t.Fatalf("preview is not the exact structured engine prompt: %+v", preview.Prompt)
	}
	if len(preview.Prompt.Normalizations) != 1 || preview.Prompt.Normalizations[0].Field != "sourceText" {
		t.Fatalf("preview hid source punctuation normalization: %+v", preview.Prompt.Normalizations)
	}
	if entries, err := os.ReadDir("out/audiobooks"); err == nil && len(entries) != 0 {
		t.Fatalf("preview created audiobook state: %+v", entries)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/audiobooks/preview", strings.NewReader(`{"engine":"audio"}`))
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"options":{}`) || !strings.Contains(rec.Body.String(), `"mapping":{}`) {
		t.Fatalf("fast narrator preview must keep an empty option/mapping set: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAudiobookEngineIdentityIncludesTrackedModelIdentity(t *testing.T) {
	router := &router{
		cfg: config.Config{Engines: map[string]config.EngineConfig{
			"dramabox": {Command: "fixture-engine", Mode: "server"},
		}},
		catalog: models.Manifest{Models: []models.Model{{
			ID: "dramabox-q8-0", Engine: "dramabox", Path: "model.gguf", Bytes: 5, SHA256: strings.Repeat("a", 64),
		}}},
		modelsRoot: t.TempDir(),
	}
	first, err := router.resolveAudiobookEngine(context.Background(), "dramabox")
	if err != nil || first.ModelID != "dramabox-q8-0" || first.RuntimeIdentity == "" || first.Fingerprint == first.RuntimeIdentity {
		t.Fatalf("tracked model identity missing: %+v, %v", first, err)
	}
	router.catalog.Models[0].SHA256 = strings.Repeat("b", 64)
	second, err := router.resolveAudiobookEngine(context.Background(), "dramabox")
	if err != nil || second.Fingerprint == first.Fingerprint || second.RuntimeIdentity != first.RuntimeIdentity {
		t.Fatalf("catalog model change did not update only synthesis identity: first=%+v second=%+v err=%v", first, second, err)
	}
}

func TestAudiobookDocumentPreviewUsesCompleteProductionSourceAndSections(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{"dramabox": helperEngine("speech-tone")})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))
	source := strings.Repeat("documented fact ", 190) + "\n\n" + strings.Repeat("later evidence ", 190) + "Hahaha is quoted in the source."
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "complete.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte(source))
	_ = writer.WriteField("engine", "dramabox")
	_ = writer.WriteField("promptSpec", `{"speakerPhrase":"A woman","deliveryPreset":"clear-explainer"}`)
	_ = writer.WriteField("verification", "off")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audiobooks/preview-document", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("document preview: got %d: %s", rec.Code, rec.Body.String())
	}
	var preview struct {
		Prompt         audiobook.PromptEvaluation       `json:"prompt"`
		PromptSections []audiobook.PromptSectionPreview `json:"promptSections"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.PromptSections) < 2 || preview.Prompt.GeneratedPrompt != "" {
		t.Fatalf("preview did not expose only production section prompts: %+v", preview)
	}
	if !strings.Contains(preview.PromptSections[len(preview.PromptSections)-1].GeneratedPrompt, "Hahaha") {
		t.Fatal("document preview omitted source text beyond the first section")
	}
	if len(preview.Prompt.Warnings) != 1 || preview.Prompt.Warnings[0].Code != "paralinguistic-cue" {
		t.Fatalf("complete-document lint warning missing: %+v", preview.Prompt.Warnings)
	}
	if entries, err := os.ReadDir("out/audiobooks"); err == nil && len(entries) != 0 {
		t.Fatalf("document preview created durable state: %+v", entries)
	}

	body.Reset()
	writer = multipart.NewWriter(&body)
	part, _ = writer.CreateFormFile("file", "too-many-sections.txt")
	// 188 words is one above the current 75-second target, so each paragraph
	// becomes a separate production section.
	_, _ = part.Write([]byte(strings.Repeat(strings.Repeat("fact ", 188)+"\n\n", audiobook.MaxChunks+1)))
	_ = writer.WriteField("engine", "dramabox")
	_ = writer.Close()
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/audiobooks/preview-document", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "more than 600") {
		t.Fatalf("preview hid production section-limit failure: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAudiobookOptionsRejectUnknownDuplicateSeedAndLegacyUse(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{
		"audio":    helperEngine("speech-tone"),
		"dramabox": helperEngine("speech-tone"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))
	for _, test := range []struct {
		name   string
		fields map[string]string
	}{
		{name: "unknown", fields: map[string]string{"engine": "dramabox", "options": `{"path":"C:/model"}`}},
		{name: "duplicate", fields: map[string]string{"engine": "dramabox", "options": `{"guidance_scale":2,"guidance_scale":3}`}},
		{name: "seed", fields: map[string]string{"engine": "dramabox", "options": `{"seed":"42"}`}},
		{name: "legacy", fields: map[string]string{"engine": "audio", "options": `{}`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			rec := postAudiobook(t, router, test.fields)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestAudiobookEngineValidationAndAvailabilityStatuses(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{
		"audio":  helperEngine("speech-slow"),
		"ffmpeg": helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	tests := []struct {
		name   string
		fields map[string]string
	}{
		{name: "unknown configured engine", fields: map[string]string{"engine": "ffmpeg"}},
		{name: "direction on audio", fields: map[string]string{"direction": "Dramatic."}},
		{name: "overlong direction", fields: map[string]string{"engine": "dramabox", "direction": strings.Repeat("x", audiobook.MaxDirectionRunes+1)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := postAudiobook(t, router, test.fields)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}

	unconfiguredConfig := testConfig(map[string]config.EngineConfig{
		"ffmpeg": helperEngine("speech"),
	})
	unconfigured := NewRouter(unconfiguredConfig, lifecycle.NewManager(unconfiguredConfig))
	for _, fields := range []map[string]string{nil, {"engine": "dramabox"}} {
		rec := postAudiobook(t, unconfigured, fields)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected unconfigured engine 503, got %d: %s", rec.Code, rec.Body.String())
		}
	}

	first := postAudiobook(t, router, nil)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first submit: got %d: %s", first.Code, first.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(first.Body).Decode(&created)
	second := postAudiobook(t, router, nil)
	if second.Code != http.StatusConflict {
		t.Fatalf("expected busy 409, got %d: %s", second.Code, second.Body.String())
	}
	cancel := httptest.NewRecorder()
	router.ServeHTTP(cancel, httptest.NewRequest(http.MethodPost, "/v1/jobs/"+created.ID+"/cancel", nil))
}

func TestAudiobookAcceptsExactDirectionRuneBoundary(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{
		"dramabox": helperEngine("speech-tone-slow"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))
	rec := postAudiobook(t, router, map[string]string{
		"engine":    "dramabox",
		"direction": strings.Repeat("é", audiobook.MaxDirectionRunes),
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected exact rune boundary to be accepted, got %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	cancel := httptest.NewRecorder()
	router.ServeHTTP(cancel, httptest.NewRequest(http.MethodPost, "/v1/jobs/"+created.ID+"/cancel", nil))
	if cancel.Code != http.StatusOK {
		t.Fatalf("cancel: got %d: %s", cancel.Code, cancel.Body.String())
	}
}

func TestAudiobookDurableLifecycleRoutes(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{
		"dramabox": helperEngine("speech-tone-slow"),
		"whisper":  helperEngine("transcribe"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	createInterrupted := func(t *testing.T) string {
		t.Helper()
		rec := postAudiobook(t, router, map[string]string{"engine": "dramabox", "verification": "off"})
		if rec.Code != http.StatusAccepted {
			t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
		}
		var created struct {
			ID       string `json:"id"`
			Sections int    `json:"sections"`
		}
		_ = json.NewDecoder(rec.Body).Decode(&created)
		if created.ID == "" || created.Sections != 1 {
			t.Fatalf("create omitted durable section count: %+v", created)
		}
		cancel := httptest.NewRecorder()
		router.ServeHTTP(cancel, httptest.NewRequest(http.MethodPost, "/v1/jobs/"+created.ID+"/cancel", nil))
		if cancel.Code != http.StatusOK {
			t.Fatalf("cancel: %d %s", cancel.Code, cancel.Body.String())
		}
		deadline := time.Now().Add(10 * time.Second)
		lastStatus := ""
		for time.Now().Before(deadline) {
			status := httptest.NewRecorder()
			router.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/v1/audiobooks/"+created.ID, nil))
			if status.Code == http.StatusOK && strings.Contains(status.Body.String(), `"status":"interrupted"`) {
				return created.ID
			}
			lastStatus = fmt.Sprintf("%d %s", status.Code, status.Body.String())
			time.Sleep(20 * time.Millisecond)
		}
		jobStatus := httptest.NewRecorder()
		router.ServeHTTP(jobStatus, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created.ID, nil))
		t.Fatalf("cancelled production did not become durably interrupted: production=%s job=%d %s", lastStatus, jobStatus.Code, jobStatus.Body.String())
		return ""
	}

	originalID := createInterrupted(t)
	listed := httptest.NewRecorder()
	router.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/v1/audiobooks", nil))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"interrupted":[`) || !strings.Contains(listed.Body.String(), originalID) {
		t.Fatalf("interrupted list: %d %s", listed.Code, listed.Body.String())
	}
	restart := httptest.NewRecorder()
	router.ServeHTTP(restart, httptest.NewRequest(http.MethodPost, "/v1/audiobooks/"+originalID+"/restart", nil))
	if restart.Code != http.StatusAccepted {
		t.Fatalf("restart: %d %s", restart.Code, restart.Body.String())
	}
	var restarted struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(restart.Body).Decode(&restarted)
	if restarted.ID == "" || restarted.ID == originalID {
		t.Fatalf("restart reused production id: %+v", restarted)
	}
	if job := waitGatewayAudiobookJob(t, router, restarted.ID); job.Status != "complete" {
		t.Fatalf("restart job: %+v", job)
	}
	discard := httptest.NewRecorder()
	router.ServeHTTP(discard, httptest.NewRequest(http.MethodPost, "/v1/audiobooks/"+originalID+"/discard", nil))
	if discard.Code != http.StatusOK || !strings.Contains(discard.Body.String(), `"discarded":true`) {
		t.Fatalf("discard: %d %s", discard.Code, discard.Body.String())
	}

	resumeID := createInterrupted(t)
	resume := httptest.NewRecorder()
	router.ServeHTTP(resume, httptest.NewRequest(http.MethodPost, "/v1/audiobooks/"+resumeID+"/resume", nil))
	if resume.Code != http.StatusAccepted {
		t.Fatalf("resume: %d %s", resume.Code, resume.Body.String())
	}
	if job := waitGatewayAudiobookJob(t, router, resumeID); job.Status != "complete" {
		t.Fatalf("resume job: %+v", job)
	}
	verification := httptest.NewRecorder()
	router.ServeHTTP(verification, httptest.NewRequest(http.MethodGet, "/v1/audiobooks/"+resumeID+"/verification", nil))
	if verification.Code != http.StatusOK || !strings.Contains(verification.Body.String(), `"status": "skipped"`) {
		t.Fatalf("verification report: %d %s", verification.Code, verification.Body.String())
	}
	retry := httptest.NewRecorder()
	router.ServeHTTP(retry, httptest.NewRequest(http.MethodPost,
		"/v1/audiobooks/"+resumeID+"/sections/section-0001/retry",
		strings.NewReader(`{"mode":"reproduce"}`)))
	if retry.Code != http.StatusAccepted {
		t.Fatalf("retry: %d %s", retry.Code, retry.Body.String())
	}
	var retried struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(retry.Body).Decode(&retried)
	if job := waitGatewayAudiobookJob(t, router, retried.ID); job.Status != "complete" {
		t.Fatalf("retry job: %+v", job)
	}
	status := httptest.NewRecorder()
	router.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/v1/audiobooks/"+resumeID, nil))
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"id":"attempt-0002"`) || !strings.Contains(status.Body.String(), `"selected":false`) {
		t.Fatalf("immutable retry attempt not visible: %d %s", status.Code, status.Body.String())
	}
	audition := httptest.NewRecorder()
	router.ServeHTTP(audition, httptest.NewRequest(http.MethodGet,
		"/v1/audiobooks/"+resumeID+"/sections/section-0001/attempts/attempt-0002/audio", nil))
	if audition.Code != http.StatusOK || audition.Header().Get("Content-Type") != "audio/wav" {
		t.Fatalf("attempt audition: %d %s", audition.Code, audition.Body.String())
	}
	selectAttempt := httptest.NewRecorder()
	router.ServeHTTP(selectAttempt, httptest.NewRequest(http.MethodPost,
		"/v1/audiobooks/"+resumeID+"/sections/section-0001/attempts/attempt-0002/select", nil))
	if selectAttempt.Code != http.StatusAccepted {
		t.Fatalf("select attempt: %d %s", selectAttempt.Code, selectAttempt.Body.String())
	}
	var selected struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(selectAttempt.Body).Decode(&selected)
	if job := waitGatewayAudiobookJob(t, router, selected.ID); job.Status != "complete" {
		t.Fatalf("render job: %+v", job)
	}
	status = httptest.NewRecorder()
	router.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/v1/audiobooks/"+resumeID, nil))
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"currentRenderId":"render-0002"`) || !strings.Contains(status.Body.String(), `book.render-0002.wav`) {
		t.Fatalf("render revision not visible: %d %s", status.Code, status.Body.String())
	}
	artifact := httptest.NewRecorder()
	router.ServeHTTP(artifact, httptest.NewRequest(http.MethodGet, "/v1/audiobooks/"+resumeID+"/artifact/book.render-0002.wav", nil))
	if artifact.Code != http.StatusOK || artifact.Header().Get("Content-Type") != "audio/wav" {
		t.Fatalf("render artifact: %d %s", artifact.Code, artifact.Body.String())
	}
	conflict := httptest.NewRecorder()
	router.ServeHTTP(conflict, httptest.NewRequest(http.MethodPost, "/v1/audiobooks/"+resumeID+"/discard", nil))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("discard complete production should conflict: %d %s", conflict.Code, conflict.Body.String())
	}
}

func TestAudiobookBenchmarkAPITracksAndServesPersistedResult(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{"dramabox": helperEngine("speech-tone")})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))
	created := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audiobooks/benchmark", strings.NewReader(`{"backend":"cpu","promptSpec":{"speakerPhrase":"A woman","deliveryPreset":"clear-explainer"}}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(created, req)
	if created.Code != http.StatusAccepted {
		t.Fatalf("benchmark create: %d %s", created.Code, created.Body.String())
	}
	var response struct {
		ID        string `json:"id"`
		ResultURL string `json:"resultUrl"`
	}
	_ = json.NewDecoder(created.Body).Decode(&response)
	if response.ID == "" || response.ResultURL == "" {
		t.Fatalf("benchmark response omitted links: %+v", response)
	}
	if job := waitGatewayAudiobookJob(t, router, response.ID); job.Status != "complete" {
		t.Fatalf("benchmark job: %+v", job)
	}
	result := httptest.NewRecorder()
	router.ServeHTTP(result, httptest.NewRequest(http.MethodGet, response.ResultURL, nil))
	if result.Code != http.StatusOK || !strings.Contains(result.Body.String(), `"schemaVersion":"dramabox-benchmark.v1"`) || !strings.Contains(result.Body.String(), `"profileFingerprint":"`) || !strings.Contains(result.Body.String(), `"cpu.long_form"`) || !strings.Contains(result.Body.String(), `"speakerPhrase":"A woman"`) || !strings.Contains(result.Body.String(), `"deliveryPreset":"clear-explainer"`) {
		t.Fatalf("benchmark result: %d %s", result.Code, result.Body.String())
	}
	listed := httptest.NewRecorder()
	router.ServeHTTP(listed, httptest.NewRequest(http.MethodGet, "/v1/audiobooks/benchmark/results", nil))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), response.ID) {
		t.Fatalf("benchmark results list: %d %s", listed.Code, listed.Body.String())
	}

	bad := httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/audiobooks/benchmark", strings.NewReader(`{"backend":"cuda"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(bad, req)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("implicit CUDA benchmark: %d %s", bad.Code, bad.Body.String())
	}
}

func TestAudiobookRequiredVerificationUnavailableIs503(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{"dramabox": helperEngine("speech-tone")})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))
	rec := postAudiobook(t, router, map[string]string{"engine": "dramabox", "verification": "required"})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("required verification without Whisper: %d %s", rec.Code, rec.Body.String())
	}
	if entries, err := os.ReadDir("out/audiobooks"); err == nil && len(entries) != 0 {
		t.Fatalf("503 created durable state: %+v", entries)
	}
}

func postAudiobook(t *testing.T, router http.Handler, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "facts.txt")
	if err != nil {
		t.Fatalf("create audiobook upload: %v", err)
	}
	_, _ = part.Write([]byte("A documented fact. Another documented fact."))
	for key, value := range fields {
		_ = writer.WriteField(key, value)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close audiobook upload: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/audiobooks", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	router.ServeHTTP(rec, req)
	return rec
}

func waitGatewayAudiobookJob(t *testing.T, router http.Handler, id string) struct {
	Status string            `json:"status"`
	Error  string            `json:"error"`
	Result map[string]string `json:"result"`
} {
	t.Helper()
	var job struct {
		Status string            `json:"status"`
		Error  string            `json:"error"`
		Result map[string]string `json:"result"`
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+id, nil))
		if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
			t.Fatalf("decode audiobook job: %v", err)
		}
		if job.Status == "complete" || job.Status == "failed" || job.Status == "cancelled" {
			return job
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("audiobook job %s did not finish: %+v", id, job)
	return job
}

func TestAudiobookRejectsBadUploads(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := testConfig(map[string]config.EngineConfig{
		"audio": helperEngine("speech"),
	})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	upload := func(filename string, content []byte, voice string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, _ := writer.CreateFormFile("file", filename)
		_, _ = part.Write(content)
		if voice != "" {
			_ = writer.WriteField("voice", voice)
		}
		_ = writer.Close()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/audiobooks", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		router.ServeHTTP(rec, req)
		return rec
	}

	if rec := upload("book.pdf", []byte("%PDF-1.4"), ""); rec.Code != http.StatusBadRequest {
		t.Errorf("pdf: got %d", rec.Code)
	}
	if rec := upload("book.exe", []byte("MZ"), ""); rec.Code != http.StatusBadRequest {
		t.Errorf("exe: got %d", rec.Code)
	}
	if rec := upload("book.txt", []byte("Fine text."), "voice_that_does_not_exist"); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown voice: got %d", rec.Code)
	}
}

func TestModelsVerifyAllRunsAsJobAndOverlaysCatalog(t *testing.T) {
	root := t.TempDir()
	content := []byte("model bytes here")
	if err := os.WriteFile(filepath.Join(root, "chat.gguf"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	if err := os.WriteFile(filepath.Join(root, "models.json"), fmt.Appendf(nil, `{"models":[
		{"id":"chat","engine":"llama","path":"chat.gguf","bytes":%d,"sha256":%q},
		{"id":"bad","engine":"sd","path":"chat.gguf","bytes":%d,"sha256":"deadbeef"}
	]}`, len(content), hex.EncodeToString(sum[:]), len(content)), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig(map[string]config.EngineConfig{"llama": {Command: "llama-server"}})
	cfg.Models = &config.ModelsConfig{Manifest: filepath.Join(root, "models.json"), Root: root}
	router := NewRouter(cfg, lifecycle.NewManager(cfg))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/models/verify", nil))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("verify: got %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var job struct {
		Status string            `json:"status"`
		Result map[string]string `json:"result"`
	}
	for time.Now().Before(deadline) {
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/jobs/"+created.ID, nil))
		if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
			t.Fatalf("decode job: %v", err)
		}
		if job.Status == "complete" || job.Status == "failed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.Status != "complete" {
		t.Fatalf("verify job did not complete: %+v", job)
	}
	if job.Result["verified"] != "1" || job.Result["total"] != "2" || !strings.Contains(job.Result["corrupt"], "bad") {
		t.Fatalf("unexpected result: %+v", job.Result)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models/catalog", nil))
	var catalog struct {
		Models []struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"models"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&catalog); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	state := map[string]string{}
	for _, m := range catalog.Models {
		state[m.ID] = m.State
	}
	if state["chat"] != "verified" || state["bad"] != "corrupt" {
		t.Fatalf("catalog overlay wrong: %+v", state)
	}
}

func TestModelsCatalogEmptyWithoutManifest(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{"llama": {Command: "llama-server"}})
	router := NewRouter(cfg, lifecycle.NewManager(cfg))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/models/catalog", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"models":[]`) {
		t.Errorf("expected empty models array, got %s", rec.Body.String())
	}
}
