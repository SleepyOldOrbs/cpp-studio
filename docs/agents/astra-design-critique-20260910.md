# Design critique: cpp-studio

**Current status:** See the [implementation and verification matrix](astra-design-implementation-20260910.md). It supersedes the intermediate implementation/test notices below and identifies the remaining browser-evidence limits.

Date: 2026-09-10. Reviewer: Astra High. Scope: visual hierarchy, usable layout, consistency and state clarity. Accessibility review is excluded at the user's request. This is the first review stage; workflow audit and UX-copy review follow separately.

## Evidence and overall impression

Inspected the current source and these current-run screenshots under `out/astra-design-20260910/screenshots/`:

- `01-overview-before.png`
- `02-speech-before.png`
- `03-voice-design-before.png`
- `04-story-builder-empty-before.png`
- `05-story-builder-project-before.png`
- `06-story-builder-library-before.png`
- `07-library-empty-before.png`

The dark console treatment, clear format navigation and consistent panel boundaries give the app a recognizable identity. The largest opportunity is to make the next useful action and the current result easier to find. The captured initial states sometimes emphasize controls that cannot yet help, while hiding the useful action behind a disclosure or leaving prerequisite guidance as plain text.

These images establish initial and lightly populated desktop states. They do not establish model health, successful media generation, narrow-screen behavior or human audio acceptance. No inference run or project mutation was performed by this reviewer.

## What works well

- The overview uses clear action-oriented cards with enough explanation to choose a tool. The active format and active tool remain recognizable across the main Studio and Story Builder.
- Voice Designer has a direct left-to-right input/result layout and an obvious Generate voice action. It provides a useful reference for the main workflow action hierarchy.
- Numbered speech steps explain what is required and what is optional without a separate onboarding flow.
- Story Builder distinguishes project selection, timeline editing, reusable voices and delivery. Its empty timeline states plainly explain the missing content.
- Library category names, descriptions and item counts create useful groups without changing the underlying ownership of saved work.

## Actionable findings

### DC-01 — Keep workflow actions visible and reveal relevant results (P1)

**Evidence:** In `02-speech-before.png`, the input and recording controls fill most of the viewport. The visible “Run the voice loop” row is a closed disclosure, so the action itself is hidden. Voice Designer in `03-voice-design-before.png` demonstrates the clearer existing pattern: its primary action is immediately visible beneath the input. Source: `internal/demo/static/index.html` around lines 248–297 puts the speech action/error and result in initially closed `details`; the same pattern appears in the Podcast action/result around lines 931–1019. `styles.css:991` hides the bodies. `app.js` opens Story errors explicitly, but the corresponding result renderers do not open their enclosing workflow result disclosure.

**Impact:** The user must discover an additional disclosure before running the task and can miss progress, completion or errors inside closed sections.

**Smallest implementation:** Keep each existing Run/Generate action row and its error feedback visible outside optional collapsed settings, preserving the numbered setup structure. Open the relevant result disclosure when a request starts and when a result or error arrives; do not auto-scroll or repeatedly reopen unrelated sections. Apply this to the existing guided workflows that share this pattern rather than introduce a new wizard or state framework.

**Acceptance:** With optional settings closed, each guided tool still exposes its primary submit action. Starting a fixture request exposes its status/result region. Completion and error messages are visible without the user opening another disclosure. Existing optional configuration remains collapsible and entered values survive toggling.

### DC-02 — Hide the floating selection inspector when nothing is selected (P2)

**Evidence:** `05-story-builder-project-before.png` shows the floating Selection panel covering the right-hand transport/status region despite an entirely empty timeline. `06-story-builder-library-before.png` shows the same floating panel occupying timeline space for an empty Dialogue track. Source: `story-builder.js:759` renders a no-selection message into the inspector rather than hiding it; `story-builder.css:358` keeps it fixed above the canvas.

**Impact:** An inactive inspector occupies and can obscure the work surface before there is anything to inspect.

**Smallest implementation:** Hide `storyBuilderSelectionPanel` whenever there are no selected clips; show it when selection becomes nonempty. Keep the selection instruction in existing inline timeline help if needed. Preserve the current selected-clip editor and drag behavior.

**Acceptance:** Empty projects and deselected timelines show no floating inspector. Selecting one or multiple clips shows the appropriate inspector; deleting the selected clips hides it. The saved position and selected-clip editing behavior continue to work.

### DC-03 — Give Library groups an explicit disclosure indicator (P2)

**Evidence:** In `07-library-empty-before.png`, Current work and all collection headers look like static cards. “Productions — 1 item” gives no visible indication that its contents are collapsed. Source: `app.js:9695` creates each collection as `details`/`summary`; `.library-jobs-summary` and `.library-group-summary` in `styles.css:2712` use a flex presentation without an explicit expanded/collapsed marker. The main workflow steps already use visible plus/minus indicators.

**Impact:** Saved work is hidden behind headers whose appearance does not explain the required interaction.

