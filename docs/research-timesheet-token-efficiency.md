---
title: "Pi harness token-efficiency research"
status: research
date: 2026-10-22
scope: "Design research for a conversational/optional-editorial Pi front end over the deterministic timesheet binary. No implementation authorization."
pi_source_commit: "58c0b02ce552adab76014e7ed712ab7981359aa8"
---

# Pi harness token-efficiency research

## Owner constraints

- Full rewrite, not a wrapper-only migration. Output-first delivery.
- Pi provides conversation and optional editorial text; all accounting and workflow decisions run in a compiled binary.
- One binary process stays alive through review, revision, and submission approval within the daily session. Persistence across Pi restarts has not been requested.
- Save locally, revise with structured typed input, and cancel are distinct outcomes.
- Token reduction is best effort, not a hard per-run budget.
- The agent never reads configuration, raw evidence, workflow records, daily notes, or exports. The binary loads the configured sources and returns only bounded status to the model.
- TigerStyle and `docs/engineering/go-rules.md` are not mandatory for the rewrite. [Effective Go](https://go.dev/doc/effective_go) is the minimum Go baseline. It is not a complete guide to modern Go; consult current official documentation when needed.
- Tests are optional, not a delivery gate. Runtime output must still demonstrate the delivered behavior.
- Use a supported chat model through an available authenticated Pi provider. Luna is not required, and no classifier call is added without a concrete need. Subscription compatibility remains to be verified against the selected provider.
- Review, structured corrections, and approval use local Pi UI and commands without model calls. Conversation and editorial assistance remain optional.
- Explicitly migrate confirmed work, remote identities, and submission history. Old approvals must not authorize writes in the rewrite. Reconcile remote entries before creating new entries.
- Target macOS first. Credentials are stored in `.env.local`, loaded only by the compiled binary; inherited environment variables may supply or override values. This supersedes the earlier Keychain-first proposal. Never read or print credentials through agent tools.
- Evidence plugins are GitHub and calendar only. Remove ai-database, journal/vault collection and export, agent-session collection, and local-git collection from the target workflow. Existing owner files remain untouched; historical provenance may be retained during migration.
- Gradion remains the submission destination; it is not an evidence plugin. Local review state and migration history remain necessary even without ai-database.
- GitHub evidence includes all three selected scopes: commits and authored PRs, submitted PR reviews, and issue comments/discussions/other user-attributable interactions in explicitly configured repositories. Preserve event provenance and group related activity; none establishes worked duration.
- GitHub topic groups show observed activity bounds only. The user supplies working intervals through structured typed input; the binary does not infer worked intervals from activity gaps. Calendar events provide proposed meeting intervals requiring user confirmation.
- Failed or incomplete evidence collection permits explicit acknowledgment of the named source failure and manual completion. Never acknowledge automatically or relabel the collection complete. Evidence acknowledgment is neither work confirmation nor submission approval.
- The owner confirmed the product design and requested a SemVer-governed kickoff. The accepted requirements and execution plan are recorded in `docs/timesheet-rewrite-spec.md` and `.omo/plans/timesheet-rewrite.md`; technical alternatives below remain research, not additional requirements.

## Verdict

1. **Pi can run with no built-in shell, read, search, edit, or write tools.** Restrict the active set to one extension tool with `--tools <name>`; use `--no-builtin-tools` or `--no-tools` only as secondary switches. `--tools` is an allowlist of tools declared to the model, not a sandbox. MCP tools survive it unless explicitly excluded, and user `!` shell commands are unaffected.
2. **A Pi slash command and custom TUI can handle typed input, previews, and approvals without invoking the model.** A registered command receives an argument string and an `ExtensionCommandContext`; it can call `ctx.ui.input`, `editor`, `select`, `confirm`, or `custom`. Commands are dispatched locally; only an ordinary user prompt or an explicit `sendUserMessage` / `sendMessage({triggerTurn:true})` starts an agent run.
3. **The model-visible tool result is `content`, not `details`.** `details` is for local rendering/state reconstruction; `structuredContent` is for programmatic callers and is not sent to the model. This is the supported mechanism for a full local preview with a compact model-visible status.
4. **One session-scoped child process is sufficient for the requested daily workflow.** Start it from the command or `session_start`, retain it across review and approval, and close it idempotently on `session_shutdown`. Persist business state in the binary. A daemon is an alternative only if the owner later requires the same process to survive Pi restarts or reloads.
5. **GPT-6 Luna is a first-party Pi model identifier, but not a chat model.** Pi's official model documentation lists `openai` / `gpt-6-luna` only as a **classifier** for OpenAI's Decisions API. It does not chat, does not appear in `/model`, and is reached through `codemode` scripts or `ctx.modelRegistry.classify()`. A design that uses Luna as the conversation model is not supported by first-party Pi.
6. **The dominant reported token volume in the supplied transcript is repeated cached context, not output.** Cache reads are 82.9% of the reported token components and output is 0.19%. Reducing turns, full-document reads, and retained tool results has much more leverage than shortening the model's prose.
7. **Approval cannot be made unforgeable by Pi alone.** Pi has no general per-call approval mode in the documented CLI; `--approve` is project trust, not tool approval. Approval is an extension responsibility. Keep commit/approve/export off the model's tool surface, require a human command, and bind approval server-side to proposal ID, revision, and content hash. This is capability separation, not an OS sandbox.

Evidence labels below: **[verified]** = documented or source-verified in Pi; **[proposed]** = recommended integration, not implemented or tested here; **[inference]** = reasoned from cited evidence.

---

## 1. Verified Pi capabilities

Pi source and docs were read at commit [`58c0b02`](https://github.com/earendil-works/pi/tree/58c0b02ce552adab76014e7ed712ab7981359aa8). The public docs site is <https://pi.dev/docs/latest/>; the repository is <https://github.com/earendil-works/pi>.

### 1.1 Tool lockdown

| Capability | Behavior | Source |
|---|---|---|
| `--tools <list>` | Replaces the active selection with a comma-separated allowlist of built-in, extension, or custom tool names/patterns. Only matching tools are enabled. | [CLI §Tools](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/cli.md#L119-L128) |
| `--exclude-tools <list>` | Denylist applied after selection; also matches MCP tools. | [CLI §Tools](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/cli.md#L121-L122) |
| `--no-builtin-tools` | Disables default built-ins but keeps extension/custom tools active. | [CLI §Tools](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/cli.md#L123-L124) |
| `--no-tools` | Starts with built-in, extension, custom, and MCP tools disabled. | [CLI §Tools](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/cli.md#L125-L126) |
| `defaultTools` setting | `[]` disables built-ins but not extension/SDK tools; `-name`/`+name` entries modify a selection. | [Settings §Tools](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/settings.md#L42-L58) |
| SDK `tools`, `noTools`, `excludeTools`, `customTools` | Per-session control; `noTools: "all"` starts with no tools; `noTools: "builtin"` keeps extension/custom tools. | [SDK options](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/src/core/sdk.ts#L60-L95), [SDK §Configuring](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/sdk.md#L100-L108) |

Important limitations:

- Available built-ins are `read`, `bash`, `powershell`, `edit`, `write`, `grep`, `find`, and `ls`; file search is `grep`/`find` (`tool_search` is tool discovery, not code search). [Settings §Tools](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/settings.md#L46)
- `--tools` does **not** remove MCP tools unless an entry starts with `mcp__`; `--no-mcp` or `--exclude-tools 'mcp__*'` is needed for MCP. [CLI §MCP tools](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/cli.md#L130-L138)
- `--no-tools` is not a security boundary. Pi's own security doc says enabled tools use the Pi process's OS permissions, the working folder does not prevent access to other paths, and project trust does not limit what tool calls can access. [Security](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/security.md#L3-L33)
- User `!` / `!!` shell commands are a separate path. `user_bash` is an extension event; a handler can intercept or replace it, but `--no-tools` does not itself remove the user's shell. [Extensions §Events](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/extensions.md#L240), [interactive-shell example](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/examples/extensions/interactive-shell.ts)
- Tools cannot be unregistered. A tool can be withdrawn by re-registering it with `exposure: "hidden"`, which makes it unreachable. [Extensions §Tool exposure](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/extensions.md#L267-L277)

### 1.2 Custom tools

A custom tool is a `registerTool()` definition with a model-facing `name`, `description`, TypeBox `parameters`, and an async `execute(toolCallId, params, signal, onUpdate, ctx)`. [Extensions §Tools](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/extensions.md#L247-L259), [types.ts](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/src/core/extensions/types.ts#L579-L663)

Result contract:

| Field | Model-visible? | Purpose |
|---|---|---|
| `content` | **Yes** | Text/image result returned to the model. |
| `details` | No | Arbitrary structured data for rendering/logs/state reconstruction. |
| `structuredContent` | No | JSON matching `outputSchema`; for programmatic callers such as codemode. |
| `usage` | No (not main context accounting) | Usage from nested model work. |
| `isError` | Yes, as error status | Reports failure while keeping `details`/`structuredContent`. |
| `terminate` | No, control hint | Skip automatic follow-up when every finalized tool result in the batch agrees. |

Source: [`AgentToolResult`](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/agent/src/types.ts#L424-L446).

`renderResult` receives the full result and draws it locally. It does not change what the model sees. [ToolDefinition](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/src/core/extensions/types.ts#L579-L690)

Operational rules from the docs:

- Truncate large model-facing results and tell the model where to read the full output. [Extensions §Tools](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/extensions.md#L257)
- Use `executionMode: "sequential"` when tools share mutable in-memory state. [Extensions §Tools](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/extensions.md#L255)
- Return `terminate: true` only when the agent should skip its automatic follow-up. [Extensions §Tools](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/extensions.md#L251-L253)
- Tool calls from one assistant message may run in parallel. [Extensions §Events](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/extensions.md#L230-L240)

### 1.3 Extension runtime and persistent subprocesses

- Extensions are TypeScript modules loaded **into the Pi process** with the same OS permissions. [Extensions intro](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/extensions.md#L3-L7)
- The factory must not start processes, sockets, watchers, or timers. Start long-lived resources from `session_start` or from the command/tool that needs them. Close session-scoped resources from an idempotent `session_shutdown` handler. [Extensions §Lifecycle](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/extensions.md#L54-L60)
- `session_shutdown` reasons are `"quit" | "reload" | "new" | "resume" | "fork"`. Reload and session replacement invalidate the old runtime; code after `await ctx.reload()` must not reuse old state. [SessionShutdownEvent](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/src/core/extensions/types.ts#L824-L829), [Extensions §Lifecycle](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/extensions.md#L126-L145)
- Cleanup must be idempotent because cancellation, reload, session replacement, and process exit can converge. [Extensions §Errors](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/extensions.md#L377-L379)
- The extension API is Node/Bun code, so `node:child_process` is available; checked examples use `spawnSync` and custom tool operations. This is a language capability, not a Pi-managed process supervisor. [interactive-shell.ts](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/examples/extensions/interactive-shell.ts)

**[inference]** A module-level `ChildProcess` survives turns but not a runtime reload or session replacement. If the compiled binary must be the same process across review, approval, and Pi restarts, the extension must be a client of a separately owned daemon (Unix socket or similar), or the host must own the child outside the extension runtime. A stdio child spawned in `session_start` is sufficient only for a single uninterrupted Pi session.

### 1.4 Commands, local UI, and approvals without a model turn

- `registerCommand(name, { description, getArgumentCompletions?, handler })`; the handler receives the typed argument string and `ExtensionCommandContext`. [RegisteredCommand](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/src/core/extensions/types.ts#L1569-L1575)
- UI primitives: `ctx.ui.select`, `confirm`, `input`, `editor`, `notify`, `custom`. [ExtensionUIContext](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/src/core/extensions/types.ts#L149-L164)
- `ctx.ui.custom()` gives one component temporary control of the interactive area; pass `overlay: true` for an overlay. [TUI §Custom screens](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/tui.md#L78-L95)
- In RPC mode, `select`/`confirm`/`input`/`editor` become `extension_ui_request` / `extension_ui_response` messages; `custom()` returns `undefined`, so full custom screens are TUI-only. `ctx.hasUI` is true in RPC, `ctx.mode` is `"rpc"`. [RPC Extension UI](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/rpc-extension-ui.md#L1-L26)
- `prompt()` handles extension commands before ordinary user messages enter the agent; an accepted agent run resolves after the run. This is the documented path that distinguishes local command handling from a model turn. [SDK §Prompting](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/sdk.md#L62-L68)
- `sendUserMessage()` **always** triggers a turn. `sendMessage()` has `triggerTurn?: boolean`; only `triggerTurn: true` starts an idle run. `appendEntry()` persists custom data but is not sent to the LLM. [ExtensionAPI](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/src/core/extensions/types.ts#L1727-L1748), [State](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/extensions.md#L337-L346)

Approval facts:

- Pi's security doc: Pi "does not ask for approval before every tool call"; safety comes from limiting what Pi can access. [Security](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/security.md#L3-L7)
- `--approve` / `--no-approve` are **project trust** controls, not per-tool approvals. [CLI](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/cli.md#L237-L240)
- The installed `omp` (oh-my-pi) fork, observed locally, exposes `--approval-mode`; that flag is not present in the first-party Pi docs and must not be assumed available in Pi. This note researches Pi, not the fork's extra runtime.
- A `tool_call` handler can mutate `event.input` in place or return `{ block: true, reason }`; a handler failure blocks the tool as a fail-safe. [ToolCallEventResult](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/src/core/extensions/types.ts#L1454-L1463), [Extensions §Errors](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/extensions.md#L377)
- The checked `permission-gate.ts` blocks dangerous bash calls when `ctx.hasUI` is false, and asks `ctx.ui.select` otherwise. [permission-gate.ts](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/examples/extensions/permission-gate.ts)
- Tool annotations (`readOnlyHint`, `destructiveHint`, `openWorldHint`) are **hints, not verified**. Pi says so directly; MCP says clients must treat annotations as untrusted unless they come from trusted servers. [Extensions §Tool exposure](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/extensions.md#L279), [MCP Tools](https://modelcontextprotocol.io/specification/2025-06-18/server/tools)

### 1.5 Model identity: GPT-6 Luna

First-party Pi documentation names the exact provider/model pair **`openai` / `gpt-6-luna`**, and only in the **classifier** section:

- "Classifier models do not chat."
- Authentication is `OPENAI_API_KEY`; ChatGPT `/login` credentials do not work, and the model is not listed while `openai` is logged in via `/login`.
- Classifier models do not appear in `/model`; they are reached through `codemode` scripts or `ctx.modelRegistry.classify()`. [Models](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/models.md#L184)
- API limits: inputs above 922K tokens rejected; requests above roughly 600K input tokens can hit a ~5s gateway timeout.

Source: [Models §Use classifier models](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/models.md#L144-L159), [codemode Luna example](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/codemode.md#L170-L180).

**Conclusion:** "Pi harness + GPT-6 Luna" is valid only if Luna is used as a classifier/gate. It is **not** a supported first-party chat or coding model. The local `~/.pi/oh-my-pi.jsonc` reference to `openai-codex/gpt-6-luna` is an oh-my-pi fork configuration, not first-party Pi support, and is not evidence of chat capability. No other first-party Pi chat alias for Luna was found in the first-party docs and source inspected.

Options:

1. Use a catalog chat model for conversation/editorial, and optionally Luna for a typed classification (for example, "does this request contain enough information to propose?"). Classifier output is probabilities over typed questions, not prose.
2. Use Luna only as a deterministic gate/ranking component; keep the human as approver.
3. Do not claim Luna conversation support. If a third-party fork exposes it differently, verify that fork separately and label it as non-first-party.

### 1.6 Context and session overhead

- Each request carries the system prompt, the **active branch** of the session, tool definitions, and skill descriptions. Tool results are recorded and become part of the active branch. [How Pi Works §Context](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/how-pi-works.md#L15-L31)
- System prompt sections are built from preamble, tools, rules, Pi docs, optional addendum, project context files, skill descriptions, and cwd. [system-prompt.ts](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/src/core/system-prompt.ts#L150-L186)
- `--system-prompt <text|path>` replaces the **preamble** in the implementation, but the tools/rules/docs sections are still built. The CLI wording says "replaces the default system prompt"; the source is more precise. True whole-prompt replacement is available to an extension via a `before_agent_start` handler returning `systemPrompt`. [agent-session.ts](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/src/core/agent-session.ts#L1730-L1745), [Extensions §before_agent_start](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/extensions.md#L216)
- Automatic compaction triggers when `contextTokens > contextWindow - reserveTokens`; defaults are 16,384 reserve and 20,000 kept recent tokens. Summarization is an extra model call, and summary requests disable cache writes. [Compaction](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/compaction.md#L25-L49), [Settings](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/settings.md#L73-L76)
- Session files are local JSONL and do not by themselves consume model context; the **active branch** does. Compaction replaces older messages in later requests while retaining raw entries. [How Pi Works §Sessions](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/how-pi-works.md#L25-L31)
- Cache warming is a global setting (`off`, `streaming`, `idle`). Refresh usage counts toward session totals and does not enter model context. Idle warming is a poor default for a low-frequency personal tool. [Settings](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/settings.md#L20-L23)
- Changing the prompt or tool set mid-session appends a change to the transcript; providers that cannot represent the transition receive a full checkpoint, which can invalidate the cached prefix. [Extensions §Activate tools dynamically](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/extensions.md#L301)

Prompt-cache economics matter because the transcript is cache-read-heavy:

- Anthropic: cache reads are 0.1x base input (model-specific exceptions), 5-minute default TTL, 1-hour writes at 2x; cache hits still consume billed tokens. [Anthropic prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)
- OpenAI: caching is automatic; GPT-5.6+ supports explicit breakpoints, 30-minute writes at 1.25x and 12-hour writes at 2.5x; reads are 0.1x (0.05x on GPT-6.1 Sol). Changing tools, system content, or compacting invalidates prefix reuse. [OpenAI prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching)

**Design implication:** a stable prefix helps, but it does not make repeated turns free. The main durable savings come from fewer turns, smaller active context, and keeping full documents out of the transcript.

---

## 2. Evidence from the supplied transcript

Source: local [`transcript_full.jsonl`](../transcript_full.jsonl). Counts below are the parent-supplied analysis of that file; step IDs are `step_index` values.

### 2.1 Volume and reported counters

| Measure | Value |
|---|---|
| JSONL records | 78 |
| `PLANNER_RESPONSE` records | 38 |
| Tool calls | 35 total: 18 `run_command`, 15 `view_file`, 2 `manage_task` |
| Reported `input` | 421,795 |
| Reported `cache_read` | 2,064,374 |
| Reported `output` | 4,721 |
| Reported component sum | 2,490,890 |
| `cache_write` / `total` fields | Absent on all 38 responses |

This is a **sum of reported components**, not an authoritative provider total or cost. `cache_write` and `total` are absent. The repository's existing additive convention treats the sum as a synthesized lower bound; provider counter overlap and billing semantics were not independently verified here. Derived shares of that component sum: cache reads 82.9%, input 16.9%, output 0.19%.

### 2.2 Segment breakdown

| Segment | Steps | Model responses | Tool calls | Reported sum |
|---|---|---|---|---|
| Main workflow | 0–63 | 31 | 30 | 1,786,240 (71.7%) |
| Usage question | 64–73 | 5 | 4 | 499,338 (20.0%) |
| Export question | 74–77 | 2 | 1 | 205,312 (8.2%) |

The "usage question" alone consumed about one fifth of the reported total. A conversational harness cannot assume that meta-questions are cheap; each one re-sends the active context.

### 2.3 Context growth and large reads

- Initial model context (`input + cache_read`) at step 1: **10,939** reported tokens.
- Final context at step 77: **102,524** reported tokens.
- Growth: **9.4x** across the session. **[inference]**
- Two instruction-file tool results at steps 2 and 4 total **67,270 characters**. No tokenizer was run on those texts.
- Nine workflow-record view attempts total **86,229 characters** across dates 21, 20, 19 (missing), and 09. The requested day was 21; day 20 was not originally user-mentioned.
- One daily-note tool result at step 30 contains **24,205 characters**.
- Task-log reads at steps 38 and 59 total **14,808 characters**, plus duplicate output in completion notifications.
- Step 72 metrics show two records for the same conversation with different content hashes as the transcript grew; duplication risk is visible but aggregate double-counting is **not confirmed**. The step 73 figure `total=2,083,211` was an earlier snapshot, not the final supplied-file sum.

**Interpretation:** the session repeatedly paid to carry large, mostly static documents. Moving extraction into the Go binary and returning a bounded summary is the highest-leverage reduction. The model does not need the full daily note or log to produce a proposal.

### 2.4 Workflow failures relevant to design

- **Step 33:** `--ack cal-work` was applied automatically before the user authorized it; no such consent appears in the preceding trace. Acknowledgment must be a human-command operation, not a model-callable tool argument.
- **Step 54:** plain mode was launched with stdin redirected from `/dev/null`; step 59 records the abort. A review/approval process must stay interactive; do not use a non-interactive print/plain path for a workflow that needs confirmation.
- **Step 63:** the final daily response only asks the user to review. No completed review, export, or submission is shown. Conversational completion is not workflow completion.

---

## 3. Local-first interaction design

### 3.1 What can stay outside the model

| Operation | Path | Model sees? |
|---|---|---|
| Start/continue conversation | user prompt → model turn | yes |
| Collect evidence | deterministic Go call from adapter/command | only compact result if adapter chooses |
| Build full preview | Go; fetched by command | **no** |
| Render/scroll/expand preview | TUI custom component or editor | **no** |
| Revise draft | command → Go; optional model turn only if user requests editorial copy | only compact status / selected text |
| Approve / cancel / save / export | command → Go, human UI | **no** |
| Proposal handoff from model | one `propose` tool call | compact `content` only |

A slash command handler runs locally. It can use `ctx.ui.editor()` for multiline input, `ctx.ui.select()` for choices, and `ctx.ui.custom()` for a full-screen preview. It does not need `sendUserMessage`; if it wants to record a local note for a later model turn without starting one, `sendMessage(..., { triggerTurn: false })` is the documented option. For non-context state, `appendEntry()`.

In RPC mode the same command can still show a full preview by prefilling `ctx.ui.editor()` and then using `ctx.ui.confirm()`; it loses the custom full-screen component, not the ability to review locally without a model turn. [RPC Extension UI](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/rpc-extension-ui.md#L1-L26)

### 3.2 Proposed flow

1. User types `/ts` (or a plain prompt such as "log yesterday").
2. If a command: handler reads the date/request, calls Go `propose`, shows the full preview locally, and waits for `approve`/`revise`/`cancel`. **No model turn.**
3. If a conversation prompt: model calls exactly one tool, `ts_propose`, with a small structured request. Go does all collection/accounting. The tool returns `content` = one-line status, `details` = proposal ID + revision + hash, and `terminate: true` when the proposal is waiting for review.
4. The model turn ends. User reviews via `/ts review`. Full data is rendered from the persistent compiled process, not from the transcript.
5. A local-review confirmation establishes Confirmed Time Blocks. A separate submission approval authorizes one exact Submission Plan. Saving locally may export without remote submission; revision and cancellation are separate actions.

This uses Pi as a **workflow front end**, not an autonomous agent. That matches the primary-source guidance in §6.

### 3.3 Approval integrity rules

Pi does not authenticate approvals. The extension must enforce these invariants:

1. **No commit/approve/export tool.** Those operations exist only in command handlers. A model cannot invoke a slash command; it can only emit text that looks like one.
2. **Ignore approval-shaped tool arguments.** The `ts_propose` schema must not contain `approved`, `reviewed`, `force`, `submit`, or an arbitrary state transition. Reject unknown fields in Go (`DisallowUnknownFields` or equivalent). A model-supplied `true` is data, never consent.
3. **Bind approval to content.** Go issues a single-use `review_token` at `review_get`, bound to `(proposal_id, revision, content_hash)`, with a short expiry. The token lives in the extension closure, never in model context. `approve` requires the token and `expected_revision`.
4. **Separate confirmation from submission approval.** Confirmed Time Blocks establish recorded work. Only Approval authorizes an exact Submission Plan; do not call local confirmation "approval."
5. **Fail closed.** Require `ctx.mode === "tui"` for the full-screen flow, or a `ctx.ui.confirm` with `ctx.hasUI`. If UI is unavailable, reject; do not auto-approve. The checked permission-gate example uses this pattern. In RPC mode the confirmation is only as trustworthy as the RPC client; a client that answers `confirmed: true` automatically is an auto-approver.
6. **Do not trust annotations.** `readOnlyHint`/`destructiveHint` are unverified hints. Approval must be based on actual operation class and server-side state.
7. **Do not treat model prose as confirmation.** A message such as "approved" in the transcript is not an approval event.

**[limitation]** A same-UID process can still connect to a local socket and call `approve` if it knows the protocol. If the model has a shell or write access, this is not a security boundary. Actual isolation requires removing those tools and/or running Pi/Go in a container, VM, or dedicated user. Pi's security doc is explicit that project trust and transcript review do not create a boundary. [Security](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/security.md#L3-L23)

### 3.4 Model-visible status vs local preview

Recommended tool result:

```ts
return {
  content: [{ type: "text", text: "Proposal p_7f3a rev 1: 5 blocks, 7.5h. Awaiting human review." }],
  details: { proposalId: "p_7f3a", revision: 1, contentHash: "sha256:…", status: "reviewing" },
  terminate: true,
};
```

Why this shape:

- `content` is the only part of the tool **result** sent to the model and re-sent on later turns. Tool call arguments are separately visible because they are part of the assistant message; keep the schema small.
- `details` is local; the command fetches the full preview from Go by ID. Keeping only the ID/hash in `details` also avoids persisting personal content in Pi session JSONL.
- `terminate: true` prevents an automatic follow-up turn after a successful proposal. If validation fails, return `isError: true` and do **not** terminate, so the model can correct.
- Error `content` should be short and actionable; never include stack traces or file contents.

**[inference]** `content` should be capped by the extension, not trusted from Go. A suggested starting cap is 1,000–1,500 characters per result and 300–500 characters for the child's `summary`; measure before fixing numbers. The user asked for best-effort, not hard token budgets, so treat these as configurable soft limits with a visible warning.

---

## 4. Recommended extension-to-binary protocol

This section is **[proposed]**. It is not implemented or benchmarked. It is designed to keep the model's tool surface at one operation, keep approval outside the model, and keep the compiled binary as the single writer.

### 4.1 Architecture

```
Pi (TUI)
  └─ thin TypeScript extension (trusted adapter, no business logic)
       ├─ registers: ts_propose (model tool, sequential)
       ├─ registers: /ts <review|approve|revise|cancel|status|export> (human commands)
       ├─ tool_call guard: allow only ts_propose; block everything else
       └─ JSONL client over child stdin/stdout
            └─ Go process (single writer; durable storage format still undecided)
                 ├─ evidence collection / accounting / validation
                 ├─ proposal revisions + content hashes
                 └─ local save / export / (gated) submission
```

The adapter has no accounting rules, no interpolation of model strings into SQL or shell, and no commit path in tool handlers.

### 4.2 Transport

**Preferred for the requested scope:** stdin/stdout JSONL to one child spawned from the command or `session_start`. It remains alive across the day's review, revisions, and approval. No shell is involved, and neither the model nor the adapter reads configuration. The adapter passes a provisioned profile reference; Go loads configuration internally.

**Alternative, not required:** a separately owned daemon with a private Unix socket if the same process must survive Pi restarts or reloads. This adds lifecycle and access-control work without reducing model tokens relative to the session-scoped child.

Protocol envelope:

```json
{"v":1,"id":"c-01J...","op":"propose","args":{"date":"2026-10-21","request_id":"r-...","request":"..."}}
{"v":1,"id":"c-01J...","ok":true,"data":{"proposal_id":"p_...","revision":1,"status":"reviewing","content_hash":"sha256:...","summary":"5 blocks, 7.5h"}}
{"v":1,"id":"c-01J...","ok":false,"error":{"code":"validation","message":"date is required","retryable":false}}
```

Rules:

- One JSON object per line; UTF-8; no ANSI; stderr is logs only.
- Max line size (for example 1 MiB); reject oversized input.
- Strict schema; unknown fields rejected.
- Every response echoes `id`; the client times out and sends `cancel` with the same `id`.
- `v` mismatch fails the handshake; the adapter reports a compact error and refuses to run.
- The daemon never returns model-ready prose beyond a bounded `summary`; the adapter chooses the final `content`.

### 4.3 Operations

| Operation | Caller | Purpose | Approval? |
|---|---|---|---|
| `hello` | adapter start | version/capability handshake | no |
| `propose` | `ts_propose` tool or `/ts` | create draft proposal; idempotent on `request_id` + content hash | no |
| `status` | both | compact state/next action | no |
| `review_get` | command only | full preview + `review_token` bound to ID/revision/hash | no |
| `revise` | command, or tool only in `draft`/`changes_requested` | create a new revision; requires `expected_revision` | no |
| `confirm` | command only | confirm reviewed Time Blocks; requires reviewed revision | explicit human confirmation |
| `approve` | command only | authorize one exact Submission Plan at the reviewed revision/hash | explicit submission approval |
| `cancel` | command (model may cancel its own draft) | discard draft; no commit | no |
| `export` | command only | write the managed daily-note section as part of save-local or completed submission | no remote approval implied |
| `submit` | command only, behind product gate | external submission; separate preview and token | **yes, separate** |

Keep local review and remote submission as separate states: proposed work becomes confirmed work, then an exact Submission Plan can become approved, submitting, and submitted or unresolved. Export is an output operation, not proof of remote submission. Revisions invalidate affected plans and approvals. The full state model remains a design decision.

### 4.4 Lifecycle and failure behavior

| Event | Adapter behavior |
|---|---|
| Extension load | Do **not** spawn/connect. Register tool/command and handlers only. |
| `session_start` or first command | Spawn the fixed compiled binary once with `shell: false`, private piped stdio, and a minimal environment. Handshake. |
| Tool call | Send one request; handle cancellation; return compact result. |
| `session_shutdown` | Close the child idempotently after it has persisted durable state. |
| Reload / new / resume / fork | Invalidate old handles and review tokens. A later process recovers durable state but never silently resumes submission. |
| Child crash | Report failure. Reconcile any in-flight submission before an explicit recovery action; never retry uncertain writes. |
| Timeout / abort | Cancel collection or draft work where safe. For an in-flight remote write, preserve the uncertain outcome and block blind replay. |
| Protocol error | Fail closed; log to stderr/file; return compact error; do not fall back to shell. |

The child survives ordinary model turns and human review. A reload ends this session-scoped process. Cross-reload process persistence is an optional requirement, not part of the owner's current request.

### 4.5 Security requirements

- Use private child stdio. If a daemon is later selected, use a private socket directory, peer checks, and no public TCP endpoint.
- Binary and data directory not writable by the model's tool surface. Remove `bash`, `write`, `edit`, `read`, `grep`, `find`, `ls`, MCP, and other extensions.
- Sanitize all model-originated strings before terminal rendering: strip/replace C0/C1 control characters and ANSI escapes to prevent terminal escape injection; bound lengths; no format-string interpolation into shell.
- Go validates date ranges, enum values, lengths, and revision/hash. No model-provided file paths. If SQL storage is selected, use parameterized statements.
- Do not pass the model provider's credentials to the child. The binary resolves its own configured source credentials without exposing configuration or secrets to the agent.
- Treat the binary and adapter as trusted local code, not a sandbox. Tool removal prevents model-directed file and shell access; harder isolation requires OS controls. [Containerization](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/containerization.md)

### 4.6 Restrictive launch profile

A defensible interactive profile, subject to verification against the installed Pi version:

```sh
pi \
  -ne -e /absolute/path/to/timesheet-adapter.ts \
  -ns -np -nc --no-mcp --no-themes \
  --tools ts_propose \
  --exclude-tools 'mcp__*' \
  --cwd /path/to/empty/pi-workspace \
  --no-approve \
  --no-session \
  --model <verified-chat-model>
```

Notes:

- `-ne` disables discovered/configured/built-in extensions; explicit `-e` still loads. [CLI §Resources](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/cli.md#L194-L198)
- `-ns`, `-np`, `-nc` remove skills, prompt templates, and `AGENTS.md`/`CLAUDE.md` discovery. [CLI §Resources](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/cli.md#L200-L214)
- `--tools ts_propose` is the actual model-tool allowlist. `--no-tools` alone would also need the extension tool to be activated; an allowlist is clearer.
- `--no-session` is optional. It prevents local Pi session persistence (and export), but it also removes resume/audit; business state belongs in Go either way. If sessions are kept, set a private `--session-dir` and review before `/export` or `/share`.
- `--cwd` to an empty directory reduces resource discovery and session grouping surprises. It is not a filesystem sandbox; Go still uses its own configured data paths.
- `--no-approve` avoids loading trust-gated project resources. Project `sessionDir` is read before trust resolution, so do not start in an untrusted project directory. [Security §Project trust](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/security.md#L27-L33)
- The current `.claude/skills/timesheet/SKILL.md` requires the harness to expose a shell. That requirement becomes obsolete for this design: the adapter, not the model, runs the binary. Do not implement the skill change here; record it as a design consequence.

---

## 5. Bounded-context recommendations

These are **[proposed]** operating rules, not Pi defaults.

1. **One tool call per run.** The model's job is to turn a request into a structured `propose` call. Return `terminate: true` on success. Do not let the model "confirm" or "summarize" in a second turn unless the user asked for editorial text.
2. **Full data stays local.** Tool `content` is a status line. Full previews are fetched by command and rendered in `ctx.ui.custom`/`editor`. `details` holds IDs/hashes, not the full record.
3. **Extract in Go.** Do not read the daily note, workflow record, or logs into the model context. Go extracts candidates and returns a bounded summary. The transcript shows 67,270 characters of instruction reads, a 24,205-character daily note, and 14,808 characters of task logs; those are the kind of inputs to eliminate. **[inference]**
4. **Soft context thresholds.** Use `ctx.getContextUsage()` (tokens/percent) to warn at a configurable level (for example 20k/40k) and recommend `/new`. Do not silently truncate or discard user-visible state. [ContextUsage](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/src/core/extensions/types.ts#L306-L312)
5. **Fresh session per day or per proposal.** Do not auto-`--continue` a long transcript for a new day. Durable state lives in Go; Pi context is disposable. This is the cleanest bound.
6. **Disable idle cache warming** for a low-frequency tool (`cacheWarming: "off"`), or leave it at `streaming` if interactive retries are common. Idle refresh consumes tokens without entering context. [Settings](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/settings.md#L20-L23)
7. **Keep the prefix stable within a session.** Do not add/remove tools or change the prompt mid-run; that can force a checkpoint and lose cache reuse. [Extensions](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/extensions.md#L301)
8. **Compaction is a fallback, not a strategy.** It adds a summarization model call and can lose detail. If the workflow is bounded, disable auto-compaction and start fresh; if a session grows, compact deliberately. [Compaction](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/compaction.md#L25-L49)
9. **Cap tool result size at the adapter.** Reject oversized `content`; return a truncation notice and a local reference. Pi's built-in tools use 2,000 lines / 50 KiB defaults, but a custom tool gets no automatic cap beyond what it implements. [truncate.ts](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/src/core/tools/truncate.ts#L11-L13)
10. **Measure the baseline.** The supplied transcript's initial context was 10,939 reported tokens; compare the minimal adapter against that. Track input, cache read, output, tool calls, and human edits per day. At this baseline, a proportional reduction in active context affects far more reported tokens than the same proportional reduction in output; the cost difference requires verified provider pricing.

---

## 6. Primary-source practices

### 6.1 Deterministic workflows first

- Anthropic distinguishes **workflows** (LLMs and tools orchestrated through predefined code paths) from **agents** (LLMs dynamically direct their own process/tool use). It recommends the simplest solution and notes workflows offer predictability for well-defined tasks. [Building effective agents](https://www.anthropic.com/research/building-effective-agents)
- OpenAI's agent guide says to validate that a use case needs an agent; otherwise "a deterministic solution may suffice," and recommends clear instructions, tools, guardrails, and a maximum number of turns/exit conditions. [A practical guide to building agents](https://cdn.openai.com/business-guides-and-resources/a-practical-guide-to-building-agents.pdf)
- MCP's tool specification states tools are model-controlled and recommends a human in the loop with the ability to deny invocations. [MCP Tools](https://modelcontextprotocol.io/specification/2025-06-18/server/tools)

Applied here: the timesheet state machine, accounting, validation, and commit are deterministic Go. The model is an input/editorial edge. Approval is a human event.

### 6.2 Context is finite and degrades with length

- Anthropic: context must be treated as a finite resource with diminishing marginal returns; every token depletes an attention budget; aim for the smallest set of high-signal tokens. It recommends compaction, structured note-taking, and sub-agents for long-horizon work. [Effective context engineering](https://www.anthropic.com/research/effective-context-engineering-for-ai-agents)
- Chroma's controlled study across 18 models finds performance becomes less reliable as input length grows, including on simple tasks; NIAH-style retrieval overstates long-context reliability. [Context Rot](https://research.trychroma.com/context-rot)
- Anthropic notes tool-result clearing as a safe, light form of compaction: once a tool call is deep in history, the raw result is usually no longer needed. [Effective context engineering](https://www.anthropic.com/research/effective-context-engineering-for-ai-agents)

Applied here: keep full records out of the transcript; return status. Each retained tool result is re-sent on every later turn.

### 6.3 Tool design

- Anthropic recommends a few high-impact tools over thin API wrappers; consolidate multi-step operations; return only high-signal information; optimize token efficiency with pagination/filtering/truncation; write unambiguous descriptions and strict schemas; evaluate tool use. It cites a default 25,000-token cap for Claude Code tool responses. [Writing effective tools for agents](https://www.anthropic.com/research/writing-tools-for-agents)
- MCP defines `content` (unstructured), `structuredContent`/`outputSchema` (structured), `isError`, and annotations; annotations are untrusted unless the server is trusted. [MCP Tools](https://modelcontextprotocol.io/specification/2025-06-18/server/tools)

Applied here: one `ts_propose` tool, not CRUD tools; a compact status; no full preview; strict parameters; `isError` for failures; `terminate` on success.

### 6.4 Prompt caching

- Keep stable instructions and tool definitions at the front; append rather than rewrite history; compaction/truncation can reset cache reuse. [OpenAI prompt caching §Best practices](https://developers.openai.com/api/docs/guides/prompt-caching)
- Cache hits are cheaper, not free: 0.1x input on Anthropic and most OpenAI models (0.05x on GPT-6.1 Sol). Cache writes cost more (1.25x for 30-minute, 2x/2.5x for longer retention). [Anthropic prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching), [OpenAI prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching)

Applied here: within a short review, a stable prefix helps; across a day, a fresh session avoids paying to carry stale context.

---

## 7. Limitations and non-claims

- **No implementation or benchmark was performed.** The protocol, caps, and launch profile are proposals. No claim is made that they achieve a specific token reduction.
- **Luna chat is not supported by first-party Pi.** The only verified identifier is `openai` / `gpt-6-luna` as a classifier. The local oh-my-pi config is a fork and is not evidence of first-party chat support.
- **Pi's approval model is not an OS sandbox.** Removing tools reduces the model's reachable surface; it does not protect against a compromised extension, a malicious local process, or the user's own shell. Use OS isolation for a hard boundary.
- **`--tools` is a declaration allowlist.** It does not secure the process, remove user `!` shell access, or prevent an extension from performing actions internally. MCP tools need separate exclusion.
- **`--system-prompt` is not a full prompt replacement in the implementation.** It replaces the preamble; tools/rules/docs sections are still generated. A true override is an extension `before_agent_start` return, which has its own cache/consistency implications.
- **Pi runtime versions differ.** The cited source is `main` at commit `58c0b02` (2026-10-21). Verify flags and event names against the installed version before implementation. The workstation has `omp` 18.12.6 (oh-my-pi) installed; a first-party `pi` CLI was not installed or run here, so the CLI behavior above is docs/source-verified, not runtime-tested.
- **The transcript counters are incomplete.** `cache_write` and `total` were absent on all 38 responses; 2,490,890 is the reported-component sum under the repository's additive convention, not a verified provider total or bill. Step 72's duplicate records are a visible duplication risk, not confirmed aggregate double-counting.

---

## Sources

### First-party Pi (commit-pinned)

- Docs index: <https://github.com/earendil-works/pi/tree/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs>
- CLI, tools, resources, prompts: [cli.md](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/cli.md)
- Extensions, lifecycle, custom tools, state, errors: [extensions.md](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/extensions.md)
- Extension types: [types.ts](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/src/core/extensions/types.ts)
- Agent tool result: [packages/agent/src/types.ts](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/agent/src/types.ts)
- System prompt construction: [system-prompt.ts](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/src/core/system-prompt.ts), [agent-session.ts](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/src/core/agent-session.ts)
- Security: [security.md](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/security.md)
- Isolation: [containerization.md](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/containerization.md)
- Context assembly and sessions: [how-pi-works.md](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/how-pi-works.md)
- Compaction: [compaction.md](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/compaction.md), [settings.md](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/settings.md)
- TUI: [tui.md](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/tui.md)
- RPC extension UI: [rpc-extension-ui.md](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/rpc-extension-ui.md)
- SDK: [sdk.md](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/sdk.md), [sdk.ts](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/src/core/sdk.ts)
- Models and Luna: [models.md](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/models.md), [codemode.md](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/docs/codemode.md)
- Examples: [permission-gate.ts](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/examples/extensions/permission-gate.ts), [interactive-shell.ts](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/examples/extensions/interactive-shell.ts), [SDK full control](https://github.com/earendil-works/pi/blob/58c0b02ce552adab76014e7ed712ab7981359aa8/packages/coding-agent/examples/sdk/12-full-control.ts)

### General practices

- Anthropic, [Building effective agents](https://www.anthropic.com/research/building-effective-agents) (2024-12-19)
- Anthropic, [Effective context engineering for AI agents](https://www.anthropic.com/research/effective-context-engineering-for-ai-agents) (2025-09-29)
- Anthropic, [Writing effective tools for agents](https://www.anthropic.com/research/writing-tools-for-agents) (2025-09-11)
- OpenAI, [A practical guide to building agents](https://cdn.openai.com/business-guides-and-resources/a-practical-guide-to-building-agents.pdf)
- Model Context Protocol, [Tools specification](https://modelcontextprotocol.io/specification/2025-06-18/server/tools)
- Anthropic, [Prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)
- OpenAI, [Prompt caching](https://developers.openai.com/api/docs/guides/prompt-caching)
- Chroma, [Context Rot](https://research.trychroma.com/context-rot) (2025-07-14)
