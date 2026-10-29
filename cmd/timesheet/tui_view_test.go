package main

import (
	"strings"
	"testing"
	"unicode"

	"github.com/huyle-gradion/pilot-timesheet/internal/timesheet"
)

// --- golden view tests: the day screen renders the loop verbatim --------------

func dayScreen(snap loopSnapshot) string {
	return renderDay(tuiModel{snap: snap})
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
			if got := renderFooter(tc.model); got != tc.want {
				t.Fatalf("footer = %q, want %q", got, tc.want)
			}
		})
	}
}

func errForTest(msg string) error { return &appErrForTest{msg} }

type appErrForTest struct{ msg string }

func (e *appErrForTest) Error() string { return e.msg }

// TestViewStripsControlRunes pins the frame boundary: an ESC or any other
// control byte in recorded content never reaches the terminal, while the text
// around it and newlines do.
func TestViewStripsControlRunes(t *testing.T) {
	snap := loopSnapshot{day: "2026-10-20", project: "WORK", action: nextAction{Day: "2026-10-20"}.withStop(
		"needful \x1b[31mred\x07 reason with\na newline")}
	frame := tuiModel{snap: snap}.View()
	for _, r := range frame {
		if unicode.IsControl(r) && r != '\n' {
			t.Fatalf("the frame carried control rune %q:\n%q", r, frame)
		}
	}
	if !strings.Contains(frame, "[31mred reason with\na newline") {
		t.Fatalf("the frame lost content it must keep:\n%q", frame)
	}
}

// TestRenderBenchGolden pins the bench form: the pre-filled document, the
// digest header, and the visible caret.
func TestRenderBenchGolden(t *testing.T) {
	m := tuiModel{bench: benchState{
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
	if got := renderBench(m); got != want {
		t.Fatalf("rendered bench differs:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}
