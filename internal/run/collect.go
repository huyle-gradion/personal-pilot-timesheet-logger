package run

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence/plugins"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
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
	// RemoteObservationError is a non-fatal warning set when the
	// collection-time remote read failed. Evidence collection still succeeds and
	// no fabricated observation is persisted. It is returned for PROGRAMMATIC
	// consumers: no CLI surface prints it yet (see issue #49).
	RemoteObservationError string `json:"remote_observation_error,omitempty"`
	// RemoteCollisions lists remote entry ids observed during collection whose
	// span overlaps a confirmed interval on the same date. A duplicate-looking
	// signal ONLY: it never creates a mapping, never proposes, and never blocks
	// collection. It is returned for PROGRAMMATIC consumers; no CLI surface
	// prints it yet (see issue #49).
	RemoteCollisions []string `json:"remote_collisions,omitempty"`
}

// Collect runs the registry for the day and merges the evidence into the record
// under the store. Collection proceeds independently of review or submission.
//
// Collection also performs the day's single read of EXISTING Gradion entries, as
// docs/timesheet-redesign.md:43 (the Collection row) requires. That read is a
// declared, injected, read-only SOURCE over the existing `run.Remote` seam
// (run.go), not an evidence plugin, and the distinction is deliberate:
//
//   - The evidence registry's only data channel is
//     `CollectResult{Evidence []Evidence}` (internal/evidence/registry.go), and
//     internal/evidence is workflow-free by design ("the import direction is
//     workflow -> evidence", contract.go:12), so it cannot even name
//     workflow.RemoteState -- the authoritative remote-entry type that
//     submit.Reconcile, submit.BuildPlan and submit.ValidateApproval consume. A
//     plugin would need a third representation of a remote entry and a second
//     identity for it.
//   - Emitting remote entries as evidence would put authoritative remote state
//     into CollectedEvidence, which Propose feeds straight into proposal.Build.
//     docs/timesheet-redesign.md:32 keeps that boundary: evidence never becomes
//     confirmed working time, and Gradion is authoritative for ALREADY SUBMITTED
//     entries, which are remote state rather than evidence.
//   - The specification's own source enumeration (line 135) lists calendar, git
//     and remote PR activity, manual evidence, optional agent-session evidence,
//     and the optional journal -- and no Gradion-entries source. It also lists
//     "manual evidence", which has a SourceKind but no plugin, so being a
//     "source" does not imply being a registered evidence plugin.
//
// The read is therefore injected through run.Deps.Remote like every other remote
// collaborator, and it is read-only: it creates no RemoteMapping, never feeds
// CollectedEvidence or Confirmed, and never reaches the proposal.
func (r *Runner) Collect(ctx context.Context, date string) (CollectResult, error) {
	// Fail closed on an invalid configured timezone BEFORE collecting. `timesheet
	// review --date <d>` reaches Collect without Preflight, so an unresolved
	// location would otherwise silently anchor the day window to UTC.
	if r.locErr != nil {
		return CollectResult{Date: date}, r.locErr
	}
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
	// Read the remote listing EXACTLY ONCE for this run when a remote is
	// configured, then hand that one object to the run's later observe
	// (cacheListing). A read failure is a non-fatal warning returned for
	// PROGRAMMATIC consumers: no CLI surface prints it yet (issue #49). No
	// observation is fabricated and evidence collection still succeeds. Offline
	// (nil Remote) makes NO remote call and leaves the observation nil — never
	// fabricated.
	//
	// The read goes through the DECLARED source seam, not an inline client:
	// Deps.Remote is the injected, read-only remote source adapter
	// (run.Remote, which embeds submit.RemoteObserver.ListEntries), so the fetch
	// has one implementation point and the same injected-collaborator discipline
	// as every other source. It is deliberately not an evidence plugin: the
	// evidence registry's output is evidence.Evidence and internal/evidence is
	// workflow-free by design, so it cannot name workflow.RemoteState, the
	// authoritative remote-entry type that submit.Reconcile, submit.BuildPlan and
	// submit.ValidateApproval consume. See the COLLECT-REMOTE-1 record for the
	// full argument.
	var (
		listing     gradion.Listing
		haveListing bool
	)
	if r.deps.Remote != nil {
		// Collection ALWAYS live-reads: it is the authoritative read for the run,
		// so it must not read through the per-run cache (a second Collect for the
		// same date on a reused Runner would otherwise serve a stale listing). It
		// then POPULATES the cache on success and INVALIDATES it on failure, so
		// the run's later observe reuses exactly the read Collection just made and
		// never a stale one — a failed re-read must not silently fall back to an
		// older listing.
		l, lErr := r.deps.Remote.ListEntries(ctx, date, date, 0)
		if lErr != nil {
			out.RemoteObservationError = lErr.Error()
			r.dropListing(date)
		} else {
			listing = l
			haveListing = true
			r.cacheListing(date, l)
		}
	}
	_, err = r.deps.Store.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		mergeEvidenceIntoRecord(rec, ev)
		reconcileJournalLifecycle(rec, ev, summary)
		if hasEnabledSources {
			summaryCopy := summary
			rec.Collection = &summaryCopy
		}
		// Persist the observation and surface collection-time collisions. This is
		// READ-ONLY w.r.t. mappings, evidence, approvals and the proposal: it
		// creates no RemoteMapping and never feeds CollectedEvidence/Confirmed.
		if haveListing {
			// Entries shares the listing's backing array with the per-run cache.
			// That is safe by contract: nothing mutates a listing's entries, and
			// the observation is serialized on write. It is also never mutated
			// after this point, so a future consumer must treat it as read-only.
			rec.RemoteObservation = &workflow.RemoteObservation{
				Date:       date,
				ObservedAt: listing.ObservedAt,
				Complete:   listing.Complete,
				Entries:    listing.Entries,
			}
			out.RemoteCollisions = remoteCollisions(date, rec.Confirmed, listing.Entries)
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	return out, nil
}

// remoteCollisions returns the observed remote entry ids whose span overlaps a
// confirmed interval on the same date. It is a duplicate-looking signal ONLY:
// it never creates a mapping, never proposes, and never blocks collection. The
// result is returned for PROGRAMMATIC consumers; no CLI surface prints it yet
// (issue #49).
func remoteCollisions(date string, confirmed []workflow.ConfirmedInterval, entries []workflow.RemoteState) []string {
	var out []string
	for _, e := range entries {
		if e.Date != "" && e.Date != date {
			continue
		}
		for _, ci := range confirmed {
			if ci.Date != "" && ci.Date != date {
				continue
			}
			if workflow.TimesOverlap(ci.StartTime, ci.EndTime, e.StartTime, e.EndTime) {
				out = append(out, e.RemoteID)
				break
			}
		}
	}
	return out
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
