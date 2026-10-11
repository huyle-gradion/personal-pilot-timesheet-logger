// Command timesheet is the THIN CLI adapter over the shared deterministic core
// (internal/run). All workflow logic lives in the core; this binary only parses
// flags, builds the injected collaborators from NON-SECRET config, and prints
// results. Secrets are resolved in memory (never persisted) and never printed.
//
// A bare `-approve` does NOT exist: approval is bound to an exact plan content
// hash via `approve --plan <PLANID>`, and `submit` executes only the record's
// ACTIVE approval.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/editorial"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence/plugins"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/export"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/git"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/keychain"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/mcp"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/run"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/selection"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/submit"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// Exit codes.
const (
	exitOK      = 0
	exitError   = 1
	exitUsage   = 2
	exitBlocked = 3
)

// Named bounds.
const (
	maxRangeDays    = 366
	githubTimeout   = 15 * time.Second
	keychainRefPref = "keychain:"
)

func main() {
	os.Exit(runCLIWithIO(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr,
		isTerminalFD(os.Stdin.Fd()), writerIsTTY(os.Stdout)))
}

// cliConfig is the CLI's non-secret configuration document (stdlib JSON). It
// embeds run.Config (the evidence config + optional date) and adds the storage,
// vault, Gradion MCP, editorial, and classification settings.
type cliConfig struct {
	run.Config
	StorageDir      string            `json:"storage_dir,omitempty"`
	Vault           string            `json:"vault,omitempty"`
	DailyDir        string            `json:"daily_dir,omitempty"`
	Gradion         gradionConfig     `json:"gradion,omitempty"`
	Editorial       editorial.Config  `json:"editorial,omitempty"`
	Classifications map[string]string `json:"classifications,omitempty"`
}

// gradionConfig points at the Gradion MCP endpoint and a keychain reference for
// its credential (never a raw secret).
type gradionConfig struct {
	Endpoint      string `json:"endpoint,omitempty"`
	CredentialRef string `json:"credential_ref,omitempty"`
}

func (c cliConfig) storageDir() string {
	if d := strings.TrimSpace(c.StorageDir); d != "" {
		return d
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".timesheet", "records")
	}
	return ".timesheet/records"
}

func runCLI(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return runCLIWithIO(ctx, args, os.Stdin, stdout, stderr,
		isTerminalFD(os.Stdin.Fd()), writerIsTTY(stdout))
}

// writerIsTTY reports whether the PASSED writer is a terminal, by inspecting its
// ACTUAL descriptor when it is an *os.File. A redirected buffer, pipe, or any
// non-file writer is NOT a terminal, so a caller that redirects stdout never
// selects the TUI on the strength of the process's global os.Stdout.
func writerIsTTY(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		return isTerminalFD(f.Fd())
	}
	return false
}

