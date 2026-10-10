package editorial

import (
	"context"
	"errors"
	"testing"
	"time"
)

const testDate = "2026-10-06"

var testNow = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

func noRSS() float64 { return 0 }

// fakeEditor is a hermetic Editor double.
type fakeEditor struct {
	calls int
	usage Usage
	fn    func(req Request) (Response, error)
}

func (f *fakeEditor) Synthesize(ctx context.Context, req Request) (Response, Usage, error) {
	f.calls++
	if f.fn != nil {
		resp, err := f.fn(req)
		return resp, f.usage, err
	}
	m := make(map[string]Text, len(req.Targets))
	for _, t := range req.Targets {
		m[t.ID] = Text{Title: "AI title", Description: "AI description"}
	}
	return Response{Suggestions: m}, f.usage, nil
}

func testRequest() Request {
	return Request{Date: testDate, Targets: []Target{{
		ID: "c1", Kind: "work", Date: testDate,
		Repository: "acme/repo", Branch: "feat/x", Project: "Proj",
		Facts: []Fact{{Kind: FactCommitSubject, Text: "fix parser"}},
	}}}
}

func opts() Options {
	return Options{Now: testNow, MeasureRSS: noRSS}
}

func TestSynthesize_NilEditorIsDeterministic(t *testing.T) {
	res, err := Synthesize(context.Background(), nil, testRequest(), nil, opts())
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(res.Suggestions) != 1 {
		t.Fatalf("want 1 suggestion, got %d", len(res.Suggestions))
	}
	if res.Suggestions[0].Origin != OriginDeterministic {
		t.Errorf("want deterministic, got %s", res.Suggestions[0].Origin)
	}
	if res.Report.Invoked {
		t.Errorf("Invoked must be false with no editor")
	}
	if res.Report.EditorialRuns != 0 {
		t.Errorf("want 0 runs, got %d", res.Report.EditorialRuns)
	}
	if !res.Report.Editorial.Total().Known || res.Report.Editorial.Total().Value != 0 {
		t.Errorf("want a Known zero editorial usage, got %+v", res.Report.Editorial)
	}
}

func TestSynthesize_EditorialSuccess(t *testing.T) {
	ed := &fakeEditor{usage: KnownUsage(10, 20, 5)}
	res, err := Synthesize(context.Background(), ed, testRequest(), nil, opts())
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if ed.calls != 1 {
		t.Errorf("want 1 call, got %d", ed.calls)
	}
	if res.Suggestions[0].Origin != OriginEditorial {
		t.Errorf("want editorial, got %s", res.Suggestions[0].Origin)
	}
	if res.Report.Editorial.Total().Value != 35 {
		t.Errorf("want 35 tokens, got %d", res.Report.Editorial.Total().Value)
	}
	if res.Report.EditorialRuns != 1 {
		t.Errorf("want 1 run, got %d", res.Report.EditorialRuns)
	}
	if !res.Report.Invoked {
		t.Errorf("Invoked must be true")
	}
}

func TestSynthesize_RetryAccounting(t *testing.T) {
	// The editor returns an invalid (empty) response every time, so both attempts
	// run and their usage is summed.
	ed := &fakeEditor{usage: KnownUsage(10, 20, 0), fn: func(Request) (Response, error) {
		return Response{Suggestions: map[string]Text{"c1": {Title: " ", Description: ""}}}, nil
	}}
	res, err := Synthesize(context.Background(), ed, testRequest(), nil, opts())
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if ed.calls != 2 {
		t.Fatalf("want 2 attempts, got %d", ed.calls)
	}
	if res.Report.EditorialRuns != 2 {
		t.Errorf("want Runs==2, got %d", res.Report.EditorialRuns)
	}
	if res.Report.Editorial.Total().Value != 60 {
		t.Errorf("want summed usage 60, got %d", res.Report.Editorial.Total().Value)
	}
	if res.Suggestions[0].Origin != OriginDeterministic {
		t.Errorf("want a deterministic fallback, got %s", res.Suggestions[0].Origin)
	}
}

