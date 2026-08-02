# DramaBox expressive audiobook integration

Status: IMPLEMENTED AND VERIFIED

- Date: 2026-08-02
- Branch: `main`
- Source release: audio.cpp `release-0.5` (`3178daf`)
- Target: cpp-studio's fully local factual-audiobook workflow

## Outcome

Add DramaBox as an optional, separately configured speech engine for audiobooks. A user can keep the current fast local narrator as the default, select DramaBox for a particular book, describe the desired performance in plain language, and receive a locally generated WAV whose manifest records both the engine and direction used.

This is not a claim that DramaBox is a drop-in default on the reference RTX 5080. The official Q8 GGUF is 18,942,803,808 bytes, larger than the card's 16,303 MiB VRAM. The integration therefore ships code, a CPU-resident reference configuration, fit warnings, and deterministic verification; CPU latency and any CUDA fit remain real-model measurement gates.

## Premise challenge

The desired outcome is not "support a new model name." It is expressive factual narration with no per-character/API fee. Local storage, compute, electricity, and the upstream model licence still apply. Replacing the existing Qwen narrator would move away from that outcome: it would make ordinary audiobooks depend on an experimental 18.9 GB model with unmeasured reference-hardware latency. The direct path is an opt-in expressive lane beside the proven fast lane.

Doing nothing leaves a real capability gap. cpp-studio can preserve facts and clone/design narrators, but the delivery of long factual prose is intentionally plain. DramaBox is the first audio.cpp model whose documented interface makes delivery direction a real synthesis input rather than inert metadata.

## Evidence from the release

- audio.cpp 0.5 documents DramaBox as experimental English TTS and cloning with prompt-driven style control, long-form chunking, optional reference WAV, and 48 kHz stereo output.
- The standalone package is `DramaBox-GGUF/dramabox-q8_0.gguf` and is distributed through `audio-cpp/audio.cpp-gguf` under the upstream LTX-2 Community License.
- CLI controls include guidance scale, inference steps, duration scale, seed, long-form thresholds, `dramabox.perf_mode`, and `dramabox.mem_saver`.
- The audio.cpp server exposes `/v1/audio/speech`, accepts request-local `voice_ref`, and can keep a model session warm. DramaBox is offline-only, so it does not provide streaming speech.
- The current cpp-studio audiobook path already owns document extraction, sentence-boundary chunking, single-engine reservation, cancellation, WAV stitching, persistence, and browser progress. Those modules should be extended, not duplicated.

## Implementation alternatives

### Approach A: configuration-only swap (minimal viable)

- Effort: S
- Risk: Medium
- Completeness: 4/10

Point the existing `audio` engine at DramaBox and tell operators to write scene prompts manually.

Pros:

- Few files and no API changes.
- Proves the binary can be launched.

Cons:

- Replaces the proven fast narrator globally.
- No audiobook-level choice, no performance-direction UI, and no provenance.
- A Qwen configuration receiving DramaBox stage directions may speak them aloud.

Reuses: existing audio.cpp CLI adapter only.

### Approach B: optional expressive audiobook engine (chosen)

- Effort: M
- Risk: Medium
- Completeness: 9/10

Add a separately named `dramabox` engine, let one audiobook select it, transform each factual chunk into a bounded director-style prompt only on that route, and persist the choice.

Pros:

- Fast narration remains the default and rollback is immediate.
- The new model's special prompt contract stays local to one deep module.
- API, UI, job detail, persistence, fixtures, and docs agree.
- Creates a small reusable engine-selection seam without forcing every current speech caller through a migration.

Cons:

- Crosses several existing modules because the vertical user journey spans HTTP, job orchestration, engine invocation, persistence, and UI.
- DramaBox voice cloning still depends on a suitable reference WAV and the upstream experimental runtime.

Reuses: `audiobook.Manager`, `engine.Spec`, router speech adapter, jobs registry, voice store, WAV module, browser audiobook desk, model catalog.

### Approach C: general speech-backend registry (ideal architecture, deferred)

- Effort: XL
- Risk: High
- Completeness: 10/10 for the platform, excessive for this release

Replace the hard-coded speech engine across Talk, Voice Loop, Story, take room, image description, and Audiobook with capability-described speech backends and per-request synthesis options.

