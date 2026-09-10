# Expressive speech models

CPP Studio supports these audio.cpp 0.7.3 integrations:

| Model | Studio engine / API model | Available tools |
| --- | --- | --- |
| IndexTTS 2.5 Q8 | `index-tts2.5` | Voice cloning, Voice chat |
| Higgs Audio v3 TTS 4B Q8 | `higgs-audio` | Voice cloning, Voice chat |
| FireRedTTS3 Base Q8 | `fireredtts3-base` | Voice cloning, Voice chat |
| FireRedTTS3 Instruct Q8 | `fireredtts3-instruct` | Voice cloning, Voice chat, Voice design |

FireRed's audio.cpp integration is experimental. IndexTTS and Higgs use a recorded
or saved Actor Voice; they do not invent voices from descriptions. FireRed Instruct
can design an Actor Voice, which can then be saved through the existing Voice Library.
Story Builder's dialogue synthesis remains OmniVoice; these additions do not change
existing Story or Audiobook production identities.

## Install and configure

Run `./scripts/install-expressive-models.ps1` from PowerShell. It downloads the four
pinned GGUF files listed in `models.json` (about 16.9 GB total) into
`models/speech/synthesis`, resumes interrupted transfers, and verifies SHA-256 before
publishing a completed file. The model manifest records repository revision,
download URL, expected size, checksum, and licence.

Merge the four engines in `config.expressive-audio.example.json` into a local config,
or use the example directly for a speech-only studio. The example expects the copied
audio.cpp 0.7.3 runtime under `engines/audio-0.7.3` and the existing sibling audio.cpp
sample reference. Set `defaultVoiceRef` and `defaultVoiceText` to another reference
when appropriate. A selected Library voice overrides those defaults. Engine calls
are serialized through Studio's shared GPU gate.

The configured Windows runtime supports UTF-8 arguments; these four integrations
preserve multilingual text and reference transcripts.

## Direct a performance

For simple text-to-speech, open **Talk & voice → Getting started TTS**. Choose a
model, enter text and any supported inline tags, and press **Play**. The text goes
directly to the speech engine using its default voice or configured reference.
The result plays automatically when permitted by the browser, with replay and
Save WAV controls. Generation can be cancelled; a failed retry retains the prior
result. Use Voice cloning for a saved Actor Voice.

Select a model in **Getting started TTS**, **Voice cloning** or **Voice chat**, then
expand **Speech performance** for optional model-specific controls.

The **Model guide** navigation entry contains short, searchable instructions for
the catalogued model families, with copyable examples, field placement and source
links. Voice-design descriptions remain separate from spoken-text examples.

- **IndexTTS:** describe the desired emotion, set its intensity from 0 to 1, and
  adjust duration (above 1 is slower). Alternatively, leave the description blank
  and supply eight strengths: happy, angry, sad, afraid, disgusted, melancholic,
  surprised, calm. Empty strengths count as zero. Language is selectable.
- **Higgs:** choose emotion, delivery, pace, pitch and expressiveness. Settings
  become native delivery tokens at the start of the utterance. In the spoken text,
  use `<|prosody:pause|>` or `<|prosody:long_pause|>` at a pause, and e.g.
  `<|sfx:laughter|>Haha` or `<|sfx:sigh|>Uh` for nonverbal sounds. Delivery tokens
  control the utterance; pause and sound tokens act where placed.
- **FireRed:** choose the cloning language. Cloning inherits the reference delivery.
  For a new emotional character voice, select **FireRedTTS3 Instruct** in
  **Voice design** and describe its voice, emotion, pace and accent. The supplied
  engine config uses English for design. Speech editing is outside these tools.

Changing models does not submit hidden controls from the previous model.
The server rejects unsupported controls and conflicting IndexTTS descriptions/blends.

## API

`POST /v1/audio/speech` accepts an optional `performance` object:

```json
{
  "model": "index-tts2.5",
  "input": "I am so relieved you are safe.",
  "performance": {
    "language": "en",
    "emotion": "Quiet relief and warmth",
    "intensity": 0.6,
    "duration_factor": 1.1
  }
}
```

For Higgs, the supported fields are `emotion`, `style`, `pace`, `pitch`, and
`expression`, using its native named values. IndexTTS also accepts `emotion_vector`
as an array of eight strengths. FireRed cloning accepts `language` using official
names such as `English` or `Japanese`. Voice chat accepts the same object encoded
as JSON in the multipart `performance` field.

`POST /v1/voices/design` accepts `model: "fireredtts3-instruct"`, `description`, and
optional `sample_text`. It returns the same preview/reference contract as the other
designers, including audio that can be saved as an Actor Voice.

## Sources and quality scope

- [audio.cpp model documentation](https://github.com/0xShug0/audio.cpp/tree/3174e6b26f11a0e39b4f150961dce98f43ba860d/docs/models)
- [IndexTTS reference implementation](https://github.com/index-tts/index-tts)
- [Higgs model card](https://huggingface.co/bosonai/higgs-tts-3-4b)
- [FireRedTTS3 reference implementation](https://github.com/FireRedTeam/FireRedTTS3)

Higgs weights have a research/non-commercial licence. Model-specific licence metadata
is retained in the catalogue. Generation checks establish working integration; they
are not a blind listening comparison against the existing models.

## Local acceptance — 10 September 2026

All four pinned GGUF files passed size and SHA-256 verification, including a rerun
of `scripts/install-expressive-models.ps1` against the completed installation.
`bin/cpp-studio.exe` was rebuilt and `config.real.json` passed its command checks.
The prior executable and local config are retained under `out/expressive-models`.

`scripts/verify.ps1` passed: all Go tests, vet, 23 JavaScript tests, config validation,
and fixture API workflows. Browser checks confirmed IndexTTS controls and playable
output, the FireRed designer choice, and Higgs emotion/delivery controls.

Live CUDA requests through Studio on the RTX 5080 produced these samples:

| Request | Request time | Output duration | WAV rate |
| --- | ---: | ---: | ---: |
| IndexTTS emotion description | 9.08 s | 5.01 s | 22.05 kHz |
| FireRed Base cloning | 4.41 s | 5.94 s | 24 kHz |
| FireRed Instruct cloning | 4.30 s | 4.50 s | 24 kHz |
| FireRed Instruct voice design | 4.50 s | 3.68 s | 24 kHz |
| Higgs relief / whisper / slow delivery | 23.31 s | 6.74 s | 24 kHz |

These are individual integration checks, not comparative benchmarks. First-load
and file-cache costs vary. PCM decoding confirmed non-silent output, and an
independent Whisper base.en CPU transcription recovered the intended spoken words
for all five samples (Higgs's nonverbal sigh was not transcribed as a word).
The IndexTTS emotion-vector CLI path also produced a valid WAV.

Audio, transcripts, generation measurements and logs are in
`out/expressive-models/`. No new Actor Voices were saved to the user's Library.
Subjective emotion quality, speaker fidelity and preference over the existing
models remain listening decisions.

Restart the normal Studio using the rebuilt `bin/cpp-studio.exe` and
`config.real.json`, then refresh its browser page. A direct launch from the project
folder is `./bin/cpp-studio.exe --config ./config.real.json`.

The Getting started TTS browser check generated a 7.46-second playable Higgs WAV
from text containing pause and sigh tags. The request reached `/v1/audio/speech`
directly; the browser decoded the result and exposed its download. The user also
confirmed that the simple TTS page worked well. Focused regressions cover literal
text forwarding, autoplay fallback, errors, cancellation and previous-result
preservation. The model guide passed browser search, copy and navigation checks.
