# Audiobook production owns durable orchestration

## Status

Accepted

## Context

Long-running narration must survive cancellation, process failure, and local engine
configuration changes without silently mixing incompatible speech or deleting a
recoverable plan. Splitting extraction, identity resolution, transport mapping, and
lifecycle authority across Gateway, the jobs registry, and Audiobook production would
make that guarantee depend on caller ordering.

## Decision

`internal/audiobook` owns document extraction, canonical source creation, resolution
of selected engine and voice identifiers through injected resolvers, the frozen
Synthesis identity, complete section planning and seed assignment, and the durable
production lifecycle. It atomically persists the source and complete initial manifest
before the first engine invocation. Gateway owns only HTTP envelope validation and
response shaping; `internal/engine` owns resident-server and subprocess transport
mapping; the shared jobs registry is only a projection of live activity.

Resume is valid only under the production's original Synthesis identity. A changed
identity requires an explicit Restart that creates a separate production and preserves
the original. Once the initial manifest exists, Cancel records `interrupted`; only an
explicit, validated Discard deletes durable production state.

## Consequences

The Audiobook production module has one high-leverage seam through which callers and
tests exercise creation, recovery, and publication. Its store remains domain-specific
instead of importing Story manifests. Identity mismatches are visible conflicts rather
than implicit regeneration, and interrupted productions may exist with zero completed
sections until the user resumes, restarts, or discards them.
