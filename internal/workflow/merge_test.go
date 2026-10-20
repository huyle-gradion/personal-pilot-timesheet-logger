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

// TestNextAttemptOperationKey pins the ATTEMPT-SCOPED key contract: the first
// dispatch at an operation identity keeps the unsuffixed DeriveOperationKey, and
// only a second-and-later dispatch at the SAME identity takes the additive
// "\x1f#<N>" suffix. It also pins that the suffix can never be confused with a
// real six-field base key, and that DeriveOperationKey itself is unchanged.
func TestNextAttemptOperationKey(t *testing.T) {
	base := DeriveOperationKey(OperationOutcome{
		Date: "2026-10-06", Action: "edit", RemoteID: "r1", LocalID: "c1",
		StartTime: "09:00", EndTime: "10:00",
	})
	// DeriveOperationKey is UNCHANGED: still the six stable fields joined by the
	// unit separator, byte-for-byte.
	if want := "2026-10-06\x1fedit\x1fr1\x1fc1\x1f09:00\x1f10:00"; base != want {
		t.Fatalf("DeriveOperationKey changed: got %q want %q", base, want)
	}

	// No row at the identity: the FIRST attempt stays unsuffixed, so every
	// existing row, plan and fingerprint is untouched (no migration).
	for name, rec := range map[string]*Record{
		"nil record": nil,
		"empty":      {},
		"other rows": {Outcomes: []OperationOutcome{{OperationKey: base + "-other"}, {OperationKey: "unrelated"}}},
	} {
		if got := NextAttemptOperationKey(rec, base); got != base {
			t.Errorf("%s: want the bare base key, got %q", name, got)
		}
	}

	terminal := &Record{Outcomes: []OperationOutcome{{
		OperationKey: base, Status: OutcomeConfirmed, Resolved: true,
	}}}
	if got := NextAttemptOperationKey(terminal, base); got != base+"\x1f#2" {
		t.Errorf("a taken base identity must take attempt 2, got %q", got)
	}

	// A SECOND attempt on the record advances to attempt 3, whatever the earlier
	// attempt's status.
	second := &Record{Outcomes: []OperationOutcome{
		{OperationKey: base, Status: OutcomeConfirmed, Resolved: true},
		{OperationKey: base + "\x1f#2", Status: OutcomeUncertain, Resolved: false},
	}}
	got := NextAttemptOperationKey(second, base)
	if got != base+"\x1f#3" {
		t.Errorf("a record holding attempts 1 and 2 must take attempt 3, got %q", got)
	}

	// The suffixed key is ADDITIVE (base keeps its prefix) and carries exactly one
	// more separator than any base key, so it can never collide with a real
	// six-field DeriveOperationKey output.
	if !strings.HasPrefix(got, base) {
		t.Errorf("an attempt key must keep the base as a prefix, got %q", got)
	}
	if strings.Count(got, operationKeySeparator) != strings.Count(base, operationKeySeparator)+1 {
		t.Errorf("an attempt key must add exactly one separator, got %q", got)
	}
	rows := []OperationOutcome{
		{Date: "2026-10-06", Action: "edit", RemoteID: "r1", LocalID: "c1", StartTime: "09:00", EndTime: "10:00"},
		{Date: "2026-10-06", Action: "create", RemoteID: "", LocalID: "c1", StartTime: "09:00", EndTime: "10:00"},
		{Date: "2026-10-07", Action: "edit", RemoteID: "r1", LocalID: "c1", StartTime: "09:00", EndTime: "10:00"},
		{Date: "2026-10-06", Action: "edit", RemoteID: "r1", LocalID: "c2", StartTime: "09:00", EndTime: "10:00"},
		{Date: "2026-10-06", Action: "edit", RemoteID: "r1", LocalID: "c1", StartTime: "09:00", EndTime: "11:00"},
	}
	for _, row := range rows {
		if DeriveOperationKey(row) == base+"\x1f#2" {
			t.Fatalf("a suffixed attempt key collided with a real base key: %q", base+"\x1f#2")
		}
		if strings.Contains(DeriveOperationKey(row), attemptKeyMarker) {
			t.Errorf("DeriveOperationKey must never emit the attempt marker: %q", DeriveOperationKey(row))
		}
	}

	// A key that merely SHARES the base prefix is not an attempt of the base, so a
	// suffixed or extended key of a longer operation can never inflate this
	// counter: with no row AT the base identity the first attempt is still
	// unsuffixed.
	longer := &Record{Outcomes: []OperationOutcome{
		{OperationKey: base + "\x1f#9x"},
		{OperationKey: base + "-other"},
		{OperationKey: base + "\x1f#02"},
	}}
	if got := NextAttemptOperationKey(longer, base); got != base {
		t.Errorf("a non-canonical suffix must not count as an attempt, got %q", got)
	}
}