func runCLIWithIO(ctx context.Context, args []string, in io.Reader, stdout, stderr io.Writer, stdinTTY, stdoutTTY bool) int {
	if len(args) == 0 {
		printUsage(stderr)
		return exitUsage
	}
	sub := args[0]
	if sub == "help" || sub == "-h" || sub == "--help" {
		printUsage(stdout)
		return exitOK
	}
	// The metrics subcommand has its OWN subcommands (import/aggregate/report) that
	// precede its flags, so it is dispatched before the shared flag set: the
	// standard flag package stops at the first non-flag argument ("import"). It
	// shares no state with the workflow runner and never triggers submission.
	if sub == "metrics" {
		return cmdMetrics(ctx, args[1:], stdout, stderr)
	}
	// The classify subcommand also has its OWN subcommands (list/select) that
	// precede its flags, so it is dispatched before the shared flag set.
	if sub == "classify" {
		return cmdClassify(ctx, args[1:], stdout, stderr)
	}
	// The config subcommand also has its OWN subcommands (init/edit) that precede
	// its flags, so it is dispatched before the shared flag set. It authors the
	// SAME non-secret cliConfig schema and never resolves a secret.
	if sub == "config" {
		return cmdConfig(ctx, args[1:], in, stdout, stderr, stdinTTY, stdoutTTY)
	}

	fs := flag.NewFlagSet("timesheet "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		date           = fs.String("date", "", "target date YYYY-MM-DD (default: today in the configured timezone)")
		from           = fs.String("from", "", "range start YYYY-MM-DD")
		to             = fs.String("to", "", "range end YYYY-MM-DD")
		configPath     = fs.String("config", "", "path to the JSON config document")
		storageDir     = fs.String("storage-dir", "", "workflow record directory (overrides config)")
		offline        = fs.Bool("offline", false, "skip the Gradion remote; source fetches (e.g. calendar ICS) still run")
		deep           = fs.Bool("deep", false, "validate ALL configured sources (including disabled) during preflight")
		editorialOn    = fs.Bool("editorial", true, "enable the optional editorial agent when configured")
		noEditorial    = fs.Bool("no-editorial", false, "disable the editorial agent (deterministic text only)")
		decisionsPath  = fs.String("decisions", "", "path to a decisions JSON file ({date} is substituted per day)")
		nonInteractive = fs.Bool("non-interactive", false, "never prompt (force the scripted path)")
		interactive    = fs.Bool("interactive", false, "start the guided run (needs a terminal on stdin and stdout)")
		plain          = fs.Bool("plain", false, "guided run with safe line-based prompts (no TUI)")
		ack            = fs.String("ack", "", "comma-separated source ids to acknowledge")
		planID         = fs.String("plan", "", "plan id to approve (approve)")
		approvalID     = fs.String("approval", "", "approval id to submit (must match the active approval)")
		vault          = fs.String("vault", "", "vault root for the managed export (overrides config)")
		dailyDir       = fs.String("daily-dir", "", "daily-note directory, relative to the vault (overrides config)")
	)
	if err := fs.Parse(args[1:]); err != nil {
		return exitUsage
	}
	if (*interactive || *plain) && sub != "run" {
		fmt.Fprintln(stderr, "timesheet: --interactive/--plain apply to `run` only")
		return exitUsage
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet:", err)
		return exitError
	}
	if *storageDir != "" {
		cfg.StorageDir = *storageDir
	}
	if *vault != "" {
		cfg.Vault = *vault
	}
	if *dailyDir != "" {
		cfg.DailyDir = *dailyDir
	}
	if *date != "" {
		cfg.Date = *date
	}

	store, err := workflow.NewJSONFileStore(cfg.storageDir())
	if err != nil {
		fmt.Fprintln(stderr, "timesheet:", err)
		return exitError
	}

	editorialEnabled := *editorialOn && !*noEditorial
	runner, editorActive, err := buildRunner(ctx, cfg, store, *offline, editorialEnabled)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet:", err)
		return exitError
	}

	switch sub {
	case "preflight":
		return cmdPreflight(ctx, runner, *deep, stdout, stderr)
	case "review":
		return cmdReview(ctx, runner, cfg, editorActive, *decisionsPath, *ack, *date, *from, *to, stdout, stderr)
	case "export":
		return cmdExport(ctx, runner, *date, *from, *to, stdout, stderr)
	case "preview":
		return cmdPreview(ctx, store, runner, cfg, *date, stdout, stderr)
	case "approve":
		return cmdApprove(ctx, store, runner, *date, *planID, stdout, stderr)
	case "submit":
		return cmdSubmit(ctx, runner, cfg, *date, *approvalID, *offline, stdout, stderr)
	case "resolve":
		return cmdResolve(ctx, runner, *date, *decisionsPath, stdout, stderr)
	case "run":
		mode, modeErr := resolveMode(guidedRequest{
			Interactive:    *interactive,
			Plain:          *plain,
			NonInteractive: *nonInteractive,
			Decisions:      strings.TrimSpace(*decisionsPath) != "",
			StdinTTY:       stdinTTY,
			StdoutTTY:      stdoutTTY,
			CI:             ciDetected(),
		})
		if modeErr != nil {
			fmt.Fprintln(stderr, "timesheet:", modeErr)
			return exitUsage
		}
		if mode == modeScripted {
			return cmdRun(ctx, runner, cfg, editorActive, *deep, *decisionsPath, *ack, *date, *from, *to, stdout, stderr)
		}
		if strings.TrimSpace(*from) != "" || strings.TrimSpace(*to) != "" {
			fmt.Fprintln(stderr, "timesheet: the guided run reviews one day at a time; use --date, or the scripted path, for a range")
			return exitUsage
		}
		var p prompter
		if mode == modeTUI {
			p = newTUIPrompter(in, stdout)
		} else {
			p = newPlainPrompter(in, stderr)
		}
		return cmdRunGuided(ctx, runner, cfg, store, editorActive, *deep, *ack, *date, p, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "timesheet: unknown subcommand %q\n", sub)
		printUsage(stderr)
		return exitUsage
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `timesheet <subcommand> [flags]

Subcommands:
  preflight   validate timezone, credentials, source configuration, and MCP capabilities
  review      collect + propose (+ editorial) and apply --decisions, or print the proposal
  export      write the managed daily-note section (never contacts the remote)
  preview     build and persist the submission plan (zero remote writes)
  approve     bind an approval to a plan: approve --plan <PLANID>
  submit      execute the record's ACTIVE approval: submit --date D [--approval <APPROVALID>]
  resolve     reconcile unresolved operations from --decisions
  run         preflight + collect + review + export convenience (--interactive for a guided run)
  classify    classification catalog: classify list | classify select
  metrics     token usage + durations (metrics import|aggregate|report|compare|run)
  config      author a config file: config init | config edit

Flags:
  --date YYYY-MM-DD        target date (default: today in the configured timezone)
  --from/--to YYYY-MM-DD   iterate days with per-day review/approval
  --config FILE            JSON config document
  --storage-dir DIR        workflow record directory (default: ~/.timesheet/records)
  --offline                skip the Gradion remote; source fetches (e.g. calendar ICS) still run
  --deep                   validate ALL configured sources (including disabled) during preflight
  --editorial/--no-editorial  toggle the optional editorial agent
  --decisions FILE         decisions JSON ({date} substituted per day)
  --non-interactive        never prompt (force the scripted path)
  --interactive            guided run (needs a terminal on stdin AND stdout)
  --plain                  guided run with safe line-based prompts (no TUI)
  --ack SOURCE,...         acknowledge incomplete sources
  --plan PLANID            plan id to approve
  --approval APPROVALID    approval id to submit (must match the active approval)
  --vault DIR              vault root for the managed export
  --daily-dir DIR          daily-note directory relative to the vault
`)
}

