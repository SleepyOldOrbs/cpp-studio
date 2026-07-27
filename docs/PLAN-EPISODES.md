# Plan: episodes, and what comes after

Written 2026-07-25 at the end of a long session, to be picked up cold in a
new one. `main` is at `2fa643f`, pushed, CI green on Windows and Ubuntu.

## Where the project is

cpp-studio is a Go gateway fronting native `.cpp` engines behind OpenAI-shaped
routes and an embedded browser console. The destination James named is the
**comedy pipeline**: upload an audio comedy, clone its cast, then write and
produce original episodes with those voices.

Every piece of that loop now exists and has been verified against real
engines:

- **In:** URL import via user's yt-dlp, or any file — with `ffmpeg`
  configured, formats the browser refuses are converted automatically.
- **Extract:** waveform, whisper transcript timeline, automatic speaker
  diarization, Clone the cast (voices carry source provenance).
- **Write:** Story desk in `sketch` mode — premise and style in, no sources,
  no grounding, cast roles honoured.
- **Produce:** per-line takes on disk, retake, rewrite a line's text, choose
  between takes, mute, per-line timing, immutable numbered render revisions
  each carrying a recipe.
- **Finish:** mastering (speakers levelled, piece placed at -16 LUFS under a
  -1.5 dBTP ceiling, all values measured), MP3/Opus export.

## The wall this plan exists to break

**Everything in "Produce" and "Finish" lives on a path that cannot make an
episode.** Verified in the code, not remembered:

| | Story path | Audiobook path |
|---|---|---|
| Length | `MaxTargetSeconds = 300`, `MaxScriptLines = 60`, `MaxGeneratedWAVBytes = 32 MiB` | unbounded |
| Cast | yes, 2-6 speakers | **none** — zero occurrences of `Cast` in `internal/audiobook` |
| Takes / mastering / exports / revisions | yes | **none** — one `book.wav` and a flat manifest |

A 28-minute Round the Horne episode is roughly 330 lines. The story path caps
at 60 lines and five minutes; the only long-form path is single-narrator with
no production layer at all. So the studio can extract a cast and write them
material, and then cannot produce the thing it exists to produce.

---

## Stage 1 — Make an episode a first-class thing

### The merge direction (recommendation, with a correction)

At the end of the session I guessed it would be easier to lift the story
production layer onto the audiobook path. **Reading the code says the
opposite, and the opposite is right.**

- `internal/audiobook` is 309 lines of manager with a flat `Manifest`
  (id/title/voiceId/chunks/duration/createdAt/artifactUrl). There is no
  script model, no line identity, no cast. Almost nothing to preserve.
- `internal/story` carries the entire production model: `ScriptLine` with
  stable ids, `Take`, `Render` with `Recipe` and `Master`, `Export`, the
  per-story mutation lock, the artifact whitelist, the take room UI.

So: **extend the story path to long form, and move audiobook's *ingest* into
it.** `internal/audiobook/ingest.go` (240 lines: `.txt`/`.md`/`.epub` →
sentence-boundary chunks) is the genuinely valuable half and it is
self-contained. Rebuilding takes/renders/mastering/exports inside audiobook
would be rebuilding the last two days.

Open question worth 20 minutes before committing to it: does the audiobook
path keep a separate front door (upload a document, one narrator, no take
room) or become "an episode with one speaker"? Leaning toward the latter —
one spine, fewer concepts — but check what the Audiobook tab would lose.

### What has to change

1. **Caps.** `MaxTargetSeconds`, `MaxScriptLines`, `MaxGeneratedWAVBytes` in
   `internal/story/types.go`. These are not one-line edits: they exist to
   bound memory and request size, and raising them is only safe after (2).
2. **Stop buffering.** `Manager.synthesizeScript` holds every clip in memory
   and `retainFirstRender` writes takes only after the whole story is
   stored. At 330 lines that is hundreds of megabytes held for the duration,
   and any failure loses all of it. Takes must be written as they are made.
3. **Resumable production.** Necessary at this length rather than nice.
   The prior review's warning stands: line id + text is **not** a sufficient
   cache key — an engine or model change between runs would splice an
   inconsistent episode together silently. `Take` needs an engine/model
   fingerprint, and a work-in-progress directory needs to exist before the
   final story directory does (today `SaveTake` assumes the finished story
   dir).
4. **Scene structure.** 330 flat lines is not a usable take room and
   probably not a usable script. Scenes are the natural unit: a sketch is a
   scene, an episode is several. This also gives the writer something to
   work at — generating 330 good lines in one llama call will not work;
   generating a scene at a time will.
5. **Single-active-job.** `Manager.Submit` allows one story at a time. That
   is fine for 40 seconds and questionable for 30 minutes.

### Where it will hurt