Pros:

- Every surface can select models and capabilities consistently.
- Future expressive, multilingual, and streaming TTS integrations become configuration work.

Cons:

- Changes provenance, resume fingerprints, story takes, direct speech compatibility, and multiple UIs at once.
- Makes it much harder to separate a DramaBox bug from a routing migration bug.

Reuses: all current engine and lifecycle seams, but changes most callers.

Recommendation: Approach B. It delivers the user's audiobook outcome without destabilising the studio, while placing the new variation at the existing engine seam rather than leaking DramaBox flags through the application.

## What already exists

| Sub-problem | Existing owner | Reuse decision |
|---|---|---|
| Document extraction and chunking | `internal/audiobook` | Keep unchanged except prompt preparation and request metadata. |
| Long-running progress/cancel | `internal/jobs` + `audiobook.Manager` | Reuse; job detail names the selected engine. |
| Native process/server invocation | `internal/engine`, router `speak` | Parameterise the engine name; keep CLI and resident-server adapters. |
| Single-run reservation | `engine.Invoker.Reserve` | Reserve the selected speech engine, not always `audio`. |
| Cloned voice lookup | `internal/voice` + router | Reuse the same stored reference for DramaBox. |
| WAV validation and stitching | `internal/wav` | Reuse without format-specific branching. |
| Model presence UI | `models.json` + Models tab | Add an honest optional catalog row. |
| Audiobook form and shelf | embedded HTML/JS | Add one engine choice and one conditional direction field. |
| Deterministic native stand-in | `cpp-studio-fixture` | Reuse by configuring both engine names to the same fixture binary. |

## Scope

In scope:

- A generic `SpeechVoiceSpecFor(engineName, text, voice)` constructor, with existing `SpeechVoiceSpec` remaining compatible.
- A router speech adapter that can target `audio` or `dramabox` while preserving subprocess and resident-server behavior. Qwen keeps its required server voice reference; DramaBox may omit `voice_ref`/`reference_text` for text-only synthesis and uses them when a stored clone is selected.
- Audiobook request fields `engine` and `direction`, strict validation, selected-engine reservation, chunk prompt preparation, job detail, and manifest provenance.
- A deterministic DramaBox prompt builder that preserves source words, bounds direction length, and normalizes embedded double-quote punctuation in both direction and source so narration remains inside one quoted passage. That quote normalization is disclosed rather than described as byte-for-byte preservation.
- Audiobook UI controls with progressive disclosure: fast local is default; DramaBox is enabled only when configured, and selecting it reveals the direction field and a hardware/experimental warning.
- An optional local config example and audio.cpp server config example for DramaBox 0.5. The reference server uses the CPU backend so configuring it cannot consume the reference GPU at startup; its latency is explicitly unverified.
- Model catalog, API/config/README documentation, unit/integration/browser-asset tests, and the normal full verification suite.

## NOT in scope

- Downloading the 18.9 GB model automatically — current model sources are catalog links, not a trusted artifact installer, and the license must be read before download.
- Claiming real-time or GPU fit on the 16 GB reference machine — no measured evidence exists yet.
- Making DramaBox the default narrator — its hardware and latency posture is not yet proven.
- Per-line direction in Story/take-room, or DramaBox in Talk/Voice Loop — those surfaces need synthesis provenance and resume-fingerprint changes together.
- Exposing every diffusion knob in the browser — upstream defaults are the quality baseline; advanced tuning belongs in engine/server configuration first.
- Voice-reference consent changes — existing consent and provenance rules continue to apply.
- Building or committing audio.cpp itself — cpp-studio integrates against the published 0.5 contract.

## Architecture

```text
Browser audiobook desk
  file + title + voice + engine + direction
                  |
                  v
POST /v1/audiobooks
  validate upload, voice, engine, direction
                  |
                  v
audiobook.Manager.Submit
  chunk source once
  reserve selected engine for whole job
                  |
          +-------+-------+
          |               |
      engine=audio    engine=dramabox
          |               |
      plain chunk     BuildDramaBoxPrompt
          |               |
          +-------+-------+
                  |
                  v
router.speakWithEngine(engineName, text, voice, reserved)
          |                             |
   subprocess CLI                resident server
   SpeechVoiceSpecFor            POST /v1/audio/speech
          |                             |
          +-------------+---------------+
                        v
              validate WAV -> stitch -> pad
                        |
                        v
       book.wav + manifest(engine, direction)
```

