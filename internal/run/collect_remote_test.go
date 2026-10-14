package run

import (
	"context"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// COLLECT-REMOTE-1: Collection must read the remote ONCE, persist a durable
// RemoteObservation, and surface a collection-time collision — while the plan
// still blocks through the pre-existing plan-time surface.
//
// REGRESSION POWER NOTE: assertion (d) — the plan blocks with
// "duplicate_looking_remote:c1" — is ALREADY GREEN BEFORE THIS CHANGE, because
// the plan-time collision surface (submit.BuildPlan) already exists and Preview
// already reads the remote. (e) also does not regress anything. The regression
// power comes from (a), (b) and (c): before the change Collect made ZERO list
// calls and had no observation and no collision field, so (a) fails
// (rec.RemoteObservation is nil), (b) fails (res.RemoteCollisions is empty), and
// (c) fails (the count goes 0 before Preview and 1 after, where the change keeps
// it at 1 and 1).
func TestCollect_PersistsRemoteObservationAndSurfacesCollision(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	// One UNMAPPED remote entry r1 spanning exactly c1's slot.
	remote.entries["r1"] = workflow.RemoteState{
		RemoteID: "r1", Date: date, StartTime: "09:00", EndTime: "10:00",
		ClassificationID: "cls-1", Description: "work", ObservedAt: fixedNow(),
	}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})

	// Seed one confirmed interval c1 09:00-10:00 (reviewed so the plan can build).
	rec := readyRecord(date)
	if _, err := store.UpdateDay(ctx, date, func(x *workflow.Record) error { *x = rec; return nil }); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := r.Collect(ctx, date)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if res.RemoteObservationError != "" {
		t.Fatalf("a successful remote read must not warn, got %q", res.RemoteObservationError)
	}

	// (a) The observation is durably persisted and carries r1.
	got, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if got.RemoteObservation == nil {
		t.Fatal("Collect must persist a remote observation when a remote is configured")
	}
	found := false
	for _, e := range got.RemoteObservation.Entries {
		if e.RemoteID == "r1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the observation must contain r1, got %+v", got.RemoteObservation.Entries)
	}

	// (b) The collection-time collision surface lists r1.
	if !containsString(res.RemoteCollisions, "r1") {
		t.Fatalf("RemoteCollisions must contain r1, got %v", res.RemoteCollisions)
	}

	// (b2) OBSERVATION-SURFACE-1: the successful read is reported OBSERVED and the
	// day's entries are surfaced for enumeration (#43).
	if !res.RemoteObserved {
		t.Error("a successful remote read must report RemoteObserved=true")
	}
	if !res.RemoteComplete {
		t.Error("a complete remote read must report RemoteComplete=true")
	}
	hasEntry := false
	for _, e := range res.RemoteEntries {
		if e.RemoteID == "r1" {
			hasEntry = true
		}
	}
	if !hasEntry {
		t.Fatalf("RemoteEntries must carry the day's observed entries, got %+v", res.RemoteEntries)
	}

	// (c) Preview reuses the SAME listing: the remote is read only ONCE per run.
	before := remote.listCalls
	pre, err := r.Preview(ctx, date, clsResolver)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if remote.listCalls != before {
		t.Fatalf("Preview must reuse the cached listing, got %d list call(s) after %d", remote.listCalls, before)
	}

	// (d) The plan still blocks through the pre-existing plan-time surface.
	if !pre.Blocked || !hasReason(pre.BlockReasons, "duplicate_looking_remote:c1") {
		t.Fatalf("want duplicate_looking_remote:c1, got blocked=%v %v", pre.Blocked, pre.BlockReasons)
	}

	// (e) The bare observation created NO mapping.
	after, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay 2: %v", err)
	}
	if len(after.RemoteMappings) != 0 {
		t.Fatalf("a bare observation must not create a mapping, got %+v", after.RemoteMappings)
	}
}

// COLLECT-REMOTE-1: an offline runner (nil Remote) must make NO remote call and
// leave RemoteObservation nil — an observation is never fabricated.
func TestCollect_OfflineLeavesNoRemoteObservation(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry()}, Config{})
	if r.deps.Remote != nil {
		t.Fatal("this runner must be offline")
	}

	res, err := r.Collect(ctx, date)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if res.RemoteObservationError != "" {
		t.Fatalf("an offline collection must not report a remote error, got %q", res.RemoteObservationError)
	}
	if len(res.RemoteCollisions) != 0 {
		t.Fatalf("an offline collection must surface no collisions, got %v", res.RemoteCollisions)
	}
	if res.RemoteObserved {
		t.Error("an offline collection must report RemoteObserved=false")
	}
	if res.RemoteComplete {
		t.Error("an offline collection must report RemoteComplete=false")
	}
	if len(res.RemoteEntries) != 0 {
		t.Fatalf("an offline collection must surface no remote entries, got %+v", res.RemoteEntries)
	}
	got, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if got.RemoteObservation != nil {
		t.Fatalf("an offline collection must leave RemoteObservation nil, got %+v", got.RemoteObservation)
	}
}

