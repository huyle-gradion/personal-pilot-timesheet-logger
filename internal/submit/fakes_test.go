package submit

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// fakeRemote implements both RemoteObserver and RemoteWriter with shared, mutable
// state so a confirmed create is visible to the next observation (and to the
// remaining-plan rebuild).
type fakeRemote struct {
	mu           sync.Mutex
	entries      map[string]workflow.RemoteState
	seq          int
	complete     bool
	listErr      error
	listErrAt    int // fail ListEntries on this 1-based call (0 = never)
	incompleteAt int // make ListEntries Complete=false on this 1-based call (0 = never)
	entryErr     error
	entryByID    func(id string) (workflow.RemoteState, bool, error)
	onCreate     func(gradion.CreateRequest) (gradion.WriteResult, error)
	onEdit       func(gradion.EditRequest) (gradion.WriteResult, error)
	creates      []gradion.CreateRequest
	edits        []gradion.EditRequest
	listCalls    int
}

func newFakeRemote() *fakeRemote {
	return &fakeRemote{entries: map[string]workflow.RemoteState{}, complete: true}
}

func (f *fakeRemote) setEntry(state workflow.RemoteState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[state.RemoteID] = state
}

func (f *fakeRemote) ListEntries(ctx context.Context, dateFrom, dateTo string, limit int) (gradion.Listing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.listErr != nil {
		return gradion.Listing{}, f.listErr
	}
	if f.listErrAt > 0 && f.listCalls == f.listErrAt {
		return gradion.Listing{}, errors.New("injected list failure")
	}
	entries := make([]workflow.RemoteState, 0, len(f.entries))
	for _, e := range f.entries {
		e.ContentObserved = true
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].RemoteID < entries[j].RemoteID })
	complete := f.complete
	if f.incompleteAt > 0 && f.listCalls == f.incompleteAt {
		complete = false
	}
	return gradion.Listing{Observed: true, Complete: complete, ObservedAt: testNow, Entries: entries}, nil
}

// EntryByID is the bounded stable-ID read-back used to verify an
// identity-acknowledged write. It fails closed (CodeListingIncomplete) when the
// listing is not complete, so absence is never claimed from a partial scan.
func (f *fakeRemote) EntryByID(ctx context.Context, id, from, to string) (workflow.RemoteState, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.entryByID != nil {
		return f.entryByID(id)
	}
	if f.entryErr != nil {
		return workflow.RemoteState{}, false, f.entryErr
	}
	if !f.complete {
		return workflow.RemoteState{}, false, &gradion.Error{Code: gradion.CodeListingIncomplete, Message: "listing incomplete"}
	}
	e, ok := f.entries[id]
	if !ok {
		return workflow.RemoteState{}, false, nil
	}
	e.ContentObserved = true
	return e, true, nil
}

func (f *fakeRemote) CreateEntry(ctx context.Context, req gradion.CreateRequest) (gradion.WriteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates = append(f.creates, req)

	var res gradion.WriteResult
	if f.onCreate != nil {
		override, err := f.onCreate(req)
		if err != nil {
			return override, err
		}
		res = override
	} else {
		f.seq++
		state := workflow.RemoteState{
			RemoteID: fmt.Sprintf("new-%d", f.seq), Date: req.Date, StartTime: req.StartTime,
			EndTime: req.EndTime, ClassificationID: req.ClassificationID,
			Description: req.Description, Task: req.Task, Billable: req.Billable, ObservedAt: testNow,
			ContentObserved: true,
		}
		res = gradion.WriteResult{Status: gradion.WriteConfirmed, Entry: &state}
	}
	if res.Status == gradion.WriteConfirmed && res.Entry != nil {
		f.entries[res.Entry.RemoteID] = *res.Entry
	}
	return res, nil
}

func (f *fakeRemote) EditEntry(ctx context.Context, req gradion.EditRequest) (gradion.WriteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, req)

	var res gradion.WriteResult
	if f.onEdit != nil {
		override, err := f.onEdit(req)
		if err != nil {
			return override, err
		}
		res = override
	} else {
		state := workflow.RemoteState{
			RemoteID: req.EntryID, Date: req.Date, StartTime: req.StartTime,
			EndTime: req.EndTime, ClassificationID: req.ClassificationID,
			Description: req.Description, Task: req.Task, Billable: req.Billable, ObservedAt: testNow,
			ContentObserved: true,
		}
		res = gradion.WriteResult{Status: gradion.WriteConfirmed, Entry: &state}
	}
	if res.Status == gradion.WriteConfirmed && res.Entry != nil {
		f.entries[res.Entry.RemoteID] = *res.Entry
	}
	return res, nil
}

func (f *fakeRemote) createCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.creates)
}

func (f *fakeRemote) editCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.edits)
}

func newTestStore(t *testing.T) *workflow.JSONFileStore {
	t.Helper()
	store, err := workflow.NewJSONFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONFileStore: %v", err)
	}
	return store
}

// withSecondInterval adds a reviewed, classified confirmed interval.
func withSecondInterval(rec workflow.Record, id, start, end, desc string) workflow.Record {
	rec.Confirmed = append(rec.Confirmed, workflow.ConfirmedInterval{
		ID: id, Date: rec.Date, StartTime: start, EndTime: end,
		Project: "Proj", Description: desc, Attendance: true, ClassificationID: "cls-1",
	})
	return rec
}

// seedApprovedDay persists a record carrying an approved plan built from obs.
func seedApprovedDay(t *testing.T, store workflow.Store, rec workflow.Record, obs gradion.Listing, resolver ClassificationResolver) (workflow.Record, workflow.Approval) {
	t.Helper()
	built, err := BuildPlan(rec, obs, nil, resolver, testNow)
	if err != nil {
		t.Fatalf("BuildPlan: %v", err)
	}
	if built.Blocked {
		t.Fatalf("unexpected plan block: %v", built.Reasons)
	}
	workflow.UpsertPlan(&rec, built.Plan)
	approval, err := Approve(&rec, built.Plan, testNow)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if _, err := store.UpdateDay(context.Background(), rec.Date, func(r *workflow.Record) error {
		*r = rec
		return nil
	}); err != nil {
		t.Fatalf("persist record: %v", err)
	}
	return rec, approval
}

func loadRecord(t *testing.T, store workflow.Store, date string) workflow.Record {
	t.Helper()
	got, err := store.GetDay(context.Background(), date)
	if err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	return *got
}
