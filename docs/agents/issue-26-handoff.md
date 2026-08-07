# Issue #26 implementation handoff

Last updated: 2026-08-07

## Objective

Implement GitHub issue #26, **Launch and prove the complete Story Builder
workflow**, on branch `codex/story-builder-complete-workflow`, stacked on issue
#25 commit `6e0a537`.

## Product contract

- The main Studio exposes an explicit Story Builder launch without merging the
  Story, Take, Audiobook, and Story Builder ownership boundaries.
- Core controls use native buttons, links, and inputs with accessible names and
  visible keyboard focus.
- Ctrl/Cmd+S, Ctrl/Cmd+Z, Ctrl/Cmd+Y, Space, and Delete provide save, undo,
  redo, playback, and selected-clip deletion only while focus is outside an
  editable field.
- One fixture-backed Gateway project proves save/reload, Character Voice
  dialogue build, copied reusable-audio placement, mixed WAV render, MP3
  export, unified Library discovery, and owning-tool reopen.
- One configured native-engine run proves real dialogue generation, durable
  reload after a Gateway restart, and a mixed render. Perceptual timing and
  spoken-content judgement remains a separate human listening gate.

## Decisions

- Keep Story Builder a separate tool and add one ordinary link to the existing
  main-Studio navigation. Do not reshape the existing product aggregates.
- Reuse the current document-level shortcut guard, native controls, and focus
  style. Add concise visible shortcut help rather than a parallel command
  system.
- Keep the complete browser workflow in two consecutive Playwright programs.
  The first leaves the same durable project selected for the second; the split
  stays below the Windows command-line limit without weakening the journey.
- Run native proof with a temporary minimal config containing the exact
  configured OmniVoice CUDA subprocess and ffmpeg paths. This avoids starting
  unrelated resident engines while preserving the production synthesis and
  mastering paths.

## Verification

- `scripts/smoke-story-builder-browser.ps1 -GatewayPort 8925 -OutDir
  output/playwright/story-builder-issue26-overlap-final-2` passes the full isolated
  real-browser suite. The new journey covers main launch, visible focus,
  text-field shortcut guards, save/undo/redo/delete, same-track overlap rejection,
  save/reload, dialogue
  build, project-owned reusable audio, WAV render, MP3 export, Library launch,
  and durable artifact records.
- `config.real.json` passes `cpp-studio --check`. The focused native run used
  its real OmniVoice binary/model and ffmpeg command, generated build
  `build_1786101861138_fb49d2`, restarted the Gateway, reloaded the Dialogue
  Clip as `ready`, and rendered revision 1 of project
  `sbp_20260807_112421_4e7615`.
- The native mixed WAV is 30,000 ms and 960,044 bytes, with SHA-256
  `5c5a1676877614d8e8fe1348e860f9e6d21fd566eb88adfa359686a1a63d3935`,
  at ignored path
  `output/native/story-builder-issue26/verified/issue-26-native-mixed-render.wav`.
- On 2026-08-07, James listened to the configured-native mixed WAV and passed
  both the generated sentence/content and the dialogue/SFX overlap timing at
  approximately 1.5 seconds. This human result is recorded separately from the
  automated WAV, persistence, placement, HTTP, and browser validation above.

## Exact next step

The Story Builder acceptance sequence is complete. The green draft stack can be
considered for merge in order from PR #35. Do not merge without explicit user
approval.
