package storybuilder

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"cpp-studio/internal/story"
)

func TestStoryImporterPreviewsMappingsAndPublishesIndependentProject(t *testing.T) {
	storyRoot := filepath.Join(t.TempDir(), "stories")
	projectRoot := filepath.Join(t.TempDir(), "projects")
	sourceTake := testWAV(16000)
	mismatchedTake := testWAV(8000)
	manifest := story.Manifest{
		ID: "story_import", Subject: "Import", Title: "Imported episode", Status: story.StatusComplete,
		Cast: []story.CastMember{
			{ID: "mara", DisplayName: "Mara", VoiceID: "actor_mara"},
			{ID: "jon", DisplayName: "Jon", VoiceID: "actor_jon"},
		},
		Script: []story.ScriptLine{
			{ID: "line-001", SpeakerID: "mara", Text: "First line.", GapBeforeMS: 100, GapAfterMS: 25, CurrentTake: "take-001", Takes: []story.Take{
				{ID: "take-001", VoiceID: "actor_mara", Text: "First line.", DurationMS: 1000},
			}},
			{ID: "line-002", SpeakerID: "jon", Text: "Second line.", GapBeforeMS: -50, CurrentTake: "take-001", Takes: []story.Take{
				{ID: "take-001", VoiceID: "different_actor", Text: "Second line.", DurationMS: 500},
			}},
			{ID: "line-003", SpeakerID: "mara", Text: "Needs a take."},
		},
		Audio: story.AudioRef{Format: "wav", URL: "/v1/stories/story_import/artifact/story.wav"},
	}
	sourceStore := story.NewStore(storyRoot)
	if err := sourceStore.Save(manifest, testWAV(16000)); err != nil {
		t.Fatalf("save retained Story: %v", err)
	}
	if _, err := sourceStore.SaveTake(manifest.ID, "line-001", "take-001", sourceTake); err != nil {
		t.Fatalf("save compatible take: %v", err)
	}
	if _, err := sourceStore.SaveTake(manifest.ID, "line-002", "take-001", mismatchedTake); err != nil {
		t.Fatalf("save mismatched take: %v", err)
	}
	manifestBefore, err := os.ReadFile(filepath.Join(storyRoot, manifest.ID, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	takeBefore, err := os.ReadFile(filepath.Join(storyRoot, manifest.ID, "lines", "line-001", "take-001.wav"))
	if err != nil {
		t.Fatal(err)
	}

	identities := map[string]VoiceIdentity{
		"character_mara": {CharacterVoiceID: "character_mara", ActorVoiceID: "actor_mara", Fingerprint: "mara-fingerprint"},
		"character_jon":  {CharacterVoiceID: "character_jon", ActorVoiceID: "actor_jon", Fingerprint: "jon-fingerprint"},
	}
	projects := NewStoreWithOptions(projectRoot, StoreOptions{ResolveCharacterVoice: func(id string) (VoiceIdentity, bool, error) {
		identity, ok := identities[id]
		return identity, ok, nil
	}})
	manager := story.NewManager(story.ManagerOptions{RootDir: storyRoot})
	importer := NewStoryImporter(manager, projects, func(actorID string) ([]VoiceIdentity, error) {
		switch actorID {
		case "actor_mara":
			return []VoiceIdentity{identities["character_mara"]}, nil
		case "actor_jon":
			return []VoiceIdentity{identities["character_jon"], {CharacterVoiceID: "character_jon_alt", ActorVoiceID: "actor_jon"}}, nil
		default:
			return nil, nil
		}
	})

	preview, err := importer.Preview(manifest.ID)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(preview.Speakers) != 2 || preview.Speakers[0].ID != "mara" ||
		preview.Speakers[0].SuggestedCharacterVoiceID != "character_mara" ||
		preview.Speakers[1].SuggestedCharacterVoiceID != "" {
		t.Fatalf("preview = %+v", preview)
	}
	if _, err := importer.Import(manifest.ID, StoryImportRequest{Mappings: []StorySpeakerMapping{
		{SpeakerID: "mara", CharacterVoiceID: "character_mara"},
	}}); !errors.Is(err, ErrStoryMappingRequired) {
		t.Fatalf("incomplete mapping error = %v", err)
	}

	project, err := importer.Import(manifest.ID, StoryImportRequest{Mappings: []StorySpeakerMapping{
		{SpeakerID: "mara", CharacterVoiceID: "character_mara"},
		{SpeakerID: "jon", CharacterVoiceID: "character_jon"},
	}})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if project.ID == "" || project.Name != manifest.Title || len(project.Tracks) != 2 ||
		project.Tracks[0].Name != "Mara" || project.Tracks[1].Name != "Jon" {
		t.Fatalf("project identity/tracks = %+v", project)
	}
	mara := project.Tracks[0].Clips
	jon := project.Tracks[1].Clips
	if len(mara) != 2 || len(jon) != 1 {
		t.Fatalf("speaker clips: mara=%+v jon=%+v", mara, jon)
	}
	ready := mara[0]
	if ready.Status != DialogueStatusReady || ready.StartMS != 450 || ready.DurationMS != 1000 ||
		ready.SourceID == "" || ready.SourceStoryID != manifest.ID || ready.SourceStoryLineID != "line-001" || ready.SourceStoryTakeID != "take-001" {
		t.Fatalf("ready clip = %+v", ready)
	}
	if jon[0].Status != DialogueStatusStale || jon[0].SourceID != "" || jon[0].StartMS != 1775 || jon[0].DurationMS != 500 {
		t.Fatalf("mismatched clip = %+v", jon[0])
	}
	if mara[1].Status != DialogueStatusStale || mara[1].Text != "Needs a take." || mara[1].StartMS != 2625 || mara[1].DurationMS != importedStaleDialogueDurationMS {
		t.Fatalf("missing clip = %+v", mara[1])
	}
	copied, err := os.ReadFile(filepath.Join(projectRoot, project.ID, "takes", ready.SourceID+".wav"))
	if err != nil || !bytes.Equal(copied, sourceTake) {
		t.Fatalf("copied ready take: err=%v equal=%v", err, bytes.Equal(copied, sourceTake))
	}

	manifestAfter, _ := os.ReadFile(filepath.Join(storyRoot, manifest.ID, "manifest.json"))
	takeAfter, _ := os.ReadFile(filepath.Join(storyRoot, manifest.ID, "lines", "line-001", "take-001.wav"))
	if !bytes.Equal(manifestBefore, manifestAfter) || !bytes.Equal(takeBefore, takeAfter) {
		t.Fatal("retained Story changed during import")
	}

	project.Tracks[0].Clips[0].SourceStoryID = "forged_story"
	updated, err := projects.Update(project.ID, ProjectUpdate{Name: project.Name, Revision: project.Revision, TimelineDurationMS: project.TimelineDurationMS, Tracks: project.Tracks})
	if err != nil {
		t.Fatalf("save imported project: %v", err)
	}
	if updated.Tracks[0].Clips[0].SourceStoryID != manifest.ID {
		t.Fatalf("client changed source provenance: %+v", updated.Tracks[0].Clips[0])
	}
}

func TestStoryImporterDoesNotPublishPartialProjectWhenManifestWriteFails(t *testing.T) {
	storyRoot := filepath.Join(t.TempDir(), "stories")
	projectRoot := filepath.Join(t.TempDir(), "projects")
	manifest := story.Manifest{
		ID: "story_atomic", Title: "Atomic", Status: story.StatusComplete,
		Cast:   []story.CastMember{{ID: "speaker", DisplayName: "Speaker", VoiceID: "actor"}},
		Script: []story.ScriptLine{{ID: "line-001", SpeakerID: "speaker", Text: "Hello."}},
	}
	if err := story.NewStore(storyRoot).Save(manifest, testWAV(16000)); err != nil {
		t.Fatal(err)
	}
	identity := VoiceIdentity{CharacterVoiceID: "character", ActorVoiceID: "actor", Fingerprint: "fingerprint"}
	projects := NewStoreWithOptions(projectRoot, StoreOptions{
		ResolveCharacterVoice: func(string) (VoiceIdentity, bool, error) { return identity, true, nil },
		WriteFileAtomic:       func(string, []byte) error { return errors.New("injected manifest failure") },
	})
	importer := NewStoryImporter(story.NewManager(story.ManagerOptions{RootDir: storyRoot}), projects, nil)
	_, err := importer.Import(manifest.ID, StoryImportRequest{Mappings: []StorySpeakerMapping{{SpeakerID: "speaker", CharacterVoiceID: "character"}}})
	if err == nil {
		t.Fatal("import succeeded despite manifest failure")
	}
	entries, readErr := os.ReadDir(projectRoot)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("partial project entries = %v err=%v", entries, readErr)
	}
}
