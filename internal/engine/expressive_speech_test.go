package engine

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"
)

func TestExpressiveSpeechMappings(t *testing.T) {
	intensity, duration := 0.0, 1.2
	p := &SpeechPerformance{Emotion: "quiet relief", Intensity: &intensity, DurationFactor: &duration, Language: "en"}
	voice := &Voice{RefWAVPath: "actor.wav", RefText: "Reference words"}
	request := SynthesisRequest{EngineID: "index-tts2.5", Text: "You are safe.", Performance: p}
	if err := ValidateSpeechPerformance(request.EngineID, p); err != nil {
		t.Fatal(err)
	}
	spec := SpeechVoiceSpecForRequest(request, voice)
	args := strings.Join(spec.BuildArgs("", "result.wav"), " ")
	for _, want := range []string{"--emotion quiet relief", "emotion_alpha=0", "duration_factor=1.2", "language=en"} {
		if !strings.Contains(args, want) {
			t.Fatalf("missing %s in %s", want, args)
		}
	}
	if spec.OverrideArgs["--voice-ref"] != "actor.wav" {
		t.Fatal("lost actor reference")
	}
	encoded, err := MarshalSpeechServerRequest("tts", request, voice, nil)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatal(err)
	}
	opts := body["options"].(map[string]any)
	if opts["emotion_text"] != "quiet relief" || opts["use_emotion_text"] != true || opts["emotion_alpha"] != float64(0) {
		t.Fatalf("bad resident controls: %s", encoded)
	}
	request.Performance = &SpeechPerformance{EmotionVector: []float64{0, 0, 0.6, 0, 0, 0, 0, 0}}
	encoded, err = MarshalSpeechServerRequest("tts", request, voice, nil)
	if err != nil || !strings.Contains(string(encoded), `"emotion_vector":"0,0,0.6,0,0,0,0,0"`) {
		t.Fatalf("invalid native vector encoding: %s %v", encoded, err)
	}
	request = SynthesisRequest{EngineID: "higgs-audio", Text: "Wait. <|sfx:sigh|>Uh", Performance: &SpeechPerformance{Emotion: "relief", Style: "whispering", Pace: "speed_slow"}}
	spec = SpeechVoiceSpecForRequest(request, voice)
	args = strings.Join(spec.BuildArgs("", "result.wav"), " ")
	if !strings.Contains(args, "<|emotion:relief|><|style:whispering|><|prosody:speed_slow|>Wait. <|sfx:sigh|>Uh") {
		t.Fatal(args)
	}
}

func TestExpressiveControlsRejectUnsupportedValues(t *testing.T) {
	nan := math.NaN()
	excessive := 1.1
	cases := []struct {
		engine string
		p      SpeechPerformance
	}{
		{"audio", SpeechPerformance{Emotion: "sad"}},
		{"index-tts2.5", SpeechPerformance{Intensity: &excessive}},
		{"index-tts2.5", SpeechPerformance{DurationFactor: &nan}},
		{"index-tts2.5", SpeechPerformance{EmotionVector: []float64{0, 1}}},
		{"index-tts2.5", SpeechPerformance{Emotion: "sad", EmotionVector: make([]float64, 8)}},
		{"higgs-audio", SpeechPerformance{Emotion: "anger|fear"}},
		{"higgs-audio", SpeechPerformance{Emotion: "<|sfx:scream|>"}},
		{"higgs-audio", SpeechPerformance{Intensity: &excessive}},
		{"fireredtts3-base", SpeechPerformance{Emotion: "sad"}},
		{"fireredtts3-instruct", SpeechPerformance{Language: "English|Finnish"}},
	}
	for _, tt := range cases {
		if err := ValidateSpeechPerformance(tt.engine, &tt.p); err == nil {
			t.Errorf("accepted %+v", tt)
		}
	}
}

func TestFireRedDesignSelectsReferenceFreeTemplate(t *testing.T) {
	spec := FireRedVoiceDesignSpec("Warm, relieved narrator", "At last, you are home.")
	args := spec.BuildArgs("", "design.wav")
	if spec.OverrideArgs["--task"] != "vdes" || !slices.Contains(args, "template_name=voice_design") || !slices.Contains(args, "instruction=Warm, relieved narrator") {
		t.Fatalf("wrong design routing: %+v %v", spec.OverrideArgs, args)
	}
	if spec.speechRequest != nil {
		t.Fatal("design must not inherit a default cloning reference")
	}
}
