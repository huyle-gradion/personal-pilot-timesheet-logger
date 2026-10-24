// Command timesheet-rewrite is the development entry point for the rewrite
// line. It is not a compatibility alias for the legacy CLI and it is not a
// release artifact.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/rewrite"
)

func usage() {
	fmt.Fprint(os.Stderr, `timesheet-rewrite `+rewrite.AppVersion+`

Usage:
  timesheet-rewrite version
  timesheet-rewrite serve --config <path> --env-file <path>

R3 adds explicit, repeat-safe migration of confirmed legacy workflow records and
a read-only Gradion boundary for reconciliation and exact submission-plan
previews. Migration is disabled unless configured, preserves unresolved writes as
blockers and historical approvals as inert data, and never writes into the legacy
root. Remote access verifies read capabilities only; no approval, submission or
remote write exists in this build. The local manual workflow and R2 evidence
collection are unchanged. The serve protocol in docs/agents/rewrite-interface.md
is the authoritative interface.

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
		fmt.Printf("timesheet-rewrite %s (protocol schema %d)\n", rewrite.AppVersion, rewrite.ProtocolSchema)
	case "serve":
		if err := runServe(os.Args[2:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			// AppError carries a stable code and a safe message. Startup errors
			// never echo configuration or credential values.
			fmt.Fprintf(os.Stderr, "timesheet-rewrite: %s\n", err)
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

	cfg, err := rewrite.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	fileEnv, err := rewrite.LoadEnvFile(*envFile)
	if err != nil {
		return err
	}
	env := rewrite.MergeEnv(fileEnv)
	store, err := rewrite.OpenStore(cfg.StatePath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	session := rewrite.NewSession(cfg, env, store)
	return session.Serve(os.Stdin, os.Stdout)
}
