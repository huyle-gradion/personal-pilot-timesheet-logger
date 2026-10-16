package submit

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// Stable, secret-free execution block reasons.
const (
	BlockWriterBusy     = "writer_busy"
	BlockRecordNotFound = "record_not_found"
	BlockObserveFailed  = "observe_failed"
	BlockPlanMissing    = "plan_missing"
	// BlockRestartGate reports a record that still has unresolved remote
	// operations (in_flight intents, or uncertain/unidentified outcomes). The run
	// is refused so an unresolved operation is never replayed (a duplicate write
	// is a blocking hazard).
	BlockRestartGate = "restart_gate"
)

// Advisory rebuild warning codes. A non-empty RunResult.RebuildWarnings NEVER
// implies a top-level error: the run's outcomes are already durably persisted, so
// a post-run rebuild problem is reported as a warning only.
const (
	// RebuildUnresolved reports that the record still has an unresolved operation,
	// so no remaining plan is proposed (it could replay the unresolved operation).
	RebuildUnresolved = "rebuild_unresolved"
	// RebuildReloadFailed reports that the updated record could not be reloaded.
	RebuildReloadFailed = "rebuild_reload_failed"
	// RebuildReobserveFailed reports that the remote could not be re-observed.
	RebuildReobserveFailed = "rebuild_reobserve_failed"
	// RebuildFailed reports that the remaining-plan build returned an error.
	RebuildFailed = "rebuild_failed"
	// RebuildBlocked reports that the rebuilt remaining plan was blocked; its
	// reasons are appended as "rebuild_blocked:<reason>".
	RebuildBlocked = "rebuild_blocked"
)

// RunResult reports one Execute call. Partial is true when the run did not finish
// with every operation confirmed (a definitive failure, an uncertain/unidentified
// stop, or an aborted dispatch). RemainingPlan is the re-derived plan for the
// operations that still need work; it is nil when the run was blocked, when an
// unresolved operation remains, or when the rebuild was blocked.
//
// RebuildWarnings reports ADVISORY post-run problems that did not prevent the run
// from completing: the remaining plan could not be rebuilt, or the rebuild was
// blocked. The values are stable, sorted, deduped strings (see the Rebuild*
// constants). A non-empty RebuildWarnings does NOT imply a top-level error; the
// run's outcomes are already durable.
type RunResult struct {
	Date            string
	PlanID          string
	Outcomes        []workflow.OperationOutcome
	Partial         bool
	Blocked         bool
	BlockReasons    []string
	RemainingPlan   *workflow.SubmissionPlan
	RebuildWarnings []string
}

