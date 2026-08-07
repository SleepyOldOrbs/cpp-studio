package storybuilder

import (
	"context"
	"errors"
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
func (s *Store) ExportRender(ctx context.Context, id string, expectedRevision, renderRevision int, format, bitrate string) (response ExportResponse, returnErr error) {
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

	rendersDir := filepath.Dir(sourcePath)
	tmp, err := os.CreateTemp(rendersDir, "."+exportFilename(renderRevision, format)+".tmp-*."+format)
	if err != nil {
		return ExportResponse{}, fmt.Errorf("stage Story Builder export: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		if err := os.Remove(tmpPath); err != nil && !os.IsNotExist(err) {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove Story Builder export temporary output %q: %w", tmpPath, err))
		}
	}()
	if closeErr := tmp.Close(); closeErr != nil {
		return ExportResponse{}, fmt.Errorf("stage Story Builder export: %w", closeErr)
	}
	if err := s.transcode(ctx, sourcePath, tmpPath, format, bitrate); err != nil {
		return ExportResponse{}, err
	}
	if err := ctx.Err(); err != nil {
		return ExportResponse{}, err
	}
	audioFormat, _ := engine.LookupAudioFormat(format)
	if err := engine.ValidateEncodedAudio(tmpPath, audioFormat); err != nil {
		return ExportResponse{}, fmt.Errorf("validate Story Builder export: %w", err)
	}
	info, err := os.Stat(tmpPath)
	if err != nil {
		return ExportResponse{}, fmt.Errorf("inspect Story Builder export: %w", err)
	}

	now := s.now()
	export := RenderExport{
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
	manifest, err := encodeProject(project)
	if err != nil {
		return ExportResponse{}, err
	}

	finalPath := filepath.Join(rendersDir, exportFilename(renderRevision, format))
	backupPath := ""
	if _, err := os.Stat(finalPath); err == nil {
		backup, createErr := os.CreateTemp(rendersDir, "."+exportFilename(renderRevision, format)+".backup-*")
		if createErr != nil {
			return ExportResponse{}, fmt.Errorf("stage existing Story Builder export: %w", createErr)
		}
		backupPath = backup.Name()
		_ = backup.Close()
		_ = os.Remove(backupPath)
		if err := os.Rename(finalPath, backupPath); err != nil {
			return ExportResponse{}, fmt.Errorf("stage existing Story Builder export: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return ExportResponse{}, fmt.Errorf("inspect Story Builder export: %w", err)
	}
	restore := func() error {
		if err := os.Remove(finalPath); err != nil && !os.IsNotExist(err) {
			if backupPath != "" {
				return fmt.Errorf("remove replacement before restoring backup %q: %w", backupPath, err)
			}
			return fmt.Errorf("remove unpublished Story Builder export: %w", err)
		}
		if backupPath != "" {
			if err := os.Rename(backupPath, finalPath); err != nil {
				return fmt.Errorf("restore Story Builder export backup %q: %w", backupPath, err)
			}
		}
		return nil
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return ExportResponse{}, errors.Join(fmt.Errorf("publish Story Builder export: %w", err), restore())
	}
	if err := s.writeFileAtomic(filepath.Join(s.rootDir, id, manifestName), manifest); err != nil {
		return ExportResponse{}, errors.Join(fmt.Errorf("record Story Builder export: %w", err), restore())
	}
	if backupPath != "" {
		if err := os.Remove(backupPath); err != nil {
			return ExportResponse{Project: project, Export: export}, fmt.Errorf("Story Builder export was published but backup cleanup failed at %q: %w", backupPath, err)
		}
	}
	return ExportResponse{Project: project, Export: export}, nil
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
