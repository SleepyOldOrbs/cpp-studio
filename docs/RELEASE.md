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

4. Build a release package:

   ```powershell
   .\scripts\package-release.ps1 -Runtime windows-amd64
   ```

   The archive is written to `dist\cpp-studio-windows-amd64.zip`. GitHub Actions also builds and uploads Windows and Linux package archives for every CI run.

5. Confirm generated files are ignored:

   ```powershell
   git status --ignored --short
   ```

6. Push the branch and let GitHub Actions run on Windows and Linux.

## Current Release Scope

- Gateway lifecycle and `/health`.
- Chat proxy route for a configured `llama-server`.
- WAV transcription subprocess route for a configured `whisper-cli`.
- WAV speech subprocess route for configured `audiocpp_cli`.
- PNG image generation subprocess route for configured `sd-cli`.
- Browser demo at `/demo/`.
- Local Qwen3 TTS proof through `audio.cpp`.
- Route contracts documented in `docs\API.md`.
- CI-built release archives for Windows and Linux containing `cpp-studio`, `cpp-studio-fixture`, configs, README, and docs.

## CI Artifacts

- `cpp-studio-windows-amd64`: `dist\cpp-studio-windows-amd64.zip`
- `cpp-studio-linux-amd64`: `dist/cpp-studio-linux-amd64.tar.gz`

Both archives extract to a top-level `cpp-studio-<runtime>` directory. They include `cpp-studio`, `cpp-studio-fixture`, README, docs, and sample configs. They do not include native inference engine binaries or model weights.

`config.ci.json` is the portable config-check example. `config.smoke.json` is included for parity with the source tree, but it is Windows-oriented because it starts `powershell.exe`.

## Still Outside This Release

- Bundled native engine binaries.
- Bundled model weights.
- Strict OpenAI compatibility.
- Realtime streaming.
- Local `stable-diffusion.cpp` model proof on James's machine.
- URL image responses and non-PNG image outputs.
- Desktop installer.
