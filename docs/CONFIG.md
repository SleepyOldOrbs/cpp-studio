# Configuration Guide

`cpp-studio` reads one JSON file. The gateway starts `mode: "server"` engines when it boots and treats `mode: "subprocess"` engines as request-time commands.

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
- `audio`: appends `--text <input> --out <generated-temp-wav>`.
- `sd`: appends `--prompt <prompt> --output <generated-temp-png>`, plus `--width <px> --height <px>` when the request includes `size`.

Keep stable model/backend options in `args`, and let the gateway own request input/output paths.

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

## Health Behavior

- Server engines report process IDs once started.
- Subprocess engines report ready after config validation.
- Successful route calls update `lastSuccessAt`.
- Failed subprocess route calls update `status` and `lastError`.
- Busy audio/transcription/image engines return `429` for concurrent route calls.