// --- config + collaborator construction -------------------------------------

func loadConfig(path string) (cliConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return cliConfig{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cliConfig{}, fmt.Errorf("read config: %w", err)
	}
	var cfg cliConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cliConfig{}, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Editorial.Validate(); err != nil {
		return cliConfig{}, err
	}
	return cfg, nil
}

func buildRunner(ctx context.Context, cfg cliConfig, store workflow.Store, offline, editorialEnabled bool) (*run.Runner, bool, error) {
	deps, editorActive, err := buildDeps(ctx, cfg, store, offline, editorialEnabled)
	if err != nil {
		return nil, false, err
	}
	r, err := run.New(deps, cfg.Config)
	if err != nil {
		return nil, false, err
	}
	return r, editorActive, nil
}

func buildDeps(ctx context.Context, cfg cliConfig, store workflow.Store, offline, editorialEnabled bool) (run.Deps, bool, error) {
	// The git credential is resolved LAZILY: only when a git source needs a
	// REMOTE credential (a mode:"local" git source does not). A read-only
	// subcommand (e.g. export) must never abort on a Keychain error.
	gitToken := ""
	if run.GitRemoteSourceEnabled(cfg.Evidence.Sources) {
		tok, err := git.ResolveAuthToken(ctx, "")
		if err != nil {
			return run.Deps{}, false, fmt.Errorf("git credential: %w", err)
		}
		gitToken = tok
	}

	deps := run.Deps{
		Store:    store,
		Registry: buildRegistry(gitToken),
		Now:      time.Now,
		GitToken: gitToken,
	}

	if !offline {
		remote, err := buildRemote(ctx, cfg)
		if err != nil {
			return run.Deps{}, false, err
		}
		deps.Remote = remote
	}

	editorActive := false
	if editorialEnabled {
		editor, err := cfg.Editorial.NewEditor()
		if err != nil {
			return run.Deps{}, false, err
		}
		deps.Editor = editor
		editorActive = editor != nil
	}

	if v := strings.TrimSpace(cfg.Vault); v != "" {
		deps.Exporter = export.NewNoteExporter(v, cfg.DailyDir)
	}
	return deps, editorActive, nil
}

// buildRegistry registers the compile-time source plugins. No dynamic plugin
// loading exists: unknown executables are never enabled.
func buildRegistry(gitToken string) *evidence.Registry {
	reg := evidence.NewRegistry()
	reg.Register(plugins.NewCalendarPlugin())
	reg.Register(plugins.NewAgentPlugin())
	gitPlugin := plugins.NewGitPlugin(
		plugins.GitHubClientAdapter{Client: git.NewGitHubClient(gitToken, githubTimeout)},
		// The historical single local collector covers the default "." path.
		plugins.LocalGitCollectorAdapter{Collector: git.NewLocalGitCollector(".")},
	)
	// Explicit local_repos paths beyond "." get a per-path collector.
	gitPlugin.LocalFactory = func(path string) plugins.LocalGitSource {
		return plugins.LocalGitCollectorAdapter{Collector: git.NewLocalGitCollector(path)}
	}
	reg.Register(gitPlugin)
	// The optional local read-only journal (Obsidian daily-note) source. It is
	// registered explicitly here: a journal source is only ever enabled when the
	// user configures one.
	plugins.RegisterJournal(reg)
	return reg
}

