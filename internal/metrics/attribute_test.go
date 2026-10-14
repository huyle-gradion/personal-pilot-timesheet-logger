package metrics

import "testing"

// TestAttributeCommand_Table covers the full attribution table plus the
// fail-closed cases (never guessed into a step).
func TestAttributeCommand_Table(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		step StepID
		cat  Category
	}{
		{"preflight", "timesheet preflight --config x.json", StepPreflight, CategoryOrchestration},
		{"review bare", "timesheet review --date 2026-10-11", StepCollect, CategoryOrchestration},
		{"review decisions", "timesheet review --date 2026-10-11 --decisions d.json", StepReview, CategoryOrchestration},
		{"review decisions equals", "timesheet review --decisions=d.json", StepReview, CategoryOrchestration},
		{"preview via ts alias", "/tmp/rt/off/ts preview --date 2026-10-11", StepPreview, CategoryOrchestration},
		{"approve via go run", "go run ./cmd/timesheet approve --plan abc", StepApprove, CategoryOrchestration},
		{"submit", "timesheet submit --date 2026-10-11", StepSubmit, CategoryOrchestration},
		{"export", "timesheet export --date 2026-10-11", StepExport, CategoryOrchestration},
		{"unrelated shell", "ls -la /tmp", StepUnknown, CategoryUnassigned},
		{"unrelated git", "git commit -m wip", StepUnknown, CategoryUnassigned},
		{"grep mentions timesheet", "grep -r timesheet submit", StepUnknown, CategoryUnassigned},
		{"echo mentions timesheet", "echo timesheet review", StepUnknown, CategoryUnassigned},
		// A compound invocation DOES resolve: the binary token after `&&` is an
		// invocation, not an argument. This case is the direct analogue of the
		// compound shapes covered by TestAttributeCommand_CompoundInvocation.
		{"compound cd prefix", "cd /repo && timesheet review", StepCollect, CategoryOrchestration},
		{"unknown subcommand", "timesheet frobnicate", StepUnknown, CategoryUnassigned},
		{"empty", "", StepUnknown, CategoryUnassigned},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			step, cat := AttributeCommand(tc.cmd)
			if step != tc.step || cat != tc.cat {
				t.Fatalf("AttributeCommand(%q) = (%q,%q), want (%q,%q)", tc.cmd, step, cat, tc.step, tc.cat)
			}
		})
	}
}

// TestAttributeCommand_CompoundInvocation covers the real agent shapes: the
// timesheet binary invoked inside a compound shell command (`&&`, `for ...; do`,
// `;`), including a quoted variable path. The subcommand token is found anywhere a
// simple command starts, and only when the next token is a recognized subcommand.
func TestAttributeCommand_CompoundInvocation(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		step StepID
		cat  Category
	}{
		{
			"cd && quoted var review (collect)",
			`cd /Users/huy.leminh/Projects/assignment-1 && "$T/ts" review --config "$CFG" --date "$D"`,
			StepCollect, CategoryOrchestration,
		},
		{
			"cd && quoted var review --decisions",
			`cd /Users/huy.leminh/Projects/assignment-1 && "$T/ts" review --config "$CFG" --date "$D" --decisions "$OUT"`,
			StepReview, CategoryOrchestration,
		},
		{
			"for loop do preview",
			`for f in a b c; do "$TS" preview --date "$D"; done`,
			StepPreview, CategoryOrchestration,
		},
		{
			// `classify` is a recognized subcommand token but carries no step of
			// its own (it is absent from subcommandSteps/stepOrder), so the code
			// attributes it to StepUnknown + CategoryUnassigned. Asserted as-is.
			"semicolon bare ts classify",
			`cd "$DIR"; ts classify select --block-id X --classification Y`,
			StepUnknown, CategoryUnassigned,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			step, cat := AttributeCommand(tc.cmd)
			if step != tc.step || cat != tc.cat {
				t.Fatalf("AttributeCommand(%q) = (%q,%q), want (%q,%q)", tc.cmd, step, cat, tc.step, tc.cat)
			}
		})
	}
}

// TestAttributeCommand_PathContainingTimesheetIsNotAStep asserts that a path which
// merely CONTAINS `timesheet` is never a step: its basename is not the binary, or
// the token after it is not a subcommand.
func TestAttributeCommand_PathContainingTimesheetIsNotAStep(t *testing.T) {
	cases := []string{
		"cat /x/.agents/skills/timesheet/workflow.md",
		"ls cmd/timesheet",
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			step, cat := AttributeCommand(cmd)
			if step != StepUnknown || cat != CategoryUnassigned {
				t.Fatalf("AttributeCommand(%q) = (%q,%q), want (unknown,unassigned)", cmd, step, cat)
			}
		})
	}
}

// TestAttributeCommand_NoFalsePositiveWithoutSubcommand asserts that the binary
// token alone, followed by a non-subcommand, stays StepUnknown: a flag is never
// guessed into a step.
func TestAttributeCommand_NoFalsePositiveWithoutSubcommand(t *testing.T) {
	cases := []string{
		`"$T/ts" --help`,
		`"$T/ts" -v`,
		`cd /repo && "$T/ts" --help`,
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			step, cat := AttributeCommand(cmd)
			if step != StepUnknown || cat != CategoryUnassigned {
				t.Fatalf("AttributeCommand(%q) = (%q,%q), want (unknown,unassigned)", cmd, step, cat)
			}
		})
	}
}

// TestAttributeCalls_RetriesAndUnassigned asserts retries on a repeated step and
// that an unassigned call is never folded into a step.
func TestAttributeCalls_RetriesAndUnassigned(t *testing.T) {
	calls := []CallRecord{
		{Step: StepUnknown, Category: CategoryUnassigned, Usage: KnownUsage(1, 0, 0, 0, 0)},
		{Step: StepCollect, Category: CategoryOrchestration, Usage: KnownUsage(10, 1, 0, 0, 0)},
		{Step: StepCollect, Category: CategoryOrchestration, Usage: KnownUsage(20, 2, 0, 0, 0)},
		{Step: StepReview, Category: CategoryOrchestration, Usage: KnownUsage(30, 3, 0, 0, 0)},
	}
	steps := AttributeCalls(calls)

	if len(steps) != 2 {
		t.Fatalf("steps = %d, want 2 (unknown excluded)", len(steps))
	}
	byStep := map[StepID]StepRecord{}
	for _, s := range steps {
		byStep[s.Step] = s
	}
	collect := byStep[StepCollect]
	if collect.Calls != 2 || collect.Retries != 1 {
		t.Fatalf("collect = %+v, want calls=2 retries=1", collect)
	}
	assertKnown(t, "collect input", collect.Usage.Input, 30)
	review := byStep[StepReview]
	if review.Calls != 1 || review.Retries != 0 {
		t.Fatalf("review = %+v, want calls=1 retries=0", review)
	}

	// Attempts are assigned in call order on the mutated calls slice.
	if calls[1].Attempt != 1 || calls[2].Attempt != 2 || calls[3].Attempt != 1 {
		t.Fatalf("attempts = %d,%d,%d want 1,2,1", calls[1].Attempt, calls[2].Attempt, calls[3].Attempt)
	}
	// The unassigned call keeps Attempt 0 and is not in any step.
	if calls[0].Attempt != 0 {
		t.Fatalf("unassigned attempt = %d, want 0", calls[0].Attempt)
	}
}
