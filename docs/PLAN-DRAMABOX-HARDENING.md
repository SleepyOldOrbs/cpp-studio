# DramaBox audiobook hardening roadmap: P0, P1, P2

Status: IN PROGRESS — P0 implementation began 2026-08-03

- Date: 2026-08-02
- Architecture reconciled: 2026-08-03
- audio.cpp WebUI reuse decision: 2026-08-03
- Branch at planning time: `main`
- Baseline commit: `fe2c1fe` (`feat: add DramaBox factual audiobook narration`)
- Implemented predecessor: [`PLAN-DRAMABOX.md`](PLAN-DRAMABOX.md)
- Durable ownership decision: [ADR-0001](adr/0001-audiobook-production-owns-durable-orchestration.md)
- Target runtime contract: audio.cpp `release-0.5`
- Local runtime at planning time: sibling `audio.cpp` checkout is still `release-0.4.2`

## Outcome

Turn the optional DramaBox lane from a fixture-proven expressive narrator into a
long-job-safe factual-audiobook workflow. A user should be able to start a large
local narration, stop or survive a failure without losing completed work, resume
under the same synthesis identity, see whether generated speech still matches the
source, and understand the actual time and hardware cost before committing hours of
compute.

The existing fast `audio` narrator remains the default. DramaBox remains optional,
English-only, experimental, local, and configuration-gated. No stage in this plan
introduces a hosted API or a per-character charge.

## Premise challenge

The product goal is not maximum theatrical expressiveness. It is useful factual
audiobooks for a user who cannot pay a recurring TTS bill. That changes the priority
order:

1. Losing two hours of local generation is worse than lacking another style control.
2. Quietly changing a date, name, or sentence is worse than a slightly plain delivery.
3. An honest projected runtime is more useful than an unmeasured “local and free” label.
4. A model browser is useful only after the selected narrator can finish and be audited.

The P0/P1/P2 labels therefore describe dependency order, not marketing importance.
P0 makes long jobs safe and auditable. P1 makes them controllable and measurable. P2
makes the model and prompt surfaces easier to discover and use correctly.

## Source contracts and verified constraints

- audio.cpp `release-0.5` documents DramaBox as experimental English TTS/cloning,
  with optional reference audio, 48 kHz stereo output, fixed seeds, diffusion and
  guidance controls, a 45-second long-form threshold, approximately 37-second native
  chunks, and a 50 ms equal-power crossfade:
  <https://github.com/0xShug0/audio.cpp/blob/release-0.5/docs/tts.md#dramabox>
- The release-0.5 server accepts `seed` at the top level of
  `POST /v1/audio/speech`; full `uint64` seeds should be encoded as JSON strings. It
  also accepts an `options` object for model-specific request values:
  <https://github.com/0xShug0/audio.cpp/blob/release-0.5/app/server/README.md#post-v1audiospeech>
- Upstream DramaBox reports quality drift beyond roughly 45 seconds, automatically
  targets approximately 37-second internal chunks, repeats the speaker prefix, and
  uses a 50 ms equal-power crossfade:
  <https://github.com/resemble-ai/DramaBox#long-form-generation-text-chunking>
- Upstream recommends 10 or more seconds of voice reference and documents higher CFG
  guidance as more text-faithful but potentially less natural:
  <https://github.com/resemble-ai/DramaBox#quick-start>
- The validated prompt shape is a generic speaker phrase followed by quoted literal
  speech; role nouns can be spoken accidentally:
  <https://github.com/resemble-ai/DramaBox/blob/master/docs/PROMPTING_GUIDE.md>
- Upstream Python applies a Perth watermark, but the audio.cpp DramaBox contract does
  not promise that watermark. cpp-studio must record provenance without claiming the
  native output is watermarked.

## Current state

| Concern | Existing implementation | Reuse or change |
|---|---|---|
| Document ingestion | `internal/audiobook/ingest.go` extracts TXT, Markdown, and EPUB | Put upload-to-canonical-source creation and engine-specific planning behind the Audiobook production seam |
| Fast narration | Approximately 300-character chunks plus 400 ms fixed gaps | Preserve unchanged for `engine=audio` |
| DramaBox narration | Same 300-character chunking, book-level direction wrapper | Replace only the DramaBox section policy |
| Job lifecycle | One active audiobook; cancellation through `jobs.Registry` | Make the audiobook store authoritative; keep `jobs.Registry` as a live projection only |
| Intermediate audio | Hidden temporary clip directory deleted on every exit | Replace with durable `.wip` directory and explicit discard |
| Final assembly | `wav.ConcatenateGapsFrom` builds the final PCM in memory | Add disk-streamed assembly with bounded crossfade buffers |
| Finished manifest | Records title, voice, engine, direction, chunk count, duration, URL | Version and extend without breaking old manifests |
| Resume precedent | Story has `.wip`, atomic manifests, fingerprints, resume/discard | Reuse the durable design, not Story domain types or implicit in-place identity changes |
| Synthesis identity | Story hashes engine command/config/file identity | Resolve and freeze an audiobook-specific identity inside the Manager before durable work begins |
| Transcription | Resident Whisper and subprocess transcription already exist | Inject a verifier function; do not add a second ASR stack |
| Voice storage | Validates basic WAV shape and byte cap; stores provenance | Extend analysis metadata without invalidating existing voices |
| Model catalog | Static `models.json`, live presence/checksum state, Models tab | Keep the tracked catalog authoritative; extend with bounded discovery, GGUF inspection, and confirmed allowlisted installation |
| Benchmarking | Deterministic local Story/audio benchmark harness and RTF reporting | Add a DramaBox-specific harness using the same evidence conventions |
| Audiobook UI | Engine, voice, direction, progress, cancel, playback, shelf | Add interrupted work, verification, benchmark, and prompt preview progressively |

## audio.cpp WebUI reuse decision (2026-08-03)

cpp-studio should adopt the upstream WebUI's useful behaviour patterns, not its page
structure or ownership model.

Adopt:

- curated controls per model family, with DramaBox ranges/defaults owned by typed
  cpp-studio request schemas;
- a resolved-request preview that distinguishes user choices from the effective
  options sent to audio.cpp;
- the actual seed used, request/load timings, output duration, and RTF wherever the
  runtime can report them honestly;
- lightweight GGUF inspection, model readiness, existing engine lifecycle controls,
  and confirmed model installation;
- reference-voice conveniences such as select, preview, clear, fitness metadata, and
  the exact authorized reference identity used by the request;
- errors that name the failed field or readiness stage and provide an in-product
  remedy; and
- raw JSON as a collapsed advanced/operator escape hatch, parsed and normalized
  through the same allowlisted schema rather than forwarded to an engine.

Reject:

- the repeated Gradio toolbox layout; the Audiobook desk and Models/Engines surfaces
  remain task-oriented cpp-studio interfaces;
- ephemeral UI-side audiobook splitting or stitching; durable section planning,
  checkpointing, verification, and assembly remain server-owned;
- changed-only option persistence; every production records the complete resolved
  effective option set so resume and provenance do not depend on UI defaults; and
- direct browser-to-audio.cpp model loading, process control, model-manager commands,
  filesystem paths, or downloads. All operations continue through cpp-studio's
  gateway, job registry, engine reservation, model catalog, and Lifecycle Manager.

Phase ownership:

- **P0:** typed DramaBox controls, resolved-request preview, and persisted resolved
  effective options.
- **P1:** actual seed visibility, request/load timing and benchmark evidence, plus
  reference-voice conveniences.
- **P2:** readiness and GGUF inspection, links to existing lifecycle controls, and
  explicit confirmed installation of allowlisted catalog artifacts.

## Priority dependency correction

The earlier recommendation listed fixed seeds under P1. The implementation must split
that feature:

- **P0 internal seed plumbing:** every DramaBox section receives and persists a stable
  `uint64` seed so resume and fingerprint behavior are coherent.
- **P1 retry controls:** the user can reproduce the same attempt or deliberately create
  a variation with a new seed.

P0 also adds disk-streamed final assembly. At 48 kHz stereo 16-bit PCM, one hour is
roughly 691 MB before container overhead. A reliable long-form plan cannot retain the
whole decoded book in one Go byte slice.

## Scope

### P0 — safe, resumable, factual long-form production

P0 delivers the minimum trustworthy DramaBox audiobook workflow.

