package submit

import (
	"context"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// RemoteObserver is the read side of the remote used during execution. It exists
// so the execution layer can be driven hermetically: *gradion.Client satisfies it
// and tests inject a fake. ListEntries is expected to re-observe the remote at
// call time; the execution layer calls it again under the held day lock rather
// than trusting a plan-time listing.
//
// ListEntries OBSERVED INVARIANT (required of every implementation): on a
// SUCCESSFUL real read it MUST return a Listing with Observed=true, and a
// zero-value/UNOBSERVED Listing (Observed=false) MUST mean the remote was never
// contacted. Consumers rely on this asymmetry: submit.BuildPlan treats an
// Observed=false listing as the supported offline mode (no remote contacted) and
// only blocks an INCOMPLETE listing that is Observed; run.Collect persists
// Observed/Complete and reports them to the CLI. An implementation that returned a
// partial (Complete=false) listing WITHOUT setting Observed would silently skip
// that block. The error returned by ListEntries MUST be secret-free: run and the
// CLI print it verbatim as `remote observation failed: <error>`.
//
// EntryByID performs a BOUNDED stable-ID read-back of one entry's CONTENT within
// [from, to]. It fails closed (found=false with a non-nil error) when absence
// cannot be established (an incomplete listing), so a write is never confirmed
// against a false absence. The read-back is a SEPARATE observation a later remote
// mutation can invalidate; it narrows, and does not eliminate, the residual
// remote race.
type RemoteObserver interface {
	ListEntries(ctx context.Context, dateFrom, dateTo string, limit int) (gradion.Listing, error)
	EntryByID(ctx context.Context, id, from, to string) (workflow.RemoteState, bool, error)
}

// RemoteWriter is the write side of the remote used during execution.
// *gradion.Client satisfies it. Each method performs EXACTLY ONE dispatch: the
// execution layer never retries a write, and a non-nil error means the request
// was rejected locally (arg bounds / uninitialized) and was NOT dispatched.
type RemoteWriter interface {
	CreateEntry(ctx context.Context, req gradion.CreateRequest) (gradion.WriteResult, error)
	EditEntry(ctx context.Context, req gradion.EditRequest) (gradion.WriteResult, error)
}
