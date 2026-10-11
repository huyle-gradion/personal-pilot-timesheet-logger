package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/selection"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// catalogFetcher is the read side used by the classify commands. *gradion.Client
// satisfies it.
type catalogFetcher interface {
	ClassificationCatalog(ctx context.Context, query string) (gradion.Catalog, error)
}

// cmdClassify dispatches `classify list` / `classify select`.
func cmdClassify(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "timesheet: classify requires a subcommand: list | select")
		return exitUsage
	}
	switch args[0] {
	case "list":
		return cmdClassifyList(ctx, args[1:], stdout, stderr)
	case "select":
		return cmdClassifySelect(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "timesheet: unknown classify subcommand %q\n", args[0])
		return exitUsage
	}
}

// cmdClassifyList fetches (live by default, or --cache) the classification
// catalog and prints it with provenance, freshness, and a completeness marker.
func cmdClassifyList(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("timesheet classify list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	search := fs.String("search", "", "substring filter for the catalog")
	asJSON := fs.Bool("json", false, "print the Catalog as JSON")
	useCache := fs.Bool("cache", false, "read the cached catalog instead of fetching live")
	configPath := fs.String("config", "", "path to the JSON config document")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet:", err)
		return exitError
	}
	cat, err := fetchCatalog(ctx, cfg, *useCache, *search)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet: classify list:", err)
		return exitError
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(cat); err != nil {
			fmt.Fprintln(stderr, "timesheet: classify list:", err)
			return exitError
		}
		return exitOK
	}
	printCatalog(stdout, cat, time.Now().UTC())
	printSelectionHint(stdout, cat)
	return exitOK
}

