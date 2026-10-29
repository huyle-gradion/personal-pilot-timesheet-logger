package main

// The TUI's rendering: pure functions, one per screen, model to string. The
// day screen renders the loop's own nextAction verbatim — state, step, why,
// judgment protocol, human-gate counts — and never paraphrases a reason or
// improvises a remedy. Nil-safe: every optional payload is checked before it
// is dereferenced.

import (
	"strconv"
	"strings"

	"github.com/huyle-gradion/pilot-timesheet/internal/timesheet"
)

// renderDay renders the day screen: the loop state and the key legend.
func renderDay(m tuiModel) string {
	var b strings.Builder
	header := "pilot timesheet " + timesheet.AppVersion + "   day " + m.snap.day
	if m.snap.project != "" {
		header += "   project " + m.snap.project
	}
	b.WriteString(header + "\n")

	a := m.snap.action
	line := "state " + a.State
	if a.Step != "" {
		line += "   step " + a.Step
	}
	b.WriteString(line + "\n")

	switch {
	case a.State == stateAction && a.Action != nil:
		b.WriteString("\n")
		renderAction(&b, a)
	case a.State == stateJudgment && a.Judgment != nil:
		b.WriteString("\n")
		renderJudgment(&b, a)
	case a.State == stateStopped:
		b.WriteString("\n")
		renderStopped(&b, a)
	case a.State == stateHumanGates && a.Human != nil:
		b.WriteString("\n")
		renderHumanGates(&b, a)
	}
	return b.String() + renderFooter(m)
}

// renderAction renders the derived argv and the observation that selected it.
func renderAction(b *strings.Builder, a nextAction) {
	b.WriteString("argv\n  " + shellJoin(a.Action.Cmd) + "\n")
	b.WriteString("why\n  " + a.Action.Why + "\n")
}

// renderJudgment renders the judgment the loop asks for, including the exact
// answer-file protocol the operator must satisfy and the staged content the
// classification judgment lists.
func renderJudgment(b *strings.Builder, a nextAction) {
	j := a.Judgment
	b.WriteString("kind         " + j.Kind + "\n")
	b.WriteString("answer file  " + j.AnswerFile + "\n")
	if j.DraftDigest != "" {
		b.WriteString("draft digest " + j.DraftDigest + "\n")
	}
	if j.LedgerDigest != "" {
		b.WriteString("ledger digest " + j.LedgerDigest + "\n")
	}
	b.WriteString("protocol\n  " + j.Protocol + "\n")
	if len(j.Blocks) > 0 {
		b.WriteString("blocks\n")
		for _, bl := range j.Blocks {
			b.WriteString("  " + bl.IntervalID + "  " + bl.BlockID + "  " + bl.Start + "-" + bl.End + "  " + bl.Description + "\n")
		}
	}
}

// renderStopped renders the blocker and remedy verbatim; remedies that are CLI
// paths already arrive as commands inside the reason text.
func renderStopped(b *strings.Builder, a nextAction) {
	b.WriteString("reason\n  " + a.Reason + "\n")
}

// renderHumanGates renders the terminal gate state: the human-only commands
// and the store-wide unresolved-write counts.
func renderHumanGates(b *strings.Builder, a nextAction) {
	h := a.Human
	b.WriteString("approve\n  " + h.ApproveCommand + "\n")
	b.WriteString("submit\n  " + h.SubmitCommand + "\n")
	b.WriteString("status\n  " + h.StatusCommand + "\n")
	b.WriteString("unresolved writes " + strconv.Itoa(h.Unresolved) + " legacy, " + strconv.Itoa(h.NativeUnresolved) + " native\n")
}

// renderFooter renders the one dynamic line: the busy lock, the last error, or
// the key legend.
func renderFooter(m tuiModel) string {
	var line string
	switch {
	case m.err != nil:
		line = "error: " + m.err.Error()
	case m.busy:
		line = "working: keys are locked until the current step finishes"
	default:
		line = "[r] re-derive   [q] quit, discarding unsaved session state"
	}
	return "\n" + line + "\n"
}
