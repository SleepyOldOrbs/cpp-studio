package storybuilder

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"cpp-studio/internal/engine"
	"cpp-studio/internal/wav"
)

type ExportResponse struct {
	Project Project      `json:"project"`
	Export  RenderExport `json:"export"`
}

// ExportRender creates or replaces one derived encoding of an immutable WAV
// revision. Encoding happens in a temporary file; publication and manifest
// update preserve the previous valid export if either later step fails.
func (s *Store) ExportRender(ctx context.Context, id string, expectedRevision, renderRevision int, format, bitrate string) (ExportResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !validProjectID(id) {
		return ExportResponse{}, ErrNotFound
	}
	if !validExportFormat(format) {
		return ExportResponse{}, ErrUnsupportedExport
	}
	if s.transcode == nil {
		return ExportResponse{}, ErrExportUnavailable
	}
	project, ok, err := s.Get(id)
	if err != nil {
		return ExportResponse{}, err
	}
	if !ok {
		return ExportResponse{}, ErrNotFound
	}
	if expectedRevision != project.Revision {
		return ExportResponse{}, ErrConflict
	}
	if renderRevision < 1 || renderRevision > len(project.Renders) {
		return ExportResponse{}, ErrRenderNotFound
	}
	render := &project.Renders[renderRevision-1]
	sourcePath := filepath.Join(s.rootDir, id, "renders", renderFilename(renderRevision))
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil || sourceInfo.Size() != int64(render.Bytes) || wav.ValidateFile(sourcePath) != nil {
		return ExportResponse{}, ErrRenderNotFound
	}

	now := s.now()
	rendersDir := filepath.Dir(sourcePath)
	finalPath := filepath.Join(rendersDir, exportFilename(renderRevision, format))
	var export RenderExport
	var manifest []byte
	published, err := publishArtifact(artifactPublication{
		FinalPath:   finalPath,
		TempPattern: "." + exportFilename(renderRevision, format) + ".staging-*." + format,
		Replace:     true,
		Stage: func(path string) error {
			// Encoding can take minutes. Other projects may mutate meanwhile;
			// this project's revision must still match before publication.
			err := func() error {
				s.mu.Unlock()
				defer s.mu.Lock()
				return s.transcode(ctx, sourcePath, path, format, bitrate)
			}()
			if err != nil {
				return err
			}
			if err := s.checkProjectRevision(id, expectedRevision); err != nil {
				return err
			}
			return ctx.Err()
		},
		Validate: func(path string) error {
			audioFormat, _ := engine.LookupAudioFormat(format)
			if err := engine.ValidateEncodedAudio(path, audioFormat); err != nil {
				return fmt.Errorf("validate Story Builder export: %w", err)
			}
			info, err := os.Stat(path)
			if err != nil {
				return fmt.Errorf("inspect Story Builder export: %w", err)
			}
			export = RenderExport{
				Format: format, Bitrate: bitrate, Bytes: int(info.Size()), CreatedAt: now,
				URL: fmt.Sprintf("/v1/story-builder-projects/%s/renders/%d/exports/%s", id, renderRevision, format),
			}
			replaced := false
			for i, existing := range render.Exports {
				if existing.Format == format {
					render.Exports[i] = export
					replaced = true
					break
				}
			}
			if !replaced {
				render.Exports = append(render.Exports, export)
			}
			project.Revision++
			project.UpdatedAt = now
			manifest, err = encodeProject(project)
			return err
		},
		Record: func() error {
			if err := s.writeFileAtomic(filepath.Join(s.rootDir, id, manifestName), manifest); err != nil {
				return fmt.Errorf("record Story Builder export: %w", err)
			}
			return nil
		},
	})
	response := ExportResponse{Project: project, Export: export}
	if err != nil {
		if published {
			return response, err
		}
		return ExportResponse{}, err
	}
	return response, nil
}

func (s *Store) ExportPath(id string, renderRevision int, format string) (string, RenderExport, error) {
	if !validProjectID(id) || renderRevision < 1 || !validExportFormat(format) {
		return "", RenderExport{}, ErrExportNotFound
	}
	project, ok, err := s.Get(id)
	if err != nil {
		return "", RenderExport{}, err
	}
	if !ok {
		return "", RenderExport{}, ErrNotFound
	}
	if renderRevision > len(project.Renders) {
		return "", RenderExport{}, ErrExportNotFound
	}
	for _, export := range project.Renders[renderRevision-1].Exports {
		if export.Format != format {
			continue
		}
		path := filepath.Join(s.rootDir, id, "renders", exportFilename(renderRevision, format))
		if info, err := os.Stat(path); err != nil || info.Size() != int64(export.Bytes) {
			return "", RenderExport{}, ErrExportNotFound
		}
		return path, export, nil
	}
	return "", RenderExport{}, ErrExportNotFound
}

func validExportFormat(format string) bool {
	return format == "mp3" || format == "flac"
}

func exportFilename(revision int, format string) string {
	return fmt.Sprintf("render-%03d.%s", revision, format)
}
