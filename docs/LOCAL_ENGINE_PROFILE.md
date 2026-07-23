# Local Engine Feasibility Profile

ENG-T0 measured the real local planner and narrator before schema-v2 and
checkpoint boundaries are frozen.

## Decision

- Synthesis unit: chapter_batch_session.
- Resource policy: exclusive_restart.
- Initial fixed-voice target: real-time factor (RTF) <= 2.0 for a chapter batch.
- CPU planner: supported as an escape hatch, not the default.

The story pipeline should submit all lines for one chapter to one audio.cpp
offline batch session. This loads the TTS model once, retains individual WAV
outputs for line-level evidence/playback metadata, and publishes the chapter
checkpoint only after every batch output validates.

Before synthesis, persist the validated plan and stop the GPU llama server.
Restart llama when another planning operation needs it. Do not select
coexist merely because one request succeeded: the measured headroom was
below the 1 GiB safety threshold.

## Measured Environment

- Date: 2026-07-10.
- Run ID: 3a9e27d9c4ac4dfdba1d7bb71f8e9175.
- OS: Windows NT 10.0.26100.
- PowerShell: 7.6.3.
- GPU: NVIDIA GeForce RTX 5080, 16,303 MiB reported.
- GPU memory at final-run start: 8,951 MiB used, 7,352 MiB free.
- Planner: Qwen3 4B Instruct Q4_K_M through the local llama-server CUDA build.
- Narrator: Qwen3 TTS 0.6B Base through audio.cpp CUDA.
- Workload: the checked-in 10-line, 171-word factual chapter excerpt at
  testdata/benchmark/chapter-lines.txt.
- Repetitions: one full matrix pass after the expanded deterministic fake-engine suite.

An unrelated GPU workload remained active throughout the run. Nothing outside
the benchmark-owned llama/audio processes was stopped. This makes the result a
useful high-pressure safety test, but not a clean per-process VRAM attribution:
Windows WDDM exposed total GPU use rather than reliable per-process memory.

## Results

| Case | Wall seconds | Audio seconds | RTF | Peak GPU MiB |
|---|---:|---:|---:|---:|
| Current per-line subprocesses (10 processes) | 174.507 | 83.280 | 2.096 | 15,713 |
| Persistent offline batch (1 process, 10 lines) | 88.159 | 83.440 | 1.057 | 15,713 |
| Paragraph subprocess | 28.985 | 20.400 | 1.421 | 15,626 |
| Chapter subprocess | 27.703 | 20.400 | 1.358 | 15,693 |
| GPU planner cold start | 17.611 | - | - | 15,688 |
| GPU planner chat | 0.343 | - | - | 15,688 |
| GPU planner restart plus sentinel proof | 2.348 | - | - | unavailable |
| Resident GPU planner plus selected 10-output batch plus sentinel proof | 83.156 | 80.640 | 1.031 | 15,809 |
| CPU planner cold start | 14.643 | - | - | unavailable |
| CPU planner chat | 0.171 | - | - | unavailable |
| CPU planner plus selected 10-output batch plus sentinel proof | 111.982 | 80.960 | 1.383 | unavailable |

The persistent batch reduced wall time by about 49.5% versus launching one
audio.cpp process per line for a comparable amount of speech. The installed
audio.cpp CLI exposes this mode through --batch-text-file, --out-dir, and
--batch-manifest-out.

The monolithic paragraph and tripled chapter probes both stopped at exactly
20.4 seconds of output. They remain comparison evidence only and are not
eligible synthesis fallbacks: they do not prove full input coverage. The safe
fallback is per-line synthesis because it preserves one validated WAV per
input line.

GPU planner/audio coexistence completed using the same selected 10-output
persistent batch, met the RTF gate at 1.031, and the planner passed a second
sentinel chat afterward. Peak use still left only 494 MiB, below the 1 GiB
safety margin. Coexist works, but it is too fragile to make the default.

CPU planning worked only after the benchmark forced all of:

    -ngl 0 --device none --no-op-offload --no-kv-offload

CPU planning avoids planner VRAM residency. Its selected-batch coexistence RTF
was 1.383, slower than the GPU-planner coexistence result, and it cannot protect
audio synthesis from unrelated GPU pressure. It remains a troubleshooting
escape hatch rather than the default.

## Architecture Consequences

1. Schema v2 models chapters and line outputs, but synthesis orchestration
   launches one batch process per chapter rather than one process per line.
2. A chapter checkpoint is atomic: batch manifest plus all validated line WAVs.
3. The resource coordinator needs controlled per-engine stop/start so planning
   can checkpoint, release llama VRAM, synthesize, then restart llama later.
4. coexist stays an opt-in policy requiring a clean rerun with at least
   1 GiB measured headroom.
5. planner_cpu uses the full CPU-only argument set above, not -ngl 0 alone.
6. The first performance acceptance gate is RTF <= 2.0 for fixed-voice chapter
   batching on James's machine. The selected batch measured 1.057; per-line
   synthesis missed the gate at 2.096 and took ten model-loading processes and
   174.507 wall seconds.

## Reproduce

Inspect the matrix without starting engines:

    .\scripts\benchmark-story-local.ps1 -PlanOnly

Run the deterministic fake-engine contract test:

    .\scripts\test-benchmark-story-local.ps1

Run the real local matrix:

    .\scripts\benchmark-story-local.ps1 -AudioConfig .\config.audio-local.example.json -CombinedConfig .\config.real.json -OutDir .\out\local-engine-benchmark

The output directory is ignored by git and contains
benchmark.json, benchmark.md, batch manifests, and generated WAV evidence.
The script only stops planner processes it started, refuses to reuse an
occupied planner port, and rejects concurrent runs sharing one output
directory. A .benchmark-running marker makes interrupted or mismatched result
publication explicit.
