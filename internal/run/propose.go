package run

import (
	"context"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"
)

// Propose builds the deterministic proposal for the day from the record's
// collected evidence. It persists NOTHING: the proposal is a pure view.
func (r *Runner) Propose(ctx context.Context, date string) (proposal.Proposal, error) {
	rec, err := r.deps.Store.GetDay(ctx, date)
	if err != nil {
		return proposal.Proposal{}, err
	}
	return proposal.Build(rec.CollectedEvidence, r.proposalOptions(date))
}

func (r *Runner) proposalOptions(date string) proposal.ProposalOptions {
	opts := proposal.ProposalOptions{Location: r.loc, Now: r.now()}
	if t, err := r.dayTime(date); err == nil {
		opts.Date = t
	}
	return opts
}
