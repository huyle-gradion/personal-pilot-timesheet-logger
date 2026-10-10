package editorial

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"
)

func containsStr(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func TestBuildRequest_TargetsAndFacts(t *testing.T) {
	prop := proposal.Proposal{
		Date: "2026-10-06",
		Work: []proposal.ProposedTimeBlock{{
			ID: "w1", Kind: proposal.BlockWork, Date: "2026-10-06",
			Start: "09:00", End: "10:00",
			Project:     proposal.ProjectResolution{Project: "Proj"},
			EvidenceIDs: []string{"e1", "e2"},
		}},
		Meetings: []proposal.ProposedTimeBlock{{
			ID: "m1", Kind: proposal.BlockMeeting, Date: "2026-10-06",
			Start: "11:00", End: "12:00", EvidenceIDs: []string{"e3"},
		}},
	}
	collected := []evidence.Evidence{
		{ID: "e1", Kind: evidence.SourceLocalGit, Title: "fix parser",
			SourceRefs: []evidence.SourceRef{{Kind: evidence.SourceLocalGit, Repository: "acme/repo", Branch: "feat/x"}}},
		{ID: "e2", Kind: evidence.SourceRemotePR, Title: "PR: tidy up",
			SourceRefs: []evidence.SourceRef{{Kind: evidence.SourceRemotePR, Repository: "acme/repo", PRNumber: 7}}},
		{ID: "e3", Kind: evidence.SourceCalendar, Title: "Standup"},
	}

	req, err := BuildRequest(prop, collected, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if len(req.Targets) != 2 {
		t.Fatalf("want 2 targets, got %d", len(req.Targets))
	}

	w := req.Targets[0]
	if w.ID != "w1" || w.Kind != "work" || w.Project != "Proj" {
		t.Errorf("unexpected work target: %+v", w)
	}
	if w.Repository != "acme/repo" || w.PRNumber != 7 || w.Branch != "feat/x" {
		t.Errorf("want repo/pr/branch resolved, got %+v", w)
	}
	if len(w.Facts) != 2 || w.Facts[0].Kind != FactCommitSubject || w.Facts[1].Kind != FactPRTitle {
		t.Errorf("unexpected work facts: %+v", w.Facts)
	}

	m := req.Targets[1]
	if len(m.Facts) != 1 || m.Facts[0].Kind != FactEventTitle {
		t.Errorf("want one event-title fact, got %+v", m.Facts)
	}

	// The request must NOT carry times/attendance/classification.
	payload, _ := json.Marshal(req)
	for _, forbidden := range []string{"start_time", "end_time", "attendance", "classification_id", "duration"} {
		if strings.Contains(string(payload), forbidden) {
			t.Errorf("request must not carry %q: %s", forbidden, payload)
		}
	}
}

func TestBuildRequest_TruncatesFactsAndTargets(t *testing.T) {
	prop := proposal.Proposal{Date: "2026-10-06"}
	var ids []string
	var collected []evidence.Evidence
	for i := 0; i < 5; i++ {
		id := fmt.Sprintf("e%d", i)
		ids = append(ids, id)
		collected = append(collected, evidence.Evidence{ID: id, Kind: evidence.SourceLocalGit, Title: fmt.Sprintf("commit %d", i)})
	}
	prop.Work = []proposal.ProposedTimeBlock{{ID: "w1", Kind: proposal.BlockWork, EvidenceIDs: ids}}

	req, err := BuildRequest(prop, collected, BuildOptions{MaxFactsPerTarget: 2})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if len(req.Targets[0].Facts) != 2 {
		t.Fatalf("want facts truncated to 2, got %d", len(req.Targets[0].Facts))
	}
	if !containsStr(req.Warnings, "facts_truncated:w1") {
		t.Errorf("want a truncation warning, got %v", req.Warnings)
	}

	prop.Work = nil
	for i := 0; i < 5; i++ {
		prop.Manual = append(prop.Manual, proposal.ProposedTimeBlock{ID: fmt.Sprintf("m%d", i), Kind: proposal.BlockManual})
	}
	req2, err := BuildRequest(prop, nil, BuildOptions{MaxTargets: 3})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if len(req2.Targets) != 3 {
		t.Fatalf("want targets truncated to 3, got %d", len(req2.Targets))
	}
	if !containsStr(req2.Warnings, "targets_truncated") {
		t.Errorf("want targets_truncated warning, got %v", req2.Warnings)
	}
}

func TestBuildRequest_TruncatesLongFact(t *testing.T) {
	prop := proposal.Proposal{Date: "2026-10-06", Work: []proposal.ProposedTimeBlock{{ID: "w1", Kind: proposal.BlockWork, EvidenceIDs: []string{"e1"}}}}
	collected := []evidence.Evidence{{ID: "e1", Kind: evidence.SourceLocalGit, Title: strings.Repeat("x", MaxFactBytes+100)}}

	req, err := BuildRequest(prop, collected, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildRequest: %v", err)
	}
	if len(req.Targets[0].Facts[0].Text) > MaxFactBytes {
		t.Errorf("a fact must be bounded, got %d bytes", len(req.Targets[0].Facts[0].Text))
	}
	if !containsStr(req.Warnings, "fact_truncated:w1") {
		t.Errorf("want fact_truncated warning, got %v", req.Warnings)
	}
}
