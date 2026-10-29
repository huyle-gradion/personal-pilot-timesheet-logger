---
status: accepted
date: 2026-10-29
deciders:
  - Huy Le Minh
related:
  - 0008
---

# Chat-confirmed submission

The owner approved a separate chat-confirmed consent path in issue #215.
Requiring terminal authorization on this path would defeat the requested
experience. `/timesheet` still logs through a ready saved plan only.

The model-invoked `submit` skill handles `/submit` and natural-language submit
requests. It first displays the exact plan day and every operation and waits
for an explicit affirmative user reply. Missing, declined or ambiguous
confirmation never authorizes an approval or write. Prior logging or a prior
submit request is not confirmation of a plan not yet displayed.

One explicit CLI wrapper, `agent-submit`, reuses the existing session ops.
Without confirmation inputs it returns the preview plus a deterministic
`review_id`. That digest binds the exact plan, day, absolute store path and
existing endpoint/credential context digest. The submission invocation must
supply the exact `--plan-id` and `--review-id` plus `--confirm-chat`. A changed
binding fails before approval. Fresh plan and classification checks then
create a native approval and consume it once under the same exclusive store
lock. Existing durable pre-write intents and uncertain-outcome barriers remain
authoritative. No new persisted grant, protocol op or state migration exists.

This assertion is not cryptographic proof of human presence or consent.
The CLI cannot distinguish a faithful agent from a script asserting consent.
Review and chat consent are enforced by the skill and the agent, with this
trust limit explicitly accepted. A `review_id` is not a secret or a grant.
It prevents stale context, not malicious approval. A persisted grant was
rejected because it duplicated the existing approval lifecycle. Terminal
authorization was rejected because the owner explicitly chose in-chat consent.

Legacy `approve`, `submit` and `resolve-write` retain their terminal checks
and typed-identifier prompts. The optional trusted Pi adapter retains its
restricted model tools; this regular-agent workflow grants it no capabilities.
Unknown or partial outcomes remain reported, never retried automatically or
silently resolved. A fresh submit request always needs fresh review and chat
confirmation. A zero-operation plan is an honest no-op. No live customer writes
are part of development verification; tests use the loopback fake remote.
