// transport.ts — the single session-scoped compiled-Go child over stdio JSONL.
//
// Security contract (docs/agents/timesheet-interface.md, R5 packet §2):
// - Exactly one child process per Pi session. No daemon, no respawn, no
//   automatic replay of pending operations, especially writes.
// - The adapter never reads configuration, environment files, records or
//   exports. It only spawns the binary with the two fixed launcher-provisioned
//   paths and a minimally filtered environment.
// - Requests are strictly serialized: one in-flight operation at a time, a
//   bounded queue, bounded frames, exact request-id correlation, fail-closed on
//   identity mismatch or any malformed/EOF/extra-id response.
// - Readiness means identity negotiation FINISHED successfully. One shared
//   startup is awaited by every caller; no ordinary operation is dispatched
//   before it passes.
// - Every active or queued caller settles exactly once on failure, cancellation,
//   shutdown or replacement. A failure after dispatch is never proof of zero
//   effects; cancellation is local and never implies the child did nothing.
// - Callbacks and frames from a replaced child can never affect the new one.

import { spawn, type ChildProcess, type StdioOptions } from "node:child_process";
import { randomUUID } from "node:crypto";

/** Application identity this adapter is compiled against. Negotiated with the
 *  child before any action; a mismatch fails closed. Must keep matching
 *  internal/timesheet/model.go AppVersion. */
// PAIRED with internal/timesheet/model.go AppVersion (negotiated fail-closed) — update both together; adapter_pin_test.go fails on drift.
export const APP_VERSION = "0.2.0-dev.7";
/** Public protocol schema negotiated with the child (docs/agents/timesheet-interface.md). */
export const PROTOCOL_SCHEMA = 1;
/** Durable store schema the running build must report. */
export const STORE_SCHEMA = 3;

const MAX_FRAME_BYTES = 16 * 1024 * 1024; // bounded RAW frame size, both directions (LF excluded)
const UTF8_FATAL = new TextDecoder("utf-8", { fatal: true });
const MAX_QUEUE = 12; // bounded serialized request queue
const HANDSHAKE_TIMEOUT_MS = 8000;
const CLOSE_GRACE_MS = 1500;
const KILL_GRACE_MS = 500;
const MAX_STDERR_LINES = 40;
const MAX_STDERR_LINE_CHARS = 512;

export interface ProtocolErrorShape {
  code: string;
  message: string;
}

export interface TransportOptions {
  /** Absolute path of the compiled Go binary (launcher-provisioned, trusted). */
  binary: string;
  /** Absolute path of the non-secret JSON configuration. Loaded by Go only. */
  configPath: string;
  /** Absolute path of the dotenv file. Loaded by Go only. */
  envFile: string;
  /** Filtered environment for the child (built by launch.mjs). */
  childEnv: Record<string, string>;
  /** Isolated working directory for the child. */
  workdir: string;
}

export interface OpOk<T = unknown> {
  ok: true;
  result: T;
}
export interface OpErr {
  ok: false;
  error: ProtocolErrorShape;
}
export type OpResult<T = unknown> = OpOk<T> | OpErr;

export type TransportState = "idle" | "starting" | "ready" | "failed" | "closed";

/** Status negotiation result (subset of internal/timesheet session.go). */
export interface StatusResultShape {
  app_version: string;
  protocol_schema: number;
  store_schema: number;
  durable_schema: number;
  durability_uncertain: boolean;
  migration_receipts: number;
  remote_bindings: number;
  unresolved_writes: number;
  plans: number;
  approvals: number;
  native_unresolved_writes: number;
  days: Array<{
    date: string;
    timezone: string;
    revision: number;
    saved_revision: number;
    dirty: boolean;
    confirmed_intervals: number;
    staged_intervals: number;
    confirmed_minutes: number;
    staged_minutes: number;
  }>;
}

interface Waiter {
  id: string;
  wire: string;
  /** True once the exact bytes were handed to the child's stdin stream. */
  dispatched: boolean;
  abandoned: boolean;
  resolve: (r: OpResult) => void;
  reject: (e: ProtocolErrorShape) => void;
}

function transportErr(message: string): ProtocolErrorShape {
  return { code: "transport_failed", message };
}

/** Plain-object guard: a parsed wire value is asserted into a shape only after
 *  its discriminator/identity fields were actually checked. */
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** Strict UTF-8: an invalid sequence is a transport failure, never silently
 *  replaced text that could be validated as if it were real data. */
