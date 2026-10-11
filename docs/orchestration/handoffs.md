# Handoff contracts

## Task packet

Fill every field before dispatch. If a field is unknown, identify the blocking question or authorized fact-finding step.

```text
Task ID and slice:
Objective and requirement links:
Authority: implement / fix / review / research; commit yes/no; live access yes/no
Execution mode: owner-mediated / direct
Selected agent and rationale (capability fit; record the actual runtime agent):
Repository and working directory:
Branch, base HEAD, dependent commits:
Snapshot ID and changed/untracked files:
Allowed files and ownership boundaries:
Inputs and targeted references:
Required behavior:
Explicit exclusions:
Acceptance tests and verification commands:
Risk cases to investigate:
Stop conditions:
Required report and handoff destination:
```

A snapshot identifies HEAD plus the exact dirty tracked and untracked content. Use a saved sanitized diff and file-content manifest, or equivalent reproducible hashes, when reviewing uncommitted work. HEAD alone is insufficient. Store task-specific private artifacts outside tracked source; never include credentials or private evidence. The orchestrator records the artifact reference, not a raw secret-bearing dump.

## Implementation or fix report

```text
Task ID / branch / base / snapshot:
Files changed and behavior:
Tests added, including demonstrated regression:
Commands, exit results, environment, and timestamp:
Acceptance criteria: pass / fail / not run, with evidence:
Known limitations and open findings:
Scope deviations and their authorization:
Current working-tree ownership:
No live writes / or separately authorized access summary:
Stopped for review; no commit performed:
```

List exactly which commands were run. Test package counts can change; use the actual output, not an expected number. Synthetic secret tests demonstrate specified redaction behavior, not universal protection against arbitrary encodings.

## Review report

```text
Task ID / exact reviewed snapshot:
Scope and references read:
Findings: ID, severity, file:line, failure scenario, evidence, recommendation:
Introduced defects / pre-existing issues / acknowledged gaps:
Acceptance claims stronger than evidence:
Checks run and results:
Verdict: accepted / changes required / blocked:
Commit suitability separately from production readiness:
Remaining verification limits:
Read-only compliance:
```

No findings is a valid result only with stated coverage and limits. “Reviewer approved” without the reviewed snapshot is insufficient for committing.

## Acceptance and commit record

```text
Slice and accepted snapshot:
Snapshot artifact reference (sanitized manifest/hash):
Independent reviewer report reference:
Reviewer independence (fresh agent / separate session, identity):
Findings resolved and deferred with rationale:
Final checks and exact results:
Commit permission reference:
Explicit staged paths:
Commit hash / branch / parent / publication status:
Vault synchronization: complete / pending, notes changed:
Remaining production blockers:
Next dependency-ready task:
```

## Resume packet

Give the next orchestrator the current state document, branch/HEAD, dirty-file owners, active worker sessions if any, exact authority, report references, open findings, and next gate. Do not convert a pending worker report into accepted work during a handoff.
