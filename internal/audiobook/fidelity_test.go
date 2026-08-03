package audiobook

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cpp-studio/internal/jobs"
	"cpp-studio/internal/wav"
)

func TestEvaluateFidelityPassesNormalizedExactTranscript(t *testing.T) {
	source := "On 3 May 2026, NASA met Ada Lovelace in London."
	section := Section{ID: "section-0001", TextSHA256: strings.Repeat("a", 64)}
	report := evaluateFidelity(section, source, Verification{
		Transcript:       "on 3 may 2026 nasa met ada lovelace in london",
		VerifierIdentity: "whisper-test",
	}, time.Unix(1_700_000_000, 0).UTC())
	if report.Status != SectionStatusVerified || report.WordErrorRate != 0 || report.VerifierIdentity != "whisper-test" {
		t.Fatalf("exact normalized transcript was not verified: %+v", report)
	}
	if len(report.MissingNumericOrDates)+len(report.MissingAcronyms)+len(report.MissingProperNames) != 0 {
		t.Fatalf("matching anchors were reported missing: %+v", report)
	}
}

func TestEvaluateFidelityFlagsEditsAndCriticalAnchors(t *testing.T) {
	source := "On 3 May 2026, NASA met Ada Lovelace."
	report := evaluateFidelity(Section{ID: "section-0001"}, source, Verification{
		Transcript: "On four May 2025, the agency met Ada.", VerifierIdentity: "whisper-test",
	}, time.Now().UTC())
	if report.Status != SectionStatusFlagged || report.WordErrorRate <= FidelityWERThreshold {
		t.Fatalf("material transcript drift was not flagged: %+v", report)
	}
	if len(report.MissingNumericOrDates) == 0 || len(report.MissingAcronyms) == 0 || len(report.MissingProperNames) == 0 {
		t.Fatalf("critical anchor warnings missing: %+v", report)
	}
	insertions, deletions, substitutions := editCounts([]string{"a", "b", "c"}, []string{"a", "x", "c", "d"})
	if insertions != 1 || deletions != 0 || substitutions != 1 {
		t.Fatalf("edit counts=%d/%d/%d want 1/0/1", insertions, deletions, substitutions)
	}
}

func TestVerificationModesPersistHonestOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		mode       VerificationMode
		verify     VerifyFunc
		wantStatus VerificationStatus
		wantJob    jobs.Status
	}{
		{name: "off", mode: VerificationModeOff, wantStatus: VerificationStatusSkipped, wantJob: jobs.StatusComplete},
		{name: "auto unavailable", mode: VerificationModeAuto, wantStatus: VerificationStatusUnavailable, wantJob: jobs.StatusComplete},
		{name: "auto verifier error", mode: VerificationModeAuto, verify: func(context.Context, string, []byte) (Verification, error) {
			return Verification{}, errors.New("fixture Whisper failure")
		}, wantStatus: VerificationStatusUnavailable, wantJob: jobs.StatusComplete},
		{name: "required verifier error", mode: VerificationModeRequired, verify: func(context.Context, string, []byte) (Verification, error) {
			return Verification{}, errors.New("fixture Whisper failure")
		}, wantStatus: VerificationStatusUnavailable, wantJob: jobs.StatusFailed},
		{name: "passed", mode: VerificationModeAuto, verify: func(_ context.Context, source string, _ []byte) (Verification, error) {
			return Verification{Transcript: source, VerifierIdentity: "whisper-fixture"}, nil
		}, wantStatus: VerificationStatusPassed, wantJob: jobs.StatusComplete},
		{name: "flagged", mode: VerificationModeAuto, verify: func(context.Context, string, []byte) (Verification, error) {
			return Verification{Transcript: "unrelated words", VerifierIdentity: "whisper-fixture"}, nil
		}, wantStatus: VerificationStatusFlagged, wantJob: jobs.StatusComplete},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			registry := jobs.NewRegistry()
			manager := NewManager(ManagerOptions{
				RootDir: root, Jobs: registry, Verify: test.verify,
				Synthesize: func(context.Context, SynthesisRequest) ([]byte, error) { return wav.SyntheticTone(800), nil },
			})
			id, _, err := manager.Submit(context.Background(), Request{
				Text: "NASA recorded 3 exact facts for Ada Lovelace.", EngineID: DramaBoxEngineID, Verification: test.mode,
			})
			if err != nil {
				t.Fatalf("submit: %v", err)
			}
			job := waitForAudiobookTerminal(t, registry, id)
			if job.Status != test.wantJob {
				t.Fatalf("job status=%s want=%s error=%s", job.Status, test.wantJob, job.Error)
			}
			manifest, ok, err := manager.Status(id)
			if err != nil || !ok || manifest.Verification == nil || manifest.Verification.Status != test.wantStatus {
				t.Fatalf("verification status: ok=%v err=%v manifest=%+v", ok, err, manifest)
			}
			verificationPath, err := manager.store.VerificationPath(id)
			if err != nil {
				t.Fatalf("verification report: %v", err)
			}
			if data, err := os.ReadFile(verificationPath); err != nil || !strings.Contains(string(data), `"status"`) {
				t.Fatalf("aggregate report missing: %v %q", err, data)
			}
			if test.wantStatus == VerificationStatusPassed || test.wantStatus == VerificationStatusFlagged {
				base := filepath.Dir(verificationPath)
				for _, path := range []string{manifest.Sections[0].TranscriptFile, manifest.Sections[0].VerificationFile} {
					if _, err := os.Stat(filepath.Join(base, filepath.FromSlash(path))); err != nil {
						t.Fatalf("section evidence missing %s: %v", path, err)
					}
				}
			}
		})
	}
}

func TestRequiredVerificationRejectsBeforeDurableCreation(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(ManagerOptions{
		RootDir:    root,
		Synthesize: func(context.Context, SynthesisRequest) ([]byte, error) { return wav.SyntheticTone(800), nil },
	})
	id, _, err := manager.Submit(context.Background(), Request{
		Text: "Verification is mandatory.", EngineID: DramaBoxEngineID, Verification: VerificationModeRequired,
	})
	if err == nil || id != "" || !errors.Is(err, ErrVerificationUnavailable) {
		t.Fatalf("required verification without Whisper: id=%q err=%v", id, err)
	}
	if entries, readErr := os.ReadDir(root); readErr == nil && len(entries) != 0 {
		t.Fatalf("rejected required verification created state: %+v", entries)
	}
}
