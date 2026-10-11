package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/selection"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// fakeCatalogFetcher is a hermetic catalogFetcher that echoes the query.
type fakeCatalogFetcher struct {
	catalog gradion.Catalog
	err     error
	queries []string
}

func (f *fakeCatalogFetcher) ClassificationCatalog(ctx context.Context, query string) (gradion.Catalog, error) {
	f.queries = append(f.queries, query)
	if f.err != nil {
		return gradion.Catalog{}, f.err
	}
	cat := f.catalog
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

// CLI/skill parity: the skill documents the SAME selection contract the CLI
// implements (classify list then an explicit classify select).
func TestClassifySkillParity(t *testing.T) {
	repoRoot := filepath.Join("..", "..")
	wfBytes, err := os.ReadFile(filepath.Join(repoRoot, ".claude", "skills", "timesheet", "workflow.md"))
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