#### P0.0 — runtime qualification and version boundary

1. Build audio.cpp `release-0.5` in a side-by-side checkout or worktree. Do not replace
   the proven `release-0.4.2` runtime in place.
2. Confirm `audiocpp_cli --list-loaders --json` includes `dramabox`.
3. Confirm a loopback `audiocpp_server` starts from the checked-in server template and
   DramaBox model example, reports healthy, and accepts a text-only speech request.
4. Keep the 18.9 GB model download manual during P0. P2 may add the confirmed,
   allowlisted installation flow defined below; no phase permits a silent download.
   Setup must show source, expected size, licence, free disk space result, and the fact
   that local electricity and compute are not free.
5. Record the exact audio.cpp tag/commit, binary identity, model size/checksum when
   available, backend, device, and session options in benchmark/provenance output.
6. Do not mark real-runtime qualification complete from fixtures. If weights are not
   installed, use `CODE_COMPLETE_RUNTIME_UNVERIFIED` as the honest stage state.

Exit gate:

- Fixture-backed development may begin once the release contract is pinned.
- Live qualification requires one valid real DramaBox WAV and a recorded runtime
  identity. Audio quality still requires human listening and remains a separate gate.

Qualification evidence (2026-08-03):

- Existing `release-0.4.2` checkout remains clean at
  `27d87ba4804e7f22f28fc90d6b211b91c7c156c8`.
- Side-by-side `release-0.5` worktree is pinned at
  `3178daf4028fa8f48ef63299aa1524ee2d3a4bb7`; its tag matched the remote tag.
- CPU `audiocpp_cli.exe` SHA-256 is
  `627bf21f7b04758c88e109589a040a7e3a17b268fb00bd6ea7230acc120bee61`;
  CPU `audiocpp_server.exe` SHA-256 is
  `806560fb69fba6bc50da4a4d802fe8c3a06866941d4b2fc0a21c5cb1983aa423`.
- `dramabox-q8_0.gguf` is 18,942,803,808 bytes with SHA-256
  `75e7e80fc748defb188cb902c34c62bc12539a7bba477215dccf59a7218a451e`.
- Loader inspection exposed `tts` and `clon` offline routes. A lazy loopback CPU
  server reported healthy and completed a seeded text-only request with
  `seed="42"`, one diffusion step, CFG 1, `duration_sec=1.5`, and
  `dramabox.mem_saver=true`.
- The qualification device was an AMD Ryzen 7 9800X3D (8 cores, 16 logical
  processors), with 16 runtime threads. The server config was derived from
  `app/server/README.md` plus the `docs/tts.md` DramaBox example and had SHA-256
  `107477b90fbadaffb2382792f8bd7dadccd4692cd8d20917e109c920e0a45c0b`; the request
  JSON had SHA-256 `d59a06719fa49bceda86d863ded6a21f5e4700642a90d81e3db3afe14a7de7e7`.
- The real output was a valid 48 kHz stereo PCM WAV: 1.61 seconds, 309,164 bytes,
  SHA-256 `5cb96e65030f022a20ae7159e55eaf9c3d46daeb093d363b5a77a1413cc92434`.
  Runtime trace reported 157,479.767 ms wall time on the CPU qualification path.
- The CUDA 120a build configured but failed in the two audio.cpp CUDA runtime
  translation units because NVCC received a malformed MSVC optimization argument.
  CUDA qualification and human listening remain separate follow-up gates; neither
  invalidates the completed CPU runtime-contract qualification.

#### P0.1 — engine-specific section planning

Keep `Chunk(text, 300)` and the current 400 ms gap for the fast narrator. Add a new
DramaBox section planner whose units are large enough to activate audio.cpp's native
long-form chunker but small enough to remain cancellable, resumable, and below the
engine invocation module's 32 MiB per-response cap.

Proposed policy:

- Estimate spoken duration at 150 words per minute (`words / 2.5`).
- Target approximately 75 seconds per app-level section.
- Hard-cap the estimate at 110 seconds.
- Pack whole paragraphs first, then sentences, then word boundaries only for a single
  over-limit sentence.
- Never silently drop text.
- Preserve a stable byte range and SHA-256 for every section against a normalized
  `source.txt` stored once in the WIP directory.
- Send the whole app-level section to audio.cpp. Let audio.cpp perform its internal
  approximately 37-second splits and 50 ms crossfades.
- Join app-level section WAVs with a 50 ms equal-power crossfade rather than a fixed
  400 ms silence. Keep lead/trail artifact padding separate.

The duration target is a starting policy, not a claim about generated timing. P1's
benchmark may tune it. Changing the policy increments `section_policy_version` and
makes existing WIP identity-incompatible with Resume; Restart creates a new production
under the new policy.

#### P0.2 — typed synthesis request and stable seed plumbing

Replace the positional internal synthesis callback with a typed request:

```go
type SynthesisRequest struct {
    Text     string
    VoiceID  string
    EngineID string
    Options  SynthesisOptions
}

type SynthesisOptions struct {
    Seed                   uint64
    NumInferenceSteps      int
    GuidanceScale          float64
    AudioChunkThresholdSec float64
    AudioChunkDurationSec  float64
    CrossFadeDurationSec   float64
}
```

P0 exposes a compact, curated DramaBox control set backed by this type: inference
steps, guidance, long-form threshold, internal chunk target, and crossfade. The first
release uses pinned defaults of 30 steps, guidance 2.5, threshold 45 seconds, internal
target 37 seconds, and crossfade 0.05 seconds. It omits explicit duration and negative
prompt. The fast narrator receives an empty option set and keeps its current behavior.
Each field has a documented range and plain-language effect; the browser never builds
engine flags.

Before submission, a resolved-request preview shows the normalized typed values,
defaults that were filled in, selected engine/model/voice identities, and the semantic
server JSON or subprocess option mapping. A collapsed raw-JSON editor remains an
advanced/operator escape hatch, but it accepts only the same allowlisted option names
and ranges, rejects duplicates/unknowns, and is normalized back into
`SynthesisOptions`. It cannot override server-assigned section seeds or introduce a
command, path, URL, token, or arbitrary audio.cpp argument.

At job creation, deterministically plan the complete section table, generate one
cryptographically random `uint64` seed per app-level section, and atomically persist
the canonical source plus complete initial manifest before the first engine
invocation. Resume reuses the stored plan and seeds. Server mode sends the seed as a
decimal JSON string; subprocess mode uses `--seed`. Invalid or unsupported option
shapes fail before durable production creation.

The complete resolved effective options, including defaults, are persisted in the
initial manifest and synthesis identity before the first invocation. cpp-studio does
not persist only the values changed in the browser, because a later default change
must not alter Resume or provenance.

The engine invocation module maps typed options to:

- top-level server fields: `seed`, `num_inference_steps`, `guidance_scale`;
- server `options`: `audio_chunk_threshold_sec`, `audio_chunk_duration_sec`,
  `cross_fade_duration_sec`;
- equivalent CLI flags and `--request-option key=value` values in subprocess mode.

#### P0.3 — durable audiobook store, fingerprints, resume, and discard

Add an audiobook store parallel to the Story store, with audiobook-specific types.
Do not import Story manifests or expose Story line/take semantics.

Proposed layout:

```text
out/audiobooks/
  .book_YYYYMMDD_HHMMSS_NNN.wip/
    manifest.json
    source.txt
    sections/
      section-0001.wav
      section-0001.transcript.txt
      section-0002.wav
  book_YYYYMMDD_HHMMSS_NNN/
    manifest.json
    source.txt
    verification.json
    book.wav
    sections/                 # retained initially for repair/evidence
```

Write source, section audio, transcripts, verification reports, and manifests with
owner-only permissions while they are WIP. Manifest updates use write-to-temp plus
atomic rename. Final publication renames the complete WIP directory into place only
after stitch validation.

Initial creation uses a separate owner-only staging directory. Only after source,
identity, the complete section table, and every seed have been written successfully is
that directory atomically renamed to the public `.wip` name. Cancellation or failure
before this rename removes the unpublished staging directory because no Audiobook
production exists yet; after it, the WIP is durable and only Discard may delete it.

