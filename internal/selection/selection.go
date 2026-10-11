// Package selection holds non-secret user selection preferences and the
// classification catalog cache. It never stores credentials. Preferences are
// SUGGESTIONS only: nothing here auto-selects a classification — an explicit
// select is always required.
package selection

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
)

// MaxRecent bounds the persisted recent-selection list.
const MaxRecent = 20

// DefaultDir is the per-user directory for preferences and the catalog cache.
const DefaultDir = ".timesheet"

// EnvHome overrides the base directory (used by tests so they never touch the real
// ~/.timesheet). The production default remains ~/.timesheet.
const EnvHome = "TIMESHEET_HOME"

// baseDir returns the base directory for non-secret selection state.
func baseDir() (string, error) {
	if v := strings.TrimSpace(os.Getenv(EnvHome)); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("selection: user home: %w", err)
	}
	return filepath.Join(home, DefaultDir), nil
}

// RecentSelection is one previously chosen classification. It carries no secret.
type RecentSelection struct {
	ClassificationID string    `json:"classification_id"`
	ActivityType     string    `json:"activity_type,omitempty"`
	Name             string    `json:"name,omitempty"`
	Code             string    `json:"code,omitempty"`
	SelectedAt       time.Time `json:"selected_at"`
}

// Prefs is the persisted, non-secret selection preference document.
type Prefs struct {
	Recent []RecentSelection `json:"recent"`
}

// DefaultPath returns ~/.timesheet/preferences.json.
func DefaultPath() (string, error) {
	dir, err := baseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "preferences.json"), nil
}

// Load reads preferences from path. A missing file yields empty preferences and a
// nil error; a corrupt file is an error (never silently discarded).
func Load(path string) (Prefs, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Prefs{}, nil
		}
		return Prefs{}, fmt.Errorf("selection: read preferences: %w", err)
	}
	var p Prefs
	if err := json.Unmarshal(data, &p); err != nil {
		return Prefs{}, fmt.Errorf("selection: parse preferences: %w", err)
	}
	return p, nil
}

// Save writes preferences to path (0600), creating the parent directory (0700).
func Save(path string, p Prefs) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("selection: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("selection: marshal preferences: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("selection: write preferences: %w", err)
	}
	return nil
}

// Record prepends sel, de-duplicating by ClassificationID and capping the list at
// MaxRecent.
func (p *Prefs) Record(sel RecentSelection) {
	if p == nil || strings.TrimSpace(sel.ClassificationID) == "" {
		return
	}
	out := make([]RecentSelection, 0, len(p.Recent)+1)
	out = append(out, sel)
	for _, r := range p.Recent {
		if r.ClassificationID == sel.ClassificationID {
			continue
		}
		out = append(out, r)
	}
	if len(out) > MaxRecent {
		out = out[:MaxRecent]
	}
	p.Recent = out
}

// MostRecent returns the most recently recorded selection.
func (p Prefs) MostRecent() (RecentSelection, bool) {
	if len(p.Recent) == 0 {
		return RecentSelection{}, false
	}
	return p.Recent[0], true
}

// --- first-run suggestion (NEVER auto-applied) -------------------------------

const (
	// FirstRunActivityType / FirstRunCode / FirstRunName are the owner-confirmed
	// first-time suggestion. They are matched against the ACTUAL fetched catalog;
	// no id is ever synthesized and a near label is never silently chosen.
	FirstRunActivityType = "internal project"
	FirstRunCode         = "INTGRADI2610"
	FirstRunName         = "Gradion Intern Academy 2026"
)

// FirstRunSuggestion resolves the owner's first-time suggestion against the
// fetched catalog. It returns (option, "", true) on an exact, compatible match.
// Otherwise it returns a human explanation asking the user to choose another
// selection (never a synthesized id or a similar label).
func FirstRunSuggestion(cat gradion.Catalog) (gradion.ClassificationOption, string, bool) {
	var matches []gradion.ClassificationOption
	for _, o := range cat.Options {
		if strings.EqualFold(strings.TrimSpace(o.Code), FirstRunCode) {
			matches = append(matches, o)
		}
	}
	switch len(matches) {
	case 0:
		// A PARTIAL (filtered or truncated) catalog can never prove absence: the
		// suggestion may simply lie beyond the first page. Qualify the message
		// rather than asserting the classification is "not present".
		if !cat.IsFullCatalog() {
			return gradion.ClassificationOption{}, fmt.Sprintf(
				"the catalog is PARTIAL, so the suggested classification %s (%s) may lie beyond the returned page; refine with a search and choose a selection explicitly",
				FirstRunCode, FirstRunName), false
		}
		return gradion.ClassificationOption{}, fmt.Sprintf(
			"suggested classification %s (%s) is not present in the fetched catalog; choose another selection",
			FirstRunCode, FirstRunName), false
	case 1:
		opt := matches[0]
		if opt.ActivityType != "" && !strings.EqualFold(strings.TrimSpace(opt.ActivityType), FirstRunActivityType) {
			return gradion.ClassificationOption{}, fmt.Sprintf(
				"catalog classification %s has activity type %q, not %q; choose another selection",
				FirstRunCode, opt.ActivityType, FirstRunActivityType), false
		}
		return opt, "", true
	default:
		return gradion.ClassificationOption{}, fmt.Sprintf(
			"catalog has %d classifications coded %s; the choice is ambiguous — choose another selection",
			len(matches), FirstRunCode), false
	}
}

