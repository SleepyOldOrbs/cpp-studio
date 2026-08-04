# Model layout

CPP Studio keeps executable runtimes and model data at separate seams:

- `engines/` contains programs and their runtime libraries.
- `models/` contains model weights and model-specific assets.
- `models.json` stores paths relative to the configured `models.root`.

The model store is organised by capability:

```text
models/
|-- text/
|-- image/
|   |-- generation/
|   `-- understanding/
|-- speech/
|   |-- synthesis/
|   |-- voice-design/
|   |-- voice-cloning/
|   `-- analysis/
|       |-- VAD/
|       |-- diarization/
|       `-- speaker-embedding/
|-- transcription/
|   `-- Whisper/
|-- music/
|   |-- generation/
|   `-- separation/
`-- sound/
    |-- generation/
    |-- conversion/
    `-- separation/
```

Download staging is an implementation detail of the model installer. It uses a
unique hidden directory beneath `models.root`, verifies the expected size and
checksum, and atomically promotes the result to its catalogued type directory.
There is no separate permanent downloads folder.