Creation is one durable transition owned by `audiobook.Manager`: extract and
normalize the uploaded document, resolve the selected engine and voice through
injected resolvers, freeze the base synthesis identity, plan every section, assign
every seed, then atomically publish `source.txt` and the complete initial manifest.
The Manager holds its own single-production slot during this work. After staging is
complete it acquires the native engine reservation, then publishes the WIP, so no
engine invocation can precede durable creation and an engine-busy conflict can still
fail synchronously without creating a production.

`POST /v1/audiobooks` returns a production id only after that transition commits, so
every id observed by Gateway is recoverable from the store. Synthesis begins
asynchronously after commit under the already-acquired reservation. If reservation or
publication fails, the unpublished staging directory is removed and no id is returned;
if engine execution later fails, the durable production becomes `interrupted` even
with zero completed sections.

Manifest additions:

```json
{
  "schemaVersion": 2,
  "status": "synthesizing|verifying|stitching|interrupted|complete",
  "sourceFile": "source.txt",
  "sourceSha256": "...",
  "synthesisFingerprint": "...",
  "sectionPolicyVersion": 1,
  "promptPolicyVersion": 1,
  "sections": [
    {
      "id": "section-0001",
      "startByte": 0,
      "endByte": 1234,
      "textSha256": "...",
      "seed": "1844674407370955161",
      "checkpointFingerprint": "...",
      "status": "pending|synthesized|verified|flagged",
      "audioFile": "sections/section-0001.wav",
      "audioSha256": "...",
      "durationMs": 78231,
      "attempts": [
        {
          "id": "attempt-0001",
          "seed": "1844674407370955161",
          "checkpointFingerprint": "...",
          "audioFile": "sections/section-0001.wav",
          "audioSha256": "...",
          "selected": true,
          "createdAt": "2026-08-03T10:00:00Z"
        }
      ]
    }
  ],
  "verification": {
    "mode": "auto|required|off",
    "status": "pending|passed|flagged|unavailable|skipped",
    "verifiedSections": 0,
    "flaggedSections": 0
  }
}
```

P0-04 adds the typed DramaBox request options to each attempt before attempts are
persisted by the durable store. That addition is optional for readers of this schema
and does not change existing field meanings.

The base synthesis fingerprint covers:

- existing engine command, args, server config identity, model path identity, and
  default voice identity;
- selected voice id plus reference WAV identity;
- engine id, direction, and pinned DramaBox request options;
- source hash, section policy version, and prompt policy version.

The base fingerprint deliberately excludes section seeds. Each section receives a
checkpoint fingerprint derived from the base fingerprint, section id and byte range,
text hash, and stored seed. Resume trusts a section only when the manifest entry,
text hash, audio file, WAV validation, audio hash, base fingerprint, and section
checkpoint fingerprint all agree. The canonical source must still match its stored
hash; a mismatch is store corruption and never becomes synthesis input. Resume is
identity-strict: a changed engine/model, voice, direction, option set, or policy
version rejects Resume with an identity-mismatch remedy. Restart creates a separate
Audiobook production from a hash-valid stored source under the new identity and leaves
the original WIP untouched. A P1 new-seed variation is the explicit local exception:
it changes only that section's checkpoint identity and must not invalidate unrelated
completed sections.

Lifecycle rules:

- Once the complete initial manifest exists, cancellation or failure marks the book
  `interrupted` and retains it even when zero sections have completed.
- Process restart discovers interrupted WIP manifests from disk; the jobs registry is
  a live projection and is never treated as durable state.
- Resume resolves the stored engine and voice identifiers, requires the original base
  synthesis identity, acquires the selected engine, skips trusted sections, and
  continues at the first missing or invalid section.
- Restart creates a new production id and initial manifest from the stored source and
  selected identity; it never mutates or deletes the interrupted production.
- Discard is an explicit POST action and removes the WIP directory only after id/path
  validation.
- Only one create, Resume, or Restart execution is active at a time in P0.

#### P0.4 — disk-streamed assembly

Add a WAV file assembler that:

1. validates every section before writing;
2. requires one consistent PCM format;
3. reads one section plus only the crossfade head/tail windows at a time;
4. writes PCM to a temporary final file;
5. performs a 50 ms equal-power crossfade at app-level section boundaries;
6. patches the RIFF/data sizes only after successful completion;
7. refuses RIFF sizes above the 32-bit WAV limit with an actionable “narrate in parts”
   error;
8. validates the completed file before atomic publication.

The final save path must move or stream a staged file. It must not read `book.wav` back
into one large `[]byte` solely to call the existing `save` method.

#### P0.5 — factual fidelity report

Add an injected verifier function so `internal/audiobook` does not depend directly on
gateway or Whisper types:

```go
type VerifyFunc func(ctx context.Context, source string, wav []byte) (Verification, error)
```

The gateway implementation reuses its configured Whisper transcription path. P0
performs verification serially, never concurrently with another section synthesis.
It does not automatically rewrite source text or silently retry generation.

Verification modes:

- `auto` (default): verify when Whisper is configured; otherwise finish honestly with
  `verification.status=unavailable`.
- `required`: reject submission before job creation when a verifier is unavailable.
- `off`: synthesize without ASR and record `skipped`.

For each section, persist:

- exact source hash;
- raw ASR transcript;
- normalized token counts;
- insertions, deletions, substitutions, and word error rate;
- separately flagged numeric/date tokens, acronyms, and likely proper-name anchors;
- verifier identity and timestamp.

Normalization may remove case and punctuation for WER but must retain the raw source
and transcript for evidence. Numeric/name heuristics are warnings because ASR itself
can be wrong. Initial thresholds are configuration constants backed by tests and a
calibration fixture; they must not be advertised as scientific certainty.

P0 behavior:

- `passed`: below the calibrated WER threshold and no critical anchor mismatch;
- `flagged`: report differences and finish the audiobook with a visible warning;
- `unavailable`: finish with a visible “Not verified” state;
- verifier engine error in `auto`: record unavailable/error and continue;
- verifier engine error in `required`: retain WIP and fail with a resume remedy.

#### P0.6 — API and UI lifecycle

Keep the existing `POST /v1/audiobooks` fields. Add optional
`verification=auto|required|off`. Preserve `chunks` in the response for compatibility
and add `sections` when using the new policy.

Extend the routes:

```text
POST /v1/audiobooks/preview
     -> resolved typed request without starting a job
GET  /v1/audiobooks
     -> {"audiobooks": [...], "interrupted": [...]}
GET  /v1/audiobooks/{id}
POST /v1/audiobooks/{id}/resume
POST /v1/audiobooks/{id}/restart
POST /v1/audiobooks/{id}/discard
GET  /v1/audiobooks/{id}/verification
GET  /v1/audiobooks/{id}/artifact/book.wav
```

The Audiobook desk adds:

- curated DramaBox controls and a resolved effective-request preview; raw JSON stays
  collapsed under an Advanced/operator disclosure and passes the same typed validator;
- a verification selector with plain-language defaults;
- section-based progress (`section 3/18`, plus native engine detail when available);
- interrupted-book cards with Resume, Restart, and Discard; identity-mismatched work
  disables Resume and explains why Restart creates a separate production;
- explicit `Verifying`, `Verified`, `Differences found`, `Not verified`, and
  `Interrupted` states;
- a link or expandable panel for section-level fidelity findings;
- no claim that ASR proof replaces listening.

P0 exit criteria:

- Fast narrator output and API behavior remain backward compatible.
- DramaBox requests activate native long-form chunking under the pinned policy.
- Cancelling after durable creation always retains work, including a zero-section
  production; resuming skips valid sections.
- Restart discovery works without a jobs-registry entry.
- Identity changes reject Resume and Restart forks a new production; missing or corrupt
  section WAVs re-synthesize safely under an unchanged identity.
- Final assembly remains bounded in memory and refuses RIFF overflow.
- Verification passes, flags, and degrades honestly when unavailable.
- The preview and stored manifest contain the same complete resolved effective options,
  including defaults; changed-only UI state is never the production record.
- Existing finished manifests still load.
- Full fixture verification passes.
- Stage status remains runtime-unverified until the real release-0.5 model run passes.

### P1 — reproducible repair, voice fitness, and measured cost

P1 turns P0 evidence into controlled user actions and a hardware profile.

#### P1.1 — reproducible and variation retries

Retain each section attempt until the book is finalized or explicitly cleaned up.
Add two bounded actions:

