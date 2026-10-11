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
	view, err := selectCatalogs(ctx, cfg, *useCache, *token)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet: classify select:", err)
		return exitError
	}

	// ALWAYS disclose the provenance and freshness of EVERY catalog used. A --cache
	// catalog that is STALE is refused unless --force is given (mirroring the
	// submission gate's 24h rule), so a selection is never applied from stale data
	// silently. A partial catalog is labelled so it is never presented as the whole
	// catalog.
	now := time.Now().UTC()
	for _, c := range view.used {
		fmt.Fprintf(stdout, "provenance: %s\n", selectionProvenanceLine(c, now))
	}
	if view.targetedErr != nil {
		fmt.Fprintf(stderr, "timesheet: warning: a targeted catalog fetch for %q failed (%v); resolving against the unfiltered catalog only\n", *token, view.targetedErr)
	}
	if *useCache && !*force {
		// Deliberately CONSERVATIVE: a selection is refused when ANY used cached
		// catalog is stale, not just the first one encountered. Mixing a fresh
		// catalog with a stale one could resolve a token from data the 24h policy
		// would otherwise reject, so we fail closed and require --force.
		for _, c := range view.used {
			provLine, fresh := selection.Freshness(c, now)
			if !fresh {
				fmt.Fprintf(stderr, "timesheet: refusing a STALE cached catalog (%s); pass --force to use it, or run `timesheet classify list` (live)\n", provLine)
				return exitBlocked
			}
		}
	}

	opt, err := selection.ResolveToken(view.union, *token)
	if err != nil {
		if errors.Is(err, selection.ErrAmbiguousToken) {
			fmt.Fprintf(stderr, "timesheet: classification %q is ambiguous in the catalog; choose an exact id\n", *token)
		} else {
			fmt.Fprintln(stderr, "timesheet: "+selectionNotFoundMessage(*token, *useCache, view.haveTargeted, view.union))
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
			r.Confirmed[i].ClassificationCode = opt.Code
			r.Confirmed[i].ClassificationName = opt.Name
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

// selectCatalogView is the resolution view used by `classify select`: the UNION of
// the unfiltered catalog and a TARGETED catalog for the token, plus the individual
// catalogs actually used (for provenance disclosure) and whether a targeted
// catalog was obtained. A targeted catalog lies outside the unfiltered 20-row
// page, so any classification beyond that page stays selectable.
type selectCatalogView struct {
	union        gradion.Catalog
	used         []gradion.Catalog
	haveTargeted bool
	targetedErr  error
}

// selectCatalogs builds the selection view. Live (default): fetch the unfiltered
// catalog AND a targeted catalog for the token, unioning by id (targeted wins);
// each is cached under its own query. A targeted fetch failure is non-fatal (the
// unfiltered catalog may still resolve the token) and is reported via targetedErr.
// --cache: load the "" entry AND the search=<token> entry and union them; a
// missing targeted entry is NOT an error (only the union's coverage is reduced).
func selectCatalogs(ctx context.Context, cfg cliConfig, useCache bool, token string) (selectCatalogView, error) {
	if useCache {
		cachePath, _ := selection.DefaultCatalogCachePath()
		base, baseErr := selection.LoadCatalogCache(cachePath, "")
		targeted, targetedErr := selection.LoadCatalogCache(cachePath, token)
		haveBase := baseErr == nil
		haveTargeted := targetedErr == nil
		if !haveBase && !haveTargeted {
			return selectCatalogView{}, errors.New("no cached catalog: run `timesheet classify list` (live) first")
		}
		switch {
		case haveBase && haveTargeted:
			return selectCatalogView{
				union:        selection.UnionCatalogs(base, targeted),
				used:         []gradion.Catalog{base, targeted},
				haveTargeted: true,
			}, nil
		case haveBase:
			return selectCatalogView{union: base, used: []gradion.Catalog{base}}, nil
		default:
			return selectCatalogView{union: targeted, used: []gradion.Catalog{targeted}, haveTargeted: true}, nil
		}
	}

	base, err := fetchCatalog(ctx, cfg, false, "")
	if err != nil {
		return selectCatalogView{}, err
	}
	targeted, terr := fetchCatalog(ctx, cfg, false, token)
	if terr != nil {
		return selectCatalogView{union: base, used: []gradion.Catalog{base}, targetedErr: terr}, nil
	}
	return selectCatalogView{
		union:        selection.UnionCatalogs(base, targeted),
		used:         []gradion.Catalog{base, targeted},
		haveTargeted: true,
	}, nil
}

// selectionProvenanceLine discloses a catalog's provenance and freshness for a
// selection, marking it PARTIAL when it cannot prove absence (filtered query or a
// truncated/capped result). It never presents a partial catalog as the whole one.
func selectionProvenanceLine(cat gradion.Catalog, now time.Time) string {
	prov, _ := selection.Freshness(cat, now)
	if !cat.IsFullCatalog() {
		prov += " (partial; get_classifications caps results, so rows beyond the first page may be omitted)"
	}
	return prov
}

// selectionNotFoundMessage explains an unresolvable selection WITHOUT asserting
// absence from the catalog. It keys the "may be beyond the page" caveat on
// IsFullCatalog, not Complete: the --cache targeted-only branch resolves against a
// FILTERED catalog (Query != "") that may still report Complete, so Complete alone
// would wrongly assert a proven absence. A filtered or partial union says the
// classification may lie beyond the first 20 rows and to refine with
// `classify list --search`; when no targeted catalog was obtained it tells the
// user to run that search LIVE first. A full (unfiltered AND complete) catalog
// still reports a genuine absence.
func selectionNotFoundMessage(token string, useCache, haveTargeted bool, union gradion.Catalog) string {
	msg := fmt.Sprintf("classification %q was not found", token)
	if !union.IsFullCatalog() {
		msg += "; the catalog is PARTIAL (get_classifications is capped at the first 20 rows), so the classification may lie beyond that page"
	}
	if useCache && !haveTargeted {
		msg += fmt.Sprintf(" — run `timesheet classify list --search %s` (live) first to fetch a targeted catalog, then re-select", token)
	} else {
		msg += fmt.Sprintf(" — refine with `timesheet classify list --search %s`", token)
	}
	return msg
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
