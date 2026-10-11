package selection

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
)

func sampleCatalog() gradion.Catalog {
	return gradion.Catalog{
		Complete:   true,
		Source:     "live",
		ObservedAt: time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC),
		Options: []gradion.ClassificationOption{
			{ID: "uuid-1", Name: "Gradion Intern Academy 2026", Code: "INTGRADI2610", ActivityType: "internal project"},
			{ID: "uuid-2", Name: "Client Alpha", Code: "ALPHA-1", ActivityType: "client project"},
		},
	}
}

func TestPrefs_RecordDedupesAndCaps(t *testing.T) {
	var p Prefs
	for i := 0; i < MaxRecent+5; i++ {
		p.Record(RecentSelection{ClassificationID: "c", SelectedAt: time.Unix(int64(i), 0)})
	}
	if len(p.Recent) != 1 {
		t.Fatalf("a repeated id must dedupe, got %d", len(p.Recent))
	}
	for i := 0; i < MaxRecent+5; i++ {
		p.Record(RecentSelection{ClassificationID: string(rune('a' + i)), SelectedAt: time.Unix(int64(i), 0)})
	}
	if len(p.Recent) != MaxRecent {
		t.Fatalf("recent list = %d, want capped at %d", len(p.Recent), MaxRecent)
	}
	most, ok := p.MostRecent()
	if !ok || most.ClassificationID == "" {
		t.Fatalf("want a most-recent entry, got %+v ok=%v", most, ok)
	}
}

func TestPrefs_LoadSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs", "preferences.json")
	if _, err := Load(path); err != nil {
		t.Fatalf("a missing file must load as empty, got %v", err)
	}
	var p Prefs
	p.Record(RecentSelection{ClassificationID: "uuid-1", Code: "INTGRADI2610", SelectedAt: time.Unix(1, 0)})
	if err := Save(path, p); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if m, ok := got.MostRecent(); !ok || m.ClassificationID != "uuid-1" {
		t.Fatalf("round trip lost the selection: %+v", got)
	}
}

func TestResolveToken_ExactIDThenCode(t *testing.T) {
	cat := sampleCatalog()
	if o, err := ResolveToken(cat, "uuid-2"); err != nil || o.ID != "uuid-2" {
		t.Fatalf("id match: %+v err=%v", o, err)
	}
	if o, err := ResolveToken(cat, "intgradi2610"); err != nil || o.ID != "uuid-1" {
		t.Fatalf("code match (case-insensitive): %+v err=%v", o, err)
	}
	if _, err := ResolveToken(cat, "nope"); !errors.Is(err, ErrUnknownToken) {
		t.Fatalf("unknown token: %v", err)
	}
}

func TestResolveToken_AmbiguousCodeRefused(t *testing.T) {
	cat := gradion.Catalog{Options: []gradion.ClassificationOption{
		{ID: "a", Code: "DUP"}, {ID: "b", Code: "DUP"},
	}}
	if _, err := ResolveToken(cat, "DUP"); !errors.Is(err, ErrAmbiguousToken) {
		t.Fatalf("ambiguous code: %v", err)
	}
}

// F5: an UNAMBIGUOUS id-prefix (what `classify list` prints) is accepted.
func TestResolveToken_UnambiguousIDPrefix(t *testing.T) {
	cat := gradion.Catalog{Options: []gradion.ClassificationOption{
		{ID: "abcdef01-1111", Name: "A", Code: "CA", ActivityType: "internal project"},
		{ID: "12345678-2222", Name: "B", Code: "CB", ActivityType: "client project"},
	}}
	for token, want := range map[string]string{"abcdef01": "abcdef01-1111", "1234": "12345678-2222", "abc": "abcdef01-1111"} {
		got, err := ResolveToken(cat, token)
		if err != nil {
			t.Fatalf("prefix %q: %v", token, err)
		}
		if got.ID != want {
			t.Errorf("prefix %q resolved to %q, want %q", token, got.ID, want)
		}
	}
}

