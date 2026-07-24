# API Reference

`cpp-studio` exposes local HTTP routes for the configured native `*.cpp` engines. The routes are intentionally small OpenAI-shaped subsets, not full OpenAI API compatibility.

All JSON errors use this shape:

```json
{ "error": "message" }
```

Wrong HTTP methods return `405` with an `Allow` header.

Story routes intentionally use a richer machine-readable error envelope. See
`docs/STORY_API.md` for the story job contract, limits, job states, artifact
layout, and story-specific errors.

## Engine Names

Routes use fixed engine keys from the config:

- `llama`: chat proxy.
- `whisper`: audio transcription subprocess.
- `audio`: speech synthesis subprocess.
- `sd`: image generation subprocess.

If a required engine is not configured, the route returns `503`.

## GET /health

Returns gateway and engine lifecycle state.

```json
{
  "status": "ready",
  "updatedAt": "2026-07-06T10:00:00Z",
  "engines": {
    "llama": {
      "name": "llama",
      "status": "ready",
      "pid": 1234,
      "ready": true,
      "updatedAt": "2026-07-06T10:00:00Z",
      "lastSuccessAt": "2026-07-06T10:00:00Z",
      "logTail": []
    }
  }
}
```

Top-level `status` is:

- `ready` when all engines are ready (deliberately stopped engines are neutral).
- `starting` when at least one engine is on its way up.
- `degraded` when any engine is failed, exited, missing, crashed, or blocked by a port conflict.

`/health` still returns HTTP `200` when the JSON body status is `degraded`; clients should inspect the response body.

Subprocess engines are marked ready after config validation and are launched per request.

## GET /v1/models/catalog

Returns the model manifest with each model's live on-disk state. Requires a
`models` block in the config; without one the catalog is empty.

```json
{
  "root": "C:\\studio",
  "models": [
    {
      "id": "qwen3-4b-instruct",
      "engine": "llama",
      "family": "llama-gguf",
      "path": "engines/models/Qwen3-4B-Instruct-2507-Q4_K_M.gguf",
      "bytes": 2497281120,
      "absPath": "C:\\studio\\engines\\models\\Qwen3-4B-Instruct-2507-Q4_K_M.gguf",
      "present": true,
      "actualBytes": 2497281120,
      "state": "present"
    }
  ]
}
```

`state` is one of `present` (size — and file count for directory models —
matches the manifest), `missing`, `size-mismatch`, `unverified` (present but
the manifest declares nothing to check), `verified` (deep verification
passed), or `corrupt` (deep verification failed). Directory models report
`actualFiles` alongside `actualBytes`.

## POST /v1/models/verify

Starts a deep integrity check of every model as a tracked job (`kind:
"verify"`): files with a manifest `sha256` are fully hashed; directory models
are exhaustively walked against expected bytes and file count. Returns
`202 {"id":"verify_...","statusUrl":"/v1/jobs/verify_..."}`; `409` while a
run is active. Results overlay the catalog (`verified` / `corrupt`) until the
gateway restarts; the catalog response also carries `"verifying":true` while
a run is in flight. Reads every model once — seconds on NVMe, minutes on
slow disks. Cancellable via the jobs surface.

## Engine Control

Server-mode engines can be started, stopped, and reloaded at runtime; this is
how VRAM is managed on cards that cannot hold every resident model at once.
Subprocess engines hold no VRAM between requests and are not controllable.

- `POST /v1/engines/{name}/start` — start a stopped or crashed engine.
- `POST /v1/engines/{name}/stop` — stop a running engine (frees its VRAM).
- `POST /v1/engines/{name}/reload` — stop then start.

Success returns the full `/health` payload. Unknown engines return `404`,
subprocess engines `400`, and lifecycle failures `409` with an error message.

- `GET /v1/engines/profiles` — the named engine sets from the config's
  `profiles` block: `{"profiles":{"chat":["llama","whisper","audio"]}}`.
- `POST /v1/engines/profiles/{name}` — apply a profile: stop every server
  engine not in the set, then start the members. Returns
  `{"profile":"chat","failures":[],"health":{...}}`; partial failures use
  HTTP `207` and list what went wrong while still applying the rest.

## Jobs

Every async pipeline (today: stories; next: audiobooks) registers with the
job registry, so one surface lists, polls, and cancels long-running work.

- `GET /v1/jobs` — all tracked jobs, newest first:
  `{"jobs":[{"id":"story_...","kind":"story","status":"running","progress":0.7,"detail":"synthesizing","createdAt":"...","updatedAt":"..."}]}`
- `GET /v1/jobs/{id}` — one job; completed jobs carry a `result` map (e.g.
  `{"artifactUrl":"/v1/stories/.../artifact/story.wav","title":"..."}`).
