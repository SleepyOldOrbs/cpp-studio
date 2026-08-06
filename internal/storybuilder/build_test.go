package storybuilder

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"cpp-studio/internal/wav"
)

func TestDialogueBuildCandidatesAreChronologicalAndSuccessfulTakeIsDurable(t *testing.T) {
	root := t.TempDir()
	store := NewStoreWithOptions(root, StoreOptions{ResolveCharacterVoice: func(id string) (VoiceIdentity, bool, error) {
		return VoiceIdentity{CharacterVoiceID: id, ActorVoiceID: "actor_mara", Fingerprint: "voice-v1"}, true, nil
	}})
	project, err := store.Create("Chronological build")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	project, err = store.Update(project.ID, ProjectUpdate{Name: project.Name, Revision: project.Revision, Tracks: []Track{
		{ID: "late_track", Name: "Late", Type: TrackTypeDialogue, Order: 0, CharacterVoiceID: "character_mara", Clips: []TimelineClip{
			{ID: "line_late", Type: ClipTypeDialogue, Label: "Late line", Text: "Late line.", StartMS: 2000, DurationMS: 1200},
		}},
		{ID: "early_track", Name: "Early", Type: TrackTypeDialogue, Order: 1, CharacterVoiceID: "character_mara", Clips: []TimelineClip{
			{ID: "line_early", Type: ClipTypeDialogue, Label: "Early line", Text: "Early line.", StartMS: 250, DurationMS: 1200},
		}},
	}})
	if err != nil {
		t.Fatalf("save dialogue: %v", err)
	}

	candidates, err := store.DialogueBuildCandidates(project.ID, project.Revision)
	if err != nil {
		t.Fatalf("list candidates: %v", err)
	}
	if len(candidates) != 2 || candidates[0].ClipID != "line_early" || candidates[1].ClipID != "line_late" {
		t.Fatalf("candidate order = %+v, want early then late", candidates)
	}

	project, err = store.BeginDialogueBuild(project.ID, candidates[0].ClipID)
	if err != nil {
		t.Fatalf("begin clip: %v", err)
	}
	if got := project.Tracks[1].Clips[0].Status; got != DialogueStatusBuilding {
		t.Fatalf("begun clip status = %q, want building", got)
	}
	project, err = store.CompleteDialogueBuild(project.ID, candidates[0].ClipID, wav.SyntheticTone(16000))
	if err != nil {
		t.Fatalf("complete clip: %v", err)
	}
	ready := project.Tracks[1].Clips[0]
	if ready.Status != DialogueStatusReady || ready.SourceID == "" || ready.SourceDurationMS != 1000 || ready.DurationMS != 1000 || ready.BuildError != "" {
		t.Fatalf("completed clip = %+v", ready)
	}
	path, err := store.DialogueAudioPath(project.ID, ready.ID)
	if err != nil {
		t.Fatalf("resolve ready take: %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || wav.ValidateBytes(data) != nil {
		t.Fatalf("durable take is not readable WAV: path=%q err=%v", path, err)
	}

	remaining, err := store.DialogueBuildCandidates(project.ID, project.Revision)
	if err != nil {
		t.Fatalf("list remaining candidates: %v", err)
	}
	if len(remaining) != 1 || remaining[0].ClipID != "line_late" {
		t.Fatalf("ready clip was not skipped: %+v", remaining)
	}
}

func TestDialogueBuildManagerRunsOneChronologicalBuildAtATime(t *testing.T) {
	store, project := buildTestProject(t, []TimelineClip{
		{ID: "line_late", Type: ClipTypeDialogue, Label: "Late", Text: "Late.", StartMS: 2000, DurationMS: 1000},
		{ID: "line_early", Type: ClipTypeDialogue, Label: "Early", Text: "Early.", StartMS: 0, DurationMS: 1000},
	})

	entered := make(chan struct{})
	continueBuild := make(chan struct{})
	var spoken []string
	released := false
	manager := NewDialogueBuildManager(DialogueBuildManagerOptions{
		Store: store,
		ReserveEngine: func(context.Context, string) (func(), bool) {
			return func() { released = true }, true
		},
		Synthesize: func(_ context.Context, text, actorVoiceID string) ([]byte, error) {
			spoken = append(spoken, text+"/"+actorVoiceID)
			if len(spoken) == 1 {
				close(entered)
				<-continueBuild
			}
			return wav.SyntheticTone(16000), nil
		},
	})

	started, err := manager.Start(context.Background(), project.ID, project.Revision)
	if err != nil {
		t.Fatalf("start build: %v", err)
	}
	if started.ID == "" || started.StatusURL == "" || started.Status != DialogueBuildQueued {
		t.Fatalf("start response = %+v", started)
	}
	<-entered
	if _, err := manager.Start(context.Background(), project.ID, project.Revision); !errors.Is(err, ErrDialogueBuildBusy) {
		t.Fatalf("second build error = %v, want busy", err)
	}
	active, ok := manager.Status(project.ID, started.ID)
	if !ok || active.Status != DialogueBuildRunning || active.ActiveClipID != "line_early" || active.Completed != 0 || active.Total != 2 {
		t.Fatalf("active status = %+v ok=%v", active, ok)
	}

	close(continueBuild)
	final := waitDialogueBuild(t, manager, project.ID, started.ID, DialogueBuildComplete)
	if !reflect.DeepEqual(spoken, []string{"Early./actor_mara", "Late./actor_mara"}) {
		t.Fatalf("synthesis order = %v", spoken)
	}
	if final.Completed != 2 || final.Total != 2 || final.Progress != 1 || final.ActiveClipID != "" || !released {
		t.Fatalf("final status = %+v released=%v", final, released)
	}
	loaded, ok, err := store.Get(project.ID)
	if err != nil || !ok {
		t.Fatalf("reload completed project: ok=%v err=%v", ok, err)
	}
	for _, clip := range loaded.Tracks[0].Clips {
		if clip.Status != DialogueStatusReady || clip.SourceID == "" {
			t.Fatalf("completed build was not durable: %+v", clip)
		}
	}
}

func TestDialogueBuildFailureKeepsCompletedTakeAndRetrySkipsIt(t *testing.T) {
	store, project := buildTestProject(t, []TimelineClip{
		{ID: "line_one", Type: ClipTypeDialogue, Label: "One", Text: "One.", StartMS: 0, DurationMS: 1000},
		{ID: "line_two", Type: ClipTypeDialogue, Label: "Two", Text: "Two.", StartMS: 2000, DurationMS: 1000},
		{ID: "line_three", Type: ClipTypeDialogue, Label: "Three", Text: "Three.", StartMS: 4000, DurationMS: 1000},
	})
	var firstPass []string
	manager := NewDialogueBuildManager(DialogueBuildManagerOptions{
		Store: store,
		Synthesize: func(_ context.Context, text, _ string) ([]byte, error) {
			firstPass = append(firstPass, text)
			if text == "Two." {
				return nil, errors.New("fixture synthesis failed")
			}
			return wav.SyntheticTone(16000), nil
		},
	})
	started, err := manager.Start(context.Background(), project.ID, project.Revision)
	if err != nil {
		t.Fatalf("start failing build: %v", err)
	}
	failed := waitDialogueBuild(t, manager, project.ID, started.ID, DialogueBuildFailed)
	if failed.Completed != 1 || failed.Error != "fixture synthesis failed" || !reflect.DeepEqual(firstPass, []string{"One.", "Two."}) {
		t.Fatalf("failed build = %+v calls=%v", failed, firstPass)
	}
	partial, ok, err := store.Get(project.ID)
	if err != nil || !ok {
		t.Fatalf("reload partial build: ok=%v err=%v", ok, err)
	}
	clips := partial.Tracks[0].Clips
	if clips[0].Status != DialogueStatusReady || clips[1].Status != DialogueStatusFailed || clips[1].BuildError == "" || clips[2].Status != DialogueStatusStale {
		t.Fatalf("partial failure state = %+v", clips)
	}

	var retryPass []string
	retry := NewDialogueBuildManager(DialogueBuildManagerOptions{
		Store: store,
		Synthesize: func(_ context.Context, text, _ string) ([]byte, error) {
			retryPass = append(retryPass, text)
			return wav.SyntheticTone(16000), nil
		},
	})
	restarted, err := retry.Start(context.Background(), partial.ID, partial.Revision)
	if err != nil {
		t.Fatalf("retry build: %v", err)
	}
	waitDialogueBuild(t, retry, partial.ID, restarted.ID, DialogueBuildComplete)
	if !reflect.DeepEqual(retryPass, []string{"Two.", "Three."}) {
		t.Fatalf("retry synthesis = %v, want failed then stale only", retryPass)
	}
}

func TestWholeProjectEditsCannotForgeAndSpeechChangesDetachDialogueTake(t *testing.T) {
	store, project := buildTestProject(t, []TimelineClip{
		{ID: "line_one", Type: ClipTypeDialogue, Label: "One", Text: "One.", StartMS: 0, DurationMS: 1000},
	})
	project, err := store.BeginDialogueBuild(project.ID, "line_one")
	if err != nil {
		t.Fatalf("begin build: %v", err)
	}
	project, err = store.CompleteDialogueBuild(project.ID, "line_one", wav.SyntheticTone(16000))
	if err != nil {
		t.Fatalf("complete build: %v", err)
	}
	originalSource := project.Tracks[0].Clips[0].SourceID

	arranged := cloneTracks(project.Tracks)
	arranged[0].Clips[0].StartMS = 250
	arranged[0].Clips[0].SourceID = "take_forged"
	project, err = store.Update(project.ID, ProjectUpdate{Name: project.Name, Revision: project.Revision, Tracks: arranged})
	if err != nil {
		t.Fatalf("save arrangement edit: %v", err)
	}
	if clip := project.Tracks[0].Clips[0]; clip.SourceID != originalSource || clip.Status != DialogueStatusReady {
		t.Fatalf("client changed server-owned take: %+v", clip)
	}

	changed := cloneTracks(project.Tracks)
	changed[0].Clips[0].Text = "Changed words."
	changed[0].Clips[0].Label = "Changed words"
	project, err = store.Update(project.ID, ProjectUpdate{Name: project.Name, Revision: project.Revision, Tracks: changed})
	if err != nil {
		t.Fatalf("save spoken text edit: %v", err)
	}
	clip := project.Tracks[0].Clips[0]
	if clip.Status != DialogueStatusStale || clip.SourceID != "" || clip.SourceDurationMS != 0 || clip.SourceOutMS != 0 {
		t.Fatalf("speech change kept the old current take: %+v", clip)
	}
}

func TestMissingDialogueTakeIsReportedWhenProjectReopens(t *testing.T) {
	store, project := buildTestProject(t, []TimelineClip{
		{ID: "line_one", Type: ClipTypeDialogue, Label: "One", Text: "One.", StartMS: 0, DurationMS: 1000},
	})
	if _, err := store.BeginDialogueBuild(project.ID, "line_one"); err != nil {
		t.Fatalf("begin build: %v", err)
	}
	project, err := store.CompleteDialogueBuild(project.ID, "line_one", wav.SyntheticTone(16000))
	if err != nil {
		t.Fatalf("complete build: %v", err)
	}
	clip := project.Tracks[0].Clips[0]
	if err := os.Remove(filepath.Join(store.rootDir, project.ID, "takes", clip.SourceID+".wav")); err != nil {
		t.Fatalf("remove dialogue take: %v", err)
	}

	reopened, ok, err := store.Get(project.ID)
	if err != nil || !ok {
		t.Fatalf("reopen project: ok=%v err=%v", ok, err)
	}
	if got := reopened.Tracks[0].Clips[0].MediaError; got == "" {
		t.Fatalf("missing dialogue take was silent: %+v", reopened.Tracks[0].Clips[0])
	}
}

func buildTestProject(t *testing.T, clips []TimelineClip) (*Store, Project) {
	t.Helper()
	store := NewStoreWithOptions(t.TempDir(), StoreOptions{ResolveCharacterVoice: func(id string) (VoiceIdentity, bool, error) {
		return VoiceIdentity{CharacterVoiceID: id, ActorVoiceID: "actor_mara", Fingerprint: "voice-v1"}, true, nil
	}})
	project, err := store.Create("Build manager")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	project, err = store.Update(project.ID, ProjectUpdate{Name: project.Name, Revision: project.Revision, Tracks: []Track{
		{ID: "dialogue", Name: "Mara", Type: TrackTypeDialogue, Order: 0, CharacterVoiceID: "character_mara", Clips: clips},
	}})
	if err != nil {
		t.Fatalf("save project: %v", err)
	}
	return store, project
}

func waitDialogueBuild(t *testing.T, manager *DialogueBuildManager, projectID, buildID string, want DialogueBuildStatus) DialogueBuild {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		build, ok := manager.Status(projectID, buildID)
		if ok && build.Status == want {
			return build
		}
		time.Sleep(5 * time.Millisecond)
	}
	build, _ := manager.Status(projectID, buildID)
	t.Fatalf("timed out waiting for build %q: %+v", want, build)
	return DialogueBuild{}
}
