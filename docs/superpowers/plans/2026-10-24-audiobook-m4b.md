# Audiobook M4B delivery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Export a finished audiobook as an M4B with working chapter navigation, cover and useful book metadata.

**Architecture:** Audiobook owns derived exports tied to a specific master/render revision. Reuse the configured FFmpeg engine and existing staged-publication patterns. Library reads those export records; it does not generate or own them.

**Tech Stack:** Go, existing UI, configured FFmpeg; FFprobe for fixture validation when available.

**Spec:** [Review](../../voicestudio-ui-review-2026-10-24.md), [master plan](2026-10-24-voicestudio-improvements.md); requires stage 2 chapter boundaries.

## Global Constraints

- Retain WAV masters, attempts and render revisions. Encoding cannot mutate or replace source audio.
- Metadata changes do not require new synthesis. Chapter timing comes from rendered samples, never word-count estimates or rounded duration seconds.
- Use the existing optional FFmpeg installation; do not install tools or change engine configuration automatically.
- Cover art is copied into production-owned storage and validated; never accept arbitrary server file paths from a request.

## Review Focus

1. Selecting a different section attempt changes timings: 3A/3B.
2. Legacy books have no verified chapter boundaries: 3B provides an explicit single-chapter fallback.
3. Metadata containing Unicode/newlines/FFmetadata delimiters must round-trip safely: 3A/3B.
4. Failed/cancelled export must preserve the previous valid export and master: 3B.
5. Oversized, malformed or replaced cover files must not corrupt publication: 3A/3B.

---

## 3A — Book details and validated cover ownership

**Files:** modify `internal/audiobook/types.go`, `store.go`; create `internal/audiobook/export.go`, `export_test.go`; extend gateway and audiobook UI in existing files.

**Interfaces:** define `BookMetadata{Title, Author, Narrator, Year, Genre, Description string}` and optional cover reference owned by the book. Add `UpdateBookMetadata(id string, metadata BookMetadata) error` and `SetBookCover(id string, data []byte) error` on the Audiobook store. Accept PNG/JPEG only, at most 10 MiB and 20 million decoded pixels; metadata text fields are bounded (title/author/narrator/genre 256 characters; description 4,000; year empty or four digits).

- [x] Add tests for old manifests, metadata edits leaving synthesis identity unchanged, Unicode details and invalid/oversized cover rejection without replacing the old cover.
- [x] Run `go test ./internal/audiobook ./internal/gateway`; establish failures, implement details/cover endpoints and UI with existing multipart/atomic-write patterns, then require PASS.
- [x] Reopen a fixture book and verify its details/cover persist. Review and commit.

## 3B — Encode and publish a revision-bound M4B

**Files:** audiobook `export.go`, `types.go`, `store.go`; `internal/engine/specs.go`, `specs_test.go`; gateway wiring/handlers; audiobook export tests. Follow `internal/storybuilder/export.go` publication conventions without moving Story Builder code.

**Interfaces:** define `M4BExportRequest{RenderID string; MasterSHA256 string}` and `ExportM4B(ctx context.Context, bookID string, req M4BExportRequest) (ExportRecord, error)` on the Audiobook service. `ExportRecord` records format, selected master/revision, metadata/cover snapshot, resulting artifact hash and timestamp. Resolve paths internally. Missing render revisions on legacy books use the identified completed master and its hash, not an invented revision.

Snapshot metadata and copy the validated cover into the export staging area before encoding, so concurrent detail/cover edits cannot change a running export. Use AAC in an M4B/MP4 container with one initial spoken-word preset (96 kbit/s). Retain master sample rate/channel count when supported by the configured encoder; report incompatibility explicitly. Encode FFmetadata chapter markers from stage 2 sample boundaries. Escape metadata correctly and pass arguments directly, never through a shell. For legacy unchaptered books, show **Single chapter: [book title]** before export. Publish only after container/audio validation succeeds; replacement of a same-render export must preserve the old valid artifact on failure.

- [x] Add fixture tests for two chapter markers, zero/out-of-range boundaries, selected-attempt timing changes, metadata escaping, concurrent cover replacement, unavailable encoder, cancellation and stale master hash.
- [x] Run `go test ./internal/audiobook ./internal/engine ./internal/gateway`; establish failures, implement encoding through existing engine lifecycle and atomic publication, then require PASS.
- [x] Create `scripts/test-audiobook-m4b.ps1` using generated short PCM fixtures. With configured FFmpeg/FFprobe, assert AAC codec, M4B container, two named ordered chapters within one encoded audio frame of planned markers, metadata/cover and decodable full audio. Missing tools must be reported as SKIP, never PASS.
- [x] Review and commit after fixture export validation; no long book render is needed.

## 3C — Export discovery and real-player acceptance

**Files:** modify audiobook UI and `internal/library/readmodel.go`, `readmodel_test.go`; extend gateway artifact tests.

**Interface:** expose exports as children of their owning audiobook/render through existing Library `Entry`, `Relationship` and `ArtifactAction` fields. The export action identifies the chosen render and metadata snapshot; changing a selection does not silently relabel an older export.

- [ ] Add tests for owner/revision relationships, download content type, missing artifact disabled reason and safe rejection of path traversal.
- [ ] Add **Export M4B**, progress, readiness remedy and download/open actions to the existing audiobook result panel and Library.
- [ ] Run `go test ./internal/library ./internal/gateway` and the M4B fixture script. Open the result in an available audiobook-capable player, inspect details/cover and seek to both chapters; record the player and observed result.
- [ ] Review and commit. If player support is unavailable, mark that acceptance step outstanding rather than claim it passed.

**Stage gate:** A real M4B is downloadable and plays with correct chapter seeks and book details. The original master remains usable after success, failure and cancellation.