// F1: an OBSERVED but INCOMPLETE collection read reports RemoteObserved=true and
// RemoteComplete=false, so a CLI surface can label the enumeration PARTIAL rather
// than presenting a partial set as complete.
func TestCollect_ReportsIncompleteObservedRead(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := newFakeRemote()
	remote.complete = false
	remote.entries["r1"] = workflow.RemoteState{
		RemoteID: "r1", Date: date, StartTime: "09:00", EndTime: "10:00", ObservedAt: fixedNow(),
	}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})

	res, err := r.Collect(ctx, date)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !res.RemoteObserved {
		t.Error("an incomplete but successful read must still report RemoteObserved=true")
	}
	if res.RemoteComplete {
		t.Error("an incomplete read must report RemoteComplete=false")
	}
	if len(res.RemoteEntries) == 0 {
		t.Error("the observed entries must still be surfaced for a labelled enumeration")
	}
}

// COLLECT-REMOTE-1: a FAILING remote read must not fail collection and must not
// persist a fabricated observation; it records a non-fatal warning in the
// returned CollectResult and continues.
func TestCollect_RemoteReadErrorWarnsAndContinues(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := &listErrRemote{fakeRemote: newFakeRemote()}
	reg := evidence.NewRegistry()
	reg.Register(&fakePlugin{kind: evidence.SourceLocalGit, ev: []evidence.Evidence{gitEvidence("e1", "acme/repo", "work")}})
	sources := []evidence.SourceConfig{{ID: "git", Type: evidence.SourceLocalGit, Enabled: true}}
	r := newRunner(t, Deps{Store: store, Registry: reg, Remote: remote},
		Config{Evidence: evidence.Config{Sources: sources}})

	res, err := r.Collect(ctx, date)
	if err != nil {
		t.Fatalf("a failed remote read must not fail evidence collection: %v", err)
	}
	if res.RemoteObservationError == "" {
		t.Fatal("a failed remote read must be surfaced as a non-fatal warning in CollectResult")
	}
	if len(res.Evidence) != 1 {
		t.Fatalf("evidence must still be collected, got %+v", res.Evidence)
	}
	got, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if got.RemoteObservation != nil {
		t.Fatalf("a failed read must not persist a fabricated observation, got %+v", got.RemoteObservation)
	}
}

// listErrRemote fails every ListEntries while keeping the fakeRemote's counters
// (e.g. listCalls) available.
type listErrRemote struct{ *fakeRemote }

func (f *listErrRemote) ListEntries(ctx context.Context, dateFrom, dateTo string, limit int) (gradion.Listing, error) {
	f.listCalls++
	return gradion.Listing{}, &gradion.Error{Code: gradion.CodeReadSchema, Message: "read failed"}
}

// COLLECT-REMOTE-1 re-review finding CR1-L1: a FAILED re-read must INVALIDATE the
// cached listing, so a later observe in the same run live-reads (and fails
// loudly) rather than silently serving the older, successful listing.
func TestCollect_FailedReReadInvalidatesCache(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	remote := &flakyRemote{fakeRemote: newFakeRemote(), failAfter: 1}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, Config{})

	if _, err := r.Collect(ctx, date); err != nil {
		t.Fatalf("first Collect: %v", err)
	}
	if remote.listCalls != 1 {
		t.Fatalf("want 1 read after the first Collect, got %d", remote.listCalls)
	}

	res, err := r.Collect(ctx, date)
	if err != nil {
		t.Fatalf("a failed re-read must not fail collection: %v", err)
	}
	if res.RemoteObservationError == "" {
		t.Fatal("a failed re-read must warn")
	}

	// The cache entry was invalidated, so Preview must live-read (and fail)
	// rather than reuse the stale listing from the first Collect.
	if _, err := r.Preview(ctx, date, clsResolver); err == nil {
		t.Fatal("Preview must live-read after a failed re-read invalidated the cache, not reuse the stale listing")
	}
	if remote.listCalls != 3 {
		t.Fatalf("want 3 reads (2 Collect + 1 Preview), got %d", remote.listCalls)
	}
}

// flakyRemote succeeds for its first failAfter ListEntries calls and fails every
// later one, counting every attempt.
type flakyRemote struct {
	*fakeRemote
	failAfter int
}

func (f *flakyRemote) ListEntries(ctx context.Context, dateFrom, dateTo string, limit int) (gradion.Listing, error) {
	f.listCalls++
	if f.listCalls > f.failAfter {
		return gradion.Listing{}, &gradion.Error{Code: gradion.CodeReadSchema, Message: "read failed"}
	}
	return gradion.Listing{Observed: true, Complete: true, ObservedAt: fixedNow()}, nil
}