// Execute performs the approved plan for one day.
//
// It takes the exclusive day lock, re-observes the remote IMMEDIATELY under that
// lock, and re-validates the approval against the fresh observation. A non-valid
// approval is invalidated and produces ZERO remote writes. For each planned
// operation, in deterministic order, it writes a durable in_flight intent BEFORE
// dispatching exactly once (writes never retry), then records the terminal
// outcome and, when confirmed, the mapping. Confirmed continues; a definitive
// failure records and continues; uncertain/unidentified records and STOPS (never
// continues, never retries). Finally it rebuilds the plan on the updated record so
// confirmed creates become edits/no-ops and are never replayed; the rebuild is
// advisory, so any problem there is surfaced in RebuildWarnings, never as a
// top-level error.
//
// PRE-VALIDATION CONTRACT: Execute does NOT itself validate plan classifications
// against the classification catalog (it has no catalog and no catalog reader).
// The CALLER is REQUIRED to pre-validate before invoking Execute.
// run.Runner.Submit is the MANDATORY, enforced boundary: it fails closed on a nil
// or stale catalog and blocks unknown/unverified classifications (including
// targeted re-validation and the activity-type comparison) BEFORE calling
// Execute. Any new caller that reaches a real remote MUST go through that gate
// (or reproduce it); calling Execute directly with unvalidated operations is a
// defect. The executor is intentionally a pure dispatcher so a caller cannot
// silently widen the payload (only the classification id is ever sent).
func Execute(ctx context.Context, store workflow.Store, obs RemoteObserver, w RemoteWriter, date string, approval workflow.Approval, resolver ClassificationResolver, now time.Time) (RunResult, error) {
	if ctx == nil {
		return RunResult{}, errors.New("submit: nil context")
	}
	if store == nil || obs == nil || w == nil {
		return RunResult{}, errors.New("submit: nil store, observer, or writer")
	}

	session, err := store.BeginDaySession(ctx, date)
	if err != nil {
		if errors.Is(err, workflow.ErrWriterBusy) {
			return RunResult{Date: date, PlanID: approval.PlanID, Blocked: true, BlockReasons: []string{BlockWriterBusy}}, nil
		}
		return RunResult{Date: date, PlanID: approval.PlanID}, err
	}
	defer func() { _ = session.Close() }()

	// Re-fetch the remote under the held lock: the approval is validated against
	// what the remote looks like NOW, not at plan time.
	listing, err := obs.ListEntries(ctx, date, date, 0)
	if err != nil {
		return RunResult{Date: date, PlanID: approval.PlanID, Blocked: true, BlockReasons: []string{BlockObserveFailed}}, err
	}

	rec, err := session.Get(ctx)
	if err != nil {
		if errors.Is(err, workflow.ErrRecordNotFound) {
			return RunResult{Date: date, PlanID: approval.PlanID, Blocked: true, BlockReasons: []string{BlockRecordNotFound}}, nil
		}
		return RunResult{Date: date, PlanID: approval.PlanID}, err
	}

	// Restart gate: a record with unresolved remote operations is never replayed.
	if blocked, reasons := RestartGate(*rec); blocked {
		return RunResult{Date: date, PlanID: approval.PlanID, Blocked: true, BlockReasons: dedupSorted(append([]string{BlockRestartGate}, reasons...))}, nil
	}

	status, reasons := ValidateApproval(*rec, approval, listing, now)
	if status != ApprovalValid {
		blockReasons := append([]string{"approval_" + string(status)}, reasons...)
		if _, uErr := session.Update(ctx, func(r *workflow.Record) error {
			workflow.InvalidateApproval(r, approval.ApprovalID, string(status), now)
			return nil
		}); uErr != nil {
			return RunResult{Date: date, PlanID: approval.PlanID, Blocked: true, BlockReasons: dedupSorted(blockReasons)}, uErr
		}
		return RunResult{Date: date, PlanID: approval.PlanID, Blocked: true, BlockReasons: dedupSorted(blockReasons)}, nil
	}

	plan, ok := workflow.FindPlan(rec, approval.PlanID)
	if !ok {
		return RunResult{Date: date, PlanID: approval.PlanID, Blocked: true, BlockReasons: []string{BlockPlanMissing}}, nil
	}

	ops := append([]workflow.PlanOperation(nil), plan.Operations...)
	sort.SliceStable(ops, func(i, j int) bool {
		if ops[i].LocalID != ops[j].LocalID {
			return ops[i].LocalID < ops[j].LocalID
		}
		if ops[i].Kind != ops[j].Kind {
			return ops[i].Kind < ops[j].Kind
		}
		return ops[i].RemoteID < ops[j].RemoteID
	})

	result := RunResult{Date: date, PlanID: plan.PlanID}
	stop := false
	for _, op := range ops {
		action := string(op.Kind)
		planned := plannedFromOp(op)

		intent := workflow.OperationOutcome{
			OperationKey: workflow.DeriveOperationKey(workflow.OperationOutcome{
				Date: op.Date, Action: action, RemoteID: op.RemoteID, LocalID: op.LocalID,
				StartTime: op.StartTime, EndTime: op.EndTime,
			}),
			LocalID: op.LocalID, RemoteID: op.RemoteID, Action: action,
			Status:           workflow.OutcomeInFlight,
			Date:             op.Date,
			StartTime:        op.StartTime,
			EndTime:          op.EndTime,
			ClassificationID: op.ClassificationID,
			Description:      op.Description,
			IntentAt:         now,
		}
		if _, err := session.Update(ctx, func(r *workflow.Record) error {
			workflow.UpsertIntent(r, intent)
			return nil
		}); err != nil {
			result.Partial = true
			return result, fmt.Errorf("submit: persist intent for %s: %w", op.LocalID, err)
		}

		res, dispatchErr := dispatch(ctx, w, op, planned)
		if dispatchErr != nil {
			// Local precondition failure: no dispatch happened and no outcome row
			// is written. The durable in_flight intent remains and the restart
			// gate blocks replay.
			result.Partial = true
			return result, fmt.Errorf("submit: dispatch %s for %s: %w", action, op.LocalID, dispatchErr)
		}

		// Identity acknowledgement is not content observation. When the write
		// only identified the entry (a reference envelope), verify its CONTENT by
		// a bounded stable-ID read-back before recording a confirmed outcome. A
		// failed/absent read-back or a content mismatch leaves the write
		// UNRESOLVED (never retried; the restart gate blocks replay).
		if res.Status == gradion.WriteIdentified {
			res = verifyIdentifiedWrite(ctx, obs, op, planned, res)
		}

		outcome := gradion.BuildOutcome(action, op.LocalID, planned, res, now)
		result.Outcomes = append(result.Outcomes, outcome)

		if _, err := session.Update(ctx, func(r *workflow.Record) error {
			workflow.UpsertOutcome(r, outcome)
			if outcome.Status == workflow.OutcomeConfirmed && res.Entry != nil {
				mapping, mErr := gradion.BuildMapping(op.LocalID, planned, *res.Entry, now)
				if mErr != nil {
					return mErr
				}
				return workflow.UpsertMapping(r, mapping)
			}
			return nil
		}); err != nil {
			result.Partial = true
			return result, fmt.Errorf("submit: persist outcome for %s: %w", op.LocalID, err)
		}

		switch outcome.Status {
		case workflow.OutcomeConfirmed:
			// continue
		case workflow.OutcomeFailed:
			result.Partial = true
		default:
			// uncertain / unidentified: STOP. Never continue, never retry.
			result.Partial = true
			stop = true
		}
		if stop {
			break
		}
	}

	// Release the lock before rebuilding: the rebuild only reads.
	_ = session.Close()

	// The rebuild is ADVISORY: the run's outcomes are already durable, so a
	// post-run problem is surfaced as a warning, never as a top-level error, and
	// the persisted RunResult is always returned.
	updated, err := store.GetDay(ctx, date)
	if err != nil {
		result.RebuildWarnings = []string{RebuildReloadFailed}
		return result, nil
	}
	if blocked, _ := RestartGate(*updated); blocked {
		// A new unresolved operation (e.g. an uncertain write from this run)
		// remains: do not propose a plan that could replay it.
		result.RebuildWarnings = []string{RebuildUnresolved}
		return result, nil
	}
	freshListing, err := obs.ListEntries(ctx, date, date, 0)
	if err != nil {
		result.RebuildWarnings = []string{RebuildReobserveFailed}
		return result, nil
	}
	remaining, err := BuildPlan(*updated, freshListing, nil, resolver, now)
	if err != nil {
		result.RebuildWarnings = []string{RebuildFailed}
		return result, nil
	}
	if remaining.Blocked {
		warnings := make([]string, 0, len(remaining.Reasons)+1)
		warnings = append(warnings, RebuildBlocked)
		for _, r := range remaining.Reasons {
			warnings = append(warnings, RebuildBlocked+":"+r)
		}
		result.RebuildWarnings = dedupSorted(warnings)
		return result, nil
	}
	planCopy := remaining.Plan
	result.RemainingPlan = &planCopy
	return result, nil
}

