package submit

import (
	"strings"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

const testDate = "2026-10-06"

var testNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// reviewedRecord is a minimal ready-to-submit day: one reviewed, attended,
// classified interval with a resolved project and a valid span.
func reviewedRecord() workflow.Record {
	return workflow.Record{
		SchemaVersion: workflow.SchemaVersion,
		Date:          testDate,
		Revision:      1,
		Confirmed: []workflow.ConfirmedInterval{
			{
				ID: "c1", Date: testDate, StartTime: "09:00", EndTime: "10:00",
				Project: "Proj", Description: "work", Attendance: true,
				ClassificationID: "cls-1",
			},
		},
		Review: &workflow.ReviewState{Reviewed: true},
	}
}

func completeListing(entries ...workflow.RemoteState) gradion.Listing {
	return gradion.Listing{Complete: true, ObservedAt: testNow, Entries: entries}
}

func remoteState(id, start, end, class, desc string) workflow.RemoteState {
	return workflow.RemoteState{
		RemoteID: id, Date: testDate, StartTime: start, EndTime: end,
		ClassificationID: class, Description: desc, ObservedAt: testNow,
	}
}

func mappingFor(localID string, r workflow.RemoteState) workflow.RemoteMapping {
	return workflow.RemoteMapping{
		LocalID: localID, RemoteID: r.RemoteID, Date: r.Date,
		StartTime: r.StartTime, EndTime: r.EndTime,
		ClassificationID: r.ClassificationID, Description: r.Description,
		LastObserved: r,
	}
}

func alwaysResolver(id string) ClassificationResolver {
	return func(string) (string, bool) { return id, true }
}

func boolp(b bool) *bool { return &b }

func hasReason(reasons []string, want string) bool {
	for _, r := range reasons {
		if r == want || strings.Contains(r, want) {
			return true
		}
	}
	return false
}

func TestBuildPlan_EditPreferredOverCreate(t *testing.T) {
	rec := reviewedRecord()
	observed := remoteState("r1", "09:00", "10:00", "cls-1", "old text")
	rec.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", observed)}

	res, err := BuildPlan(rec, completeListing(observed), nil, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if res.Blocked {
		t.Fatalf("unexpected block: %v", res.Reasons)
	}
	if len(res.Plan.Operations) != 1 {
		t.Fatalf("want 1 operation, got %d: %+v", len(res.Plan.Operations), res.Plan.Operations)
	}
	op := res.Plan.Operations[0]
	if op.Kind != workflow.PlanOpEdit {
		t.Errorf("a mapped interval with differing content must be edited, not created; got %q", op.Kind)
	}
	if op.RemoteID != "r1" {
		t.Errorf("an edit must target the mapped remote, got %q", op.RemoteID)
	}
	if op.ExpectedRemote == nil || op.ExpectedRemote.Description != "old text" {
		t.Errorf("an edit must carry the observed ExpectedRemote, got %+v", op.ExpectedRemote)
	}
	if op.Reason != "timesheet review update "+testDate {
		t.Errorf("an edit must carry the fixed, revision-independent audit reason, got %q", op.Reason)
	}
}

func TestBuildPlan_CreateRequiresCompleteAbsence(t *testing.T) {
	rec := reviewedRecord()
	res, err := BuildPlan(rec, completeListing(), nil, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if res.Blocked {
		t.Fatalf("unexpected block: %v", res.Reasons)
	}
	if len(res.Plan.Operations) != 1 || res.Plan.Operations[0].Kind != workflow.PlanOpCreate {
		t.Fatalf("want one create, got %+v", res.Plan.Operations)
	}
	if op := res.Plan.Operations[0]; op.RemoteID != "" || op.ExpectedRemote != nil {
		t.Errorf("a create must have an empty RemoteID and nil ExpectedRemote, got %+v", op)
	}

	incomplete := completeListing()
	incomplete.Complete = false
	res2, err := BuildPlan(rec, incomplete, nil, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if !res2.Blocked || !hasReason(res2.Reasons, "remote_listing_incomplete") {
		t.Fatalf("an incomplete listing must block a plan, got %v", res2.Reasons)
	}
}

func TestBuildPlan_DuplicateLookingRemoteBlocks(t *testing.T) {
	rec := reviewedRecord()
	overlap := remoteState("rX", "09:30", "10:30", "cls-1", "unrelated")
	res, err := BuildPlan(rec, completeListing(overlap), nil, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if !res.Blocked || !hasReason(res.Reasons, "duplicate_looking_remote:c1") {
		t.Fatalf("an overlapping unmapped remote must block as duplicate_looking_remote, got %v", res.Reasons)
	}
}

func TestBuildPlan_UnresolvedClassificationBlocks(t *testing.T) {
	rec := reviewedRecord()
	rec.Confirmed[0].ClassificationID = ""
	none := func(string) (string, bool) { return "", false }
	res, err := BuildPlan(rec, completeListing(), nil, none, testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if !res.Blocked || !hasReason(res.Reasons, "unresolved_classification:c1") {
		t.Fatalf("want unresolved_classification block, got %v", res.Reasons)
	}

	res2, err := BuildPlan(rec, completeListing(), nil, nil, testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if !res2.Blocked || !hasReason(res2.Reasons, "unresolved_classification:c1") {
		t.Fatalf("a nil resolver must block an unset classification, got %v", res2.Reasons)
	}
}

func TestBuildPlan_ReviewOverrideClassificationHonored(t *testing.T) {
	rec := reviewedRecord()
	rec.Confirmed[0].ClassificationID = "override"
	res, err := BuildPlan(rec, completeListing(), nil, alwaysResolver("resolved"), testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if res.Blocked {
		t.Fatalf("unexpected block: %v", res.Reasons)
	}
	if got := res.Plan.Operations[0].ClassificationID; got != "override" {
		t.Errorf("the explicit review choice must be used, got %q", got)
	}

	// RESOLVER-AUTOFILL REMOVED: an unset classification is NEVER supplied by the
	// resolver. It BLOCKS, and the resolver's value is surfaced as a NON-APPLIED
	// suggestion (Source=config_map).
	rec2 := reviewedRecord()
	rec2.Confirmed[0].ClassificationID = ""
	res2, err := BuildPlan(rec2, completeListing(), nil, alwaysResolver("resolved"), testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if !res2.Blocked || !hasReason(res2.Reasons, "unresolved_classification:c1") {
		t.Fatalf("an unset classification must block, got blocked=%v reasons=%v", res2.Blocked, res2.Reasons)
	}
	if len(res2.Plan.Operations) != 0 {
		t.Fatalf("a blocked plan must have zero operations, got %+v", res2.Plan.Operations)
	}
	if len(res2.Suggestions) != 1 {
		t.Fatalf("want exactly one suggestion, got %+v", res2.Suggestions)
	}
	if s := res2.Suggestions[0]; s.IntervalID != "c1" || s.SuggestedClassificationID != "resolved" || s.Source != "config_map" {
		t.Errorf("unexpected suggestion: %+v", s)
	}
}

func TestBuildPlan_UnresolvedOperationBlocks(t *testing.T) {
	rec := reviewedRecord()
	rec.Outcomes = []workflow.OperationOutcome{{OperationKey: "k", LocalID: "c1", Status: workflow.OutcomeInFlight}}
	res, err := BuildPlan(rec, completeListing(), nil, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if !res.Blocked || !hasReason(res.Reasons, "unresolved_operation") {
		t.Fatalf("an in-flight operation must block a plan, got %v", res.Reasons)
	}
}

func TestBuildPlan_MappedRemoteMissingBlocks(t *testing.T) {
	rec := reviewedRecord()
	rec.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", remoteState("r1", "09:00", "10:00", "cls-1", "work"))}
	res, err := BuildPlan(rec, completeListing(), nil, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if !res.Blocked || !hasReason(res.Reasons, "mapped_remote_missing:c1") {
		t.Fatalf("a mapped remote absent under a complete listing must block, got %v", res.Reasons)
	}
}

func TestBuildPlan_NoOpWhenRemoteAlreadyMatches(t *testing.T) {
	rec := reviewedRecord()
	observed := remoteState("r1", "09:00", "10:00", "cls-1", "work")
	rec.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", observed)}
	res, err := BuildPlan(rec, completeListing(observed), nil, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if res.Blocked {
		t.Fatalf("unexpected block: %v", res.Reasons)
	}
	if len(res.Plan.Operations) != 0 {
		t.Fatalf("an already-matching remote must be a no-op, got %+v", res.Plan.Operations)
	}
}

func TestBuildPlan_NoDeletionPath(t *testing.T) {
	rec := reviewedRecord()
	rec.Confirmed = append(rec.Confirmed, workflow.ConfirmedInterval{
		ID: "c2", Date: testDate, StartTime: "11:00", EndTime: "12:00",
		Project: "Proj", Description: "second", Attendance: true, ClassificationID: "cls-1",
	})
	observed := remoteState("r1", "09:00", "10:00", "cls-1", "old text")
	rec.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", observed)}

	res, err := BuildPlan(rec, completeListing(observed), nil, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if res.Blocked {
		t.Fatalf("unexpected block: %v", res.Reasons)
	}
	kinds := map[workflow.PlanOpKind]int{}
	for _, op := range res.Plan.Operations {
		kinds[op.Kind]++
		if op.Kind != workflow.PlanOpCreate && op.Kind != workflow.PlanOpEdit {
			t.Fatalf("unexpected op kind %q; there is no deletion path", op.Kind)
		}
	}
	if kinds[workflow.PlanOpEdit] != 1 || kinds[workflow.PlanOpCreate] != 1 {
		t.Fatalf("want exactly one edit and one create, got %+v", kinds)
	}
}

func TestBuildPlan_PlanIDIsContentHash(t *testing.T) {
	rec := reviewedRecord()
	res, err := BuildPlan(rec, completeListing(), nil, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if res.Plan.PlanID == "" || workflow.PlanHash(res.Plan) != res.Plan.PlanID {
		t.Fatalf("PlanID must be the plan content hash: id=%q hash=%q", res.Plan.PlanID, workflow.PlanHash(res.Plan))
	}
}

// F1(i): an explicit adoption (RemoteID -> LocalID) is an effective mapping even
// when the adopted remote does NOT overlap the local slot, so it suppresses the
// duplicate create and yields an edit against the adopted remote.
func TestBuildPlan_AdoptionSuppressesCreateAndYieldsEdit(t *testing.T) {
	rec := reviewedRecord()                                                // c1 09:00-10:00, no mapping
	adopted := remoteState("r1", "13:00", "14:00", "cls-1", "remote text") // non-overlapping
	adoptions := []Adoption{{RemoteID: "r1", LocalID: "c1", AdoptedAt: testNow}}

	res, err := BuildPlan(rec, completeListing(adopted), adoptions, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if res.Blocked {
		t.Fatalf("unexpected block: %v", res.Reasons)
	}
	if len(res.Plan.Operations) != 1 {
		t.Fatalf("want exactly one operation, got %+v", res.Plan.Operations)
	}
	op := res.Plan.Operations[0]
	if op.Kind != workflow.PlanOpEdit {
		t.Fatalf("an adoption must suppress the create and yield an edit, got %q", op.Kind)
	}
	if op.RemoteID != "r1" {
		t.Errorf("the edit must target the adopted remote, got %q", op.RemoteID)
	}
	// The adopted remote must not be reported as an unmapped/foreign remote.
	if len(res.Reconcile.Remotes) != 1 || !res.Reconcile.Remotes[0].Mapped {
		t.Errorf("an adopted remote must be reported as mapped, got %+v", res.Reconcile.Remotes)
	}
}

// F1(i) no-op variant: an adoption whose remote already matches the local content
// yields no operation (never a duplicate create).
func TestBuildPlan_AdoptionNoOpWhenAdoptedRemoteMatches(t *testing.T) {
	rec := reviewedRecord()
	adopted := remoteState("r1", "09:00", "10:00", "cls-1", "work")
	res, err := BuildPlan(rec, completeListing(adopted), []Adoption{{RemoteID: "r1", LocalID: "c1", AdoptedAt: testNow}}, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if res.Blocked {
		t.Fatalf("unexpected block: %v", res.Reasons)
	}
	if len(res.Plan.Operations) != 0 {
		t.Fatalf("an adoption that already matches must be a no-op, got %+v", res.Plan.Operations)
	}
}

// F1(ii): a foreign adoption (empty LocalID) maps no local interval and must not
// suppress an unrelated create.
func TestBuildPlan_ForeignAdoptionDoesNotSuppressUnrelatedCreate(t *testing.T) {
	rec := reviewedRecord()                                            // c1 09:00-10:00
	foreign := remoteState("r9", "13:00", "14:00", "cls-9", "foreign") // non-overlapping
	res, err := BuildPlan(rec, completeListing(foreign), []Adoption{{RemoteID: "r9", LocalID: "", AdoptedAt: testNow}}, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if res.Blocked {
		t.Fatalf("unexpected block: %v", res.Reasons)
	}
	if len(res.Plan.Operations) != 1 || res.Plan.Operations[0].Kind != workflow.PlanOpCreate {
		t.Fatalf("a foreign adoption must not suppress an unrelated create, got %+v", res.Plan.Operations)
	}
	if len(res.Reconcile.Remotes) != 1 || res.Reconcile.Remotes[0].Mapped {
		t.Errorf("a foreign adoption must not map a local interval, got %+v", res.Reconcile.Remotes)
	}
}

// F2: the edit reason (and thus PlanID) is stable across a revision bump.
func TestBuildPlan_ReasonAndPlanIDStableAcrossRevisionBump(t *testing.T) {
	rec := reviewedRecord()
	observed := remoteState("r1", "09:00", "10:00", "cls-1", "old text")
	rec.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", observed)}

	first, err := BuildPlan(rec, completeListing(observed), nil, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	bumped := rec
	bumped.Revision = 99
	second, err := BuildPlan(bumped, completeListing(observed), nil, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if len(first.Plan.Operations) != 1 || len(second.Plan.Operations) != 1 {
		t.Fatalf("want one edit each, got %+v / %+v", first.Plan.Operations, second.Plan.Operations)
	}
	if first.Plan.Operations[0].Reason != second.Plan.Operations[0].Reason {
		t.Errorf("the edit reason must be revision-independent: %q vs %q", first.Plan.Operations[0].Reason, second.Plan.Operations[0].Reason)
	}
	if first.Plan.PlanID != second.Plan.PlanID {
		t.Errorf("a revision bump alone must not change the plan hash: %q vs %q", first.Plan.PlanID, second.Plan.PlanID)
	}
}

// T4: a remote conflict surfaced by reconciliation blocks the plan.
func TestBuildPlan_RemoteConflictBlocks(t *testing.T) {
	rec := reviewedRecord()
	rec.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", remoteState("r1", "09:00", "10:00", "cls-1", "work"))}
	changed := remoteState("r1", "09:00", "10:00", "cls-1", "changed remotely")
	res, err := BuildPlan(rec, completeListing(changed), nil, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if !res.Blocked || !hasReason(res.Reasons, "remote_conflict:c1") {
		t.Fatalf("a remote conflict must block the plan, got %v", res.Reasons)
	}
}

// T7: a non-overlapping unmapped remote does not block; a create is emitted.
func TestBuildPlan_CreateWithNonOverlappingUnmappedRemote(t *testing.T) {
	rec := reviewedRecord()
	far := remoteState("rX", "13:00", "14:00", "cls-1", "unrelated")
	res, err := BuildPlan(rec, completeListing(far), nil, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if res.Blocked {
		t.Fatalf("a non-overlapping remote must not block, got %v", res.Reasons)
	}
	if len(res.Plan.Operations) != 1 || res.Plan.Operations[0].Kind != workflow.PlanOpCreate {
		t.Fatalf("want one create, got %+v", res.Plan.Operations)
	}
}
