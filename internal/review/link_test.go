package review

import (
	"errors"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

func journalEvidence(id string) evidence.Evidence {
	return evidence.Evidence{
		ID: id, Kind: evidence.SourceJournal, Title: "note", Description: "note",
		Duration:   evidence.UnknownDuration(),
		SourceRefs: []evidence.SourceRef{{Kind: evidence.SourceJournal, SourceID: "journal-work", Section: "work journal"}},
	}
}

// TestAddManualInterval_LinksEvidence proves a manual interval can cite several
// supporting journal items: the linkage persists on the manual evidence and
// reaches the proposed manual block and a confirmed interval WITHOUT adding
// minutes, and it survives a four-hour split without losing provenance.
func TestAddManualInterval_LinksEvidence(t *testing.T) {
	r := workflow.Record{Date: "2026-10-06", CollectedEvidence: []evidence.Evidence{
		journalEvidence("j1"), journalEvidence("j2"),
	}}
	if err := AddManualInterval(&r, ManualInterval{
		UUID: "u1", StartAt: at(9, 0), EndAt: at(14, 0),
		Description: "Pairing", EvidenceIDs: []string{"j1", "j2"},
	}); err != nil {
		t.Fatalf("AddManualInterval: %v", err)
	}

	var manual evidence.Evidence
	for _, e := range r.CollectedEvidence {
		if e.Kind == evidence.SourceManual {
			manual = e
		}
	}
	if manual.ID == "" {
		t.Fatal("manual evidence not created")
	}
	if len(manual.LinkedEvidenceIDs) != 2 {
		t.Fatalf("expected 2 linked ids, got %v", manual.LinkedEvidenceIDs)
	}

	// The proposed manual block carries the manual id plus the linked ids.
	prop, err := proposal.Build(r.CollectedEvidence, testProposalOpts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// A 5h manual interval is split at assignment into <=4h fragments, each
	// carrying the full provenance (own + linked ids) and the exact total.
	if len(prop.Manual) != 2 {
		t.Fatalf("expected a 4h split into 2 manual blocks, got %d", len(prop.Manual))
	}
	total := 0
	for _, b := range prop.Manual {
		if len(b.EvidenceIDs) != 3 {
			t.Fatalf("proposed manual block must carry own + linked ids, got %v", b.EvidenceIDs)
		}
		if !b.Duration.Known {
			t.Fatalf("manual duration must be known: %+v", b.Duration)
		}
		total += b.Duration.Minutes
	}
	if total != 300 {
		t.Fatalf("linked evidence must not add minutes: total %d", total)
	}

	// Confirming the fragments preserves the linkage and the exact total.
	for _, b := range prop.Manual {
		if err := ConfirmProposed(&r, b, ConfirmOptions{Now: at(12, 0)}); err != nil {
			t.Fatalf("ConfirmProposed: %v", err)
		}
	}
	if len(r.Confirmed) != 2 {
		t.Fatalf("expected 2 confirmed fragments, got %d", len(r.Confirmed))
	}
	confirmedTotal := 0
	for _, ci := range r.Confirmed {
		if len(ci.EvidenceIDs) != 3 {
			t.Errorf("split fragment lost provenance: %v", ci.EvidenceIDs)
		}
		sm, _ := parseHHMM(ci.StartTime)
		em, _ := parseHHMM(ci.EndTime)
		confirmedTotal += em - sm
	}
	if confirmedTotal != 300 {
		t.Errorf("split must preserve the exact total, got %d minutes", confirmedTotal)
	}
}

// TestAddManualInterval_RejectsBadLinks proves an unknown linked id and a cycle
// are refused.
func TestAddManualInterval_RejectsBadLinks(t *testing.T) {
	r := workflow.Record{Date: "2026-10-06", CollectedEvidence: []evidence.Evidence{journalEvidence("j1")}}
	if err := AddManualInterval(&r, ManualInterval{
		UUID: "u1", StartAt: at(9, 0), EndAt: at(10, 0), EvidenceIDs: []string{"nope"},
	}); !errors.Is(err, ErrLinkedEvidenceNotFound) {
		t.Errorf("expected ErrLinkedEvidenceNotFound, got %v", err)
	}

	// Self-reference is a cycle.
	selfID := evidence.ManualID("2026-10-06", "u2")
	if err := AddManualInterval(&r, ManualInterval{
		UUID: "u2", StartAt: at(9, 0), EndAt: at(10, 0), EvidenceIDs: []string{selfID},
	}); !errors.Is(err, ErrLinkedEvidenceCycle) {
		t.Errorf("expected ErrLinkedEvidenceCycle for self-reference, got %v", err)
	}

	// Two manual items linking to each other form a cycle.
	r2 := workflow.Record{Date: "2026-10-06"}
	if err := AddManualInterval(&r2, ManualInterval{UUID: "a", StartAt: at(9, 0), EndAt: at(10, 0)}); err != nil {
		t.Fatal(err)
	}
	if err := AddManualInterval(&r2, ManualInterval{UUID: "b", StartAt: at(10, 0), EndAt: at(11, 0), EvidenceIDs: []string{evidence.ManualID("2026-10-06", "a")}}); err != nil {
		t.Fatal(err)
	}
	err := AddManualInterval(&r2, ManualInterval{
		UUID: "a", StartAt: at(9, 0), EndAt: at(10, 0),
		EvidenceIDs: []string{evidence.ManualID("2026-10-06", "b")},
	})
	if !errors.Is(err, ErrLinkedEvidenceCycle) {
		t.Errorf("expected a cycle refusal, got %v", err)
	}
	_ = time.Now
}
