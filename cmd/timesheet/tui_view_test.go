package main

import (
	"strings"
	"testing"
	"unicode"

	"github.com/huyle-gradion/pilot-timesheet/internal/timesheet"
)

// --- golden view tests: the day screen renders the loop verbatim --------------

func dayScreen(snap loopSnapshot) string {
	return stripANSI(renderDay(tuiModel{env: tuiEnv{styles: testStyles()}, snap: snap}))
}

func TestRenderDayGolden(t *testing.T) {
	tests := []struct {
		name string
		snap loopSnapshot
		want string
	}{
		{
			name: "action renders the argv and why verbatim",
			snap: loopSnapshot{day: "2026-10-20", project: "WORK", action: nextAction{Day: "2026-10-20"}.withAction(
				"run",
				[]string{"/bin/timesheet", "run", "--config", "/cfg/config.json", "--date", "2026-10-20"},
				"no draft artifact exists at /tmp/x/draft.json for this day",
			)},
			want: "pilot timesheet " + timesheet.AppVersion + `   day 2026-10-20   project WORK
state action   step run

argv
  /bin/timesheet run --config /cfg/config.json --date 2026-10-20
why
  no draft artifact exists at /tmp/x/draft.json for this day

[r] re-derive   [enter] run the derived step   [q] quit, discarding unsaved session state
`,
		},
		{
			name: "judgment renders the protocol and blocks verbatim",
			snap: loopSnapshot{day: "2026-10-20", project: "WORK", action: nextAction{Day: "2026-10-20"}.withJudgment(
				"review",
				nextJudgment{
					Kind:        judgmentBlockDescriptions,
					Day:         "2026-10-20",
					AnswerFile:  "/tmp/x/descriptions.json",
					DraftDigest: "abc123",
					Protocol:    "write {\"draft_digest\":\"abc123\"} to /tmp/x/descriptions.json",
				},
			)},
			want: "pilot timesheet " + timesheet.AppVersion + `   day 2026-10-20   project WORK
state judgment   step review

kind         block_descriptions
answer file  /tmp/x/descriptions.json
draft digest abc123
protocol
  write {"draft_digest":"abc123"} to /tmp/x/descriptions.json

[r] re-derive   [enter] run the derived step   [q] quit, discarding unsaved session state
`,
		},
		{
			name: "classification judgment renders the staged blocks",
			snap: loopSnapshot{day: "2026-10-20", project: "WORK", action: nextAction{Day: "2026-10-20"}.withJudgment(
				"classify",
				nextJudgment{
					Kind:         judgmentClassifications,
					Day:          "2026-10-20",
					AnswerFile:   "/tmp/x/choices.json",
					LedgerDigest: "ef4567",
					Protocol:     "write {\"ledger_digest\":\"ef4567\"} to /tmp/x/choices.json",
					Blocks: []nextBlock{
						{IntervalID: "iv-20261020-1", BlockID: "b-1", Start: "10:00", End: "11:00", Description: "Standup"},
					},
				},
			)},
			want: "pilot timesheet " + timesheet.AppVersion + `   day 2026-10-20   project WORK
state judgment   step classify

kind         classifications
answer file  /tmp/x/choices.json
ledger digest ef4567
protocol
  write {"ledger_digest":"ef4567"} to /tmp/x/choices.json
blocks
  iv-20261020-1  b-1  10:00-11:00  Standup

[r] re-derive   [enter] run the derived step   [q] quit, discarding unsaved session state
`,
		},
		{
			name: "stopped renders the reason verbatim",
			snap: loopSnapshot{day: "2026-10-20", project: "WORK", action: nextAction{Day: "2026-10-20"}.withStop(
				"the source gate is closed by an unacknowledged attempt",
			)},
			want: "pilot timesheet " + timesheet.AppVersion + `   day 2026-10-20   project WORK
state stopped

reason
  the source gate is closed by an unacknowledged attempt

[r] re-derive   [enter] run the derived step   [q] quit, discarding unsaved session state
`,
		},
		{
			name: "human gates render the commands and store-wide counts",
			snap: loopSnapshot{day: "2026-10-20", project: "WORK", action: nextAction{Day: "2026-10-20"}.withHuman(
				nextHumanGates{
					ApproveCommand: "/bin/timesheet approve --config /cfg/config.json --date 2026-10-20",
					SubmitCommand:  "/bin/timesheet submit --config /cfg/config.json --date 2026-10-20 --approval-id <approval_id>",
					StatusCommand:  "/bin/timesheet submit-status --config /cfg/config.json --date 2026-10-20",
					Unresolved:     1, NativeUnresolved: 2,
				},
			)},
			want: "pilot timesheet " + timesheet.AppVersion + `   day 2026-10-20   project WORK
state human_gates   step approve

approve
  /bin/timesheet approve --config /cfg/config.json --date 2026-10-20
submit
  /bin/timesheet submit --config /cfg/config.json --date 2026-10-20 --approval-id <approval_id>
status
  /bin/timesheet submit-status --config /cfg/config.json --date 2026-10-20
unresolved writes 1 legacy, 2 native

[enter] approve gate   [s] submit gate   [r] re-derive   [q] quit
`,
		},
		{
			name: "a missing optional payload renders the state without panic",
			snap: loopSnapshot{day: "2026-10-20", action: nextAction{Day: "2026-10-20", State: stateAction}},
			want: "pilot timesheet " + timesheet.AppVersion + `   day 2026-10-20
state action

[r] re-derive   [enter] run the derived step   [q] quit, discarding unsaved session state
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := dayScreen(tc.snap); got != tc.want {
				t.Fatalf("rendered day screen differs:\n--- got ---\n%s--- want ---\n%s", got, tc.want)
			}
		})
	}
}

// TestRenderFooterGolden pins the dynamic line: busy lock, last error, key
// legend.
func TestRenderFooterGolden(t *testing.T) {
	tests := []struct {
		name  string
		model tuiModel
		want  string
	}{
		{
			name:  "idle shows the key legend",
			model: tuiModel{},
			want:  "\n[r] re-derive   [enter] run the derived step   [q] quit, discarding unsaved session state\n",
		},
		{
			name:  "busy locks the keys honestly",
			model: tuiModel{busy: true},
			want:  "\nworking: keys are locked until the current step finishes\n",
		},
		{
			name:  "an error renders typed",
			model: tuiModel{err: errForTest("stale_revision: expected revision 1 but the day is at revision 2")},
			want:  "\nerror: stale_revision: expected revision 1 but the day is at revision 2\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.model
			m.env.styles = testStyles()
			if got := stripANSI(renderFooter(m)); got != tc.want {
				t.Fatalf("footer = %q, want %q", got, tc.want)
			}
		})
	}
}

func errForTest(msg string) error { return &appErrForTest{msg} }

type appErrForTest struct{ msg string }

func (e *appErrForTest) Error() string { return e.msg }

// TestRecordedContentNeverDrivesTheTerminal feeds control runes into every
// recorded field of every render branch; the stripped frame must carry none yet
// keep the bracketed payload as plain text, so a missed sanitizeContent call
// site fails here.
func TestRecordedContentNeverDrivesTheTerminal(t *testing.T) {
	const hostile = "evil\x1b[31mred\x07tail"
	model := func(snap loopSnapshot) tuiModel {
		return tuiModel{env: tuiEnv{styles: testStyles()}, snap: snap}
	}
	frames := []struct {
		name  string
		model tuiModel
	}{
		{
			name:  "stopped reason",
			model: model(loopSnapshot{day: hostile, action: nextAction{Day: hostile}.withStop(hostile)}),
		},
		{
			name: "judgment protocol and blocks",
			model: model(loopSnapshot{day: hostile, project: hostile, action: nextAction{Day: hostile}.withJudgment(
				"review",
				nextJudgment{
					Kind: hostile, AnswerFile: hostile, DraftDigest: hostile,
					LedgerDigest: hostile, Protocol: hostile,
					Blocks: []nextBlock{{IntervalID: hostile, BlockID: hostile, Start: hostile, End: hostile, Description: hostile}},
				},
			)}),
		},
		{
			name: "human gates rows",
			model: model(loopSnapshot{day: hostile, project: hostile, action: nextAction{Day: hostile}.withHuman(
				nextHumanGates{ApproveCommand: hostile, SubmitCommand: hostile, StatusCommand: hostile},
			), status: &timesheet.SubmissionStatusResult{
				Unresolved: []timesheet.UnresolvedRow{{WriteRef: hostile, Origin: hostile, Status: hostile, ErrorCode: hostile, Detail: hostile}},
				Blockers:   []timesheet.Blocker{{Code: hostile, Detail: hostile}},
				Approvals:  []timesheet.ApprovalRow{{ApprovalID: hostile, PlanID: hostile, State: hostile}},
				NextAction: hostile,
			}}),
		},
		{
			name: "approve plan body",
			model: tuiModel{env: tuiEnv{styles: testStyles()}, bench: benchState{
				screen: screenGate, kind: gateApprove, answerFile: hostile, text: hostile, caret: 2,
				preview: &timesheet.PlanPreview{
					PlanID: hostile, State: hostile,
					Operations: []timesheet.PlanOperation{{
						Kind: hostile, Date: hostile, LocalID: hostile, StartTime: hostile, EndTime: hostile,
						Description: hostile, ClassificationID: hostile, Task: hostile, Reason: hostile,
					}},
					Blockers: []timesheet.Blocker{{Code: hostile, Detail: hostile}},
				},
			}},
		},
		{
			name: "submit approvals",
			model: tuiModel{env: tuiEnv{styles: testStyles()}, bench: benchState{
				screen: screenGate, kind: gateSubmit, answerFile: hostile, text: hostile, caret: 2,
				approvals: []timesheet.ApprovalRow{{ApprovalID: hostile, PlanID: hostile, State: hostile}},
			}},
		},
		{
			name: "bench text",
			model: tuiModel{env: tuiEnv{styles: testStyles()}, bench: benchState{
				screen: screenBench, kind: hostile, answerFile: hostile, digest: hostile, text: hostile, caret: 2,
			}},
		},
		{
			name:  "footer error",
			model: tuiModel{env: tuiEnv{styles: testStyles()}, err: errForTest(hostile)},
		},
		{
			name:  "footer notice",
			model: tuiModel{env: tuiEnv{styles: testStyles()}, notice: hostile},
		},
	}
	for _, tc := range frames {
		t.Run(tc.name, func(t *testing.T) {
			frame := stripANSI(tc.model.View())
			for _, r := range frame {
				if unicode.IsControl(r) && r != '\n' {
					t.Fatalf("the frame carried control rune %q:\n%q", r, frame)
				}
			}
			if !strings.Contains(frame, "[31m") || !strings.Contains(frame, "evil") || !strings.Contains(frame, "tail") {
				t.Fatalf("the frame lost recorded payload it must keep:\n%q", frame)
			}
		})
	}
}

// TestRenderBenchGolden pins the bench form: the pre-filled document, the
// digest header, and the visible caret.
func TestRenderBenchGolden(t *testing.T) {
	m := tuiModel{env: tuiEnv{styles: testStyles()}, bench: benchState{
		screen: screenBench, kind: judgmentBlockDescriptions,
		digest: "d1", answerFile: "/tmp/x/descriptions.json",
		text:  "{\"draft_digest\": \"d1\",\"descriptions\": {\"b-1\": \"\"}}\n",
		caret: 4,
	}}
	want := "author block_descriptions\n" +
		"answer file  /tmp/x/descriptions.json\n" +
		"digest       d1 (copied from the judgment; keep this field)\n" +
		"\n" +
		"{\"dr▌aft_digest\": \"d1\",\"descriptions\": {\"b-1\": \"\"}}\n" +
		"\n" +
		"\n" +
		"[ctrl+s] write   [esc] abandon form, unsaved text dies\n"
	if got := stripANSI(renderBench(m)); got != want {
		t.Fatalf("rendered bench differs:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestFitTail(t *testing.T) {
	s := testStyles()
	frame := "a\nb\nc\nd"
	for _, n := range []int{0, -1} {
		if got := fitTail(frame, n, s); got != frame {
			t.Fatalf("fitTail(%d) = %q, want the frame unchanged with no geometry", n, got)
		}
	}
	if got := fitTail(frame, 4, s); got != frame {
		t.Fatalf("fitTail that fits = %q, want unchanged", got)
	}
	if got, want := fitTail(frame, 2, s), "+2 lines\nc\nd"; got != want {
		t.Fatalf("fitTail = %q, want %q", got, want)
	}
}

func TestFitAround(t *testing.T) {
	frame := "0\n1\n2\n3\n4\n5\n6\n7\n8\n9"
	tests := []struct {
		name   string
		n      int
		anchor int
		want   string
	}{
		{"no geometry leaves the frame whole", 0, 5, frame},
		{"an anchor in the middle centers the window", 4, 5, "3\n4\n5\n6"},
		{"an anchor at the top clamps the start", 4, 0, "0\n1\n2\n3"},
		{"an anchor at the bottom clamps the end", 4, 9, "6\n7\n8\n9"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := fitAround(frame, tc.n, tc.anchor); got != tc.want {
				t.Fatalf("fitAround(%d, %d) = %q, want %q", tc.n, tc.anchor, got, tc.want)
			}
		})
	}
}

func TestCaretLineOf(t *testing.T) {
	const text = "ab\ncd"
	tests := []struct {
		caret int
		want  int
	}{
		{0, 0},
		{2, 0},
		{3, 1},
		{5, 1},
		{-4, 0},
		{99, 1},
	}
	for _, tc := range tests {
		if got := caretLineOf(text, tc.caret); got != tc.want {
			t.Fatalf("caretLineOf(%q, %d) = %d, want %d", text, tc.caret, got, tc.want)
		}
	}
}

func TestDayScreenClipsTallBodyHonestly(t *testing.T) {
	blocks := make([]nextBlock, 40)
	for i := range blocks {
		blocks[i] = nextBlock{IntervalID: "iv", BlockID: "b", Start: "10:00", End: "11:00", Description: "work"}
	}
	m := tuiModel{
		env:  tuiEnv{styles: testStyles()},
		view: viewState{height: 15},
		snap: loopSnapshot{day: "2026-10-20", action: nextAction{Day: "2026-10-20"}.withJudgment(
			"review", nextJudgment{Kind: judgmentBlockDescriptions, AnswerFile: "/tmp/x", Protocol: "write", Blocks: blocks})},
	}
	frame := stripANSI(renderDay(m))
	for _, want := range []string{
		"pilot timesheet " + timesheet.AppVersion,
		"state judgment   step review",
		"[r] re-derive   [enter] run the derived step",
		" lines\n",
	} {
		if !strings.Contains(frame, want) {
			t.Fatalf("clipped day frame lost %q:\n%s", want, frame)
		}
	}
	if !strings.Contains(frame, "+") {
		t.Fatalf("clipped day frame has no hidden-lines indicator:\n%s", frame)
	}
}

func TestBenchScreenFitsAroundTheCaret(t *testing.T) {
	lines := make([]string, 41)
	for i := range lines {
		lines[i] = "line" + string(rune('A'+i%26))
	}
	text := strings.Join(lines, "\n")
	caret := len([]rune(strings.Join(lines[:20], "\n"))) + 1
	m := tuiModel{
		env:  tuiEnv{styles: testStyles()},
		view: viewState{height: 15},
		bench: benchState{
			screen: screenBench, kind: judgmentBlockDescriptions, digest: "d1",
			answerFile: "/tmp/x/descriptions.json", text: text, caret: caret,
		},
	}
	frame := stripANSI(renderBench(m))
	for _, want := range []string{
		"author block_descriptions",
		"digest       d1 (copied from the judgment; keep this field)",
		"[ctrl+s] write   [esc] abandon form, unsaved text dies",
		"▌lineU",
	} {
		if !strings.Contains(frame, want) {
			t.Fatalf("fitted bench frame lost %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "lineA") {
		t.Fatalf("fitted bench frame kept the far-away head of the document:\n%s", frame)
	}
}
