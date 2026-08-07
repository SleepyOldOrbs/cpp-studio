package storybuilder

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"cpp-studio/internal/story"
	"cpp-studio/internal/wav"
)

func TestRenderMixesSavedArrangementAndPublishesImmutableRevisions(t *testing.T) {
	root := t.TempDir()
	tone := steppedRenderTestWAV()
	assets := map[string]LibraryAudio{
		"lib_a":     {ID: "lib_a", Name: "A", MediaRole: MediaRoleSFX, Data: tone},
		"lib_b":     {ID: "lib_b", Name: "B", MediaRole: MediaRoleMusic, Data: tone},
		"lib_muted": {ID: "lib_muted", Name: "Muted", MediaRole: MediaRoleSFX, Data: tone},
	}
	store := NewStoreWithOptions(root, StoreOptions{
		ResolveLibraryAudio: func(id string) (LibraryAudio, bool, error) { asset, ok := assets[id]; return asset, ok, nil },
		MasterRender: func(_ context.Context, audio []byte) ([]byte, *story.Master, error) {
			return audio, &story.Master{GainDB: 2, TargetPeakDBTP: story.TargetTruePeakDBTP}, nil
		},
	})
	project, err := store.Create("Mixed master")
	if err != nil {
		t.Fatal(err)
	}
	project, err = store.Update(project.ID, ProjectUpdate{Name: project.Name, Revision: project.Revision, TimelineDurationMS: 3000, Tracks: []Track{
		{ID: "foley", Name: "Foley", Type: TrackTypeSFX, Order: 0},
		{ID: "score", Name: "Score", Type: TrackTypeMusic, Order: 1},
		{ID: "muted", Name: "Muted", Type: TrackTypeSFX, Order: 2, Muted: true},
		{ID: "silence", Name: "Silence", Type: TrackTypeSFX, Order: 3, Clips: []TimelineClip{{ID: "hold", Type: ClipTypeSilence, Label: "Hold", DurationMS: 3000}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, placement := range []LibraryAudioPlacement{
		{TrackID: "foley", LibraryItemID: "lib_a"},
		{TrackID: "score", LibraryItemID: "lib_b"},
		{TrackID: "muted", LibraryItemID: "lib_muted"},
	} {
		placement.Revision = project.Revision
		project, err = store.PlaceLibraryAudio(project.ID, placement)
		if err != nil {
			t.Fatalf("place %s: %v", placement.LibraryItemID, err)
		}
	}
	project.Tracks[0].Clips[0].StartMS = 500
	project.Tracks[0].Clips[0].SourceInMS = 1000
	project.Tracks[0].Clips[0].SourceOutMS = 2000
	project.Tracks[0].Clips[0].DurationMS = 1000
	project.Tracks[1].Clips[0].StartMS = 1000
	project.Tracks[1].Clips[0].SourceOutMS = 1000
	project.Tracks[1].Clips[0].DurationMS = 1000
	project, err = store.Update(project.ID, ProjectUpdate{Name: project.Name, Revision: project.Revision, TimelineDurationMS: 3000, Tracks: project.Tracks})
	if err != nil {
		t.Fatalf("save trims: %v", err)
	}

	first, err := store.Render(context.Background(), project.ID, project.Revision)
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	if first.Render.Revision != 1 || first.Render.Master == nil || first.Render.Master.GainDB != 2 || first.Project.Revision != project.Revision+1 {
		t.Fatalf("first render response = %+v", first)
	}
	firstPath, _, err := store.RenderPath(project.ID, 1)
	if err != nil {
		t.Fatalf("resolve first render: %v", err)
	}
	firstBytes, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	if duration, err := wav.Duration(firstBytes); err != nil || duration.Milliseconds() != 3000 {
		t.Fatalf("render duration = %s, err=%v", duration, err)
	}
	_, pcm, err := wav.Decode(firstBytes)
	if err != nil {
		t.Fatal(err)
	}
	sampleAt := func(ms int) int16 {
		offset := ms * wav.ToneSampleRate / 1000 * 2
		return int16(binary.LittleEndian.Uint16(pcm[offset : offset+2]))
	}
	for _, check := range []struct {
		ms   int
		want int16
	}{{0, 0}, {500, 3000}, {1000, 4000}, {1500, 1000}, {2000, 0}} {
		if got := sampleAt(check.ms); got != check.want {
			t.Fatalf("sample at %d ms = %d, want %d", check.ms, got, check.want)
		}
	}

	second, err := store.Render(context.Background(), project.ID, first.Project.Revision)
	if err != nil {
		t.Fatalf("second render: %v", err)
	}
	if second.Render.Revision != 2 || second.Render.URL == first.Render.URL || len(second.Project.Renders) != 2 {
		t.Fatalf("second render response = %+v", second)
	}
	unchanged, err := os.ReadFile(firstPath)
	if err != nil || string(unchanged) != string(firstBytes) {
		t.Fatalf("second render rewrote first: err=%v", err)
	}
	latest, err := store.LatestRender(project.ID)
	if err != nil || latest.Revision != 2 {
		t.Fatalf("latest = %+v, err=%v", latest, err)
	}
}

func steppedRenderTestWAV() []byte {
	format := wav.Format{Channels: 1, SampleRate: wav.ToneSampleRate, BitsPerSample: 16}
	pcm := make([]byte, 2*wav.ToneSampleRate*2)
	for sample := 0; sample < 2*wav.ToneSampleRate; sample++ {
		value := int16(1000)
		if sample >= wav.ToneSampleRate {
			value = 3000
		}
		binary.LittleEndian.PutUint16(pcm[sample*2:sample*2+2], uint16(value))
	}
	return wav.Encode(format, pcm)
}

func TestRenderRejectsUnreadyOrMissingAudioBeforePublication(t *testing.T) {
	root := t.TempDir()
	store := NewStoreWithOptions(root, StoreOptions{ResolveCharacterVoice: func(id string) (VoiceIdentity, bool, error) {
		return VoiceIdentity{CharacterVoiceID: id, ActorVoiceID: "actor", Fingerprint: "voice-v1"}, true, nil
	}})
	project, err := store.Create("Readiness")
	if err != nil {
		t.Fatal(err)
	}
	project, err = store.Update(project.ID, ProjectUpdate{Name: project.Name, Revision: project.Revision, TimelineDurationMS: 1000, Tracks: []Track{{
		ID: "dialogue", Name: "Dialogue", Type: TrackTypeDialogue, Order: 0, CharacterVoiceID: "character", Clips: []TimelineClip{{
			ID: "line", Type: ClipTypeDialogue, Label: "Line", Text: "Line", DurationMS: 1000,
		}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Render(context.Background(), project.ID, project.Revision); !errors.Is(err, ErrRenderNotReady) {
		t.Fatalf("unready render error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, project.ID, "renders")); !os.IsNotExist(err) {
		t.Fatalf("unready render created artifacts: %v", err)
	}

	project.Tracks[0].Muted = true
	project, err = store.Update(project.ID, ProjectUpdate{Name: project.Name, Revision: project.Revision, TimelineDurationMS: 1000, Tracks: project.Tracks})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Render(context.Background(), project.ID, project.Revision); err != nil {
		t.Fatalf("muted stale dialogue should not block render: %v", err)
	}
}

func TestRenderRejectsMalformedOrChangedMasterBeforePublication(t *testing.T) {
	for _, test := range []struct {
		name   string
		master func([]byte) []byte
	}{
		{name: "malformed", master: func([]byte) []byte { return []byte("RIFFtestWAVE") }},
		{name: "format changed", master: func(audio []byte) []byte {
			format, pcm, _ := wav.Decode(audio)
			format.SampleRate = 24000
			return wav.Encode(format, pcm)
		}},
		{name: "duration changed", master: func(audio []byte) []byte {
			format, pcm, _ := wav.Decode(audio)
			return wav.Encode(format, pcm[:len(pcm)/2])
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			store := NewStoreWithOptions(root, StoreOptions{MasterRender: func(_ context.Context, audio []byte) ([]byte, *story.Master, error) {
				return test.master(audio), &story.Master{}, nil
			}})
			project, err := store.Create("Invalid master")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Render(context.Background(), project.ID, project.Revision); err == nil {
				t.Fatalf("invalid master was published")
			}
			loaded, ok, err := store.Get(project.ID)
			if err != nil || !ok || loaded.Revision != project.Revision || len(loaded.Renders) != 0 {
				t.Fatalf("invalid master mutated project: %+v ok=%v err=%v", loaded, ok, err)
			}
			if _, err := os.Stat(filepath.Join(root, project.ID, "renders")); !os.IsNotExist(err) {
				t.Fatalf("invalid master wrote artifacts: %v", err)
			}
		})
	}
}

func TestRenderFailurePreservesEarlierRevisionAndProject(t *testing.T) {
	root := t.TempDir()
	tone := wav.SyntheticTone(wav.ToneSampleRate)
	store := NewStoreWithOptions(root, StoreOptions{ResolveLibraryAudio: func(id string) (LibraryAudio, bool, error) {
		return LibraryAudio{ID: id, Name: "Door", MediaRole: MediaRoleSFX, Data: tone}, true, nil
	}})
	project, err := store.Create("Failure safety")
	if err != nil {
		t.Fatal(err)
	}
	project, err = store.Update(project.ID, ProjectUpdate{Name: project.Name, Revision: project.Revision, TimelineDurationMS: 1000, Tracks: []Track{{ID: "sfx", Name: "SFX", Type: TrackTypeSFX, Order: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	project, err = store.PlaceLibraryAudio(project.ID, LibraryAudioPlacement{Revision: project.Revision, TrackID: "sfx", LibraryItemID: "door"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Render(context.Background(), project.ID, project.Revision)
	if err != nil {
		t.Fatal(err)
	}
	firstPath, _, _ := store.RenderPath(project.ID, 1)
	firstBytes, _ := os.ReadFile(firstPath)

	mediaPath := filepath.Join(root, project.ID, "media", project.Tracks[0].Clips[0].SourceID+".wav")
	if err := os.Remove(mediaPath); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Render(context.Background(), project.ID, first.Project.Revision); !errors.Is(err, ErrProjectMediaNotFound) {
		t.Fatalf("missing-media render error = %v", err)
	}
	if err := os.WriteFile(mediaPath, tone, 0o644); err != nil {
		t.Fatal(err)
	}

	failing := NewStoreWithOptions(root, StoreOptions{WriteFileAtomic: func(path string, data []byte) error {
		if filepath.Base(path) == manifestName {
			return errors.New("injected manifest failure")
		}
		return writeFileAtomic(path, data)
	}})
	if _, err := failing.Render(context.Background(), project.ID, first.Project.Revision); err == nil {
		t.Fatalf("manifest failure render succeeded")
	}
	loaded, ok, err := store.Get(project.ID)
	if err != nil || !ok || loaded.Revision != first.Project.Revision || len(loaded.Renders) != 1 {
		t.Fatalf("failed render mutated project: %+v ok=%v err=%v", loaded, ok, err)
	}
	if _, err := os.Stat(filepath.Join(root, project.ID, "renders", renderFilename(2))); !os.IsNotExist(err) {
		t.Fatalf("failed render left revision 2: %v", err)
	}
	unchanged, _ := os.ReadFile(firstPath)
	if string(unchanged) != string(firstBytes) {
		t.Fatalf("failed render changed revision 1")
	}
}