// buildRemote builds the Gradion MCP client from non-secret config, resolving the
// credential reference in memory. It returns a nil Remote when no endpoint is
// configured (offline).
func buildRemote(ctx context.Context, cfg cliConfig) (run.Remote, error) {
	endpoint := strings.TrimSpace(cfg.Gradion.Endpoint)
	if endpoint == "" {
		return nil, nil
	}
	token, err := resolveSecretRef(ctx, cfg.Gradion.CredentialRef)
	if err != nil {
		return nil, err
	}
	sess, err := mcp.NewSession(mcp.Config{Endpoint: endpoint, Token: token})
	if err != nil {
		return nil, fmt.Errorf("mcp session: %w", err)
	}
	client := gradion.NewClient(sess)
	if _, err := client.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}
	if err := client.VerifyCapabilities(ctx); err != nil {
		return nil, fmt.Errorf("mcp capabilities: %w", err)
	}
	return client, nil
}

// resolveSecretRef resolves a keychain:<service> reference in memory. A raw value
// is rejected: secrets are never stored in config.
func resolveSecretRef(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	if !strings.HasPrefix(ref, keychainRefPref) {
		return "", errors.New("credential reference must be keychain:<service>")
	}
	service := strings.TrimSpace(strings.TrimPrefix(ref, keychainRefPref))
	if service == "" {
		return "", errors.New("credential reference has an empty service")
	}
	return keychain.Lookup(ctx, service)
}

func classificationResolver(cfg cliConfig) submit.ClassificationResolver {
	return func(project string) (string, bool) {
		id, ok := cfg.Classifications[strings.TrimSpace(project)]
		return id, ok
	}
}

// --- date range -------------------------------------------------------------

func resolveDates(r *run.Runner, date, from, to string) ([]string, error) {
	if d := strings.TrimSpace(date); d != "" {
		return []string{d}, nil
	}
	f, t := strings.TrimSpace(from), strings.TrimSpace(to)
	if f != "" || t != "" {
		if f == "" || t == "" {
			return nil, errors.New("--from and --to must be given together")
		}
		return dayRange(f, t)
	}
	d, err := r.Date()
	if err != nil {
		return nil, err
	}
	return []string{d}, nil
}

func dayRange(from, to string) ([]string, error) {
	start, err := time.ParseInLocation("2006-01-02", from, time.UTC)
	if err != nil {
		return nil, fmt.Errorf("invalid --from %q", from)
	}
	end, err := time.ParseInLocation("2006-01-02", to, time.UTC)
	if err != nil {
		return nil, fmt.Errorf("invalid --to %q", to)
	}
	if end.Before(start) {
		return nil, errors.New("--to must not precede --from")
	}
	var out []string
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		out = append(out, d.Format("2006-01-02"))
		if len(out) > maxRangeDays {
			return nil, fmt.Errorf("range exceeds %d days", maxRangeDays)
		}
	}
	return out, nil
}

// --- subcommands ------------------------------------------------------------

func cmdPreflight(ctx context.Context, r *run.Runner, deep bool, stdout, stderr io.Writer) int {
	res, err := r.Preflight(ctx, run.PreflightOptions{Deep: deep})
	if err != nil {
		fmt.Fprintln(stderr, "timesheet: preflight:", err)
		return exitError
	}
	for _, c := range res.Checks {
		status := "ok"
		if !c.Passed {
			status = "FAIL"
		}
		line := fmt.Sprintf("%-4s %s", status, c.Name)
		if c.Code != "" {
			line += " [" + c.Code + "]"
		}
		fmt.Fprintln(stdout, line)
	}
	if !res.OK {
		fmt.Fprintln(stderr, "timesheet: preflight refused")
		return exitBlocked
	}
	return exitOK
}

func cmdReview(ctx context.Context, r *run.Runner, cfg cliConfig, editorActive bool, decisionsPath, ack, date, from, to string, stdout, stderr io.Writer) int {
	dates, err := resolveDates(r, date, from, to)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet:", err)
		return exitError
	}
	for _, d := range dates {
		// Always collect, even with NO configured sources: Collect with an empty
		// registry persists the day's record with an empty collection summary, so an
		// empty draft is durable and `review` is consistent for every date. Collect
		// preserves user decisions (the merge is an upsert).
		collectRes, err := r.Collect(ctx, d)
		if err != nil {
			fmt.Fprintf(stderr, "timesheet: collect %s: %v\n", d, err)
			return exitError
		}
		// Explicitly report the no-configured-source condition (empty source list
		// or every source disabled) so "nothing is configured" is never confused
		// with "configured sources returned no evidence".
		if collectRes.NoSourcesConfigured {
			fmt.Fprintf(stdout, "%s: No evidence sources configured.\n", d)
		}
		// Surface every enabled source that is not complete/skipped, so a remote
		// failure or an incomplete collection is visible.
		printSourceStatus(stdout, collectRes.Summary)
		prop, err := r.Propose(ctx, d)
		if err != nil {
			fmt.Fprintf(stderr, "timesheet: propose %s: %v\n", d, err)
			return exitError
		}
		if editorActive {
			if _, err := r.Synthesize(ctx, d, prop); err != nil {
				fmt.Fprintf(stderr, "timesheet: synthesize %s: %v\n", d, err)
				return exitError
			}
			if prop, err = r.CurrentProposal(ctx, d); err != nil {
				fmt.Fprintf(stderr, "timesheet: proposal %s: %v\n", d, err)
				return exitError
			}
		}

		path := strings.TrimSpace(substituteDate(decisionsPath, d))
		if path == "" {
			// --ack applies INDEPENDENTLY of --decisions.
			if a := splitCSV(ack); len(a) > 0 {
				if err := r.Acknowledge(ctx, d, a); err != nil {
					fmt.Fprintf(stderr, "timesheet: acknowledge %s: %v\n", d, err)
					return exitError
				}
				fmt.Fprintf(stdout, "%s: acknowledged %s\n", d, strings.Join(a, ", "))
			}
			printProposal(stdout, d, prop)
			continue
		}
		decisions, err := loadDecisions(path, ack)
		if err != nil {
			fmt.Fprintf(stderr, "timesheet: decisions %s: %v\n", d, err)
			return exitError
		}
		if err := r.ApplyReview(ctx, d, decisions); err != nil {
			fmt.Fprintf(stderr, "timesheet: review %s: %v\n", d, err)
			return exitError
		}
		fmt.Fprintf(stdout, "%s: review applied\n", d)
	}
	return exitOK
}

