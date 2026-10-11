package run

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// CollectResult reports one collection pass.
type CollectResult struct {
	Date        string                     `json:"date"`
	Evidence    []evidence.Evidence        `json:"evidence,omitempty"`
	Summary     evidence.CollectionSummary `json:"summary"`
	MergedCount int                        `json:"merged_count"`
	// NoSourcesConfigured is true when ZERO evidence sources are enabled (an
	// empty source list or every source disabled). It distinguishes "nothing is
	// configured" from "configured sources legitimately returned no evidence".
	NoSourcesConfigured bool `json:"no_sources_configured,omitempty"`
}

// Collect runs the registry for the day and merges the evidence into the record
// under the store. Collection proceeds independently of review or submission.
func (r *Runner) Collect(ctx context.Context, date string) (CollectResult, error) {
	ev, summary, err := r.deps.Registry.CollectDay(ctx, r.cfg.Evidence.Sources, evidence.CollectRequest{Date: date})
	if err != nil {
		return CollectResult{Date: date, Summary: summary}, err
	}
	enabled := enabledSourceCount(r.cfg.Evidence.Sources)
	out := CollectResult{
		Date:                date,
		Evidence:            ev,
		Summary:             summary,
		MergedCount:         len(ev),
		NoSourcesConfigured: enabled == 0,
	}
	// Only (over)write the collection summary when collection actually RAN with
	// at least one ENABLED source. With NO enabled source, an existing (possibly
	// INCOMPLETE) summary is preserved, so a prior partial collection is never
	// silently presented as complete to the readiness gate. Using the enabled
	// count (not len(sources)) covers an all-disabled source list, whose summary
	// would otherwise be vacuously "complete" and clobber a real partial one.
	hasEnabledSources := enabled > 0
	_, err = r.deps.Store.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		mergeEvidenceIntoRecord(rec, ev)
		if hasEnabledSources {
			summaryCopy := summary
			rec.Collection = &summaryCopy
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	return out, nil
}

// enabledSourceCount returns the number of enabled sources.
func enabledSourceCount(sources []evidence.SourceConfig) int {
	n := 0
	for _, s := range sources {
		if s.Enabled {
			n++
		}
	}
	return n
}

// mergeEvidenceIntoRecord upserts collected evidence and the legacy reference
// representation. User decisions live in Review and are never touched.
func mergeEvidenceIntoRecord(rec *workflow.Record, incoming []evidence.Evidence) {
	workflow.MergeEvidence(rec, evidenceReferences(incoming))
	byID := make(map[string]int, len(rec.CollectedEvidence))
	for i, e := range rec.CollectedEvidence {
		byID[e.ID] = i
	}
	for _, e := range incoming {
		if i, ok := byID[e.ID]; ok {
			rec.CollectedEvidence[i] = e
			continue
		}
		byID[e.ID] = len(rec.CollectedEvidence)
		rec.CollectedEvidence = append(rec.CollectedEvidence, e)
	}
}

func evidenceReferences(ev []evidence.Evidence) []workflow.EvidenceReference {
	out := make([]workflow.EvidenceReference, 0, len(ev))
	for _, e := range ev {
		out = append(out, workflow.EvidenceReference{
			ID:         e.ID,
			Source:     string(e.Kind),
			Timestamp:  formatClock(e.Start),
			EndTime:    formatClock(e.End),
			Title:      e.Title,
			Ref:        evidenceRef(e),
			ObservedAt: e.ObservedAt,
		})
	}
	return out
}

func evidenceRef(e evidence.Evidence) string {
	for _, ref := range e.SourceRefs {
		if strings.TrimSpace(ref.Repository) != "" {
			if ref.PRNumber > 0 {
				return ref.Repository + "#" + strconv.Itoa(ref.PRNumber)
			}
			return ref.Repository
		}
	}
	return ""
}

func formatClock(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("15:04")
}
