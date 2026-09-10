# UX copy review: cpp-studio

**Current status:** See the [implementation and verification matrix](astra-design-implementation-20260910.md). It supersedes the intermediate implementation/test notices below, including the initial demo-test assertion failure, and identifies the remaining browser-evidence limits.

Date: 2026-09-10. Reviewer: Astra High. This follows the completed design critique and product audit. Accessibility is excluded as requested. Tone: direct, practical and calm. Preserve API routes, element IDs and domain behavior.

## Evidence and boundaries

Reviewed the current `CONTEXT.md` contract, both preceding reports, current main/Story Builder HTML and relevant JavaScript renderers. Inspected saved screenshots 01–12 and 20–29 under `out/astra-design-20260910/screenshots/`. The training-data scope is established by the domain contract, source and screenshot 29. Fixture screenshots demonstrate wording and interactions, not native model quality.

The existing source/cast requirements, Actor Voice versus Character Voice distinction, cancellation versus deletion, supported file formats and model-specific controls communicate useful constraints and should remain. The changes below address specific mismatches and recurring wording, not a general rewrite of technical settings.

## Actionable findings and exact replacements

### UC-01 — Name the assistant conversation honestly (P1)

The route currently titled Text to speech asks an assistant to reply and then speaks that reply. Direct reading of supplied text is a different existing control in Voice cloning. Screenshots 01, 02 and 10 show the mismatch.

| Location | Old | New |
| --- | --- | --- |
| Main navigation, overview card, tool heading and descriptive labels | Text to speech | Voice chat |
| Overview card action category | Speak | Converse |
| Main overview description | Turn text into speech, transcribe recordings, build reusable voices, invent a new voice, or transform a performance. | Talk with an assistant, transcribe recordings, build reusable voices, invent a new voice, or transform a performance. |
| Voice action step | Run the voice loop | Send your message |
| Voice submit button | Run voice loop | Send and hear reply |
| Message hint | Used when no WAV is loaded. Ctrl+Enter runs the loop. | Used when no WAV is loaded. Ctrl+Enter sends your message. |
| Reply-voice hint | Clone a voice below to add options here. | Add a voice in Voice cloning or Voice design to use it here. |
| Voice-library selection button, JavaScript | Use in loop | Use voice |
| Direct-speech hint | Ctrl+Enter speaks. Pick a voice above with “Use in loop”. | Ctrl+Enter speaks this text exactly. Pick a voice above with “Use voice”. |

Use the existing `/demo/#voice-cloning` route as a link on the words “Voice cloning” in the reply-voice hint. Keep the existing `#text-to-speech` route and API contract unchanged. Internal Voice Loop terminology and diagnostic logs can retain their domain name.

**Acceptance:** Navigation, heading and submit action clearly promise an assistant reply. The direct-speech control explicitly promises to speak the supplied words. Model names containing “text to speech” remain valid and unchanged.

### UC-02 — Describe Training as dataset preparation and export (P1)

The browser only receives checked clips and exports WAV/TXT/JSONL data; it does not run a trainer. The current title overstates this scope.

| Location | Old | New |
| --- | --- | --- |
| Navigation | Training | Training data |
| Page title and descriptive label | Voice LoRA training | Prepare training data |
| Extract handoff action | Pass to Training | Prepare training data |
| Initial and JavaScript empty summary | No speech has been passed from Extract yet. | No checked clips yet. In Extract, check each speech clip, then choose Prepare training data. |
| Export explanation | Exports one extracted WAV without cleanup processing and one corrected TXT transcript per clip, plus a train.jsonl manifest. Choose the folder where the training set should live. | Export each extracted WAV and corrected TXT transcript, plus train.jsonl, for a separate training tool. Audio is exported without cleanup processing. Export before closing or resetting this page; unexported clips remain only in this browser session. |

Keep “Export training folder” as the action and preserve file-format terms because they explain what the user receives. Add an “Open Extract” link to `#extract` immediately after the summary, so the empty state provides its next action (screenshot 29).

**Acceptance:** No page title implies that training runs here; the handoff and destination use matching labels, including after JavaScript renders an empty state. The export boundary and browser-session lifetime are visible.

### UC-03 — Make tool and save labels consistent (P2)

Screenshots 03, 08, 20 and 22 expose variant tool names and ambiguous Library actions.

| Location | Old | New |
| --- | --- | --- |
| Music subnavigation | Audio Seperation | Voice & stem separation |
| Voice-cloning heading | Voice clone | Voice cloning |
| Voice-design heading | Voice designer | Voice design |
| Main output actions | To library / Save to library | Save to Library |
| Podcast collection label | Retained podcasts | Saved podcasts |
| Voice Designer detail label | Engine input | Voice description used |
| Voice Designer saving hint | Saving stores the audition clip as the voice's cloning reference, so the loop, speak box, and image describer can all use it. | Saves this audition as a reusable Actor Voice for compatible speech tools. |

**Acceptance:** Identical owning destinations use identical save labels. The separation spelling is corrected, and voice headings match navigation. The audition/reference distinction remains accurate.

### UC-04 — Replace implementation language in ordinary production guidance (P2)

Screenshots 05, 07, 23 and 25 show technical persistence/request terms where a user needs a next action. Keep detailed configuration vocabulary in the existing advanced controls.