The module remains deep: `audiobook.Manager` exposes one request containing the user's narration intent and hides chunk transformation, reservation, progress, and persistence. The engine invocation seam varies only the adapter name; DramaBox CLI flags remain in configuration.

## Data flows

Happy path:

```text
upload -> extract -> validate engine/direction -> chunk -> reserve
       -> prompt each chunk -> synthesize -> validate/stitch -> atomic save -> play
```

Nil/missing path:

```text
engine omitted -> "audio"
direction omitted + audio -> plain narration
direction omitted + dramabox -> documented default factual direction
voice omitted + audio server -> configured default reference required
voice omitted + dramabox -> text-only synthesis; omit voice_ref/reference_text
voice selected + dramabox -> send stored clone reference
```

Empty/invalid path:

```text
empty direction -> default only for dramabox
direction with audio -> 400 (never silently spoken as prose)
unknown engine -> 400; recognized but unconfigured engine -> 503 with setup remedy
direction over limit -> 400 before job creation
```

Error path:

```text
invalid request -> 400; missing configuration -> 503, no job
active audiobook or selected engine busy -> 409, no job
engine fails on chunk N -> job failed with engine + chunk N/N context
cancel -> synthesis context cancelled -> job cancelled -> reservation released
save fails -> job failed; temp directory removed
```

## Error & Rescue Registry

| Method/codepath | What can go wrong | Class/kind | Rescue action | User sees |
|---|---|---|---|---|
| audiobook HTTP submit | bad engine, direction, or missing configuration | invalid input / HTTP 400 or unavailable / HTTP 503 | reject before starting | exact field problem and remedy |
| `Manager.Submit` | selected engine busy | conflict | do not create job; release nothing | `engine "dramabox" is busy` |
| prompt preparation | empty/oversized direction | invalid input | default or reject deterministically | actionable 400 |
| `speakWithEngine` | engine absent | `KindNotConfigured` | return typed engine error | configure `dramabox`; see CONFIG.md |
| CLI/server synthesis | timeout/crash/non-2xx | `KindEngineFailure` | fail current job with engine/chunk context | failed job detail |
| WAV validation | invalid/oversized output | `KindEngineFailure` | reject output, mark engine failed | invalid output detail |
| cancellation | context cancelled mid-run | context cancellation | mark cancelled, release selected engine | Cancelled |
| stitching/save | inconsistent WAV or filesystem error | job failure | no partial final directory; retain error | stitching/save detail |

## Failure Modes Registry

| Failure mode | Likelihood | Impact | Mitigation | Critical gap? |
|---|---|---|---|---|
| 18.9 GB model cannot fit CUDA VRAM | High on reference GPU | High | optional engine, warning, no default claim, CPU reference config | No; honest limitation |
| CPU inference is too slow for books | Medium/high | High | require benchmark before recommendation; keep fast engine default | No; rollback is selection |
| Direction is spoken aloud by plain TTS | Medium without routing | Medium | reject direction unless DramaBox selected | No |
| Quotes in source/direction break prompt structure | Medium | Medium | normalize embedded double quotes to single quotes; disclose punctuation-only transformation; unit tests | No |
| Long-form upstream chunking doubles cpp-studio chunking | Low | Medium | cpp-studio chunks stay under existing budget; upstream threshold remains unused | No |
| Configured DramaBox prevents or destabilizes startup | Low after installation | High | optional config only; reference server backend is CPU; dual-engine boot/config fixture test | No |
| Two GPU engines run concurrently and exhaust VRAM | Low in reference posture | High | DramaBox reference server stays on CPU; GPU use is opt-in only after a measured fit test | No |
| Manifest loses how audio was made | High without schema change | Medium | persist engine and direction | No |
| Experimental upstream CLI changes | Medium | Medium | pin docs/config to release 0.5 and keep adapter contract tests | No |

## Security and privacy