// printSourceStatus prints one concise line for every enabled source that is not
// complete or skipped, so a partial or failed collection is visible. Details are
// drawn from the stable error code, else the propagated registry warnings, else
// the message; none of these ever contains a secret.
func printSourceStatus(w io.Writer, sum evidence.CollectionSummary) {
	for _, sr := range sum.Sources {
		if sr.Status == evidence.StatusComplete || sr.Status == evidence.StatusSkipped {
			continue
		}
		detail := sr.ErrorCode
		if detail == "" {
			detail = strings.Join(sr.Warnings, ", ")
		}
		if detail == "" {
			detail = sr.Message
		}
		line := fmt.Sprintf("source %s: %s", sr.SourceID, sr.Status)
		if detail != "" {
			line += " (" + detail + ")"
		}
		fmt.Fprintln(w, line)
	}
}

func cmdExport(ctx context.Context, r *run.Runner, date, from, to string, stdout, stderr io.Writer) int {
	dates, err := resolveDates(r, date, from, to)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet:", err)
		return exitError
	}
	for _, d := range dates {
		res, err := r.Export(ctx, d)
		if err != nil {
			fmt.Fprintf(stderr, "timesheet: export %s: %v\n", d, err)
			return exitError
		}
		if res.Written {
			fmt.Fprintf(stdout, "%s: exported %s (%s)\n", d, res.Path, res.Status)
		} else {
			fmt.Fprintf(stdout, "%s: export disabled\n", d)
		}
	}
	return exitOK
}

func cmdPreview(ctx context.Context, store workflow.Store, r *run.Runner, cfg cliConfig, date string, stdout, stderr io.Writer) int {
	d, err := singleDate(r, date)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet:", err)
		return exitError
	}
	res, err := r.Preview(ctx, d, classificationResolver(cfg))
	if err != nil {
		fmt.Fprintf(stderr, "timesheet: preview %s: %v\n", d, err)
		return exitError
	}
	if res.Blocked {
		printSuggestions(stdout, res.Suggestions)
		fmt.Fprintf(stdout, "preview blocked: %s\n", strings.Join(res.BlockReasons, ", "))
		return exitBlocked
	}
	// Machine-readable lines (kept byte-for-byte: the e2e extracts plan_id:).
	fmt.Fprintf(stdout, "plan_id: %s\n", res.Plan.PlanID)
	fmt.Fprintf(stdout, "operations: %d\n", len(res.Plan.Operations))
	// Suggestions are NON-APPLIED hints; surface them so the user can accept one
	// with an explicit `timesheet classify select`.
	printSuggestions(stdout, res.Suggestions)
	// Human-readable detail: the EXACT creates/edits, intervals, descriptions,
	// projects, and total hours the user approves (secret-free: plan fields only).
	projects := confirmedProjects(ctx, store, d)
	fmt.Fprintf(stdout, "remote observed: %s\n", res.Plan.RemoteObservedAt.Format(time.RFC3339))
	fmt.Fprintf(stdout, "remote complete: %t\n", res.Plan.RemoteComplete)
	totalMinutes := 0
	for _, op := range res.Plan.Operations {
		fmt.Fprintln(stdout, formatPlanOperation(op, cfg, projects))
		totalMinutes += minutesBetween(op.StartTime, op.EndTime)
	}
	fmt.Fprintf(stdout, "total: %dh %dm across %d operation(s)\n", totalMinutes/60, totalMinutes%60, len(res.Plan.Operations))
	return exitOK
}

