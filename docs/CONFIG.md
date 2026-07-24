# Configuration Guide

`cpp-studio` reads one JSON file. The gateway starts `mode: "server"` engines when it boots and treats `mode: "subprocess"` engines as request-time commands.

## Portable Configs: `vars`

Configs support `${name}` substitution across engine `command`, `args`,
`workingDir`, `healthUrl`, `defaultVoiceRef`, and the `models` block, so a
tracked config can avoid machine-specific absolute paths:

```json
{
  "vars": { "root": "C:\\path\\to\\your\\studio" },
  "engines": {
    "llama": {
      "command": "${root}\\engines\\llama.cpp\\llama-server.exe",
      "args": ["-m", "${root}\\engines\\models\\chat.gguf"]
    }
  }
}
```

Resolution order: the built-in `${configDir}` (absolute directory of the
config file), then `vars`, then the process environment. An unresolved token
is left literal so a typo surfaces plainly. Absolute paths without `${}` work
unchanged. `config.example.json` uses this shape — a new machine edits one
`root` value.

## Model Manifest: `models`

The optional `models` block points at the tracked model registry
(`models.json`) and the root its relative paths resolve against:

```json
{
  "models": { "manifest": "${configDir}/models.json", "root": "${root}" }
}
```

`GET /v1/models/catalog` then reports every declared model with its live
on-disk state (`present`, `missing`, `size-mismatch`, `unverified` for
directory models without size checks), which powers the console's Models tab.
Each manifest entry carries `id`, `engine`, `family`, relative `path`, and
optionally `bytes`, `sha256`, `source`, `license`, and `description`. Without
a `models` block the catalog is empty and everything else works unchanged.

## VRAM Profiles: `profiles`

Named engine sets for trading resident models against a fixed VRAM budget:

```json
{
  "profiles": {
    "everything": ["llama", "whisper", "audio", "sd"],
    "chat": ["llama", "whisper", "audio"],
    "art": ["sd"]
  }
}
```

`POST /v1/engines/profiles/{name}` stops every server-mode engine not in the
set and starts the members (subprocess engines are unaffected — they hold no
VRAM between requests). Individual engines can also be controlled with
`POST /v1/engines/{name}/start|stop|reload`. The console's Engines tab
surfaces both, along with `GET /v1/gpu`'s VRAM readout. Profile names must
reference declared engines; validation rejects unknown names.

## Engine Modes

### Server

Use `server` for long-running HTTP engines such as `llama-server`.

```json
{
  "command": "llama-server",
  "args": ["--host", "127.0.0.1", "--port", "8733", "-m", "C:\\Temp\\model.gguf", "--jinja"],
  "mode": "server",
  "healthUrl": "http://127.0.0.1:8733/health",
  "startupTimeoutSeconds": 60,
  "shutdownTimeoutSeconds": 10,
  "requestTimeoutSeconds": 120
}
```

The chat route infers the upstream URL from `healthUrl`. For example, `http://127.0.0.1:8733/health` becomes `http://127.0.0.1:8733/v1/chat/completions`.

### Subprocess

Use `subprocess` for tools that should run once per request.

```json
{
  "command": "whisper-cli",
  "args": ["-m", "C:\\Temp\\whisper.bin"],
  "mode": "subprocess",
  "requestTimeoutSeconds": 120
}
```

Subprocess engines are validated at startup and shown as ready in `/health`, but they are not launched until a route needs them.

### GPU serialization

Set `"gpu": true` on subprocess engines that are heavy GPU users (typically `audio` and `sd`). Runs of GPU-marked engines are serialized across engine names: a speech synthesis that lands while an image is rendering waits for the GPU instead of crashing on VRAM contention. Each engine still has its own single-run slot; `gpu` only adds the cross-engine ordering.

## Route-Specific Arguments

The gateway appends request arguments to the configured base args:

