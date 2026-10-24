# Audiobook chapter planning Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show the book's chapter structure and audition its actual text before committing to narration.

**Architecture:** Extend Audiobook ingestion and `PreviewDocument`; persist chapters with the owning production. Keep chapter source ranges separate from synthesis sections/chunks. Add chapter boundary recording to both current narration paths without migrating legacy Qwen books to DramaBox orchestration.

**Tech Stack:** Go, existing upload/preview UI in `internal/demo/static`, existing synthesis reservation/jobs.

**Spec:** [Review](../../voicestudio-ui-review-2026-10-24.md), [master plan](2026-10-24-voicestudio-improvements.md), [ADR-0001](../../adr/0001-audiobook-production-owns-durable-orchestration.md).

## Global Constraints

- Original source remains unchanged. Canonical text ranges use UTF-8 byte offsets; do not mix them with JavaScript character indexes.
- Chapters are editorial navigation, not engine-sized chunks. No automatic semantic chapter inference.
- Legacy books/resume retain their existing extraction and section policy; no silent repartition.
- Preview must use the same engine, voice and settings resolution as Create. Listening estimates are not compute-time promises.

## Review Focus

1. Unicode, BOM and newline handling must produce valid source ranges: 2A.
2. EPUB spine order and untitled front matter must not lose text: 2A.
3. Changed uploads/settings after preview must not submit a stale plan: 2B.
4. Both narration paths must preserve chapters without inventing retry support: 2B.
5. Audition cancellation, errors or busy engine must release resources without creating a book: 2C.

---

## 2A — Deterministic chapter extraction

**Files:** modify `internal/audiobook/ingest.go`, `types.go`; create `internal/audiobook/chapters_test.go`.

**Interfaces:** add `Chapter{ID, Title string; StartByte, EndByte int}`, `ExtractedDocument{Text string; Chapters []Chapter; PolicyVersion int}` and `ExtractDocument(filename string, data []byte) (ExtractedDocument, error)`. Keep existing `Extract` behaviour available for legacy contracts. Chapter IDs follow document order, not a mutable title.

**Initial rules:** TXT is one chapter. Markdown splits at top-level ATX headings (`# ` outside fenced code); preceding text is **Introduction**. EPUB uses readable spine documents in order, with first heading as title or **Chapter N** fallback. An EPUB spine document can contain multiple literary chapters: present this as an explicit initial limitation, not inferred structure. Headings remain spoken text. Do not offer manual chapter editing in this slice.

- [ ] Write `TestExtractDocumentChapters` table cases for all three formats, empty input, fenced Markdown, multibyte names, BOM/CRLF, front matter and missing EPUB titles. Assert ordered non-overlapping valid byte ranges and no lost narratable text.
- [ ] Run `go test ./internal/audiobook -run TestExtractDocumentChapters`; establish failure, implement extraction using current parsing/size limits, then require PASS.
- [ ] Run `go test ./internal/audiobook` to protect the old extraction contract. Review and commit.

## 2B — One preview plan, saved with production and render boundaries

**Files:** modify `internal/audiobook/manager.go`, `identity.go`, `types.go`, `sections.go`, `store.go`; `internal/gateway/gateway.go`; `internal/demo/static/index.html`, `app.js`, `styles.css`. Extend audiobook identity/sections/store tests, `internal/gateway/gateway_test.go`, `scripts/test-studio-ux.cjs`.

**Interfaces:** extend `ResolvedRequest` with `Plan *DocumentPlan`; define `DocumentPlan{Digest string; ExtractionPolicyVersion int; Chapters []Chapter}`. Add optional `Request.PlanDigest`. `PreviewDocument(ctx, DocumentRequest)` returns the plan; Create recomputes it and rejects a supplied digest that no longer matches canonical text, chapters and resolved synthesis settings. No server-side draft-plan store is needed.

Persist the plan on new manifests. Plan sections/chunks within chapter boundaries for new productions only. Record `ChapterBoundary{ChapterID string; StartSample, EndSample int64}` and sample rate with each rendered artifact/revision; boundaries refer to the actual joined master and account for the existing crossfade. Use the first contributing sample of each new chapter as its marker; end each navigation interval at the next marker, last at total samples. Legacy artifacts without recorded boundaries remain explicitly unchaptered. Chapter/section-policy identity changes apply only to the new planning policy.

- [ ] Add tests for preview/Create agreement, stale digest rejection, two-chapter Qwen and DramaBox production using fixture synthesis, chapter-boundary crossfades, and unchanged resume fingerprints for legacy WIP.
- [ ] Run `go test ./internal/audiobook ./internal/gateway`; establish new-case failures, then implement plan persistence and boundary recording in each existing narration branch. Do not add retry/resume features to the simpler branch.
- [ ] Add the chapter list, source excerpt, word count, resolved narrator and listening estimate to preview. Make the estimate visibly approximate; clear it when relevant inputs change. Show no generation-time estimate without measured evidence.
- [ ] Run the Go packages and `node --test scripts/test-studio-ux.cjs`. Verify a two-chapter fixture through preview, generation and reopen; retry/select a DramaBox section and confirm the next render records recalculated boundaries.
- [ ] Review and commit the complete plan-to-production slice.

## 2C — Audition a real passage

**Files:** modify audiobook manager/service interface, gateway handlers and audiobook UI; create `internal/audiobook/audition_test.go`; extend gateway tests.

**Interface:** add `AuditionDocument(ctx context.Context, document DocumentRequest, chapterID string) (string, error)` to the service, returning an existing job-registry ID. Re-extract the document server-side and choose the chapter's first synthesis-sized passage using the same planner and request resolution as production. Show the exact excerpt before generation. Keep preview audio in bounded temporary job storage; do not create a saved Audiobook or Library asset. Use the existing cancellation and engine reservation mechanisms.

- [ ] Add tests asserting the synthesizer receives the displayed source passage/resolved voice/settings, never the voice's generic preview text; cover missing chapter, busy engine, cancel and failure cleanup.
- [ ] Run `go test ./internal/audiobook ./internal/gateway`; establish failures, implement the audition action/player and explicit regeneration after settings change, then require PASS.
- [ ] Demonstrate a short real-engine audition and cancellation. Reopen the Library and confirm audition did not create a phantom book. Review and commit.

**Stage gate:** Import, inspect, audition and narrate a two-chapter document. The saved production exposes its chapter plan, and its render contains accurate boundaries for stage 3. Old books remain readable and old durable work resumes.
