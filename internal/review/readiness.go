package review

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// ReadinessResult reports whether a day is ready for submission. It is the local
// gate only: approval/remote (S6) and in-flight operations (S2) are separate
// gates.
type ReadinessResult struct {
	Blocked bool     `json:"blocked"`
	Reasons []string `json:"reasons,omitempty"`
}

// Readiness evaluates the local readiness of a day's record. It blocks on a
// confirmed interval with an unconfirmed duration, a not-attended meeting
// represented as work, an unresolved/ambiguous project, an overlap across ALL
// confirmed blocks (including manual↔manual), an incomplete collection without
// acknowledgment, and a non-empty day that has not been reviewed. Only a day
// that is reviewed, flagged empty, AND has no confirmed intervals is ready
// without further checks.
//
// (Go forbids a type and function sharing the name "Readiness", so the result
// type is ReadinessResult.)
func Readiness(r workflow.Record) ReadinessResult {
	if len(r.Confirmed) == 0 && r.Review != nil && r.Review.Reviewed && r.Review.Empty {
		return ReadinessResult{Blocked: false}
	}

	var reasons []string

	// A non-empty day must be reviewed.
	if len(r.Confirmed) > 0 && (r.Review == nil || !r.Review.Reviewed) {
		reasons = append(reasons, "not_reviewed")
	}

	// Unconfirmed duration: a confirmed interval with no usable span.
	for _, ci := range r.Confirmed {
		if strings.TrimSpace(ci.StartTime) == "" || strings.TrimSpace(ci.EndTime) == "" || ci.StartTime == ci.EndTime {
			reasons = append(reasons, "unconfirmed_duration:"+ci.ID)
		}
	}

	// A not-attended meeting represented as work.
	notAttended := map[string]bool{}
	if r.Review != nil {
		for _, ad := range r.Review.Attendance {
			if !ad.Attended {
				notAttended[ad.EvidenceID] = true
			}
		}
	}
	for _, ci := range r.Confirmed {
		for _, id := range ci.EvidenceIDs {
			if notAttended[id] {
				reasons = append(reasons, "not_attended_as_work:"+ci.ID)
				break
			}
		}
	}

	// Unresolved project.
	for _, ci := range r.Confirmed {
		if strings.TrimSpace(ci.Project) == "" {
			reasons = append(reasons, "unresolved_project:"+ci.ID)
		}
	}

	// Overlap across ALL confirmed blocks (incl. manual↔manual).
	if err := proposal.ValidateNoOverlap(confirmedBlocks(r)); err != nil {
		var oe *proposal.OverlapError
		if errors.As(err, &oe) {
			reasons = append(reasons, "overlap:"+oe.FirstID+":"+oe.SecondID)
		} else {
			reasons = append(reasons, "overlap")
		}
	}

	// Incomplete collection without acknowledgment.
	if r.Collection != nil && !r.Collection.Complete {
		ack := map[string]bool{}
		if r.Review != nil {
			for _, s := range r.Review.AcknowledgedSources {
				ack[s] = true
			}
		}
		if err := evidence.RequireAcknowledgment(*r.Collection, ack); err != nil {
			reasons = append(reasons, "incomplete_collection")
		}
	}

	sort.Strings(reasons)
	return ReadinessResult{Blocked: len(reasons) > 0, Reasons: reasons}
}

// confirmedBlocks converts confirmed intervals to time blocks for overlap
// validation. "HH:MM" strings are placed on a fixed UTC date; an end at or before
// the start is treated as crossing midnight.
func confirmedBlocks(r workflow.Record) []proposal.ProposedTimeBlock {
	base := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	var blocks []proposal.ProposedTimeBlock
	for _, ci := range r.Confirmed {
		sm, ok1 := parseHHMM(ci.StartTime)
		em, ok2 := parseHHMM(ci.EndTime)
		if !ok1 || !ok2 {
			continue
		}
		if em <= sm {
			em += minutesPerDay
		}
		blocks = append(blocks, proposal.ProposedTimeBlock{
			ID:      ci.ID,
			StartAt: base.Add(time.Duration(sm) * time.Minute),
			EndAt:   base.Add(time.Duration(em) * time.Minute),
		})
	}
	return blocks
}
