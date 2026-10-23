package storybuilder

import (
	"context"
	"cpp-studio/internal/wav"
	"errors"
	"os"
	"testing"
)

func TestDialogueEngineSwitchPreservesFilesAndInvalidatesCurrentTakes(t *testing.T) {
	store, project := buildTestProject(t, []TimelineClip{{ID: "line_one", Type: ClipTypeDialogue, Label: "One", Text: "One.", DurationMS: 1000}})
	input, err := store.BeginDialogueBuild(project.ID, "line_one")
	if err != nil {
		t.Fatal(err)
	}
	if input.EngineID != "omnivoice" {
		t.Fatal(input)
	}
	project, err = store.CompleteDialogueBuild(project.ID, input, wav.SyntheticTone(16000))
	if err != nil {
		t.Fatal(err)
	}
	path, err := store.DialogueAudioPath(project.ID, "line_one")
	if err != nil {
		t.Fatal(err)
	}
	drama := "fish-drama3"
	project, err = store.Update(project.ID, ProjectUpdate{Name: project.Name, Revision: project.Revision, Tracks: project.Tracks, DialogueEngine: &drama})
	if err != nil {
		t.Fatal(err)
	}
	if clip := project.Tracks[0].Clips[0]; clip.Status != DialogueStatusStale || clip.SourceID != "" {
		t.Fatal(clip)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("lost previous take", err)
	}
	reopened := NewStoreWithOptions(store.rootDir, StoreOptions{ResolveCharacterVoice: store.resolveCharacterVoice})
	project, _, err = reopened.Get(project.ID)
	if err != nil || project.DialogueEngine != drama {
		t.Fatalf("reload: %+v %v", project, err)
	}
	// Older clients omit the field; arrangement-only saves must preserve the engine.
	project, err = reopened.Update(project.ID, ProjectUpdate{Name: project.Name, Revision: project.Revision, Tracks: project.Tracks})
	if err != nil || project.DialogueEngine != drama {
		t.Fatalf("omitted engine: %+v %v", project, err)
	}
	bad := "whisper"
	if _, err := store.Update(project.ID, ProjectUpdate{Name: project.Name, Revision: project.Revision, Tracks: project.Tracks, DialogueEngine: &bad}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	input, err = store.BeginDialogueBuild(project.ID, "line_one")
	if err != nil || input.EngineID != drama {
		t.Fatalf("input: %+v %v", input, err)
	}
	project, err = store.CompleteDialogueBuild(project.ID, input, wav.SyntheticTone(16000))
	if err != nil {
		t.Fatal(err)
	}
	source := project.Tracks[0].Clips[0].SourceID
	project.Tracks[0].Clips[0].StartMS = 1000
	project, err = store.Update(project.ID, ProjectUpdate{Name: project.Name, Revision: project.Revision, Tracks: project.Tracks})
	if err != nil || project.Tracks[0].Clips[0].SourceID != source {
		t.Fatalf("arrangement invalidated take: %v", err)
	}
}

func TestDialogueBuildCannotPublishOrInvokeAfterEngineChanges(t *testing.T) {
	store, project := buildTestProject(t, []TimelineClip{{ID: "line_one", Type: ClipTypeDialogue, Label: "One", Text: "One.", DurationMS: 1000}})
	drama := "fish-drama3"
	reserved := ""
	invoked := false
	manager := NewDialogueBuildManager(DialogueBuildManagerOptions{Store: store,
		ReserveEngine: func(_ context.Context, engineID string) (func(), bool) {
			reserved = engineID
			var err error
			project, err = store.Update(project.ID, ProjectUpdate{Name: project.Name, Revision: project.Revision, Tracks: project.Tracks, DialogueEngine: &drama})
			if err != nil {
				t.Fatal(err)
			}
			return func() {}, true
		},
		Synthesize: func(context.Context, DialogueSynthesisInput) ([]byte, error) {
			invoked = true
			return wav.SyntheticTone(16000), nil
		},
	})
	started, err := manager.Start(context.Background(), project.ID, project.Revision)
	if err != nil {
		t.Fatal(err)
	}
	waitDialogueBuild(t, manager, project.ID, started.ID, DialogueBuildFailed)
	if reserved != "omnivoice" || invoked {
		t.Fatalf("reserved=%s invoked=%v", reserved, invoked)
	}
	input, err := store.BeginDialogueBuild(project.ID, "line_one")
	if err != nil {
		t.Fatal(err)
	}
	project, _, _ = store.Get(project.ID)
	omni := "omnivoice"
	_, err = store.Update(project.ID, ProjectUpdate{Name: project.Name, Revision: project.Revision, Tracks: project.Tracks, DialogueEngine: &omni})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CompleteDialogueBuild(project.ID, input, wav.SyntheticTone(16000)); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}
