# Issue #20 implementation handoff

Last updated: 2026-08-07

## Objective

Implement GitHub issue #20, **Play, seek, and audition the complete timeline**, on
branch `codex/story-builder-playback`, stacked on the reviewed issue #19 commit
`1568f07292a8efbfd14c78abcab07b5f138d4ddc`.

## Product contract

- Play starts from the visible playhead. Pause retains the current position, and a seek
  controls the next schedule.
- Browser scheduling uses clip start and duration, nondestructive source trim, track
  mute, silence, and simultaneous clips across tracks. Zoom and horizontal scroll do
  not alter timing.
- Isolated audition plays only the selected ready audio-backed clip.
- Playback and audition use existing project-owned WAV routes. They do not create a
  render or change the project revision.
- Stale dialogue and missing or unreadable project media remain visible as transport
  warnings instead of being treated as current audio.
- Starting Play, audition, a timeline edit, Undo/Redo, revoice, media placement, or a
  dialogue build stops conflicting browser sources.
- Play, Pause, and Playhead expose native accessible names; Space toggles Play/Pause
  when focus is outside an editable field.

## Decisions

- Keep playback browser-owned and read-only. Do not add a render job, persistence
  record, backend playback service, or dependency.
- Fetch the existing validated dialogue-take and project-media routes and schedule
  decoded buffers with the Web Audio API.
- Use a monotonically changing playback token plus direct source stops to discard
  superseded async loads and stop already scheduled audio.
- Derive the visible playhead from the actual rendered timeline stage so short projects,
  wide viewports, zoom, and lane padding share the same coordinate system as clips.
- Treat omitted zero-valued `source_in_ms` as zero in both validation and playback,
  matching the JSON contract.

## Verification

- `node --check internal/demo/static/story-builder.js` passes.
- `go test ./... -count=1` and `go vet ./...` pass.
- `git diff --check` passes; the PowerShell LF-to-CRLF message is only a working-copy
  warning.
- `scripts/verify.ps1` passes, including the local Story benchmark and fixture-backed
  demo UI smoke.
- `scripts/smoke-story-builder-browser.ps1 -GatewayPort 8898 -OutDir
  .\out\story-builder-browser-smoke-issue20-final` passes. Its deterministic browser
  clock covers Play/Pause, seek offsets and remaining durations, overlap, trim, mute,
  silence, zoom/scroll invariance, conflicting-source stops, isolated audition,
  revision invariance, stale dialogue, missing media, rendered playhead alignment, and
  Undo stopping the old schedule.
- Two-axis Standards and issue #20 Spec review reports no remaining actionable findings.

## Exact next step

Commit and push `codex/story-builder-playback`, open a draft pull request with
`codex/story-builder-build-recovery` as its base, and wait for required checks. After
issue #19 merges, retarget this pull request to `main`. Merge only when the user
explicitly requests it.
