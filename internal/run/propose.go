package run

import (
	"context"
	"errors"
	"sort"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/review"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// Propose builds the deterministic proposal for the day from the record's
// collected evidence. It persists NOTHING: the proposal is a pure view.
//
// A day with no stored record has NO evidence, so it produces an EMPTY draft
// rather than an error (the redesign: "a day without evidence produces an empty
// draft, not an invented or zero-hour remote entry"). Only a missing record is
// treated this way; any other read failure is still returned.
//
// A persisted cross-midnight carry item from an EARLIER day that targets this day
// is merged in as an UNCONFIRMED proposed block. Merging is a read-only view:
// nothing is written into this day's record until the user explicitly confirms
// the block.
func (r *Runner) Propose(ctx context.Context, date string) (proposal.Proposal, error) {
	rec, err := r.deps.Store.GetDay(ctx, date)
	if err != nil {
		if errors.Is(err, workflow.ErrRecordNotFound) {
			prop, berr := proposal.Build(nil, r.proposalOptions(date))
			if berr != nil {
				return prop, berr
			}
			return r.withCarries(ctx, date, prop)
		}
		return proposal.Proposal{}, err
	}
	prop, err := proposal.Build(rec.CollectedEvidence, r.proposalOptions(date))
	if err != nil {
		return proposal.Proposal{}, err
	}
	return r.withCarries(ctx, date, prop)
}

// withCarries merges the persisted carry items that target date into the
// proposal as unconfirmed blocks. The previous day's record is read only.
func (r *Runner) withCarries(ctx context.Context, date string, prop proposal.Proposal) (proposal.Proposal, error) {
	carries, err := r.carriesForDay(ctx, date)
	if err != nil {
		return proposal.Proposal{}, err
	}
	return mergeCarryBlocks(prop, carries), nil
}

// maxCarryLookbackDays bounds the backward scan for carries. A carry can target
// a day more than one day after its originating day (an interval spanning two or
// more local midnights), so the scan reads up to this many earlier records.
const maxCarryLookbackDays = 7

// carriesForDay returns the persisted carry items that target date, read from
// EARLIER records. A carry is recorded on its ORIGINATING day, which can be more
// than one day before date (an interval spanning several local midnights), so the
// scan walks back up to maxCarryLookbackDays days and collects every matching
// carry. It does NOT stop early on a day with no carries: an intermediate day may
// exist without carries while the originating day holds one. A missing record is
// skipped. Nothing is written.
func (r *Runner) carriesForDay(ctx context.Context, date string) ([]workflow.CarryItem, error) {
	var out []workflow.CarryItem
	cur := date
	for i := 0; i < maxCarryLookbackDays; i++ {
		prev, err := r.previousDate(cur)
		if err != nil {
			return nil, err
		}
		rec, err := r.deps.Store.GetDay(ctx, prev)
		if err != nil {
			if errors.Is(err, workflow.ErrRecordNotFound) {
				cur = prev
				continue
			}
			return nil, err
		}
		for _, c := range rec.Carries {
			if c.Date == date {
				out = append(out, c)
			}
		}
		cur = prev
	}
	return out, nil
}

// previousDate returns the calendar day before date in the configured location.
func (r *Runner) previousDate(date string) (string, error) {
	t, err := r.dayTime(date)
	if err != nil {
		return "", err
	}
	return t.AddDate(0, 0, -1).Format("2006-01-02"), nil
}

// mergeCarryBlocks adds each carry item to the proposal as an UNCONFIRMED block.
// A carry of a meeting is surfaced with the meetings; otherwise it is surfaced
// with the manual blocks. Nothing is preselected or auto-confirmed.
func mergeCarryBlocks(prop proposal.Proposal, carries []workflow.CarryItem) proposal.Proposal {
	if len(carries) == 0 {
		return prop
	}
	seen := map[string]bool{}
	for _, b := range proposalBlocksOf(prop) {
		seen[b.ID] = true
	}
	for _, item := range carries {
		b := review.SurfaceCarry(item)
		if seen[b.ID] {
			continue
		}
		seen[b.ID] = true
		if b.Kind == proposal.BlockMeeting {
			prop.Meetings = append(prop.Meetings, b)
		} else {
			prop.Manual = append(prop.Manual, b)
		}
	}
	sort.Slice(prop.Meetings, func(i, j int) bool {
		if prop.Meetings[i].StartAt.Equal(prop.Meetings[j].StartAt) {
			return prop.Meetings[i].ID < prop.Meetings[j].ID
		}
		return prop.Meetings[i].StartAt.Before(prop.Meetings[j].StartAt)
	})
	sort.Slice(prop.Manual, func(i, j int) bool {
		if prop.Manual[i].StartAt.Equal(prop.Manual[j].StartAt) {
			return prop.Manual[i].ID < prop.Manual[j].ID
		}
		return prop.Manual[i].StartAt.Before(prop.Manual[j].StartAt)
	})
	return prop
}

func (r *Runner) proposalOptions(date string) proposal.ProposalOptions {
	opts := proposal.ProposalOptions{Location: r.loc, Now: r.now()}
	if t, err := r.dayTime(date); err == nil {
		opts.Date = t
	}
	return opts
}
