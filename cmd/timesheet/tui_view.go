package main

// The TUI's rendering: pure functions, one per screen, model to string.

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/huyle-gradion/pilot-timesheet/internal/timesheet"
)

// sanitizeContent strips every control rune except '\n' from one recorded string
// at its last hop into a rendered frame. Recorded content — evidence text,
// descriptions, protocol strings, plan bodies, approval rows, stop reasons, error
// and notice lines, authored bench text — passes through here exactly once. The
// UI's own styling never passes through it, so styling escapes survive. This
// replaces the deleted frame-wide sanitizeFrame: washing the whole frame would
// wash the styling too.
//
// Invariant: every control rune in a finished frame came from the style layer
// (tui_style.go), never from content.
func sanitizeContent(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' {
			return -1
		}
		return r
	}, s)
}

// renderDay renders the day screen: the loop state and the key legend.
func renderDay(m tuiModel) string {
	var b strings.Builder
	b.WriteString("pilot timesheet " + timesheet.AppVersion + "   day " + sanitizeContent(m.snap.day))
	if m.snap.project != "" {
		b.WriteString("   project " + sanitizeContent(m.snap.project))
	}
	b.WriteString("\n")

	a := m.snap.action
	line := "state " + sanitizeContent(a.State)
	if a.Step != "" {
		line += "   step " + sanitizeContent(a.Step)
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
		b.WriteString("\nreason\n  " + sanitizeContent(a.Reason) + "\n")
	case a.State == stateHumanGates && a.Human != nil:
		b.WriteString("\n")
		renderHumanGates(&b, a, m.snap.status)
	}
	return b.String() + renderFooter(m)
}

// renderAction renders the derived argv and the observation that selected it.
func renderAction(b *strings.Builder, a nextAction) {
	b.WriteString("argv\n  " + sanitizeContent(shellJoin(a.Action.Cmd)) + "\nwhy\n  " + sanitizeContent(a.Action.Why) + "\n")
}

// renderJudgment renders the judgment the loop asks for, protocol included.
func renderJudgment(b *strings.Builder, a nextAction) {
	j := a.Judgment
	b.WriteString("kind         " + sanitizeContent(j.Kind) + "\n")
	b.WriteString("answer file  " + sanitizeContent(j.AnswerFile) + "\n")
	if j.DraftDigest != "" {
		b.WriteString("draft digest " + sanitizeContent(j.DraftDigest) + "\n")
	}
	if j.LedgerDigest != "" {
		b.WriteString("ledger digest " + sanitizeContent(j.LedgerDigest) + "\n")
	}
	b.WriteString("protocol\n  " + sanitizeContent(j.Protocol) + "\n")
	if len(j.Blocks) > 0 {
		b.WriteString("blocks\n")
		for _, bl := range j.Blocks {
			b.WriteString("  " + sanitizeContent(bl.IntervalID) + "  " + sanitizeContent(bl.BlockID) + "  " + sanitizeContent(bl.Start) + "-" + sanitizeContent(bl.End) + "  " + sanitizeContent(bl.Description) + "\n")
		}
	}
}

// renderHumanGates renders the terminal gate state: the human-only commands,
// store-wide unresolved-write counts, and the day's submission status with
// its unresolved rows and active approvals.
func renderHumanGates(b *strings.Builder, a nextAction, status *timesheet.SubmissionStatusResult) {
	h := a.Human
	b.WriteString("approve\n  " + sanitizeContent(h.ApproveCommand) + "\n")
	b.WriteString("submit\n  " + sanitizeContent(h.SubmitCommand) + "\n")
	b.WriteString("status\n  " + sanitizeContent(h.StatusCommand) + "\n")
	b.WriteString("unresolved writes " + strconv.Itoa(h.Unresolved) + " legacy, " + strconv.Itoa(h.NativeUnresolved) + " native\n")
	if status == nil {
		return
	}
	for _, r := range status.Unresolved {
		row := "  unresolved " + sanitizeContent(r.WriteRef) + " (" + sanitizeContent(r.Origin) + " " + sanitizeContent(r.Status)
		if r.ErrorCode != "" {
			row += " " + sanitizeContent(r.ErrorCode)
		}
		row += ") " + sanitizeContent(r.Detail) + "\n"
		b.WriteString(row)
	}
	for _, bl := range status.Blockers {
		b.WriteString("  blocker " + sanitizeContent(bl.Code) + ": " + sanitizeContent(bl.Detail) + "\n")
	}
	for _, ap := range status.Approvals {
		b.WriteString("  approval " + sanitizeContent(ap.ApprovalID) + " plan " + sanitizeContent(ap.PlanID) + " " + sanitizeContent(ap.State) + "\n")
	}
	b.WriteString("next: " + sanitizeContent(status.NextAction) + "\n")
}

// renderFooter renders the one dynamic line: the busy lock, the last error, or
// the key legend.
func renderFooter(m tuiModel) string {
	var line string
	switch {
	case m.err != nil:
		line = "error: " + sanitizeContent(m.err.Error())
	case m.busy:
		line = "working: keys are locked until the current step finishes"
	case m.notice != "":
		line = sanitizeContent(m.notice)
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
	out := "author " + sanitizeContent(b.kind) + "\n" +
		"answer file  " + sanitizeContent(b.answerFile) + "\n" +
		"digest       " + sanitizeContent(b.digest) + " (copied from the judgment; keep this field)\n\n" +
		renderCaret(b.text, b.caret) + "\n"
	if m.err != nil {
		out += "\nerror: " + sanitizeContent(m.err.Error()) + "\n"
	}
	return out + "\n[ctrl+s] write   [esc] abandon form, unsaved text dies\n"
}

// renderGate renders a display-before-ask gate dialog: the fresh artifact and
// the exact id to type.
func renderGate(m tuiModel) string {
	b := m.bench
	out := "human gate: " + sanitizeContent(b.kind) + "\n"
	if b.kind == gateApprove && b.preview != nil {
		out += "plan_id: " + sanitizeContent(b.preview.PlanID) + "   state: " + sanitizeContent(b.preview.State) + "\n" + sanitizeContent(renderPlanBody(*b.preview))
	} else if b.kind == gateSubmit {
		out += "active approvals\n"
		for _, a := range b.approvals {
			out += "  " + sanitizeContent(a.ApprovalID) + "  plan " + sanitizeContent(a.PlanID) + "  " + sanitizeContent(a.State) + "\n"
		}
		if len(b.approvals) == 0 {
			out += "  (none)\n"
		}
	}
	out += "\n" + sanitizeContent(b.answerFile) + "\n" + renderCaret(b.text, b.caret) + "\n"
	if m.err != nil {
		out += "\nerror: " + sanitizeContent(m.err.Error()) + "\n"
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
	return sanitizeContent(string(runes[:caret])) + "▌" + sanitizeContent(string(runes[caret:]))
}
