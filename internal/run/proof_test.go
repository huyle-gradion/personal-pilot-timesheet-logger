package run

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/submit"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// fullRow is a fully-populated live catalog row matching readyRecord's metadata.
func fullRow() gradion.ClassificationOption {
	return gradion.ClassificationOption{ID: "cls-1", Name: "Class cls-1", Code: "CODE-cls-1", ActivityType: "internal project"}
}

// proofRecord builds a ready record with n selected intervals that all share one
// classification and its recorded code/name retrieval metadata.
func proofRecord(date string, n int) workflow.Record {
	rec := readyRecord(date)
	rec.Confirmed = nil
	for i := 0; i < n; i++ {
		start := fmt.Sprintf("%02d:00", i)
		end := fmt.Sprintf("%02d:00", i+1)
		rec.Confirmed = append(rec.Confirmed, workflow.ConfirmedInterval{
			ID: fmt.Sprintf("c%d", i+1), Date: date, StartTime: start, EndTime: end,
			Project: "Proj", Description: "work", Attendance: true,
			ClassificationID: "cls-1", ClassificationCode: "INTGRADI2610",
			ClassificationName: "Gradion Intern Academy 2026", ActivityType: "internal project",
		})
	}
	return rec
}

// Every planned selection is proven by ONE bounded targeted read of the recorded
// CODE, even when the uuid-only lookup would return nothing (the pre-fix
// regression). The base catalog is fresh but partial, so it never proves identity.
func TestSubmit_EverySelectionProvenByOneCodeRead(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	remote.catalogByQuery = func(query string) (gradion.Catalog, error) {
		if query == "INTGRADI2610" {
			return gradion.Catalog{Complete: true, Query: query, Source: "live", ObservedAt: fixedNow(),
				Options: []gradion.ClassificationOption{{ID: "cls-1", Name: "Gradion Intern Academy 2026", Code: "INTGRADI2610", ActivityType: "internal project"}}}, nil
		}
		// A uuid/other query returns NOTHING: the pre-fix uuid-only lookup blocked.
		return gradion.Catalog{Complete: true, Query: query, Source: "live", ObservedAt: fixedNow()}, nil
	}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	// Regression (pre-fix): a UUID-ONLY lookup returns an EMPTY page, so the old
	// targeted-by-id logic blocked the plan. The recorded-code lookup resolves it.
	if uuidCat, _ := remote.ClassificationCatalog(ctx, "cls-1"); len(uuidCat.Options) != 0 {
		t.Fatalf("pre-fix uuid lookup must be empty")
	}
	remote.catalogCalls = nil

	rec := proofRecord(date, 14)
	if _, err := store.UpdateDay(ctx, date, func(x *workflow.Record) error { *x = rec; return nil }); err != nil {
		t.Fatalf("seed: %v", err)
	}
	pre, err := r.Preview(ctx, date, clsResolver)
	if err != nil || pre.Blocked {
		t.Fatalf("Preview: %v %v", err, pre.BlockReasons)
	}
	if len(pre.Plan.Operations) != 14 {
		t.Fatalf("want 14 planned ops, got %d", len(pre.Plan.Operations))
	}
	if _, err := r.Approve(ctx, date, pre.Plan); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: partialCatalog("intern", "other")})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Blocked {
		t.Fatalf("a code-proven plan must not block, got %v", res.BlockReasons)
	}
	if remote.creates != 14 {
		t.Fatalf("want 14 creates, got %d", remote.creates)
	}
	if remote.edits != 0 {
		t.Fatalf("want zero edits, got %d", remote.edits)
	}
	if len(remote.catalogCalls) != 1 || remote.catalogCalls[0] != "INTGRADI2610" {
		t.Fatalf("want exactly ONE targeted read by the recorded code, got %v", remote.catalogCalls)
	}
}

// A fresh CACHE that already contains the row is NEVER proof: a live targeted read
// is still performed.
func TestSubmit_FreshCacheDoesNotBypassLiveRead(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	remote.catalogByQuery = func(query string) (gradion.Catalog, error) {
		return gradion.Catalog{Complete: true, Query: query, Source: "live", ObservedAt: fixedNow(),
			Options: []gradion.ClassificationOption{fullRow()}}, nil
	}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	seedApproved(t, store, r, date)

	cacheCat := gradion.Catalog{Complete: true, Source: "cache", ObservedAt: fixedNow(),
		Options: []gradion.ClassificationOption{fullRow()}}
	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: &cacheCat})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Blocked {
		t.Fatalf("must not block: %v", res.BlockReasons)
	}
	if len(remote.catalogCalls) != 1 {
		t.Fatalf("a cache row must NOT bypass the live read, got calls %v", remote.catalogCalls)
	}
	if remote.creates != 1 {
		t.Fatalf("want one create, got %d", remote.creates)
	}
}

