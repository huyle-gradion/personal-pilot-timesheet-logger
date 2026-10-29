// Command timesheet runs the local timesheet server.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/huyle-gradion/pilot-timesheet/internal/timesheet"
)

func usage() {
	fmt.Fprint(os.Stderr, `timesheet `+timesheet.AppVersion+`

Usage:
  timesheet version
  timesheet setup [--project <id>=<name>[=<classification>]]... [--seed-config <path>] [--tz <IANA>]
  timesheet serve --config <path> --env-file <path>
  timesheet collect [--config <path>] [--env-file <path>] [--date <YYYY-MM-DD>]
  timesheet draft [--config <path>] [--env-file <path>] [--date <YYYY-MM-DD>]
  timesheet run [--config <path>] [--env-file <path>] [--date <YYYY-MM-DD>]
  timesheet next [--config <path>] [--env-file <path>] [--date <YYYY-MM-DD>] [--project <id>]
  timesheet tui [--config <path>] [--env-file <path>] [--date <YYYY-MM-DD>] [--project <id>]
  timesheet record [--config <path>] [--date <YYYY-MM-DD>] (--draft <draft.json> --descriptions <desc.json> | --file <reviewed.json>) [--replace]
  timesheet status [--config <path>] [--env-file <path>] [--date <YYYY-MM-DD>]
  timesheet stage [--config <path>] [--env-file <path>] --date <YYYY-MM-DD> [--project <id>] [--revise]
  timesheet classifications [--config <path>] [--env-file <path>] --query <text>
  timesheet classify [--config <path>] [--env-file <path>] --date <YYYY-MM-DD> (--file <choices.json> | --configured-default) [--project <id>]
  timesheet acknowledge [--config <path>] [--env-file <path>] --date <YYYY-MM-DD> --source <github|calendar> --collection-id <id>
  timesheet decide [--config <path>] [--env-file <path>] --date <YYYY-MM-DD> --meeting-id <id> --decision <attended|skipped> --collection-id <id>
  timesheet confirm [--config <path>] [--env-file <path>] --date <YYYY-MM-DD> [--project <id>]
  timesheet save [--config <path>] [--env-file <path>] --date <YYYY-MM-DD> [--project <id>]
  timesheet plan [--config <path>] [--env-file <path>] --date <YYYY-MM-DD>
  timesheet reconcile [--config <path>] [--env-file <path>] --date <YYYY-MM-DD>
  timesheet resolve [--config <path>] [--env-file <path>] --date <YYYY-MM-DD> --file <decision.json>
  timesheet approve [--config <path>] [--env-file <path>] --date <YYYY-MM-DD>
  timesheet submit [--config <path>] [--env-file <path>] --date <YYYY-MM-DD> --approval-id <id>
  timesheet submit-status [--config <path>] [--env-file <path>] --date <YYYY-MM-DD>
  timesheet agent-submit [--config <path>] [--env-file <path>] --date <YYYY-MM-DD> [--plan-id <id> --review-id <id> --confirm-chat]
  timesheet resolve-write [--config <path>] [--env-file <path>] --file <decision.json>

Except serve (whose paths the launcher supplies), every command resolves its
configuration in three tiers — an explicit --config, then ./timesheet.json in
the current directory when it exists (project-local), then
$HOME/.timesheet/config.json — and reports the chosen path once on stderr. A
missing configuration is an error naming both candidates and the fixes. No
ancestor directory is ever searched and the tiers are never merged. --env-file
defaults to $HOME/.timesheet/.env.local when that file exists (no file values
otherwise). An explicit flag always wins verbatim; an explicit --env-file that
cannot be read still fails.

The setup subcommand creates $HOME/.timesheet (mode 0700) with config.json and
.env.local (mode 0600) exactly once. It validates the generated configuration
in memory before its first target effect, writes each file through a private
temporary file that is fsynced and hard-linked into place with no replace, and
never deletes, replaces, repairs or chmods an existing file in any state — a
present config that fails to load is reported verbatim and left
byte-identical, and a present env file is never read or parsed. Project ids and
names are never invented: pass repeatable
--project '<id>=<name>[=<classification>]' values or a loadable --seed-config;
without either, a machine with no configuration is told the exact remedy and
nothing is created. The timezone is an explicit --tz or the host's IANA zone,
never a guessed Local or UTC zone. State paths in generated configurations are
absolute; existing configuration files keep today's loader semantics unchanged,
including a relative state_path.

The collect/draft/run/record/status subcommands are the deterministic headless
surface used by the timesheet skill: collect performs one bounded collection
attempt, draft prints the compact one-day draft the skill's single judgment
step reviews, run composes collect and draft in one process and prints ONLY
the draft JSON on stdout (the collect source report is not printed; its
substance is already in the draft's flags and per-block completeness) while
also writing the same draft bytes to the deterministic artifacts directory
$TMPDIR/timesheet/<store-identity>/<date>/draft.json (created as needed; the path is printed
on stderr, so redirecting stdout yields exactly the draft bytes) — collect
and run also print the fresh collection_id as one stderr line (issue #162),
so acknowledge/decide can copy it without reading state.json — record
merges the reviewed result into the per-day entries file
(idempotent per time block) — composing it from --draft plus the
descriptions-only --descriptions file, or taking the full reviewed --file
(recovery path) — or, with --replace, treats the reviewed result as the day's
COMPLETE content: all blocks for the resolved day are replaced with the reviewed blocks
(block ids from the reviewed input; blocks absent from it are removed from
the entries file). Use --replace only after re-collecting a day, when the new
generation supersedes the old; without it the merge behavior is unchanged —
and status reports store state plus the day's recorded entries and per-source
latest collection attempt (status and collection_id) for the
acknowledge/decide steps. All five treat an omitted
--date as today in the configured timezone, resolved by the binary, and an
explicit --date always wins; status opens that day either way. They hold the
same fixed-path, no-shell, credential-safe contract as
serve and perform no approval or submission.

The next subcommand is the skill's read-only step planner (issue #195). It
writes nothing and executes nothing: each invocation re-derives ONE typed JSON
action for the resolved day from artifacts the binary already owns — the
entries ledger and stage-map sidecar, the run artifacts under
$TMPDIR/timesheet/<store-identity>/<date>, and the durable store snapshot — and the caller obeys
that one state before invoking next again. The state is action (the exact argv
in action.cmd, run verbatim and never through a shell), judgment (a model-owned
answer file in judgment.answer_file; next never writes it), stopped (a precise
blocker in reason that names the human remedy; next never acknowledges,
guesses or resolves it), or human_gates (terminal: the read-only submit-status
command plus the exact approve and submit commands for a human, which an agent
never executes). An omitted --date means today in the configured timezone, and
--project names the staged intervals when the configuration defines several.

The stage/acknowledge/decide/confirm/save subcommands finalize one recorded
day (campaign issue #160, slice SUBMIT-1). Natural order: run --date first so
collect has attempted every enabled source (the run stderr and status --date
both surface the fresh collection_id); then stage converts the day's
recorded entries into staged intervals; then acknowledge (and decide) with
the --collection-id copied verbatim from that collect receipt or the status
surface; then confirm;
then save. stage converts entries with both a start and an end into intervals
through the same session ops serve uses (the description text carries any PRs
list verbatim); entries without time anchors are skipped and listed in the
receipt — times are never invented. Overlapping ledger entries (the remote
forbids overlaps) resolve at the bridge: specific shorter blocks claim their
time first and broader spans split around them, so a meeting inside an
evidence span splits that evidence span — the ledger keeps the human's
overlapping truth, no minute is lost or doubled, and an entry fully covered
by more specific entries is skipped with that reason. Entries longer than the
Gradion remote's
four-hour policy (live-verified, issue #167) are staged as contiguous chunk
intervals of at most 240 minutes each — the entries ledger keeps the human's
true block untouched, and the receipt lists every chunk. The entries file stays
the single human
ledger: each command re-derives the staged set from the entries ledger in its
own process, the entries/<date>.stage.json block-to-interval sidecar serves
same-process restage and audit, and a day that already has confirmed
intervals is never restaged — unless stage --revise explicitly replaces the
finalized set with a fresh derivation from the entries ledger and re-finalizes
the day at a new revision (interval ids change; re-run classify afterwards). Interval ids in receipts are valid only against
the entries file as it stood when the receipt printed; editing entries before
confirm renumbers them (later commands fail closed on stale ids). confirm
runs the source gate: every enabled source needs a collection attempt for the
date, and a non-complete attempt needs an explicit acknowledgment — a missing
one fails with the gate error naming what is missing. save commits the
confirmed day durably. Each of these commands is one process (open day, ops,
close) and holds the store's exclusive lock: it cannot run while a serve
process holds the store. Nothing here approves or submits anything.

The classifications and classify subcommands add classification (campaign
issue #160, slice SUBMIT-2). The full flow order is: run → record → stage →
classifications → classify → confirm → save. classifications performs one
bounded authoritative catalog read through the configured Gradion workspace:
it is the one command in this flow that requires live access (network and
credential; it fails closed with the op's own error when the configuration
disables remote access or the credential is missing, and live catalog
behavior is owner-verified). Its receipt prints the catalog's choices exactly
as returned (catalog_id, query, complete, observed_at, and options with id,
name, code, activity_type) and the catalog is persisted in the store's
bounded cache, so classify works offline against the stored catalog. classify
applies choices.json — {"selections": {"<interval_id>": {"catalog_id": "...",
"classification_id": "..."}}} with catalog_id and classification_id copied
verbatim from the classifications receipt — through the select_classification
op, and has two paths. NOT YET FINALIZED (the day has no confirmed
intervals): selections key off the stage receipt ids — classify re-derives
the staged set from the entries ledger exactly as stage does (same
derivation, same interval ids for the same entries state), persists the
selections to entries/<date>.classify.json, each content-bound to the
recorded block it was chosen for (a SHA-256 digest of the block's date,
block_id, start, end and description), and the day stays staged — confirm and
save replay the sidecar's selections against their own re-derived staged set
before confirming, and save commits remote_classification_id durably — but
ONLY when the re-derived day is content-identical to the one classify chose
against. Staged interval ids re-derive sequentially, so any entries change
after classify (insertion, removal, replacement, time or description edit)
would silently re-attach a selection to the wrong interval; the content
binding makes that impossible — the replay fails closed naming the block, and
the fix is to re-run stage + classify. ALREADY FINALIZED (a saved day that
carries durable confirmed intervals, e.g. it was confirmed and saved before
any classification was selected): selections key off the durable interval ids
exactly as 'status --date' prints them, applied directly against the durable
staged set, and classify itself re-finalizes the day at a NEW revision — it
runs confirm and save in the same process (the serve flow's reopen → select →
confirm → save revision step) and its receipt says the day was re-finalized
at the new revision with the selections applied to the confirmed intervals;
no sidecar is written on this path because the selections become durable
immediately. On both paths an empty file ({}) is a valid no-op: on the staged
path it clears any earlier selections; on a finalized day it re-finalizes
nothing and the saved revision is left untouched. On both paths an unknown or
stale interval id fails closed with the op's own error, untouched, and the
saved revision is unchanged. Nothing here approves or submits anything.

The plan, reconcile and resolve subcommands add identity reconciliation and
exact submission previews (campaign issue #160, slice SUBMIT-3). The full flow
order through this slice is: run → record → stage → classifications/classify →
confirm → save → plan → (reconcile and resolve when the plan lists identity
blockers) → plan again for a clean plan_id. plan wraps the preview_plan op
verbatim: one fresh read-only observation of every affected date through the
configured Gradion workspace — live access, like classifications (it fails
closed with the op's own remote_disabled/remote_unavailable error without the
configuration or credential, and live remote behavior is owner-verified) — and
prints the exact PlanPreview as deterministic JSON: plan_id, state
(ready/blocked), the per-allocation create/edit operations, and any blockers
with their codes. The op persists the fresh observations and the immutable
plan record durably in one transaction under the process's exclusive store
lock (plan cannot run while serve holds the store); that durable record is
what a later approve consumes after re-rendering fresh in its own process —
nothing here approves or submits anything. reconcile wraps the reconcile_remote
op: one fresh read-only observation of the date (live access, same fail-closed
behavior) printing mapped/changed/missing/orphaned identities, unbound remotes
and candidates exactly as returned. It is read-only in the identity sense: it
persists the fresh observation record itself, but it never binds an identity,
never mutates a mapping and never clears a blocker. resolve is the human
decision gate that replaces the interactive picker: after reading the
reconcile (or plan) receipt, the human authors one decision JSON file —
{"revision": <n>, "observation_id": "...", "decision":
"adopt|accept_observed|foreign", "remote_id": "...", "local_id": "..."} with
revision, observation_id and remote_id copied verbatim from the receipt, and
local_id naming the confirmed durable interval for adopt/accept_observed but
omitted for foreign — and resolve passes it to the resolve_remote op. Every
refusal is the op's own, verbatim: a wrong or stale observation_id
(stale_observation), a remote_id the observation does not carry
(unknown_remote), adopt without confirmed durable local work (unknown_local),
a decision that would steal or erase an active binding (identity_conflict),
and a stale day revision (stale_revision). resolve performs no remote read.
Blockers are resolved only by these explicit human decisions, never
auto-cleared; because a plan's identity includes its exact blockers, the
re-preview after any resolve yields a NEW plan_id — the stale one is never
reused or re-approved.

The approve, submit, submit-status and resolve-write subcommands add approval
and submission (campaign issue #160, slice SUBMIT-4). The full flow order is:
run → record → stage → classifications/classify → confirm → save → plan →
(reconcile/resolve when the plan lists identity blockers) → plan again for a
clean plan_id → approve → submit; with submit-status and resolve-write for
recovery. THE GATE DISCIPLINE, stated plainly: approve, submit and
resolve-write are interactive-only — each refuses to run unless BOTH stdin and
stdout are terminals and each requires typing the exact plan_id (approve),
approval id (submit) or write reference (resolve-write) at an interactive
prompt, so agent shells physically cannot execute them; this is a
deliberate-act barrier, not proof a human is present: a pty-allocating wrapper
satisfies the terminal check and reaches the prompt, consistent with the
approval ledger's own admission that a scripted request is not proof a human
is physically present; the agent may run
everything else, including submit-status, which is read-only and not gated.
approve renders the CURRENT plan fresh (preview_plan; a stored plan record is
never trusted or shown), displays the plan_id, operations and blockers, and on
confirmation calls the approve_plan op — every refusal (stale_plan,
plan_not_ready, an uncertain store, a changed catalog) passes through
verbatim, and the printed approval_id is the input to submit. submit
dispatches exactly what the approval bound: it re-validates every
precondition, commits a durable pre-write intent per operation before one
never-retried dispatch, and confirms a write only after an identified
acknowledgment AND a matching complete read-back; the SubmissionReceipt is
printed faithfully — every aggregate count and per-reference status — and
blocked_before_dispatch (including submit without a persisted active approval,
or with one already consumed) and durability_uncertain are distinct receipt
states, printed as returned. Writes are never auto-retried; an unknown outcome
stays a durable unresolved write reference — it blocks every new submission,
appears in submit-status with its write_ref and the next_safe_action, and
stays unresolved until the human resolves it. resolve-write records that
explicit human decision from a decision file — {"write_ref": "...",
"observation_id": "...", "decision": "adopt|not_applied", "remote_id":
"..."} with write_ref and observation_id copied verbatim from submit-status
and reconcile receipts (adopt requires remote_id) — against a CURRENT complete
observation: run reconcile (or plan) first, because only it persists the
observation a resolution can bind to. It is TTY-gated like approve and
submit; every op refusal (stale_observation, unknown_remote, identity_conflict
and the rest) passes through verbatim. The TTY check lives in the CLI wrapper
only: the Session ops stay transport-agnostic.

R4 adds the native approval ledger and one verified submission path. The
'approve_plan' request binds a unique approval to the exact current rendered plan
after fresh precondition proof; it never dispatches. The 'submit_plan' request
revalidates everything fresh, commits a durable pre-write intent per operation
before one never-retried dispatch, and confirms a write only after an identified
acknowledgment AND a matching complete read-back. Unverifiable writes stay
durable unresolved references resolved only by explicit 'resolve_write'
decisions; they never auto-retry, never replay across restarts, and never block
silently. Migrated legacy approvals remain inert audit data. The R1-R3 local
workflow, migration and read boundary are unchanged. The serve protocol in
the protocol reference linked in README.md is the authoritative interface.

Fixed configuration and environment-file paths are launcher inputs. They are
never accepted as protocol request fields.
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		if len(os.Args) != 2 {
			fmt.Fprintln(os.Stderr, "version takes no arguments")
			os.Exit(2)
		}
		fmt.Printf("timesheet %s (protocol schema %d)\n", timesheet.AppVersion, timesheet.ProtocolSchema)
	case "setup":
		if err := runSetup(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			var usageErr *setupUsageError
			if errors.As(err, &usageErr) {
				os.Exit(2)
			}
			os.Exit(1)
		}
	case "serve":
		if err := runServe(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			// AppError carries a stable code and a safe message. Startup errors
			// never echo configuration or credential values.
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "collect":
		if err := runCollect(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "draft":
		if err := runDraft(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "run":
		if err := runRun(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "next":
		if err := runNext(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "tui":
		if err := runTui(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "record":
		if err := runRecord(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "status":
		if err := runStatus(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "stage":
		if err := runStage(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "classifications":
		if err := runClassifications(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "classify":
		if err := runClassify(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "acknowledge":
		if err := runAcknowledge(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "decide":
		if err := runDecide(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "confirm":
		if err := runConfirm(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "save":
		if err := runSave(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "plan":
		if err := runPlan(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "reconcile":
		if err := runReconcile(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "resolve":
		if err := runResolve(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "agent-submit":
		if err := runAgentSubmit(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "approve":
		if err := runApprove(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "submit":
		if err := runSubmit(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "submit-status":
		if err := runSubmitStatus(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	case "resolve-write":
		if err := runResolveWrite(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "timesheet: %s\n", err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func runServe(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configPath := flags.String("config", "", "fixed configuration path supplied by the launcher")
	envFile := flags.String("env-file", "", "fixed environment-file path supplied by the launcher")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("serve does not accept positional arguments")
	}
	if *configPath == "" || *envFile == "" {
		return fmt.Errorf("serve requires --config and --env-file")
	}

	cfg, err := timesheet.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	fileEnv, err := timesheet.LoadEnvFile(*envFile)
	if err != nil {
		return err
	}
	env := timesheet.MergeEnv(fileEnv)
	store, err := timesheet.OpenStore(cfg.StatePath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	session := timesheet.NewSession(cfg, env, store)
	return session.Serve(os.Stdin, os.Stdout)
}
