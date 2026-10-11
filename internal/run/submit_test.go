package run

import (
	"errors"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/export"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/submit"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

func TestSubmit_NoActiveApprovalBlocked(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	rec := readyRecord(date)
	if _, err := store.UpdateDay(ctx, date, func(x *workflow.Record) error { *x = rec; return nil }); err != nil {
		t.Fatalf("seed: %v", err)
	}
	remote := newFakeRemote()
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})

	res, err := r.Submit(ctx, date, SubmitOptions{})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked || !hasReason(res.BlockReasons, BlockNoActiveApproval) {
		t.Fatalf("want no_active_approval, got %v", res.BlockReasons)
	}
	if remote.creates != 0 || remote.edits != 0 {
		t.Fatalf("a missing approval must produce ZERO writes")
	}
}

func TestSubmit_ApprovalMismatchBlocked(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	seedApproved(t, store, r, date)

	res, err := r.Submit(ctx, date, SubmitOptions{ApprovalID: "not-the-active-id"})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked || !hasReason(res.BlockReasons, BlockApprovalMismatch) {
		t.Fatalf("want approval_mismatch, got %v", res.BlockReasons)
	}
	if remote.creates != 0 || remote.edits != 0 {
		t.Fatalf("an approval mismatch must produce ZERO writes")
	}
}

func TestSubmit_ExecutesActiveApprovalAndExports(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	exp := &fakeExporter{}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote, Exporter: exp}, Config{})
	seedApproved(t, store, r, date)

	res, err := r.Submit(ctx, date, SubmitOptions{Resolver: clsResolver})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Blocked {
		t.Fatalf("want a run, got blocked %v", res.BlockReasons)
	}
	if remote.creates != 1 {
		t.Fatalf("want one create, got %d", remote.creates)
	}
	if exp.calls != 1 {
		t.Fatalf("want one export, got %d", exp.calls)
	}
	if res.Export.Status != export.StatusSubmitted {
		t.Errorf("want submitted status, got %s", res.Export.Status)
	}
}

func TestSubmit_ExportFailureDoesNotReplay(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	exp := &fakeExporter{err: errors.New("vault write failed")}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote, Exporter: exp}, Config{})
	seedApproved(t, store, r, date)

	if _, err := r.Submit(ctx, date, SubmitOptions{Resolver: clsResolver}); err == nil {
		t.Fatalf("want the export error surfaced")
	}
	if remote.creates != 1 {
		t.Fatalf("want exactly one create (no replay), got %d", remote.creates)
	}
	rec, _ := store.GetDay(ctx, date)
	if len(rec.Outcomes) != 1 || rec.Outcomes[0].Status != workflow.OutcomeConfirmed {
		t.Fatalf("outcomes must be durable, got %+v", rec.Outcomes)
	}

	// Re-exporting succeeds and never replays a write.
	exp.err = nil
	if _, err := r.Export(ctx, date); err != nil {
		t.Fatalf("re-export: %v", err)
	}
	if remote.creates != 1 {
		t.Fatalf("a re-export must not replay a write, got %d", remote.creates)
	}
}

func TestSubmit_DailyIsolation(t *testing.T) {
	store := newStore(t)
	d1, d2 := "2026-10-06", "2026-10-07"
	remote := newFakeRemote()
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})

	a1 := seedApproved(t, store, r, d1)
	a2 := seedApproved(t, store, r, d2)
	if a1.ApprovalID == a2.ApprovalID {
		t.Fatalf("distinct days must have distinct approvals")
	}
	rec1, _ := store.GetDay(ctx, d1)
	if act, ok := workflow.ActiveApproval(rec1, d1); !ok || act.ApprovalID != a1.ApprovalID {
		t.Fatalf("d1 must have its own active approval, got %+v", act)
	}
	if act, _ := workflow.ActiveApproval(rec1, d1); act.ApprovalID == a2.ApprovalID {
		t.Fatalf("d2's approval must never be active for d1")
	}

	res, err := r.Submit(ctx, d1, SubmitOptions{ApprovalID: a2.ApprovalID})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked || !hasReason(res.BlockReasons, BlockApprovalMismatch) {
		t.Fatalf("cross-day approval must be a mismatch, got %v", res.BlockReasons)
	}
	if remote.creates != 0 {
		t.Fatalf("a cross-day mismatch must produce ZERO writes")
	}
}