- `whisper`: appends `-f <uploaded-temp-wav>`.
- `audio`: appends `--text <input> --out <generated-temp-wav>`, plus `--voice-ref <wav> --reference-text <transcript>` when the request selects a cloned voice (per-run values replace matching config flags in place).
- `sd`: appends `--prompt <prompt> --output <generated-temp-png>`, plus `--width <px> --height <px>` when the request includes `size`.
- `voicedesign` / `omnivoice`: append `--instruct <description> --text <sample> --out <generated-temp-wav>`.
- `voxcpm2`: appends `--text "(<description>)<sample>" --out <generated-temp-wav>`.
- `vision`: a `server` engine like `llama`; the image description route infers `/v1/chat/completions` from its `healthUrl`.

Keep stable model/backend options in `args`, and let the gateway own request input/output paths.

## Resident Audio Server

The `audio` engine can run as `mode: "server"` pointing at `audiocpp_server`
(build it with `cmake --build <build-dir> --target audiocpp_server` in the
audio.cpp checkout). The server keeps the TTS model and session loaded
between requests, cutting speech latency roughly 10x versus the
subprocess CLI (~0.8s vs ~8s per short reply on the reference machine).

```json
{
  "command": "..\\audio.cpp\\build\\windows-cuda-release\\bin\\audiocpp_server.exe",
  "args": ["--config", "..\\engines\\audio-server.json"],
  "mode": "server",
  "healthUrl": "http://127.0.0.1:8736/health",
  "startupTimeoutSeconds": 120,
  "requestTimeoutSeconds": 180,
  "defaultVoiceRef": "..\\audio.cpp\\assets\\resources\\b.wav",
  "defaultVoiceText": "Some call me nature. ..."
}
```

The gateway infers `/v1/audio/speech` from `healthUrl` and must find a model
with id `"tts"` in the audiocpp_server config. `defaultVoiceRef` /
`defaultVoiceText` replace the `--voice-ref`/`--reference-text` args of the
subprocess shape (cloned voices are passed per request). Subprocess mode
keeps working unchanged — the gateway routes per the configured `mode`.

## Voice Design Engines

`POST /v1/voices/design` picks its engine from the request's `model` field: `voxcpm2` (default), `omnivoice`, or `qwen3` (engine name `voicedesign`). All three run `audiocpp_cli` as subprocess engines with `"gpu": true`; configure only the ones whose models you have — the demo page shows a model choice for each configured engine.

- `voicedesign`: `--task vdes --family qwen3_tts --model models/Qwen3-TTS-12Hz-1.7B-VoiceDesign` (most expressive; American-leaning).
- `omnivoice`: `--task tts --family omnivoice --model models/OmniVoice` (precision accents; strict attribute instruct — the gateway normalizes free prose into its vocabulary via the `llama` engine when configured).
- `voxcpm2`: `--task tts --family voxcpm2 --model models/VoxCPM2` (realistic free-prose design, 48 kHz). One-time setup: the HF snapshot ships `audiovae.pth`; convert it with audio.cpp's model manager before first use:

```powershell
py -3.11 ..\audio.cpp\tools\model_manager.py install voxcpm2_audiovae --source-file ..\audio.cpp\models\VoxCPM2\audiovae.pth --models-root ..\audio.cpp\models --overwrite
```

## Local Examples

### llama.cpp

The local notes include a Qwen llama-server shape like this:

```powershell
llama-server --host 127.0.0.1 --port 8733 `
  -m "C:\Temp\Qwen3.6-27B-Q3_K_M.gguf" `
  --alias qwen-local `
  --jinja `
  --ctx-size 16384
```

Prefer `127.0.0.1` for the first local demo. Use `0.0.0.0` only when you intentionally want LAN access.

Cap the context with `-c` (e.g. `-c 16384`): left unset, llama-server sizes
its KV cache from the model's maximum (80k+ tokens on recent models), which
can cost several GB of VRAM that the voice loop's short conversations never
use — VRAM that transient engines (sd, voice design) need.

### audio.cpp

The verified local `audio.cpp` route uses the sibling checkout:

