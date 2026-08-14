package models

import "testing"

func TestManifestResolveOwnsModelIdentityAndRoute(t *testing.T) {
	manifest := Manifest{Models: []Model{
		{ID: "speech-q8", Engine: "audio", Family: "qwen_tts", Capabilities: []string{"speech"}, Aliases: []string{"legacy-speech"}},
		{ID: "asr-q8", Engine: "asr", Family: "qwen_asr", Capabilities: []string{"transcription"}},
	}}

	for _, test := range []struct {
		name, value, capability, defaultEngine, wantID, wantEngine, wantFamily string
	}{
		{name: "catalog id", value: "asr-q8", capability: "transcription", wantID: "asr-q8", wantEngine: "asr", wantFamily: "qwen_asr"},
		{name: "alias", value: "legacy-speech", capability: "speech", wantID: "speech-q8", wantEngine: "audio", wantFamily: "qwen_tts"},
		{name: "configured engine", value: "audio", capability: "speech", wantID: "speech-q8", wantEngine: "audio", wantFamily: "qwen_tts"},
		{name: "default", capability: "speech", defaultEngine: "audio", wantID: "speech-q8", wantEngine: "audio", wantFamily: "qwen_tts"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := manifest.Resolve(test.value, test.capability, test.defaultEngine)
			if err != nil || got.ID != test.wantID || got.Engine != test.wantEngine || got.Family != test.wantFamily {
				t.Fatalf("Resolve() = %+v, %v", got, err)
			}
		})
	}
	if _, err := manifest.Resolve("asr-q8", "speech", ""); err == nil {
		t.Fatal("transcription-only model resolved for speech")
	}
	if _, err := manifest.Resolve("--model=C:/arbitrary.gguf", "speech", ""); err == nil {
		t.Fatal("arbitrary model input was accepted")
	}
}
