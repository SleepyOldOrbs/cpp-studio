package storybuilder

import (
	"context"
	"cpp-studio/internal/story"
	"cpp-studio/internal/wav"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestReadyDialogueTrimPreservesSourceAndRenders(t *testing.T) {
	s, p := buildTestProject(t, []TimelineClip{{ID: "line_1", Type: ClipTypeDialogue, Label: "Hello", Text: "Hello.", DurationMS: 1000}})
	input, err := s.BeginDialogueBuild(p.ID, "line_1")
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.CompleteDialogueBuild(p.ID, input, wav.SyntheticTone(16000))
	if err != nil {
		t.Fatal(err)
	}
	sourceID := p.Tracks[0].Clips[0].SourceID
	p.Tracks[0].Clips[0].SourceInMS = 100
	p.Tracks[0].Clips[0].DurationMS = 900
	p.Tracks[0].Clips[0].SourceID = "injected_source"
	p, err = s.Update(p.ID, ProjectUpdate{Name: p.Name, Revision: p.Revision, Tracks: p.Tracks, TimelineDurationMS: p.TimelineDurationMS})
	if err != nil {
		t.Fatalf("valid trim rejected: %v", err)
	}
	clip := p.Tracks[0].Clips[0]
	if clip.SourceID != sourceID || clip.SourceInMS != 100 || clip.SourceOutMS != 1000 || clip.DurationMS != 900 {
		t.Fatalf("wrong source/trim: %+v", clip)
	}
	rendered, err := s.Render(context.Background(), p.ID, p.Revision)
	if err != nil {
		t.Fatal(err)
	}
	p = rendered.Project
	p.Tracks[0].Clips[0].SourceOutMS = 1100
	p.Tracks[0].Clips[0].DurationMS = 1000
	if _, err := s.Update(p.ID, ProjectUpdate{Name: p.Name, Revision: p.Revision, Tracks: p.Tracks, TimelineDurationMS: p.TimelineDurationMS}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("out-of-source trim accepted: %v", err)
	}
}

func TestSlowRenderAndExportAllowOtherSavesAndRejectStalePublication(t *testing.T) {
	for _, operation := range []string{"render", "export"} {
		for _, mutation := range []string{"other", "edit", "delete"} {
			t.Run(operation+"/"+mutation, func(t *testing.T) {
				s := NewStore(t.TempDir())
				p, err := s.Create("Target")
				if err != nil {
					t.Fatal(err)
				}
				other, err := s.Create("Other")
				if err != nil {
					t.Fatal(err)
				}
				if operation == "export" {
					r, err := s.Render(context.Background(), p.ID, p.Revision)
					if err != nil {
						t.Fatal(err)
					}
					p = r.Project
				}
				entered := make(chan struct{})
				release := make(chan struct{})
				var once sync.Once
				unblock := func() { once.Do(func() { close(release) }) }
				t.Cleanup(unblock)
				if operation == "render" {
					s.masterRender = func(_ context.Context, data []byte) ([]byte, *story.Master, error) {
						close(entered)
						<-release
						return data, nil, nil
					}
				} else {
					s.transcode = func(_ context.Context, _, out, _, _ string) error {
						close(entered)
						<-release
						return os.WriteFile(out, []byte("ID3\x03\x00\x00\x00payload"), 0600)
					}
				}
				finished := make(chan error, 1)
				go func() {
					var err error
					if operation == "render" {
						_, err = s.Render(context.Background(), p.ID, p.Revision)
					} else {
						_, err = s.ExportRender(context.Background(), p.ID, p.Revision, 1, "mp3", "128k")
					}
					finished <- err
				}()
				select {
				case <-entered:
				case <-time.After(2 * time.Second):
					t.Fatal("expensive operation did not start")
				}
				mutated := make(chan error, 1)
				started := time.Now()
				go func() {
					target := p
					if mutation == "other" {
						target = other
					}
					if mutation == "delete" {
						mutated <- s.Delete(p.ID)
						return
					}
					_, err := s.Update(target.ID, ProjectUpdate{Name: "Edited", Revision: target.Revision, Tracks: target.Tracks, TimelineDurationMS: target.TimelineDurationMS})
					mutated <- err
				}()
				select {
				case err := <-mutated:
					if err != nil {
						t.Errorf("mutation: %v", err)
					}
					t.Logf("save/delete completed while %s remained blocked: %s", operation, time.Since(started))
				case <-time.After(time.Second):
					unblock()
					<-mutated
					<-finished
					t.Fatal("unrelated mutation blocked by expensive operation")
				}
				unblock()
				err = <-finished
				if mutation == "other" && err != nil {
					t.Fatalf("independent operation failed: %v", err)
				}
				if mutation == "edit" && !errors.Is(err, ErrConflict) {
					t.Fatalf("stale operation must conflict: %v", err)
				}
				if mutation == "delete" {
					if err == nil {
						t.Fatal("deleted project published")
					}
					if _, err := os.Stat(filepath.Join(s.rootDir, p.ID)); !os.IsNotExist(err) {
						t.Fatalf("deleted project recreated: %v", err)
					}
				}
				if mutation == "edit" {
					current, _, err := s.Get(p.ID)
					if err != nil {
						t.Fatal(err)
					}
					if current.Name != "Edited" {
						t.Fatal("concurrent edit lost")
					}
					if operation == "render" && len(current.Renders) != 0 {
						t.Fatal("stale render recorded")
					}
					if operation == "export" && len(current.Renders[0].Exports) != 0 {
						t.Fatal("stale export recorded")
					}
				}
			})
		}
	}
}
