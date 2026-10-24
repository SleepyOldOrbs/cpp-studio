# VoiceStudio-inspired improvements: acceptance record

This records the five bounded improvements from the [UI review](voicestudio-ui-review-2026-10-24.md)
and [master plan](superpowers/plans/2026-10-24-voicestudio-improvements.md).
VoiceStudio screenshots and UI copy were research material, not implementation instructions.
Existing module owners, source documents, voice identities and saved productions remain authoritative.

## Delivery and demonstrations

| Stage | Delivery | Demonstrated outcome |
| --- | --- | --- |
| 1. Story Builder script authoring | [PR #56](https://github.com/SleepyOldOrbs/cpp-studio/pull/56) | Script and Timeline share edits, casting, delivery overrides and selected-line builds. A separate example project retains complete takes and current drafts. |
| 2. Chapters and passage audition | [PR #57](https://github.com/SleepyOldOrbs/cpp-studio/pull/57) | Canonical source chapters, authoritative preview/create agreement, actual PCM chapter boundaries, source-passage audition and cancellation without a phantom book. |
| 3. M4B delivery | [PR #58](https://github.com/SleepyOldOrbs/cpp-studio/pull/58) | Revision-bound AAC/M4B exports with chapters, cover and credits; exact tool/Library download hashes and real VLC chapter/detail inspection. |
| 4. Pronunciation corrections | [PR #59](https://github.com/SleepyOldOrbs/cpp-studio/pull/59) | Explicit rule application, highlighted source/spoken previews, original manuscript retention and frozen resumed/retried speech. |
| 5. Voice browsing and reuse | [Issue #55](https://github.com/SleepyOldOrbs/cpp-studio/issues/55) | Editable design starters, three candidates and explicit save, Actor/Character browsing, exclusive auditions and validated reuse in an audiobook or selected Story Builder line. |

Each stage received a fresh independent review. All Important findings were fixed
with regressions observed failing before the repair and passing afterwards. No Minor
findings were deferred. All five implementation subplans have their acceptance steps checked.

## Verification

- Final local `go test ./...`, `go vet ./...`, formatting and diff checks passed.
- Final `node --test scripts/test-studio-ux.cjs scripts/test-story-builder-state.cjs`: **77/77** passed.
- Complete Story Builder browser acceptance passed, including save/recovery, casting, builds, exports, owner links and retained console drafts.
- The final voice/Library browser workflow passed, including a nondefault speaker phrase, failed direct-link assignment followed by successful retry, retained manuscript, stopped hidden audition, selected-line build and 800-pixel layouts.
- Native Qwen3 generated all three candidates. Candidate 2 was explicitly saved and read back with SHA-256 `2b06da88a90fe711706ddcbbf11fa037e071c78d9e6670eed9df6bc7569cba5d`.
- Native OmniVoice generated the actual source-passage audition, the complete example opening sentence and reused dialogue. Whisper transcribed both pronunciation-corrected book and dialogue audio exactly as `James opens the door.` while original source remained `Jmes opens the door.`
- FFmpeg 8.1.2 and 9.0.1 exports passed full decode and adversarial metadata round-trips, including Unicode, newlines, carriage returns and trailing backslashes. Chapter markers were compared to actual selected audio; VLC showed the cover, credits and both chapters.
- All acceptance gateways used isolated output/configuration and were stopped afterwards. The regular installation, configured models and primary checkout were preserved.

Native artifacts remain in local temporary directories, not in the repository:

| Evidence | Directory under `%TEMP%` |
| --- | --- |
| Final voice/Library fixture and narrow screenshot | `voice-library-0cf347c269b048c7bba52cdd1dee85fb` |
| Real Qwen3/OmniVoice candidate and production reuse | `voice-library-b3173eb10aad4bd3bfdf0eed1a780bdb` |
| Corrected native book/dialogue | `pronunciation-native-a12b688de71a4785aa322423d4cb18f8` |
| Final M4B browser/download proof | `audiobook-m4b-7357034aecad4aee9741e0bf34686843` |

## Rulings I made

The following decisions preserve the execution ledger's order. Each includes the
cost if the decision proves unsuitable.

1. Implement inline with a fresh reviewer at each stage; use the user's existing push/merge authorization. Cost: fewer intermediate independent reviews.
2. Track stage/slice identifiers directly because the plan does not use numeric Task headings. Cost: manual ledger bookkeeping.
3. Assert the existing hidden empty inspector instead of obsolete empty-state text. Cost: that copy is not checked.
4. Verify dependency-protected deletion through owner endpoints after old voice selectors changed; retain project deletion UI coverage. Cost: less voice-deletion UI coverage.
5. Commit shared casting, delivery, example and covered review repairs together after the initial Script view. Cost: a larger acceptance diff.
6. Keep one temporary in-memory audition WAV, capped at 16 MiB. Cost: the next audition or restart replaces it.
7. Commit shared chapter preview/form/audition integration together after acceptance, with extraction separate. Cost: a larger integration diff.
8. Audition up to 300 runes from the actual first DramaBox section, retaining source newlines. Cost: a short sample may not represent a full chapter.
9. Begin isolated Stage 3 work while Stage 2 Windows CI finishes; keep merging gated on green CI. Cost: switching branches if earlier CI requires repair.
10. Terminate only temporary FFmetadata values ending in an encoded backslash with NUL before LF, avoiding the FFmpeg 9 reader's escaped-line ambiguity. Cost: reliance on the tested metadata-reader behavior; stored metadata never receives NUL.
11. Use the existing local `out/` store for pronunciation; normalize language codes, match exact codes plus blank fallback, and reject conflicting merges. Cost: callers needing overwrite or another language fallback must explicitly replace/add rules.
12. Commit integrated pronunciation editing and frozen consumer snapshots together. Cost: a larger final Stage 4 diff.
13. Bind policy identity only for applied nonempty snapshots; preserve takes whose spoken input does not change. Cost: future resolver changes must bump their policy version explicitly.
14. Keep replacement phrases atomic and omit whitespace-only synthesis units while retaining canonical source ranges. Cost: pathological whitespace does not create audible pause units.
15. Start independent Stage 5 starter work while Stage 4 review runs, then merge the reviewed Stage 4 head. Cost: a possible ordinary merge conflict.
16. Use existing expressive narration for direction-bearing Character Voices; preserve book DOM drafts and use new tabs for Story/Story Builder handoffs. Cost: explicit engine choices and additional tabs.
17. Make the deterministic designer fixture accept native candidate seeds. Cost: fixture/native transport must remain aligned.
18. Assert exact owner projects in new tabs; explicitly reload for the empty-Library check. Cost: popup policy may require a visible fallback; owner URLs remain available.
19. Enforce Character direction compatibility in the audiobook owner as well as the UI. Cost: reference-only reuse of a direction-bearing Character requires choosing its parent Actor.
20. Do not reopen unchanged earlier stages during Stage 5 review; their touched boundaries and prior reviews remain the gate. Cost: inherited defects outside those boundaries need a separate fix.
21. Do not guarantee subjective generated-voice quality. Cost: users may need to edit descriptions or regenerate candidates.
22. Keep additional catalogue/parity features outside the accepted scope. Cost: those workflows require a separate scope decision.
23. Keep inherited unrelated timeline and EPUB parsing policies unchanged; the new extraction and editing boundaries were tested. Cost: inherited semantics outside those boundaries may still need separate work.
24. Treat VLC inspection and FFmpeg validation as player evidence, not universal player compatibility. Cost: another player may expose a format-specific quirk.
25. Keep regex/phonemes and automatic pronunciation application excluded; explicit literal corrections satisfy the accepted scope. Cost: more advanced pronunciation requires later work.

## Deferred minors and limits

No review minors were deferred. This work does not promise universal player behavior,
subjective voice quality, universal emotion markup, multi-narrator books or catalogue parity.
Legacy narration paths retain their existing differences in retry/resume support.
Implementation and GitHub integration do not replace the user's running installation;
using the new binary requires its normal build/update and restart workflow.
