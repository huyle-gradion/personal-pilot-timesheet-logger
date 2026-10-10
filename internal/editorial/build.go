package editorial

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"
)

// BuildOptions tunes BuildRequest.
type BuildOptions struct {
	// Now is the injectable clock (kept for symmetry; unused by BuildRequest).
	Now time.Time
	// MaxTargets/MaxFactsPerTarget override the named bounds (0 = default).
	MaxTargets        int
	MaxFactsPerTarget int
}

// BuildRequest builds a bounded, non-secret editorial request from a proposal and
// its collected evidence. Targets are the proposal's Work/Meetings/Manual blocks
// (their stable content-addressed IDs). Facts are bounded evidence-derived strings
// (commit subject, PR title, event title). It never sends unbounded input and
// records any truncation in Request.Warnings.
func BuildRequest(prop proposal.Proposal, collected []evidence.Evidence, opts BuildOptions) (Request, error) {
	date := strings.TrimSpace(prop.Date)
	maxTargets := opts.MaxTargets
	if maxTargets <= 0 {
		maxTargets = MaxTargets
	}
	maxFacts := opts.MaxFactsPerTarget
	if maxFacts <= 0 {
		maxFacts = MaxFactsPerTarget
	}

	evByID := make(map[string]evidence.Evidence, len(collected))
	for _, e := range collected {
		evByID[e.ID] = e
	}

	blocks := make([]proposal.ProposedTimeBlock, 0, len(prop.Work)+len(prop.Meetings)+len(prop.Manual))
	blocks = append(blocks, prop.Work...)
	blocks = append(blocks, prop.Meetings...)
	blocks = append(blocks, prop.Manual...)

	var warnings []string
	targets := make([]Target, 0, len(blocks))
	for _, b := range blocks {
		if strings.TrimSpace(b.ID) == "" {
			continue
		}
		t, w := buildTarget(b, date, evByID, maxFacts)
		warnings = append(warnings, w...)
		targets = append(targets, t)
	}
	if len(targets) > maxTargets {
		targets = targets[:maxTargets]
		warnings = append(warnings, "targets_truncated")
	}

	req := Request{Date: date, Targets: targets, Warnings: dedupSorted(warnings)}
	payload, err := json.Marshal(req)
	if err != nil {
		return Request{}, fmt.Errorf("editorial: marshal request: %w", err)
	}
	if int64(len(payload)) > MaxRequestBytes {
		return Request{}, &Error{Code: CodeRequestTooLarge, Excerpt: fmt.Sprintf("bytes=%d", len(payload))}
	}
	return req, nil
}

// buildTarget projects one proposal block onto an editorial target. It carries NO
// times, attendance, or classification: sending times invites duration reasoning.
func buildTarget(block proposal.ProposedTimeBlock, date string, evByID map[string]evidence.Evidence, maxFacts int) (Target, []string) {
	t := Target{
		ID:          block.ID,
		Kind:        string(block.Kind),
		Date:        strings.TrimSpace(block.Date),
		Project:     strings.TrimSpace(block.Project.Project),
		EvidenceIDs: append([]string(nil), block.EvidenceIDs...),
	}
	if t.Date == "" {
		t.Date = date
	}

	var warnings []string
	var facts []Fact
	for _, id := range block.EvidenceIDs {
		e, ok := evByID[id]
		if !ok {
			continue
		}
		fillProvenance(&t, e)
		if f, ok := factFromEvidence(e); ok {
			facts = append(facts, f)
		}
	}

	if maxFacts > 0 && len(facts) > maxFacts {
		facts = facts[:maxFacts]
		warnings = append(warnings, "facts_truncated:"+t.ID)
	}
	for i := range facts {
		facts[i].Text = sanitizeText(strings.TrimSpace(facts[i].Text))
		if len(facts[i].Text) > MaxFactBytes {
			facts[i].Text = truncateUTF8(facts[i].Text, MaxFactBytes)
			warnings = append(warnings, "fact_truncated:"+t.ID)
		}
	}
	t.Facts = facts
	return t, warnings
}

// fillProvenance takes repository/PR/branch from the first evidence that carries
// them (SourceRefs first, then TopicHint).
func fillProvenance(t *Target, e evidence.Evidence) {
	for _, ref := range e.SourceRefs {
		if t.Repository == "" {
			t.Repository = strings.TrimSpace(ref.Repository)
		}
		if t.Branch == "" {
			t.Branch = strings.TrimSpace(ref.Branch)
		}
		if t.PRNumber == 0 {
			t.PRNumber = ref.PRNumber
		}
	}
	if e.TopicHint != nil {
		if t.Repository == "" {
			t.Repository = strings.TrimSpace(e.TopicHint.Repository)
		}
		if t.Branch == "" {
			t.Branch = strings.TrimSpace(e.TopicHint.Branch)
		}
		if t.PRNumber == 0 {
			t.PRNumber = e.TopicHint.PRNumber
		}
	}
}

// factFromEvidence maps a bounded, non-secret evidence string to a fact. Only the
// three editorial fact kinds are used; other kinds contribute no fact.
func factFromEvidence(e evidence.Evidence) (Fact, bool) {
	text := strings.TrimSpace(e.Title)
	if text == "" {
		return Fact{}, false
	}
	switch e.Kind {
	case evidence.SourceLocalGit:
		return Fact{Kind: FactCommitSubject, Text: text}, true
	case evidence.SourceRemotePR:
		return Fact{Kind: FactPRTitle, Text: text}, true
	case evidence.SourceCalendar:
		return Fact{Kind: FactEventTitle, Text: text}, true
	default:
		return Fact{}, false
	}
}
