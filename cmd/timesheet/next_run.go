package main

// runNext is the `next` verb boundary: flag parsing, the read-only observation
// pass, and the typed JSON print. It never opens a write path, never executes a
// derived action, and closes the store before deriving or printing so a future
// in-process dispatcher could re-open the store without an EWOULDBLOCK
// self-lock (store.go's flock is per open file descriptor).

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

// nextFlags is next's own surface. It is deliberately separate from
// parseHeadlessFlags: next also carries --project and --do, and it resolves an
// omitted --date through the binary's existing helper without depending on the
// shared dateOptionalCommands map being wired for "next" yet.
type nextFlags struct {
	configPath string
	envFile    string
	date       string
	project    string
	do         bool
}

func parseNextFlags(args []string) (*nextFlags, error) {
	flags := flag.NewFlagSet("next", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	f := &nextFlags{}
	flags.StringVar(&f.configPath, "config", "", "configuration path (optional; defaults to $HOME/.timesheet/config.json)")
	flags.StringVar(&f.envFile, "env-file", "", "environment-file path (optional; defaults to $HOME/.timesheet/.env.local when it exists)")
	flags.StringVar(&f.date, "date", "", "day to operate on, YYYY-MM-DD (optional; omitted means today in the configured timezone)")
	flags.StringVar(&f.project, "project", "", "project id for staged/classified intervals (required when the configuration defines multiple projects)")
	flags.BoolVar(&f.do, "do", false, "reserved: execute the derived action in-process (unavailable; run action.cmd instead)")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, fmt.Errorf("next does not accept positional arguments")
	}
	configPath, envFile, err := resolveStoreFlags("next", f.configPath, f.envFile)
	if err != nil {
		return nil, err
	}
	f.configPath, f.envFile = configPath, envFile
	if dateSupplied(flags) {
		if _, err := time.Parse("2006-01-02", f.date); err != nil {
			return nil, fmt.Errorf("next: --date must be YYYY-MM-DD")
		}
	}
	return f, nil
}

