# Transcribe and Extract product split plan

Last updated: 2026-08-08

Status: implementation and technical verification complete; owner visual acceptance pending

## Objective

Turn the current shared Transcription/Extraction screen into two distinct tools
without duplicating decoding, Whisper transcription, diarization, waveform, or
loaded-audio state:

- **Transcribe** is a transcription desk whose product is an accurate,
  searchable, editable, exportable transcript.
- **Extract** is a sampler and clipping desk whose product is reusable audio for
  the Library and Voices.

The one-sentence product boundary is: **Transcribe produces words; Extract
produces audio assets.**

## Current state and problem

`internal/demo/static/index.html` currently exposes one module with
`data-pages="transcription extract"`. `applyPage` in
`internal/demo/static/app.js` changes only the title and workflow note; both
tabs otherwise show the same controls and state.

The shared implementation already provides useful foundations:

- browser decoding with ffmpeg fallback and optional URL import;
- a waveform, cursor, zoom, playback, and marked region;
- timestamped Whisper segments with chunking for long recordings;
- automatic diarization plus manual speaker correction;
- editable and mergeable transcript lines;
- speaker filtering and checked-segment selection;
- stitched WAV creation, Library saving, clone-reference handoff, and cast
  cloning.

The problem is product emphasis, not missing inference machinery. Transcribe
currently exposes extraction actions that distract from the written result,
while Extract does not visually prioritize the waveform and clip workflow.

## Product contract

### Transcribe

Transcribe answers **“What was said?”** and treats the edited transcript as the
primary result.

- Load an audio/video file or record from the microphone.
- Choose a configured Whisper model.
- Transcribe the complete source into timestamped segments.
- Detect speakers, rename speakers across the transcript, and correct individual
  speaker assignments.
- Correct and merge transcript text without losing source timestamps.
- Search text and speaker names and navigate matches by timestamp.
- Export the current edited transcript as TXT, Markdown, SRT, WebVTT, or
  timestamped JSON.
- Keep a compact waveform for orientation, seeking, and listening, subordinate
  to the transcript.
- Offer **Open in Extract** without reloading or retranscribing the source.

Transcribe does not show clone-reference, cast-cloning, clip-selection, or
audio-Library actions.

### Extract

Extract answers **“Which audio should I keep?”** and treats the selected audio
as the primary result.

- Load an audio/video file or import audio from a URL.
- Scrub and zoom a prominent waveform.
- Transcribe and diarize when transcript navigation would help locate material.
- Use transcript lines and speaker filters to find useful source moments.
- Mark precise start/end points or tick multiple transcript segments.
- Audition the marked or stitched selection.
- Save the selected WAV to the audio Library.
- Hand the selection to Voices as a clone reference.
- Clone one speaker or the complete tagged cast.
- Offer **Open in Transcribe** without reloading or retranscribing the source.

The transcript remains editable because its text and speaker labels are useful
provenance, but transcript download formats are owned by Transcribe.

## Shared module and seam

Keep one deep browser-local audio-analysis module behind a small interface. Its
implementation owns:

- source name, decoded samples, sample rate, and duration;
- current playback cursor and audio URL;
- Whisper segments and edited text/speaker labels;
- decode, transcribe, diarize, waveform, and playback behavior.

Transcribe owns search and transcript export state. Extract owns region,
checked-segment, speaker-filter, clip-export, and cloning state. Both views read
the same source and segments through the shared interface.

For the first implementation, deepen the existing `ex` implementation in
`app.js`; do not create a parallel state object, new persistence system, or
framework. Add one mode-rendering seam, for example
`applyAudioWorkspaceMode("transcribe" | "extract")`, which shows the relevant
subsections while preserving the single in-memory session.

No Gateway or Engine interface change is required. Continue using:

- `POST /v1/audio/transcriptions?format=segments`;
- `POST /v1/audio/diarization`;
- `POST /v1/audio/import` and `/v1/audio/decode` when configured;
- existing Voice and Library routes for extracted audio.

## Pinned interaction decisions

- Switching between Transcribe and Extract never reloads, retranscribes, or
  discards edits, speaker labels, search state, regions, or checked segments.
- Stopping a microphone recording loads it as the current source; transcription
  remains an explicit user action.
- Search is client-side, case-insensitive, and matches both edited text and
  speaker names. Selecting a result seeks to that segment.
- Renaming a speaker updates every segment with that exact speaker label.
- Transcript exports use the current edited segments, not the original Whisper
  response.
- TXT and Markdown include timestamps and speakers when present. SRT and WebVTT
  preserve segment start/end times. JSON includes source name, duration, sample
  rate, and `{start, end, speaker, text}` segments.
- Downloads use the source basename with `.txt`, `.md`, `.srt`, `.vtt`, or
  `.json`.
- Flush any active contenteditable transcript edit before search, export, or
  mode switching.
- The 30-minute editor and existing upload/decode limits remain unchanged.

## Implementation slices

### 1. Establish the two view interfaces

- Divide the existing shared markup into clearly named shared, Transcribe-only,
  and Extract-only subsections without duplicating controls or state.
- Add the mode-rendering function and call it from the existing navigation
  path.
- Preserve loaded audio, transcript edits, filters, region, checked segments,
  cursor, and zoom while switching tabs.
- Give each page its own heading, workflow note, empty state, and concise help.

Acceptance:

- Transcribe contains no audio-extraction or cloning actions.
- Extract contains no transcript-format download actions.
- Switching tabs performs no network request and preserves the complete session.

