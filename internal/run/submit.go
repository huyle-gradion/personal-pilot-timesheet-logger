package run

import (
	"context"
	"sort"
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
	// a nil Catalog fails closed with missing_catalog BEFORE any write, and a
	// catalog older than the shared gradion.CatalogMaxAge policy fails closed with
	// stale_catalog. A supplied catalog is validated against every planned
	// operation before any write. An operation whose classification is empty or
	// absent blocks the whole run with ZERO writes: unknown_classification when the
	// catalog is FULL (proven absent), else unverified_classification — but an
	// absent choice under a filtered/partial catalog is re-validated with a bounded
	// targeted read before blocking. The CLI always supplies it (a fresh cache,
	// else live, else a partial cache) whenever a Gradion endpoint is configured.
	// It is nil only for an offline runner (deps.Remote == nil), which blocks
	// earlier with remote_unavailable.
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

	// Classification-safety gate: with a remote configured, a catalog is MANDATORY
	// and must be FRESH. Both checks fail closed BEFORE any write — a caller that
	// reaches a real remote can never skip classification validation.
	if opts.Catalog == nil {
		return SubmitResult{Blocked: true, BlockReasons: []string{BlockMissingCatalog}}, nil
	}
	if !gradion.CatalogFresh(*opts.Catalog, r.now()) {
		return SubmitResult{Blocked: true, BlockReasons: []string{BlockStaleCatalog}}, nil
	}
	// Verify every planned operation's classification BEFORE any write. An
	// unverifiable classification blocks with zero writes; it never becomes a
	// default. When the catalog cannot prove absence, a bounded targeted read may
	// authoritatively validate a specific choice.
	if plan, ok := workflow.FindPlan(rec, active.PlanID); ok {
		if reasons := r.validateClassification(ctx, *opts.Catalog, plan.Operations, rec.Confirmed); len(reasons) > 0 {
			return SubmitResult{Blocked: true, BlockReasons: reasons}, nil
		}
	}

	// This is the ONLY sanctioned path to submit.Execute: classification was
	// validated (catalog present, fresh, and every planned operation checked —
	// including targeted re-validation and the activity-type comparison) ABOVE,
	// before any write. submit.Execute is a pure dispatcher and does NOT gate.
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

// validateClassification returns the block reasons for a plan's operations. When
// the catalog is a FULL, COMPLETE fetch, absence is authoritative and the reasons
// from submit.ClassifyOperations stand. Otherwise an ABSENT choice is not proof of
// invalidity: it is re-validated with a bounded, read-only TARGETED fetch of that
// specific id — one call per distinct missing id, deduped — and a fetch that FINDS
// the id makes that option available to the FULL comparison.
//
// The resolved options are fed back through submit.ClassifyOperations so the
// ACTIVITY-TYPE comparison is re-run against the FETCHED option: a targeted fetch
// that returns the id with a DIFFERENT derived activity type still blocks with
// unverified_classification (it never clears the op merely because the id exists).
// A not-found or errored fetch fails closed (the op stays blocked).
//
// TARGETED-READ ASSUMPTION: ClassificationCatalog(ctx, <id>) matches the
// classification by that id (the server filters on the id/code search term). A
// server that does not match the id returns no option, so the choice stays blocked
// — the assumption is fail-closed, never fail-open.
func (r *Runner) validateClassification(ctx context.Context, cat gradion.Catalog, ops []workflow.PlanOperation, confirmed []workflow.ConfirmedInterval) []string {
	issues := submit.ClassifyOperations(cat, ops, confirmed)
	if len(issues) == 0 || cat.IsFullCatalog() {
		return submit.IssueReasons(issues)
	}
	resolved := cat
	resolved.Options = append([]gradion.ClassificationOption(nil), cat.Options...)
	added := false
	for _, id := range absentIDs(issues) {
		res, err := r.deps.Remote.ClassificationCatalog(ctx, id)
		if err != nil {
			continue // fail closed: the op stays blocked
		}
		opt, ok := optionByID(res, id)
		if !ok {
			continue
		}
		resolved.Options = append(resolved.Options, opt)
		added = true
	}
	if !added {
		return submit.IssueReasons(issues)
	}
	// Re-run the FULL comparison (absence AND activity type) against the resolved
	// options. A resolved id whose fetched activity type disagrees with the
	// confirmed interval blocks as unverified_classification.
	return submit.IssueReasons(submit.ClassifyOperations(resolved, ops, confirmed))
}

// absentIDs returns the distinct, non-empty classification ids of ABSENT issues,
// sorted, so each is fetched at most once.
func absentIDs(issues []submit.ClassificationIssue) []string {
	seen := map[string]bool{}
	var out []string
	for _, is := range issues {
		if !is.Absent {
			continue
		}
		id := strings.TrimSpace(is.ClassificationID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// optionByID returns the catalog option with the given id (first match).
func optionByID(cat gradion.Catalog, id string) (gradion.ClassificationOption, bool) {
	id = strings.TrimSpace(id)
	for _, o := range cat.Options {
		if strings.TrimSpace(o.ID) == id {
			return o, true
		}
	}
	return gradion.ClassificationOption{}, false
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
