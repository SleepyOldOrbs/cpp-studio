# Fixture Engine

`cmd/cpp-studio-fixture` is a deterministic test helper for gateway and demo development. It is not a native inference engine.

It exists so CI and new contributors can prove the full route sequence without downloading model weights:

```text
WAV upload -> /v1/audio/transcriptions -> /v1/chat/completions -> /v1/audio/speech -> WAV output
```

## Commands

Start a llama-server-like HTTP fixture:

```powershell
.\bin\cpp-studio-fixture.exe server --host 127.0.0.1 --port 8799
```

Run a whisper-like transcription:

```powershell
.\bin\cpp-studio-fixture.exe whisper -f .\input.wav
```

Run an audio.cpp-like speech command:

```powershell
.\bin\cpp-studio-fixture.exe speech --text "hello" --out .\reply.wav
```

## Smoke Test

Run the complete loop through the real gateway:

```powershell
.\scripts\smoke-voice-loop-fixture.ps1
```

The script builds `cpp-studio.exe` and `cpp-studio-fixture.exe`, generates a temporary config under `out\fixture-loop`, starts the gateway, sends all three route requests, verifies the final WAV header, and stops the gateway.

## Native Engine Boundary

The fixture is only for repeatable route and lifecycle checks. Real usage should replace it with:

- `llama-server` for chat.
- `whisper-cli` for transcription.
- `audiocpp_cli` for speech.

Use `docs\CONFIG.md` for native engine config examples.