// TestLatestOutcomePerOperation pins the human-surface projection: one row per
// operation identity, the LATEST attempt of each, deterministic (RecordedAt, then
// IntentAt, then ledger position), and never collapsing two different operations.
func TestLatestOutcomePerOperation(t *testing.T) {
	first := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	later := first.Add(time.Minute)
	base := DeriveOperationKey(OperationOutcome{
		Date: "2026-10-06", Action: "edit", RemoteID: "r1", LocalID: "c1",
		StartTime: "09:00", EndTime: "10:00",
	})
	other := DeriveOperationKey(OperationOutcome{
		Date: "2026-10-06", Action: "create", RemoteID: "", LocalID: "c2",
		StartTime: "11:00", EndTime: "12:00",
	})

	rec := &Record{Outcomes: []OperationOutcome{
		{OperationKey: base, LocalID: "c1", Status: OutcomeFailed, Resolved: true, RecordedAt: first},
		{OperationKey: other, LocalID: "c2", Status: OutcomeConfirmed, Resolved: true, RecordedAt: first},
		{OperationKey: base + "\x1f#2", LocalID: "c1", Status: OutcomeConfirmed, Resolved: true, RecordedAt: later},
	}}
	got := LatestOutcomePerOperation(rec)
	if len(got) != 2 {
		t.Fatalf("want one row per operation, got %d: %+v", len(got), got)
	}
	if got[0].OperationKey != base+"\x1f#2" || got[0].Status != OutcomeConfirmed {
		t.Errorf("want the latest attempt of the first operation, got %+v", got[0])
	}
	if got[1].OperationKey != other {
		t.Errorf("want the untouched second operation, got %+v", got[1])
	}

	// DECISIVE case for the grouping rule: a retried EDIT whose first attempt
	// FAILED. A failed row records NO RemoteID, so DeriveOperationKey(row) would
	// re-derive it with an EMPTY remote id ("...\x1f\x1fc1\x1f...") while the
	// confirmed retry re-derives WITH the entry id — two groups, and therefore a
	// failed-plus-confirmed mix. Grouping follows the row's own key: ONE
	// operation, the retry. This case FAILS if operationGroupKey is replaced by
	// DeriveOperationKey(row), which is why it exists.
	failedEdit := &Record{Outcomes: []OperationOutcome{
		{
			OperationKey: base, LocalID: "c1", RemoteID: "", Action: "edit",
			Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00",
			Status: OutcomeFailed, Resolved: true, RecordedAt: first,
		},
		{
			OperationKey: base + "\x1f#2", LocalID: "c1", RemoteID: "r1", Action: "edit",
			Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00",
			Status: OutcomeConfirmed, Resolved: true, RecordedAt: later,
		},
	}}
	retried := LatestOutcomePerOperation(failedEdit)
	if len(retried) != 1 {
		t.Fatalf("a failed attempt and its confirmed retry are ONE operation, got %d: %+v", len(retried), retried)
	}
	if retried[0].OperationKey != base+"\x1f#2" || retried[0].Status != OutcomeConfirmed || retried[0].RemoteID != "r1" {
		t.Errorf("want the confirmed retry projected, got %+v", retried[0])
	}

	// The same hazard on the CREATE path (the second instance): the CONFIRMED
	// retry records the OBSERVED entry id while the planned key used the empty
	// pre-write id, so the two attempts are ONE operation, not two.
	createKey := DeriveOperationKey(OperationOutcome{
		Date: "2026-10-06", Action: "create", RemoteID: "", LocalID: "c3",
		StartTime: "13:00", EndTime: "14:00",
	})
	create := &Record{Outcomes: []OperationOutcome{
		{
			OperationKey: createKey, LocalID: "c3", RemoteID: "", Action: "create",
			Date: "2026-10-06", StartTime: "13:00", EndTime: "14:00",
			Status: OutcomeFailed, Resolved: true, RecordedAt: first,
		},
		{
			OperationKey: createKey + "\x1f#2", LocalID: "c3", RemoteID: "new-2", Action: "create",
			Date: "2026-10-06", StartTime: "13:00", EndTime: "14:00",
			Status: OutcomeConfirmed, Resolved: true, RecordedAt: later,
		},
	}}
	if rows := LatestOutcomePerOperation(create); len(rows) != 1 || rows[0].Status != OutcomeConfirmed {
		t.Fatalf("two attempts of ONE create must project to one row, got %+v", rows)
	}

	// Equal timestamps resolve to the later ledger position, deterministically.
	tied := &Record{Outcomes: []OperationOutcome{
		{OperationKey: base, LocalID: "c1", Status: OutcomeFailed, Resolved: true, RecordedAt: first},
		{OperationKey: base + "\x1f#2", LocalID: "c1", Status: OutcomeConfirmed, Resolved: true, RecordedAt: first},
	}}
	if rows := LatestOutcomePerOperation(tied); len(rows) != 1 || rows[0].Status != OutcomeConfirmed {
		t.Fatalf("a timestamp tie must resolve to the later attempt, got %+v", rows)
	}

	if rows := LatestOutcomePerOperation(nil); rows != nil {
		t.Errorf("a nil record must project to nothing, got %+v", rows)
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
