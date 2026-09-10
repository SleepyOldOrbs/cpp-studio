# Astra review fixes

Scope: the nine defects and two performance observations in the 2026-09-10
whole-codebase review, against HEAD `9dd8f79` plus existing local edits.
Preserve those edits and keep native GPU engines and real user media untouched.

## Implementation plan and acceptance

| Item | Change | Acceptance evidence |
|---|---|---|
| ST1 | Exclusive process installation and process-owned completion/readiness | Concurrent starts and failed-start recovery tests; existing lifecycle suite |
| ST2 | Serialize legacy voice migration and publish atomically | Concurrent legacy loads and failed publication preserve valid manifest |
| ST3 | Resolve voice design through catalogue; preserve aliases | Canonical IDs and legacy aliases select correct engine; unsupported model rejected |
| ST4 | Separate resident request failures from managed process lifecycle | Cancellation/error then successful request preserves process health; real process exits remain authoritative |
| ST5 | Stop verification/package/smoke scripts on native failures | Inject failed native checks/builds and verify immediate failure |
| SP1 | Keep production identity separate from active job identity | Deletion refused during retry and attempt render; allowed after completion |
| SP2 | Preserve validated dialogue trim bounds, protect source identity | Valid generated trim accepted/rendered; invalid trim/source replacement rejected |
| SP3 | Preserve newer duration during save reconciliation | Delayed save cannot undo subsequent duration change |
| SP4 | Ship model manifest and surface explicitly configured load failures | Archive contains/load-validates manifest; missing/invalid configured manifest fails visibly |
| PF1 | Poll lightweight status, refresh project only on change | Unchanged polls do not fetch project/redo timeline; completion/cancel still refresh |
| PF2 | Release shared mutation lock during expensive render/export work | Other project saves progress during blocked work; concurrent edits/deletion cannot publish stale artifacts |

Run focused regressions first, then formatting, all Go tests, vet, JavaScript
behavior/syntax tests, isolated fixture/browser smoke, and actual packaging.
Use generated fixtures and temporary output, not real user productions.
No new inference model, streaming feature, broad rewrite, or GitHub publication
is included. Record final validation and any limits here before completion.

## Implementation outcome

All nine defects and both performance observations are implemented. Voice Design
also retains the optional-models configuration behavior by embedding the existing
root catalogue, while an explicitly configured broken catalogue remains an error.
Resident request errors no longer overwrite process lifecycle state. Render/export
publication checks the captured project revision after expensive work, so edits
or deletion cannot publish obsolete results.

Two independent Astra follow-up reviews found no remaining actionable issue in
the implemented findings. The ten files already modified before implementation
were compared against the saved starting diff and preserved.

## Verification

- All Go packages: `go test ./...` and `go vet ./...` passed on Windows.
- Four Node state regressions passed: delayed save reconciliation and terminal
  completion, cancellation, and failure polling behavior.
- Native-failure injection passed for verification stages and release building.
- The full verification script passed in a temporary source copy, including the
  benchmark harness and fixture smoke. After the catalogue compatibility fix,
  all Go tests, vet, Node tests, and native-failure checks passed again.
- The Windows release archive built successfully, contains the matching
  `models.json`, and its executable passed a configuration check using that
  packaged catalogue and fixture executable.
- Formatting, JavaScript syntax, and `git diff --check` passed.
- The broader Story Builder browser smoke passed arrangement, serialized saves,
  keyboard editing, audio trim, and dialogue-status checks, then timed out waiting
  for `.dialogue-text-inline` after a Character Voice drag. An isolated checkout
  of the original HEAD plus the ten original local edits reproduced the identical
  timeout. A diagnostic drag attempt found studio/track headers intercepting the
  pointer during scrolling. This inherited browser acceptance failure remains
  open; the complete browser suite is not reported as passed. No speculative UI
  fix was added to the bounded report implementation.

Evidence is retained under `out/astra-review-20260910/`; `verification.log`
records the full isolated gate. Review-only Go evidence is archived with `.go.txt`
extensions so `go test ./...` does not discover it as extra source packages.
`browser-verification.log` and `browser-baseline.log` retain the matching browser
failures; the baseline used no implementation changes from this review.

Real GPU inference latency and human audio quality were not measured. The
performance fixes have controlled regression evidence for reduced refreshes and
independent project access, not a claimed percentage speedup. Linux CI and the
Go race detector were not run locally. Changes remain uncommitted for review.
