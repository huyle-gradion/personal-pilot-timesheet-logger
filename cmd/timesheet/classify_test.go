package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/selection"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// fakeCatalogFetcher is a hermetic catalogFetcher that echoes the query. When
// byQuery is set, a matching query returns that catalog (used to model an
// unfiltered page WITHOUT a target and a targeted page WITH it); otherwise the
// single catalog field is returned.
type fakeCatalogFetcher struct {
	catalog gradion.Catalog
	byQuery map[string]gradion.Catalog
	err     error
	queries []string
}

func (f *fakeCatalogFetcher) ClassificationCatalog(ctx context.Context, query string) (gradion.Catalog, error) {
	f.queries = append(f.queries, query)
	if f.err != nil {
		return gradion.Catalog{}, f.err
	}
	cat := f.catalog
	if c, ok := f.byQuery[query]; ok {
		cat = c
	}
	cat.Query = query
	cat.Source = "live"
	return cat, nil
}

// withCatalogFetcher installs a fake catalog fetcher for the test's duration.
func withCatalogFetcher(t *testing.T, f *fakeCatalogFetcher) {
	t.Helper()
	prev := newCatalogFetcher
	newCatalogFetcher = func(context.Context, cliConfig) (catalogFetcher, error) { return f, nil }
	t.Cleanup(func() { newCatalogFetcher = prev })
}

// withHome points the non-secret selection state at a temp dir so tests never
// touch the real ~/.timesheet.
func withHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(selection.EnvHome, home)
	return home
}

func seedCache(t *testing.T, cat gradion.Catalog) {
	t.Helper()
	p, err := selection.DefaultCatalogCachePath()
	if err != nil {
		t.Fatalf("cache path: %v", err)
	}
	if err := selection.SaveCatalogCache(p, cat); err != nil {
		t.Fatalf("save cache: %v", err)
	}
}

func sampleCat() gradion.Catalog {
	return gradion.Catalog{
		Complete:   true,
		Source:     "live",
		ObservedAt: time.Now().UTC(),
		Options: []gradion.ClassificationOption{
			{ID: "uuid-1", Name: "Gradion Intern Academy 2026", Code: "INTGRADI2610", ActivityType: "internal project"},
			{ID: "uuid-2", Name: "Client Alpha", Code: "ALPHA-1", ActivityType: "client project"},
		},
	}
}

// partialPage returns the server's capped UNFILTERED page: exactly
// MaxClassificationResults rows, none of which is the target, marked PARTIAL.
func partialPage() gradion.Catalog {
	cat := gradion.Catalog{Complete: false, Source: "live", ObservedAt: time.Now().UTC()}
	for i := 0; i < 20; i++ {
		cat.Options = append(cat.Options, gradion.ClassificationOption{
			ID:           fmt.Sprintf("u-%02d", i),
			Name:         fmt.Sprintf("Unrelated Row %d", i),
			Code:         fmt.Sprintf("ROW-%02d", i),
			ActivityType: "client project",
		})
	}
	return cat
}

// targetedIntern returns a TARGETED catalog that contains the out-of-page target.
func targetedIntern() gradion.Catalog {
	return gradion.Catalog{
		Complete:   true,
		Source:     "live",
		ObservedAt: time.Now().UTC(),
		Options: []gradion.ClassificationOption{
			{ID: "uuid-int", Name: "Gradion Intern Academy 2026", Code: "INTGRADI2610", ActivityType: "internal project"},
		},
	}
}

func TestClassifyList_CacheMissingErrors(t *testing.T) {
	withHome(t)
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, cliConfig{})
	code, _, stderr := runArgs(t, "classify", "list", "--config", cfgPath, "--cache")
	if code != exitError {
		t.Fatalf("want error, got %d (%s)", code, stderr)
	}
	if !strings.Contains(stderr, "no cached catalog") {
		t.Fatalf("want a clear no-cache message, got %q", stderr)
	}
}

func TestClassifyList_PrintsOptionsProvenanceAndCompleteness(t *testing.T) {
	withHome(t)
	seedCache(t, sampleCat())
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, cliConfig{})
	code, stdout, stderr := runArgs(t, "classify", "list", "--config", cfgPath, "--cache")
	if code != exitOK {
		t.Fatalf("want ok, got %d (%s)", code, stderr)
	}
	for _, want := range []string{
		"provenance: cache",
		"completeness: complete",
		"Gradion Intern Academy 2026 (INTGRADI2610) [internal project] · uuid-1",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("catalog output missing %q:\n%s", want, stdout)
		}
	}
}

