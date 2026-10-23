package gateway

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cpp-studio/internal/config"
	"cpp-studio/internal/engine"
	"cpp-studio/internal/lifecycle"
	"cpp-studio/internal/voice"
	"cpp-studio/internal/wav"
)

func TestSAMSeparationValidatesPromptAndReturnsBothTracks(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{"sam-audio": {Command: "audio-cli"}, "htdemucs": {Command: "audio-cli"}})
	r := NewRouter(cfg, lifecycle.NewManager(cfg)).(*router)
	fake := engine.NewFake()
	calls := 0
	fake.Handle("sam-audio", func(spec engine.Spec) (engine.Result, error) {
		calls++
		args := spec.BuildArgs("mixture.wav", spec.OutputPath)
		if spec.OverrideArgs["--task"] != "s2s" || args[2] != "--text" || args[3] != "audience laughter" {
			t.Fatalf("SAM mapping: %v / %v", args, spec.OverrideArgs)
		}
		for _, name := range []string{"target.wav", "residual.wav"} {
			if err := os.WriteFile(filepath.Join(spec.OutputPath, name), wav.SyntheticTone(48000), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return engine.Result{}, nil
	})
	r.engines = fake
	for _, tc := range []struct {
		model, prompt string
		status        int
	}{
		{"sam-audio-small-q8-0", "audience laughter", http.StatusOK},
		{"sam-audio-small-q8-0", "   ", http.StatusBadRequest},
		{"sam-audio-small-q8-0", strings.Repeat("x", 1001), http.StatusBadRequest},
		{"htdemucs-q8-0", "audience laughter", http.StatusBadRequest},
	} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, _ := writer.CreateFormFile("file", "recording.wav")
		_, _ = part.Write(wav.SyntheticTone(16000))
		_ = writer.WriteField("model", tc.model)
		_ = writer.WriteField("prompt", tc.prompt)
		_ = writer.Close()
		req := httptest.NewRequest(http.MethodPost, "/v1/audio/separation", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("status=%d want=%d body=%s", rec.Code, tc.status, rec.Body.String())
		}
		if rec.Code == http.StatusOK {
			archive, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
			if err != nil {
				t.Fatal(err)
			}
			if len(archive.File) != 2 || archive.File[0].Name != "residual.wav" || archive.File[1].Name != "target.wav" {
				t.Fatal("missing SAM output tracks")
			}
		}
	}
	if calls != 1 {
		t.Fatalf("invalid requests invoked engine: %d calls", calls)
	}
}

func TestDrama3ResolvesOrderedSavedVoicesAndRejectsInvalidReferences(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{"fish-drama3": {Command: "audio-cli"}, "audio": {Command: "audio-cli"}})
	r := NewRouter(cfg, lifecycle.NewManager(cfg)).(*router)
	r.voices = voice.NewStore(t.TempDir())
	a, err := r.voices.Save("A", "First reference.", wav.SyntheticTone(16000), false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.voices.Save("B", "Second reference.", wav.SyntheticTone(16000), false)
	if err != nil {
		t.Fatal(err)
	}
	fake := engine.NewFake()
	calls := 0
	fake.Handle("fish-drama3", func(spec engine.Spec) (engine.Result, error) {
		calls++
		args := spec.BuildArgs("", "result.wav")
		var refs []struct {
			Text      string `json:"text"`
			AudioPath string `json:"audio_path"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(args[3], "multi_reference_cond=")), &refs); err != nil {
			t.Fatal(err)
		}
		if len(refs) != 2 || refs[0].Text != "<|speaker:0|> First reference." || refs[1].Text != "<|speaker:1|> Second reference." {
			t.Fatalf("references=%+v", refs)
		}
		for _, ref := range refs {
			if _, err := os.Stat(ref.AudioPath); err != nil {
				t.Fatal(err)
			}
		}
		if len(spec.OverrideArgs) != 0 {
			t.Fatal("single/default voice must not override ordered voices")
		}
		return engine.Result{Output: wav.SyntheticTone(44100)}, nil
	})
	r.engines = fake
	valid := map[string]any{"model": "fish-drama3-small-iq3-xxs", "input": "<|speaker:0|> Hello. <|speaker:1|> Good morning.", "speakers": []map[string]string{{"voice": a.ID}, {"voice": b.ID}}}
	for _, kind := range []string{"valid", "wrong-model", "unknown", "path", "default", "conflict", "missing-tag", "too-many"} {
		body := map[string]any{}
		for k, v := range valid {
			body[k] = v
		}
		switch kind {
		case "wrong-model":
			body["model"] = "audio"
		case "unknown":
			body["speakers"] = []map[string]string{{"voice": "missing"}}
		case "path":
			body["speakers"] = []map[string]string{{"voice": `C:\private.wav`}}
		case "default":
			body["speakers"] = []map[string]string{{"voice": "default"}}
		case "conflict":
			body["voice"] = a.ID
		case "missing-tag":
			body["input"] = "Plain words."
		case "too-many":
			body["speakers"] = make([]map[string]string, 17)
		}
		encoded, _ := json.Marshal(body)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/audio/speech", bytes.NewReader(encoded)))
		want := http.StatusBadRequest
		if kind == "valid" {
			want = http.StatusOK
		}
		if rec.Code != want {
			t.Fatalf("%s status=%d want=%d body=%s", kind, rec.Code, want, rec.Body.String())
		}
	}
	if calls != 1 {
		t.Fatalf("invalid requests invoked engine: %d calls", calls)
	}
}