- `POST /v1/jobs/{id}/cancel` — asks the owning pipeline to stop; `409` when
  the job is unknown or already terminal.

Statuses: `queued`, `running`, `complete`, `failed`, `cancelled`. The
registry is in-memory coordination state (finished jobs are capped at 100 and
forgotten on restart); artifacts always persist in their pipeline's store.

## Audiobooks

Single-narrator document narration: upload a document, pick a voice, get one
stitched WAV. Runs as an `audiobook` job on the jobs surface.

- `POST /v1/audiobooks` — multipart form: `file` (.txt, .md, or .epub; 16 MB
  cap; PDFs are rejected with a pointer to export as text), optional `title`
  (defaults to the file name), optional `voice` (a stored voice id; ""
  narrates with the studio default). The document is chunked on paragraph and
  sentence boundaries (~300 chars per spoken chunk, 600-chunk cap). Returns
  `202 {"id":"book_...","chunks":42,"statusUrl":"/v1/jobs/book_..."}`; `409`
  when another narration is running or the audio engine is busy.
- `GET /v1/jobs/{id}` — narration progress (`"narrating chunk 12/42"`), and
  `POST /v1/jobs/{id}/cancel` stops it.
- `GET /v1/audiobooks` — finished narrations, newest first:
  `{"audiobooks":[{"id":"book_...","title":"...","chunks":42,"durationSeconds":312,"artifactUrl":"/v1/audiobooks/book_.../artifact/book.wav"}]}`.
- `GET /v1/audiobooks/{id}/artifact/book.wav` — the narration WAV.

Finished audiobooks persist in `out/audiobooks` and survive restarts.

## Library

The studio's persistent output shelf: audio and images saved from the
console live in `out/library` on disk and survive restarts.

- `GET /v1/library` — all saved items, newest first.
- `POST /v1/library` — save one item:
  `{"kind":"audio"|"image","name":"My take","data_b64":"...","meta":{"voice":"cox"}}`.
  Bytes are validated against the kind (WAV header / PNG signature); the
  artifact cap is 64 MB. Returns `201` with the item record.
- `GET /v1/library/{id}/artifact` — the raw WAV/PNG.
- `DELETE /v1/library/{id}` — remove an item (`204`).

## GET /v1/gpu

Reports GPU memory via `nvidia-smi` when available, so profile effects are
visible: `{"available":true,"gpus":[{"name":"...","totalMiB":16303,"usedMiB":11949}]}`.
Machines without `nvidia-smi` return `{"available":false}`.

## POST /v1/chat/completions

Proxies the request body to the configured `llama` server.

Requirements:

- Engine key: `llama`.
- `llama.healthUrl` must be an absolute URL ending in `/health`.
- The upstream chat URL is inferred by replacing that suffix with `/v1/chat/completions`.

Behavior:

- The gateway forwards the request body as JSON without validating the OpenAI chat schema.
- The upstream status code, body, and content type are passed through.
- Successful 2xx upstream responses mark `llama.lastSuccessAt`.
- Upstream connection failures return `502`.

## POST /v1/audio/transcriptions

Accepts a WAV upload and transcribes it through the configured `whisper`
engine. With `mode: "subprocess"` the gateway runs the command per request;
with `mode: "server"` it posts the WAV to the resident `whisper-server`'s
`/inference` route (inferred from `healthUrl`, like the chat proxy), which
keeps the model loaded between requests and is markedly faster for repeated
calls such as live transcription.

Request:

- `Content-Type: multipart/form-data`.
- Required field: `file`.
- Upload limit: 32 MiB.
- The uploaded file must include a filename.
- The file must have a RIFF/WAVE header.
- Other OpenAI transcription fields are not interpreted by the gateway.

Gateway subprocess arguments (subprocess mode):

```text
<configured whisper args> -f <uploaded-temp-wav>
```

Response:

```json
{
  "text": "transcript text",
  "duration_ms": 1234
}
```

Behavior:

- Engine key: `whisper`.
- Subprocess mode: only one transcription request can run at a time for this gateway process; concurrent requests return `429`. Command stdout becomes `text` after trimming whitespace. Command failure marks `whisper` crashed and returns `502` with bounded stdout/stderr details.
- Server mode: segment newlines in the server's `text` are collapsed to single spaces; upstream failures return `502`.

### Segments format

`POST /v1/audio/transcriptions?format=segments` returns timestamped speech
spans instead of one flat string — the Extractor's transcript timeline:

```json
{
  "text": "Hello there. Second thought.",
  "duration_ms": 640,
  "segments": [
    { "start": 0.0, "end": 2.5, "text": "Hello there.", "speaker": "" },
    { "start": 4.0, "end": 6.0, "text": "Second thought.", "speaker": "" }
  ]
}
```