**Smallest implementation:** Add a consistent visible chevron or plus/minus to the two Library summary classes, changing its orientation or symbol with the parent `open` state. Keep item counts and filtering behavior intact.

**Acceptance:** Both collapsed and expanded Library groups clearly show their state. Opening Productions reveals its existing project; Refresh and search preserve the established disclosure behavior. The indicator does not overlap descriptions or item counts.

### DC-04 — Link the empty voice prerequisite to the existing creation tool (P2)

**Evidence:** `06-story-builder-library-before.png` has an empty Dialogue track asking for a Character Voice and an empty voice library saying “Create Character Voices in the Library to use them here.” There is no action beside that explanation. Source: `story-builder.js:1820` emits plain empty-state text. Actor/Character Voice creation and editing already exist in the main Studio voice tool (`app.js:2680–2846`, with the owning page at `/demo/#voice-cloning`).

**Impact:** The first Dialogue track reaches a prerequisite that tells the user what is missing without providing a direct route to resolve it. Sending them to the general read-only Library adds another discovery step.

**Smallest implementation:** Add a clearly labeled link from the unfiltered no-Character-Voices state to the existing voice-creation tool. Briefly state that an Actor Voice needs a Character Voice before it can be used on a Dialogue track. Preserve the existing Refresh voices control for returning to the project. A search with no matches should retain search-specific feedback rather than claim no voices exist.

**Acceptance:** A project with no Character Voices offers a working route to voice setup. Existing saved project state is preserved during that transition. An unmatched search has search-specific guidance and does not hide existing assets permanently.

### DC-05 — Make a blank project's first timeline action more prominent than delivery (P2)

**Evidence:** `05-story-builder-project-before.png` shows an amber Save Project button, an enabled purple Render master button and an empty delivery-history panel above the timeline, while the empty canvas offers only the sentence “Add a Dialogue, SFX, or Music track to begin.” The add-track controls are comparatively quiet in the distant canvas toolbar. Source: `story-builder.html:77–112` defines the delivery/save controls; `story-builder.js:516` enables Render whenever a project exists; `story-builder.js:1037` renders a text-only empty timeline.

**Impact:** The empty project emphasizes saving and delivery before the user has added the content those actions need.

**Smallest implementation:** Add an “Add dialogue track” action alongside the existing empty-canvas guidance using the current `addTrack("dialogue")` handler; keep the existing SFX/Music toolbar actions. Disable Render master while the project contains no clips and explain the prerequisite in its nearby status. This is a narrow empty-state gate, not a second copy of server render validation. Keep delivery history and existing exports visible for projects that have them.

**Acceptance:** A fresh blank project visibly offers a direct add-track action in the empty canvas. Render cannot be submitted with zero clips and its nearby message explains why. Adding the first clip removes that empty-project gate, while server validation still decides whether the actual arrangement can render. Existing render/export history is unchanged.

## Implementation boundary

Implement DC-01 through DC-05 using the existing HTML, CSS and event handlers. Preserve the current console aesthetic, domain stores, autosave contract and existing dirty work. No palette overhaul, new design system, new workflow engine or speculative mobile redesign is required by the captured evidence. Data-loss protection, detailed terminology and model availability behavior belong in the subsequent workflow/copy review and should be reconciled there rather than counted twice.

## Story Builder implementation plan

- DC-02: hide the inspector with no selected clips; position it on first actual selection and preserve its last dragged position while hidden.
- DC-04: provide a voice-setup link to `/demo/#voice-cloning` in a new tab, explicitly labeled, so the current project's unsaved browser state remains in place. Keep unmatched-search feedback separate.
- DC-05: add an empty-canvas button using the existing add-Dialogue-track handler; disable Render only for zero clips or an existing mutation, with nearby guidance. Any clip, including silence, removes the empty-project gate. Retain prior render links.
- Verification: Node syntax check and the existing focused Story Builder state suite, plus a regression case covering empty rendering, valid silence clips and retained render links. Root will handle browser acceptance and the separately audited project-switch behavior.

### Story Builder implementation result

DC-02, DC-04 and DC-05 are implemented in the existing Story Builder HTML/CSS/JS. The inspector is hidden while selection is empty and keeps its stored position; the missing-voice state has an explicitly labeled new-tab setup link; the empty canvas has a direct Dialogue-track action; and the zero-clip render gate provides guidance without hiding previous deliveries or rejecting silence clips. The project-switch code was not changed by this implementation.

Verification on 2026-09-10:

- `node --check internal/demo/static/story-builder.js` passed.
- `node --test scripts/test-story-builder-state.cjs` passed all 5 tests, including the added empty-project/silence/delivery regression.
- `git diff --check -- internal/demo/static/story-builder.js internal/demo/static/story-builder.css internal/demo/static/story-builder.html scripts/test-story-builder-state.cjs` passed.
- Browser visual/interaction acceptance remains with the root task; this result does not claim it has been completed.
