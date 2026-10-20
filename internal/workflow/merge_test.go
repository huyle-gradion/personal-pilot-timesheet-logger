package workflow

import (
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestMergeEvidence_PreservesConfirmed proves an evidence refresh does not touch
// confirmed intervals or their user-authored Description/Project.
func TestMergeEvidence_PreservesConfirmed(t *testing.T) {
	r := &Record{}
	MergeConfirmed(r, []ConfirmedInterval{{
		ID:          "confirmed-1",
		Date:        "2026-10-08",
		StartTime:   "09:00",
		EndTime:     "10:00",
		Project:     "PersonalPilot",
		Description: "user kept description",
	}})

	MergeEvidence(r, []EvidenceReference{{
		ID:     "evidence-1",
		Source: "git",
		Title:  "commit",
		Ref:    "org/repo#123",
	}})

	if len(r.Confirmed) != 1 {
		t.Fatalf("expected 1 confirmed interval, got %d", len(r.Confirmed))
	}
	if r.Confirmed[0].Description != "user kept description" {
		t.Errorf("evidence refresh clobbered user description: %q", r.Confirmed[0].Description)
	}
	if r.Confirmed[0].Project != "PersonalPilot" {
		t.Errorf("evidence refresh clobbered user project: %q", r.Confirmed[0].Project)
	}
	if len(r.Evidence) != 1 {
		t.Errorf("expected 1 evidence reference, got %d", len(r.Evidence))
	}
}

// TestMergeConfirmed_PreservesUserFieldsAndSetsConfirmedAt proves an upsert keeps
// existing user fields when incoming ones are empty and stamps ConfirmedAt.
func TestMergeConfirmed_PreservesUserFieldsAndSetsConfirmedAt(t *testing.T) {
	r := &Record{}
	MergeConfirmed(r, []ConfirmedInterval{{
		ID: "c1", Date: "2026-10-08", StartTime: "09:00", EndTime: "10:00",
		Project: "Proj", Description: "original",
	}})

	MergeConfirmed(r, []ConfirmedInterval{{
		ID: "c1", Date: "2026-10-08", StartTime: "09:00", EndTime: "11:00",
	}})

	if len(r.Confirmed) != 1 {
		t.Fatalf("expected 1 interval after upsert, got %d", len(r.Confirmed))
	}
	if r.Confirmed[0].Description != "original" || r.Confirmed[0].Project != "Proj" {
		t.Errorf("empty incoming fields clobbered existing values: %+v", r.Confirmed[0])
	}
	if r.Confirmed[0].EndTime != "11:00" {
		t.Errorf("expected updated end time, got %q", r.Confirmed[0].EndTime)
	}
	if r.Confirmed[0].ConfirmedAt.IsZero() {
		t.Errorf("expected ConfirmedAt to be stamped")
	}
}

// TestUpsertOutcome_DoesNotOverwriteConfirmed proves a confirmed success is never
// replaced by a later failed/uncertain outcome with the same key.
func TestUpsertOutcome_DoesNotOverwriteConfirmed(t *testing.T) {
	r := &Record{}
	UpsertOutcome(r, OperationOutcome{OperationKey: "k", LocalID: "a", Action: "create", Status: OutcomeConfirmed, Resolved: true})
	UpsertOutcome(r, OperationOutcome{OperationKey: "k", LocalID: "a", Action: "create", Status: OutcomeUncertain, Resolved: false})
	UpsertOutcome(r, OperationOutcome{OperationKey: "k", LocalID: "a", Action: "create", Status: OutcomeFailed, Resolved: true})

	if len(r.Outcomes) != 1 {
		t.Fatalf("expected 1 outcome, got %d", len(r.Outcomes))
	}
	if r.Outcomes[0].Status != OutcomeConfirmed {
		t.Errorf("confirmed success was overwritten: got %s", r.Outcomes[0].Status)
	}
}

// TestUpsertOutcome_DerivesOperationKey proves empty keys are derived via
// DeriveOperationKey, so distinct empty-key slots never collide.
func TestUpsertOutcome_DerivesOperationKey(t *testing.T) {
	r := &Record{}
	a := OperationOutcome{LocalID: "local-a", Action: "create", Status: OutcomeConfirmed}
	UpsertOutcome(r, a)
	if r.Outcomes[0].OperationKey != DeriveOperationKey(a) {
		t.Errorf("expected DeriveOperationKey fallback, got %q", r.Outcomes[0].OperationKey)
	}

	b := OperationOutcome{Date: "2026-10-08", StartTime: "09:00", EndTime: "10:00", Action: "create", Status: OutcomeFailed}
	c := OperationOutcome{Date: "2026-10-08", StartTime: "10:00", EndTime: "11:00", Action: "create", Status: OutcomeFailed}
	UpsertOutcome(r, b)
	UpsertOutcome(r, c)

	if len(r.Outcomes) != 3 {
		t.Fatalf("expected 3 outcomes, got %d", len(r.Outcomes))
	}
	if r.Outcomes[1].OperationKey != DeriveOperationKey(b) {
		t.Errorf("expected DeriveOperationKey fallback, got %q", r.Outcomes[1].OperationKey)
	}
	if r.Outcomes[2].OperationKey != DeriveOperationKey(c) {
		t.Errorf("expected DeriveOperationKey fallback, got %q", r.Outcomes[2].OperationKey)
	}
	if r.Outcomes[1].OperationKey == r.Outcomes[2].OperationKey {
		t.Errorf("empty-key outcomes for different slots collided on key %q", r.Outcomes[1].OperationKey)
	}
}

// TestUpsertOutcome_EmptyKeyMatchesIntentRow proves an intent and its outcome,
// both with an empty OperationKey, resolve to the SAME ledger row via
// DeriveOperationKey (no duplicate row).
func TestUpsertOutcome_EmptyKeyMatchesIntentRow(t *testing.T) {
	r := &Record{}
	base := OperationOutcome{
		Date: "2026-10-08", Action: "create", LocalID: "local-a",
		StartTime: "09:00", EndTime: "10:00",
	}
	intent := base
	intent.Status = OutcomeInFlight
	intent.IntentAt = time.Now().UTC()
	UpsertIntent(r, intent)

	outcome := base
	outcome.Status = OutcomeConfirmed
	outcome.Resolved = true
	UpsertOutcome(r, outcome)

	if len(r.Outcomes) != 1 {
		t.Fatalf("expected a single ledger row shared by intent and outcome, got %d: %+v", len(r.Outcomes), r.Outcomes)
	}
	if r.Outcomes[0].Status != OutcomeConfirmed {
		t.Errorf("expected the terminal outcome to replace the in_flight row, got %s", r.Outcomes[0].Status)
	}
	if UnresolvedOperations(r) != nil && len(UnresolvedOperations(r)) != 0 {
		t.Errorf("expected no unresolved operations after the terminal outcome")
	}
}

// TestUpsertMapping_RejectsEmptyRemoteID proves no identity is fabricated.
func TestUpsertMapping_RejectsEmptyRemoteID(t *testing.T) {
	r := &Record{}
	err := UpsertMapping(r, RemoteMapping{LocalID: "a"})
	if !errors.Is(err, ErrMappingWithoutRemoteID) {
		t.Fatalf("expected ErrMappingWithoutRemoteID, got %v", err)
	}
	if len(r.RemoteMappings) != 0 {
		t.Errorf("expected no mapping to be created, got %+v", r.RemoteMappings)
	}
}

// TestUpsertMapping_RejectsEmptyLocalID proves an empty/whitespace LocalID is
// rejected so distinct mappings never collapse onto the same key.
func TestUpsertMapping_RejectsEmptyLocalID(t *testing.T) {
	for _, localID := range []string{"", "   "} {
		r := &Record{}
		err := UpsertMapping(r, RemoteMapping{LocalID: localID, RemoteID: "remote-1"})
		if !errors.Is(err, ErrMappingWithoutLocalID) {
			t.Fatalf("expected ErrMappingWithoutLocalID for %q, got %v", localID, err)
		}
		if len(r.RemoteMappings) != 0 {
			t.Errorf("expected no mapping created for LocalID %q, got %+v", localID, r.RemoteMappings)
		}
	}
}

// TestUpsertMapping_PreservesCreatedAt proves an update preserves CreatedAt while
// refreshing RemoteID/LastObserved and bumping UpdatedAt.
func TestUpsertMapping_PreservesCreatedAt(t *testing.T) {
	r := &Record{}
	if err := UpsertMapping(r, RemoteMapping{LocalID: "a", RemoteID: "remote-1"}); err != nil {
		t.Fatalf("initial upsert failed: %v", err)
	}
	createdAt := r.RemoteMappings[0].CreatedAt

	if err := UpsertMapping(r, RemoteMapping{
		LocalID: "a", RemoteID: "remote-2",
		LastObserved: RemoteState{RemoteID: "remote-2"},
	}); err != nil {
		t.Fatalf("update upsert failed: %v", err)
	}

	if len(r.RemoteMappings) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(r.RemoteMappings))
	}
	if r.RemoteMappings[0].RemoteID != "remote-2" {
		t.Errorf("expected refreshed remote ID, got %q", r.RemoteMappings[0].RemoteID)
	}
	if !r.RemoteMappings[0].CreatedAt.Equal(createdAt) {
		t.Errorf("CreatedAt was not preserved")
	}
	if r.RemoteMappings[0].LastObserved.RemoteID != "remote-2" {
		t.Errorf("expected refreshed LastObserved, got %+v", r.RemoteMappings[0].LastObserved)
	}
}