`speaker` is a first-class field, empty today: the console fills it with
manual tags, and a future diarization engine fills it automatically. Segment
output requires the `whisper` engine in `server` mode (the resident server's
`verbose_json`); subprocess whisper returns `503` for this format.

## POST /v1/audio/speech

Runs the configured `audio` subprocess and returns WAV bytes.

Request JSON limit: 64 KiB.

```json
{
  "input": "Text to speak",
  "voice": "default",
  "format": "wav"
}
```

Supported fields:

- `input` is required and must be non-empty after trimming.
- `format` may be omitted or `wav`.
- `voice` is accepted for shape compatibility but is not interpreted by the gateway.

Gateway subprocess arguments:

```text
<configured audio args> --text <input> --out <generated-temp-wav>
```

Response:

- `Content-Type: audio/wav`.
- Output limit: 32 MiB.
- The generated file must have a RIFF/WAVE header.

Behavior:

- Engine key: `audio`.
- Only one speech request can run at a time for this gateway process; concurrent requests return `429`.
- Command failure or invalid output marks `audio` crashed and returns `502` with bounded stdout/stderr details.

## POST /v1/voice

Runs the whole voice loop server-side: transcription -> chat -> speech in one
request. This is the route the browser demo uses.

Multipart form fields (upload limit 32 MiB):

- `file`: optional WAV recording; must have a RIFF/WAVE header.
- `message`: optional typed text, used as the transcript when no `file` is sent.
- `history`: optional JSON array of prior conversation turns, oldest first:
  `[{"role":"user","text":"..."},{"role":"assistant","text":"..."}]`. Roles
  must be `user` or `assistant`, at most 40 turns, each at most 4000
  characters; invalid history returns `400`. The gateway replays the turns to
  `llama` ahead of the new message so follow-up questions resolve.

At least one of `file` or `message` is required; requests with neither return `400`.

The reply is spoken aloud, so the gateway prepends a system prompt asking for
short plain-text conversational replies, and transliterates the reply to
ASCII before it reaches the `audio` engine.

Response:

```json
{
  "transcript": "what the user said",
  "reply": "assistant reply text",
  "audio_format": "wav",
  "audio_b64": "<base64 WAV of the spoken reply>"
}
```

Behavior:

- Engine keys: `whisper` (skipped for typed messages), `llama`, `audio`.
- Each stage reserves its engine exactly like the standalone routes, so a
  voice request and a direct `/v1/audio/speech` request cannot race.
- Stage failures map to the same statuses as the standalone routes:
  missing engine `503`, busy engine `429`, invalid input `400`, engine
  failure `502` (which also marks the engine crashed in `/health`).

## POST /v1/images/generations

Runs the configured `sd` subprocess and returns one PNG as `b64_json`.

Request JSON limit: 64 KiB.

```json
{
  "prompt": "a small cabin",
  "size": "512x512",
  "response_format": "b64_json",
  "n": 1
}
```

Supported fields:

- `prompt` is required and must be non-empty after trimming.
- `size` may be omitted or formatted as `WIDTHxHEIGHT`.
- `response_format` may be omitted or `b64_json`.
- `n` may be omitted or `1`.

Limits:

- Requested dimensions must be positive.
- Requested width and height are capped at 2048 px per side.
- Requested area is capped at 4,194,304 total pixels.
- Generated PNG output is capped at 32 MiB.

Gateway subprocess arguments:

```text
<configured sd args> --prompt <prompt> --output <generated-temp-png>
```

When `size` is supplied, the gateway also appends:

```text
--width <width> --height <height>
```

Response:

```json
{
  "created": 1783332000,
  "data": [
    {
      "b64_json": "iVBORw0KGgo..."
    }
  ]
}
```

Behavior:

- Engine key: `sd`.
- Only one image generation request can run at a time for this gateway process; concurrent requests return `429`.
- The generated file must be a valid PNG. The gateway checks PNG signature, decodes PNG metadata, enforces dimensions, and performs a full PNG decode before returning data.
- Command failure or invalid output marks `sd` crashed and returns `502` with bounded stdout/stderr details.

Not supported yet:

- URL responses.
- More than one image.
- Non-PNG output.
- `model`, `quality`, `style`, `user`, masks, image edits, or streaming.

## Timeouts

Each route uses `requestTimeoutSeconds` from the engine config when set. Otherwise it falls back to:

- Chat: 120 seconds.
- Transcription: 120 seconds.
- Speech: 180 seconds.
- Image generation: 300 seconds.

HTTP client cancellation also cancels the subprocess or upstream request context.

Subprocess failure responses can include captured stdout/stderr, bounded internally to 1 MiB per stream before a `[truncated]` marker.
