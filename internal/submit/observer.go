package submit

import (
	"context"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
)

// RemoteObserver is the read side of the remote used during execution. It exists
// so the execution layer can be driven hermetically: *gradion.Client satisfies it
// and tests inject a fake. ListEntries is expected to re-observe the remote at
// call time; the execution layer calls it again under the held day lock rather
// than trusting a plan-time listing.
type RemoteObserver interface {
	ListEntries(ctx context.Context, dateFrom, dateTo string, limit int) (gradion.Listing, error)
}

// RemoteWriter is the write side of the remote used during execution.
// *gradion.Client satisfies it. Each method performs EXACTLY ONE dispatch: the
// execution layer never retries a write, and a non-nil error means the request
// was rejected locally (arg bounds / uninitialized) and was NOT dispatched.
type RemoteWriter interface {
	CreateEntry(ctx context.Context, req gradion.CreateRequest) (gradion.WriteResult, error)
	EditEntry(ctx context.Context, req gradion.EditRequest) (gradion.WriteResult, error)
}
