package story

import (
	"context"
	"errors"
	"fmt"
	"math"
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
	manifest.Renders = []Render{{
		Revision: 1,
		Exports:  []Export{{Format: "mp3", Bitrate: "192k", URL: "/v1/stories/" + manifest.ID + "/artifact/renders/render-001.mp3"}},
	}}
	if err := store.SaveManifest(manifest); err != nil {
		t.Fatalf("SaveManifest returned error: %v", err)
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
	if len(list[0].Exports) != 1 || list[0].Exports[0].Format != "mp3" {
		t.Fatalf("expected exports in story summary, got %+v", list[0].Exports)
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

func TestProductionKeepsTakesWhenSynthesisFails(t *testing.T) {
	rootDir := t.TempDir()
	calls := 0
	manager := NewManager(ManagerOptions{
		RootDir: rootDir,
		Synthesize: func(ctx context.Context, text string, voiceID string) ([]byte, error) {
			calls++
			if calls == 3 {
				return nil, errors.New("the engine died mid-episode")
			}
			return wav.SyntheticTone(wav.ToneSampleRate / 2), nil
		},
		StageDelay: time.Millisecond,
		Now:        fixedNow,
	})

	req := validSketchRequest()
	req.VoiceMode = "fixed"
	created, err := manager.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var status StatusResponse
	for time.Now().Before(deadline) {
		status, _, _ = manager.Status(created.ID)
		if status.Status == StatusFailed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if status.Status != StatusFailed || status.Error == nil || status.Error.Code != CodeSynthesisFailure {
		t.Fatalf("expected a synthesis failure, got %+v", status)
	}

	// The point of writing takes as they are made: the two lines recorded
	// before the failure are on disk in the work-in-progress directory,
	// waiting for a resume — not lost with the job.
	wip := filepath.Join(rootDir, "."+created.ID+".wip")
	for _, lineID := range []string{"line-001", "line-002"} {
		takePath := filepath.Join(wip, "lines", lineID, "take-001.wav")
		if _, err := os.Stat(takePath); err != nil {
			t.Fatalf("expected %s to survive the failure: %v", takePath, err)
		}
	}
	// And the story itself never came to exist.
	if _, err := os.Stat(filepath.Join(rootDir, created.ID)); !os.IsNotExist(err) {
		t.Fatalf("a failed story must not be published: %v", err)
	}
}

func TestCancelKeepsRecordedTakesForResume(t *testing.T) {
	rootDir := t.TempDir()
	idCh := make(chan string, 1)
	var manager *Manager
	calls := 0
	manager = NewManager(ManagerOptions{
		RootDir:              rootDir,
		SynthesisFingerprint: "fp-1",
		Synthesize: func(ctx context.Context, text string, voiceID string) ([]byte, error) {
			calls++
			if calls == 3 {
				// Cancel mid-production: two takes are already on disk. At
				// episode scale a cancel is a pause, not a shredder — the
				// recorded work stays behind as an interrupted story.
				if _, err := manager.Cancel(<-idCh); err != nil {
					t.Errorf("Cancel returned error: %v", err)
				}
			}
			return wav.SyntheticTone(wav.ToneSampleRate / 2), nil
		},
		StageDelay: time.Millisecond,
		Now:        fixedNow,
	})

	req := validSketchRequest()
	req.VoiceMode = "fixed"
	created, err := manager.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	idCh <- created.ID

	wip := filepath.Join(rootDir, "."+created.ID+".wip")
	deadline := time.Now().Add(2 * time.Second)
	var status StatusResponse
	for time.Now().Before(deadline) {
		status, _, _ = manager.Status(created.ID)
		if status.Status == StatusCancelled {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if status.Status != StatusCancelled {
		t.Fatalf("expected the job to cancel, got %+v", status)
	}
	if _, err := os.Stat(filepath.Join(wip, "lines")); err != nil {
		t.Fatalf("a cancel with recorded takes must keep them: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rootDir, created.ID)); !os.IsNotExist(err) {
		t.Fatalf("a cancelled story must not be published: %v", err)
	}
	// Cancel marks the status immediately, while the worker releases the
	// production slot asynchronously. Wait for that lifecycle boundary before
	// asserting that the WIP lists as resumable or starting its replacement.
	deadline = time.Now().Add(5 * time.Second)
	for manager.Active() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if manager.Active() {
		t.Fatal("cancelled production did not release the active job slot")
	}

	// The kept directory is an ordinary interruption: it lists, and a
	// resume finishes the episode with the recorded takes spliced in.
	summaries, err := manager.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	interrupted := false
	for _, summary := range summaries {
		if summary.ID == created.ID && summary.Status == StatusInterrupted {
			interrupted = true
		}
	}
	if !interrupted {
		t.Fatalf("cancelled production should list as interrupted: %+v", summaries)
	}
	if _, err := manager.Resume(context.Background(), created.ID); err != nil {
		t.Fatalf("Resume returned error: %v", err)
	}
	final := waitStoryStatus(t, manager, created.ID, StatusComplete)
	for i, line := range final.Manifest.Script {
		if line.CurrentTake == "" {
			t.Fatalf("script[%d] unrecorded after resume: %+v", i, line)
		}
	}
	if _, err := os.Stat(wip); !os.IsNotExist(err) {
		t.Fatalf("wip should be renamed away after the resumed run: %v", err)
	}
}

func TestActiveTracksTheProductionLifecycle(t *testing.T) {
	rootDir := t.TempDir()
	reached := make(chan struct{})
	release := make(chan struct{})
	manager := NewManager(ManagerOptions{
		RootDir:              rootDir,
		SynthesisFingerprint: "fp-1",
		Synthesize: func(ctx context.Context, text string, voiceID string) ([]byte, error) {
			select {
			case <-reached:
			default:
				close(reached)
			}
			<-release
			return wav.SyntheticTone(wav.ToneSampleRate / 2), nil
		},
		StageDelay: time.Millisecond,
		Now:        fixedNow,
	})
	if manager.Active() {
		t.Fatal("expected no active production before any submit")
	}
	req := validSketchRequest()
	req.VoiceMode = "fixed"
	created, err := manager.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if !manager.Active() {
		t.Fatal("expected an active production immediately after submit")
	}
	<-reached
	close(release)
	waitStoryStatus(t, manager, created.ID, StatusComplete)
	deadline := time.Now().Add(2 * time.Second)
	for manager.Active() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if manager.Active() {
		t.Fatal("expected the active slot released after completion")
	}
}

func TestCancelDuringSynthesisReleasesTheJobSlot(t *testing.T) {
	// A real engine call respects its context: cancelling mid-line makes
	// the synthesizer return ctx.Err() rather than a clip. That error path
	// races the cancelled status into fail(), which must still release the
	// job slot — the live smoke caught it staying "active" forever,
	// unlistable and unresumable.
	rootDir := t.TempDir()
	reached := make(chan struct{})
	var manager *Manager
	calls := 0
	manager = NewManager(ManagerOptions{
		RootDir:              rootDir,
		SynthesisFingerprint: "fp-1",
		Synthesize: func(ctx context.Context, text string, voiceID string) ([]byte, error) {
			calls++
			if calls == 3 {
				close(reached)
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return wav.SyntheticTone(wav.ToneSampleRate / 2), nil
		},
		StageDelay: time.Millisecond,
		Now:        fixedNow,
	})

	req := validSketchRequest()
	req.VoiceMode = "fixed"
	created, err := manager.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	<-reached
	if _, err := manager.Cancel(created.ID); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}

	// The job slot must come free: a resume of the kept work succeeds
	// rather than reporting the story busy for the life of the process.
	deadline := time.Now().Add(2 * time.Second)
	var resumeErr error
	for time.Now().Before(deadline) {
		_, resumeErr = manager.Resume(context.Background(), created.ID)
		if resumeErr == nil || !storyErrorIs(resumeErr, CodeStoryBusy) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if resumeErr != nil {
		t.Fatalf("Resume after mid-synthesis cancel returned %v", resumeErr)
	}
	waitStoryStatus(t, manager, created.ID, StatusComplete)
}

func TestCancelBeforeAnyTakeLeavesNoResidue(t *testing.T) {
	// A placeholder story records nothing, so cancelling it mid-flight
	// must sweep its work-in-progress directory clean rather than leaving
	// an unresumable husk in the list.
	rootDir := t.TempDir()
	manager := NewManager(ManagerOptions{
		RootDir:    rootDir,
		StageDelay: 40 * time.Millisecond,
		Now:        fixedNow,
	})
	created, err := manager.Submit(context.Background(), validSketchRequest())
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	if _, err := manager.Cancel(created.ID); err != nil {
		t.Fatalf("Cancel returned error: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	wip := filepath.Join(rootDir, "."+created.ID+".wip")
	for time.Now().Before(deadline) {
		_, wipErr := os.Stat(wip)
		if os.IsNotExist(wipErr) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := os.Stat(wip); !os.IsNotExist(err) {
		t.Fatalf("a cancel with nothing recorded should leave nothing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rootDir, created.ID)); !os.IsNotExist(err) {
		t.Fatalf("a cancelled story must not be published: %v", err)
	}
}

func TestAuditionStitchesOneScene(t *testing.T) {
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(),
		Synthesize: func(ctx context.Context, text string, voiceID string) ([]byte, error) {
			return wav.SyntheticTone(wav.ToneSampleRate / 2), nil
		},
		StageDelay: time.Millisecond,
		Now:        fixedNow,
	})
	req := sceneEpisodeRequest()
	req.VoiceMode = "fixed"
	created, err := manager.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	status := waitStoryStatus(t, manager, created.ID, StatusComplete)
	manifest := *status.Manifest

	audio, err := manager.Audition(created.ID, "the-shop")
	if err != nil {
		t.Fatalf("Audition returned error: %v", err)
	}
	if err := wav.ValidateBytes(audio); err != nil {
		t.Fatalf("audition is not a WAV: %v", err)
	}
	full, err := manager.Audition(created.ID, "the-return")
	if err != nil {
		t.Fatalf("Audition of second scene returned error: %v", err)
	}
	// Two lines per scene, equal takes: the two scenes audition to the
	// same length, and both are shorter than the published whole.
	if len(audio) != len(full) {
		t.Fatalf("expected equal-length scene auditions, got %d vs %d", len(audio), len(full))
	}
	render, err := manager.ArtifactPath(created.ID, StoryArtifactName)
	if err != nil {
		t.Fatalf("artifact path: %v", err)
	}
	info, err := os.Stat(render)
	if err != nil {
		t.Fatalf("stat story.wav: %v", err)
	}
	if int64(len(audio)) >= info.Size() {
		t.Fatalf("a one-scene audition should be shorter than the episode: %d vs %d", len(audio), info.Size())
	}

	// A muted line drops out of its scene's audition.
	if _, err := manager.EditLine(created.ID, manifest.Script[0].ID, LinePatch{Muted: boolPtr(true)}); err != nil {
		t.Fatalf("EditLine returned error: %v", err)
	}
	muted, err := manager.Audition(created.ID, "the-shop")
	if err != nil {
		t.Fatalf("Audition after mute returned error: %v", err)
	}
	if len(muted) >= len(audio) {
		t.Fatalf("muting a line should shorten the audition: %d vs %d", len(muted), len(audio))
	}

	if _, err := manager.Audition(created.ID, "no-such-scene"); !storyErrorIs(err, CodeSceneNotFound) {
		t.Fatalf("expected scene not found, got %v", err)
	}
	if _, err := manager.Audition("story_20260101_000000_009", "the-shop"); !storyErrorIs(err, CodeNotFound) {
		t.Fatalf("expected story not found, got %v", err)
	}
}

func boolPtr(v bool) *bool { return &v }

func TestValidateCreateRequestEpisodeScale(t *testing.T) {
	// The wall the episodes plan exists to break: a 28-minute radio
	// half-hour is roughly 330 lines. It must validate.
	req := validSketchRequest()
	req.VoiceMode = "fixed"
	req.TargetSeconds = 1680
	req.Title = "A Half Hour"
	req.Cast = []CastInput{{Name: "Kenneth"}, {Name: "Hugh"}}
	for i := 0; i < 330; i++ {
		speaker := "kenneth"
		if i%2 == 1 {
			speaker = "hugh"
		}
		req.Script = append(req.Script, ScriptLine{SpeakerID: speaker, Text: fmt.Sprintf("Line %d of the episode.", i+1)})
	}
	if _, err := ValidateCreateRequest(req); err != nil {
		t.Fatalf("an episode-sized request must validate: %v", err)
	}

	req.TargetSeconds = MaxTargetSeconds + 1
	if _, err := ValidateCreateRequest(req); !storyErrorIs(err, CodeTargetSecondsInvalid) {
		t.Fatalf("expected the target-seconds cap to hold, got %v", err)
	}
	req.TargetSeconds = 1680
	for len(req.Script) <= MaxScriptLines {
		req.Script = append(req.Script, ScriptLine{SpeakerID: "kenneth", Text: "Overflow."})
	}
	if _, err := ValidateCreateRequest(req); !storyErrorIs(err, CodeInvalidRequest) {
		t.Fatalf("expected the lines cap to hold, got %v", err)
	}
}

// interruptedProduction runs a fixed-voice production that fails after two
// takes, leaving a resumable work-in-progress directory behind. It returns
// the shared root directory and the story id.
func interruptedProduction(t *testing.T, fingerprint string) (string, string) {
	t.Helper()
	rootDir := t.TempDir()
	calls := 0
	manager := NewManager(ManagerOptions{
		RootDir:              rootDir,
		SynthesisFingerprint: fingerprint,
		Synthesize: func(ctx context.Context, text string, voiceID string) ([]byte, error) {
			calls++
			if calls == 3 {
				return nil, errors.New("the process died here")
			}
			return wav.SyntheticTone(wav.ToneSampleRate / 2), nil
		},
		StageDelay: time.Millisecond,
		Now:        fixedNow,
	})
	req := validSketchRequest()
	req.VoiceMode = "fixed"
	created, err := manager.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, _, _ := manager.Status(created.ID)
		if status.Status == StatusFailed {
			return rootDir, created.ID
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("production did not fail in time")
	return "", ""
}

func TestResumeFinishesInterruptedProduction(t *testing.T) {
	rootDir, id := interruptedProduction(t, "fp-1")

	// A fresh manager over the same root is a restarted process. Same
	// synthesis fingerprint: the two takes already on disk are kept.
	resumed := 0
	later := func() time.Time { return fixedNow().Add(time.Hour) }
	manager := NewManager(ManagerOptions{
		RootDir:              rootDir,
		SynthesisFingerprint: "fp-1",
		Synthesize: func(ctx context.Context, text string, voiceID string) ([]byte, error) {
			resumed++
			return wav.SyntheticTone(wav.ToneSampleRate / 2), nil
		},
		StageDelay: time.Millisecond,
		Now:        later,
	})

	response, err := manager.Resume(context.Background(), id)
	if err != nil {
		t.Fatalf("Resume returned error: %v", err)
	}
	if response.ID != id {
		t.Fatalf("a resume continues the same story, got %q", response.ID)
	}
	status := waitStoryStatus(t, manager, id, StatusComplete)
	manifest := *status.Manifest

	// The fixture sketch is nine lines; two were already performed.
	if resumed != len(manifest.Script)-2 {
		t.Fatalf("expected %d lines synthesized on resume, got %d", len(manifest.Script)-2, resumed)
	}
	for i, line := range manifest.Script {
		if len(line.Takes) != 1 || line.CurrentTake == "" {
			t.Fatalf("script[%d] should have its take after resume, got %+v", i, line)
		}
		if line.Takes[0].Fingerprint != "fp-1" {
			t.Fatalf("script[%d] take should carry the fingerprint, got %+v", i, line.Takes[0])
		}
	}
	// Kept takes keep their original recording time; new ones carry the
	// resume's clock. That difference is the proof nothing was re-made.
	if !manifest.Script[0].Takes[0].CreatedAt.Equal(fixedNow()) {
		t.Fatalf("kept take should keep its original time, got %v", manifest.Script[0].Takes[0].CreatedAt)
	}
	if !manifest.Script[2].Takes[0].CreatedAt.Equal(later()) {
		t.Fatalf("resumed take should carry the resume time, got %v", manifest.Script[2].Takes[0].CreatedAt)
	}
	// The story is published and the work-in-progress directory is gone.
	if _, err := os.Stat(filepath.Join(rootDir, id, StoryArtifactName)); err != nil {
		t.Fatalf("resumed story should be published: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rootDir, "."+id+".wip")); !os.IsNotExist(err) {
		t.Fatalf("wip directory should be renamed away: %v", err)
	}
}

func TestResumeResynthesizesWhenFingerprintChanged(t *testing.T) {
	rootDir, id := interruptedProduction(t, "fp-old")

	resumed := 0
	manager := NewManager(ManagerOptions{
		RootDir:              rootDir,
		SynthesisFingerprint: "fp-new",
		Synthesize: func(ctx context.Context, text string, voiceID string) ([]byte, error) {
			resumed++
			return wav.SyntheticTone(wav.ToneSampleRate / 2), nil
		},
		StageDelay: time.Millisecond,
		Now:        fixedNow,
	})
	if _, err := manager.Resume(context.Background(), id); err != nil {
		t.Fatalf("Resume returned error: %v", err)
	}
	status := waitStoryStatus(t, manager, id, StatusComplete)

	// The engine changed under the takes: every line is performed again.
	// Splicing two engines into one episode is exactly what must not happen.
	if resumed != len(status.Manifest.Script) {
		t.Fatalf("expected every line re-synthesized under the new fingerprint, got %d of %d", resumed, len(status.Manifest.Script))
	}
}

func TestResumeRefusals(t *testing.T) {
	t.Run("unknown story", func(t *testing.T) {
		manager := NewManager(ManagerOptions{
			RootDir:    t.TempDir(),
			Synthesize: func(ctx context.Context, text string, voiceID string) ([]byte, error) { return nil, nil },
			Now:        fixedNow,
		})
		if _, err := manager.Resume(context.Background(), "story_20260101_000000_001"); !storyErrorIs(err, CodeNotFound) {
			t.Fatalf("expected not found, got %v", err)
		}
	})
	t.Run("no synthesizer", func(t *testing.T) {
		manager := NewManager(ManagerOptions{RootDir: t.TempDir(), Now: fixedNow})
		if _, err := manager.Resume(context.Background(), "story_20260101_000000_001"); !storyErrorIs(err, CodeSynthesisFailure) {
			t.Fatalf("expected synthesis failure, got %v", err)
		}
	})
	t.Run("placeholder production", func(t *testing.T) {
		rootDir := t.TempDir()
		store := NewStore(rootDir)
		manifest, _, err := BuildFixtureManifest("story_20260101_000000_001", mustNormalize(t, validSketchRequest()), fixedNow())
		if err != nil {
			t.Fatalf("build fixture manifest: %v", err)
		}
		manifest.VoiceMode = "placeholder"
		if err := store.BeginWIP(manifest.ID); err != nil {
			t.Fatalf("BeginWIP: %v", err)
		}
		if err := store.SaveManifestWIP(manifest); err != nil {
			t.Fatalf("SaveManifestWIP: %v", err)
		}
		manager := NewManager(ManagerOptions{
			RootDir:    rootDir,
			Synthesize: func(ctx context.Context, text string, voiceID string) ([]byte, error) { return nil, nil },
			Now:        fixedNow,
		})
		if _, err := manager.Resume(context.Background(), manifest.ID); !storyErrorIs(err, CodeNotResumable) {
			t.Fatalf("expected not resumable, got %v", err)
		}
	})
}

func TestInterruptedProductionsListStatusAndDiscard(t *testing.T) {
	rootDir, id := interruptedProduction(t, "fp-1")

	// A fresh manager sees the interruption in the list and in status.
	manager := NewManager(ManagerOptions{RootDir: rootDir, Now: fixedNow})
	summaries, err := manager.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	found := false
	for _, summary := range summaries {
		if summary.ID == id {
			found = true
			if summary.Status != StatusInterrupted {
				t.Fatalf("expected interrupted status in the list, got %+v", summary)
			}
		}
	}
	if !found {
		t.Fatalf("interrupted production missing from the list: %+v", summaries)
	}

	status, ok, err := manager.Status(id)
	if err != nil || !ok {
		t.Fatalf("Status returned %v ok=%v", err, ok)
	}
	if status.Status != StatusInterrupted || status.Manifest == nil {
		t.Fatalf("expected interrupted status with manifest, got %+v", status)
	}
	// Two of nine lines were performed before the failure.
	want := 2.0 / float64(len(status.Manifest.Script))
	if math.Abs(status.Progress-want) > 0.001 {
		t.Fatalf("expected progress %.3f, got %.3f", want, status.Progress)
	}

	if err := manager.Discard(id); err != nil {
		t.Fatalf("Discard returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rootDir, "."+id+".wip")); !os.IsNotExist(err) {
		t.Fatalf("discard should remove the wip directory: %v", err)
	}
	if err := manager.Discard(id); !storyErrorIs(err, CodeNotFound) {
		t.Fatalf("a second discard has nothing to remove, got %v", err)
	}
}

func mustNormalize(t *testing.T, req CreateRequest) NormalizedRequest {
	t.Helper()
	normalized, err := ValidateCreateRequest(req)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	return normalized
}

func TestValidateCreateRequestScenes(t *testing.T) {
	t.Run("resolves ids and slugs titles", func(t *testing.T) {
		req := sceneEpisodeRequest()
		normalized, err := ValidateCreateRequest(req)
		if err != nil {
			t.Fatalf("validate: %v", err)
		}
		if len(normalized.Scenes) != 2 {
			t.Fatalf("expected 2 scenes, got %+v", normalized.Scenes)
		}
		if normalized.Scenes[0].ID != "the-shop" || normalized.Scenes[0].Title != "The Shop" {
			t.Fatalf("unexpected first scene %+v", normalized.Scenes[0])
		}
		// The second scene declared no id: it slugs from the title exactly
		// as cast ids slug from names.
		if normalized.Scenes[1].ID != "the-return" {
			t.Fatalf("expected slugged id the-return, got %+v", normalized.Scenes[1])
		}
	})

	tests := []struct {
		name string
		edit func(*CreateRequest)
	}{
		{
			name: "duplicate scene id",
			edit: func(req *CreateRequest) {
				req.Scenes[1].ID = "the-shop"
			},
		},
		{
			name: "id and title both missing",
			edit: func(req *CreateRequest) {
				req.Scenes[1] = SceneInput{Premise: "a premise names nothing"}
			},
		},
		{
			name: "id outside the story-id alphabet",
			edit: func(req *CreateRequest) {
				req.Scenes[0].ID = "the/shop"
			},
		},
		{
			name: "too many scenes",
			edit: func(req *CreateRequest) {
				req.Scenes = nil
				for i := 0; i <= MaxScenes; i++ {
					req.Scenes = append(req.Scenes, SceneInput{ID: fmt.Sprintf("scene-%d", i+1)})
				}
			},
		},
		{
			name: "scene title too long",
			edit: func(req *CreateRequest) {
				req.Scenes[0].Title = strings.Repeat("x", MaxSceneTitleChars+1)
			},
		},
		{
			name: "scenes without a script",
			edit: func(req *CreateRequest) {
				req.Script = nil
			},
		},
		{
			// The cross-checks run at request time, not only at the
			// manifest gate: a knowably doomed script must not occupy the
			// story slot just to fail asynchronously.
			name: "interleaved scene runs",
			edit: func(req *CreateRequest) {
				req.Script[1].SceneID = "the-return"
				req.Script[2].SceneID = "the-shop"
			},
		},
		{
			name: "scene id without declared scenes",
			edit: func(req *CreateRequest) {
				req.Scenes = nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := sceneEpisodeRequest()
			tt.edit(&req)
			if _, err := ValidateCreateRequest(req); !storyErrorIs(err, CodeInvalidScenes) {
				t.Fatalf("expected %s, got %v", CodeInvalidScenes, err)
			}
		})
	}
}

func TestValidateManifestSceneInvariants(t *testing.T) {
	line := func(speaker, text, sceneID string) ScriptLine {
		return ScriptLine{SpeakerID: speaker, Text: text, SceneID: sceneID}
	}
	manifest := func(scenes []Scene, script []ScriptLine) Manifest {
		return Manifest{
			Mode: ModeSketch,
			Cast: []CastMember{
				{ID: "kenneth", DisplayName: "Kenneth", VoiceID: "studio-default"},
				{ID: "hugh", DisplayName: "Hugh", VoiceID: "studio-default"},
			},
			Scenes: scenes,
			Script: script,
		}
	}
	two := []Scene{{ID: "one"}, {ID: "two"}}

	if err := ValidateManifest(manifest(two, []ScriptLine{
		line("kenneth", "First.", "one"),
		line("hugh", "Second.", "one"),
		line("kenneth", "Third.", "two"),
	})); err != nil {
		t.Fatalf("contiguous ordered scenes should validate, got %v", err)
	}
	// The degenerate case: no scenes at all is exactly a pre-episode story.
	if err := ValidateManifest(manifest(nil, []ScriptLine{
		line("kenneth", "Alone.", ""),
	})); err != nil {
		t.Fatalf("sceneless story should validate, got %v", err)
	}

	tests := []struct {
		name   string
		scenes []Scene
		script []ScriptLine
	}{
		{
			name:   "scene id on a story with no scenes",
			scenes: nil,
			script: []ScriptLine{line("kenneth", "Lost.", "one")},
		},
		{
			name:   "line missing its scene id",
			scenes: two,
			script: []ScriptLine{line("kenneth", "First.", "one"), line("hugh", "Where am I?", ""), line("kenneth", "Third.", "two")},
		},
		{
			name:   "line naming an unknown scene",
			scenes: two,
			script: []ScriptLine{line("kenneth", "First.", "one"), line("hugh", "Second.", "three")},
		},
		{
			name:   "scene resuming after an interruption",
			scenes: two,
			script: []ScriptLine{line("kenneth", "First.", "one"), line("hugh", "Second.", "two"), line("kenneth", "Back again.", "one")},
		},
		{
			name:   "scenes out of declared order",
			scenes: two,
			script: []ScriptLine{line("kenneth", "First.", "two"), line("hugh", "Second.", "one")},
		},
		{
			name:   "declared scene with no lines",
			scenes: two,
			script: []ScriptLine{line("kenneth", "First.", "one")},
		},
		{
			name:   "duplicate declared scene id",
			scenes: []Scene{{ID: "one"}, {ID: "one"}},
			script: []ScriptLine{line("kenneth", "First.", "one")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateManifest(manifest(tt.scenes, tt.script)); !storyErrorIs(err, CodeInvalidScenes) {
				t.Fatalf("expected %s, got %v", CodeInvalidScenes, err)
			}
		})
	}
}

func TestManagerProducesMultiSceneEpisode(t *testing.T) {
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(),
		Synthesize: func(ctx context.Context, text string, voiceID string) ([]byte, error) {
			return wav.SyntheticTone(wav.ToneSampleRate / 2), nil
		},
		StageDelay: time.Millisecond,
		Now:        fixedNow,
	})

	req := sceneEpisodeRequest()
	req.VoiceMode = "fixed"
	created, err := manager.Submit(context.Background(), req)
	if err != nil {
		t.Fatalf("Submit returned error: %v", err)
	}
	status := waitStoryStatus(t, manager, created.ID, StatusComplete)
	if status.Manifest == nil {
		t.Fatalf("expected completed manifest, got %+v", status)
	}
	manifest := *status.Manifest
	if len(manifest.Scenes) != 2 || manifest.Scenes[0].ID != "the-shop" || manifest.Scenes[1].ID != "the-return" {
		t.Fatalf("expected both scenes stored, got %+v", manifest.Scenes)
	}
	for i, l := range manifest.Script {
		if l.ID == "" || l.SceneID == "" {
			t.Fatalf("script[%d] should have line and scene ids, got %+v", i, l)
		}
		if len(l.Takes) != 1 || l.CurrentTake != l.Takes[0].ID {
			t.Fatalf("script[%d] should have its first take current, got %+v", i, l)
		}
	}

	// A take-room mutation on a scene-two line reloads the manifest from
	// disk: scenes must survive the JSON round trip and the republish.
	sceneTwoLine := manifest.Script[len(manifest.Script)-1]
	reloaded, take, err := manager.Retake(context.Background(), manifest.ID, sceneTwoLine.ID)
	if err != nil {
		t.Fatalf("Retake returned error: %v", err)
	}
	if take.ID != "take-002" {
		t.Fatalf("expected a second take, got %+v", take)
	}
	if len(reloaded.Scenes) != 2 {
		t.Fatalf("scenes should survive reload, got %+v", reloaded.Scenes)
	}
}

func TestManagerDraftEchoesScenes(t *testing.T) {
	manager := NewManager(ManagerOptions{RootDir: t.TempDir(), Now: fixedNow})
	draft, err := manager.Draft(context.Background(), sceneEpisodeRequest())
	if err != nil {
		t.Fatalf("Draft returned error: %v", err)
	}
	if len(draft.Scenes) != 2 || draft.Scenes[1].ID != "the-return" {
		t.Fatalf("expected the resolved scene list in the draft, got %+v", draft.Scenes)
	}
}

// sceneEpisodeRequest is a two-scene sketch episode submitted through the
// draft → edit → produce flow: declared scenes, a script whose lines name
// them, and a cast to speak it.
func sceneEpisodeRequest() CreateRequest {
	return CreateRequest{
		Subject: "a shop that only sells apologies",
		Mode:    ModeSketch,
		Premise: "A customer wants to return an apology that did not fit.",
		Style:   "1960s BBC radio comedy: fast, silly, groan-worthy puns.",
		Cast: []CastInput{
			{Name: "Kenneth", Role: "the browbeaten customer"},
			{Name: "Hugh", Role: "the evasive shopkeeper"},
		},
		Title: "The Apology Shop",
		Scenes: []SceneInput{
			{ID: "the-shop", Title: "The Shop", Premise: "Kenneth attempts the return."},
			{Title: "The Return", Premise: "The apology comes back anyway."},
		},
		Script: []ScriptLine{
			{SpeakerID: "kenneth", Text: "I'd like to return this apology.", SceneID: "the-shop"},
			{SpeakerID: "hugh", Text: "All apologies are final, sir.", SceneID: "the-shop"},
			{SpeakerID: "kenneth", Text: "Then I apologise for asking.", SceneID: "the-return"},
			{SpeakerID: "hugh", Text: "That one I can take in part exchange.", SceneID: "the-return"},
		},
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
	deadline := time.Now().Add(5 * time.Second)
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
