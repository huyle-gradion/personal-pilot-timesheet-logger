// timesheet.ts — the trusted Pi adapter for the daily timesheet workflow (R5).
//
// Responsibilities: one session-scoped compiled-Go child via transport.ts,
// local forms rendered via view.ts, and exactly ONE model-declared preparation
// tool. The compiled binary owns grammar, minutes/allocations, revisions,
// identity, persistence and readiness; the adapter owns transport/UI only:
// no duplicate accounting, no provider fetching, no local scanning, no direct
// store writes, no classifier, no editorial model calls in this slice.
//
// Capability surface (fail-closed):
// - Only `timesheet_prepare` is model-declared/callable. Every other tool call
//   is blocked defensively in addition to the launcher's CLI isolation.
// - User `!`/`!!` shell dispatch is refused unconditionally via the user_bash
//   gate. Ordinary pasted/model/evidence text cannot become a local command.
// - Operator functions (acknowledge, attendance, classification selection,
//   approval, submission, recovery) are NEVER registered as tools — not even
//   hidden or deferred ones. They run only through these local forms.
// - Driver commands and custom screens are guarded with ctx.mode === "tui";
//   non-TUI modes fail safely instead of auto-answering forms.

import { Type, type Static } from "typebox";
import type { AgentToolResult, ExtensionAPI, ExtensionCommandContext } from "@earendil-works/pi-coding-agent";
import { Transport, APP_VERSION, type OpResult, type ProtocolErrorShape } from "./transport";
import {
  openScreen,
  renderBlocker,
  renderSegment,
  renderView,
  minutesLabel,
  sanitizeText,
  type BlockerShape,
  type ListChoice,
  type ScreenOptions,
  type SegmentShape,
} from "./view";

// --- child result shapes (local copies; Go is authoritative) -----------------

interface EvidenceItemShape {
  ref?: string;
  kind?: string;
  title?: string;
  time?: string;
  provenance?: string;
}

interface TopicShape {
  ref: string;
  title: string;
  provider: string;
  /** The child may serialize an empty topic list as JSON null. */
  items?: EvidenceItemShape[] | null;
  first_at?: string;
  last_at?: string;
  truncated?: boolean;
  dropped_items?: number;
}

interface MeetingShape {
  ref: string;
  uid?: string;
  title?: string;
  start: string;
  end?: string;
  all_day?: boolean;
  status?: string;
  attendance?: string;
  recurrence?: string;
  decision?: string;
}

interface SourceStatusShape {
  source: string;
  enabled: boolean;
  status: string;
  detail?: string;
  collection_id?: string;
  attempted_at?: string;
  acknowledged?: boolean;
}

/** Matches the child's accepted shapes: an evidence list with no rows is a
 *  valid JSON null, not a missing capability and not an error. */
interface EvidenceReviewShape {
  sources?: SourceStatusShape[] | null;
  topics?: TopicShape[] | null;
  meetings?: MeetingShape[] | null;
}

interface ReviewShape {
  date: string;
  timezone: string;
  revision: number;
  saved_revision: number;
  dirty: boolean;
  confirmed: { segments: SegmentShape[]; total_minutes: number };
  staged: { segments: SegmentShape[]; total_minutes: number };
  notes?: string[];
  cross_midnight?: Array<{
    interval_id: string;
    start: string;
    end: string;
    total_minutes: number;
    allocations: Array<{ date: string; start: string; end: string; minutes: number }>;
  }>;
  evidence?: EvidenceReviewShape;
}

interface EnvCheckShape {
  required: string[];
  missing: string[];
  present: string[];
}

interface CollectShape {
  date: string;
  collection_id?: string;
  no_sources?: boolean;
  sources: Array<{
    source: string;
    status: string;
    detail?: string;
    topics?: number;
    meetings?: number;
    acknowledged?: boolean;
  }>;
}

