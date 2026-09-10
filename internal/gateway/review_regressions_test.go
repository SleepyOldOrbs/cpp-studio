package gateway

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"cpp-studio/internal/config"
	"cpp-studio/internal/engine"
	"cpp-studio/internal/lifecycle"
	"cpp-studio/internal/models"
	"cpp-studio/internal/wav"
)

func TestVoiceDesignResolvesCanonicalIDsAndAliases(t *testing.T) {
	cfg := testConfig(nil)
	manifest, err := models.Load(cfg.Models.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"qwen3-tts-1.7b-voicedesign", "qwen3", "voicedesign", "omnivoice", "voxcpm2"} {
		t.Run(id, func(t *testing.T) {
			selected, err := manifest.Resolve(id, "voice_design", "voxcpm2")
			if err != nil {
				t.Fatal(err)
			}
			called := false
			fake := engine.NewFake()
			fake.Handle(selected.Engine, func(spec engine.Spec) (engine.Result, error) {
				called = true
				return engine.Result{Output: wav.SyntheticTone(200)}, nil
			})
			r := &router{catalog: manifest, engines: fake, cfg: config.Config{Engines: map[string]config.EngineConfig{selected.Engine: {Mode: "subprocess"}}}}
			rec := httptest.NewRecorder()
			r.handleVoiceDesign(rec, httptest.NewRequest(http.MethodPost, "/v1/voices/design", strings.NewReader(`{"description":"Warm voice","model":"`+id+`"}`)))
			if rec.Code != http.StatusOK || !called {
				t.Fatalf("status=%d called=%v body=%s", rec.Code, called, rec.Body.String())
			}
		})
	}
}

func TestConfiguredMissingManifestIsVisible(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{"audio": {Mode: "subprocess"}})
	cfg.Models.Manifest = filepath.Join(t.TempDir(), "missing.json")
	handler := NewRouter(cfg, lifecycle.NewManager(cfg))
	for _, path := range []string{"/health", "/v1/models/catalog"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "configured model manifest") {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestVoiceDesignStandaloneConfigRetainsCatalogueAliases(t *testing.T) {
	for _, id := range []string{"qwen3", "qwen3-tts-1.7b-voicedesign", "omnivoice", "voxcpm2", ""} {
		t.Run(id, func(t *testing.T) {
			cfg := testConfig(map[string]config.EngineConfig{"voicedesign": helperEngine("design"), "omnivoice": helperEngine("design"), "voxcpm2": helperEngine("speech")})
			cfg.Models = nil
			handler := NewRouter(cfg, lifecycle.NewManager(cfg))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/voices/design", strings.NewReader(`{"description":"Warm voice","model":"`+id+`"}`)))
			if rec.Code != http.StatusOK {
				t.Fatalf("%d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}
