package export

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

func TestDeriveStatus_AllStates(t *testing.T) {
	base := workflow.Record{Date: "2026-10-06"}

	unresolved := base
	unresolved.Outcomes = []workflow.OperationOutcome{{OperationKey: "k", Status: workflow.OutcomeInFlight}}
	partial := base
	partial.Outcomes = []workflow.OperationOutcome{
		{OperationKey: "a", Status: workflow.OutcomeConfirmed, Resolved: true},
		{OperationKey: "b", Status: workflow.OutcomeFailed, Resolved: true},
	}
	submitted := base
	submitted.Outcomes = []workflow.OperationOutcome{{OperationKey: "a", Status: workflow.OutcomeConfirmed, Resolved: true}}
	allFailed := base
	allFailed.Outcomes = []workflow.OperationOutcome{{OperationKey: "a", Status: workflow.OutcomeFailed, Resolved: true}}
	empty := base
	empty.Review = &workflow.ReviewState{Reviewed: true, Empty: true}
	reviewed := base
	reviewed.Review = &workflow.ReviewState{Reviewed: true}

	cases := []struct {
		name string
		rec  workflow.Record
		want Status
	}{
		{"unresolved", unresolved, StatusUnresolved},
		{"partial", partial, StatusPartial},
		{"failed", allFailed, StatusFailed},
		{"submitted", submitted, StatusSubmitted},
		{"reviewed-empty", empty, StatusReviewedEmpty},
		{"reviewed-not-submitted", reviewed, StatusReviewedNotSubmitted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveStatus(tc.rec); got != tc.want {
				t.Fatalf("want %s, got %s", tc.want, got)
			}
		})
	}
}

// S8L1-2: precedence interactions, including all-failed.
func TestDeriveStatus_Precedence(t *testing.T) {
	base := workflow.Record{Date: "2026-10-06"}
	failedOutcome := workflow.OperationOutcome{OperationKey: "a", Status: workflow.OutcomeFailed, Resolved: true}

	allFailed := base
	allFailed.Outcomes = []workflow.OperationOutcome{failedOutcome}
	if got := DeriveStatus(allFailed); got != StatusFailed {
		t.Fatalf("all-failed must render failed, got %s", got)
	}

	// unresolved beats failed.
	withUnresolved := base
	withUnresolved.Outcomes = []workflow.OperationOutcome{failedOutcome, {OperationKey: "b", Status: workflow.OutcomeInFlight}}
	if got := DeriveStatus(withUnresolved); got != StatusUnresolved {
		t.Fatalf("unresolved must beat failed, got %s", got)
	}

	// partial beats failed.
	withConfirmed := base
	withConfirmed.Outcomes = []workflow.OperationOutcome{failedOutcome, {OperationKey: "c", Status: workflow.OutcomeConfirmed, Resolved: true}}
	if got := DeriveStatus(withConfirmed); got != StatusPartial {
		t.Fatalf("a failed+confirmed mix must be partial, got %s", got)
	}

	// failed beats reviewed-empty.
	failedEmpty := allFailed
	failedEmpty.Review = &workflow.ReviewState{Reviewed: true, Empty: true}
	if got := DeriveStatus(failedEmpty); got != StatusFailed {
		t.Fatalf("failed must beat reviewed-empty, got %s", got)
	}
}

func TestExport_IdempotentAndPreservesJournal(t *testing.T) {
	ctx := context.Background()
	vault := t.TempDir()
	dir := "Daily"
	full := filepath.Join(vault, dir)
	if err := os.MkdirAll(full, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(full, "2026-10-06.md")
	original := "---\ntitle: Day\ntags: [x]\n---\n\n## Work journal\n\nmy notes\n\n## Trailing\n\nend\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	exp := NewNoteExporter(vault, dir)
	note := Note{
		Date: "2026-10-06", Status: StatusReviewedNotSubmitted,
		Confirmed: []Row{{Start: "09:00", End: "10:00", Project: "P", Description: "work"}},
	}

	if _, err := exp.Export(ctx, note); err != nil {
		t.Fatalf("Export: %v", err)
	}
	first, _ := os.ReadFile(path)
	if _, err := exp.Export(ctx, note); err != nil {
		t.Fatalf("Export 2: %v", err)
	}
	second, _ := os.ReadFile(path)

	if string(first) != string(second) {
		t.Fatalf("a repeat export must be byte-identical:\n%s\n---\n%s", first, second)
	}
	if strings.Count(string(second), MarkerBegin) != 1 || strings.Count(string(second), MarkerEnd) != 1 {
		t.Fatalf("want exactly one marker block, got %d begin / %d end",
			strings.Count(string(second), MarkerBegin), strings.Count(string(second), MarkerEnd))
	}
	for _, want := range []string{"title: Day", "tags: [x]", "## Work journal", "my notes", "## Trailing", "end"} {
		if !strings.Contains(string(second), want) {
			t.Errorf("must preserve %q\n%s", want, second)
		}
	}
	if !strings.Contains(string(second), "09:00") || !strings.Contains(string(second), "work") {
		t.Errorf("want the managed content, got:\n%s", second)
	}
}

// S8L1-4: the atomic write leaves no temp file behind.
func TestExport_LeavesNoTempFiles(t *testing.T) {
	ctx := context.Background()
	vault := t.TempDir()
	exp := NewNoteExporter(vault, "Daily")
	if _, err := exp.Export(ctx, Note{Date: "2026-10-06", Status: StatusReviewedNotSubmitted}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(vault, "Daily"))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".timesheet-") {
			t.Fatalf("a temp file was left behind: %s", e.Name())
		}
	}
	if len(entries) != 1 || entries[0].Name() != "2026-10-06.md" {
		t.Fatalf("unexpected directory contents: %+v", entries)
	}
}

func TestExport_AppendsWhenMarkersAbsentAndIsSecretFree(t *testing.T) {
	ctx := context.Background()
	vault := t.TempDir()
	exp := NewNoteExporter(vault, "Daily")
	note := Note{
		Date: "2026-10-06", Status: StatusUnresolved,
		Outcomes: []Row{{LocalID: "c1", Status: "uncertain", Code: "GRADION_WRITE_UNCERTAIN"}},
	}
	if _, err := exp.Export(ctx, note); err != nil {
		t.Fatalf("Export: %v", err)
	}
	blob, err := os.ReadFile(filepath.Join(vault, "Daily", "2026-10-06.md"))
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	if !strings.Contains(string(blob), MarkerBegin) || !strings.Contains(string(blob), MarkerEnd) {
		t.Fatalf("want a marker block, got:\n%s", blob)
	}
	if !strings.Contains(string(blob), "GRADION_WRITE_UNCERTAIN") {
		t.Errorf("want the stable code surfaced, got:\n%s", blob)
	}
	for _, forbidden := range []string{"Bearer ", "keychain:", "http://", "https://"} {
		if strings.Contains(string(blob), forbidden) {
			t.Errorf("the note must never contain %q:\n%s", forbidden, blob)
		}
	}
}
