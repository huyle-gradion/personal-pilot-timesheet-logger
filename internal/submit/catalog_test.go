package submit

import (
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

func catWith(options ...gradion.ClassificationOption) gradion.Catalog {
	return gradion.Catalog{Complete: true, Source: "live", Options: options}
}

func TestValidateCatalog_AllPresentPasses(t *testing.T) {
	ops := []workflow.PlanOperation{{LocalID: "c1", ClassificationID: "cls-1"}}
	confirmed := []workflow.ConfirmedInterval{{ID: "c1", ClassificationID: "cls-1", ActivityType: "internal project"}}
	cat := catWith(gradion.ClassificationOption{ID: "cls-1", Code: "INTGRADI2610", ActivityType: "internal project"})
	if reasons := ValidateCatalog(cat, ops, confirmed); len(reasons) != 0 {
		t.Fatalf("want no reasons, got %v", reasons)
	}
}

// A FULL catalog (Query == "" && Complete) PROVES absence: the block reason is
// unknown_classification, not unverified_classification.
func TestValidateCatalog_FullCatalogMissingIDIsUnknown(t *testing.T) {
	ops := []workflow.PlanOperation{{LocalID: "c1", ClassificationID: "unknown"}}
	cat := catWith(gradion.ClassificationOption{ID: "cls-1", ActivityType: "internal project"})
	reasons := ValidateCatalog(cat, ops, nil)
	if len(reasons) != 1 || reasons[0] != BlockUnknownClassification+":c1" {
		t.Fatalf("want %s:c1, got %v", BlockUnknownClassification, reasons)
	}
}

// A FILTERED catalog cannot prove absence: a missing id is only unverified.
func TestValidateCatalog_FilteredCatalogMissingIDIsUnverified(t *testing.T) {
	ops := []workflow.PlanOperation{{LocalID: "c1", ClassificationID: "unknown"}}
	cat := catWith(gradion.ClassificationOption{ID: "cls-1", ActivityType: "internal project"})
	cat.Query = "intern"
	reasons := ValidateCatalog(cat, ops, nil)
	if len(reasons) != 1 || reasons[0] != BlockUnverifiedClassification+":c1" {
		t.Fatalf("want %s:c1, got %v", BlockUnverifiedClassification, reasons)
	}
}

// A PARTIAL catalog cannot prove absence either.
func TestValidateCatalog_PartialCatalogMissingIDIsUnverified(t *testing.T) {
	ops := []workflow.PlanOperation{{LocalID: "c1", ClassificationID: "unknown"}}
	cat := catWith(gradion.ClassificationOption{ID: "cls-1", ActivityType: "internal project"})
	cat.Complete = false
	reasons := ValidateCatalog(cat, ops, nil)
	if len(reasons) != 1 || reasons[0] != BlockUnverifiedClassification+":c1" {
		t.Fatalf("want %s:c1, got %v", BlockUnverifiedClassification, reasons)
	}
}

// An activity-type disagreement is a proven conflict and is never a "unknown"
// absence, even under a full catalog.
func TestClassifyOperations_ActivityTypeMismatchIsNotAbsent(t *testing.T) {
	ops := []workflow.PlanOperation{{LocalID: "c1", ClassificationID: "cls-1"}}
	confirmed := []workflow.ConfirmedInterval{{ID: "c1", ClassificationID: "cls-1", ActivityType: "client project"}}
	cat := catWith(gradion.ClassificationOption{ID: "cls-1", ActivityType: "internal project"})
	issues := ClassifyOperations(cat, ops, confirmed)
	if len(issues) != 1 || issues[0].Absent || issues[0].Reason != BlockUnverifiedClassification {
		t.Fatalf("want one non-absent unverified issue, got %+v", issues)
	}
}

func TestValidateCatalog_EmptyIDBlocks(t *testing.T) {
	ops := []workflow.PlanOperation{{LocalID: "c1", ClassificationID: ""}}
	reasons := ValidateCatalog(catWith(), ops, nil)
	if len(reasons) != 1 {
		t.Fatalf("an empty classification id must block, got %v", reasons)
	}
}

func TestValidateCatalog_EmptyCatalogBlocks(t *testing.T) {
	ops := []workflow.PlanOperation{{LocalID: "c1", ClassificationID: "cls-1"}}
	reasons := ValidateCatalog(gradion.Catalog{Complete: true}, ops, nil)
	if len(reasons) != 1 {
		t.Fatalf("an empty catalog is unverifiable and must block, got %v", reasons)
	}
}

func TestValidateCatalog_ActivityTypeMismatchBlocks(t *testing.T) {
	ops := []workflow.PlanOperation{{LocalID: "c1", ClassificationID: "cls-1"}}
	confirmed := []workflow.ConfirmedInterval{{ID: "c1", ClassificationID: "cls-1", ActivityType: "client project"}}
	cat := catWith(gradion.ClassificationOption{ID: "cls-1", ActivityType: "internal project"})
	reasons := ValidateCatalog(cat, ops, confirmed)
	if len(reasons) != 1 {
		t.Fatalf("an activity-type mismatch must block, got %v", reasons)
	}
}

func TestValidateCatalog_ActivityTypeOmittedIsTolerated(t *testing.T) {
	ops := []workflow.PlanOperation{{LocalID: "c1", ClassificationID: "cls-1"}}
	// Confirmed interval carries no activity type (older records): no mismatch.
	cat := catWith(gradion.ClassificationOption{ID: "cls-1", ActivityType: "internal project"})
	if reasons := ValidateCatalog(cat, ops, nil); len(reasons) != 0 {
		t.Fatalf("a missing local activity type must not block, got %v", reasons)
	}
}
