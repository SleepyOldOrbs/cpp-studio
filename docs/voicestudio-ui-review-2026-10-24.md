# VoiceStudio desktop review — 24 October 2026

## Scope and evidence

Reviewed the running VoiceStudio desktop application through Windows Computer Use. Its Updates screen reported version **0.5.6**. The supplied [release page](https://github.com/debpalash/VoiceStudio/releases/tag/v0.5.6) is a reference; the observations below come from the live UI.

Visited Stories, Audiobook, Clone, Design, Saved voices, Gallery, Dub, Batch Dub, Transcribe, Projects, every Tools tab, Integrations, and selected Settings pages (General, Appearance, Models/Translation, Pronunciation dictionary, Compute device, Storage and Updates). This is a broad interface and workflow review, not an exhaustive test of every setting or engine.

Loaded the built-in sample into the initially empty Stories and Audiobook editors. Opened controls and the audiobook plan preview. No generation, imports of user files, model downloads, project saves, deletions or configuration changes were requested or performed. The two sample drafts may remain in the app; Stories was left open. Existing demo audio was visible but its quality was not evaluated. Successful export, recovery, auto-casting and synthesis behaviour are **not verified** by seeing their controls.

CPP Studio comparisons use the current checkout: `CONTEXT.md`, `internal/demo/static/index.html`, `internal/demo/static/story-builder.html`, `internal/audiobook/ingest.go`, and ADR-0001. No application code was changed.

## Main finding

VoiceStudio's strongest advantage is the presentation of a creative workflow: choose a voice, work directly on a script, inspect the plan, then deliver a book. CPP Studio already has substantial production capabilities. The best adaptation would make these capabilities easier to discover and add a few specific authoring/delivery features, while retaining existing stores and recovery guarantees.

## Stories — closest comparison is Story Builder

Observed:

- A persistent left pane holds title, default voice, global reading speed, language and M4B/MP3 output choice. Generate stays at the bottom.
- Cast maps named characters to voices. The sample supplied Narrator, Mara and Cole.
- The main editor presents separate script blocks, each with audition, pause insertion, ordering and removal controls. Voice, character and tone/speed controls expand within a block.
- The tone panel exposes reaction buttons such as Laugh and Sigh alongside a speed slider.
- Auto-cast advertises tagged text, screenplay labels and quoted prose. A separate Split into lines action offers paragraph splitting and explains why larger passages suit single-narrator material. Parsing quality was not tested.
- A sample demonstrates chapters, characters and pauses. The draft survived navigating away and back within this session; persistence across restart was not tested.
- Advanced panels include production overrides, cover/details, loudness and stems. Stems output was not explored.

Worth adapting:

1. **A script view within Story Builder.** Read and edit dialogue in narrative order, with character assignment, preview and direction beside the text. Keep the existing timeline for arranging Dialogue, SFX and Music. Both views should refer to the same project and clip identities.
2. **A small demonstration project.** A short scene should teach character assignment, a pause and one sound effect without making the user invent content first. Loading the example should remain distinct from generating audio.
3. **Script import with a review step.** Start with explicit speaker labels and paragraphs. Show the proposed character mapping before applying it. Free-prose speaker inference is a larger and less predictable task.

Do not copy the block layout literally: a chapter heading occupied a large card with inapplicable voice controls and a disabled preview. Individual short lines also consumed considerable vertical space. A compact script view with expanded controls only for the selected line would scale better.

CPP Studio's grounded Podcast Generator is not the equivalent of VoiceStudio Stories. Its factual sources and citations must retain their existing meaning. Story Builder is the natural home for fiction and scripted production improvements.

## Audiobook — the clearest delivery gap

Observed:

- A continuous manuscript editor supports chapter headings and inline voice/pause/delivery markup.
- A toolbar exposes Pause, Voice, Slow, Fast, Emphasis, Spell and Reactions, making syntax discoverable.
- Chapter count, word count and estimated listening duration update below the manuscript. The sample displayed two chapters, 206 words and approximately 1:20.
- Preview plan produced two named chapter rows with character counts and chapter-audition controls. Audition was disabled in the current voice state.
- Cast entries identify characters extracted from the sample.
- Cover/details exposes author, narrator, year, genre, description and cover artwork.
- Production controls include separate line and paragraph gaps, trimming engine silence and varying repeated lines, alongside technical sampling controls.
- Pronunciation is available both within the production and through a central dictionary. The dictionary UI describes language-scoped respelling, a one-off inline override, a substitution preview and JSON backup/import.
- Output choices prominently include M4B with chapters and MP3. Actual files and metadata were not generated or validated.

CPP Studio currently presents document import (TXT/Markdown/EPUB), narrator selection, settings preview, narration, playback and recoverable work. Its UI describes the result as one WAV. EPUB ingestion currently joins chapter text into a single canonical text stream. Its durable sections are processing units; they should not automatically be treated as book chapters.

Worth adapting:

1. **An inspectable chapter plan.** Preserve explicit book chapter identity during ingestion and show names, text, word counts and estimated listening time before a long render. Distinguish listening duration from compute-time estimates.
2. **A short audition from the actual book.** The current saved-voice preview and DramaBox benchmark are useful, but are not the same as hearing the selected narrator read the user's passage.
3. **Chapter-aware delivery.** M4B with chapter navigation and book metadata would make the result more useful in audiobook players. Keep the existing master and add derived delivery exports; never replace recoverable sections with an export-only workflow.
4. **Pronunciation fixes that preserve source text.** Let a user preview the spoken substitution. Freeze the resolved rules with the production's Synthesis identity so a later dictionary change cannot silently affect resumed sections.

Rough edge observed: after loading the sample, the sidebar displayed VoiceStudio Demo Voice, but Create audiobook was disabled with a request to choose a default voice. A warning listed Narrator, Mara and Cole as unknown voices that would fall back to the default. This was not diagnosed as a backend defect, but the visible feedback was inconsistent. CPP Studio should show the exact resolved voice for each character and a direct remedy for missing assignments.

## Wider app observations

| Area | Live UI observation | Useful adaptation for CPP Studio |
| --- | --- | --- |
| Clone | Saved voice or reference upload/recording is the first decision. Profile editor combines waveform, transcript, style, language and reference replacement. | Keep reference audio and transcript together; make the next action clear. CPP Studio already has Actor/Character reference ownership, which should be preserved. |
| Design | Description, named starting points, expandable details, audition script and latest take share one workspace. | Add a small set of understandable starting descriptions. Retain CPP Studio's existing three-candidate comparison. |
| Gallery | Purpose categories, voice traits, language/accent filters, favourites, audition and Use voice. | A curated starter set and better browsing can reduce the blank-page problem. Hundreds of entries are not required for a first improvement. |
| Projects | One searchable view groups stories, audiobooks, voices, transcripts, history and exports, with counts, grid/list and reuse actions. | Improve presentation and return-to-tool actions in the existing Library; do not build a second project catalogue. |
| Transcribe | Fast/Accurate choices explain the dictation-versus-ASR trade-off. History remains visible beside the workspace. | Plain-language choices can sit above advanced model selection. Preserve CPP Studio's waveform/editing workspace. No completed VoiceStudio transcript was available for comparison. |
| Dub | Numbered upload/transcribe, translation and generation stages; original/dubbed demonstration with synchronised playheads and transcripts. | A/B playback and stage review are good patterns. Dubbing itself is separate product scope, not a necessary consequence of this review. |
| Batch Dub | Active/Completed/Failed filters, multiple target languages, background-audio option and watch-folder entry. | Borrow clear job status presentation where useful. Do not introduce a scheduler or folder watcher without a concrete requirement. |
| Tools | Direction parsing, speech-rate fit, file probing, A/B voice comparison and voice conversion. | Same-text A/B audition is useful for casting. Direction help belongs near performance controls. Avoid duplicating existing conversion tools. |
| Integrations | Distinguishes 'Works with VoiceStudio' from external links, but contains a very large directory. | Show a few working, copyable integration recipes. A long directory of unrelated links adds little to production. |
| General | Pause for review versus run straight through. | Make stage review clear in workflows that need it; respect existing production semantics. |
| Appearance | Theme previews, font, scale and sidebar behaviour. | Prioritise readable spacing, contrast and responsive layout before a large theme collection. |
| Compute/Models | System checks, active device, grouped engines, quality presets and memory information. | Explain readiness and remedies in ordinary language, using current model catalogue and lifecycle data. |
| Storage | Separate project/data and model-cache locations, disk usage, backups and retention controls. | Make locations and disk use visible. Do not adopt automatic deletion of production takes; CPP Studio preserves historical work. |

## What to avoid copying

- Nested scroll panes and large cards that make a long script cumbersome to scan.
- Raw model terminology on the normal creative path: CFG, position/class temperatures and taxonomy tokens need not be first-class user decisions.
- A generic markup toolbar that implies every engine supports the same performance controls. CPP Studio must expose only supported behaviour and clearly distinguish text preprocessing from native engine direction.
- The persistent sponsor strip, which occupies valuable editing space and visually competes with the user's work.
- Inconsistent state messaging, such as the audiobook voice selection observed above.
- The pronunciation form's cramped multi-column layout at the current window size.
- Broad feature parity. Translation agents, telephony, remote workers and a directory of integrations do not follow from improving stories and audiobooks.

## Recommended order

1. **Story Builder script authoring and sample** — a more approachable entrance to the existing project/timeline model.
2. **Audiobook chapter plan and short passage audition** — confidence before committing to a long render.
3. **M4B chapters and book metadata** — a meaningful improvement to the finished deliverable.
4. **Pronunciation corrections** — source-preserving and bound to the saved synthesis identity.
5. **Voice browsing and Library polish** — curated starting points, audition, clear reuse actions and consistent layout.

These are separate bounded changes, not approval to implement a combined redesign. Under ADR-0001, Audiobook production continues to own ingestion, planning and durable orchestration. A changed synthesis identity requires an explicit new production/restart; already completed work must remain recoverable. Story Builder continues to own its project and immutable takes/masters, and Library remains a read model over existing owners.
