package storybuilder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"cpp-studio/internal/story"
	"cpp-studio/internal/wav"
)

const MaxRenderedWAVBytes = story.MaxGeneratedWAVBytes

type RenderResponse struct {
	Project Project        `json:"project"`
	Render  RenderRevision `json:"render"`
}

// Render validates the complete audible arrangement before it mixes or
// publishes anything, then records one new immutable project-owned WAV.
func (s *Store) Render(ctx context.Context, id string, expectedRevision int) (RenderResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !validProjectID(id) {
		return RenderResponse{}, ErrNotFound
	}
	project, ok, err := s.Get(id)
	if err != nil {
		return RenderResponse{}, err
	}
	if !ok {
		return RenderResponse{}, ErrNotFound
	}
	if expectedRevision != project.Revision {
		return RenderResponse{}, ErrConflict
	}
	placements := make([]wav.TimelinePlacement, 0)
	for _, track := range project.Tracks {
		if track.Muted {
			continue
		}
		for _, clip := range track.Clips {
			if clip.Type == ClipTypeSilence {
				continue
			}
			if clip.Type == ClipTypeDialogue && clip.Status != DialogueStatusReady {
				return RenderResponse{}, fmt.Errorf("%w: clip %s", ErrRenderNotReady, clip.ID)
			}
			if clip.MediaError != "" || !validTimelineID(clip.SourceID) {
				return RenderResponse{}, fmt.Errorf("%w: clip %s", ErrProjectMediaNotFound, clip.ID)
			}
			directory := "media"
			if clip.Type == ClipTypeDialogue {
				directory = "takes"
			}
			data, err := os.ReadFile(filepath.Join(s.rootDir, id, directory, clip.SourceID+".wav"))
			if err != nil {
				return RenderResponse{}, fmt.Errorf("%w: clip %s", ErrProjectMediaNotFound, clip.ID)
			}
			duration, err := wav.Duration(data)
			if err != nil || duration.Milliseconds() != clip.SourceDurationMS {
				return RenderResponse{}, fmt.Errorf("%w: clip %s", ErrProjectMediaNotFound, clip.ID)
			}
			placements = append(placements, wav.TimelinePlacement{
				Data: data, StartMS: clip.StartMS, SourceInMS: clip.SourceInMS, DurationMS: clip.DurationMS,
			})
		}
	}
	if err := ctx.Err(); err != nil {
		return RenderResponse{}, err
	}
	mixed, err := wav.MixTimeline(project.TimelineDurationMS, placements)
	if err != nil {
		return RenderResponse{}, fmt.Errorf("mix Story Builder Project: %w", err)
	}
	if len(mixed) > MaxRenderedWAVBytes {
		return RenderResponse{}, fmt.Errorf("mixed WAV is %d bytes, max is %d bytes", len(mixed), MaxRenderedWAVBytes)
	}
	_, mixedPCM, err := wav.Decode(mixed)
	if err != nil {
		return RenderResponse{}, fmt.Errorf("validate mixed Story Builder Project: %w", err)
	}
	mastered := mixed
	var master *story.Master
	if s.masterRender != nil {
		mastered, master, err = s.masterRender(ctx, mixed)
		if err != nil {
			return RenderResponse{}, fmt.Errorf("master Story Builder Project: %w", err)
		}
		if len(mastered) > MaxRenderedWAVBytes {
			return RenderResponse{}, fmt.Errorf("mastered WAV is %d bytes, max is %d bytes", len(mastered), MaxRenderedWAVBytes)
		}
		masteredFormat, masteredPCM, decodeErr := wav.Decode(mastered)
		if decodeErr != nil {
			return RenderResponse{}, fmt.Errorf("validate mastered Story Builder Project: %w", decodeErr)
		}
		if masteredFormat != wav.TimelineMixFormat() || len(masteredPCM) != len(mixedPCM) {
			return RenderResponse{}, fmt.Errorf("validate mastered Story Builder Project: format or duration changed")
		}
		duration, durationErr := wav.Duration(mastered)
		if durationErr != nil {
			return RenderResponse{}, fmt.Errorf("validate mastered Story Builder Project: %w", durationErr)
		}
		if duration.Milliseconds() != project.TimelineDurationMS {
			return RenderResponse{}, fmt.Errorf("validate mastered Story Builder Project: duration changed")
		}
	}

	number := len(project.Renders) + 1
	rendersDir := filepath.Join(s.rootDir, id, "renders")
	if err := os.MkdirAll(rendersDir, 0o755); err != nil {
		return RenderResponse{}, fmt.Errorf("create Story Builder renders directory: %w", err)
	}
	path := filepath.Join(rendersDir, renderFilename(number))
	if _, err := os.Stat(path); err == nil {
		return RenderResponse{}, ErrConflict
	} else if !os.IsNotExist(err) {
		return RenderResponse{}, fmt.Errorf("inspect Story Builder render: %w", err)
	}
	if err := s.writeFileAtomic(path, mastered); err != nil {
		return RenderResponse{}, fmt.Errorf("write Story Builder render: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.Remove(path)
		}
	}()

	now := s.now()
	revision := RenderRevision{
		Revision: number, CreatedAt: now, DurationMS: project.TimelineDurationMS, Bytes: len(mastered),
		URL: fmt.Sprintf("/v1/story-builder-projects/%s/renders/%d", id, number), Master: master,
	}
	project.Renders = append(project.Renders, revision)
	project.Revision++
	project.UpdatedAt = now
	data, err := encodeProject(project)
	if err != nil {
		return RenderResponse{}, err
	}
	if err := s.writeFileAtomic(filepath.Join(s.rootDir, id, manifestName), data); err != nil {
		return RenderResponse{}, fmt.Errorf("record Story Builder render: %w", err)
	}
	published = true
	return RenderResponse{Project: project, Render: revision}, nil
}

