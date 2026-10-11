package proposal

import (
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
)

func journalEvidence(id, section, text string, detached bool, hints ...string) evidence.Evidence {
	state := evidence.JournalActive
	if detached {
		state = evidence.JournalDetached
	}
	return evidence.Evidence{
		ID:           id,
		Kind:         evidence.SourceJournal,
		Title:        text,
		Description:  text,
		Duration:     evidence.UnknownDuration(),
		JournalState: state,
		ProjectHints: hints,
		SourceRefs: []evidence.SourceRef{{
			Kind: evidence.SourceJournal, SourceID: "journal-work", Section: section,
		}},
	}
}

// TestBuild_NarrativesAreUntimedAndSorted proves active journal evidence becomes
// UNTIMED narrative candidates (no time fields, unknown duration), sorted
// deterministically, while a detached item is excluded as visible history.
func TestBuild_NarrativesAreUntimedAndSorted(t *testing.T) {
	ev := []evidence.Evidence{
		journalEvidence("j2", "work journal", "second", false, "Personal Pilot"),
		journalEvidence("j1", "daily impact", "first", false),
		journalEvidence("j3", "work journal", "gone", true),
	}
	prop, err := Build(ev, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(prop.Narratives) != 2 {
		t.Fatalf("expected 2 narratives, got %d", len(prop.Narratives))
	}
	// Deterministic order: daily impact before work journal.
	if prop.Narratives[0].Section != "daily impact" || prop.Narratives[1].Section != "work journal" {
		t.Errorf("narratives not deterministically sorted: %+v", prop.Narratives)
	}
	for _, n := range prop.Narratives {
		if !n.Duration.IsUnknown() {
			t.Errorf("narrative duration must be unknown: %+v", n)
		}
		if n.Date != "2026-10-06" {
			t.Errorf("expected target date, got %q", n.Date)
		}
	}
	// Journal evidence never becomes a timed work/meeting/manual block.
	if len(prop.Work) != 0 || len(prop.Meetings) != 0 || len(prop.Manual) != 0 {
		t.Errorf("journal evidence must not create timed blocks: %+v", prop)
	}
	// Detached item is visible as excluded, never re-proposed.
	if len(prop.Excluded) != 1 || prop.Excluded[0].EvidenceID != "j3" || prop.Excluded[0].Reason != ExcludeDetached {
		t.Errorf("detached journal item must be visible as excluded: %+v", prop.Excluded)
	}

	// Order independence: the same input in any order yields identical narratives.
	rev := []evidence.Evidence{ev[2], ev[1], ev[0]}
	prop2, _ := Build(rev, testOpts())
	if len(prop2.Narratives) != len(prop.Narratives) ||
		prop2.Narratives[0].ID != prop.Narratives[0].ID ||
		prop2.Narratives[1].ID != prop.Narratives[1].ID {
		t.Error("narrative order must be independent of input order")
	}
}

// TestBuild_DetachedExcludedCarriesProvenance proves a detached journal item is
// surfaced with human-readable provenance (title/path/heading/line/date), not an
// opaque id.
func TestBuild_DetachedExcludedCarriesProvenance(t *testing.T) {
	e := evidence.Evidence{
		ID: "j1", Kind: evidence.SourceJournal, Title: "Old note", Description: "Old note",
		Duration:     evidence.UnknownDuration(),
		JournalState: evidence.JournalDetached,
		SourceRefs: []evidence.SourceRef{{
			Kind: evidence.SourceJournal, SourceID: "journal-work", Section: "work journal",
			NotePath: "02 - Daily & Logs/2026-10-06.md", HeadingPath: "Work journal",
			LineStart: 12, LineEnd: 14, StatedDate: "2026-10-06",
		}},
	}
	prop, err := Build([]evidence.Evidence{e}, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(prop.Excluded) != 1 {
		t.Fatalf("expected one excluded entry, got %d", len(prop.Excluded))
	}
	ex := prop.Excluded[0]
	if ex.Reason != ExcludeDetached || ex.NotePath == "" || ex.HeadingPath == "" ||
		ex.LineStart != 12 || ex.LineEnd != 14 || ex.StatedDate == "" || ex.Title == "" {
		t.Fatalf("detached excluded entry lacks provenance: %+v", ex)
	}
}

// TestBuild_ManualLinkedEvidence proves a manual interval's linked evidence IDs
// reach the proposed manual block (and thus any four-hour fragment) without
// adding minutes: the duration is the manual interval's own span.
func TestBuild_ManualLinkedEvidence(t *testing.T) {
	manual := evidence.Evidence{
		ID:                "m1",
		Kind:              evidence.SourceManual,
		Title:             "Pairing",
		Description:       "Pairing session",
		Start:             at(6, 9, 0),
		End:               at(6, 11, 0),
		Duration:          evidence.KnownDuration(120),
		LinkedEvidenceIDs: []string{"j1", "j2"},
	}
	prop, err := Build([]evidence.Evidence{manual}, testOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(prop.Manual) != 1 {
		t.Fatalf("expected one manual block, got %d", len(prop.Manual))
	}
	got := prop.Manual[0].EvidenceIDs
	want := map[string]bool{"m1": true, "j1": true, "j2": true}
	if len(got) != 3 {
		t.Fatalf("expected 3 evidence ids (own + linked), got %v", got)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("unexpected evidence id %q", id)
		}
	}
	if !prop.Manual[0].Duration.Known || prop.Manual[0].Duration.Minutes != 120 {
		t.Errorf("linked evidence must not add minutes: %+v", prop.Manual[0].Duration)
	}
}
