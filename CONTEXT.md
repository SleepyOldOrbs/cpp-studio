# cpp-studio domain glossary

Terms used consistently in code, docs, and architecture discussions. Each
entry names the module that owns the concept.

## Gateway

The local HTTP server (`internal/gateway`) exposing OpenAI-shaped routes for
the native `*.cpp` inference family. HTTP handlers validate envelopes, call
the owning product module or Engine invocation for direct routes, and shape
responses; orchestration never lives in handlers.

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

## BYOM variant (`internal/lifecycle`)

A variant synthesized from a file rather than declared in config: each
`*.gguf` in an engine's `byomDir` lists as `byom:<filename>`, launched
through the engine's `byomArgs` template (`{model}` → the file's path).
The lifecycle manager owns synthesis, the traversal guard, and remedy
extra-args; it stays ignorant of what the flags mean.

## Fit preflight (`internal/gateway`)

The judgement attached to byom variants in the listing: file size against
live nvidia-smi free VRAM (plus what the running model will free), giving
`fits` / `tight` / `too_big` / `no_gpu_info`, and offering server-defined
**remedies** (today only `cpu-moe`, for over-VRAM MoE models). Policy and
llama-specific flag knowledge live here, not in lifecycle.

## GGUF reader (`internal/gguf`)

Reads just enough of a GGUF header to answer the preflight's questions —
architecture, expert count, size label. Stops before the tokenizer
section; any parse trouble degrades the fit check to size-only, never an
error surfaced to the user.

## Voice Loop (`internal/voice`)

The flagship pipeline: transcription -> chat -> speech as one server-side
unit behind `POST /v1/voice`. Takes the engine seam and a `ChatFunc` as
injected dependencies, so the whole loop is testable without native binaries.
The browser demo only records, uploads, and plays.

The Voice Library uses two related terms. An **Actor Voice** is an existing
recorded or designed reusable voice with its own reference WAV and transcript.
A **Character Voice** is a durable child direction beneath one Actor Voice. It
stores identity and performance direction, but never duplicates the parent's
reference WAV. Its optional generated preview is replaceable evaluation data,
not a Library asset or production take.

## Story (`internal/story`)

The staged story pipeline behind `/v1/stories`, persisted atomically by the
story Store. Runs as a single-active-job state machine with injectable clock,
stage delay, and engine reservation.

**Mode** is the contract a story is written under, resolved once in
validation and carried on `NormalizedRequest.Mode` through to the manifest.
*Grounded* (the default) is the factual pipeline: source notes → fact cards →
cited script → audio, where no line ships that a fact card does not support.
*Sketch* is the fiction contract: premise + style + cast roles → script →
audio, with no sources, no fact cards, and no citations — only the shared
script shape (speakable text, a speaker in the cast) still gates it. One
field decides all four differences, so neither mode can weaken the other.

**Mastering** is two linear-gain passes applied while rendering, when a
measurement engine is configured: speakers levelled toward the median on
their aggregate material (never per line — that flattens a performance),
then one gain toward -16 LUFS under a -1.5 dBTP ceiling. ffmpeg measures
(BS.1770), Go applies the gain. The ceiling wins when the two constraints
conflict, and the render says `target_met: false` rather than compressing.

**Exports** are delivery encodings of one render revision (MP3/Opus via the
operator's optional ffmpeg), written beside the revision they encode and
recorded on it. They are derived data: re-exporting a format replaces it,
and a render's exports say nothing about any other revision.

**Takes and renders** are the production layer. A produced line keeps every
recording ever made of it (`lines/<line-id>/<take-id>.wav`), and the manifest
says which take is current, whether the line is muted, and how its timing is
nudged. A *render* is an immutable revision stitched from those choices
(`renders/render-NNN.wav`), with `story.wav` mirroring the latest so existing
readers keep working. Takes and renders only accumulate; the manifest is the
one mutable file, which is what makes an edit visible. Because `Status`
prefers a tracked in-memory job over the store, every take-room mutation must
republish the manifest that job is serving.

## Story Builder Project (`internal/storybuilder`)

A Story Builder Project is one separately saved production owned by the
Story Builder tool. Its manifest has a stable id, user-facing name, typed
Dialogue/SFX/Music tracks, timeline clips, timestamps, and a monotonic revision.
Tracks own stable identity, order, mute state, and optional Character Voice
binding. Silence clips are timing-only metadata and do not own media bytes.
Reusable SFX and Music remain Library-owned until first placement. The project
Store then validates and copies one immutable WAV into the selected project's
`media` directory, records source Library provenance on each clip, and reuses
that copy for later placements. A dialogue build selects stale, failed, and
orphaned building clips in timeline order; treating building clips as retryable
recovers an invocation whose final status could not be saved. The build holds
both the shared `audio` gate and the directed
`omnivoice` Engine Reservation for the build, resolves the current Character
Voice direction into OmniVoice synthesis, and stores each successful result as an immutable project-owned WAV
under `takes`. Each completed take is attached by an atomic manifest replacement
before the next clip starts, so a later failure does not discard earlier work.
Generated take identities and source metadata are server-owned. Clips expose a
derived media error when their project-owned source is missing or unreadable;
browser-provided paths are never part of the contract.
Whole-project writes use the revision as an
optimistic-concurrency boundary: a stale client receives a conflict instead of
silently replacing newer work. Each project lives in its own directory under
`out/story-builder-projects`; atomic manifest replacement and project-scoped
deletion are owned by the project Store.

The separate browser tool at `/demo/story-builder.html` owns project and typed
track arrangement and starts/polls asynchronous dialogue builds. Build
coordination is in memory, while clip status and successful takes are durable in
the project Store. Later Story Builder slices extend this aggregate rather than
creating a second persistence system.

## Timeline Track

An ordered lane in a Story Builder Project, typed as Dialogue, SFX, or Music.
A Dialogue Track belongs to one Character Voice.

## Timeline Clip

A timed piece of dialogue, SFX, music, or silence placed on a Timeline Track.
Clips may overlap across tracks but not within the same track.

## Library

The studio-wide browse, search, and launch surface over durable work. Voices,
Stories, Audiobooks, Story Builder Projects, reusable audio, and exports remain
owned by their purpose-built stores rather than being copied into one store.

## Audiobook production

An Audiobook production is a long-running narration of one immutable source
document into a playable book. It advances through durable sections so
completed work survives interruption, and resume reuses only sections sharing
its synthesis identity.

## Synthesis identity

A Synthesis identity is the immutable combination of source, engine, model,
voice, direction, request options, and production policies that determines
whether generated speech belongs to the same Audiobook production.

## Audiobook lifecycle

Resume continues an interrupted Audiobook production only under its original
Synthesis identity, while Restart creates a separate production from the same
source. Cancel preserves durable work; Discard is the only action that deletes it.

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