function decodeFrame(bytes: Buffer): string | null {
  try {
    return UTF8_FATAL.decode(bytes);
  } catch {
    return null;
  }
}

/** The minimal status identity needed before any identity comparison. A
 *  malformed or null result is a transport failure, not a child refusal. */
function validateStatusIdentity(value: unknown): ProtocolErrorShape | null {
  if (!isRecord(value)) {
    return transportErr("malformed status identity: the result is not a JSON object");
  }
  if (
    typeof value.app_version !== "string" ||
    typeof value.protocol_schema !== "number" ||
    typeof value.store_schema !== "number"
  ) {
    return transportErr(
      "malformed status identity: application/protocol/store identity is missing or invalid",
    );
  }
  return null;
}

/** A failure after dispatch is an unknown outcome, never a proven no-op. */
function settledFailure(err: ProtocolErrorShape, dispatched: boolean): ProtocolErrorShape {
  if (dispatched) {
    return transportErr(
      err.message +
        " (the request was already dispatched; a dispatched write may have applied and its outcome is unknown" +
        " — do not retry; verify with a fresh read-only status/reconciliation and resolve any unresolved write explicitly)",
    );
  }
  return transportErr(err.message + " (the request was not dispatched; no replay)");
}

/** Serialized, correlated single-child Go transport. */
export class Transport {
  readonly opts: TransportOptions;
  private proc: ChildProcess | null = null;
  private stderrTail: string[] = [];
  private lifecycle: TransportState = "idle";
  private failReason: ProtocolErrorShape | null = null;
  private queue: Waiter[] = [];
  private pending: Waiter | null = null;
  private startup: Promise<void> | null = null;
  /** Monotonic child generation: stale callbacks/frames are ignored. */
  private epoch = 0;
  private lastIdentity: StatusResultShape | null = null;

  constructor(opts: TransportOptions) {
    this.opts = opts;
  }

  pid(): number | null {
    return this.proc?.pid ?? null;
  }

  getState(): TransportState {
    return this.lifecycle;
  }

  /** Bounded sanitized stderr tail for local display only. Never model-visible. */
  diagnostics(): string[] {
    return this.stderrTail.slice(-20);
  }

  failure(): ProtocolErrorShape | null {
    return this.lifecycle === "failed" ? this.failReason : null;
  }

  /** Negotiated identity; null before the first successful status. */
  identity(): StatusResultShape | null {
    return this.lastIdentity;
  }

  /** Spawn (if needed) and negotiate protocol/application identity before any
   *  other operation. Concurrent callers share ONE startup and all wait for it;
   *  readiness is reported only after identity negotiation succeeded. */
  ensureReady(): Promise<void> {
    if (this.lifecycle === "ready") return Promise.resolve();
    if (this.lifecycle === "failed" || this.lifecycle === "closed") {
      return Promise.reject(
        transportErr("transport is not open; reopen explicitly before operating (no automatic replay)"),
      );
    }
    return this.startShared();
  }

  /** Explicit human reopen after failure or interruption. Never automatic. */
  async reopen(): Promise<StatusResultShape> {
    await this.hardStop(false);
    this.lifecycle = "idle";
    this.failReason = null;
    await this.startShared();
    return this.lastIdentity ?? this.requestResult<StatusResultShape>("status");
  }

  /** Serialized request that resolves with the child result or throws its safe
   *  protocol error. Local transport failures still reject. */
  async requestResult<T = unknown>(op: string, fields: Record<string, unknown> = {}): Promise<T> {
    const envelope = await this.request<OpResult<T>>(op, fields);
    if (envelope.ok) return envelope.result;
    throw envelope.error;
  }

  private startShared(): Promise<void> {
    if (this.startup) return this.startup;
    // "starting" with no shared startup would otherwise start a second child
    // beside the first; fail closed and require an explicit reopen instead.
    if (this.lifecycle === "starting") {
      return Promise.reject(
        transportErr("child startup is in an inconsistent state; reopen explicitly (no automatic respawn)"),
      );
    }
    const startup = this.startOnce();
    this.startup = startup;
    const clear = () => {
      if (this.startup === startup) this.startup = null;
    };
    startup.then(clear, clear);
    return startup;
  }

