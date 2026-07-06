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

## Route-Specific Arguments

The gateway appends request arguments to the configured base args:

- `whisper`: appends `-f <uploaded-temp-wav>`.
- `audio`: appends `--text <input> --out <generated-temp-wav>`.

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

```json
{
  "command": "whisper-cli",
  "args": ["-m", "C:\\Temp\\ggml-base.en.bin"],
  "mode": "subprocess",
  "requestTimeoutSeconds": 120
}
```

The current transcription route accepts WAV uploads only.

## Health Behavior

- Server engines report process IDs once started.
- Subprocess engines report ready after config validation.
- Successful route calls update `lastSuccessAt`.
- Failed subprocess route calls update `status` and `lastError`.
- Busy audio/transcription engines return `429` for concurrent route calls.
