# VoiceStudio-inspired improvements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver the five recommended improvements in independently reviewable stages, starting with Story Builder and preserving existing productions.

**Architecture:** Extend the current Story Builder, Audiobook and Library owners. Chapters describe a book; sections remain synthesis units. Reuse the existing UI, engine lifecycle and production storage rather than introduce another application framework or catalogue.

**Tech Stack:** Go, existing HTML/CSS/JavaScript, Node test runner, PowerShell browser checks, optional configured FFmpeg for M4B.

**Spec:** [Live VoiceStudio review](../../voicestudio-ui-review-2026-10-24.md), [domain](../../../CONTEXT.md), [ADR-0001](../../adr/0001-audiobook-production-owns-durable-orchestration.md).

## Global Constraints

- This is a planning deliverable, not implementation approval or a combined redesign.
- Preserve original source, existing project identities, production takes, masters and recoverable work.
- Story Builder owns scripted fiction; the grounded Podcast Generator keeps its factual-source semantics.
- Audiobook owns ingestion, planning and durable orchestration. Library remains a read model over existing owners.
- No new dependency or generic framework without a demonstrated need. Do not turn UI changes into broad refactors.
- Use isolated fixture data for automated checks. Real-engine acceptance is a short sample, not a full book.
- Each stage must work independently before starting the next. An unfinished later stage must not be required to use an earlier one.

## Review Focus

1. Existing projects and interrupted books must still open/resume: compatibility checks in stages 1, 2 and 4.
2. Editing while a build/save is in flight must not attach outdated audio: stage 1 identity and reconciliation checks.
3. Chapter markers must correspond to the actual selected audio, including crossfades: stages 2 and 3 timing checks.
4. Changed pronunciation rules must not silently alter resumed production: stage 4 frozen-snapshot checks.
5. A visible voice must not imply an available compatible engine: stage 5 readiness/action checks.

---

## Delivery sequence

| Stage | Outcome | Reviewable slices | Dependency | Completion demonstration |
| --- | --- | --- | --- | --- |
| [1. Story Builder script authoring](2026-10-24-story-script-view.md) | Compact script view, character assignment, line audition, delivery override and a small example | 4 | Existing Story Builder | Edit a line in Script, see the same clip in Timeline, audition it, reopen the project |
| [2. Audiobook chapter plan and passage audition](2026-10-24-audiobook-chapter-plan.md) | Inspect chapters and hear the selected narrator read the actual manuscript before production | 3 | Existing Audiobook | Import a two-chapter book, inspect the plan, audition a passage, produce and reopen it |
| [3. M4B delivery](2026-10-24-audiobook-m4b.md) | Chaptered M4B with author, narrator, cover and book metadata | 3 | Stage 2 chapter map and render boundaries | Open exported M4B in a real player; seek to both chapters and inspect cover/details |
| [4. Pronunciation corrections](2026-10-24-pronunciation-corrections.md) | Reusable corrections with visible substitutions and frozen production snapshots | 3 | Stage 2 planning; stage 1 line builds | Correct a name, preview it, resume after changing the dictionary, prove the saved production did not change |
| [5. Voice browsing and Library polish](2026-10-24-voice-library-polish.md) | Useful voice-design starters, saved-voice audition and clear compatible reuse actions | 3 | Existing design/Library; show exports from stage 3 | Generate candidates from a starter, save one and use it in a production without losing context |

**Working estimate: 16 reviewable slices, not one release-sized change.** This is a scope breakdown, not a promise of 16 sessions or a calendar estimate. Stage 1 can ship first. Stages 2 and 4 have the greatest compatibility risk; stage 3 has the strongest dependency. Stage 5 is mostly independent and can move earlier if a quicker usability improvement becomes the priority.

## What current code already gives us

Execution status (24 October 2026): Stage 1 implementation and independent review
are complete and merged in GitHub PR #56 (merge 15ee10d). Its final verification passed
the Go suite, 56 Node state regressions, and the complete Story Builder browser
smoke, including focused editing during a delayed save and an 800-pixel window.
Local OmniVoice produced the example's opening sentence as a 3.14-second take;
the entire take remained available and Whisper transcribed the complete source.
Stage 2A extraction is implemented and committed as 20bc750; stage 2B is next. Stages 2–5 remain tracked as GitHub issues #52–#55; stage 1 issue #51 is closed.

- Story Builder has stable clip/track IDs, revision checks, edit history, timeline editing, voice bindings, stored takes and `auditionClip`. A script view should project that data, not create a second document.
- The audiobook `PreviewDocument` path resolves the source, engine and voice without durable state. Extend it for chapter planning.
- DramaBox currently has durable sections, attempts and render revisions. The other narration path uses simpler chunks. Stage 2 adds chapter information to both without pretending that both already have identical retry/resume support.
- Existing FFmpeg delivery and validation in `internal/engine/specs.go` and Story Builder export publication provide patterns for stage 3, not a reason to generalise every exporter.
- The Library already projects owner records through `internal/library/readmodel.go`; stage 5 improves that surface.

## Scope boundaries

Included: the five outcomes above and the changes needed to keep them correct and recoverable.

Deferred: automatic free-prose casting, a full script-import language, drag-reordering that silently retimes dialogue, universal emotion/speed markup, multi-narrator audiobook production, pronunciation phoneme engines, remote voice catalogues, favourites/accounts, dubbing, translation, telephony and a global visual redesign. MP3 already has a separate delivery precedent; extending audiobook MP3 is not required for the initial M4B gate.

## How to execute and resume

- [ ] Start with stage 1, slice 1A. Re-read its named files and confirm current signatures before changing code; these plans describe the 2026-10-24 checkout.
- [ ] Deliver one slice at a time with a focused diff, relevant tests and a short demonstration. Commit each accepted slice separately; do not combine all five stages into one PR.
- [ ] At each stage gate, record what was demonstrated, what failed and the next unchecked slice. Link the commit/PR and any fixture output here or in the corresponding GitHub issue.
- [ ] Use GitHub Issues as the execution tracker when implementation begins; check for existing matching issues before creating one per stage. These local documents are the implementation specifications, not a replacement tracker.
- [ ] Reassess the next stage after using the completed one. Broader changes require a concrete reason and a separate scope decision.

No application tests or rendering are required to approve these documents. During implementation, use the focused commands in each subplan, then any additional checks required by repository policy. Do not repeat broad suites after every UI change without a reason.

## Planning review

The five recommendations are mapped to stages 1–5. The sample is included in stage 1; chapter identity, source passage preview and dual narration paths are included in stage 2; real container validation is included in stage 3; source preservation and synthesis identity are included in stage 4; three-candidate voice design and existing Library ownership are preserved in stage 5. The remaining review suggestions are explicitly deferred above.
