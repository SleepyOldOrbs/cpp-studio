# Release Notes

## Source Release Checklist

1. Run the portable gate:

   ```powershell
   .\scripts\verify.ps1
   ```

2. On James's local Windows machine, run the audio route proof:

   ```powershell
   .\scripts\verify.ps1 -IncludeLocalAudio
   .\scripts\smoke-speech-route.ps1
   ```

3. Run the deterministic full-loop fixture smoke:

   ```powershell
   .\scripts\smoke-voice-loop-fixture.ps1
   ```

4. Confirm generated files are ignored:

   ```powershell
   git status --ignored --short
   ```

5. Push the branch and let GitHub Actions run on Windows and Linux.

## Current Release Scope

- Gateway lifecycle and `/health`.
- Chat proxy route for a configured `llama-server`.
- WAV transcription subprocess route for a configured `whisper-cli`.
- WAV speech subprocess route for configured `audiocpp_cli`.
- Browser demo at `/demo/`.
- Local Qwen3 TTS proof through `audio.cpp`.

## Still Outside This Release

- Bundled native engine binaries.
- Bundled model weights.
- Strict OpenAI compatibility.
- Realtime streaming.
- `stable-diffusion.cpp` image generation.
- Desktop installer.