- The feature remains loopback/local and adds no network service or credential.
- Direction and document text are passed as `exec.Command` argument values or JSON, never shell-interpolated.
- Direction is length-bounded and treated as delivery instruction; it cannot modify source documents or filesystem paths.
- Engine selection is exactly `{audio, dramabox}` intersected with configured engines, not every configured engine name and never an arbitrary executable or URL.
- Voice references remain server-owned paths resolved from the existing voice store.
- Existing local voice-cloning consent requirements apply unchanged.

## UI design review

Classification: app UI. Reuse the existing calm audiobook control panel; no new cards, route, tab, modal, or decorative system.

Information order:

```text
1. Document
2. Title and narrator voice
3. Narration engine (Fast local default / DramaBox expressive)
4. Conditional performance direction + fit warning
5. Narrate / Cancel
6. Progress, playback, finished shelf
```

Interaction states:

| Feature | Loading | Empty | Error | Success | Partial |
|---|---|---|---|---|---|
| Engine choice | health-backed select | fast default; DramaBox disabled as "not configured" | unavailable choice cannot submit | selection retained for submit | DramaBox warning visible before submit |
| Direction | hidden for fast engine | default DramaBox direction shown | inline bounded validation/server error | sent and persisted | editable before submit only |
| Audiobook job | existing upload/progress | existing idle state | failed job names engine/chunk | playback + shelf | progress detail names chunk and engine |

User journey:

| Step | User does | User should feel | Plan support |
|---|---|---|---|
| 1 | chooses a factual document | familiar and safe | existing default path unchanged |
| 2 | selects DramaBox | excited but informed | concise experimental/18.9 GB warning |
| 3 | describes delivery | in creative control, not editing facts | example direction and helper copy |
| 4 | starts job | confident it is local | existing job progress names selected engine |
| 5 | listens | able to compare without lock-in | manifest/shelf preserve engine; fast path remains available |

Accessibility/responsive requirements:

- Use existing visible `<label>` patterns and native select/textarea controls.
- Direction region uses `hidden` rather than focus-hostile CSS; selection toggles it before focus can enter.
- Helper/error text is plain text; no color-only distinction.
- Existing responsive control-panel layout continues; no new fixed widths.
- Keyboard submit and Cancel behavior remain native.

Design scores: information architecture 9/10; states 9/10; journey 9/10; AI-slop resistance 10/10; system alignment 9/10; responsive/accessibility 9/10; unresolved decisions 0.

## Engineering test diagram

```text
CODE PATHS                                           USER FLOWS
SpeechVoiceSpecFor(engine, text, voice)              Audiobook fast default
  + custom engine name [unit]                          + upload -> complete [integration]
  + legacy wrapper stays audio [regression]            + direction rejected [integration]
  + cloned voice overrides [unit]

BuildDramaBoxPrompt(direction, chunk)                DramaBox audiobook
  + default direction [unit]                           + engine/direction forwarded [integration]
  + whitespace normalization [unit]                    + selected engine reserved [manager test]
  + embedded quotes [unit]                             + manifest provenance [manager test]
  + length boundary [unit/integration]                 + UI conditional field [asset test]

speakWithEngine                                      Failure/recovery
  + subprocess selected name [integration]             + missing engine -> actionable 503
  + resident selected name [integration]               + busy selected engine -> conflict
  + DramaBox text-only omits voice ref [integration]   + invalid -> 400, unavailable -> 503, busy -> 409
  + DramaBox clone overrides ref [integration]         + cancel releases engine [manager test]
  + dual audio/DramaBox boot config [integration]
```

The Friday-2am test is an HTTP upload using the `dramabox` fixture engine that completes, records engine/direction, and serves a valid WAV while the normal `audio` route still passes. The hostile test submits an arbitrary engine name, an overlong direction, and a direction paired with the plain engine; none may start a job. The chaos test cancels while the selected engine is blocked and proves its reservation is released.

## Performance and rollout

The new Go/JS overhead is negligible; native inference dominates. The prompt adds a short direction per existing ~300-character chunk. Generated chunks are spooled to a hidden job staging directory and loaded one at a time for stitching, avoiding retention of hundreds of 48 kHz clips in memory; final joined-audio buffers still scale with book duration. One audiobook still runs at a time, and the selected engine is reserved for the whole job. The shipped DramaBox server example is CPU-resident so cpp-studio's eager server startup cannot take GPU memory; its audiobook latency is unverified. Operators may opt into CUDA only after proving load, peak VRAM, and real-time factor on their hardware.

