package storybuilder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cpp-studio/internal/wav"
)

func TestPlaceLibraryAudioCopiesOnceAndSurvivesLibraryDeletion(t *testing.T) {
	root := t.TempDir()
	source := LibraryAudio{ID: "lib_door", Name: "Door slam", MediaRole: MediaRoleSFX, Data: testWAV(16000)}
	available := true
	resolver := func(id string) (LibraryAudio, bool, error) {
		if !available || id != source.ID {
			return LibraryAudio{}, false, nil
		}
		return source, true, nil
	}
	store := NewStoreWithOptions(root, StoreOptions{ResolveLibraryAudio: resolver})
	created, err := store.Create("Durable foley")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	created, err = store.Update(created.ID, ProjectUpdate{Name: created.Name, Revision: created.Revision, Tracks: []Track{
		{ID: "foley", Name: "Foley", Type: TrackTypeSFX, Order: 0},
	}})
	if err != nil {
		t.Fatalf("add track: %v", err)
	}

	placed, err := store.PlaceLibraryAudio(created.ID, LibraryAudioPlacement{
		Revision: created.Revision, TrackID: "foley", LibraryItemID: source.ID, StartMS: 250,
	})
	if err != nil {
		t.Fatalf("place audio: %v", err)
	}
	clip := placed.Tracks[0].Clips[0]
	if clip.Type != ClipTypeSFX || clip.SourceLibraryItemID != source.ID || clip.SourceLibraryName != source.Name ||
		clip.SourceMediaRole != MediaRoleSFX || clip.StartMS != 250 || clip.DurationMS != 1000 || clip.MediaError != "" {
		t.Fatalf("placed clip = %+v", clip)
	}
	mediaPath := filepath.Join(root, created.ID, "media", clip.SourceID+".wav")
	if data, err := os.ReadFile(mediaPath); err != nil || !bytes.Equal(data, source.Data) {
		t.Fatalf("project media copy: err=%v equal=%v", err, bytes.Equal(data, source.Data))
	}

	placed, err = store.PlaceLibraryAudio(created.ID, LibraryAudioPlacement{
		Revision: placed.Revision, TrackID: "foley", LibraryItemID: source.ID, StartMS: 2000,
	})
	if err != nil {
		t.Fatalf("reuse audio: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, created.ID, "media"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("reused media entries = %v err=%v, want one", entries, err)
	}

	available = false
	reopened, ok, err := NewStore(root).Get(created.ID)
	if err != nil || !ok || len(reopened.Tracks[0].Clips) != 2 {
		t.Fatalf("reopen after Library deletion: %+v ok=%v err=%v", reopened, ok, err)
	}
	if reopened.Tracks[0].Clips[0].MediaError != "" {
		t.Fatalf("Library deletion broke project copy: %+v", reopened.Tracks[0].Clips[0])
	}

	if err := os.Remove(mediaPath); err != nil {
		t.Fatalf("remove project media: %v", err)
	}
	broken, ok, err := NewStore(root).Get(created.ID)
	if err != nil || !ok {
		t.Fatalf("reopen missing media: ok=%v err=%v", ok, err)
	}
	for _, affected := range broken.Tracks[0].Clips {
		if affected.MediaError == "" {
			t.Fatalf("missing project media was silent: %+v", affected)
		}
	}
}

func TestPlaceLibraryAudioRejectsIncompatibleOrUntrustedInputWithoutMutation(t *testing.T) {
	root := t.TempDir()
	store := NewStoreWithOptions(root, StoreOptions{ResolveLibraryAudio: func(id string) (LibraryAudio, bool, error) {
		if id != "lib_score" {
			return LibraryAudio{}, false, nil
		}
		return LibraryAudio{ID: id, Name: "Low strings", MediaRole: MediaRoleMusic, Data: testWAV(16000)}, true, nil
	}})
	created, err := store.Create("Typed placement")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	created, err = store.Update(created.ID, ProjectUpdate{Name: created.Name, Revision: created.Revision, Tracks: []Track{
		{ID: "foley", Name: "Foley", Type: TrackTypeSFX, Order: 0},
	}})
	if err != nil {
		t.Fatalf("add track: %v", err)
	}
	for _, itemID := range []string{"lib_score", `..\\outside.wav`} {
		if _, err := store.PlaceLibraryAudio(created.ID, LibraryAudioPlacement{
			Revision: created.Revision, TrackID: "foley", LibraryItemID: itemID, StartMS: 0,
		}); err == nil {
			t.Fatalf("placement %q succeeded", itemID)
		}
	}
	loaded, ok, err := store.Get(created.ID)
	if err != nil || !ok || loaded.Revision != created.Revision || len(loaded.Tracks[0].Clips) != 0 {
		t.Fatalf("rejected placement mutated project: %+v ok=%v err=%v", loaded, ok, err)
	}
}

func testWAV(samples int) []byte {
	return wav.SyntheticTone(samples)
}

func TestUserCanArrangeTypedTracksAndSilenceClips(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	created, err := store.Create("The Lantern at Crow Point")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	tracks := []Track{
		{ID: "track_mara", Name: "Mara", Type: TrackTypeDialogue, Order: 0},
		{ID: "track_foley", Name: "Foley", Type: TrackTypeSFX, Order: 1, Muted: true, Clips: []TimelineClip{
			{ID: "clip_pause", Type: ClipTypeSilence, Label: "Hold for thunder", StartMS: 1250, DurationMS: 800},
		}},
		{ID: "track_score", Name: "Score", Type: TrackTypeMusic, Order: 2},
	}
	saved, err := store.Update(created.ID, ProjectUpdate{Name: created.Name, Revision: created.Revision, Tracks: tracks})
	if err != nil {
		t.Fatalf("arrange tracks: %v", err)
	}
	if len(saved.Tracks) != 3 || saved.Tracks[1].ID != "track_foley" || !saved.Tracks[1].Muted ||
		len(saved.Tracks[1].Clips) != 1 || saved.Tracks[1].Clips[0].DurationMS != 800 {
		t.Fatalf("saved timeline drifted: %+v", saved.Tracks)
	}

	reopened, ok, err := NewStore(root).Get(created.ID)
	if err != nil || !ok {
		t.Fatalf("reopen project: ok=%v err=%v", ok, err)
	}
	if len(reopened.Tracks) != 3 || reopened.Tracks[0].Type != TrackTypeDialogue || reopened.Tracks[2].Type != TrackTypeMusic {
		t.Fatalf("reopened tracks drifted: %+v", reopened.Tracks)
	}
	entries, err := os.ReadDir(filepath.Join(root, created.ID))
	if err != nil {
		t.Fatalf("list project files: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != manifestName {
		t.Fatalf("silence clip wrote media bytes: %+v", entries)
	}
}

func TestLegacyProjectDerivesTimelineDurationFromExistingClips(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	created, err := store.Create("Legacy long project")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	created.Tracks = []Track{{ID: "foley", Name: "Foley", Type: TrackTypeSFX, Order: 0, Clips: []TimelineClip{
		{ID: "late_clip", Type: ClipTypeSilence, Label: "Late clip", StartMS: 45000, DurationMS: 1000},
	}}}
	data, err := json.Marshal(created)
	if err != nil {
		t.Fatalf("encode legacy project: %v", err)
	}
	var legacy map[string]any
	if err := json.Unmarshal(data, &legacy); err != nil {
		t.Fatalf("decode legacy project: %v", err)
	}
	delete(legacy, "timeline_duration_ms")
	data, err = json.Marshal(legacy)
	if err != nil {
		t.Fatalf("encode legacy manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, created.ID, manifestName), data, 0o644); err != nil {
		t.Fatalf("write legacy manifest: %v", err)
	}

	reopened, ok, err := NewStore(root).Get(created.ID)
	if err != nil || !ok {
		t.Fatalf("reopen legacy project: ok=%v err=%v", ok, err)
	}
	if reopened.TimelineDurationMS != 46000 {
		t.Fatalf("legacy timeline duration = %d, want 46000", reopened.TimelineDurationMS)
	}
	if _, err := NewStore(root).Update(created.ID, ProjectUpdate{
		Name: reopened.Name, Revision: reopened.Revision, Tracks: reopened.Tracks,
	}); err != nil {
		t.Fatalf("save reopened legacy project: %v", err)
	}
}

func TestInvalidTimelineUpdateIsRejectedWithoutCorruptingProject(t *testing.T) {
	tests := []struct {
		name   string
		tracks []Track
	}{
		{name: "unknown track type", tracks: []Track{{ID: "track_1", Name: "Odd", Type: "video", Order: 0}}},
		{name: "duplicate track ids", tracks: []Track{
			{ID: "same", Name: "One", Type: TrackTypeSFX, Order: 0},
			{ID: "same", Name: "Two", Type: TrackTypeMusic, Order: 1},
		}},
		{name: "negative start", tracks: []Track{{ID: "track_1", Name: "Foley", Type: TrackTypeSFX, Order: 0, Clips: []TimelineClip{
			{ID: "clip_1", Type: ClipTypeSilence, Label: "Pause", StartMS: -1, DurationMS: 100},
		}}}},
		{name: "zero duration", tracks: []Track{{ID: "track_1", Name: "Score", Type: TrackTypeMusic, Order: 0, Clips: []TimelineClip{
			{ID: "clip_1", Type: ClipTypeSilence, Label: "Pause", StartMS: 0, DurationMS: 0},
		}}}},
		{name: "dialogue without character voice", tracks: []Track{{ID: "track_1", Name: "Mara", Type: TrackTypeDialogue, Order: 0, Clips: []TimelineClip{
			{ID: "clip_1", Type: ClipTypeDialogue, Label: "Hello", StartMS: 0, DurationMS: 100},
		}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			store := NewStore(root)
			created, err := store.Create("Safe project")
			if err != nil {
				t.Fatalf("create project: %v", err)
			}
			if _, err := store.Update(created.ID, ProjectUpdate{Name: created.Name, Revision: created.Revision, Tracks: tt.tracks}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("update error = %v, want ErrInvalid", err)
			}
			loaded, ok, err := NewStore(root).Get(created.ID)
			if err != nil || !ok || loaded.Revision != created.Revision || len(loaded.Tracks) != 0 {
				t.Fatalf("invalid update changed durable project: %+v ok=%v err=%v", loaded, ok, err)
			}
		})
	}
}

func TestUserCanCreateRenameReloadAndDeleteProject(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)

	created, err := store.Create("The Lantern at Crow Point")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if created.ID == "" || created.Name != "The Lantern at Crow Point" || created.Revision != 1 {
		t.Fatalf("unexpected created project: %+v", created)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("created project needs timestamps: %+v", created)
	}

	projects, err := store.List()
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	if len(projects) != 1 || projects[0].ID != created.ID {
		t.Fatalf("unexpected project list: %+v", projects)
	}

	renamed, err := store.Update(created.ID, ProjectUpdate{Name: "Lantern — final edit", Revision: created.Revision})
	if err != nil {
		t.Fatalf("rename project: %v", err)
	}
	if renamed.Name != "Lantern — final edit" || renamed.Revision != 2 || !renamed.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("unexpected renamed project: %+v", renamed)
	}

	if _, err := store.Update(created.ID, ProjectUpdate{Name: "stale browser overwrite", Revision: created.Revision}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error = %v, want ErrConflict", err)
	}

	restarted := NewStore(root)
	loaded, ok, err := restarted.Get(created.ID)
	if err != nil || !ok {
		t.Fatalf("reload project: ok=%v err=%v", ok, err)
	}
	if loaded.Name != renamed.Name || loaded.Revision != renamed.Revision || loaded.ID != created.ID ||
		!loaded.CreatedAt.Equal(renamed.CreatedAt) || !loaded.UpdatedAt.Equal(renamed.UpdatedAt) {
		t.Fatalf("reloaded project drifted: %+v", loaded)
	}
	sibling, err := restarted.Create("A separate production")
	if err != nil {
		t.Fatalf("create sibling project: %v", err)
	}
	sentinelPath := filepath.Join(root, "not-a-project.txt")
	if err := os.WriteFile(sentinelPath, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write root sentinel: %v", err)
	}

	if err := restarted.Delete(created.ID); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	if _, ok, err := restarted.Get(created.ID); err != nil || ok {
		t.Fatalf("deleted project still loads: ok=%v err=%v", ok, err)
	}
	if err := restarted.Delete(created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing project error = %v, want ErrNotFound", err)
	}
	if _, ok, err := restarted.Get(sibling.ID); err != nil || !ok {
		t.Fatalf("delete removed sibling project: ok=%v err=%v", ok, err)
	}
	if data, err := os.ReadFile(sentinelPath); err != nil || string(data) != "keep" {
		t.Fatalf("delete changed root sentinel: data=%q err=%v", data, err)
	}
}

func TestConcurrentSavesOfOneRevisionAllowOneWinner(t *testing.T) {
	root := t.TempDir()
	created, err := NewStore(root).Create("Shared project")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	enteredWrite := make(chan struct{}, 2)
	releaseWrite := make(chan struct{}, 2)
	store := NewStoreWithOptions(root, StoreOptions{
		WriteFileAtomic: func(path string, data []byte) error {
			enteredWrite <- struct{}{}
			<-releaseWrite
			return writeFileAtomic(path, data)
		},
	})
	results := make(chan error, 2)
	go func() {
		_, err := store.Update(created.ID, ProjectUpdate{Name: "first editor", Revision: created.Revision})
		results <- err
	}()
	<-enteredWrite
	go func() {
		_, err := store.Update(created.ID, ProjectUpdate{Name: "second editor", Revision: created.Revision})
		results <- err
	}()

	select {
	case <-enteredWrite:
		releaseWrite <- struct{}{}
		releaseWrite <- struct{}{}
		<-results
		<-results
		t.Fatal("two clients reached the write for the same revision")
	case <-time.After(50 * time.Millisecond):
		releaseWrite <- struct{}{}
	}

	errA, errB := <-results, <-results
	conflicts := 0
	for _, err := range []error{errA, errB} {
		if errors.Is(err, ErrConflict) {
			conflicts++
		} else if err != nil {
			t.Fatalf("unexpected save error: %v", err)
		}
	}
	if conflicts != 1 {
		t.Fatalf("conflicts = %d, want 1 (errors %v, %v)", conflicts, errA, errB)
	}
}

func TestFailedUpdateLeavesLastValidProjectReadable(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	created, err := store.Create("Safe version")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	writes := 0
	failing := NewStoreWithOptions(root, StoreOptions{
		WriteFileAtomic: func(path string, data []byte) error {
			writes++
			if writes == 1 {
				return fmt.Errorf("injected disk failure")
			}
			return writeFileAtomic(path, data)
		},
	})
	if _, err := failing.Update(created.ID, ProjectUpdate{Name: "unsafe version", Revision: created.Revision}); err == nil {
		t.Fatal("expected injected update failure")
	}

	loaded, ok, err := NewStore(root).Get(created.ID)
	if err != nil || !ok {
		t.Fatalf("load after failed update: ok=%v err=%v", ok, err)
	}
	if loaded.Name != "Safe version" || loaded.Revision != 1 {
		t.Fatalf("failed update changed durable project: %+v", loaded)
	}

	retried, err := failing.Update(created.ID, ProjectUpdate{Name: "retry version", Revision: created.Revision})
	if err != nil {
		t.Fatalf("retry update: %v", err)
	}
	if retried.Name != "retry version" || retried.Revision != 2 {
		t.Fatalf("unexpected retried project: %+v", retried)
	}
	projects, err := failing.List()
	if err != nil {
		t.Fatalf("list after retry: %v", err)
	}
	if len(projects) != 1 || projects[0].ID != created.ID {
		t.Fatalf("retry duplicated project: %+v", projects)
	}
}

func TestProjectValidationAndTraversalAreRejected(t *testing.T) {
	store := NewStore(t.TempDir())
	for _, name := range []string{"", "   ", string(make([]byte, MaxNameLength+1))} {
		if _, err := store.Create(name); !errors.Is(err, ErrInvalid) {
			t.Fatalf("create name %q error = %v, want ErrInvalid", name, err)
		}
	}

	for _, id := range []string{"", "../../outside", `..\outside`, "a/b", "UPPER", "sneaky.."} {
		if _, ok, err := store.Get(id); err != nil || ok {
			t.Fatalf("Get(%q) = ok %v, err %v", id, ok, err)
		}
		if _, err := store.Update(id, ProjectUpdate{Name: "name", Revision: 1}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Update(%q) error = %v, want ErrNotFound", id, err)
		}
		if err := store.Delete(id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Delete(%q) error = %v, want ErrNotFound", id, err)
		}
	}
}
