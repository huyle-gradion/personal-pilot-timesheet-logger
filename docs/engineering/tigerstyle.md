# TigerStyle: a portable reliability core

> Language-agnostic. This document defines principles only: no project-specific
> limits, technologies, names, or evidence formats. A project profile binds these
> principles to its own authorities and constraints. When a project profile is
> more specific, the profile controls.

TigerStyle applies to any system that handles external input, bounded work,
durable state, or network boundaries. It is a reliability discipline, not a
language style. Adopt the principles as written; supply your own numbers.

## Set finite bounds and name enforcement ownership

Give every externally controlled loop, queue, retry, batch, allocation, and unit
of concurrent work a finite bound. Record the value, unit, scope, and the
component that enforces it. An unbounded operation is not acceptable because its
input is usually small. An intentionally nonterminating loop must assert the
invariant that permits it to continue.

Enforce a bound before work begins and at every boundary where the value can be
changed or bypassed. The producer and the consumer each own the check that
applies to their side of the boundary. Test the value just below, at, and just
above each bound.

## Classify failures before writing code

Separate contract-allowed operating errors from programmer-contract violations.

| Class                         | Meaning                                                                                                             | Required behavior                                                                                                                                                                                           |
| ----------------------------- | ------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Expected operating error      | Invalid external input, dependency failure, capacity pressure, or another condition allowed by the contract         | Return or persist a typed, bounded outcome. Define retry, acknowledgement, and durable-state behavior. Do not assert or terminate the process.                                                              |
| Programmer-contract violation | An impossible internal state, violated invariant, corrupt transition, or ownership error that correct code prevents | Assert at the point where the invariant is known. Keep the assertion enabled in every environment. Let the affected process terminate. Do not recover or translate the violation into an ordinary response. |

Assertions remain enabled in development, tests, staging, and production. An
assertion message uses a fixed invariant identifier and excludes secrets, client
content, raw payloads, and object bytes.

## Pair producer and consumer checks

Check a contract through distinct paths on both sides of a durable or network
boundary. The producer checks what it creates before persistence or transmission.
The consumer checks untrusted bytes and metadata before use.

The final durable authority remains the owner of its invariant. An application
precondition can improve diagnostics, but it does not replace a database,
filesystem, or storage rule.

Record the producer check, the consumer check, the durable authority, and the
state that remains after each failure. If one side does not exist yet, record the
owner and mark that check deferred. Sender-only evidence is not end-to-end
evidence.

## Write a resource sketch

Estimate the worst case before accepting a new externally controlled path. Show
the arithmetic, units, concurrency, and retry multipliers.

- Network: request and response bytes multiplied by retries and concurrent work.
- Disk or storage: inputs, outputs, metadata, temporary copies, retained records,
  and cleanup lag.
- Memory: encoded and decoded data, parser overhead, queue entries, and
  concurrent work.
- CPU: work per item multiplied by item count, retries, and concurrency.
- Latency: phase deadlines, queue delay, retry backoff, and total work age.
- Bandwidth: peak bytes per second and sustained transfer volume.

Identify the component that enforces each factor. A statement such as "the batch
is small" is not a resource sketch.

## Keep control flow reviewable

Keep branching decisions in parent functions that own the operation. Keep leaf
helpers focused on computation. Give each state transition one mutation owner.
Make loops, retries, and failure paths visible enough for a reviewer to trace
their bounds and outcomes without reconstructing hidden control flow.

When a project sets a function-size limit, measure production logic after
formatting and record an exception for code that cannot be split safely. The
exception names the exact scope, the safety reason, the review owner, the end or
renewal event, and the tests that cover the control flow.

## Use precise contracts and bounded redacted errors

Name identifiers for their domain meaning. Include units in names when the type
does not carry them. Assert distinct preconditions, postconditions, and
invariants at the transitions that own them.

Parse external data into a typed valid form once. Keep operating-error variants
explicit and exhaustive. Return stable error identifiers and bounded messages.
Exclude secrets, client content, raw payloads, paths, stack traces, and object
bytes from errors, logs, responses, and retained evidence.

## Require behavioral evidence

For every changed durable or network seam, record the governing authority, limits
and owners, failure classifications, paired checks, and the resource sketch.

Evidence covers the valid path, every invalid class, every bound edge, and every
impossible internal transition. A fatal assertion has an isolated subprocess or
equivalent process-boundary test. Every durable side effect has restart and replay
evidence. Evidence also shows that logs, responses, and retained artifacts contain
no secrets or restricted content.

Source-text searches, field counts, and passing scaffold commands do not replace
behavioral evidence.

## Primary sources

- [TigerStyle](https://tigerstyle.dev/)
- [TigerBeetle Tiger Style](https://github.com/tigerbeetle/tigerbeetle/blob/main/docs/TIGER_STYLE.md)
- [The Primeagen interview with Joran Dirk Greef](https://www.youtube.com/watch?v=w3WYdYyjek4)
- [The Power of Ten: Rules for Developing Safety-Critical Code](https://spinroot.com/gerard/pdf/P10.pdf)
