package audiobook

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"cpp-studio/internal/jobs"
	"cpp-studio/internal/wav"
)

func TestStructuredPromptExactPreviewAndNormalizationEvidence(t *testing.T) {
	source := `Ada wrote “the engine works” in 1843.`
	evaluation, err := EvaluateDramaBoxPrompt(DramaBoxPromptSpec{
		SpeakerPhrase: "A woman", DeliveryPreset: "factual-documentary",
	}, source)
	if err != nil {
		t.Fatal(err)
	}
	want := `A woman speaks with warm, measured documentary delivery, clear diction, restrained emotion, and thoughtful pauses, "Ada wrote 'the engine works' in 1843."`
	if evaluation.GeneratedPrompt != want || !strings.HasSuffix(evaluation.GeneratedPrompt, `"`) {
		t.Fatalf("exact prompt mismatch:\nwant %q\n got %q", want, evaluation.GeneratedPrompt)
	}
	if len(evaluation.Normalizations) != 1 || evaluation.Normalizations[0].Field != "sourceText" || evaluation.Normalizations[0].Original != source {
		t.Fatalf("source punctuation change was not disclosed: %+v", evaluation.Normalizations)
	}
	if !strings.Contains(evaluation.Normalizations[0].Normalized, "the engine works") {
		t.Fatal("source words changed during punctuation normalization")
	}
}

func TestPromptLinterWarnsWithoutMutatingAdvancedDirection(t *testing.T) {
	direction := "  A radio host narrator, intensely warm, extremely calm, highly precise, very dramatic, deeply thoughtful  "
	evaluation, err := EvaluateDramaBoxPrompt(DramaBoxPromptSpec{SpeakerPhrase: "A man", AdvancedDirection: direction}, "A fact.")
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, warning := range evaluation.Warnings {
		codes[warning.Code] = true
	}
	if !codes["role-noun"] || !codes["stacked-description"] {
		t.Fatalf("missing warnings: %+v", evaluation.Warnings)
	}
	if evaluation.Spec.AdvancedDirection != strings.TrimSpace(direction) {
		t.Fatalf("linter silently rewrote direction: %q", evaluation.Spec.AdvancedDirection)
	}
	if len(evaluation.Normalizations) != 2 || evaluation.Normalizations[1].Field != "advancedDirectionPunctuation" {
		t.Fatalf("advanced punctuation normalization was not disclosed: %+v", evaluation.Normalizations)
	}
}

func TestPromptPolicyRejectsQuotesAndUnvalidatedSpeakerPhrase(t *testing.T) {
	for _, spec := range []DramaBoxPromptSpec{
		{SpeakerPhrase: "A narrator", DeliveryPreset: DefaultDeliveryPreset},
		{SpeakerPhrase: "A man", AdvancedDirection: `says "stage direction"`},
	} {
		if _, err := EvaluateDramaBoxPrompt(spec, "A fact."); err == nil || !IsRequestError(err) {
			t.Fatalf("unsafe prompt spec accepted: %+v, %v", spec, err)
		}
	}
}

func TestPromptLinterFlagsParalinguisticSourceWithoutChangingIt(t *testing.T) {
	evaluation, err := EvaluateDramaBoxPrompt(DramaBoxPromptSpec{}, "The source literally records Hahaha in the transcript.")
	if err != nil {
		t.Fatal(err)
	}
	if len(evaluation.Warnings) != 1 || evaluation.Warnings[0].Code != "paralinguistic-cue" {
		t.Fatalf("expected factual cue warning: %+v", evaluation.Warnings)
	}
	if !strings.Contains(evaluation.GeneratedPrompt, "Hahaha") {
		t.Fatal("warning mutated immutable source content")
	}
}

func TestPromptSectionPreviewUsesProductionRanges(t *testing.T) {
	source := repeatedWords("first", 190) + "\n\n" + repeatedWords("second", 190)
	previews, err := PreviewDramaBoxPromptSections(DramaBoxPromptSpec{}, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(previews) != 2 || previews[0].StartByte != 0 || previews[1].EndByte != int64(len(source)) {
		t.Fatalf("prompt preview did not use production ranges: %+v", previews)
	}
	for _, preview := range previews {
		if !strings.HasSuffix(preview.GeneratedPrompt, `"`) {
			t.Fatalf("section prompt does not end at final quote: %q", preview.GeneratedPrompt)
		}
	}
}

func TestPromptWarningsRequireAcceptanceAndPersistAsProvenance(t *testing.T) {
	registry := jobs.NewRegistry()
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(), Jobs: registry, SeedSource: bytes.NewReader(make([]byte, 16)),
		Synthesize: func(context.Context, SynthesisRequest) ([]byte, error) { return wav.SyntheticTone(1000), nil },
	})
	request := Request{Text: "The source records Hahaha exactly.", EngineID: DramaBoxEngineID, Verification: VerificationModeOff}
	if _, _, err := manager.Submit(context.Background(), request); err == nil || !strings.Contains(err.Error(), "lint warnings") {
		t.Fatalf("unaccepted warning started work: %v", err)
	}
	if len(registry.List()) != 0 {
		t.Fatalf("warning rejection created a job: %+v", registry.List())
	}
	request.AcceptPromptWarnings = true
	id, _, err := manager.Submit(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	waitForAudiobookJob(t, registry, id)
	manifest, ok, err := manager.Status(id)
	if err != nil || !ok || len(manifest.PromptWarnings) != 1 || !manifest.AIGenerated || manifest.Watermark != "unknown" {
		t.Fatalf("prompt provenance missing: %+v, %v", manifest, err)
	}
}
