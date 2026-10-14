package metrics

import (
	"sort"
	"strings"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// Attribution heuristic (LABELLED): a call is attributed to a workflow step by
// the `timesheet` subcommand token carried in the assistant turn's tool call.
// The subcommand is found whether the binary is invoked directly or inside a
// compound shell command (`cd x && "$T/ts" review`, `for ...; do "$TS" preview;
// done`), but ONLY when the token after the binary is a recognized subcommand: a
// path or argument that merely mentions `timesheet` is never guessed into a step.
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

// subcommandTokens is the recognised subcommand vocabulary. A `timesheet`/`ts`
// token is accepted as an invocation ONLY when the token immediately following it
// is one of these; otherwise the scan keeps going and NEVER guesses. It is a
// superset of the mapped steps (subcommandSteps): tokens such as `classify`,
// `resolve`, `run`, `config` and `metrics` are real subcommands that carry no
// step of their own and therefore attribute to StepUnknown.
var subcommandTokens = map[string]bool{
	"preflight": true,
	"review":    true,
	"preview":   true,
	"approve":   true,
	"submit":    true,
	"export":    true,
	"classify":  true,
	"resolve":   true,
	"run":       true,
	"config":    true,
	"metrics":   true,
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

// subcommandAndArgs returns the subcommand token and its trailing arguments when
// the command invokes the timesheet binary, whether directly or inside a compound
// shell command:
//
//   - a token whose BASENAME (after stripping surrounding quote characters and any
//     `$`/`${...}` variable syntax) is `timesheet` or `ts` — e.g. `timesheet`,
//     `/tmp/rt/off/ts`, `./bin/timesheet`, `"$T/ts"`, `$TS`, `${TS}`; and
//   - that token sits at the start of a simple command (the first token, after a
//     shell separator, or after the `go run` prefix), not as an argument; and
//   - the token IMMEDIATELY FOLLOWING it is a recognized subcommand token
//     (subcommandTokens).
//
// Any other command yields no subcommand: a path that merely CONTAINS `timesheet`
// (`cat ~/.agents/skills/timesheet/workflow.md`, `ls cmd/timesheet`), a mention in
// an argument (`grep -r timesheet submit`, `echo timesheet review`), or a bare
// binary with no subcommand (`"$T/ts" --help`) is never guessed into a step. Only
// the subcommand TOKEN is returned; the command text is never retained.
func subcommandAndArgs(command string) (string, []string) {
	fields := strings.Fields(command)
	for i := 0; i < len(fields); i++ {
		if !isTimesheetToken(fields[i]) || !isInvocationPosition(fields, i) {
			continue
		}
		if i+1 >= len(fields) || !subcommandTokens[fields[i+1]] {
			// The next token is not a subcommand (a flag, a path segment, or
			// nothing): keep scanning and never guess.
			continue
		}
		return fields[i+1], fields[i+2:]
	}
	return "", nil
}

// isTimesheetToken reports whether a shell token names the timesheet binary. The
// basename is taken after stripping surrounding quote characters and any `$` or
// `${...}` variable syntax, so a variable expansion such as `"$T/ts"`, `$TS` or
// `${TS}` is recognized. The comparison is case-insensitive (`$TS` names `ts`).
func isTimesheetToken(token string) bool {
	t := strings.Trim(token, `"'`)
	t = strings.TrimPrefix(t, "$")
	if strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}") {
		t = t[1 : len(t)-1]
	}
	return executableBases[strings.ToLower(basename(t))]
}

// isInvocationPosition reports whether the token at idx begins a simple command
// within the (possibly compound) command, so a timesheet token there is an
// invocation rather than an argument. It accepts the start of the command, the
// `go run` prefix, and any shell separator.
func isInvocationPosition(fields []string, idx int) bool {
	if idx == 0 {
		return true
	}
	if idx >= 2 && fields[idx-2] == "go" && fields[idx-1] == "run" {
		return true
	}
	return isCommandSeparator(fields[idx-1])
}

// isCommandSeparator reports whether a token ends a simple command: a standalone
// separator (`&&`, `;`, `|`, ...), a `do`/`then`/`else` keyword, or a token with a
// separator glued to its end (e.g. the `"$DIR";` in `cd "$DIR"; ts classify`).
func isCommandSeparator(token string) bool {
	t := strings.Trim(token, `"'`)
	switch t {
	case "&&", "||", ";", "|", "&", "(", "{", "do", "then", "else", "!":
		return true
	}
	if t == "" {
		return false
	}
	switch t[len(t)-1] {
	case ';', '&', '|':
		return true
	}
	return false
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
