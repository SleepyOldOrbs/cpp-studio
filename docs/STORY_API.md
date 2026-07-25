# Story API Contract

`cpp-studio` story routes create short, factual, multi-speaker story artifacts
from Human-supplied source excerpts. V1 is deterministic and local-first: no
autonomous web search, no URL fetching, no bundled model weights, and no phone
inference.

The gateway remains `127.0.0.1` by default. LAN/APK mode is a later slice and
must explicitly document route exposure and Windows firewall setup before use.

## V1 Scope

Included:

- Curated pasted excerpts only.
- One active story job per gateway process.
- Fixture story planning and target-duration fixture WAV output.
- Local artifacts under `out/stories/<story-id>/`.
- Browser and API clients can poll status, cancel active work, list retained
  stories, and play `story.wav`.

Deferred:

- Autonomous search, URL fetching, source ranking, arXiv/Wikipedia selection,
  paywall handling, and login-only pages.
- Real multi-voice acoustic output through `audio.cpp`.
- MP3, streaming, cloud libraries, accounts, remote access, TLS, QR pairing,
  and mDNS discovery.
- General automatic scientific conflict detection. V1 can carry explicit
  `conflicting: true` fact-card flags and deterministic fixture cases.

## Limits

Story routes use their own limits instead of inheriting the smaller chat,
speech, and image JSON limits:

- Request body: 512 KiB.
- Subject: 1-200 characters after trimming.
- Target seconds: 30-300 when supplied.
- Sources: 3-5 entries (grounded mode; sketch mode takes none).
- Premise: max 2000 characters. Style notes: max 1000 characters.
- Source title: 1-200 characters after trimming.
- Source URL: optional metadata only, max 2048 characters.
- Source excerpt: 1-12000 characters after trimming.
- Script line text: 1-2000 characters.
- Generated WAV artifact: 32 MiB.

URLs are never fetched in V1. A source with only a URL and no excerpt is
invalid.

## Error Shape

Existing engine routes use:

```json
{ "error": "message" }
```

Story routes intentionally use a richer shape:

```json
{
  "error": {
    "code": "insufficient_sources",
    "message": "Need at least 3 usable source excerpts to generate a factual story."
  }
}
```

Status codes:

- `400`: invalid request, unsupported mode, unsupported source mode,
  unsupported voice mode, missing excerpts, insufficient sources, an
  unusable script (`invalid_script`), or over-limit input.
- `404`: story id or whitelisted artifact not found.
- `405`: wrong method, with `Allow` set.
- `409`: cancellation requested after a story is already complete or failed.
- `429`: a story job or reserved engine is busy.
- `500`: local store or serialization failure.
- `502`: future real-engine subprocess failure.
- `503`: future required engine is unavailable.

## POST /v1/stories

Starts one local story job.

Request:

```json
{
  "subject": "how stars are born",
  "target_seconds": 90,
  "source_mode": "curated",
  "voice_mode": "placeholder",
  "sources": [
    {
      "id": "src-1",
      "title": "NASA Science: Star Basics",
      "url": "https://science.nasa.gov/universe/stars/",
      "excerpt": "Stars form inside molecular clouds of gas and dust. Cold cloud conditions help gas clump into denser pockets. As clumps gain mass, gravity can make them collapse."
    },
    {
      "id": "src-2",
      "title": "NASA Webb: Fiery Hourglass",
      "url": "https://science.nasa.gov/missions/webb/nasas-webb-catches-fiery-hourglass-as-new-star-forms/",
      "excerpt": "A forming protostar gathers material from its surrounding molecular cloud. Falling material spirals inward and forms an accretion disk. The disk feeds material onto the protostar."
    },
    {
      "id": "src-3",
      "title": "NASA Hubble: Planet-Forming Disks",
      "url": "https://science.nasa.gov/missions/hubble/hubbles-album-of-planet-forming-disks/",
      "excerpt": "Some falling material forms a rotating disk around the protostar. Jets from magnetic poles are part of star formation. Jets help carry away angular momentum so material can continue collecting."
    }
  ]
}
```

