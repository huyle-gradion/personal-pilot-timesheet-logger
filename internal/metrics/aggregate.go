package metrics

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// RecordSource is the narrow read surface Aggregate needs from the workflow
// store. It is satisfied by *workflow.JSONFileStore. Defining it here keeps the
// metrics package decoupled from the workflow store's full interface.
type RecordSource interface {
	GetDay(ctx context.Context, date string) (*workflow.Record, error)
}

// Budget state values. These mirror editorial.BudgetState strings WITHOUT
// importing internal/editorial (dependency boundary).
const (
	BudgetKnownAvailable = "known_available"
	BudgetKnownExhausted = "known_exhausted"
	BudgetUnknownState   = "unknown"
)

// BuildAggregate computes a DailyAggregate as a PURE function of its inputs: the
// day's runs plus the workflow record (for editorial usage). It stores NO
// generation timestamp, so aggregating the same inputs twice is byte-identical.
//
// Calls are bucketed by category: `Orchestration` holds ONLY
// `CategoryOrchestration` calls, and every other (unattributable) call goes to
// `Unassigned` — it is never folded into orchestration. Editorial usage is
// DAY-granular and copied from the workflow record; it is never added to
// orchestration. Duration totals are known-only sums (UNKNOWN if any contributing
// run is unknown).
func BuildAggregate(ctx context.Context, date string, runs []RunRecord, src RecordSource) (DailyAggregate, error) {
	if ctx == nil {
		return DailyAggregate{}, errors.New("metrics: nil context")
	}

	agg := DailyAggregate{
		SchemaVersion: SchemaVersion,
		Date:          date,
		RunIDs:        []string{},
	}

	// Sorted union of run ids (deterministic across re-aggregation).
	seen := make(map[string]struct{}, len(runs))
	ids := make([]string, 0, len(runs))
	orchestration := ZeroUsage()
	unassigned := ZeroUsage()
	unassignedCalls := 0
	prov := ProvenanceUnknown
	var endToEnd, active, humanWait []Duration
	var commits, versions, modes, changes []string
	for i := range runs {
		r := runs[i]
		if _, ok := seen[r.RunID]; ok {
			continue
		}
		seen[r.RunID] = struct{}{}
		ids = append(ids, r.RunID)
		for j := range r.Calls {
			c := r.Calls[j]
			if c.Category == CategoryOrchestration {
				orchestration = orchestration.Add(c.Usage)
			} else {
				unassigned = unassigned.Add(c.Usage)
				unassignedCalls++
			}
		}
		prov = mergeProvenance(prov, r.Provenance)
		endToEnd = append(endToEnd, r.EndToEnd)
		active = append(active, r.Active)
		humanWait = append(humanWait, r.HumanWait)
		commits = appendNonEmpty(commits, r.Commit)
		versions = appendNonEmpty(versions, r.WorkflowVersion)
		modes = appendNonEmpty(modes, r.ConfigMode)
		changes = appendNonEmpty(changes, r.OptimizationChange)
	}
	sort.Strings(ids)
	agg.RunIDs = ids
	agg.RunCount = len(ids)
	agg.Orchestration = orchestration
	agg.Unassigned = unassigned
	agg.UnassignedCalls = unassignedCalls
	agg.Provenance = prov
	agg.EndToEnd = sumDurations(endToEnd)
	agg.Active = sumDurations(active)
	agg.HumanWait = sumDurations(humanWait)
	agg.Annotations = Annotations{
		Commits:             sortedUnique(commits),
		WorkflowVersions:    sortedUnique(versions),
		ConfigModes:         sortedUnique(modes),
		OptimizationChanges: sortedUnique(changes),
	}

	rec, err := getRecord(ctx, date, src)
	if err != nil {
		return DailyAggregate{}, err
	}
	applyEditorial(&agg, rec)
	return agg, nil
}