// --- token resolution (exact, unique) ----------------------------------------

// ErrUnknownToken / ErrAmbiguousToken distinguish an unresolvable selection.
var (
	ErrUnknownToken   = errors.New("unknown classification token")
	ErrAmbiguousToken = errors.New("ambiguous classification token")
)

// ResolveToken matches token against the catalog by EXACT id first, then by EXACT
// (case-insensitive) code, then by an UNAMBIGUOUS id-prefix (the same short prefix
// `classify list` prints). It refuses an unknown or ambiguous token and NEVER
// synthesizes or fuzzy-matches an id.
func ResolveToken(cat gradion.Catalog, token string) (gradion.ClassificationOption, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return gradion.ClassificationOption{}, ErrUnknownToken
	}
	for _, o := range cat.Options {
		if strings.TrimSpace(o.ID) == token {
			return o, nil
		}
	}
	var byCode []gradion.ClassificationOption
	for _, o := range cat.Options {
		if o.Code != "" && strings.EqualFold(strings.TrimSpace(o.Code), token) {
			byCode = append(byCode, o)
		}
	}
	switch len(byCode) {
	case 1:
		return byCode[0], nil
	case 0:
		// fall through to id-prefix matching
	default:
		return gradion.ClassificationOption{}, ErrAmbiguousToken
	}

	// An id-prefix is accepted only when it identifies EXACTLY ONE option.
	var byPrefix []gradion.ClassificationOption
	for _, o := range cat.Options {
		id := strings.TrimSpace(o.ID)
		if id == "" || len(id) < len(token) {
			continue
		}
		if strings.EqualFold(id[:len(token)], token) {
			byPrefix = append(byPrefix, o)
		}
	}
	switch len(byPrefix) {
	case 1:
		return byPrefix[0], nil
	case 0:
		return gradion.ClassificationOption{}, ErrUnknownToken
	default:
		return gradion.ClassificationOption{}, ErrAmbiguousToken
	}
}

// --- catalog union -----------------------------------------------------------

// UnionCatalogs merges base and targeted into one selection view, keyed by option
// ID. A targeted option REPLACES a same-ID base option (the targeted fetch is the
// more specific one); a targeted-only option is appended. An option with a BLANK
// ID has no identity, so it is keyed by its content (name/code/activity type) and
// deduped the same way: an identical blank-ID row is collapsed, while genuinely
// distinct blank-ID rows all survive. The result lists base options in base order
// first, then any targeted-only options in targeted order, so a caller can
// resolve a token that is absent from the unfiltered page but present in the
// targeted one. Complete is true only when EVERY contributing catalog is complete,
// so a union never claims to prove absence while one input was partial.
func UnionCatalogs(base, targeted gradion.Catalog) gradion.Catalog {
	merged := base
	merged.Options = nil
	// key identifies an option for dedup: by ID when present, else by content so
	// blank-ID rows still collapse on exact duplicates instead of double-counting.
	type optionKey struct{ kind, id, name, code, activity string }
	keyOf := func(o gradion.ClassificationOption) optionKey {
		if id := strings.TrimSpace(o.ID); id != "" {
			return optionKey{kind: "id", id: id}
		}
		return optionKey{
			kind:     "blank",
			name:     strings.TrimSpace(o.Name),
			code:     strings.TrimSpace(o.Code),
			activity: strings.TrimSpace(o.ActivityType),
		}
	}
	seen := make(map[optionKey]int, len(base.Options)+len(targeted.Options))
	merge := func(o gradion.ClassificationOption, targetedWins bool) {
		k := keyOf(o)
		if idx, ok := seen[k]; ok {
			if targetedWins {
				merged.Options[idx] = o // targeted wins on conflict
			}
			return // never double-count
		}
		seen[k] = len(merged.Options)
		merged.Options = append(merged.Options, o)
	}
	for _, o := range base.Options {
		merge(o, false)
	}
	for _, o := range targeted.Options {
		merge(o, true)
	}
	merged.Complete = base.Complete && targeted.Complete
	// The union is a resolution view, not a filtered fetch: it is unfiltered by
	// construction. Completeness is carried by Complete alone.
	merged.Query = ""
	if targeted.ObservedAt.After(merged.ObservedAt) {
		merged.ObservedAt = targeted.ObservedAt
	}
	return merged
}

// --- catalog cache -----------------------------------------------------------
//
// The staleness policy itself lives in the shared lower layer: gradion.CatalogMaxAge
// (with gradion.CatalogFresh). This package only DISPLAYS freshness via Freshness.

// DefaultCatalogCachePath returns ~/.timesheet/catalog-cache.json.
func DefaultCatalogCachePath() (string, error) {
	dir, err := baseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "catalog-cache.json"), nil
}