func TestClassifyList_JSONIsTheCatalog(t *testing.T) {
	withHome(t)
	seedCache(t, sampleCat())
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, cliConfig{})
	code, stdout, stderr := runArgs(t, "classify", "list", "--config", cfgPath, "--cache", "--json")
	if code != exitOK {
		t.Fatalf("want ok, got %d (%s)", code, stderr)
	}
	var cat gradion.Catalog
	if err := json.Unmarshal([]byte(stdout), &cat); err != nil {
		t.Fatalf("--json output is not a Catalog: %v\n%s", err, stdout)
	}
	if len(cat.Options) != 2 || cat.Options[0].Code != "INTGRADI2610" {
		t.Errorf("json catalog = %+v", cat)
	}
}

func TestClassifyList_SurfacesRecentSelectionWithoutApplying(t *testing.T) {
	withHome(t)
	seedCache(t, sampleCat())
	p, _ := selection.DefaultPath()
	prefs := selection.Prefs{}
	prefs.Record(selection.RecentSelection{ClassificationID: "uuid-2", Code: "ALPHA-1", Name: "Client Alpha", ActivityType: "client project"})
	if err := selection.Save(p, prefs); err != nil {
		t.Fatalf("save prefs: %v", err)
	}
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, cliConfig{})
	_, stdout, _ := runArgs(t, "classify", "list", "--config", cfgPath, "--cache")
	if !strings.Contains(stdout, "recent selection (requires explicit confirmation)") {
		t.Fatalf("a recent selection must be surfaced as a confirmation hint:\n%s", stdout)
	}
}

func TestClassifyList_FirstRunSuggestionWhenNoRecent(t *testing.T) {
	withHome(t)
	seedCache(t, sampleCat())
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, cliConfig{})
	_, stdout, _ := runArgs(t, "classify", "list", "--config", cfgPath, "--cache")
	if !strings.Contains(stdout, "suggested first selection: Gradion Intern Academy 2026 (INTGRADI2610)") {
		t.Fatalf("want the owner's first-run suggestion surfaced:\n%s", stdout)
	}
	// A suggestion is NEVER auto-applied or auto-persisted.
	p, _ := selection.DefaultPath()
	prefs, err := selection.Load(p)
	if err != nil {
		t.Fatalf("load prefs: %v", err)
	}
	if _, ok := prefs.MostRecent(); ok {
		t.Fatalf("listing a suggestion must not persist or apply a selection: %+v", prefs)
	}
}

// The first-run hint is SUPPRESSED whenever a recent selection exists.
func TestClassifyList_SuppressesFirstRunHintWhenRecentExists(t *testing.T) {
	withHome(t)
	seedCache(t, sampleCat())
	p, _ := selection.DefaultPath()
	prefs := selection.Prefs{}
	prefs.Record(selection.RecentSelection{ClassificationID: "uuid-2", Code: "ALPHA-1", Name: "Client Alpha", ActivityType: "client project"})
	if err := selection.Save(p, prefs); err != nil {
		t.Fatalf("save prefs: %v", err)
	}
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, cliConfig{})
	_, stdout, _ := runArgs(t, "classify", "list", "--config", cfgPath, "--cache")
	if !strings.Contains(stdout, "recent selection (requires explicit confirmation)") {
		t.Fatalf("a recent selection must be surfaced:\n%s", stdout)
	}
	if strings.Contains(stdout, "suggested first selection") {
		t.Fatalf("the first-run hint must be suppressed when a recent exists:\n%s", stdout)
	}
}

