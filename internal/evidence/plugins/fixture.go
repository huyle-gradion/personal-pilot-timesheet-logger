// Package plugins provides concrete evidence source adapters registered in the
// compile-time evidence Registry: the calendar, git (local commits + remote PRs),
// and agent-session adapters, plus the `fixture` plugin. The fixture plugin emits
// deterministic contract evidence from a fixture file so registry and
// contract-check behavior can be exercised end to end without a live source.
package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
)

// TypeFixture is the registry type of the fixture plugin.
const TypeFixture evidence.SourceKind = "fixture"

// ConfigKeyPath is the fixture plugin's only required config key: the path to a
// fixture JSON document. It is non-secret.
const ConfigKeyPath = "path"

// Fixture emits deterministic contract evidence from a fixture JSON file. It
// proves extensibility and drives registry/contract-check tests; it never
// touches a live source.
type Fixture struct{}

// New returns a fixture plugin.
func New() *Fixture { return &Fixture{} }

// Register registers the fixture plugin on r.
func Register(r *evidence.Registry) { r.Register(New()) }

// Type implements evidence.Plugin.
func (f *Fixture) Type() evidence.SourceKind { return TypeFixture }

// RequiredConfig implements evidence.Plugin.
func (f *Fixture) RequiredConfig() []evidence.ConfigRequirement {
	return []evidence.ConfigRequirement{{Key: ConfigKeyPath, Secret: false, Required: true}}
}

// DeclaredEvidenceKinds implements evidence.Plugin. A fixture may emit any of the
// S3 evidence kinds; declaring all of them proves the declared-kind check
// accepts a multi-kind plugin.
func (f *Fixture) DeclaredEvidenceKinds() []evidence.SourceKind {
	return []evidence.SourceKind{
		evidence.SourceCalendar,
		evidence.SourceLocalGit,
		evidence.SourceRemotePR,
		evidence.SourceManual,
		evidence.SourceAgentSession,
	}
}

// CheckContract implements evidence.Plugin. It verifies the fixture path exists
// and is readable before enablement.
func (f *Fixture) CheckContract(_ context.Context, cfg evidence.SourceConfig) []evidence.ContractCheck {
	path := cfg.Config[ConfigKeyPath]
	if _, err := os.Stat(path); err != nil {
		return []evidence.ContractCheck{{
			Name:   "fixture_path_readable",
			Passed: false,
			Code:   "FIXTURE_PATH_UNREADABLE",
			Detail: "fixture path is not readable",
		}}
	}
	return []evidence.ContractCheck{{Name: "fixture_path_readable", Passed: true}}
}

// fixtureFile is the fixture document shape.
type fixtureFile struct {
	Evidence []fixtureEntry `json:"evidence"`
}

// fixtureEntry is one fixture evidence template. IdentityParts are the stable
// tuple parts fed to evidence.StableID, so identical fixtures yield identical
// IDs across runs.
type fixtureEntry struct {
	Kind            string   `json:"kind"`
	Title           string   `json:"title"`
	Description     string   `json:"description,omitempty"`
	Start           string   `json:"start,omitempty"`
	End             string   `json:"end,omitempty"`
	AllDay          bool     `json:"all_day,omitempty"`
	Status          string   `json:"status,omitempty"`
	Invitation      string   `json:"invitation,omitempty"`
	DurationKnown   bool     `json:"duration_known,omitempty"`
	DurationMinutes int      `json:"duration_minutes,omitempty"`
	Repository      string   `json:"repository,omitempty"`
	Branch          string   `json:"branch,omitempty"`
	PRNumber        int      `json:"pr_number,omitempty"`
	ProjectHints    []string `json:"project_hints,omitempty"`
	IdentityParts   []string `json:"identity_parts,omitempty"`
}

// Collect implements evidence.Plugin. It reads the fixture file and emits one
// Evidence per entry with a deterministic ID. Cancelled events and declined or
// unknown invitations are retained with their status, never dropped here.
func (f *Fixture) Collect(ctx context.Context, cfg evidence.SourceConfig, req evidence.CollectRequest) (evidence.CollectResult, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return evidence.CollectResult{}, err
		}
	}

	data, err := os.ReadFile(cfg.Config[ConfigKeyPath])
	if err != nil {
		return evidence.CollectResult{}, fmt.Errorf("fixture: failed to read fixture file")
	}
	var ff fixtureFile
	if err := json.Unmarshal(data, &ff); err != nil {
		return evidence.CollectResult{}, fmt.Errorf("fixture: failed to parse fixture JSON")
	}

	observed := time.Now().UTC()
	evs := make([]evidence.Evidence, 0, len(ff.Evidence))
	for _, e := range ff.Evidence {
		kind := evidence.SourceKind(e.Kind)
		parts := e.IdentityParts
		if len(parts) == 0 {
			parts = []string{e.Title}
		}

		ev := evidence.Evidence{
			ID:           evidence.StableID(kind, parts...),
			Kind:         kind,
			Title:        e.Title,
			Description:  e.Description,
			AllDay:       e.AllDay,
			Status:       e.Status,
			Invitation:   evidence.InvitationStatus(e.Invitation),
			Duration:     evidence.Duration{Known: e.DurationKnown, Minutes: e.DurationMinutes},
			ProjectHints: e.ProjectHints,
			ObservedAt:   observed,
		}
		if e.Start != "" {
			if t, err := time.Parse(time.RFC3339, e.Start); err == nil {
				ev.Start = t
			}
		}
		if e.End != "" {
			if t, err := time.Parse(time.RFC3339, e.End); err == nil {
				ev.End = t
			}
		}
		if e.Repository != "" || e.PRNumber != 0 || e.Branch != "" {
			ev.TopicHint = &evidence.TopicHint{
				Repository: e.Repository,
				PRNumber:   e.PRNumber,
				Branch:     e.Branch,
			}
			ev.SourceRefs = append(ev.SourceRefs, evidence.SourceRef{
				Kind:       kind,
				Repository: e.Repository,
				Branch:     e.Branch,
				PRNumber:   e.PRNumber,
			})
		}
		evs = append(evs, ev)
	}

	return evidence.CollectResult{
		Evidence: evs,
		Result:   evidence.SourceResult{Status: evidence.StatusComplete, EvidenceCount: len(evs)},
	}, nil
}
