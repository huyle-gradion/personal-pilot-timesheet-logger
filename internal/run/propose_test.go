package run

import (
	"context"
	"errors"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// A day with no stored record has no evidence: Propose must return an EMPTY draft,
// not an error, and must persist nothing.
func TestPropose_NoRecordYieldsEmptyDraft(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry()}, Config{})

	prop, err := r.Propose(ctx, "2026-10-06")
	if err != nil {
		t.Fatalf("a day with no record must not error: %v", err)
	}
	if len(prop.Work)+len(prop.Meetings)+len(prop.Manual) != 0 {
		t.Fatalf("want an empty draft, got %+v", prop)
	}
	if prop.Date != "2026-10-06" {
		t.Errorf("want the requested date on the draft, got %q", prop.Date)
	}

	// Propose stays a pure view: it must NOT create a record.
	if _, err := store.GetDay(ctx, "2026-10-06"); err == nil {
		t.Fatalf("Propose must not create a record")
	}
}

// failingGetStore returns an injected error from GetDay.
type failingGetStore struct {
	workflow.Store
	err error
}

func (s failingGetStore) GetDay(ctx context.Context, date string) (*workflow.Record, error) {
	return nil, s.err
}

// F4: Propose fails CLOSED on a non-ErrRecordNotFound store error (it must be
// returned, never treated as an empty day).
func TestPropose_NonNotFoundErrorFailsClosed(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("injected store failure")
	r := newRunner(t, Deps{
		Store:    failingGetStore{Store: newStore(t), err: boom},
		Registry: evidence.NewRegistry(),
	}, Config{})

	_, err := r.Propose(ctx, "2026-10-06")
	if !errors.Is(err, boom) {
		t.Fatalf("a non-NotFound error must be returned, got %v", err)
	}
}
