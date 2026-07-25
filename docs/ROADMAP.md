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

### M5 — The Extractor  ·  *payoff + the diarization foundation*

A sampler deck for voices: submit audio/video, find the voice you want, mark
it, extract it, clone it. Closes the loop on the studio's strongest feature —
today, getting 5–15 s of clean reference speech means external tools.

- **Client-side core.** Browsers natively decode MP3/OGG/FLAC/WAV/MP4-AAC via
  Web Audio; the app already builds WAVs in JS. Drop a file → waveform canvas
  → scrub, zoom, drag a region → play it → slice PCM → WAV clip. Soft cap
  ~30 min (decoded PCM memory); MPEG-1/2 and CDs are "convert first".
- **Whisper transcript timeline (the soul of the tool).** Chunked 16 kHz
  upload → resident whisper-server `verbose_json` → timestamped segments
  beside the waveform. Read to find the moment, click a line to seek, search
  the text. *Verified: whisper-server returns `segments:[{start,end,text}]`.*
- **Speaker-first data model.** Every transcript segment carries a `speaker`
  field from day one. v1 fills it by hand: click-to-audition + one-key
  tagging (humans identify voices instantly; the tool makes the loop tight),
  filter to a tagged speaker, extract their cleanest runs. The same field is
  where automatic diarization lands later — same UI, less work.
- **Clip destinations.** "Use as clone reference" feeds the existing clone
  flow (which already whisper-transcribes references); "Save to library"
  uses the existing library. Clips carry provenance metadata (source name,
  time range, speaker tag).
- **Deferred to v3:** a config-gated `yt-dlp` importer (user-supplied binary,
  hidden when absent) for URL submission; ffmpeg importer for long/exotic
  files. A consent note about cloning real voices ships in the README with
  this milestone.

### M6 — Speaker diarization  ·  *shipped*

Automatic "who spoke when": the optional, config-gated `diarize` engine —
**sherpa-onnx offline speaker diarization** (native C++, CPU-only, from
k2-fsa, the OmniVoice team) with the pyannote segmentation 3.0 model and an
English speaker-embedding model. `POST /v1/audio/diarization` returns
anonymous clusters labelled A/B/C…; the Extractor's "Detect speakers" button
(hidden without the engine) fills every transcript line's `speaker` tag by
maximum overlap — exactly the surface M5 built, no rework. Embedding bake-off
on a known two-voice file: **nemo_en_titanet_small** was perfect (clusters
and boundaries exact, re-identified a returning speaker); CAM++_LM fragmented
turns — titanet is the default, CAM++ kept in the manifest for comparison.
Reference numbers: ~1 s per 22 s of audio, 6/6 lines tagged correctly.
pyannote-the-Python-package (HF token, heavy deps) was rejected as the wrong
shape for a native-engine studio.

### M7 — The comedy pipeline  ·  *the destination James named*

The studio's converging purpose: upload an audio comedy, extract and clone
its cast, then write and produce original episodes with those voices.
Sequenced plan; each step starts only when the previous is done.

1. **Cast extraction — DONE.** "Clone the cast" in the Extractor: one click
   after speaker tagging mints a library voice per speaker from their
   longest clean run (≤15 s, skipped under 2 s), named `<source> <speaker>`.
2. **Real-episode shakedown — DONE.** A 1968 Round the Horne episode
   (28:55, laughter, music, character voices): MP3 decoded in-browser, 651
   whisper segments, and the predicted crack appeared — threshold clustering
   exploded into 59 "speakers". The planned lever fixed it: a "number of
   speakers" input on Detect (`?speakers=N` →
   `--clustering.num-clusters=N`, which sherpa prioritises over the
   threshold) yields exactly 5 clusters, 610/651 lines tagged (94%), ~100 s
   per 28 min. Remaining known limits, accepted for now: actors playing
   several characters can split or blend clusters, and laughter-adjacent
   references may need hand-picking over clone-the-cast's auto-pick.
