package audiobook

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"cpp-studio/internal/engine"
	"cpp-studio/internal/jobs"
	"cpp-studio/internal/wav"
)

func TestSynthesisIdentityCoversAudioAffectingInputsButExcludesSectionSeed(t *testing.T) {
	request := Request{
		Text:      "A documented fact.",
		EngineID:  DramaBoxEngineID,
		VoiceID:   "voice-one",
		Direction: "Measured.",
		Options:   engine.DefaultDramaBoxOptions(),
	}
	resolvedEngine := EngineIdentity{ID: DramaBoxEngineID, Mode: "server", ModelID: "tts", Fingerprint: "engine-fingerprint"}
	resolvedVoice := VoiceIdentity{ID: "voice-one", Fingerprint: "voice-fingerprint", ReferenceSHA256: strings.Repeat("a", 64)}
	base := buildSynthesisIdentity(request, resolvedEngine, resolvedVoice)
	if len(base.Fingerprint) != 64 || len(base.SourceSHA256) != 64 {
		t.Fatalf("identity hashes must be full SHA-256 values: %+v", base)
	}

	seeded := request
	seeded.Options.Seed = 42
	if got := buildSynthesisIdentity(seeded, resolvedEngine, resolvedVoice).Fingerprint; got != base.Fingerprint {
		t.Fatalf("section seed changed the base identity: %s != %s", got, base.Fingerprint)
	}

	tests := []struct {
		name   string
		change func(*Request, *EngineIdentity, *VoiceIdentity)
	}{
		{name: "source", change: func(req *Request, _ *EngineIdentity, _ *VoiceIdentity) { req.Text += " More." }},
		{name: "engine", change: func(_ *Request, id *EngineIdentity, _ *VoiceIdentity) { id.Fingerprint = "other-engine" }},
		{name: "model", change: func(_ *Request, id *EngineIdentity, _ *VoiceIdentity) { id.ModelID = "other-model" }},
		{name: "voice", change: func(_ *Request, _ *EngineIdentity, id *VoiceIdentity) { id.Fingerprint = "other-voice" }},
		{name: "direction", change: func(req *Request, _ *EngineIdentity, _ *VoiceIdentity) { req.Direction = "Brisk." }},
		{name: "options", change: func(req *Request, _ *EngineIdentity, _ *VoiceIdentity) { req.Options.GuidanceScale = 3.5 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changedRequest := request
			changedEngine := resolvedEngine
			changedVoice := resolvedVoice
			test.change(&changedRequest, &changedEngine, &changedVoice)
			if got := buildSynthesisIdentity(changedRequest, changedEngine, changedVoice).Fingerprint; got == base.Fingerprint {
				t.Fatalf("%s change did not change identity", test.name)
			}
		})
	}
}

func TestManagerResolvesAndFreezesEngineAndVoiceBeforeSynthesis(t *testing.T) {
	registry := jobs.NewRegistry()
	resolvedReference := &engine.Voice{RefWAVPath: "frozen-reference.wav", RefText: "reference words"}
	var order []string
	var spoken SynthesisRequest
	manager := NewManager(ManagerOptions{
		RootDir:    t.TempDir(),
		Jobs:       registry,
		SeedSource: bytes.NewReader([]byte{0, 0, 0, 0, 0, 0, 0, 42}),
		ResolveEngine: func(context.Context, string) (EngineIdentity, error) {
			order = append(order, "engine")
			return EngineIdentity{ID: DramaBoxEngineID, Mode: "server", ModelID: "tts", Fingerprint: strings.Repeat("e", 64)}, nil
		},
		ResolveVoice: func(context.Context, string) (VoiceIdentity, error) {
			order = append(order, "voice")
			return VoiceIdentity{ID: "voice-one", Fingerprint: strings.Repeat("v", 64), ReferenceSHA256: strings.Repeat("a", 64), Reference: resolvedReference}, nil
		},
		ReserveEngine: func(context.Context, string) (func(), bool) {
			order = append(order, "reserve")
			return func() {}, true
		},
		Synthesize: func(_ context.Context, request SynthesisRequest) ([]byte, error) {
			order = append(order, "synthesize")
			spoken = request
			return wav.SyntheticTone(160), nil
		},
	})

	id, _, err := manager.Submit(context.Background(), Request{Text: "A documented fact.", EngineID: DramaBoxEngineID, VoiceID: "voice-one"})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	waitForAudiobookJob(t, registry, id)
	if !reflect.DeepEqual(order, []string{"engine", "voice", "reserve", "synthesize"}) {
		t.Fatalf("resolution/invocation order = %v", order)
	}
	if spoken.Voice != resolvedReference || spoken.VoiceID != "voice-one" || spoken.Options.Seed != 42 {
		t.Fatalf("synthesis did not use the frozen resolution: %+v", spoken)
	}
	books, err := manager.List()
	if err != nil || len(books) != 1 {
		t.Fatalf("list: %v, books=%d", err, len(books))
	}
	manifest := books[0]
	if manifest.SynthesisIdentity == nil || manifest.SynthesisFingerprint != manifest.SynthesisIdentity.Fingerprint || manifest.SynthesisIdentity.Voice.Reference != nil {
		t.Fatalf("manifest identity was not safely persisted: %+v", manifest)
	}
}