// S8L1-7: Submit with no remote is refused (offline).
func TestSubmit_OfflineBlocked(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry()}, Config{})
	seedApproved(t, store, r, date)

	res, err := r.Submit(ctx, date, SubmitOptions{})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked || !hasReason(res.BlockReasons, BlockRemoteUnavailable) {
		t.Fatalf("want remote_unavailable, got %v", res.BlockReasons)
	}
}

// An available catalog that lacks a planned classification blocks submission with
// ZERO writes (unverified_classification).
func TestSubmit_UnverifiedClassificationBlocksZeroWrites(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	seedApproved(t, store, r, date)

	cat := gradion.Catalog{Complete: true, Source: "live", Options: []gradion.ClassificationOption{{ID: "other-cls"}}}
	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: &cat})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked || !hasReason(res.BlockReasons, submit.BlockUnverifiedClassification+":c1") {
		t.Fatalf("want %s:c1, got %v", submit.BlockUnverifiedClassification, res.BlockReasons)
	}
	if remote.creates != 0 || remote.edits != 0 {
		t.Fatalf("an unverified classification must produce ZERO writes, got creates=%d edits=%d", remote.creates, remote.edits)
	}
}

// An available catalog that contains the planned classification allows submission.
func TestSubmit_VerifiedClassificationProceeds(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	seedApproved(t, store, r, date)

	cat := gradion.Catalog{Complete: true, Source: "live", Options: []gradion.ClassificationOption{
		{ID: "cls-1", Code: "INTGRADI2610", ActivityType: "internal project"},
	}}
	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: &cat})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Blocked {
		t.Fatalf("a verified classification must not block, got %v", res.BlockReasons)
	}
	if remote.creates != 1 {
		t.Fatalf("want one create, got %d", remote.creates)
	}
}

func TestPreview_OfflineIsZeroRemoteWrites(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	rec := readyRecord(date)
	if _, err := store.UpdateDay(ctx, date, func(x *workflow.Record) error { *x = rec; return nil }); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry()}, Config{})
	pre, err := r.Preview(ctx, date, clsResolver)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if pre.Blocked {
		t.Fatalf("want a plan, got %v", pre.BlockReasons)
	}
	got, _ := store.GetDay(ctx, date)
	if len(got.Plans) != 1 {
		t.Fatalf("preview must persist the plan, got %d", len(got.Plans))
	}
}

// S8L1-7: Resolve adopts an unresolved operation through the explicit decision.
func TestResolve_AdoptUnresolved(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	seedApproved(t, store, r, date)

	key := workflow.DeriveOperationKey(workflow.OperationOutcome{
		Date: date, Action: "create", LocalID: "c1", StartTime: "09:00", EndTime: "10:00",
	})
	if _, err := store.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		workflow.UpsertOutcome(rec, workflow.OperationOutcome{
			OperationKey: key, LocalID: "c1", Action: "create", Status: workflow.OutcomeUncertain,
			Date: date, StartTime: "09:00", EndTime: "10:00",
		})
		return nil
	}); err != nil {
		t.Fatalf("seed unresolved outcome: %v", err)
	}

	remote.entries["r1"] = workflow.RemoteState{
		RemoteID: "r1", Date: date, StartTime: "09:00", EndTime: "10:00",
		ClassificationID: "cls-1", Description: "work", ObservedAt: fixedNow(),
	}

	res, err := r.Resolve(ctx, date, []submit.Resolution{{
		OperationKey: key, Kind: submit.ResolutionAdopt, RemoteID: "r1", LocalID: "c1",
	}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.Applied != 1 || len(res.Unmatched) != 0 {
		t.Fatalf("want 1 applied decision, got %+v", res)
	}
	rec, _ := store.GetDay(ctx, date)
	if len(rec.Outcomes) != 1 || rec.Outcomes[0].Status != workflow.OutcomeConfirmed {
		t.Fatalf("want a confirmed outcome, got %+v", rec.Outcomes)
	}
	if blocked, reasons := submit.RestartGate(*rec); blocked {
		t.Fatalf("the restart gate must clear after resolution, got %v", reasons)
	}
}
