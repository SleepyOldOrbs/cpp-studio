package story

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cpp-studio/internal/wav"
)

func TestValidateCreateRequest(t *testing.T) {
	t.Run("normalizes valid request", func(t *testing.T) {
		got, err := ValidateCreateRequest(validCreateRequest())
		if err != nil {
			t.Fatalf("ValidateCreateRequest returned error: %v", err)
		}
		if got.Subject != "how stars are born" {
			t.Fatalf("unexpected subject %q", got.Subject)
		}
		if got.TargetSeconds != 90 {
			t.Fatalf("unexpected target seconds %d", got.TargetSeconds)
		}
		if got.SourceMode != "curated" || got.VoiceMode != "placeholder" {
			t.Fatalf("unexpected modes source=%q voice=%q", got.SourceMode, got.VoiceMode)
		}
	})

	tests := []struct {
		name string
		edit func(*CreateRequest)
		code ErrorCode
	}{
		{
			name: "missing excerpt",
			edit: func(req *CreateRequest) {
				req.Sources[0].Excerpt = ""
			},
			code: CodeMissingSourceExcerpt,
		},
		{
			name: "insufficient sources",
			edit: func(req *CreateRequest) {
				req.Sources = req.Sources[:2]
			},
			code: CodeInsufficientSources,
		},
		{
			name: "unsupported source mode",
			edit: func(req *CreateRequest) {
				req.SourceMode = "web"
			},
			code: CodeUnsupportedSourceMode,
		},
		{
			name: "target seconds too low",
			edit: func(req *CreateRequest) {
				req.TargetSeconds = 10
			},
			code: CodeTargetSecondsInvalid,
		},
		{
			name: "excerpt too large",
			edit: func(req *CreateRequest) {
				req.Sources[0].Excerpt = strings.Repeat("x", MaxSourceExcerptChars+1)
			},
			code: CodeSourceExcerptTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validCreateRequest()
			tt.edit(&req)
			_, err := ValidateCreateRequest(req)
			if !storyErrorIs(err, tt.code) {
				t.Fatalf("expected code %s, got %v", tt.code, err)
			}
		})
	}
}

func TestBuildFixtureManifestGrounding(t *testing.T) {
	req, err := ValidateCreateRequest(validCreateRequest())
	if err != nil {
		t.Fatalf("validate request: %v", err)
	}
	manifest, audio, err := BuildFixtureManifest("story_20260706_130000_001", req, fixedNow())
	if err != nil {
		t.Fatalf("BuildFixtureManifest returned error: %v", err)
	}
	if manifest.Title != "The Nursery of Stars" {
		t.Fatalf("unexpected title %q", manifest.Title)
	}
	if len(manifest.SourceNotes) != 9 {
		t.Fatalf("expected 9 source notes, got %d", len(manifest.SourceNotes))
	}
	if len(manifest.FactCards) != 9 {
		t.Fatalf("expected 9 fact cards, got %d", len(manifest.FactCards))
	}
	if len(manifest.Cast) != 3 {
		t.Fatalf("expected 3 cast members, got %d", len(manifest.Cast))
	}
	if len(manifest.Script) < 10 {
		t.Fatalf("expected richer script, got %d lines", len(manifest.Script))
	}
	if manifest.DurationSeconds != 90 {
		t.Fatalf("expected 90 second target duration, got %d", manifest.DurationSeconds)
	}
	for i, line := range manifest.Script {
		if len(line.FactIDs) == 0 {
			t.Fatalf("script line %d has no fact ids", i)
		}
	}
	if len(audio) < 12 || string(audio[:4]) != "RIFF" || string(audio[8:12]) != "WAVE" {
		t.Fatalf("expected fixture WAV bytes, got %q", string(audio[:min(len(audio), 12)]))
	}
}

func TestValidateManifestGroundingRejectsBadFacts(t *testing.T) {
	req, err := ValidateCreateRequest(validCreateRequest())
	if err != nil {
		t.Fatalf("validate request: %v", err)
	}
	manifest, _, err := BuildFixtureManifest("story_20260706_130000_001", req, fixedNow())
	if err != nil {
		t.Fatalf("BuildFixtureManifest returned error: %v", err)
	}

	t.Run("unknown fact", func(t *testing.T) {
		bad := manifest
		bad.Script = append([]ScriptLine{}, manifest.Script...)
		bad.Script[0].FactIDs = []string{"fact-missing"}
		err := ValidateManifestGrounding(bad)
		if !storyErrorIs(err, CodeGroundingFailure) {
			t.Fatalf("expected grounding failure, got %v", err)
		}
	})

	t.Run("conflicting fact", func(t *testing.T) {
		bad := manifest
		bad.FactCards = append([]FactCard{}, manifest.FactCards...)
		bad.FactCards[0].Conflicting = true
		err := ValidateManifestGrounding(bad)
		if !storyErrorIs(err, CodeGroundingFailure) {
			t.Fatalf("expected grounding failure, got %v", err)
		}
	})
}

func TestValidateCreateRequestSketchMode(t *testing.T) {
	t.Run("needs no sources", func(t *testing.T) {
		got, err := ValidateCreateRequest(validSketchRequest())
		if err != nil {
			t.Fatalf("ValidateCreateRequest returned error: %v", err)
		}
		if got.Mode != ModeSketch {
			t.Fatalf("expected sketch mode, got %q", got.Mode)
		}
		if got.SourceMode != "none" || len(got.Sources) != 0 {
			t.Fatalf("expected no sources, got mode %q and %d sources", got.SourceMode, len(got.Sources))
		}
		if got.Premise == "" || got.Style == "" {
			t.Fatalf("expected premise and style to survive, got %+v", got)
		}
	})

	t.Run("ignores sources it was handed", func(t *testing.T) {
		req := validSketchRequest()
		req.Sources = validCreateRequest().Sources
		req.SourceMode = "curated"
		got, err := ValidateCreateRequest(req)
		if err != nil {
			t.Fatalf("ValidateCreateRequest returned error: %v", err)
		}
		if len(got.Sources) != 0 {
			t.Fatalf("expected sketch to drop sources, got %d", len(got.Sources))
		}
	})

	t.Run("grounded mode still demands sources", func(t *testing.T) {
		req := validSketchRequest()
		req.Mode = ModeGrounded
		_, err := ValidateCreateRequest(req)
		if !storyErrorIs(err, CodeInsufficientSources) {
			t.Fatalf("expected insufficient sources, got %v", err)
		}
	})

	t.Run("grounded mode drops premise and style", func(t *testing.T) {
		req := validCreateRequest()
		req.Premise = "not used here"
		req.Style = "nor this"
		got, err := ValidateCreateRequest(req)
		if err != nil {
			t.Fatalf("ValidateCreateRequest returned error: %v", err)
		}
		if got.Premise != "" || got.Style != "" {
			t.Fatalf("expected grounded mode to drop premise/style, got %+v", got)
		}
	})

	t.Run("rejects an unknown mode", func(t *testing.T) {
		req := validSketchRequest()
		req.Mode = "documentary"
		_, err := ValidateCreateRequest(req)
		if !storyErrorIs(err, CodeUnsupportedMode) {
			t.Fatalf("expected unsupported mode, got %v", err)
		}
	})

	t.Run("rejects an over-long premise", func(t *testing.T) {
		req := validSketchRequest()
		req.Premise = strings.Repeat("x", MaxPremiseChars+1)
		_, err := ValidateCreateRequest(req)
		if !storyErrorIs(err, CodeInvalidRequest) {
			t.Fatalf("expected invalid request, got %v", err)
		}
	})
}