func TestDramaBoxRejectsOnlyIneligibleCloneReferences(t *testing.T) {
	manager := NewManager(ManagerOptions{
		ResolveEngine: func(_ context.Context, engineID string) (EngineIdentity, error) {
			return EngineIdentity{ID: engineID, Mode: "subprocess", ModelID: engineID, Fingerprint: engineID}, nil
		},
		ResolveVoice: func(_ context.Context, voiceID string) (VoiceIdentity, error) {
			return VoiceIdentity{
				ID: voiceID, Fingerprint: voiceID,
				Reference:                &engine.Voice{RefWAVPath: "ref.wav", RefText: "words"},
				DramaBoxIneligibleReason: "requires at least 10 seconds of usable speech; measured 4.0 seconds via pcm-heuristic-v1",
			}, nil
		},
	})
	_, err := manager.Preview(context.Background(), Request{EngineID: DramaBoxEngineID, VoiceID: "short"})
	if err == nil || !IsRequestError(err) || !strings.Contains(err.Error(), "10 seconds") {
		t.Fatalf("DramaBox accepted an ineligible clone: %v", err)
	}
	resolved, err := manager.Preview(context.Background(), Request{EngineID: DefaultEngineID, VoiceID: "short"})
	if err != nil || resolved.Voice.ID != "short" {
		t.Fatalf("engine-specific eligibility broke fast narration: resolved=%+v err=%v", resolved, err)
	}
}

type countingReader struct{ reads int }

func (reader *countingReader) Read([]byte) (int, error) {
	reader.reads++
	return 0, errors.New("unexpected entropy read")
}

func TestEngineResolutionFailurePrecedesPlanningReservationAndJobCreation(t *testing.T) {
	registry := jobs.NewRegistry()
	entropy := &countingReader{}
	reserved := false
	manager := NewManager(ManagerOptions{
		RootDir:    t.TempDir(),
		Jobs:       registry,
		SeedSource: entropy,
		ResolveEngine: func(context.Context, string) (EngineIdentity, error) {
			return EngineIdentity{}, errors.New("runtime unavailable")
		},
		ReserveEngine: func(context.Context, string) (func(), bool) {
			reserved = true
			return func() {}, true
		},
		Synthesize: func(context.Context, SynthesisRequest) ([]byte, error) {
			return nil, errors.New("must not run")
		},
	})

	if _, _, err := manager.Submit(context.Background(), Request{Text: "A fact.", EngineID: DramaBoxEngineID}); err == nil || !strings.Contains(err.Error(), "runtime unavailable") {
		t.Fatalf("expected resolution error, got %v", err)
	}
	if entropy.reads != 0 || reserved || len(registry.List()) != 0 {
		t.Fatalf("resolution failure leaked work: reads=%d reserved=%v jobs=%v", entropy.reads, reserved, registry.List())
	}
}