// confirmedProjects maps each confirmed interval's LocalID to its project name, so
// the preview labels an operation from the interval the user actually reviewed.
func confirmedProjects(ctx context.Context, store workflow.Store, date string) map[string]string {
	rec, err := store.GetDay(ctx, date)
	if err != nil || rec == nil {
		return nil
	}
	out := make(map[string]string, len(rec.Confirmed))
	for _, ci := range rec.Confirmed {
		out[ci.ID] = strings.TrimSpace(ci.Project)
	}
	return out
}

// formatPlanOperation renders one planned create/edit as a stable, greppable line:
//
//	"  <create|edit> <start>-<end> <project-or-classification> <description>[ remote=<id>]"
//
// The label prefers the CONFIRMED INTERVAL's own project; it falls back to the
// config classifications reverse lookup, then the classification id. The rendered
// text is collapsed to one line. It never prints a secret.
func formatPlanOperation(op workflow.PlanOperation, cfg cliConfig, projects map[string]string) string {
	line := fmt.Sprintf("  %s %s-%s %s %s", op.Kind, op.StartTime, op.EndTime,
		oneLine(operationLabel(op, cfg, projects)), oneLine(op.Description))
	if op.Kind == workflow.PlanOpEdit {
		line += " remote=" + op.RemoteID
	}
	return line
}

// operationLabel labels an operation. It prefers the confirmed interval's OWN
// project (via the plan op's LocalID), because a classification-id reverse lookup
// can mislabel when several projects share a classification id or when the
// interval's project is absent from the map. It then falls back to the deterministic
// reverse lookup, then the classification id, then "(unclassified)".
func operationLabel(op workflow.PlanOperation, cfg cliConfig, projects map[string]string) string {
	if p := strings.TrimSpace(projects[op.LocalID]); p != "" {
		return p
	}
	if best := reverseClassificationLabel(op.ClassificationID, cfg); best != "" {
		return best
	}
	if strings.TrimSpace(op.ClassificationID) == "" {
		return "(unclassified)"
	}
	return op.ClassificationID
}

// reverseClassificationLabel returns the lexicographically smallest configured
// project name mapped to classID, or "" (an empty classID never matches).
func reverseClassificationLabel(classID string, cfg cliConfig) string {
	if strings.TrimSpace(classID) == "" {
		return ""
	}
	best := ""
	for project, cid := range cfg.Classifications {
		if cid != classID {
			continue
		}
		if best == "" || project < best {
			best = project
		}
	}
	return best
}

// oneLine collapses control characters (including newlines/tabs) to a single space
// so a rendered operation stays on one line, and trims the result.
func oneLine(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	pendingSpace := false
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			pendingSpace = true
			continue
		}
		if pendingSpace && b.Len() > 0 {
			b.WriteByte(' ')
		}
		pendingSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// minutesBetween returns the exact minutes from "HH:MM" to "HH:MM". Only an end
// BEFORE the start is treated as crossing midnight (equal times return 0).
// Unparseable times contribute 0.
func minutesBetween(start, end string) int {
	s, err1 := time.Parse("15:04", strings.TrimSpace(start))
	e, err2 := time.Parse("15:04", strings.TrimSpace(end))
	if err1 != nil || err2 != nil {
		return 0
	}
	m := int(e.Sub(s).Minutes())
	if m < 0 {
		m += 24 * 60
	}
	return m
}

func cmdApprove(ctx context.Context, store workflow.Store, r *run.Runner, date, planID string, stdout, stderr io.Writer) int {
	d, err := singleDate(r, date)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet:", err)
		return exitError
	}
	if strings.TrimSpace(planID) == "" {
		fmt.Fprintln(stderr, "timesheet: approve requires --plan <PLANID>")
		return exitUsage
	}
	rec, err := store.GetDay(ctx, d)
	if err != nil {
		fmt.Fprintf(stderr, "timesheet: approve %s: %v\n", d, err)
		return exitError
	}
	plan, ok := workflow.FindPlan(rec, planID)
	if !ok {
		fmt.Fprintf(stderr, "timesheet: plan %s not found for %s\n", planID, d)
		return exitError
	}
	approval, err := r.Approve(ctx, d, plan)
	if err != nil {
		fmt.Fprintf(stderr, "timesheet: approve %s: %v\n", d, err)
		return exitError
	}
	fmt.Fprintf(stdout, "approval_id: %s\n", approval.ApprovalID)
	return exitOK
}