3. **Sketch mode for the Story Desk — DONE.** `"mode": "sketch"` on
   `POST /v1/stories`: premise + style notes + cast roles → banter, with
   the source requirement and the grounding validator both off. One
   resolved field carries the contract — `NormalizedRequest.Mode` decides
   whether sources are required, whether the scaffold builds fact cards,
   which system prompt llama gets, and which validator gates the manifest
   — so grounded mode keeps every guarantee it had (its existing grounding
   tests pass untouched). Sketch lines are still checked for shape
   (speakable text, a speaker in the cast) under a new `invalid_script`
   code, invented fact ids are stripped rather than rejected, and the mode
   is stored in the manifest so a retained sketch says which rules wrote
   it. The Story desk gets a two-option mode switch that swaps sources for
   premise/style; the fixture chat server reads the cast out of the prompt
   so a fixture sketch is performable by any cast.
4. **URL importer — DONE.** `POST /v1/audio/import` runs the optional,
   user-supplied `ytdlp` engine: paste a URL, the audio lands in the
   Extractor. Format selection stays in the config args (site- and
   browser-dependent); the gateway owns the rest of the CLI contract,
   including the `--force-overwrites` that stops yt-dlp treating the
   runner's empty temp file as an already-finished download. Fetched bytes
   are container-sniffed against what Web Audio can decode and streamed
   straight to the Extractor — never stored — with the source title in a
   header. Only http(s) URLs run, so local paths and flag-shaped input are
   refused before yt-dlp sees them. The row is hidden without the engine,
   exactly like "Detect speakers". The README consent note grew a paragraph
   about pasting other people's URLs.

#### M8 — The take room: story production schema v2  ·  *the destination, properly*