// ErrNoCache reports that no cached catalog exists at the given path.
var ErrNoCache = errors.New("no cached catalog")

// catalogCacheFile is the QUERY-SCOPED cache document. Each fetched catalog is
// stored under its normalized query, so a filtered `--search Q` fetch can never
// overwrite the unfiltered ("") catalog.
type catalogCacheFile struct {
	Entries map[string]gradion.Catalog `json:"entries"`
}

// normalizeQuery is the cache key for a search query: trimmed, so "" is the
// unfiltered catalog.
func normalizeQuery(q string) string { return strings.TrimSpace(q) }

// readCacheFile loads the cache document, migrating a LEGACY single-Catalog file
// (written before query scoping) to the unfiltered ("") entry so it is never
// silently discarded. A missing file yields an empty document.
func readCacheFile(path string) (catalogCacheFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return catalogCacheFile{}, nil
		}
		return catalogCacheFile{}, fmt.Errorf("selection: read catalog cache: %w", err)
	}
	var file catalogCacheFile
	if err := json.Unmarshal(data, &file); err != nil {
		return catalogCacheFile{}, fmt.Errorf("selection: parse catalog cache: %w", err)
	}
	if file.Entries != nil {
		return file, nil
	}
	// Legacy single-Catalog document: migrate it to the unfiltered entry.
	var legacy gradion.Catalog
	if err := json.Unmarshal(data, &legacy); err != nil {
		return catalogCacheFile{}, fmt.Errorf("selection: parse catalog cache: %w", err)
	}
	return catalogCacheFile{Entries: map[string]gradion.Catalog{"": legacy}}, nil
}

// LoadCatalogCache reads the cached catalog for query. A missing file or a
// missing entry for that query returns ErrNoCache. A legacy single-Catalog file
// is treated as the unfiltered ("") entry. The loaded catalog's Source is forced
// to "cache".
func LoadCatalogCache(path, query string) (gradion.Catalog, error) {
	file, err := readCacheFile(path)
	if err != nil {
		return gradion.Catalog{}, err
	}
	if file.Entries == nil {
		return gradion.Catalog{}, ErrNoCache
	}
	cat, ok := file.Entries[normalizeQuery(query)]
	if !ok {
		return gradion.Catalog{}, ErrNoCache
	}
	cat.Source = "cache"
	return cat, nil
}

// LoadCatalogCacheAny returns the NEWEST cached catalog across ALL queries,
// regardless of query. It is the submit path's last resort when no unfiltered
// catalog is available: the returned catalog may be PARTIAL/filtered, and the
// submission boundary then validates absent choices with targeted per-id reads.
// It returns ErrNoCache when the cache holds no entries. Ties break on the
// normalized query for determinism.
func LoadCatalogCacheAny(path string) (gradion.Catalog, error) {
	file, err := readCacheFile(path)
	if err != nil {
		return gradion.Catalog{}, err
	}
	if len(file.Entries) == 0 {
		return gradion.Catalog{}, ErrNoCache
	}
	var best gradion.Catalog
	bestKey := ""
	first := true
	for key, cat := range file.Entries {
		if first || cat.ObservedAt.After(best.ObservedAt) || (cat.ObservedAt.Equal(best.ObservedAt) && key < bestKey) {
			best, bestKey, first = cat, key, false
		}
	}
	best.Source = "cache"
	return best, nil
}

// SaveCatalogCache UPSERTS a fetched catalog under its normalized Query (0600),
// creating the directory (0700). Every other query's entry is PRESERVED, so a
// filtered fetch never clobbers the unfiltered catalog. A legacy single-Catalog
// file is migrated first.
func SaveCatalogCache(path string, cat gradion.Catalog) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("selection: mkdir: %w", err)
	}
	file, err := readCacheFile(path)
	if err != nil {
		return err
	}
	if file.Entries == nil {
		file.Entries = map[string]gradion.Catalog{}
	}
	file.Entries[normalizeQuery(cat.Query)] = cat
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("selection: marshal catalog cache: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("selection: write catalog cache: %w", err)
	}
	return nil
}

// Freshness describes a catalog's provenance and age. fresh is false for a cache
// older than CatalogMaxAge. The freshness decision is delegated to the shared
// gradion.CatalogFresh policy.
func Freshness(cat gradion.Catalog, now time.Time) (string, bool) {
	source := strings.TrimSpace(cat.Source)
	if source == "" {
		source = "unknown"
	}
	fresh := gradion.CatalogFresh(cat, now)
	if cat.ObservedAt.IsZero() {
		return fmt.Sprintf("%s, observed-at unknown", source), fresh
	}
	age := now.Sub(cat.ObservedAt)
	if age < 0 {
		age = 0
	}
	state := "fresh"
	if !fresh {
		state = "STALE"
	}
	return fmt.Sprintf("%s, observed %s (%s ago, %s)", source,
		cat.ObservedAt.UTC().Format(time.RFC3339), age.Truncate(time.Second), state), fresh
}