func TestBuildFixtureManifestSketch(t *testing.T) {
	req, err := ValidateCreateRequest(validSketchRequest())
	if err != nil {
		t.Fatalf("validate request: %v", err)
	}
	manifest, audio, err := BuildFixtureManifest("story_20260706_130000_002", req, fixedNow())
	if err != nil {
		t.Fatalf("BuildFixtureManifest returned error: %v", err)
	}
	if manifest.Mode != ModeSketch {
		t.Fatalf("expected sketch manifest, got mode %q", manifest.Mode)
	}
	if manifest.Premise == "" || manifest.Style == "" {
		t.Fatalf("expected premise and style recorded, got %+v", manifest)
	}
	if len(manifest.Sources) != 0 || len(manifest.SourceNotes) != 0 || len(manifest.FactCards) != 0 {
		t.Fatalf("expected an unsourced sketch, got %d sources, %d notes, %d facts", len(manifest.Sources), len(manifest.SourceNotes), len(manifest.FactCards))
	}
	if len(manifest.Script) < 4 {
		t.Fatalf("expected a sketch script, got %d lines", len(manifest.Script))
	}
	for i, line := range manifest.Script {
		if len(line.FactIDs) != 0 {
			t.Fatalf("sketch line %d cites facts that do not exist: %v", i, line.FactIDs)
		}
	}
	if len(audio) < 12 || string(audio[:4]) != "RIFF" {
		t.Fatalf("expected fixture WAV bytes")
	}
}

func TestValidateManifestSketchChecksShapeOnly(t *testing.T) {
	req, err := ValidateCreateRequest(validSketchRequest())
	if err != nil {
		t.Fatalf("validate request: %v", err)
	}
	manifest, _, err := BuildFixtureManifest("story_20260706_130000_002", req, fixedNow())
	if err != nil {
		t.Fatalf("BuildFixtureManifest returned error: %v", err)
	}

	t.Run("uncited lines pass", func(t *testing.T) {
		if err := ValidateManifest(manifest); err != nil {
			t.Fatalf("expected sketch to validate, got %v", err)
		}
	})

	t.Run("unknown speaker still fails", func(t *testing.T) {
		bad := manifest
		bad.Script = append([]ScriptLine{}, manifest.Script...)
		bad.Script[0].SpeakerID = "nobody"
		if err := ValidateManifest(bad); !storyErrorIs(err, CodeInvalidScript) {
			t.Fatalf("expected invalid script, got %v", err)
		}
	})

	t.Run("empty text still fails", func(t *testing.T) {
		bad := manifest
		bad.Script = append([]ScriptLine{}, manifest.Script...)
		bad.Script[0].Text = "   "
		if err := ValidateManifest(bad); !storyErrorIs(err, CodeInvalidScript) {
			t.Fatalf("expected invalid script, got %v", err)
		}
	})
}

func TestManagerProducesSketchWithoutSources(t *testing.T) {
	var gotRequest ScriptRequest
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(),
		Script: func(ctx context.Context, req ScriptRequest) (string, []ScriptLine, error) {
			gotRequest = req
			return "The Apology Shop", []ScriptLine{
				{SpeakerID: req.Cast[0].ID, Text: "I'd like to return this apology."},
				{SpeakerID: req.Cast[1].ID, Text: "Was it not sincere enough, sir?"},
				{SpeakerID: req.Cast[0].ID, Text: "It was far too sincere. It made my wife cry."},
				{SpeakerID: req.Cast[1].ID, Text: "Ah. You'll be wanting the insincere counter, then."},
			}, nil
		},
		StageDelay: time.Millisecond,
		Now:        fixedNow,
	})

	created, err := manager.Submit(context.Background(), validSketchRequest())
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	status := waitStoryStatus(t, manager, created.ID, StatusComplete)
	if status.Manifest == nil {
		t.Fatalf("expected completed manifest, got %+v", status)
	}
	if status.Manifest.Mode != ModeSketch || status.Manifest.Title != "The Apology Shop" {
		t.Fatalf("unexpected sketch manifest %+v", status.Manifest)
	}
	if len(status.Manifest.Script) != 4 {
		t.Fatalf("expected the writer's four lines, got %d", len(status.Manifest.Script))
	}
	// The writer is handed the premise and style instead of fact cards.
	if gotRequest.Mode != ModeSketch || gotRequest.Premise == "" || gotRequest.Style == "" {
		t.Fatalf("expected sketch steering in the script request, got %+v", gotRequest)
	}
	if len(gotRequest.Facts) != 0 {
		t.Fatalf("expected no facts in a sketch script request, got %d", len(gotRequest.Facts))
	}
}

func TestManagerSketchStillRejectsUnknownSpeaker(t *testing.T) {
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(),
		Script: func(ctx context.Context, req ScriptRequest) (string, []ScriptLine, error) {
			return "Bad Sketch", []ScriptLine{
				{SpeakerID: "a-speaker-nobody-cast", Text: "Who am I?"},
			}, nil
		},
		StageDelay: time.Millisecond,
		Now:        fixedNow,
	})

	created, err := manager.Submit(context.Background(), validSketchRequest())
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	status := waitStoryStatus(t, manager, created.ID, StatusFailed)
	if status.Error == nil || status.Error.Code != CodeInvalidScript {
		t.Fatalf("expected invalid script, got %+v", status.Error)
	}
}

