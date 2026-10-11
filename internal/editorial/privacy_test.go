package editorial

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"
)

// TestBuildRequest_PrivateTextNeverForwarded proves the ACTUAL serialized
// editorial request never carries private raw text: neither a journal narrative's
// description nor a manually linked journal description (nor the manual item's own
// user-authored description) is forwarded to an external service.
func TestBuildRequest_PrivateTextNeverForwarded(t *testing.T) {
	const journalSentinel = "SENTINEL_PRIVATE_JOURNAL_NOTE_TEXT"
	const manualSentinel = "SENTINEL_USER_MANUAL_TEXT"

	journal := evidence.Evidence{
		ID: "j1", Kind: evidence.SourceJournal, Title: "note", Description: journalSentinel,
		Duration:   evidence.UnknownDuration(),
		SourceRefs: []evidence.SourceRef{{Kind: evidence.SourceJournal, SourceID: "journal-work", Section: "work journal"}},
	}
	manual := evidence.Evidence{
		ID: "m1", Kind: evidence.SourceManual, Title: "Pairing", Description: manualSentinel,
		Start:             time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC),
		End:               time.Date(2026, 10, 6, 11, 0, 0, 0, time.UTC),
		Duration:          evidence.KnownDuration(120),
		LinkedEvidenceIDs: []string{"j1"},
	}
	git := evidence.Evidence{
		ID: "g1", Kind: evidence.SourceLocalGit, Title: "fix parser",
		Duration:   evidence.UnknownDuration(),
		SourceRefs: []evidence.SourceRef{{Kind: evidence.SourceLocalGit, Repository: "acme/app"}},
		TopicHint:  &evidence.TopicHint{Repository: "acme/app", Branch: "main"},
	}

	prop := proposal.Proposal{
		Date: "2026-10-06",
		Work: []proposal.ProposedTimeBlock{{
			ID: "w1", Kind: proposal.BlockWork, Date: "2026-10-06",
			EvidenceIDs: []string{"g1"},
		}},
		Manual: []proposal.ProposedTimeBlock{{
			ID: "man1", Kind: proposal.BlockManual, Date: "2026-10-06",
			Description: manualSentinel,
			EvidenceIDs: []string{"m1", "j1"},
		}},
		Narratives: []proposal.NarrativeCandidate{{
			ID: "j1", Section: "work journal", Date: "2026-10-06", Description: journalSentinel,
		}},
	}

	req, err := BuildRequest(prop, []evidence.Evidence{journal, manual, git}, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	wire := string(data)
	if strings.Contains(wire, journalSentinel) {
		t.Errorf("private journal text leaked into the editorial request: %s", wire)
	}
	if strings.Contains(wire, manualSentinel) {
		t.Errorf("user-authored manual text leaked into the editorial request: %s", wire)
	}
	// Positive control: an ordinary commit subject IS forwarded.
	if !strings.Contains(wire, "fix parser") {
		t.Errorf("expected the git commit fact in the request: %s", wire)
	}
}