- **Reproduce:** run the same text, prompt, options, voice, model fingerprint, and seed.
  This diagnoses nondeterministic runtime behavior.
- **Variation:** allocate a new seed while preserving the earlier attempt and its
  verification report.

Add:

```text
POST /v1/audiobooks/{id}/sections/{sectionId}/retry
{"mode":"reproduce|variation"}
```

Only generated section ids are accepted. The request cannot supply file paths, raw
flags, arbitrary model ids, or arbitrary seeds. Each new attempt records its parent,
seed, option set, output hash, transcript, and selection state. Selecting a repaired
attempt creates a new final render revision rather than overwriting the original WAV.

Each attempt distinguishes `requestedSeed` from `actualSeed`. When audio.cpp reports
the seed actually used, persist and display it; a mismatch is an engine-contract error
and the output is not eligible for automatic Resume reuse. When the transport cannot
report an actual seed, label the stored value `requested seed` rather than claiming it
was independently confirmed. Seed values remain decimal strings at JSON boundaries.

Advanced guidance is exposed as tested presets, not free numeric fields:

- `natural` — upstream defaults;
- `faithful` — values chosen only after benchmark/listening evidence;
- `custom` remains a configuration-file escape hatch for operators, with strict
  numeric ranges and provenance.

#### P1.2 — DramaBox voice-reference fitness

Extend WAV analysis with:

- duration, sample rate, channels, and bit depth;
- peak amplitude and clipped-sample ratio for 16-bit PCM;
- RMS/energy estimate and leading/trailing/total low-energy ratios;
- optional spoken-duration estimate through a configured VAD capability.

Persist analysis in the voice manifest so it is not recomputed on every book. Existing
voice manifests without analysis remain valid and are analyzed lazily.

Policy:

- A valid stored voice remains usable by other engines.
- DramaBox cloning requires at least 10 seconds of usable reference speech; a shorter
  reference is disabled only in the DramaBox voice picker.
- Clipping, heavy silence, or unsupported format produces a warning and remedy.
- Text-only DramaBox remains available when no suitable clone exists.
- Engine-specific guidance replaces the generic “5–15 seconds” hint when DramaBox is
  selected.
- Optional VAD absence falls back to PCM heuristics and is disclosed.

The browser lets the user select or clear an authorized stored reference, listen to it,
see duration/format/fitness and provenance before submission, and reuse the same
reference without re-uploading it. It shows the exact reference id and content hash in
the resolved-request preview. Convenience never weakens the existing consent gate,
silently trims/replaces a source, or treats a voice selection as permission to clone.

Do not adopt RE-USE denoising by default. Its Windows dependency posture and
non-commercial licence are incompatible with a simple general-purpose local setup.

#### P1.3 — DramaBox benchmark harness

Create `scripts/benchmark-dramabox-audiobook.ps1` and a deterministic fixture under
`testdata/benchmark/`. Reuse the evidence discipline of the existing Story benchmark,
but do not force DramaBox into its batch-line assumptions.

Required cases:

1. plan-only/config validation without model weights;
2. one cold text-only factual paragraph;
3. the same request warm;
4. one multi-section factual excerpt exercising native long-form chunking;
5. one voice-clone case when an authorized reference is supplied;
6. default versus `dramabox.mem_saver=true` in fresh servers;
7. CPU baseline; CUDA only when explicitly requested;
8. cancellation and process-restart recovery;
9. ASR fidelity report for generated outputs.

Record:

- audio.cpp tag/commit and binary identity;
- model path identity, expected/actual bytes, checksum when available;
- backend, device, threads, session and request options, seed;
- cold load, queue/reservation wait, synthesis wall time, verification time, assembly
  time, total wall time, audio duration, and RTF;
- peak and post-request resident VRAM;
- CPU and process memory where available;
- WAV format and output size;
- WER/anchor findings;
- projected time for a representative 10,000-word chapter.

Performance labels are descriptive rather than dishonest pass/fail claims:

- `interactive`: RTF <= 1;
- `batch-usable`: 1 < RTF <= 5;
- `overnight`: RTF > 5, with a projected completion time;
- `failed`: OOM, timeout, invalid output, or incomplete workload.

The user may choose an overnight narrator. The app's job is to show the cost before a
full book starts. A benchmark result never certifies subjective voice quality.

#### P1.4 — benchmark and repair UI

Add a `Benchmark this narrator` action using the checked-in factual excerpt. Display:

- runtime/profile identity;
- cold and warm RTF;
- projected chapter time;
- peak/resident VRAM when measurable;
- verification result;
- last benchmark time and whether engine/model identity has changed since.

Before a large DramaBox submission, show the current projection. Do not block an
overnight run if the user knowingly accepts it. Do block a configuration whose last
matching benchmark failed to load or produced invalid audio, unless the user changes
the configuration and reruns the benchmark.

Flagged sections expose Reproduce and Variation actions, attempt auditioning, exact
requested/actual seed status, resolved options, per-attempt timings, and an explicit
Select action. No attempt silently replaces another. Live production shows current
phase elapsed time and completed-section timing without estimating precision the
runtime does not provide.

P1 exit criteria:

- Same-seed reproduce requests are byte-identical when the runtime is deterministic,
  or visibly reported as nondeterministic when they are not.
- Variation preserves earlier attempts and produces a new render revision.
- DramaBox voice eligibility is engine-specific and does not break other TTS lanes.
- Benchmark fixtures test plan-only/error paths without requiring the real model.
- A real run, when assets exist, records all metrics and projects chapter time.
- Requested versus actual seed status and measured timing phases are visible and
  retained with every attempt and benchmark.
- Reference selection, preview, clearing, fitness, and provenance are available without
  bypassing voice authorization.
- The UI never calls local generation “free” without its compute/storage warning.

### P2 — discovery, confirmed setup, and prompt correctness

P2 improves discovery and setup after reliable production and measurement exist. Its
download boundary is **no silent download**, not “no automatic download”: cpp-studio
may install an allowlisted catalog artifact only after a fresh preview and explicit
confirmation, and the resulting work remains a normal tracked job.

#### P2.1 — bounded audio.cpp capability discovery

Keep tracked `models.json` as cpp-studio's provenance, licence, expected-size, revision,
destination, and checksum authority. Add a discovery adapter that executes only
configured, fixed commands:

```text
python tools/model_manager_v2.py list --json
python tools/model_manager_v2.py info <server-allowlisted-package> --json
audiocpp_cli --list-loaders --json
```

The set of package ids eligible for the `info` command is defined server-side and must
also map to tracked catalog entries. Do not accept a command path, package id, or
arbitrary arguments from HTTP input. Discovery output can report availability but
cannot override catalog provenance or authorize installation.

Merge these states without conflating them:

| State | Meaning |
|---|---|
| Cataloged | Declared by cpp-studio `models.json` |
| Installable | Catalog entry is server-allowlisted and has complete immutable install metadata |
| Loader available | Current audio.cpp binary reports the family |
| Present | Expected local path exists and matches fast checks |
| Verified | Expected size and tracked checksum verification passed |
| Configured | A cpp-studio engine points at the model/runtime |
| Healthy | The configured engine currently reports healthy |

Discovery is optional and cached with the runtime identity. Missing Python, missing
manager script, old audio.cpp, malformed JSON, or a timed-out command leaves the
existing catalog functional and adds an actionable `discoveryError`.

Extend `GET /v1/models/catalog` with optional discovery and installation-readiness
fields rather than replacing it. Old clients ignore the additions.

#### P2.2 — confirmed model installation

The first supported in-product package is the currently ungated tracked
`dramabox-q8-0` artifact. The contract remains reusable only for later packages that
are explicitly added to the server allowlist and have the same complete catalog
metadata. Installation is a two-step operation:

```text
POST /v1/models/{id}/install/preview
     -> confirmationId, expiresAt, modelId, source, revision, destination,
        licence, expectedBytes, checksum, freeSpace, vramWarning, blockers

POST /v1/models/{id}/install
{"confirmationId":"model-install-confirmation-id"}
     -> existing-style tracked job
```

The preview resolves all values from the tracked catalog and server configuration,
then stores an immutable, short-lived confirmation record server-side. It includes a
fingerprint of the selected catalog entry, models root, artifact identity, and
destination. The install endpoint accepts only the opaque confirmation id: the route
model id and current selection must still match, the confirmation must be unexpired
and unused, and consuming it is atomic. Reuse, expiry, a catalog/configuration change,
or a changed selected model blocks installation and requires a fresh preview.

