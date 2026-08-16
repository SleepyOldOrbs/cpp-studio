# Prepared Corpus preserves speech masters and exports immutable packages

## Status

Accepted

## Context

Corpus preparation must keep carefully selected character performances useful
independently of any one trainer. VoxCPM2 has a specific 16 kHz mono JSONL input
contract, while the studio already has Stable Audio models that are effective
for applause, laughter, Foley, ambience, and other non-speech generation.
Mixing both concerns into one annotation and corpus workflow would complicate
identity, verification, filtering, and readiness without helping the first
single-voice LoRA proof.

## Decision

The Corpus Preparation Workspace handles speech only. Every Source Annotation
belongs to exactly one Performance Identity, and every successful extraction
creates a self-contained Corpus Item bundle containing the best available
lossless extracted WAV, metadata, and transcript. Trainer-specific resampling
never replaces that master WAV. Non-speech capture and training are outside this
workspace; the existing Stable Audio models remain the route for generated
applause, laughter, Foley, and ambience.

Performer and Character are both studio-wide reusable identities rather than
Source Series children. Their pairing is a reusable Performance Identity with a
stable, automatically assigned colour that the human may change. Character
names are globally unique, and each Character belongs to exactly one Performer;
one Performer may portray many Characters.

Only human-verified, eligible Corpus Items can enter a voice Dataset Package.
Adjust and Reprocess retains the earlier bundle but marks it Superseded and
excluded by default. The human may restore, exclude, or permanently delete an
individual Corpus Item.

A Dataset Package is an immutable snapshot for one Performance Identity. The
first adapter exports VoxCPM2 LoRA input: 16 kHz mono PCM WAV copies and one
verified `audio`/`text` pair per `train.jsonl` row. It omits optional reference
audio, dataset identifiers, and an automatic validation split, and must pass the
official VoxCPM validator before becoming ready. Later Corpus Item or transcript
changes require a new package rather than altering a prior package. Resampling
and mono conversion are the only automatic audio transforms; denoising,
normalisation, silence trimming, and dynamic compression are excluded. Packages
are retained in managed storage, presented by the Library, and may be explicitly
deleted without affecting their source Corpus Items.

## Consequences

The Prepared Corpus stays reusable and lossless while the first export remains
small and directly compatible with the selected trainer. Training runs can
always identify the exact package they consumed. Sound generation remains a
separate existing capability instead of creating a second corpus taxonomy,
verification flow, and trainer integration in this project.

The Prepared Corpus itself has no five-to-ten-minute target or upper bound. That
VoxCPM guidance is displayed only as early-training context; clean items may
continue accumulating across many Episodes for future cloning systems.
