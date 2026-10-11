package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence/plugins"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

const journalDailyDir = "02 - Daily & Logs"

func journalRunner(t *testing.T, vault string) *Runner {
	t.Helper()
	store := newStore(t)
	reg := evidence.NewRegistry()
	plugins.RegisterJournal(reg)
	sources := []evidence.SourceConfig{{
		ID: "journal-work", Type: plugins.TypeJournal, Enabled: true,
		Config: map[string]string{plugins.JournalConfigVault: vault, plugins.JournalConfigDailyDir: journalDailyDir},
	}}
	return newRunner(t, Deps{Store: store, Registry: reg, Now: fixedNow}, Config{Evidence: evidence.Config{Timezone: "UTC", Sources: sources}})
}

func writeJournalNote(t *testing.T, vault, date, body string) {
	t.Helper()
	dir := filepath.Join(vault, journalDailyDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, date+".md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func journalItemByText(t *testing.T, rec workflow.Record, substr string) evidence.Evidence {
	t.Helper()
	for _, e := range rec.CollectedEvidence {
		if e.Kind == evidence.SourceJournal && strings.Contains(e.Description, substr) {
			return e
		}
	}
	t.Fatalf("no journal evidence containing %q", substr)
	return evidence.Evidence{}
}

// TestCollect_JournalDetachesMissingAndPreservesConfirmed proves a successful,
// complete journal collection marks a prior item that is no longer present as
// DETACHED (retained as history), keeps present items active, and never touches
// confirmed decisions.
func TestCollect_JournalDetachesMissingAndPreservesConfirmed(t *testing.T) {
	date := "2026-10-06"
	vault := t.TempDir()
	writeJournalNote(t, vault, date, "## Work journal\n- Alpha task\n- Beta task\n")
	r := journalRunner(t, vault)

	if _, err := r.Collect(ctx, date); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	rec, _ := r.deps.Store.GetDay(ctx, date)
	alpha := journalItemByText(t, *rec, "Alpha task")
	beta := journalItemByText(t, *rec, "Beta task")
	if alpha.JournalState != evidence.JournalActive || beta.JournalState != evidence.JournalActive {
		t.Fatalf("items must start active: %+v", alpha.JournalState)
	}

	// Seed a confirmed interval backed by the journal item.
	if _, err := r.deps.Store.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		rec.Confirmed = append(rec.Confirmed, workflow.ConfirmedInterval{
			ID: "conf-1", Date: date, StartTime: "09:00", EndTime: "10:00",
			Description: "User text", EvidenceIDs: []string{alpha.ID},
		})
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Edit the note: Alpha changes (new implicit id), Beta stays.
	writeJournalNote(t, vault, date, "## Work journal\n- Alpha task edited\n- Beta task\n")
	if _, err := r.Collect(ctx, date); err != nil {
		t.Fatalf("Collect 2: %v", err)
	}
	rec2, _ := r.deps.Store.GetDay(ctx, date)

	detached := journalItemByText(t, *rec2, "Alpha task")
	if detached.ID != alpha.ID || detached.JournalState != evidence.JournalDetached {
		t.Fatalf("the edited-away item must be retained DETACHED: %+v", detached)
	}
	active := journalItemByText(t, *rec2, "Alpha task edited")
	if active.JournalState != evidence.JournalActive {
		t.Fatalf("the new item must be active: %+v", active)
	}
	if beta2 := journalItemByText(t, *rec2, "Beta task"); beta2.ID != beta.ID || beta2.JournalState != evidence.JournalActive {
		t.Fatalf("an unchanged item must stay active: %+v", beta2)
	}
	// The confirmed decision is preserved across the refresh.
	if len(rec2.Confirmed) != 1 || rec2.Confirmed[0].ID != "conf-1" || rec2.Confirmed[0].Description != "User text" {
		t.Fatalf("confirmed decision was not preserved: %+v", rec2.Confirmed)
	}
}

// TestCollect_FailedJournalSourceDoesNotDetach proves a failed/partial journal
// collection never detaches prior history.
func TestCollect_FailedJournalSourceDoesNotDetach(t *testing.T) {
	date := "2026-10-06"
	vault := t.TempDir()
	writeJournalNote(t, vault, date, "## Work journal\n- Alpha task\n")
	r := journalRunner(t, vault)

	if _, err := r.Collect(ctx, date); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	rec, _ := r.deps.Store.GetDay(ctx, date)
	alpha := journalItemByText(t, *rec, "Alpha task")

	// Remove the note: the next collection is incomplete.
	if err := os.Remove(filepath.Join(vault, journalDailyDir, date+".md")); err != nil {
		t.Fatal(err)
	}
	res, err := r.Collect(ctx, date)
	if err != nil {
		t.Fatalf("Collect 2: %v", err)
	}
	if res.Summary.Complete {
		t.Fatalf("a missing note must be incomplete: %+v", res.Summary)
	}
	rec2, _ := r.deps.Store.GetDay(ctx, date)
	kept := journalItemByText(t, *rec2, "Alpha task")
	if kept.JournalState == evidence.JournalDetached {
		t.Fatal("a failed journal collection must never detach prior history")
	}
	if kept.ID != alpha.ID {
		t.Fatalf("prior journal history must be retained: %+v", kept)
	}
}

// TestCollect_JournalIdenticalRecollection proves identical recollection creates
// no duplicate candidates.
func TestCollect_JournalIdenticalRecollection(t *testing.T) {
	date := "2026-10-06"
	vault := t.TempDir()
	writeJournalNote(t, vault, date, "## Work journal\n- Alpha task\n- Beta task\n")
	r := journalRunner(t, vault)

	if _, err := r.Collect(ctx, date); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if _, err := r.Collect(ctx, date); err != nil {
		t.Fatalf("Collect 2: %v", err)
	}
	rec, _ := r.deps.Store.GetDay(ctx, date)
	n := 0
	for _, e := range rec.CollectedEvidence {
		if e.Kind == evidence.SourceJournal {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("identical recollection must not duplicate journal evidence, got %d", n)
	}
}
