# Go rules: a portable reliability profile

> Language-specific companion to [`tigerstyle.md`](tigerstyle.md). This document
> is generic Go guidance: no project-specific limits, issue identifiers, or
> evidence formats. It assumes the TigerStyle core and applies it to Go.
> Keep Go's native casing, type system, `gofmt`, and `go vet`.

These rules target Go code that handles external input, bounded work, durable
state, or network boundaries — services, workers, CLIs, and adapters.

## Panic only for programmer-contract violations

Use `panic` (or an assert helper that panics) exclusively for impossible internal
states, violated invariants, corrupt transitions, and ownership errors. Assert at
the point where the invariant is known, keep it enabled in every environment, and
let the affected process terminate.

```go
if activeSlotToken != completionSlotToken {
	panic("GO_OUTBOX_001_SLOT_OWNER")
}
```

- Do **not** `recover` a programmer-contract violation and translate it into a
  normal return value, an HTTP response, or an acknowledgement.
- Assertion messages use a fixed, non-secret invariant identifier and exclude
  secrets, client content, raw payloads, paths, and object bytes.
- Every fatal path has a subprocess test proving nonzero exit, no false
  acknowledgement, and replay-safe durable state.

## Make operating errors typed and bounded

Every failure allowed by the contract is a value, not a panic: invalid input,
dependency failure, capacity pressure, deadline, retry exhaustion. Return a
typed outcome with a stable identifier and a bounded message; carry retry,
acknowledgement, and persistence behavior explicitly. Keep error variants
exhaustive and switch on them explicitly — no string matching, no blanket
`if err != nil { return err }` that loses classification.

```go
type Outcome int

const (
	OutcomeAcknowledged Outcome = iota // committed receiver result or valid duplicate
	OutcomeRetryable                   // transport, deadline, invalid 2xx, 408, 429, 5xx
	OutcomeTerminal                    // bound overflow, 3xx, other 4xx, signature/schema failure
)
```

## Bound every externally controlled dimension

Externally controlled work is unbounded until a named limit has an exact value, a
unit, and an enforcement owner. Common dimensions: request/body bytes, item
counts, deadlines, queue records and bytes, retry attempts and age, backoff base
and cap, batch files and bytes, scratch bytes, object bytes, decoded memory,
and active goroutines/tasks.

- Include units in names: `maxImageBytes`, `callbackAttemptDeadlineSeconds`,
  `maxConcurrentBatches`, `maxOutboxEvents`.
- Enforce at admission **and** at every place the value can change or bypass,
  including the sending side. Test just below, at, and just above each bound.
- Keep every limit independent: byte, pixel, decoded-memory, scratch, queue, and
  concurrency checks do not substitute for one another.

## Bound concurrency and goroutine lifetimes

Never start a goroutine without a bound on how many can run and a way for it to
end. Give each worker a context with a deadline and cancel/join it. A leaked
goroutine is an unbounded resource.

- Cap active work with an explicit semaphore or worker count.
- Never leave an unbounded channel, wait, or select without a termination path.
- Prefer `context.Context` deadlines over ad-hoc timers, and ensure every phase
  of a call honors the same deadline.

## Keep authority out of the process

Do not let a process own a durable invariant it cannot enforce. If another
component (a database, object store, or service) is the durable authority, this
process reports to it through a verified boundary instead of writing its state
directly. A local precondition improves diagnostics; it never replaces the
durable rule.

- Producer checks what it creates before persistence or transmission.
- Consumer independently checks untrusted bytes and metadata before use.
- Never rely on the other side's check as your own.

## Durable state: write and sync before you depend on it

For any durable side effect, write and `fsync` the exact payload before acting on
it, then acknowledge only after the receiver's bounded, validated result.

- Record the exact bytes and their hash durably before transmission.
- Delivery may be at-least-once; make application idempotent: same identity +
  same body is an idempotent acknowledgement; same identity + different body is a
  conflict that is never applied.
- Persist terminal and acknowledged records as replay-suppression state.
- Prove restart and replay behavior for every durable state, including pending,
  reserved, acknowledged, and terminal.

```go
// write + fsync the event body and hash before any network call
if err := outbox.PutAndSync(evt); err != nil {
	return OutcomeRetryable, err
}
```

## HTTP client and server discipline

- **Disable redirects.** A redirect is a terminal protocol result, not a silent
  follow.
- **One deadline per attempt.** Derive the request from a context with a deadline
  and configure client and transport timeouts so no phase outlives it.
- **Bound the response before parsing.** Set `MaxResponseHeaderBytes`; hard-limit
  the body size before JSON parsing. Exceeding either is terminal.
- **Always close bounded response bodies.**
- **Classify explicitly:** valid bounded 2xx = acknowledged; transport/deadline/
  invalid 2xx/408/429/5xx = retryable within limits; 3xx, other 4xx, bound
  overflow, signature/timestamp/schema failure = terminal.
- **Verify authenticity before use.** Compare MACs in constant time; check
  timestamp windows, request identity, body size, version, and schema before any
  durable work. Hash the exact raw body; never re-serialize before hashing.

```go
client := &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
	Transport: &http.Transport{MaxResponseHeaderBytes: maxAckHeaderBytes},
}
```

## Filesystem and subprocess safety

- Open paths with `O_NOFOLLOW` descriptor traversal; reject symlinks at any path
  component and non-regular final nodes.
- Use restrictive modes: `0600` for files, `0700` for private directories.
- Hash bytes read from the final descriptor and compare to the expected digest;
  never derive authority from the file you are validating.
- Put a child in its own process group. Forward SIGINT/SIGTERM only to that
  group, wait a bounded time, then escalate to SIGKILL. Never signal a parent or
  an unrelated process group.
- Give child execution a bounded timeout and a bounded, regular-file receipt.

## Keep dependencies minimal

Add a dependency only when the standard library or an existing dependency cannot
meet a named contract. Record the missing capability, the maintenance owner, the
security and license review, and the removal condition. Prefer the standard
library; a smaller dependency surface is easier to bound and audit.

## Bound redacted logs and errors

Logs and errors are bounded and keyed by stable identifiers. Never include
secrets, credentials, client content, raw payloads, full paths, stack traces, or
object bytes. A fatal assertion logs only its fixed invariant identifier.

## Keep functions reviewable

Keep operation-level branching in parent functions that own the operation; keep
leaf helpers focused on computation; give each state transition one mutation
owner. Measure production functions (not generated code, data declarations, or
test fixtures) after `gofmt`.

A project-set size limit is measured on the formatted body, excluding the
declaration and closing delimiter. A longer function needs a recorded exception:
exact function and measured line count, the correctness/ownership reason
splitting is less safe, the review owner and the event that ends or renews the
exception, and positive/negative tests covering the long control flow. Do not
rewrite an existing function only because it is long — first record a real defect
and a failing regression test.

## Verify in this order

```sh
gofmt -l .          # formatting
go vet ./...        # static checks
go test ./...       # behavior
go test -race ./... # concurrency safety
```

Passing these commands is necessary, not sufficient. Behavioral evidence still
covers the valid path, every invalid class, every bound edge, every impossible
internal transition, subprocess proof for fatal assertions, and restart/replay
proof for durable side effects.
