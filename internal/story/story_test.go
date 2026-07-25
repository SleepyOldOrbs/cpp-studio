package story

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
