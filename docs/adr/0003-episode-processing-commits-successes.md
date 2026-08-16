# Episode processing commits successes without a durable queue

## Status

Accepted

## Context

One Source Episode is marked and processed in a deliberate browser sitting.
Persisting every handle movement or building recoverable queue orchestration
would add a second editing and job lifecycle, while losing successfully
extracted audio because one later item failed would waste human review work.

## Decision

Draft Annotations remain browser-local until GO starts Process Episode. That
human action marks the Source Episode Reviewed and drives extraction and Speech
transcription one annotation at a time. Each successful WAV persists immediately
as a Corpus Item with its Source Annotation; failed items remain retryable only
in the current browser session. Closing the browser stops the attempt and
discards all unprocessed drafts. There is no durable or automatically resumable
queue.

An exact processed annotation cannot be processed twice. A processed range keeps
its Performance Identity colour with a processed pattern and check mark. Adjust
and Reprocess temporarily returns it to draft editing; success creates another
Corpus Item without overwriting the earlier WAV, while abandonment restores the
last processed range.

## Consequences

The human is responsible for completing an Episode processing pass before
closing the browser. Individual extraction or transcription failures cannot
roll back other successes, Retry Failed remains available during that session,
and Corpus Items already stored in the Prepared Corpus survive interruption.