  private isCurrent(child: ChildProcess, epoch: number): boolean {
    return epoch === this.epoch && child === this.proc;
  }

  private async startOnce(): Promise<void> {
    this.lifecycle = "starting";
    this.failReason = null;
    this.queue = [];
    this.pending = null;
    this.stderrTail = [];
    this.lastIdentity = null;
    const epoch = ++this.epoch;
    let child: ChildProcess;
    try {
      child = spawn(this.opts.binary, [
        "serve",
        "--config", this.opts.configPath,
        "--env-file", this.opts.envFile,
      ], {
        cwd: this.opts.workdir,
        shell: false, // never a shell
        env: this.opts.childEnv,
        stdio: ["pipe", "pipe", "pipe"] as StdioOptions,
      });
    } catch (spawnError: unknown) {
      const reason = transportErr("child process launch failed: " + safeReason(spawnError));
      this.lifecycle = "failed";
      this.failReason = reason;
      throw reason;
    }
    this.proc = child;
    child.on("error", (e: NodeJS.ErrnoException) => {
      if (!this.isCurrent(child, epoch)) return;
      this.failAndStop(transportErr("child process launch failed: " + safeReason(e)));
    });
    child.on("exit", (code: number | null, signal: string | null) => {
      if (!this.isCurrent(child, epoch)) return;
      if (this.lifecycle === "closed" || this.lifecycle === "failed") return;
      this.fail(transportErr(
        "child exited unexpectedly (" + (signal ?? "code " + code) +
        "); no automatic respawn or replay; reopen explicitly"));
    });
    // A child pipe error is a transport failure; it must settle callers
    // instead of surfacing as an unhandled stream error.
    child.stdin?.on("error", (e: NodeJS.ErrnoException) => {
      if (!this.isCurrent(child, epoch)) return;
      this.failAndStop(transportErr("child stdin error: " + safeReason(e)));
    });
    if (child.stderr) {
      child.stderr.setEncoding("utf8");
      child.stderr.on("data", (chunk: string) => {
        if (this.isCurrent(child, epoch)) this.pushStderr(chunk);
      });
    }
    void this.pumpStdout(child, epoch);
    try {
      await this.negotiate(epoch);
    } catch (startupError: unknown) {
      // Any negotiation failure must leave the transport FAILED and the child
      // stopped — never "starting" with a live child that a later ensureReady
      // could spawn beside. Malformed negotiation requires explicit reopen.
      if (this.isCurrent(child, epoch) && this.lifecycle === "starting") {
        this.failAndStop(transportErr(
          "child startup failed before identity was accepted: " + startupReason(startupError)));
      }
      throw this.failReason ?? startupError;
    }
  }

  /** Decode complete frames only: raw bytes are buffered until a newline, so a
   *  multi-byte UTF-8 sequence split across chunks is never corrupted and the
   *  bound is a real byte bound. */
  private async pumpStdout(child: ChildProcess, epoch: number): Promise<void> {
    let buffered: Buffer = Buffer.alloc(0);
    try {
      for await (const chunk of child.stdout!) {
        if (!this.isCurrent(child, epoch)) return;
        const bytes = Buffer.isBuffer(chunk) ? chunk : Buffer.from(String(chunk), "utf8");
        buffered = buffered.length ? Buffer.concat([buffered, bytes]) : bytes;
        let newline = buffered.indexOf(0x0a);
        while (newline >= 0) {
          // One complete RAW frame: bound it BEFORE decoding or dispatching, so
          // a newline, trailing whitespace or chunk alignment cannot exempt an
          // oversized frame from the byte bound.
          const frameBytes = buffered.subarray(0, newline);
          buffered = buffered.subarray(newline + 1);
          if (frameBytes.length > MAX_FRAME_BYTES) {
            this.failAndStop(transportErr("response frame exceeds the bounded frame size"));
            return;
          }
          const text = decodeFrame(frameBytes);
          if (text === null) {
            this.failAndStop(transportErr("response frame is not valid UTF-8"));
            return;
          }
          const trimmed = text.trim();
          if (trimmed) {
            this.dispatch(trimmed);
            if (!this.isCurrent(child, epoch)) return;
            if (this.lifecycle === "failed" || this.lifecycle === "closed") return;
          }
          newline = buffered.indexOf(0x0a);
        }
        // An unterminated remainder is bounded too, so a child cannot pause
        // just below the limit and then complete an oversized frame later.
        if (buffered.length > MAX_FRAME_BYTES) {
          this.failAndStop(transportErr(
            "unterminated response frame exceeds the bounded frame size"));
          return;
        }
      }
      if (!this.isCurrent(child, epoch)) return;
      if (this.lifecycle === "closed" || this.lifecycle === "failed") return;
      this.fail(transportErr(buffered.length
        ? "child closed stdout mid-frame (EOF); the response is incomplete and cannot be used"
        : "child closed stdout (EOF); no automatic respawn or replay"));
    } catch (readError: unknown) {
      if (!this.isCurrent(child, epoch)) return;
      this.failAndStop(transportErr("child stdout could not be read: " + safeReason(readError)));
    }
  }

