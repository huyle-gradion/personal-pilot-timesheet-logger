package metrics

import (
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// Observation is one timestamped transcript event in order. IsTurn marks an
// assistant turn (a "turn"); every other event (tool result, user, system) is an
// observation point that bounds the active span of the preceding turn.
type Observation struct {
	Timestamp time.Time
	IsTurn    bool
}

// ComputeTranscriptDurations derives the transcript-based durations from the
// ordered observations:
//
//   - EndToEnd = last - first. Known(0) for a single observation.
//   - Active   = Σ over each turn of (next observation - turn). This is the time
//     the harness was demonstrably working.
//   - HumanWait = EndToEnd - Active (clamped at 0). This is the time between
//     events where no turn was in flight.
//
// All three are UNKNOWN (never zero) when there are no timestamps. The Source is
// DurationTranscript (an estimate, not a wall-clock measurement).
func ComputeTranscriptDurations(obs []Observation) (endToEnd, active, humanWait Duration) {
	unknown := Duration{Value: workflow.UnknownMeasurement(), Source: DurationUnknown}
	if len(obs) == 0 {
		return unknown, unknown, unknown
	}

	first, last := obs[0].Timestamp, obs[len(obs)-1].Timestamp
	if first.IsZero() || last.IsZero() {
		return unknown, unknown, unknown
	}
	endToEnd = Duration{Value: workflow.KnownMeasurement(last.Sub(first).Milliseconds()), Source: DurationTranscript}

	var activeMS int64
	for i := 0; i < len(obs)-1; i++ {
		if !obs[i].IsTurn {
			continue
		}
		d := obs[i+1].Timestamp.Sub(obs[i].Timestamp)
		if d < 0 {
			return endToEnd, unknown, unknown
		}
		activeMS += d.Milliseconds()
	}
	active = Duration{Value: workflow.KnownMeasurement(activeMS), Source: DurationTranscript}

	waitMS := endToEnd.Value.Value - activeMS
	if waitMS < 0 {
		waitMS = 0
	}
	humanWait = Duration{Value: workflow.KnownMeasurement(waitMS), Source: DurationTranscript}
	return endToEnd, active, humanWait
}

// HarnessStartUnknown is the honest HarnessStart: the harness start latency is
// not observable from a transcript, so it is UNKNOWN, never zero.
func HarnessStartUnknown() Duration {
	return Duration{Value: workflow.UnknownMeasurement(), Source: DurationUnknown}
}

// MonotonicSince measures the elapsed time since start with the live monotonic
// clock. It is used ONLY for the metrics command's own work (time.Since), and is
// labelled DurationMonotonic.
func MonotonicSince(start time.Time) Duration {
	return Duration{Value: workflow.KnownMeasurement(time.Since(start).Milliseconds()), Source: DurationMonotonic}
}

// StateInput carries the observable signals used to derive a RunState.
type StateInput struct {
	Steps []StepRecord
	// ExitCode is the measured run's exit code when observable; nil when not
	// observable (a transcript import has no exit code).
	ExitCode *int
	// Cancelled is an explicit cancellation signal.
	Cancelled bool
	// ClosingTurn reports whether the transcript ended with a normal assistant
	// completion (a final turn that issued no tool call).
	ClosingTurn bool
}

// DeriveState derives a RunState from observable signals, in strict priority:
//
//  1. an OBSERVABLE exit code: 0 -> completed; 3 -> blocked; anything else
//     (including 1) -> failed. A transcript import has no exit code, so this is
//     reachable only from a live run (`timesheet metrics run`).
//  2. highest completed step is submit/export -> completed.
//  3. highest step < submit and a collect/review-or-later step occurred -> partial.
//  4. explicit cancel, or no closing turn -> cancelled.
//  5. otherwise -> unknown.
//
// A review-stopped run (highest step == review) is NEVER labelled completed.
func DeriveState(in StateInput) RunState {
	if in.ExitCode != nil {
		switch *in.ExitCode {
		case 0:
			return RunCompleted
		case 3:
			return RunBlocked
		default:
			return RunFailed
		}
	}

	highest := 0
	occurred := false
	for _, s := range in.Steps {
		order, ok := stepOrder[s.Step]
		if !ok {
			continue
		}
		occurred = true
		if order > highest {
			highest = order
		}
	}

	if highest >= stepOrder[StepSubmit] {
		return RunCompleted
	}
	if occurred && highest >= stepOrder[StepCollect] {
		return RunPartial
	}
	if in.Cancelled || !in.ClosingTurn {
		return RunCancelled
	}
	return RunUnknown
}
