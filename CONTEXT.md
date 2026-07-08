# cpp-studio domain glossary

Terms used consistently in code, docs, and architecture discussions. Each
entry names the module that owns the concept.

## Gateway

The local HTTP server (`internal/gateway`) exposing OpenAI-shaped routes for
the native `*.cpp` inference family. HTTP handlers are thin: they validate
requests, cross the engine seam, and shape responses. Orchestration lives
behind that seam, not in handlers.

## Engine

A native inference program (llama.cpp, whisper.cpp, audio.cpp,
stable-diffusion.cpp) named in the config (`llama`, `whisper`, `audio`, `sd`).
Two modes: `server` engines are long-lived daemons managed by the Lifecycle
Manager; `subprocess` engines are launched once per request.

## Engine invocation (`internal/engine`)

The single home for "how do you call engine X": per-engine CLI flags, temp
file staging, output validation, reservation, and success/failure recording,
all behind `Invoker.Run(ctx, Spec)`. Specs are built only by the constructors
(`SpeechSpec`, `TranscriptionSpec`, `ImageSpec`) so the CLI contract never
leaks into handlers. Two adapters sit at this seam: `Runner` (subprocess, for
production) and `Fake` (in-memory, for tests).

## Reservation

The single-run-at-a-time guarantee per engine, owned by the engine invocation
module (`Invoker.Reserve`). Both direct routes and the Story pipeline reserve
through the same slot, so a story synthesis and a direct speech request
cannot race on `audio`.

## Lifecycle Manager (`internal/lifecycle`)

Owns engine process state: start/stop, health polling, log capture, crash
classification, and the `/health` rollup. It knows nothing about CLI
invocation flags — that is the engine invocation module's job.

## Voice Loop (`internal/voice`)

The flagship pipeline: transcription -> chat -> speech as one server-side
unit behind `POST /v1/voice`. Takes the engine seam and a `ChatFunc` as
injected dependencies, so the whole loop is testable without native binaries.
The browser demo only records, uploads, and plays.

## Story (`internal/story`)

The staged factual-story pipeline behind `/v1/stories`: source notes → fact
cards → grounded script → synthesized audio, persisted atomically by the
story Store. Runs as a single-active-job state machine with injectable clock,
stage delay, and engine reservation.

## WAV knowledge (`internal/wav`)

The single home for the WAV format invariant: `Validate*` (RIFF/WAVE header)
and `SyntheticTone` (the deterministic square-wave fixture audio). Gateway,
story, and the fixture binary all cross this one interface; only the browser
mic encoder (app.js) keeps its own writer, because it encodes live PCM
client-side.

## Fixture engine (`cmd/cpp-studio-fixture`)

The deterministic stand-in adapter for all four engine kinds, used by smoke
scripts and CI configs. It fills the same slot as a native engine at the
config seam.

## Config acceptability (`internal/config`)

`Load` answers the portable question (shape, ranges, modes, URLs);
`LoadChecked` adds the machine-local question (does every engine command
resolve to an executable, probe injectable via `CheckCommands`). CI validates
with `Load` semantics; the gateway binary always runs `LoadChecked`.
