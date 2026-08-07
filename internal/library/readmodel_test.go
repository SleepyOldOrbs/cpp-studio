package library

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"cpp-studio/internal/audiobook"
	"cpp-studio/internal/story"
	"cpp-studio/internal/storybuilder"
	"cpp-studio/internal/voice"
)

func TestReadModelAggregatesPurposeBuiltStores(t *testing.T) {
	model := NewReadModel(readModelFixture())

	response, err := model.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 4 {
		t.Fatalf("legacy items = %d, want 4", len(response.Items))
	}
	wantKinds := map[string]int{
		KindActorVoice: 1, KindReusableAudio: 3, KindSavedImage: 1,
		KindStory: 1, KindStoryBuilderProject: 1, KindAudiobook: 1,
		KindRenderRevision: 2, KindMixedMaster: 1, KindExport: 3,
	}
	gotKinds := make(map[string]int)
	for _, entry := range response.Entries {
		gotKinds[entry.Kind]++
		if entry.ID == "" || entry.Name == "" || entry.CreatedAt.IsZero() {
			t.Fatalf("entry lacks stable identity/timestamp: %#v", entry)
		}
	}
	if !reflect.DeepEqual(gotKinds, wantKinds) {
		t.Fatalf("kinds = %#v, want %#v", gotKinds, wantKinds)
	}

	actor := findEntry(t, response.Entries, KindActorVoice, "voice_keeper")
	if len(actor.Children) != 2 || actor.Children[0].Relationship == nil || actor.Children[0].Relationship.Name != "Morgan" {
		t.Fatalf("Actor Voice children = %#v", actor.Children)
	}
	if actor.LaunchAction == nil || actor.LaunchAction.URL != "/demo/#voice-cloning" || actor.DeleteAction.URL != "/v1/voices/voice_keeper" {
		t.Fatalf("Actor Voice actions = %#v / %#v", actor.LaunchAction, actor.DeleteAction)
	}
	project := findEntry(t, response.Entries, KindStoryBuilderProject, "project_1")
	if project.LaunchAction == nil || project.LaunchAction.URL != "/demo/story-builder.html?project=project_1" {
		t.Fatalf("project launch = %#v", project.LaunchAction)
	}
	master := findEntry(t, response.Entries, KindMixedMaster, "project_1/render/2")
	if master.Relationship == nil || master.Relationship.ID != project.ID || master.ArtifactAction.ContentType != "audio/wav" {
		t.Fatalf("master relationship/action = %#v / %#v", master.Relationship, master.ArtifactAction)
	}
	exported := findEntry(t, response.Entries, KindExport, "project_1/render/2/export/flac")
	if exported.Relationship == nil || exported.Relationship.Kind != KindMixedMaster || exported.ArtifactAction.ContentType != "audio/flac" {
		t.Fatalf("export relationship/action = %#v / %#v", exported.Relationship, exported.ArtifactAction)
	}
	storyRender := findEntry(t, response.Entries, KindRenderRevision, "story_1/render/1")
	if storyRender.Relationship == nil || storyRender.Relationship.Kind != KindStory {
		t.Fatalf("Story render relationship = %#v", storyRender.Relationship)
	}
	bookRender := findEntry(t, response.Entries, KindRenderRevision, "book_1/render/render-0001")
	if bookRender.Relationship == nil || bookRender.Relationship.Kind != KindAudiobook {
		t.Fatalf("Audiobook render relationship = %#v", bookRender.Relationship)
	}
}

func TestReadModelUsesInterruptedStoryLifecycle(t *testing.T) {
	entry := storyEntries(story.Summary{
		ID: "story_interrupted", Title: "Interrupted", Status: story.StatusInterrupted,
		CreatedAt: time.Date(2026, 8, 7, 8, 0, 0, 0, time.UTC),
	})[0]
	if entry.DeleteAction == nil || entry.DeleteAction.Label != "Discard" || entry.DeleteAction.Method != "POST" || entry.DeleteAction.URL != "/v1/stories/story_interrupted/discard" {
		t.Fatalf("interrupted Story action = %#v", entry.DeleteAction)
	}
}