// sumDurations sums Duration VALUES with the known-only rule (a single unknown
// contributor makes the sum UNKNOWN) and labels the result with the strongest
// contributing source (monotonic > transcript). Zero durations yield UNKNOWN.
func sumDurations(durs []Duration) Duration {
	unknown := Duration{Value: workflow.UnknownMeasurement(), Source: DurationUnknown}
	if len(durs) == 0 {
		return unknown
	}
	sum := workflow.KnownMeasurement(0)
	source := DurationUnknown
	for _, d := range durs {
		sum = addMeasurement(sum, d.Value)
		source = mergeDurationSource(source, d.Source)
	}
	if !sum.Known {
		return unknown
	}
	return Duration{Value: sum, Source: source}
}

func mergeDurationSource(a, b DurationSource) DurationSource {
	if durationSourceRank(b) > durationSourceRank(a) {
		return b
	}
	return a
}

func durationSourceRank(s DurationSource) int {
	switch s {
	case DurationMonotonic:
		return 3
	case DurationTranscript:
		return 2
	case DurationUnknown:
		return 1
	default:
		return 0
	}
}

// appendNonEmpty appends a trimmed non-empty string.
func appendNonEmpty(dst []string, v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return dst
	}
	return append(dst, v)
}

// sortedUnique returns a sorted, de-duplicated copy. It returns a NON-NIL empty
// slice for an empty input so the JSON is `[]`, never `null`.
func sortedUnique(in []string) []string {
	out := make([]string, 0, len(in))
	if len(in) == 0 {
		return out
	}
	seen := make(map[string]struct{}, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// getRecord reads the day's workflow record. A missing record is not an error:
// it yields a nil record (no editorial usage).
func getRecord(ctx context.Context, date string, src RecordSource) (*workflow.Record, error) {
	if src == nil {
		return nil, nil
	}
	rec, err := src.GetDay(ctx, date)
	if err != nil {
		if errors.Is(err, workflow.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return rec, nil
}

// applyEditorial copies editorial usage from the workflow record into the
// aggregate. The editorial lane is SEPARATE from orchestration. A record with no
// Usage yields UNKNOWN editorial usage (never zero), and EditorialInputToStep is
// a Known(0) ONLY when there were zero editorial CALLS.
func applyEditorial(agg *DailyAggregate, rec *workflow.Record) {
	agg.Budget.Target = BudgetTargetTokens
	if rec == nil || rec.Usage == nil {
		agg.Editorial = UnknownUsage()
		agg.EditorialRuns = 0
		agg.EditorialInvoked = false
		// Zero editorial CALLS: the §3.5 optimization metric is a genuine zero.
		agg.EditorialInputToStep = workflow.KnownMeasurement(0)
		agg.Budget.Spent = workflow.UnknownMeasurement()
		agg.Budget.State = BudgetUnknownState
		return
	}
	u := rec.Usage
	agg.Editorial = Usage{
		Input:     u.EditorialInput,
		Output:    u.EditorialOutput,
		CacheRead: u.EditorialCache,
		// The workflow record reports a single editorial cache measurement and no
		// reasoning counter; those components are UNKNOWN (never asserted zero).
		CacheWrite: workflow.UnknownMeasurement(),
		Reasoning:  workflow.UnknownMeasurement(),
	}
	agg.EditorialRuns = u.EditorialRuns
	agg.EditorialInvoked = u.Invoked
	if !u.Invoked && u.EditorialRuns == 0 {
		agg.EditorialInputToStep = workflow.KnownMeasurement(0)
	} else {
		agg.EditorialInputToStep = u.EditorialInput
	}
	agg.Budget.Spent = u.Editorial
	agg.Budget.State = budgetState(u.Editorial, BudgetTargetTokens)
}

// applySessions is a PURE, additive roll-up of session-scoped token metrics into
// agg. The session fields are SEPARATE from Orchestration/Editorial/Unassigned and
// are never merged with them. SessionTotalTokens sums each session's consumed
// total (reasoning EXCLUDED). A nil cache-hit or cost stays nil (UNKNOWN), never
// zero. Duplicate session ids are collapsed; ids are sorted for determinism.
//
// PRECONDITION: it folds EVERY session it is handed into the day's lane, so it
// MUST be handed sessions ALREADY filtered to the exact day (see sessionsForDate).
// Handing it an unfiltered slice would fold a session's tokens into a day it may
// not belong to — the double-count the design forbids. It also does NOT own
// UnassociatedSessions: the caller sets that field from the unassociated sessions
// that touch the day (see Store.Aggregate), because those sessions are precisely
// the ones excluded from the filtered slice passed here.
func applySessions(agg *DailyAggregate, sessions []SessionRecord) {
	ids := make([]string, 0, len(sessions))
	seen := make(map[string]struct{}, len(sessions))
	usage := ZeroUsage()
	total := workflow.KnownMeasurement(0)
	unassociated := 0
	costKnown := true
	hasCost := false
	var costSum float64
	for i := range sessions {
		s := sessions[i]
		if _, ok := seen[s.SessionID]; ok {
			continue
		}
		seen[s.SessionID] = struct{}{}
		ids = append(ids, s.SessionID)
		usage = usage.Add(s.Usage)
		total = addMeasurement(total, s.TotalTokens)
		if s.Association == AssociationUnknown {
			unassociated++
		}
		if s.Cost == nil {
			costKnown = false
		} else {
			costSum += *s.Cost
			hasCost = true
		}
	}
	sort.Strings(ids)
	agg.SessionIDs = ids
	agg.SessionCount = len(ids)
	agg.SessionUsage = usage
	agg.SessionTotalTokens = total
	agg.UnassociatedSessions = unassociated
	agg.SessionCacheHitPct = CacheHitPercent(usage)
	if len(ids) > 0 && hasCost && costKnown {
		c := costSum
		agg.SessionCost = &c
	} else {
		agg.SessionCost = nil
	}
}

// sessionsForDate selects the sessions counted into a day's session lane: ONLY
// sessions whose LoggedDate is exactly date. There is deliberately NO fallback to
// LocalDates. A day's totals must never include usage that could belong to another
// day, so a session whose day could not be established (LoggedDate == "") is
// excluded from EVERY day's lane instead of being folded into each day its
// workflow-step calls touch — which double-counts a session straddling local
// midnight. Such a session stays VISIBLE rather than silently dropped: it is
// reported by unassociatedSessionsForDate (the UnassociatedSessions count) and
// listed by `metrics sessions`.
func sessionsForDate(sessions []SessionRecord, date string) []SessionRecord {
	out := make([]SessionRecord, 0, len(sessions))
	for i := range sessions {
		if sessions[i].LoggedDate == date {
			out = append(out, sessions[i])
		}
	}
	return out
}

// unassociatedSessionsForDate counts the sessions whose day could not be
// established (LoggedDate == "") that touch date through their workflow-step
// LocalDates. They are NOT folded into the day's session lane; this count only
// keeps them VISIBLE as UnassociatedSessions, so a session that could belong to
// another day is surfaced on each day it actually touched, never dropped.
// Duplicate session ids are collapsed.
func unassociatedSessionsForDate(sessions []SessionRecord, date string) int {
	seen := make(map[string]struct{})
	n := 0
	for i := range sessions {
		s := sessions[i]
		if s.LoggedDate != "" || !containsDate(s.LocalDates, date) {
			continue
		}
		if _, ok := seen[s.SessionID]; ok {
			continue
		}
		seen[s.SessionID] = struct{}{}
		n++
	}
	return n
}

// containsDate reports whether dates contains date.
func containsDate(dates []string, date string) bool {
	for _, d := range dates {
		if d == date {
			return true
		}
	}
	return false
}

// budgetState classifies the soft daily editorial budget. An unknown spent total
// is BudgetUnknownState: it is NEVER treated as zero and NEVER assumed exhausted.
func budgetState(spent workflow.Measurement, target int64) string {
	if target <= 0 {
		target = BudgetTargetTokens
	}
	if !spent.Known {
		return BudgetUnknownState
	}
	if spent.Value >= target {
		return BudgetKnownExhausted
	}
	return BudgetKnownAvailable
}

// mergeProvenance returns the stronger of two provenances: observed > declared >
// estimated > unknown.
func mergeProvenance(a, b Provenance) Provenance {
	if rank(b) > rank(a) {
		return b
	}
	return a
}

func rank(p Provenance) int {
	switch p {
	case ProvenanceObserved:
		return 3
	case ProvenanceDeclared:
		return 2
	case ProvenanceEstimated:
		return 1
	default:
		return 0
	}
}
