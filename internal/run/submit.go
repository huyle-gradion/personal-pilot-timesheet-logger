package run

import (
	"context"
	"strings"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/export"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/review"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/submit"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// PlanResult is the submission preview (ZERO remote writes). Suggestions are
// NON-APPLIED classification hints for the user to accept through an explicit
// selection; they never enter the plan.
type PlanResult struct {
	Blocked      bool
	BlockReasons []string
	Plan         workflow.SubmissionPlan
	Reconcile    submit.ReconcileResult
	Suggestions  []submit.ClassificationSuggestion
}

// Preview builds the submission plan for the day and persists it. It performs NO
// remote writes (a read-only re-observe is allowed).
func (r *Runner) Preview(ctx context.Context, date string, resolver submit.ClassificationResolver) (PlanResult, error) {
	rec, err := r.deps.Store.GetDay(ctx, date)
	if err != nil {
		return PlanResult{}, err
	}
	listing, err := r.observe(ctx, date)
	if err != nil {
		return PlanResult{}, err
	}
	res, err := submit.BuildPlan(*rec, listing, nil, resolver, r.now())
	if err != nil {
		return PlanResult{}, err
	}
	out := PlanResult{Blocked: res.Blocked, BlockReasons: res.Reasons, Plan: res.Plan, Reconcile: res.Reconcile, Suggestions: res.Suggestions}
	if res.Blocked {
		return out, nil
	}
	if _, err := r.deps.Store.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		workflow.UpsertPlan(rec, res.Plan)
		return nil
	}); err != nil {
		return out, err
	}
	return out, nil
}

// Approve binds an approval to the plan's content hash.
func (r *Runner) Approve(ctx context.Context, date string, plan workflow.SubmissionPlan) (workflow.Approval, error) {
	var approval workflow.Approval
	_, err := r.deps.Store.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		a, aErr := submit.Approve(rec, plan, r.now())
		approval = a
		return aErr
	})
	return approval, err
}

// SubmitOptions tunes Submit.
type SubmitOptions struct {
	// ApprovalID, when set, must match the record's ACTIVE approval id.
	ApprovalID string
	Resolver   submit.ClassificationResolver
	// Catalog is MANDATORY whenever the runner has a Remote (deps.Remote != nil):
	// it is validated against every planned operation before any write. An
	// operation whose classification is missing/empty/inconsistent blocks the whole
	// run with ZERO writes (unverified_classification). The CLI always supplies it
	// (live, else a fresh cache) whenever a Gradion endpoint is configured; a
	// caller that reaches a real remote MUST NOT leave it nil. It is nil only for an
	// offline runner (deps.Remote == nil), which blocks earlier with
	// remote_unavailable.
	Catalog *gradion.Catalog
}

// SubmitResult reports one submission attempt plus its export.
type SubmitResult struct {
	Blocked      bool
	BlockReasons []string
	Execution    submit.RunResult
	Export       export.Result
}

// Submit executes the day's ACTIVE approval and then exports (read-only). It
// discharges S6-F1 by resolving the active approval from the RECORD rather than
// trusting a caller-supplied value. It refuses with ZERO writes when there is no
// active approval, the requested id does not match, or no remote is configured.
func (r *Runner) Submit(ctx context.Context, date string, opts SubmitOptions) (SubmitResult, error) {
	rec, err := r.deps.Store.GetDay(ctx, date)
	if err != nil {
		return SubmitResult{}, err
	}
	active, ok := workflow.ActiveApproval(rec, date)
	if !ok {
		return SubmitResult{Blocked: true, BlockReasons: []string{BlockNoActiveApproval}}, nil
	}
	if id := strings.TrimSpace(opts.ApprovalID); id != "" && id != active.ApprovalID {
		return SubmitResult{Blocked: true, BlockReasons: []string{BlockApprovalMismatch}}, nil
	}
	if r.deps.Remote == nil {
		return SubmitResult{Blocked: true, BlockReasons: []string{BlockRemoteUnavailable}}, nil
	}

	// Approval-safety gate: when a catalog is available (live or cached), verify
	// every planned operation's classification BEFORE any write. An unverifiable
	// classification blocks with zero writes; it never becomes a default.
	//
	// Contract: Catalog is MANDATORY whenever deps.Remote != nil (the CLI always
	// supplies it). It is left nil only for an offline runner, which blocks earlier
	// with remote_unavailable. This stays opt-in at the API level to avoid breaking
	// programmatic callers; the CLI path is unconditional when a remote is
	// configured.
	if opts.Catalog != nil {
		if plan, ok := workflow.FindPlan(rec, active.PlanID); ok {
			if reasons := submit.ValidateCatalog(*opts.Catalog, plan.Operations, rec.Confirmed); len(reasons) > 0 {
				return SubmitResult{Blocked: true, BlockReasons: reasons}, nil
			}
		}
	}

	run, execErr := submit.Execute(ctx, r.deps.Store, r.deps.Remote, r.deps.Remote, date, active, opts.Resolver, r.now())
	out := SubmitResult{Blocked: run.Blocked, BlockReasons: run.BlockReasons, Execution: run}
	if execErr != nil {
		return out, execErr
	}
	// Export is read-only w.r.t. the remote: a failure here never replays a write.
	exp, expErr := r.Export(ctx, date)
	out.Export = exp
	return out, expErr
}

