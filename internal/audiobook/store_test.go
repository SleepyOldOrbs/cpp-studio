package audiobook

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
