package main

// tui is the `tui` verb: the native terminal UI over the same store, config
// and session surface every other command uses. It opens the store once and
// holds the exclusive lock for the whole process, mirroring serve. No AI
// harness and no model calls exist on this path: the loop's own derivation
// (buildNextObservation + deriveDayNext) decides what may happen now, and the
// screen renders it verbatim.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/huyle-gradion/pilot-timesheet/internal/timesheet"
)

// tuiFlags is tui's own surface: the store-path flags and the day, parsed like
// next's (minus next's reserved --do).
type tuiFlags struct {
	configPath string
	envFile    string
	date       string
	project    string
}

func parseTuiFlags(args []string) (*tuiFlags, error) {
	flags := flag.NewFlagSet("tui", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	f := &tuiFlags{}
	flags.StringVar(&f.configPath, "config", "", "configuration path (optional; defaults to $HOME/.timesheet/config.json)")
	flags.StringVar(&f.envFile, "env-file", "", "environment-file path (optional; defaults to $HOME/.timesheet/.env.local when it exists)")
	flags.StringVar(&f.date, "date", "", "day to operate on, YYYY-MM-DD (optional; omitted means today in the configured timezone)")
	flags.StringVar(&f.project, "project", "", "project id for staged/classified intervals (required when the configuration defines multiple projects)")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, fmt.Errorf("tui does not accept positional arguments")
	}
	configPath, envFile, err := resolveStoreFlags("tui", f.configPath, f.envFile)
	if err != nil {
		return nil, err
	}
	f.configPath, f.envFile = configPath, envFile
	if dateSupplied(flags) {
		if _, err := time.Parse("2006-01-02", f.date); err != nil {
			return nil, fmt.Errorf("tui: --date must be YYYY-MM-DD")
		}
	}
	return f, nil
}

// runTui derives the day's loop state and prints it. The seam keeps the
// derivation path identical to `next`: one store, one observation, one
// deriveDayNext call, one typed JSON document on stdout.
func runTui(args []string) error {
	f, err := parseTuiFlags(args)
	if err != nil {
		return err
	}
	configPath, err := filepath.Abs(f.configPath)
	if err != nil {
		return fmt.Errorf("tui: the configuration path cannot be made absolute")
	}
	envFile := ""
	if f.envFile != "" {
		if envFile, err = filepath.Abs(f.envFile); err != nil {
			return fmt.Errorf("tui: the environment-file path cannot be made absolute")
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
	store, err := timesheet.OpenStore(cfg.StatePath)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	obs, stop, err := buildNextObservation(cfg, configPath, envFile, day, f.project, store)
	if err != nil {
		return err
	}
	if stop != nil {
		return printJSON(*stop)
	}
	return printJSON(deriveDayNext(obs))
}
