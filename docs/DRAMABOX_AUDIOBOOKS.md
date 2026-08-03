# Durable DramaBox audiobooks

DramaBox is an optional, experimental English narrator from audio.cpp
`release-0.5`. It does not replace the fast `audio` narrator. The official Q8 model
is 18,942,803,808 bytes, so the supplied server example uses CPU and lazy loading;
real CPU speed and reference RTX 5080 CUDA fit remain unverified until measured.

## Configure

1. Review the upstream model card and LTX-2 Community License.
2. Put `DramaBox-GGUF/dramabox-q8_0.gguf` under `audio.cpp/models`.
3. Copy `config.dramabox-local.example.json`, set its `root`, and keep
   `dramabox-server.example.json` beside it.
4. Optionally merge your working `whisper` engine configuration for factual
   verification.
5. Run `go run .\cmd\cpp-studio --config .\your-config.json --check` before start.

The browser enables DramaBox only when `/health` contains that engine. Removing the
engine is the rollback; existing finished WAVs and manifests remain readable.

## Create and recover

Resolve the effective request in the Audiobook desk before Narrate. The preview shows
the exact engine/model/voice identities, pinned defaults, transport mapping, and
verification mode without creating work. The advanced JSON editor accepts only the
same typed allowlist as the curated controls and never accepts a seed, path, command,
URL, token, or arbitrary audio.cpp argument.

`POST /v1/audiobooks` returns only after the canonical source, full identity, all
section ranges, options, and cryptographically random section seeds are atomically
published under `out/audiobooks/.book_....wip`. Synthesis checkpoints every section.

- **Resume** keeps the same ID and seeds, and reuses only WAVs whose source range,
  text hash, checkpoint, selected attempt, WAV format, and audio hash all agree. A
  changed model, voice, direction, option set, or policy disables Resume.
- **Restart** reads the hash-valid stored source and creates a separate ID under the
  current identity. It never deletes or mutates the original interrupted production.
- **Discard** is the only action that deletes an interrupted WIP.

The jobs registry is live progress only. After a process restart, recoverable work is
discovered from its manifest and appears in the Audiobook desk without a job entry.

## Repair attempts and render revisions

Finished durable productions retain immutable per-section attempts. Reproduce uses
the selected attempt's requested seed; Variation allocates a new server-side random
seed. The request cannot supply a seed or path:

```text
POST /v1/audiobooks/{id}/sections/{sectionId}/retry
{"mode":"reproduce|variation"}
```

Retries remain unselected. An explicit selection publishes a new full-book render
without overwriting `book.wav` or any prior revision:

```text
POST /v1/audiobooks/{id}/sections/{sectionId}/attempts/{attemptId}/select
```

The manifest records requested and reported actual seeds, hashes, options, synthesis
and verification timings, duration, verification evidence, parent lineage, selection,
and the exact attempt map used by every render revision. A reported seed mismatch
fails the attempt; unavailable actual-seed reporting remains labelled `requested`.

Stored voice references retain one-time PCM analysis in their voice manifest. The
Audiobook picker shows and can play the exact stored WAV, its id and SHA-256,
provenance, duration, sample rate, channels, bit depth, signal fitness, and warnings;
Clear reference returns to text-only/default narration. Older voice manifests are
analyzed lazily without changing the reference. The usable-speech value is explicitly
the `pcm-heuristic-v1` low-energy estimate unless optional VAD evidence says otherwise.
When the active server-mode Whisper configuration contains its fixed `--vad` option,
the timestamped speech spans replace only the usable-duration estimate and the method
is stored as `configured-vad+pcm-v1`. Missing VAD is `not-configured`; failure remains
a visible warning and safely retains the PCM estimate.

DramaBox cloning requires at least 10.0 seconds of usable speech from that stored
analysis. Short or unsupported references are disabled only for DramaBox and produce
the same bounded server-side rejection if submitted directly; the fast narrator and
other voice consumers remain available. Text-only DramaBox is always an option.

## Verification states

- `auto`: use configured Whisper; finish as `unavailable` if it is absent or fails.
- `required`: refuse creation when Whisper is absent; retain WIP on an ASR failure.
- `off`: record `skipped`.

Each successful comparison retains the raw transcript, word edit counts/WER, and
separate missing numeric/date, acronym, and likely proper-name warnings. `Verified`
means the configured heuristic passed; `Differences found` requires review; `Not
verified` is never presented as proof. ASR can itself be wrong, so listen to the
finished joins, voice, delivery, and any flagged sections.

## Assembly and limits

App-level section WAVs are assembled from disk with a 50 ms equal-power crossfade
and 300 ms lead/trail padding. All sections must share one 16-bit PCM format. The
assembler validates the finished file before atomic publication and refuses the
32-bit RIFF limit with a `narrate in parts` remedy; RF64 is intentionally unsupported.

Fixture verification proves orchestration without the 18.9 GB model:

```powershell
.\scripts\verify.ps1
```

This does not prove real-model quality, peak VRAM, or real-time factor. Keep those
claims runtime-unverified until P0-14 real acceptance succeeds.

Plan or run the dedicated evidence harness with:

```powershell
.\scripts\benchmark-dramabox-audiobook.ps1 -PlanOnly
.\scripts\test-benchmark-dramabox-audiobook.ps1
```

The plan covers cold/warm text, native long form, conditional authorized cloning,
fresh mem-saver modes, CPU, explicitly requested CUDA, cancel/restart recovery, and
ASR fidelity. Real runs use the tracked benchmark API/job and retain the full metric
schema; they accept loopback gateways only. Labels are descriptive (`interactive`,
`batch-usable`, `overnight`, or `failed`) and never certify subjective quality.

The harness calls `POST /v1/audiobooks/benchmark`, follows the returned normal job,
and reads `GET /v1/audiobooks/benchmark/results/{id}`. The result directory retains
checkpointed JSON and case WAVs under `out/audiobook-benchmarks`; the collection is
available from `GET /v1/audiobooks/benchmark/results` after a restart. Each result
fingerprints the exact engine/model, authorized voice and reference hash, effective
options, requested backend, and canonical fixture. A current mismatch is returned as
`identityChanged`, so old performance is never silently projected onto new assets.
CPU/GPU memory fields remain absent when the platform cannot measure them, and the
fresh-server mem-saver cases remain `profile-required` until run in those profiles.

The Audiobook desk loads the newest matching benchmark for the selected voice,
direction, and effective options. It shows cold/warm RTF, a measured 10,000-word
projection, available VRAM evidence, fidelity state, date, and staleness. Overnight is
an informed choice rather than a block; a matching result that failed to load or
produce valid audio blocks Narrate until configuration changes and a new run succeeds.

Finished DramaBox cards expose every immutable section attempt. Reproduce, new-seed
Variation, per-attempt requested/actual seed state and timings, audition playback, and
explicit Select are available together. Select publishes a new render revision and
the card loads its new artifact without overwriting earlier audio. Live work reports
total/phase elapsed time plus measured completed-section synthesis and verification;
it does not invent native progress precision the runtime did not return.
