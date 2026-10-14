package run

import (
	"errors"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/export"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/submit"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// clsOpt is a fully-populated catalog row matching readyRecord's stored
// classification metadata (id-derived code/name + a fixed derived activity type),
// so the fresh-live proof can validate it. A row missing any of id/name/code/
// activity type fails the proof closed.
func clsOpt(id string) gradion.ClassificationOption {
	return gradion.ClassificationOption{ID: id, Name: "Class " + id, Code: "CODE-" + id, ActivityType: "internal project"}
}

// fullCatalogWith returns a fresh, FULL (unfiltered, complete) live catalog that
// contains the given classification ids. A live catalog is always fresh.
func fullCatalogWith(ids ...string) *gradion.Catalog {
	cat := gradion.Catalog{Complete: true, Source: "live"}
	for _, id := range ids {
		cat.Options = append(cat.Options, clsOpt(id))
	}
	return &cat
}

// partialCatalog returns a fresh but FILTERED/PARTIAL catalog for the given query,
// which cannot prove absence. Its options are the given ids.
func partialCatalog(query string, ids ...string) *gradion.Catalog {
	cat := gradion.Catalog{Complete: false, Source: "live", Query: query}
	for _, id := range ids {
		cat.Options = append(cat.Options, clsOpt(id))
	}
	return &cat
}

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

	res, err := r.Submit(ctx, date, SubmitOptions{Resolver: clsResolver, Catalog: fullCatalogWith("cls-1")})
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

	if _, err := r.Submit(ctx, date, SubmitOptions{Resolver: clsResolver, Catalog: fullCatalogWith("cls-1")}); err == nil {
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

// A FULL catalog that genuinely lacks a planned classification PROVES it invalid:
// the block is unknown_classification, with ZERO writes.
func TestSubmit_FullCatalogMissingClassificationBlocksZeroWrites(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	seedApproved(t, store, r, date)

	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: fullCatalogWith("other-cls")})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked || !hasReason(res.BlockReasons, submit.BlockUnknownClassification+":c1") {
		t.Fatalf("want %s:c1, got %v", submit.BlockUnknownClassification, res.BlockReasons)
	}
	if remote.creates != 0 || remote.edits != 0 {
		t.Fatalf("an unknown classification must produce ZERO writes, got creates=%d edits=%d", remote.creates, remote.edits)
	}
	// A full catalog is authoritative: NO targeted read is performed.
	if len(remote.catalogCalls) != 0 {
		t.Fatalf("a full catalog must not trigger a targeted read, got %v", remote.catalogCalls)
	}
}

// W1: a remote with a NIL catalog fails closed with missing_catalog BEFORE any
// write — the closed bypass.
func TestSubmit_MissingCatalogBlocksZeroWrites(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	seedApproved(t, store, r, date)

	res, err := r.Submit(ctx, date, SubmitOptions{Resolver: clsResolver})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked || !hasReason(res.BlockReasons, BlockMissingCatalog) {
		t.Fatalf("want %s, got %v", BlockMissingCatalog, res.BlockReasons)
	}
	if remote.creates != 0 || remote.edits != 0 {
		t.Fatalf("a missing catalog must produce ZERO writes, got creates=%d edits=%d", remote.creates, remote.edits)
	}
}

// W1: a STALE catalog fails closed with stale_catalog BEFORE any write.
func TestSubmit_StaleCatalogBlocksZeroWrites(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	seedApproved(t, store, r, date)

	stale := gradion.Catalog{
		Complete: true, Source: "cache", ObservedAt: fixedNow().Add(-48 * time.Hour),
		Options: []gradion.ClassificationOption{{ID: "cls-1"}},
	}
	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: &stale})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked || !hasReason(res.BlockReasons, BlockStaleCatalog) {
		t.Fatalf("want %s, got %v", BlockStaleCatalog, res.BlockReasons)
	}
	if remote.creates != 0 || remote.edits != 0 {
		t.Fatalf("a stale catalog must produce ZERO writes, got creates=%d edits=%d", remote.creates, remote.edits)
	}
}