// The recent list is widened to the maxRecentHints most recent choices, each still
// an UNAPPLIED suggestion; older entries are not surfaced and the persisted list is
// untouched.
func TestClassifyList_SurfacesUpToThreeRecentUnapplied(t *testing.T) {
	withHome(t)
	seedCache(t, sampleCat())
	p, _ := selection.DefaultPath()
	prefs := selection.Prefs{}
	// Record four; only the three most recent may be surfaced.
	for i := 0; i < 4; i++ {
		prefs.Record(selection.RecentSelection{
			ClassificationID: fmt.Sprintf("uuid-%d", i),
			Code:             fmt.Sprintf("CODE-%d", i),
			Name:             fmt.Sprintf("Recent %d", i),
			ActivityType:     "internal project",
		})
	}
	if err := selection.Save(p, prefs); err != nil {
		t.Fatalf("save prefs: %v", err)
	}
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, cliConfig{})
	_, stdout, _ := runArgs(t, "classify", "list", "--config", cfgPath, "--cache")
	// The three MOST recent (3, 2, 1) are surfaced, each a confirmation hint.
	for _, want := range []string{"Recent 3", "Recent 2", "Recent 1"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("want recent %q surfaced:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "Recent 0") {
		t.Errorf("only %d recent selections may be surfaced:\n%s", maxRecentHints, stdout)
	}
	if !strings.Contains(stdout, "recent selection (requires explicit confirmation)") {
		t.Errorf("each recent must be labelled a confirmation hint:\n%s", stdout)
	}
	// Surfacing hints must never change the persisted list.
	after, err := selection.Load(p)
	if err != nil {
		t.Fatalf("load prefs: %v", err)
	}
	if len(after.Recent) != 4 {
		t.Fatalf("listing must not change the persisted recents: %+v", after.Recent)
	}
}

func seedSelectableDay(t *testing.T, storage string, approval bool) {
	t.Helper()
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		rec.Confirmed[0].ClassificationID = ""
		rec.Confirmed[0].ActivityType = ""
		if approval {
			rec.Approvals = []workflow.Approval{{ApprovalID: "a1", PlanID: "p1", Date: testDate}}
		}
	})
}

