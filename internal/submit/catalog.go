package submit

import (
	"strings"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// BlockUnverifiedClassification is a distinct pre-execution block reason: a
// planned operation's classification could not be VERIFIED against the catalog.
// It is used when the catalog cannot prove absence — it is filtered (Query != "")
// or partial (!Complete) — or when the confirmed interval's activity type
// disagrees with the catalog's derived type. The block produces ZERO writes.
const BlockUnverifiedClassification = "unverified_classification"

// BlockUnknownClassification is the stronger block reason for an authoritative
// FULL catalog (Query == "" && Complete) in which the planned classification id
// is genuinely ABSENT or empty: absence from a full catalog PROVES the choice is
// not a valid classification, rather than merely failing to retrieve it. The
// block produces ZERO writes.
const BlockUnknownClassification = "unknown_classification"

// ClassificationIssue is one validation finding for a planned operation.
type ClassificationIssue struct {
	// LocalID is the plan operation's local interval id.
	LocalID string
	// ClassificationID is the operation's chosen classification id (may be empty).
	ClassificationID string
	// Absent is true when the id is empty or not present in the catalog. Such a
	// finding may be RESOLVED by an authoritative targeted fetch when the catalog
	// is not full (see run.Runner.Submit). It is false for an activity-type
	// disagreement, which is a proven conflict that a targeted fetch cannot clear.
	Absent bool
	// Reason is the block reason for the finding.
	Reason string
}

// ClassifyOperations inspects every planned operation's classification against a
// fetched or cached catalog and returns one issue per operation that must block.
// An empty or absent id is "absent": it is BlockUnknownClassification under a
// FULL catalog (proven absent) and BlockUnverifiedClassification otherwise
// (absence cannot be proven). An activity-type disagreement is always
// BlockUnverifiedClassification. The payload is never extended: only the
// classification id is sent to log_time/edit_time.
func ClassifyOperations(cat gradion.Catalog, ops []workflow.PlanOperation, confirmed []workflow.ConfirmedInterval) []ClassificationIssue {
	byID := make(map[string]gradion.ClassificationOption, len(cat.Options))
	for _, o := range cat.Options {
		if id := strings.TrimSpace(o.ID); id != "" {
			byID[id] = o
		}
	}
	activityByLocal := make(map[string]string, len(confirmed))
	for _, ci := range confirmed {
		activityByLocal[ci.ID] = strings.TrimSpace(ci.ActivityType)
	}

	full := cat.IsFullCatalog()
	var issues []ClassificationIssue
	for _, op := range ops {
		id := strings.TrimSpace(op.ClassificationID)
		opt, ok := byID[id]
		if id == "" || !ok {
			reason := BlockUnverifiedClassification
			if full {
				reason = BlockUnknownClassification
			}
			issues = append(issues, ClassificationIssue{
				LocalID: op.LocalID, ClassificationID: id, Absent: true, Reason: reason,
			})
			continue
		}
		declared := activityByLocal[op.LocalID]
		if declared != "" && opt.ActivityType != "" && !strings.EqualFold(declared, opt.ActivityType) {
			issues = append(issues, ClassificationIssue{
				LocalID: op.LocalID, ClassificationID: id, Absent: false, Reason: BlockUnverifiedClassification,
			})
		}
	}
	return issues
}

// IssueReasons renders issues as sorted, deduped "<reason>:<localID>" strings.
func IssueReasons(issues []ClassificationIssue) []string {
	reasons := make([]string, 0, len(issues))
	for _, is := range issues {
		reasons = append(reasons, is.Reason+":"+is.LocalID)
	}
	return dedupSorted(reasons)
}

// ValidateCatalog checks every planned operation's classification against a
// fetched or cached catalog and returns sorted, deduped block reasons. It blocks
// when the operation's classification id is empty or absent (BlockUnknownClassification
// under a full catalog, BlockUnverifiedClassification otherwise) or when the
// confirmed interval carries an activity type that disagrees with the catalog's
// derived activity type. An empty catalog is treated as unverifiable, never as
// "no constraint". The payload is never extended: only the classification id is
// sent to log_time/edit_time.
func ValidateCatalog(cat gradion.Catalog, ops []workflow.PlanOperation, confirmed []workflow.ConfirmedInterval) []string {
	return IssueReasons(ClassifyOperations(cat, ops, confirmed))
}