func TestManagerDraftsSketchWithCustomCast(t *testing.T) {
	manager := NewManager(ManagerOptions{RootDir: t.TempDir(), Now: fixedNow})

	req := validSketchRequest()
	req.Cast = []CastInput{
		{Name: "Kenneth", Role: "the browbeaten customer"},
		{Name: "Hugh", Role: "the evasive shopkeeper"},
	}
	draft, err := manager.Draft(context.Background(), req)
	if err != nil {
		t.Fatalf("Draft returned error: %v", err)
	}
	if draft.Mode != ModeSketch {
		t.Fatalf("expected the draft to echo sketch mode, got %q", draft.Mode)
	}
	if len(draft.FactCards) != 0 || len(draft.Sources) != 0 {
		t.Fatalf("expected an unsourced draft, got %d facts, %d sources", len(draft.FactCards), len(draft.Sources))
	}
	if len(draft.Cast) != 2 {
		t.Fatalf("expected the two-hander cast, got %d", len(draft.Cast))
	}
	// The fixture script deals its lines round whatever cast was named.
	speakers := map[string]bool{}
	for _, line := range draft.Script {
		speakers[line.SpeakerID] = true
	}
	if !speakers["kenneth"] || !speakers["hugh"] {
		t.Fatalf("expected both cast members to speak, got %v", speakers)
	}
}

// producedStory runs a real fixed-voice production through the manager and
// returns its manifest — the starting point for every take-room test.
func producedStory(t *testing.T, opts ...func(*ManagerOptions)) (*Manager, Manifest) {
	t.Helper()
	options := ManagerOptions{
		RootDir: t.TempDir(),
		Synthesize: func(ctx context.Context, text string, voiceID string) ([]byte, error) {
			return wav.SyntheticTone(wav.ToneSampleRate / 2), nil
		},
		StageDelay: time.Millisecond,
		Now:        fixedNow,
	}
	for _, opt := range opts {
		opt(&options)
	}
	manager := NewManager(options)

	req := validSketchRequest()
	req.VoiceMode = "fixed"
	created, err := manager.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	status := waitStoryStatus(t, manager, created.ID, StatusComplete)
	if status.Manifest == nil {
		t.Fatalf("expected a completed manifest, got %+v", status)
	}
	return manager, *status.Manifest
}

func TestProductionRetainsTakesAndFirstRender(t *testing.T) {
	manager, manifest := producedStory(t)

	for i, line := range manifest.Script {
		if line.ID == "" {
			t.Fatalf("script[%d] has no stable line id", i)
		}
		if len(line.Takes) != 1 || line.CurrentTake != line.Takes[0].ID {
			t.Fatalf("script[%d] expected one current take, got %+v", i, line)
		}
		take := line.Takes[0]
		if take.Text != line.Text {
			t.Fatalf("take %s recorded text %q, line says %q", take.ID, take.Text, line.Text)
		}
		if take.DurationMS <= 0 || take.Bytes <= 0 || take.URL == "" {
			t.Fatalf("take %s is not described: %+v", take.ID, take)
		}
		// The take audio is on disk and re-readable, which is the whole point.
		audio, err := manager.store.LoadTake(manifest.ID, line.ID, take.ID)
		if err != nil {
			t.Fatalf("load take %s: %v", take.ID, err)
		}
		if err := wav.ValidateBytes(audio); err != nil {
			t.Fatalf("take %s is not valid WAV: %v", take.ID, err)
		}
	}

	if len(manifest.Renders) != 1 || manifest.Renders[0].Revision != 1 {
		t.Fatalf("expected render revision 1, got %+v", manifest.Renders)
	}
	if _, err := manager.ArtifactPath(manifest.ID, "renders", "render-001.wav"); err != nil {
		t.Fatalf("render revision 1 is not servable: %v", err)
	}
}

func TestRetakeAddsATakeAndMakesItCurrent(t *testing.T) {
	var spokenTexts []string
	manager, manifest := producedStory(t, func(o *ManagerOptions) {
		o.Synthesize = func(ctx context.Context, text string, voiceID string) ([]byte, error) {
			spokenTexts = append(spokenTexts, text)
			return wav.SyntheticTone(wav.ToneSampleRate / 2), nil
		}
	})

	target := manifest.Script[1]
	updated, take, err := manager.Retake(context.Background(), manifest.ID, target.ID)
	if err != nil {
		t.Fatalf("Retake returned error: %v", err)
	}
	if take.ID != "take-002" {
		t.Fatalf("expected take-002, got %q", take.ID)
	}
	if spokenTexts[len(spokenTexts)-1] != target.Text {
		t.Fatalf("retake spoke %q, expected the line's text %q", spokenTexts[len(spokenTexts)-1], target.Text)
	}
	line := updated.Script[1]
	if len(line.Takes) != 2 || line.CurrentTake != "take-002" {
		t.Fatalf("expected two takes with the new one current, got %+v", line)
	}
	// The first take survives — a retake is an addition, not a replacement.
	if _, err := manager.store.LoadTake(manifest.ID, target.ID, "take-001"); err != nil {
		t.Fatalf("original take was lost: %v", err)
	}

	// And it survives a reload: the manifest on disk carries the history.
	reloaded, ok, err := manager.store.Load(manifest.ID)
	if err != nil || !ok {
		t.Fatalf("reload story: %v (ok=%v)", err, ok)
	}
	if len(reloaded.Script[1].Takes) != 2 {
		t.Fatalf("takes did not persist: %+v", reloaded.Script[1])
	}

	t.Run("unknown line", func(t *testing.T) {
		_, _, err := manager.Retake(context.Background(), manifest.ID, "line-999")
		if !storyErrorIs(err, CodeLineNotFound) {
			t.Fatalf("expected line_not_found, got %v", err)
		}
	})
}

func TestEditLineChoosesTakesMutesAndTiming(t *testing.T) {
	manager, manifest := producedStory(t)
	lineID := manifest.Script[0].ID

	t.Run("rejects an unknown take", func(t *testing.T) {
		missing := "take-404"
		_, err := manager.EditLine(manifest.ID, lineID, LinePatch{CurrentTake: &missing})
		if !storyErrorIs(err, CodeTakeNotFound) {
			t.Fatalf("expected take_not_found, got %v", err)
		}
	})

	t.Run("rejects out-of-range timing", func(t *testing.T) {
		tooLong := MaxGapMS + 1
		_, err := manager.EditLine(manifest.ID, lineID, LinePatch{GapAfterMS: &tooLong})
		if !storyErrorIs(err, CodeInvalidRequest) {
			t.Fatalf("expected invalid_request, got %v", err)
		}
	})

	t.Run("applies mute and timing", func(t *testing.T) {
		muted := true
		gap := 400
		updated, err := manager.EditLine(manifest.ID, lineID, LinePatch{Muted: &muted, GapAfterMS: &gap})
		if err != nil {
			t.Fatalf("EditLine returned error: %v", err)
		}
		if !updated.Script[0].Muted || updated.Script[0].GapAfterMS != 400 {
			t.Fatalf("edit did not apply: %+v", updated.Script[0])
		}
		// Untouched fields stay untouched.
		if updated.Script[0].GapBeforeMS != 0 || updated.Script[0].CurrentTake != "take-001" {
			t.Fatalf("edit disturbed other fields: %+v", updated.Script[0])
		}
	})
}

