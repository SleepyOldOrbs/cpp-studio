# Pronunciation corrections Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Correct recurring words and names with previewable respelling rules while retaining original text and reproducible saved work.

**Architecture:** A small local rule store and pure resolver serve two concrete consumers: Audiobook and Story Builder. Each production/project saves an explicit rule snapshot; mutable dictionary state is never consulted during resume or a queued build. This is text preprocessing, not a new speech engine.

**Tech Stack:** Go, JSON persistence and existing production/settings UI.

**Spec:** [Review](../../voicestudio-ui-review-2026-10-24.md), [master plan](2026-10-24-voicestudio-improvements.md), ADR-0001. Builds on stages 1 and 2.

## Global Constraints

- Preserve source text and source byte offsets; separately record the resolved spoken text/identity.
- No-rule legacy productions retain their existing identity and resume behaviour.
- A changed audiobook synthesis identity requires an explicit new production/restart; existing work remains recoverable.
- Do not add regex, phonemes, inline markup, automatic dictionary application or integrations across every speech tool in this stage.

## Review Focus

1. Overlapping words, Unicode and replacements containing other terms must be deterministic: 4A.
2. Dictionary edits after interruption must not change resumed speech: 4B.
3. Substitutions must not cause fidelity checking to falsely report changed source: 4B.
4. Replacement text may exceed engine limits despite short source text: 4B/4C.
5. Rule changes during a Story Builder build must not attach stale audio: 4C.

---

## 4A — Bounded rule store and substitution preview

**Files:** create `internal/pronunciation/rules.go`, `store.go`, `rules_test.go`, `store_test.go`; modify gateway wiring and existing settings UI.

**Interfaces:** `Rule{Term, Replacement, Language string}`, `Snapshot{Rules []Rule; Fingerprint string; PolicyVersion int}`, `Resolve(source, language string, rules []Rule) (string, error)` and `Freeze(rules []Rule) (Snapshot, error)`. Store one versioned JSON document through existing local-data conventions. Limits: 1,000 rules, non-empty term up to 120 characters, non-empty replacement up to 240. Reject duplicate term/language pairs.

Matching is case-sensitive literal whole-word/phrase matching using Unicode letter/number boundaries. Longest match wins; a matching explicit language takes precedence over the empty-language fallback. Replacement is one pass, never recursive. Users explicitly choose a language; do not infer it. Show both source and spoken text, with matched substitutions highlighted.

- [x] Add table tests for overlapping phrases, apostrophes, Unicode boundaries, language precedence, duplicate rules, non-recursion and deterministic snapshot fingerprints regardless of input ordering.
- [x] Run `go test ./internal/pronunciation`; establish failures, implement the small store/resolver and preview endpoints/UI, then require PASS.
- [x] Add JSON export/import using the same validated schema. Test malformed/oversized import leaves the current store unchanged; importing requires choosing replace or merge explicitly.
- [x] Review and commit with a short preview demonstration; no synthesis is required for this slice.

## 4B — Frozen audiobook corrections

**Files:** modify audiobook request/identity/types, ingestion planning, manager, sections, fidelity and store; extend existing identity/sections/fidelity/attempts tests and audiobook UI.

**Interfaces:** add optional `Pronunciation *pronunciation.Snapshot` and explicit language to the production request/manifest/identity. Bind the exact snapshot and resolver policy to the synthesis fingerprint only when rules are applied. Add `SpokenText`/spoken hash to newly planned sections while retaining their original source ranges and hashes. The same resolved text drives preview, audition, synthesis, retry and fidelity expectations.

Plan new synthesis units against both source boundaries and the engine's resolved spoken-text limit. Keep source-to-spoken mapping so safe subdivision remains possible; reject an indivisible over-limit replacement before reserving the engine. Chapter identity remains tied to source; rendered chapter boundaries remain tied to audio. Do not apply rules a second time on resume.

- [x] Add tests covering interrupted resume after global dictionary edits, frozen retry attempts, no-rule legacy fingerprint equality, longer substitutions, unchanged original manuscript and fidelity comparison against resolved spoken text. Include a replacement that changes audio duration: source chapter identity stays stable, the new render gets updated chapter times, and earlier M4B exports retain their original render association.
- [x] Run `go test ./internal/pronunciation ./internal/audiobook ./internal/gateway`; establish failures, implement the explicit **Apply pronunciation rules** preview and snapshot binding, then require PASS.
- [x] Demonstrate a corrected name in a short audition, then a fixture cancel/resume after dictionary changes. Confirm the existing production's spoken text and hashes are unchanged.
- [x] Review and commit. Offer changed rules as an explicit new production/restart, never silent continuation.

## 4C — Story Builder uses the same resolver and frozen project rules

**Files:** modify Story Builder project/update types, dialogue build inputs/completion checks, gateway and Script controls; extend build/store/state tests.

**Interfaces:** add optional `Project.Pronunciation *pronunciation.Snapshot` and language through the existing revision-checked update. Resolve each line from original `TimelineClip.Text` at build time using the saved snapshot. Bind resolved text and snapshot policy to build completion checks. Applying a new snapshot invalidates only lines whose resolved synthesis input changes; historical takes remain retained.

- [x] Add tests for rule changes during a build, unchanged-line take reuse, over-limit rejection, source preservation, undo/save consistency and old projects with no snapshot.
- [x] Run `go test ./internal/pronunciation ./internal/storybuilder ./internal/gateway` and `node --test scripts/test-story-builder-state.cjs`; establish failures, implement explicit apply/preview, then require PASS.
- [x] Audition a corrected line, change the global dictionary and prove the project is unchanged until explicitly applying a new snapshot. Review and commit.

**Stage gate:** The same rule behaves consistently in audiobook and scripted dialogue, source stays intact, and later dictionary edits cannot silently change saved or resumed production.

## Acceptance evidence

Implemented and independently reviewed in the pronunciation branch. Full Go suite and vet, 70 Node checks, delayed dictionary-load browser acceptance, frozen book/project apply and saved rules, large byte-bounded project previews, and native OmniVoice audition/dialogue verified by exact Whisper transcripts passed. Review regressions cover initial-load replacement safety, resolved DramaBox warning parity and all 2,048 supported project clips. Existing source and prior render/export identities remain intact.
