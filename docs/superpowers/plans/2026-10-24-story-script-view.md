# Story Builder script authoring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a user read, edit, cast and audition a short drama as a script while keeping the existing timeline authoritative.

**Architecture:** Script and Timeline are views of the same Project, Tracks and TimelineClips. Reuse `createEditSession`, project revision checks and the dialogue build manager. Add only the clip-specific delivery override needed by this feature.

**Tech Stack:** Go and existing Story Builder HTML/CSS/JavaScript; Node tests and isolated browser smoke.

**Spec:** [Review](../../voicestudio-ui-review-2026-10-24.md), [master plan](2026-10-24-voicestudio-improvements.md), `CONTEXT.md` Story Builder terms.

## Global Constraints

- No second script store; stable clip IDs, track voice ownership, source provenance and immutable historical takes remain intact.
- No changes to grounded Podcast Generator or implicit changes to saved Character Voices.
- Do not silently reflow timing or overwrite a loaded project with an example.
- Unsupported performance controls are not presented as working controls.

## Review Focus

1. Same-time dialogue must have a deterministic display order: 1A.
2. Script edits during autosave must survive reconciliation: 1A.
3. Moving a line to a character with overlapping dialogue must reject cleanly: 1B.
4. Text or direction changes during generation must reject the stale result: 1B/1C.
5. Loading the example with unsaved work must preserve that work: 1D.

---

## 1A — Script and Timeline share one edit session

**Files:** modify `internal/demo/static/story-builder.html`, `story-builder.js`, `story-builder.css`; extend `scripts/test-story-builder-state.cjs` and `scripts/smoke-story-builder-browser.ps1`.

**Interface:** add `scriptRows(project)` in `story-builder.js`, returning references by `{clipID, trackID, sceneID}` sorted by clip start, track order, then existing clip order. No new persisted schema. Script text edits use the existing `updateClip(id, changes)`/`dialogueTextChanges` path. Scene membership follows existing scene starts.

- [ ] Add state tests asserting deterministic ties and that a Script text edit made during save remains after reconciliation, undo and redo.
- [ ] Run `node --test scripts/test-story-builder-state.cjs`; confirm the added assertions fail before implementation.
- [ ] Add Script/Timeline view switching, compact dialogue rows, scene headings and a shared selected clip. Show silence/SFX/music as concise cues, with timing editing in Timeline.
- [ ] Extend the browser smoke to edit in Script, switch views, undo/redo, save/reopen and verify the same clip ID/text. Run the Node command and `pwsh -File scripts/smoke-story-builder-browser.ps1` against its isolated data; both must pass.
- [ ] Review and commit this slice independently.

## 1B — Character reassignment and selected-line generation

**Files:** modify `internal/storybuilder/store.go`, `build.go`, `internal/gateway/gateway.go`, Story Builder JS/HTML; extend `internal/storybuilder/build_test.go`, `store_test.go`, `internal/gateway/gateway_test.go` and the Story Builder browser smoke.

**Interfaces:** keep `DialogueBuildManager.Start(ctx, projectID, revision)` unchanged for bulk generation; add `StartClip(ctx context.Context, projectID string, revision int, clipID string) (DialogueBuild, error)` using the same candidate validation, reservation and completion path. Extend the existing build HTTP action with optional `clip_id`; absent retains bulk behaviour. Reassignment moves the clip to a Dialogue track bound to the chosen Character Voice, retaining its ID and start time.

- [ ] Add tests proving selected build touches exactly one eligible clip, rejects an unknown/non-dialogue clip, honours revision/busy errors and refuses outdated completion after a text edit.
- [ ] Add a reassignment test: conflicting placement leaves the whole project unchanged; successful placement changes only the chosen clip's binding/take validity, not other clips on either track.
- [ ] Run `go test ./internal/storybuilder ./internal/gateway` and confirm the new cases fail for the intended missing behaviour.
- [ ] Implement the selected-line path and character chooser through existing save/build mechanisms. Use existing `auditionClip(clip)` for a ready take; distinguish **Generate line** from **Play take**, with explicit missing-voice/engine remedies.
- [ ] Run the focused Go packages and browser smoke. Demonstrate two lines with different characters and prove only the requested line was generated. Review and commit.

## 1C — Per-line delivery without mutating the Character Voice

**Files:** modify `internal/storybuilder/store.go`, `build.go`, `internal/gateway/gateway.go`, Story Builder JS/HTML; extend `internal/storybuilder/build_test.go`, `drama3_test.go`, `internal/gateway/storybuilder_drama3_test.go` and state tests.

**Interface:** add optional `TimelineClip.DirectionOverride string` (`direction_override`), empty meaning inherit the Character Voice direction. Show the resolved inherited direction. A non-empty override replaces that direction for the line and uses the existing supported direction parser/validation. Include the resolved value in `DialogueSynthesisInput` and completion identity checks. Existing empty-override projects keep their previous behaviour and fingerprints.

- [ ] Add tests for inheritance, override, clearing back to inheritance, unsupported engine/direction rejection and stale completion when direction changes during build.
- [ ] Run the focused packages to establish the new failures, then implement persistence, resolution and line controls. Reuse engine limits; do not add generic speed or emotion sliders.
- [ ] Run `go test ./internal/storybuilder ./internal/gateway` and the Story Builder state tests. Verify a loaded legacy project needs no migration and a saved Character Voice remains unchanged.
- [ ] Audition one short line with the configured compatible engine; distinguish a successful API call from audible behaviour. Review and commit.

## 1D — A safe, small example scene

**Files:** modify Story Builder JS/HTML and browser smoke; use existing project creation and Library media-placement APIs.

**Interface:** **Try an example** creates a separate named project using two explicitly selected saved Character Voices, four original dialogue lines and one silence clip. Offer one SFX placement using a user-selected Library item; show a concise empty-state remedy when none exists. No bundled voice reference or automatic generation.

- [ ] Add browser assertions that the example receives a new project ID, existing unsaved work follows the current save/leave protection, and loading the example does not start synthesis.
- [ ] Implement the example chooser and original scene text; use existing project/media APIs so provenance and copying rules remain unchanged.
- [ ] Run the browser smoke and manually complete the short example at a normal window size using keyboard navigation. Confirm no nested scrolling is required to read four lines. Review and commit.

**Stage gate:** A new user can open the example, assign characters, edit and generate one line, play the result, switch to Timeline and reopen the same project. Existing project/save tests pass. Stop here before starting the next stage.
