package metrics

import (
	"testing"
	"time"
)

func TestComputeTranscriptDurations_Boundaries(t *testing.T) {
	base := time.UnixMilli(1791695713126)

	t.Run("empty is unknown", func(t *testing.T) {
		e, a, h := ComputeTranscriptDurations(nil)
		if e.Value.Known || a.Value.Known || h.Value.Known {
			t.Fatalf("empty obs should be UNKNOWN: %+v %+v %+v", e, a, h)
		}
		if e.Source != DurationUnknown {
			t.Fatalf("source = %q, want unknown", e.Source)
		}
	})

	t.Run("equal timestamps known zero", func(t *testing.T) {
		obs := []Observation{{Timestamp: base, IsTurn: true}, {Timestamp: base, IsTurn: false}}
		e, a, h := ComputeTranscriptDurations(obs)
		assertKnown(t, "endToEnd", e.Value, 0)
		assertKnown(t, "active", a.Value, 0)
		assertKnown(t, "humanWait", h.Value, 0)
	})

	t.Run("single turn known zero", func(t *testing.T) {
		e, a, h := ComputeTranscriptDurations([]Observation{{Timestamp: base, IsTurn: true}})
		assertKnown(t, "endToEnd", e.Value, 0)
		assertKnown(t, "active", a.Value, 0)
		assertKnown(t, "humanWait", h.Value, 0)
	})

	t.Run("turn to next observation", func(t *testing.T) {
		obs := []Observation{
			{Timestamp: base, IsTurn: true},
			{Timestamp: base.Add(10 * time.Millisecond)},
			{Timestamp: base.Add(100 * time.Millisecond), IsTurn: true},
			{Timestamp: base.Add(110 * time.Millisecond)},
		}
		e, a, h := ComputeTranscriptDurations(obs)
		assertKnown(t, "endToEnd", e.Value, 110)
		assertKnown(t, "active", a.Value, 20)
		assertKnown(t, "humanWait", h.Value, 90)
		if e.Source != DurationTranscript || a.Source != DurationTranscript {
			t.Fatalf("sources = %q/%q, want transcript", e.Source, a.Source)
		}
	})

	t.Run("missing timestamps unknown", func(t *testing.T) {
		e, a, h := ComputeTranscriptDurations([]Observation{{IsTurn: true}})
		if e.Value.Known || a.Value.Known || h.Value.Known {
			t.Fatalf("zero-timestamp obs should be UNKNOWN")
		}
	})
}

func TestMonotonicSince_Label(t *testing.T) {
	d := MonotonicSince(time.Now().Add(-5 * time.Millisecond))
	if d.Source != DurationMonotonic {
		t.Fatalf("source = %q, want monotonic", d.Source)
	}
	if !d.Value.Known {
		t.Fatalf("monotonic duration unknown")
	}
}

func TestDeriveState_Priority(t *testing.T) {
	three, one := 3, 1
	cases := []struct {
		name string
		in   StateInput
		want RunState
	}{
		{"exit blocked", StateInput{ExitCode: &three, Steps: []StepRecord{{Step: StepSubmit}}}, RunBlocked},
		{"exit failed", StateInput{ExitCode: &one, Steps: []StepRecord{{Step: StepSubmit}}}, RunFailed},
		{"submit completed", StateInput{Steps: []StepRecord{{Step: StepSubmit}}, ClosingTurn: true}, RunCompleted},
		{"export completed", StateInput{Steps: []StepRecord{{Step: StepExport}}, ClosingTurn: true}, RunCompleted},
		{"review partial", StateInput{Steps: []StepRecord{{Step: StepReview}}, ClosingTurn: true}, RunPartial},
		{"collect partial", StateInput{Steps: []StepRecord{{Step: StepCollect}}, ClosingTurn: true}, RunPartial},
		{"preview partial", StateInput{Steps: []StepRecord{{Step: StepPreview}}, ClosingTurn: true}, RunPartial},
		{"preflight only unknown", StateInput{Steps: []StepRecord{{Step: StepPreflight}}, ClosingTurn: true}, RunUnknown},
		{"no closing turn cancelled", StateInput{Steps: []StepRecord{{Step: StepPreflight}}, ClosingTurn: false}, RunCancelled},
		{"explicit cancel", StateInput{Cancelled: true, ClosingTurn: true}, RunCancelled},
		{"nothing unknown", StateInput{ClosingTurn: true}, RunUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DeriveState(tc.in); got != tc.want {
				t.Fatalf("DeriveState = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDeriveState_ReviewStoppedNeverCompleted is the explicit guard: a run whose
// highest step is review (or earlier) is NEVER labelled completed.
func TestDeriveState_ReviewStoppedNeverCompleted(t *testing.T) {
	for _, step := range []StepID{StepPreflight, StepCollect, StepEditorial, StepReview, StepPreview, StepApprove} {
		got := DeriveState(StateInput{Steps: []StepRecord{{Step: step}}, ClosingTurn: true})
		if got == RunCompleted {
			t.Fatalf("step %q derived completed; review-stopped must never be completed", step)
		}
	}
}

// TestDeriveState_ExitCodes (F5) asserts the live exit-code mapping.
func TestDeriveState_ExitCodes(t *testing.T) {
	cases := []struct {
		code int
		want RunState
	}{
		{0, RunCompleted},
		{3, RunBlocked},
		{1, RunFailed},
		{2, RunFailed},
		{127, RunFailed},
	}
	for _, tc := range cases {
		c := tc.code
		if got := DeriveState(StateInput{ExitCode: &c}); got != tc.want {
			t.Fatalf("exit %d -> %q, want %q", tc.code, got, tc.want)
		}
	}
}

// TestNewLiveRun_MonotonicAndState (F5) asserts a live run records a monotonic
// duration and the exit-code state, with HarnessStart UNKNOWN.
func TestNewLiveRun_MonotonicAndState(t *testing.T) {
	start := time.Now().Add(-20 * time.Millisecond)
	rec := NewLiveRun(start, 0, LiveRunOptions{Date: "2026-10-11"})
	if rec.EndToEnd.Source != DurationMonotonic {
		t.Fatalf("end_to_end source = %q, want monotonic", rec.EndToEnd.Source)
	}
	if !rec.EndToEnd.Value.Known || rec.EndToEnd.Value.Value < 1 {
		t.Fatalf("end_to_end = %+v, want a positive monotonic value", rec.EndToEnd)
	}
	if rec.State != RunCompleted {
		t.Fatalf("state = %q, want completed", rec.State)
	}
	if rec.HarnessStart.Value.Known {
		t.Fatalf("harness start = %+v, want UNKNOWN", rec.HarnessStart)
	}
	if rec.Provenance != ProvenanceObserved {
		t.Fatalf("provenance = %q, want observed", rec.Provenance)
	}
	if rec.Date != "2026-10-11" {
		t.Fatalf("date = %q", rec.Date)
	}
	if got := NewLiveRun(start, 3, LiveRunOptions{}).State; got != RunBlocked {
		t.Fatalf("exit 3 -> %q, want blocked", got)
	}
	if got := NewLiveRun(start, 1, LiveRunOptions{}).State; got != RunFailed {
		t.Fatalf("exit 1 -> %q, want failed", got)
	}
}
