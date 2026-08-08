# Audio model expansion plan

This plan maps the requested audio.cpp families to the smallest cpp-studio
integration that makes each one usable. The checked-in audio.cpp source,
`model_specs` catalogue, and the deployed `audiocpp_cli.exe --list-loaders`
output are the source of truth.

## Viability

| Requested model | audio.cpp family / task | Viable Studio surface | Constraint |
|---|---|---|---|
| OmniVoice | `omnivoice` / `tts` | Text to speech, voice clone, voice design | Already configured; keep its instruction-aware path. |
| Qwen3 0.6B | `qwen3_tts` / `tts` | Text to speech, voice clone | Already resident as the default audio server model. |
| Qwen3 1.7B Base | `qwen3_tts` / `tts` | Text to speech, voice clone | Add a separate subprocess lane. |
| Qwen3 1.7B CustomVoice | `qwen3_tts` / `tts` | Text to speech with a built-in speaker | This checkpoint is controlled TTS, not arbitrary voice design. |
| VoxCPM2 | `voxcpm2` / `tts` | Text to speech, voice design | Already configured. |
| VibeVoice 1.5B | `vibevoice` / `tts` | Text to speech and dialogue | It is not a reference voice-clone checkpoint. |
| Fish Audio S2 Pro | `fish_audio` / `tts` | Text to speech, voice clone | Reference transcript must match the reference WAV. |
| DramaBox | `dramabox` / `tts`, `clon` | Expressive TTS, voice clone, audiobooks | Already configured; large local model. |
| Chatterbox | `chatterbox` / `clon`, `vc` | Voice clone and conversion | The deployed loader exposes clone/VC, not text-only TTS. |
| Qwen3-ASR 0.6B | `qwen3_asr` / `asr` | Transcribe | Add an offline audio.cpp ASR lane. |
| Qwen3-ASR 1.7B HF | `qwen3_asr` / `asr` | Transcribe | Add an offline safetensors lane. |
| VibeVoice ASR | `vibevoice_asr` / `asr` | Transcribe | Add an offline lane; speaker/segment metadata is model-owned. |
| ACE-Step 1.5 | `ace_step` / `gen` | Music generation and editing | Already configured. |
| Stable Audio 3 Medium | `stable_audio` / `gen` | Music and SFX generation | Add text-to-audio generation; keep ACE-only edit controls disabled. |
| Stable Audio 3 Small SFX | `stable_audio` / `gen` | Sound-effects generation | SFX-focused checkpoint. |
| HeartMuLa 3B | `heartmula` / `gen` | Music generation | Song generation; no ACE edit routes. |
| Chatterbox conversion | `chatterbox` / `vc` | Voice conversion | Already configured. |
| VeVo2 voice conversion | `vevo2` / `vc` | Voice conversion | `style_preserved_vc` route. |
| VeVo2 singing conversion | `vevo2` / `svc` | Singing voice conversion | `style_preserved_svc` route. |
| VeVo2 speech editing | `vevo2` / `s2s` | Speech editing | Requires replacement text as well as source and target WAVs. |
| HTDemucs | `htdemucs` / `sep` | Four-stem separation | Input must be a valid 44.1 kHz WAV. |
| BS-RoFormer | `bs_roformer` / `sep` | Vocal/instrumental separation | Q8 package is the practical default. |
| Mel-Band RoFormer | `mel_band_roformer` / `sep` | Vocal/source separation | Writes named WAV stems. |
| Silero VAD | `silero_vad` / `vad` | Speech-region analysis | Bundled audio.cpp asset; supports offline and streaming. |
| MarbleNet VAD | `marblenet_vad` / `vad` | Speech-region analysis | Bundled audio.cpp asset; offline only. |
| Sortformer 4-speaker diarization | `sortformer_diar` / `diar` | Speaker diarization | Already configured; 16 kHz mono, up to four speakers/120 seconds. |
| Qwen3 Forced Aligner | `qwen3_forced_aligner` / `align` | Word alignment | Requires an exact transcript and language. |

All requested families are viable in the deployed runtime. “Viable” does not
mean “installed”: missing weights remain visible but disabled until their
catalogue artifact is present. Automatic downloads stay restricted to entries
with pinned revision, size, checksum, and URL metadata.

## Implementation slices

1. Add declarative display names and capabilities to `models.json`, retaining
   the current model-store and confirmed-download policy.
2. Add one configured subprocess engine per new local model so the existing
   reservation and shared-GPU gates continue to apply.
3. Resolve request model ids through the catalogue for speech, ASR, music,
   conversion, separation, and analysis instead of accepting arbitrary family,
   model path, task, or command input.
4. Extend the existing Studio selectors and add compact separation/analysis
   workspaces. The browser sends model ids and allowlisted route names only.
5. Verify argument mapping with focused engine/gateway tests, then run the Go,
   JavaScript, configuration, and fixture smoke gates.

## Acceptance

- Every requested model is present in the Models catalogue with its exact
  audio.cpp family and Studio capabilities.
- Installed and configured models are selectable only on compatible tools.
- Missing models are visible but cannot start a job.
- No browser input can supply a model path, native family, executable, flag,
  or arbitrary request option.
- Existing Qwen3 0.6B, Whisper, ACE-Step, Chatterbox, Sortformer, DramaBox,
  voice-library, Story, and Story Builder flows remain compatible.
