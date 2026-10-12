package run

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence/plugins"
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
	resolved := r.resolveCalendarSources(ctx, r.cfg.Evidence.Sources)
	ev, summary, err := r.deps.Registry.CollectDay(ctx, resolved, evidence.CollectRequest{Date: date, Location: r.loc})
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
		reconcileJournalLifecycle(rec, ev, summary)
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

// resolveCalendarSources returns a COPY of sources with each ENABLED calendar
// source's keychain url_ref resolved IN MEMORY into Config[source] immediately
// before collection. It never mutates the caller's/Runner's config: a source with
// a resolvable reference gets a fresh copied Config map, and every other source is
// passed through unchanged (its map shared but never written). Only calendar
// sources are touched; the reference itself is left intact.
//
// A resolution failure (NotFound or any other error) leaves source UNSET so the
// calendar plugin reports incomplete (SOURCE_ERROR). It never aborts the run,
// never fabricates a source, and never resolves any non-calendar source. The
// resolved ICS URL is a SECRET: it is placed only in the returned in-memory copy
// and is never persisted to a record, export, log, warning, or error. Preflight
// semantics are unaffected: this runs at collection only.
func (r *Runner) resolveCalendarSources(ctx context.Context, sources []evidence.SourceConfig) []evidence.SourceConfig {
	out := make([]evidence.SourceConfig, len(sources))
	copy(out, sources)
	for i := range out {
		s := out[i]
		if !s.Enabled || s.Type != evidence.SourceCalendar {
			continue
		}
		service, ok := keychainService(s.Config[plugins.CalendarConfigURLRef])
		if !ok {
			continue
		}
		secret, err := r.keychainResolver().Lookup(ctx, service, "")
		if err != nil || strings.TrimSpace(secret) == "" {
			continue
		}
		// Copy the map before writing so the caller's config is never mutated.
		cfg := make(map[string]string, len(s.Config)+1)
		for k, v := range s.Config {
			cfg[k] = v
		}
		cfg[plugins.CalendarConfigSource] = secret
		out[i].Config = cfg
	}
	return out
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

// reconcileJournalLifecycle marks prior journal evidence DETACHED when a
// successful, COMPLETE authoritative journal collection no longer contains it,
// and marks present items ACTIVE. It runs ONLY for a journal source that is
// StatusComplete in this pass: on a failed or partial collection, prior journal
// history is never detached and no positional superseded guess is made. Non-journal
// evidence and user decisions (Confirmed/Review/text/approvals/outcomes) are never
// touched.
func reconcileJournalLifecycle(rec *workflow.Record, incoming []evidence.Evidence, summary evidence.CollectionSummary) {
	if rec == nil {
		return
	}
	completeJournal := map[string]bool{}
	for _, sr := range summary.Sources {
		if sr.Type == evidence.SourceJournal {
			completeJournal[sr.SourceID] = sr.Status == evidence.StatusComplete
		}
	}
	if len(completeJournal) == 0 {
		return
	}

	active := map[string]map[string]bool{}
	for _, e := range incoming {
		if e.Kind != evidence.SourceJournal {
			continue
		}
		sid := journalSourceIDOf(e)
		if active[sid] == nil {
			active[sid] = map[string]bool{}
		}
		active[sid][e.ID] = true
	}

	for i := range rec.CollectedEvidence {
		e := &rec.CollectedEvidence[i]
		if e.Kind != evidence.SourceJournal {
			continue
		}
		sid := journalSourceIDOf(*e)
		if !completeJournal[sid] {
			// A failed/partial journal collection never detaches prior history.
			continue
		}
		if active[sid][e.ID] {
			e.JournalState = evidence.JournalActive
		} else {
			e.JournalState = evidence.JournalDetached
		}
	}
}

// journalSourceIDOf returns a journal item's configured source id, or "" when it
// carries none.
func journalSourceIDOf(e evidence.Evidence) string {
	for _, ref := range e.SourceRefs {
		if ref.Kind == evidence.SourceJournal {
			return ref.SourceID
		}
	}
	return ""
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
