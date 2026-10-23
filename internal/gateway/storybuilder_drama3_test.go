package gateway

import (
	"context"
	"cpp-studio/internal/config"
	"cpp-studio/internal/engine"
	"cpp-studio/internal/lifecycle"
	"cpp-studio/internal/storybuilder"
	"cpp-studio/internal/voice"
	"cpp-studio/internal/wav"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoryBuilderDrama3UsesCharacterReferenceDirectionAndReservations(t *testing.T) {
	cfg := testConfig(map[string]config.EngineConfig{
		"audio":       {Command: "audio-helper", Mode: "subprocess"},
		"fish-drama3": {Command: "drama-helper", Mode: "subprocess"},
	})
	r := NewRouter(cfg, lifecycle.NewManager(cfg)).(*router)
	root := t.TempDir()
	r.voices = voice.NewStore(filepath.Join(root, "voices"))
	r.storyBuilderProjects = storybuilder.NewStoreWithOptions(filepath.Join(root, "projects"), storybuilder.StoreOptions{ResolveCharacterVoice: r.resolveStoryBuilderCharacterVoice})
	actor, err := r.voices.Save("Keeper", "Reference words.", wav.SyntheticTone(16000), false)
	if err != nil {
		t.Fatal(err)
	}
	character, err := r.voices.CreateCharacterVoice(actor.ID, "Keeper", "softly, with relief")
	if err != nil {
		t.Fatal(err)
	}
	project, err := r.storyBuilderProjects.Create("Drama3 dialogue")
	if err != nil {
		t.Fatal(err)
	}
	drama := "fish-drama3"
	payload, _ := json.Marshal(storybuilder.ProjectUpdate{Name: project.Name, Revision: project.Revision, DialogueEngine: &drama, Tracks: []storybuilder.Track{
		{ID: "keeper", Name: "Keeper", Type: storybuilder.TrackTypeDialogue, CharacterVoiceID: character.ID, Clips: []storybuilder.TimelineClip{
			{ID: "line_one", Type: storybuilder.ClipTypeDialogue, Label: "Welcome", Text: "[sighs] You made it.", DurationMS: 1000},
		}},
	}})
	saved := httptest.NewRecorder()
	r.ServeHTTP(saved, httptest.NewRequest(http.MethodPut, "/v1/story-builder-projects/"+project.ID, strings.NewReader(string(payload))))
	if saved.Code != http.StatusOK {
		t.Fatal(saved.Code, saved.Body.String())
	}
	if err = json.Unmarshal(saved.Body.Bytes(), &project); err != nil {
		t.Fatal(err)
	}
	fake := engine.NewFake()
	fake.Handle(drama, func(spec engine.Spec) (engine.Result, error) {
		args := spec.BuildArgs("", "take.wav")
		if args[1] != "[softly, with relief] [sighs] You made it." || spec.OverrideArgs["--reference-text"] != "Reference words." || spec.OverrideArgs["--voice-ref"] == "" {
			t.Errorf("wrong character mapping: %v %v", args, spec.OverrideArgs)
		}
		for _, id := range []string{"audio", drama} {
			if release, ok := r.reserveEngine(context.Background(), id); ok {
				release()
				t.Errorf("%s was not reserved", id)
			}
		}
		return engine.Result{Output: wav.SyntheticTone(16000)}, nil
	})
	r.engines = fake
	r.storyBuilderDialogueBuilds = r.newStoryBuilderDialogueBuildManager()
	release, ok := r.reserveEngine(context.Background(), drama)
	if !ok {
		t.Fatal("could not reserve Drama3")
	}
	if _, err = r.storyBuilderDialogueBuilds.Start(context.Background(), project.ID, project.Revision); err != storybuilder.ErrDialogueEngineBusy {
		t.Fatal(err)
	}
	release()
	// Failed Drama3 reservation must release the shared audio gate.
	release, ok = r.reserveEngine(context.Background(), "audio")
	if !ok {
		t.Fatal("leaked audio reservation")
	}
	release()
	started, err := r.storyBuilderDialogueBuilds.Start(context.Background(), project.ID, project.Revision)
	if err != nil {
		t.Fatal(err)
	}
	waitGatewayStoryBuilderBuild(t, r, project.ID, started.ID, storybuilder.DialogueBuildComplete)
	loaded, ok, err := r.storyBuilderProjects.Get(project.ID)
	if err != nil || !ok || loaded.DialogueEngine != drama || loaded.Tracks[0].Clips[0].Status != storybuilder.DialogueStatusReady {
		t.Fatalf("not durable: %+v %v", loaded, err)
	}
}
