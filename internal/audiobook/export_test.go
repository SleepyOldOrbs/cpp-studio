package audiobook

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"cpp-studio/internal/jobs"
	"cpp-studio/internal/wav"
)

func legacyExportBook(t *testing.T) (*Store, string) {
	t.Helper()
	store := NewStore(t.TempDir())
	id := "book_20261024_120000_001"
	if err := os.MkdirAll(filepath.Join(store.rootDir, id), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(filepath.Join(store.rootDir, id, manifestFileName), Manifest{ID: id, Title: "Original title", SynthesisFingerprint: "untouched"}); err != nil {
		t.Fatal(err)
	}
	return store, id
}

func testCoverPNG(t *testing.T) []byte {
	t.Helper()
	var data bytes.Buffer
	cover := image.NewRGBA(image.Rect(0, 0, 4, 3))
	cover.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := png.Encode(&data, cover); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func TestBookDetailsRoundTripWithoutChangingSynthesis(t *testing.T) {
	store, id := legacyExportBook(t)
	metadata := BookMetadata{Title: "Café at sea", Author: "A. Writer", Narrator: "Ada", Year: "2026", Genre: "Fiction", Description: "First line.\nSecond; # = \\ line."}
	if err := store.UpdateBookMetadata(id, metadata); err != nil {
		t.Fatal(err)
	}
	book, ok, err := loadManifest(filepath.Join(store.rootDir, id, manifestFileName))
	if err != nil || !ok || book.Metadata == nil || !reflect.DeepEqual(*book.Metadata, metadata) || book.Title != metadata.Title || book.SynthesisFingerprint != "untouched" {
		t.Fatal(book, ok, err)
	}
	for _, invalid := range []BookMetadata{{Year: "２０２６"}, {Year: "20260"}, {Author: strings.Repeat("x", 257)}, {Description: strings.Repeat("x", 4001)}} {
		if err := store.UpdateBookMetadata(id, invalid); !IsRequestError(err) {
			t.Fatal("invalid details accepted", invalid, err)
		}
	}
	after, _, _ := loadManifest(filepath.Join(store.rootDir, id, manifestFileName))
	if !reflect.DeepEqual(after, book) {
		t.Fatal("invalid details changed the saved book")
	}
}

func TestBookCoverValidationPreservesPreviousCover(t *testing.T) {
	store, id := legacyExportBook(t)
	cover := testCoverPNG(t)
	if err := store.SetBookCover(id, cover); err != nil {
		t.Fatal(err)
	}
	book, _, err := loadManifest(filepath.Join(store.rootDir, id, manifestFileName))
	if err != nil || book.Cover == nil || book.Cover.Width != 4 || book.Cover.Height != 3 || book.Cover.ContentType != "image/png" {
		t.Fatal(book, err)
	}
	path := filepath.Join(store.rootDir, id, book.Cover.File)
	before, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, cover) {
		t.Fatal("cover not owned by the book", err)
	}
	for _, invalid := range [][]byte{nil, []byte("not an image"), cover[:len(cover)-12], make([]byte, 10*1024*1024+1)} {
		if err := store.SetBookCover(id, invalid); !IsRequestError(err) {
			t.Fatal("invalid cover accepted", err)
		}
	}
	tooManyPixels := append([]byte(nil), cover...)
	binary.BigEndian.PutUint32(tooManyPixels[16:20], 5000)
	binary.BigEndian.PutUint32(tooManyPixels[20:24], 4001)
	binary.BigEndian.PutUint32(tooManyPixels[29:33], crc32.ChecksumIEEE(tooManyPixels[12:29]))
	if err := store.SetBookCover(id, tooManyPixels); !IsRequestError(err) {
		t.Fatal("oversized dimensions accepted", err)
	}
	after, _, _ := loadManifest(filepath.Join(store.rootDir, id, manifestFileName))
	if !reflect.DeepEqual(after, book) {
		t.Fatal("bad cover replaced manifest")
	}
	saved, err := os.ReadFile(path)
	if err != nil || sha256.Sum256(saved) != sha256.Sum256(before) {
		t.Fatal("bad cover changed original bytes", err)
	}
}

func TestJPEGBookCoverAndCorruptPathRejection(t *testing.T) {
	store, id := legacyExportBook(t)
	var data bytes.Buffer
	if err := jpeg.Encode(&data, image.NewRGBA(image.Rect(0, 0, 4, 3)), nil); err != nil {
		t.Fatal(err)
	}
	if err := store.SetBookCover(id, data.Bytes()); err != nil {
		t.Fatal(err)
	}
	if _, contentType, err := store.BookCoverPath(id); err != nil || contentType != "image/jpeg" {
		t.Fatal(contentType, err)
	}
	book, _, _ := loadManifest(filepath.Join(store.rootDir, id, manifestFileName))
	book.Cover.File = "../../outside.jpg"
	if err := store.SaveManifestFinal(book); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.BookCoverPath(id); !errors.Is(err, ErrStoreCorrupt) {
		t.Fatal("unsafe cover path accepted", err)
	}
}

func m4bFixture(t *testing.T, encode EncodeM4BFunc) (*Manager, string) {
	t.Helper()
	registry := jobs.NewRegistry()
	manager := NewManager(ManagerOptions{RootDir: t.TempDir(), Jobs: registry, EncodeM4B: encode, Synthesize: func(context.Context, SynthesisRequest) ([]byte, error) { return wav.SyntheticTone(16000), nil }})
	id, _, err := manager.Create(context.Background(), DocumentRequest{Filename: "book.md", Data: []byte("# First\nFirst words.\n# Second\nSecond words."), Request: Request{EngineID: DramaBoxEngineID, Verification: VerificationModeOff}})
	if err != nil {
		t.Fatal(err)
	}
	waitForAudiobookJob(t, registry, id)
	deadline := time.Now().Add(time.Second)
	for {
		manager.mu.Lock()
		idle := manager.activeID == ""
		manager.mu.Unlock()
		if idle {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("production did not become idle")
		}
		time.Sleep(time.Millisecond)
	}
	return manager, id
}

func TestM4BExportUsesExactChaptersAndPreservesPreviousArtifactOnFailure(t *testing.T) {
	fail := false
	var receivedMetadata string
	manager, id := m4bFixture(t, func(ctx context.Context, in, out, metadata, cover string, format wav.Format) error {
		if fail {
			return errors.New("encoder failed")
		}
		data, err := os.ReadFile(metadata)
		if err != nil {
			return err
		}
		receivedMetadata = string(data)
		if format.SampleRate != 16000 || format.Channels != 1 {
			t.Error("format changed", format)
		}
		return os.WriteFile(out, []byte("\x00\x00\x00\x20ftypM4A fixture export"), 0o600)
	})
	metadata := BookMetadata{Title: "Café #1=two; \\ title", Author: "Writer", Description: "Line one\nLine two"}
	if err := manager.UpdateBookMetadata(id, metadata); err != nil {
		t.Fatal(err)
	}
	plan, err := manager.PreviewM4B(context.Background(), id, "")
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Available || len(plan.Chapters) != 2 || plan.Chapters[1].StartSample != 20000 || plan.Chapters[1].EndSample != 40800 {
		t.Fatal(plan)
	}
	masterPath := filepath.Join(manager.rootDir, id, ArtifactName)
	masterBefore, _ := os.ReadFile(masterPath)
	request := M4BExportRequest{RenderID: plan.RenderID, MasterSHA256: plan.MasterSHA256}
	export, err := manager.ExportM4B(context.Background(), id, request)
	if err != nil {
		t.Fatal(err)
	}
	if export.RenderID != plan.RenderID || export.MasterSHA256 != plan.MasterSHA256 || export.Metadata != metadata || export.Format != "m4b" {
		t.Fatal(export)
	}
	if strings.Count(receivedMetadata, "[CHAPTER]") != 2 || !strings.Contains(receivedMetadata, "TIMEBASE=1/16000\nSTART=20000\nEND=40800") || !strings.Contains(receivedMetadata, `title=Café \#1\=two\; \\ title`) || !strings.Contains(receivedMetadata, "description=Line one\\\nLine two") {
		t.Fatal("incorrect ffmetadata", receivedMetadata)
	}
	path, _, err := manager.ExportPath(id, export.RenderID)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	fail = true
	if _, err := manager.ExportM4B(context.Background(), id, request); err == nil {
		t.Fatal("failed encoder succeeded")
	}
	after, _ := os.ReadFile(path)
	masterAfter, _ := os.ReadFile(masterPath)
	if !bytes.Equal(before, after) || !bytes.Equal(masterBefore, masterAfter) {
		t.Fatal("failed export changed retained audio")
	}
	request.MasterSHA256 = "stale"
	if _, err := manager.ExportM4B(context.Background(), id, request); !IsRequestError(err) {
		t.Fatal("stale master accepted", err)
	}
}

func TestM4BExportPlanRejectsBadBoundariesAndReportsUnavailableEncoder(t *testing.T) {
	manager, id := m4bFixture(t, nil)
	plan, err := manager.PreviewM4B(context.Background(), id, "")
	if err != nil || plan.Available {
		t.Fatal(plan, err)
	}
	if _, err := manager.ExportM4B(context.Background(), id, M4BExportRequest{RenderID: plan.RenderID, MasterSHA256: plan.MasterSHA256}); !errors.Is(err, ErrExportUnavailable) {
		t.Fatal(err)
	}
	book, _, _ := manager.Status(id)
	book.RenderRevisions[0].ChapterBoundaries[1].StartSample = 0
	if err := manager.store.SaveManifestFinal(book); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.PreviewM4B(context.Background(), id, ""); !errors.Is(err, ErrStoreCorrupt) {
		t.Fatal("invalid timing accepted", err)
	}
}

func TestLegacyM4BPlanExplicitlyUsesOneChapter(t *testing.T) {
	store, id := legacyExportBook(t)
	if err := os.WriteFile(filepath.Join(store.rootDir, id, ArtifactName), wav.SyntheticTone(16000), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewManager(ManagerOptions{RootDir: store.rootDir})
	plan, err := manager.PreviewM4B(context.Background(), id, "")
	if err != nil || !plan.SingleChapter || plan.RenderID != "" || len(plan.Chapters) != 1 || plan.Chapters[0].Title != "Original title" || plan.Chapters[0].EndSample != 16000 {
		t.Fatal(plan, err)
	}
}

func TestM4BCancellationPreservesPriorExportAndCleansStaging(t *testing.T) {
	entered := make(chan struct{})
	calls := 0
	manager, id := m4bFixture(t, func(ctx context.Context, in, out, metadata, cover string, format wav.Format) error {
		calls++
		if calls > 1 {
			close(entered)
			<-ctx.Done()
			return errors.New("native process stopped")
		}
		return os.WriteFile(out, []byte("\x00\x00\x00\x20ftypM4A original export"), 0o600)
	})
	plan, err := manager.PreviewM4B(context.Background(), id, "")
	if err != nil {
		t.Fatal(err)
	}
	request := M4BExportRequest{RenderID: plan.RenderID, MasterSHA256: plan.MasterSHA256}
	original, err := manager.ExportM4B(context.Background(), id, request)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := manager.ExportM4B(ctx, id, request); done <- err }()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("cancel not propagated", err)
	}
	path, record, err := manager.ExportPath(id, request.RenderID)
	if err != nil || record.SHA256 != original.SHA256 {
		t.Fatal(record, err)
	}
	if hash, err := fileSHA256(context.Background(), path); err != nil || hash != original.SHA256 {
		t.Fatal("old export changed", err)
	}
	entries, _ := os.ReadDir(filepath.Join(manager.rootDir, id))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".m4b-staging-") {
			t.Fatal("staging leaked")
		}
	}
}

