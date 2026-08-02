package audiobook

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cpp-studio/internal/jobs"
	"cpp-studio/internal/wav"
)

func TestExtractPlainText(t *testing.T) {
	text, err := Extract("book.txt", []byte("\xEF\xBB\xBFHello.\r\nSecond line.\r\n\r\nNew paragraph."))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !strings.Contains(text, "Hello.\nSecond line.\n\nNew paragraph.") {
		t.Fatalf("unexpected text %q", text)
	}
	if _, err := Extract("book.txt", []byte{0xFF, 0xFE, 0x00}); err == nil {
		t.Fatal("expected invalid UTF-8 to be rejected")
	}
	if _, err := Extract("book.pdf", []byte("%PDF-1.4")); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected pdf rejection, got %v", err)
	}
	if _, err := Extract("book.docx", []byte("x")); err == nil {
		t.Fatal("expected unsupported type to be rejected")
	}
}

func buildEPUB(t *testing.T, chapters map[string]string, spine []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name, content string) {
		f, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	write("META-INF/container.xml", `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles>
</container>`)
	var manifest, spineXML strings.Builder
	for id := range chapters {
		fmt.Fprintf(&manifest, `<item id=%q href="%s.xhtml" media-type="application/xhtml+xml"/>`, id, id)
	}
	for _, id := range spine {
		fmt.Fprintf(&spineXML, `<itemref idref=%q/>`, id)
	}
	write("OEBPS/content.opf", `<?xml version="1.0"?>
<package xmlns="http://www.idpf.org/2007/opf"><manifest>`+manifest.String()+`</manifest><spine>`+spineXML.String()+`</spine></package>`)
	for id, body := range chapters {
		write("OEBPS/"+id+".xhtml", body)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func TestExtractEPUBFollowsSpine(t *testing.T) {
	epub := buildEPUB(t, map[string]string{
		"ch1": `<html><head><style>p{}</style></head><body><h1>Chapter One</h1><p>It was a dark &amp; stormy night.</p></body></html>`,
		"ch2": `<html><body><p>The lighthouse blinked twice.</p><script>alert(1)</script></body></html>`,
	}, []string{"ch1", "ch2"})

	text, err := Extract("book.epub", epub)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !strings.Contains(text, "Chapter One") || !strings.Contains(text, "dark & stormy night") {
		t.Fatalf("chapter 1 content missing: %q", text)
	}
	if !strings.Contains(text, "lighthouse blinked") {
		t.Fatalf("chapter 2 content missing: %q", text)
	}
	if strings.Contains(text, "alert(1)") || strings.Contains(text, "p{}") {
		t.Fatalf("script/style leaked into text: %q", text)
	}
	if strings.Index(text, "Chapter One") > strings.Index(text, "lighthouse") {
		t.Fatalf("spine order not respected: %q", text)
	}
}

func TestChunkRespectsBoundaries(t *testing.T) {
	text := "First sentence. Second sentence is a bit longer than the first one. Third one.\n\nA new paragraph starts here. It also has sentences."
	chunks := Chunk(text, 80)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d: %q", len(chunks), chunks)
	}
	for i, chunk := range chunks {
		if len(chunk) > 80 {
			t.Errorf("chunk %d exceeds budget (%d chars): %q", i, len(chunk), chunk)
		}
	}
	// A paragraph boundary must end a chunk.
	joined := strings.Join(chunks, "|")
	first := strings.Index(joined, "Third one.")
	second := strings.Index(joined, "A new paragraph")
	if first == -1 || second == -1 || !strings.Contains(joined[first:second], "|") {
		t.Fatalf("paragraph boundary not respected: %q", chunks)
	}

	// One giant unbroken sentence still chunks.
	long := strings.Repeat("word ", 200)
	for _, chunk := range Chunk(long, 100) {
		if len(chunk) > 100 {
			t.Fatalf("oversized chunk from unbroken text: %d chars", len(chunk))
		}
	}
}

func TestNarrationPipeline(t *testing.T) {
	registry := jobs.NewRegistry()
	var synthesized []string
	var engines []string
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(),
		Jobs:    registry,
		Synthesize: func(_ context.Context, text, voiceID, engineID string) ([]byte, error) {
			synthesized = append(synthesized, text)
			engines = append(engines, engineID)
			if voiceID != "narrator" {
				return nil, fmt.Errorf("unexpected voice %q", voiceID)
			}
			return wav.SyntheticTone(1600), nil
		},
	})

	id, chunks, err := manager.Submit(context.Background(), Request{
		Title:   "Test Book",
		Text:    "First paragraph of the book.\n\nSecond paragraph, slightly longer, with two sentences. Here is the second.",
		VoiceID: "narrator",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if chunks < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", chunks)
	}

	deadline := time.Now().Add(5 * time.Second)
	var job jobs.Job
	for time.Now().Before(deadline) {
		job, _ = registry.Get(id)
		if job.Status == jobs.StatusComplete || job.Status == jobs.StatusFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.Status != jobs.StatusComplete {
		t.Fatalf("job did not complete: %+v", job)
	}
	if len(synthesized) != chunks {
		t.Fatalf("synthesized %d chunks, expected %d", len(synthesized), chunks)
	}
	for _, engineID := range engines {
		if engineID != DefaultEngineID {
			t.Fatalf("expected default engine %q, got %q", DefaultEngineID, engineID)
		}
	}
	if job.Result["title"] != "Test Book" || job.Result["artifactUrl"] == "" {
		t.Fatalf("unexpected result: %+v", job.Result)
	}

	books, err := manager.List()
	if err != nil || len(books) != 1 {
		t.Fatalf("list: %v, %d books", err, len(books))
	}
	if books[0].Title != "Test Book" || books[0].Chunks != chunks || books[0].EngineID != DefaultEngineID {
		t.Fatalf("unexpected manifest: %+v", books[0])
	}
	path, err := manager.ArtifactPath(id, ArtifactName)
	if err != nil {
		t.Fatalf("artifact path: %v", err)
	}
	if path == "" {
		t.Fatal("empty artifact path")
	}
}

func TestNarrationCancellation(t *testing.T) {
	registry := jobs.NewRegistry()
	entered := make(chan struct{}, 1)
	released := make(chan string, 2)
	var attempts atomic.Int32
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(),
		Jobs:    registry,
		ReserveEngine: func(_ context.Context, name string) (func(), bool) {
			return func() { released <- name }, true
		},
		Synthesize: func(ctx context.Context, _, _, _ string) ([]byte, error) {
			if attempts.Add(1) == 1 {
				entered <- struct{}{}
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return wav.SyntheticTone(160), nil
		},
	})

	id, _, err := manager.Submit(context.Background(), Request{Text: "One. Two. Three.", EngineID: DramaBoxEngineID})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("synthesis did not start")
	}
	if _, err := registry.Cancel(id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if job := waitForAudiobookTerminal(t, registry, id); job.Status != jobs.StatusCancelled {
		t.Fatalf("expected cancelled job, got %+v", job)
	}
	if engineID := <-released; engineID != DramaBoxEngineID {
		t.Fatalf("released %q, want %q", engineID, DramaBoxEngineID)
	}

	secondID := submitAudiobookEventually(t, manager, Request{Text: "Recovered.", EngineID: DramaBoxEngineID})
	waitForAudiobookJob(t, registry, secondID)
	if engineID := <-released; engineID != DramaBoxEngineID {
		t.Fatalf("second release %q, want %q", engineID, DramaBoxEngineID)
	}
}

