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
      "command": "${root}\\engines\\llama-engines\\llama-server.exe",
      "args": ["-m", "${root}\\models\\text\\chat.gguf"]
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
  "models": {
    "manifest": "${configDir}/models.json",
    "root": "${root}\\models",
    "discovery": {
      "pythonCommand": "python",
      "managerScript": "${root}/../audio.cpp/tools/model_manager_v2.py",
      "audioCli": "${root}/engines/audio-engines/audiocpp_cli.exe",
      "workingDir": "${root}/../audio.cpp",
      "allowedPackages": ["dramabox_q8_0"],
      "timeoutSeconds": 10
    }
  }
}
```

`GET /v1/models/catalog` then reports every declared model with its live
on-disk state (`present`, `missing`, `size-mismatch`, `unverified` for
directory models without size checks), which powers the console's Models tab.
Each manifest entry carries `id`, `engine`, `family`, relative `path`, and
optionally `bytes`, `sha256`, `source`, `license`, and `description`. Immutable
install entries additionally carry `packageId`, a pinned `revision`, and an HTTPS
`downloadUrl`. Without
a `models` block the catalog is empty and everything else works unchanged.

Discovery is optional and server-owned. cpp-studio executes only the fixed manager
`list`/allowlisted `info` commands and the fixed CLI loader query; HTTP requests can
never choose a command, package, argument, source, token, or destination. Missing or
old tools add `discoveryError` but do not hide the tracked catalog. Only packages in
`allowedPackages` with complete immutable metadata become installable. Package ids
are simple bounded identifiers and the allowlist is capped at 64 entries. Each command
uses the configured timeout (10 seconds by default, bounded to at most 300 seconds).

## Bring Your Own Model

For chat models, byo is first-class: give the `llama` engine a `byomDir`
and a `byomArgs` template, and every `*.gguf` file in that directory
appears in the Talk panel's chat-model picker (and in
`GET /v1/engines/llama/variants` as a `byom:<filename>` entry) with no
config edit and no restart — drop a file in, it shows up.

```json
"llama": {
  "defaultVariant": "qwen3-4b",
  "variants": { "qwen3-4b": { "args": ["...", "-m", "${root}\\models\\qwen.gguf", "..."] } },
  "byomDir": "${root}",
  "byomArgs": ["--host", "127.0.0.1", "--port", "8733", "-m", "{model}", "--jinja", "-ngl", "99", "-c", "8192"]
}
```

Rules: `byomDir` requires a `variants` block (boot never depends on a
directory scan), and `byomArgs` must contain the `{model}` placeholder
exactly once — written with braces only, because `${model}` would be
expanded against vars and the environment at load time. Switching restarts
llama-server on the chosen file; a model that fails to load auto-reverts
to the previous one. After a successful switch the gateway fires a one-token
warmup completion in the background, so the minutes a large model spends
paging weights off slow storage are paid immediately rather than by your
first message (which would otherwise hit the request timeout and come back
empty).

Each byom entry carries a **fit preflight**: file size against live
`nvidia-smi` free memory (crediting back what the currently loaded model
will free), with verdicts `fits`, `tight`, `too_big`, or `no_gpu_info`.
The headroom numbers are calibrated for the `-c 8192` context in the
template above — raise the context in your `byomArgs` and the verdicts
get optimistic. A `too_big` model whose GGUF header says
mixture-of-experts is offered the one black-and-white remedy: `POST
/v1/engines/llama/variant {"id": "byom:...", "remedy": "cpu-moe"}` loads
it with `--cpu-moe` (experts in system RAM, attention on GPU — it loads
and runs, slower per token). Remedies are server-defined names, never
client-supplied flags. While a story production is running, chat-model
switches are refused with `409` — the production is scripting through the
same llama.

For whisper the older recipe still applies: point a variant's `-m` at any
ggml `.bin` and switch with the Extract panel's picker, or edit the config
and `POST /v1/engines/whisper/reload`. The same manual recipe covers the
SD checkpoint. The TTS and voice-design engines are coupled to their model
families' CLI contracts — treat those as fixed unless you are also
changing the audio.cpp invocation.

## Speaker Diarization: Sortformer with sherpa fallback

Optional. The `diarize` engine is audio.cpp's CUDA Sortformer route. It handles
16 kHz mono PCM WAVs up to 120 seconds and up to four speakers. The gateway
sizes Sortformer's fixed graph window from the uploaded WAV and serializes it
with the other GPU subprocesses. The packaged NVIDIA model is
`CC-BY-NC-4.0`, so it is for non-commercial use.

The separate `diarize-sherpa` CPU engine remains the whole-recording fallback:
it is selected when `?speakers=N` requests an exact cluster count, the WAV is
not 16 kHz mono PCM, or the recording exceeds 120 seconds.
For recordings that may contain more than four speakers, callers must provide
the count; Sortformer cannot discover a fifth output channel after it starts.

```json
{
  "diarize": {
    "command": "${root}\\engines\\audio-engines\\audiocpp_cli.exe",
    "args": [
      "--task", "diar",
      "--family", "sortformer_diar",
      "--model", "${root}\\models\\speech\\analysis\\diarization\\Sortformer-Diar-4spk-v1-GGUF\\sortformer-diar-4spk-v1-q8_0.gguf",
      "--backend", "cuda"
    ],
    "mode": "subprocess",
    "requestTimeoutSeconds": 300,
    "gpu": true
  },
  "diarize-sherpa": {
    "command": "${root}\\engines\\sherpa-onnx\\sherpa-onnx-v1.13.4-win-x64-shared-MD-Release\\bin\\sherpa-onnx-offline-speaker-diarization.exe",
    "args": [
      "--segmentation.pyannote-model=${root}\\models\\speech\\analysis\\diarization\\sherpa-onnx-pyannote-segmentation-3-0\\model.onnx",
      "--embedding.model=${root}\\models\\speech\\analysis\\speaker-embedding\\nemo_en_titanet_small.onnx",
      "--clustering.cluster-threshold=0.8"
    ],
    "mode": "subprocess",
    "requestTimeoutSeconds": 300
  }
}
```

Install the GGUF with audio.cpp's model manager, targeting CPP Studio's typed
model directory:

```powershell
python ..\audio.cpp\tools\model_manager_v2.py install --models-root .\models\speech\analysis\diarization sortformer_diar_4spk_v1_q8_0
```

For sherpa, the gateway appends the input WAV as the positional argument and
parses `start -- end speaker_NN` stdout lines. Swap `--embedding.model` to
compare embeddings (titanet_small beat CAM++ decisively on the reference
bake-off). If neither engine is configured, `POST /v1/audio/diarization`
returns `503` and the Extractor's "Detect speakers" button is hidden.

## URL Import: the `ytdlp` engine

Optional, and deliberately not bundled: **you supply the binary**. Point a
subprocess engine named `ytdlp` at your own
[yt-dlp](https://github.com/yt-dlp/yt-dlp) (2021.05 or later — the importer
uses `--print` with `--no-simulate`) and the Extractor grows a URL row:

```json
{
  "ytdlp": {
    "command": "${root}\\tools\\yt-dlp.exe",
    "args": ["-f", "bestaudio[ext=m4a]/bestaudio/best"],
    "mode": "subprocess",
    "requestTimeoutSeconds": 900
  }
}
```

The `args` are yours: format selection is the one decision that depends on
both the site and what your browser can decode. The gateway appends the rest
of the contract — `--no-simulate --print "%(title)s" --force-overwrites
--no-playlist -o <temp file> <url>` — then sniffs the container, refuses
anything that is not decodable audio (an HTML error page, say), and streams
the bytes to the Extractor with the source title in an `X-Import-Title`
header. Nothing is written to your library on the way through.

Fetched audio must land in a format Web Audio decodes: MP3, M4A/MP4, OGG,
Opus/WebM, FLAC, or WAV. A merged video format would need ffmpeg and is not
what this is for — keep the selector on `bestaudio`. Downloads are capped at
192 MB and the request times out per `requestTimeoutSeconds` (the built-in
default is 15 minutes). Without this engine, `POST /v1/audio/import` returns
`503` and the Extractor's URL row is hidden.

Whether a URL is yours to download, and whose voice is in it, is settled
before you paste it — see the cloning note in the README.

## Delivery Exports: the `ffmpeg` engine

Optional, and you supply the binary. Point a subprocess engine named
`ffmpeg` at your own build and produced stories can be encoded to MP3 or
Opus:

```json
{
  "ffmpeg": {
    "command": "${root}\\tools\\ffmpeg\\bin\\ffmpeg.exe",
    "mode": "subprocess",
    "requestTimeoutSeconds": 600
  }
}
```

No `args`: the gateway owns the whole command line for a transcode
(`-nostdin -y -i <render> -vn -c:a <encoder> -b:a <bitrate> <out>`), because
unlike a model path there is nothing here for an operator to tune.

A configured ffmpeg is not the same claim as an ffmpeg that can make an MP3
— builds vary. The gateway asks yours once, with `-encoders`, and
`GET /v1/audio/formats` reports what came back, so the console offers only
formats that will actually work. An export naming a missing encoder fails
with `export_unavailable` and names the encoder it wanted.

Exports are written beside the render revision they encode
(`renders/render-003.mp3` next to `renders/render-003.wav`) and recorded on
that revision in the manifest. Re-exporting the same format replaces it;
re-rendering leaves earlier revisions and their exports untouched. Bitrates
are 32k-320k, defaulting to 128k for MP3 and 64k for Opus.

For scale: a 43-second two-hander is 2.0 MB as WAV, 687 KB as MP3, 348 KB as
Opus. Without this engine, `POST /v1/stories/{id}/export` returns
`export_unavailable` and the console's export buttons are hidden.

The same engine also **decodes what the browser refuses**. The Extractor
reads WAV, MP3, OGG, FLAC and MP4/WebM client-side; with ffmpeg configured,
anything else — old MPEG-1/2 radio rips, WMA, AC3, video containers with
unusual audio tracks — is converted automatically at the moment the browser
gives up, instead of being turned away. This is the ffmpeg importer M5
deferred. See `POST /v1/audio/decode` in [`API.md`](API.md).

The same engine also **masters every render**: speakers are levelled against
each other and the finished piece is placed at -16 LUFS under a -1.5 dBTP
ceiling, with the measured before and after values recorded in the manifest.
See "Mastering" in [`STORY_API.md`](STORY_API.md). Configure ffmpeg and your
renders are mastered; leave it out and they are exactly as stitched, with no
`master` block claiming otherwise.

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

## Engine Variants: `variants`

One binary, several models: whisper's large-v3 against its turbo, sd-server's
SD 1.5 against FLUX.2. A server-mode engine may declare named argument sets
and the default it boots with; `args` must then be empty — the variants are
the single source of truth.

```json
{
  "whisper": {
    "command": "...\\whisper-server.exe",
    "mode": "server",
    "healthUrl": "http://127.0.0.1:8734/health",
    "defaultVariant": "large-v3",
    "variants": {
      "large-v3":       {"label": "large-v3 — best quality", "args": ["-m", "${root}\\models\\transcription\\Whisper\\ggml-large-v3.bin", "..."]},
      "large-v3-turbo": {"label": "large-v3-turbo — faster", "args": ["-m", "${root}\\models\\transcription\\Whisper\\ggml-large-v3-turbo.bin", "..."]}
    }
  }
}
```

`GET /v1/engines/{name}/variants` lists them; `POST /v1/engines/{name}/variant`
with `{"id": "..."}` switches — a running server restarts on the new args
(the model load is the wait), a stopped one simply starts on them next time.
The console grows a model picker wherever an engine declares more than one
variant: the transcription model in Extract, the diffusion model in the
Image Lab. Health reports the active variant per engine. Variants are for
server-mode engines only; a subprocess engine takes per-run flags instead.

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
- `audio` / `dramabox`: append `--text <input> --out <generated-temp-wav>`, plus `--voice-ref <wav> --reference-text <transcript>` when the request selects a cloned voice (per-run values replace matching config flags in place). DramaBox also supports text-only requests with no reference. Its typed per-section options map to fixed allowlisted CLI/server fields; cpp-studio owns the seed and never accepts an arbitrary native argument from HTTP.
- `sd`: appends `--prompt <prompt> --output <generated-temp-png>`, plus `--width <px> --height <px>` when the request includes `size`.
- `voicedesign` / `omnivoice` voice design: append `--instruct <description> --text <sample> --out <generated-temp-wav>`.
- `omnivoice` Character Voice preview: append `--voice-ref <Actor Voice wav> --reference-text <Actor Voice transcript> --instruct <Character Voice direction> --text <sample> --out <generated-temp-wav>`. Per-request values replace matching configured flags.
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

### Optional DramaBox audiobook engine

`config.dramabox-local.example.json` keeps the existing `audio` narrator and
adds a separately named `dramabox` resident server. The Audiobook desk enables
DramaBox only when that engine is present in `/health`; removing the engine
from config is the rollback. Engine selection is deliberately limited to
`audio` and `dramabox`, even if other tools are configured.

The example requires audio.cpp `release-0.5` and pairs with
`dramabox-server.example.json`. Put the official
`DramaBox-GGUF/dramabox-q8_0.gguf` under `cpp-studio/models/speech/synthesis`, edit the gateway
example's `root`, and start cpp-studio with that copied config. The server
registers model id `tts`, lazy-loads it on the first expressive book, and
keeps it resident afterward. Text-only DramaBox requests omit `voice_ref`;
selecting a stored voice sends its reference WAV. The existing Qwen `audio`
server still requires `defaultVoiceRef`. Keep the paired server JSON beside
the gateway example: audio.cpp resolves its `models/speech/synthesis/...` model
path relative to that JSON file, not the process working directory.

The shipped server example uses `"backend": "cpu"`. This is a conservative
startup posture, not a speed recommendation: the GGUF is 18,942,803,808 bytes,
larger than the 16,303 MiB reference GPU. After correcting release-0.5's signed
native `int` seed boundary, the canonical Ryzen 7 9800X3D resident run measured cold
RTF 37.24 and warm RTF 27.11 (a 30.13-hour 10,000-word projection). A fresh
`dramabox.mem_saver=true` run measured 32.05 and 25.21 (28.01 hours). Both required
native long-form cases failed with `regex_error(error_stack)` insufficient memory on
61.6 GiB RAM. cpp-studio therefore uses positive 31-bit section seeds, but that seed
fix does not make this CPU machine long-form qualified. This is technical evidence,
not a voice-quality claim.
Only switch the server JSON to `cuda` after a representative chapter proves
load, peak VRAM, factual fidelity, and real-time factor on your machine. If it
is too slow or fails to fit, select Fast local narrator or remove `dramabox`;
existing WAVs and manifests remain usable.

The Models tab can install only the pinned `dramabox-q8-0` catalog artifact. Preview
shows immutable source/revision, exact destination, licence, expected bytes, checksum,
free space, and warnings. Confirmation is short-lived and single-use. The tracked job
stages under the models root, streams progress, verifies size and SHA-256, never
overwrites, and becomes non-cancellable only for atomic promotion and catalog refresh.
Installation does not start or reload the engine; use the existing Engine controls.

The DramaBox example intentionally does not invent a Whisper path. Merge the
`whisper` engine from your working base config to enable factual verification.
`auto` then compares each section when Whisper is present and otherwise records
`unavailable`; `required` refuses creation if Whisper is absent and retains an
interrupted WIP if ASR later fails; `off` records `skipped`. Verification runs
serially after synthesis, so it does not race section generation for local compute.

Every returned DramaBox production ID already has a private
`out/audiobooks/.book_....wip` containing `source.txt`, the complete seeded plan,
and the frozen synthesis identity. Do not edit these files in place. Use the
Audiobook desk or the Resume/Restart/Discard API described in
[`DRAMABOX_AUDIOBOOKS.md`](DRAMABOX_AUDIOBOOKS.md).

## Voice Design Engines

`POST /v1/voices/design` picks its engine from the request's `model` field: `voxcpm2` (default), `omnivoice`, or `qwen3` (engine name `voicedesign`). All three run `audiocpp_cli` as subprocess engines with `"gpu": true`; configure only the ones whose models you have — the demo page shows a model choice for each configured engine.

- `voicedesign`: `--task vdes --family qwen3_tts --model models/speech/voice-design/Qwen3-TTS-12Hz-1.7B-VoiceDesign` (most expressive; American-leaning).
- `omnivoice`: `--task tts --family omnivoice --model models/speech/voice-design/OmniVoice` (precision accents; strict attribute instruct — the gateway normalizes free prose into its vocabulary via the `llama` engine when configured).
- `voxcpm2`: `--task tts --family voxcpm2 --model models/speech/voice-design/VoxCPM2` (realistic free-prose design, 48 kHz). One-time setup: the HF snapshot ships `audiovae.pth`; convert it with audio.cpp's model manager before first use:

```powershell
py -3.11 ..\audio.cpp\tools\model_manager.py install voxcpm2_audiovae --source-file .\models\speech\voice-design\VoxCPM2\audiovae.pth --models-root .\models\speech\voice-design --overwrite
```

The same `omnivoice` engine powers Character Voice previews. That path uses an
Actor Voice reference plus the Character Voice direction, so operators who
want previews must configure `omnivoice`; the ordinary `audio` Qwen clone path
does not provide an equivalent instruction control.

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
    "--model", ".\\models\\speech\\synthesis\\Qwen3-TTS-12Hz-0.6B-Base",
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