func TestM4BExportFreezesCoverAndDetailsDuringConcurrentEdits(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	oldCover := testCoverPNG(t)
	manager, id := m4bFixture(t, func(ctx context.Context, in, out, metadata, cover string, format wav.Format) error {
		close(entered)
		<-release
		data, err := os.ReadFile(cover)
		if err != nil {
			return err
		}
		if !bytes.Equal(data, oldCover) {
			return errors.New("cover snapshot changed")
		}
		data, err = os.ReadFile(metadata)
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), "artist=Original author") {
			return errors.New("metadata snapshot changed")
		}
		return os.WriteFile(out, []byte("\x00\x00\x00\x20ftypM4A frozen snapshot"), 0o600)
	})
	if err := manager.SetBookCover(id, oldCover); err != nil {
		t.Fatal(err)
	}
	if err := manager.UpdateBookMetadata(id, BookMetadata{Title: "Book", Author: "Original author"}); err != nil {
		t.Fatal(err)
	}
	plan, err := manager.PreviewM4B(context.Background(), id, "")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := manager.ExportM4B(context.Background(), id, M4BExportRequest{RenderID: plan.RenderID, MasterSHA256: plan.MasterSHA256})
		done <- err
	}()
	<-entered
	if err := manager.UpdateBookMetadata(id, BookMetadata{Title: "New title", Author: "New author"}); err != nil {
		close(release)
		t.Fatal(err)
	}
	var newCover bytes.Buffer
	if err := png.Encode(&newCover, image.NewRGBA(image.Rect(0, 0, 3, 5))); err != nil {
		close(release)
		t.Fatal(err)
	}
	if err := manager.SetBookCover(id, newCover.Bytes()); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	book, _, err := manager.Status(id)
	if err != nil {
		t.Fatal(err)
	}
	if book.Metadata.Author != "New author" || book.Exports[0].Metadata.Author != "Original author" || book.Cover.SHA256 == book.Exports[0].Cover.SHA256 {
		t.Fatal("publication overwrote concurrent edits or changed snapshot", book)
	}
}

