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

// --- catalog cache -----------------------------------------------------------

// CatalogMaxAge bounds how old a cached catalog may be before it is STALE.
const CatalogMaxAge = 24 * time.Hour

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

// LoadCatalogCache reads a cached catalog. A missing file returns ErrNoCache. The
// loaded catalog's Source is forced to "cache".
func LoadCatalogCache(path string) (gradion.Catalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return gradion.Catalog{}, ErrNoCache
		}
		return gradion.Catalog{}, fmt.Errorf("selection: read catalog cache: %w", err)
	}
	var cat gradion.Catalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return gradion.Catalog{}, fmt.Errorf("selection: parse catalog cache: %w", err)
	}
	cat.Source = "cache"
	return cat, nil
}

// SaveCatalogCache writes a fetched catalog (0600), creating the directory (0700).
func SaveCatalogCache(path string, cat gradion.Catalog) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("selection: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return fmt.Errorf("selection: marshal catalog cache: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("selection: write catalog cache: %w", err)
	}
	return nil
}

// Freshness describes a catalog's provenance and age. fresh is false for a cache
// older than CatalogMaxAge.
func Freshness(cat gradion.Catalog, now time.Time) (string, bool) {
	source := strings.TrimSpace(cat.Source)
	if source == "" {
		source = "unknown"
	}
	if cat.ObservedAt.IsZero() {
		return fmt.Sprintf("%s, observed-at unknown", source), source == "live"
	}
	age := now.Sub(cat.ObservedAt)
	if age < 0 {
		age = 0
	}
	fresh := source == "live" || age <= CatalogMaxAge
	state := "fresh"
	if !fresh {
		state = "STALE"
	}
	return fmt.Sprintf("%s, observed %s (%s ago, %s)", source,
		cat.ObservedAt.UTC().Format(time.RFC3339), age.Truncate(time.Second), state), fresh
}
