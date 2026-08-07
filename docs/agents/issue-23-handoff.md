# Issue #23 implementation handoff

Last updated: 2026-08-07

## Objective

Implement GitHub issue #23, **Export mixed masters as WAV, MP3, and FLAC**, on
branch `codex/story-builder-exports`, stacked on issue #22 commit `816351d`.

## Product contract

- Every immutable render revision keeps its WAV download and may record one MP3
  and one FLAC delivery export.
- Encoder availability comes from the operator's configured ffmpeg. An
  unavailable requested encoder is reported clearly and does not mutate the
  project.
- Re-exporting the same revision and format atomically replaces only that
  derived file. It never changes the numbered WAV or another revision/format.
- Failed or cancelled encoding, derived-file publication failure, or manifest
  failure preserves the render, all existing exports, and the project revision;
  temporary files are removed.
- Export URLs are server-derived and whitelist only exact positive decimal
  revisions plus `mp3` or `flac`. No caller-provided path is accepted.

## Decisions

- Keep export identity, publication, rollback, and metadata in the Story Builder
  Store beside its render revisions. Gateway only validates requests, maps
  errors, and serves resolved files.
- Reuse the existing ffmpeg capability probe and transcode path. Add FLAC as the
  one required engine delivery format without adding a second encoder system.
- Keep retained Story exports at their existing MP3/Opus boundary; FLAC support
  is exposed to Story Builder and the general audio encoding route only.
- Use the current project revision for optimistic concurrency and the URL render
  revision for immutable source selection.

## Verification

- Focused Store, Gateway, engine, retained Story, fixture, and browser tests
  cover MP3/FLAC export, capability reporting, replacement, revision isolation,
  immutable WAV preservation, rollback, cancellation, and path rejection.
- Run `go test ./... -count=1`, `go vet ./...`, `git diff --check`,
  `scripts/verify.ps1`, and the real-browser Story Builder smoke before review.

## Exact next step

Run the two-axis review against `816351d`, address all findings, then commit,
push, and open a draft pull request based on `codex/story-builder-render`. Do
not merge without explicit user approval.
