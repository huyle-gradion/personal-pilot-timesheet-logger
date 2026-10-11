package submit

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/review"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// ClassificationResolver maps a project to a SUGGESTED Gradion classification id.
// It is a SUGGESTION source only: a resolved id is NEVER applied to a plan. The
// plan uses exclusively the explicit ConfirmedInterval.ClassificationID; the
// resolver's output is surfaced as a PlanResult.Suggestion for the user to accept
// through an explicit selection.
type ClassificationResolver func(project string) (classificationID string, ok bool)

// ClassificationSuggestion is a NON-APPLIED hint for an interval whose explicit
// classification is empty. Source names where the suggestion came from (currently
// "config_map"). A suggestion is surfaced for the user and never written to a plan.
type ClassificationSuggestion struct {
	IntervalID                string `json:"interval_id"`
	Project                   string `json:"project,omitempty"`
	SuggestedClassificationID string `json:"suggested_classification_id,omitempty"`
	SuggestedActivityType     string `json:"suggested_activity_type,omitempty"`
	Source                    string `json:"source"`
}

// PlanResult is the pure result of BuildPlan. Blockers are DATA (Reasons), never
// returned as an error; a non-nil error is reserved for programmer failures.
// Suggestions are NEVER applied: they are informational only.
type PlanResult struct {
	Plan        workflow.SubmissionPlan    `json:"plan"`
	Blocked     bool                       `json:"blocked"`
	Reasons     []string                   `json:"reasons,omitempty"`
	Reconcile   ReconcileResult            `json:"reconcile"`
	Suggestions []ClassificationSuggestion `json:"suggestions,omitempty"`
}

// BuildPlan derives a submission plan from a reviewed record and an observation.
// It performs NO IO. Guards run in order: review readiness, unresolved
// operations, an incomplete listing, and reconciliation with unresolved
// conditions. Then each confirmed interval becomes a create or an edit (edits are
// preferred when a mapping exists). Classification comes EXCLUSIVELY from the
// explicit review choice (ConfirmedInterval.ClassificationID): an empty choice
// BLOCKS with unresolved_classification. The resolver is a SUGGESTION source only
// (see ClassificationSuggestion); its output is surfaced, never applied. Never
// deletes; never silently replaces.
func BuildPlan(rec workflow.Record, obs gradion.Listing, adoptions []Adoption, resolver ClassificationResolver, now time.Time) (PlanResult, error) {
	res := Reconcile(rec, obs, adoptions, now)
	suggestions := buildSuggestions(rec, resolver)

	var reasons []string
	if rd := review.Readiness(rec); rd.Blocked {
		for _, r := range rd.Reasons {
			reasons = append(reasons, "readiness:"+r)
		}
	}
	if unresolved := workflow.UnresolvedOperations(&rec); len(unresolved) > 0 {
		reasons = append(reasons, "unresolved_operation")
	}
	if !obs.Complete {
		reasons = append(reasons, "remote_listing_incomplete")
	}
	for _, u := range res.Unresolved {
		reasons = append(reasons, "reconcile:"+u)
	}
	if len(reasons) > 0 {
		return PlanResult{Blocked: true, Reasons: dedupSorted(reasons), Reconcile: res, Suggestions: suggestions}, nil
	}

	observedByID := make(map[string]workflow.RemoteState, len(obs.Entries))
	for _, e := range obs.Entries {
		observedByID[e.RemoteID] = e
	}
	// Effective mappings include explicit adoptions: an adopted (RemoteID, LocalID)
	// pair suppresses a duplicate create for that local and is planned as an
	// edit/no-op against the adopted remote (owner decision 2 / ADR-0005).
	effectiveByLocal, _, claimedRemoteIDs := effectiveMappings(rec, adoptions)

	intervals := append([]workflow.ConfirmedInterval(nil), rec.Confirmed...)
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].ID < intervals[j].ID })

	var ops []workflow.PlanOperation
	for _, ci := range intervals {
		classificationID, ok := resolveClassification(ci)
		if !ok {
			reasons = append(reasons, "unresolved_classification:"+ci.ID)
			continue
		}

		if remoteID, mapped := effectiveByLocal[ci.ID]; mapped {
			observed, present := observedByID[remoteID]
			if !present {
				reasons = append(reasons, "mapped_remote_missing:"+ci.ID)
				continue
			}
			if RemoteContentEqual(desiredRemoteState(ci, classificationID, observed), observed) {
				continue // already identical: no-op
			}
			expected := observed
			ops = append(ops, workflow.PlanOperation{
				LocalID:          ci.ID,
				Kind:             workflow.PlanOpEdit,
				RemoteID:         remoteID,
				Date:             ci.Date,
				StartTime:        ci.StartTime,
				EndTime:          ci.EndTime,
				ClassificationID: classificationID,
				Description:      ci.Description,
				Reason:           editReason(rec),
				ExpectedRemote:   &expected,
			})
			continue
		}

		if dup := overlappingUnmappedRemote(ci.Date, ci.StartTime, ci.EndTime, obs.Entries, claimedRemoteIDs); dup != "" {
			reasons = append(reasons, "duplicate_looking_remote:"+ci.ID)
			continue
		}
		ops = append(ops, workflow.PlanOperation{
			LocalID:          ci.ID,
			Kind:             workflow.PlanOpCreate,
			Date:             ci.Date,
			StartTime:        ci.StartTime,
			EndTime:          ci.EndTime,
			ClassificationID: classificationID,
			Description:      ci.Description,
		})
	}

	if len(reasons) > 0 {
		return PlanResult{Blocked: true, Reasons: dedupSorted(reasons), Reconcile: res, Suggestions: suggestions}, nil
	}

	plan := workflow.SubmissionPlan{
		Date:             rec.Date,
		LocalFingerprint: workflow.LocalFingerprint(rec),
		RecordRevision:   rec.Revision,
		RemoteObservedAt: observedAt(obs, now),
		RemoteComplete:   obs.Complete,
		Operations:       ops,
		CreatedAt:        now,
	}
	plan.PlanID = workflow.PlanHash(plan)
	return PlanResult{Plan: plan, Reconcile: res, Suggestions: suggestions}, nil
}

