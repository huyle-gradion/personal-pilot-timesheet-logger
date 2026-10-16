package submit

import (
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

func TestReconcile_SlotEqualityNeverMatches(t *testing.T) {
	rec := reviewedRecord() // c1 09:00-10:00, no mapping
	same := remoteState("r1", "09:00", "10:00", "cls-1", "work")
	res := Reconcile(rec, completeListing(same), nil, testNow)

	if len(res.Locals) != 1 {
		t.Fatalf("want 1 local decision, got %d", len(res.Locals))
	}
	if res.Locals[0].Kind != MatchCandidate {
		t.Errorf("slot equality must be a candidate, never a match; got %q", res.Locals[0].Kind)
	}
	if len(res.Locals[0].Overlaps) != 1 || res.Locals[0].Overlaps[0] != "r1" {
		t.Errorf("want the overlapping remote surfaced as a candidate, got %v", res.Locals[0].Overlaps)
	}
	if res.Locals[0].RemoteID != "" {
		t.Errorf("a candidate must not be bound to a remote id, got %q", res.Locals[0].RemoteID)
	}
	for _, rd := range res.Remotes {
		if rd.Mapped {
			t.Errorf("an unmapped remote must not be marked mapped: %+v", rd)
		}
	}
}

func TestReconcile_MappedByStableIDOnly(t *testing.T) {
	rec := reviewedRecord()
	observed := remoteState("r1", "09:00", "10:00", "cls-1", "work")
	rec.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", observed)}

	res := Reconcile(rec, completeListing(observed), nil, testNow)
	if res.Locals[0].Kind != MatchMapped {
		t.Errorf("want mapped, got %q", res.Locals[0].Kind)
	}
	if res.Locals[0].RemoteID != "r1" {
		t.Errorf("want remote r1, got %q", res.Locals[0].RemoteID)
	}
	if len(res.Remotes) != 1 || !res.Remotes[0].Mapped || res.Remotes[0].LocalID != "c1" {
		t.Errorf("want remote r1 mapped to c1, got %+v", res.Remotes)
	}
}

func TestReconcile_RemoteContentChangeIsConflict(t *testing.T) {
	rec := reviewedRecord()
	rec.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", remoteState("r1", "09:00", "10:00", "cls-1", "work"))}
	changed := remoteState("r1", "09:00", "10:00", "cls-1", "changed remotely")

	res := Reconcile(rec, completeListing(changed), nil, testNow)
	if res.Locals[0].Kind != MatchConflict {
		t.Errorf("a remote content change must be a conflict, got %q", res.Locals[0].Kind)
	}
	if !hasReason(res.Unresolved, "remote_conflict:c1") {
		t.Errorf("want remote_conflict unresolved, got %v", res.Unresolved)
	}
}

// F1: a legacy mapping whose LastObserved predates ContentObserved (unobserved)
// must NOT be asserted as a conflict — its content is UNKNOWN, not changed —
// whether the fresh observation matches or differs. An unprovable conflict is
// never asserted.
func TestReconcile_UnobservedLegacyBaselineIsNotAConflict(t *testing.T) {
	legacy := remoteState("r1", "09:00", "10:00", "cls-1", "work")
	legacy.ContentObserved = false

	matching := reviewedRecord()
	matching.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", legacy)}
	res := Reconcile(matching, completeListing(remoteState("r1", "09:00", "10:00", "cls-1", "work")), nil, testNow)
	if res.Locals[0].Kind != MatchMapped {
		t.Errorf("a matching unobserved baseline must map, got %q", res.Locals[0].Kind)
	}
	if hasReason(res.Unresolved, "remote_conflict:c1") {
		t.Errorf("an unobserved baseline must not conflict, got %v", res.Unresolved)
	}

	changed := reviewedRecord()
	changed.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", legacy)}
	res = Reconcile(changed, completeListing(remoteState("r1", "09:00", "10:00", "cls-1", "changed remotely")), nil, testNow)
	if res.Locals[0].Kind == MatchConflict {
		t.Errorf("an unobserved baseline must not assert a conflict even when content differs, got %q", res.Locals[0].Kind)
	}
	if hasReason(res.Unresolved, "remote_conflict:c1") {
		t.Errorf("an unobserved baseline must not conflict, got %v", res.Unresolved)
	}
}

// F1 guard: real change detection is preserved — an OBSERVED baseline whose
// content differs from the fresh observation still conflicts.
func TestReconcile_ObservedBaselineChangeStillConflicts(t *testing.T) {
	observed := remoteState("r1", "09:00", "10:00", "cls-1", "work")
	if !observed.ContentObserved {
		t.Fatal("the test helper must produce an observation-derived baseline")
	}
	rec := reviewedRecord()
	rec.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", observed)}

	res := Reconcile(rec, completeListing(remoteState("r1", "09:00", "10:00", "cls-1", "changed remotely")), nil, testNow)
	if res.Locals[0].Kind != MatchConflict || !hasReason(res.Unresolved, "remote_conflict:c1") {
		t.Fatalf("an observed baseline with changed content must still conflict, got kind=%q unresolved=%v", res.Locals[0].Kind, res.Unresolved)
	}

	// PIN — this deliberately passes BEFORE AND AFTER the DEFECT-2 fix. Recent
	// reconciliation compares an observed baseline against a fresh observation
	// and MUST treat a classification-only change as a conflict. Its job is to
	// stop a future "fix" that loosens RemoteContentEqual by dropping
	// ClassificationID: that would silently make this case a match.
	clsOnly := reviewedRecord()
	clsOnly.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", observed)}
	res = Reconcile(clsOnly, completeListing(remoteState("r1", "09:00", "10:00", "cls-2", "work")), nil, testNow)
	if res.Locals[0].Kind != MatchConflict || !hasReason(res.Unresolved, "remote_conflict:c1") {
		t.Fatalf("a classification-only observed change must still conflict, got kind=%q unresolved=%v", res.Locals[0].Kind, res.Unresolved)
	}
}

func TestReconcile_OrphanedMappingUnresolved(t *testing.T) {
	rec := reviewedRecord()
	observed := remoteState("r1", "09:00", "10:00", "cls-1", "work")
	rec.RemoteMappings = []workflow.RemoteMapping{mappingFor("gone", observed)}

	res := Reconcile(rec, completeListing(observed), nil, testNow)
	if !hasReason(res.Unresolved, "orphaned_mapping:gone") {
		t.Errorf("want orphaned_mapping unresolved, got %v", res.Unresolved)
	}
	found := false
	for _, rd := range res.Remotes {
		if rd.RemoteID == "r1" && rd.Orphaned {
			found = true
		}
	}
	if !found {
		t.Errorf("want remote r1 flagged orphaned, got %+v", res.Remotes)
	}
}

func TestReconcile_MissingMappedRemoteUnresolved(t *testing.T) {
	rec := reviewedRecord()
	rec.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", remoteState("r1", "09:00", "10:00", "cls-1", "work"))}
	res := Reconcile(rec, completeListing(), nil, testNow)
	if !hasReason(res.Unresolved, "mapped_remote_missing:c1") {
		t.Errorf("want mapped_remote_missing unresolved, got %v", res.Unresolved)
	}
}

func TestReconcile_AdoptionReferencingUnobservedRemoteUnresolved(t *testing.T) {
	rec := reviewedRecord()
	res := Reconcile(rec, completeListing(), []Adoption{{RemoteID: "ghost", LocalID: "c1", AdoptedAt: testNow}}, testNow)
	if !hasReason(res.Unresolved, "adoption_missing_remote:ghost") {
		t.Errorf("want adoption_missing_remote unresolved, got %v", res.Unresolved)
	}
}

func TestApplyReconcile_AdoptionPopulatesMapping(t *testing.T) {
	rec := reviewedRecord()
	observed := remoteState("r1", "09:00", "10:00", "cls-1", "work")
	adoptions := []Adoption{{RemoteID: "r1", LocalID: "c1", AdoptedAt: testNow}}

	res := Reconcile(rec, completeListing(observed), adoptions, testNow)
	if err := ApplyReconcile(&rec, res, testNow); err != nil {
		t.Fatalf("ApplyReconcile: %v", err)
	}
	if len(rec.RemoteMappings) != 1 {
		t.Fatalf("an adoption must create exactly one mapping, got %d", len(rec.RemoteMappings))
	}
	if m := rec.RemoteMappings[0]; m.LocalID != "c1" || m.RemoteID != "r1" {
		t.Errorf("want mapping c1->r1, got %s->%s", m.LocalID, m.RemoteID)
	}

	res2 := Reconcile(rec, completeListing(observed), nil, testNow)
	if res2.Locals[0].Kind != MatchMapped {
		t.Errorf("an adoption must establish a stable match, got %q", res2.Locals[0].Kind)
	}
}

func TestApplyReconcile_ForeignAdoptionCreatesNoMapping(t *testing.T) {
	rec := reviewedRecord()
	observed := remoteState("r9", "13:00", "14:00", "cls-9", "foreign")
	res := Reconcile(rec, completeListing(observed), []Adoption{{RemoteID: "r9", LocalID: "", AdoptedAt: testNow}}, testNow)

	if err := ApplyReconcile(&rec, res, testNow); err != nil {
		t.Fatalf("ApplyReconcile: %v", err)
	}
	if len(rec.RemoteMappings) != 0 {
		t.Errorf("a foreign adoption must not fabricate a local mapping, got %+v", rec.RemoteMappings)
	}
	if len(res.Remotes) != 1 || !res.Remotes[0].Adopted {
		t.Errorf("the foreign remote must still be acknowledged as adopted, got %+v", res.Remotes)
	}
}

func TestApplyReconcile_RefreshesLastObservedAndNeverDeletes(t *testing.T) {
	rec := reviewedRecord()
	rec.RemoteMappings = []workflow.RemoteMapping{
		mappingFor("c1", remoteState("r1", "09:00", "10:00", "cls-1", "work")),
		mappingFor("gone", remoteState("r2", "15:00", "16:00", "cls-2", "orphan")),
	}
	changed := remoteState("r1", "09:00", "10:00", "cls-1", "changed remotely")

	res := Reconcile(rec, completeListing(changed), nil, testNow)
	if err := ApplyReconcile(&rec, res, testNow); err != nil {
		t.Fatalf("ApplyReconcile: %v", err)
	}
	if len(rec.RemoteMappings) != 2 {
		t.Fatalf("ApplyReconcile must never delete mappings, got %d", len(rec.RemoteMappings))
	}
	refreshed := false
	for _, m := range rec.RemoteMappings {
		if m.LocalID == "c1" && m.LastObserved.Description == "changed remotely" {
			refreshed = true
		}
	}
	if !refreshed {
		t.Errorf("a re-observed mapped entry must refresh LastObserved: %+v", rec.RemoteMappings)
	}
	if !hasReason(res.Unresolved, "orphaned_mapping:gone") {
		t.Errorf("want orphaned_mapping unresolved, got %v", res.Unresolved)
	}
}

func TestApplyReconcile_NilRecordIsSafe(t *testing.T) {
	if err := ApplyReconcile(nil, ReconcileResult{}, testNow); err != nil {
		t.Fatalf("ApplyReconcile(nil) must be a no-op, got %v", err)
	}
}

// T3: a bare read/skip (no adoption) must not populate a mapping.
func TestApplyReconcile_NoAdoptionsAddsNoMapping(t *testing.T) {
	rec := reviewedRecord()
	observed := remoteState("r1", "09:00", "10:00", "cls-1", "work")
	res := Reconcile(rec, completeListing(observed), nil, testNow)

	if len(res.Remotes) != 1 || res.Remotes[0].Adopted {
		t.Fatalf("a bare read must not mark any remote adopted: %+v", res.Remotes)
	}
	if err := ApplyReconcile(&rec, res, testNow); err != nil {
		t.Fatalf("ApplyReconcile: %v", err)
	}
	if len(rec.RemoteMappings) != 0 {
		t.Fatalf("a bare read/skip must not create a mapping, got %+v", rec.RemoteMappings)
	}
}

// F4: Reconcile must not fabricate a local now for an UNOBSERVED listing — the
// same class of fabrication submit.observedAt was fixed for. The now fallback is
// for an OBSERVED listing with no timestamp only.
func TestReconcile_UnobservedHasNoObservedAt(t *testing.T) {
	rec := reviewedRecord()

	unobserved := Reconcile(rec, gradion.Listing{}, nil, testNow)
	if !unobserved.ObservedAt.IsZero() {
		t.Errorf("an unobserved listing must yield a ZERO ObservedAt, got %v", unobserved.ObservedAt)
	}

	// An OBSERVED listing with a real timestamp uses it.
	stamped := Reconcile(rec, gradion.Listing{Observed: true, ObservedAt: testNow.Add(-time.Hour)}, nil, testNow)
	if !stamped.ObservedAt.Equal(testNow.Add(-time.Hour)) {
		t.Errorf("an observed listing's own timestamp must win, got %v", stamped.ObservedAt)
	}

	// The defensive fallback survives only for an OBSERVED listing with no stamp.
	fallback := Reconcile(rec, gradion.Listing{Observed: true}, nil, testNow)
	if !fallback.ObservedAt.Equal(testNow) {
		t.Errorf("an observed listing without a timestamp must fall back to now, got %v", fallback.ObservedAt)
	}
}