func TestM4BExportsStayAssociatedWithTheirSelectedRenderTimings(t *testing.T) {
	manager, id := m4bFixture(t, func(ctx context.Context, in, out, metadata, cover string, format wav.Format) error {
		return os.WriteFile(out, []byte("\x00\x00\x00\x20ftypM4A render export"), 0o600)
	})
	first, err := manager.PreviewM4B(context.Background(), id, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ExportM4B(context.Background(), id, M4BExportRequest{RenderID: first.RenderID, MasterSHA256: first.MasterSHA256}); err != nil {
		t.Fatal(err)
	}
	manager.synthesizeDetailed = func(ctx context.Context, req SynthesisRequest) (SynthesisResult, error) {
		actual := req.Options.Seed
		return SynthesisResult{Audio: wav.SyntheticTone(32000), ActualSeed: &actual}, nil
	}
	retry, err := manager.RetrySection(context.Background(), id, "section-0001", RetryModeVariation)
	if err != nil {
		t.Fatal(err)
	}
	waitForAudiobookJob(t, manager.registry, retry)
	// Terminal registry state can precede the deferred reservation release.
	for {
		manager.mu.Lock()
		idle := manager.activeID == ""
		manager.mu.Unlock()
		if idle {
			break
		}
		time.Sleep(time.Millisecond)
	}
	render, err := manager.SelectAttempt(context.Background(), id, "section-0001", "attempt-0002")
	if err != nil {
		t.Fatal(err)
	}
	waitForAudiobookJob(t, manager.registry, render)
	for {
		manager.mu.Lock()
		idle := manager.activeID == ""
		manager.mu.Unlock()
		if idle {
			break
		}
		time.Sleep(time.Millisecond)
	}
	second, err := manager.PreviewM4B(context.Background(), id, "")
	if err != nil {
		t.Fatal(err)
	}
	if second.RenderID == first.RenderID || second.Chapters[1].StartSample != 36000 || first.Chapters[1].StartSample != 20000 {
		t.Fatal(first, second)
	}
	if _, err := manager.ExportM4B(context.Background(), id, M4BExportRequest{RenderID: second.RenderID, MasterSHA256: second.MasterSHA256}); err != nil {
		t.Fatal(err)
	}
	book, _, _ := manager.Status(id)
	if len(book.Exports) != 2 || book.Exports[0].Chapters[1].StartSample != 20000 || book.Exports[1].Chapters[1].StartSample != 36000 {
		t.Fatal(book.Exports)
	}
	if _, _, err := manager.ExportPath(id, first.RenderID); err != nil {
		t.Fatal("old export unavailable", err)
	}
}