func TestRenderPublishesImmutableRevisions(t *testing.T) {
	manager, manifest := producedStory(t)

	before, _, err := manager.Status(manifest.ID)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	firstDuration := before.Manifest.DurationSeconds

	// Mute a line, then re-render: the new revision must be shorter and the
	// old one must still be on disk.
	muted := true
	if _, err := manager.EditLine(manifest.ID, manifest.Script[0].ID, LinePatch{Muted: &muted}); err != nil {
		t.Fatalf("EditLine returned error: %v", err)
	}
	updated, render, err := manager.Render(context.Background(), manifest.ID)
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if render.Revision != 2 {
		t.Fatalf("expected revision 2, got %d", render.Revision)
	}
	if len(updated.Renders) != 2 {
		t.Fatalf("expected two render revisions, got %+v", updated.Renders)
	}
	if render.DurationSeconds > firstDuration {
		t.Fatalf("muting a line should not lengthen the story: %d -> %d", firstDuration, render.DurationSeconds)
	}
	for _, revision := range []string{"render-001.wav", "render-002.wav"} {
		if _, err := manager.ArtifactPath(manifest.ID, "renders", revision); err != nil {
			t.Fatalf("%s is not servable: %v", revision, err)
		}
	}

	t.Run("refuses to render nothing", func(t *testing.T) {
		for _, line := range updated.Script {
			mute := true
			if _, err := manager.EditLine(manifest.ID, line.ID, LinePatch{Muted: &mute}); err != nil {
				t.Fatalf("EditLine returned error: %v", err)
			}
		}
		if _, _, err := manager.Render(context.Background(), manifest.ID); !storyErrorIs(err, CodeNothingToRender) {
			t.Fatalf("expected nothing_to_render, got %v", err)
		}
	})
}

// Status prefers the tracked in-memory job over the store for as long as the
// process lives, so every take-room edit has to republish the manifest that
// job is serving. Without it an edit lands on disk and stays invisible.
func TestStatusReflectsTakeRoomEdits(t *testing.T) {
	manager, manifest := producedStory(t)
	lineID := manifest.Script[1].ID

	if _, _, err := manager.Retake(context.Background(), manifest.ID, lineID); err != nil {
		t.Fatalf("Retake returned error: %v", err)
	}
	status, _, err := manager.Status(manifest.ID)
	if err != nil {
		t.Fatalf("Status returned error: %v", err)
	}
	if got := len(status.Manifest.Script[1].Takes); got != 2 {
		t.Fatalf("status still reports %d takes after a retake", got)
	}

	muted := true
	if _, err := manager.EditLine(manifest.ID, lineID, LinePatch{Muted: &muted}); err != nil {
		t.Fatalf("EditLine returned error: %v", err)
	}
	if status, _, _ = manager.Status(manifest.ID); !status.Manifest.Script[1].Muted {
		t.Fatalf("status does not reflect the mute")
	}

	if _, _, err := manager.Render(context.Background(), manifest.ID); err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if status, _, _ = manager.Status(manifest.ID); len(status.Manifest.Renders) != 2 {
		t.Fatalf("status reports %d renders after re-rendering", len(status.Manifest.Renders))
	}
}

// A muted line contributes nothing to the render — not its audio and not
// its timing. The first version of stitchTakes reached for script[i-1]
// rather than the last line it actually kept, so a muted middle line still
// donated its trailing gap to the silence around it.
func TestEditLineTextDeselectsTheStaleTake(t *testing.T) {
	manager, manifest := producedStory(t)
	lineID := manifest.Script[0].ID

	newText := "A completely different joke."
	updated, err := manager.EditLine(manifest.ID, lineID, LinePatch{Text: &newText})
	if err != nil {
		t.Fatalf("EditLine returned error: %v", err)
	}
	line := updated.Script[0]
	if line.Text != newText {
		t.Fatalf("text was not applied: %q", line.Text)
	}
	if line.CurrentTake != "" {
		t.Fatalf("the take recorded against the old words is still selected: %q", line.CurrentTake)
	}
	// The recording itself is kept — only the selection was dropped.
	if len(line.Takes) != 1 {
		t.Fatalf("editing text destroyed take history: %+v", line.Takes)
	}

	t.Run("the stale take cannot be reselected", func(t *testing.T) {
		stale := "take-001"
		if _, err := manager.EditLine(manifest.ID, lineID, LinePatch{CurrentTake: &stale}); !storyErrorIs(err, CodeStaleTake) {
			t.Fatalf("expected stale_take, got %v", err)
		}
	})

	t.Run("rendering refuses to publish words nobody spoke", func(t *testing.T) {
		// The line has no current take, so it contributes nothing; that is
		// fine. But reselecting is blocked above, and a take whose text
		// drifted must never reach a render.
		bad := updated
		bad.Script[0].CurrentTake = "take-001"
		if err := manager.store.SaveManifest(bad); err != nil {
			t.Fatalf("SaveManifest returned error: %v", err)
		}
		if _, _, err := manager.Render(context.Background(), manifest.ID); !storyErrorIs(err, CodeStaleTake) {
			t.Fatalf("expected stale_take from Render, got %v", err)
		}
	})

	t.Run("a retake makes the line renderable again", func(t *testing.T) {
		if _, _, err := manager.Retake(context.Background(), manifest.ID, lineID); err != nil {
			t.Fatalf("Retake returned error: %v", err)
		}
		after, _, err := manager.Status(manifest.ID)
		if err != nil {
			t.Fatalf("Status returned error: %v", err)
		}
		take := after.Manifest.Script[0]
		if take.CurrentTake == "" {
			t.Fatalf("retake did not select the new recording")
		}
		if got := takeByID(take.Takes, take.CurrentTake); got == nil || got.Text != newText {
			t.Fatalf("the new take was not recorded against the new words: %+v", got)
		}
		if _, _, err := manager.Render(context.Background(), manifest.ID); err != nil {
			t.Fatalf("Render returned error after retake: %v", err)
		}
	})
}