// resolveClassification returns the classification for an interval. It uses ONLY
// the explicit review choice (ConfirmedInterval.ClassificationID): there is no
// resolver auto-fill, no config default, no AI or calendar inference. An empty
// choice returns ok=false and blocks the plan.
func resolveClassification(ci workflow.ConfirmedInterval) (string, bool) {
	id := strings.TrimSpace(ci.ClassificationID)
	if id == "" {
		return "", false
	}
	return id, true
}

// buildSuggestions surfaces NON-APPLIED classification suggestions for intervals
// whose explicit classification is empty. It never mutates the record and never
// supplies a plan classification. A nil resolver yields no suggestions.
func buildSuggestions(rec workflow.Record, resolver ClassificationResolver) []ClassificationSuggestion {
	if resolver == nil {
		return nil
	}
	intervals := append([]workflow.ConfirmedInterval(nil), rec.Confirmed...)
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].ID < intervals[j].ID })
	var out []ClassificationSuggestion
	for _, ci := range intervals {
		if strings.TrimSpace(ci.ClassificationID) != "" {
			continue
		}
		id, ok := resolver(ci.Project)
		id = strings.TrimSpace(id)
		if !ok || id == "" {
			continue
		}
		out = append(out, ClassificationSuggestion{
			IntervalID:                ci.ID,
			Project:                   strings.TrimSpace(ci.Project),
			SuggestedClassificationID: id,
			SuggestedActivityType:     strings.TrimSpace(ci.ActivityType),
			Source:                    "config_map",
		})
	}
	return out
}

// desiredRemoteState is the content a planned edit would produce. It overrides
// the locally-managed fields (times, classification, description) and preserves
// remote-only fields (task, billable) so a partial edit does not clear them.
func desiredRemoteState(ci workflow.ConfirmedInterval, classificationID string, observed workflow.RemoteState) workflow.RemoteState {
	return workflow.RemoteState{
		RemoteID:         observed.RemoteID,
		Date:             ci.Date,
		StartTime:        ci.StartTime,
		EndTime:          ci.EndTime,
		ClassificationID: classificationID,
		Description:      ci.Description,
		Task:             observed.Task,
		Billable:         observed.Billable,
	}
}

// editReason is the fixed, honest, machine-readable audit reason for an edit. It
// is deliberately revision-independent so a revision bump does not change the
// plan's content hash (and thus does not invalidate an otherwise-identical plan).
func editReason(rec workflow.Record) string {
	date := strings.TrimSpace(rec.Date)
	if date == "" {
		date = "unknown"
	}
	return fmt.Sprintf("timesheet review update %s", date)
}

// overlappingUnmappedRemote returns the first observed remote that overlaps the
// given slot on the same date and is NOT claimed by a mapping. Slot overlap is
// only a duplicate-looking signal; it never establishes a match.
func overlappingUnmappedRemote(date, start, end string, entries []workflow.RemoteState, mappedRemoteIDs map[string]bool) string {
	for _, e := range entries {
		if mappedRemoteIDs[e.RemoteID] {
			continue
		}
		if !slotsOverlap(date, start, end, e.Date, e.StartTime, e.EndTime) {
			continue
		}
		return e.RemoteID
	}
	return ""
}

func observedAt(obs gradion.Listing, now time.Time) time.Time {
	if obs.ObservedAt.IsZero() {
		return now
	}
	return obs.ObservedAt
}

func dedupSorted(values []string) []string {
	seen := make(map[string]bool, len(values))
	var out []string
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

const minutesPerDay = 24 * 60

// slotsOverlap reports whether two "HH:MM" slots overlap on the same date. An end
// at or before the start is treated as crossing midnight. Missing/unparseable
// times never overlap.
func slotsOverlap(aDate, aStart, aEnd, bDate, bStart, bEnd string) bool {
	if aDate != "" && bDate != "" && aDate != bDate {
		return false
	}
	as, ok1 := parseHHMM(aStart)
	ae, ok2 := parseHHMM(aEnd)
	bs, ok3 := parseHHMM(bStart)
	be, ok4 := parseHHMM(bEnd)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return false
	}
	if ae <= as {
		ae += minutesPerDay
	}
	if be <= bs {
		be += minutesPerDay
	}
	return as < be && bs < ae
}

func parseHHMM(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	h, err1 := strconv.Atoi(s[:2])
	m, err2 := strconv.Atoi(s[3:])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}