func cmdSubmit(ctx context.Context, r *run.Runner, cfg cliConfig, date, approvalID string, offline bool, stdout, stderr io.Writer) int {
	d, err := singleDate(r, date)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet:", err)
		return exitError
	}
	// Approval safety: when a remote is configured, obtain a catalog (live, else a
	// FRESH cache) and validate every planned classification before any write. A
	// stale or unavailable catalog blocks rather than defaulting. `Catalog` is
	// MANDATORY whenever the runner has a Remote: the CLI always supplies it here.
	var cat *gradion.Catalog
	if !offline && strings.TrimSpace(cfg.Gradion.Endpoint) != "" {
		var prov string
		cat, prov = loadSubmitCatalog(ctx, cfg)
		if cat == nil {
			fmt.Fprintln(stdout, "submit blocked: "+run.BlockMissingCatalog+" (no live catalog and no fresh cache)")
			return exitBlocked
		}
		// ALWAYS disclose which catalog validated the submission.
		fmt.Fprintf(stdout, "provenance: %s\n", prov)
	}
	res, err := r.Submit(ctx, d, run.SubmitOptions{ApprovalID: approvalID, Resolver: classificationResolver(cfg), Catalog: cat})
	if err != nil {
		fmt.Fprintf(stderr, "timesheet: submit %s: %v\n", d, err)
		return exitError
	}
	if res.Blocked {
		fmt.Fprintf(stdout, "submit blocked: %s\n", strings.Join(res.BlockReasons, ", "))
		return exitBlocked
	}
	fmt.Fprintf(stdout, "outcomes: %d partial=%v\n", len(res.Execution.Outcomes), res.Execution.Partial)
	if res.Export.Written {
		fmt.Fprintf(stdout, "exported: %s\n", res.Export.Path)
	}
	return exitOK
}

// loadSubmitCatalog resolves the catalog that validates a submission. It prefers
// the UNFILTERED cache entry (query ""), then a live unfiltered fetch (saved to
// the unfiltered cache entry), then the newest PARTIAL cache entry — which the
// submission boundary may use to TARGETED-validate an absent choice with a
// per-id read. It returns the catalog and a provenance/freshness line, or
// (nil, "") when nothing usable is available.
func loadSubmitCatalog(ctx context.Context, cfg cliConfig) (*gradion.Catalog, string) {
	cachePath, pathErr := selection.DefaultCatalogCachePath()
	now := time.Now().UTC()

	// 1. Prefer a FRESH UNFILTERED cache entry, so a filtered search can never
	// shadow the whole-catalog entry the submission gate needs.
	if pathErr == nil {
		if cached, err := selection.LoadCatalogCache(cachePath, ""); err == nil {
			if prov, fresh := catalogProvenance(cached, now); fresh {
				return &cached, prov
			}
		}
	}
	// 2. Else fetch live (unfiltered) and cache it under the unfiltered entry.
	if client, err := newCatalogFetcher(ctx, cfg); err == nil {
		if fetched, ferr := client.ClassificationCatalog(ctx, ""); ferr == nil {
			fetched.Source = "live"
			if pathErr == nil {
				_ = selection.SaveCatalogCache(cachePath, fetched)
			}
			prov, _ := catalogProvenance(fetched, now)
			return &fetched, prov
		}
	}
	// 3. Else fall back to the newest PARTIAL cache entry; the submit boundary
	// targeted-validates any choice absent from it. This keeps a recent preference
	// outside the current search resolvable rather than treating it as invalid.
	if pathErr == nil {
		if cached, err := selection.LoadCatalogCacheAny(cachePath); err == nil {
			if prov, fresh := catalogProvenance(cached, now); fresh {
				return &cached, prov
			}
		}
	}
	return nil, ""
}

// catalogProvenance returns the freshness/provenance line for a catalog used to
// validate a submission. A catalog that is NOT full (unfiltered AND complete) is
// labelled PARTIAL, so a filtered or truncated entry is never presented as a
// complete whole-catalog validation. fresh mirrors selection.Freshness.
func catalogProvenance(cat gradion.Catalog, now time.Time) (string, bool) {
	prov, fresh := selection.Freshness(cat, now)
	if !cat.IsFullCatalog() {
		prov += " (partial; targeted validation)"
	}
	return prov, fresh
}

// printSuggestions renders NON-APPLIED classification suggestions.
func printSuggestions(w io.Writer, suggestions []submit.ClassificationSuggestion) {
	for _, s := range suggestions {
		fmt.Fprintf(w, "suggestion: %s project=%s -> %s (source=%s)\n",
			s.IntervalID, s.Project, s.SuggestedClassificationID, s.Source)
	}
}

