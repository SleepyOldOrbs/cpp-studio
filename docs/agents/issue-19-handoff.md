# Issue #19 implementation handoff

Last updated: 2026-08-07

## Objective

Implement GitHub issue #19, **Recover from failed or cancelled dialogue builds**, on
branch `codex/story-builder-build-recovery`.

## Product contract

- A later synthesis failure keeps earlier completed takes ready and durable, marks the
  active clip failed with a bounded useful error, and leaves later clips stale.
- `Build stale` retries failed, stale, and orphaned building clips while skipping ready
  clips, so a failed status save cannot permanently strand work.
- Cancellation reaches the active synthesis context. Completed takes remain ready; the
  active clip returns to stale; no later clip starts.
- The latest in-memory build identity and status are available through the Gateway so a
  browser reload can resume monitoring and offer the correct retry action.
- A generated take is not published as ready until its project manifest update succeeds;
  an unsuccessful publication removes the unreferenced take.

## Decisions

- Extend the existing Story Builder build manager and Store rather than add a second job
  or persistence system.
- Keep build coordination in memory and clip/take state durable in the project manifest,
  matching the existing Story Builder ownership boundary.
- Use one cancel URL on the existing build resource and propagate a cancellable context
  through the existing Engine Reservation and synthesis callback.
- Treat a durable `building` status as retryable recovery residue. A live manager still
  prevents concurrent builds through its existing global build guard.
- Keep deterministic fixture failure and wait markers confined to the test fixture.

## Verification

- Focused Story Builder, Gateway, fixture, and demo tests pass.
- `go test ./... -count=1` passes.
- `go vet ./...` passes.
- JavaScript syntax and `git diff --check` pass.
- The 11-case local Story benchmark and fixture-backed demo smoke pass.
- The real-browser Story Builder smoke passes failure, reload, retry, cancellation,
  no-regeneration, and durable-state assertions.
- Two-axis code review found no remaining Spec finding. Its save-failure and incomplete
  Gateway-coverage findings were fixed and re-reviewed.

## Exact next step

Commit and push `codex/story-builder-build-recovery`, open the issue #19 pull request,
and wait for required checks. Merge only when the user explicitly requests it.