Neither endpoint accepts a browser-supplied command, package, path, repository,
revision, token, proxy, destination, source URL, or arbitrary argument. Hugging Face
authentication comes only from the cpp-studio server environment or its authenticated
local cache; credentials are never accepted from, returned to, or logged for the
browser.

Safe installation behavior:

- Missing licence, unknown/non-positive download size, missing tracked checksum,
  insufficient free space, an existing destination, or any other preview blocker
  disables confirmation.
- Low projected remaining disk is a prominent warning. Estimated VRAM incompatibility
  is also a warning because CPU/memory-saving operation may still be valid; it does
  not block downloading.
- Download into a unique staging directory beneath the configured models root. Resolve
  and verify both staging and final paths remain beneath that root; never stage into a
  browser-selected or system temporary directory.
- Mark staging with installer-owned metadata. On startup or the next catalog refresh,
  remove only validated orphan staging created by this installer; never infer cleanup
  targets from names alone.
- Hold a destination-scoped install lock. A concurrent install for the same artifact
  or destination returns a conflict and cannot race promotion.
- Stream to staging with byte-count progress. On cancellation or failure, remove only
  that validated staging directory and preserve every existing model.
- Verify expected size and tracked checksum in staging, then atomically promote the
  file or complete package directory. v1 never overwrites or merges an existing
  destination.
- After promotion, refresh the catalog and require the normal size/checksum checks to
  pass before reporting the model `ready`. A refresh/verification failure leaves the
  artifact present but not ready and provides an actionable repair; it never silently
  starts or reloads an engine.

The returned job uses `GET /v1/jobs/{id}` and
`POST /v1/jobs/{id}/cancel`. Its detail reports `preparing`, `downloading`,
`verifying`, `promoting`, and `refreshing_catalog`, with bytes downloaded and expected
bytes where applicable. Cancellation becomes unavailable once atomic promotion begins.

#### P2.3 — readiness, GGUF inspection, and lifecycle integration

The Models tab presents readiness as a sequence:

```text
Loader available -> package known -> installation confirmed/file present
-> checksum verified -> engine configured -> engine healthy -> benchmark current
```

Each failed step shows problem, cause, and remedy. Examples:

- `Loader unavailable`: “This binary is audio.cpp 0.4.2; DramaBox requires 0.5.”
- `Model missing`: show expected destination, bytes, immutable source/revision,
  licence, disk result, warnings, and Preview installation.
- `Configured but unhealthy`: link engine log tail and configuration section.
- `Benchmark stale`: identify which engine/model fingerprint changed.

For a present GGUF, reuse the bounded `internal/gguf` reader to show supported header
facts such as GGUF version, architecture, expert count, file size, and size label.
Inspection failure degrades to size/readiness information with the exact reason; it
does not make an otherwise verified artifact disappear or attempt a full tensor scan.

Model cards link to cpp-studio's existing engine/profile/variant controls. Start,
Stop, Reload, or variant selection continues through the current gateway routes and
Lifecycle Manager, honoring busy-engine and reservation rules. The browser never
talks directly to audio.cpp, and successful installation does not imply configuration,
health, benchmark acceptance, or an automatic lifecycle action.

Do not merge “present” and “works.” A correct 18.9 GB file behind an old binary is not
ready, and a healthy fixture is not a real-model proof.

#### P2.4 — structured factual prompt builder and linter

Replace the single free-form default with a structured factual prompt model while
retaining an advanced escape hatch:

```go
type DramaBoxPromptSpec struct {
    SpeakerPhrase string // allowlisted generic phrases
    DeliveryPreset string
    AdvancedDirection string
}
```

The initial speaker phrase list comes only from upstream patterns validated by
listening (`A man`, `A woman`, `A young woman`, `An elderly man`, `A child`). Do not
invent a neutral phrase and label it validated. If a suitable neutral form is found,
add it only with a checked-in listening note and fixture prompt.

Linter rules:

- warn on role nouns such as radio host, teacher, detective, nurse, or narrator;
- warn on stacked speaker adjectives and overlong descriptions;
- reject double quotes in stage direction unless safely normalized and previewed;
- keep all source speech inside one or more quoted spans;
- end at the final closing quote;
- warn on paralinguistic spellings or actions in factual presets;
- show every punctuation normalization applied to source or direction;
- never change source words silently;
- version the prompt policy and include it in synthesis fingerprints.

The UI shows the exact generated prompt before submission and distinguishes:

- **Source text:** immutable narratable content;
- **Delivery controls:** affect performance only;
- **Generated prompt preview:** the exact request sent to DramaBox.

Book-level direction remains the default. Per-section direction is out of scope until
there is a concrete factual use case and an editing workflow that can audit it.

#### P2.5 — provenance export

Extend the final manifest and optional JSON sidecar with:

- `aiGenerated: true`;
- engine/family/runtime/model identity;
- prompt and section policy versions;
- voice reference provenance when authorized;
- request presets/options and section seeds;
- verification summary and report path;
- benchmark profile identity;
- explicit `watermark: unknown|present|absent`, never an inferred claim.

P2 exit criteria:

- Catalog behavior is unchanged when discovery is unavailable.
- Loader/package/presence/config/health/benchmark states remain distinct.
- Discovery commands are fixed, bounded, and fixture-tested against malformed output.
- No download occurs without a fresh, explicit preview confirmation tied to immutable
  server-side catalog data; stale, changed, reused, or concurrent confirmations fail.
- Installation is cancellable while safe, reports phase/byte progress, never
  overwrites, cleans partial staging on failure, and verifies size/checksum before
  readiness.
- GGUF facts and engine lifecycle remedies use existing bounded inspection, gateway,
  reservation, and Lifecycle Manager seams.
- Prompt preview matches the exact server/CLI request byte-for-byte after documented
  normalization.
- Lint warnings never alter source content automatically.
- No silent model download, unreviewed licence, or browser-controlled install input is
  introduced.

## Architecture

```text
Browser Audiobook desk + Models/Engines
  |
  | audiobook preview/lifecycle/retry/benchmark
  | install preview/confirm + existing engine lifecycle actions
  v
Gateway HTTP validation
  |
  +--> audiobook.Manager --------------------+
  |      |                                   |
  |      +--> Document extraction            |
  |      +--> Engine + voice resolvers       |
  |      +--> SectionPlanner                 |
  |      +--> audiobook.Store (.wip/final)   |
  |      +--> Frozen identity + request      |
  |      +--> FidelityVerifier               |
  |      +--> Streaming WAV assembler        |
  |      |                                   |
  |      +--> Engine invocation ---------> audio.cpp 0.5
  |      |     server JSON / CLI             DramaBox
  |      |                                   |
  |      +--> Whisper adapter -----------> whisper.cpp
  |
  +--> Benchmark runner ----------------> process/GPU metrics
  |
  +--> Model catalog --------------------> models.json (install authority)
  |         |
  |         +--> bounded discovery -----> model_manager_v2 + --list-loaders
  |         +--> GGUF inspection -------> internal/gguf
  |         +--> confirmation store
  |                    |
  |                    +--> install job -> models-root staging
  |                                      -> size/checksum -> atomic promote
  |
  +--> Lifecycle Manager ---------------> existing start/stop/reload/profile routes
```

Ownership boundaries:

- `internal/audiobook` owns extraction and canonical source creation, engine and voice
  resolution through injected resolvers, the frozen synthesis identity, complete
  section plans and seeds, durable lifecycle, verification records, attempt selection,
  and final audiobook manifests.
- `internal/gateway` owns HTTP envelope validation and response/error shaping only.
- `internal/engine` owns typed option validation and mapping to audio.cpp resident JSON
  and subprocess CLI transports.
- `internal/wav` owns PCM validation and bounded file assembly.
- `internal/models` owns catalog/discovery state, install-preview confirmations,
  destination-safe staged installation, GGUF inspection, and catalog refresh; it never
  owns synthesis or directly mutates engine lifecycle.
- `jobs.Registry` projects live installation phase and byte progress and routes safe
  cancellation; durable model truth remains the filesystem plus tracked catalog.
- `internal/lifecycle` remains the sole owner of engine start/stop/reload/profile and
  variant operations after installation.