Supported fields:

- `mode` must be omitted, `grounded`, or `sketch`. `grounded` (the default)
  is the contract above: 3-5 sources, fact cards, every line cited.
  `sketch` is the opposite contract for fiction — see "Sketch mode" below.
- `premise` and `style` (sketch only, both optional): the situation to play
  and the tone to play it in. Max 2000 and 1000 characters. Ignored — and
  dropped from the manifest — in grounded mode.
- `source_mode` must be omitted or `curated` in grounded mode. Sketch mode
  ignores the field and reports `none`.
- `voice_mode` must be omitted, `placeholder`, or `fixed`. `placeholder` (the
  default) produces the deterministic synthetic tone. `fixed` synthesizes
  every script line through the configured `audio` engine and stitches the
  clips (350 ms gaps) into the story WAV; the manifest's `duration_seconds`
  is updated to the real stitched length, and synthesis failures end the job
  with code `synthesis_failure`.
- `sources[].url` is attribution metadata only.
- `cast` (optional): 2-6 speakers, each `{"name", "role", "voice_id"}` (plus
  optional explicit `id`; ids otherwise slug from names). Empty means the
  default trio narrator/nova/dr-lumen. `role` steers the script writer;
  `voice_id` names a stored voice from `/v1/voices` (validated at submit),
  empty meaning the studio default voice. In `fixed` mode each line is
  synthesized with its speaker's voice.
- `cast_voices` (optional): `{speaker_id: voice_id}` — an alternative way to
  assign voices to cast member ids.
- `title` + `script` (optional): when `script` is non-empty the job produces
  exactly this script (the draft → edit → produce flow) instead of writing
  one. Lines must cite fact ids derived from the same `sources`, or the job
  fails with `grounding_failure`. Max 60 lines.

Script writing: with a `llama` engine configured, the story script is written
by the model, grounded in the fact cards, with one corrective retry before
failing with `grounding_failure`. Without `llama`, the deterministic fixture
script is used.

### Sketch mode

`"mode": "sketch"` swaps the writing contract so a cloned cast can perform
new material. What changes:

- **No sources.** `sources` is not required and any entries sent are
  dropped; `source_mode` reports `none`. The manifest carries no sources,
  no source notes, and no fact cards.
- **No grounding.** Script lines carry no `fact_ids`; ids the model invents
  out of habit are stripped rather than rejected. Lines are still checked
  for shape — non-empty text, within the length limit, and a `speaker_id`
  that is in the cast — failing with code `invalid_script`.
- **Premise instead of facts.** The script prompt is built from `subject`
  (the one-line premise), `premise` (extra detail), `style`, and the cast
  roles.
- **Stages.** A sketch never reports `extracting_sources`; it starts at
  `planning`.
- **The manifest records it.** `mode`, `premise`, and `style` are stored, so
  a retained sketch still says which rules wrote it. Manifests written
  before this field existed have no `mode` and read as grounded.

Everything else is unchanged: cast, cast voices, `voice_mode`, the draft →
edit → produce flow, jobs, cancellation, and the library.

```json
{
  "subject": "a shop that only sells apologies",
  "mode": "sketch",
  "premise": "A customer wants to return an apology that did not fit.",
  "style": "1960s BBC radio comedy: fast, silly, groan-worthy puns.",
  "target_seconds": 60,
  "voice_mode": "fixed",
  "cast": [
    {"name": "Kenneth", "role": "the browbeaten customer", "voice_id": "voice_20260724_090216_73ca8a"},
    {"name": "Hugh", "role": "the evasive shopkeeper", "voice_id": "voice_20260723_150438_710ef0"}
  ]
}
```

Immediate response:

```json
{
  "id": "story_20260706_130000_001",
  "status": "queued",
  "status_url": "/v1/stories/story_20260706_130000_001"
}
```

Concurrency:

- If another story job is active, the route returns `429` with code
  `story_busy`.
- A story job reserves the shared `audio` engine lock while it runs so direct
  `/v1/audio/speech` calls cannot race story synthesis.

## POST /v1/stories/draft

