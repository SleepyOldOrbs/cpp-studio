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
- Sources: 3-5 entries.
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

- `400`: invalid request, unsupported source mode, unsupported voice mode,
  missing excerpts, insufficient sources, or over-limit input.
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

- `source_mode` must be omitted or `curated`.
- `voice_mode` must be omitted, `placeholder`, or `fixed`. `placeholder` (the
  default) produces the deterministic synthetic tone. `fixed` synthesizes
  every script line through the configured `audio` engine in one voice and
  stitches the clips (350 ms gaps) into the story WAV; the manifest's
  `duration_seconds` is updated to the real stitched length, and synthesis
  failures end the job with code `synthesis_failure`.
- `sources[].url` is attribution metadata only.

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

## GET /v1/stories/{id}/artifact/{filename}

Serves whitelisted story artifacts.

V1 whitelist:

- `story.wav`

The route rejects path traversal, absolute paths, unknown filenames, missing
files, and non-WAV content for `story.wav`.

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
downloads.