// cmdClassifySelect sets the day's explicit classification selection. It requires
// an EXACT unique catalog match and an explicit target (--block-id or --all).
func cmdClassifySelect(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("timesheet classify select", flag.ContinueOnError)
	fs.SetOutput(stderr)
	date := fs.String("date", "", "target date YYYY-MM-DD")
	token := fs.String("classification", "", "classification code or id (explicit user choice)")
	blockID := fs.String("block-id", "", "set a single confirmed interval")
	all := fs.Bool("all", false, "apply to EVERY confirmed interval of the day (bulk; requires this flag)")
	useCache := fs.Bool("cache", false, "read the cached catalog instead of fetching live")
	force := fs.Bool("force", false, "allow a STALE cached catalog (--cache only)")
	configPath := fs.String("config", "", "path to the JSON config document")
	storageDir := fs.String("storage-dir", "", "workflow record directory (overrides config)")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	d := strings.TrimSpace(*date)
	if d == "" {
		fmt.Fprintln(stderr, "timesheet: classify select requires --date YYYY-MM-DD")
		return exitUsage
	}
	if strings.TrimSpace(*token) == "" {
		fmt.Fprintln(stderr, "timesheet: classify select requires --classification <code|id>")
		return exitUsage
	}
	one := strings.TrimSpace(*blockID)
	if !*all && one == "" {
		fmt.Fprintln(stderr, "timesheet: classify select requires --block-id <ID> or --all")
		return exitUsage
	}
	if *all && one != "" {
		fmt.Fprintln(stderr, "timesheet: --all and --block-id are mutually exclusive")
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
	cat, err := fetchCatalog(ctx, cfg, *useCache, "")
	if err != nil {
		fmt.Fprintln(stderr, "timesheet: classify select:", err)
		return exitError
	}

	// ALWAYS disclose the catalog's provenance and freshness. A --cache catalog
	// that is STALE is refused unless --force is given (mirroring the submission
	// gate's 24h rule), so a selection is never applied from stale data silently.
	now := time.Now().UTC()
	provLine, fresh := selection.Freshness(cat, now)
	fmt.Fprintf(stdout, "provenance: %s\n", provLine)
	if *useCache && !fresh && !*force {
		fmt.Fprintf(stderr, "timesheet: refusing a STALE cached catalog (%s); pass --force to use it, or run `timesheet classify list` (live)\n", provLine)
		return exitBlocked
	}

	opt, err := selection.ResolveToken(cat, *token)
	if err != nil {
		switch {
		case errors.Is(err, selection.ErrAmbiguousToken):
			fmt.Fprintf(stderr, "timesheet: classification %q is ambiguous in the catalog; choose an exact id\n", *token)
		default:
			msg := fmt.Sprintf("timesheet: classification %q was not found in the catalog", *token)
			if !cat.Complete {
				msg += " (the catalog is PARTIAL; refine it with `timesheet classify list --search <term>`)"
			}
			fmt.Fprintln(stderr, msg)
		}
		return exitError
	}

	store, err := workflow.NewJSONFileStore(cfg.storageDir())
	if err != nil {
		fmt.Fprintln(stderr, "timesheet:", err)
		return exitError
	}
	rec, err := store.GetDay(ctx, d)
	if err != nil {
		fmt.Fprintf(stderr, "timesheet: classify select %s: %v\n", d, err)
		return exitError
	}

	targets, err := selectTargets(rec, one, *all)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet:", err)
		return exitError
	}

	// Print EXACTLY what will change BEFORE applying.
	fmt.Fprintf(stdout, "classification: %s (%s) [%s] · %s\n", opt.Name, opt.Code, opt.ActivityType, idPrefix(opt.ID))
	fmt.Fprintf(stdout, "target date: %s\n", d)
	for _, ci := range targets {
		fmt.Fprintf(stdout, "  %s: %s -> %s\n", ci.ID, displayClass(ci.ClassificationID), opt.ID)
	}

	if _, err := store.UpdateDay(ctx, d, func(r *workflow.Record) error {
		for i := range r.Confirmed {
			if !targetSet(targets)[r.Confirmed[i].ID] {
				continue
			}
			r.Confirmed[i].ClassificationID = opt.ID
			r.Confirmed[i].ActivityType = opt.ActivityType
		}
		invalidateApprovalsForDate(r, d, now)
		return nil
	}); err != nil {
		fmt.Fprintf(stderr, "timesheet: classify select %s: %v\n", d, err)
		return exitError
	}

	// Persist the recent preference (non-secret) for reuse — as a SUGGESTION that
	// still requires explicit confirmation.
	if prefsPath, perr := selection.DefaultPath(); perr == nil {
		prefs, _ := selection.Load(prefsPath)
		prefs.Record(selection.RecentSelection{
			ClassificationID: opt.ID, ActivityType: opt.ActivityType,
			Name: opt.Name, Code: opt.Code, SelectedAt: now,
		})
		if serr := selection.Save(prefsPath, prefs); serr != nil {
			fmt.Fprintf(stderr, "timesheet: warning: could not persist recent selection: %v\n", serr)
		}
	}
	fmt.Fprintf(stdout, "%s: selection applied to %d interval(s)\n", d, len(targets))
	return exitOK
}

// selectTargets resolves the intervals to change: one by id, or all confirmed.
func selectTargets(rec *workflow.Record, blockID string, all bool) ([]workflow.ConfirmedInterval, error) {
	if all {
		if len(rec.Confirmed) == 0 {
			return nil, errors.New("classify select --all: the day has no confirmed intervals")
		}
		return append([]workflow.ConfirmedInterval(nil), rec.Confirmed...), nil
	}
	for _, ci := range rec.Confirmed {
		if ci.ID == blockID {
			return []workflow.ConfirmedInterval{ci}, nil
		}
	}
	return nil, fmt.Errorf("classify select: no confirmed interval %q on the day", blockID)
}

func targetSet(targets []workflow.ConfirmedInterval) map[string]bool {
	out := make(map[string]bool, len(targets))
	for _, ci := range targets {
		out[ci.ID] = true
	}
	return out
}

// invalidateApprovalsForDate marks every not-yet-invalidated approval for the date
// invalidated. A selection change alters the plan content (LocalFingerprint/
// PlanHash), so any prior approval is stale.
func invalidateApprovalsForDate(r *workflow.Record, date string, now time.Time) {
	for i := range r.Approvals {
		if r.Approvals[i].Date != date || r.Approvals[i].InvalidatedAt != nil {
			continue
		}
		workflow.InvalidateApproval(r, r.Approvals[i].ApprovalID, "classification_selection", now)
	}
}

