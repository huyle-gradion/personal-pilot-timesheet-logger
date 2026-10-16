package gradion

import (
	"errors"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/mcp"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

func TestBuildMapping_EmptyRemoteID(t *testing.T) {
	_, err := BuildMapping("l1", PlannedEntry{Date: "2026-10-06"}, workflow.RemoteState{}, time.Now())
	if CodeOf(err) != CodeEmptyRemoteID {
		t.Fatalf("expected %s, got %v", CodeEmptyRemoteID, err)
	}
}

func TestBuildMapping_OK(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	entry := workflow.RemoteState{
		RemoteID: "uuid-1", Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00",
		ClassificationID: "c1", Description: "work", ContentObserved: true,
	}
	m, err := BuildMapping("l1", PlannedEntry{
		Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00",
		ClassificationID: "c1", Description: "work",
	}, entry, now)
	if err != nil {
		t.Fatalf("BuildMapping: %v", err)
	}
	if m.LocalID != "l1" || m.RemoteID != "uuid-1" {
		t.Errorf("mapping identity wrong: %+v", m)
	}
	if !m.LastObserved.ObservedAt.Equal(now) {
		t.Errorf("ObservedAt not stamped: %v", m.LastObserved.ObservedAt)
	}
}

// A mapping must never be built from a plan echo / identity-only state: an
// unobserved entry fails closed rather than becoming LastObserved.
func TestBuildMapping_UnobservedEntryFailsClosed(t *testing.T) {
	entry := workflow.RemoteState{RemoteID: "uuid-1", Date: "2026-10-06"}
	if _, err := BuildMapping("l1", PlannedEntry{Date: "2026-10-06"}, entry, time.Now()); CodeOf(err) != CodeUnobservedEntry {
		t.Fatalf("expected %s, got %v", CodeUnobservedEntry, err)
	}
}

func TestBuildOutcome_StatusesAndResolution(t *testing.T) {
	now := time.Now().UTC()
	planned := PlannedEntry{Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00", Description: "work", ClassificationID: "cls-2"}
	cases := []struct {
		name     string
		res      WriteResult
		status   workflow.OutcomeStatus
		resolved bool
	}{
		{"confirmed", WriteResult{Status: WriteConfirmed, Entry: &workflow.RemoteState{RemoteID: "uuid-1", ContentObserved: true}}, workflow.OutcomeConfirmed, true},
		{"uncertain", WriteResult{Status: WriteUncertain, ErrorCode: CodeWriteUncertain}, workflow.OutcomeUncertain, false},
		{"unidentified", WriteResult{Status: WriteUnidentified, ErrorCode: CodeWriteUnidentified}, workflow.OutcomeUnidentified, false},
		{"failed", WriteResult{Status: WriteFailed, ErrorCode: CodeWriteFailed}, workflow.OutcomeFailed, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := BuildOutcome("create", "l1", planned, tc.res, now)
			if out.Status != tc.status {
				t.Errorf("status = %s, want %s", out.Status, tc.status)
			}
			if out.Resolved != tc.resolved {
				t.Errorf("resolved = %v, want %v", out.Resolved, tc.resolved)
			}
			if out.OperationKey == "" {
				t.Error("operation key must be set")
			}
			if out.ClassificationID != "cls-2" {
				t.Errorf("classification = %q, want cls-2 (carried from the plan)", out.ClassificationID)
			}
			if tc.status == workflow.OutcomeConfirmed && out.RemoteID != "uuid-1" {
				t.Errorf("confirmed outcome missing remote id: %+v", out)
			}
		})
	}
}

// TestBuildOutcome_ConfirmedWithoutRemoteIDIsUnidentified proves a confirmed
// result with no stable remote id is downgraded to unidentified (F4).
func TestBuildOutcome_ConfirmedWithoutRemoteIDIsUnidentified(t *testing.T) {
	now := time.Now().UTC()
	planned := PlannedEntry{Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00"}
	cases := []struct {
		name string
		res  WriteResult
	}{
		{"nil entry", WriteResult{Status: WriteConfirmed}},
		{"empty id", WriteResult{Status: WriteConfirmed, Entry: &workflow.RemoteState{ContentObserved: true}}},
		{"whitespace id", WriteResult{Status: WriteConfirmed, Entry: &workflow.RemoteState{RemoteID: "  ", ContentObserved: true}}},
		{"unobserved entry (plan echo)", WriteResult{Status: WriteConfirmed, Entry: &workflow.RemoteState{RemoteID: "uuid-1"}}},
	}
	for _, tc := range cases {
		out := BuildOutcome("create", "l1", planned, tc.res, now)
		if out.Status != workflow.OutcomeUnidentified || out.Resolved {
			t.Errorf("%s: status=%s resolved=%v, want unidentified/false", tc.name, out.Status, out.Resolved)
		}
		if out.ErrorCode == "" {
			t.Errorf("%s: empty error code", tc.name)
		}
	}
}

// TestBuildOutcome_OperationKeyMatchesPreWritePlan proves the operation key is
// derived from the PRE-WRITE plan (empty remote id for a create), so it matches
// the in_flight intent row.
func TestBuildOutcome_OperationKeyMatchesPreWritePlan(t *testing.T) {
	planned := PlannedEntry{RemoteID: "", Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00"}
	out := BuildOutcome("create", "l1", planned,
		WriteResult{Status: WriteConfirmed, Entry: &workflow.RemoteState{RemoteID: "uuid-1", ContentObserved: true}}, time.Now())
	want := workflow.DeriveOperationKey(workflow.OperationOutcome{
		Date: "2026-10-06", Action: "create", RemoteID: "", LocalID: "l1",
		StartTime: "09:00", EndTime: "10:00",
	})
	if out.OperationKey != want {
		t.Errorf("operation key = %q, want %q", out.OperationKey, want)
	}
}

// TestBuildOutcome_IdentifiedPreservesRemoteID proves an identity-acknowledged
// write is UNRESOLVED (uncertain) and preserves the acknowledged remote id so the
// caller can reconcile it.
func TestBuildOutcome_IdentifiedPreservesRemoteID(t *testing.T) {
	planned := PlannedEntry{Date: "2026-10-06", StartTime: "09:00", EndTime: "10:00"}
	out := BuildOutcome("create", "l1", planned,
		WriteResult{Status: WriteIdentified, RemoteID: "uuid-9"}, time.Now())
	if out.Status != workflow.OutcomeUncertain || out.Resolved {
		t.Fatalf("identified must be unresolved uncertain, got %+v", out)
	}
	if out.RemoteID != "uuid-9" {
		t.Errorf("acknowledged remote id must be preserved, got %q", out.RemoteID)
	}
}

// TestClassifyWrite pins the write uncertainty taxonomy (T3/T4).
func TestClassifyWrite(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want WriteStatus
	}{
		{"transport", &mcp.Error{Kind: mcp.ErrTransport}, WriteUncertain},
		{"timeout", &mcp.Error{Kind: mcp.ErrTransport, StableCode: mcp.CodeTimeout}, WriteUncertain},
		{"408", &mcp.Error{Kind: mcp.ErrHTTPStatus, StatusCode: 408}, WriteUncertain},
		{"409", &mcp.Error{Kind: mcp.ErrHTTPStatus, StatusCode: 409}, WriteUncertain},
		{"425", &mcp.Error{Kind: mcp.ErrHTTPStatus, StatusCode: 425}, WriteUncertain},
		{"429", &mcp.Error{Kind: mcp.ErrHTTPStatus, StatusCode: 429}, WriteUncertain},
		{"500", &mcp.Error{Kind: mcp.ErrHTTPStatus, StatusCode: 500}, WriteUncertain},
		{"400", &mcp.Error{Kind: mcp.ErrHTTPStatus, StatusCode: 400}, WriteFailed},
		{"401", &mcp.Error{Kind: mcp.ErrHTTPStatus, StatusCode: 401}, WriteFailed},
		{"404", &mcp.Error{Kind: mcp.ErrHTTPStatus, StatusCode: 404}, WriteFailed},
		{"request error code", &mcp.Error{Kind: mcp.ErrProtocol, Code: -32602}, WriteFailed},
		{"other rpc error code", &mcp.Error{Kind: mcp.ErrProtocol, Code: -32000}, WriteUncertain},
		{"schema", &mcp.Error{Kind: mcp.ErrSchema, StableCode: mcp.CodeSchema}, WriteUncertain},
		{"uninitialized", &mcp.Error{Kind: mcp.ErrUninitialized}, WriteFailed},
		{"unknown", errors.New("boom"), WriteUncertain},
	}
	for _, tc := range cases {
		if got := classifyWrite(tc.err).Status; got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
}