Rollout is configuration-gated: without a `dramabox` engine, every old request and UI default behaves as before and the DramaBox option is visibly disabled as "not configured." Rollback is a git revert or removal of that optional engine config; persisted WAVs remain playable and manifests remain backward-compatible because new fields are optional.

Post-implementation verification:

1. Targeted red/green Go tests for prompt construction, selected-engine reservation, HTTP validation, and manifest output.
2. `gofmt` and targeted package tests after each vertical slice.
3. `go test ./...`, `go vet ./...`, config validation, and `scripts/verify.ps1` once complete.
4. Deterministic browser smoke with both engine names mapped to the fixture.
5. Validate all JSON examples parse and all documented paths/flags match audio.cpp 0.5.
6. Do not mark real DramaBox runtime verified unless the model is installed and a measured synthesis succeeds.

## Developer journey and DX

Primary persona: a Windows user/operator who can edit one local JSON config but should not need to understand audio.cpp internals. Secondary persona: an OSS contributor validating the gateway without model weights.

| Stage | Developer does | Friction addressed | Target |
|---|---|---|---|
| Discover | reads README capability and hardware note | no paid-service ambiguity | under 2 minutes |
| Install | builds audio.cpp 0.5 and obtains upstream model after reviewing license | exact release/model/package named | explicit, not one-click |
| Configure | copies CPU-resident DramaBox examples and changes root/model paths; voice reference stays optional | copy-paste complete JSON | under 5 minutes after assets exist |
| Check | runs gateway `--check` and audio.cpp `/health` | actionable missing-command/model errors | immediate |
| Use | selects DramaBox and enters direction | progressive disclosure | one form submission |
| Debug | reads job detail and engine log tail | engine/chunk context | no silent failure |
| Upgrade | compares pinned 0.5 contract before changing | versioned setup section | safe/manual |
| Test | maps DramaBox to fixture engine | no 18.9 GB CI dependency | normal `verify.ps1` |
| Roll back | selects fast local or removes optional config | no data migration | immediate |

Developer perspective: I can prove the application path without downloading 18.9 GB, and the docs do not pretend that a model larger than my GPU is a one-click win. Once I deliberately install it, I copy a complete server and gateway example, run the existing config check, and see a clearly labelled DramaBox choice in the same Audiobook desk I already know. When generation fails, the job tells me which engine and chunk failed; when it succeeds, the manifest tells me exactly how the book was made.

DX scores: getting started 8/10 (model download/build dominates); interface 9/10; errors 9/10; docs 9/10; upgrade 8/10; tooling 9/10; community/ecosystem 8/10; measurement 8/10. Target time-to-hello-world is under five minutes with fixtures and under five minutes after native assets are installed; model acquisition time is reported separately.

## Dream state delta

```text
CURRENT                         THIS PLAN                         12-MONTH IDEAL
one fast speech engine   ->     optional expressive book lane -> capability-described speech
plain factual delivery         with provenance and fallback     backends across every surface,
                                                               measured fit/quality profiles,
                                                               safe per-project defaults
```

## Decision Audit Trail

| # | Phase | Decision | Classification | Principle | Rationale | Rejected |
|---|---|---|---|---|---|---|
| 1 | CEO | Preserve fast narration as default | Auto-decided | completeness + reversibility | DramaBox is experimental and unmeasured on reference hardware | global replacement |
| 2 | CEO | Implement optional expressive audiobook lane | Auto-decided | direct user outcome | matches the user's factual audiobook need | config-only demo |
| 3 | Architecture | Use a separately named speech engine | Auto-decided | explicit over clever | isolates reservation, failure, and rollback | static model variant hidden from jobs |
| 4 | Architecture | Keep generic platform registry deferred | Auto-decided | right-sized diff | avoids unrelated Story/Talk provenance migration | all-surfaces rewrite |
| 5 | Design | Progressive-disclosure direction field | Auto-decided | one job per section | plain narrator users see no extra complexity | always-visible tuning panel |
| 6 | Performance | Make no fit/latency claim without benchmark | Auto-decided | evidence over inference | package exceeds reference VRAM and no upstream metric is published | optimistic CUDA default |
| 7 | DX | Ship fixture proof plus exact optional setup | Auto-decided | complete error paths | CI has no model/API fee and stays deterministic while real setup is honest | untested documentation-only support |
| 8 | Independent review | Permit text-only DramaBox but retain Qwen reference requirement | Auto-decided | preserve backend contracts | upstream reference is optional only for DramaBox | globally optional reference |
| 9 | Independent review | Ship CPU-resident reference server posture | Auto-decided | safe startup | eager configured-server startup makes an unproved CUDA default unsafe | optimistic CUDA example |
| 10 | Independent review | Disable unconfigured DramaBox in UI and split 400/409/503 | Auto-decided | honest affordances | unavailable choices and validation conflicts should not masquerade as runnable/busy | permanent failing option |

