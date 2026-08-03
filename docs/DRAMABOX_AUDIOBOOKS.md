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
