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
  timesheet record --config <path> --date <YYYY-MM-DD> --file <reviewed.json>
  timesheet status --config <path> [--env-file <path>] [--date <YYYY-MM-DD>]

The collect/draft/record/status subcommands are the deterministic headless
surface used by the timesheet skill: collect performs one bounded collection
attempt, draft prints the compact one-day draft the skill's single judgment
step reviews, record merges the reviewed result into the per-day entries file
(idempotent per time block), and status reports store state plus recorded
entries. They hold the same fixed-path, no-shell, credential-safe contract as
serve and perform no approval or submission.

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
