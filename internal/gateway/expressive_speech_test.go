package gateway

import (
	"cpp-studio/internal/config"
	"cpp-studio/internal/engine"
	"cpp-studio/internal/models"
	"cpp-studio/internal/wav"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExpressiveSpeechRoutesAndValidatesBeforeInference(t *testing.T) {
	manifest, err := models.DefaultManifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		model, performance, want string
		status                   int
	}{
		{"index-tts2.5", `{"emotion":"relief","intensity":0}`, "--emotion relief", 200},
		{"higgs-audio", `{"emotion":"relief","style":"whispering"}`, "<|emotion:relief|><|style:whispering|>", 200},
		{"fireredtts3-base", `{"language":"English"}`, "language=English", 200},
		{"fireredtts3-instruct", `{"language":"English"}`, "language=English", 200},
		{"higgs-audio", `{"emotion":"relief|contemplation"}`, "", 400},
		{"index-tts2.5", `{"intensity":2}`, "", 400},
		{"fireredtts3-base", `{"emotion":"angry"}`, "", 400},
	} {
		t.Run(tt.model+tt.performance, func(t *testing.T) {
			called := false
			fake := engine.NewFake()
			fake.Handle(tt.model, func(spec engine.Spec) (engine.Result, error) {
				called = true
				if !strings.Contains(strings.Join(spec.BuildArgs("", "result.wav"), " "), tt.want) {
					t.Fatalf("missing native control %s", tt.want)
				}
				return engine.Result{Output: wav.SyntheticTone(200)}, nil
			})
			r := &router{catalog: manifest, engines: fake, cfg: config.Config{Engines: map[string]config.EngineConfig{tt.model: {Mode: "subprocess"}}}}
			rec := httptest.NewRecorder()
			r.handleSpeech(rec, httptest.NewRequest(http.MethodPost, "/v1/audio/speech", strings.NewReader(`{"input":"Welcome home.","model":"`+tt.model+`","performance":`+tt.performance+`}`)))
			if rec.Code != tt.status || called != (tt.status == 200) {
				t.Fatalf("status %d called %v: %s", rec.Code, called, rec.Body.String())
			}
		})
	}
}