// F5: an AMBIGUOUS id-prefix is refused (never synthesized).
func TestResolveToken_AmbiguousIDPrefixRefused(t *testing.T) {
	cat := gradion.Catalog{Options: []gradion.ClassificationOption{
		{ID: "uuid-1", Code: "C1"}, {ID: "uuid-2", Code: "C2"},
	}}
	if _, err := ResolveToken(cat, "uuid"); !errors.Is(err, ErrAmbiguousToken) {
		t.Fatalf("ambiguous id-prefix: %v", err)
	}
}

func TestFirstRunSuggestion_Match(t *testing.T) {
	opt, why, ok := FirstRunSuggestion(sampleCatalog())
	if !ok {
		t.Fatalf("expected a match, got why=%q", why)
	}
	if opt.ID != "uuid-1" || opt.Code != FirstRunCode {
		t.Errorf("unexpected option: %+v", opt)
	}
}

func TestFirstRunSuggestion_AbsentExplainsAndAsks(t *testing.T) {
	cat := gradion.Catalog{Options: []gradion.ClassificationOption{{ID: "x", Code: "OTHER", ActivityType: "internal project"}}}
	_, why, ok := FirstRunSuggestion(cat)
	if ok || why == "" {
		t.Fatalf("an absent suggestion must explain and ask for another selection, ok=%v why=%q", ok, why)
	}
}

func TestFirstRunSuggestion_AmbiguousExplains(t *testing.T) {
	cat := gradion.Catalog{Options: []gradion.ClassificationOption{
		{ID: "a", Code: FirstRunCode, ActivityType: FirstRunActivityType},
		{ID: "b", Code: FirstRunCode, ActivityType: FirstRunActivityType},
	}}
	_, why, ok := FirstRunSuggestion(cat)
	if ok || why == "" {
		t.Fatalf("an ambiguous suggestion must explain, ok=%v why=%q", ok, why)
	}
}

func TestFirstRunSuggestion_IncompatibleActivityTypeRejected(t *testing.T) {
	cat := gradion.Catalog{Options: []gradion.ClassificationOption{
		{ID: "a", Code: FirstRunCode, ActivityType: "client project"},
	}}
	_, why, ok := FirstRunSuggestion(cat)
	if ok || why == "" {
		t.Fatalf("an incompatible activity type must be rejected, ok=%v why=%q", ok, why)
	}
}

// SELECT-TARGETED: a PARTIAL catalog must not assert the suggestion is "not
// present" — the suggestion may lie beyond the returned page. The message must
// qualify (mention the partial page) instead.
func TestFirstRunSuggestion_PartialDoesNotClaimAbsent(t *testing.T) {
	partial := gradion.Catalog{
		Complete: false,
		Query:    "intern",
		Options:  []gradion.ClassificationOption{{ID: "x", Code: "OTHER", ActivityType: "internal project"}},
	}
	_, why, ok := FirstRunSuggestion(partial)
	if ok {
		t.Fatalf("a catalog without the suggestion must not match: %+v", why)
	}
	if strings.Contains(strings.ToLower(why), "not present") {
		t.Fatalf("a PARTIAL catalog must not claim the suggestion is not present: %q", why)
	}
	if !strings.Contains(why, "PARTIAL") {
		t.Fatalf("the partial explanation must say the catalog is PARTIAL: %q", why)
	}
}

// SELECT-TARGETED: a COMPLETE unfiltered catalog with the suggestion absent may
// still say "not present" (absence is proven there).
func TestFirstRunSuggestion_CompleteAbsentSaysNotPresent(t *testing.T) {
	complete := gradion.Catalog{
		Complete: true,
		Options:  []gradion.ClassificationOption{{ID: "x", Code: "OTHER", ActivityType: "internal project"}},
	}
	_, why, ok := FirstRunSuggestion(complete)
	if ok {
		t.Fatalf("a catalog without the suggestion must not match")
	}
	if !strings.Contains(strings.ToLower(why), "not present") {
		t.Fatalf("a COMPLETE catalog may assert the suggestion is not present: %q", why)
	}
}

