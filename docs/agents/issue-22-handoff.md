# Issue #22 implementation handoff

Last updated: 2026-08-07

## Objective

Implement GitHub issue #22, **Render immutable mixed-master WAV revisions**, on
branch `codex/story-builder-render`, stacked on issue #21 commit `19613cd`.

## Product contract

- Rendering validates the complete saved audible arrangement before publishing
  anything. Unready Dialogue and missing or changed project media reject the
  request; muted tracks do not block it.
- The project-length 16 kHz mono 16-bit PCM mix applies timeline positions,
  nondestructive source trims, track mute, explicit silence, and cross-track
  overlap. Valid PCM source rates, channels, and 8/16/24/32-bit depths are
  normalized deterministically while mixing.
- When ffmpeg loudness measurement is configured, the mixed WAV uses the same
  final linear-gain target and true-peak ceiling as retained Story renders and
  records the measured mastering report.
- Every success appends a new numbered manifest record and immutable
  `renders/render-NNN.wav`. `/master` redirects to the newest numbered URL; it
  is a convenience action, not a mutable copy or identity.
- Exact positive decimal render revisions are the only served artifacts.
  Browser-provided filenames, paths, aliases, and unsupported subresources are
  rejected.
- A validation, mix, master, render write, or manifest write failure leaves the
  saved project and all earlier render revisions unchanged.

## Decisions

- Keep render orchestration and publication in the Story Builder Store. Gateway
  validates the JSON envelope and shapes HTTP responses only.
- Keep sample decoding, placement, summing, and final int16 clamping in
  `internal/wav`, the repository's existing PCM home. Do not add a parallel
  audio graph or external mixer.
- Reuse the retained Story final mastering function through one exported helper;
  without configured measurement, preserve the existing raw-render behavior.
- Store render identity in the project manifest and derive filenames and URLs
  server-side. Whole-project edits preserve render history but cannot author it.
- Write and validate the WAV before appending the atomic manifest. If the
  manifest save fails, remove the unrecorded file.

## Verification

- `go test ./... -count=1`, `go vet ./...`, `git diff --check`, and
  `scripts/verify.ps1` pass.
- `scripts/smoke-story-builder-browser.ps1 -GatewayPort 8901 -OutDir
  .\out\story-builder-browser-smoke-issue22d` passes. It renders twice, hashes
  revision one before and after, inspects exact mixed samples for placement,
  trim, mute, silence, and overlap, follows the latest action, and proves
  missing media publishes no revision.
- The two-axis review against `19613cd` has no remaining Standards or issue #22
  specification findings. Review fixes removed duplicate/unused request code,
  normalized differing valid PCM inputs to the fixed delivery format, and made
  mastered-output format and duration validation complete.

## Exact next step

Commit and publish a draft pull request based on
`codex/story-builder-story-import`. Do not merge without explicit user approval.