Five candidates were drafted against field research (Voicebox at ~45k stars
on the same Qwen3-TTS family; LM Studio/Jan/Ollama winning on click-to-install
model libraries; NeuTTS Air's GGUF CPU cloning) and argued out with an
independent review. What survived:

**Do now — the take room, as a persistence problem.** A story is produced as
one stitched WAV and the per-line clips are thrown away in
`Manager.synthesizeScript`; `Store.Save` refuses to touch an existing story;
`ScriptLine` has no stable id; `wav.Concatenate` knows one uniform gap. So
"regenerate line 7" means regenerating the episode and hoping. That is the
gap between generating audio and producing an episode, and it is a schema
gap, not three buttons:

- Stable `line_id`, and takes persisted at `lines/<line-id>/<take-id>.wav`.
- Per line: the current take, mute, and gap before/after.
- Each take records the voice it was spoken in and when — provenance
  becomes automatic rather than bookkeeping.
- Renders are immutable revisions (`renders/<n>.wav`), so what you
  published stays what you published.
- Retake and re-render are Jobs, with the manifest replaced atomically.

**Deferred, redesigned — model fetching.** The idea was "stream `source`,
check the `sha256` the manifest already has". Both premises are false:
every `source` in `models.json` is a landing page, not a downloadable
object, and the four directory models carry no checksum at all. A fetcher
needs artifact-level descriptors first (resolved URL, revision, per-file
digest, archive member). And weights alone do not unblock a stranger — they
still need engine binaries. The honest version is a versioned reference
pack, not a download button.

**Deferred, narrowed — OpenAI compatibility.** Worth doing as a *tested*
conformance profile, not a claim. Note the premise correction: there is no
`/v1/models` collision (the catalog already lives at `/v1/models/catalog`),
and `handleChatCompletions` already proxies the upstream body, so streaming
is conformance hardening (explicit SSE flushing, header forwarding, tests)
rather than new machinery.

**Cut — NeuTTS Air as a CPU engine.** Its GGUF backbone is real, but the
shipped runtime is a Python package plus `llama-cpp-python`, eSpeak, and a
separate NeuCodec/ONNX path. That is not a native engine behind the existing
seam, and `SpeechVoiceSpec`/`router.speak`/`Manager.Submit` all hardcode a
single `audio` engine with audio.cpp's CLI contract, so it would need
capability routing on top of a new dependency stack. A CPU benchmark spike
can revisit it; it is not a milestone.

**Not adopted — batch synthesis.** ENG-T0 measured a 49.5% wall-time win for
`--batch-text-file` over per-line subprocesses, and that number is stale: the
resident `audiocpp_server` (2026-07-23) already took an 8-line story from
~71 s to ~25 s, "mostly llama scripting". Retaining line WAVs is the part of
that decision still worth having, and M8 takes it. Revisit batching only
against a fresh measurement.

### M9 — What the second review settled

A second round of five candidates, drawn from what related tools do
(podcast mastering to -16 LUFS, delivery/emotion control, music beds,
MP3 delivery), went through the same independent review. The verdict that
mattered was that none of them should come first:

- **Take-room integrity — DONE, and it outranked all five.** The take room
  shipped with the *appearance* of immutable revisioning and not the
  transactional integrity to back it. Two concurrent retakes both computed
  `take-002` and overwrote each other; two renders both claimed one
  revision number; and `stitchTakes` charged a muted line's trailing gap to
  its neighbours, so muting a line changed the timing around it. All three
  are fixed, with tests, plus a render `recipe` — take ids, voices, words
  and timing — so a published revision can still explain itself after the
  script moves on.
- **Edit the line, then retake — DONE.** Ranked first of the five: the take
  room could re-record a line but not change what it says, which left the
  original problem intact one level up. `text` is now patchable, and
  changing it deselects every take recorded against the old words. A
  `stale_take` error refuses to select or render one.
- **ffmpeg export — DONE.** `POST /v1/stories/{id}/export` encodes a render
  revision to MP3 or Opus through the operator's own ffmpeg, config-gated
  in the `ytdlp` mould. Every trap the review named was taken seriously:
  `engine.Spec` gained `InputPath`/`OutputPath` so a transcode never
  round-trips a whole recording through memory; artifact serving now derives
  its content type instead of hardcoding `audio/wav`; and the gateway probes
  `-encoders` once, so `GET /v1/audio/formats` can tell the console what
  this machine will actually produce rather than discovering it mid-job.
  Exports hang off the revision they encode, and re-exporting a format
  replaces it. Measured on a real 43 s two-hander: 2.0 MB WAV → 687 KB MP3
  → 348 KB Opus. **The ffmpeg *importer* (long and exotic files for the
  Extractor, deferred at M5) is still open** — it needs a multipart upload
  path, not the URL-shaped contract the yt-dlp route uses.
- **Mastering — later, and through ffmpeg `loudnorm`, not pure Go.** The
  measurement standard is BS.1770; -16 LUFS is the podcast delivery target,
  not "EBU R128", whose programme target is -23. Two constraints also
  cannot always both hold: if linear gain to -16 would breach the true-peak
  ceiling, the honest outcome is a quieter render and a recorded actual
  value, never a claim that both were met. Level per speaker on aggregate
  material rather than per take, or intentional whispers and shouts get
  flattened.
- **Per-line delivery direction — CUT.** Not reachable: the resident audio
  server takes `{model, input, voice_ref, reference_text}`, `SynthesizeFunc`
  is `(ctx, text, voiceID)`, and `--instruct` belongs to the voice *design*
  engines, not cloned speech. A direction field would be inert metadata
  pretending to be a feature.
- **Resumable production — later, and bigger than it looks.** Story
  synthesis still holds every clip in memory and writes takes only after the
  story is stored, so resume needs a work-in-progress layout first. Line id
  plus text is not a sufficient cache key either: an engine or model change
  between runs would splice an inconsistent episode together silently.
  Audiobooks have no take layout at all, so story resume would not help the
  long-form case that motivates it.

## Cross-cutting — repo presentation  ·  *stars*

- **README — DONE.** Rebuilt around the vision, with honest hardware
  requirements and a clear setup path (clone → fetch models → run). The
  shop window is three stills captured from the real gateway, not a GIF:
  the Extractor mid-job (a real 47 s two-hander, 18 whisper segments, both
  speakers tagged automatically), the sketch desk with a llama-written
  script cast on cloned voices, and the engine rack reading 13.8/15.9 GB on
  the reference RTX 5080. Stills beat a GIF here — a dithered 256-colour
  animation of a dark console loses the text that is the whole point, and
  every frame here is a screen a visitor can reproduce.
- **CI badge.** A GitHub Action running the GPU-free `go test ./...` (fixture
  engine, `config.ci.json`) — a free quality signal.

## Sequence & rationale

`M1 → M2 → M3 → M4`, with README/CI folded into M1 (initial) and M4 (polish).
Tabs make it coherent; the manifest makes it runnable by others; VRAM control
removes the contention tax; the job system + library are the spine the audiobook
needs; the audiobook is the payoff that proves the foundation.
