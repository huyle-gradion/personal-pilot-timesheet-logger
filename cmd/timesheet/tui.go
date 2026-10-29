package main

// tui is the `tui` verb: the native terminal UI over the same store, config
// and session surface every other command uses. It opens the store once and
// holds the exclusive lock for the whole process, mirroring serve, and every
// refusal (non-terminal stdio, store_locked) happens before the alt screen
// starts. No AI harness and no model calls exist on this path: the loop's own
// derivation (buildNextObservation + deriveDayNext) decides what may happen
// now, and the screen renders it verbatim.
//
// This file owns the bubbletea driver and nothing else: the reducer lives in
// tui_model.go, the views in tui_view.go, the step recipes in tui_steps.go.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

// ttyGate is the interactive-only gate, overridable in tests the way
// interactiveFactory is for the human-gate verbs.
var ttyGate = func() bool { return isTerminal(os.Stdin) && isTerminal(os.Stdout) }

func runTui(args []string) error {
	f, err := parseTuiFlags(args)
	if err != nil {
		return err
	}
	// The gate fires before the store is opened and before the alt screen
	// starts, so a non-terminal invocation opens no store and changes no state.
	if !ttyGate() {
		return fmt.Errorf("tui is interactive-only: run it in a terminal (both stdin and stdout must be terminals)")
	}
	m, err := preflightTui(f)
	if err != nil {
		return err
	}
	defer func() { _ = m.env.h.store.Close() }()
	return runTuiProgram(m)
}

// preflightTui does everything that must succeed before the alt screen starts:
// load the configuration, resolve the day, open the store once and hold the
// lock, build the session over it, and derive the first snapshot. A
// store_locked refusal returns here, before any screen exists.
func preflightTui(f *tuiFlags) (tuiModel, error) {
	configPath, err := filepath.Abs(f.configPath)
	if err != nil {
		return tuiModel{}, fmt.Errorf("tui: the configuration path cannot be made absolute")
	}
	envFile := ""
	if f.envFile != "" {
		if envFile, err = filepath.Abs(f.envFile); err != nil {
			return tuiModel{}, fmt.Errorf("tui: the environment-file path cannot be made absolute")
		}
	}
	cfg, err := timesheet.LoadConfig(configPath)
	if err != nil {
		return tuiModel{}, err
	}
	day, err := resolveDay(configPath, f.date)
	if err != nil {
		return tuiModel{}, err
	}
	store, err := timesheet.OpenStore(cfg.StatePath)
	if err != nil {
		return tuiModel{}, err
	}
	fileEnv := map[string]string{}
	if envFile != "" {
		loaded, err := timesheet.LoadEnvFile(envFile)
		if err != nil {
			_ = store.Close()
			return tuiModel{}, err
		}
		fileEnv = timesheet.MergeEnv(loaded)
	}
	env := tuiEnv{
		cfg: cfg, configPath: configPath, envFile: envFile, day: day, project: f.project,
		// One held session over the TUI's own store, built literally; never
		// openHeadless, which would open a second store.
		h: &headlessSession{
			session: timesheet.NewSession(cfg, fileEnv, store),
			store:   store, cfg: cfg,
			close: func() { _ = store.Close() },
		},
	}
	first, ok := env.tick().(tickEvent)
	if !ok {
		_ = store.Close()
		return tuiModel{}, fmt.Errorf("tui: the first derivation produced no state")
	}
	return tuiModel{env: env, snap: first.snap}, nil
}

// runTuiProgram runs the driver. This is the only TUI code that touches the
// terminal.
func runTuiProgram(m tuiModel) error {
	final, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if model, ok := final.(tuiModel); ok && model.closeErr != nil {
		fmt.Fprintln(os.Stderr, "tui:", model.closeErr)
	}
	if err != nil {
		// The program died rather than quit: release the session's protocol
		// state best-effort before the deferred store close.
		_, _ = m.env.h.call("close", timesheet.Request{})
		return err
	}
	return nil
}

// Init seeds nothing: the first snapshot is already on the model.
func (m tuiModel) Init() tea.Cmd { return nil }

// Update adapts the driver's messages onto the reducer's events and runs at
// most one effect to completion.
func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	ev := tuiEventFromMsg(msg)
	if ev == nil {
		return m, nil
	}
	next, eff := update(m, ev)
	if next.stop {
		return next, tea.Quit
	}
	if eff == nil {
		return next, nil
	}
	run := eff
	return next, func() tea.Msg { return run() }
}

// View renders the screen the model is on.
func (m tuiModel) View() string { return renderDay(m) }

// tuiEventFromMsg maps the driver's messages onto the reducer's events. An
// effect's own event is already a tuiEvent and passes through unchanged.
func tuiEventFromMsg(msg tea.Msg) tuiEvent {
	switch m := msg.(type) {
	case tuiEvent:
		return m
	case tea.KeyMsg:
		return keyEvent{key: m.String()}
	case tea.WindowSizeMsg:
		return windowEvent{width: m.Width, height: m.Height}
	default:
		return nil
	}
}