The take room UI is a flat list — fine at 8 lines, unusable at 330. Expect
scene grouping, collapse, and "jump to the next unrendered line" to become
necessary rather than nice. Budget real time for this; it is the surface
James will actually live in.

---

## Stage 2 — Produce a real one, and let a stranger run it

**Make an actual episode.** This is the honest end-to-end test — everything
so far has been verified in pieces, and the pieces have repeatedly hidden
things that only appeared in live use (three separate bugs this session,
including a CSS rule that had been hiding a broken panel for weeks). It also
produces the artifact the README has been missing: an MP3 of a cast you
cloned performing material you wrote. Screenshots show the studio; an audio
file *is* the demo.

**Then the stranger problem.** Still true after two rounds of deferral: to
run this you need ~15 GB of weights whose `models.json` `source` values are
all landing pages rather than downloadable objects, four of which carry no
checksum at all — plus engine binaries you must find and build yourself.
The corrected shape is a **versioned reference pack** (resolved URLs,
revisions, per-file digests, archive members, and the engine binaries), not
a download button. Weights alone do not unblock anyone.

This is the whole of the "shareable" goal, and it is the largest remaining
gap between a repo people admire and one they run.

---

## Stage 3 — A fork

**Sound design** *(recommended)*. Music beds, stings, scene transitions,
audience laughter. Radio comedy has all of it and this has none. It became
much cheaper this session: mixing needs gain (`wav.ApplyGain` exists) and
asset conversion needs ffmpeg (an engine now). Serves the destination James
actually named, and makes an episode sound like a show rather than voices in
a row.

**Or: be a drop-in OpenAI endpoint.** Distribution by being plugged into
other people's stacks. Narrower and well-defined, but it must be a *tested*
conformance profile rather than a claim — half-compatible is worse than
honestly incompatible. Note the premise correction from the review: there is
no `/v1/models` collision (the catalog already lives at
`/v1/models/catalog`), and `handleChatCompletions` already proxies the
upstream body, so streaming is conformance hardening rather than new
machinery.

---

## Already decided — do not re-litigate

- **Per-line delivery direction: CUT.** Unreachable. The resident audio
  server takes `{model, input, voice_ref, reference_text}`, `SynthesizeFunc`
  is `(ctx, text, voiceID)`, and `--instruct` belongs to the voice *design*
  engines, not cloned speech. Needs an engine contract change first.
- **NeuTTS Air as a CPU engine: CUT.** GGUF backbone is real, but the
  shipped runtime is a Python package plus `llama-cpp-python`, eSpeak and a
  separate NeuCodec/ONNX path. Not a native engine behind the existing seam.
- **Batch synthesis: not adopted.** The 49.5% figure in
  `LOCAL_ENGINE_PROFILE.md` predates the resident `audiocpp_server`, which
  already took an 8-line story from ~71 s to ~25 s. Revisit only against a
  fresh measurement.
- **Chasing -16 LUFS with a limiter: no.** Spoken-word TTS is peaky enough
  that the true-peak ceiling usually binds first; the render stays quieter
  and reports `target_met: false`. Adding compression to hit a number would
  undo what mastering is for. If it ever matters, the fix is quieter
  synthesis at source, not a limiter.

## Conventions that will save time

- **Verify live, not just green.** `scripts/verify.ps1` is the portable
  check. But three bugs this session were invisible to tests and obvious in
  a browser. Drive the console with the gstack browse daemon at
  `~/.claude/skills/gstack/browse/dist/browse` — it clicks *and*
  screenshots, unlike the in-app Browser pane which needs a visible pane.
  Cache-bust with `?v=$(date +%s)` after editing static files.
- **The real gateway** is `.claude/launch.json` → `cpp-studio-real-gateway`
  (config.real.json, all nine engines, ~14 GB VRAM). It writes to the
  *workspace root* `out/`, not `cpp-studio/out/`. Stop it when done.
- **Optional engines follow one pattern:** config-gated, hidden in the UI
  when absent, with a fixture stand-in in `cmd/cpp-studio-fixture` so CI
  covers them. `ytdlp`, `diarize` and `ffmpeg` are all examples.
- **`engine.Spec` has a file-path mode** (`InputPath`/`OutputPath`) for
  payloads that are whole recordings rather than a sentence. Use it for
  anything long-form.
- **Every take-room mutation takes the per-story lock and republishes the
  manifest** (`m.publishManifest`) — `Status` prefers the tracked in-memory
  job over the store, so an edit that skips it lands on disk and stays
  invisible for the life of the process.

## Questions for James before starting

1. **Episode shape.** Scenes inside one episode, or episodes as a sequence
   of independent sketches stitched at the end? Changes the schema.
2. **Does the Audiobook tab survive** as its own thing, or become
   "an episode with one speaker"?
3. **Stage 3 fork** — sound design or OpenAI conformance? (Recommendation:
   sound design.)
