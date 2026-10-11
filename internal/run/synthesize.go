package run

import (
	"context"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/editorial"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// usageUnknownWarning is recorded when the prior editorial total is not known, so
// the budget is enforced from a known zero rather than silently zeroing the total.
const usageUnknownWarning = "usage_unknown"

// Synthesize produces editorial suggestions for the proposal.
//
// With no editor it returns deterministic text and persists NOTHING (a no-op on
// the record). With an editor it persists the suggestions and an ACCUMULATED usage
// report (editorial total += new, runs += new), keeping orchestration SEPARATE.
// The budget's Spent is reconstructed from the persisted editorial total; an
// unknown prior total passes UnknownUsage and records a usage_unknown warning.
func (r *Runner) Synthesize(ctx context.Context, date string, prop proposal.Proposal) (editorial.Result, error) {
	rec, err := r.deps.Store.GetDay(ctx, date)
	if err != nil {
		return editorial.Result{}, err
	}
	req, err := editorial.BuildRequest(prop, rec.CollectedEvidence, editorial.BuildOptions{})
	if err != nil {
		return editorial.Result{}, err
	}
	cache := editorialCache(rec)

	if r.deps.Editor == nil {
		// No-AI: deterministic text only, and the record is left untouched.
		return editorial.Synthesize(ctx, nil, req, cache, editorial.Options{Now: r.now()})
	}

	spent, warnings := reconstructSpent(rec)
	res, err := editorial.Synthesize(ctx, r.deps.Editor, req, cache, editorial.Options{
		Now:         r.now(),
		Target:      editorial.SoftDailyTokenTarget,
		Spent:       spent,
		MaxAttempts: editorial.DefaultMaxAttempts,
	})
	if err != nil {
		return editorial.Result{}, err
	}
	if len(warnings) > 0 {
		for i := range res.Suggestions {
			res.Suggestions[i].Warnings = appendUnique(res.Suggestions[i].Warnings, warnings...)
		}
	}

	if _, err := r.deps.Store.UpdateDay(ctx, date, func(rec *workflow.Record) error {
		for _, s := range res.Suggestions {
			workflow.UpsertEditorialSuggestion(rec, workflow.EditorialSuggestion{
				TargetID:    s.TargetID,
				RequestHash: s.RequestHash,
				Text:        workflow.EditorialText{Title: s.Text.Title, Description: s.Text.Description},
				Origin:      string(s.Origin),
				Warnings:    s.Warnings,
			})
		}
		workflow.SetUsageReport(rec, accumulateUsage(rec, res))
		return nil
	}); err != nil {
		return res, err
	}
	return res, nil
}

// reconstructSpent rebuilds the editorial budget input from the persisted total. A
// fresh day (no report) is a KNOWN zero; a persisted-but-unknown total passes
// UnknownUsage with a usage_unknown warning.
func reconstructSpent(rec *workflow.Record) (editorial.Usage, []string) {
	if rec.Usage == nil {
		return editorial.KnownUsage(0, 0, 0), nil
	}
	if rec.Usage.Editorial.Known {
		return editorial.KnownUsage(rec.Usage.Editorial.Value, 0, 0), nil
	}
	return editorial.UnknownUsage(), []string{usageUnknownWarning}
}

// accumulateUsage folds the new result into the persisted report: editorial total
// and runs accumulate; orchestration ACCUMULATES TOO but stays SEPARATE and is
// never summed with editorial. The actual orchestration measurement is supplied by
// layer 2/S9; the core only folds in a Known value when one is provided.
func accumulateUsage(rec *workflow.Record, res editorial.Result) workflow.EditorialUsage {
	var prior workflow.EditorialUsage
	priorEditorial := workflow.KnownMeasurement(0)
	if rec.Usage != nil {
		prior = *rec.Usage
		priorEditorial = prior.Editorial
	}
	out := prior
	out.Date = res.Date
	newTotal := res.Report.Editorial.Total()
	if priorEditorial.Known && newTotal.Known {
		out.Editorial = workflow.KnownMeasurement(priorEditorial.Value + newTotal.Value)
	} else {
		out.Editorial = workflow.UnknownMeasurement()
	}
	// METRICS-1: accumulate the editorial COMPONENT usage additively. A fresh day
	// (no prior report) starts from a KNOWN zero for each component, mirroring the
	// total's prior handling, so the first run's components are recorded rather
	// than lost to an absent prior. A persisted-but-unknown prior component stays
	// UNKNOWN (never silently zeroed).
	priorInput, priorOutput, priorCache := workflow.KnownMeasurement(0), workflow.KnownMeasurement(0), workflow.KnownMeasurement(0)
	if rec.Usage != nil {
		priorInput, priorOutput, priorCache = prior.EditorialInput, prior.EditorialOutput, prior.EditorialCache
	}
	out.EditorialInput = addComponent(priorInput, res.Report.Editorial.Input)
	out.EditorialOutput = addComponent(priorOutput, res.Report.Editorial.Output)
	out.EditorialCache = addComponent(priorCache, res.Report.Editorial.Cache)
	out.EditorialRuns = prior.EditorialRuns + res.Report.EditorialRuns
	out.Invoked = prior.Invoked || res.Report.Invoked
	out.RuntimeMS = res.Report.RuntimeMS
	out.PeakRSSMB = res.Report.PeakRSSMB
	// Orchestration accumulates additively and separately from editorial.
	if orch := res.Report.Orchestration.Total(); orch.Known {
		if prior.Orchestration.Known {
			out.Orchestration = workflow.KnownMeasurement(prior.Orchestration.Value + orch.Value)
		} else {
			out.Orchestration = workflow.KnownMeasurement(orch.Value)
		}
	} else if !prior.Orchestration.Known {
		out.Orchestration = workflow.UnknownMeasurement()
	}
	return out
}

// suggestionsFromRecord returns the persisted editorial suggestions in the
// editorial package's type, so they can be folded into a proposal or reused as a
// synthesis cache.
func suggestionsFromRecord(rec *workflow.Record) []editorial.Suggestion {
	if rec == nil || len(rec.Editorial) == 0 {
		return nil
	}
	out := make([]editorial.Suggestion, 0, len(rec.Editorial))
	for _, s := range rec.Editorial {
		out = append(out, editorial.Suggestion{
			TargetID:    s.TargetID,
			RequestHash: s.RequestHash,
			Text:        editorial.Text{Title: s.Text.Title, Description: s.Text.Description},
			Origin:      editorial.Origin(s.Origin),
			Warnings:    append([]string(nil), s.Warnings...),
		})
	}
	return out
}

func editorialCache(rec *workflow.Record) map[string]editorial.Suggestion {
	suggestions := suggestionsFromRecord(rec)
	if len(suggestions) == 0 {
		return nil
	}
	cache := make(map[string]editorial.Suggestion, len(suggestions))
	for _, s := range suggestions {
		cache[s.TargetID] = s
	}
	return cache
}

// addComponent accumulates a workflow.Measurement componentwise. It is Known only
// when both operands are Known; a mixed/unknown component stays Unknown (never
// zero). It is the METRICS-1 editorial-component accumulator.
func addComponent(a, b workflow.Measurement) workflow.Measurement {
	if !a.Known || !b.Known {
		return workflow.UnknownMeasurement()
	}
	return workflow.KnownMeasurement(a.Value + b.Value)
}

func appendUnique(dst []string, values ...string) []string {
	seen := make(map[string]bool, len(dst)+len(values))
	for _, d := range dst {
		seen[d] = true
	}
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		dst = append(dst, v)
	}
	return dst
}
