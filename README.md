# cpp-studio

Local gateway and launcher for the native `*.cpp` inference family: `llama.cpp`, `whisper.cpp`, `audio.cpp`, and later `stable-diffusion.cpp`.

Milestone 1 is intentionally narrow: load a config, manage native engine process lifecycle, expose `/health`, and provide the first OpenAI-shaped local routes for chat, transcription, and speech. Engines stay native subprocesses or local servers; the gateway owns orchestration.

## Quick Start

```powershell
go test ./...
go run .\cmd\cpp-studio --config .\config.smoke.json --check
```

`config.example.json` is a shape example. Point `command`, `args`, and `healthUrl` at real local binaries before starting the gateway.
`config.smoke.json` is a runnable local smoke config that starts a harmless PowerShell process so the gateway can be tested end to end.
`config.audio-local.example.json` points at the sibling `..\audio.cpp` checkout and the verified Qwen3 TTS model from the proof note.

After wiring real binaries into a copy of `config.example.json`, validate it with:

```powershell
go run .\cmd\cpp-studio --config .\config.example.json --check
```

Run the gateway:

```powershell
go run .\cmd\cpp-studio --config .\config.example.json
```

Smoke the gateway without native model binaries:

```powershell
go run .\cmd\cpp-studio --config .\config.smoke.json --run-seconds 5
```

That smoke config only proves lifecycle and `/health`; it does not configure the voice-loop engines.

Health:

```powershell
Invoke-RestMethod http://127.0.0.1:8765/health
```

Browser demo:

```text
http://127.0.0.1:8765/demo/
```

The demo can refresh gateway health, record a WAV in the browser, upload a WAV fallback, run transcription -> chat -> speech, and play the returned WAV.
The full loop requires configured `whisper`, `llama`, and `audio` engines. With `config.audio-local.example.json`, the demo can exercise health and the speech route; transcription and chat still need local `whisper.cpp` and `llama.cpp` paths.

## Verification

Portable checks:

```powershell
.\scripts\verify.ps1
```

James-local audio route check:

```powershell
.\scripts\verify.ps1 -IncludeLocalAudio
.\scripts\smoke-speech-route.ps1
```

Deterministic full-loop fixture check:

```powershell
.\scripts\smoke-voice-loop-fixture.ps1
```

Release checklist:

```text
docs\RELEASE.md
```

Configuration guide:

```text
docs\CONFIG.md
```

Fixture engine guide:

```text
docs\FIXTURE.md
```

## Routes

- `GET /health`: gateway and engine state, process IDs, last errors, and log tails.
- `POST /v1/chat/completions`: proxies JSON to the configured `llama` server. If `healthUrl` is `http://127.0.0.1:8733/health`, the gateway proxies to `http://127.0.0.1:8733/v1/chat/completions`.
- `POST /v1/audio/transcriptions`: accepts multipart field `file`, runs the configured `whisper` subprocess with `-f <temp-file>`, and returns `{ "text": "...", "duration_ms": 1234 }`.
- `POST /v1/audio/speech`: accepts `{ "input": "...", "voice": "default", "format": "wav" }`, runs the configured `audio` subprocess with `--text <input> --out <temp.wav>`, and returns `audio/wav`.

Milestone 1 supports WAV speech output only. Streaming, MP3, WebRTC, and image generation are still deferred.

## Engine Modes

Use `mode: "server"` for long-running services such as `llama-server`. These are started with the gateway and can use `healthUrl`.

Use `mode: "subprocess"` for request-time tools such as `whisper-cli` and `audiocpp_cli`. These are validated at startup, shown as ready in `/health`, and launched per request with cancellation and timeout inherited from the HTTP request.

## audio.cpp TTS Proof

The local `audio.cpp` proof lives one directory up:

```text
..\AUDIO_CPP_TTS_PROOF.md
```

Confirmed working fallback:

- `audio.cpp` branch `release-0.1`, commit `e74a2902d0ea309473a458e53718fe5b1170387c`
- CUDA `audiocpp_cli.exe` build succeeded
- Qwen3 TTS 0.6B Base model path: `..\audio.cpp\models\Qwen3-TTS-12Hz-0.6B-Base`
- Direct CLI proof output: `..\audio.cpp\build\out\qwen3_tts_0_6b_smoke.wav`
- Direct CLI proof runtime: `7.74s`
- Output format: mono 16-bit 24 kHz WAV, `3.28s`
- GPU detected: NVIDIA GeForce RTX 5080, 16 GB VRAM
- Quality verdict: acceptable for the first local demo route; PocketTTS can be revisited once model access is available.
- PocketTTS was blocked by Hugging Face 401 without a token

Gateway speech route proof:

```powershell
$env:Path = [Environment]::GetEnvironmentVariable('Path','Machine') + ';' + [Environment]::GetEnvironmentVariable('Path','User')
$server = Start-Process -WindowStyle Hidden -PassThru -FilePath "go" -ArgumentList @("run", ".\cmd\cpp-studio", "--config", ".\config.audio-local.example.json")
try {
  do {
    Start-Sleep -Milliseconds 500
    try { $health = Invoke-RestMethod http://127.0.0.1:8765/health } catch { $health = $null }
  } until ($health.status -eq "ready")

  New-Item -ItemType Directory -Force -Path .\out | Out-Null
  $out = ".\out\speech-route.wav"
  $body = @{ input = 'Hello from cpp-studio.'; voice = 'default'; format = 'wav' } | ConvertTo-Json
  Invoke-WebRequest -Uri http://127.0.0.1:8765/v1/audio/speech -Method Post -ContentType 'application/json' -Body $body -OutFile $out
  py -3.11 -c "import wave, pathlib; p=pathlib.Path(r'$out'); w=wave.open(str(p),'rb'); print({'bytes': p.stat().st_size, 'channels': w.getnchannels(), 'sample_width_bytes': w.getsampwidth(), 'sample_rate': w.getframerate(), 'frames': w.getnframes(), 'duration_seconds': round(w.getnframes()/w.getframerate(), 3)})"
}
finally {
  if ($server -and -not $server.HasExited) { Stop-Process -Id $server.Id }
}
```

Verified route output: mono 16-bit 24 kHz WAV.
