package audiobook

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cpp-studio/internal/jobs"
	"cpp-studio/internal/wav"
)

func TestLegacySynthesisIdentityWithoutDocumentPlan(t *testing.T) {
	identity := buildSynthesisIdentity(Request{Text: "The old book.", EngineID: DefaultEngineID, VoiceID: "voice-one"}, EngineIdentity{ID: DefaultEngineID, Mode: "subprocess", ModelID: "narrator", Fingerprint: "engine-v1"}, VoiceIdentity{ID: "voice-one", Fingerprint: "voice-v1"})
	if identity.Fingerprint != "295bf04c3e79570178f5bea761f4b16abb4e9f4910eb9bf67034b32e4e97a128" {
		t.Fatal("legacy fingerprint changed", identity.Fingerprint)
	}
}

func TestChapteredResumePreservesPlanAndCompletedAudio(t *testing.T) {
	root := t.TempDir()
	registry := jobs.NewRegistry()
	started := make(chan struct{}, 1)
	var calls atomic.Int32
	manager := NewManager(ManagerOptions{RootDir: root, Jobs: registry, Synthesize: func(ctx context.Context, _ SynthesisRequest) ([]byte, error) {
		if calls.Add(1) == 1 {
			return wav.SyntheticTone(16000), nil
		}
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	document := DocumentRequest{Filename: "book.md", Data: []byte("# One\nFirst words.\n# Two\nSecond words."), Request: Request{EngineID: DramaBoxEngineID, Verification: VerificationModeOff}}
	preview, err := manager.PreviewDocument(context.Background(), document)
	if err != nil {
		t.Fatal(err)
	}
	document.Request.PlanDigest = preview.Plan.Digest
	id, _, err := manager.Create(context.Background(), document)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("second chapter did not start")
	}
	if _, err := registry.Cancel(id); err != nil {
		t.Fatal(err)
	}
	if job := waitForAudiobookTerminal(t, registry, id); job.Status != jobs.StatusCancelled {
		t.Fatal(job)
	}
	manifest, _, err := manager.store.LoadDurableWIP(id)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.DocumentPlan.Digest != preview.Plan.Digest {
		t.Fatal("interruption changed plan")
	}
	tampered := manifest
	tampered.Sections = append([]Section(nil), manifest.Sections...)
	tampered.Sections[0].ChapterID = "chapter_002"
	if err := validateDurableManifest(tampered, string(document.Data)); err == nil {
		t.Fatal("corrupt chapter membership accepted")
	}
	resumedRegistry := jobs.NewRegistry()
	var resumedCalls atomic.Int32
	resumed := NewManager(ManagerOptions{RootDir: root, Jobs: resumedRegistry, Synthesize: func(_ context.Context, req SynthesisRequest) ([]byte, error) {
		resumedCalls.Add(1)
		if !strings.Contains(req.Text, "# Two\nSecond words.") || strings.Contains(req.Text, "First words.") {
			t.Errorf("regenerated wrong chapter: %q", req.Text)
		}
		return wav.SyntheticTone(16000), nil
	}})
	if err := resumed.CanResume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := resumed.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	waitForAudiobookJob(t, resumedRegistry, id)
	final, _, err := resumed.store.LoadDurableFinal(id)
	if err != nil {
		t.Fatal(err)
	}
	if resumedCalls.Load() != 1 || final.DocumentPlan.Digest != preview.Plan.Digest || len(final.ChapterBoundaries) != 2 || final.ChapterBoundaries[1].EndSample != 40800 {
		t.Fatal("resume lost frozen plan or audio", final)
	}
}

func chapterManager(t *testing.T, engineFingerprint *string, reserved *int, spoken *[]string) (*Manager, *jobs.Registry) {
	t.Helper()
	registry := jobs.NewRegistry()
	manager := NewManager(ManagerOptions{RootDir: t.TempDir(), Jobs: registry,
		ResolveEngine: func(_ context.Context, id string) (EngineIdentity, error) {
			return EngineIdentity{ID: id, Mode: "subprocess", ModelID: "narrator", Fingerprint: *engineFingerprint}, nil
		},
		ResolveVoice: func(_ context.Context, id string) (VoiceIdentity, error) {
			return VoiceIdentity{ID: id, Fingerprint: "voice-v1"}, nil
		},
		ReserveEngine: func(context.Context, string) (func(), bool) { *reserved++; return func() {}, true },
		Synthesize: func(_ context.Context, req SynthesisRequest) ([]byte, error) {
			*spoken = append(*spoken, req.Text)
			return wav.SyntheticTone(16000), nil
		},
	})
	return manager, registry
}

func TestDocumentPlanPreviewCreateAgreementAcrossNarrators(t *testing.T) {
	for _, engineID := range []string{DefaultEngineID, DramaBoxEngineID} {
		t.Run(engineID, func(t *testing.T) {
			fingerprint := "engine-v1"
			reserved := 0
			var spoken []string
			manager, registry := chapterManager(t, &fingerprint, &reserved, &spoken)
			document := DocumentRequest{Filename: "book.md", Data: []byte("# One\nFirst words.\n# Two\nSecond words."), Request: Request{EngineID: engineID, Verification: VerificationModeOff}}
			preview, err := manager.PreviewDocument(context.Background(), document)
			if err != nil {
				t.Fatal(err)
			}
			if preview.Plan == nil {
				t.Fatal("document preview has no plan")
			}
			if preview.Plan == nil || len(preview.Plan.Chapters) != 2 || len(preview.Plan.Digest) != 64 {
				t.Fatalf("missing chapter plan: %+v", preview.Plan)
			}
			if reserved != 0 || len(spoken) != 0 {
				t.Fatal("preview invoked the engine")
			}
			document.Request.PlanDigest = preview.Plan.Digest
			id, count, err := manager.Create(context.Background(), document)
			if err != nil {
				t.Fatal(err)
			}
			waitForAudiobookJob(t, registry, id)
			if count != 2 || len(spoken) != 2 {
				t.Fatalf("chapters shared a synthesis unit: count=%d, spoken=%v", count, spoken)
			}
			manifest, ok, err := manager.Status(id)
			if err != nil || !ok {
				t.Fatal(ok, err)
			}
			if manifest.DocumentPlan == nil || manifest.DocumentPlan.Digest != preview.Plan.Digest || manifest.SynthesisIdentity.PlanDigest != preview.Plan.Digest {
				t.Fatal("preview plan was not frozen", manifest)
			}
			wantSecond, wantEnd := int64(27200), int64(48000)
			if engineID == DramaBoxEngineID {
				wantSecond, wantEnd = 20000, 40800
			}
			if manifest.SampleRate != 16000 || len(manifest.ChapterBoundaries) != 2 || manifest.ChapterBoundaries[0].StartSample != 0 || manifest.ChapterBoundaries[0].EndSample != wantSecond || manifest.ChapterBoundaries[1].StartSample != wantSecond || manifest.ChapterBoundaries[1].EndSample != wantEnd {
				t.Fatalf("wrong rendered boundaries: rate=%d, chapters=%+v", manifest.SampleRate, manifest.ChapterBoundaries)
			}
			if len(manifest.RenderRevisions) != 1 || manifest.RenderRevisions[0].ChapterBoundaries[1].EndSample != wantEnd {
				t.Fatal("render did not freeze its own boundaries", manifest.RenderRevisions)
			}
			source, err := os.ReadFile(filepath.Join(manager.rootDir, id, sourceFileName))
			if err != nil || string(source) != string(document.Data) {
				t.Fatal("canonical source not saved", err)
			}
		})
	}
}

func TestStaleDocumentPlanRejectedBeforeReservationOrWork(t *testing.T) {
	for _, change := range []string{"source", "voice", "engine"} {
		t.Run(change, func(t *testing.T) {
			fingerprint := "engine-v1"
			reserved := 0
			var spoken []string
			manager, registry := chapterManager(t, &fingerprint, &reserved, &spoken)
			document := DocumentRequest{Filename: "book.txt", Data: []byte("First words."), Request: Request{VoiceID: "voice-one"}}
			preview, err := manager.PreviewDocument(context.Background(), document)
			if err != nil {
				t.Fatal(err)
			}
			document.Request.PlanDigest = preview.Plan.Digest
			switch change {
			case "source":
				document.Data = []byte("Changed words.")
			case "voice":
				document.Request.VoiceID = "voice-two"
			case "engine":
				fingerprint = "engine-v2"
			}
			if _, _, err := manager.Create(context.Background(), document); !IsRequestError(err) {
				t.Fatalf("stale plan accepted: %v", err)
			}
			if reserved != 0 || len(spoken) != 0 || len(registry.List()) != 0 {
				t.Fatal("stale plan leaked work")
			}
			entries, _ := os.ReadDir(manager.rootDir)
			if len(entries) != 0 {
				t.Fatal("stale plan created durable work")
			}
		})
	}
}

func TestDocumentPreviewEnforcesTotalUnitLimitAcrossChapters(t *testing.T) {
	for _, id := range []string{DefaultEngineID, DramaBoxEngineID} {
		manager := NewManager(ManagerOptions{RootDir: t.TempDir()})
		paragraph := strings.Repeat("fact ", 188) + "\n\n"
		source := "# One\n" + strings.Repeat(paragraph, 301) + "# Two\n" + strings.Repeat(paragraph, 301)
		if _, err := manager.PreviewDocument(context.Background(), DocumentRequest{Filename: "large.md", Data: []byte(source), Request: Request{EngineID: id}}); !IsRequestError(err) {
			t.Fatalf("%s preview approved oversized production: %v", id, err)
		}
	}
}
