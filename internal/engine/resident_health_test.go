package engine

import (
	"context"
	"cpp-studio/internal/config"
	"cpp-studio/internal/lifecycle"
	"cpp-studio/internal/wav"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResidentRequestFailureDoesNotChangeProcessLifecycle(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { _, _ = w.Write(wav.SyntheticTone(200)) }))
	defer upstream.Close()
	cfg := config.Config{Engines: map[string]config.EngineConfig{"audio": {Mode: "server", HealthURL: upstream.URL + "/health", DefaultVoiceRef: "reference.wav", DefaultVoiceText: "reference"}}}
	manager := lifecycle.NewManager(cfg)
	// The HTTP test server is external to the process manager; seed the state
	// its successful startup probe would normally publish.
	manager.MarkFailure("audio", lifecycle.StatusReady, "")
	runner := NewRunner(cfg.Engines, manager)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runner.Run(cancelled, SpeechSpec("cancelled")); err == nil {
		t.Fatal("expected cancellation")
	}
	if health := manager.Health().Engines["audio"]; !health.Ready || health.LastError != "" {
		t.Fatalf("cancel changed lifecycle: %+v", health)
	}
	if _, err := runner.Run(context.Background(), SpeechSpec("healthy")); err != nil {
		t.Fatal(err)
	}
	if health := manager.Health().Engines["audio"]; !health.Ready || health.LastError != "" {
		t.Fatalf("success changed lifecycle: %+v", health)
	}
	// A response from an old invocation must not revive a stopped generation.
	manager.MarkFailure("audio", lifecycle.StatusStopped, "")
	if _, err := runner.Run(context.Background(), SpeechSpec("late response")); err != nil {
		t.Fatal(err)
	}
	if health := manager.Health().Engines["audio"]; health.Status != lifecycle.StatusStopped || health.Ready {
		t.Fatalf("late success revived stopped process: %+v", health)
	}
}
