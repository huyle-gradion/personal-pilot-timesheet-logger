package metrics

import (
	"strconv"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// LiveRunOptions tunes NewLiveRun.
type LiveRunOptions struct {
	Date               string
	Provider           string
	Commit             string
	WorkflowVersion    string
	ConfigMode         string
	OptimizationChange string
}

// NewLiveRun records the metrics command's OWN execution of a child command. It
// measures elapsed time with the live MONOTONIC clock (time.Since) and derives
// RunState from the child's exit code (0 -> completed; 3 -> blocked; anything else
// -> failed). HarnessStart stays UNKNOWN, never zero.
//
// A live run carries NO calls and NO token usage: a child shell command is not a
// harness turn, so its token usage is UNKNOWN and it contributes no token usage to
// an aggregate (only its duration and state are meaningful). This is what makes
// RunBlocked and RunFailed reachable in production; a transcript import cannot
// observe them.
func NewLiveRun(start time.Time, exitCode int, opts LiveRunOptions) RunRecord {
	d := MonotonicSince(start)
	code := exitCode
	return RunRecord{
		RunID:              "live-" + strconv.FormatInt(start.UnixNano(), 10),
		Provider:           providerOr(opts.Provider, "timesheet"),
		Date:               opts.Date,
		StartedAt:          start.UTC(),
		EndedAt:            time.Now().UTC(),
		State:              DeriveState(StateInput{ExitCode: &code}),
		EndToEnd:           d,
		Active:             d,
		HumanWait:          Duration{Value: workflow.KnownMeasurement(0), Source: DurationMonotonic},
		HarnessStart:       HarnessStartUnknown(),
		Usage:              UnknownUsage(),
		Calls:              []CallRecord{},
		Steps:              []StepRecord{},
		Commit:             opts.Commit,
		WorkflowVersion:    opts.WorkflowVersion,
		ConfigMode:         opts.ConfigMode,
		OptimizationChange: opts.OptimizationChange,
		Provenance:         ProvenanceObserved,
	}
}

func providerOr(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}
