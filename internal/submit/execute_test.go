package submit

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

func TestExecute_HappyCreateAndEdit(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	rec := withSecondInterval(reviewedRecord(), "c2", "11:00", "12:00", "second")
	fake := newFakeRemote()
	existing := remoteState("r1", "09:00", "10:00", "cls-1", "old text")
	fake.setEntry(existing)
	rec.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", existing)}

	_, approval := seedApprovedDay(t, store, rec, completeListing(existing), alwaysResolver("cls-1"))

	result, err := Execute(ctx, store, fake, fake, testDate, approval, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Blocked || result.Partial {
		t.Fatalf("want a clean run, got blocked=%v partial=%v reasons=%v", result.Blocked, result.Partial, result.BlockReasons)
	}
	if fake.editCount() != 1 || fake.createCount() != 1 {
		t.Fatalf("want one edit and one create, got edits=%d creates=%d", fake.editCount(), fake.createCount())
	}
	if len(result.Outcomes) != 2 {
		t.Fatalf("want 2 outcomes, got %d", len(result.Outcomes))
	}
	for _, o := range result.Outcomes {
		if o.Status != workflow.OutcomeConfirmed || !o.Resolved {
			t.Errorf("want confirmed+resolved, got %s resolved=%v", o.Status, o.Resolved)
		}
	}
	if result.RemainingPlan == nil {
		t.Fatalf("want a remaining plan")
	}
	if len(result.RemainingPlan.Operations) != 0 {
		t.Errorf("confirmed operations must become no-ops on rebuild, got %+v", result.RemainingPlan.Operations)
	}
	if got := loadRecord(t, store, testDate); len(got.RemoteMappings) != 2 {
		t.Errorf("want 2 mappings after the run, got %+v", got.RemoteMappings)
	}
}

func TestExecute_RemoteChangeStopsAffectedWrites(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	rec := reviewedRecord()
	fake := newFakeRemote()
	existing := remoteState("r1", "09:00", "10:00", "cls-1", "old text")
	fake.setEntry(existing)
	rec.RemoteMappings = []workflow.RemoteMapping{mappingFor("c1", existing)}

	_, approval := seedApprovedDay(t, store, rec, completeListing(existing), alwaysResolver("cls-1"))

	// The remote changed AFTER approval, before execution.
	fake.setEntry(remoteState("r1", "09:00", "10:00", "cls-1", "changed remotely"))

	result, err := Execute(ctx, store, fake, fake, testDate, approval, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Blocked || !hasReason(result.BlockReasons, "approval_stale_remote") {
		t.Fatalf("want an approval_stale_remote block, got %v", result.BlockReasons)
	}
	if fake.createCount() != 0 || fake.editCount() != 0 {
		t.Fatalf("a stale approval must produce ZERO writes, got creates=%d edits=%d", fake.createCount(), fake.editCount())
	}
	got := loadRecord(t, store, testDate)
	invalidated := false
	for _, ap := range got.Approvals {
		if ap.ApprovalID == approval.ApprovalID && ap.InvalidatedAt != nil {
			invalidated = true
		}
	}
	if !invalidated {
		t.Errorf("a stale approval must be invalidated on the record")
	}
}

func TestExecute_UncertainWriteBlocksAndStops(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	rec := withSecondInterval(reviewedRecord(), "c2", "11:00", "12:00", "second")
	fake := newFakeRemote()
	fake.onCreate = func(gradion.CreateRequest) (gradion.WriteResult, error) {
		return gradion.WriteResult{Status: gradion.WriteUncertain, ErrorCode: gradion.CodeWriteUncertain}, nil
	}

	_, approval := seedApprovedDay(t, store, rec, completeListing(), alwaysResolver("cls-1"))

	result, err := Execute(ctx, store, fake, fake, testDate, approval, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Partial {
		t.Fatalf("an uncertain write must mark the run partial")
	}
	if fake.createCount() != 1 {
		t.Fatalf("an uncertain write must STOP the run (no further dispatch), got %d creates", fake.createCount())
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Status != workflow.OutcomeUncertain {
		t.Fatalf("want one uncertain outcome, got %+v", result.Outcomes)
	}
	if result.RemainingPlan != nil {
		t.Errorf("an unresolved operation must block the rebuild, got %+v", result.RemainingPlan)
	}
	if blocked, reasons := RestartGate(loadRecord(t, store, testDate)); !blocked {
		t.Fatalf("RestartGate must block on the uncertain operation, got %v", reasons)
	}
}

func TestExecute_PartialFailureContinuesAndPlansRemaining(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	rec := reviewedRecord()
	rec = withSecondInterval(rec, "c2", "11:00", "12:00", "second")
	rec = withSecondInterval(rec, "c3", "13:00", "14:00", "third")

	fake := newFakeRemote()
	n := 0
	fake.onCreate = func(req gradion.CreateRequest) (gradion.WriteResult, error) {
		n++
		if n == 2 {
			// The second create is definitively rejected (e.g. auth/validation).
			return gradion.WriteResult{Status: gradion.WriteFailed, ErrorCode: gradion.CodeWriteFailed}, nil
		}
		st := workflow.RemoteState{
			RemoteID: fmt.Sprintf("new-%d", n), Date: req.Date, StartTime: req.StartTime,
			EndTime: req.EndTime, ClassificationID: req.ClassificationID,
			Description: req.Description, Task: req.Task, Billable: req.Billable, ObservedAt: testNow,
		}
		return gradion.WriteResult{Status: gradion.WriteConfirmed, Entry: &st}, nil
	}

	_, approval := seedApprovedDay(t, store, rec, completeListing(), alwaysResolver("cls-1"))

	result, err := Execute(ctx, store, fake, fake, testDate, approval, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Partial {
		t.Fatalf("a definitive failure must mark the run partial")
	}
	if fake.createCount() != 3 {
		t.Fatalf("a definitive failure must continue to the remaining ops, got %d creates", fake.createCount())
	}
	if result.RemainingPlan == nil {
		t.Fatalf("want a remaining plan for the failed op")
	}
	ops := result.RemainingPlan.Operations
	if len(ops) != 1 || ops[0].LocalID != "c2" || ops[0].Kind != workflow.PlanOpCreate {
		t.Fatalf("want exactly the failed op re-planned (no replay of confirmed ops), got %+v", ops)
	}
}

func TestExecute_PreservesOperationIdentity(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	rec := reviewedRecord()
	fake := newFakeRemote()
	_, approval := seedApprovedDay(t, store, rec, completeListing(), alwaysResolver("cls-1"))

	if _, err := Execute(ctx, store, fake, fake, testDate, approval, alwaysResolver("cls-1"), testNow); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := loadRecord(t, store, testDate)
	if len(got.Outcomes) != 1 {
		t.Fatalf("the intent and its outcome must share one ledger row, got %d", len(got.Outcomes))
	}
	o := got.Outcomes[0]
	if o.LocalID != "c1" {
		t.Errorf("want local id c1, got %q", o.LocalID)
	}
	want := workflow.DeriveOperationKey(workflow.OperationOutcome{
		Date: testDate, Action: string(workflow.PlanOpCreate), RemoteID: "", LocalID: "c1",
		StartTime: "09:00", EndTime: "10:00",
	})
	if o.OperationKey != want {
		t.Errorf("the operation key must be stable from the pre-write plan: got %q want %q", o.OperationKey, want)
	}
}

func TestExecute_ConcurrentSessionBusy(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	rec := reviewedRecord()
	fake := newFakeRemote()
	_, approval := seedApprovedDay(t, store, rec, completeListing(), alwaysResolver("cls-1"))

	held, err := store.BeginDaySession(ctx, testDate)
	if err != nil {
		t.Fatalf("BeginDaySession: %v", err)
	}
	defer func() { _ = held.Close() }()

	result, err := Execute(ctx, store, fake, fake, testDate, approval, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Blocked || !hasReason(result.BlockReasons, "writer_busy") {
		t.Fatalf("want a writer_busy block, got %v", result.BlockReasons)
	}
	if fake.createCount() != 0 || fake.editCount() != 0 {
		t.Fatalf("a busy writer must produce ZERO writes, got creates=%d edits=%d", fake.createCount(), fake.editCount())
	}
}

// failingIntentStore wraps a Store so the FIRST Session.Update fails, standing in
// for a durability failure on the pre-write intent write (the workflow package's
// syncWorkflowDir seam is unexported and not reachable from this package).
type failingIntentStore struct {
	workflow.Store
	failed bool
}

func (s *failingIntentStore) BeginDaySession(ctx context.Context, date string) (workflow.Session, error) {
	inner, err := s.Store.BeginDaySession(ctx, date)
	if err != nil {
		return nil, err
	}
	return &failingIntentSession{Session: inner, owner: s}, nil
}

type failingIntentSession struct {
	workflow.Session
	owner *failingIntentStore
}

func (s *failingIntentSession) Update(ctx context.Context, mutate func(*workflow.Record) error) (*workflow.Record, error) {
	if !s.owner.failed {
		s.owner.failed = true
		return nil, errors.New("injected durability failure: workflow directory sync failed")
	}
	return s.Session.Update(ctx, mutate)
}

func TestExecute_IntentPersistenceFailureSurfaces(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	rec := reviewedRecord()
	fake := newFakeRemote()
	_, approval := seedApprovedDay(t, store, rec, completeListing(), alwaysResolver("cls-1"))

	wrapped := &failingIntentStore{Store: store}
	if _, err := Execute(ctx, wrapped, fake, fake, testDate, approval, alwaysResolver("cls-1"), testNow); err == nil {
		t.Fatalf("a persistence failure on the intent must surface as an error")
	}
	if fake.createCount() != 0 || fake.editCount() != 0 {
		t.Fatalf("no dispatch may happen when the intent cannot be persisted, got creates=%d edits=%d", fake.createCount(), fake.editCount())
	}
}

func TestExecute_NilDependencies(t *testing.T) {
	store := newTestStore(t)
	if _, err := Execute(context.Background(), nil, newFakeRemote(), newFakeRemote(), testDate, workflow.Approval{}, nil, testNow); err == nil {
		t.Fatalf("nil store must be rejected")
	}
	if _, err := Execute(context.Background(), store, nil, newFakeRemote(), testDate, workflow.Approval{}, nil, testNow); err == nil {
		t.Fatalf("nil observer must be rejected")
	}
}

func TestExecute_RestartGateBlocksUnresolvedRecord(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	rec := reviewedRecord()
	fake := newFakeRemote()
	_, approval := seedApprovedDay(t, store, rec, completeListing(), alwaysResolver("cls-1"))

	// A prior run left a durable in_flight intent and an uncertain outcome.
	if _, err := store.UpdateDay(ctx, testDate, func(r *workflow.Record) error {
		workflow.UpsertIntent(r, workflow.OperationOutcome{OperationKey: "prior", LocalID: "c1", Status: workflow.OutcomeInFlight})
		workflow.UpsertOutcome(r, workflow.OperationOutcome{OperationKey: "prior-uncertain", LocalID: "c1", Status: workflow.OutcomeUncertain})
		return nil
	}); err != nil {
		t.Fatalf("seed unresolved op: %v", err)
	}

	result, err := Execute(ctx, store, fake, fake, testDate, approval, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Blocked || !hasReason(result.BlockReasons, "restart_gate") {
		t.Fatalf("want a restart_gate block, got %v", result.BlockReasons)
	}
	// F7: the reason set is deterministic (sorted and deduped).
	if !reflect.DeepEqual(result.BlockReasons, dedupSorted(result.BlockReasons)) {
		t.Errorf("initial gate reasons must be sorted and deduped, got %v", result.BlockReasons)
	}
	if fake.createCount() != 0 || fake.editCount() != 0 {
		t.Fatalf("an unresolved record must produce ZERO writes, got creates=%d edits=%d", fake.createCount(), fake.editCount())
	}
}

// F2: an advisory rebuild failure must not be reported as a top-level error.
func TestExecute_AdvisoryRebuildFailureIsWarningNotError(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	rec := reviewedRecord()
	fake := newFakeRemote()
	_, approval := seedApprovedDay(t, store, rec, completeListing(), alwaysResolver("cls-1"))

	// Call 1 = the start re-fetch (succeeds); call 2 = the advisory rebuild re-observe (fails).
	fake.listErrAt = 2

	result, err := Execute(ctx, store, fake, fake, testDate, approval, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("an advisory rebuild failure must NOT be a top-level error, got %v", err)
	}
	if len(result.Outcomes) != 1 || result.Outcomes[0].Status != workflow.OutcomeConfirmed {
		t.Fatalf("the run must still report its confirmed outcome, got %+v", result.Outcomes)
	}
	if !hasReason(result.RebuildWarnings, RebuildReobserveFailed) {
		t.Fatalf("want a rebuild_reobserve_failed warning, got %v", result.RebuildWarnings)
	}
	if result.RemainingPlan != nil {
		t.Errorf("a failed rebuild must not yield a remaining plan")
	}
	if got := loadRecord(t, store, testDate); len(got.Outcomes) != 1 {
		t.Errorf("the outcome must be durably persisted, got %+v", got.Outcomes)
	}
}

// F3: a blocked rebuild's reasons must be observable.
func TestExecute_BlockedRebuildReasonsAreObservable(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	rec := reviewedRecord()
	fake := newFakeRemote()
	_, approval := seedApprovedDay(t, store, rec, completeListing(), alwaysResolver("cls-1"))

	// Call 1 = the start re-fetch (complete); call 2 = the rebuild re-observe (incomplete).
	fake.incompleteAt = 2

	result, err := Execute(ctx, store, fake, fake, testDate, approval, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.RemainingPlan != nil {
		t.Errorf("a blocked rebuild must not yield a remaining plan")
	}
	if !hasReason(result.RebuildWarnings, "rebuild_blocked:remote_listing_incomplete") {
		t.Fatalf("want the blocked rebuild's reasons surfaced, got %v", result.RebuildWarnings)
	}
}

// F6: end-to-end restart cycle. An uncertain create blocks the restart gate; an
// explicit adoption resolves it; the next run succeeds with NO duplicate create.
func TestExecute_RestartCycleUncertainThenAdoptNoDuplicate(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)
	rec := reviewedRecord()
	fake := newFakeRemote()
	fake.onCreate = func(gradion.CreateRequest) (gradion.WriteResult, error) {
		return gradion.WriteResult{Status: gradion.WriteUncertain, ErrorCode: gradion.CodeWriteUncertain}, nil
	}
	_, approval1 := seedApprovedDay(t, store, rec, completeListing(), alwaysResolver("cls-1"))

	run1, err := Execute(ctx, store, fake, fake, testDate, approval1, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("Execute run 1: %v", err)
	}
	if !run1.Partial || fake.createCount() != 1 {
		t.Fatalf("want one uncertain create, got partial=%v creates=%d", run1.Partial, fake.createCount())
	}

	// The remote actually holds the entry: the write applied but its response was lost.
	adopted := remoteState("r1", "09:00", "10:00", "cls-1", "work")
	fake.setEntry(adopted)

	if blocked, _ := RestartGate(loadRecord(t, store, testDate)); !blocked {
		t.Fatalf("the restart gate must block after an uncertain write")
	}

	// Resolve by EXPLICIT adoption.
	afterRun1 := loadRecord(t, store, testDate)
	key := createKey("c1", "09:00", "10:00")
	if err := ResolveUnresolved(&afterRun1, completeListing(adopted), []Resolution{{OperationKey: key, Kind: ResolutionAdopt, RemoteID: "r1", LocalID: "c1"}}, testNow); err != nil {
		t.Fatalf("ResolveUnresolved: %v", err)
	}
	if _, err := store.UpdateDay(ctx, testDate, func(r *workflow.Record) error { *r = afterRun1; return nil }); err != nil {
		t.Fatalf("persist resolution: %v", err)
	}
	if blocked, reasons := RestartGate(loadRecord(t, store, testDate)); blocked {
		t.Fatalf("the restart gate must clear after resolution, got %v", reasons)
	}

	// Build and approve the remaining plan: the adopted create is now a no-op.
	rec2 := loadRecord(t, store, testDate)
	built, err := BuildPlan(rec2, completeListing(adopted), nil, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if built.Blocked {
		t.Fatalf("remaining plan blocked: %v", built.Reasons)
	}
	if len(built.Plan.Operations) != 0 {
		t.Fatalf("the adopted create must become a no-op, got %+v", built.Plan.Operations)
	}
	workflow.UpsertPlan(&rec2, built.Plan)
	approval2, err := Approve(&rec2, built.Plan, testNow)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if _, err := store.UpdateDay(ctx, testDate, func(r *workflow.Record) error { *r = rec2; return nil }); err != nil {
		t.Fatalf("persist remaining plan: %v", err)
	}

	// Run 2 must NOT duplicate the create.
	run2, err := Execute(ctx, store, fake, fake, testDate, approval2, alwaysResolver("cls-1"), testNow)
	if err != nil {
		t.Fatalf("Execute run 2: %v", err)
	}
	if fake.createCount() != 1 {
		t.Fatalf("a restart cycle must not duplicate the create, got %d creates", fake.createCount())
	}
	if len(run2.Outcomes) != 0 {
		t.Errorf("run 2 must dispatch nothing, got %+v", run2.Outcomes)
	}
}