func TestSubmitRejectsBusyAndEmpty(t *testing.T) {
	registry := jobs.NewRegistry()
	released := make(chan struct{}, 1)
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(),
		Jobs:    registry,
		ReserveEngine: func(context.Context, string) (func(), bool) {
			return func() { released <- struct{}{} }, true
		},
		Synthesize: func(ctx context.Context, _, _, _ string) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	if _, _, err := manager.Submit(context.Background(), Request{Text: "   "}); err == nil {
		t.Fatal("expected empty text rejection")
	}
	id, _, err := manager.Submit(context.Background(), Request{Text: "A sentence."})
	if err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if _, _, err := manager.Submit(context.Background(), Request{Text: "Another."}); err == nil {
		t.Fatal("expected busy rejection")
	}
	if _, err := registry.Cancel(id); err != nil {
		t.Fatalf("cancel first job: %v", err)
	}
	if job := waitForAudiobookTerminal(t, registry, id); job.Status != jobs.StatusCancelled {
		t.Fatalf("first job did not cancel: %+v", job)
	}
	select {
	case <-released:
	case <-time.After(5 * time.Second):
		t.Fatal("first job did not release its engine")
	}
}

func TestNormalizeRequestEngineAndDirectionPolicy(t *testing.T) {
	tests := []struct {
		name      string
		request   Request
		engine    string
		direction string
		errorText string
	}{
		{name: "defaults to audio", request: Request{}, engine: DefaultEngineID},
		{name: "normalizes dramabox", request: Request{EngineID: " DramaBox "}, engine: DramaBoxEngineID, direction: DefaultDramaBoxDirection},
		{name: "keeps explicit direction", request: Request{EngineID: DramaBoxEngineID, Direction: "  Calm and measured.  "}, engine: DramaBoxEngineID, direction: "Calm and measured."},
		{name: "accepts exact ASCII boundary", request: Request{EngineID: DramaBoxEngineID, Direction: strings.Repeat("x", MaxDirectionRunes)}, engine: DramaBoxEngineID, direction: strings.Repeat("x", MaxDirectionRunes)},
		{name: "accepts exact multibyte boundary", request: Request{EngineID: DramaBoxEngineID, Direction: strings.Repeat("é", MaxDirectionRunes)}, engine: DramaBoxEngineID, direction: strings.Repeat("é", MaxDirectionRunes)},
		{name: "rejects direction for audio", request: Request{Direction: "Dramatic"}, errorText: "only supported with dramabox"},
		{name: "rejects unknown engine", request: Request{EngineID: "ffmpeg"}, errorText: "must be audio or dramabox"},
		{name: "bounds direction by runes", request: Request{EngineID: DramaBoxEngineID, Direction: strings.Repeat("é", MaxDirectionRunes+1)}, errorText: "500 characters"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeRequest(test.request)
			if test.errorText != "" {
				if err == nil || !strings.Contains(err.Error(), test.errorText) {
					t.Fatalf("expected error containing %q, got %v", test.errorText, err)
				}
				if !IsRequestError(err) {
					t.Fatalf("expected request error classification, got %T", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			if got.EngineID != test.engine || got.Direction != test.direction {
				t.Fatalf("unexpected normalized request: %+v", got)
			}
		})
	}
}

func TestBuildDramaBoxPromptKeepsWordsInsideOneQuotedPassage(t *testing.T) {
	got := BuildDramaBoxPrompt("  Warm, “curious” documentary delivery.\nPause naturally.  ", `Ada said "the engine works" in 1843.`)
	want := `Warm, 'curious' documentary delivery. Pause naturally. "Ada said 'the engine works' in 1843."`
	if got != want {
		t.Fatalf("unexpected prompt:\nwant %q\n got %q", want, got)
	}
}

func TestDramaBoxNarrationReservesSelectedEngineAndRecordsProvenance(t *testing.T) {
	registry := jobs.NewRegistry()
	rootDir := t.TempDir()
	var reserved string
	var spokenText, spokenEngine string
	manager := NewManager(ManagerOptions{
		RootDir: rootDir,
		Jobs:    registry,
		ReserveEngine: func(_ context.Context, name string) (func(), bool) {
			reserved = name
			return func() {}, true
		},
		Synthesize: func(_ context.Context, text, _ string, engineID string) ([]byte, error) {
			spokenText = text
			spokenEngine = engineID
			return wav.SyntheticTone(1600), nil
		},
	})

	id, _, err := manager.Submit(context.Background(), Request{
		Title:     "Facts",
		Text:      `The witness wrote "three" in the ledger.`,
		EngineID:  DramaBoxEngineID,
		Direction: "Restrained and precise.",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	waitForAudiobookJob(t, registry, id)

	if reserved != DramaBoxEngineID || spokenEngine != DramaBoxEngineID {
		t.Fatalf("expected dramabox reservation/synthesis, reserved=%q spoken=%q", reserved, spokenEngine)
	}
	if want := `Restrained and precise. "The witness wrote 'three' in the ledger."`; spokenText != want {
		t.Fatalf("unexpected spoken text: want %q, got %q", want, spokenText)
	}
	books, err := manager.List()
	if err != nil || len(books) != 1 {
		t.Fatalf("list: %v, %d books", err, len(books))
	}
	if books[0].EngineID != DramaBoxEngineID || books[0].Direction != "Restrained and precise." {
		t.Fatalf("missing provenance: %+v", books[0])
	}
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		t.Fatalf("read audiobook root: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Fatalf("chunk staging directory leaked after completion: %s", entry.Name())
		}
	}
}

func TestDefaultNarrationPreservesLegacyProgressDetail(t *testing.T) {
	registry := jobs.NewRegistry()
	entered := make(chan struct{}, 1)
	unblock := make(chan struct{})
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(),
		Jobs:    registry,
		Synthesize: func(_ context.Context, _, _, _ string) ([]byte, error) {
			entered <- struct{}{}
			<-unblock
			return wav.SyntheticTone(160), nil
		},
	})
	id, _, err := manager.Submit(context.Background(), Request{Text: "A fact."})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	<-entered
	job, _ := registry.Get(id)
	if job.Detail != "narrating chunk 1/1" {
		t.Fatalf("default progress detail changed: %q", job.Detail)
	}
	close(unblock)
	waitForAudiobookJob(t, registry, id)
}

func TestDramaBoxFailureReleasesReservationAndRecovers(t *testing.T) {
	registry := jobs.NewRegistry()
	released := make(chan string, 2)
	var attempts atomic.Int32
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(),
		Jobs:    registry,
		ReserveEngine: func(_ context.Context, name string) (func(), bool) {
			return func() { released <- name }, true
		},
		Synthesize: func(_ context.Context, _, _, _ string) ([]byte, error) {
			if attempts.Add(1) == 1 {
				return nil, fmt.Errorf("fixture synthesis failure")
			}
			return wav.SyntheticTone(160), nil
		},
	})

	id, _, err := manager.Submit(context.Background(), Request{Text: "A fact.", EngineID: DramaBoxEngineID})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	job := waitForAudiobookTerminal(t, registry, id)
	if job.Status != jobs.StatusFailed || !strings.Contains(job.Error, "chunk 1/1 with dramabox") {
		t.Fatalf("unexpected failed job: %+v", job)
	}
	if engineID := <-released; engineID != DramaBoxEngineID {
		t.Fatalf("released %q, want %q", engineID, DramaBoxEngineID)
	}

	secondID := submitAudiobookEventually(t, manager, Request{Text: "Recovered.", EngineID: DramaBoxEngineID})
	waitForAudiobookJob(t, registry, secondID)
	if engineID := <-released; engineID != DramaBoxEngineID {
		t.Fatalf("second release %q, want %q", engineID, DramaBoxEngineID)
	}
}

func waitForAudiobookJob(t *testing.T, registry *jobs.Registry, id string) jobs.Job {
	t.Helper()
	job := waitForAudiobookTerminal(t, registry, id)
	if job.Status != jobs.StatusComplete {
		t.Fatalf("job did not complete: %+v", job)
	}
	return job
}

func waitForAudiobookTerminal(t *testing.T, registry *jobs.Registry, id string) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, _ := registry.Get(id)
		if job.Status == jobs.StatusComplete || job.Status == jobs.StatusFailed || job.Status == jobs.StatusCancelled {
			return job
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return jobs.Job{}
}

func submitAudiobookEventually(t *testing.T, manager *Manager, req Request) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		id, _, err := manager.Submit(context.Background(), req)
		if err == nil {
			return id
		}
		if !strings.Contains(err.Error(), "already narrating") && !strings.Contains(err.Error(), "is busy") {
			t.Fatalf("submit after cleanup: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("audiobook manager did not accept a follow-up job")
	return ""
}