func displayClass(id string) string {
	if strings.TrimSpace(id) == "" {
		return "(unset)"
	}
	return id
}

func idPrefix(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// newCatalogFetcher is the injectable client factory used by the classify and
// submit catalog paths. It is a package variable so hermetic tests can drive the
// live-fetch→cache path without a network. Production uses catalogClient.
var newCatalogFetcher = catalogClient

// fetchCatalog returns the catalog: live by default (and cached under the search
// query), or the cache ENTRY for that query with --cache. Source is set to "live"
// or "cache" accordingly. The cache is QUERY-SCOPED, so a filtered fetch never
// overwrites the unfiltered ("") entry.
func fetchCatalog(ctx context.Context, cfg cliConfig, useCache bool, search string) (gradion.Catalog, error) {
	cachePath, _ := selection.DefaultCatalogCachePath()
	if useCache {
		cat, err := selection.LoadCatalogCache(cachePath, search)
		if err != nil {
			if errors.Is(err, selection.ErrNoCache) {
				return gradion.Catalog{}, errors.New("no cached catalog: run `timesheet classify list` (live) first")
			}
			return gradion.Catalog{}, err
		}
		return cat, nil
	}
	client, err := newCatalogFetcher(ctx, cfg)
	if err != nil {
		return gradion.Catalog{}, err
	}
	cat, err := client.ClassificationCatalog(ctx, search)
	if err != nil {
		return gradion.Catalog{}, err
	}
	cat.Source = "live"
	if cachePath != "" {
		_ = selection.SaveCatalogCache(cachePath, cat)
	}
	return cat, nil
}

// catalogClient builds the MCP client from non-secret config and returns its
// catalog read side. It returns an error when no endpoint is configured.
func catalogClient(ctx context.Context, cfg cliConfig) (catalogFetcher, error) {
	remote, err := buildRemote(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if remote == nil {
		return nil, errors.New("no Gradion endpoint configured (set gradion.endpoint)")
	}
	c, ok := remote.(catalogFetcher)
	if !ok {
		return nil, errors.New("configured remote does not expose a classification catalog")
	}
	return c, nil
}

// printCatalog prints one line per option plus provenance and completeness.
func printCatalog(w io.Writer, cat gradion.Catalog, now time.Time) {
	provenance, _ := selection.Freshness(cat, now)
	state := "complete"
	if !cat.Complete {
		state = "partial"
	}
	fmt.Fprintf(w, "provenance: %s\n", provenance)
	fmt.Fprintf(w, "completeness: %s\n", state)
	for _, o := range cat.Options {
		fmt.Fprintf(w, "%s (%s) [%s] · %s\n", o.Name, o.Code, o.ActivityType, idPrefix(o.ID))
	}
}

// printSelectionHint surfaces either the most-recent selection or the owner's
// first-time suggestion. It NEVER applies a selection: both require an explicit
// `timesheet classify select`.
func printSelectionHint(w io.Writer, cat gradion.Catalog) {
	prefsPath, err := selection.DefaultPath()
	if err != nil {
		return
	}
	prefs, err := selection.Load(prefsPath)
	if err == nil {
		if m, ok := prefs.MostRecent(); ok {
			fmt.Fprintf(w, "recent selection (requires explicit confirmation): %s (%s) [%s] · %s\n",
				m.Name, m.Code, m.ActivityType, idPrefix(m.ClassificationID))
			return
		}
	}
	opt, why, ok := selection.FirstRunSuggestion(cat)
	if !ok {
		fmt.Fprintf(w, "first-run suggestion unavailable: %s\n", why)
		return
	}
	fmt.Fprintf(w, "suggested first selection: %s (%s) [%s] · %s\n", opt.Name, opt.Code, opt.ActivityType, idPrefix(opt.ID))
}