// A fresh LIVE FULL COMPLETE catalog is authoritative and reused: ZERO reads.
func TestSubmit_FullLiveCompleteCatalogReusedNoReads(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	seedApproved(t, store, r, date)

	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: fullCatalogWith("cls-1")})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Blocked {
		t.Fatalf("must not block: %v", res.BlockReasons)
	}
	if len(remote.catalogCalls) != 0 {
		t.Fatalf("a full live catalog must be reused with zero reads, got %v", remote.catalogCalls)
	}
	if remote.creates != 1 {
		t.Fatalf("want one create, got %d", remote.creates)
	}
}

// A LEGACY selection (no recorded code/name) with only a partial/cache base fails
// closed with a reselect diagnostic, ZERO writes, and the record bytes are
// unchanged.
func TestSubmit_LegacyNoMetadataReselectBlocksUnchanged(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})

	rec := readyRecord(date)
	rec.Confirmed[0].ClassificationCode = ""
	rec.Confirmed[0].ClassificationName = ""
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
	before, _ := store.GetDay(ctx, date)
	beforeBytes, _ := json.Marshal(before)

	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: partialCatalog("intern", "other")})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked || !hasReason(res.BlockReasons, submit.BlockReselectClassification+":c1") {
		t.Fatalf("want a reselect diagnostic, got %v", res.BlockReasons)
	}
	if remote.creates != 0 || remote.edits != 0 {
		t.Fatalf("a legacy selection must produce ZERO writes, got %d/%d", remote.creates, remote.edits)
	}
	after, _ := store.GetDay(ctx, date)
	afterBytes, _ := json.Marshal(after)
	if !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatalf("a blocked validation must not mutate the record")
	}
}

// A LEGACY selection CAN be proven from a fresh LIVE FULL COMPLETE catalog whose
// row identity/type metadata meets safety (the documented alternative).
func TestSubmit_LegacyNoMetadataFullCatalogProves(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})

	rec := readyRecord(date)
	rec.Confirmed[0].ClassificationCode = ""
	rec.Confirmed[0].ClassificationName = ""
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

	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: fullCatalogWith("cls-1")})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Blocked {
		t.Fatalf("a safe full catalog proves a legacy choice, got %v", res.BlockReasons)
	}
	if len(remote.catalogCalls) != 0 {
		t.Fatalf("a full catalog needs no reads, got %v", remote.catalogCalls)
	}
	if remote.creates != 1 {
		t.Fatalf("want one create, got %d", remote.creates)
	}
}

// A PARTIAL targeted page fails closed even though it contains the desired row.
func TestSubmit_PartialTargetedResultWithRowBlocks(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	remote.catalogByQuery = func(query string) (gradion.Catalog, error) {
		return gradion.Catalog{Complete: false, Query: query, Source: "live", ObservedAt: fixedNow(),
			Options: []gradion.ClassificationOption{fullRow()}}, nil
	}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	seedApproved(t, store, r, date)

	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: partialCatalog("intern", "other")})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked || !hasReason(res.BlockReasons, submit.BlockUnverifiedClassification+":c1") {
		t.Fatalf("a partial targeted page must block, got %v", res.BlockReasons)
	}
	if remote.creates != 0 || remote.edits != 0 {
		t.Fatalf("a partial targeted page must produce ZERO writes")
	}
}

// A non-live (cache) targeted result never proves identity.
func TestSubmit_NonLiveTargetedResultBlocks(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	remote.catalogByQuery = func(query string) (gradion.Catalog, error) {
		return gradion.Catalog{Complete: true, Query: query, Source: "cache", ObservedAt: fixedNow(),
			Options: []gradion.ClassificationOption{fullRow()}}, nil
	}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	seedApproved(t, store, r, date)

	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: partialCatalog("intern", "other")})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked || !hasReason(res.BlockReasons, submit.BlockUnverifiedClassification+":c1") {
		t.Fatalf("a non-live targeted result must block, got %v", res.BlockReasons)
	}
	if remote.creates != 0 {
		t.Fatalf("a non-live targeted result must produce ZERO writes")
	}
}

