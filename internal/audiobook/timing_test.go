package audiobook

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"

	"cpp-studio/internal/jobs"
	"cpp-studio/internal/wav"
)

func TestChapterTimingsMatchShortClipCrossfadeAssembly(t *testing.T) {
	root := t.TempDir()
	paths := []string{filepath.Join(root, "one.wav"), filepath.Join(root, "two.wav")}
	for _, path := range paths {
		if err := os.WriteFile(path, wav.SyntheticTone(320), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	plan := &DocumentPlan{Chapters: []Chapter{{ID: "chapter_001"}, {ID: "chapter_002"}}}
	units := []narrationUnit{{chapterID: "chapter_001"}, {chapterID: "chapter_002"}}
	rate, bounds, err := chapterTimings(plan, units, paths, sectionCrossfade, 0)
	if err != nil {
		t.Fatal(err)
	}
	if rate != 16000 || bounds[1].StartSample != 4800 || bounds[1].EndSample != 9920 {
		t.Fatal(rate, bounds)
	}
	master := filepath.Join(root, "book.wav")
	if err := wav.AssembleFiles(master, paths, sectionCrossfade, artifactPad, artifactPad); err != nil {
		t.Fatal(err)
	}
	_, frames, err := wav.PCMFramesFile(master)
	if err != nil || frames != bounds[1].EndSample {
		t.Fatal("chapter end differs from actual master", frames, bounds, err)
	}
}

func TestChapterAttemptSelectionRecalculatesBoundariesWithoutChangingOldRender(t *testing.T) {
	root := t.TempDir()
	registry := jobs.NewRegistry()
	entropy := bytes.NewReader([]byte{0, 0, 0, 0, 0, 0, 0, 42, 0, 0, 0, 0, 0, 0, 0, 43, 0, 0, 0, 0, 0, 0, 0, 99})
	manager := NewManager(ManagerOptions{RootDir: root, Jobs: registry, SeedSource: entropy, SynthesizeDetailed: func(_ context.Context, req SynthesisRequest) (SynthesisResult, error) {
		actual := req.Options.Seed
		samples := 16000
		if actual == 99 {
			samples = 32000
		}
		return SynthesisResult{Audio: wav.SyntheticTone(samples), ActualSeed: &actual}, nil
	}})
	id, _, err := manager.Create(context.Background(), DocumentRequest{Filename: "book.md", Data: []byte("# One\nFirst words.\n# Two\nSecond words."), Request: Request{EngineID: DramaBoxEngineID, Verification: VerificationModeOff}})
	if err != nil {
		t.Fatal(err)
	}
	waitForAudiobookJob(t, registry, id)
	before, err := os.ReadFile(filepath.Join(root, id, ArtifactName))
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(before)
	retry, err := manager.RetrySection(context.Background(), id, "section-0001", RetryModeVariation)
	if err != nil {
		t.Fatal(err)
	}
	waitForAudiobookJob(t, registry, retry)
	render, err := manager.SelectAttempt(context.Background(), id, "section-0001", "attempt-0002")
	if err != nil {
		t.Fatal(err)
	}
	waitForAudiobookJob(t, registry, render)
	manifest, _, err := manager.store.LoadDurableFinal(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.RenderRevisions) != 2 || manifest.RenderRevisions[0].ChapterBoundaries[1].StartSample != 20000 || manifest.RenderRevisions[0].ChapterBoundaries[1].EndSample != 40800 || manifest.RenderRevisions[1].ChapterBoundaries[1].StartSample != 36000 || manifest.RenderRevisions[1].ChapterBoundaries[1].EndSample != 56800 {
		t.Fatalf("render timing lineage wrong: %+v", manifest.RenderRevisions)
	}
	after, err := os.ReadFile(filepath.Join(root, id, ArtifactName))
	if err != nil || sha256.Sum256(after) != hash {
		t.Fatal("old master changed", err)
	}
}
