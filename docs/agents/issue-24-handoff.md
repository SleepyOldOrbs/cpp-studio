# Issue #24 implementation handoff

Last updated: 2026-08-07

## Objective

Implement GitHub issue #24, **Unify Library browse, search, and launch**, on
branch `codex/unified-library`, stacked on issue #23 commit `f7f65e1`.

## Product contract

- One read-only Library response discovers Actor Voices with nested Character
  Voices, reusable audio grouped by role, Stories, Story Builder Projects,
  Audiobooks, mixed masters, and delivery exports.
- Every entry exposes stable kind, identity, name, timestamps, relevant subtype
  or relationship metadata, and only the preview, artifact, launch, or delete
  actions that its owning module supports.
- Search covers entry names, Actor parent names, Character Voice directions,
  relationships, and reusable-audio roles. Empty and reloaded catalogs remain
  deterministic.
- Purpose-built stores remain the source of truth. The Library cannot generically
  edit or delete a Voice, Story, Story Builder Project, Audiobook, render, or
  export.
- The original saved-audio/image `items` projection and its save, preview,
  download, and delete behavior remain compatible.

## Decisions

- Keep aggregation in an `internal/library` read model and inject the existing
  store list functions. Gateway only accepts the search query and serializes the
  result.
- Reuse `GET /v1/library` so the browser makes one catalog request instead of
  assembling six collections. Preserve `items` in the response for Story Builder
  and existing console consumers.
- Represent navigation and artifact access as server-derived actions. The browser
  follows those actions rather than reconstructing domain URLs.

## Verification

- Unit tests cover mixed aggregation, nested voices, relationship and role
  search, deterministic reload, empty sources, and source failures.
- Gateway tests cover the stable response, query validation, owning action URLs,
  legacy items, and rejection of generic domain deletion.
- The real-browser fixture covers mixed groups, nested Character Voices, search,
  reload, owning-tool launch with identity, render/export discovery, and empty
  state.

## Exact next step

Run the focused and full verification gates, then perform the two-axis review
against `f7f65e1`. Address every actionable finding before committing, pushing,
and opening a draft pull request based on `codex/story-builder-exports`. Do not
merge without explicit user approval.
