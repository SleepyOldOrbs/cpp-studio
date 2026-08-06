# Issue #18 implementation handoff

Last updated: 2026-08-06

## Objective

Implement GitHub issue #18, **Build stale dialogue through the audio engine**, on
branch `codex/story-builder-build-dialogue`.

## Required workflow

1. Work test-first at the already approved Store, Gateway/fixture-engine, and real-browser seams.
2. Run the `code-review` skill against `origin/main` on Standards and issue #18 Spec axes.
3. Address every actionable finding and commit the cohesive result.
4. Publishing and merging remain a later explicit step.

## Product contract

- `Build stale` is asynchronous, returns `202 Accepted`, and exposes a pollable build identity,
  active clip, completed/total count, and progress.
- Stale and failed Dialogue Clips build in timeline order; ready clips are skipped.
- The shared `audio` Engine Reservation is held for the build, so direct speech, Story production,
  and Story Builder cannot race.
- Each successful WAV is validated, copied into project-owned immutable takes, and made ready in an
  atomic project save before the next clip begins.
- A synthesis failure marks the active clip failed, preserves earlier ready takes, leaves later clips
  stale, and stops that build.
- A second build cannot run concurrently for the same project.
- Ready Dialogue Clips can be auditioned directly without creating a master render.
- The browser refreshes durable project and build state from the Gateway; it never invents completion.

## Decisions

- Extend the existing `internal/storybuilder` aggregate and Store; do not create a second project store.
- Add one small Story Builder build manager for in-memory async coordination around durable clip state.
- Reuse the existing Gateway speech callback and reserve the existing `audio` slot once for the whole build.
- Keep generated takes under each project and serve only the ready clip's validated, referenced WAV.
- Preserve accepted timeline timing when attaching a generated source; trimming remains nondestructive.
- Cancellation and restart recovery remain issue #19 scope.

## Approved test seams

- Store: chronological candidates and atomic generated-take state transitions.
- Gateway with fixture Engine: `202`, polling, persistence, busy behavior, partial failure, and audition.
- Real browser with fixture-backed Gateway: visible state/progress, Build stale, reload, and audition.

## Current state

- Issue #18 is open, `ready-for-agent`, and blocked only by closed issue #16.
- Branch created from merged `origin/main` at `fde98f4`.
- Store/build manager, Gateway routes, browser controls, fixture-backed browser
  journey, and missing-take reporting are implemented locally.
- Focused and full Go tests, `go vet`, the repository verification script,
  JavaScript syntax, and diff checks are green.
- The clean fixture-backed real-browser journey is green on port 8898, including
  visible build progress, two durable ready takes, and WAV audition.

## Exact next step

Commit the cohesive implementation so `code-review` can compare the branch to
`origin/main`, then run its Standards and issue #18 Spec axes. Address every
actionable finding in a follow-up commit. Do not push or open a PR without
explicit direction.