// TestNoTimeSlotInferenceFunction proves no function in this package derives a
// remote mapping identity from time-slot equality.
func TestNoTimeSlotInferenceFunction(t *testing.T) {
	badName := regexp.MustCompile(`(?i)(infer|guess|derive|slot)`)
	fset := token.NewFileSet()

	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("failed to parse package source: %v", err)
	}

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				fn, ok := n.(*ast.FuncDecl)
				if !ok {
					return true
				}
				// DeriveOperationKey is an explicit allowlist: it derives a
				// deterministic OPERATION key (date/action/ids/slot, unit
				// separated) for the operation ledger. It never fabricates a
				// REMOTE MAPPING identity from time-slot equality, which is what
				// this guard exists to prevent.
				if fn.Name.Name == "DeriveOperationKey" {
					return true
				}
				if badName.MatchString(fn.Name.Name) {
					t.Errorf("found time-slot inference function %q; mappings must only use stable remote IDs", fn.Name.Name)
				}
				return true
			})
		}
	}
}

// TestOperationOutcome_OriginErrorCodeOmitemptyAndHashNeutral specifies the
// ADDITIVE OperationOutcome.OriginErrorCode contract: a row with an empty value
// marshals with NO origin_error_code key and a non-empty value serialises it, so
// a legacy record marshals byte-identically. It further asserts the field does not
// feed LocalFingerprint.
func TestOperationOutcome_OriginErrorCodeOmitemptyAndHashNeutral(t *testing.T) {
	legacy := OperationOutcome{
		OperationKey: "k1", LocalID: "c1", Action: string(PlanOpEdit), Status: OutcomeUncertain,
		Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00", Description: "work",
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy outcome: %v", err)
	}
	if strings.Contains(string(data), "origin_error_code") {
		t.Fatalf("an empty OriginErrorCode must be omitted, got %s", data)
	}

	stamped := legacy
	stamped.OriginErrorCode = "GRADION_WRITE_CONTENT_MISMATCH"
	data, err = json.Marshal(stamped)
	if err != nil {
		t.Fatalf("marshal stamped outcome: %v", err)
	}
	if !strings.Contains(string(data), `"origin_error_code":"GRADION_WRITE_CONTENT_MISMATCH"`) {
		t.Fatalf("a non-empty OriginErrorCode must be serialized, got %s", data)
	}

	rec := Record{
		SchemaVersion: SchemaVersion,
		Date:          "2026-10-06",
		Confirmed: []ConfirmedInterval{{
			ID: "c1", Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00", ClassificationID: "cls-1",
		}},
	}
	f1 := LocalFingerprint(rec)

	// LocalFingerprint hashes confirmed intervals, review decisions and the
	// plan-relevant mapping fields; outcome rows participate in NONE of them, so
	// adding outcome rows must not move it.
	rec.Outcomes = []OperationOutcome{legacy, stamped}
	if LocalFingerprint(rec) != f1 {
		t.Error("OperationOutcome.OriginErrorCode must not feed LocalFingerprint")
	}

	// PlanHash is sha256 over the canonical JSON of
	// SubmissionPlan{Date, LocalFingerprint, RemoteObservedAt, RemoteComplete,
	// Operations}. No outcome input participates, so "an outcome field does not
	// feed PlanHash" holds by construction and is recorded here as a statement of
	// the contract rather than asserted (a comparison over the same plan value
	// would be vacuous).
}