func TestSynthesize_PersistentErrorFallsBackDeterministic(t *testing.T) {
	ed := &fakeEditor{fn: func(Request) (Response, error) { return Response{}, errors.New("boom") }}
	res, err := Synthesize(context.Background(), ed, testRequest(), nil, opts())
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(res.Suggestions) != 1 {
		t.Fatalf("a fallback must still yield one suggestion per target")
	}
	if res.Suggestions[0].Origin != OriginDeterministic {
		t.Errorf("want deterministic, got %s", res.Suggestions[0].Origin)
	}
}

func TestSynthesize_ReuseOnUnchangedRequest(t *testing.T) {
	req := testRequest()
	cache := map[string]Suggestion{"c1": {
		TargetID: "c1", RequestHash: TargetHash(req.Targets[0]),
		Text: Text{Title: "cached", Description: "cached"}, Origin: OriginEditorial,
	}}
	ed := &fakeEditor{usage: KnownUsage(10, 20, 0)}
	res, err := Synthesize(context.Background(), ed, req, cache, opts())
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if ed.calls != 0 {
		t.Fatalf("a cache hit must not call the editor, got %d calls", ed.calls)
	}
	if res.Report.Editorial.Total().Value != 0 {
		t.Errorf("a cache hit must spend zero tokens, got %d", res.Report.Editorial.Total().Value)
	}
	if res.Suggestions[0].Origin != OriginCache || res.Suggestions[0].Text.Title != "cached" {
		t.Errorf("want the cached suggestion, got %+v", res.Suggestions[0])
	}
}

func TestSynthesize_ReinvokesOnChangedFacts(t *testing.T) {
	req := testRequest()
	cache := map[string]Suggestion{"c1": {TargetID: "c1", RequestHash: "stale", Text: Text{Title: "old", Description: "old"}}}
	ed := &fakeEditor{usage: KnownUsage(1, 1, 0)}
	res, err := Synthesize(context.Background(), ed, req, cache, opts())
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if ed.calls != 1 {
		t.Fatalf("changed facts must reinvoke the editor, got %d calls", ed.calls)
	}
	if res.Suggestions[0].Origin != OriginEditorial {
		t.Errorf("want editorial, got %s", res.Suggestions[0].Origin)
	}
}

func TestSynthesize_ReinvokesOnNewTargetID(t *testing.T) {
	req := testRequest()
	cache := map[string]Suggestion{"c1": {TargetID: "c1", RequestHash: TargetHash(req.Targets[0]), Text: Text{Title: "old", Description: "old"}}}
	req.Targets[0].ID = "c2" // a new target id has no cache entry
	ed := &fakeEditor{usage: KnownUsage(1, 1, 0)}
	if _, err := Synthesize(context.Background(), ed, req, cache, opts()); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if ed.calls != 1 {
		t.Fatalf("a new target id must reinvoke the editor, got %d calls", ed.calls)
	}
}

// S7-F1: the omitted-Spent (fresh-day) path must enforce the soft target.
func TestSynthesize_DefaultSpentEnforcesSoftTarget(t *testing.T) {
	req := Request{Date: testDate, Targets: []Target{{ID: "a", Kind: "work"}, {ID: "b", Kind: "work"}}}
	ed := &fakeEditor{usage: KnownUsage(1000, 0, 0)}
	res, err := Synthesize(context.Background(), ed, req, nil, Options{Now: testNow, MaxAttempts: 1, MeasureRSS: noRSS})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if ed.calls != 1 {
		t.Fatalf("the omitted-Spent path must enforce the soft target: want 1 call, got %d", ed.calls)
	}
	if res.Suggestions[0].Origin != OriginEditorial {
		t.Errorf("the first target must use the editor, got %s", res.Suggestions[0].Origin)
	}
	if res.Suggestions[1].Origin != OriginDeterministic {
		t.Errorf("the second target must fall back once the target is exhausted, got %s", res.Suggestions[1].Origin)
	}
	if res.Report.Editorial.Total().Value != 1000 {
		t.Errorf("want 1000 editorial tokens, got %d", res.Report.Editorial.Total().Value)
	}
}