Writes a story without producing it: same request shape as `POST
/v1/stories`, synchronous response, no audio engine reservation, no stored
artifact. Runs fine while a story job is active.

Response:

```json
{
  "subject": "how stars are born",
  "title": "The Birth of a Star",
  "sources": [ ... ],
  "source_notes": [ ... ],
  "fact_cards": [ ... ],
  "cast": [ ... ],
  "script": [ {"speaker_id": "narrator", "text": "...", "fact_ids": ["fact-1"]} ]
}
```

The intended flow: draft, let the user edit `title`/`script` (speakers and
text; citations travel with each line), then `POST /v1/stories` with the
edited `title` + `script` to produce audio.

## GET /v1/stories

Lists retained local story manifests from `out/stories/`.

Response:

```json
{
  "stories": [
    {
      "id": "story_20260706_130000_001",
      "subject": "how stars are born",
      "title": "The Nursery of Stars",
      "status": "complete",
      "created_at": "2026-07-06T13:00:00Z",
      "duration_seconds": 90,
      "artifact_url": "/v1/stories/story_20260706_130000_001/artifact/story.wav"
    }
  ]
}
```

Partial or corrupt story directories are ignored by the list response unless
they are still active in memory.

## GET /v1/stories/{id}

Returns current in-memory status for active jobs, or the retained manifest for
completed jobs.

Job states:

```text
queued -> extracting_sources -> planning -> scripting -> synthesizing -> stitching -> complete
queued -> ... -> failed
queued -> ... -> cancelled
```

In-progress response:

```json
{
  "id": "story_20260706_130000_001",
  "status": "scripting",
  "stage": "scripting",
  "progress": 0.55,
  "error": null,
  "artifact_url": null,
  "retry_after_ms": 500
}
```

Completed response:

```json
{
  "id": "story_20260706_130000_001",
  "status": "complete",
  "stage": "complete",
  "progress": 1,
  "error": null,
  "artifact_url": "/v1/stories/story_20260706_130000_001/artifact/story.wav",
  "manifest": {
    "id": "story_20260706_130000_001",
    "subject": "how stars are born",
    "title": "The Nursery of Stars"
  }
}
```

## POST /v1/stories/{id}/cancel

Requests cancellation.

Behavior:

- Active job: status becomes `cancelled`.
- Already `complete` or `failed`: `409` with code `cannot_cancel`.
- Unknown id: `404`.

Cancellation may leave a temporary or partial directory on disk. It must not
write a completed `manifest.json` unless the story finished successfully.

## The take room

A produced story keeps each line's own recording, so one bad read can be
replaced without regenerating the episode.

- **Takes.** Every line has a stable `id` and a list of `takes`, each with
  its own `id`, the `voice_id` it was spoken in, the exact `text` at the
  time, a duration, and a `url`. `current_take` names the one that renders.
  Retaking adds a take; nothing is ever overwritten.
- **Production settings.** `muted` drops a line from renders without
  deleting it; `gap_before_ms` / `gap_after_ms` adjust its timing on top of
  the default 350 ms inter-line gap (each between -500 and 3000).
- **Renders are immutable, and they explain themselves.** Each render is a
  numbered revision under `renders/`, listed in the manifest's `renders`
  with a `recipe`: the exact take id, voice, words and timing of every line
  that went into it. The script stays editable afterwards, so without the
  snapshot an "immutable" revision would be immutable bytes with no
  surviving account of what they are. `story.wav` always mirrors the latest.
- **Editing the words invalidates the recording.** Changing a line's `text`
  deselects its current take — every existing take says the old words. The
  takes stay on disk, but the line contributes nothing to a render until it
  is retaken. Selecting or rendering a take whose recorded text no longer
  matches its line fails with `stale_take`.
- **Mutations are serialized per story.** Retake, line edits and renders
  take a per-story lock, so two retakes cannot mint the same take id and two
  renders cannot claim the same revision.

Placeholder-tone stories have no per-line audio, so they have no takes.

### POST /v1/stories/{id}/lines/{line_id}/takes

