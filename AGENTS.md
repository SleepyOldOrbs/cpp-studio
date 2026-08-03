## Engineering approach: K.I.S.S. first

The default engineering strategy is the simplest complete solution that satisfies the
current request. Over-engineering is a defect: extra architecture, scope, abstraction,
and verification time must earn their place through a concrete current requirement.

### Firm rules

- Implement exactly the requested outcome and only the supporting changes required to
  make it correct. Do not turn a bounded task into a repository-wide programme.
- Do not implement adjacent improvements, speculative future requirements, or other
  architecture candidates unless the user explicitly requests them.
- Prefer direct, readable code and existing repository patterns. Modify an existing
  path before creating a parallel system.
- Do not add a framework, generic engine, plugin system, abstraction layer, interface,
  adapter, configuration switch, migration, or dependency for a single hypothetical
  future use case.
- Add a seam or generalized module only when present requirements demonstrate real
  variation, multiple concrete consumers, an unavoidable external dependency, or a
  safety constraint that cannot be handled clearly without it.
- Keep cohesive logic together. Do not split small behavior across extra files or types
  merely to make the design look architectural or independently testable.
- Keep diffs narrow. Do not perform unrelated cleanup, renaming, formatting, dependency
  upgrades, or refactors while completing a feature or fix.
- Use the smallest verification set proportional to the changed behavior and its risk.
  Run focused tests first; run broad or slow gates only when repository policy, release
  scope, cross-cutting risk, or the user requires them.
- Stop when the requested acceptance criteria are met and the relevant verification is
  green. Report optional follow-up ideas instead of implementing them automatically.
- If a materially broader solution appears necessary, explain the concrete blocker and
  obtain the user's approval before expanding scope.

K.I.S.S. does not override correctness, security, data safety, explicit product
boundaries, `CONTEXT.md`, or accepted ADRs. Choose the simplest solution that preserves
those constraints, not merely the solution with the fewest lines.

## Agent skills

### Issue tracker

Issues and PRDs are tracked in GitHub Issues for `SleepyOldOrbs/cpp-studio`. See `docs/agents/issue-tracker.md`.

### Triage labels

Use the five canonical triage labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, and `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

This is a single-context repository: read the root `CONTEXT.md` and relevant ADRs under `docs/adr/`. See `docs/agents/domain.md`.
