# Issue #21 implementation handoff

Last updated: 2026-08-07

## Objective

Implement GitHub issue #21, **Open a retained Story as a separate project**, on
branch `codex/story-builder-story-import`, stacked on issue #20 commit
`d3e370a`.

## Product contract

- A retained Story opens through an explicit speaker-to-Character-Voice mapping
  flow and creates a new Story Builder Project. It never edits the Story.
- One Dialogue Track is created per Story cast member in cast order. Imported
  clips preserve line order and available source gap/take duration timing.
- Exactly one Character Voice beneath the Story Actor Voice is suggested; zero
  or multiple matches remain unresolved. Every speaker must be explicitly
  mapped, and the browser can create a Character Voice inside the mapping flow.
- A current take is ready only when its text, Actor Voice, WAV, and recorded
  duration still match. Its bytes are copied into the new project's `takes`
  directory. Missing or mismatched material becomes stale with the Story text.
- Story, line, and take ids are server-owned provenance on the new clip only.
  The source Story manifest and take files remain unchanged.
- The project manifest and copied ready takes publish atomically, so a failed
  import leaves no partial project.

## Decisions

- Keep import orchestration in `internal/storybuilder`. The Story manager exposes
  only read-only retained-manifest and retained-take methods; the Gateway only
  decodes, delegates, and shapes HTTP responses.
- Blank projects and Story imports use the Store's same staged publication
  transaction; an import merely adds its validated take copies before publish.
- Reuse the existing Character Voice resolver and creation endpoint. Do not add
  a default voice, import-specific voice type, or parallel voice-creation API.
  Both browser surfaces call the same creation helper and apply their own error
  and selection behavior.
- Use the Story render's 350 ms inter-line gap plus stored before/after nudges.
  When no take duration is available, give stale dialogue the browser's existing
  2400 ms new-dialogue duration so it remains visible and editable.
- Preserve import provenance through later whole-project saves, but reject
  client attempts to create or replace it.

## Verification

- `node --check internal/demo/static/app.js` passes.
- `go test ./... -count=1`, `go vet ./...`, and `git diff --check` pass.
- `scripts/smoke-story-builder-browser.ps1 -GatewayPort 8897 -OutDir
  .\out\story-builder-browser-smoke-issue21e` passes. It covers the unique
  preselection, ambiguous unresolved state, required mapping, inline Character
  Voice creation, separate-project navigation, track order, ready/stale take
  handling, provenance, and before/after source manifest and take hashes.
- `scripts/verify.ps1` passes, including the local Story benchmark and
  fixture-backed demo UI smoke.

## Exact next step

Complete the two-axis review against `d3e370a`, then commit and publish a draft
pull request based on `codex/story-builder-playback`. Do not merge without
explicit user approval.