// runNext derives and prints ONE typed action. Read-only: no artifact, store,
// sidecar or config is written.
//
// Reporting boundary: an underivable STATE (unreadable durable fact, artifact
// or sidecar) prints one typed `stopped` document on stdout; only CLI/flag
// errors, config load failures and the store's own lock refusal
// (store_locked) remain nonzero process errors.
func runNext(args []string) error {
	f, err := parseNextFlags(args)
	if err != nil {
		return err
	}
	// The refusal happens before any config load or store open, so no state is
	// touched by an unsupported mode.
	if f.do {
		return fmt.Errorf("next: --do is not available in this revision; run the printed action.cmd argv and then next again")
	}

	configPath, err := filepath.Abs(f.configPath)
	if err != nil {
		return fmt.Errorf("next: the configuration path cannot be made absolute")
	}
	envFile := ""
	if f.envFile != "" {
		if envFile, err = filepath.Abs(f.envFile); err != nil {
			return fmt.Errorf("next: the environment-file path cannot be made absolute")
		}
	}
	cfg, err := timesheet.LoadConfig(configPath)
	if err != nil {
		return err
	}
	day, err := resolveDay(configPath, f.date)
	if err != nil {
		return err
	}
	exe, err := absoluteExecutable()
	if err != nil {
		return err
	}
	dir, err := dayArtifactsDir(cfg.StatePath, day)
	if err != nil {
		return err
	}
	obs := nextObservation{
		Day:              day,
		Exe:              exe,
		ConfigPath:       configPath,
		EnvFile:          envFile,
		DraftPath:        filepath.Join(dir, "draft.json"),
		DescriptionsPath: filepath.Join(dir, "descriptions.json"),
		ChoicesPath:      filepath.Join(dir, "choices.json"),
		SidecarPath:      timesheet.ChoicesPath(cfg.StatePath, day),
		GradionEnabled:   cfg.Gradion != nil && cfg.Gradion.Enabled,
	}
	obs.Project, obs.ProjectBlocked, obs.ProjectIDs, err = resolveNextProject(cfg, f.project)
	if err != nil {
		return err
	}

	// Durable facts are read under the store's exclusive lock and the store is
	// closed before derivation. An unreadable durable fact is an underivable
	// state and prints a typed stopped document; only CLI/flag errors and the
	// store's own lock refusal (store_locked) stay nonzero process errors.
	store, err := timesheet.OpenStore(cfg.StatePath)
	if err != nil {
		return err
	}
	snapshot, snapErr := timesheet.ReadWorkflowSnapshot(cfg, store, day)
	if closeErr := store.Close(); closeErr != nil {
		return fmt.Errorf("next: the store lock cannot be released")
	}
	if snapErr != nil {
		return printJSON(nextAction{Day: day}.withStop("cannot read the day's durable workflow facts: " + snapErr.Error()))
	}
	obs.Workflow = snapshot

	// The durable snapshot is read under the store lock and the store is closed
	// before the artifacts are read (record never opens the store, so artifact
	// files are lock-free by the binary's existing design). A writer racing
	// between the two reads can only produce a conservative derivation: every
	// derived verb re-validates its own inputs fail-closed, and next mutates
	// nothing.
	ledger, err := timesheet.ReadDayArtifacts(cfg.StatePath, day)
	if err != nil {
		return printJSON(nextAction{Day: day}.withStop("cannot read the day's recorded artifacts: " + err.Error()))
	}
	obs.Artifacts = ledger
	if ledger.Entries != nil {
		obs.Units = ledgerUnits(ledger.Entries.Entries, day)
	}

	// One read per artifact. The draft is read for its content digest (the
	// descriptions answer is bound to it); each answer file is read once for
	// its binding. Nothing is read twice, so a file cannot change between a
	// presence check and a content check.
	draft, err := os.ReadFile(obs.DraftPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return printJSON(nextAction{Day: day}.withStop("cannot read the draft artifact " + obs.DraftPath + ": " + err.Error()))
	default:
		obs.DraftPresent = true
		obs.DraftDigest = draftDigest(draft)
		// Parse the SAME type record unmarshals: an unreadable draft is not
		// judged or recorded against, it is re-collected.
		var parsed timesheet.Draft
		if err := json.Unmarshal(draft, &parsed); err == nil {
			obs.DraftValid, obs.DraftBlocks = true, len(parsed.Blocks)
		}
	}
	obs.DescriptionsPresent, obs.DescriptionsBinding, err = readAnswerBinding(obs.DescriptionsPath, "draft_digest")
	if err != nil {
		return printJSON(nextAction{Day: day}.withStop("cannot read the descriptions answer " + obs.DescriptionsPath + ": " + err.Error()))
	}
	obs.ChoicesPresent, obs.ChoicesBinding, err = readAnswerBinding(obs.ChoicesPath, "ledger_digest")
	if err != nil {
		return printJSON(nextAction{Day: day}.withStop("cannot read the classification answer " + obs.ChoicesPath + ": " + err.Error()))
	}

	return printJSON(deriveDayNext(obs))
}

// resolveNextProject returns the exact project id derived commands carry. It
// never guesses: several configured projects require an explicit --project, and
// an explicit id must be one the configuration defines.
func resolveNextProject(cfg *timesheet.Config, explicit string) (string, bool, []string, error) {
	ids := make([]string, 0, len(cfg.Projects))
	for _, p := range cfg.Projects {
		ids = append(ids, p.ID)
	}
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		for _, id := range ids {
			if id == explicit {
				return id, false, ids, nil
			}
		}
		return "", false, ids, fmt.Errorf("next: --project %s is not configured (configured: %s)", explicit, strings.Join(ids, ", "))
	}
	if len(ids) == 1 {
		return ids[0], false, ids, nil
	}
	return "", true, ids, nil
}

// absoluteExecutable is the running binary's absolute path, so a derived argv
// stays executable after the caller changes directory.
func absoluteExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("next: the running executable path cannot be resolved")
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if !filepath.IsAbs(exe) {
		return "", fmt.Errorf("next: the running executable path is not absolute")
	}
	return exe, nil
}
