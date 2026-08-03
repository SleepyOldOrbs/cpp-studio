package audiobook

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cpp-studio/internal/jobs"
	"cpp-studio/internal/wav"
)

func durableTestManifest(t *testing.T, id, source string) Manifest {
	t.Helper()
	req, err := NormalizeRequest(Request{Text: source, EngineID: DramaBoxEngineID})
	if err != nil {
		t.Fatal(err)
	}
	identity := buildSynthesisIdentity(req,
		EngineIdentity{ID: DramaBoxEngineID, Mode: "subprocess", ModelID: "release-0.5", Fingerprint: "engine-fingerprint"},
		VoiceIdentity{ID: "default", Fingerprint: "voice-fingerprint"})
	sections, err := planDramaBoxSections(source, bytes.NewReader(bytes.Repeat([]byte{7}, 8*MaxChunks)))
	if err != nil {
		t.Fatal(err)
	}
	sections = prepareSectionCheckpoints(identity, sections)
	options := req.Options
	return Manifest{
		SchemaVersion: CurrentManifestSchemaVersion, ID: id, Title: "Durable test",
		EngineID: DramaBoxEngineID, Chunks: len(sections), CreatedAt: time.Unix(1_700_000_000, 0).UTC(),
		ArtifactURL: "/v1/audiobooks/" + id + "/artifact/" + ArtifactName,
		Status:      ProductionStatusSynthesizing, SourceFile: sourceFileName,
		SourceSHA256: identity.SourceSHA256, SynthesisFingerprint: identity.Fingerprint,
		SectionPolicyVersion: identity.SectionPolicyVersion, PromptPolicyVersion: identity.PromptPolicyVersion,
		Sections: sections, ResolvedOptions: &options, SynthesisIdentity: &identity,
	}
}

func TestStoreStagesCompletePlanThenPublishesAtomically(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	source := "First durable paragraph.\n\nSecond durable paragraph."
	manifest := durableTestManifest(t, "book_20260803_100000_001", source)

	staged, err := store.StageInitial(manifest, source)
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if wip, err := store.ListWIP(); err != nil || len(wip) != 0 {
		t.Fatalf("staging became discoverable: %v %+v", err, wip)
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{staged.path, filepath.Join(staged.path, sourceFileName), filepath.Join(staged.path, manifestFileName)} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm()&0o077 != 0 {
				t.Fatalf("staging path is not owner-only: %s mode=%v err=%v", path, info.Mode(), err)
			}
		}
	}
	if err := store.PublishInitial(staged); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if staged.path != "" {
		t.Fatal("published staging handle still names a private path")
	}
	loaded, ok, err := store.LoadWIP(manifest.ID)
	if err != nil || !ok {
		t.Fatalf("load WIP: ok=%v err=%v", ok, err)
	}
	if loaded.SourceSHA256 != manifest.SourceSHA256 || len(loaded.Sections) != len(manifest.Sections) {
		t.Fatalf("published manifest lost its complete plan: %+v", loaded)
	}
	for _, section := range loaded.Sections {
		if section.Seed == 0 || section.CheckpointFingerprint == "" {
			t.Fatalf("section seed/checkpoint not durable: %+v", section)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "."+manifest.ID+".wip", sourceFileName))
	if err != nil || string(data) != source {
		t.Fatalf("canonical source mismatch: %v %q", err, data)
	}
	newStore := NewStore(root)
	if found, err := newStore.ListWIP(); err != nil || len(found) != 1 || found[0].ID != manifest.ID {
		t.Fatalf("restart did not discover WIP: %v %+v", err, found)
	}
}

