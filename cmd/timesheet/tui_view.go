package main

// The TUI's rendering: pure functions, one per screen, model to string.

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/huyle-gradion/pilot-timesheet/internal/timesheet"
)

// sanitizeContent strips every control rune except '\n' from one recorded
// string at its last hop into a frame. Recorded content — evidence,
// descriptions, protocol strings, plan bodies, errors — passes through here
// exactly once; the UI's own styling never does, so every control rune in a
// finished frame came from the style layer, never from content.
func sanitizeContent(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' {
			return -1
		}
		return r
	}, s)
}

// Rows of pinned chrome per screen, so the clip budget keeps the screen's
// identity, its decision point and its key legend visible; the footer-pinned
// rendering tests fail when a renderer's layout drifts from these counts.
const (
	dayChromeRows   = 5 // title, state, footer blank, footer legend, trailing row
	gateChromeRows  = 8 // gate title, plan_id line, echo block, hint block, trailing row
	benchChromeRows = 7 // author, answer file, digest, two blanks, hint block, trailing row
	errorChromeRows = 2 // an error line plus its surrounding blank
)

// renderDay renders the day screen: the loop state and the key legend.
func renderDay(m tuiModel) string {
	s := m.env.styles
	var b strings.Builder
	b.WriteString(s.title.Render("pilot timesheet "+timesheet.AppVersion) + "   day " + sanitizeContent(m.snap.day))
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

	var body strings.Builder
	// The state block is the tail-clipped middle: its decision point (reason,
	// protocol tail, next: line) lives at the bottom, next to the pinned footer.
	switch {
	case a.State == stateAction && a.Action != nil:
		body.WriteString("\n")
		renderAction(&body, a, s)
	case a.State == stateJudgment && a.Judgment != nil:
		body.WriteString("\n")
		renderJudgment(&body, a, s)
	case a.State == stateStopped:
		// The blocker and remedy render verbatim; CLI remedies arrive as
		// commands inside the reason text.
		body.WriteString("\nreason\n  " + sanitizeContent(a.Reason) + "\n")
	case a.State == stateHumanGates && a.Human != nil:
		body.WriteString("\n")
		renderHumanGates(&body, a, m.snap.status, s)
	}
	stateBlock := body.String()
	if n := m.view.bodyHeight(dayChromeRows); n > 0 {
		stateBlock = fitTail(stateBlock, n, s)
	}
	return b.String() + stateBlock + renderFooter(m)
}

// renderAction renders the derived argv and the observation that selected it.
func renderAction(b *strings.Builder, a nextAction, s uiStyles) {
	b.WriteString(s.label.Render("argv") + "\n  " + sanitizeContent(shellJoin(a.Action.Cmd)) +
		"\n" + s.label.Render("why") + "\n  " + sanitizeContent(a.Action.Why) + "\n")
}

// renderJudgment renders the judgment the loop asks for, protocol included.
func renderJudgment(b *strings.Builder, a nextAction, s uiStyles) {
	j := a.Judgment
	b.WriteString(s.label.Render("kind") + "         " + sanitizeContent(j.Kind) + "\n")
	b.WriteString(s.label.Render("answer file") + "  " + sanitizeContent(j.AnswerFile) + "\n")
	if j.DraftDigest != "" {
		b.WriteString(s.label.Render("draft digest") + " " + sanitizeContent(j.DraftDigest) + "\n")
	}
	if j.LedgerDigest != "" {
		b.WriteString(s.label.Render("ledger digest") + " " + sanitizeContent(j.LedgerDigest) + "\n")
	}
	b.WriteString(s.label.Render("protocol") + "\n  " + sanitizeContent(j.Protocol) + "\n")
	if len(j.Blocks) > 0 {
		b.WriteString(s.label.Render("blocks") + "\n")
		for _, bl := range j.Blocks {
			b.WriteString("  " + sanitizeContent(bl.IntervalID) + "  " + sanitizeContent(bl.BlockID) + "  " + sanitizeContent(bl.Start) + "-" + sanitizeContent(bl.End) + "  " + sanitizeContent(bl.Description) + "\n")
		}
	}
}

// renderHumanGates renders the terminal gate state: the human-only commands,
// store-wide unresolved-write counts, and the day's submission status with
// its unresolved rows and active approvals.
func renderHumanGates(b *strings.Builder, a nextAction, status *timesheet.SubmissionStatusResult, s uiStyles) {
	h := a.Human
	b.WriteString(s.label.Render("approve") + "\n  " + sanitizeContent(h.ApproveCommand) + "\n")
	b.WriteString(s.label.Render("submit") + "\n  " + sanitizeContent(h.SubmitCommand) + "\n")
	b.WriteString(s.label.Render("status") + "\n  " + sanitizeContent(h.StatusCommand) + "\n")
	b.WriteString(s.label.Render("unresolved writes") + " " + strconv.Itoa(h.Unresolved) + " legacy, " + strconv.Itoa(h.NativeUnresolved) + " native\n")
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
	s := m.env.styles
	var line string
	switch {
	case m.err != nil:
		line = s.errSty.Render("error: ") + sanitizeContent(m.err.Error())
	case m.busy:
		line = s.hint.Render("working: keys are locked until the current step finishes")
	case m.notice != "":
		line = sanitizeContent(m.notice)
	case stepAct(m.snap.action) == stateHumanGates:
		line = s.hint.Render("[enter] approve gate   [s] submit gate   [r] re-derive   [q] quit")
	default:
		line = s.hint.Render("[r] re-derive   [enter] run the derived step   [q] quit, discarding unsaved session state")
	}
	return "\n" + line + "\n"
}

