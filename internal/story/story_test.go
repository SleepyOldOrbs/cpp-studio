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
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(),
		Synthesize: func(ctx context.Context, text string) ([]byte, error) {
			synthesized = append(synthesized, text)
			return wav.SyntheticTone(wav.ToneSampleRate), nil // one second per line
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
	status := waitStoryStatus(t, manager, created.ID, StatusComplete)

	if status.Manifest == nil {
		t.Fatalf("expected completed manifest, got %+v", status)
	}
	if len(synthesized) != len(status.Manifest.Script) {
		t.Fatalf("expected %d synthesized lines, got %d", len(status.Manifest.Script), len(synthesized))
	}
	if synthesized[0] != status.Manifest.Script[0].Text {
		t.Fatalf("expected first synthesized line to match script, got %q", synthesized[0])
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
		Synthesize: func(ctx context.Context, text string) ([]byte, error) {
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
