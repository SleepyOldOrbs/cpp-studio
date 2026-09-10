# audio.cpp versus cpp-studio: model coverage audit

Date: 10 September 2026. Audit only; no application changes, model downloads, runtime replacement or inference runs.

## Result

cpp-studio has **26 audio.cpp catalogue entries spanning 20 distinct families**. The installed audio.cpp CLI reports **74 families**. **54 installed-runtime families are absent from Studio's catalogue**. Current upstream main adds **VibeVoice ASR Streaming**, making **55 missing families out of 75 documented families**. This is family coverage, not 55 equally mature ready-to-use downloads.

The 55 missing families have these upstream spec labels: 16 community, 11 experimental, 26 supported, 1 testing, 1 wip. Labels are upstream metadata, not a result of testing the models here.

## Comparison boundaries

- Studio snapshot: commit `1824d7aae3dda526a02cc195b8e1bff1c883fb95`, current checked-out branch, [models.json](../../models.json), UI selectors and gateway/task adapters. This includes the pushed PR #49 work, not just main.
- Installed runtime: `engines/audio-0.7.3/audiocpp_cli.exe`; read-only `--list-loaders --json` returned 74 entries. Local `config.real.json` points the audio routes and discovery at this installation. No engine was started or stopped. [Release v0.7.3](https://github.com/0xShug0/audio.cpp/releases/tag/v0.7.3) was published 7 September; tag commit `9c6a282337cc83f227cc10428867a478947706ad`.
- Latest upstream: [README at 3174e6b](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/README.md), committed 10 September 2026, plus all 73 model-spec JSON files. Two bundled VAD utilities bring the distinct family total to 75. Repeated core/community table rows are deduplicated.
- Local sibling audio.cpp source checkout is older, at `bf3315fe4aaa16dc1125f580c29aff90a8900b36` (31 August). It was inspected but not used as the latest-support baseline and was not updated.
- Qwen3 legacy Studio family strings `qwen3-tts` and `qwen3-tts-voicedesign` are normalized to `qwen3_tts` for this comparison. Model sizes, precision choices and aliases are not counted as separate families.
- Whisper/whisper.cpp, chat/vision, Stable Diffusion and sherpa diarization assets are outside the audio.cpp gap count. Studio's 37 total catalogue rows include 11 such rows.
- A loader being present proves the binary advertises that family; it does not prove weights are installed, model output works, or a specific backend is production-ready. Path existence in the inventory CSV is a fast filesystem check, not checksum or inference validation. The browser's port-52510 fixture preview was not used as real-model availability evidence.

## Complete missing-family list

Every row below is absent from Studio's catalogue. All rows except VibeVoice ASR Streaming have a matching loader in the installed binary. Links point to exact upstream model specifications; status labels are preserved even where README wording sounds more mature.

### Speech and voice design (30)

| Model/family | Tasks | Upstream label | Installed loader |
| --- | --- | --- | --- |
| [Audio8 TTS Preview 0.6B](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/audio8_tts.json) (`audio8_tts`) | TTS, Clone | community | Yes |
| [BreezeTTS 2](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/breeze_tts.json) (`breeze_tts`) | TTS, Clone, Design, Ctrl | supported | Yes |
| [Chatterbox Turbo](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/chatterbox_turbo.json) (`chatterbox_turbo`) | TTS (testing) | testing | Yes |
| [Confucius4-TTS](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/confucius4_tts.json) (`confucius4_tts`) | Clone | experimental | Yes |
| [CosyVoice3](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/cosyvoice3.json) (`cosyvoice3`) | TTS, Clone | supported | Yes |
| [DotTTS](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/dots_tts.json) (`dots_tts`) | TTS, Clone, Edit, Ctrl | supported | Yes |
| [Echo-TTS](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/echo_tts.json) (`echo_tts`) | Clone | experimental | Yes |
| [F5-TTS](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/f5_tts.json) (`f5_tts`) | TTS, Clone | community | Yes |
| [FireRedAudio](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/firered_audio.json) (`firered_audio`) | ASR, TTS, Clone, Design, Ctrl | experimental | Yes |
| [FireRedTTS3](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/fireredtts3.json) (`fireredtts3`) | TTS, Clone, Design, Ctrl | experimental | Yes |
| [GLM-TTS](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/glm_tts.json) (`glm_tts`) | TTS, Clone | community | Yes |
| [Higgs Audio v3 TTS](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/higgs_audio_tts.json) (`higgs_audio_tts`) | TTS, Clone, Ctrl | supported | Yes |
| [IndexTTS2](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/index_tts2.json) (`index_tts2`) | TTS, Clone, Ctrl | supported | Yes |
| [Inflect Micro v2](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/inflect_v2.json) (`inflect_v2`) | TTS | community | Yes |
| [Irodori-TTS](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/irodori_tts.json) (`irodori_tts`) | TTS, Clone, Design, Ctrl | supported | Yes |
| [MagpieTTS Multilingual 357M](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/magpie_tts.json) (`magpie_tts`) | TTS | supported | Yes |
| [MioTTS](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/miotts.json) (`miotts`) | TTS, Clone | supported | Yes |
| [MiraTTS](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/mira_tts.json) (`mira_tts`) | TTS, Clone | experimental | Yes |
| [MOSS-TTS-Local](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/moss_tts_local.json) (`moss_tts_local`) | TTS, Clone, Ctrl | supported | Yes |
| [MOSS-TTS-Nano](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/moss_tts_nano.json) (`moss_tts_nano`) | TTS, Clone | supported | Yes |
| [MOSS-VoiceGenerator](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/moss_voicegen.json) (`moss_voicegen`) | Voice Design | community | Yes |
| [NeuTTS](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/neutts.json) (`neutts`) | TTS, Ctrl | supported | Yes |
| [Llama-OuteTTS 1.0](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/outetts.json) (`outetts`) | TTS, Clone | community | Yes |
| [PocketTTS](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/pocket_tts.json) (`pocket_tts`) | TTS, Clone | supported | Yes |
| [sanoTTS Nano](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/sanotts.json) (`sanotts`) | TTS | community | Yes |
| [Soprano](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/soprano_tts.json) (`soprano_tts`) | TTS | community | Yes |
| [Sopro V2 Turbo](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/sopro_tts.json) (`sopro_tts`) | TTS, Clone | community | Yes |
| [Supertonic 3](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/supertonic.json) (`supertonic`) | TTS | supported | Yes |
| [VieNeu-TTS v3 Turbo](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/vietneu_tts.json) (`vietneu_tts`) | TTS, Clone | community | Yes |
| [VoxCPM1](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/voxcpm1.json) (`voxcpm1`) | TTS, Clone | supported | Yes |

### Transcription (13)

| Model/family | Tasks | Upstream label | Installed loader |
| --- | --- | --- | --- |
| [Audio8-ASR-0.1B](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/audio8_asr.json) (`audio8_asr`) | ASR | community | Yes |
| [Citrinet ASR](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/citrinet_asr.json) (`citrinet_asr`) | ASR | supported | Yes |
| [Fun-ASR-Nano](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/fun_asr_nano.json) (`fun_asr_nano`) | ASR | wip | Yes |
| [Granite Speech 5.0 470M TurboCTC](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/granite5asr.json) (`granite5asr`) | ASR | supported | Yes |
| [Higgs Audio v3 STT](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/higgs_audio_stt.json) (`higgs_audio_stt`) | ASR | supported | Yes |
| [Hviske ASR](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/hviske_asr.json) (`hviske_asr`) | ASR | supported | Yes |
| [Kroko Community ASR](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/kroko_asr.json) (`kroko_asr`) | ASR | community | Yes |
| [Nemotron 3.5 ASR](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/nemotron_asr.json) (`nemotron_asr`) | ASR | supported | Yes |
| [Parakeet-TDT 0.6B v3](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/parakeet_tdt.json) (`parakeet_tdt`) | ASR | community | Yes |
| [SenseVoice-Small](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/sense_asr.json) (`sense_asr`) | ASR | community | Yes |
| [VibeVoice-ASR-BitNet](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/vibeasr.json) (`vibeasr`) | ASR | community | Yes |
| [VibeVoice ASR Streaming 7B](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/vibevoice_asr_streaming.json) (`vibevoice_asr_streaming`) | ASR | supported | No; newer upstream |
| [Voxtral Mini 4B Realtime](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/voxtral_realtime.json) (`voxtral_realtime`) | ASR | supported | Yes |

### Voice conversion (4)

| Model/family | Tasks | Upstream label | Installed loader |
| --- | --- | --- | --- |
| [MeanVC2](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/meanvc2.json) (`meanvc2`) | VC | supported | Yes |
| [MioCodec](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/miocodec.json) (`miocodec`) | Codec, VC | supported | Yes |
| [RVC](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/rvc.json) (`rvc`) | VC | experimental | Yes |
| [Seed-VC](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/seed_vc.json) (`seed_vc`) | VC | supported | Yes |

### Music, sound and video (4)

| Model/family | Tasks | Upstream label | Installed loader |
| --- | --- | --- | --- |
| [ControlFoley](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/controlfoley.json) (`controlfoley`) | SFX | experimental | Yes |
| [MiDashengLM-Gen](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/midashenglm_gen.json) (`midashenglm_gen`) | Music, SFX | experimental | Yes |
| [MiniMax-H3](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/minimax_h3.json) (`minimax_h3`) | Video, Music, TTS/Dialogue | experimental | Yes |
| [MiniMax Music 3](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/minimax_music3.json) (`minimax_music3`) | Music | experimental | Yes |

### Enhancement and MIDI (2)

| Model/family | Tasks | Upstream label | Installed loader |
| --- | --- | --- | --- |
| [AudioSR](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/audiosr.json) (`audiosr`) | S2S | experimental | Yes |
| [MuScriptor](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/muscriptor.json) (`muscriptor`) | MIDI | supported | Yes |

### Conversation (1)

| Model/family | Tasks | Upstream label | Installed loader |
| --- | --- | --- | --- |
| [PersonaPlex](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/personaplex.json) (`personaplex`) | Dialogue, S2S | supported | Yes |

### Alignment (1)

| Model/family | Tasks | Upstream label | Installed loader |
| --- | --- | --- | --- |
| [Meta MMS-300M Forced Aligner](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/mms_forced_aligner.json) (`mms_forced_aligner`) | Align | community | Yes |

## Already represented

`ace_step`, `bs_roformer`, `chatterbox`, `dramabox`, `fish_audio`, `heartmula`, `htdemucs`, `marblenet_vad`, `mel_band_roformer`, `omnivoice`, `qwen3_asr`, `qwen3_forced_aligner`, `qwen3_tts`, `silero_vad`, `sortformer_diar`, `stable_audio`, `vevo2`, `vibevoice`, `vibevoice_asr`, `voxcpm2`.

The [Studio inventory CSV](audio-model-studio-inventory-20260910.csv) records all 26 rows, engine configuration, declared capabilities, raw family names and fast path presence. All 26 entries have a configured engine and an existing model path in the current local setup. This was a fast path-existence check, not checksum or inference validation. Represented does not mean every upstream feature is exposed.

## Missing variants within represented families

| Existing family | Studio presently lists | Missing meaningful variant/package |
| --- | --- | --- |
| Stable Audio 3 | Medium and Small SFX | **Small Music**; separate music-specialized variant. |
| VibeVoice TTS | 1.5B | **7B**. The newer streaming ASR is a separate family, already counted above. |
| ACE-Step 1.5 | Turbo | **Base, XL Turbo and XL SFT**; XL packages have distinct planner/DiT precision combinations. |
| Qwen3 ASR | 0.6B Q8 and 1.7B HF/Safetensors | **1.7B GGUF Q8/F16** packages; not a missing 1.7B model family. |
| HTDemucs | HTDemucs Q8 | README also names **HTDemucs_ft**, but current model spec declares only ordinary HTDemucs Q8/F16 packages. Treat _ft as a documented variant requiring package verification, not a ready install entry. |
| Existing GGUF families | Generally one selected Q8 package | Alternative F16/BF16/original formats and precisions are mostly unlisted. These are package choices, not additional model families. |

Package evidence: [Stable Audio spec](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/stable_audio.json), [VibeVoice spec](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/vibevoice.json), [ACE-Step spec](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/ace_step.json), [Qwen3 ASR spec](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/qwen3_asr.json), [HTDemucs spec](https://github.com/0xShug0/audio.cpp/blob/3174e6b26f11a0e39b4f150961dce98f43ba860d/model_specs/htdemucs.json). The [package inventory CSV](audio-model-packages-20260910.csv) has 222 declared upstream packages and exact Studio package-ID matches. An unmatched package ID can be another format of a model already present, or a legacy Studio entry without packageId; do not read that column as a missing-model count.

## Capability and integration gaps

1. **Runtime discovery does not populate the catalogue.** [handleModelsCatalog](../../internal/gateway/gateway.go) annotates the fixed manifest with discovery status. It does not add all discovered loaders/packages. Upgrading audio.cpp therefore leaves Studio's selection largely unchanged.
2. **Installation is narrower than catalogue coverage.** Local configuration explicitly allowlists only `dramabox_q8_0`, `chatterbox_q8_0`, and `ace_step_turbo_q8_0` for the guarded installer. Other installed/manual models can still work, but new download buttons require immutable metadata and the appropriate allowlist entry; merely adding a package to models.json is insufficient. Two Chatterbox rows share one package.
3. **Product selectors impose further limits.** Main speech and cloning selectors include explicit engine lists; Voice Design has three hard-coded choices. Podcast uses only the `audio` speech engine, Story Builder synthesizes through the fixed OmniVoice lane while reserving both audio and OmniVoice, and Audiobook accepts only `audio` or `dramabox`. Catalogue presence alone does not make every TTS family selectable in those production tools. See [index.html](../../internal/demo/static/index.html), [app.js](../../internal/demo/static/app.js), [gateway.go](../../internal/gateway/gateway.go) and [audiobook manager](../../internal/audiobook/manager.go).
4. **Existing model capabilities are also hidden.** VoxCPM2 is tagged speech/design, not voice_clone, and excluded from the clone selector although upstream documents cloning. Chatterbox's clone entry is not in the ordinary speech selector. Vevo2 is represented for conversion/singing conversion/editing rather than all its upstream synthesis modes. Stable Audio editing/init-audio/inpainting is blocked by Studio's explicit non-ACE text-generation-only guard. These need targeted route/option validation, not another family entry.
5. **Streaming is an integration gap.** Upstream advertises streaming for several already-present and missing families. Studio's recorded/uploaded-audio request flows do not expose the corresponding live ASR/TTS session controls; listing a model does not add live partial transcripts or low-latency speech playback. Likewise PersonaPlex conversation, MuScriptor MIDI, AudioSR enhancement and multimodal Foley/video need appropriate task workflows.
6. **Legacy family naming can misreport loader support.** Studio's loader check is an exact string comparison. The legacy Qwen3 hyphenated names do not match the installed `qwen3_tts` loader, despite representing the same family. This is a readiness-reporting discrepancy, not a missing Qwen family.

## Upstream qualifications that matter

- **Chatterbox Turbo** is marked testing; **Fun-ASR-Nano** is marked wip in its model spec despite positive support text in README. Eleven missing families are marked experimental. Keep those labels visible during any later implementation.
- **Audio8 ASR**: its spec marks GGUF downloading unsupported/local-conversion-only; the original checkpoint is separately described. **MiraTTS**: current spec has no package entries and README describes local conversion. **VibeASR**: separate CPU-only BitNet/conversion path, not a synonym for the existing VibeVoice ASR.
- **VibeVoice ASR Streaming** is the one current upstream family absent from the installed v0.7.3 loader list. It needs a suitable newer build before Studio integration. Irodori's new Anime model is a variant within a loader already present; do not count it as another family.
- No model-weight downloads, licences, CUDA compatibility or real-quality benchmarks were revalidated here. Model/spec status is recorded evidence, not an endorsement.

## Suggested order for a later implementation

These are integration priorities inferred from existing Studio workflows, not quality rankings or measured latency claims.

1. **Fill smaller existing-family gaps:** Stable Audio Small Music; VibeVoice 7B; Qwen3 ASR 1.7B GGUF; ACE-Step Base/XL choices as separate evaluated packages. Fix catalogue/readiness naming and expose only compatible per-tool choices.
2. **Evaluate responsive speech options:** Supertonic 3, PocketTTS and Soprano first; then broader voices such as Higgs Audio, IndexTTS and CosyVoice3. Benchmark common text/reference inputs before choosing defaults.
3. **Expand transcription:** Parakeet-TDT, Nemotron 3.5 ASR and Voxtral Realtime, then SenseVoice and specialist languages. Separate offline integration from a later streaming workflow.
4. **Evaluate new creative routes separately:** MiniMax Music 3, MiDashengLM-Gen, FireRed variants and MOSS-VoiceGenerator. Their model-spec maturity and request contracts require dedicated acceptance. PersonaPlex, MuScriptor, AudioSR, ControlFoley and MiniMax-H3 are larger feature additions.

No implementation or GitHub publication is part of this audit.

## Data and reproducibility

- [All 75 families](audio-model-families-20260910.csv)
- [The 55 missing families](audio-models-missing-20260910.csv)
- [All upstream package declarations](audio-model-packages-20260910.csv)
- [Studio audio-model inventory](audio-model-studio-inventory-20260910.csv)

Raw README, pinned specs, installed loader advertisements and the generation script are retained locally under `out/audio-model-audit-20260910/`. The model catalogue/application/runtime configurations were not changed.
