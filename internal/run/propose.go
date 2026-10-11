package run

import (
	"context"
	"errors"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// Propose builds the deterministic proposal for the day from the record's
// collected evidence. It persists NOTHING: the proposal is a pure view.
//
// A day with no stored record has NO evidence, so it produces an EMPTY draft
// rather than an error (the redesign: "a day without evidence produces an empty
// draft, not an invented or zero-hour remote entry"). Only a missing record is
// treated this way; any other read failure is still returned.
func (r *Runner) Propose(ctx context.Context, date string) (proposal.Proposal, error) {
	rec, err := r.deps.Store.GetDay(ctx, date)
	if err != nil {
		if errors.Is(err, workflow.ErrRecordNotFound) {
			return proposal.Build(nil, r.proposalOptions(date))
		}
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