- `scripts` owns hardware measurement; production handlers do not scrape `nvidia-smi`
  during normal narration.

### Deepened Audiobook production seam

Gateway crosses one high-leverage Manager interface for lifecycle commands (`Create`,
`Resume`, `Restart`, `Cancel`, `Discard`) and durable queries (`Get`, `List`, artifact
resolution). `Create` receives an HTTP-independent input containing the filename,
uploaded bytes, title, selected engine and voice ids, direction, and verification
intent. Gateway may enforce multipart and upload-size rules, but it does not call
extraction, resolvers, planning, fingerprinting, the store, or Engine invocation
separately.

Those collaborators remain internal seams of the module and are injectable where a
real adapter varies or a safety constraint requires control. Callers and lifecycle
tests use the same Manager interface. Under the deletion test, removing the Manager
would redistribute extraction order, identity freezing, atomic storage, reservation,
checkpoint trust, and recovery across every caller, so the module earns its depth.

## State machine

```text
submitted
   |
   v
planning ---------- cancel/failure -------------> interrupted
   |                                               |
   | complete source, plan, identity, seeds        | exact-identity resume
   v                                               |
synthesizing ------ cancel/failure ---------------+
   |                                               |
   | all sections                                  |
   v                                               |
verifying -------- required verifier failure ------+
   |
   +--> passed / flagged / unavailable / skipped
   v
stitching -------- failure -----------------------> interrupted
   |
   v
complete -------- P1 section repair -------------> new render revision

interrupted ------ explicit discard -------------> deleted
      |
      +---------- changed-identity restart -------> submitted (new id)
```

`flagged` is a verification outcome, not a claim that the source is wrong. P0 can
publish a flagged audiobook with an unmistakable warning and retained evidence.

## API compatibility and migration

- Existing multipart fields remain valid.
- Missing engine still defaults to `audio`.
- Existing finished manifests without `schemaVersion`, sections, or verification load
  as legacy complete audiobooks.
- Existing artifact URLs remain unchanged.
- New response fields are additive.
- WIP directories are new; old hidden temporary clip directories are neither resumed
  nor treated as finished books.
- The jobs route remains a live-process projection. Audiobook status and interrupted
  discovery always come from the audiobook store, including after process restart.
- Install-preview confirmations are short-lived, in-memory authorization records, not
  durable jobs or API credentials. A gateway restart invalidates them safely.
- Model install jobs use the existing jobs envelope and cancellation route; new phase
  and byte fields are additive.
- No migration rewrites old manifests in place.

## Error & Rescue Registry

| Failure | Classification | Persisted state | Rescue shown to user |
|---|---|---|---|
| Local audio.cpp is older than 0.5 | Not configured | None | Build the pinned 0.5 runtime side by side; keep fast narrator available |
| DramaBox loader absent | Not configured | None | Show binary identity and `--list-loaders` result |
| Model missing/size mismatch | Not configured/corrupt | None | Show expected path, bytes, source, licence, verification action |
| Model load OOM | Engine failure | Planned WIP retained as interrupted | Restore the same engine identity to Resume, or Restart with a different profile |
| Section synthesis fails | Engine failure | Completed sections retained | Resume from failed section after fixing engine |
| User cancels after durable creation | Interrupted | WIP retained, even with zero completed sections | Resume or Discard |
| Process dies | Interrupted on next discovery | WIP retained | Resume after startup |
| Manifest corrupt | Store failure | WIP quarantined/read-only | Report exact file; do not synthesize or delete automatically |
| Source missing/hash mismatch | Store integrity failure | WIP quarantined/read-only | Restore the original source or create a new production from a trusted upload |
| Section WAV missing/hash mismatch | Invalid checkpoint | Other valid sections retained | Re-synthesize only invalid section |
| Synthesis identity changed | Resume conflict | Original WIP remains untouched | Restore the original configuration to Resume, or Restart as a new production |
| Disk full while checkpointing | Store failure | Last atomic manifest remains | Free space, then Resume |
| Section output exceeds 32 MiB | Engine/output failure | Earlier sections retained | Revise the versioned section policy and Restart; preserve the original WIP |
| Final RIFF would exceed 4 GiB | Unsupported output | Sections retained | Narrate/export in parts; RF64 is out of scope |
| Crossfade format mismatch | Invalid output | Sections retained | Identify offending section and re-synthesize |
| Whisper unavailable in auto mode | Verification unavailable | Book can complete | Show Not verified; configure Whisper or choose required next time |
| Whisper unavailable in required mode | Not configured | Submission rejected | Configure Whisper or change verification mode |
| Whisper fails mid-required verification | Interrupted | Synthesized sections retained | Fix verifier and Resume |
| Fidelity differences found | Verification warning | Report retained | Review transcript/diff; P1 retry or accept with warning |
| Voice reference too short for DramaBox | Invalid engine-specific choice | Voice remains stored | Use text-only or provide 10+ seconds of clean speech |
| Benchmark fails/aborts | Measurement failure | Partial result marked invalid | Fix configuration and rerun; never reuse partial metrics |
| Discovery command missing/malformed | Optional discovery failure | Cached/static catalog retained | Show exact command/error; normal catalog continues |
| Install preview has incomplete catalog metadata | Installation blocked | No confirmation/job | Add licence, immutable revision, size, checksum, and allowlisted destination to the tracked catalog |
| Install confirmation expired/reused/changed | Confirmation conflict | No new download | Refresh readiness, preview again, and explicitly reconfirm |
| Destination already exists | Installation conflict | Existing model untouched | Verify/use the existing artifact or choose an operator-managed cleanup path |
| Download cancelled or fails | Interrupted installation | Validated staging removed; existing models untouched | Check authentication/network/disk and create a fresh preview |
| Download size/checksum mismatch | Artifact verification failure | Invalid staging removed; destination untouched | Refresh trusted catalog metadata or retry from the immutable source |
| Catalog refresh fails after promotion | Readiness failure | New artifact present but not ready | Re-run verification/refresh; do not start the engine yet |
| Prompt lint warning | User-correctable warning | No job yet | Edit structured direction or explicitly accept advanced prompt |

## Failure Modes Registry

| Risk | Severity | Prevention/detection | Stage |
|---|---|---|---|
| App-level tiny chunks prevent DramaBox native joining | High | Engine-specific duration planner and request-shape tests | P0 |
| Resume splices audio from changed model/voice/options | Critical | Strict base identity; mismatch rejects Resume and requires Restart | P0 |
| Cancellation deletes planned or completed work | Critical | Durable WIP from initial manifest; only Discard deletes | P0 |
| Final book exhausts RAM | Critical | Streaming assembler and 4 GiB guard | P0 |
| Generated speech changes facts | High | ASR report, anchor warnings, raw evidence | P0 |
| ASR errors are misrepresented as TTS errors | High | Label report as heuristic; retain transcript/source; no silent rewrite | P0 |
| Stable seed exceeds JSON integer precision | High | Send decimal JSON string and test max `uint64` | P0 |
| DramaBox and Whisper compete for GPU memory | High | Serial phases, CPU-first DramaBox, benchmark/profile evidence before coexistence | P0/P1 |
| Poor reference produces unstable clone | Medium | Engine-specific duration/PCM/VAD fitness | P1 |
| Benchmark fixture succeeds but real model does not | High | Separate fixture/code and live-runtime gates | P1 |
| User mistakes local for costless | Medium | Runtime projection plus storage/electricity warning | P1 |
| Discovery output is treated as trusted install authority | High | Tracked catalog plus server allowlist remain the only install authority | P2 |
| Browser input reaches a model-manager command | Critical | Install accepts only opaque confirmation id; fixed argv and allowlisted package tests | P2 |
| Catalog/destination path escapes the models root | Critical | Canonical containment checks for staging and final paths; hostile traversal tests | P2 |
| Download exhausts disk | Critical | Known size and free-space blocker; low-remaining warning; streamed writes and disk-full cleanup | P2 |
| Cancelled/failed download becomes visible as a model | Critical | Unique in-root staging, checksum/size gate, atomic promotion, cleanup tests | P2 |
| Missing or ambiguous licence is treated as accepted | High | Missing licence blocks preview confirmation; exact catalog licence is shown before confirmation | P2 |
| Concurrent installs corrupt or overwrite an artifact | Critical | Destination lock, no-overwrite promotion, concurrency tests | P2 |
| Stale or reused confirmation installs changed content | Critical | Short TTL, single-use consume, immutable selection fingerprint, adversarial replay tests | P2 |
| Prompt direction is spoken aloud | High | Structured prompt, role-noun lint, exact preview | P2 |
| Prompt policy change makes resume incompatible | High | Prompt policy version rejects Resume and directs Restart | P2 |
| Watermark is claimed without native evidence | Medium | Explicit unknown/present/absent provenance field | P2 |

