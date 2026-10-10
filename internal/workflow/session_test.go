package workflow

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestBeginDaySession_HoldsLockOnceAndSecondSessionBusy proves a session's
// repeated Updates do not re-acquire (and deadlock on) the day lock, while a
// second session for the same day fails closed with ErrWriterBusy.
func TestBeginDaySession_HoldsLockOnceAndSecondSessionBusy(t *testing.T) {
	ctx := context.Background()
	date := "2026-10-08"
	root := t.TempDir()
	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	session, err := store.BeginDaySession(ctx, date)
	if err != nil {
		t.Fatalf("BeginDaySession: %v", err)
	}
	defer func() { _ = session.Close() }()

	if session.Date() != date {
		t.Errorf("expected session date %q, got %q", date, session.Date())
	}

	for i := 0; i < 3; i++ {
		intent := OperationOutcome{
			OperationKey: "k",
			LocalID:      "local-a",
			Action:       "create",
			Status:       OutcomeInFlight,
			Date:         date,
			StartTime:    "09:00",
			EndTime:      "10:00",
			IntentAt:     time.Now().UTC(),
		}
		if _, err := session.Update(ctx, func(r *Record) error {
			UpsertIntent(r, intent)
			return nil
		}); err != nil {
			t.Fatalf("session.Update %d must not re-acquire the lock, got %v", i, err)
		}
	}

	if _, err := store.BeginDaySession(ctx, date); !errors.Is(err, ErrWriterBusy) {
		t.Fatalf("expected ErrWriterBusy for a second session, got %v", err)
	}
	// UpdateDay is also a self-locking writer and must see the held lock.
	if _, err := store.UpdateDay(ctx, date, func(r *Record) error { return nil }); !errors.Is(err, ErrWriterBusy) {
		t.Fatalf("expected UpdateDay ErrWriterBusy while a session holds the lock, got %v", err)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("session.Close: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second session.Close must be idempotent, got %v", err)
	}
	if _, err := store.BeginDaySession(ctx, date); err != nil {
		t.Fatalf("expected to acquire after Close, got %v", err)
	}
}

// TestSession_IntentPersistedDsyncedAndUnresolved proves an in_flight intent is
// durably written (directory-synced) and returned by UnresolvedOperations across
// a fresh store instance.
func TestSession_IntentPersistedFsyncedAndUnresolved(t *testing.T) {
	ctx := context.Background()
	date := "2026-10-08"
	root := t.TempDir()
	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	calls := 0
	orig := syncWorkflowDir
	defer func() { syncWorkflowDir = orig }()
	syncWorkflowDir = func(dir *os.File) error {
		calls++
		return orig(dir)
	}

	session, err := store.BeginDaySession(ctx, date)
	if err != nil {
		t.Fatalf("BeginDaySession: %v", err)
	}
	intent := OperationOutcome{
		OperationKey: "op-1",
		LocalID:      "local-a",
		Action:       "create",
		Status:       OutcomeInFlight,
		Date:         date,
		StartTime:    "09:00",
		EndTime:      "10:00",
		IntentAt:     time.Now().UTC(),
	}
	if _, err := session.Update(ctx, func(r *Record) error {
		UpsertIntent(r, intent)
		return nil
	}); err != nil {
		t.Fatalf("session.Update: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if calls == 0 {
		t.Fatalf("intent persistence did not directory-sync; durability is not established")
	}

	fresh, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("fresh store: %v", err)
	}
	got, err := fresh.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("fresh GetDay: %v", err)
	}
	pending := UnresolvedOperations(got)
	if len(pending) != 1 {
		t.Fatalf("expected 1 unresolved operation, got %d (%+v)", len(pending), got.Outcomes)
	}
	if pending[0].OperationKey != "op-1" || pending[0].Status != OutcomeInFlight {
		t.Fatalf("unexpected unresolved operation: %+v", pending[0])
	}
	if pending[0].IntentAt.IsZero() {
		t.Errorf("expected a non-zero IntentAt to survive reload")
	}
}

