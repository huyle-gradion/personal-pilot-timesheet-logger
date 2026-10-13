package run

import (
	"context"
	"sort"
	"strings"
	"sync"

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
	// stale_catalog. It is a FRESHNESS gate only: identity is NEVER proven from a
	// cache. A fresh LIVE, UNFILTERED, COMPLETE catalog is authoritative and reused
	// for every lookup; otherwise one bounded targeted read per unique recorded
	// code (code preferred, else name) proves each selection against a fresh live
	// result. A selection with no recorded code/name (legacy) blocks with
	// reselect_classification unless proven from the reusable full catalog. The CLI
	// always supplies it (a fresh cache, else live, else a partial cache) whenever
	// a Gradion endpoint is configured. It is nil only for an offline runner
	// (deps.Remote == nil), which blocks earlier with remote_unavailable.
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
	// Verify every planned operation's classification gets FRESH LIVE remote proof
	// BEFORE any write. A fresh cache never proves identity; an unverifiable choice
	// blocks with zero writes and never becomes a default.
	if plan, ok := workflow.FindPlan(rec, active.PlanID); ok {
		if reasons := r.proveSelections(ctx, *opts.Catalog, plan.Operations, rec.Confirmed); len(reasons) > 0 {
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

// proveSelections returns the block reasons for a plan's operations. EVERY
// planned selected classification must be PROVEN against a FRESH LIVE remote
// result before any write: a fresh cache never proves identity.
//
// When the supplied catalog is itself a fresh LIVE, UNFILTERED, COMPLETE fetch it
// is authoritative and reused for every lookup (no extra reads). Otherwise a
// bounded targeted read is issued per UNIQUE lookup query (the recorded code,
// else the name) — at most one call per distinct query — and each result must be
// live, complete, and carry the exact selected row. A lookup with NO retrieval
// metadata can only be proven from the reusable full catalog; otherwise it fails
// closed with an explicit reselect-by-code diagnostic.
//
// Ops that SHARE a classification id but disagree on retrieval metadata or derived
// activity type block as a GROUP (never "one-wins"). The payload is never
// extended: only the classification id is sent to log_time/edit_time.
func (r *Runner) proveSelections(ctx context.Context, base gradion.Catalog, ops []workflow.PlanOperation, confirmed []workflow.ConfirmedInterval) []string {
	byLocal := make(map[string]workflow.ConfirmedInterval, len(confirmed))
	for _, ci := range confirmed {
		byLocal[ci.ID] = ci
	}
	lookups := make([]submit.ClassificationLookup, 0, len(ops))
	for _, op := range ops {
		ci := byLocal[op.LocalID]
		lookups = append(lookups, submit.ClassificationLookup{
			LocalID:      op.LocalID,
			ID:           op.ClassificationID,
			Code:         ci.ClassificationCode,
			Name:         ci.ClassificationName,
			ActivityType: ci.ActivityType,
		})
	}

	reasons := submit.InconsistentIDMetadata(lookups)
	blocked := map[string]bool{}
	for _, l := range lookups {
		// A lookup is pre-blocked when its id group is inconsistent.
		if hasPrefixReason(reasons, l.LocalID) {
			blocked[l.LocalID] = true
		}
	}

	full := strings.EqualFold(strings.TrimSpace(base.Source), "live") && base.IsFullCatalog()
	type readResult struct {
		cat gradion.Catalog
		err error
	}
	reads := map[string]readResult{}
	for _, lk := range lookups {
		if blocked[lk.LocalID] {
			continue
		}
		if full {
			if reason := submit.ProveClassification(base, lk, true); reason != "" {
				reasons = append(reasons, reason+":"+lk.LocalID)
			}
			continue
		}
		q := lk.Query()
		if q == "" {
			// Retrieval metadata is required to re-prove a choice from live data.
			reasons = append(reasons, submit.BlockUnverifiedClassification+":"+lk.LocalID, submit.BlockReselectClassification+":"+lk.LocalID)
			continue
		}
		rr, ok := reads[q]
		if !ok {
			cat, err := r.deps.Remote.ClassificationCatalog(ctx, q)
			rr = readResult{cat: cat, err: err}
			reads[q] = rr
		}
		if rr.err != nil {
			reasons = append(reasons, submit.BlockUnverifiedClassification+":"+lk.LocalID)
			continue
		}
		if reason := submit.ProveClassification(rr.cat, lk, false); reason != "" {
			reasons = append(reasons, reason+":"+lk.LocalID)
		}
	}
	return dedupSort(reasons)
}

// hasPrefixReason reports whether reasons contains "<BlockUnverifiedClassification>:<localID>".
func hasPrefixReason(reasons []string, localID string) bool {
	want := submit.BlockUnverifiedClassification + ":" + localID
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}

// dedupSort returns the non-empty, deduplicated, sorted reasons.
func dedupSort(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
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
	return r.fetchListing(ctx, date)
}

// listingCache holds the listing read by Collection for a (Runner, date) pair,
// so the later observe within the SAME run reuses that object instead of reading
// the remote a second time. It is keyed by the Runner pointer, so a standalone
// `preview` with a fresh Runner — or `Resolve` — misses and live-reads. The map
// holds a strong reference to each Runner for the process's (short-lived) run, so
// an entry is never evicted and a Runner address is never reused while cached.
//
// DEBT (COLLECT-REMOTE-1 F2, low): the map is UNBOUNDED and keeps a strong
// reference to every *Runner for the process lifetime, so a long-lived process
// that builds many Runners would neither evict entries nor let a finished Runner
// be collected. It is latent, not reachable in today's CLI, which builds one
// short-lived Runner per invocation. Proposed fix: make the cache a field on
// Runner (needs internal/run/run.go, outside COLLECT-REMOTE-1's file set) or
// otherwise bound the map. Carried as documented debt in the COLLECT-REMOTE-1
// record.
var (
	listingCacheMu sync.Mutex
	listingCache   = map[*Runner]map[string]gradion.Listing{}
)

// fetchListing is the read primitive used by observe: it returns a
// Collection-cached listing on a hit, otherwise it live-reads the remote. Collect
// does NOT call it — Collection always live-reads (its own read is authoritative)
// and then populates the cache through cacheListing. fetchListing never populates
// the cache itself, so a standalone preview or a Resolve always observes the
// CURRENT remote. An error is never cached.
func (r *Runner) fetchListing(ctx context.Context, date string) (gradion.Listing, error) {
	listingCacheMu.Lock()
	if byDate, ok := listingCache[r]; ok {
		if listing, ok := byDate[date]; ok {
			listingCacheMu.Unlock()
			return listing, nil
		}
	}
	listingCacheMu.Unlock()
	return r.deps.Remote.ListEntries(ctx, date, date, 0)
}

// cacheListing stores a Collection listing for the rest of the run, so the later
// observe returns the same object instead of a second remote read.
func (r *Runner) cacheListing(date string, listing gradion.Listing) {
	listingCacheMu.Lock()
	if listingCache[r] == nil {
		listingCache[r] = map[string]gradion.Listing{}
	}
	listingCache[r][date] = listing
	listingCacheMu.Unlock()
}