  /** Correlated dispatch: only the single outstanding request id is accepted;
   *  duplicate/unknown/extra ids are a hygiene failure and fail closed. */
  private dispatch(text: string): void {
    const waiter = this.pending;
    if (!waiter) {
      this.failAndStop(transportErr("protocol response arrived with no outstanding request"));
      return;
    }
    let parsed: unknown;
    try {
      parsed = JSON.parse(text);
    } catch {
      this.failAndStop(transportErr("malformed protocol response"));
      return;
    }
    if (!isRecord(parsed)) {
      this.failAndStop(transportErr("malformed protocol response: not a JSON object"));
      return;
    }
    if (typeof parsed.id !== "string" || parsed.id !== waiter.id) {
      this.failAndStop(transportErr("unexpected or duplicate protocol response id"));
      return;
    }
    // The discriminator is validated, not asserted: a non-boolean "ok" is a
    // malformed transport frame, never a fabricated child refusal.
    if (typeof parsed.ok !== "boolean") {
      this.failAndStop(transportErr("malformed protocol response: missing boolean ok discriminator"));
      return;
    }
    if (parsed.ok) {
      this.pending = null;
      waiter.resolve({ ok: true, result: parsed.result });
    } else {
      const error = parsed.error;
      if (!isRecord(error) || typeof error.code !== "string" || typeof error.message !== "string") {
        this.failAndStop(transportErr("malformed protocol error envelope"));
        return;
      }
      this.pending = null;
      waiter.resolve({ ok: false, error: { code: error.code, message: error.message } });
    }
    this.drain();
  }

  /** Identity negotiation over the same serialized slot, before readiness. */
  private async negotiate(epoch: number): Promise<void> {
    const timer = setTimeout(() => {
      if (epoch !== this.epoch) return;
      this.failAndStop(transportErr("child did not negotiate protocol identity in time"));
    }, HANDSHAKE_TIMEOUT_MS);
    try {
      const envelope = await this.sendNow("status", {});
      if (!envelope.ok) {
        const error = envelope.error;
        if (error.code !== "transport_failed") this.failAndStop(error);
        throw this.failReason ?? error;
      }
      // Validate the minimal identity structure BEFORE reading any field.
      const invalidIdentity = validateStatusIdentity(envelope.result);
      if (invalidIdentity !== null) {
        this.failAndStop(invalidIdentity);
        throw invalidIdentity;
      }
      const status = envelope.result as StatusResultShape;
      const mismatch = checkIdentity(status);
      if (mismatch !== null) {
        this.failAndStop(mismatch);
        throw mismatch;
      }
      if (this.lifecycle !== "starting") {
        throw this.failReason ?? transportErr("child startup was interrupted before identity was accepted");
      }
      this.lastIdentity = status;
      this.failReason = null;
      this.lifecycle = "ready";
      this.drain();
    } finally {
      clearTimeout(timer);
    }
  }

  /** One direct send on the serialized slot (negotiation only). */
  private sendNow(op: string, fields: Record<string, unknown>): Promise<OpResult> {
    return new Promise<OpResult>((resolve, reject) => {
      if (this.pending) {
        resolve({ ok: false, error: transportErr("an operation is already in flight") });
        return;
      }
      const id = "r-" + randomUUID().slice(0, 12);
      const waiter: Waiter = {
        id,
        wire: JSON.stringify({ id, op, ...fields }) + "\n",
        dispatched: false,
        abandoned: false,
        resolve,
        reject,
      };
      this.pending = waiter;
      this.writeWaiter(waiter);
    });
  }