// TestSession_IntentDirectorySyncFailureSurfaces proves a directory-sync failure
// on intent persistence is surfaced rather than swallowed.
func TestSession_IntentDirectorySyncFailureSurfaces(t *testing.T) {
	ctx := context.Background()
	date := "2026-10-08"
	root := t.TempDir()
	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	orig := syncWorkflowDir
	defer func() { syncWorkflowDir = orig }()
	boom := errors.New("simulated directory fsync failure")
	syncWorkflowDir = func(*os.File) error { return boom }

	session, err := store.BeginDaySession(ctx, date)
	if err != nil {
		t.Fatalf("BeginDaySession: %v", err)
	}
	defer func() { _ = session.Close() }()

	_, err = session.Update(ctx, func(r *Record) error {
		UpsertIntent(r, OperationOutcome{OperationKey: "op-1", Status: OutcomeInFlight, Date: date})
		return nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("expected wrapped directory-sync failure, got %v", err)
	}
}

// TestUpsertIntent_IdempotentAndNeverResurrectsTerminal proves repeated intents
// collapse to one row and an intent is a no-op once a terminal row exists.
func TestUpsertIntent_IdempotentAndNeverResurrectsTerminal(t *testing.T) {
	r := &Record{}
	intent := OperationOutcome{OperationKey: "k", Status: OutcomeInFlight, Date: "2026-10-08", IntentAt: time.Now().UTC()}
	UpsertIntent(r, intent)
	UpsertIntent(r, intent)
	if len(r.Outcomes) != 1 {
		t.Fatalf("expected 1 intent row, got %d", len(r.Outcomes))
	}

	UpsertOutcome(r, OperationOutcome{OperationKey: "k", Status: OutcomeConfirmed, Resolved: true})
	if len(r.Outcomes) != 1 || r.Outcomes[0].Status != OutcomeConfirmed {
		t.Fatalf("expected confirmed row, got %+v", r.Outcomes)
	}
	UpsertIntent(r, intent)
	if len(r.Outcomes) != 1 || r.Outcomes[0].Status != OutcomeConfirmed {
		t.Fatalf("an intent must not resurrect a terminal row, got %+v", r.Outcomes)
	}
}

// TestUpsertOutcome_DoesNotDowngradeTerminalToIntent proves a terminal row is
// never overwritten by an in_flight intent.
func TestUpsertOutcome_DoesNotDowngradeTerminalToIntent(t *testing.T) {
	r := &Record{}
	UpsertOutcome(r, OperationOutcome{OperationKey: "k", Status: OutcomeUncertain, Resolved: false})
	UpsertOutcome(r, OperationOutcome{OperationKey: "k", Status: OutcomeInFlight, Date: "2026-10-08"})
	if len(r.Outcomes) != 1 || r.Outcomes[0].Status != OutcomeUncertain {
		t.Fatalf("terminal row must not be downgraded to in_flight, got %+v", r.Outcomes)
	}
}

// TestDeriveOperationKey_StableAndCollisionFree proves Description/Classification
// edits do not change identity and distinct slots do not collide (F5).
func TestDeriveOperationKey_StableAndCollisionFree(t *testing.T) {
	base := OperationOutcome{Date: "2026-10-08", Action: "create", StartTime: "09:00", EndTime: "10:00"}

	edited := base
	edited.Description = "totally different text"
	if DeriveOperationKey(base) != DeriveOperationKey(edited) {
		t.Errorf("Description edits must not change operation identity")
	}

	other := base
	other.StartTime = "10:00"
	other.EndTime = "11:00"
	if DeriveOperationKey(base) == DeriveOperationKey(other) {
		t.Errorf("distinct slots must not collide")
	}

	withLocal := base
	withLocal.LocalID = "local-a"
	if DeriveOperationKey(base) == DeriveOperationKey(withLocal) {
		t.Errorf("a distinct LocalID must change identity")
	}
}

// TestSaveDayCAS_ConflictAndAdvance proves a mis-matched expected revision writes
// nothing (bytes unchanged), and a matching revision advances it by one.
func TestSaveDayCAS_ConflictAndAdvance(t *testing.T) {
	ctx := context.Background()
	date := "2026-10-08"
	root := t.TempDir()
	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	path := filepath.Join(root, "workflow", date+".json")

	// Absent record has persisted revision 0, so expected 0 creates revision 1.
	if err := store.SaveDayCAS(ctx, 0, Record{
		SchemaVersion: SchemaVersion,
		Date:          date,
		Outcomes:      []OperationOutcome{{OperationKey: "a", Status: OutcomeConfirmed, Resolved: true}},
	}); err != nil {
		t.Fatalf("SaveDayCAS(0) create: %v", err)
	}
	loaded, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if loaded.Revision != 1 {
		t.Fatalf("expected revision 1 after create, got %d", loaded.Revision)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read before conflict: %v", err)
	}
	if err := store.SaveDayCAS(ctx, 0, Record{
		SchemaVersion: SchemaVersion,
		Date:          date,
		Outcomes:      []OperationOutcome{{OperationKey: "b", Status: OutcomeConfirmed, Resolved: true}},
	}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("expected ErrRevisionConflict, got %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after conflict: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("a conflicted CAS must not modify the file bytes")
	}

	if err := store.SaveDayCAS(ctx, 1, Record{
		SchemaVersion: SchemaVersion,
		Date:          date,
		Outcomes:      []OperationOutcome{{OperationKey: "c", Status: OutcomeConfirmed, Resolved: true}},
	}); err != nil {
		t.Fatalf("SaveDayCAS(1): %v", err)
	}
	final, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay final: %v", err)
	}
	if final.Revision != 2 {
		t.Fatalf("expected revision 2 after advance, got %d", final.Revision)
	}
}
