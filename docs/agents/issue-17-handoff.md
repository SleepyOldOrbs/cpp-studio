# Issue #17 implementation handoff

Last updated: 2026-08-06

## Objective

Implement GitHub issue #17, **Place reusable SFX and Music in projects**, on
branch `codex/story-builder-library-audio`.

## Required workflow

1. Implement with tests at the Store, Gateway, fixture-media, and real-browser seams.
2. Run the `code-review` skill and address all actionable findings.
3. Commit the cohesive result. Publishing and merging require the user's later direction.

## Product contract

- Reusable Library audio is classified as SFX, Music/ambience, or utility and exposes duration.
- Only compatible SFX and Music tracks accept placement; rejection must not mutate the project.
- First placement validates a trusted Library-owned audio path, copies immutable bytes into the
  project's own `media` directory, and records Library provenance.
- Reusing the same source within a project reuses the project copy.
- Deleting the Library source after placement must not break the project copy.
- Missing or unreadable project media becomes an explicit affected-clip error.
- Browser-provided filesystem paths are never accepted.

## Decisions

- Extend `internal/storybuilder.Store`; do not introduce a second project or media store.
- Resolve Library asset identity server-side through the existing Library/reusable-audio owner.
- Keep project manifests as the optimistic-concurrency boundary.
- Use existing WAV validation and existing Library classification patterns.
- Implement the smallest API addition that makes placement atomic and testable.

## Current state

- Issue #17 inspected and confirmed `ready-for-agent`.
- `CONTEXT.md` reviewed; the only ADR concerns Audiobook production and does not constrain this work.
- Branch created from merged `main` (`1471002`).
- Initial implementation plan recorded on issue #17.
- Library audio now has server-derived `mediaRole` (`sfx`, `music`, or `utility`) and `durationMs`.
- Story Builder has a dedicated `POST /v1/story-builder-projects/{id}/library-audio` placement route.
- The Store validates and copies WAV bytes into `project/media`, deduplicates by Library item ID,
  records provenance on the clip, and derives `media_error` when the project copy is broken.
- `GET /v1/story-builder-projects/{id}/media/{source-id}` serves only referenced project media.
- Ordinary whole-project saves cannot introduce a new SFX/Music source; the dedicated route owns first placement.
- The browser dock now loads `/v1/library`, groups reusable audio by role with duration, and provides
  drag-and-drop plus named Add buttons. Placement is an atomic server mutation guarded against concurrent edits.
- Incompatible drops are visibly rejected before mutation; missing media is visible on the clip and Selection panel.
- Cohesive feature commit: `7cbad5b feat: place reusable Story Builder audio (#17)`.
- Code review completed against `origin/main` on both Standards and issue #17 Spec axes.
- Standards finding addressed: unknown legacy audio roles now degrade safely to utility audio while new writes remain strict.
- Standards judgement addressed: the repeated browser media-role-to-track mapping now has one small helper.
- Spec finding addressed: Store coverage now corrupts the project-owned WAV and proves unreadable media is an explicit clip error.
- Standards and Spec re-reviews report no remaining actionable findings.

## Verification log

- Red tests were added first for Library metadata and Store/Gateway placement behaviour.
- `go test ./internal/library ./internal/storybuilder` — pass.
- `go test ./internal/gateway -run '^TestStoryBuilderPlacesDurableLibraryAudioThroughGateway$' -count=1` — pass.
- `node --check internal/demo/static/story-builder.js` — pass.
- `go test ./internal/library ./internal/storybuilder ./internal/gateway ./internal/demo -count=1` — pass.
- `scripts/smoke-story-builder-browser.ps1 -GatewayPort 8892 -OutDir .\\out\\story-builder-browser-smoke-17-red` — pass.
- `go test ./... -count=1` — pass.
- `go vet ./...` — pass.
- `scripts/verify.ps1` — pass, including fixture Gateway/UI/package smoke coverage.
- Post-review `go test ./internal/library ./internal/storybuilder -count=1` — pass.
- Post-review `node --check internal/demo/static/story-builder.js` — pass.
- Post-review `go test ./... -count=1` — pass.
- Post-review `go vet ./...` — pass.
- Post-review `scripts/smoke-story-builder-browser.ps1 -GatewayPort 8893 -OutDir .\\out\\story-builder-browser-smoke-17-review` — pass.
- Post-review `git diff --check` — pass.

## Exact next step

Record the review-fix commit on GitHub issue #17, then—after user authorization—push the branch,
open its PR, wait for CI, and merge it.