## Implementation tasks

- [x] T1 (P1) — Add selectable speech-engine and DramaBox prompt behavior to the audiobook module, with manifest provenance.
- [x] T2 (P1) — Parameterise the existing speech invocation seam without breaking `audio` callers.
- [x] T3 (P1) — Validate and route `engine`/`direction` at the HTTP boundary with actionable errors.
- [x] T4 (P1) — Add the conditional engine/direction UI and deterministic browser markers.
- [x] T5 (P1) — Add optional release-0.5 config examples and the model catalog entry with exact size/license/source.
- [x] T6 (P1) — Document API, setup, hardware posture, rollback, and verification.
- [x] T7 (P1) — Run targeted/full verification and an adversarial diff review; fix all P1/P2 findings.
- [x] T8 (P1) — Commit only intended files and push `main` to `origin`.

## Completion summary

| Review | Result |
|---|---|
| CEO | Selective expansion; 7 decisions, 0 unresolved; separate expressive lane chosen |
| Design | 9/10 overall; existing audiobook hierarchy reused; 0 unresolved |
| Engineering | Full review; explicit engine seam, failure registry, coverage diagram, and rollback defined |
| DX | 8.5/10 overall; fixture TTHW under 5 minutes; native asset acquisition called out separately |
| Independent review | 2 high, 3 medium, 1 low found; all incorporated; implementation-ready on re-review |
| Implementation review | Security clear; API compatibility, UI states, config path, failure recovery, and memory findings fixed; red-team re-review clear |
| Critical gaps | 0 after corrections; fixture-backed full verification passed twice; real-model load/fit/RTF is a documented post-install measurement gate |
| Deferred | general speech registry; Story/Talk integration; automatic model acquisition; advanced knobs |

## Cross-phase themes

Hardware honesty and reversibility appear in CEO, engineering, design, and DX review: DramaBox should feel like a powerful optional instrument, never a silent replacement for the known-good narrator. Provenance also spans product and architecture: an expressive output is only trustworthy if the manifest records which engine and direction created it.

## GSTACK REVIEW REPORT

| Review | Trigger | Why | Runs | Status | Findings |
|---|---|---|---|---|---|
| CEO Review | `/autoplan` | Scope & strategy | 1 | CLEAR | selective expansion; optional expressive lane; 0 critical gaps |
| Codex Review | `/autoplan` | Independent second opinion | 1 | CLEAR | GPT agent (not Claude): 2 high, 3 medium, 1 low found and incorporated; Codex CLI unavailable |
| Eng Review | `/autoplan` | Architecture & tests | 1 | CLEAR | explicit adapter seam, failure registry, full test diagram |
| Design Review | `/autoplan` | UI/UX gaps | 1 | CLEAR | score 9/10; progressive-disclosure controls |
| DX Review | `/autoplan` | Developer experience gaps | 1 | CLEAR | score 8.5/10; fixture TTHW under 5 minutes |
| Implementation Review | `/review` | Diff correctness and adversarial release gate | 2 | CLEAR | 1 critical config-path blocker found, fixed, and cleared on re-review; no remaining findings |

**VERDICT:** CEO + DESIGN + ENG + DX + INDEPENDENT + IMPLEMENTATION REVIEW CLEARED — ready to ship.

NO UNRESOLVED DECISIONS
