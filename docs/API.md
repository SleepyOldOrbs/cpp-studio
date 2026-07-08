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

- `ready` when all engines are ready.
- `starting` when at least one engine is not ready yet.
- `degraded` when any engine is failed, exited, missing, crashed, or blocked by a port conflict.

`/health` still returns HTTP `200` when the JSON body status is `degraded`; clients should inspect the response body.

Subprocess engines are marked ready after config validation and are launched per request.

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

Accepts a WAV upload and runs the configured `whisper` subprocess.

Request:

- `Content-Type: multipart/form-data`.
- Required field: `file`.
- Upload limit: 32 MiB.
- The uploaded file must include a filename.
- The file must have a RIFF/WAVE header.
- Other OpenAI transcription fields are not interpreted by the gateway.

Gateway subprocess arguments:

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
- Only one transcription request can run at a time for this gateway process; concurrent requests return `429`.
- Command stdout becomes `text` after trimming whitespace.
- Command failure marks `whisper` crashed and returns `502` with bounded stdout/stderr details.

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

At least one of `file` or `message` is required; requests with neither return `400`.

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