func cmdResolve(ctx context.Context, r *run.Runner, date, decisionsPath string, stdout, stderr io.Writer) int {
	d, err := singleDate(r, date)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet:", err)
		return exitError
	}
	path := strings.TrimSpace(decisionsPath)
	if path == "" {
		fmt.Fprintln(stderr, "timesheet: resolve requires --decisions FILE")
		return exitUsage
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "timesheet: decisions: %v\n", err)
		return exitError
	}
	var decisions []submit.Resolution
	if err := json.Unmarshal(data, &decisions); err != nil {
		fmt.Fprintf(stderr, "timesheet: parse decisions: %v\n", err)
		return exitError
	}
	res, err := r.Resolve(ctx, d, decisions)
	if err != nil {
		fmt.Fprintf(stderr, "timesheet: resolve %s: %v\n", d, err)
		return exitError
	}
	fmt.Fprintf(stdout, "%s: resolved %d/%d decision(s)\n", d, res.Applied, res.Decisions)
	if len(res.Unmatched) > 0 {
		fmt.Fprintf(stderr, "timesheet: %d decision(s) matched no unresolved operation: %s\n",
			len(res.Unmatched), strings.Join(res.Unmatched, ", "))
		return exitError
	}
	return exitOK
}

func cmdRun(ctx context.Context, r *run.Runner, cfg cliConfig, editorActive, deep bool, decisionsPath, ack, date, from, to string, stdout, stderr io.Writer) int {
	pre, err := r.Preflight(ctx, run.PreflightOptions{Deep: deep})
	if err != nil {
		fmt.Fprintln(stderr, "timesheet: preflight:", err)
		return exitError
	}
	if !pre.OK {
		fmt.Fprintln(stderr, "timesheet: preflight refused")
		return exitBlocked
	}
	if code := cmdReview(ctx, r, cfg, editorActive, decisionsPath, ack, date, from, to, stdout, stderr); code != exitOK {
		return code
	}
	return cmdExport(ctx, r, date, from, to, stdout, stderr)
}

// --- helpers ----------------------------------------------------------------

func singleDate(r *run.Runner, date string) (string, error) {
	if d := strings.TrimSpace(date); d != "" {
		return d, nil
	}
	return r.Date()
}

func substituteDate(path, date string) string {
	return strings.ReplaceAll(path, "{date}", date)
}

func loadDecisions(path, ack string) (run.ReviewDecisions, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return run.ReviewDecisions{}, err
	}
	var d run.ReviewDecisions
	if err := json.Unmarshal(data, &d); err != nil {
		return run.ReviewDecisions{}, err
	}
	if a := strings.TrimSpace(ack); a != "" {
		d.Acknowledge = append(d.Acknowledge, splitCSV(a)...)
	}
	return d, nil
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ciDetected reports whether a CI environment is present. A TUI is never started
// under CI even if a pseudo-terminal exists.
func ciDetected() bool {
	for _, k := range []string{"CI", "GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "CIRCLECI", "TEAMCITY_VERSION", "JENKINS_URL"} {
		if strings.TrimSpace(os.Getenv(k)) != "" {
			return true
		}
	}
	return false
}

func printProposal(w io.Writer, date string, prop proposal.Proposal) {
	fmt.Fprintf(w, "%s: proposal (reviewed=%d blocks, excluded=%d)\n", date, proposalBlockCount(prop), len(prop.Excluded))
	for _, b := range prop.Work {
		fmt.Fprintf(w, "  work    %s %s-%s %s\n", b.ID, b.Start, b.End, b.Description)
	}
	for _, b := range prop.Meetings {
		fmt.Fprintf(w, "  meeting %s %s-%s %s\n", b.ID, b.Start, b.End, b.Description)
	}
	for _, b := range prop.Manual {
		fmt.Fprintf(w, "  manual  %s %s-%s %s\n", b.ID, b.Start, b.End, b.Description)
	}
	// Narrative journal candidates are UNTIMED: print provenance and an explicit
	// unknown-duration marker rather than any fabricated time.
	for _, n := range prop.Narratives {
		fmt.Fprintf(w, "  narrative %s section=%s %s duration=unknown\n", n.ID, n.Section, n.Description)
	}
	// Excluded evidence is advisory history. A detached journal item is printed
	// with its human-readable provenance (title/path/heading/line/date) so the
	// uncertainty is identifiable, not an opaque id or a bare count.
	for _, ex := range prop.Excluded {
		line := fmt.Sprintf("  excluded %s reason=%s", ex.EvidenceID, ex.Reason)
		if ex.Kind != "" {
			line += " kind=" + string(ex.Kind)
		}
		if ex.NotePath != "" {
			line += " note=" + oneLine(ex.NotePath)
		}
		if ex.HeadingPath != "" {
			line += " heading=" + oneLine(ex.HeadingPath)
		}
		if ex.LineStart > 0 {
			line += fmt.Sprintf(" lines=%d-%d", ex.LineStart, ex.LineEnd)
		}
		if ex.StatedDate != "" {
			line += " date=" + ex.StatedDate
		}
		if ex.Title != "" {
			line += " title=" + oneLine(ex.Title)
		}
		fmt.Fprintln(w, line)
	}
}

func proposalBlockCount(p proposal.Proposal) int {
	return len(p.Work) + len(p.Meetings) + len(p.Manual)
}