// Two retakes of the same line arriving together used to compute the same
// take id, overwrite each other's audio, and race the manifest write. Same
// for two renders claiming one revision number.
func TestConcurrentTakeRoomEditsDoNotCollide(t *testing.T) {
	manager, manifest := producedStory(t)
	lineID := manifest.Script[0].ID

	const retakes = 6
	var wg sync.WaitGroup
	errs := make(chan error, retakes)
	for i := 0; i < retakes; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := manager.Retake(context.Background(), manifest.ID, lineID); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent retake failed: %v", err)
	}

	stored, ok, err := manager.store.Load(manifest.ID)
	if err != nil || !ok {
		t.Fatalf("load story: %v (ok=%v)", err, ok)
	}
	takes := stored.Script[0].Takes
	if len(takes) != retakes+1 {
		t.Fatalf("expected %d takes after %d concurrent retakes, got %d", retakes+1, retakes, len(takes))
	}
	seen := map[string]bool{}
	for _, take := range takes {
		if seen[take.ID] {
			t.Fatalf("duplicate take id %s — one retake overwrote another", take.ID)
		}
		seen[take.ID] = true
		if _, err := manager.store.LoadTake(manifest.ID, lineID, take.ID); err != nil {
			t.Fatalf("take %s has no audio on disk: %v", take.ID, err)
		}
	}

	const renders = 4
	wg = sync.WaitGroup{}
	rerrs := make(chan error, renders)
	for i := 0; i < renders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := manager.Render(context.Background(), manifest.ID); err != nil {
				rerrs <- err
			}
		}()
	}
	wg.Wait()
	close(rerrs)
	for err := range rerrs {
		t.Fatalf("concurrent render failed: %v", err)
	}

	stored, _, err = manager.store.Load(manifest.ID)
	if err != nil {
		t.Fatalf("load story: %v", err)
	}
	revisions := map[int]bool{}
	for _, render := range stored.Renders {
		if revisions[render.Revision] {
			t.Fatalf("duplicate render revision %d — one render overwrote another", render.Revision)
		}
		revisions[render.Revision] = true
		if _, err := manager.ArtifactPath(manifest.ID, "renders", fmt.Sprintf("render-%03d.wav", render.Revision)); err != nil {
			t.Fatalf("revision %d is not on disk: %v", render.Revision, err)
		}
	}
	if len(stored.Renders) != renders+1 {
		t.Fatalf("expected %d revisions, got %d", renders+1, len(stored.Renders))
	}
}

func TestRenderRecordsItsRecipe(t *testing.T) {
	manager, manifest := producedStory(t)

	muted := true
	if _, err := manager.EditLine(manifest.ID, manifest.Script[0].ID, LinePatch{Muted: &muted}); err != nil {
		t.Fatalf("EditLine returned error: %v", err)
	}
	updated, render, err := manager.Render(context.Background(), manifest.ID)
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if len(render.Recipe) != len(manifest.Script)-1 {
		t.Fatalf("expected one recipe line per rendered line, got %d for %d lines", len(render.Recipe), len(manifest.Script)-1)
	}
	for _, entry := range render.Recipe {
		if entry.LineID == manifest.Script[0].ID {
			t.Fatalf("the muted line appears in the recipe: %+v", entry)
		}
		if entry.TakeID == "" || entry.Text == "" {
			t.Fatalf("recipe entry is not self-describing: %+v", entry)
		}
	}

	// The recipe is a snapshot: editing the script afterwards must not
	// rewrite what an already-published revision says it was.
	newText := "rewritten after the fact"
	if _, err := manager.EditLine(manifest.ID, updated.Script[1].ID, LinePatch{Text: &newText}); err != nil {
		t.Fatalf("EditLine returned error: %v", err)
	}
	reloaded, _, err := manager.store.Load(manifest.ID)
	if err != nil {
		t.Fatalf("load story: %v", err)
	}
	for _, entry := range reloaded.Renders[len(reloaded.Renders)-1].Recipe {
		if entry.Text == newText {
			t.Fatalf("a published render's recipe changed under it: %+v", entry)
		}
	}
}

func TestStitchIgnoresTimingOfMutedLines(t *testing.T) {
	clip := wav.SyntheticTone(wav.ToneSampleRate / 4)
	script := []ScriptLine{
		{ID: "line-001", SpeakerID: "a", Text: "one"},
		{ID: "line-002", SpeakerID: "b", Text: "two", Muted: true, GapAfterMS: MaxGapMS},
		{ID: "line-003", SpeakerID: "a", Text: "three"},
	}
	withMutedGap, err := stitchTakes([][]byte{clip, clip, clip}, script)
	if err != nil {
		t.Fatalf("stitchTakes returned error: %v", err)
	}

	// The same render with the muted line carrying no nudge at all must be
	// identical: a muted line's timing is not part of the piece.
	script[1].GapAfterMS = 0
	withoutMutedGap, err := stitchTakes([][]byte{clip, clip, clip}, script)
	if err != nil {
		t.Fatalf("stitchTakes returned error: %v", err)
	}
	if len(withMutedGap) != len(withoutMutedGap) {
		t.Fatalf("a muted line's gap leaked into the render: %d bytes vs %d", len(withMutedGap), len(withoutMutedGap))
	}

	// And the surviving neighbour's own nudge still applies.
	script[0].GapAfterMS = 500
	louder, err := stitchTakes([][]byte{clip, clip, clip}, script)
	if err != nil {
		t.Fatalf("stitchTakes returned error: %v", err)
	}
	if len(louder) <= len(withoutMutedGap) {
		t.Fatalf("the kept line's own gap was dropped: %d bytes vs %d", len(louder), len(withoutMutedGap))
	}
}

func TestArtifactPathRejectsEscapes(t *testing.T) {
	manager, manifest := producedStory(t)

	cases := [][]string{
		{"lines", "..", "take-001.wav"},
		{"lines", "line-001", "../../../manifest.json"},
		{"renders", "../manifest.json"},
		{"renders", "render-001.txt"},
		{"manifest.json"},
		{"lines", "line-001", "take-001.mp3"},
		{"lines", "line-001"},
	}
	for _, segments := range cases {
		if _, err := manager.ArtifactPath(manifest.ID, segments...); err == nil {
			t.Fatalf("expected %v to be refused", segments)
		}
	}
}