func TestStoryExportIdentityDoesNotShiftWhenEarlierRenderGainsExport(t *testing.T) {
	created := time.Date(2026, 8, 7, 8, 0, 0, 0, time.UTC)
	mp3 := story.Export{Format: "mp3", CreatedAt: created, URL: "/v1/stories/story_1/artifact/renders/render-002.mp3"}
	before := storyEntries(story.Summary{ID: "story_1", Title: "Story", CreatedAt: created, Renders: []story.RenderSummary{
		{Revision: 1, CreatedAt: created, URL: "/v1/stories/story_1/artifact/renders/render-001.wav"},
		{Revision: 2, CreatedAt: created, URL: "/v1/stories/story_1/artifact/renders/render-002.wav", Exports: []story.Export{mp3}},
	}})
	after := storyEntries(story.Summary{ID: "story_1", Title: "Story", CreatedAt: created, Renders: []story.RenderSummary{
		{Revision: 1, CreatedAt: created, URL: "/v1/stories/story_1/artifact/renders/render-001.wav", Exports: []story.Export{{Format: "opus", CreatedAt: created, URL: "/v1/stories/story_1/artifact/renders/render-001.opus"}}},
		{Revision: 2, CreatedAt: created, URL: "/v1/stories/story_1/artifact/renders/render-002.wav", Exports: []story.Export{mp3}},
	}})
	wantID := "story_1/render/2/export/mp3"
	if findEntry(t, before, KindExport, wantID).ID != findEntry(t, after, KindExport, wantID).ID {
		t.Fatal("later export identity shifted when an earlier render gained an export")
	}
}

func TestReadModelSearchesRelationshipsDirectionsAndAudioRoles(t *testing.T) {
	model := NewReadModel(readModelFixture())

	direction, err := model.List("whisper")
	if err != nil {
		t.Fatal(err)
	}
	if len(direction.Entries) != 1 || direction.Entries[0].Kind != KindActorVoice || len(direction.Entries[0].Children) != 1 || direction.Entries[0].Children[0].Name != "Keeper" {
		t.Fatalf("direction search = %#v", direction.Entries)
	}
	parent, err := model.List("Morgan")
	if err != nil {
		t.Fatal(err)
	}
	if len(parent.Entries) != 1 || len(parent.Entries[0].Children) != 2 {
		t.Fatalf("parent search = %#v", parent.Entries)
	}
	music, err := model.List("music")
	if err != nil {
		t.Fatal(err)
	}
	if len(music.Entries) != 1 || music.Entries[0].Subtype != "music" || music.Entries[0].Name != "Harbour theme" {
		t.Fatalf("media role search = %#v", music.Entries)
	}
}

func TestReadModelEmptyReloadAndSourceFailure(t *testing.T) {
	empty := func() ReadModelSources {
		return ReadModelSources{
			Items:           func() ([]Item, error) { return nil, nil },
			ActorVoices:     func() ([]voice.Clone, error) { return nil, nil },
			CharacterVoices: func(string) ([]voice.CharacterVoice, error) { return nil, nil },
			Stories:         func() ([]story.Summary, error) { return nil, nil },
			Projects:        func() ([]storybuilder.Project, error) { return nil, nil },
			Audiobooks:      func() ([]audiobook.Manifest, error) { return nil, nil },
		}
	}
	response, err := NewReadModel(empty()).List("")
	if err != nil {
		t.Fatal(err)
	}
	if response.Entries == nil || response.Items == nil || len(response.Entries) != 0 || len(response.Items) != 0 {
		t.Fatalf("empty response = %#v", response)
	}

	model := NewReadModel(readModelFixture())
	first, err := model.List("")
	if err != nil {
		t.Fatal(err)
	}
	second, err := model.List("")
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("reload changed response:\n%s\n%s", firstJSON, secondJSON)
	}

	broken := empty()
	broken.Projects = func() ([]storybuilder.Project, error) { return nil, errors.New("offline") }
	_, err = NewReadModel(broken).List("")
	if err == nil || !strings.Contains(err.Error(), "list Story Builder Projects: offline") {
		t.Fatalf("source error = %v", err)
	}
}

