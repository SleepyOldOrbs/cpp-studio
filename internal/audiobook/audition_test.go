package audiobook

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cpp-studio/internal/jobs"
	"cpp-studio/internal/wav"
)

func TestAuditionUsesDisplayedPassageAndResolvedSettingsWithoutSavingBook(t *testing.T) {
	for _, engineID := range []string{DefaultEngineID, DramaBoxEngineID} {
		t.Run(engineID, func(t *testing.T) {
			root := t.TempDir()
			registry := jobs.NewRegistry()
			var spoken SynthesisRequest
			var released atomic.Int32
			manager := NewManager(ManagerOptions{RootDir: root, Jobs: registry, ReserveEngine: func(context.Context, string) (func(), bool) { return func() { released.Add(1) }, true }, Synthesize: func(_ context.Context, request SynthesisRequest) ([]byte, error) {
				spoken = request
				return wav.SyntheticTone(16000), nil
			}})
			document := DocumentRequest{Filename: "story.md", Data: []byte("# Opening\nThe old clock chimed.\n# Ending\nThe town fell quiet."), Request: Request{EngineID: engineID, VoiceID: "selected-voice", Verification: VerificationModeOff}}
			preview, err := manager.PreviewDocument(context.Background(), document)
			if err != nil {
				t.Fatal(err)
			}
			document.Request.PlanDigest = preview.Plan.Digest
			id, err := manager.AuditionDocument(context.Background(), document, "chapter_002")
			if err != nil {
				t.Fatal(err)
			}
			waitForAudiobookJob(t, registry, id)
			if spoken.EngineID != engineID || spoken.VoiceID != "selected-voice" || !strings.Contains(spoken.Text, preview.Plan.Chapters[1].Excerpt) || strings.Contains(spoken.Text, "old clock") || spoken.Options != preview.Request.Options && engineID != DramaBoxEngineID {
				t.Fatalf("wrong audition: %+v", spoken)
			}
			if audio, err := manager.AuditionAudio(id); err != nil || wav.ValidateBytes(audio) != nil {
				t.Fatal("missing playable audition", err)
			}
			if entries, _ := os.ReadDir(root); len(entries) != 0 {
				t.Fatal("audition created a saved book")
			}
			deadline := time.Now().Add(time.Second)
			for released.Load() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if released.Load() != 1 {
				t.Fatal("reservation leaked")
			}
		})
	}
}

func TestAuditionRejectsMissingChapterStalePlanAndBusyEngine(t *testing.T) {
	for _, mode := range []string{"chapter", "stale", "busy"} {
		registry := jobs.NewRegistry()
		var invoked atomic.Int32
		manager := NewManager(ManagerOptions{RootDir: t.TempDir(), Jobs: registry, ReserveEngine: func(context.Context, string) (func(), bool) { return nil, false }, Synthesize: func(context.Context, SynthesisRequest) ([]byte, error) {
			invoked.Add(1)
			return wav.SyntheticTone(100), nil
		}})
		doc := DocumentRequest{Filename: "text.txt", Data: []byte("The displayed source.")}
		chapter := "chapter_001"
		if mode == "chapter" {
			chapter = "missing"
		}
		if mode == "stale" {
			doc.Request.PlanDigest = "stale"
		}
		if _, err := manager.AuditionDocument(context.Background(), doc, chapter); err == nil {
			t.Fatal(mode, "accepted")
		}
		if invoked.Load() != 0 || len(registry.List()) != 0 {
			t.Fatal(mode, "leaked work")
		}
	}
}

func TestAuditionCancellationAndFailureReleaseResources(t *testing.T) {
	for _, mode := range []string{"cancel", "failure", "invalid"} {
		registry := jobs.NewRegistry()
		started := make(chan struct{})
		var releases atomic.Int32
		manager := NewManager(ManagerOptions{RootDir: t.TempDir(), Jobs: registry, ReserveEngine: func(context.Context, string) (func(), bool) { return func() { releases.Add(1) }, true }, Synthesize: func(ctx context.Context, _ SynthesisRequest) ([]byte, error) {
			close(started)
			if mode == "cancel" {
				<-ctx.Done()
				return nil, errors.New("native process terminated with exit status 1")
			}
			if mode == "invalid" {
				return []byte("not audio"), nil
			}
			return nil, errors.New("fixture failure")
		}})
		id, err := manager.AuditionDocument(context.Background(), DocumentRequest{Filename: "source.txt", Data: []byte("Actual source passage.")}, "chapter_001")
		if err != nil {
			t.Fatal(err)
		}
		<-started
		if mode == "cancel" {
			if _, err := registry.Cancel(id); err != nil {
				t.Fatal(err)
			}
		}
		job := waitForAudiobookTerminal(t, registry, id)
		if mode == "cancel" && job.Status != jobs.StatusCancelled || mode != "cancel" && job.Status != jobs.StatusFailed {
			t.Fatal(mode, job)
		}
		if _, err := manager.AuditionAudio(id); err == nil {
			t.Fatal("failed audition retained audio")
		}
		deadline := time.Now().Add(time.Second)
		for releases.Load() == 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if releases.Load() != 1 {
			t.Fatal("reservation leaked")
		}
	}
}

func TestDramaBoxAuditionExcerptKeepsFirstProductionSectionSource(t *testing.T) {
	registry := jobs.NewRegistry()
	var spoken SynthesisRequest
	manager := NewManager(ManagerOptions{RootDir: t.TempDir(), Jobs: registry, Synthesize: func(_ context.Context, request SynthesisRequest) ([]byte, error) {
		spoken = request
		return wav.SyntheticTone(16000), nil
	}})
	source := "# Opening\n\nThe actual story begins here.\n\n" + strings.Repeat("The clock is ticking. ", 40)
	doc := DocumentRequest{Filename: "story.md", Data: []byte(source), Request: Request{EngineID: DramaBoxEngineID, Verification: VerificationModeOff}}
	preview, err := manager.PreviewDocument(context.Background(), doc)
	if err != nil {
		t.Fatal(err)
	}
	excerpt := preview.Plan.Chapters[0].Excerpt
	if !strings.HasPrefix(excerpt, "# Opening\n\nThe actual story begins here.") || !strings.HasPrefix(source, excerpt) || len([]rune(excerpt)) > DefaultChunkChars {
		t.Fatalf("excerpt is not the bounded original passage: %q", excerpt)
	}
	doc.Request.PlanDigest = preview.Plan.Digest
	id, err := manager.AuditionDocument(context.Background(), doc, "chapter_001")
	if err != nil {
		t.Fatal(err)
	}
	waitForAudiobookJob(t, registry, id)
	want := buildRequestPrompt(preview.Request, excerpt)
	if spoken.Text != want {
		t.Fatal("audition differs from displayed native passage", spoken.Text, want)
	}
}
