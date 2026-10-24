package storybuilder

import (
	"context"
	"cpp-studio/internal/pronunciation"
	"cpp-studio/internal/wav"
	"errors"
	"strings"
	"testing"
)

func TestProjectPronunciationInvalidatesOnlyChangedSpeechAndRejectsStaleBuild(t *testing.T) {
	store, p := buildTestProject(t, []TimelineClip{{ID: "line_one", Type: ClipTypeDialogue, Label: "Line", Text: "James arrived.", StartMS: 0, DurationMS: 1000}, {ID: "line_two", Type: ClipTypeDialogue, Label: "Line", Text: "Quiet now.", StartMS: 2000, DurationMS: 1000}})
	for _, id := range []string{"line_one", "line_two"} {
		input, err := store.BeginDialogueBuild(p.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		p, err = store.CompleteDialogueBuild(p.ID, input, wav.SyntheticTone(16000))
		if err != nil {
			t.Fatal(err)
		}
	}
	oldID := p.Tracks[0].Clips[1].SourceID
	snap, _ := pronunciation.Freeze([]pronunciation.Rule{{Term: "James", Replacement: "Jaymz", Language: "en"}})
	p, err := store.Update(p.ID, ProjectUpdate{Name: p.Name, Revision: p.Revision, Tracks: p.Tracks, ApplyPronunciation: true, Pronunciation: &snap, Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Tracks[0].Clips[0].Text != "James arrived." || p.Tracks[0].Clips[0].Status != DialogueStatusStale || p.Tracks[0].Clips[1].SourceID != oldID || p.Tracks[0].Clips[1].Status != DialogueStatusReady {
		t.Fatal(p.Tracks)
	}
	input, err := store.BeginDialogueBuild(p.ID, "line_one")
	if err != nil {
		t.Fatal(err)
	}
	if input.Text != "Jaymz arrived." || input.SourceText != "James arrived." {
		t.Fatal(input)
	}
	p, _, _ = store.Get(p.ID)
	next, _ := pronunciation.Freeze([]pronunciation.Rule{{Term: "James", Replacement: "Jamez", Language: "en"}})
	p, err = store.Update(p.ID, ProjectUpdate{Name: p.Name, Revision: p.Revision, Tracks: p.Tracks, ApplyPronunciation: true, Pronunciation: &next, Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CompleteDialogueBuild(p.ID, input, wav.SyntheticTone(16000)); !errors.Is(err, ErrConflict) {
		t.Fatal("stale audio attached", err)
	}
	// Reapplying identical spoken text retains a valid in-flight build.
	input, err = store.BeginDialogueBuild(p.ID, "line_one")
	if err != nil {
		t.Fatal(err)
	}
	p, _, _ = store.Get(p.ID)
	next.Rules = append(next.Rules, pronunciation.Rule{Term: "Unrelated", Replacement: "Other"})
	next, _ = pronunciation.Freeze(next.Rules)
	p, err = store.Update(p.ID, ProjectUpdate{Name: p.Name, Revision: p.Revision, Tracks: p.Tracks, ApplyPronunciation: true, Pronunciation: &next, Language: "en"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.CompleteDialogueBuild(p.ID, input, wav.SyntheticTone(16000)); err != nil {
		t.Fatal(err)
	}
}

func TestProjectExpandedSpeechRejectedBeforeReservation(t *testing.T) {
	store, p := buildTestProject(t, []TimelineClip{{ID: "line_one", Type: ClipTypeDialogue, Label: "Line", Text: strings.Repeat("X ", 100), DurationMS: 1000}})
	snap, _ := pronunciation.Freeze([]pronunciation.Rule{{Term: "X", Replacement: strings.Repeat("x", 100)}})
	if _, err := store.Update(p.ID, ProjectUpdate{Name: p.Name, Revision: p.Revision, Tracks: p.Tracks, ApplyPronunciation: true, Pronunciation: &snap, Language: "en"}); err == nil {
		t.Fatal("expanded line accepted")
	}
	reserved := false
	m := NewDialogueBuildManager(DialogueBuildManagerOptions{Store: store, ReserveEngine: func(context.Context, string) (func(), bool) { reserved = true; return func() {}, true }, Synthesize: func(context.Context, DialogueSynthesisInput) ([]byte, error) { return nil, nil }})
	// Saved state is unchanged after the failed update.
	saved, _, _ := store.Get(p.ID)
	if saved.Pronunciation != nil || reserved {
		t.Fatal("invalid update changed project")
	}
	_ = m
}
