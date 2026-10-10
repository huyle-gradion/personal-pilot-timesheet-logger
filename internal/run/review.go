package run

import (
	"context"
	"fmt"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/editorial"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/review"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// ReviewDecisions is the serializable decision set the CLI and the skill both
// apply. It is the parity seam between interactive review and a scripted review.
type ReviewDecisions struct {
	Confirm     []ConfirmDecision `json:"confirm,omitempty"`
	Exclude     []ExcludeDecision `json:"exclude,omitempty"`
	Join        []JoinDecision    `json:"join,omitempty"`
	Trim        []TrimDecision    `json:"trim,omitempty"`
	Project     []ProjectDecision `json:"project,omitempty"`
	Manual      []ManualDecision  `json:"manual,omitempty"`
	MarkEmpty   bool              `json:"mark_empty,omitempty"`
	Acknowledge []string          `json:"acknowledge,omitempty"`
}

// ConfirmDecision confirms one proposed block.
type ConfirmDecision struct {
	BlockID    string `json:"block_id"`
	Project    string `json:"project,omitempty"`
	Attendance *bool  `json:"attendance,omitempty"`
}

// ExcludeDecision excludes one evidence item from confirmed work.
type ExcludeDecision struct {
	EvidenceID string `json:"evidence_id"`
	Reason     string `json:"reason,omitempty"`
}

// JoinDecision joins a work block and a meeting into one joint interval.
type JoinDecision struct {
	WorkBlockID string `json:"work_block_id"`
	MeetingEvID string `json:"meeting_ev_id"`
}

// TrimDecision adjusts a confirmed block's span.
type TrimDecision struct {
	BlockID  string `json:"block_id"`
	NewStart string `json:"new_start"`
	NewEnd   string `json:"new_end"`
}

// ProjectDecision sets a confirmed block's project.
type ProjectDecision struct {
	BlockID string `json:"block_id"`
	Project string `json:"project"`
}

// ManualDecision adds or updates a manual interval (keyed by UUID).
type ManualDecision struct {
	UUID        string    `json:"uuid,omitempty"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Project     string    `json:"project,omitempty"`
	Description string    `json:"description,omitempty"`
}

// CurrentProposal re-derives the proposal honoring the record's review decisions
// AND folds in the persisted editorial suggestions, so AI text is what the user
// reviews.
func (r *Runner) CurrentProposal(ctx context.Context, date string) (proposal.Proposal, error) {
	rec, err := r.deps.Store.GetDay(ctx, date)
	if err != nil {
		return proposal.Proposal{}, err
	}
	return r.appliedProposal(rec, date)
}

// appliedProposal re-derives the current proposal and folds in the persisted
// editorial suggestions (TEXT ONLY: only ProposedTimeBlock.Description changes), so
// an AI description reaches ConfirmedInterval.Description through the review path.
func (r *Runner) appliedProposal(rec *workflow.Record, date string) (proposal.Proposal, error) {
	prop, err := review.CurrentProposal(*rec, r.proposalOptions(date))
	if err != nil {
		return proposal.Proposal{}, err
	}
	return editorial.Apply(prop, suggestionsFromRecord(rec)), nil
}

// ApplyReview applies a decision set inside ONE day session, then marks the day
// reviewed (or reviewed-empty). It uses only the existing internal/review actions,
// so a scripted review and an interactive review produce the same record.
func (r *Runner) ApplyReview(ctx context.Context, date string, d ReviewDecisions) error {
	session, err := r.deps.Store.BeginDaySession(ctx, date)
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()
	_, err = session.Update(ctx, func(rec *workflow.Record) error {
		return r.applyDecisions(rec, date, d)
	})
	return err
}

func (r *Runner) applyDecisions(rec *workflow.Record, date string, d ReviewDecisions) error {
	prop, err := r.appliedProposal(rec, date)
	if err != nil {
		return err
	}
	blocks := proposalBlocksByID(prop)

	// Confirm first, recording which confirmed interval each proposal block
	// produced, so Trim/Project/Join decisions may reference EITHER a proposal
	// block id or the confirmed interval id.
	blockToConfirmed := make(map[string]string, len(d.Confirm))
	for _, c := range d.Confirm {
		block, ok := blocks[c.BlockID]
		if !ok {
			return fmt.Errorf("run: confirm: unknown block %q", c.BlockID)
		}
		before := confirmedIDSet(rec)
		if err := review.ConfirmProposed(rec, block, review.ConfirmOptions{
			Project: c.Project, Attendance: c.Attendance, Now: r.now(),
		}); err != nil {
			return err
		}
		for _, ci := range rec.Confirmed {
			if !before[ci.ID] {
				blockToConfirmed[c.BlockID] = ci.ID
			}
		}
	}
	resolve := func(id string) string {
		if _, ok := findConfirmedID(rec, id); ok {
			return id
		}
		if confirmedID, ok := blockToConfirmed[id]; ok {
			return confirmedID
		}
		return id
	}

	for _, ex := range d.Exclude {
		if err := review.ExcludeEvidence(rec, ex.EvidenceID, ex.Reason); err != nil {
			return err
		}
	}
	for _, j := range d.Join {
		if err := review.JoinCollision(rec, resolve(j.WorkBlockID), j.MeetingEvID); err != nil {
			return err
		}
	}
	for _, t := range d.Trim {
		if err := review.TrimWork(rec, resolve(t.BlockID), t.NewStart, t.NewEnd); err != nil {
			return err
		}
	}
	for _, p := range d.Project {
		if err := review.ResolveProject(rec, resolve(p.BlockID), p.Project); err != nil {
			return err
		}
	}
	for _, m := range d.Manual {
		if err := review.AddManualInterval(rec, review.ManualInterval{
			UUID: m.UUID, StartAt: m.Start, EndAt: m.End,
			Project: m.Project, Description: m.Description, Now: r.now(),
		}); err != nil {
			return err
		}
	}
	if len(d.Acknowledge) > 0 {
		if err := review.AcknowledgeSources(rec, d.Acknowledge); err != nil {
			return err
		}
	}

	if d.MarkEmpty {
		return review.MarkReviewedEmpty(rec)
	}
	final, err := r.appliedProposal(rec, date)
	if err != nil {
		return err
	}
	return review.MarkReviewed(rec, review.ProposalHash(final))
}

func confirmedIDSet(rec *workflow.Record) map[string]bool {
	out := make(map[string]bool, len(rec.Confirmed))
	for _, ci := range rec.Confirmed {
		out[ci.ID] = true
	}
	return out
}

func findConfirmedID(rec *workflow.Record, id string) (string, bool) {
	for _, ci := range rec.Confirmed {
		if ci.ID == id {
			return ci.ID, true
		}
	}
	return "", false
}

func proposalBlocksByID(prop proposal.Proposal) map[string]proposal.ProposedTimeBlock {
	out := make(map[string]proposal.ProposedTimeBlock, len(prop.Work)+len(prop.Meetings)+len(prop.Manual))
	for _, b := range prop.Work {
		out[b.ID] = b
	}
	for _, b := range prop.Meetings {
		out[b.ID] = b
	}
	for _, b := range prop.Manual {
		out[b.ID] = b
	}
	return out
}
