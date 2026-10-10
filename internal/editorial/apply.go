package editorial

import "github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"

// Apply replaces ONLY the Description of each matching ProposedTimeBlock with the
// suggestion's Description. It touches nothing else: times, project, attendance,
// evidence links, kind, and every other proposal field are byte-identical. The
// returned Proposal shares no mutated backing array with the input.
func Apply(prop proposal.Proposal, suggestions []Suggestion) proposal.Proposal {
	if len(suggestions) == 0 {
		return prop
	}
	byID := make(map[string]string, len(suggestions))
	for _, s := range suggestions {
		if s.TargetID == "" {
			continue
		}
		byID[s.TargetID] = s.Text.Description
	}
	out := prop
	out.Work = applyDescriptions(prop.Work, byID)
	out.Meetings = applyDescriptions(prop.Meetings, byID)
	out.Manual = applyDescriptions(prop.Manual, byID)
	return out
}

func applyDescriptions(blocks []proposal.ProposedTimeBlock, byID map[string]string) []proposal.ProposedTimeBlock {
	if len(blocks) == 0 {
		return blocks
	}
	out := make([]proposal.ProposedTimeBlock, len(blocks))
	copy(out, blocks)
	for i := range out {
		if desc, ok := byID[out[i].ID]; ok {
			out[i].Description = desc
		}
	}
	return out
}
