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
