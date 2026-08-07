package storybuilder

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRenderExportsReplacePerFormatAndStayIsolatedByRevision(t *testing.T) {
	root := t.TempDir()
	mode := "ok"
	encodeCount := 0
	transcode := func(ctx context.Context, _, outPath, format, bitrate string) error {
		encodeCount++
		header := []byte("ID3\x03\x00\x00\x00")
		if format == "flac" {
			header = []byte("fLaC")
		}
		data := append(header, []byte(format+":"+bitrate+":"+string(rune('0'+encodeCount)))...)
		if mode == "corrupt" {
			data = []byte("not encoded audio")
		}
		if err := os.WriteFile(outPath, data, 0o644); err != nil {
			return err
		}
		if mode == "fail" {
			return errors.New("injected encoder failure")
		}
		if mode == "cancel" {
			return context.Canceled
		}
		return ctx.Err()
	}
	store := NewStoreWithOptions(root, StoreOptions{Transcode: transcode})
	project, err := store.Create("Delivery exports")
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Render(context.Background(), project.ID, project.Revision)
	if err != nil {
		t.Fatal(err)
	}
	firstWAV, _, _ := store.RenderPath(project.ID, 1)
	firstWAVBytes, _ := os.ReadFile(firstWAV)

	mp3, err := store.ExportRender(context.Background(), project.ID, first.Project.Revision, 1, "mp3", "128k")
	if err != nil {
		t.Fatalf("export mp3: %v", err)
	}
	if mp3.Export.Format != "mp3" || mp3.Export.Bitrate != "128k" || len(mp3.Project.Renders[0].Exports) != 1 {
		t.Fatalf("mp3 response = %+v", mp3)
	}
	oldMP3Path, _, err := store.ExportPath(project.ID, 1, "mp3")
	if err != nil {
		t.Fatal(err)
	}
	oldMP3Bytes, _ := os.ReadFile(oldMP3Path)

	second, err := store.Render(context.Background(), project.ID, mp3.Project.Revision)
	if err != nil {
		t.Fatal(err)
	}
	secondWAVPath, _, _ := store.RenderPath(project.ID, 2)
	secondWAVBytes, _ := os.ReadFile(secondWAVPath)
	flac, err := store.ExportRender(context.Background(), project.ID, second.Project.Revision, 2, "flac", "")
	if err != nil {
		t.Fatalf("export flac: %v", err)
	}
	if len(flac.Project.Renders[0].Exports) != 1 || len(flac.Project.Renders[1].Exports) != 1 || flac.Project.Renders[1].Exports[0].Format != "flac" {
		t.Fatalf("revision exports crossed: %+v", flac.Project.Renders)
	}

	again, err := store.ExportRender(context.Background(), project.ID, flac.Project.Revision, 1, "mp3", "64k")
	if err != nil {
		t.Fatalf("re-export mp3: %v", err)
	}
	if len(again.Project.Renders[0].Exports) != 1 || again.Project.Renders[0].Exports[0].Bitrate != "64k" ||
		len(again.Project.Renders[1].Exports) != 1 {
		t.Fatalf("re-export accumulated or crossed revisions: %+v", again.Project.Renders)
	}
	newMP3Bytes, _ := os.ReadFile(oldMP3Path)
	if bytes.Equal(newMP3Bytes, oldMP3Bytes) {
		t.Fatalf("re-export did not replace the derived mp3")
	}
	if got, _ := os.ReadFile(firstWAV); !bytes.Equal(got, firstWAVBytes) {
		t.Fatalf("exports changed render 1 WAV")
	}
	if got, _ := os.ReadFile(secondWAVPath); !bytes.Equal(got, secondWAVBytes) {
		t.Fatalf("exports changed render 2 WAV")
	}

	for _, failureMode := range []string{"fail", "cancel", "corrupt"} {
		mode = failureMode
		before, _ := os.ReadFile(oldMP3Path)
		if _, err := store.ExportRender(context.Background(), project.ID, again.Project.Revision, 1, "mp3", "96k"); err == nil {
			t.Fatalf("%s export succeeded", failureMode)
		}
		after, _ := os.ReadFile(oldMP3Path)
		if !bytes.Equal(before, after) {
			t.Fatalf("%s export damaged existing mp3", failureMode)
		}
		loaded, _, _ := store.Get(project.ID)
		if loaded.Revision != again.Project.Revision || loaded.Renders[0].Exports[0].Bitrate != "64k" {
			t.Fatalf("%s export mutated manifest: %+v", failureMode, loaded)
		}
	}
	entries, err := os.ReadDir(filepath.Dir(oldMP3Path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name()[0] == '.' {
			t.Fatalf("temporary export leaked after encoder failure: %s", entry.Name())
		}
	}
}

func TestRenderExportManifestFailureRestoresExistingArtifact(t *testing.T) {
	root := t.TempDir()
	transcode := func(_ context.Context, _, outPath, format, bitrate string) error {
		return os.WriteFile(outPath, append([]byte("ID3\x03\x00\x00\x00"), []byte(format+":"+bitrate)...), 0o644)
	}
	store := NewStoreWithOptions(root, StoreOptions{Transcode: transcode})
	project, _ := store.Create("Export transaction")
	rendered, err := store.Render(context.Background(), project.ID, project.Revision)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := store.ExportRender(context.Background(), project.ID, rendered.Project.Revision, 1, "mp3", "128k")
	if err != nil {
		t.Fatal(err)
	}
	path, _, _ := store.ExportPath(project.ID, 1, "mp3")
	before, _ := os.ReadFile(path)

	failing := NewStoreWithOptions(root, StoreOptions{Transcode: transcode, WriteFileAtomic: func(path string, data []byte) error {
		if filepath.Base(path) == manifestName {
			return errors.New("injected manifest failure")
		}
		return writeFileAtomic(path, data)
	}})
	if _, err := failing.ExportRender(context.Background(), project.ID, exported.Project.Revision, 1, "mp3", "64k"); err == nil {
		t.Fatalf("manifest failure export succeeded")
	}
	after, _ := os.ReadFile(path)
	loaded, _, _ := store.Get(project.ID)
	if !bytes.Equal(before, after) || loaded.Revision != exported.Project.Revision || loaded.Renders[0].Exports[0].Bitrate != "128k" {
		t.Fatalf("manifest failure did not restore prior export: %+v", loaded)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name()[0] == '.' {
			t.Fatalf("temporary export leaked: %s", entry.Name())
		}
	}
}

func TestRenderExportRejectsUnavailableOrUnsupportedFormats(t *testing.T) {
	store := NewStore(t.TempDir())
	project, _ := store.Create("Unavailable export")
	rendered, _ := store.Render(context.Background(), project.ID, project.Revision)
	if _, err := store.ExportRender(context.Background(), project.ID, rendered.Project.Revision, 1, "mp3", "128k"); !errors.Is(err, ErrExportUnavailable) {
		t.Fatalf("unavailable export error = %v", err)
	}
	if _, err := store.ExportRender(context.Background(), project.ID, rendered.Project.Revision, 1, "wav", ""); !errors.Is(err, ErrUnsupportedExport) {
		t.Fatalf("unsupported export error = %v", err)
	}
	if _, _, err := store.ExportPath(project.ID, 1, `../project.json`); !errors.Is(err, ErrExportNotFound) {
		t.Fatalf("path-like export lookup error = %v", err)
	}
}