// dispatch performs exactly one remote write for an operation.
func dispatch(ctx context.Context, w RemoteWriter, op workflow.PlanOperation, planned gradion.PlannedEntry) (gradion.WriteResult, error) {
	switch op.Kind {
	case workflow.PlanOpCreate:
		return w.CreateEntry(ctx, gradion.CreateRequest{
			LocalID:          op.LocalID,
			Date:             op.Date,
			StartTime:        op.StartTime,
			EndTime:          op.EndTime,
			Description:      op.Description,
			ClassificationID: op.ClassificationID,
			Task:             planned.Task,
			Billable:         planned.Billable,
		})
	case workflow.PlanOpEdit:
		return w.EditEntry(ctx, gradion.EditRequest{
			LocalID:          op.LocalID,
			EntryID:          op.RemoteID,
			Reason:           op.Reason,
			Date:             op.Date,
			StartTime:        op.StartTime,
			EndTime:          op.EndTime,
			Description:      op.Description,
			ClassificationID: op.ClassificationID,
			Task:             planned.Task,
			Billable:         planned.Billable,
		})
	default:
		return gradion.WriteResult{}, fmt.Errorf("submit: unsupported plan operation kind %q", op.Kind)
	}
}

// verifyIdentifiedWrite confirms an identity-acknowledged write by a BOUNDED
// stable-ID read-back of the entry's content. It converts a WriteIdentified
// result into either WriteConfirmed (content observed AND matching the plan) or
// WriteUncertain with a distinct code, PRESERVING the acknowledged RemoteID:
//
//   - read-back error, not found, or no id -> GRADION_WRITE_CONTENT_UNVERIFIED;
//   - content mismatch -> GRADION_WRITE_CONTENT_MISMATCH.
//
// It never retries. The read-back is a separate observation a later mutation can
// invalidate (the residual remote race); it does not eliminate that race.
func verifyIdentifiedWrite(ctx context.Context, obs RemoteObserver, op workflow.PlanOperation, planned gradion.PlannedEntry, res gradion.WriteResult) gradion.WriteResult {
	if strings.TrimSpace(res.RemoteID) == "" {
		res.Status = gradion.WriteUncertain
		res.ErrorCode = gradion.CodeWriteContentUnverified
		return res
	}
	observed, found, err := obs.EntryByID(ctx, res.RemoteID, op.Date, op.Date)
	if err != nil || !found {
		res.Status = gradion.WriteUncertain
		res.ErrorCode = gradion.CodeWriteContentUnverified
		return res
	}
	if !contentMatchesPlan(observed, planned) {
		res.Status = gradion.WriteUncertain
		res.ErrorCode = gradion.CodeWriteContentMismatch
		return res
	}
	observed.ContentObserved = true
	res.Entry = &observed
	res.Status = gradion.WriteConfirmed
	return res
}