func TestClassifySelect_BlockIDAppliesAndInvalidatesApproval(t *testing.T) {
	withHome(t)
	seedCache(t, sampleCat())
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedSelectableDay(t, storage, true)

	code, stdout, stderr := runArgs(t, "classify", "select", "--config", cfgPath,
		"--storage-dir", storage, "--date", testDate, "--classification", "INTGRADI2610",
		"--block-id", "c1", "--cache")
	if code != exitOK {
		t.Fatalf("want ok, got %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "c1: (unset) -> uuid-1") {
		t.Errorf("the change preview must be printed before applying:\n%s", stdout)
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Confirmed[0].ClassificationID != "uuid-1" || rec.Confirmed[0].ActivityType != "internal project" {
		t.Fatalf("the selection must be applied: %+v", rec.Confirmed[0])
	}
	if rec.Confirmed[0].ClassificationCode != "INTGRADI2610" || rec.Confirmed[0].ClassificationName != "Gradion Intern Academy 2026" {
		t.Fatalf("the selection must persist the retrieval metadata: %+v", rec.Confirmed[0])
	}
	if len(rec.Approvals) != 1 || rec.Approvals[0].InvalidatedAt == nil {
		t.Fatalf("a selection change must invalidate the existing approval: %+v", rec.Approvals)
	}
	// The recent preference is persisted (non-secret).
	p, _ := selection.DefaultPath()
	prefs, err := selection.Load(p)
	if err != nil {
		t.Fatalf("load prefs: %v", err)
	}
	if m, ok := prefs.MostRecent(); !ok || m.ClassificationID != "uuid-1" {
		t.Fatalf("the recent selection must be persisted: %+v", prefs)
	}
}

func TestClassifySelect_RequiresExplicitTarget(t *testing.T) {
	withHome(t)
	seedCache(t, sampleCat())
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedSelectableDay(t, storage, false)

	code, _, stderr := runArgs(t, "classify", "select", "--config", cfgPath,
		"--storage-dir", storage, "--date", testDate, "--classification", "INTGRADI2610", "--cache")
	if code != exitUsage {
		t.Fatalf("a selection without --block-id/--all must be a usage error, got %d (%s)", code, stderr)
	}
	if !strings.Contains(stderr, "--block-id") || !strings.Contains(stderr, "--all") {
		t.Fatalf("the message must explain the target requirement, got %q", stderr)
	}
}

func TestClassifySelect_AllAppliesBulk(t *testing.T) {
	withHome(t)
	seedCache(t, sampleCat())
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		rec.Confirmed = []workflow.ConfirmedInterval{
			{ID: "c1", Date: testDate, StartTime: "09:00", EndTime: "10:00", Project: "Proj", Description: "a", Attendance: true},
			{ID: "c2", Date: testDate, StartTime: "11:00", EndTime: "12:00", Project: "Proj", Description: "b", Attendance: true},
		}
		rec.Approvals = []workflow.Approval{{ApprovalID: "a1", PlanID: "p1", Date: testDate}}
	})
	code, stdout, stderr := runArgs(t, "classify", "select", "--config", cfgPath,
		"--storage-dir", storage, "--date", testDate, "--classification", "INTGRADI2610", "--all", "--cache")
	if code != exitOK {
		t.Fatalf("want ok, got %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "c1:") || !strings.Contains(stdout, "c2:") {
		t.Fatalf("--all must print every interval it will change:\n%s", stdout)
	}
	rec := loadRecord(t, storage, testDate)
	for _, ci := range rec.Confirmed {
		if ci.ClassificationID != "uuid-1" {
			t.Fatalf("--all must apply to every interval, got %+v", ci)
		}
	}
	if len(rec.Approvals) != 1 || rec.Approvals[0].InvalidatedAt == nil {
		t.Fatalf("--all must invalidate the existing approval: %+v", rec.Approvals)
	}
}

func TestClassifySelect_UnknownTokenRefused(t *testing.T) {
	withHome(t)
	seedCache(t, sampleCat())
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedSelectableDay(t, storage, false)

	code, _, stderr := runArgs(t, "classify", "select", "--config", cfgPath,
		"--storage-dir", storage, "--date", testDate, "--classification", "NOPE", "--block-id", "c1", "--cache")
	if code != exitError {
		t.Fatalf("an unknown token must be refused, got %d", code)
	}
	if !strings.Contains(stderr, "not found") {
		t.Fatalf("want a clear not-found message, got %q", stderr)
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Confirmed[0].ClassificationID != "" {
		t.Fatalf("an unknown token must not change the record: %+v", rec.Confirmed[0])
	}
}

func TestClassifySelect_AmbiguousTokenRefused(t *testing.T) {
	withHome(t)
	seedCache(t, gradion.Catalog{
		Complete: true, Source: "live", ObservedAt: time.Now().UTC(),
		Options: []gradion.ClassificationOption{
			{ID: "a", Name: "A", Code: "DUP", ActivityType: "internal project"},
			{ID: "b", Name: "B", Code: "DUP", ActivityType: "internal project"},
		},
	})
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedSelectableDay(t, storage, false)

	code, _, stderr := runArgs(t, "classify", "select", "--config", cfgPath,
		"--storage-dir", storage, "--date", testDate, "--classification", "DUP", "--block-id", "c1", "--cache")
	if code != exitError {
		t.Fatalf("an ambiguous token must be refused, got %d", code)
	}
	if !strings.Contains(stderr, "ambiguous") {
		t.Fatalf("want a clear ambiguous message, got %q", stderr)
	}
}

// F1: `classify select --cache` discloses provenance and REFUSES a STALE cache
// unless --force is given; a refusal applies NO selection.
func TestClassifySelect_StaleCacheRefusedWithoutForce(t *testing.T) {
	withHome(t)
	stale := sampleCat()
	stale.ObservedAt = time.Now().UTC().Add(-48 * time.Hour)
	seedCache(t, stale)
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedSelectableDay(t, storage, true)

	code, stdout, stderr := runArgs(t, "classify", "select", "--config", cfgPath,
		"--storage-dir", storage, "--date", testDate, "--classification", "INTGRADI2610",
		"--block-id", "c1", "--cache")
	if code != exitBlocked {
		t.Fatalf("a stale cache must be refused (exit %d), got %d", exitBlocked, code)
	}
	if !strings.Contains(stdout, "provenance: cache") {
		t.Errorf("provenance must be disclosed even on refusal:\n%s", stdout)
	}
	if !strings.Contains(stderr, "STALE") || !strings.Contains(stderr, "--force") {
		t.Errorf("the refusal must explain staleness and --force, got %q", stderr)
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Confirmed[0].ClassificationID != "" {
		t.Fatalf("a refused selection must not change the record: %+v", rec.Confirmed[0])
	}
	if len(rec.Approvals) != 1 || rec.Approvals[0].InvalidatedAt != nil {
		t.Fatalf("a refused selection must not invalidate an approval: %+v", rec.Approvals)
	}
}

// F1: --force allows a stale cache to be used.
func TestClassifySelect_StaleCacheAllowedWithForce(t *testing.T) {
	withHome(t)
	stale := sampleCat()
	stale.ObservedAt = time.Now().UTC().Add(-48 * time.Hour)
	seedCache(t, stale)
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedSelectableDay(t, storage, false)

	code, stdout, stderr := runArgs(t, "classify", "select", "--config", cfgPath,
		"--storage-dir", storage, "--date", testDate, "--classification", "INTGRADI2610",
		"--block-id", "c1", "--cache", "--force")
	if code != exitOK {
		t.Fatalf("--force must allow a stale cache, got %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "provenance: cache") || !strings.Contains(stdout, "STALE") {
		t.Errorf("a forced stale cache must still disclose staleness:\n%s", stdout)
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Confirmed[0].ClassificationID != "uuid-1" {
		t.Fatalf("--force must apply the selection, got %+v", rec.Confirmed[0])
	}
}

// F7: a PARTIAL catalog's not-found message suggests refining --search.
func TestClassifySelect_PartialCatalogNotFoundSuggestsSearch(t *testing.T) {
	withHome(t)
	partial := sampleCat()
	partial.Complete = false
	seedCache(t, partial)
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedSelectableDay(t, storage, false)

	code, _, stderr := runArgs(t, "classify", "select", "--config", cfgPath,
		"--storage-dir", storage, "--date", testDate, "--classification", "NOPE", "--block-id", "c1", "--cache")
	if code != exitError {
		t.Fatalf("an unknown token must be refused, got %d", code)
	}
	if !strings.Contains(stderr, "PARTIAL") || !strings.Contains(stderr, "--search") {
		t.Fatalf("a partial-catalog not-found must suggest refining --search, got %q", stderr)
	}
}

// SELECT-TARGETED-1: a classification that lies OUTSIDE the unfiltered 20-row
// page is still selectable, because `classify select` (live) also fetches a
// TARGETED catalog for the token and resolves against the union.
func TestClassifySelect_TargetedFetchResolvesBeyondPage(t *testing.T) {
	withHome(t)
	fetch := &fakeCatalogFetcher{
		catalog: partialPage(),
		byQuery: map[string]gradion.Catalog{"INTGRADI2610": targetedIntern()},
	}
	withCatalogFetcher(t, fetch)
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedSelectableDay(t, storage, false)

	code, stdout, stderr := runArgs(t, "classify", "select", "--config", cfgPath,
		"--storage-dir", storage, "--date", testDate, "--classification", "INTGRADI2610", "--all")
	if code != exitOK {
		t.Fatalf("an out-of-page classification must be selectable live, got %d (%s)", code, stderr)
	}
	// The unfiltered fetch (""), then the targeted fetch (the token).
	if len(fetch.queries) != 2 || fetch.queries[0] != "" || fetch.queries[1] != "INTGRADI2610" {
		t.Fatalf("want an unfiltered then targeted fetch, got %v", fetch.queries)
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Confirmed[0].ClassificationID != "uuid-int" || rec.Confirmed[0].ActivityType != "internal project" {
		t.Fatalf("the out-of-page selection must be applied: %+v", rec.Confirmed[0])
	}
	if rec.Confirmed[0].ClassificationID == "" {
		t.Fatalf("the target must be resolved from the union, not left unset")
	}
	// Provenance/freshness is disclosed for every catalog used; the partial page
	// must be labelled partial, never presented as the whole catalog.
	if strings.Count(stdout, "provenance:") < 2 {
		t.Errorf("every catalog used must have its provenance disclosed:\n%s", stdout)
	}
	if !strings.Contains(stdout, "partial") {
		t.Errorf("the capped unfiltered page must be disclosed as partial:\n%s", stdout)
	}
}

// SELECT-TARGETED-1 regression: a token already in the unfiltered page still
// selects (the union must not break the in-page path).
func TestClassifySelect_InPageCodeStillSelects(t *testing.T) {
	withHome(t)
	page := sampleCat()
	page.Options = []gradion.ClassificationOption{
		{ID: "uuid-1", Name: "Gradion Intern Academy 2026", Code: "INTGRADI2610", ActivityType: "internal project"},
	}
	fetch := &fakeCatalogFetcher{catalog: page, byQuery: map[string]gradion.Catalog{"INTGRADI2610": page}}
	withCatalogFetcher(t, fetch)
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedSelectableDay(t, storage, false)

	code, _, stderr := runArgs(t, "classify", "select", "--config", cfgPath,
		"--storage-dir", storage, "--date", testDate, "--classification", "INTGRADI2610", "--all")
	if code != exitOK {
		t.Fatalf("an in-page code must still select, got %d (%s)", code, stderr)
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Confirmed[0].ClassificationID != "uuid-1" {
		t.Fatalf("the in-page selection must be applied: %+v", rec.Confirmed[0])
	}
}

// SELECT-TARGETED-1: `classify select --cache` unions the "" entry and the
// search=<token> entry, so a classification outside the cached unfiltered page is
// still selectable offline.
func TestClassifySelect_CacheUnionResolvesTargetedEntry(t *testing.T) {
	withHome(t)
	seedCache(t, partialPage()) // "" entry: partial, WITHOUT the target
	targeted := targetedIntern()
	targeted.Query = "INTGRADI2610"
	targeted.ObservedAt = time.Now().UTC()
	seedCache(t, targeted) // search=<token> entry: WITH the target
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedSelectableDay(t, storage, false)

	code, stdout, stderr := runArgs(t, "classify", "select", "--config", cfgPath,
		"--storage-dir", storage, "--date", testDate, "--classification", "INTGRADI2610",
		"--all", "--cache")
	if code != exitOK {
		t.Fatalf("the cached union must resolve the target, got %d (%s)", code, stderr)
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Confirmed[0].ClassificationID != "uuid-int" {
		t.Fatalf("the cached targeted entry must be unioned in: %+v", rec.Confirmed[0])
	}
	if strings.Count(stdout, "provenance:") < 2 || !strings.Contains(stdout, "partial") {
		t.Errorf("both cached catalogs and the partial state must be disclosed:\n%s", stdout)
	}
}

// SELECT-TARGETED-1: with --cache and NO targeted entry, an unresolved token FAILS
// with a partial-aware message that points at `classify list --search` (live) —
// never a bare "not found" that asserts absence from the catalog.
func TestClassifySelect_CacheTargetedAbsentPartialAwareError(t *testing.T) {
	withHome(t)
	seedCache(t, partialPage()) // only the "" entry; no search=INTGRADI2610 entry
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedSelectableDay(t, storage, false)

	code, _, stderr := runArgs(t, "classify", "select", "--config", cfgPath,
		"--storage-dir", storage, "--date", testDate, "--classification", "INTGRADI2610",
		"--block-id", "c1", "--cache")
	if code != exitError {
		t.Fatalf("an unresolved token must be refused, got %d", code)
	}
	for _, want := range []string{"not found", "PARTIAL", "20", "--search", "list", "live"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("the not-found message must be partial-aware and mention %q:\n%s", want, stderr)
		}
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Confirmed[0].ClassificationID != "" {
		t.Fatalf("a refused selection must not change the record: %+v", rec.Confirmed[0])
	}
}

// D1: the --cache TARGETED-ONLY branch. The cache holds ONLY the search=<token>
// entry, which is a FILTERED catalog and may legitimately be Complete=true while
// still not containing the token. The not-found message must remain partial-aware
// (page cap / --search), never assert a proven absence.
func TestClassifySelect_CacheTargetedOnlyBranchPartialAware(t *testing.T) {
	withHome(t)
	only := gradion.Catalog{
		Complete:   true, // filtered yet complete: the exact trap D1 targets
		Source:     "live",
		ObservedAt: time.Now().UTC(),
		Options: []gradion.ClassificationOption{
			{ID: "uuid-other", Name: "Something Else", Code: "OTHER-1", ActivityType: "client project"},
		},
	}
	only.Query = "INTGRADI2610" // keyed under the token, but the token is absent
	seedCache(t, only)
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedSelectableDay(t, storage, false)

	code, _, stderr := runArgs(t, "classify", "select", "--config", cfgPath,
		"--storage-dir", storage, "--date", testDate, "--classification", "INTGRADI2610",
		"--block-id", "c1", "--cache")
	if code != exitError {
		t.Fatalf("an unresolved token must be refused, got %d", code)
	}
	for _, want := range []string{"not found", "PARTIAL", "20", "--search"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("the targeted-only not-found message must be partial-aware and mention %q:\n%s", want, stderr)
		}
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Confirmed[0].ClassificationID != "" {
		t.Fatalf("a refused selection must not change the record: %+v", rec.Confirmed[0])
	}
}

// COVERAGE: a targeted search=<token> that returns an UNRELATED classification
// (a mere substring match) must NOT be silently selected. Resolution stays exact
// and applies nothing.
func TestClassifySelect_TargetedSubstringMatchNotSilentlySelected(t *testing.T) {
	withHome(t)
	unrelated := gradion.Catalog{
		Complete:   true,
		Source:     "live",
		ObservedAt: time.Now().UTC(),
		Options: []gradion.ClassificationOption{
			{ID: "uuid-unrelated", Name: "Gradion Intern Academy 2026 Retrospective", Code: "RETRO-2026", ActivityType: "internal project"},
		},
	}
	fetch := &fakeCatalogFetcher{
		catalog: partialPage(),
		byQuery: map[string]gradion.Catalog{"INTGRADI2610": unrelated},
	}
	withCatalogFetcher(t, fetch)
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedSelectableDay(t, storage, false)

	code, _, stderr := runArgs(t, "classify", "select", "--config", cfgPath,
		"--storage-dir", storage, "--date", testDate, "--classification", "INTGRADI2610", "--all")
	if code != exitError {
		t.Fatalf("a substring match must not be silently selected, got %d (%s)", code, stderr)
	}
	if !strings.Contains(stderr, "not found") {
		t.Fatalf("the mismatch must be reported as not found, got %q", stderr)
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Confirmed[0].ClassificationID != "" || rec.Confirmed[0].ActivityType != "" {
		t.Fatalf("a substring match must apply NOTHING: %+v", rec.Confirmed[0])
	}
}

// CLI/skill parity: the skill documents the SAME selection contract the CLI
// implements (classify list then an explicit classify select).
func TestClassifySkillParity(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	// The skill's one physical copy lives in .agents; .claude/skills/timesheet is a
	// symlink to it (pinned by TestAgentsSkill_CanonicalLocationAndHarnessSymlink).
	wfBytes, err := os.ReadFile(filepath.Join(repoRoot, ".agents", "skills", "timesheet", "workflow.md"))
	if err != nil {
		t.Fatalf("read workflow.md: %v", err)
	}
	wf := string(wfBytes)
	for _, want := range []string{"classify list", "classify select", "--all"} {
		if !strings.Contains(wf, want) {
			t.Errorf("workflow.md must document %q", want)
		}
	}
	// The CLI path exists and is non-interactive: with no cache it refuses clearly.
	withHome(t)
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, cliConfig{})
	if code, _, _ := runArgs(t, "classify", "list", "--config", cfgPath, "--cache"); code != exitError {
		t.Errorf("the CLI classify list path must exist and refuse without a cache, got %d", code)
	}
}

// Submission catalog availability: an absent cache, or a STALE cache, yields no
// catalog (so submission blocks rather than defaulting); a fresh cache is used and
// its provenance is returned so the caller can disclose it.
func TestLoadSubmitCatalog_StaleOrAbsentBlocks(t *testing.T) {
	withHome(t)
	cfg := cliConfig{} // no endpoint: the live fetch is unavailable
	if cat, prov := loadSubmitCatalog(nil, cfg); cat != nil || prov != "" {
		t.Fatalf("an absent cache must yield no catalog, got %+v prov=%q", cat, prov)
	}

	// A stale cache must NOT be used.
	stale := sampleCat()
	stale.ObservedAt = time.Now().UTC().Add(-48 * time.Hour)
	seedCache(t, stale)
	if cat, _ := loadSubmitCatalog(nil, cfg); cat != nil {
		t.Fatalf("a stale cache must yield no catalog, got %+v", cat)
	}

	// A fresh cache is used, and its provenance is disclosed (F2).
	seedCache(t, sampleCat())
	cat, prov := loadSubmitCatalog(nil, cfg)
	if cat == nil || cat.Source != "cache" {
		t.Fatalf("a fresh cache must be used, got %+v", cat)
	}
	if !strings.Contains(prov, "cache") {
		t.Fatalf("the cache provenance must be disclosed, got %q", prov)
	}
}

// W2: `classify list` (live) caches under the SEARCH query: the unfiltered fetch
// under "", a filtered fetch under its query — and neither clobbers the other.
func TestClassifyList_LiveCachesUnderQuery(t *testing.T) {
	withHome(t)
	fetch := &fakeCatalogFetcher{catalog: sampleCat()}
	withCatalogFetcher(t, fetch)
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, cliConfig{})

	if code, _, stderr := runArgs(t, "classify", "list", "--config", cfgPath); code != exitOK {
		t.Fatalf("unfiltered list: %d (%s)", code, stderr)
	}
	if code, _, stderr := runArgs(t, "classify", "list", "--config", cfgPath, "--search", "intern"); code != exitOK {
		t.Fatalf("filtered list: %d (%s)", code, stderr)
	}
	if len(fetch.queries) != 2 || fetch.queries[0] != "" || fetch.queries[1] != "intern" {
		t.Fatalf("want an unfiltered then filtered fetch, got %v", fetch.queries)
	}

	p, err := selection.DefaultCatalogCachePath()
	if err != nil {
		t.Fatalf("cache path: %v", err)
	}
	unfiltered, err := selection.LoadCatalogCache(p, "")
	if err != nil || len(unfiltered.Options) != 2 {
		t.Fatalf("the unfiltered entry must be cached and intact: %+v err=%v", unfiltered, err)
	}
	filtered, err := selection.LoadCatalogCache(p, "intern")
	if err != nil || filtered.Query != "intern" {
		t.Fatalf("the filtered entry must be cached under its query: %+v err=%v", filtered, err)
	}
}

// W2: loadSubmitCatalog prefers the UNFILTERED cache entry over a newer filtered
// one, so a filtered search can never shadow the whole-catalog gate.
func TestLoadSubmitCatalog_PrefersUnfilteredEntry(t *testing.T) {
	withHome(t)
	unfiltered := sampleCat()
	unfiltered.ObservedAt = time.Now().UTC().Add(-2 * time.Hour)
	filtered := sampleCat()
	filtered.Query = "intern"
	filtered.ObservedAt = time.Now().UTC() // newer, but filtered
	filtered.Options = []gradion.ClassificationOption{{ID: "uuid-1"}}
	seedCache(t, unfiltered)
	seedCache(t, filtered)

	cat, prov := loadSubmitCatalog(context.Background(), cliConfig{})
	if cat == nil {
		t.Fatalf("want the unfiltered cache entry")
	}
	if cat.Query != "" || len(cat.Options) != 2 {
		t.Fatalf("must prefer the unfiltered entry, got %+v", cat)
	}
	if !strings.Contains(prov, "cache") {
		t.Fatalf("provenance must be disclosed, got %q", prov)
	}
}

// F5: a fresh but NON-FULL unfiltered cache entry (truncated) is labelled partial,
// never presented as a complete whole-catalog validation.
func TestLoadSubmitCatalog_PartialUnfilteredEntryLabelledPartial(t *testing.T) {
	withHome(t)
	partial := sampleCat()
	partial.Complete = false // unfiltered (Query == "") but truncated
	seedCache(t, partial)

	cat, prov := loadSubmitCatalog(context.Background(), cliConfig{})
	if cat == nil || cat.Query != "" {
		t.Fatalf("want the unfiltered partial entry, got %+v", cat)
	}
	if !strings.Contains(prov, "partial") {
		t.Fatalf("a non-full entry must be labelled partial, got %q", prov)
	}
}

// F5: a live but truncated (non-full) fetch is likewise labelled partial.
func TestLoadSubmitCatalog_LivePartialIsLabelledPartial(t *testing.T) {
	withHome(t)
	partial := sampleCat()
	partial.Complete = false
	withCatalogFetcher(t, &fakeCatalogFetcher{catalog: partial})

	cat, prov := loadSubmitCatalog(context.Background(), cliConfig{})
	if cat == nil || cat.Source != "live" {
		t.Fatalf("want a live fetch, got %+v", cat)
	}
	if !strings.Contains(prov, "partial") {
		t.Fatalf("a non-full live fetch must be labelled partial, got %q", prov)
	}
}

// W2: when only a PARTIAL (filtered) cache exists, loadSubmitCatalog returns it
// (with a partial marker) so the submit boundary can targeted-validate — a recent
// preference outside the current search is never treated as invalid.
func TestLoadSubmitCatalog_FallsBackToPartialEntry(t *testing.T) {
	withHome(t)
	filtered := sampleCat()
	filtered.Query = "intern"
	filtered.Complete = false
	seedCache(t, filtered)

	cat, prov := loadSubmitCatalog(context.Background(), cliConfig{})
	if cat == nil {
		t.Fatalf("want the partial cache entry as a fallback")
	}
	if cat.Query != "intern" {
		t.Fatalf("want the filtered entry, got %+v", cat)
	}
	if !strings.Contains(prov, "partial") {
		t.Fatalf("a partial fallback must be disclosed, got %q", prov)
	}
}
