# Source Catalogue owns corpus preparation

## Status

Superseded

Superseded on 2026-08-16 by the deliberately browser-local clean-speech flow in
`docs/agents/transcribe-extract-plan.md`. There is no Source Catalogue or durable
annotation owner in the implemented first slice.

## Context

The Library is the studio-wide read-only browse, search, and launch surface,
while corpus preparation requires durable imported programme media, editable
time-based annotations, and human review declarations. Making the Library own
those writes would turn its read model into a second persistence system.

## Decision

The Source Catalogue owns Source Series, Source Seasons, Source Episodes, their
project-managed media copies, annotations, and review declarations. The Library
presents and launches Source Catalogue work through its existing read-model
role. Replacing a Source Episode's managed media clears its annotations because
their time ranges belong to the exact recording that was reviewed.

The human creates the catalogue structure and imports media into the chosen
location. Every Source Episode belongs to a Source Series. Source Seasons are
optional folders: a Source Episode may sit directly inside its Source Series,
and the product does not manufacture an Unnumbered or placeholder season. The
human may rename and rearrange the structure without invalidating media or
annotations. An exact byte-for-byte duplicate of already managed Source Media
is reported and not copied again.

Removing Source Media removes both the managed recording and its Source
Annotations while retaining the Source Episode catalogue record. Deleting the
Source Episode also removes that record. In either case, previously extracted
Corpus Items survive with frozen source provenance because they own the useful
WAV and transcript independently.

Source Episodes have no automatic retention or cleanup policy. Processed and
unprocessed Episodes remain until the human explicitly removes Source Media or
deletes any selected Source Episodes through actions presented by the Library.

## Consequences

The Corpus Preparation Workspace can deepen the existing Audio Workspace while
persisting through one purpose-built owner. Source Catalogue material remains
independent of training tools and Dataset Packages, and the Library does not
become an alternative write path. Removing a source cannot silently destroy the
prepared material required to repeat training later.