func TestManagerBusyDoesNotPublishDramaBoxProduction(t *testing.T) {
	root := t.TempDir()
	registry := jobs.NewRegistry()
	manager := NewManager(ManagerOptions{
		RootDir: root, Jobs: registry,
		ReserveEngine: func(context.Context, string) (func(), bool) { return nil, false },
		Synthesize: func(context.Context, SynthesisRequest) ([]byte, error) {
			t.Fatal("synthesis ran before reservation")
			return nil, nil
		},
	})
	if id, _, err := manager.Submit(context.Background(), Request{Text: "Never publish me.", EngineID: DramaBoxEngineID}); err == nil || id != "" {
		t.Fatalf("busy submit returned id=%q err=%v", id, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 || len(registry.List()) != 0 {
		t.Fatalf("busy submit left durable or live state: entries=%v jobs=%v", entries, registry.List())
	}
}

func TestStoreTrustsOnlyMatchingSectionAudio(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	manifest := durableTestManifest(t, "book_20260803_100000_002", "A durable section.")
	staged, err := store.StageInitial(manifest, "A durable section.")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PublishInitial(staged); err != nil {
		t.Fatal(err)
	}
	audio := wav.SyntheticTone(800)
	path, err := store.SaveSectionWIP(manifest.ID, manifest.Sections[0].ID, audio)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(audio)
	section := &manifest.Sections[0]
	section.Status = SectionStatusSynthesized
	section.AudioSHA256 = hex.EncodeToString(sum[:])
	section.Attempts = []Attempt{{
		ID: "attempt-0001", Seed: section.Seed, CheckpointFingerprint: section.CheckpointFingerprint,
		AudioFile: section.AudioFile, AudioSHA256: section.AudioSHA256, Selected: true, CreatedAt: time.Now().UTC(),
	}}
	if got, trusted, err := store.TrustedSectionWIP(manifest, *section); err != nil || !trusted || got != path {
		t.Fatalf("matching section was not trusted: path=%q trusted=%v err=%v", got, trusted, err)
	}
	if err := os.WriteFile(path, append([]byte(nil), audio[:len(audio)-1]...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, trusted, err := store.TrustedSectionWIP(manifest, *section); err != nil || trusted {
		t.Fatalf("tampered section was trusted: trusted=%v err=%v", trusted, err)
	}
}

func TestManagerPublishesCompleteWIPBeforeDramaBoxSynthesis(t *testing.T) {
	root := t.TempDir()
	registry := jobs.NewRegistry()
	entered := make(chan error, 1)
	release := make(chan struct{})
	manager := NewManager(ManagerOptions{
		RootDir: root, Jobs: registry,
		Synthesize: func(ctx context.Context, request SynthesisRequest) ([]byte, error) {
			entries, err := os.ReadDir(root)
			if err != nil {
				entered <- err
			} else {
				var wipName string
				for _, entry := range entries {
					if strings.HasSuffix(entry.Name(), ".wip") {
						wipName = entry.Name()
					}
					if strings.Contains(entry.Name(), ".staging-") {
						entered <- &testStoreError{"private staging remained visible"}
						return nil, context.Canceled
					}
				}
				if wipName == "" {
					entered <- &testStoreError{"no durable WIP before synthesis"}
				} else if _, err := os.Stat(filepath.Join(root, wipName, sourceFileName)); err != nil {
					entered <- err
				} else if _, err := os.Stat(filepath.Join(root, wipName, manifestFileName)); err != nil {
					entered <- err
				} else {
					entered <- nil
				}
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-release:
				return wav.SyntheticTone(800), nil
			}
		},
	})
	id, sections, err := manager.Submit(context.Background(), Request{Title: "Durable", Text: "A durable section.", EngineID: DramaBoxEngineID})
	if err != nil || id == "" || sections != 1 {
		t.Fatalf("submit: id=%q sections=%d err=%v", id, sections, err)
	}
	if err := <-entered; err != nil {
		t.Fatal(err)
	}
	manifest, ok, err := manager.store.LoadWIP(id)
	if err != nil || !ok || manifest.Status != ProductionStatusSynthesizing || len(manifest.Sections) != sections {
		t.Fatalf("durable initial manifest: ok=%v err=%v manifest=%+v", ok, err, manifest)
	}
	close(release)
	waitForAudiobookJob(t, registry, id)
	if _, err := os.Stat(filepath.Join(root, "."+id+".wip")); !os.IsNotExist(err) {
		t.Fatalf("completed WIP was not atomically published: %v", err)
	}
	final, ok, err := loadManifest(filepath.Join(root, id, manifestFileName))
	if err != nil || !ok || final.Status != ProductionStatusComplete || len(final.Sections[0].Attempts) != 1 {
		t.Fatalf("final durable manifest: ok=%v err=%v manifest=%+v", ok, err, final)
	}
	for _, path := range []string{sourceFileName, ArtifactName, "sections/section-0001.wav"} {
		if _, err := os.Stat(filepath.Join(root, id, filepath.FromSlash(path))); err != nil {
			t.Fatalf("missing durable production file %s: %v", path, err)
		}
	}
}

type testStoreError struct{ message string }

func (e *testStoreError) Error() string { return e.message }

func TestDramaBoxCancelPersistsAndResumeSkipsTrustedSections(t *testing.T) {
	root := t.TempDir()
	firstRegistry := jobs.NewRegistry()
	secondStarted := make(chan struct{}, 1)
	var firstCalls atomic.Int32
	manager := NewManager(ManagerOptions{
		RootDir: root, Jobs: firstRegistry,
		Synthesize: func(ctx context.Context, _ SynthesisRequest) ([]byte, error) {
			if firstCalls.Add(1) == 1 {
				return wav.SyntheticTone(800), nil
			}
			secondStarted <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	source := repeatedWords("first", 130) + "\n\n" + repeatedWords("second", 130)
	id, sections, err := manager.Submit(context.Background(), Request{Text: source, EngineID: DramaBoxEngineID})
	if err != nil || sections != 2 {
		t.Fatalf("submit: sections=%d err=%v", sections, err)
	}
	select {
	case <-secondStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("second section did not start")
	}
	if _, err := firstRegistry.Cancel(id); err != nil {
		t.Fatal(err)
	}
	if job := waitForAudiobookTerminal(t, firstRegistry, id); job.Status != jobs.StatusCancelled {
		t.Fatalf("cancel status: %+v", job)
	}
	interrupted, _, err := manager.store.LoadDurableWIP(id)
	if err != nil || interrupted.Status != ProductionStatusInterrupted || interrupted.Sections[0].Status != SectionStatusSynthesized || interrupted.Sections[1].Status != SectionStatusPending {
		t.Fatalf("interrupted checkpoint: err=%v manifest=%+v", err, interrupted)
	}

	secondRegistry := jobs.NewRegistry()
	var resumedCalls atomic.Int32
	resumed := NewManager(ManagerOptions{
		RootDir: root, Jobs: secondRegistry,
		Synthesize: func(context.Context, SynthesisRequest) ([]byte, error) {
			resumedCalls.Add(1)
			return wav.SyntheticTone(800), nil
		},
	})
	if resumedSections, err := resumed.Resume(context.Background(), id); err != nil || resumedSections != 2 {
		t.Fatalf("resume: sections=%d err=%v", resumedSections, err)
	}
	waitForAudiobookJob(t, secondRegistry, id)
	if resumedCalls.Load() != 1 {
		t.Fatalf("resume synthesized %d sections, want only the missing section", resumedCalls.Load())
	}
	final, ok, err := loadManifest(filepath.Join(root, id, manifestFileName))
	if err != nil || !ok || final.Status != ProductionStatusComplete || len(final.Sections) != 2 {
		t.Fatalf("resumed final manifest: ok=%v err=%v manifest=%+v", ok, err, final)
	}
}

func TestDramaBoxFinalAssemblyUsesSectionCrossfade(t *testing.T) {
	root := t.TempDir()
	registry := jobs.NewRegistry()
	format := wav.Format{Channels: 1, SampleRate: 1000, BitsPerSample: 16}
	pcm := make([]byte, 1000*2)
	manager := NewManager(ManagerOptions{
		RootDir: root, Jobs: registry,
		Synthesize: func(context.Context, SynthesisRequest) ([]byte, error) {
			return wav.Encode(format, pcm), nil
		},
	})
	source := repeatedWords("first", 130) + "\n\n" + repeatedWords("second", 130)
	id, sections, err := manager.Submit(context.Background(), Request{Text: source, EngineID: DramaBoxEngineID})
	if err != nil || sections != 2 {
		t.Fatalf("submit: sections=%d err=%v", sections, err)
	}
	waitForAudiobookJob(t, registry, id)
	duration, err := wav.DurationFile(filepath.Join(root, id, ArtifactName))
	if err != nil {
		t.Fatal(err)
	}
	if want := 2550 * time.Millisecond; duration != want {
		t.Fatalf("streamed audiobook duration=%s want=%s", duration, want)
	}
}

func TestResumeRejectsIdentityChangeAndRestartPreservesOriginal(t *testing.T) {
	root := t.TempDir()
	registry := jobs.NewRegistry()
	manager := NewManager(ManagerOptions{
		RootDir: root, Jobs: registry,
		Synthesize: func(context.Context, SynthesisRequest) ([]byte, error) {
			return nil, &testStoreError{"planned failure"}
		},
	})
	id, _, err := manager.Submit(context.Background(), Request{Text: "Preserve this source.", EngineID: DramaBoxEngineID})
	if err != nil {
		t.Fatal(err)
	}
	if job := waitForAudiobookTerminal(t, registry, id); job.Status != jobs.StatusFailed {
		t.Fatalf("planned job did not fail: %+v", job)
	}
	original, _, err := manager.store.LoadDurableWIP(id)
	if err != nil {
		t.Fatal(err)
	}

	changedRegistry := jobs.NewRegistry()
	changed := NewManager(ManagerOptions{
		RootDir: root, Jobs: changedRegistry,
		ResolveEngine: func(context.Context, string) (EngineIdentity, error) {
			return EngineIdentity{ID: DramaBoxEngineID, Mode: "subprocess", ModelID: "new-model", Fingerprint: "new-engine-fingerprint"}, nil
		},
		Synthesize: func(context.Context, SynthesisRequest) ([]byte, error) {
			return wav.SyntheticTone(800), nil
		},
	})
	if _, err := changed.Resume(context.Background(), id); !errors.Is(err, ErrSynthesisIdentityChanged) {
		t.Fatalf("resume accepted changed identity: %v", err)
	}
	stillOriginal, _, err := changed.store.LoadDurableWIP(id)
	if err != nil || stillOriginal.SynthesisFingerprint != original.SynthesisFingerprint || stillOriginal.Status != ProductionStatusInterrupted {
		t.Fatalf("identity conflict mutated original: err=%v manifest=%+v", err, stillOriginal)
	}
	newID, _, err := changed.Restart(context.Background(), id)
	if err != nil || newID == id {
		t.Fatalf("restart: old=%s new=%s err=%v", id, newID, err)
	}
	waitForAudiobookJob(t, changedRegistry, newID)
	if _, _, err := changed.store.LoadDurableWIP(id); err != nil {
		t.Fatalf("restart removed original WIP: %v", err)
	}
	restarted, ok, err := loadManifest(filepath.Join(root, newID, manifestFileName))
	if err != nil || !ok || restarted.SynthesisFingerprint == original.SynthesisFingerprint {
		t.Fatalf("restart did not use new identity: ok=%v err=%v manifest=%+v", ok, err, restarted)
	}
	if err := changed.Discard(id); err != nil {
		t.Fatalf("discard original: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "."+id+".wip")); !os.IsNotExist(err) {
		t.Fatalf("discard left WIP: %v", err)
	}
}

func TestResumeRejectsCanonicalSourceCorruption(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	manifest := durableTestManifest(t, "book_20260803_100000_009", "Canonical source.")
	manifest.Status = ProductionStatusInterrupted
	staged, err := store.StageInitial(func() Manifest { copy := manifest; copy.Status = ProductionStatusSynthesizing; return copy }(), "Canonical source.")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PublishInitial(staged); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveManifestWIP(manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "."+manifest.ID+".wip", sourceFileName), []byte("Tampered source."), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(ManagerOptions{RootDir: root, Synthesize: func(context.Context, SynthesisRequest) ([]byte, error) {
		return wav.SyntheticTone(800), nil
	}})
	if _, err := manager.Resume(context.Background(), manifest.ID); !errors.Is(err, ErrStoreCorrupt) {
		t.Fatalf("resume accepted corrupt canonical source: %v", err)
	}
}
