package audiobook

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"cpp-studio/internal/jobs"
	"cpp-studio/internal/wav"
)

func TestReproduceSectionRetainsImmutableAttemptAndSeedEvidence(t *testing.T) {
	root := t.TempDir()
	registry := jobs.NewRegistry()
	manager := NewManager(ManagerOptions{
		RootDir: root, Jobs: registry, SeedSource: bytes.NewReader([]byte{0, 0, 0, 0, 0, 0, 0, 42}),
		SynthesizeDetailed: func(_ context.Context, request SynthesisRequest) (SynthesisResult, error) {
			actual := request.Options.Seed
			return SynthesisResult{Audio: wav.SyntheticTone(800), ActualSeed: &actual}, nil
		},
	})
	id, _, err := manager.Submit(context.Background(), Request{Text: "A reproducible fact.", EngineID: DramaBoxEngineID, Verification: VerificationModeOff})
	if err != nil {
		t.Fatal(err)
	}
	waitForAudiobookJob(t, registry, id)
	jobID, err := manager.RetrySection(context.Background(), id, "section-0001", RetryModeReproduce)
	if err != nil {
		t.Fatal(err)
	}
	job := waitForAudiobookJob(t, registry, jobID)
	if job.Result["deterministicMatch"] != "true" {
		t.Fatalf("same-seed byte comparison not reported: %+v", job.Result)
	}
	manifest, _, err := manager.store.LoadDurableFinal(id)
	if err != nil {
		t.Fatal(err)
	}
	attempts := manifest.Sections[0].Attempts
	if len(attempts) != 2 || !attempts[0].Selected || attempts[1].Selected {
		t.Fatalf("retry silently replaced selection: %+v", attempts)
	}
	if attempts[1].ParentAttemptID != attempts[0].ID || attempts[1].RequestedSeed != 42 || attempts[1].ActualSeed == nil || *attempts[1].ActualSeed != 42 || attempts[1].SeedStatus != "confirmed" {
		t.Fatalf("retry seed evidence is incomplete: %+v", attempts[1])
	}
	if attempts[1].DeterministicMatch == nil || !*attempts[1].DeterministicMatch || attempts[1].SynthesisMS <= 0 {
		t.Fatalf("retry timing/determinism missing: %+v", attempts[1])
	}
	if _, err := os.Stat(filepath.Join(root, id, filepath.FromSlash(attempts[1].AudioFile))); err != nil {
		t.Fatalf("immutable retry audio missing: %v", err)
	}
}

func TestRetryRejectsActualSeedMismatchWithoutPersistingAttempt(t *testing.T) {
	root := t.TempDir()
	registry := jobs.NewRegistry()
	var calls atomic.Int32
	manager := NewManager(ManagerOptions{
		RootDir: root, Jobs: registry,
		SynthesizeDetailed: func(_ context.Context, request SynthesisRequest) (SynthesisResult, error) {
			actual := request.Options.Seed
			if calls.Add(1) > 1 {
				actual++
			}
			return SynthesisResult{Audio: wav.SyntheticTone(800), ActualSeed: &actual}, nil
		},
	})
	id, _, err := manager.Submit(context.Background(), Request{Text: "A seed-bound fact.", EngineID: DramaBoxEngineID, Verification: VerificationModeOff})
	if err != nil {
		t.Fatal(err)
	}
	waitForAudiobookJob(t, registry, id)
	jobID, err := manager.RetrySection(context.Background(), id, "section-0001", RetryModeReproduce)
	if err != nil {
		t.Fatal(err)
	}
	job := waitForAudiobookTerminal(t, registry, jobID)
	if job.Status != jobs.StatusFailed || job.Error == "" {
		t.Fatalf("seed mismatch did not fail retry: %+v", job)
	}
	manifest, _, err := manager.store.LoadDurableFinal(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Sections[0].Attempts) != 1 {
		t.Fatalf("mismatched actual seed became an attempt: %+v", manifest.Sections[0].Attempts)
	}
}