// S7-F1: an unknown/omitted Spent normalizes to a KNOWN zero (fresh day), so the
// budget is enforced from zero rather than treated as unlimited.
func TestSynthesize_UnknownSpentDefaultsToKnownZero(t *testing.T) {
	normalized := normalizeSpent(UnknownUsage())
	if !normalized.Total().Known || normalized.Total().Value != 0 {
		t.Fatalf("an unknown spent must normalize to a known zero, got %+v", normalized)
	}
	if got := EvaluateBudget(normalized.Total(), SoftDailyTokenTarget); got != BudgetKnownAvailable {
		t.Fatalf("want the budget enforced from zero, got %s", got)
	}
}

// S7-F6: TargetHash excludes ID/EvidenceIDs, so a changed evidence SET with
// identical facts reuses cached text; a changed FACT reinvokes.
func TestSynthesize_ReusesOnChangedEvidenceSetWithIdenticalFacts(t *testing.T) {
	a := Target{
		ID: "c1", Kind: "work", Date: testDate, Repository: "r", Project: "P",
		EvidenceIDs: []string{"e1"}, Facts: []Fact{{Kind: FactCommitSubject, Text: "fix parser"}},
	}
	b := a
	b.EvidenceIDs = []string{"e1", "e2"} // a changed evidence SET, identical facts
	if TargetHash(a) != TargetHash(b) {
		t.Fatalf("a changed evidence set with identical facts must hash the same (reuse is safe)")
	}

	cache := map[string]Suggestion{"c1": {
		TargetID: "c1", RequestHash: TargetHash(a),
		Text: Text{Title: "cached", Description: "cached"},
	}}
	ed := &fakeEditor{usage: KnownUsage(10, 0, 0)}
	res, err := Synthesize(context.Background(), ed, Request{Date: testDate, Targets: []Target{b}}, cache, opts())
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if ed.calls != 0 {
		t.Fatalf("a changed evidence set with identical facts must reuse cached text, got %d calls", ed.calls)
	}
	if res.Suggestions[0].Origin != OriginCache || res.Suggestions[0].Text.Title != "cached" {
		t.Fatalf("want the cached suggestion, got %+v", res.Suggestions[0])
	}

	// A changed FACT reinvokes.
	b.Facts = []Fact{{Kind: FactCommitSubject, Text: "changed fact"}}
	if _, err := Synthesize(context.Background(), ed, Request{Date: testDate, Targets: []Target{b}}, cache, opts()); err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if ed.calls != 1 {
		t.Fatalf("a changed fact must reinvoke the editor, got %d calls", ed.calls)
	}
}

func TestSynthesize_BudgetExhaustedSkipsAgent(t *testing.T) {
	ed := &fakeEditor{usage: KnownUsage(10, 20, 0)}
	res, err := Synthesize(context.Background(), ed, testRequest(), nil, Options{
		Now: testNow, Spent: KnownUsage(1000, 0, 0), MeasureRSS: noRSS,
	})
	if err != nil {
		t.Fatalf("budget exhaustion must never block review, got %v", err)
	}
	if ed.calls != 0 {
		t.Fatalf("an exhausted budget must skip the agent, got %d calls", ed.calls)
	}
	if len(res.Suggestions) != 1 || res.Suggestions[0].Origin != OriginDeterministic {
		t.Fatalf("budget exhaustion must fall back to deterministic, got %+v", res.Suggestions)
	}
}