## Security, privacy, and licence boundaries

- All services remain loopback-bound by default.
- Source books, voice references, generated WAVs, transcripts, diffs, and benchmark
  outputs remain local.
- WIP files use restrictive permissions; final published artifacts keep current local
  serving behavior.
- Generated ids and allowlisted route segments are used for every artifact path.
- Resume/restart/discard/retry endpoints never accept filesystem paths.
- audio.cpp option names are typed and allowlisted; no shell interpolation or raw flag
  passthrough is added.
- Discovery executes configured binaries with fixed argument arrays and bounded time,
  output size, and JSON depth.
- Model installation requires an unexpired, single-use confirmation derived wholly
  from server-side allowlisted catalog data. No install endpoint accepts commands,
  packages, paths, repositories, revisions, tokens, proxies, URLs, or arguments.
- Staging and promotion stay beneath the configured models root, never overwrite, and
  are guarded by canonical path containment plus a destination lock.
- Licence, immutable source/revision, expected size, checksum, disk result, VRAM
  warning, and blockers are displayed before confirmation. Missing/ambiguous licence
  or unknown size/checksum blocks installation; VRAM incompatibility warns only.
- Hugging Face credentials come only from the server environment or authenticated
  local cache and are never exposed to the browser or job logs.
- Existing voice-reference consent and provenance requirements remain unchanged.
- RE-USE is excluded by default because of platform and licence constraints.
- Native DramaBox output is not labelled Perth-watermarked without a successful
  detector result or an explicit audio.cpp contract.

## UI design requirements

Information order in the Audiobook desk:

1. document and title;
2. narrator engine and voice readiness;
3. verification mode;
4. restrained delivery controls;
5. benchmark projection and material warning;
6. Narrate action;
7. live section/progress state;
8. verification outcome and playback;
9. interrupted and finished books.

Required states:

| State | Primary text | Available actions |
|---|---|---|
| Checking configuration | Checking local narrator… | None |
| Ready | Narrator ready | Benchmark, Narrate |
| Benchmark stale/missing | Runtime not measured | Benchmark, Narrate with warning if load has not failed |
| Synthesizing | Section N of M | Cancel |
| Verifying | Checking spoken text against source | Cancel |
| Interrupted, identity compatible | N of M sections preserved | Resume, Discard |
| Interrupted, identity changed | Original production preserved | Restart, restore configuration, Discard |
| Differences found | N sections need review | Inspect, P1 Retry/Variation, Play |
| Not verified | Whisper unavailable or verification off | Play, configure verifier |
| Complete | Verified/flagged status plus duration | Play, Save |
| Fatal setup error | Problem + cause + remedy | Open Models/Engines guidance |

Required Models states:

| State | Primary text | Available actions |
|---|---|---|
| Install metadata blocked | Cannot install safely + exact blocker | View catalog guidance |
| Preview ready | Source, revision, licence, size, disk and VRAM result | Confirm installation, Cancel |
| Downloading | Phase + downloaded/expected bytes | Cancel while safe |
| Verifying/promoting | Verification or atomic installation in progress | Cancel only before promotion |
| Installed, not configured | Artifact verified; engine not configured | Open configuration/lifecycle guidance |
| Configured, stopped/unhealthy | Exact lifecycle state and remedy | Existing Start/Reload/log actions |
| Ready | Verified, configured, healthy | Inspect GGUF, Benchmark, Select |

Keyboard access, visible focus, labelled progress, non-colour-only status, and live
region announcements apply to every new control. Large warnings use the existing
`warn-box` treatment rather than muted helper text.

## Engineering test diagram

```text
P0 request validation
  +-- audio engine keeps legacy chunk/gap behavior ........ unit + gateway regression
  +-- dramabox section estimator/boundaries ............... table/property tests
  +-- option range and JSON/CLI mapping .................... engine + upstream fixture tests
  +-- uint64 seed encoded as JSON string ................... engine contract test
  +-- gateway passes upload intent through one Manager seam  gateway seam test

P0 durable lifecycle
  +-- extraction + full plan + all seeds -> atomic manifest  manager/store test
  +-- no engine invocation precedes durable creation ....... manager ordering test
  +-- cancel/fail with zero or more sections -> interrupted  manager + gateway test
  +-- process restart -> WIP listing ....................... store/gateway test
  +-- exact-identity resume skips trusted sections ......... manager test
  +-- identity mismatch rejects resume without mutation .... adversarial table tests
  +-- restart forks new id and preserves original WIP ...... manager/store test
  +-- missing/corrupt/tampered section re-synthesizes ...... hostile filesystem tests
  +-- explicit discard only removes allowlisted WIP ........ route/store security tests

P0 audio assembly
  +-- identical PCM formats crossfade correctly ............ WAV sample-level unit test
  +-- format mismatch and corrupt clip fail ................ negative tests
  +-- memory remains bounded for many fixture sections ..... allocation/large fixture test
  +-- RIFF overflow is refused before header wrap .......... boundary test
  +-- final publish is atomic ............................... filesystem test

P0 fidelity
  +-- identical transcript passes ......................... normalizer/diff unit test
  +-- insertion/deletion/substitution counts ............... table tests
  +-- numeric/name anchors flag separately ................. table tests
  +-- raw evidence preserved ............................... store test
  +-- auto/required/off verifier modes ...................... manager + gateway tests
  +-- verifier busy/failure/cancel paths .................... chaos tests

P1 repair and voice fitness
  +-- reproduce keeps seed/options/fingerprint ............. manager/adapter tests
  +-- variation allocates seed and preserves parent ........ manager/store tests
  +-- attempt selection creates render revision ............ store/gateway tests
  +-- 10-second eligibility boundary ....................... WAV/voice table tests
  +-- clipping/silence/unsupported PCM warnings ............ sample-level tests
  +-- old voice manifests lazily gain analysis ............. compatibility test

P1 benchmark
  +-- plan-only never needs model weights .................. PowerShell harness test
  +-- malformed manifests/metrics rejected ................. harness negative tests
  +-- stale output/concurrent run rejected .................. harness lock tests
  +-- CPU/CUDA/mem-saver identities distinct ............... fixture test
  +-- projection and RTF labels deterministic .............. calculation tests

P2 discovery and prompt
  +-- manager/loader JSON parsed with bounded output ........ models unit tests
  +-- missing tool/malformed JSON/timeouts degrade .......... negative tests
  +-- static catalog remains authoritative ................. gateway regression
  +-- fixed command arguments only ......................... security tests
  +-- preview resolves immutable allowlisted metadata ....... models/gateway tests
  +-- command/package/path/repo/revision/token/proxy rejected  adversarial HTTP tests
  +-- traversal and symlink escape stay under models root ... hostile filesystem tests
  +-- stale/reused/changed confirmation rejected ............ clock/replay tests
  +-- concurrent same-destination install conflicts ......... race/concurrency tests
  +-- unknown size/licence/checksum and low disk policy ..... preflight table tests
  +-- disk full/cancel/network failure removes staging ...... chaos/filesystem tests
  +-- restart removes only marked orphan install staging .... recovery/security tests
  +-- size/checksum then no-overwrite atomic promotion ...... artifact tests
  +-- progress exposes phase and byte counts; cancel works .. jobs + browser tests
  +-- bounded GGUF facts degrade without blocking catalog ... gguf/models tests
  +-- lifecycle links call existing gateway routes only ..... browser/gateway tests
  +-- prompt lint rules and exact preview ................... table + browser tests
  +-- policy version rejects Resume and preserves WIP ....... resume/restart regression

Whole application
  +-- gofmt/go test/go vet/config checks .................... scripts/verify.ps1
  +-- browser assets and audiobook lifecycle smoke ......... demo + smoke tests
  +-- real DramaBox load/RTF/fidelity ....................... manual local acceptance
  +-- human listening for joins/voice/acting ................ explicit human gate
```