  private pushStderr(chunk: string): void {
    for (const raw of chunk.split(/\r?\n/)) {
      const bounded = raw.length > MAX_STDERR_LINE_CHARS ? raw.slice(0, MAX_STDERR_LINE_CHARS) : raw;
      if (bounded) this.stderrTail.push(bounded);
    }
    if (this.stderrTail.length > MAX_STDERR_LINES) {
      this.stderrTail = this.stderrTail.slice(-MAX_STDERR_LINES);
    }
  }

  /** Send one strictly serialized request. The promise settles with the child's
   *  own result/error envelope (OpResult), or rejects only for local
   *  transport-level failures. Nothing is dispatched before identity is ready. */
  request<T = unknown>(op: string, fields: Record<string, unknown> = {}): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const enqueue = () => {
        if (this.lifecycle === "failed" || this.lifecycle === "closed") {
          reject(transportErr("transport is not open; reopen explicitly (no automatic replay)"));
          return;
        }
        if (this.lifecycle !== "ready") {
          reject(transportErr("child is not ready; wait for identity negotiation or run /timesheet first"));
          return;
        }
        if (this.queue.length >= MAX_QUEUE) {
          reject({ code: "transport_busy", message: "too many operations queued for the child" });
          return;
        }
        const id = "r-" + randomUUID().slice(0, 12);
        const wire: Record<string, unknown> = { id, op };
        for (const [k, v] of Object.entries(fields)) {
          if (v !== undefined) wire[k] = v;
        }
        let payload: string;
        try {
          payload = JSON.stringify(wire) + "\n";
        } catch {
          reject({ code: "bad_request", message: "operation arguments cannot be encoded" });
          return;
        }
        // Same raw-frame definition in both directions: the terminating LF is
        // the frame boundary and does not count toward the bound.
        if (Buffer.byteLength(payload.slice(0, -1), "utf8") > MAX_FRAME_BYTES) {
          reject({ code: "bad_request", message: "operation arguments exceed the bounded frame size" });
          return;
        }
        const waiter: Waiter = {
          id,
          wire: payload,
          dispatched: false,
          abandoned: false,
          resolve: resolve as (r: OpResult) => void,
          reject: reject as unknown as (e: ProtocolErrorShape) => void,
        };
        this.queue.push(waiter);
        this.drain();
      };
      if (this.lifecycle === "starting") {
        // Wait for the shared startup instead of dispatching early.
        const startup = this.startup;
        if (startup) {
          startup.then(enqueue, (e: unknown) => {
            const err = e as Partial<ProtocolErrorShape>;
            reject(transportErr(typeof err?.message === "string" ? err.message : "child startup failed"));
          });
          return;
        }
      }
      enqueue();
    });
  }

  private writeWaiter(waiter: Waiter): void {
    const stdin = this.proc?.stdin;
    if (!stdin || stdin.destroyed || stdin.writableEnded) {
      this.failAndStop(transportErr("child stdin is not writable"));
      return;
    }
    try {
      stdin.write(waiter.wire, "utf8");
      // write returning false is ordinary backpressure: Node keeps the bytes
      // buffered and flushes them. It is NOT evidence that nothing was sent.
      waiter.dispatched = true;
    } catch (writeError: unknown) {
      this.failAndStop(transportErr("child stdin write failed: " + safeReason(writeError)));
    }
  }

  private drain(): void {
    if (this.pending !== null || this.lifecycle !== "ready") return;
    const next = this.queue.shift();
    if (!next) return;
    if (next.abandoned) {
      next.reject({
        code: "cancelled",
        message: "operation abandoned by the operator; observe current state before continuing",
      });
      this.drain();
      return;
    }
    this.pending = next;
    this.writeWaiter(next);
  }

  /** Local-only abandonment of queued requests. Does NOT kill the child and
   *  DOES NOT imply the child did not complete an applied operation. */
  abandonQueued(reason: string): void {
    for (const waiter of this.queue) {
      waiter.abandoned = true;
      waiter.reject({ code: "cancelled", message: reason });
    }
    this.queue = [];
    // The in-flight operation is NOT abandoned: its result is awaited so the
    // canceled caller's view is reconciled by what actually happened.
  }

  private fail(err: ProtocolErrorShape): void {
    if (this.lifecycle === "failed" || this.lifecycle === "closed") return;
    this.lifecycle = "failed";
    this.failReason = err;
    this.settleAll(err);
  }

  /** Fail closed and stop the child so no further frames or effects arrive. */
  private failAndStop(err: ProtocolErrorShape): void {
    this.fail(err);
    this.terminateChild();
  }

  private terminateChild(): void {
    const proc = this.proc;
    if (!proc) return;
    if (proc.exitCode === null && proc.signalCode === null) proc.kill("SIGKILL");
  }

  private settleAll(err: ProtocolErrorShape): void {
    const pending = this.pending;
    this.pending = null;
    if (pending) pending.reject(settledFailure(err, pending.dispatched));
    const queued = this.queue;
    this.queue = [];
    for (const waiter of queued) {
      waiter.reject(transportErr(err.message + " (the request was not dispatched; no automatic replay)"));
    }
  }

  /** Idempotent shutdown for session_shutdown/quit/session replacement. */
  async shutdown(): Promise<void> {
    await this.hardStop(true);
  }

  private async hardStop(polite: boolean): Promise<void> {
    const proc = this.proc;
    const wasFailed = this.lifecycle === "failed";
    // Invalidate every callback/frame of this generation before releasing it.
    this.epoch += 1;
    this.proc = null;
    this.startup = null;
    this.settleForClose();
    if (!wasFailed) this.lifecycle = "closed";
    if (!proc) return;
    if (polite && proc.exitCode === null && proc.signalCode === null) {
      try {
        proc.stdin?.write(JSON.stringify({ id: "close-x", op: "close" }) + "\n", "utf8");
      } catch {
        // fall through to signals
      }
      const clean = await Promise.race([waitExit(proc), sleep(CLOSE_GRACE_MS).then(() => false)]);
      if (clean) return;
    }
    if (proc.exitCode === null && proc.signalCode === null) proc.kill("SIGTERM");
    const exited = await Promise.race([waitExit(proc), sleep(KILL_GRACE_MS).then(() => false)]);
    if (!exited) proc.kill("SIGKILL");
    await waitExit(proc);
  }

  /** Settle every active/queued caller on shutdown; nothing is left hanging. */
  private settleForClose(): void {
    const pending = this.pending;
    this.pending = null;
    if (pending) {
      pending.reject(settledFailure(
        transportErr("the child session ended before this operation completed"),
        pending.dispatched,
      ));
    }
    const queued = this.queue;
    this.queue = [];
    for (const waiter of queued) {
      waiter.reject(transportErr("the child session ended before this operation was dispatched; nothing is replayed"));
    }
  }
}