// W1: a PARTIAL/FILTERED catalog cannot prove absence; a TARGETED read that finds
// the id clears the choice and the submission proceeds.
func TestSubmit_PartialCatalogTargetedReadResolvesChoice(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	remote.catalogByQuery = func(query string) (gradion.Catalog, error) {
		return gradion.Catalog{Complete: true, Query: query, Source: "live", ObservedAt: fixedNow(),
			Options: []gradion.ClassificationOption{clsOpt("cls-1")}}, nil
	}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	seedApproved(t, store, r, date)

	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: partialCatalog("intern", "other-cls")})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Blocked {
		t.Fatalf("a targeted read that finds the id must clear the block, got %v", res.BlockReasons)
	}
	if remote.creates != 1 {
		t.Fatalf("want one create, got %d", remote.creates)
	}
	if len(remote.catalogCalls) != 1 || remote.catalogCalls[0] != "CODE-cls-1" {
		t.Fatalf("want one targeted read by the recorded code, got %v", remote.catalogCalls)
	}
}

// W1: a targeted read that does NOT find the id fails closed.
func TestSubmit_PartialCatalogTargetedReadNotFoundBlocks(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	remote.catalogByQuery = func(query string) (gradion.Catalog, error) {
		return gradion.Catalog{Complete: true, Query: query, Source: "live", ObservedAt: fixedNow(),
			Options: []gradion.ClassificationOption{clsOpt("something-else")}}, nil
	}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	seedApproved(t, store, r, date)

	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: partialCatalog("intern", "other-cls")})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked || !hasReason(res.BlockReasons, submit.BlockUnverifiedClassification+":c1") {
		t.Fatalf("want %s:c1, got %v", submit.BlockUnverifiedClassification, res.BlockReasons)
	}
	if remote.creates != 0 || remote.edits != 0 {
		t.Fatalf("a not-found targeted read must produce ZERO writes, got creates=%d edits=%d", remote.creates, remote.edits)
	}
}

// W1: a targeted read that ERRORS fails closed.
func TestSubmit_PartialCatalogTargetedReadErrorBlocks(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	remote.catalogByQuery = func(query string) (gradion.Catalog, error) {
		return gradion.Catalog{}, errors.New("targeted read failed")
	}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	seedApproved(t, store, r, date)

	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: partialCatalog("intern", "other-cls")})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked || !hasReason(res.BlockReasons, submit.BlockUnverifiedClassification+":c1") {
		t.Fatalf("want %s:c1, got %v", submit.BlockUnverifiedClassification, res.BlockReasons)
	}
	if remote.creates != 0 {
		t.Fatalf("an errored targeted read must produce ZERO writes, got %d", remote.creates)
	}
}

// seedApprovedWithActivityType writes a ready record whose single confirmed
// interval carries an activity type, then previews and approves it.
func seedApprovedWithActivityType(t *testing.T, store workflow.Store, r *Runner, date, activityType string) workflow.Approval {
	t.Helper()
	rec := readyRecord(date)
	rec.Confirmed[0].ActivityType = activityType
	if _, err := store.UpdateDay(ctx, date, func(x *workflow.Record) error { *x = rec; return nil }); err != nil {
		t.Fatalf("seed: %v", err)
	}
	pre, err := r.Preview(ctx, date, clsResolver)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if pre.Blocked {
		t.Fatalf("unexpected preview block: %v", pre.BlockReasons)
	}
	approval, err := r.Approve(ctx, date, pre.Plan)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	return approval
}

// F1: a targeted read that FINDS the id but returns a DIFFERENT derived activity
// type must still block (unverified_classification) with ZERO writes.
func TestSubmit_PartialCatalogTargetedReadActivityTypeMismatchBlocks(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	remote.catalogByQuery = func(query string) (gradion.Catalog, error) {
		o := clsOpt("cls-1")
		o.ActivityType = "client project"
		return gradion.Catalog{Complete: true, Query: query, Source: "live", ObservedAt: fixedNow(),
			Options: []gradion.ClassificationOption{o}}, nil
	}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	seedApprovedWithActivityType(t, store, r, date, "internal project")

	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: partialCatalog("intern", "other-cls")})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked || !hasReason(res.BlockReasons, submit.BlockUnverifiedClassification+":c1") {
		t.Fatalf("a targeted activity-type mismatch must block with %s:c1, got %v", submit.BlockUnverifiedClassification, res.BlockReasons)
	}
	if remote.creates != 0 || remote.edits != 0 {
		t.Fatalf("a targeted activity-type mismatch must produce ZERO writes, got creates=%d edits=%d", remote.creates, remote.edits)
	}
}