// contentMatchesPlan compares the OBSERVED remote content against the pre-write
// plan. Fields compared (each after TrimSpace): Date, StartTime, EndTime,
// ClassificationID, Description. Task is compared ONLY when the plan specifies
// one (a create without a task, or an edit preserving a remote-only task, does
// not require a task match). Billable is NEVER required or fabricated: an omitted
// remote billable stays nil/"unknown" and is neither treated as an observed false
// nor filled from the plan.
//
// Normalization is a conservative exact string comparison after trimming: a value
// the server normalizes (e.g. a different time format or a canonicalized
// description) reads as a MISMATCH and leaves the write unresolved — never a
// false confirm.
func contentMatchesPlan(observed workflow.RemoteState, planned gradion.PlannedEntry) bool {
	eq := func(a, b string) bool { return strings.TrimSpace(a) == strings.TrimSpace(b) }
	if !eq(observed.Date, planned.Date) ||
		!eq(observed.StartTime, planned.StartTime) ||
		!eq(observed.EndTime, planned.EndTime) ||
		!eq(observed.ClassificationID, planned.ClassificationID) ||
		!eq(observed.Description, planned.Description) {
		return false
	}
	if strings.TrimSpace(planned.Task) != "" && !eq(observed.Task, planned.Task) {
		return false
	}
	return true
}

// plannedFromOp projects a plan operation onto the pre-write plan used to derive
// the stable operation key and to build mappings and outcomes. RemoteID is empty
// for a create and the entry id for an edit. Task/Billable are sourced from
// ExpectedRemote for edits (a partial edit preserves remote-only fields); a create
// has no local source for them.
func plannedFromOp(op workflow.PlanOperation) gradion.PlannedEntry {
	planned := gradion.PlannedEntry{
		RemoteID:         op.RemoteID,
		Date:             op.Date,
		StartTime:        op.StartTime,
		EndTime:          op.EndTime,
		ClassificationID: op.ClassificationID,
		Description:      op.Description,
	}
	if op.Kind == workflow.PlanOpEdit && op.ExpectedRemote != nil {
		planned.Task = op.ExpectedRemote.Task
		planned.Billable = op.ExpectedRemote.Billable
	}
	return planned
}
