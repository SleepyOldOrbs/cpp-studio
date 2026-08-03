package audiobook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cpp-studio/internal/jobs"
	"cpp-studio/internal/wav"
)

func TestBenchmarkFixtureMatchesCheckedInHarnessFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "benchmark", "dramabox-factual.txt"))
	if err != nil {
		t.Fatal(err)
	}
	normalized := strings.ReplaceAll(string(data), "\r\n", "\n")
	if strings.TrimSpace(normalized) != strings.TrimSpace(BenchmarkFixture) {
		t.Fatal("server benchmark fixture drifted from the checked-in harness fixture")
	}
}

func TestBenchmarkJobPersistsFingerprintMetricsAndStaleness(t *testing.T) {
	root := t.TempDir()
	benchmarkRoot := filepath.Join(root, "benchmarks")
	registry := jobs.NewRegistry()
	manager := NewManager(ManagerOptions{
		RootDir: filepath.Join(root, "books"), BenchmarkRootDir: benchmarkRoot, Jobs: registry,
		SynthesizeDetailed: func(_ context.Context, request SynthesisRequest) (SynthesisResult, error) {
			actual := request.Options.Seed
			return SynthesisResult{Audio: wav.SyntheticTone(1600), ActualSeed: &actual}, nil
		},
		ResolveEngine: func(context.Context, string) (EngineIdentity, error) {
			return EngineIdentity{ID: DramaBoxEngineID, Mode: "subprocess", ModelID: "fixture", Fingerprint: "engine-a"}, nil
		},
		Verify: func(_ context.Context, source string, _ []byte) (Verification, error) {
			return Verification{Transcript: source, VerifierIdentity: "fixture-asr"}, nil
		},
	})
	id, err := manager.StartBenchmark(context.Background(), BenchmarkRequest{Backend: "cpu"})
	if err != nil {
		t.Fatal(err)
	}
	waitForAudiobookJob(t, registry, id)
	result, err := manager.BenchmarkResult(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "complete" || result.ProfileFingerprint == "" || result.WarmRTF <= 0 || result.ProjectedChapterSeconds <= 0 || result.IdentityChanged {
		t.Fatalf("benchmark result omitted evidence: %+v", result)
	}
	if result.PromptSpec.SpeakerPhrase != DefaultSpeakerPhrase || result.PromptSpec.DeliveryPreset != DefaultDeliveryPreset {
		t.Fatalf("benchmark omitted the exact structured prompt profile: %+v", result.PromptSpec)
	}
	byID := map[string]BenchmarkCaseResult{}
	for _, item := range result.Cases {
		byID[item.ID] = item
	}
	for _, caseID := range []string{"cpu.cold_text", "cpu.warm_text", "cpu.long_form", "cpu.mem_saver_off", "cpu.mem_saver_on", "voice.clone", "recovery.cancel_restart", "fidelity.asr"} {
		if _, ok := byID[caseID]; !ok {
			t.Fatalf("benchmark case %s missing: %+v", caseID, result.Cases)
		}
	}
	if byID["cpu.long_form"].Sections < 2 || byID["fidelity.asr"].Fidelity == nil || byID["fidelity.asr"].Fidelity.Status != SectionStatusVerified {
		t.Fatalf("long-form/fidelity evidence incomplete: long=%+v fidelity=%+v", byID["cpu.long_form"], byID["fidelity.asr"])
	}
	if _, err := os.Stat(filepath.Join(benchmarkRoot, id, "result.json")); err != nil {
		t.Fatal(err)
	}

	changed := NewManager(ManagerOptions{
		RootDir: filepath.Join(root, "other-books"), BenchmarkRootDir: benchmarkRoot,
		ResolveEngine: func(context.Context, string) (EngineIdentity, error) {
			return EngineIdentity{ID: DramaBoxEngineID, Mode: "subprocess", ModelID: "fixture", Fingerprint: "engine-b"}, nil
		},
	})
	stale, err := changed.BenchmarkResult(context.Background(), id)
	if err != nil || !stale.IdentityChanged || stale.IdentityChangeReason == "" {
		t.Fatalf("changed runtime did not stale persisted result: stale=%+v err=%v", stale, err)
	}
	listed, err := changed.ListBenchmarkResults(context.Background())
	if err != nil || len(listed) != 1 || listed[0].ID != id || !listed[0].IdentityChanged {
		t.Fatalf("persisted benchmark discovery failed: listed=%+v err=%v", listed, err)
	}
}

func TestBenchmarkProfileFingerprintIncludesStructuredPrompt(t *testing.T) {
	engineIdentity := EngineIdentity{ID: DramaBoxEngineID, Fingerprint: "engine"}
	voiceIdentity := VoiceIdentity{ID: "default", Fingerprint: "default"}
	options := SynthesisOptions{NumInferenceSteps: 30, GuidanceScale: 2.5}
	man := DramaBoxPromptSpec{SpeakerPhrase: "A man", DeliveryPreset: DefaultDeliveryPreset}
	woman := man
	woman.SpeakerPhrase = "A woman"
	first := benchmarkProfileFingerprint(engineIdentity, voiceIdentity, options, "delivery", man, "cpu", "fixture")
	second := benchmarkProfileFingerprint(engineIdentity, voiceIdentity, options, "delivery", woman, "cpu", "fixture")
	if first == second {
		t.Fatal("speaker phrase did not change benchmark profile identity")
	}
}

func TestBenchmarkUsesRequestedStructuredPrompt(t *testing.T) {
	registry := jobs.NewRegistry()
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(), Jobs: registry,
		Synthesize: func(context.Context, SynthesisRequest) ([]byte, error) { return wav.SyntheticTone(1000), nil },
	})
	want := DramaBoxPromptSpec{SpeakerPhrase: "A woman", DeliveryPreset: "clear-explainer"}
	id, err := manager.StartBenchmark(context.Background(), BenchmarkRequest{Backend: "cpu", PromptSpec: want})
	if err != nil {
		t.Fatal(err)
	}
	waitForAudiobookJob(t, registry, id)
	result, err := manager.BenchmarkResult(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if result.PromptSpec != want {
		t.Fatalf("benchmark prompt drifted: got %+v want %+v", result.PromptSpec, want)
	}
}

func TestBenchmarkRequiresExplicitCUDA(t *testing.T) {
	manager := NewManager(ManagerOptions{})
	if _, err := manager.StartBenchmark(context.Background(), BenchmarkRequest{Backend: "cuda"}); err == nil || !IsRequestError(err) {
		t.Fatalf("implicit CUDA benchmark was accepted: %v", err)
	}
}
