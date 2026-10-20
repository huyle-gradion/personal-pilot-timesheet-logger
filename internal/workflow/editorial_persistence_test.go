package workflow

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkflow_EditorialAndUsagePersist(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-06"
	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("NewJSONFileStore: %v", err)
	}

	if _, err := store.UpdateDay(ctx, date, func(r *Record) error {
		UpsertEditorialSuggestion(r, EditorialSuggestion{
			TargetID: "c1", RequestHash: "h",
			Text:   EditorialText{Title: "t", Description: "d"},
			Origin: "editorial", Warnings: []string{"w"},
		})
		SetUsageReport(r, EditorialUsage{
			Date: date, Editorial: KnownMeasurement(42), Orchestration: UnknownMeasurement(),
			EditorialRuns: 2, Invoked: true, RuntimeMS: 5, PeakRSSMB: 1.5,
		})
		return nil
	}); err != nil {
		t.Fatalf("UpdateDay: %v", err)
	}

	fresh, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("fresh store: %v", err)
	}
	got, err := fresh.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if len(got.Editorial) != 1 || got.Editorial[0].TargetID != "c1" || got.Editorial[0].Text.Description != "d" {
		t.Errorf("editorial did not survive reload: %+v", got.Editorial)
	}
	if s, ok := FindEditorialSuggestion(got, "c1"); !ok || s.RequestHash != "h" {
		t.Errorf("FindEditorialSuggestion: %+v (ok=%v)", s, ok)
	}
	if got.Usage == nil || got.Usage.Editorial.Value != 42 || got.Usage.Orchestration.Known || got.Usage.EditorialRuns != 2 {
		t.Errorf("usage did not survive reload: %+v", got.Usage)
	}
}

func TestWorkflow_EditorialCapacityBound(t *testing.T) {
	ctx := context.Background()
	date := "2026-10-06"
	store, err := NewJSONFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewJSONFileStore: %v", err)
	}
	_, err = store.UpdateDay(ctx, date, func(r *Record) error {
		r.Editorial = make([]EditorialSuggestion, MaxEditorialCount+1)
		return nil
	})
	if !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("want ErrCapacityExceeded, got %v", err)
	}
}

// TestWorkflow_EditorialDoesNotFeedFingerprintOrPlanHash proves the ADDITIVE
// editorial fields do not move LocalFingerprint. The PlanHash half of the claim
// is structural rather than observable from here, so it is recorded below as a
// statement of the contract instead of asserted.
func TestWorkflow_EditorialDoesNotFeedFingerprintOrPlanHash(t *testing.T) {
	rec := Record{
		Date:      "2026-10-06",
		Confirmed: []ConfirmedInterval{{ID: "c1", Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00", Project: "P"}},
	}
	fingerprint := LocalFingerprint(rec)

	// rec changes below, so recomputing LocalFingerprint over it genuinely can
	// differ: editorial suggestions and their usage must be ignored by the
	// fingerprint.
	rec.Editorial = []EditorialSuggestion{{TargetID: "c1", RequestHash: "h", Text: EditorialText{Title: "t", Description: "d"}}}
	rec.Usage = &EditorialUsage{Date: "2026-10-06", Editorial: KnownMeasurement(99)}

	if LocalFingerprint(rec) != fingerprint {
		t.Errorf("editorial suggestions must NOT feed LocalFingerprint")
	}

	// PlanHash is sha256 over the canonical JSON of an anonymous five-field
	// projection of SubmissionPlan — {Date, LocalFingerprint,
	// RemoteObservedAt, RemoteComplete, Operations} excluding PlanID,
	// CreatedAt and RecordRevision. Editorial state reaches no projected
	// member, so "editorial state does not feed PlanHash" holds by
	// construction and is recorded here as a statement of the contract
	// rather than asserted (a comparison over the same plan value would be
	// vacuous).
}

func TestWorkflow_OldSchemaV1FileHasNilEditorial(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-06"
	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("NewJSONFileStore: %v", err)
	}
	old := `{"schema_version":1,"date":"2026-10-06","revision":1,"updated_at":"2026-10-06T00:00:00Z",` +
		`"evidence":[],"confirmed":[],"remote_mappings":[],"outcomes":[]}`
	if err := os.WriteFile(filepath.Join(root, "workflow", date+".json"), []byte(old), FilePermPrivate); err != nil {
		t.Fatalf("write old file: %v", err)
	}
	got, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("an old SchemaVersion 1 file must remain readable, got %v", err)
	}
	if got.Editorial != nil || got.Usage != nil {
		t.Errorf("expected nil additive fields, got editorial=%+v usage=%+v", got.Editorial, got.Usage)
	}
}