// Ops sharing a classification id but disagreeing on retrieval metadata block as a
// GROUP (never "one-wins").
func TestSubmit_InconsistentSharedIDBlocks(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})

	rec := readyRecord(date)
	rec.Confirmed = append(rec.Confirmed, workflow.ConfirmedInterval{
		ID: "c2", Date: date, StartTime: "11:00", EndTime: "12:00",
		Project: "Proj", Description: "b", Attendance: true, ClassificationID: "cls-1",
		ClassificationCode: "A-DIFFERENT-CODE", ClassificationName: "Class cls-1", ActivityType: "internal project",
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

	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: partialCatalog("intern", "other")})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked {
		t.Fatalf("an inconsistent shared id must block")
	}
	for _, id := range []string{"c1", "c2"} {
		if !hasReason(res.BlockReasons, submit.BlockUnverifiedClassification+":"+id) {
			t.Fatalf("want %s blocked too, got %v", id, res.BlockReasons)
		}
	}
	if len(remote.catalogCalls) != 0 {
		t.Fatalf("an inconsistent group must not issue reads, got %v", remote.catalogCalls)
	}
	if remote.creates != 0 {
		t.Fatalf("an inconsistent group must produce ZERO writes")
	}
}

// A pre-existing approval still validates when ONLY the optional retrieval hint
// (code/name) is added to the confirmed interval: they are not fingerprinted.
func TestSubmit_ClassificationHintAdditionKeepsApprovalValid(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	remote.catalogByQuery = func(query string) (gradion.Catalog, error) {
		return gradion.Catalog{Complete: true, Query: query, Source: "live", ObservedAt: fixedNow(),
			Options: []gradion.ClassificationOption{fullRow()}}, nil
	}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	seedApproved(t, store, r, date)

	// Add the hint AFTER approval; the fingerprint must not change.
	if _, err := store.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		rec.Confirmed[0].ClassificationCode = "CODE-cls-1"
		rec.Confirmed[0].ClassificationName = "Class cls-1"
		return nil
	}); err != nil {
		t.Fatalf("hint edit: %v", err)
	}

	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: fullCatalogWith("cls-1")})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if res.Blocked {
		t.Fatalf("a hint-only change must not stale the approval, got %v", res.BlockReasons)
	}
	if remote.creates != 1 {
		t.Fatalf("want one create, got %d", remote.creates)
	}
}

// An ID/type EDIT after approval still stales it (fail closed, ZERO writes).
func TestSubmit_ClassificationTypeEditStalesApproval(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	seedApproved(t, store, r, date)

	if _, err := store.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		rec.Confirmed[0].ActivityType = "client project"
		return nil
	}); err != nil {
		t.Fatalf("type edit: %v", err)
	}

	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: fullCatalogWith("cls-1")})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked {
		t.Fatalf("an activity-type edit must stale the approval")
	}
	if remote.creates != 0 || remote.edits != 0 {
		t.Fatalf("a stale approval must produce ZERO writes")
	}
}

// A proven FULL-catalog absence is NOT downgraded into a targeted-read success,
// even though a targeted read would have found the row.
func TestSubmit_ProvenFullAbsenceNotDowngraded(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	remote.catalogByQuery = func(query string) (gradion.Catalog, error) {
		return gradion.Catalog{Complete: true, Query: query, Source: "live", ObservedAt: fixedNow(),
			Options: []gradion.ClassificationOption{fullRow()}}, nil
	}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})
	seedApproved(t, store, r, date)

	res, err := r.Submit(ctx, date, SubmitOptions{Catalog: fullCatalogWith("other-cls")})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !res.Blocked || !hasReason(res.BlockReasons, submit.BlockUnknownClassification+":c1") {
		t.Fatalf("want a proven absence block, got %v", res.BlockReasons)
	}
	if len(remote.catalogCalls) != 0 {
		t.Fatalf("a full-catalog absence must not trigger a read, got %v", remote.catalogCalls)
	}
	if remote.creates != 0 {
		t.Fatalf("a proven absence must produce ZERO writes")
	}
}
