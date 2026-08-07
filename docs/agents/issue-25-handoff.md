# Issue #25 implementation handoff

Last updated: 2026-08-07

## Objective

Implement GitHub issue #25, **Protect voice and project deletion boundaries**,
on branch `codex/protect-deletion-boundaries`, stacked on issue #24 commit
`c2343ab`.

## Product contract

- Actor Voice deletion is blocked while a Story Builder Project depends on the
  Actor directly or through one of its Character Voices.
- Character Voice deletion is blocked while a project binds it to a Dialogue
  Track. A ready take does not remove that dependency because a future rebuild
  still needs the voice.
- A dependency conflict returns the affected project identities, and the
  browser presents their names before the user can try another destructive
  action.
- Project deletion removes only that project's manifest and owned media, takes,
  renders, and exports. It never deletes an imported Story, Actor Voice,
  Character Voice, or original reusable Library audio.
- A project remains usable after its source reusable Library item is deleted
  because placement copied the validated WAV into project-owned media.

## Decisions

- Keep dependency traversal in the Story Builder Store, which owns the complete
  saved project manifests. Its deletion guard holds the project mutation lock
  through Gateway's callback to the owning Voice Store, so a concurrent save
  cannot add a binding between the check and deletion.
- Inspect both Dialogue Track bindings and clip bindings so older manifests are
  protected, and return deterministic project id/name summaries.
- Preserve direct Actor ids already present in older manifests for traversal,
  but reject a newly submitted direct Actor-only binding; current bindings are
  created through a resolvable Character Voice and derive their Actor identity.
- Keep project cleanup as removal of the single project directory. This is the
  existing ownership boundary for all copied media and generated artifacts.
- Return one structured `409` response with `dependent_projects`; existing
  browser error surfaces present the explanatory project names without adding a
  separate confirmation protocol.

## Verification

- Store tests cover ready-dialogue and direct-Actor dependencies, the guarded
  deletion/autosave race, and project-directory cleanup without source-aggregate
  deletion.
- Gateway tests cover Actor and Character conflicts, exact dependent project
  identities, successful project deletion, and surviving Story, voice, and
  Library sources.
- The real-browser fixture covers visible dependency errors, scoped project
  confirmation copy, project removal, and source artifact survival.

## Exact next step

Run the focused and full verification gates, then perform the two-axis review
against `c2343ab`. Address every actionable finding before committing, pushing,
and opening a draft pull request based on `codex/unified-library`. Do not merge
without explicit user approval.