## Planned file map

Names may adjust during implementation, but responsibilities must remain separated.

| Area | Likely files |
|---|---|
| Section policy and request types | `internal/audiobook/sections.go`, `internal/audiobook/types.go` |
| Durable orchestration, store, resume, restart | `internal/audiobook/store.go`, `internal/audiobook/manager.go` |
| Fidelity comparison | `internal/audiobook/fidelity.go` or `internal/fidelity/` |
| Streaming WAV assembly | `internal/wav/wav.go` or `internal/wav/stream.go` |
| Typed audio.cpp transport | `internal/engine/specs.go`, `internal/engine/engine.go` |
| Voice fitness | `internal/wav/analysis.go`, `internal/voice/store.go` |
| Benchmark | `scripts/benchmark-dramabox-audiobook.ps1`, paired harness test, `testdata/benchmark/` |
| Discovery, inspection, and confirmed install | `internal/models/discovery.go`, `internal/models/install.go`, `internal/gguf/`, `internal/gateway/gateway.go` |
| Browser | `internal/demo/static/index.html`, `app.js`, `styles.css`, `internal/demo/demo_test.go` |
| Docs/config | `README.md`, `docs/API.md`, `docs/CONFIG.md`, `docs/ROADMAP.md`, example JSON |

## Implementation sequence

### P0 task order

- [x] P0-01: Pin and qualify the release-0.5 runtime contract without replacing 0.4.2.
- [x] P0-02: Introduce versioned audiobook manifest, section, verification, and attempt types.
- [x] P0-03: Add DramaBox duration-based section planning and stable seed assignment; preserve fast chunking.
- [x] P0-04: Add curated controls, resolved-request preview, complete effective-option persistence, and engine-owned typed server/subprocess mapping.
- [x] P0-05: Add injected engine/voice resolution and freeze the audiobook synthesis identity.
- [x] P0-06: Add the durable audiobook store and atomic source/plan/seed creation.
- [x] P0-07: Deepen the Manager around create/cancel/fail/resume/restart/discard store transitions.
- [x] P0-08: Add streaming WAV crossfade assembly and RIFF boundary guard.
- [x] P0-09: Add injected fidelity verification and report storage.
- [x] P0-10: Extend API routes and error mapping.
- [x] P0-11: Add interrupted/identity-conflict/verification UI states.
- [x] P0-12: Update docs and examples.
- [x] P0-13: Run full fixture verification, including Manager interface and crash-ordering tests.
- [x] P0-14: Run real release-0.5 acceptance if the model is locally available.
  Checked 2026-08-03: the exact DramaBox GGUF is absent and the sibling audio.cpp
  checkout is `release-0.4.2` (`27d87ba`), so real acceptance was not runnable.
  P0 remains fixture-verified and explicitly runtime-unverified.

### P1 task order

- [x] P1-01: Add immutable section attempts, requested/actual seed reporting, and same-seed reproduction.
- [x] P1-02: Add new-seed variation, attempt selection, per-attempt timings, and render revisions.
- [x] P1-03: Implement PCM reference analysis, persisted fitness metadata, and select/preview/clear reference UX.
- [x] P1-04: Add optional VAD integration and honest fallback.
- [x] P1-05: Enforce DramaBox-only 10-second usable-speech eligibility.
- [x] P1-06: Build and self-test the DramaBox benchmark harness.
- [x] P1-07: Add benchmark API/job and fingerprinted result persistence.
- [x] P1-08: Add timing/projection, repair, reference, and attempt-audition UI.
- [ ] P1-09: Run real CPU and optional CUDA/mem-saver measurements.

### P2 task order

- [ ] P2-01: Add bounded model-manager/loader discovery with server-side package allowlisting.
- [ ] P2-02: Extend tracked install metadata and merge optional readiness fields into the catalog API.
- [ ] P2-03: Implement short-lived, single-use installation preview confirmations and blocker policy.
- [ ] P2-04: Implement destination-locked staged download, size/checksum verification, no-overwrite atomic promotion, cleanup, and catalog refresh.
- [ ] P2-05: Register installation as an existing-style job with phase/byte progress and safe cancellation.
- [ ] P2-06: Add preview/confirmation, progress/cancellation, warning, and rescue states to the Models tab.
- [ ] P2-07: Expose bounded GGUF inspection and link readiness remedies to existing lifecycle/profile/variant controls.
- [ ] P2-08: Add structured factual prompt types and policy versioning.
- [ ] P2-09: Add prompt linter, exact preview, and advanced escape hatch.
- [ ] P2-10: Extend manifest/sidecar provenance without watermark assumptions.
- [ ] P2-11: Update setup, API, model, installation, lifecycle, and prompt documentation.
- [ ] P2-12: Run command-injection, traversal, disk, partial-download, licence, concurrency, and confirmation-replay security tests.
- [ ] P2-13: Run browser acceptance for install preview/confirm, progress/cancel, GGUF inspection, lifecycle links, and error remedies.
- [ ] P2-14: Run compatibility and full verification suites.

## Verification commands

Implementation should keep the normal repository checks green:

```powershell
gofmt -l .\cmd .\internal
go test ./... -count=1
go vet ./...
go run .\cmd\cpp-studio --config .\config.ci.json --check
go run .\cmd\cpp-studio --config .\config.smoke.json --check
.\scripts\test-benchmark-story-local.ps1
.\scripts\test-benchmark-dramabox-audiobook.ps1
.\scripts\smoke-demo-ui.ps1
.\scripts\verify.ps1
```

Real-model commands belong in a separate opt-in acceptance invocation and must not be
part of CI.

## Rollout and rollback

1. Ship all schema/API additions as backward-compatible and optional.
2. Keep `audio` selected by default.
3. Keep DramaBox disabled unless its engine is configured and healthy.
4. Mark benchmark and verification state separately from engine health.
5. Roll out P0 before exposing P1 controls; roll out P1 evidence before P2 guided setup.
6. Rollback removes or disables the optional DramaBox engine. Existing finished WAVs
   and legacy manifests remain playable.
7. Never delete WIP during rollback automatically; provide explicit discard or a
   documented manual archive path.

Stage completion labels:

- `PLANNED`
- `CODE_COMPLETE_RUNTIME_UNVERIFIED`
- `RUNTIME_VERIFIED_LISTENING_PENDING`
- `ACCEPTED`

## NOT in scope

- Replacing the fast narrator as default.
- Hosted ElevenLabs, Resemble, OpenAI, or other paid TTS APIs.
- Silent, background, prefetch, or browser-parameterized model downloads; every
  in-product install requires a fresh preview and explicit confirmation.
- Overwriting, upgrading, deleting, or relocating an existing model in v1.
- Embedding the upstream Python DramaBox runtime beside audio.cpp.
- RE-USE denoising by default.
- LoRA training or dataset preparation.
- RF64 or multi-file chapter export in P0-P2; RIFF overflow receives an actionable
  split-book error.
- Autonomous rewriting of source text to improve pronunciation.
- Treating ASR comparison as proof of truth or human listening quality.
- Per-section theatrical direction in the first structured prompt release.
- Publishing or claiming a Perth watermark without native evidence.
- Remote access, accounts, cloud synchronization, or telemetry.

## Completion summary

| Stage | User-visible result | Primary risk retired | Runtime dependency | Definition of done |
|---|---|---|---|---|
| P0 | Resume-safe long-form book with verification status | Lost compute, RAM blow-up, silent text drift | audio.cpp 0.5 for real proof; Whisper optional/required by mode | Fixture suite green plus real run when assets exist |
| P1 | Reproducible repair, suitable clone warnings, runtime projection | Uncontrolled retries, bad references, unknown time/VRAM | Authorized voice for clone case; metrics tools | Harness self-tests plus measured local profile |
| P2 | Honest readiness/GGUF inspection, confirmed installation, lifecycle remedies, and exact factual prompt preview | Unsafe acquisition, setup ambiguity, and spoken directions | model manager/loader discovery optional; complete allowlisted catalog metadata for install | Catalog degrades cleanly; install is explicit/tracked/verified; browser and security acceptance pass; prompt preview is exact |

The plan is intentionally front-loaded toward preserving time and source integrity. If
implementation stops after P0, the app is already materially safer and more useful. P1
and P2 add control and discoverability without weakening that foundation.