Resynthesizes one line in its speaker's voice, appends the result as a new
take, and makes it current. Reserves the `audio` engine for the duration
(`429` with `engine_busy` if it is held). Returns `{"take": ..., "manifest": ...}`.
Unknown line ids return `400` with `line_not_found`.

### PATCH /v1/stories/{id}/lines/{line_id}

Edits one line without touching audio. Omitted fields are left alone:

```json
{ "text": "A better joke.", "current_take": "take-002", "muted": false, "gap_after_ms": 250 }
```

Returns `{"manifest": ...}`. Changing `text` clears `current_take` (see
above). Selecting a take the line does not have returns `400` with
`take_not_found`; selecting one recorded against different words returns
`stale_take`; empty text or out-of-range timing returns `invalid_request`.

### POST /v1/stories/{id}/render

Restitches the story from every line's current take, honouring mutes and
per-line timing, and publishes it as the next revision with its recipe.
Returns `{"render": ..., "manifest": ...}`. A muted line contributes
nothing at all — not its audio and not its timing. A story with nothing
left to render returns `400` with `nothing_to_render`; a line whose selected
take was recorded against different words returns `stale_take`.

## GET /v1/stories/{id}/artifact/{path}

Serves whitelisted story artifacts.

Whitelist:

- `story.wav` — the current mix.
- `lines/{line_id}/{take_id}.wav` — one take.
- `renders/render-NNN.wav` — one published revision.

Every id is validated the same way a story id is, and the route rejects path
traversal, absolute paths, unknown filenames, missing files, and non-WAV
content.

Response:

- `Content-Type: audio/wav`.
- Body: WAV bytes.

## Manifest Shape

Completed stories write `manifest.json` last, after `story.wav` is complete.
The store writes into a temporary directory and renames it to
`out/stories/<story-id>` only after the final manifest succeeds.

```json
{
  "id": "story_20260706_130000_001",
  "subject": "how stars are born",
  "title": "The Nursery of Stars",
  "status": "complete",
  "created_at": "2026-07-06T13:00:00Z",
  "duration_seconds": 90,
  "sources": [
    {
      "id": "src-1",
      "title": "NASA Science: Star Basics",
      "url": "https://science.nasa.gov/universe/stars/"
    }
  ],
  "source_notes": [
    {
      "id": "note-1",
      "source_id": "src-1",
      "text": "Stars form inside molecular clouds of gas and dust."
    }
  ],
  "fact_cards": [
    {
      "id": "fact-1",
      "source_note_ids": ["note-1"],
      "claim": "Stars form inside molecular clouds of gas and dust.",
      "conflicting": false
    }
  ],
  "cast": [
    {
      "id": "narrator",
      "display_name": "Narrator",
      "voice_id": "fixture-narrator"
    },
    {
      "id": "nova",
      "display_name": "Nova",
      "voice_id": "fixture-character-a"
    },
    {
      "id": "dr-lumen",
      "display_name": "Dr. Lumen",
      "voice_id": "fixture-character-b"
    }
  ],
  "script": [
    {
      "speaker_id": "narrator",
      "text": "In the dark between stars, the story begins inside the sources: a cold place where gas and dust can gather.",
      "fact_ids": ["fact-1", "fact-2"]
    },
    {
      "speaker_id": "nova",
      "text": "So a star does not begin as a spark. It begins as a cloud?",
      "fact_ids": ["fact-1"]
    },
    {
      "speaker_id": "dr-lumen",
      "text": "Exactly. The source notes point to dense pockets where gravity can take over and collapse the material inward.",
      "fact_ids": ["fact-2", "fact-3"]
    }
  ],
  "audio": {
    "format": "wav",
    "url": "/v1/stories/story_20260706_130000_001/artifact/story.wav"
  }
}
```

Every factual script line must reference existing, non-conflicting fact-card
ids. The fixture planner is deterministic so CI can verify this without model
downloads. A sketch manifest instead carries `"mode": "sketch"` with
`premise`/`style`, empty `sources`/`source_notes`/`fact_cards`, and script
lines with no `fact_ids`.