func TestStoreSaveLoadListAndArtifactPath(t *testing.T) {
	store := NewStore(t.TempDir())
	req, err := ValidateCreateRequest(validCreateRequest())
	if err != nil {
		t.Fatalf("validate request: %v", err)
	}
	manifest, audio, err := BuildFixtureManifest("story_20260706_130000_001", req, fixedNow())
	if err != nil {
		t.Fatalf("BuildFixtureManifest returned error: %v", err)
	}
	if err := store.Save(manifest, audio); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	loaded, ok, err := store.Load(manifest.ID)
	if err != nil || !ok {
		t.Fatalf("Load ok=%v err=%v", ok, err)
	}
	if loaded.ID != manifest.ID || loaded.Audio.URL == "" {
		t.Fatalf("unexpected loaded manifest %+v", loaded)
	}
	list, err := store.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 1 || list[0].ID != manifest.ID {
		t.Fatalf("unexpected list %+v", list)
	}
	artifactPath, err := store.ArtifactPath(manifest.ID, StoryArtifactName)
	if err != nil {
		t.Fatalf("ArtifactPath returned error: %v", err)
	}
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("expected artifact file: %v", err)
	}
	if _, err := store.ArtifactPath(manifest.ID, "../config.json"); !storyErrorIs(err, CodeUnsupportedArtifact) {
		t.Fatalf("expected unsupported artifact error, got %v", err)
	}
	if _, ok, err := store.Load("../bad"); ok || !storyErrorIs(err, CodeNotFound) {
		t.Fatalf("expected invalid story id to return not found, ok=%v err=%v", ok, err)
	}
}

func TestStoreListIgnoresCorruptManifests(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "story_bad"), 0o755); err != nil {
		t.Fatalf("mkdir corrupt story: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "story_bad", "manifest.json"), []byte("{bad json"), 0o644); err != nil {
		t.Fatalf("write corrupt manifest: %v", err)
	}
	list, err := NewStore(root).List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected corrupt manifest to be ignored, got %+v", list)
	}
}

func TestManagerSubmitBusyCancelAndComplete(t *testing.T) {
	t.Run("busy and cancel", func(t *testing.T) {
		manager := NewManager(ManagerOptions{
			RootDir:    t.TempDir(),
			StageDelay: time.Hour,
			Now:        fixedNow,
		})
		first, err := manager.Submit(context.Background(), validCreateRequest())
		if err != nil {
			t.Fatalf("first Submit returned error: %v", err)
		}
		if _, err := manager.Submit(context.Background(), validCreateRequest()); !storyErrorIs(err, CodeStoryBusy) {
			t.Fatalf("expected story busy, got %v", err)
		}
		cancelled, err := manager.Cancel(first.ID)
		if err != nil {
			t.Fatalf("Cancel returned error: %v", err)
		}
		if cancelled.Status != StatusCancelled {
			t.Fatalf("expected cancelled status, got %+v", cancelled)
		}
		waitUntilSubmitSucceeds(t, manager)
	})

	t.Run("complete and persist", func(t *testing.T) {
		reserved := false
		manager := NewManager(ManagerOptions{
			RootDir: t.TempDir(),
			ReserveEngine: func(ctx context.Context, name string) (func(), bool) {
				if name != "audio" {
					t.Fatalf("unexpected reserved engine %q", name)
				}
				reserved = true
				return func() {}, true
			},
			StageDelay: time.Millisecond,
			Now:        fixedNow,
		})
		created, err := manager.Submit(context.Background(), validCreateRequest())
		if err != nil {
			t.Fatalf("Submit returned error: %v", err)
		}
		status := waitStoryStatus(t, manager, created.ID, StatusComplete)
		if !reserved {
			t.Fatalf("expected audio engine reservation")
		}
		if status.Manifest == nil || status.ArtifactURL == nil {
			t.Fatalf("expected completed manifest and artifact URL, got %+v", status)
		}
		list, err := manager.List()
		if err != nil {
			t.Fatalf("List returned error: %v", err)
		}
		if len(list) != 1 || list[0].ID != created.ID {
			t.Fatalf("unexpected persisted list %+v", list)
		}
		if _, err := manager.Cancel(created.ID); !storyErrorIs(err, CodeCannotCancel) {
			t.Fatalf("expected cannot cancel complete story, got %v", err)
		}
	})

	t.Run("engine busy blocks create before acceptance", func(t *testing.T) {
		manager := NewManager(ManagerOptions{
			RootDir: t.TempDir(),
			ReserveEngine: func(ctx context.Context, name string) (func(), bool) {
				return nil, false
			},
			Now: fixedNow,
		})
		if _, err := manager.Submit(context.Background(), validCreateRequest()); !storyErrorIs(err, CodeEngineBusy) {
			t.Fatalf("expected engine busy, got %v", err)
		}
	})
}

func TestManagerSynthesizesFixedVoiceStories(t *testing.T) {
	var synthesized []string
	var voices []string
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(),
		Synthesize: func(ctx context.Context, text string, voiceID string) ([]byte, error) {
			synthesized = append(synthesized, text)
			voices = append(voices, voiceID)
			return wav.SyntheticTone(wav.ToneSampleRate), nil // one second per line
		},
		StageDelay: time.Millisecond,
		Now:        fixedNow,
	})

	req := validCreateRequest()
	req.VoiceMode = "fixed"
	req.CastVoices = map[string]string{"narrator": "voice-a", "dr-lumen": "voice-b"}
	created, err := manager.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	status := waitStoryStatus(t, manager, created.ID, StatusComplete)

	if status.Manifest == nil {
		t.Fatalf("expected completed manifest, got %+v", status)
	}
	if len(synthesized) != len(status.Manifest.Script) {
		t.Fatalf("expected %d synthesized lines, got %d", len(status.Manifest.Script), len(synthesized))
	}
	// Cast voices route per speaker: narrator/dr-lumen lines carry their
	// assigned voices, nova falls back to the default (""). Synthesis order
	// is grouped by voice, so match on text rather than position.
	voiceByText := make(map[string]string, len(synthesized))
	for i, text := range synthesized {
		voiceByText[text] = voices[i]
	}
	for _, line := range status.Manifest.Script {
		want := map[string]string{"narrator": "voice-a", "dr-lumen": "voice-b", "nova": ""}[line.SpeakerID]
		if voiceByText[line.Text] != want {
			t.Fatalf("line %q (%s): expected voice %q, got %q", line.Text, line.SpeakerID, want, voiceByText[line.Text])
		}
	}
	// Grouped synthesis: each voice's lines are contiguous, so the voice
	// sequence never revisits an earlier voice.
	seen := make(map[string]bool)
	current := "\x00none"
	for _, v := range voices {
		if v == current {
			continue
		}
		if seen[v] {
			t.Fatalf("voice %q synthesized in multiple groups: %v", v, voices)
		}
		seen[v] = true
		current = v
	}
	for _, member := range status.Manifest.Cast {
		want := map[string]string{"narrator": "voice-a", "dr-lumen": "voice-b", "nova": "studio-default"}[member.ID]
		if member.VoiceID != want {
			t.Fatalf("cast %s: expected voice id %q, got %q", member.ID, want, member.VoiceID)
		}
	}

	// Stitched audio: len(script) seconds of clips plus 350ms gaps between.
	wantSeconds := len(synthesized)
	if got := status.Manifest.DurationSeconds; got < wantSeconds || got > wantSeconds+len(synthesized) {
		t.Fatalf("expected roughly %ds of stitched audio, manifest says %ds", wantSeconds, got)
	}

	artifactPath, err := manager.ArtifactPath(created.ID, StoryArtifactName)
	if err != nil {
		t.Fatalf("ArtifactPath returned error: %v", err)
	}
	data, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	duration, err := wav.Duration(data)
	if err != nil {
		t.Fatalf("artifact is not decodable WAV: %v", err)
	}
	if duration < time.Duration(wantSeconds)*time.Second {
		t.Fatalf("expected at least %ds of audio, got %s", wantSeconds, duration)
	}
}

