# Voice browsing and Library polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make it easier to find, audition and reuse an appropriate voice or production without expanding into another catalogue.

**Architecture:** Add a small set of local voice-design descriptions to the existing candidate workflow. Improve browsing and return-to-tool actions through the current Library read model. Actor Voice and Character Voice retain their distinct ownership and meanings.

**Tech Stack:** Existing HTML/CSS/JavaScript and Go Library/gateway code. No remote catalogue or new persistent store.

**Spec:** [Review](../../voicestudio-ui-review-2026-10-24.md), [master plan](2026-10-24-voicestudio-improvements.md), `CONTEXT.md` Voice Library and Library definitions.

## Global Constraints

- Design descriptions are starting points, not saved voices or guaranteed audio identities.
- Keep the existing three-candidate comparison and explicit save flow.
- Only filter on real stored metadata; do not infer personal attributes from audio or names.
- Reuse actions must respect the target tool's engine/reference compatibility and preserve unsaved work.
- No favourites store, marketplace, account system or duplicate project catalogue.

## Review Focus

1. A starter must not appear as an already generated/saved voice: 5A.
2. Candidate generation with a missing engine must give a direct remedy: 5A.
3. Search/filter changes must preserve the correct selected voice and audition: 5B.
4. Missing/deleted references or incompatible target engines must disable misleading actions: 5C.
5. Opening a production or assigning a voice must not silently discard unsaved work: 5C.

---

## 5A — A small curated starting set for voice design

**Files:** modify `internal/demo/static/index.html`, `app.js`, `styles.css`; extend `scripts/test-studio-ux.cjs`.

**Interface:** six static, original description presets in the existing design UI, each `{id, name, description, purpose}`. Initial purposes: calm narration, documentary explanation, warm storytelling, energetic presentation, understated dialogue and theatrical dialogue. No real-person imitation, fabricated sample audio or promised accents. **Use description** fills the editable prompt; **Generate candidates** retains the existing explicit action.

- [x] Add a focused UI state assertion that choosing a preset changes only the description and does not generate, save or replace a selected Actor Voice.
- [x] Run `node --test scripts/test-studio-ux.cjs`; establish the new failure, implement the compact starter chooser and engine readiness copy, then require PASS.
- [x] Generate one short three-candidate set with the configured engine and save the chosen candidate through the existing flow. Review and commit.

## 5B — Saved voices are easy to scan and audition

**Files:** modify existing Library/voice UI in `index.html`, `app.js`, `styles.css`; change `internal/library/readmodel.go` only where already-owned metadata is missing; extend `readmodel_test.go` as needed.

**Interface:** reuse `ReadModel.List(query)` and `Entry` identity/metadata/actions. Provide search and Actor Voice/Character Voice filters, clear inherited voice relationships, existing preview audio and ready/unavailable states. Keep at most one audition playing. Do not advertise language/accent filters unless those fields actually exist in the saved records.

- [x] Add read-model cases for Actor/Character relationships, empty search results and unavailable preview actions. Add a UI state case for filter changes while preview audio is playing.
- [x] Run `go test ./internal/library` and `node --test scripts/test-studio-ux.cjs`; establish relevant new failures, implement the browsing layout and player behaviour, then require PASS.
- [x] Inspect keyboard focus, labels, contrast and a narrow window using a fixture library. Avoid oversized cards and nested scroll panes. Review and commit.

## 5C — Clear reuse and return-to-production actions

**Files:** modify `internal/library/readmodel.go`, `readmodel_test.go`, relevant gateway action resolution and existing app/Story Builder UI; extend gateway tests and browser smoke.

**Interface:** extend existing `Action`/`LaunchAction` projections with explicit target actions and `DisabledReason` when needed. Voice actions are **Use for audiobook** and **Assign in Story Builder**; project actions reopen their owner; artifact actions identify master/render/export. A pending target voice ID is validated by the receiving tool before assignment. Never change a global default engine as a side effect.

- [x] Add tests for compatible selection, missing/deleted voice reference, incompatible engine, stable owner/revision links and disabled export actions when artifacts are missing.
- [x] Implement contextual reuse: selecting a Character Voice for a book resolves its existing Actor/reference and supported direction, while Story Builder asks which existing Dialogue track or selected line receives it. Preserve current edits through the receiving tool's established save/leave handling.
- [x] Run `go test ./internal/library ./internal/gateway`, `node --test scripts/test-studio-ux.cjs` and the Story Builder browser smoke where its flow changed.
- [x] Demonstrate saved voice → audiobook preview and saved voice → selected Story Builder line; reopen a book/project from Library and download the correct render/export. Review and commit.

**Stage gate:** A user can start from a description, compare/save a voice, locate it again and use it in the appropriate production. Every enabled action works; unavailable actions explain the concrete remedy.