func (s *Store) RenderPath(id string, revision int) (string, RenderRevision, error) {
	if !validProjectID(id) || revision < 1 {
		return "", RenderRevision{}, ErrRenderNotFound
	}
	project, ok, err := s.Get(id)
	if err != nil {
		return "", RenderRevision{}, err
	}
	if !ok {
		return "", RenderRevision{}, ErrNotFound
	}
	if revision > len(project.Renders) {
		return "", RenderRevision{}, ErrRenderNotFound
	}
	render := project.Renders[revision-1]
	path := filepath.Join(s.rootDir, id, "renders", renderFilename(revision))
	stat, err := os.Stat(path)
	if err != nil || stat.Size() != int64(render.Bytes) || wav.ValidateFile(path) != nil {
		return "", RenderRevision{}, ErrRenderNotFound
	}
	return path, render, nil
}

func (s *Store) LatestRender(id string) (RenderRevision, error) {
	project, ok, err := s.Get(id)
	if err != nil {
		return RenderRevision{}, err
	}
	if !ok {
		return RenderRevision{}, ErrNotFound
	}
	if len(project.Renders) == 0 {
		return RenderRevision{}, ErrRenderNotFound
	}
	_, render, err := s.RenderPath(id, len(project.Renders))
	return render, err
}

func validateRenderRevisions(renders []RenderRevision, projectID string) error {
	for i, render := range renders {
		if render.Revision != i+1 || render.DurationMS <= 0 || render.Bytes <= 44 || render.CreatedAt.IsZero() ||
			render.URL != fmt.Sprintf("/v1/story-builder-projects/%s/renders/%d", projectID, i+1) {
			return errors.New("decode Story Builder Project: invalid render revisions")
		}
		formats := make(map[string]struct{}, len(render.Exports))
		for _, export := range render.Exports {
			_, duplicate := formats[export.Format]
			if !validExportFormat(export.Format) || duplicate || export.Bytes <= 0 || export.CreatedAt.IsZero() ||
				export.URL != fmt.Sprintf("/v1/story-builder-projects/%s/renders/%d/exports/%s", projectID, i+1, export.Format) ||
				(export.Format == "mp3" && export.Bitrate == "") || (export.Format == "flac" && export.Bitrate != "") {
				return errors.New("decode Story Builder Project: invalid render exports")
			}
			formats[export.Format] = struct{}{}
		}
	}
	return nil
}

func renderFilename(revision int) string {
	return fmt.Sprintf("render-%03d.wav", revision)
}
