package run

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/editorial"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/review"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/selection"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// ReviewDecisions is the serializable decision set the CLI and the skill both
// apply. It is the parity seam between interactive review and a scripted review.
//
// Description and Classification are ADDITIVE (omitempty): a decision set that
// omits them behaves exactly as before. A Description or Classification decision
// may reference EITHER a proposal block id or a confirmed interval id, like
// Trim/Project/Join.
type ReviewDecisions struct {
	Confirm        []ConfirmDecision        `json:"confirm,omitempty"`
	Exclude        []ExcludeDecision        `json:"exclude,omitempty"`
	Join           []JoinDecision           `json:"join,omitempty"`
	Trim           []TrimDecision           `json:"trim,omitempty"`
	Project        []ProjectDecision        `json:"project,omitempty"`
	Description    []DescriptionDecision    `json:"description,omitempty"`
	Classification []ClassificationDecision `json:"classification,omitempty"`
	Manual         []ManualDecision         `json:"manual,omitempty"`
	MarkEmpty      bool                     `json:"mark_empty,omitempty"`
	Acknowledge    []string                 `json:"acknowledge,omitempty"`
}

// AuthoritativeClassification resolves a classification id against a catalog
// using the shared selection contract (selection.ResolveToken) and returns the
// AUTHORITATIVE id together with the activity type the catalog DERIVES for it. A
// declared activity type that disagrees with the catalog's derived type is
// refused, and an id that does not resolve is refused. It never invents an id or
// an activity type.
//
// This is a CALLER-side validator: the guided UI calls it before staging a
// choice, so an inconsistent pair is refused there. ApplyReview itself consults
// NO catalog: a scripted decisions file may carry its own ClassificationID /
// ActivityType, and Runner.Submit is the mandatory gate that re-validates the
// classification id (and the activity type) against a FRESH catalog before any
// write. ActivityType is display/validation only and is never sent.
func AuthoritativeClassification(cat gradion.Catalog, classificationID, declaredActivityType string) (string, string, error) {
	opt, err := AuthoritativeClassificationOption(cat, classificationID, declaredActivityType)
	if err != nil {
		return "", "", err
	}
	return opt.ID, opt.ActivityType, nil
}

