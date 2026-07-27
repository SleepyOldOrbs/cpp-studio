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

### Shape: scenes inside one episode  ·  *decided*

An episode is a sequence of **scenes**, each of which is what a sketch is
today. That single decision resolves four problems at once, which is why it
is the right one:

- **Writing.** Generating 330 good lines in one llama call will not work.
  A scene is one call, and a scene is a unit a writer actually thinks in.
- **The take room at length.** 330 flat lines is unusable; scenes give it
  grouping, collapse, and somewhere to put "jump to the next unrendered
  line".
- **Resumability.** A completed scene is a natural checkpoint. Much of the
  work in (3) below falls out of scene boundaries rather than needing a
  bespoke work-in-progress format.
- **Sound design** (stage 3, now confirmed) attaches to exactly these
  seams: a bed under a scene, a sting between scenes.

**Aim for "a sketch is a one-scene episode"** rather than two parallel
concepts. If the current story path becomes the degenerate case of the new
one, nothing has to be maintained twice and existing stories stay valid.

Leave room in the scene schema for per-scene audio assets even though stage
1 will not implement them. Cheap now, expensive to retrofit.

### No merge with audiobooks  ·  *decided*

Audiobooks keep their own front door, tab and manifest. **This makes stage 1
smaller than the earlier draft assumed**, and corrects the plan:

- The previous version proposed moving `internal/audiobook/ingest.go` into
  the story path. That was predicated on a merge. **Do not move it.** It
  stays where it is.
- More usefully: **episodes probably do not need document ingest at all.**
  An episode is written from a premise, not extracted from an `.epub`. The
  ingest work was only ever load-bearing for the merge that is not
  happening.
- If "perform a script I already wrote" is wanted later, that is *script
  import* — paste or upload a cast script — and it is a different, smaller
  thing than epub chunking.

Note for later, not for stage 1: `audiobook.Manager.run` has the same
memory problem as stories (all chunks held, saved only after stitching). If
(2) and (3) below produce something reusable, audiobooks should eventually
get it. They do not block this stage.

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
4. **Scene structure.** The decision above, in the schema: episode → scenes
   → lines, with line ids staying stable and takes hanging off them exactly
   as they do now. Scenes get their own ids so takes, renders and (later)
   audio assets can reference them.
5. **Single-active-job.** `Manager.Submit` allows one story at a time. That
   is fine for 40 seconds and questionable for 30 minutes.

### Where it will hurt

The take room UI is a flat list — fine at 8 lines, unusable at 330. Scene
grouping and collapse are the minimum; "jump to the next unrendered line" is
the thing that makes it workable. Budget real time here. It is the surface
James will actually live in, and it is the part most likely to be
underestimated.

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

## Stage 3 — Sound design  ·  *decided*

Music beds, stings, scene transitions, audience laughter. Radio comedy has
all of it and this has none — it is most of the distance between "voices in
a row" and "a show".

It became much cheaper this session: mixing needs gain (`wav.ApplyGain`
exists) and asset conversion needs ffmpeg (an engine now, with a file-path
spec mode for large assets). The missing primitive is **overlay** — mixing
two streams at an offset with independent gain — which `internal/wav` does
not have yet and which is the one genuinely new piece of audio maths here.

Because scenes are the seam this attaches to, the stage 1 schema should
leave room for it: a scene that can carry assets, even with nothing reading
that field yet.

Open, for when we get there: user-supplied assets only, or a small bundled
set? Bundling raises licensing questions the project has so far avoided
entirely by shipping no weights and no audio. Leaning strongly toward
user-supplied, consistent with every other optional capability here.

**Not doing: the OpenAI conformance profile.** It stays on the shelf rather
than being cut — if it is ever picked up, note that there is no
`/v1/models` collision (the catalog already lives at
`/v1/models/catalog`), and `handleChatCompletions` already proxies the
upstream body, so streaming would be conformance hardening rather than new
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

## Answered, 2026-07-25

James settled all three before the session ended:

1. **Scenes inside one episode.** Not independent sketches stitched at the
   end. Folded into Stage 1 above.
2. **Audiobooks stay separate.** No merge — which makes Stage 1 smaller,
   and means `ingest.go` stays where it is.
3. **Sound design** for Stage 3. OpenAI conformance shelved, not cut.

Nothing is blocked on a decision. Stage 1 can start from the schema.

## First moves, when the session starts

Roughly in this order, and none of it is committed to:

1. Read `internal/story/types.go`, `takes.go` and `manager.go` end to end —
   they changed a great deal on 2026-07-25 and the take-room model is the
   thing being extended.
2. Sketch the scene schema on paper first: episode → scenes → lines, where
   a one-scene episode is exactly today's story. Check it against an
   existing stored manifest in the workspace-root `out/stories/` before
   writing code.
3. Decide what a scene render is. A per-scene render revision is
   attractive — natural checkpoint, cheap re-render of one scene — but it
   multiplies artifacts, so think it through rather than assuming.
4. Only then touch the caps in `types.go`. They are the last step, not the
   first: raising them before the buffering is fixed just makes the failure
   mode bigger.
