# cpp-studio roadmap: from personal studio to shareable local AI studio

## Vision

cpp-studio is a **fully local, private, GPU-native AI studio**: talk to it, clone and
design voices, narrate documents, describe images, and generate art — entirely
offline on your own machine, no cloud. The gateway fronts native `*.cpp` engines
(llama, whisper, audio, stable-diffusion) behind one OpenAI-style API and a
browser console.

Two goals drive this roadmap:

- **Better base** — a foundation stable and general enough to build on for years.
- **Shareable** — a stranger can clone the repo, run it, and *get it* in ten seconds.
  (This is what earns GitHub stars — not an installer.)

Every milestone is tagged with which goal it serves.

## Architecture principles

- **Config is declarative and portable.** No absolute machine paths in tracked
  files. Models are referenced through a manifest, not hardcoded.
- **The engine seam stays.** `internal/engine.Invoker` remains the boundary the
  gateway crosses; new capabilities cross it or proxy to resident servers.
- **The lifecycle manager owns processes.** Start/stop/reload and VRAM profiles
  extend it rather than bypass it.
- **Async work is a first-class Job.** Anything longer than a request/response
  (audiobooks, batches) is a Job with progress, cancel, resume, and a persisted
  result in the Library.
- **The UI is a thin shell over the API.** Vanilla JS behind `go:embed`; every
  capability lives in a tab and is driven entirely by the HTTP API.

## Milestones

### M1 — UI shell + Model manifest + config portability  ·  *stars + base*

The keystone. Makes the app coherent to look at and, for the first time,
runnable by someone who isn't the author.

- **Tabs/router.** Group the existing `<section class="module">` blocks into
  tabs: **Talk** (voice loop + transcription), **Voices** (clone + design +
  library), **Image** (generate + describe), **Story**, **Models**, **Engines**
  (the engine rack, grows in M2). Session log becomes a persistent drawer.
  Hash-based routing (`#talk`, `#models`), no framework.
- **Model manifest.** A tracked `models.json` registry: for each model — `id`,
  `engine`, `family`, relative `path`, `bytes`, `sha256`, `source` URL,
  `license`, and derived `status` (present / missing / corrupt / unverified).
- **Config portability.** Config resolves paths relative to a `root` (or env),
  so tracked `config.example.json` actually works. `config.real.json` stays the
  local override. A stranger edits one root path, not fifteen.
- **Models tab + API.** `GET /v1/models/catalog` returns the manifest with live
  status; a Models tab lists each model, its size/state, and (later) re-download.
- **Acceptance:** `verify.ps1` green; a fresh checkout + example config +
  documented model fetch runs the fixture and real gateways; the console renders
  as tabs; the Models tab shows real present/missing status.

### M2 — Engine control + VRAM profiles  ·  *base + stars*

Turns the lifecycle manager into user-facing control and fixes the documented
16 GB contention (warm-everything degrades voice to 2–3 s and has caused
transient crashes — see `LOCAL_ENGINE_PROFILE.md`).

- **Per-engine control API.** `POST /v1/engines/{name}/{start|stop|reload}` over
  the lifecycle manager; the Engines tab gets power switches + live status/VRAM.
- **Profiles.** Named sets — *Audiobook* (llama + TTS), *Art* (SD), *Chat*
  (llama + whisper + TTS), *Everything* — that load/unload engines to fit VRAM.
  `POST /v1/engines/profile/{name}` applies one.
- **Acceptance:** switching profiles measurably changes resident VRAM
  (nvidia-smi); stopping an engine frees it; the manager stays authoritative;
  tests via the fixture engine.

### M3 — Async job system + Studio Library  ·  *base + enables the payoff*

Generalize the Story pipeline's `story_status` into a real Job abstraction and
persist outputs, so long/async work (audiobooks, batches) has a home.

- **Jobs.** A `Job{ id, kind, status, progress, createdAt, error, resultRef }`
  with `GET /v1/jobs`, `GET /v1/jobs/{id}` (progress), `POST /v1/jobs/{id}/cancel`.
  Story generation migrates onto it as the first consumer.
- **Library.** Outputs (audio, images, stories, cloned voices) persist to a
  tracked-schema on-disk library with metadata; `GET /v1/library` lists them; a
  Library view (drawer or tab) browses/plays/downloads.
- **Acceptance:** a story runs as a Job with live progress and lands in the
  Library; cancel works; restart preserves the Library; tests via fixture.

### M4 — Audiobook (single narrator + document ingest) + BYO model  ·  *payoff*

The magical outcome, riding on M1–M3.

- **Single-narrator Story mode.** One speaker, one source — straight narration
  rather than a multi-voice cast (a simplification of existing cast logic).
- **Document ingest.** Upload `.txt`/`.md` (trivial) or `.epub` (spine-ordered
  XHTML via stdlib zip+xml) → extract text → chunk → TTS per chunk → stitch via
  existing `wav.Concatenate`, all as a Job with chunk progress. **PDF is
  deferred**: honest extraction needs a third-party library in a deliberately
  stdlib-only module; uploads are rejected with a pointer to export as text.
- **BYO model (the achievable slice).** The manifest + config vars + engine
  reload make swapping a chat/whisper/SD model a documented three-step recipe
  (see CONFIG.md "Bring Your Own Model"). A picker UI that rewrites config is
  future work. (TTS/voice-design swap stays out of scope — those engines' CLI
  contracts are tightly coupled.)
- **Acceptance:** a multi-page document becomes a stitched audiobook in a chosen
  voice, as a cancellable Job in the Library; a user-supplied GGUF chat model
  loads and serves.

### Cross-cutting — repo presentation  ·  *stars*

- **README** rebuilt around the vision, with a hero screenshot/GIF, honest
  hardware requirements, and a clear setup path (clone → fetch models → run).
- **CI badge.** A GitHub Action running the GPU-free `go test ./...` (fixture
  engine, `config.ci.json`) — a free quality signal.

## Sequence & rationale

`M1 → M2 → M3 → M4`, with README/CI folded into M1 (initial) and M4 (polish).
Tabs make it coherent; the manifest makes it runnable by others; VRAM control
removes the contention tax; the job system + library are the spine the audiobook
needs; the audiobook is the payoff that proves the foundation.