// AuthoritativeClassificationOption is AuthoritativeClassification returning the
// WHOLE resolved catalog option, so a caller can also capture the retrieval
// metadata (code/name) from the SAME authoritative match.
func AuthoritativeClassificationOption(cat gradion.Catalog, classificationID, declaredActivityType string) (gradion.ClassificationOption, error) {
	opt, err := selection.ResolveToken(cat, classificationID)
	if err != nil {
		return gradion.ClassificationOption{}, fmt.Errorf("run: classification %q: %w", classificationID, err)
	}
	declared := strings.TrimSpace(declaredActivityType)
	if declared != "" && opt.ActivityType != "" && !strings.EqualFold(declared, opt.ActivityType) {
		return gradion.ClassificationOption{}, fmt.Errorf("run: classification %q declares activity type %q but the catalog derives %q",
			opt.ID, declared, opt.ActivityType)
	}
	return opt, nil
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

// DescriptionDecision sets a confirmed block's user-authored description. It is
// ADDITIVE: it is applied after Confirm so it may reference a proposal block id
// or a confirmed interval id.
type DescriptionDecision struct {
	BlockID string `json:"block_id"`
	Text    string `json:"text"`
}

// ClassificationDecision sets a confirmed block's EXPLICIT Gradion classification
// and the activity type. It is ADDITIVE. The guided caller resolves the choice
// through the catalog and stores the DERIVED activity type; a scripted decisions
// file may supply its own ClassificationID/ActivityType, which ApplyReview does
// NOT validate against a catalog. Runner.Submit re-validates the classification
// id (and the activity type) against a FRESH catalog before any write, and only
// the id is ever sent. Applying a classification decision invalidates a prior
// approval for the date, so a stale approval cannot survive a change.
type ClassificationDecision struct {
	BlockID          string `json:"block_id"`
	ClassificationID string `json:"classification_id"`
	ActivityType     string `json:"activity_type,omitempty"`
	// ClassificationCode/ClassificationName are ADDITIVE RETRIEVAL metadata
	// captured from the explicit catalog option (code preferred). They are a
	// retrieval hint ONLY: they are never proof and never a payload field. A
	// scripted decision may supply them; Runner.Submit still requires a fresh live
	// remote proof before any write.
	ClassificationCode string `json:"classification_code,omitempty"`
	ClassificationName string `json:"classification_name,omitempty"`
}

// ManualDecision adds or updates a manual interval (keyed by UUID). When Confirm
// is set, the interval's materialized proposed block is ALSO confirmed in the
// same ApplyReview, so an explicitly user-timed activity needs no second pass.
// ClassificationID/ActivityType are applied to the confirmed manual block(s). The
// guided caller derives the activity type from a catalog option; ApplyReview does
// not validate the pair, and Runner.Submit re-validates the id against a fresh
// catalog before any write.
type ManualDecision struct {
	UUID        string    `json:"uuid,omitempty"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Project     string    `json:"project,omitempty"`
	Description string    `json:"description,omitempty"`
	// EvidenceIDs links the manual activity to supporting source records. They
	// add no minutes; the manual interval's own span is authoritative.
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	// Confirm confirms the materialized manual block(s) in this same ApplyReview.
	Confirm          bool   `json:"confirm,omitempty"`
	ClassificationID string `json:"classification_id,omitempty"`
	ActivityType     string `json:"activity_type,omitempty"`
	// ClassificationCode/ClassificationName are ADDITIVE RETRIEVAL metadata
	// captured from the explicit catalog option (code preferred). Retrieval hint
	// only: never proof, never a payload field.
	ClassificationCode string `json:"classification_code,omitempty"`
	ClassificationName string `json:"classification_name,omitempty"`
}

// CurrentProposal re-derives the proposal honoring the record's review decisions
// AND folds in the persisted editorial suggestions, so AI text is what the user
// reviews. Persisted cross-midnight carries from the previous day are merged as
// unconfirmed blocks (read-only).
func (r *Runner) CurrentProposal(ctx context.Context, date string) (proposal.Proposal, error) {
	rec, err := r.deps.Store.GetDay(ctx, date)
	if err != nil {
		return proposal.Proposal{}, err
	}
	prop, err := r.appliedProposal(rec, date)
	if err != nil {
		return proposal.Proposal{}, err
	}
	return r.withCarries(ctx, date, prop)
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
	// Read the previous day's carries BEFORE opening this day's session, so the
	// carry blocks are available for a normal Confirm decision.
	carries, err := r.carriesForDay(ctx, date)
	if err != nil {
		return err
	}
	session, err := r.deps.Store.BeginDaySession(ctx, date)
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()
	_, err = session.Update(ctx, func(rec *workflow.Record) error {
		return r.applyDecisions(rec, date, d, carries)
	})
	return err
}

func (r *Runner) applyDecisions(rec *workflow.Record, date string, d ReviewDecisions, carries []workflow.CarryItem) error {
	prop, err := r.appliedProposal(rec, date)
	if err != nil {
		return err
	}
	// A persisted carry from the previous day is surfaced as a NORMAL unconfirmed
	// block: it is confirmed only through an explicit Confirm decision, exactly
	// like any other proposed block. There is no special auto path.
	prop = mergeCarryBlocks(prop, carries)
	blocks := proposalBlocksByID(prop)

	// Confirm first, recording EVERY confirmed interval each proposal block
	// produced (in creation order). Trim/Project/Join then reference EITHER a
	// proposal block id or the confirmed interval id; Description/Classification
	// reach EVERY fragment of a split block.
	blockToConfirmed := make(map[string][]string, len(d.Confirm))
	for _, c := range d.Confirm {
		block, ok := blocks[c.BlockID]
		if !ok {
			return fmt.Errorf("run: confirm: unknown block %q", c.BlockID)
		}
		ids, err := confirmProposedBlock(rec, block, review.ConfirmOptions{
			Project: c.Project, Attendance: c.Attendance, Now: r.now(), Location: r.loc,
		})
		if err != nil {
			return err
		}
		blockToConfirmed[c.BlockID] = ids
	}
	// resolveAll returns EVERY confirmed interval a decision targets (all
	// fragments of a split block). resolve returns a SINGLE id and keeps the
	// historical "last fragment" behavior for the single-target actions.
	resolveAll := func(id string) []string {
		if _, ok := findConfirmedID(rec, id); ok {
			return []string{id}
		}
		if ids, ok := blockToConfirmed[id]; ok && len(ids) > 0 {
			return ids
		}
		return []string{id}
	}
	resolve := func(id string) string {
		ids := resolveAll(id)
		return ids[len(ids)-1]
	}

	for _, ex := range d.Exclude {
		if err := review.ExcludeEvidence(rec, ex.EvidenceID, ex.Reason); err != nil {
			return err
		}
	}
	// Trim/Project/Description/Classification are applied BEFORE Join, so a work
	// interval that is later joined carries every edit (and its classification)
	// into the joint interval, which JoinCollision copies from it.
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
	for _, ds := range d.Description {
		for _, id := range resolveAll(ds.BlockID) {
			if err := review.SetDescription(rec, id, ds.Text); err != nil {
				return err
			}
		}
	}
	for _, cs := range d.Classification {
		for _, id := range resolveAll(cs.BlockID) {
			if err := review.SetClassificationMeta(rec, id, cs.ClassificationID, cs.ActivityType, cs.ClassificationCode, cs.ClassificationName); err != nil {
				return err
			}
		}
	}
	for _, j := range d.Join {
		if err := review.JoinCollision(rec, resolve(j.WorkBlockID), j.MeetingEvID); err != nil {
			return err
		}
	}
	for _, m := range d.Manual {
		if err := review.AddManualInterval(rec, review.ManualInterval{
			UUID: m.UUID, StartAt: m.Start, EndAt: m.End,
			Project: m.Project, Description: m.Description,
			EvidenceIDs: m.EvidenceIDs, Now: r.now(),
		}); err != nil {
			return err
		}
	}
	if err := r.confirmManualDecisions(rec, date, d.Manual); err != nil {
		return err
	}
	if len(d.Acknowledge) > 0 {
		if err := review.AcknowledgeSources(rec, d.Acknowledge); err != nil {
			return err
		}
	}

	// Record durable carry items for any confirmed cross-midnight interval. A
	// carry is recorded ONLY for a group that was actually confirmed, so an
	// unconfirmed (or never-reviewed) day leaves no carry behind.
	if err := review.RecordCarries(rec, proposalCarryItems(prop)); err != nil {
		return err
	}

	if d.MarkEmpty {
		// A nonempty day is NEVER marked empty: refuse when the record already
		// carries confirmed work or the decision set stages any confirm/manual
		// work. Returning here (before MarkReviewed) fails the whole apply closed,
		// so no partial confirm is persisted.
		if len(d.Confirm) > 0 || len(d.Manual) > 0 || len(rec.Confirmed) > 0 {
			return fmt.Errorf("run: mark_empty: the day has confirmed or staged work and cannot be marked empty")
		}
		return review.MarkReviewedEmpty(rec)
	}

	// Any decision that changes the user-reviewed schedule invalidates a prior
	// approval for the date (mirroring `classify select`), so a stale approval
	// cannot survive an edit. This is EAGER: submit also blocks via the plan's
	// LocalFingerprint, but the approval is marked invalidated here as well.
	// Acknowledge alone does not invalidate (it does not change the schedule), and
	// collection never calls ApplyReview, so an unchanged approved day stays valid.
	if scheduleAffecting(d) {
		invalidateApprovalsForDate(rec, date, r.now())
	}

	final, err := r.appliedProposal(rec, date)
	if err != nil {
		return err
	}
	final = mergeCarryBlocks(final, carries)
	return review.MarkReviewed(rec, review.ProposalHash(final))
}

// proposalCarryItems converts a proposal's carry fragments into durable carry
// items.
func proposalCarryItems(prop proposal.Proposal) []workflow.CarryItem {
	if len(prop.Carries) == 0 {
		return nil
	}
	out := make([]workflow.CarryItem, 0, len(prop.Carries))
	for _, c := range prop.Carries {
		out = append(out, workflow.CarryItem{
			SourceDate:   c.SourceDate,
			Date:         c.Date,
			Start:        c.Start,
			End:          c.End,
			StartAt:      c.StartAt,
			EndAt:        c.EndAt,
			Minutes:      c.Minutes,
			SplitGroupID: c.SplitGroupID,
			PartIndex:    c.PartIndex,
			Kind:         string(c.Kind),
			Project:      c.Project,
			Description:  c.Description,
			EvidenceIDs:  append([]string{}, c.EvidenceIDs...),
		})
	}
	return out
}

// scheduleAffecting reports whether a decision set contains any action that can
// change the user-reviewed schedule (and therefore the plan content).
func scheduleAffecting(d ReviewDecisions) bool {
	return len(d.Confirm) > 0 || len(d.Exclude) > 0 || len(d.Join) > 0 ||
		len(d.Trim) > 0 || len(d.Project) > 0 || len(d.Description) > 0 ||
		len(d.Classification) > 0 || len(d.Manual) > 0
}

// confirmProposedBlock confirms one proposed block and returns the confirmed
// interval ids it created (in creation order), so a later decision can reach
// every fragment.
func confirmProposedBlock(rec *workflow.Record, block proposal.ProposedTimeBlock, opts review.ConfirmOptions) ([]string, error) {
	before := confirmedIDSet(rec)
	if err := review.ConfirmProposed(rec, block, opts); err != nil {
		return nil, err
	}
	var ids []string
	for _, ci := range rec.Confirmed {
		if !before[ci.ID] {
			ids = append(ids, ci.ID)
		}
	}
	return ids, nil
}

// confirmManualDecisions confirms the materialized proposed manual block(s) for
// every ManualDecision with Confirm set, in THIS pass, so an explicitly
// user-timed activity needs no second review pass. It requires an explicit UUID
// so the materialized evidence id (and therefore the block) is identifiable. A
// classification carried on the decision is applied to every confirmed fragment.
func (r *Runner) confirmManualDecisions(rec *workflow.Record, date string, manuals []ManualDecision) error {
	any := false
	for _, m := range manuals {
		if m.Confirm {
			any = true
			break
		}
	}
	if !any {
		return nil
	}
	prop, err := r.appliedProposal(rec, date)
	if err != nil {
		return err
	}
	for _, m := range manuals {
		if !m.Confirm {
			continue
		}
		uuid := strings.TrimSpace(m.UUID)
		if uuid == "" {
			return fmt.Errorf("run: manual confirm: an explicit uuid is required")
		}
		evID := evidence.ManualID(manualEvidenceDate(rec, m), uuid)
		for _, b := range proposalBlocksOf(prop) {
			if b.Kind != proposal.BlockManual || !containsString(b.EvidenceIDs, evID) {
				continue
			}
			ids, err := confirmProposedBlock(rec, b, review.ConfirmOptions{
				Project: m.Project, Now: r.now(), Location: r.loc,
			})
			if err != nil {
				return err
			}
			if strings.TrimSpace(m.ClassificationID) != "" {
				for _, id := range ids {
					if err := review.SetClassificationMeta(rec, id, m.ClassificationID, m.ActivityType, m.ClassificationCode, m.ClassificationName); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// manualEvidenceDate mirrors review.AddManualInterval's date choice so the
// materialized manual evidence id is reproducible.
func manualEvidenceDate(rec *workflow.Record, m ManualDecision) string {
	if d := strings.TrimSpace(rec.Date); d != "" {
		return d
	}
	return m.Start.Format("2006-01-02")
}

// proposalBlocksOf returns every proposed block (work, meetings, manual).
func proposalBlocksOf(prop proposal.Proposal) []proposal.ProposedTimeBlock {
	out := make([]proposal.ProposedTimeBlock, 0, len(prop.Work)+len(prop.Meetings)+len(prop.Manual))
	out = append(out, prop.Work...)
	out = append(out, prop.Meetings...)
	out = append(out, prop.Manual...)
	return out
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// invalidateApprovalsForDate marks every not-yet-invalidated approval for the
// date invalidated, so a change to the reviewed content cannot leave a stale
// approval active.
func invalidateApprovalsForDate(rec *workflow.Record, date string, now time.Time) {
	for i := range rec.Approvals {
		if rec.Approvals[i].Date != date || rec.Approvals[i].InvalidatedAt != nil {
			continue
		}
		workflow.InvalidateApproval(rec, rec.Approvals[i].ApprovalID, "review_selection", now)
	}
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