// SELECT-TARGETED: the union lets a token present ONLY in the targeted catalog
// resolve, without double-counting and without mislabeling completeness.
func TestUnionCatalogs_TargetedWinsAndNoDoubleCount(t *testing.T) {
	base := gradion.Catalog{
		Complete:   false,
		Source:     "live",
		ObservedAt: time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC),
		Options: []gradion.ClassificationOption{
			{ID: "x", Name: "Base X", Code: "X1", ActivityType: "internal project"},
			{ID: "y", Name: "Base Y", Code: "Y1"},
		},
	}
	targeted := gradion.Catalog{
		Complete:   true,
		Query:      "TARGET",
		Source:     "live",
		ObservedAt: time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC),
		Options: []gradion.ClassificationOption{
			{ID: "x", Name: "Targeted X", Code: "TX", ActivityType: "internal project"},
			{ID: "z", Name: "Targeted Z", Code: "TZ"},
		},
	}
	got := UnionCatalogs(base, targeted)
	if len(got.Options) != 3 {
		t.Fatalf("union = %d options, want 3 (no double-count): %+v", len(got.Options), got.Options)
	}
	// Targeted wins on the shared id, and base order is preserved for the rest.
	if got.Options[0].ID != "x" || got.Options[0].Name != "Targeted X" || got.Options[0].Code != "TX" {
		t.Errorf("targeted option must win on id conflict, got %+v", got.Options[0])
	}
	if got.Options[1].ID != "y" || got.Options[2].ID != "z" {
		t.Errorf("union order must be base then targeted-only, got %+v", got.Options)
	}
	counts := map[string]int{}
	for _, o := range got.Options {
		counts[o.ID]++
	}
	for id, n := range counts {
		if n != 1 {
			t.Errorf("id %q appears %d times; the union must not double-count", id, n)
		}
	}
	// A partial input must keep the union partial (never a false absence proof).
	if got.Complete {
		t.Errorf("a union containing a PARTIAL catalog must not be complete")
	}
	if got.Query != "" {
		t.Errorf("the union is unfiltered by construction, got query %q", got.Query)
	}
	// A token present only in the targeted catalog must now resolve.
	if o, err := ResolveToken(got, "TZ"); err != nil || o.ID != "z" {
		t.Errorf("a targeted-only token must resolve: %+v err=%v", o, err)
	}
}

// SELECT-TARGETED: the union of two COMPLETE catalogs is complete.
func TestUnionCatalogs_CompleteOnlyWhenBothComplete(t *testing.T) {
	if got := UnionCatalogs(sampleCatalog(), sampleCatalog()); !got.Complete {
		t.Fatalf("two complete catalogs must union to a complete catalog")
	}
	partial := sampleCatalog()
	partial.Complete = false
	if got := UnionCatalogs(sampleCatalog(), partial); got.Complete {
		t.Fatalf("a partial input must keep the union partial")
	}
}

// D4: blank-ID options have no identity, so the union dedups them by content —
// an identical blank-ID row is collapsed, distinct blank-ID rows survive.
func TestUnionCatalogs_BlankIDOptionsDeduped(t *testing.T) {
	blank := gradion.ClassificationOption{Name: "No Id", Code: "NI", ActivityType: "internal project"}
	base := gradion.Catalog{Complete: true, Options: []gradion.ClassificationOption{blank, blank}}
	targeted := gradion.Catalog{Complete: true, Options: []gradion.ClassificationOption{
		blank, // same content as base: must not double-count
		{Name: "Other", Code: "OT", ActivityType: "client project"}, // distinct blank-ID row
	}}
	got := UnionCatalogs(base, targeted)
	if len(got.Options) != 2 {
		t.Fatalf("blank-ID options must be deduped by content, got %d: %+v", len(got.Options), got.Options)
	}
	if got.Options[0].Code != "NI" || got.Options[1].Code != "OT" {
		t.Fatalf("unexpected union: %+v", got.Options)
	}
}