// F1: a targeted read that finds the id with a MATCHING activity type proceeds.
func TestSubmit_PartialCatalogTargetedReadActivityTypeMatchProceeds(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	remote.catalogByQuery = func(query string) (gradion.Catalog, error) {
		return gradion.Catalog{Complete: true, Query: query, Source: "live", ObservedAt: fixedNow(),
			Options: []gradion.ClassificationOption{clsOpt("cls-1")}}, nil
	}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	seedApprovedWithActivityType(t, store, r, date, "internal project")

	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: partialCatalog("intern", "other-cls")})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Blocked {
		t.Fatalf("a matching targeted activity type must not block, got %v", res.BlockReasons)
	}
	if remote.creates != 1 {
		t.Fatalf("want one create, got %d", remote.creates)
	}
}

// W1: the targeted read is BOUNDED: one call per distinct missing id, deduped.
func TestSubmit_TargetedReadIsDeduped(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	remote.catalogByQuery = func(query string) (gradion.Catalog, error) {
		return gradion.Catalog{Complete: true, Query: query, Source: "live", ObservedAt: fixedNow(),
			Options: []gradion.ClassificationOption{clsOpt("cls-1")}}, nil
	}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})

	// Two confirmed intervals share cls-1, which is absent from the partial catalog.
	rec := readyRecord(date)
	rec.Confirmed = append(rec.Confirmed, workflow.ConfirmedInterval{
		ID: "c2", Date: date, StartTime: "11:00", EndTime: "12:00",
		Project: "Proj", Description: "b", Attendance: true, ClassificationID: "cls-1",
		ClassificationCode: "CODE-cls-1", ClassificationName: "Class cls-1", ActivityType: "internal project",
	})
	if _, err := store.UpdateDay(ctx, date, func(x *workflow.Record) error { *x = rec; return nil }); err != nil {
		t.Fatalf("seed: %v", err)
	}
	pre, err := r.Preview(ctx, date, clsResolver)
	if err != nil || pre.Blocked {
		t.Fatalf("Preview: %v %v", err, pre.BlockReasons)
	}
	if _, err := r.Approve(ctx, date, pre.Plan); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: partialCatalog("intern", "other-cls")})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Blocked {
		t.Fatalf("both ops share cls-1 and it is found, so submission must proceed: %v", res.BlockReasons)
	}
	if len(remote.catalogCalls) != 1 {
		t.Fatalf("want exactly ONE targeted read for the shared id, got %v", remote.catalogCalls)
	}
	if remote.creates != 2 {
		t.Fatalf("want two creates, got %d", remote.creates)
	}
}

// An available catalog that contains the planned classification allows submission.
func TestSubmit_VerifiedClassificationProceeds(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	seedApproved(t, store, r, date)

	cat := gradion.Catalog{Complete: true, Source: "live", Options: []gradion.ClassificationOption{clsOpt("cls-1")}}
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

// OBSERVATION-SURFACE-1: an OFFLINE preview (nil Remote) is UNOBSERVED. The plan
// records a ZERO RemoteObservedAt and RemoteComplete=false, and PlanResult reports
// RemoteObserved=false — no local timestamp stands in for a remote observation.
// Fail-before: the offline listing was {Complete:true, ObservedAt:now}, so
// RemoteObservedAt was now and RemoteComplete true.
func TestPreview_OfflineIsUnobserved(t *testing.T) {
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
		t.Fatalf("an offline preview must still plan, got %v", pre.BlockReasons)
	}
	if pre.RemoteObserved {
		t.Error("an offline preview must report RemoteObserved=false")
	}
	if !pre.Plan.RemoteObservedAt.IsZero() {
		t.Errorf("an offline plan must record a ZERO RemoteObservedAt, got %v", pre.Plan.RemoteObservedAt)
	}
	if pre.Plan.RemoteComplete {
		t.Error("an offline plan must record RemoteComplete=false")
	}
}

// OBSERVATION-SURFACE-1: an ONLINE preview reports the observed timestamp and
// complete=true, and PlanResult reports RemoteObserved=true.
func TestPreview_OnlineReportsObserved(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	rec := readyRecord(date)
	if _, err := store.UpdateDay(ctx, date, func(x *workflow.Record) error { *x = rec; return nil }); err != nil {
		t.Fatalf("seed: %v", err)
	}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: newFakeRemote()}, Config{})
	pre, err := r.Preview(ctx, date, clsResolver)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if pre.Blocked {
		t.Fatalf("want a plan, got %v", pre.BlockReasons)
	}
	if !pre.RemoteObserved {
		t.Error("an online preview must report RemoteObserved=true")
	}
	if !pre.Plan.RemoteObservedAt.Equal(fixedNow()) {
		t.Errorf("an online plan must record the observed timestamp, got %v", pre.Plan.RemoteObservedAt)
	}
	if !pre.Plan.RemoteComplete {
		t.Error("an online plan must record RemoteComplete=true")
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
