package export

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/storage"
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
	// An EXISTING note is never re-seeded: its own frontmatter (which has no
	// `date`) stays byte-for-byte, and nothing is prepended.
	if !strings.HasPrefix(string(second), original) {
		t.Fatalf("the existing note outside the markers must be preserved exactly:\n%s", second)
	}
	if strings.Contains(string(second), storage.DailyNoteFrontmatter("2026-10-06")) {
		t.Fatalf("an existing note must not be re-seeded with the export template:\n%s", second)
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

// TestBuildNote_CarryVisible proves a cross-midnight carry is visible on the
// originating day's export: a carry marker/note naming the later date and the
// linked group, plus a warning bullet.
func TestBuildNote_CarryVisible(t *testing.T) {
	rec := workflow.Record{Date: "2026-10-06", Carries: []workflow.CarryItem{{
		SourceDate: "2026-10-06", Date: "2026-10-07", Start: "00:00", End: "00:30",
		SplitGroupID: "split_ab12", PartIndex: 1, Minutes: 30,
	}}}
	note := BuildNote(rec)
	if len(note.Carries) != 1 {
		t.Fatalf("note carries = %d, want 1", len(note.Carries))
	}
	out := Render(note)
	for _, want := range []string{"2026-10-07", "00:00-00:30", "split_ab12", "carry:", "Carryover"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered note missing %q:\n%s", want, out)
		}
	}
}

// TestRender_CarryoverMarkerOnConfirmedRow proves F5: the WRITTEN note marks the
// D+1 confirmed row as a carryover, naming the linked group.
func TestRender_CarryoverMarkerOnConfirmedRow(t *testing.T) {
	rec := workflow.Record{Date: "2026-10-07", Confirmed: []workflow.ConfirmedInterval{{
		ID: "c1", Date: "2026-10-07", StartTime: "00:00", EndTime: "00:30",
		Project: "P", Description: "late", Carryover: true, SplitGroupID: "split_ab12",
	}}}
	out := Render(BuildNote(rec))
	if !strings.Contains(out, "carryover") || !strings.Contains(out, "split_ab12") {
		t.Fatalf("the written note must mark the D+1 carryover row:\n%s", out)
	}
}

// TestExport_ExistingEmptyNoteNotSeeded pins the creation-only rule: a zero-byte
// file EXISTS, so it is not "created" and must receive only the managed block, no
// frontmatter seeding.
func TestExport_ExistingEmptyNoteNotSeeded(t *testing.T) {
	ctx := context.Background()
	vault := t.TempDir()
	dir := filepath.Join(vault, "Daily")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "2026-10-06.md")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write empty note: %v", err)
	}
	note := Note{Date: "2026-10-06", Status: StatusReviewedNotSubmitted}
	if _, err := NewNoteExporter(vault, "Daily").Export(ctx, note); err != nil {
		t.Fatalf("Export: %v", err)
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	if strings.HasPrefix(string(blob), storage.DailyNoteFrontmatter(note.Date)) {
		t.Fatalf("an existing (even empty) note must not be seeded:\n%s", blob)
	}
	if got, want := string(blob), Render(note)+"\n"; got != want {
		t.Fatalf("an existing empty note must contain only the managed block:\n%q\nwant:\n%q", got, want)
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
	// A CREATED note is the shared daily-note frontmatter (the SAME source the
	// Obsidian sink uses) followed by the managed block, and nothing else.
	wantPrefix := storage.DailyNoteFrontmatter(note.Date) + "\n" + MarkerBegin
	if !strings.HasPrefix(string(blob), wantPrefix) {
		t.Fatalf("a created note must begin with the shared frontmatter then the managed block:\n%s", blob)
	}
	if strings.Count(string(blob), storage.DailyNoteFrontmatter(note.Date)) != 1 {
		t.Errorf("want exactly one shared frontmatter block, got:\n%s", blob)
	}
	if strings.Contains(string(blob), "# Daily work log") {
		t.Errorf("creation seeding writes the frontmatter and the managed block only, got:\n%s", blob)
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