func findEntry(t *testing.T, entries []Entry, kind, id string) Entry {
	t.Helper()
	for _, entry := range entries {
		if entry.Kind == kind && entry.ID == id {
			return entry
		}
	}
	t.Fatalf("missing %s %s", kind, id)
	return Entry{}
}

func readModelFixture() ReadModelSources {
	created := time.Date(2026, 8, 7, 8, 0, 0, 0, time.UTC)
	updated := created.Add(time.Hour)
	return ReadModelSources{
		Items: func() ([]Item, error) {
			return []Item{
				{ID: "audio_sfx", Kind: "audio", Name: "Door slam", MediaRole: MediaRoleSFX, DurationMS: 500, CreatedAt: created},
				{ID: "audio_music", Kind: "audio", Name: "Harbour theme", MediaRole: MediaRoleMusic, DurationMS: 2500, CreatedAt: created},
				{ID: "audio_utility", Kind: "audio", Name: "Room tone", MediaRole: MediaRoleUtility, Meta: map[string]string{"role": "noise bed"}, CreatedAt: created},
				{ID: "image_1", Kind: "image", Name: "Cover", CreatedAt: created},
			}, nil
		},
		ActorVoices: func() ([]voice.Clone, error) {
			return []voice.Clone{{ID: "voice_keeper", Name: "Morgan", Transcript: "Keep the lamp lit.", CreatedAt: created}}, nil
		},
		CharacterVoices: func(actorID string) ([]voice.CharacterVoice, error) {
			return []voice.CharacterVoice{
				{ID: "character_keeper", ActorVoiceID: actorID, Name: "Keeper", Direction: "elderly whisper", CreatedAt: created, UpdatedAt: updated, Preview: &voice.CharacterPreview{SampleText: "Keep watch."}},
				{ID: "character_captain", ActorVoiceID: actorID, Name: "Captain", Direction: "brisk command", CreatedAt: created, UpdatedAt: updated},
			}, nil
		},
		Stories: func() ([]story.Summary, error) {
			return []story.Summary{{ID: "story_1", Title: "The Signal", Subject: "A harbour signal", Mode: "grounded", Status: story.StatusComplete, CreatedAt: created, ArtifactURL: "/v1/stories/story_1/artifact", Exports: []story.Export{{Format: "mp3", Bitrate: "192k", CreatedAt: updated, URL: "/v1/stories/story_1/renders/1/export.mp3", RenderRevision: 1}}, Renders: []story.RenderSummary{{Revision: 1, CreatedAt: updated, DurationSeconds: 5, URL: "/v1/stories/story_1/artifact/renders/render-001.wav", Exports: []story.Export{{Format: "mp3", Bitrate: "192k", CreatedAt: updated, URL: "/v1/stories/story_1/renders/1/export.mp3", RenderRevision: 1}}}}}}, nil
		},
		Projects: func() ([]storybuilder.Project, error) {
			return []storybuilder.Project{{ID: "project_1", Name: "Signal edit", CreatedAt: created, UpdatedAt: updated, Tracks: []storybuilder.Track{{ID: "track_1", Name: "Dialogue"}}, Renders: []storybuilder.RenderRevision{{Revision: 2, CreatedAt: updated, DurationMS: 5000, URL: "/v1/story-builder-projects/project_1/renders/2/artifact.wav", Exports: []storybuilder.RenderExport{{Format: "flac", CreatedAt: updated, URL: "/v1/story-builder-projects/project_1/renders/2/export.flac"}, {Format: "mp3", Bitrate: "192k", CreatedAt: updated, URL: "/v1/story-builder-projects/project_1/renders/2/export.mp3"}}}}}}, nil
		},
		Audiobooks: func() ([]audiobook.Manifest, error) {
			return []audiobook.Manifest{{ID: "book_1", Title: "Harbour Book", EngineID: "audio", Direction: "warm", DurationSeconds: 60, CreatedAt: created, ArtifactURL: "/v1/audiobooks/book_1/artifact", Status: audiobook.ProductionStatusComplete, RenderRevisions: []audiobook.RenderRevision{{ID: "render-0001", ArtifactURL: "/v1/audiobooks/book_1/artifact/book.render-0001.wav", CreatedAt: updated, DurationSeconds: 60}}}}, nil
		},
	}
}
