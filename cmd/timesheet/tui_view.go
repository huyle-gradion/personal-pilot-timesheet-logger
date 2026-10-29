package main

// The TUI's rendering: pure functions, one per screen, model to string.

import (
	"strconv"
	"strings"

	"github.com/huyle-gradion/pilot-timesheet/internal/timesheet"
)

// renderDay renders the day screen: the loop state and the key legend.
func renderDay(m tuiModel) string {
	var b strings.Builder
	b.WriteString("pilot timesheet " + timesheet.AppVersion + "   day " + m.snap.day)
	if m.snap.project != "" {
		b.WriteString("   project " + m.snap.project)
	}
	b.WriteString("\n")

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
		// The blocker and remedy render verbatim; CLI remedies arrive as
		// commands inside the reason text.
		b.WriteString("\nreason\n  " + a.Reason + "\n")
	case a.State == stateHumanGates && a.Human != nil:
		b.WriteString("\n")
		renderHumanGates(&b, a, m.snap.status)
	}
	return b.String() + renderFooter(m)
}

// renderAction renders the derived argv and the observation that selected it.
func renderAction(b *strings.Builder, a nextAction) {
	b.WriteString("argv\n  " + shellJoin(a.Action.Cmd) + "\nwhy\n  " + a.Action.Why + "\n")
}

// renderJudgment renders the judgment the loop asks for, protocol included.
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

// renderHumanGates renders the terminal gate state: the human-only commands,
// store-wide unresolved-write counts, and the day's submission status with
// its unresolved rows and active approvals.
func renderHumanGates(b *strings.Builder, a nextAction, status *timesheet.SubmissionStatusResult) {
	h := a.Human
	b.WriteString("approve\n  " + h.ApproveCommand + "\n")
	b.WriteString("submit\n  " + h.SubmitCommand + "\n")
	b.WriteString("status\n  " + h.StatusCommand + "\n")
	b.WriteString("unresolved writes " + strconv.Itoa(h.Unresolved) + " legacy, " + strconv.Itoa(h.NativeUnresolved) + " native\n")
	if status == nil {
		return
	}
	for _, r := range status.Unresolved {
		row := "  unresolved " + r.WriteRef + " (" + r.Origin + " " + r.Status
		if r.ErrorCode != "" {
			row += " " + r.ErrorCode
		}
		row += ") " + r.Detail + "\n"
		b.WriteString(row)
	}
	for _, bl := range status.Blockers {
		b.WriteString("  blocker " + bl.Code + ": " + bl.Detail + "\n")
	}
	for _, ap := range status.Approvals {
		b.WriteString("  approval " + ap.ApprovalID + " plan " + ap.PlanID + " " + ap.State + "\n")
	}
	b.WriteString("next: " + status.NextAction + "\n")
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
	case m.notice != "":
		line = m.notice
	case stepAct(m.snap.action) == stateHumanGates:
		line = "[enter] approve gate   [s] submit gate   [r] re-derive   [q] quit"
	default:
		line = "[r] re-derive   [enter] run the derived step   [q] quit, discarding unsaved session state"
	}
	return "\n" + line + "\n"
}

// renderBench renders the bench form: the pre-filled answer document the
// operator edits, with the judgment's digest shown above it.
func renderBench(m tuiModel) string {
	b := m.bench
	out := "author " + b.kind + "\n" +
		"answer file  " + b.answerFile + "\n" +
		"digest       " + b.digest + " (copied from the judgment; keep this field)\n\n" +
		renderCaret(b.text, b.caret) + "\n"
	if m.err != nil {
		out += "\nerror: " + m.err.Error() + "\n"
	}
	return out + "\n[ctrl+s] write   [esc] abandon form, unsaved text dies\n"
}

// renderGate renders a display-before-ask gate dialog: the fresh artifact and
// the exact id to type.
func renderGate(m tuiModel) string {
	b := m.bench
	out := "human gate: " + b.kind + "\n"
	if b.kind == gateApprove && b.preview != nil {
		out += "plan_id: " + b.preview.PlanID + "   state: " + b.preview.State + "\n" + renderPlanBody(*b.preview)
	} else if b.kind == gateSubmit {
		out += "active approvals\n"
		for _, a := range b.approvals {
			out += "  " + a.ApprovalID + "  plan " + a.PlanID + "  " + a.State + "\n"
		}
		if len(b.approvals) == 0 {
			out += "  (none)\n"
		}
	}
	out += "\n" + b.answerFile + "\n" + renderCaret(b.text, b.caret) + "\n"
	if m.err != nil {
		out += "\nerror: " + m.err.Error() + "\n"
	}
	return out + "\n[enter] confirm (re-checks freshness first)   [esc] dismiss\n"
}

func renderCaret(text string, caret int) string {
	runes := []rune(text)
	if caret < 0 {
		caret = 0
	}
	if caret > len(runes) {
		caret = len(runes)
	}
	return string(runes[:caret]) + "▌" + string(runes[caret:])
}