func TestManagerFailsWhenSynthesisFails(t *testing.T) {
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(),
		Synthesize: func(ctx context.Context, text string, voiceID string) ([]byte, error) {
			return nil, fmt.Errorf("audio engine exploded")
		},
		StageDelay: time.Millisecond,
		Now:        fixedNow,
	})

	req := validCreateRequest()
	req.VoiceMode = "fixed"
	created, err := manager.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	status := waitStoryStatus(t, manager, created.ID, StatusFailed)
	if status.Error == nil || status.Error.Code != CodeSynthesisFailure {
		t.Fatalf("expected synthesis failure, got %+v", status.Error)
	}
}

func TestManagerUsesInjectedScriptWriter(t *testing.T) {
	var gotRequest ScriptRequest
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(),
		Script: func(ctx context.Context, req ScriptRequest) (string, []ScriptLine, error) {
			gotRequest = req
			return "A Custom Tale", []ScriptLine{
				{SpeakerID: "narrator", Text: "A written opening.", FactIDs: []string{req.Facts[0].ID}},
				{SpeakerID: "nova", Text: "A written question?", FactIDs: []string{req.Facts[1].ID}},
				{SpeakerID: "dr-lumen", Text: "A written answer.", FactIDs: []string{req.Facts[2].ID}},
				{SpeakerID: "narrator", Text: "A written ending.", FactIDs: []string{req.Facts[0].ID}},
			}, nil
		},
		StageDelay: time.Millisecond,
		Now:        fixedNow,
	})

	created, err := manager.Submit(context.Background(), validCreateRequest())
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	status := waitStoryStatus(t, manager, created.ID, StatusComplete)
	if status.Manifest == nil {
		t.Fatalf("expected completed manifest, got %+v", status)
	}
	if status.Manifest.Title != "A Custom Tale" {
		t.Fatalf("expected script writer title, got %q", status.Manifest.Title)
	}
	if len(status.Manifest.Script) != 4 || status.Manifest.Script[0].Text != "A written opening." {
		t.Fatalf("expected script writer lines, got %+v", status.Manifest.Script)
	}
	if gotRequest.Subject != "how stars are born" || gotRequest.TargetSeconds != 90 {
		t.Fatalf("unexpected script request %+v", gotRequest)
	}
	if len(gotRequest.Facts) < 8 || len(gotRequest.Cast) != 3 {
		t.Fatalf("expected scaffold facts and cast in script request, got %d facts, %d cast", len(gotRequest.Facts), len(gotRequest.Cast))
	}
}

func TestManagerFailsWhenScriptWriterFails(t *testing.T) {
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(),
		Script: func(ctx context.Context, req ScriptRequest) (string, []ScriptLine, error) {
			return "", nil, NewError(CodeGroundingFailure, "the writer had no ideas")
		},
		StageDelay: time.Millisecond,
		Now:        fixedNow,
	})

	created, err := manager.Submit(context.Background(), validCreateRequest())
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	status := waitStoryStatus(t, manager, created.ID, StatusFailed)
	if status.Error == nil || status.Error.Code != CodeGroundingFailure {
		t.Fatalf("expected grounding failure, got %+v", status.Error)
	}
}

func TestManagerRejectsUngroundedScriptWriterOutput(t *testing.T) {
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(),
		Script: func(ctx context.Context, req ScriptRequest) (string, []ScriptLine, error) {
			return "Bad Tale", []ScriptLine{
				{SpeakerID: "narrator", Text: "Cites nothing.", FactIDs: nil},
			}, nil
		},
		StageDelay: time.Millisecond,
		Now:        fixedNow,
	})

	created, err := manager.Submit(context.Background(), validCreateRequest())
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	status := waitStoryStatus(t, manager, created.ID, StatusFailed)
	if status.Error == nil || status.Error.Code != CodeGroundingFailure {
		t.Fatalf("expected grounding failure, got %+v", status.Error)
	}
}

func TestManagerDraftWritesWithoutProducing(t *testing.T) {
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(),
		Script: func(ctx context.Context, req ScriptRequest) (string, []ScriptLine, error) {
			return "Drafted Tale", []ScriptLine{
				{SpeakerID: req.Cast[0].ID, Text: "Drafted opening.", FactIDs: []string{req.Facts[0].ID}},
				{SpeakerID: req.Cast[1].ID, Text: "Drafted middle.", FactIDs: []string{req.Facts[1].ID}},
				{SpeakerID: req.Cast[0].ID, Text: "Drafted end.", FactIDs: []string{req.Facts[0].ID}},
				{SpeakerID: req.Cast[1].ID, Text: "Drafted coda.", FactIDs: []string{req.Facts[1].ID}},
			}, nil
		},
		Now: fixedNow,
	})

	draft, err := manager.Draft(context.Background(), validCreateRequest())
	if err != nil {
		t.Fatalf("Draft returned error: %v", err)
	}
	if draft.Title != "Drafted Tale" || len(draft.Script) != 4 {
		t.Fatalf("unexpected draft %+v", draft)
	}
	if len(draft.FactCards) < 8 || len(draft.Cast) != 3 {
		t.Fatalf("expected scaffold in draft, got %d facts, %d cast", len(draft.FactCards), len(draft.Cast))
	}
	// Draft leaves no stored story behind.
	stories, err := manager.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(stories) != 0 {
		t.Fatalf("expected no stored stories after draft, got %+v", stories)
	}
}

