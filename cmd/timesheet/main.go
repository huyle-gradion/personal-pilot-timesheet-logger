// Command timesheet runs the local timesheet server.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

func usage() {
	fmt.Fprint(os.Stderr, `timesheet `+timesheet.AppVersion+`

Usage:
  timesheet version
  timesheet serve --config <path> --env-file <path>
  timesheet collect --config <path> [--env-file <path>] --date <YYYY-MM-DD>
  timesheet draft --config <path> [--env-file <path>] --date <YYYY-MM-DD>
  timesheet run --config <path> [--env-file <path>] --date <YYYY-MM-DD>
  timesheet record --config <path> --date <YYYY-MM-DD> (--draft <draft.json> --descriptions <desc.json> | --file <reviewed.json>)
  timesheet status --config <path> [--env-file <path>] [--date <YYYY-MM-DD>]
  timesheet stage --config <path> [--env-file <path>] --date <YYYY-MM-DD> [--project <id>]
  timesheet classifications --config <path> [--env-file <path>] --query <text>
  timesheet classify --config <path> [--env-file <path>] --date <YYYY-MM-DD> --file <choices.json> [--project <id>]
  timesheet acknowledge --config <path> [--env-file <path>] --date <YYYY-MM-DD> --source <github|calendar> --collection-id <id>
  timesheet decide --config <path> [--env-file <path>] --date <YYYY-MM-DD> --meeting-id <id> --decision <attended|skipped> --collection-id <id>
  timesheet confirm --config <path> [--env-file <path>] --date <YYYY-MM-DD> [--project <id>]
  timesheet save --config <path> [--env-file <path>] --date <YYYY-MM-DD> [--project <id>]
  timesheet plan --config <path> [--env-file <path>] --date <YYYY-MM-DD>
  timesheet reconcile --config <path> [--env-file <path>] --date <YYYY-MM-DD>
  timesheet resolve --config <path> [--env-file <path>] --date <YYYY-MM-DD> --file <decision.json>
  timesheet approve --config <path> [--env-file <path>] --date <YYYY-MM-DD>
  timesheet submit --config <path> [--env-file <path>] --date <YYYY-MM-DD> --approval-id <id>
  timesheet submit-status --config <path> [--env-file <path>] --date <YYYY-MM-DD>
  timesheet resolve-write --config <path> [--env-file <path>] --file <decision.json>

The collect/draft/run/record/status subcommands are the deterministic headless
surface used by the timesheet skill: collect performs one bounded collection
attempt, draft prints the compact one-day draft the skill's single judgment
step reviews, run composes collect and draft in one process and prints ONLY
the draft JSON on stdout (the collect source report is not printed; its
substance is already in the draft's flags and per-block completeness) while
also writing the same draft bytes to the deterministic artifacts directory
$TMPDIR/timesheet/<date>/draft.json (created as needed; the path is printed
on stderr, so redirecting stdout yields exactly the draft bytes), record
merges the reviewed result into the per-day entries file
(idempotent per time block) — composing it from --draft plus the
descriptions-only --descriptions file, or taking the full reviewed --file
(recovery path) — and status reports store state plus recorded
entries. They hold the same fixed-path, no-shell, credential-safe contract as
serve and perform no approval or submission.

The stage/acknowledge/decide/confirm/save subcommands finalize one recorded
day (campaign issue #160, slice SUBMIT-1). Natural order: run --date first so
collect has attempted every enabled source; then stage converts the day's
recorded entries into staged intervals; then acknowledge (and decide) with
the --collection-id copied verbatim from that collect receipt; then confirm;
then save. stage converts entries with both a start and an end into intervals
through the same session ops serve uses (the description text carries any PRs
list verbatim); entries without time anchors are skipped and listed in the
receipt — times are never invented. The entries file stays the single human
ledger: each command re-derives the staged set from the entries ledger in its
own process, the entries/<date>.stage.json block-to-interval sidecar serves
same-process restage and audit, and a day that already has confirmed
intervals is never restaged. Interval ids in receipts are valid only against
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
re-derives the staged set from the entries ledger exactly as stage does
(same derivation, same interval ids for the same entries state), then applies
choices.json — {"selections": {"<interval_id>": {"catalog_id": "...",
"classification_id": "..."}}} with both ids copied verbatim from the
classifications and stage receipts — through the select_classification op,
and persists the selections to entries/<date>.classify.json, each
content-bound to the recorded block it was chosen for (a SHA-256 digest of
the block's date, block_id, start, end and description). An empty file ({})
is a valid no-op and clears any earlier selections. An unknown interval id
fails closed with the op's own error, untouched. Selections on staged
intervals are session-only, so classify alone makes nothing durable: confirm
and save replay the sidecar's selections against their own re-derived staged
set before confirming, and save commits remote_classification_id durably —
but ONLY when the re-derived day is content-identical to the one classify
chose against. Staged interval ids re-derive sequentially, so any entries
change after classify (insertion, removal, replacement, time or description
edit) would silently re-attach a selection to the wrong interval; the
content binding makes that impossible — the replay fails closed naming the
block, and the fix is to re-run stage + classify. Nothing here approves or
submits anything.

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