interface StatusShape {
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

interface ObservationShape {
  date: string;
  revision: number;
  observation_id: string;
  observed_at: string;
  complete: boolean;
  failed?: boolean;
  detail?: string;
  coverage?: string[];
  mapped: Array<{
    local_id: string;
    remote_id?: string;
    date?: string;
    start_time?: string;
    end_time?: string;
    changed?: boolean;
    accepted?: boolean;
  }>;
  changed: Array<{
    local_id: string;
    remote_id?: string;
    date?: string;
    start_time?: string;
    end_time?: string;
    changed?: boolean;
    accepted?: boolean;
  }>;
  missing: Array<{ local_id: string; remote_id?: string }>;
  unbound: Array<{
    remote_id: string;
    date: string;
    start_time: string;
    end_time: string;
    classification_id?: string;
    description?: string;
    task?: string;
    status?: string;
  }>;
  candidates: Array<{ local_id: string; remote_id: string; reason: string }>;
  orphaned: Array<{ local_id: string; remote_id?: string; date?: string }>;
  unresolved_outcomes?: BlockerShape[];
  blockers?: BlockerShape[];
  ready: boolean;
}

interface OperationShape {
  local_id: string;
  kind: string;
  remote_id?: string;
  date: string;
  start_time: string;
  end_time: string;
  minutes: number;
  classification_id: string;
  description: string;
  task?: string;
  reason?: string;
  crosses_midnight?: boolean;
  origin_dates?: string[];
  source_origin_revisions?: Record<string, number>;
  expected_remote?: { remote_id: string; description?: string } | null;
  selected_choice?: { id: string; name?: string; code?: string; activity_type?: string } | null;
}

interface PlanShape {
  date: string;
  plan_id: string;
  state: string;
  remote_complete: boolean;
  source_revisions?: Record<string, number>;
  operations: OperationShape[];
  blockers: BlockerShape[];
  surfaced_remotes?: Array<{ remote_id: string; date: string; description?: string }>;
  unresolved_outcomes?: BlockerShape[];
  approved: boolean;
  approval_available: boolean;
  persisted: boolean;
}

interface ReceiptShape {
  date: string;
  revision?: number;
  saved_revision?: number;
  durable?: boolean;
  intervals?: number;
  total_minutes?: number;
  discarded?: number;
  saved_at?: string;
}

interface SubmissionShape {
  date: string;
  approval_id: string;
  execution_id?: string;
  status: string;
  operations: number;
  attempts: number;
  confirmed: number;
  unresolved: number;
  unattempted: number;
  /** Per-reference attempt rows (Go WriteReceiptRow: status, not "outcome"). */
  writes?: Array<{
    write_ref: string;
    op_index?: number;
    action?: string;
    date?: string;
    local_id?: string;
    status?: string;
    remote_id?: string;
    error_code?: string;
  }>;
  blockers?: BlockerShape[];
  next_action: string;
  calls?: { write_calls: number; read_calls: number };
}

interface UnresolvedRowShape {
  origin: string;
  write_ref: string;
  status?: string;
  error_code?: string;
  action?: string;
  date?: string;
  local_id?: string;
  remote_id?: string;
  detail?: string;
}

interface SubmissionStatusShape {
  date: string;
  approvals: Array<{ approval_id: string; plan_id: string; state: string; approved_at?: string }>;
  unresolved: UnresolvedRowShape[];
  next_action: string;
}

interface ClassificationShape {
  catalog_id: string;
  query?: string;
  complete: boolean;
  options: Array<{ id: string; name?: string; code?: string; activity_type?: string }>;
  observed_at?: string;
  note?: string;
}

interface MigrationPreviewShape {
  migration_id: string;
  dates: string[];
  timezone: string;
  ready: boolean;
  no_change: boolean;
  existing_receipt?: string;
  counts: Record<string, number>;
  conflicts: Array<{ code: string; date?: string; detail?: string }>;
  conflicts_truncated?: number;
  source_hashes: Record<string, string>;
}

interface MigrationApplyShape {
  migration_id: string;
  status: string;
  receipt_id: string;
  dates: string[];
  counts: Record<string, number>;
  unresolved_outcomes?: number;
}

interface AckShape {
  date: string;
  source: string;
  collection_id: string;
  acknowledged: boolean;
}

interface DecisionShape {
  date: string;
  meeting_id: string;
  decision: string;
}

interface PrepareDetails {
  day: string;
  collection_id?: string;
  child_state?: string;
}

interface UsageRow {
  input?: number;
  output?: number;
  cacheRead?: number;
  cacheWrite?: number;
  cacheWrite1h?: number;
  reasoning?: number;
  totalTokens?: number;
  observedAt: string;
}

// --- ephemeral runtime state -------------------------------------------------

interface AdapterState {
  transport: Transport | null;
  api: ExtensionAPI | null;
  day: string | null;
  review: ReviewShape | null;
  status: StatusShape | null;
  envCheck: EnvCheckShape | null;
  observation: ObservationShape | null;
  catalog: ClassificationShape | null;
  plan: PlanShape | null;
  submissionStatus: SubmissionStatusShape | null;
  lastSubmit: SubmissionShape | null;
  lastError: string | null;
  childPid: number | null;
  childInvocations: number;
  childOperations: number;
  modelRequests: number;
  usageRows: UsageRow[];
  lastCollectionId: string | null;
  /** True while the human /timesheet form flow owns the review state. */
  humanFlowActive: boolean;
  /** True while the one preparation tool call is executing. */
  toolBusy: boolean;
}

// AdapterState is created per extension runtime invocation (see the factory
// below), never at module scope: reload/new/resume/fork replace the runtime,
// and a stale continuation must only ever observe its OWN generation's state.
// Nothing survives those boundaries and nothing is replayed.

/** Launcher-provisioned child paths (constants for this Pi process). They are
 *  fixed inputs, never model arguments and never request fields. */
const CHILD = {
  binary: process.env.TIMESHEET_CHILD_BINARY ?? "",
  configPath: process.env.TIMESHEET_CHILD_CONFIG ?? "",
  envFile: process.env.TIMESHEET_CHILD_ENVFILE ?? "",
  workdir: process.env.TIMESHEET_CHILD_WORKDIR ?? process.cwd(),
  childEnv: childEnvironment(),
};

/** The child receives only required OS variables plus the explicitly
 *  provisioned timesheet credential NAMES. Inherited values are preserved
 *  verbatim, including empty strings. Chat-provider credentials are never
 *  forwarded wholesale, and nothing is ever logged. */
function childEnvironment(): Record<string, string> {
  const forwardedNames = (process.env.TIMESHEET_FORWARD_ENV ?? "")
    .split(",")
    .map((n) => n.trim())
    .filter((n) => /^[A-Za-z_][A-Za-z0-9_]*$/.test(n));
  const out: Record<string, string> = {};
  for (const name of ["PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "TZ", "SHELL", "USER", "LOGNAME"]) {
    const value = process.env[name];
    if (value !== undefined) out[name] = value;
  }
  for (const name of forwardedNames) {
    const value = process.env[name];
    if (value !== undefined) out[name] = value; // preserves inherited empty strings
  }
  return out;
}

function freshState(): AdapterState {
  return {
    transport: null,
    api: null,
    day: null,
    review: null,
    status: null,
    envCheck: null,
    observation: null,
    catalog: null,
    plan: null,
    submissionStatus: null,
    lastSubmit: null,
    lastError: null,
    childPid: null,
    childInvocations: 0,
    childOperations: 0,
    modelRequests: 0,
    usageRows: [],
    lastCollectionId: null,
    humanFlowActive: false,
    toolBusy: false,
  };
}

// --- transport helpers -------------------------------------------------------

function errText(e: unknown): string {
  const err = e as Partial<ProtocolErrorShape>;
  if (err && typeof err.code === "string" && typeof err.message === "string") {
    return `${err.code}: ${err.message}`;
  }
  return "internal: unexpected local failure";
}

// --- extension factory -------------------------------------------------------

export default function timesheetExtension(pi: ExtensionAPI): void {
  // Everything below belongs to this ONE extension runtime generation. Pi
  // creates a new generation for reload/new/resume/fork; each generation keeps
  // its own state closure, so an old continuation can never read, clear or
  // overwrite the replacement generation's day, review, guard or child.
  let state: AdapterState = freshState();
  state.api = pi;

  function ensureTransport(): Transport {
    if (!state.transport) {
      state.transport = new Transport({
        binary: CHILD.binary,
        configPath: CHILD.configPath,
        envFile: CHILD.envFile,
        childEnv: CHILD.childEnv,
        workdir: CHILD.workdir,
      });
    }
    return state.transport;
  }

  /** Wire envelope → result, or throw the child's safe error. */
  async function op<T = unknown>(name: string, fields: Record<string, unknown> = {}): Promise<T> {
    const transport = state.transport;
    if (!transport) throw { code: "transport_failed", message: "child not started" };
    state.childOperations++;
    const envelope = await transport.request<OpResult<T>>(name, fields);
    if (envelope.ok) return envelope.result;
    // Only a VALIDATED Go error envelope reaches here: the transport fails closed
    // on malformed wire data, so a fabricated local error can never masquerade as
    // a contract refusal. Callers distinguish this from a local transport/unknown
    // failure (which never proves zero effects).
    throw { ...envelope.error, from_child: true } as ProtocolErrorShape & { from_child: true };
  }

  // --- runtime lifetime ------------------------------------------------------

  /** True once Pi told this generation to shut down (quit/reload/new/resume/
   *  fork). Every continuation checks it before touching state or the UI. */
  let disposed = false;
  /** Custom screens awaiting their `done` callback; settled on disposal. */
  const pendingScreens = new Set<() => void>();
  /** Aborts signal-aware dialogs (input/confirm) on disposal. */
  const dialogAbort = new AbortController();
  let signalClosed: () => void = () => {};
  /** Resolves once this generation is disposed; races every awaited UI call so
   *  a dialog Pi detached without calling its callback still unwinds us. */
  const closed = new Promise<void>((resolve) => {
    signalClosed = resolve;
  });

  class DisposedError extends Error {
    constructor() {
      super("timesheet runtime was disposed by reload/session replacement");
    }
  }

  function assertLive(): void {
    if (disposed) throw new DisposedError();
  }

  /** Settle with `p`, or stop waiting immediately on disposal. The command
   *  handler is awaited by Pi's interactive input loop; a UI promise that can
   *  never resolve would otherwise block every later submitted command. */
  async function withLifecycle<T>(p: Promise<T>): Promise<T> {
    const outcome = await Promise.race([
      p.then(
        (value) => ({ kind: "value" as const, value }),
        (error: unknown) => ({ kind: "error" as const, error }),
      ),
      closed.then(() => ({ kind: "closed" as const })),
    ]);
    if (outcome.kind === "closed") throw new DisposedError();
    if (outcome.kind === "error") throw outcome.error;
    return outcome.value;
  }

  /** The adapter's UI needs: both lifecycle handlers and command handlers
   *  expose `ui`, so one structural type keeps the guards honest in both. */
  type AdapterUiContext = Pick<ExtensionCommandContext, "ui">;

  /** Notifications from a disposed generation would paint over the replacement
   *  runtime's screen, so they are dropped rather than queued. */
  function notify(
    ctx: AdapterUiContext,
    message: string,
    type?: "info" | "warning" | "error",
  ): void {
    if (disposed) return;
    ctx.ui.notify(message, type);
  }

  async function askInput(
    ctx: AdapterUiContext,
    title: string,
    placeholder?: string,
  ): Promise<string | undefined> {
    assertLive();
    return withLifecycle(ctx.ui.input(title, placeholder, { signal: dialogAbort.signal }));
  }

  async function askConfirm(
    ctx: AdapterUiContext,
    title: string,
    message: string,
  ): Promise<boolean> {
    assertLive();
    return withLifecycle(ctx.ui.confirm(title, message, { signal: dialogAbort.signal }));
  }

  async function askEditor(
    ctx: AdapterUiContext,
    title: string,
    prefill?: string,
  ): Promise<string | undefined> {
    assertLive();
    // `ui.editor` accepts no abort signal in the pinned API; the lifecycle race
    // is what guarantees the caller unwinds even if its component was detached.
    return withLifecycle(ctx.ui.editor(title, prefill));
  }

  /** The only custom-screen entry: tracked so disposal can settle it. */
  async function screen(
    ctx: ExtensionCommandContext,
    options: ScreenOptions,
  ): Promise<string | undefined> {
    assertLive();
    const choice = await withLifecycle(
      openScreen(ctx, options, {
        track(cancel: () => void): () => void {
          pendingScreens.add(cancel);
          return () => pendingScreens.delete(cancel);
        },
      }),
    );
    assertLive();
    return choice;
  }

  // --- lifecycle -------------------------------------------------------------

  pi.on("session_start", (_event, ctx) => {
    if (ctx.mode !== "tui") return; // non-TUI: fail safe; no forms at all
    const tools = pi.getActiveTools();
    if (tools.length !== 1 || tools[0] !== "timesheet_prepare") {
      notify(
        ctx,
        `Unexpected effective tool exposure (${tools.join(", ") || "none"}); the timesheet profile requires exactly timesheet_prepare. Failing closed.`,
        "error",
      );
      ctx.shutdown();
      return;
    }
    const transport = ensureTransport();
    transport
      .ensureReady()
      .then(() => {
        if (disposed) return;
        state.childPid = transport.pid();
      })
      .catch((e: unknown) => {
        if (disposed) return;
        state.lastError = errText(e);
        notify(
          ctx,
          "Timesheet child failed to start (" +
            state.lastError +
            "); use Reopen child in /timesheet.",
          "error",
        );
      });
    return;
  });

  pi.on("session_shutdown", () => {
    // Idempotent disposal on quit/reload/new/resume/fork. Pi AWAITS this
    // handler before it invalidates the runtime and starts the replacement.
    // Returning the transport shutdown is the ownership boundary: the old
    // child can hold the durable store's exclusive lock while a slow operation
    // (for example a collect waiting on its provider) is in flight, so the
    // replacement's session_start must not race it. Nothing is replayed.
    if (disposed) return;
    disposed = true;
    signalClosed();
    dialogAbort.abort();
    for (const cancel of [...pendingScreens]) {
      try {
        cancel();
      } catch {
        // Settling the caller is what matters; a disposed component may throw.
      }
    }
    pendingScreens.clear();
    const transport = state.transport;
    if (!transport) return;
    return transport.shutdown().catch(() => undefined);
  });

  // Defense in depth: even if CLI isolation drifted, refuse every model tool
  // call except the one declared preparation tool.
  pi.on("tool_call", (event) => {
    const name = (event as { toolName?: string }).toolName ?? "";
    if (name !== "timesheet_prepare") {
      return {
        block: true,
        reason: `tool ${name || "(unknown)"} is not part of the timesheet adapter`,
        terminate: true,
      };
    }
    return undefined;
  });

  // Refuse user `!`/`!!` shell dispatch in this dedicated profile — always.
  pi.on("user_bash", () => ({
    result: {
      output:
        "Shell dispatch is disabled in the timesheet profile. " +
        "No pasted, model or evidence text is executed as a local command.",
      exitCode: 126,
      cancelled: false,
      truncated: false,
    },
  }));

  // Honest incremental counters from event results (never re-reading the
  // growing transcript). before_provider_request fires once per actual model
  // request; turn_end carries that assistant message's own usage object.
  pi.on("before_provider_request", () => {
    state.modelRequests++;
    return undefined;
  });
  pi.on("turn_end", (event) => {
    const message = event.message as { usage?: Record<string, unknown> } | undefined;
    const usage = message?.usage;
    if (!usage) return undefined;
    const row: UsageRow = { observedAt: new Date().toISOString() };
    for (const key of [
      "input",
      "output",
      "cacheRead",
      "cacheWrite",
      "cacheWrite1h",
      "reasoning",
      "totalTokens",
    ] as const) {
      const value = usage[key];
      if (typeof value === "number" && Number.isFinite(value)) row[key] = value;
    }
    state.usageRows.push(row);
    if (state.usageRows.length > 100) state.usageRows = state.usageRows.slice(-100);
    return undefined;
  });

  // --- the one model-facing preparation tool ---------------------------------

  const DayArgs = Type.Object(
    {
      day: Type.String({
        description:
          "The civil date to prepare, in ISO YYYY-MM-DD form. The local day, nothing else.",
        pattern: "^\\d{4}-\\d{2}-\\d{2}$",
        minLength: 10,
        maxLength: 10,
      }),
    },
    // Explicitly bounded: no op/path/executable/env/URL/approval/ack fields.
    { additionalProperties: false },
  );

  pi.registerTool({
    name: "timesheet_prepare",
    label: "Prepare timesheet review",
    description:
      "Prepare the local timesheet review for exactly one civil date. Opens the day and collects configured evidence into the LOCAL review UI. " +
      "It never confirms work, acknowledges sources, decides meeting attendance, selects classifications, previews/approves/submits plans or resolves writes. " +
      "After it returns, tell the person to open the local /timesheet UI to review and act. The result is a bounded status, never the review data.",
    parameters: DayArgs,
    executionMode: "sequential",
    async execute(_toolCallId, params: Static<typeof DayArgs>): Promise<AgentToolResult<PrepareDetails>> {
      const day = params.day;
      if (validateCivilDate(day) !== null) {
        return modelResult(day, `status=failed code=invalid_date day=${day} next=provide_one_valid_civil_date`, null, true);
      }
      // A generation that is already shutting down must not mutate its own
      // state or the replacement's: report unavailability and stop.
      if (disposed) {
        return modelResult(
          day,
          `status=transport_unavailable day=${day} next=let_the_person_reopen_with_slash_timesheet`,
          null,
          true,
        );
      }
      // Ownership guard: an in-flight model preparation must never replace or
      // mutate an active human form/day/collection. Pi's scheduler normally
      // serializes these, but the review state is shared, so the boundary is
      // explicit and fail-closed.
      if (state.humanFlowActive) {
        return modelResult(
          day,
          `status=busy code=human_review_active day=${day} next=finish_or_close_the_open_local_review`,
          null,
          true,
        );
      }
      state.toolBusy = true;
      try {
        return await prepareForModel(day);
      } finally {
        state.toolBusy = false;
      }
    },
  });

  /** Bounded model-visible result: ≤512 bytes, fixed next action, opaque ids. */
  function modelResult(
    day: string,
    text: string,
    collectionId: string | null,
    isError = false,
  ): AgentToolResult<PrepareDetails> {
    const bounded = text.length > 512 ? text.slice(0, 508) + "..." : text;
    return {
      content: [{ type: "text", text: bounded }],
      details: { day, collection_id: collectionId ?? undefined },
      ...(isError ? { isError: true } : {}),
      terminate: true,
    };
  }

  async function prepareForModel(day: string): Promise<AgentToolResult<PrepareDetails>> {
    const transport = state.transport;
    if (!transport || transport.getState() === "failed" || transport.getState() === "closed") {
      return modelResult(
        day,
        `status=transport_unavailable day=${day} next=let_the_person_reopen_with_slash_timesheet`,
        null,
        true,
      );
    }
    let text: string;
    let collectionId: string | null = null;
    try {
      await transport.ensureReady();
      if (disposed) {
        return modelResult(
          day,
          `status=transport_unavailable day=${day} next=let_the_person_reopen_with_slash_timesheet`,
          null,
          true,
        );
      }
      // Re-check ownership after startup: a human form may have opened while
      // the child negotiated. No day/collection mutation happens before this.
      if (state.humanFlowActive) {
        return modelResult(
          day,
          `status=busy code=human_review_active day=${day} next=finish_or_close_the_open_local_review`,
          null,
          true,
        );
      }
      await op("open_day", { day });
      state.day = day;
      const collected = await op<CollectShape>("collect", { day });
      collectionId = collected.collection_id ?? null;
      state.lastCollectionId = collectionId;
      state.review = await op<ReviewShape>("review", { day });
      const sources = (collected.sources ?? []).map((s) => `${s.source}=${s.status}`).join(",");
      text = `status=ok day=${day} sources=${sources || "none"} next=open_the_local_timesheet_ui_to_review`;
      if (collected.no_sources) text += " note=no_configured_sources";
    } catch (e: unknown) {
      const err = e as Partial<ProtocolErrorShape>;
      const code = typeof err?.code === "string" ? sanitizeText(err.code) : "child_error";
      text = `status=failed code=${code} day=${day} next=let_the_person_open_the_local_timesheet_ui`;
    }
    return modelResult(day, text, collectionId);
  }

  // --- operator command ------------------------------------------------------

  pi.registerCommand("timesheet", {
    description: "Open the local timesheet review for a day (no model calls)",
    async handler(args: string, ctx) {
      if (disposed) return; // a command already queued when the runtime ended
      if (ctx.mode !== "tui") return; // fail safe: no auto-answered forms
      if (state.humanFlowActive) {
        notify(ctx, "A timesheet review is already open; finish or close it first.", "warning");
        return;
      }
      if (state.toolBusy) {
        notify(ctx, "A preparation tool call is finishing; open /timesheet again after it returns.", "warning");
        return;
      }
      state.humanFlowActive = true;
      try {
        await runHumanCommand(args, ctx);
      } catch (e: unknown) {
        // Disposal owns the command's outcome: no dialog, no error, no write.
        if (!(e instanceof DisposedError)) throw e;
      } finally {
        state.humanFlowActive = false;
      }
    },
  });

  /** The human-owned local flow: the review state belongs to it until it ends. */
  async function runHumanCommand(args: string, ctx: ExtensionCommandContext): Promise<void> {
    let day = args.trim();
    if (day === "migrate") {
      // Migration activation requires NO open day, so it has its own entry
      // that never opens one. The child still enforces that precondition.
      const transport = ensureTransport();
      try {
        await transport.ensureReady();
        assertLive();
        state.childPid = transport.pid();
      } catch (e: unknown) {
        if (disposed || e instanceof DisposedError) return;
        state.lastError = errText(e);
        if (!(await offerChildReopen(ctx))) return;
        assertLive();
      }
      state.day = null;
      await migrationFlow(ctx);
      return;
    }
    if (!day) {
      const asked = await askInput(ctx, "Timesheet date", "YYYY-MM-DD (the day to review)");
      day = (asked ?? "").trim();
    }
    if (!day) return;
    if (validateCivilDate(day) !== null) {
      notify(ctx, "Timesheet date must be a civil date in YYYY-MM-DD form.", "error");
      return;
    }
    const transport = ensureTransport();
    try {
      await transport.ensureReady();
      assertLive();
      state.childPid = transport.pid();
    } catch (e: unknown) {
      if (disposed || e instanceof DisposedError) return;
      state.lastError = errText(e);
      if (!(await offerChildReopen(ctx))) return;
      assertLive();
    }
    state.childInvocations++;
    if (!(await openDayAndCollect(ctx, day))) return;
    assertLive();
    await reviewLoop(ctx);
  }

  async function openDayAndCollect(ctx: ExtensionCommandContext, day: string): Promise<boolean> {
    try {
      await op("open_day", { day });
      state.day = day;
      const collected = await op<CollectShape>("collect", { day });
      state.lastCollectionId = collected.collection_id ?? null;
      state.review = await op<ReviewShape>("review", { day });
      await refreshLooseState().catch(() => undefined);
      return true;
    } catch (e: unknown) {
      if (disposed || e instanceof DisposedError) return false;
      state.lastError = errText(e);
      notify(ctx, "Timesheet child failed: " + state.lastError, "error");
      return offerChildReopen(ctx);
    }
  }

  async function offerChildReopen(ctx: ExtensionCommandContext): Promise<boolean> {
    // A disposed generation must never open a recovery modal over the
    // replacement runtime (and must never consume that runtime's input).
    if (disposed) return false;
    const failure = state.transport?.failure();
    const choice = await screen(ctx, {
      title: "Timesheet child is not available",
      header: [
        "reason: " +
          (failure
            ? sanitizeText(failure.code + ": " + failure.message)
            : sanitizeText(state.lastError ?? "unknown")),
        "",
        "Reopening starts a fresh child. Nothing is replayed automatically;",
        "unresolved writes stay unresolved until you explicitly observe and resolve them.",
      ],
      actions: [
        { value: "reopen", label: "Reopen child (explicit)" },
        { value: "cancel", label: "Cancel" },
      ],
    });
    if (choice !== "reopen") return false;
    return (await reopenChild(ctx)) !== null;
  }

  async function refreshLooseState(): Promise<void> {
    state.status = await op<StatusShape>("status");
    state.envCheck = await op<EnvCheckShape>("check_environment");
  }

  async function reopenChild(ctx: ExtensionCommandContext): Promise<StatusShape | null> {
    const transport = state.transport;
    if (!transport || disposed) return null;
    try {
      const status = await transport.reopen();
      assertLive();
      state.childPid = transport.pid();
      notify(ctx, "Child reopened; nothing was replayed.", "info");
      return status;
    } catch (e: unknown) {
      if (disposed || e instanceof DisposedError) return null;
      state.lastError = errText(e);
      notify(ctx, "reopen failed: " + state.lastError, "error");
      return null;
    }
  }

  // --- local review loop (all operator authority lives here) -----------------

  async function reviewLoop(ctx: ExtensionCommandContext): Promise<void> {
    for (;;) {
      const review = state.review;
      if (!review) return;
      const transport = state.transport;
      const failure = transport?.failure();
      const choice = await screen(ctx, {
        title: `Timesheet ${review.date} — ${review.timezone} · child pid ${transport?.pid() ?? "?"} · adapter v${APP_VERSION}`,
        header: [
          `revision ${review.revision} · saved ${review.saved_revision} · ` + reviewStateLabel(review),
          `confirmed total ${minutesLabel(review.confirmed.total_minutes)} · staged total ${minutesLabel(review.staged.total_minutes)}`,
          evidenceSummary(review),
          failure ? "CHILD FAILED: " + sanitizeText(failure.code) + " — reopen before writing" : "",
          state.lastError ? "last error: " + sanitizeText(state.lastError) : "",
        ].filter(Boolean),
        detail: reviewDetailLines(review),
        actions: failure
          ? [
              { value: "reopen_child", label: "Reopen child (explicit, no replay)" },
              { value: "child_info", label: "Child / session evidence" },
            ]
          : reviewActions(review),
        footer: ["Full rows are locally inspectable above; nothing is truncated into an approval."],
      });
      if (choice === undefined) return;
      switch (choice) {
        case "reopen_child":
          await reopenAndRefresh(ctx);
          break;
        case "child_info":
          await showChildInfo(ctx);
          break;
        case "evidence_details":
          await showEvidenceDetails(ctx);
          break;
        case "refresh":
          await refreshReview(ctx);
          break;
        case "add_interval":
          await addInterval(ctx);
          break;
        case "edit_interval":
          await editIntervalPick(ctx);
          break;
        case "remove_interval":
          await removeIntervalPick(ctx);
          break;
        case "cancel_staged":
          await cancelStaged(ctx);
          break;
        case "confirm":
          await confirmWork(ctx);
          break;
        case "save_local":
          await saveLocal(ctx);
          break;
        case "attendance":
          await attendanceFlow(ctx);
          break;
        case "acknowledge":
          await acknowledgeFlow(ctx);
          break;
        case "classifications":
          await classificationFlow(ctx);
          break;
        case "reconcile":
          await reconcileFlow(ctx);
          break;
        case "plan":
          await planFlow(ctx);
          break;
        case "submission_status":
          await submissionStatusFlow(ctx);
          break;
        case "migration":
          await migrationFlow(ctx);
          break;
        case "misc":
          await miscFlow(ctx);
          break;
        default:
          return;
      }
    }
  }

  /** Staged dirtiness and an unsaved confirmed revision are different facts. */
  function reviewStateLabel(review: ReviewShape): string {
    const unsavedConfirmed = review.revision !== review.saved_revision;
    if (review.dirty && unsavedConfirmed) {
      return `unsaved staged changes · confirmed revision ${review.revision} is NOT saved (durable ${review.saved_revision})`;
    }
    if (review.dirty) return "unsaved staged changes";
    if (unsavedConfirmed) {
      return `no staged changes · confirmed revision ${review.revision} is NOT saved yet (durable ${review.saved_revision})`;
    }
    return "no unsaved changes";
  }

  function evidenceSummary(review: ReviewShape): string {
    const ev = review.evidence;
    if (!ev) return "no evidence collected on this day yet";
    const sources = ev.sources ?? [];
    const src =
      sources
        .map(
          (s) =>
            `${s.source}${s.enabled ? "" : "(disabled)"}=${s.status}${s.acknowledged ? "·ack" : ""}`,
        )
        .join(" ") || "none";
    return `sources: ${src} · topics: ${(ev.topics ?? []).length} · meetings: ${(ev.meetings ?? []).length}`;
  }

  function reviewDetailLines(review: ReviewShape): string[] {
    const lines: string[] = [
      "CONFIRMED:",
      ...renderView(review.confirmed),
      "",
      "STAGED DRAFT:",
      ...renderView(review.staged),
    ];
    if (review.cross_midnight?.length) {
      lines.push("", "CROSS-MIDNIGHT ALLOCATIONS:");
      for (const cm of review.cross_midnight) {
        lines.push(
          `  ${cm.interval_id}: ${cm.start} → ${cm.end} · total ${cm.total_minutes}m`,
          ...cm.allocations.map((a) => `    ${a.date} ${a.start}-${a.end} → ${a.minutes}m`),
        );
      }
      lines.push("Locked spill segments are edited on their origin day.");
    }
    const topics = review.evidence?.topics ?? [];
    if (topics.length) {
      lines.push("", "EVIDENCE TOPICS (verify-only; never minutes):");
      for (const topic of topics) {
        const items = topic.items ?? [];
        lines.push(
          `  ${topic.ref} [${topic.provider}] ${sanitizeText(topic.title)}` +
            ` items=${items.length}` +
            (topic.truncated ? ` truncated(dropped=${topic.dropped_items ?? "?"})` : "") +
            (topic.first_at ? ` first=${topic.first_at}` : "") +
            (topic.last_at ? ` last=${topic.last_at}` : ""),
        );
        for (const item of items.slice(0, 5)) {
          lines.push(
            `    - ${item.kind ?? "?"} ${sanitizeText(item.title ?? item.ref ?? "")}` +
              (item.time ? " @" + item.time : ""),
          );
        }
        if (items.length > 5) lines.push(`    … ${items.length - 5} more items`);
      }
    }
    const meetings = review.evidence?.meetings ?? [];
    if (meetings.length) {
      lines.push("", "CALENDAR PROPOSALS (proposals only):");
      for (const m of meetings) {
        lines.push(
          `  ${m.ref} ${sanitizeText(m.uid ?? "")} @ ${m.start}` +
            (m.end ? "→" + m.end : "") +
            ` status=${m.status ?? "?"} attendance=${m.attendance ?? "?"} decision=${m.decision ?? "pending"}`,
        );
      }
    }
    if (review.notes?.length) {
      lines.push("", "NOTES:");
      for (const note of review.notes) lines.push("  - " + sanitizeText(note));
    }
    return lines;
  }

  /** Availability follows lifecycle/revisions, never nonempty segment lists:
   *  removing the final interval still needs Cancel/Confirm, and confirming an
   *  empty draft still needs the durable Save. */
  function reviewActions(review: ReviewShape): ListChoice[] {
    const stagedCount = (review.staged.segments ?? []).length;
    const actions: ListChoice[] = [
      { value: "refresh", label: "Refresh review" },
      { value: "add_interval", label: "Add interval" },
    ];
    // A segment can only be edited/removed/classified when the draft HAS one;
    // that is content, not the transition availability F5 is about.
    if (stagedCount) {
      actions.push(
        { value: "edit_interval", label: "Edit interval (staged)" },
        { value: "remove_interval", label: "Remove interval (staged)" },
        { value: "classifications", label: "Classification (search/select)" },
      );
    }
    if (review.dirty) {
      actions.push({ value: "cancel_staged", label: "Cancel staged changes" });
      actions.push({ value: "confirm", label: "Confirm staged work (local)" });
    }
    if (review.revision !== review.saved_revision) {
      actions.push({ value: "save_local", label: "Save local (durable)" });
    }
    actions.push(
      { value: "attendance", label: "Meeting attendance decision" },
      { value: "acknowledge", label: "Acknowledge incomplete source" },
      { value: "reconcile", label: "Remote reconciliation (read-only)" },
      { value: "plan", label: "Exact plan preview / approval / submit" },
      { value: "submission_status", label: "Submission status / recovery" },
      { value: "migration", label: "Legacy migration preview/apply" },
      { value: "evidence_details", label: "Evidence details (topics/meetings, all rows)" },
      { value: "misc", label: "Environment / child reopen" },
      { value: "child_info", label: "Child / session evidence" },
    );
    return actions;
  }

  async function refreshReview(ctx: ExtensionCommandContext): Promise<void> {
    const day = state.day;
    if (!day) return;
    try {
      state.review = await op<ReviewShape>("review", { day });
      state.status = await op<StatusShape>("status");
    } catch (e: unknown) {
      state.lastError = errText(e);
      notify(ctx, "review refresh failed: " + state.lastError, "error");
    }
  }

  async function reopenAndRefresh(ctx: ExtensionCommandContext): Promise<void> {
    const day = state.day;
    state.lastError = null;
    if (!(await reopenChild(ctx))) return;
    if (!day) return;
    try {
      // After an interruption the child session is fresh: reopen the day and
      // review WITHOUT replaying any earlier draft; unresolved writes stay
      // unresolved and are visible through submission_status.
      await op("open_day", { day });
      state.review = await op<ReviewShape>("review", { day });
      notify(ctx,
        "Reopened. Unsaved confirmations were session state and did not survive; " +
          "any unresolved write still needs explicit re-observation.",
        "warning",
      );
    } catch (e: unknown) {
      state.lastError = errText(e);
      notify(ctx, "reopen refresh failed: " + state.lastError, "error");
    }
  }

  // --- typed interval flows --------------------------------------------------

  async function addInterval(ctx: ExtensionCommandContext): Promise<void> {
    const review = state.review;
    const day = state.day;
    if (!review || !day) return;
    const project = (review.confirmed.segments ?? [])[0]?.project ?? "PROJECT";
    const text = await askEditor(ctx,
      "Add interval — DATE HH:MM-HH:MM PROJECT[/CLASSIFICATION] DESCRIPTION",
      `${day} 09:00-10:00 ${project} `,
    );
    if (text === undefined || text === null) return;
    await stageChange(
      ctx,
      () => op("add_interval", { day, revision: review.revision, text }),
      String(text),
    );
  }

  async function stageChange(
    ctx: ExtensionCommandContext,
    run: () => Promise<unknown>,
    submitted: string,
  ): Promise<void> {
    try {
      await run();
      state.lastError = null;
      await refreshReview(ctx);
    } catch (e: unknown) {
      state.lastError = errText(e);
      await refreshReview(ctx);
      await screen(ctx, {
        title: "The child rejected the change",
        header: [
          "submitted: " + sanitizeText(String(submitted ?? "").trim().slice(0, 80)),
          "",
          sanitizeText(state.lastError),
          "",
          "The authoritative state is unchanged and shown in the review screen.",
        ],
        actions: [{ value: "ok", label: "Back to review" }],
      });
    }
  }

  /** Canonical interval text from authoritative review data. An allocation's
   *  display endpoints (23:00-24:00) are NOT the interval grammar text: a
   *  cross-midnight interval is 23:00-01:00, and a midnight-ending interval is
   *  23:00-00:00. Go's grammar stays authoritative; only its display label is
   *  avoided. */
  function canonicalIntervalText(review: ReviewShape, seg: SegmentShape): string {
    let start = seg.start;
    let end = seg.end;
    const cross = (review.cross_midnight ?? []).find((c) => c.interval_id === seg.interval_id);
    if (cross) {
      // CrossMidnight.start is the origin date plus the wall clock
      // ("2026-10-22 23:00"); only its clock belongs in the grammar text.
      start = clockOf(cross.start);
      end = clockOf(cross.end);
    } else if (end === "24:00") {
      end = "00:00";
    }
    const project = seg.project + (seg.classification ? "/" + seg.classification : "");
    return `${seg.date} ${start}-${end} ${project} ${seg.description}`;
  }

  /** Last whitespace-separated token: a wall clock, with or without a date. */
  function clockOf(value: string): string {
    const parts = String(value ?? "").trim().split(/\s+/);
    return parts[parts.length - 1] || String(value ?? "");
  }

  function canonicalSpan(review: ReviewShape, seg: SegmentShape): string {
    const text = canonicalIntervalText(review, seg);
    const rest = text.slice(text.indexOf(" ") + 1);
    return rest.slice(0, rest.indexOf(" "));
  }

  /** Resolve the interval on the day that actually owns it. After navigation
   *  from a locked spill, the origin day, interval ID and CURRENT origin
   *  revision travel together — never the spill date with an origin revision. */
  async function resolveStagedTarget(
    ctx: ExtensionCommandContext,
    pick: string,
    chosen: SegmentShape,
  ): Promise<{ seg: SegmentShape; review: ReviewShape } | null> {
    if (chosen.locked && chosen.origin_date !== state.day) {
      if (!(await switchToOrigin(ctx, chosen.origin_date))) return null;
    }
    const review = state.review;
    const seg = (review?.staged.segments ?? []).find((s) => s.interval_id === pick);
    if (!review || !seg) {
      notify(ctx, "Interval is not staged on the selected day; review again.", "error");
      return null;
    }
    return { seg, review };
  }

  async function editIntervalPick(ctx: ExtensionCommandContext): Promise<void> {
    const review = state.review;
    if (!review) return;
    const staged = review.staged.segments ?? [];
    if (!staged.length) return;
    const pick = await screen(ctx, {
      title: "Edit staged interval",
      detail: staged.map(renderSegment),
      actions: staged.map((seg) => ({
        value: seg.interval_id,
        label: `${seg.interval_id} ${seg.start}-${seg.end} ${seg.project}`,
        description: seg.locked
          ? `locked spill — edit on origin day ${seg.origin_date}`
          : sanitizeText(seg.description).slice(0, 80),
      })),
    });
    if (!pick) return;
    const chosen = staged.find((s) => s.interval_id === pick);
    if (!chosen) return;
    const target = await resolveStagedTarget(ctx, pick, chosen);
    if (!target) return;
    const text = await askEditor(ctx,
      "Edit interval " + pick,
      canonicalIntervalText(target.review, target.seg),
    );
    if (text === undefined || text === null) return;
    await stageChange(
      ctx,
      () =>
        op("edit_interval", {
          day: target.review.date,
          revision: target.review.revision,
          interval_id: pick,
          text,
        }),
      String(text),
    );
  }

  async function switchToOrigin(ctx: ExtensionCommandContext, origin: string): Promise<boolean> {
    try {
      await op("open_day", { day: origin });
      state.day = origin;
      state.review = await op<ReviewShape>("review", { day: origin });
      notify(ctx, "Moved to origin day " + origin + " for the locked segment.", "info");
      return true;
    } catch (e: unknown) {
      state.lastError = errText(e);
      notify(ctx, "could not switch to origin day " + origin + ": " + state.lastError, "error");
      return false;
    }
  }

  async function removeIntervalPick(ctx: ExtensionCommandContext): Promise<void> {
    const review = state.review;
    if (!review) return;
    const staged = review.staged.segments ?? [];
    if (!staged.length) return;
    const pick = await screen(ctx, {
      title: "Remove staged interval",
      detail: staged.map(renderSegment),
      actions: staged.map((seg) => ({
        value: seg.interval_id,
        label: `${seg.interval_id} ${seg.start}-${seg.end} ${seg.project}`,
        description: seg.locked
          ? `locked spill — edit on origin day ${seg.origin_date}`
          : sanitizeText(seg.description).slice(0, 80),
      })),
    });
    if (!pick) return;
    const chosen = staged.find((s) => s.interval_id === pick);
    if (!chosen) return;
    const target = await resolveStagedTarget(ctx, pick, chosen);
    if (!target) return;
    const ok = await askConfirm(ctx,
      "Remove interval",
      `Remove ${pick} (${canonicalSpan(target.review, target.seg)} ${target.seg.project}) from the staged draft?` +
        (target.seg.crosses_midnight ? " Its later-day spill contribution is removed too." : ""),
    );
    if (!ok) return;
    await stageChange(
      ctx,
      () =>
        op("remove_interval", {
          day: target.review.date,
          revision: target.review.revision,
          interval_id: pick,
        }),
      pick,
    );
  }

  // --- attendance & acknowledgment -------------------------------------------

  async function attendanceFlow(ctx: ExtensionCommandContext): Promise<void> {
    const review = state.review;
    if (!review) return;
    if (!(review.evidence?.meetings ?? []).length) {
      await screen(ctx, {
        title: "Meeting attendance",
        detail: ["No proposed meetings were collected for this day."],
        actions: [{ value: "ok", label: "Back" }],
      });
      return;
    }
    for (;;) {
      const meetings = state.review?.evidence?.meetings ?? [];
      const calendarId =
        (state.review?.evidence?.sources ?? []).find((s) => s.source === "calendar")?.collection_id ?? "";
      const detail = meetings.map(
        (m) =>
          `[${m.ref}] ${m.uid ?? ""} @ ${m.start}${m.end ? "→" + m.end : ""}` +
          (m.title ? " — " + sanitizeText(m.title) : "") +
          ` · status=${m.status ?? "?"} attendance=${m.attendance ?? "?"} decision=${m.decision ?? "pending"}` +
          (m.recurrence ? ` (${m.recurrence})` : ""),
      );
      const pick = await screen(ctx, {
        title: "Meeting attendance (explicit local decision; never minutes)",
        header: [
          "Decision identity is the reviewed calendar attempt: " + (calendarId || "(none)"),
          "A recollected/replaced instance requires a fresh collect and a NEW decision.",
        ],
        detail,
        actions: [
          ...meetings.map((m) => ({
            value: m.ref,
            label: `${m.ref} @ ${m.start}`,
            description: `${sanitizeText(m.title ?? "").slice(0, 50)} · ${m.decision ?? "pending"}`,
          })),
          { value: "back", label: "Back" },
        ],
      });
      if (!pick || pick === "back") return;
      const meeting = meetings.find((m) => m.ref === pick);
      if (!meeting) continue;
      if (!calendarId) {
        notify(ctx, "No calendar collection identity on this day; collect first.", "error");
        continue;
      }
      const decision = await screen(ctx, {
        title: `Decision for ${meeting.ref} (${meeting.uid ?? "?"} @ ${meeting.start})`,
        detail: [
          "attended / skipped are recorded review decisions, separate from confirmation.",
          "pending clears the decision. None of them create or alter working minutes.",
        ],
        actions: [
          { value: "attended", label: "Attended" },
          { value: "skipped", label: "Skipped" },
          { value: "pending", label: "Clear to pending" },
          { value: "back", label: "Back without deciding" },
        ],
      });
      if (!decision || decision === "back") continue;
      try {
        const receipt = await op<DecisionShape>("decide_meeting", {
          day: review.date,
          meeting_id: meeting.ref,
          decision,
          collection_id: calendarId,
        });
        await refreshReview(ctx);
        notify(ctx, `Attendance recorded: ${receipt.meeting_id} → ${receipt.decision}.`, "info");
      } catch (e: unknown) {
        state.lastError = errText(e);
        await refreshReview(ctx);
        notify(ctx, "decision rejected: " + state.lastError, "error");
      }
    }
  }

  async function acknowledgeFlow(ctx: ExtensionCommandContext): Promise<void> {
    const review = state.review;
    if (!review) return;
    const sources = review.evidence?.sources ?? [];
    const ackable = sources.filter((s) => s.enabled && s.collection_id);
    if (!ackable.length) {
      await screen(ctx, {
        title: "Source acknowledgment",
        detail: ["No collection attempts to review; run collect (refresh) first."],
        actions: [{ value: "ok", label: "Back" }],
      });
      return;
    }
    for (;;) {
      const pick = await screen(ctx, {
        title: "Acknowledge an incomplete source attempt (explicit, attempt-bound)",
        detail: sources.map(
          (s) =>
            `- ${s.source} status=${s.status}${s.acknowledged ? " (acknowledged)" : ""}` +
            ` collection=${s.collection_id ?? "-"} at=${s.attempted_at ?? "-"}` +
            (s.detail ? " detail=" + sanitizeText(s.detail) : ""),
        ),
        actions: [
          ...ackable.map((s) => ({
            value: s.source,
            label: `Acknowledge ${s.source}`,
            description: `collection ${s.collection_id}`,
          })),
          { value: "back", label: "Back" },
        ],
      });
      if (!pick || pick === "back") return;
      const source = sources.find((s) => s.source === pick)!;
      const ok = await askConfirm(ctx,
        "Acknowledge " + source.source,
        `You reviewed ${source.source} attempt ${source.collection_id} (status ${source.status}).` +
          (source.detail ? ` Coverage: ${sanitizeText(source.detail)}` : "") +
          " Acknowledge it? A later collect requires a fresh acknowledgment.",
      );
      if (!ok) continue;
      try {
        await op<AckShape>("acknowledge_source", {
          day: review.date,
          source: source.source,
          collection_id: source.collection_id,
        });
        await refreshReview(ctx);
        notify(ctx, `${source.source} acknowledged (attempt-bound).`, "info");
        return;
      } catch (e: unknown) {
        state.lastError = errText(e);
        await refreshReview(ctx);
        notify(ctx, "acknowledgment rejected: " + state.lastError, "error");
      }
    }
  }

  // --- classification --------------------------------------------------------

  async function classificationFlow(ctx: ExtensionCommandContext): Promise<void> {
    const review = state.review;
    if (!review) return;
    const staged = review.staged.segments ?? [];
    if (!staged.length) return;
    const intervalPick = await screen(ctx, {
      title: "Explicit remote classification selection",
      header: [
        "A bounded authoritative catalog is read; the chosen option is recorded on the",
        "staged interval with its catalog identity and becomes durable only via confirm+save.",
        "Project defaults and migrated hints are suggestions only.",
      ],
      detail: staged.map(renderSegment),
      actions: staged.map((seg) => ({
        value: seg.interval_id,
        label: `${seg.interval_id} (${seg.project}${seg.classification ? "/" + seg.classification : ""})`,
        description: sanitizeText(seg.description).slice(0, 70),
      })),
    });
    if (!intervalPick) return;
    const query = await askInput(ctx, "Classification search", "free text (empty = whole catalog)");
    let catalog: ClassificationShape;
    try {
      catalog = await op<ClassificationShape>("classifications", {
        query: query && query.trim() ? query.trim() : undefined,
      });
    } catch (e: unknown) {
      state.lastError = errText(e);
      notify(ctx, "catalog read failed: " + state.lastError, "error");
      return;
    }
    state.catalog = catalog;
    const options = catalog.options ?? [];
    if (!options.length) {
      await screen(ctx, {
        title: "Catalog returned no options",
        detail: [
          `catalog ${catalog.catalog_id} complete=${catalog.complete}`,
          catalog.note ? sanitizeText(catalog.note) : "",
        ].filter(Boolean),
        actions: [{ value: "ok", label: "Back" }],
      });
      return;
    }
    const selection = await screen(ctx, {
      title: `Catalog ${catalog.catalog_id} · complete=${catalog.complete}`,
      header: catalog.complete
        ? []
        : ["⚠ Bounded/truncated catalog: it cannot prove an option absent."],
      detail: options.map(
        (o) =>
          `- ${o.id} name=${o.name ?? ""} code=${o.code ?? ""} activity_type=${o.activity_type ?? ""}`,
      ),
      actions: [
        ...options.map((o) => ({
          value: o.id,
          label: o.id,
          description: o.name ? sanitizeText(o.name) : undefined,
        })),
        { value: "back", label: "Back without selecting" },
      ],
    });
    if (!selection || selection === "back") return;
    try {
      await op("select_classification", {
        day: review.date,
        revision: review.revision,
        interval_id: intervalPick,
        catalog_id: catalog.catalog_id,
        classification_id: selection,
      });
      await refreshReview(ctx);
      notify(ctx, `Classification ${selection} selected for ${intervalPick} (staged).`, "info");
    } catch (e: unknown) {
      state.lastError = errText(e);
      await refreshReview(ctx);
      notify(ctx, "selection rejected: " + state.lastError, "error");
    }
  }

  // --- confirm / save / cancel ------------------------------------------------

  async function cancelStaged(ctx: ExtensionCommandContext): Promise<void> {
    const review = state.review;
    if (!review) return;
    const ok = await askConfirm(ctx,
      "Cancel staged changes",
      "Discard the staged draft and keep the confirmed revision? Durable saved work is untouched.",
    );
    if (!ok) return;
    try {
      await op<ReceiptShape>("cancel", { day: review.date, revision: review.revision });
      await refreshReview(ctx);
      notify(ctx, "Staged changes discarded.", "info");
    } catch (e: unknown) {
      state.lastError = errText(e);
      notify(ctx, "cancel failed: " + state.lastError, "error");
    }
  }

  async function confirmWork(ctx: ExtensionCommandContext): Promise<void> {
    const review = state.review;
    if (!review) return;
    try {
      const receipt = await op<ReceiptShape>("confirm", {
        day: review.date,
        revision: review.revision,
      });
      await refreshReview(ctx);
      await screen(ctx, {
        title: "Confirm receipt (session state, durable=false)",
        detail: [
          `date ${receipt.date} · revision ${receipt.revision} · durable ${receipt.durable === true}`,
          `intervals ${receipt.intervals ?? 0} · total ${minutesLabel(receipt.total_minutes ?? 0)}`,
          "",
          "Not durable yet: run Save local to persist. No remote effect exists in confirm.",
        ],
        actions: [{ value: "ok", label: "Back" }],
      });
    } catch (e: unknown) {
      state.lastError = errText(e);
      await refreshReview(ctx);
      await screen(ctx, {
        title: "Confirmation refused",
        detail: [sanitizeText(state.lastError)],
        actions: [{ value: "ok", label: "Back" }],
      });
    }
  }

  async function saveLocal(ctx: ExtensionCommandContext): Promise<void> {
    const review = state.review;
    if (!review) return;
    try {
      const receipt = await op<ReceiptShape>("save", {
        day: review.date,
        revision: review.revision,
      });
      await refreshReview(ctx);
      await screen(ctx, {
        title: "Save receipt (durable, local only)",
        detail: [
          `date ${receipt.date} · revision ${receipt.revision} · saved ${receipt.saved_revision}`,
          `intervals ${receipt.intervals ?? 0} · total ${minutesLabel(receipt.total_minutes ?? 0)}`,
          `saved at ${receipt.saved_at ?? "?"}`,
          "",
          "This is exactly what a restart will load for this day. No remote effect.",
        ],
        actions: [{ value: "ok", label: "Back" }],
      });
    } catch (e: unknown) {
      state.lastError = errText(e);
      await refreshReview(ctx);
      await screen(ctx, {
        title: "Save refused or failed",
        detail: [
          sanitizeText(state.lastError),
          "",
          "A pre-activation failure leaves the previous durable file unchanged;",
          "write_uncertain requires a restart before saving again.",
        ],
        actions: [{ value: "ok", label: "Back" }],
      });
    }
  }

  // --- reconciliation & explicit identity decisions ---------------------------

  async function reconcileFlow(ctx: ExtensionCommandContext): Promise<void> {
    const day = state.day;
    if (!day) return;
    try {
      state.observation = await op<ObservationShape>("reconcile_remote", { day });
    } catch (e: unknown) {
      state.lastError = errText(e);
      notify(ctx, "reconciliation failed: " + state.lastError, "error");
      return;
    }
    for (;;) {
      const obs = state.observation!;
      const choice = await screen(ctx, {
        title: `Reconciliation ${obs.date} · observation ${obs.observation_id} · complete=${obs.complete} ready=${obs.ready}`,
        header: [
          "A read never binds an identity and never mutates anything; explicit decisions only.",
          "Stale observations and identity conflicts are rejected by the child.",
        ],
        detail: observationDetailLines(obs),
        actions: [
          { value: "resolve", label: "Record explicit identity decision" },
          { value: "refresh", label: "Fresh observation" },
          { value: "back", label: "Back" },
        ],
      });
      if (choice === "refresh") {
        await reconcileFlow(ctx);
        return;
      }
      if (choice !== "resolve") return;
      await resolveRemoteFlow(ctx);
    }
  }

  function observationDetailLines(obs: ObservationShape): string[] {
    const lines: string[] = ["MAPPED:"];
    const mapped = obs.mapped ?? [];
    const changed = obs.changed ?? [];
    const missing = obs.missing ?? [];
    const unbound = obs.unbound ?? [];
    const candidates = obs.candidates ?? [];
    const orphaned = obs.orphaned ?? [];
    if (!mapped.length) lines.push("  (none)");
    for (const m of mapped) {
      lines.push(
        `  ${m.local_id} → ${m.remote_id ?? "-"} ${m.date ?? ""} ${m.start_time ?? ""}-${m.end_time ?? ""}` +
          (m.changed ? "  ⚠ changed (accept_observed needed)" : m.accepted ? "  (accepted)" : ""),
      );
    }
    lines.push("CHANGED: " + (changed.length ? "" : "(none)"));
    for (const m of changed) lines.push(`  ${m.local_id} → ${m.remote_id ?? "-"}`);
    lines.push("MISSING: " + (missing.length ? "" : "(none)"));
    for (const m of missing) {
      lines.push(`  ${m.local_id} (bound remote ${m.remote_id ?? "?"} no longer present)`);
    }
    lines.push("UNBOUND REMOTE ENTRIES: " + (unbound.length ? "" : "(none)"));
    for (const u of unbound) {
      lines.push(
        `  ${u.remote_id} ${u.date} ${u.start_time}-${u.end_time} (${u.classification_id ?? "?"}) ` +
          sanitizeText(u.description ?? "") +
          (u.task ? " task=" + sanitizeText(u.task) : ""),
      );
    }
    lines.push(
      "CANDIDATES (overlap/equal-text hints; never bindings): " +
        (candidates.length ? "" : "(none)"),
    );
    for (const c of candidates) {
      lines.push(`  ${c.local_id} ⇄ ${c.remote_id} (${sanitizeText(c.reason)})`);
    }
    lines.push("ORPHANED: " + (orphaned.length ? "" : "(none)"));
    for (const o of orphaned) {
      lines.push(`  ${o.local_id} → ${o.remote_id ?? "-"}${o.date ? " @" + o.date : ""}`);
    }
    if (obs.unresolved_outcomes?.length) {
      lines.push("UNRESOLVED WRITE HISTORY:");
      for (const b of obs.unresolved_outcomes) lines.push("  " + renderBlocker(b));
    }
    if (obs.blockers?.length) {
      lines.push("BLOCKERS:");
      for (const b of obs.blockers) lines.push("  " + renderBlocker(b));
    }
    if (obs.detail) lines.push(sanitizeText(obs.detail));
    return lines;
  }

  async function resolveRemoteFlow(ctx: ExtensionCommandContext): Promise<void> {
    const obs = state.observation;
    if (!obs) return;
    const decision = await screen(ctx, {
      title: "Explicit decision against observation " + obs.observation_id,
      header: [
        "adopt: bind an observed entry to an unmapped local allocation (its own date).",
        "accept_observed: accept observed content for the SAME mapped remote identity.",
        "foreign: acknowledge a remote entry with no local counterpart.",
      ],
      detail: observationDetailLines(obs),
      actions: [
        { value: "adopt", label: "Adopt unbound entry → local allocation" },
        { value: "accept_observed", label: "Accept observed content (mapped entry)" },
        { value: "foreign", label: "Mark remote entry foreign (no local counterpart)" },
        { value: "back", label: "Back" },
      ],
    });
    if (!decision || decision === "back") return;
    if (decision === "adopt") {
      const remote = await pickUnboundRemote(ctx, obs);
      if (!remote) return;
      const local = await pickLocalAllocation(ctx, remote.date);
      if (!local) return;
      await submitResolution(ctx, {
        decision: "adopt",
        remote_id: remote.remoteId,
        local_id: local,
      });
      return;
    }
    if (decision === "accept_observed") {
      // The child reports a changed mapped entry in "changed", not "mapped":
      // both lists address the same existing binding and must be selectable.
      const targets: Array<{
        localId: string;
        remoteId: string;
        date?: string;
        start?: string;
        end?: string;
        changed: boolean;
      }> = [];
      const seen = new Set<string>();
      for (const m of [...(obs.changed ?? []), ...(obs.mapped ?? [])]) {
        if (!m.local_id || !m.remote_id) continue;
        const key = m.local_id + "|" + m.remote_id;
        if (seen.has(key)) continue;
        seen.add(key);
        targets.push({
          localId: m.local_id,
          remoteId: m.remote_id,
          date: m.date,
          start: m.start_time,
          end: m.end_time,
          changed: m.changed === true,
        });
      }
      if (!targets.length) {
        await screen(ctx, {
          title: "No mapped or changed identity to accept",
          detail: [
            "accept_observed applies to an already-mapped remote identity on this allocation date.",
            "Confirm and save the local work, then reconcile again; a fresh read never binds.",
          ],
          actions: [{ value: "ok", label: "Back" }],
        });
        return;
      }
      const pick = await screen(ctx, {
        title: "Which mapped identity? (changed entries included)",
        header: [
          "accept_observed accepts observed content for the SAME mapped remote identity.",
          "The exact remote_id, local_id, observation and current revision travel together.",
        ],
        detail: targets.map(
          (t) =>
            `- ${t.localId} → ${t.remoteId} ${t.date ?? ""} ${t.start ?? ""}-${t.end ?? ""}` +
            (t.changed ? "  ⚠ changed" : ""),
        ),
        actions: [
          ...targets.map((t, idx) => ({
            value: String(idx),
            label: `${t.localId} → ${t.remoteId}`,
            description: `remote_id=${t.remoteId}${t.changed ? " · changed" : ""}`,
          })),
          { value: "back", label: "Back" },
        ],
      });
      if (!pick || pick === "back") return;
      const target = targets[Number(pick)];
      if (!target) return;
      await submitResolution(ctx, {
        decision: "accept_observed",
        local_id: target.localId,
        remote_id: target.remoteId,
      });
      return;
    }
    const remote = await pickUnboundRemote(ctx, obs);
    if (!remote) return;
    await submitResolution(ctx, { decision: "foreign", remote_id: remote.remoteId });
  }

  async function pickUnboundRemote(
    ctx: ExtensionCommandContext,
    obs: ObservationShape,
  ): Promise<{ remoteId: string; date: string } | null> {
    const unbound = obs.unbound ?? [];
    if (!unbound.length) {
      notify(ctx, "No unbound remote entries in this observation.", "info");
      return null;
    }
    const pick = await screen(ctx, {
      title: "Which observed remote entry?",
      detail: unbound.map(
        (u) =>
          `- ${u.remote_id} ${u.date} ${u.start_time}-${u.end_time} description=${sanitizeText(u.description ?? "")}`,
      ),
      actions: [
        ...unbound.map((u) => ({
          value: u.remote_id,
          label: u.remote_id,
          description: `${u.date} ${u.start_time}-${u.end_time}`,
        })),
        { value: "back", label: "Back" },
      ],
    });
    if (!pick || pick === "back") return null;
    const target = unbound.find((u) => u.remote_id === pick);
    return target ? { remoteId: target.remote_id, date: target.date } : null;
  }

  /** Address-bound adopt for an unresolved write: EVERY currently observed
   *  entry is selectable (changed and already-bound included). A lost
   *  acknowledgment usually leaves the intended effect visible as a bound
   *  changed entry; restricting this picker to "unbound" made that recovery
   *  unreachable. Go still enforces content, date and one-to-one checks. */
  async function pickObservedRemote(
    ctx: ExtensionCommandContext,
    obs: ObservationShape,
  ): Promise<string | null> {
    const rows: Array<{ remoteId: string; bucket: string; text: string }> = [];
    const seen = new Set<string>();
    const add = (remoteId: string | undefined, bucket: string, text: string) => {
      if (!remoteId || seen.has(remoteId)) return;
      seen.add(remoteId);
      rows.push({ remoteId, bucket, text });
    };
    for (const m of obs.changed ?? []) {
      add(m.remote_id, "changed", `${m.local_id} → ${m.remote_id} ${m.date ?? ""} ${m.start_time ?? ""}-${m.end_time ?? ""}`);
    }
    for (const m of obs.mapped ?? []) {
      add(m.remote_id, "mapped", `${m.local_id} → ${m.remote_id} ${m.date ?? ""} ${m.start_time ?? ""}-${m.end_time ?? ""}`);
    }
    for (const u of obs.unbound ?? []) {
      add(u.remote_id, "unbound", `${u.remote_id} ${u.date} ${u.start_time}-${u.end_time} ${sanitizeText(u.description ?? "")}`);
    }
    if (!rows.length) {
      await screen(ctx, {
        title: "No observed remote entry to select",
        detail: [
          "Resolution binds one entry visible in the CURRENT complete observation.",
          "Reconcile again after the provider becomes readable; never guess an identity.",
        ],
        actions: [{ value: "ok", label: "Back" }],
      });
      return null;
    }
    const pick = await screen(ctx, {
      title: "Which observed remote entry is this write's effect?",
      header: [
        "Mapped and changed entries are listed too: an edit whose acknowledgment was",
        "lost is usually already bound, and the recovery must name that same remote id.",
        "This selects an identity; the child still checks content and one-to-one binding.",
      ],
      detail: rows.map((r) => `- [${r.bucket}] ${r.text}`),
      actions: [
        ...rows.map((r, idx) => ({
          value: String(idx),
          label: `${r.remoteId} (${r.bucket})`,
          description: r.text,
        })),
        { value: "back", label: "Back" },
      ],
    });
    if (!pick || pick === "back") return null;
    return rows[Number(pick)]?.remoteId ?? null;
  }

  async function pickLocalAllocation(
    ctx: ExtensionCommandContext,
    allocateDate: string,
  ): Promise<string | null> {
    const review = state.review;
    if (!review) return null;
    const candidates = (review.confirmed.segments ?? []).filter(
      (s) => s.date === allocateDate || (s.crosses_midnight && s.date <= allocateDate),
    );
    if (!candidates.length) {
      await screen(ctx, {
        title: "No local confirmed allocation renders on " + allocateDate,
        detail: ["Adoption binds the allocation's OWN date; confirm and save the origin work first."],
        actions: [{ value: "ok", label: "Back" }],
      });
      return null;
    }
    const pick = await screen(ctx, {
      title: "Bind to which confirmed local allocation (date " + allocateDate + ")?",
      detail: candidates.map(renderSegment),
      actions: [
        ...candidates.map((s) => ({
          value: s.interval_id,
          label: s.interval_id,
          description: `${s.date} ${s.start}-${s.end} ${s.project}`,
        })),
        { value: "back", label: "Back" },
      ],
    });
    if (!pick || pick === "back") return null;
    return pick;
  }

  async function submitResolution(
    ctx: ExtensionCommandContext,
    fields: { decision: string; remote_id?: string; local_id?: string },
  ): Promise<void> {
    const obs = state.observation;
    const review = state.review;
    if (!obs || !review) return;
    try {
      await op("resolve_remote", {
        day: review.date,
        revision: review.revision,
        observation_id: obs.observation_id,
        decision: fields.decision,
        remote_id: fields.remote_id,
        local_id: fields.local_id,
      });
      notify(ctx,
        `Decision ${fields.decision} recorded against observation ${obs.observation_id}.`,
        "info",
      );
      state.observation = await op<ObservationShape>("reconcile_remote", { day: review.date });
      await refreshReview(ctx);
    } catch (e: unknown) {
      state.lastError = errText(e);
      notify(ctx, "decision rejected: " + state.lastError, "error");
    }
  }

  // --- exact plan preview / approval / submission -----------------------------

  async function planFlow(ctx: ExtensionCommandContext): Promise<void> {
    const day = state.day;
    if (!day) return;
    try {
      state.plan = await op<PlanShape>("preview_plan", { day });
    } catch (e: unknown) {
      state.lastError = errText(e);
      notify(ctx, "plan preview failed: " + state.lastError, "error");
      await refreshReview(ctx);
      return;
    }
    for (;;) {
      const plan = state.plan;
      if (!plan) return;
      const actions: ListChoice[] = [
        { value: "repreview", label: "Fresh preview (new identity if content changed)" },
      ];
      if (plan.approval_available && !plan.approved) {
        actions.push({ value: "approve", label: "Approve THIS exact plan (explicit)" });
      }
      if (plan.approved) {
        actions.push({ value: "submit", label: "Submit the active approval (explicit)" });
      }
      actions.push({ value: "back", label: "Back" });
      const choice = await screen(ctx, {
        title: `Exact plan ${plan.plan_id} · state=${plan.state} ready=${plan.approval_available} approved=${plan.approved}`,
        header: [
          "Approval binds EXACTLY this rendered plan content. Changed preconditions invalidate it;",
          "a blocked or partial view is never turned into an approval UI.",
        ],
        detail: [
          ...planDetailLines(plan),
          ...(state.lastError ? ["", "last error: " + sanitizeText(state.lastError)] : []),
        ],
        actions,
      });
      if (choice === "approve") await approveFlow(ctx);
      else if (choice === "submit") await submitFlow(ctx);
      else if (choice === "repreview") await planFlow(ctx);
      else return;
    }
  }

  function planDetailLines(plan: PlanShape): string[] {
    const operations = plan.operations ?? [];
    const blockers = plan.blockers ?? [];
    const lines: string[] = [`OPERATIONS (${operations.length}):`];
    for (const o of operations) {
      lines.push(
        `  ${o.kind.toUpperCase()} ${o.local_id} ${o.date} ${o.start_time}-${o.end_time} (${o.minutes}m)`,
        `    classification=${o.classification_id}` +
          (o.selected_choice
            ? ` choice=${o.selected_choice.id}/${o.selected_choice.code ?? ""}`
            : " (explicitly unselected)"),
        `    description=${sanitizeText(o.description)}`,
      );
      if (o.task) lines.push(`    task=${sanitizeText(o.task)} (remote-only; preserved)`);
      if (o.reason) lines.push(`    edit reason=${sanitizeText(o.reason)}`);
      if (o.remote_id) {
        lines.push(
          `    remote identity=${o.remote_id}` +
            (o.expected_remote ? " (precondition bound)" : ""),
        );
      }
      if (o.origin_dates?.length) {
        lines.push(
          `    contributing origins=${o.origin_dates.join(",")}` +
            (o.source_origin_revisions
              ? " revisions=" + JSON.stringify(o.source_origin_revisions)
              : ""),
        );
      }
      if (o.crosses_midnight) lines.push("    cross-midnight allocation (per-date operation)");
    }
    lines.push("SOURCE REVISIONS: " + JSON.stringify(plan.source_revisions ?? {}));
    lines.push(`BLOCKERS (${blockers.length}):`);
    if (!blockers.length) lines.push("  (none)");
    for (const b of blockers) lines.push("  " + renderBlocker(b));
    if (plan.unresolved_outcomes?.length) {
      lines.push("UNRESOLVED WRITES (block approval/submission):");
      for (const b of plan.unresolved_outcomes) lines.push("  " + renderBlocker(b));
    }
    if (plan.surfaced_remotes?.length) {
      lines.push("SURFACED REMOTES (never deleted):");
      for (const r of plan.surfaced_remotes) {
        lines.push(
          `  ${r.remote_id} ${r.date}` + (r.description ? " " + sanitizeText(r.description) : ""),
        );
      }
    }
    return lines;
  }

  async function approveFlow(ctx: ExtensionCommandContext): Promise<void> {
    const plan = state.plan;
    if (!plan || plan.date !== state.day) return;
    if (!plan.approval_available || plan.approved) return;
    const confirmed = await screen(ctx, {
      title: "Approve this exact plan? (display-before-ask)",
      detail: [
        ...planDetailLines(plan),
        "",
        `plan_id: ${plan.plan_id}`,
        "Approving creates ONE new approval occurrence bound to this exact content.",
        "It never dispatches. Cancel means no approval and no submission.",
      ],
      actions: [
        { value: "approve", label: "Approve exactly this" },
        { value: "back", label: "Cancel (no approval)" },
      ],
    });
    if (confirmed !== "approve") return;
    try {
      const receipt = await op<{ approval_id: string; plan_id: string; state: string }>(
        "approve_plan",
        { day: plan.date, plan_id: plan.plan_id },
      );
      await refreshReview(ctx);
      state.plan = { ...plan, approved: true };
      notify(ctx,
        `Approval ${receipt.approval_id} recorded for plan ${receipt.plan_id}. Submit explicitly when ready.`,
        "info",
      );
    } catch (e: unknown) {
      state.lastError = errText(e);
      notify(ctx, "approval refused: " + state.lastError, "error");
    }
  }

  async function submitFlow(ctx: ExtensionCommandContext): Promise<void> {
    const day = state.day;
    const plan = state.plan;
    // The submit target is the CURRENT day's exact plan, never a plan left
    // behind by another day's review.
    if (!day || !plan || plan.date !== day) return;
    const approvalId = await currentActiveApprovalId(day, plan.plan_id);
    if (!approvalId) {
      notify(ctx,
        "No active approval binds this exact plan; approve it explicitly first (no automatic reapproval).",
        "error",
      );
      return;
    }
    const confirmed = await screen(ctx, {
      title: `Submit approval ${approvalId}? (one dispatch per operation; never retried)`,
      header: [
        "Every precondition is revalidated fresh by the child before any dispatch.",
        "Any changed plan/remote/choice/content stops before remote mutation.",
      ],
      detail: planDetailLines(plan),
      actions: [
        { value: "submit", label: "Submit exactly this approval" },
        { value: "back", label: "Cancel (no submission)" },
      ],
    });
    if (confirmed !== "submit") return;
    try {
      const receipt = await op<SubmissionShape>("submit_plan", { day, approval_id: approvalId });
      state.lastSubmit = receipt;
      await refreshReview(ctx);
      await showSubmissionReceipt(ctx, receipt);
    } catch (e: unknown) {
      state.lastError = errText(e);
      // Only a real child envelope is a documented revalidation refusal (the
      // submit contract rejects before dispatch). A transport failure or an
      // unexpected local/unknown exception is NOT proof of zero effects.
      const provenRefusal = (e as { from_child?: boolean } | null)?.from_child === true;
      const choice = await screen(ctx, {
        title: provenRefusal
          ? "Submission refused before dispatch"
          : "Submission interrupted — remote outcome UNKNOWN",
        detail: provenRefusal
          ? [
              sanitizeText(state.lastError),
              "",
              "The child rejected this submission while revalidating preconditions; the",
              "contract makes that a pre-dispatch refusal with no remote call and no",
              "consumed approval. No silent retry: re-preview and approve a new exact",
              "plan if content changed.",
            ]
          : [
              sanitizeText(state.lastError),
              "",
              "Zero remote mutations is NOT proven: the request may have been dispatched",
              "before the connection failed, and a dispatched write may have applied.",
              "Do not retry. Read the current status, reconcile the current remote state,",
              "and resolve any unresolved write explicitly against that observation.",
            ],
        actions: provenRefusal
          ? [{ value: "ok", label: "Back to review" }]
          : [
              { value: "status", label: "Open submission status (read-only)" },
              { value: "ok", label: "Back to review" },
            ],
      });
      if (choice === "status") await submissionStatusFlow(ctx);
    }
  }

  /** Select the exact ACTIVE approval for this day + plan from the existing
   *  bounded read. There is no cache: an approval id is never reused across a
   *  day, a changed plan or a consumed/revoked occurrence. The read is
   *  read-only and never creates, replaces or reapproves anything. */
  async function currentActiveApprovalId(day: string, planId: string): Promise<string | null> {
    try {
      const status = await op<SubmissionStatusShape>("submission_status", { day });
      state.submissionStatus = status;
      const approvals = status.approvals ?? [];
      const active = approvals.find(
        (a) => a.plan_id === planId && a.state === "active" && typeof a.approval_id === "string",
      );
      if (active) return active.approval_id;
    } catch {
      // A refused status read leaves submission unavailable; the message above stands.
    }
    return null;
  }

  /** Distinct receipt outcomes, reported from the child's status field. */
  function submissionExplanation(status: string): string[] {
    switch (status) {
      case "all_confirmed":
        return ["Every reference was confirmed by a fresh read-back; nothing is unresolved."];
      case "partial_stopped":
        return [
          "Only a verified prefix is confirmed; the per-reference rows are the honest stop state.",
          "Unresolved references block every new approval/submission until explicitly resolved.",
        ];
      case "blocked_before_dispatch":
        return ["Blocked before dispatch: zero remote calls and no consumed write."];
      case "persistence_failed":
        return [
          "persistence_failed: NOTHING was consumed and no execution is durable; no remote call was made.",
          "The approval stays active; a NEW explicit submit is required after local persistence is available.",
        ];
      case "durability_uncertain":
        return [
          "Local durability of this execution is UNCONFIRMED; nothing is claimed as durable.",
          "Close and restart to reconcile the state file before any further approval or submission.",
        ];
      default:
        return ["The child's receipt status is reported exactly as returned; no outcome is invented."];
    }
  }

  async function showSubmissionReceipt(
    ctx: ExtensionCommandContext,
    receipt: SubmissionShape,
  ): Promise<void> {
    const calls = receipt.calls
      ? `calls: writes=${receipt.calls.write_calls} reads=${receipt.calls.read_calls}`
      : "calls: unavailable";
    const writes = receipt.writes ?? [];
    const choice = await screen(ctx, {
      title: `Submission ${receipt.status} · execution ${receipt.execution_id ?? "-"}`,
      header: [
        `${receipt.approval_id}: ${receipt.confirmed} confirmed · ${receipt.unresolved} unresolved · ${receipt.unattempted} unattempted`,
        `operations ${receipt.operations} · attempts ${receipt.attempts} · ${calls}`,
        "next: " + sanitizeText(receipt.next_action),
      ],
      detail: [
        "PER-REFERENCE ATTEMPTS: " + (writes.length ? "" : "(no write rows returned)"),
        ...writes.map(
          (w) =>
            `- ${w.write_ref} op#${w.op_index ?? "?"} ${w.action ?? "?"} status=${w.status ?? "?"}` +
            (w.date ? " date=" + w.date : "") +
            (w.error_code ? " err=" + sanitizeText(w.error_code) : "") +
            (w.local_id ? " local=" + w.local_id : "") +
            (w.remote_id ? " remote=" + w.remote_id : ""),
        ),
        "",
        ...submissionExplanation(receipt.status),
        ...((receipt.blockers ?? []).length ? ["", ...(receipt.blockers ?? []).map(renderBlocker)] : []),
      ],
      actions:
        receipt.unresolved > 0 || receipt.status === "partial_stopped"
          ? [
              { value: "status", label: "Open submission status (read-only)" },
              { value: "ok", label: "Back to review" },
            ]
          : [{ value: "ok", label: "Back to review" }],
    });
    if (choice === "status") await submissionStatusFlow(ctx);
  }

  // --- submission status & explicit recovery -----------------------------------

  async function submissionStatusFlow(ctx: ExtensionCommandContext): Promise<void> {
    const day = state.day;
    if (!day) return;
    try {
      state.submissionStatus = await op<SubmissionStatusShape>("submission_status", { day });
    } catch (e: unknown) {
      state.lastError = errText(e);
      notify(ctx, "submission status failed: " + state.lastError, "error");
      return;
    }
    for (;;) {
      const s = state.submissionStatus!;
      const approvals = s.approvals ?? [];
      const unresolved = s.unresolved ?? [];
      const detail: string[] = ["APPROVALS:"];
      if (!approvals.length) detail.push("  (none)");
      for (const a of approvals) {
        detail.push(
          `  ${a.approval_id} state=${a.state} plan=${a.plan_id}` +
            (a.approved_at ? " at=" + a.approved_at : ""),
        );
      }
      detail.push("UNRESOLVED WRITES: " + (unresolved.length ? "" : "(none)"));
      for (const u of unresolved) {
        detail.push(
          `  ${u.write_ref} origin=${u.origin} status=${u.status ?? "?"}` +
            (u.error_code ? " err=" + sanitizeText(u.error_code) : "") +
            (u.date ? " write-date=" + u.date : "") +
            (u.local_id ? " local=" + u.local_id : "") +
            (u.remote_id ? " remote-claimed=" + u.remote_id : "") +
            (u.detail ? " detail=" + sanitizeText(u.detail) : ""),
        );
      }
      detail.push("next: " + sanitizeText(s.next_action));
      const choice = await screen(ctx, {
        title: `Submission status ${s.date}`,
        header: [
          "Every unresolved reference blocks new approval/submission on affected dates.",
          "Recovery is one explicit, address-bound decision against the CURRENT observation.",
        ],
        detail,
        actions: [
          { value: "resolve", label: "Resolve one unresolved write explicitly" },
          { value: "refresh", label: "Refresh status" },
          { value: "back", label: "Back" },
        ],
      });
      if (choice === "refresh") {
        state.submissionStatus = await op<SubmissionStatusShape>("submission_status", {
          day,
        }).catch(() => state.submissionStatus);
        continue;
      }
      if (choice !== "resolve") return;
      await resolveWriteFlow(ctx);
      state.submissionStatus = await op<SubmissionStatusShape>("submission_status", {
        day,
      }).catch(() => state.submissionStatus);
    }
  }

  async function resolveWriteFlow(ctx: ExtensionCommandContext): Promise<void> {
    const s = state.submissionStatus;
    const day = state.day;
    if (!s || !day) return;
    if (!(s.unresolved ?? []).length) {
      notify(ctx, "No unresolved writes to resolve.", "info");
      return;
    }
    const pick = await screen(ctx, {
      title: "Which unresolved write (one, address-bound)?",
      detail: (s.unresolved ?? []).map(
        (u) =>
          `- ${u.write_ref} (${u.origin}/${u.status ?? "?"}${u.error_code ? "/" + u.error_code : ""}) ${u.date ?? "?"}` +
          (u.local_id ? " local=" + u.local_id : "") +
          (u.remote_id ? " claimed remote=" + u.remote_id : ""),
      ),
      actions: [
        ...(s.unresolved ?? []).map((u) => ({
          value: u.write_ref,
          label: u.write_ref,
          description: u.date ?? u.origin,
        })),
        { value: "back", label: "Back" },
      ],
    });
    if (!pick || pick === "back") return;
    const row = (s.unresolved ?? []).find((u) => u.write_ref === pick)!;
    const targetDate = row.date && /^\d{4}-\d{2}-\d{2}$/.test(row.date) ? row.date : day;
    // Recovery must bind the CURRENT observation. A cached view can have been
    // superseded by the very submission that created this unresolved write, so
    // always take one fresh read-only observation before offering decisions.
    let obs: ObservationShape;
    try {
      obs = await op<ObservationShape>("reconcile_remote", { day: targetDate });
      state.observation = obs;
    } catch (e: unknown) {
      state.lastError = errText(e);
      notify(ctx, "observation failed: " + state.lastError, "error");
      return;
    }
    const decision = await screen(ctx, {
      title: `Resolve ${row.write_ref} · observation ${obs.observation_id} · complete=${obs.complete}`,
      header: [
        "adopt binds one explicitly selected observed remote id to the intent's local allocation;",
        "an already-bound changed entry is selectable (one-to-one checks still apply).",
        "not_applied records YOUR attestation that the effect is absent; refused when a matching",
        "effect is visible. This never dispatches and never clears anything automatically.",
      ],
      detail: [
        ...observationDetailLines(obs),
        "",
        `This write: ${row.write_ref} (${row.origin}, status ${row.status ?? "?"})`,
      ],
      actions: [
        { value: "adopt", label: "Adopt observed effect (select remote id next)" },
        { value: "not_applied", label: "Attest not_applied (effect absent)" },
        { value: "back", label: "Back without deciding" },
      ],
    });
    if (!decision || decision === "back") return;
    let remoteId: string | undefined;
    if (decision === "adopt") {
      const observed = await pickObservedRemote(ctx, obs);
      if (!observed) return;
      remoteId = observed;
    }
    try {
      await op("resolve_write", {
        write_ref: row.write_ref,
        observation_id: obs.observation_id,
        decision,
        remote_id: remoteId,
      });
      notify(ctx, `${row.write_ref} resolved (${decision}); audit is additive.`, "info");
    } catch (e: unknown) {
      state.lastError = errText(e);
      notify(ctx, "resolution refused: " + state.lastError, "error");
    }
  }

  // --- migration ---------------------------------------------------------------

  async function migrationFlow(ctx: ExtensionCommandContext): Promise<void> {
    const choice = await screen(ctx, {
      title: "Explicit legacy migration (read-only preview first)",
      header: [
        "Dates are selected explicitly. The legacy root stays invisible: the child reads only",
        "its own launcher-provisioned configuration; no path ever reaches the model or you.",
      ],
      detail: [
        "Preview validates the whole candidate without activating anything.",
        "Apply requires explicit confirmation and no open draft day in this session.",
      ],
      actions: [
        { value: "preview", label: "Preview selected dates" },
        { value: "back", label: "Back" },
      ],
    });
    if (choice !== "preview") return;
    const raw = await askInput(ctx, "Legacy dates", "comma-separated YYYY-MM-DD (bounded selection)");
    if (!raw) return;
    const dates = raw
      .split(",")
      .map((d) => d.trim())
      .filter(Boolean);
    if (!dates.length) return;
    let preview: MigrationPreviewShape;
    try {
      preview = await op<MigrationPreviewShape>("migration_preview", { dates });
    } catch (e: unknown) {
      state.lastError = errText(e);
      await screen(ctx, {
        title: "Migration preview rejected",
        detail: [sanitizeText(state.lastError)],
        actions: [{ value: "ok", label: "Back" }],
      });
      return;
    }
    const apply = await screen(ctx, {
      title: `Migration preview ${preview.migration_id} · ready=${preview.ready} no_change=${preview.no_change}`,
      header: [
        "timezone " + preview.timezone,
        "counts " + JSON.stringify(preview.counts),
        "existing receipt: " + (preview.existing_receipt ?? "-"),
      ],
      detail: [
        ...((preview.conflicts ?? []).length
          ? [
              "CONFLICTS:",
              ...(preview.conflicts ?? [])
                .slice(0, 50)
                .map(
                  (c) =>
                    "  " +
                    c.code +
                    (c.date ? " @" + c.date : "") +
                    (c.detail ? ": " + sanitizeText(c.detail) : ""),
                ),
              ...(preview.conflicts_truncated
                ? [`  … ${preview.conflicts_truncated} more`]
                : []),
            ]
          : ["conflicts: (none)"]),
        "",
        preview.no_change
          ? "The exact source bytes were already imported; applying is a valid no-op."
          : preview.ready
            ? "Ready to apply. Source bytes are re-hashed immediately before activation."
            : "Not ready; fix conflicts and preview again. Nothing can be applied from this view.",
      ],
      // A repeated identical source is a valid no-op apply even when the child
      // reports ready=false with no_change=true.
      actions:
        preview.ready || preview.no_change
          ? [
              {
                value: "apply",
                label: preview.no_change ? "Apply (expecting no_change)" : "Apply migration (explicit)",
              },
              { value: "back", label: "Back" },
            ]
          : [{ value: "back", label: "Back (not ready)" }],
    });
    if (apply !== "apply") return;
    if (state.day) {
      notify(ctx,
        "Activation requires no open day: the child keeps the current draft open. " +
          "Finish/cancel the day, then run migration from a fresh /timesheet session.",
        "error",
      );
      return;
    }
    try {
      const receipt = await op<MigrationApplyShape>("migration_apply", {
        migration_id: preview.migration_id,
      });
      await screen(ctx, {
        title: `Migration ${receipt.status}`,
        detail: [
          `receipt ${receipt.receipt_id}`,
          "dates " + (receipt.dates ?? []).join(", "),
          "counts " + JSON.stringify(receipt.counts),
          "unresolved outcomes carried as durable blockers: " +
            (receipt.unresolved_outcomes ?? 0),
        ],
        actions: [{ value: "ok", label: "Back" }],
      });
      await refreshLooseState().catch(() => undefined);
    } catch (e: unknown) {
      state.lastError = errText(e);
      notify(ctx, "activation refused: " + state.lastError, "error");
    }
  }

  // --- environment / child evidence --------------------------------------------

  async function miscFlow(ctx: ExtensionCommandContext): Promise<void> {
    await refreshLooseState().catch(() => undefined);
    const transport = state.transport;
    const env = state.envCheck;
    const choice = await screen(ctx, {
      title: "Environment & child",
      detail: [
        env ? `required env (NAMES only): ${env.required.join(", ") || "(none)"}` : "environment: unavailable",
        env ? `present: [${env.present.join(", ")}]` : "",
        env ? `missing: [${env.missing.join(", ")}]` : "",
        "",
        transport
          ? `child pid ${transport.pid()} · state ${transport.getState()}`
          : "child: not started",
        transport?.failure()
          ? "failure: " + sanitizeText(transport.failure()!.code + ": " + transport.failure()!.message)
          : "",
        "",
        ...(transport?.diagnostics().map((l) => "diag: " + sanitizeText(l)) ?? []),
      ].filter(Boolean),
      actions: [
        { value: "reopen", label: "Reopen child (explicit, no replay)" },
        { value: "back", label: "Back" },
      ],
    });
    if (choice === "reopen") await reopenAndRefresh(ctx);
  }

  async function showEvidenceDetails(ctx: ExtensionCommandContext): Promise<void> {
    const review = state.review;
    if (!review?.evidence) return;
    const lines: string[] = [];
    for (const source of review.evidence.sources ?? []) {
      lines.push(
        `- ${source.source} enabled=${source.enabled} status=${source.status}` +
          `${source.acknowledged ? " acknowledged" : ""} collection=${source.collection_id ?? "-"}` +
          (source.detail ? " detail=" + sanitizeText(source.detail) : ""),
      );
    }
    lines.push("", "TOPICS:");
    for (const topic of review.evidence.topics ?? []) {
      lines.push(`  ${topic.ref} [${topic.provider}] ${sanitizeText(topic.title)} items=${(topic.items ?? []).length}`);
      for (const item of topic.items ?? []) {
        lines.push(`    - ${item.kind ?? "?"} ${sanitizeText(item.title ?? item.ref ?? "")}` + (item.time ? " @" + item.time : ""));
      }
      if (topic.truncated) lines.push(`    (truncated; dropped=${topic.dropped_items ?? "?"})`);
    }
    lines.push("", "MEETINGS:");
    for (const m of review.evidence.meetings ?? []) {
      lines.push(
        `  ${m.ref} ${sanitizeText(m.uid ?? "")} @ ${m.start}${m.end ? "→" + m.end : ""}`,
        `    title=${sanitizeText(m.title ?? "")} status=${m.status ?? "?"} attendance=${m.attendance ?? "?"} decision=${m.decision ?? "pending"}`,
      );
    }
    await screen(ctx, {
      title: "Evidence details (verify-only; full local rows, sanitized)",
      detail: lines,
      actions: [{ value: "ok", label: "Back" }],
    });
  }

  async function showChildInfo(ctx: ExtensionCommandContext): Promise<void> {
    const transport = state.transport;
    const identity = transport?.identity();
    const failure = transport?.failure();
    const usage = state.usageRows.slice(-5);
    const tools = state.api?.getActiveTools() ?? [];
    const commands = state.api?.getCommands() ?? [];
    const unexpectedCommands = commands
      .map((c) => c.name)
      .filter((n) => n !== "timesheet");
    await screen(ctx, {
      title: "Child / session evidence (local only, sanitized)",
      detail: [
        "child pid: " + (transport?.pid() ?? "(none)") + (state.childPid ? " · first seen " + state.childPid : ""),
        "child state: " + (transport?.getState() ?? "n/a") + (failure ? " · " + sanitizeText(failure.code) : ""),
        identity
          ? `identity: app=${identity.app_version} protocol=${identity.protocol_schema} store=${identity.store_schema} durable=${identity.durable_schema} durability_uncertain=${identity.durability_uncertain}`
          : "identity: (none negotiated yet)",
        identity
          ? `ledger counts: unresolved legacy=${identity.unresolved_writes} native=${identity.native_unresolved_writes} plans=${identity.plans} approvals=${identity.approvals}`
          : "",
        "",
        "/timesheet invocations: " + state.childInvocations,
        "child protocol operations: " + state.childOperations,
        "",
        "model requests this session: " + state.modelRequests,
        "usage fields from event results (last rows; absent = unavailable, not zero):",
        ...(usage.length
          ? usage.map((u) => summarizeUsage(u))
          : ["  (no usage row observed)"]),
        "",
        "declared effective tools: [" + tools.join(", ") + "]",
        "commands beyond /timesheet: [" + unexpectedCommands.join(", ") + "]",
        "cwd (isolated workdir): " + sanitizeText(process.cwd()),
        "session storage: launcher-provisioned --no-session (in-memory)",
        "",
        "last error: " + (state.lastError ? sanitizeText(state.lastError) : "(none)"),
        "",
        ...(transport?.diagnostics().slice(-6).map((l) => "diag: " + sanitizeText(l)) ?? []),
      ].filter((l) => l !== ""),
      actions: [{ value: "ok", label: "Back" }],
    });
  }

  function summarizeUsage(u: UsageRow): string {
    const parts: string[] = [];
    for (const [k, v] of Object.entries(u)) {
      if (k === "observedAt") continue;
      parts.push(`${k}=${String(v)}`);
    }
    return "  " + u.observedAt + ": " + (parts.join(", ") || "(no numeric fields reported)");
  }
}

function validateCivilDate(day: string): string | null {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(day)) return "invalid shape";
  const parsed = new Date(day + "T00:00:00Z");
  if (Number.isNaN(parsed.getTime())) return "not a real date";
  if (parsed.toISOString().slice(0, 10) !== day) return "not a calendar date";
  return null;
}