```json
{
  "command": "..\\audio.cpp\\build\\windows-cuda-release\\bin\\audiocpp_cli.exe",
  "args": [
    "--task", "tts",
    "--family", "qwen3_tts",
    "--model", "..\\audio.cpp\\models\\Qwen3-TTS-12Hz-0.6B-Base",
    "--backend", "cuda",
    "--voice-ref", "..\\audio.cpp\\assets\\resources\\b.wav",
    "--reference-text", "Some call me nature. Others call me Mother Nature. I've been here for over 4.5 billion years. 22,500 times longer than you.",
    "--max-tokens", "256",
    "--session-option", "qwen3_tts.weight_type=q8_0"
  ],
  "mode": "subprocess",
  "requestTimeoutSeconds": 180
}
```

This is captured in `config.audio-local.example.json`.

### whisper.cpp

Point `command` at your `whisper-cli` binary and keep the model in `args`.
Add `-nt -np` so stdout carries only the transcript.

```json
{
  "command": "whisper-cli",
  "args": ["-m", "C:\\Temp\\ggml-base.en.bin", "-nt", "-np"],
  "mode": "subprocess",
  "requestTimeoutSeconds": 120
}
```

For repeated transcription (the demo's live-transcribe mode), prefer
`whisper-server` in server mode — the model stays loaded between requests:

```json
{
  "command": "whisper-server",
  "args": ["-m", "C:\\Temp\\ggml-base.en.bin", "--host", "127.0.0.1", "--port", "8734"],
  "mode": "server",
  "healthUrl": "http://127.0.0.1:8734/health",
  "startupTimeoutSeconds": 30,
  "shutdownTimeoutSeconds": 5,
  "requestTimeoutSeconds": 120
}
```

The gateway infers the inference route from `healthUrl` (`/health` ->
`/inference`). The current transcription route accepts WAV uploads only.

### stable-diffusion.cpp

Point `command` at your `sd-cli` binary and keep model/backend options in `args`.

```json
{
  "command": "sd-cli",
  "args": ["-m", "C:\\Temp\\v1-5-pruned-emaonly.safetensors"],
  "mode": "subprocess",
  "requestTimeoutSeconds": 300
}
```

The image route accepts `{ "prompt": "...", "size": "512x512", "response_format": "b64_json" }`, runs the configured `sd` subprocess, validates the generated PNG, and returns OpenAI-shaped `b64_json` data. Requested dimensions are capped at 2048 px per side and 4,194,304 total pixels. This is a narrow subset: one PNG only, `b64_json` only, `n` must be omitted or `1`, and request fields such as `model`, `quality`, `style`, and `user` are ignored.

The gateway currently appends `--prompt`, `--output`, `--width`, and `--height`. The upstream `stable-diffusion.cpp` README describes the project as active development and notes that CLI options may change frequently, so run `sd-cli -h` against your exact binary before relying on a newer build.

For fast repeated generation, prefer `sd-server` in server mode — the model
stays loaded between requests (~2s per 512x512 on the reference machine vs
~30-55s per subprocess run, which reloads the model every time):

```json
{
  "command": "sd-server",
  "args": [
    "--listen-ip", "127.0.0.1",
    "--listen-port", "8737",
    "-m", "C:\\Temp\\v1-5-pruned-emaonly.safetensors",
    "--type", "f16",
    "--diffusion-fa",
    "--vae-tiling"
  ],
  "mode": "server",
  "healthUrl": "http://127.0.0.1:8737/v1/models",
  "startupTimeoutSeconds": 60,
  "requestTimeoutSeconds": 300
}
```

`sd-server` has no `/health` route, so `healthUrl` points at `/v1/models`
(returns 200 once listening); the gateway takes the URL's origin and posts to
`/v1/images/generations`. `--type f16` halves resident VRAM with no visible
quality loss on SD 1.5, and `--vae-tiling` bounds the decode spike so the
model coexists with the other engines on a 16 GB card. One sharp edge: the
gateway deliberately omits the `n` field from upstream requests — sd-server's
JSON parser fatally rejects `"n":null`.

## Health Behavior

- Server engines report process IDs once started.
- Subprocess engines report ready after config validation.
- Successful route calls update `lastSuccessAt`.
- Failed subprocess route calls update `status` and `lastError`.
- Busy audio/transcription/image engines return `429` for concurrent route calls.
