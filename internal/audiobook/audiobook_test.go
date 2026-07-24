package audiobook

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"strings"
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
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(),
		Jobs:    registry,
		Synthesize: func(_ context.Context, text, voiceID string) ([]byte, error) {
			synthesized = append(synthesized, text)
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
	if job.Result["title"] != "Test Book" || job.Result["artifactUrl"] == "" {
		t.Fatalf("unexpected result: %+v", job.Result)
	}

	books, err := manager.List()
	if err != nil || len(books) != 1 {
		t.Fatalf("list: %v, %d books", err, len(books))
	}
	if books[0].Title != "Test Book" || books[0].Chunks != chunks {
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
	block := make(chan struct{})
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(),
		Jobs:    registry,
		Synthesize: func(ctx context.Context, _, _ string) ([]byte, error) {
			select {
			case <-block:
				return wav.SyntheticTone(160), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	})

	id, _, err := manager.Submit(context.Background(), Request{Text: "One. Two. Three."})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := registry.Cancel(id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if job, _ := registry.Get(id); job.Status == jobs.StatusCancelled {
			close(block)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job never reached cancelled")
}

func TestSubmitRejectsBusyAndEmpty(t *testing.T) {
	manager := NewManager(ManagerOptions{
		RootDir: t.TempDir(),
		Synthesize: func(ctx context.Context, _, _ string) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	if _, _, err := manager.Submit(context.Background(), Request{Text: "   "}); err == nil {
		t.Fatal("expected empty text rejection")
	}
	if _, _, err := manager.Submit(context.Background(), Request{Text: "A sentence."}); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if _, _, err := manager.Submit(context.Background(), Request{Text: "Another."}); err == nil {
		t.Fatal("expected busy rejection")
	}
}
