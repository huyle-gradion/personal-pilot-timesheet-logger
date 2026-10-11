package metrics

import (
	"sort"
	"strings"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// Attribution heuristic (LABELLED): a call is attributed to a workflow step by
// the `timesheet` subcommand token carried in the assistant turn's tool call.
// This is a HEURISTIC over the transcript shape, not a contract emitted by the
// workflow core. It is documented here and surfaced in the report/docs. Only the
// subcommand token is persisted (as StepID); the command text is never stored.
//
// Table (on the subcommand token):
//
//	preflight                      -> StepPreflight
//	review (no --decisions)        -> StepCollect
//	review --decisions             -> StepReview
//	preview                        -> StepPreview
//	approve                        -> StepApprove
//	submit                         -> StepSubmit
//	export                         -> StepExport
//	anything else / none           -> StepUnknown + CategoryUnassigned
var subcommandSteps = map[string]StepID{
	"preflight": StepPreflight,
	"preview":   StepPreview,
	"approve":   StepApprove,
	"submit":    StepSubmit,
	"export":    StepExport,
}

// executableBases are the basenames recognized as the timesheet binary. A token
// whose path basename is one of these introduces the subcommand.
var executableBases = map[string]bool{
	"timesheet": true,
	"ts":        true,
}

// AttributeCommand maps a shell command string to a workflow step and accounting
// category. It returns StepUnknown + CategoryUnassigned when no recognized
// `timesheet` subcommand is present: an unrecognized command is never guessed
// into a step. It is PURE and reads only the command tokens; it never retains
// the command text.
func AttributeCommand(command string) (StepID, Category) {
	sub, args := subcommandAndArgs(command)
	if sub == "" {
		return StepUnknown, CategoryUnassigned
	}
	if sub == "review" {
		if hasFlag(args, "--decisions") {
			return StepReview, CategoryOrchestration
		}
		return StepCollect, CategoryOrchestration
	}
	if step, ok := subcommandSteps[sub]; ok {
		return step, CategoryOrchestration
	}
	return StepUnknown, CategoryUnassigned
}

// subcommandAndArgs returns the subcommand token and its trailing arguments ONLY
// when the command is a direct invocation of the timesheet binary:
//
//   - the FIRST token's basename is `timesheet` or `ts` (e.g. `timesheet`,
//     `/tmp/rt/off/ts`, `./bin/timesheet`); or
//   - the `go run ./cmd/timesheet` form: `go` `run` <path whose basename is
//     `timesheet`|`ts`>.
//
// Any other command (including `grep -r timesheet submit`, `echo timesheet`, or a
// compound `cd x && timesheet review`) yields no subcommand: it is never guessed
// into a step.
func subcommandAndArgs(command string) (string, []string) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "", nil
	}
	idx := -1
	switch {
	case executableBases[basename(fields[0])]:
		idx = 0
	case len(fields) >= 3 && fields[0] == "go" && fields[1] == "run" && executableBases[basename(fields[2])]:
		idx = 2
	}
	if idx < 0 || idx+1 >= len(fields) {
		return "", nil
	}
	sub := fields[idx+1]
	if strings.HasPrefix(sub, "-") {
		return "", nil
	}
	return sub, fields[idx+2:]
}

func basename(token string) string {
	if idx := strings.LastIndexByte(token, '/'); idx >= 0 {
		return token[idx+1:]
	}
	return token
}

// hasFlag reports whether any argument equals the flag, or uses the `--flag=value`
// form.
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag || strings.HasPrefix(a, flag+"=") {
			return true
		}
	}
	return false
}

// AttributeCalls groups calls into step records. Only recognized steps form a
// StepRecord; a StepUnknown call stays in Calls and is NEVER folded into a step.
// A repeated attributed subcommand after that step was already seen increments
// the call's Attempt (prior+1); StepRecord.Retries == Calls-1. The returned slice
// is ordered by the first appearance of each step.
func AttributeCalls(calls []CallRecord) []StepRecord {
	type acc struct {
		step      StepID
		calls     int
		usage     Usage
		firstSeen int
		first     int // index of first call
		last      int // index of last call
	}
	order := make([]StepID, 0, len(calls))
	byStep := make(map[StepID]*acc)
	attempts := make(map[StepID]int)

	for i := range calls {
		step := calls[i].Step
		if _, ok := stepOrder[step]; !ok {
			// StepUnknown (or any unrecognized step): never folded in.
			calls[i].Attempt = 0
			continue
		}
		a, ok := byStep[step]
		if !ok {
			a = &acc{step: step, usage: ZeroUsage(), firstSeen: i, first: i}
			byStep[step] = a
			order = append(order, step)
		}
		attempts[step]++
		calls[i].Attempt = attempts[step]
		a.calls++
		a.usage = a.usage.Add(calls[i].Usage)
		a.last = i
	}

	out := make([]StepRecord, 0, len(order))
	for _, step := range order {
		a := byStep[step]
		out = append(out, StepRecord{
			Step:    step,
			Calls:   a.calls,
			Retries: a.calls - 1,
			Usage:   a.usage,
			Elapsed: elapsedBetween(calls, a.first, a.last),
			// State is the STEP's own outcome: the step was observed to execute.
			// It is distinct from RunRecord.State, which reflects whether the RUN
			// reached submit/export.
			State: RunCompleted,
		})
	}
	return out
}

// elapsedBetween returns the transcript-derived interval from the first to the
// last call of a step. A single call yields Known(0); a missing timestamp yields
// UNKNOWN (never zero).
func elapsedBetween(calls []CallRecord, first, last int) Duration {
	if first < 0 || last < 0 || first >= len(calls) || last >= len(calls) {
		return Duration{Value: workflow.UnknownMeasurement(), Source: DurationUnknown}
	}
	if calls[first].Timestamp.IsZero() || calls[last].Timestamp.IsZero() {
		return Duration{Value: workflow.UnknownMeasurement(), Source: DurationUnknown}
	}
	ms := calls[last].Timestamp.Sub(calls[first].Timestamp).Milliseconds()
	if ms < 0 {
		ms = 0
	}
	return Duration{Value: workflow.KnownMeasurement(ms), Source: DurationTranscript}
}

// SortSteps returns steps in ascending step-progression order (StepUnknown last).
// It is used for deterministic report rendering.
func SortSteps(steps []StepRecord) []StepRecord {
	out := make([]StepRecord, len(steps))
	copy(out, steps)
	sort.SliceStable(out, func(i, j int) bool {
		oi, iok := stepOrder[out[i].Step]
		oj, jok := stepOrder[out[j].Step]
		if iok != jok {
			return iok
		}
		return oi < oj
	})
	return out
}
