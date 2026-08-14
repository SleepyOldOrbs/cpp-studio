package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cpp-studio/internal/config"
	"cpp-studio/internal/wav"
)

func TestRunnerInvokesResidentSpeechThroughEngineInterface(t *testing.T) {
	var request serverSpeechRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/audio/speech" {
			t.Fatalf("path = %q", req.URL.Path)
		}
		if err := json.NewDecoder(req.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write(wav.SyntheticTone(200))
	}))
	defer server.Close()

	recorder := &fakeRecorder{}
	runner := NewRunner(map[string]config.EngineConfig{
		"audio": {Mode: "server", HealthURL: server.URL + "/health", DefaultVoiceRef: `C:\voices\default.wav`, DefaultVoiceText: "reference"},
	}, recorder)
	result, err := runner.Run(context.Background(), SpeechSpec("hello"))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if request.Input != "hello" || request.VoiceRef != "C:/voices/default.wav" || len(result.Output) == 0 {
		t.Fatalf("request=%+v output=%d", request, len(result.Output))
	}
	if len(recorder.calls) != 1 || !recorder.calls[0].success || recorder.calls[0].name != "audio" {
		t.Fatalf("recorder calls = %+v", recorder.calls)
	}
}

func TestRunnerInvokesResidentTranscriptionAndSegments(t *testing.T) {
	formats := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if err := req.ParseMultipartForm(1024 * 1024); err != nil {
			t.Fatalf("parse multipart: %v", err)
		}
		format := req.FormValue("response_format")
		formats <- format
		w.Header().Set("Content-Type", "application/json")
		if format == "verbose_json" {
			_, _ = w.Write([]byte(`{"segments":[{"start":0.25,"end":1.5,"text":" hello "},{"start":2,"end":3,"text":"  "}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"text":" hello\nworld "}`))
	}))
	defer server.Close()

	runner := NewRunner(map[string]config.EngineConfig{
		"whisper": {Mode: "server", HealthURL: server.URL + "/health"},
	}, &fakeRecorder{})
	audio := wav.SyntheticTone(200)
	result, err := runner.Run(context.Background(), TranscriptionSpec(audio))
	if err != nil || string(result.Stdout) != "hello world" {
		t.Fatalf("transcription = %q, %v", result.Stdout, err)
	}
	result, err = runner.Run(context.Background(), TranscriptionSegmentsSpec(audio))
	if err != nil {
		t.Fatalf("segments: %v", err)
	}
	segments, err := ParseTranscriptSegments(result.Stdout)
	if err != nil || len(segments) != 1 || segments[0].Text != "hello" {
		t.Fatalf("segments = %+v, %v", segments, err)
	}
	if first, second := <-formats, <-formats; first != "json" || second != "verbose_json" {
		t.Fatalf("formats = %q, %q", first, second)
	}
}