func TestSynthesize_UnknownUsageNotTreatedAsZero(t *testing.T) {
	ed := &fakeEditor{usage: UnknownUsage()}
	res, err := Synthesize(context.Background(), ed, testRequest(), nil, Options{
		Now: testNow, Spent: UnknownUsage(), MeasureRSS: noRSS,
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if ed.calls != 1 {
		t.Fatalf("an unknown budget must not skip the agent, got %d calls", ed.calls)
	}
	if res.Report.Editorial.Total().Known {
		t.Errorf("an unknown measurement must never be recorded as a known (zero) value")
	}
	if EvaluateBudget(UnknownUsage().Total(), SoftDailyTokenTarget) != BudgetUnknown {
		t.Errorf("unknown spent must classify as BudgetUnknown")
	}
}

func TestSynthesize_EditorialAndOrchestrationSeparate(t *testing.T) {
	ed := &fakeEditor{usage: KnownUsage(10, 20, 0)}
	res, err := Synthesize(context.Background(), ed, testRequest(), nil, Options{
		Now: testNow, Orchestration: KnownUsage(100, 200, 50), MeasureRSS: noRSS,
	})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if res.Report.Editorial.Total().Value != 30 {
		t.Errorf("want editorial 30, got %d", res.Report.Editorial.Total().Value)
	}
	if res.Report.Orchestration.Total().Value != 350 {
		t.Errorf("want orchestration 350, got %d", res.Report.Orchestration.Total().Value)
	}
}

func TestSynthesize_UnsupportedImpactFlaggedNotConfirmed(t *testing.T) {
	ed := &fakeEditor{usage: KnownUsage(1, 1, 0), fn: func(Request) (Response, error) {
		return Response{Suggestions: map[string]Text{"c1": {Title: "Shipped it", Description: "improved by 50%"}}}, nil
	}}
	res, err := Synthesize(context.Background(), ed, testRequest(), nil, opts())
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	s := res.Suggestions[0]
	if s.Origin != OriginEditorial {
		t.Fatalf("want editorial, got %s", s.Origin)
	}
	if len(s.Warnings) == 0 {
		t.Fatalf("want unsupported-impact warnings, got none")
	}
	// The text is still surfaced for human review; it is never auto-confirmed or
	// blocked.
	if s.Text.Description == "" || s.Text.Title == "" {
		t.Errorf("the flagged text must still be surfaced, got %+v", s.Text)
	}
}

func TestSynthesize_AlwaysOneSuggestionPerTarget(t *testing.T) {
	req := Request{Date: testDate, Targets: []Target{{ID: "a", Kind: "work"}, {ID: "b", Kind: "meeting"}}}
	ed := &fakeEditor{fn: func(Request) (Response, error) { return Response{}, errors.New("boom") }}
	res, err := Synthesize(context.Background(), ed, req, nil, opts())
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(res.Suggestions) != 2 {
		t.Fatalf("want one suggestion per target, got %d", len(res.Suggestions))
	}
	for _, s := range res.Suggestions {
		if s.Origin != OriginDeterministic {
			t.Errorf("want a deterministic fallback, got %s", s.Origin)
		}
	}
}

func TestTargetHash_StableAndExcludesID(t *testing.T) {
	a := Target{ID: "c1", Kind: "work", Date: testDate, Repository: "r", Facts: []Fact{{Kind: FactCommitSubject, Text: "x"}}}
	b := a
	b.ID = "c2"
	if TargetHash(a) != TargetHash(b) {
		t.Errorf("TargetHash must exclude the target id")
	}
	b.Facts = []Fact{{Kind: FactCommitSubject, Text: "y"}}
	if TargetHash(a) == TargetHash(b) {
		t.Errorf("TargetHash must change with the facts")
	}

	c := a
	c.Facts = []Fact{{Kind: FactPRTitle, Text: "p"}, {Kind: FactCommitSubject, Text: "x"}}
	d := a
	d.Facts = []Fact{{Kind: FactCommitSubject, Text: "x"}, {Kind: FactPRTitle, Text: "p"}}
	if TargetHash(c) != TargetHash(d) {
		t.Errorf("fact order must not change TargetHash")
	}
}
