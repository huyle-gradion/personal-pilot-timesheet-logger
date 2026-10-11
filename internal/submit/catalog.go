package submit

import (
	"strings"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// BlockUnverifiedClassification is a distinct pre-execution block reason: a
// planned operation's classification could not be verified against the catalog
// (missing, empty, or inconsistent with the confirmed interval's activity type).
// The block produces ZERO writes.
const BlockUnverifiedClassification = "unverified_classification"

// ValidateCatalog checks every planned operation's classification against a
// fetched or cached catalog and returns sorted, deduped block reasons (each
// "unverified_classification:<localID>"). It blocks when the operation's
// classification id is empty or absent from the catalog, or when the confirmed
// interval carries an activity type that disagrees with the catalog's derived
// activity type for that classification. An empty catalog is treated as
// unverifiable, never as "no constraint". The payload is never extended: only the
// classification id is sent to log_time/edit_time.
func ValidateCatalog(cat gradion.Catalog, ops []workflow.PlanOperation, confirmed []workflow.ConfirmedInterval) []string {
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

	var reasons []string
	for _, op := range ops {
		id := strings.TrimSpace(op.ClassificationID)
		opt, ok := byID[id]
		if id == "" || !ok {
			reasons = append(reasons, BlockUnverifiedClassification+":"+op.LocalID)
			continue
		}
		declared := activityByLocal[op.LocalID]
		if declared != "" && opt.ActivityType != "" && !strings.EqualFold(declared, opt.ActivityType) {
			reasons = append(reasons, BlockUnverifiedClassification+":"+op.LocalID)
		}
	}
	return dedupSorted(reasons)
}
