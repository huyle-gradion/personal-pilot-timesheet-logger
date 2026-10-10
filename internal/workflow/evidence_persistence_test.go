package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence/plugins"
)

const persistenceFixture = `{
  "evidence": [
    {"kind": "manual", "title": "manual work", "identity_parts": ["a"], "project_hints": ["Proj"]},
    {"kind": "local_git", "title": "commit", "identity_parts": ["owner/repo", "sha1"], "repository": "owner/repo", "branch": "main"}
  ]
}`

// collectFixtureEvidence runs the fixture plugin through the registry, exercising
// the real collection path before persistence.
func collectFixtureEvidence(t *testing.T, root, date string) ([]evidence.Evidence, evidence.CollectionSummary) {
	t.Helper()
	fixturePath := filepath.Join(root, "fixture.json")
	if err := os.WriteFile(fixturePath, []byte(persistenceFixture), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	reg := evidence.NewRegistry()
	plugins.Register(reg)
	ev, summary, err := reg.CollectDay(context.Background(), []evidence.SourceConfig{{
		ID: "fix", Type: plugins.TypeFixture, Enabled: true,
		Config: map[string]string{plugins.ConfigKeyPath: fixturePath},
	}}, evidence.CollectRequest{Date: date})
	if err != nil {
		t.Fatalf("CollectDay failed: %v", err)
	}
	if !summary.Complete || len(ev) != 2 {
		t.Fatalf("unexpected collection: complete=%v evidence=%d", summary.Complete, len(ev))
	}
	return ev, summary
}

func marshalJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	return string(data)
}

// TestWorkflow_CollectedEvidenceSurvivesReload proves collected evidence and the
// collection summary persist through the S1 store and reload intact.
func TestWorkflow_CollectedEvidenceSurvivesReload(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-08"

	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("NewJSONFileStore: %v", err)
	}
	ev, summary := collectFixtureEvidence(t, root, date)

	if _, err := store.UpdateDay(ctx, date, func(r *Record) error {
		r.CollectedEvidence = ev
		r.Collection = &summary
		return nil
	}); err != nil {
		t.Fatalf("UpdateDay failed: %v", err)
	}

	fresh, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("fresh NewJSONFileStore: %v", err)
	}
	got, err := fresh.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay failed: %v", err)
	}

	if gotJSON, wantJSON := marshalJSON(t, got.CollectedEvidence), marshalJSON(t, ev); gotJSON != wantJSON {
		t.Errorf("CollectedEvidence changed across reload:\n got=%s\nwant=%s", gotJSON, wantJSON)
	}
	if got.Collection == nil {
		t.Fatal("Collection did not survive reload")
	}
	if gotJSON, wantJSON := marshalJSON(t, got.Collection), marshalJSON(t, &summary); gotJSON != wantJSON {
		t.Errorf("Collection changed across reload:\n got=%s\nwant=%s", gotJSON, wantJSON)
	}
}

// TestWorkflow_EvidenceRefreshLeavesConfirmedUntouched proves a CollectedEvidence
// refresh (upsert) never disturbs Confirmed user work.
func TestWorkflow_EvidenceRefreshLeavesConfirmedUntouched(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-08"

	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("NewJSONFileStore: %v", err)
	}

	ev, summary := collectFixtureEvidence(t, root, date)

	if _, err := store.UpdateDay(ctx, date, func(r *Record) error {
		MergeConfirmed(r, []ConfirmedInterval{{
			ID: "c1", Date: date, StartTime: "09:00", EndTime: "10:00",
			Project: "Proj", Description: "user kept this",
		}})
		r.CollectedEvidence = ev
		r.Collection = &summary
		return nil
	}); err != nil {
		t.Fatalf("initial UpdateDay failed: %v", err)
	}

	// Refresh: upsert collected evidence by ID, leaving Confirmed alone.
	if _, err := store.UpdateDay(ctx, date, func(r *Record) error {
		incoming := []evidence.Evidence{{
			ID:    evidence.StableID(evidence.SourceManual, "a"),
			Kind:  evidence.SourceManual,
			Title: "manual work (refreshed)",
		}}
		for _, inc := range incoming {
			replaced := false
			for i := range r.CollectedEvidence {
				if r.CollectedEvidence[i].ID == inc.ID {
					r.CollectedEvidence[i] = inc
					replaced = true
					break
				}
			}
			if !replaced {
				r.CollectedEvidence = append(r.CollectedEvidence, inc)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("refresh UpdateDay failed: %v", err)
	}

	got, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay failed: %v", err)
	}
	if len(got.Confirmed) != 1 {
		t.Fatalf("expected 1 confirmed interval, got %d", len(got.Confirmed))
	}
	if got.Confirmed[0].Description != "user kept this" || got.Confirmed[0].Project != "Proj" {
		t.Errorf("refresh clobbered confirmed user text: %+v", got.Confirmed[0])
	}
}

// TestWorkflow_CollectedEvidenceCapacity proves an over-capacity collected
// evidence set is rejected with ErrCapacityExceeded and nothing is written.
func TestWorkflow_CollectedEvidenceCapacity(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-08"

	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("NewJSONFileStore: %v", err)
	}

	over := make([]evidence.Evidence, MaxCollectedEvidenceCount+1)
	for i := range over {
		over[i] = evidence.Evidence{ID: "id", Kind: evidence.SourceManual, Title: "x"}
	}

	if _, err := store.UpdateDay(ctx, date, func(r *Record) error {
		r.CollectedEvidence = over
		return nil
	}); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("expected ErrCapacityExceeded, got %v", err)
	}

	if _, err := store.GetDay(ctx, date); !errors.Is(err, ErrRecordNotFound) {
		t.Errorf("expected no record written on capacity failure, got %v", err)
	}
}