// Export writes the managed daily-note section. A nil Exporter disables export.
func (r *Runner) Export(ctx context.Context, date string) (export.Result, error) {
	if r.deps.Exporter == nil {
		return export.Result{}, nil
	}
	rec, err := r.deps.Store.GetDay(ctx, date)
	if err != nil {
		return export.Result{}, err
	}
	return r.deps.Exporter.Export(ctx, export.BuildNote(*rec))
}

// ResolveResult reports how an explicit decision set was applied.
type ResolveResult struct {
	// Decisions is the number of decisions supplied.
	Decisions int
	// Applied is the number of decisions that matched an unresolved operation.
	Applied int
	// Unmatched lists the operation keys of decisions that matched NO unresolved
	// operation. A silently dropped decision is never acceptable: the caller must
	// see it.
	Unmatched []string
}

// Resolve reconciles unresolved operations from explicit decisions and reports how
// many decisions were actually applied.
func (r *Runner) Resolve(ctx context.Context, date string, decisions []submit.Resolution) (ResolveResult, error) {
	rec, err := r.deps.Store.GetDay(ctx, date)
	if err != nil {
		return ResolveResult{}, err
	}
	unresolved := unresolvedKeySet(rec)
	result := ResolveResult{Decisions: len(decisions)}
	for _, d := range decisions {
		if unresolved[d.OperationKey] {
			result.Applied++
			continue
		}
		result.Unmatched = append(result.Unmatched, d.OperationKey)
	}

	listing, err := r.observe(ctx, date)
	if err != nil {
		return result, err
	}
	_, err = r.deps.Store.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		return submit.ResolveUnresolved(rec, listing, decisions, r.now())
	})
	return result, err
}

// Acknowledge records explicit acknowledgment of incomplete sources without
// applying any other review decision (and without marking the day reviewed).
func (r *Runner) Acknowledge(ctx context.Context, date string, sourceIDs []string) error {
	if len(sourceIDs) == 0 {
		return nil
	}
	_, err := r.deps.Store.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		return review.AcknowledgeSources(rec, sourceIDs)
	})
	return err
}

// unresolvedKeySet returns the operation keys of a record's unresolved outcomes.
func unresolvedKeySet(rec *workflow.Record) map[string]bool {
	out := map[string]bool{}
	for _, o := range rec.Outcomes {
		if o.Resolved {
			continue
		}
		switch o.Status {
		case workflow.OutcomeInFlight, workflow.OutcomeUncertain, workflow.OutcomeUnidentified:
			out[o.OperationKey] = true
		}
	}
	return out
}

// observe re-fetches the remote listing, or returns an empty complete listing when
// the run is offline.
func (r *Runner) observe(ctx context.Context, date string) (gradion.Listing, error) {
	if r.deps.Remote == nil {
		return gradion.Listing{Complete: true, ObservedAt: r.now()}, nil
	}
	return r.deps.Remote.ListEntries(ctx, date, date, 0)
}