function sleep(ms: number): Promise<boolean> {
  return new Promise((resolve) => setTimeout(() => resolve(false), ms));
}

function waitExit(proc: ChildProcess): Promise<boolean> {
  return new Promise((resolve) => {
    if (proc.exitCode !== null || proc.signalCode !== null) {
      void resolve(true);
      return;
    }
    proc.once("exit", () => void resolve(true));
  });
}

/** Stable local failure text for a failed negotiation; never child wire bytes. */
function startupReason(e: unknown): string {
  const err = e as Partial<ProtocolErrorShape> | null;
  if (err && typeof err.code === "string" && typeof err.message === "string") {
    return err.code + ": " + err.message;
  }
  return "unexpected local startup failure";
}

function safeReason(e: unknown): string {
  const msg = e instanceof Error ? e.message : String(e);
  const compact = msg.length > 120 ? msg.slice(0, 117) + "…" : msg;
  // Never echo environment values or paths beyond the failure class.
  return (e instanceof Error && "code" in e ? String((e as NodeJS.ErrnoException).code) : "unknown") + ": " + compact;
}

function checkIdentity(status: StatusResultShape): ProtocolErrorShape | null {
  if (status.app_version !== APP_VERSION) {
    return {
      code: "protocol_mismatch",
      message: "child application version " + status.app_version +
        " does not match the adapter's compiled identity " + APP_VERSION,
    };
  }
  if (status.protocol_schema !== PROTOCOL_SCHEMA) {
    return {
      code: "protocol_mismatch",
      message: "child protocol schema " + status.protocol_schema +
        " does not match the adapter's " + PROTOCOL_SCHEMA,
    };
  }
  if (status.store_schema !== STORE_SCHEMA) {
    return {
      code: "protocol_mismatch",
      message: "child store schema " + status.store_schema +
        " does not match the adapter's " + STORE_SCHEMA,
    };
  }
  return null;
}