func TestManagerProducesProvidedScript(t *testing.T) {
	scriptCalled := false
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(),
		Script: func(ctx context.Context, req ScriptRequest) (string, []ScriptLine, error) {
			scriptCalled = true
			return "", nil, NewError(CodeGroundingFailure, "should not be called")
		},
		StageDelay: time.Millisecond,
		Now:        fixedNow,
	})

	req := validCreateRequest()
	req.Title = "The Edited Tale"
	req.Script = []ScriptLine{
		{SpeakerID: "narrator", Text: "An edited opening.", FactIDs: []string{"fact-1"}},
		{SpeakerID: "nova", Text: "An edited question?", FactIDs: []string{"fact-2"}},
		{SpeakerID: "dr-lumen", Text: "An edited answer.", FactIDs: []string{"fact-3"}},
	}
	created, err := manager.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	status := waitStoryStatus(t, manager, created.ID, StatusComplete)
	if status.Manifest == nil {
		t.Fatalf("expected completed manifest, got %+v", status)
	}
	if scriptCalled {
		t.Fatalf("provided script must bypass the script writer")
	}
	if status.Manifest.Title != "The Edited Tale" {
		t.Fatalf("expected provided title, got %q", status.Manifest.Title)
	}
	if len(status.Manifest.Script) != 3 || status.Manifest.Script[0].Text != "An edited opening." {
		t.Fatalf("expected provided script, got %+v", status.Manifest.Script)
	}
}

func TestManagerRejectsUngroundedProvidedScript(t *testing.T) {
	manager := NewManager(ManagerOptions{
		RootDir:    t.TempDir(),
		StageDelay: time.Millisecond,
		Now:        fixedNow,
	})

	req := validCreateRequest()
	req.Script = []ScriptLine{
		{SpeakerID: "narrator", Text: "Cites an invented fact.", FactIDs: []string{"fact-999"}},
	}
	created, err := manager.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	status := waitStoryStatus(t, manager, created.ID, StatusFailed)
	if status.Error == nil || status.Error.Code != CodeGroundingFailure {
		t.Fatalf("expected grounding failure, got %+v", status.Error)
	}
}

func TestValidateCreateRequestCustomCast(t *testing.T) {
	req := validCreateRequest()
	req.Cast = []CastInput{
		{Name: "Captain Salt", Role: "gruff sea captain", VoiceID: "voice-1"},
		{Name: "First Mate"},
	}
	normalized, err := ValidateCreateRequest(req)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(normalized.Cast) != 2 {
		t.Fatalf("expected 2 cast members, got %+v", normalized.Cast)
	}
	if normalized.Cast[0].ID != "captain-salt" || normalized.Cast[0].VoiceID != "voice-1" || normalized.Cast[0].Role != "gruff sea captain" {
		t.Fatalf("unexpected first member %+v", normalized.Cast[0])
	}
	if normalized.Cast[1].ID != "first-mate" || normalized.Cast[1].VoiceID != "studio-default" {
		t.Fatalf("unexpected second member %+v", normalized.Cast[1])
	}
	if normalized.CastVoices["captain-salt"] != "voice-1" {
		t.Fatalf("expected voice map from cast, got %+v", normalized.CastVoices)
	}

	req.Cast = []CastInput{{Name: "Solo"}}
	if _, err := ValidateCreateRequest(req); !storyErrorIs(err, CodeInvalidRequest) {
		t.Fatalf("expected too-few-cast error, got %v", err)
	}
	req.Cast = []CastInput{{Name: "Twin"}, {Name: "Twin"}}
	if _, err := ValidateCreateRequest(req); !storyErrorIs(err, CodeInvalidRequest) {
		t.Fatalf("expected duplicate-id error, got %v", err)
	}
}

func TestValidateCreateRequestCastVoices(t *testing.T) {
	req := validCreateRequest()
	req.CastVoices = map[string]string{"narrator": "voice-1", "nova": " ", "dr-lumen": "voice-2"}
	normalized, err := ValidateCreateRequest(req)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(normalized.CastVoices) != 2 || normalized.CastVoices["narrator"] != "voice-1" || normalized.CastVoices["dr-lumen"] != "voice-2" {
		t.Fatalf("unexpected normalized cast voices %+v", normalized.CastVoices)
	}

	req.CastVoices = map[string]string{"villain": "voice-3"}
	if _, err := ValidateCreateRequest(req); !storyErrorIs(err, CodeInvalidRequest) {
		t.Fatalf("expected invalid cast key error, got %v", err)
	}
}

func validCreateRequest() CreateRequest {
	return CreateRequest{
		Subject:       "how stars are born",
		TargetSeconds: 90,
		SourceMode:    "curated",
		VoiceMode:     "placeholder",
		Sources: []SourceInput{
			{ID: "src-1", Title: "NASA Science: Star Basics", URL: "https://science.nasa.gov/universe/stars/", Excerpt: "Stars form inside molecular clouds of gas and dust. Cold cloud conditions help gas clump into denser pockets. As clumps gain mass, gravity can make them collapse."},
			{ID: "src-2", Title: "NASA Webb: Fiery Hourglass", URL: "https://science.nasa.gov/missions/webb/nasas-webb-catches-fiery-hourglass-as-new-star-forms/", Excerpt: "A forming protostar gathers material from its surrounding molecular cloud. Falling material spirals inward and forms an accretion disk. The disk feeds material onto the protostar."},
			{ID: "src-3", Title: "NASA Hubble: Planet-Forming Disks", URL: "https://science.nasa.gov/missions/hubble/hubbles-album-of-planet-forming-disks/", Excerpt: "Some falling material forms a rotating disk around the protostar. Jets from magnetic poles are part of star formation. Jets help carry away angular momentum so material can continue collecting."},
		},
	}
}

// validSketchRequest is the sketch-mode counterpart: a premise and a style,
// no sources at all.
func validSketchRequest() CreateRequest {
	return CreateRequest{
		Subject:       "a shop that only sells apologies",
		Mode:          ModeSketch,
		Premise:       "A customer wants to return an apology that did not fit.",
		Style:         "1960s BBC radio comedy: fast, silly, groan-worthy puns.",
		TargetSeconds: 60,
		VoiceMode:     "placeholder",
	}
}

func fixedNow() time.Time {
	return time.Date(2026, 7, 6, 13, 0, 0, 0, time.UTC)
}

func storyErrorIs(err error, code ErrorCode) bool {
	var storyErr *StoryError
	return errors.As(err, &storyErr) && storyErr.Code == code
}

func waitStoryStatus(t *testing.T, manager *Manager, id string, want Status) StatusResponse {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, ok, err := manager.Status(id)
		if err != nil {
			t.Fatalf("Status returned error: %v", err)
		}
		if !ok {
			t.Fatalf("story %s not found", id)
		}
		if status.Status == want {
			return status
		}
		if status.Status == StatusFailed {
			t.Fatalf("story failed: %+v", status.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for status %s", want)
	return StatusResponse{}
}

func waitUntilSubmitSucceeds(t *testing.T, manager *Manager) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		created, err := manager.Submit(context.Background(), validCreateRequest())
		if err == nil {
			_, _ = manager.Cancel(created.ID)
			return
		}
		if !storyErrorIs(err, CodeStoryBusy) {
			t.Fatalf("unexpected submit error while waiting for cancelled worker: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for cancelled worker to release active slot")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