// COVERAGE: resolution stays EXACT. A substring search that returns an UNRELATED
// classification must not be silently selected; ResolveToken refuses it.
func TestResolveToken_SubstringMatchNotSelected(t *testing.T) {
	cat := gradion.Catalog{Complete: true, Options: []gradion.ClassificationOption{
		{ID: "uuid-unrelated", Name: "Gradion Intern Academy 2026 Retrospective", Code: "RETRO-2026", ActivityType: "internal project"},
		{ID: "uuid-other", Name: "Intern Onboarding", Code: "INTGRADI-OTHER", ActivityType: "internal project"},
	}}
	if _, err := ResolveToken(cat, "INTGRADI2610"); !errors.Is(err, ErrUnknownToken) {
		t.Fatalf("a substring match must not be selected; want ErrUnknownToken, got %v", err)
	}
}

func TestCatalogCache_RoundTripAndSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache", "catalog-cache.json")
	if _, err := LoadCatalogCache(path, ""); !errors.Is(err, ErrNoCache) {
		t.Fatalf("missing cache: %v", err)
	}
	if err := SaveCatalogCache(path, sampleCatalog()); err != nil {
		t.Fatalf("SaveCatalogCache: %v", err)
	}
	got, err := LoadCatalogCache(path, "")
	if err != nil {
		t.Fatalf("LoadCatalogCache: %v", err)
	}
	if got.Source != "cache" {
		t.Errorf("a loaded cache must be marked source=cache, got %q", got.Source)
	}
	if len(got.Options) != 2 || got.Options[0].Code != "INTGRADI2610" {
		t.Errorf("cache lost options: %+v", got.Options)
	}
	// A query with no entry is a cache miss, not a silent unfiltered fallback.
	if _, err := LoadCatalogCache(path, "intern"); !errors.Is(err, ErrNoCache) {
		t.Fatalf("a missing query entry must be ErrNoCache, got %v", err)
	}
}

// W2: the cache is QUERY-SCOPED. A filtered fetch must not clobber the unfiltered
// entry, and vice versa; each is retrievable by its own query.
func TestCatalogCache_QueryScopedEntriesPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache", "catalog-cache.json")

	unfiltered := sampleCatalog() // Query == ""
	filtered := sampleCatalog()
	filtered.Query = "intern"
	filtered.Options = []gradion.ClassificationOption{{ID: "uuid-1", Name: "Intern", Code: "INTGRADI2610", ActivityType: "internal project"}}

	// Filtered first, then unfiltered: the unfiltered write must PRESERVE the
	// filtered entry, and the filtered write must not have created a "" entry.
	if err := SaveCatalogCache(path, filtered); err != nil {
		t.Fatalf("save filtered: %v", err)
	}
	if _, err := LoadCatalogCache(path, ""); !errors.Is(err, ErrNoCache) {
		t.Fatalf("a filtered fetch must not populate the unfiltered entry: %v", err)
	}
	if err := SaveCatalogCache(path, unfiltered); err != nil {
		t.Fatalf("save unfiltered: %v", err)
	}

	gotUnfiltered, err := LoadCatalogCache(path, "")
	if err != nil || len(gotUnfiltered.Options) != 2 {
		t.Fatalf("the unfiltered entry must survive a later filtered fetch: %+v err=%v", gotUnfiltered, err)
	}
	gotFiltered, err := LoadCatalogCache(path, "intern")
	if err != nil || len(gotFiltered.Options) != 1 || gotFiltered.Options[0].ID != "uuid-1" {
		t.Fatalf("the filtered entry must be preserved: %+v err=%v", gotFiltered, err)
	}
	if gotFiltered.Query != "intern" {
		t.Errorf("the filtered entry must retain its query, got %q", gotFiltered.Query)
	}
}

