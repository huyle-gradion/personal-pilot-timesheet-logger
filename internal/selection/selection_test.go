package selection

import (
	"errors"
	"path/filepath"
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

func TestCatalogCache_RoundTripAndSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache", "catalog-cache.json")
	if _, err := LoadCatalogCache(path); !errors.Is(err, ErrNoCache) {
		t.Fatalf("missing cache: %v", err)
	}
	if err := SaveCatalogCache(path, sampleCatalog()); err != nil {
		t.Fatalf("SaveCatalogCache: %v", err)
	}
	got, err := LoadCatalogCache(path)
	if err != nil {
		t.Fatalf("LoadCatalogCache: %v", err)
	}
	if got.Source != "cache" {
		t.Errorf("a loaded cache must be marked source=cache, got %q", got.Source)
	}
	if len(got.Options) != 2 || got.Options[0].Code != "INTGRADI2610" {
		t.Errorf("cache lost options: %+v", got.Options)
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