| Location | Old | New |
| --- | --- | --- |
| Audiobook action step | Resolve and narrate | Preview and narrate |
| Audiobook step explanation | Resolve the exact request first, review it, then start or cancel narration. | Preview the narration settings, review them, then start narration. |
| Audiobook preview button | Resolve request | Preview narration settings |
| Preview hint | Required before Narrate; no job or engine invocation occurs. | Review this preview before Narrate. No audio is generated yet. |
| Initial preview | Request not resolved. | Narration settings have not been previewed. |
| JavaScript invalidation | Request changed. Resolve it before narration. | Settings changed. Preview them again before narration. |
| JavaScript loading | Resolving effective request… | Preparing narration preview… |
| JavaScript failure | Request could not be resolved. | Could not preview narration settings. Check the error below and try again. |
| JavaScript pre-submit error | Resolve the current narrator request before starting narration. | Preview the current narration settings before starting narration. |
| Advanced direction disclosure | Advanced direction escape hatch | Custom performance direction |
| Source/preview hint | Source words remain immutable. The resolved preview shows every exact DramaBox section prompt and punctuation normalization. | Your original document stays unchanged. The preview shows the exact direction and punctuation used for each DramaBox section. |
| Library explanation | The Library gathers every durable studio collection in one place. Each item stays in its existing voice, story, audiobook, or output store on disk. | Browse your saved voices, audio, productions and images. Open an item in its tool to continue working. |
| Audiobook overview card | Turn a document into durable, resumable narration with listening and verification controls. | Narrate a document, review the audio, and resume interrupted work. |
| Story Builder subtitle | Arrange Dialogue, SFX, and Music on a durable story timeline. | Arrange Dialogue, SFX, and Music on your project timeline. |
| Story Builder initial and JavaScript action | Build stale / Build stale (N) | Build dialogue / Build dialogue (N) |
| Story Builder delivery heading | Mixed-master revisions | Rendered masters |

Remove decorative `.module-route` spans from ordinary tool headers and output panels (the visible `POST /v1/...` / `GET /...` badges). These static badges carry no element IDs or behavior; actual API routes and handlers remain unchanged. Their removal does not remove technical model settings or advanced preview information needed for a decision.

**Acceptance:** Narration preview instructions match throughout initial, changed, loading and failure states. Builder action wording describes the content being generated. Ordinary headings no longer expose transport routes; advanced options remain available.

### UC-05 — Describe reset scope and save completion (existing PA-01/PA-04)

These are wording requirements for the existing product-audit findings, not additional features.

| Location | Old | New |
| --- | --- | --- |
| Header action | Clear all | Reset session |
| Reset confirmation | None | Reset this session? Unsaved inputs, results and browser-local clips will be cleared. Saved Library items and running jobs will remain. |
| Voice Designer save confirmation | No visible confirmation | Saved “{voice name}” to your voice library. |

Use the existing Library/voice-tool route for the successful-save follow-up. A new candidate replaces the previous confirmation; saving failure is separate from success.

**Acceptance:** PA-01 and PA-04 acceptance remains authoritative. Canceling reset preserves the draft. A saved voice's name is visible beside the save action.

### UC-06 — Do not recommend a different music model than the selector (P2)

Screenshot 20 shows HeartMuLa selected while the unavailable-model hint says “Install ACE-Step from Models before generating. The 6.19 GB download always requires confirmation.” `renderMusicModels()` hard-codes this text for every catalogue state with compatible but unusable models. Replace it with “Open Models to install or configure a compatible music model.” Keep the existing Models link and the actual install confirmation behavior. The catalogue is the source of package names and sizes; this hint should not introduce a conflicting model or stale download size.

**Acceptance:** With HeartMuLa, ACE-Step or another compatible model unavailable, guidance points to Models without claiming that a different named model or fixed download size is required. Existing usable-model guidance remains unchanged.

## Implementation plan and ownership

The copy reviewer implements static main-page wording in `internal/demo/static/index.html` only, adds the agreed `designSaveStatus` paragraph, and makes the three guided submit steps static visible sections for DC-01 while retaining numbered headers and all controls. The product-audit reviewer owns the corresponding JavaScript text and action-heading CSS; root owns the four Story Builder text substitutions. Existing advanced disclosures remain collapsible. No new route, setting or backend behavior is introduced.

Verify HTML structure, unique element IDs and the existing focused demo contract tests; browser acceptance is coordinated by root. Avoid copy-only tests that merely repeat strings. There is no localization system in this change; keep file-format abbreviations and domain voice names stable, and allow labels to wrap naturally.

## Main HTML implementation result

Static main-page changes are implemented in `index.html`, including the three non-collapsible numbered action sections, `designSaveStatus`, the direct Extract link and removal of decorative route badges. All existing form/control IDs and tool routes remain unchanged.

- HTML parsing passed: balanced nesting, 366 unique IDs, and all three guided submit actions remain inside their forms and outside disclosures.
- `git diff --check -- internal/demo/static/index.html docs/agents/astra-ux-copy-20260910.md` passed.
- The initial `go test ./internal/demo` run exposed obsolete presentation assertions requiring removed route badges and the misspelled separation label. Root has the exact assertions to update; this reviewer did not modify that separately owned test file.
- Corresponding JavaScript/CSS and Story Builder copy changes are delegated to their existing owners. Root retains browser acceptance and the final combined test gate.
