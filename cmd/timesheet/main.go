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

func main() { os.Exit(runCLI(context.Background(), os.Args[1:], os.Stdout, os.Stderr)) }

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
	if len(args) == 0 {
		printUsage(stderr)
		return exitUsage
	}
	sub := args[0]
	if sub == "help" || sub == "-h" || sub == "--help" {
		printUsage(stdout)
		return exitOK
	}

	fs := flag.NewFlagSet("timesheet "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		date          = fs.String("date", "", "target date YYYY-MM-DD (default: today in the configured timezone)")
		from          = fs.String("from", "", "range start YYYY-MM-DD")
		to            = fs.String("to", "", "range end YYYY-MM-DD")
		configPath    = fs.String("config", "", "path to the JSON config document")
		storageDir    = fs.String("storage-dir", "", "workflow record directory (overrides config)")
		offline       = fs.Bool("offline", false, "never contact the remote (no reads or writes)")
		deep          = fs.Bool("deep", false, "run the deeper contract verification during preflight")
		editorialOn   = fs.Bool("editorial", true, "enable the optional editorial agent when configured")
		noEditorial   = fs.Bool("no-editorial", false, "disable the editorial agent (deterministic text only)")
		decisionsPath = fs.String("decisions", "", "path to a decisions JSON file ({date} is substituted per day)")
		_             = fs.Bool("non-interactive", false, "never prompt (the CLI is always non-interactive)")
		ack           = fs.String("ack", "", "comma-separated source ids to acknowledge")
		planID        = fs.String("plan", "", "plan id to approve (approve)")
		approvalID    = fs.String("approval", "", "approval id to submit (must match the active approval)")
		vault         = fs.String("vault", "", "vault root for the managed export (overrides config)")
		dailyDir      = fs.String("daily-dir", "", "daily-note directory, relative to the vault (overrides config)")
	)
	if err := fs.Parse(args[1:]); err != nil {
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
		return cmdPreview(ctx, runner, cfg, *date, stdout, stderr)
	case "approve":
		return cmdApprove(ctx, store, runner, *date, *planID, stdout, stderr)
	case "submit":
		return cmdSubmit(ctx, runner, cfg, *date, *approvalID, stdout, stderr)
	case "resolve":
		return cmdResolve(ctx, runner, *date, *decisionsPath, stdout, stderr)
	case "run":
		return cmdRun(ctx, runner, cfg, editorActive, *deep, *decisionsPath, *ack, *date, *from, *to, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "timesheet: unknown subcommand %q\n", sub)
		printUsage(stderr)
		return exitUsage
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `timesheet <subcommand> [flags]

Subcommands:
  preflight   validate timezone, credentials, source contracts, and MCP capabilities
  review      collect + propose (+ editorial) and apply --decisions, or print the proposal
  export      write the managed daily-note section (never contacts the remote)
  preview     build and persist the submission plan (zero remote writes)
  approve     bind an approval to a plan: approve --plan <PLANID>
  submit      execute the record's ACTIVE approval: submit --date D [--approval <APPROVALID>]
  resolve     reconcile unresolved operations from --decisions
  run         preflight + collect + review + export convenience

Flags:
  --date YYYY-MM-DD        target date (default: today in the configured timezone)
  --from/--to YYYY-MM-DD   iterate days with per-day review/approval
  --config FILE            JSON config document
  --storage-dir DIR        workflow record directory (default: ~/.timesheet/records)
  --offline                never contact the remote
  --deep                   deeper contract verification during preflight
  --editorial/--no-editorial  toggle the optional editorial agent
  --decisions FILE         decisions JSON ({date} substituted per day)
  --non-interactive        never prompt (the CLI is always non-interactive)
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
	// The git credential is resolved LAZILY: only when a git source is enabled. A
	// read-only subcommand (e.g. export) must never abort on a Keychain error.
	gitToken := ""
	if run.GitSourceEnabled(cfg.Evidence.Sources) {
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
	reg.Register(plugins.NewGitPlugin(
		plugins.GitHubClientAdapter{Client: git.NewGitHubClient(gitToken, githubTimeout)},
		plugins.LocalGitCollectorAdapter{Collector: git.NewLocalGitCollector(".")},
	))
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
		if len(cfg.Evidence.Sources) > 0 {
			if _, err := r.Collect(ctx, d); err != nil {
				fmt.Fprintf(stderr, "timesheet: collect %s: %v\n", d, err)
				return exitError
			}
		}
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

func cmdPreview(ctx context.Context, r *run.Runner, cfg cliConfig, date string, stdout, stderr io.Writer) int {
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
		fmt.Fprintf(stdout, "preview blocked: %s\n", strings.Join(res.BlockReasons, ", "))
		return exitBlocked
	}
	fmt.Fprintf(stdout, "plan_id: %s\n", res.Plan.PlanID)
	fmt.Fprintf(stdout, "operations: %d\n", len(res.Plan.Operations))
	return exitOK
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

func cmdSubmit(ctx context.Context, r *run.Runner, cfg cliConfig, date, approvalID string, stdout, stderr io.Writer) int {
	d, err := singleDate(r, date)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet:", err)
		return exitError
	}
	res, err := r.Submit(ctx, d, run.SubmitOptions{ApprovalID: approvalID, Resolver: classificationResolver(cfg)})
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
}

func proposalBlockCount(p proposal.Proposal) int {
	return len(p.Work) + len(p.Meetings) + len(p.Manual)
}