// TestWorkflow_OldSchemaV1FileStillReadable proves the additive omitempty fields
// do not break reads of pre-S3 SchemaVersion 1 day files that lack them.
func TestWorkflow_OldSchemaV1FileStillReadable(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-08"

	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("NewJSONFileStore: %v", err)
	}

	oldDoc := `{"schema_version":1,"date":"2026-10-08","revision":1,"updated_at":"2026-10-08T00:00:00Z",` +
		`"evidence":[],"confirmed":[],"remote_mappings":[],"outcomes":[]}`
	path := filepath.Join(root, "workflow", date+".json")
	if err := os.WriteFile(path, []byte(oldDoc), FilePermPrivate); err != nil {
		t.Fatalf("write old day file: %v", err)
	}

	got, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("old SchemaVersion 1 day file must remain readable, got %v", err)
	}
	if got.CollectedEvidence != nil || got.Collection != nil {
		t.Errorf("expected absent additive fields to stay nil, got %+v / %+v", got.CollectedEvidence, got.Collection)
	}
}

// TestWorkflow_IncompleteCollectionSummarySurvivesReload proves an explicitly
// incomplete CollectionSummary (Complete=false) persists and reloads intact, so a
// partial draft is never silently presented as complete.
func TestWorkflow_IncompleteCollectionSummarySurvivesReload(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-08"

	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("NewJSONFileStore: %v", err)
	}

	summary := evidence.CollectionSummary{
		Date:     date,
		Complete: false,
		Sources: []evidence.SourceResult{
			{SourceID: "cal", Type: evidence.SourceCalendar, Status: evidence.StatusIncomplete, ErrorCode: evidence.CodeSourceTimeout},
			{SourceID: "git", Type: evidence.SourceLocalGit, Status: evidence.StatusComplete, EvidenceCount: 1},
		},
	}

	if _, err := store.UpdateDay(ctx, date, func(r *Record) error {
		r.Collection = &summary
		return nil
	}); err != nil {
		t.Fatalf("UpdateDay failed: %v", err)
	}

	fresh, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("fresh NewJSONFileStore: %v", err)
	}
	got, err := fresh.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay failed: %v", err)
	}
	if got.Collection == nil {
		t.Fatal("Collection did not survive reload")
	}
	if got.Collection.Complete {
		t.Error("Complete=false did not survive reload")
	}
	if gotJSON, wantJSON := marshalJSON(t, got.Collection), marshalJSON(t, &summary); gotJSON != wantJSON {
		t.Errorf("incomplete Collection changed across reload:\n got=%s\nwant=%s", gotJSON, wantJSON)
	}
}

// secretRefPlugin is a minimal plugin declaring one secret config key, used to
// prove a declared secret value never reaches disk.
type secretRefPlugin struct{}

func (secretRefPlugin) Type() evidence.SourceKind { return evidence.SourceManual }
func (secretRefPlugin) RequiredConfig() []evidence.ConfigRequirement {
	return []evidence.ConfigRequirement{{Key: "token", Secret: true, Required: true}}
}
func (secretRefPlugin) DeclaredEvidenceKinds() []evidence.SourceKind {
	return []evidence.SourceKind{evidence.SourceManual}
}
func (secretRefPlugin) CheckContract(context.Context, evidence.SourceConfig) []evidence.ContractCheck {
	return nil
}
func (secretRefPlugin) Collect(context.Context, evidence.SourceConfig, evidence.CollectRequest) (evidence.CollectResult, error) {
	ev := evidence.Evidence{ID: evidence.StableID(evidence.SourceManual, "a"), Kind: evidence.SourceManual, Title: "work"}
	return evidence.CollectResult{
		Evidence: []evidence.Evidence{ev},
		Result:   evidence.SourceResult{Status: evidence.StatusComplete, EvidenceCount: 1},
	}, nil
}

// TestWorkflow_NoDeclaredSecretOnDisk proves the persisted day JSON never carries
// a declared secret config value (nor its keychain reference).
func TestWorkflow_NoDeclaredSecretOnDisk(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	date := "2026-10-08"

	const sentinel = "SENTINEL-SECRET-4c1d"
	secretRef := "keychain:svc-" + sentinel

	reg := evidence.NewRegistry()
	reg.Register(secretRefPlugin{})
	ev, summary, err := reg.CollectDay(ctx, []evidence.SourceConfig{{
		ID: "s", Type: evidence.SourceManual, Enabled: true,
		Config: map[string]string{"token": secretRef},
	}}, evidence.CollectRequest{Date: date})
	if err != nil {
		t.Fatalf("CollectDay failed: %v", err)
	}
	if !summary.Complete {
		t.Fatalf("expected complete collection, got %+v", summary)
	}

	store, err := NewJSONFileStore(root)
	if err != nil {
		t.Fatalf("NewJSONFileStore: %v", err)
	}
	if _, err := store.UpdateDay(ctx, date, func(r *Record) error {
		r.CollectedEvidence = ev
		r.Collection = &summary
		return nil
	}); err != nil {
		t.Fatalf("UpdateDay failed: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(root, "workflow", date+".json"))
	if err != nil {
		t.Fatalf("read persisted day file: %v", err)
	}
	for _, secret := range []string{sentinel, secretRef} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("declared secret value %q leaked into the persisted day JSON", secret)
		}
	}
}
