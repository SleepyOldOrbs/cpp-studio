# Fixture Engine

`cmd/cpp-studio-fixture` is a deterministic test helper for gateway and demo development. It is not a native inference engine.

It exists so CI and new contributors can prove the full route sequence without downloading model weights:

```text
WAV upload -> /v1/audio/transcriptions -> /v1/chat/completions -> /v1/audio/speech -> WAV output
WAV upload -> /v1/voice (server-side transcription -> chat -> speech) -> transcript + reply + WAV output
POST /v1/images/generations -> PNG b64_json output
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

Run an sd-cli-like image command:

```powershell
.\bin\cpp-studio-fixture.exe image --prompt "a small cabin" --output .\image.png --width 512 --height 512
```

Stand in for yt-dlp so the URL importer can be exercised without a real
download (it asserts the flag contract the gateway sends, prints a title the
way `--print` does, and writes fixture audio):

```powershell
.\bin\cpp-studio-fixture.exe import --no-simulate --print "%(title)s" --force-overwrites --no-playlist -o .\imported.wav https://example.com/episode
```

## Smoke Test

Run the complete loop through the real gateway:

```powershell
.\scripts\smoke-voice-loop-fixture.ps1
```

The script builds `cpp-studio.exe` and `cpp-studio-fixture.exe`, generates a temporary config under `out\fixture-loop`, starts the gateway, sends the voice-loop requests plus an image request, verifies the final WAV header and PNG payload signature, and stops the gateway.

Story route fixture smoke:

```powershell
.\scripts\smoke-story-fixture.ps1
```

The script starts the gateway with a harmless subprocess engine, submits a curated
three-source story request to `/v1/stories`, polls completion, verifies fact-card
grounding, downloads `story.wav`, validates the WAV header and 90-second fixture
duration, checks retained story listing, and stops the gateway.

## Native Engine Boundary

The fixture is only for repeatable route and lifecycle checks. Real usage should replace it with:

- `llama-server` for chat.
- `whisper-cli` for transcription.
- `audiocpp_cli` for speech.
- `sd-cli` for image generation.

Use `docs\CONFIG.md` for native engine config examples.