### 2. Complete the Transcribe desk

- Make the transcript the dominant workspace and reduce the waveform to a
  compact seek/listen reference.
- Add microphone record/stop using the existing browser recording and cleanup
  patterns.
- Add whole-speaker rename, text/speaker search, match count, next/previous
  navigation, and clear search.
- Add deterministic client-side TXT, Markdown, SRT, WebVTT, and JSON builders
  plus download actions.
- Retain line editing, merging, timestamp seeking, diarization, and manual
  speaker correction.
- Add **Open in Extract**.

Acceptance:

- A recorded or loaded source can be fully transcribed and corrected.
- Speaker rename changes every matching segment and no others.
- Search finds edited text and speaker names and seeks correctly.
- Every exported format reflects edited text, speaker names, and timestamps.
- The compact waveform remains keyboard reachable and usable.

### 3. Focus the Extract sampler

- Make the waveform, zoom, cursor, region, and selection summary visually
  primary.
- Keep file load, URL import, transcription, diarization, transcript navigation,
  speaker filters, checked segments, and merge behavior.
- Group Play/Stop, Save to Library, Use as clone reference, and Clone the cast
  around the selected audio result.
- Make the current selection duration, speaker provenance, and stitched-span
  count explicit before an action runs.
- Add **Open in Transcribe**.

Acceptance:

- A region or checked multi-segment selection can be auditioned and saved as the
  expected WAV.
- Clone-reference handoff retains source, time, and speaker provenance.
- Cast cloning remains available only after speaker-tagged material exists.
- Transcript navigation helps selection without visually displacing the
  waveform.

### 4. Verification and documentation

- Add focused JavaScript/asset assertions in `internal/demo/demo_test.go`.
- Add deterministic tests for timestamp formatting, escaping, speaker rename,
  search, and all five transcript export builders.
- Add a focused fixture-backed real-browser smoke for both journeys. It must
  prove mode-specific visibility, edit/export behavior, audio selection actions,
  and state preservation across Transcribe -> Extract -> Transcribe.
- Run `node --check internal/demo/static/app.js`, focused Go tests, and
  `git diff --check` before the browser gate.
- Perform a separate human browser check that Transcribe reads as text-first and
  Extract reads as waveform-first; do not substitute static or HTTP checks for
  this judgement.
- Update README/API wording that currently describes the combined Extractor so
  the two product roles and shared routes are explicit.

## Completion evidence

- Slices 1–3 are implemented on `codex/transcribe-extract-split` with one shared
  loaded-audio/transcript session and mode-specific Transcribe and Extract
  interfaces.
- The fixture-backed browser smoke proves mode visibility, active-edit and
  selection-state preservation, microphone cleanup, speaker rename/search,
  all five downloads, keyboard waveform use, stitched selection playback and
  Library save, cast gating, and clone-reference source/time/speaker provenance.
- The saved two-span browser fixture is asserted as a 2.3-second WAV and the
  test removes its isolated Library artifact through the owning API route.
- `node --check internal/demo/static/app.js`, focused Go tests, `go test ./...`,
  `go vet ./...`, and `git diff --check` are green on 2026-08-08.
- Browser captures are written to
  `output/playwright/transcribe-extract/transcribe.png` and
  `output/playwright/transcribe-extract/extract.png`. Agent visual inspection
  confirms the intended text-first and waveform-first hierarchy; the plan's
  separate human judgement remains an owner acceptance gate.

## Non-goals

- No durable transcript-project store or server-side editing lifecycle.
- No translation, summarization, AI rewriting, or transcript reconciliation.
- No multitrack audio editor, noise removal, mastering, or destructive source
  editing.
- No new model installation or switching behavior.
- No duplicate decoding, transcription, diarization, or waveform implementation.
- No changes to Story, Story Builder, Voice, Audiobook, or Library ownership.

## Risks and guards

- **Mode leakage:** mode-specific controls could remain visible on the wrong
  page. Cover exact visibility in the browser smoke.
- **State loss:** rebuilding markup could discard contenteditable changes or
  selected regions. Flush edits and test round-trip mode switching.
- **Timestamp drift:** SRT/WebVTT rounding could create overlapping or reversed
  times. Centralize formatting and test millisecond boundaries.
- **Recording leaks:** microphone tracks or AudioContexts could survive stop or
  navigation. Reuse existing cleanup behavior and assert controls recover.
- **Scope growth:** keep exports client-side and reuse current routes; do not
  create persistence or a general editor framework.

## Tomorrow restart state

- Working directory:
  `H:\James_NAS\AI_WEBSITES\llama.cpp-audio.cpp\cpp-studio`
- Branch at planning time: `main`, tracking `origin/main`.
- HEAD at planning time: `261b525` (`Merge pull request #42 ...`).
- The worktree already contains intentional, uncommitted edits in:
  `internal/demo/static/app.js`, `internal/demo/static/index.html`, and
  `internal/demo/demo_test.go`. They cover today's Talk hands-free transcript
  correction and enabled one-model selectors. Preserve them; do not reset or
  overwrite them when starting this work.
- This plan file is also intentionally uncommitted.
- The local gateway was healthy on port 8765 at the end of the planning session,
  but tomorrow's run must verify the live process, port, jobs, and Git state
  rather than assume they are unchanged.

## Current handoff

Review the two browser captures and perform the owner visual-acceptance check.
Do not commit, push, open an issue/PR, or merge without explicit authorization.
