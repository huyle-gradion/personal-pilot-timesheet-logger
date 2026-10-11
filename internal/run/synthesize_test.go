package run

import (
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/editorial"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// S8L1-7: a persisted-but-unknown prior total passes UnknownUsage and records a
// usage_unknown warning rather than silently zeroing the budget.
func TestSynthesize_UsageUnknownWarning(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	seedEvidence(t, store, date, []evidence.Evidence{gitEvidence("e1", "acme/repo", "fix parser")})
	if _, err := store.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		workflow.SetUsageReport(rec, workflow.EditorialUsage{Date: date, Editorial: workflow.UnknownMeasurement()})
		return nil
	}); err != nil {
		t.Fatalf("seed unknown usage: %v", err)
	}

	ed := &fakeEditor{usage: editorial.KnownUsage(1, 1, 0)}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Editor: ed}, Config{})
	prop, err := r.Propose(ctx, date)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	res, err := r.Synthesize(ctx, date, prop)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}

	warned := false
	for _, s := range res.Suggestions {
		for _, w := range s.Warnings {
			if w == usageUnknownWarning {
				warned = true
			}
		}
	}
	if !warned {
		t.Fatalf("want a %s warning on the suggestions, got %+v", usageUnknownWarning, res.Suggestions)
	}
	rec, _ := store.GetDay(ctx, date)
	if rec.Usage == nil || rec.Usage.Editorial.Known {
		t.Fatalf("an unknown prior total must stay Unknown (never silently zero), got %+v", rec.Usage)
	}
}

func TestSynthesize_NoAIProducesDeterministicText(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	seedEvidence(t, store, date, []evidence.Evidence{gitEvidence("e1", "acme/repo", "fix parser")})

	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry()}, Config{})
	prop, err := r.Propose(ctx, date)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(prop.Work)+len(prop.Meetings)+len(prop.Manual) == 0 {
		t.Fatalf("want a proposal block, got %+v", prop)
	}

	res, err := r.Synthesize(ctx, date, prop)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if res.Report.Invoked {
		t.Errorf("no editor means Invoked must be false")
	}
	if len(res.Suggestions) == 0 {
		t.Fatalf("want deterministic suggestions")
	}
	for _, s := range res.Suggestions {
		if s.Origin != editorial.OriginDeterministic {
			t.Errorf("want deterministic origin, got %s", s.Origin)
		}
	}
	rec, _ := store.GetDay(ctx, date)
	if len(rec.Editorial) != 0 || rec.Usage != nil {
		t.Errorf("the no-AI path must be a no-op on the record, got editorial=%d usage=%+v", len(rec.Editorial), rec.Usage)
	}
}

func TestSynthesize_AccumulatesUsageAcrossRuns(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	seedEvidence(t, store, date, []evidence.Evidence{gitEvidence("e1", "acme/repo", "fix parser")})

	ed := &fakeEditor{usage: editorial.KnownUsage(10, 20, 0)}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Editor: ed}, Config{})

	prop1, err := r.Propose(ctx, date)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if _, err := r.Synthesize(ctx, date, prop1); err != nil {
		t.Fatalf("Synthesize run 1: %v", err)
	}

	// A second topic: the first target is reused from cache, the new one invokes
	// the editor, so the run still spends tokens and adds a run.
	seedEvidence(t, store, date, []evidence.Evidence{gitEvidence("e2", "acme/other", "tidy up")})
	prop2, err := r.Propose(ctx, date)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if _, err := r.Synthesize(ctx, date, prop2); err != nil {
		t.Fatalf("Synthesize run 2: %v", err)
	}

	rec, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if rec.Usage == nil {
		t.Fatalf("want a persisted usage report")
	}
	if rec.Usage.Editorial.Value != 60 {
		t.Errorf("want 60 accumulated editorial tokens, got %d", rec.Usage.Editorial.Value)
	}
	if rec.Usage.EditorialRuns != 2 {
		t.Errorf("want 2 accumulated runs, got %d", rec.Usage.EditorialRuns)
	}
	if rec.Usage.Orchestration.Known {
		t.Errorf("orchestration must be reported separately, never fabricated or summed with editorial")
	}
	if !rec.Usage.Invoked {
		t.Errorf("want Invoked true")
	}
}

// S7-F4: a rerun accumulates the editorial total rather than replacing it.
func TestSynthesize_AccumulatesEditorialTotal(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	seedEvidence(t, store, date, []evidence.Evidence{gitEvidence("e1", "acme/repo", "fix parser")})
	ed := &fakeEditor{usage: editorial.KnownUsage(5, 5, 0)}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Editor: ed}, Config{})

	// Two runs with different evidence so each invokes the editor.
	for i, repo := range []string{"acme/repo", "acme/other"} {
		if i > 0 {
			seedEvidence(t, store, date, []evidence.Evidence{gitEvidence("e2", repo, "tidy up")})
		}
		prop, err := r.Propose(ctx, date)
		if err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if _, err := r.Synthesize(ctx, date, prop); err != nil {
			t.Fatalf("Synthesize: %v", err)
		}
	}
	rec, _ := store.GetDay(ctx, date)
	if rec.Usage.Editorial.Value != 20 { // 10 per run x 2 runs
		t.Fatalf("want 20 accumulated tokens, got %d", rec.Usage.Editorial.Value)
	}
}

// METRICS-1: the editorial COMPONENT fields accumulate additively so the metrics
// layer can report the §3.5 input-to-step optimization metric without
// re-deriving it from the total.
func TestSynthesize_AccumulatesEditorialComponents(t *testing.T) {
	store := newStore(t)
	date := "2026-10-06"
	seedEvidence(t, store, date, []evidence.Evidence{gitEvidence("e1", "acme/repo", "fix parser")})
	ed := &fakeEditor{usage: editorial.KnownUsage(10, 20, 3)}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Editor: ed}, Config{})

	for i, repo := range []string{"acme/repo", "acme/other"} {
		if i > 0 {
			seedEvidence(t, store, date, []evidence.Evidence{gitEvidence("e2", repo, "tidy up")})
		}
		prop, err := r.Propose(ctx, date)
		if err != nil {
			t.Fatalf("Propose: %v", err)
		}
		if _, err := r.Synthesize(ctx, date, prop); err != nil {
			t.Fatalf("Synthesize: %v", err)
		}
	}
	rec, err := store.GetDay(ctx, date)
	if err != nil {
		t.Fatalf("GetDay: %v", err)
	}
	if !rec.Usage.EditorialInput.Known || rec.Usage.EditorialInput.Value != 20 {
		t.Fatalf("editorial_input = %+v, want known 20", rec.Usage.EditorialInput)
	}
	if !rec.Usage.EditorialOutput.Known || rec.Usage.EditorialOutput.Value != 40 {
		t.Fatalf("editorial_output = %+v, want known 40", rec.Usage.EditorialOutput)
	}
	if !rec.Usage.EditorialCache.Known || rec.Usage.EditorialCache.Value != 6 {
		t.Fatalf("editorial_cache = %+v, want known 6", rec.Usage.EditorialCache)
	}
}