// renderBench renders the bench form: the pre-filled answer document the
// operator edits, with the judgment's digest shown above it.
func renderBench(m tuiModel) string {
	s := m.env.styles
	b := m.bench
	head := s.label.Render("author") + " " + sanitizeContent(b.kind) + "\n" +
		s.label.Render("answer file") + "  " + sanitizeContent(b.answerFile) + "\n" +
		s.label.Render("digest") + "       " + sanitizeContent(b.digest) + " (copied from the judgment; keep this field)\n\n"
	doc := renderCaret(b.text, b.caret, s)
	reserved := benchChromeRows
	if m.err != nil {
		reserved += errorChromeRows
	}
	if n := m.view.bodyHeight(reserved); n > 0 {
		doc = fitAround(doc, n, caretLineOf(b.text, b.caret))
	}
	out := head + doc + "\n"
	if m.err != nil {
		out += "\n" + s.errSty.Render("error: ") + sanitizeContent(m.err.Error()) + "\n"
	}
	return out + "\n" + s.hint.Render("[ctrl+s] write   [esc] abandon form, unsaved text dies") + "\n"
}

// renderGate renders a display-before-ask gate dialog: the fresh artifact and
// the exact id to type.
func renderGate(m tuiModel) string {
	s := m.env.styles
	b := m.bench
	head := s.gate.Render("human gate:") + " " + sanitizeContent(b.kind) + "\n"
	var middle string
	switch {
	case b.kind == gateApprove && b.preview != nil:
		head += s.gate.Render("plan_id:") + " " + sanitizeContent(b.preview.PlanID) +
			"   " + s.gate.Render("state:") + " " + sanitizeContent(b.preview.State) + "\n"
		middle = sanitizeContent(renderPlanBody(*b.preview))
	case b.kind == gateSubmit:
		head += s.label.Render("active approvals") + "\n"
		for _, a := range b.approvals {
			middle += "  " + sanitizeContent(a.ApprovalID) + "  plan " + sanitizeContent(a.PlanID) + "  " + sanitizeContent(a.State) + "\n"
		}
		if len(b.approvals) == 0 {
			middle = "  (none)\n"
		}
	}
	reserved := gateChromeRows
	if m.err != nil {
		reserved += errorChromeRows
	}
	if n := m.view.bodyHeight(reserved); n > 0 {
		middle = fitTail(middle, n, s)
	}
	tail := "\n" + sanitizeContent(b.answerFile) + "\n" + renderCaret(b.text, b.caret, s) + "\n"
	if m.err != nil {
		tail += "\n" + s.errSty.Render("error: ") + sanitizeContent(m.err.Error()) + "\n"
	}
	return head + middle + tail + "\n" + s.hint.Render("[enter] confirm (re-checks freshness first)   [esc] dismiss") + "\n"
}

func renderCaret(text string, caret int, s uiStyles) string {
	runes := []rune(text)
	if caret < 0 {
		caret = 0
	}
	if caret > len(runes) {
		caret = len(runes)
	}
	return sanitizeContent(string(runes[:caret])) + s.caret.Render("▌") + sanitizeContent(string(runes[caret:]))
}

// fitTail returns the last n lines of frame, prepending a faint "+N lines"
// indicator when it clips. n <= 0 returns frame unchanged: no geometry yet, or
// the pinned chrome already fills the window. Splitting on '\n' cannot cut a
// style sequence: Render arguments are single-line (the adjacency rule).
func fitTail(frame string, n int, s uiStyles) string {
	if n <= 0 {
		return frame
	}
	lines := strings.Split(frame, "\n")
	if len(lines) <= n {
		return frame
	}
	hidden := len(lines) - n
	return s.hint.Render("+"+strconv.Itoa(hidden)+" lines") + "\n" + strings.Join(lines[len(lines)-n:], "\n")
}

// fitAround returns a window of n lines of frame with anchorLine inside it,
// centered on the anchor as far as both edges allow.
func fitAround(frame string, n, anchorLine int) string {
	if n <= 0 {
		return frame
	}
	lines := strings.Split(frame, "\n")
	if len(lines) <= n {
		return frame
	}
	if anchorLine < 0 {
		anchorLine = 0
	}
	if anchorLine >= len(lines) {
		anchorLine = len(lines) - 1
	}
	start := anchorLine - n/2
	if start < 0 {
		start = 0
	}
	if start+n > len(lines) {
		start = len(lines) - n
	}
	return strings.Join(lines[start:start+n], "\n")
}

// caretLineOf reports which line of the raw text the caret (a rune index) sits on.
func caretLineOf(text string, caret int) int {
	runes := []rune(text)
	if caret < 0 {
		caret = 0
	}
	if caret > len(runes) {
		caret = len(runes)
	}
	return strings.Count(string(runes[:caret]), "\n")
}

// bodyHeight is the clip budget after reserving chrome lines; <= 0 means
// unbounded.
func (v viewState) bodyHeight(reserved int) int { return v.height - reserved }