// W2: a filtered fetch overwriting the cache must not clobber an EXISTING
// unfiltered entry (the exact bug this fixes).
func TestCatalogCache_FilteredFetchDoesNotClobberUnfiltered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache", "catalog-cache.json")
	if err := SaveCatalogCache(path, sampleCatalog()); err != nil {
		t.Fatalf("save unfiltered: %v", err)
	}
	filtered := sampleCatalog()
	filtered.Query = "alpha"
	filtered.Options = []gradion.ClassificationOption{{ID: "uuid-2", Code: "ALPHA-1"}}
	if err := SaveCatalogCache(path, filtered); err != nil {
		t.Fatalf("save filtered: %v", err)
	}
	got, err := LoadCatalogCache(path, "")
	if err != nil {
		t.Fatalf("the unfiltered entry was clobbered: %v", err)
	}
	if len(got.Options) != 2 {
		t.Fatalf("the unfiltered entry must be intact, got %+v", got.Options)
	}
}

// W2: a LEGACY single-Catalog file loads as the unfiltered ("") entry, and is
// migrated on the next save rather than silently discarded.
func TestCatalogCache_LegacySingleCatalogFileLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache", "catalog-cache.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy, _ := json.Marshal(sampleCatalog())
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	got, err := LoadCatalogCache(path, "")
	if err != nil || len(got.Options) != 2 {
		t.Fatalf("a legacy file must load as the unfiltered entry: %+v err=%v", got, err)
	}
	if _, err := LoadCatalogCache(path, "intern"); !errors.Is(err, ErrNoCache) {
		t.Fatalf("a legacy file has no filtered entry: %v", err)
	}
	// A subsequent filtered save migrates the legacy entry under "".
	filtered := sampleCatalog()
	filtered.Query = "intern"
	if err := SaveCatalogCache(path, filtered); err != nil {
		t.Fatalf("save after legacy: %v", err)
	}
	if got, err := LoadCatalogCache(path, ""); err != nil || len(got.Options) != 2 {
		t.Fatalf("the legacy entry must be migrated to the unfiltered key, got %+v err=%v", got, err)
	}
}

// W2: LoadCatalogCacheAny returns the newest entry across queries.
func TestCatalogCache_AnyReturnsNewest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache", "catalog-cache.json")
	old := sampleCatalog()
	old.ObservedAt = time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	newer := sampleCatalog()
	newer.Query = "intern"
	newer.ObservedAt = time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	newer.Options = []gradion.ClassificationOption{{ID: "uuid-9"}}
	if err := SaveCatalogCache(path, old); err != nil {
		t.Fatalf("save old: %v", err)
	}
	if err := SaveCatalogCache(path, newer); err != nil {
		t.Fatalf("save newer: %v", err)
	}
	got, err := LoadCatalogCacheAny(path)
	if err != nil {
		t.Fatalf("LoadCatalogCacheAny: %v", err)
	}
	if got.Query != "intern" || got.Options[0].ID != "uuid-9" || got.Source != "cache" {
		t.Fatalf("want the newest filtered entry, got %+v", got)
	}
	if _, err := LoadCatalogCacheAny(filepath.Join(t.TempDir(), "absent.json")); !errors.Is(err, ErrNoCache) {
		t.Fatalf("an absent cache must be ErrNoCache, got %v", err)
	}
}

func TestFreshness_StaleAndFresh(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	fresh := gradion.Catalog{Source: "cache", ObservedAt: now.Add(-1 * time.Hour)}
	if _, ok := Freshness(fresh, now); !ok {
		t.Error("a 1h-old cache must be fresh")
	}
	stale := gradion.Catalog{Source: "cache", ObservedAt: now.Add(-48 * time.Hour)}
	if _, ok := Freshness(stale, now); ok {
		t.Error("a 48h-old cache must be stale")
	}
	live := gradion.Catalog{Source: "live", ObservedAt: now.Add(-100 * time.Hour)}
	if _, ok := Freshness(live, now); !ok {
		t.Error("a live catalog is always fresh")
	}
}
