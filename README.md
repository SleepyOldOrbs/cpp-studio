# cpp-studio

[![ci](https://github.com/SleepyOldOrbs/cpp-studio/actions/workflows/ci.yml/badge.svg)](https://github.com/SleepyOldOrbs/cpp-studio/actions/workflows/ci.yml)

**A fully local, private, GPU-native AI studio.** Talk to it, clone and design
voices, mine voices out of any recording, narrate whole documents into
audiobooks, describe what it sees, and generate art — entirely offline on
your own machine. No cloud, no API keys, no telemetry. One Go gateway fronts
the native inference family —
[llama.cpp](https://github.com/ggml-org/llama.cpp),
[whisper.cpp](https://github.com/ggml-org/whisper.cpp),
[audio.cpp](https://github.com/0xShug0/audio.cpp),
[stable-diffusion.cpp](https://github.com/leejet/stable-diffusion.cpp), and
[sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx) speaker diarization —
behind OpenAI-shaped HTTP routes and a browser studio console.

## What it does

- **Talk** — a multi-turn voice loop: record or type, whisper transcribes,
  llama replies, TTS speaks the answer in ~1.5 s round trips. Live
  transcription re-transcribes your growing take as you speak.
- **Voices** — clone a voice from 5–15 seconds of reference audio, or design
  one from a written description across three voice-design engines
  (VoxCPM2, OmniVoice, Qwen3 TTS), audition it, and keep it in a library.
- **Audiobook** — upload a `.txt`, `.md`, or `.epub`; it is chunked on
  sentence boundaries, narrated chunk by chunk in any library voice, and
  stitched into one WAV, with live progress and cancellation.
- **Story desk** — two modes, one desk. **Grounded**: paste sources, llama
  writes a fact-cited script for your cast, and no line ships that a source
  does not support. **Sketch**: give it a premise and a style instead, and
  the grounding comes off — the writer invents, and your cloned cast
  performs it. Either way every line is spoken and stitched into one piece.
- **The Extractor** — a sampler deck for voices. Drop in any audio or video
  the browser can decode (MP3/OGG/FLAC/WAV/MP4 — a podcast, an old radio
  show), get a scrubbable waveform with a whisper transcript timeline, and
  let **automatic speaker diarization** tag who says what (a 1968 radio
  episode: 5 speakers, 94% of 651 lines tagged in ~100 s, on CPU). Filter to
  one speaker, tick their lines, and export them as one WAV — or press
  **Clone the cast** and mint a library voice per speaker in one click.
  Transcript lines are editable and mergeable; every clip carries
  source/time/speaker provenance.
- **Image lab** — Stable Diffusion generation (~2 s per 512×512 resident),
  plus true vision: a VLM describes any image and speaks the description.
- **Engine rack** — every engine has a power switch, and named VRAM profiles
  (chat / art / audiobook / everything) trade resident models against your
  card's budget with one click, with a live VRAM meter.
- **Library** — outputs you keep persist on disk with metadata; async work
  (stories, audiobooks) runs as cancellable jobs with progress.

## Requirements

- Windows (primary; the gateway and tests are cross-platform Go, the engine
  scripts and verified setup are Windows), Go 1.26+.
- An NVIDIA GPU for the full experience — the reference machine is an RTX
  5080 (16 GB). Engines also run on CPU builds, just slower.
- Native engine binaries and model weights are **not** bundled: you bring
  llama.cpp, whisper.cpp, audio.cpp, and stable-diffusion.cpp builds plus
  models (~15 GB for the full reference setup). `models.json` declares every
  model with its source; the Models tab shows what is present or missing.

## Quick start

```powershell
git clone https://github.com/SleepyOldOrbs/cpp-studio.git
cd cpp-studio
go test ./...
```

Prove the whole pipeline with zero native binaries — the fixture engine
fakes every engine deterministically:

```powershell
.\scripts\smoke-demo-ui.ps1
```

Then wire real engines: copy `config.example.json`, set the single `root`
var to where your engines and models live, and check it:

```powershell
go run .\cmd\cpp-studio --config .\my-config.json --check
go run .\cmd\cpp-studio --config .\my-config.json
```

Open the studio console at `http://127.0.0.1:8765/demo/`. Config vars,
engine modes, resident servers, VRAM profiles, and the bring-your-own-model
recipe are documented in [`docs/CONFIG.md`](docs/CONFIG.md).

## API

OpenAI-shaped where a shape exists, plain JSON where it doesn't. Highlights
(full reference in [`docs/API.md`](docs/API.md)):

| Route | What it does |
| --- | --- |
| `POST /v1/chat/completions` | proxy to the resident llama-server |
| `POST /v1/audio/transcriptions` | whisper transcription (resident or per-run) |
| `POST /v1/audio/speech` | TTS, default or any stored voice |
| `POST /v1/voice` | the whole voice loop in one call |
| `POST /v1/voices` · `/v1/voices/design` | clone / design voices |
| `POST /v1/audio/transcriptions?format=segments` | timestamped transcript segments |
| `POST /v1/audio/diarization` | who-spoke-when speaker clusters (sherpa-onnx) |
| `POST /v1/images/generations` | Stable Diffusion, resident sd-server |
| `POST /v1/images/descriptions` | VLM describes an image, spoken aloud |
| `POST /v1/stories` | multi-voice story jobs, grounded or sketch |
| `POST /v1/audiobooks` | document → single-narrator audiobook job |
| `GET /v1/jobs` · `POST /v1/jobs/{id}/cancel` | every async job, one surface |
| `GET /v1/library` | persistent saved outputs |
| `POST /v1/engines/{name}/{start,stop,reload}` · `/v1/engines/profiles/{name}` | engine power + VRAM profiles |
| `GET /health` · `GET /v1/models/catalog` · `GET /v1/gpu` | health, model states, VRAM |

## Verification

```powershell
.\scripts\verify.ps1
```

gofmt, `go test ./...`, vet, config checks, and the deterministic
browser-demo smoke (voice loop, cloning, design, image, story) in one call.
Individual fixture smokes: `smoke-voice-loop-fixture.ps1`,
`smoke-story-fixture.ps1`, `smoke-demo-ui.ps1`. Measured local-engine
latencies and policies: [`docs/LOCAL_ENGINE_PROFILE.md`](docs/LOCAL_ENGINE_PROFILE.md).

Release packaging (`dist/`, no weights bundled): `.\scripts\package-release.ps1`
— see [`docs/RELEASE.md`](docs/RELEASE.md).

## A note on cloning voices

The voice tools can reproduce a real person's voice from a few seconds of
audio. That power is the point — and it comes with an obvious
responsibility: **clone only voices you have the right to use** (your own,
voices with the speaker's consent, or material whose licence permits it),
and never present synthetic speech as a real person's words. Everything
runs locally; what you make and how you use it is on you.

## Architecture in one paragraph

The gateway loads one JSON config describing engines as either resident
servers (started, health-checked, and proxied — llama, whisper, TTS, SD) or
per-request subprocesses (voice design). A lifecycle manager owns every
process; an engine-invocation seam owns CLI contracts, temp files, and
validation; GPU-heavy runs are serialized so they never race for VRAM.
Async pipelines (stories, audiobooks) register with a job registry and
persist artifacts to on-disk stores. The console is vanilla JS embedded in
the binary — rebuild to change it. Design notes live in
[`docs/ROADMAP.md`](docs/ROADMAP.md) and [`CONTEXT.md`](CONTEXT.md).
