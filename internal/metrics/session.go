package metrics

import (
	"sort"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// tokensPerMillion is the catalog's rate unit: USD per million tokens.
const tokensPerMillion float64 = 1_000_000

// CacheHitPercent returns cacheRead / (input + cacheRead + cacheWrite) * 100. It
// returns nil (UNKNOWN) when the denominator is zero OR any of those three
// components is unknown; a percentage is never fabricated. Output and reasoning do
// not participate. A known 0% (cacheRead == 0 with a non-zero denominator) is a
// non-nil pointer to 0.
func CacheHitPercent(u Usage) *float64 {
	for _, m := range []workflow.Measurement{u.Input, u.CacheRead, u.CacheWrite} {
		if !m.Known {
			return nil
		}
	}
	denom := u.Input.Value + u.CacheRead.Value + u.CacheWrite.Value
	if denom <= 0 {
		return nil
	}
	pct := float64(u.CacheRead.Value) / float64(denom) * 100
	return &pct
}

// EstimateCost returns the USD cost of a usage at a price, or nil (UNKNOWN) when
// ANY contributing token count or rate is unknown. Reasoning is EXCLUDED: it is
// not a consumed, billed component in this package. A fully-known zero usage with
// a fully-known price yields a non-nil pointer to 0.
func EstimateCost(u Usage, price TokenPrice) *float64 {
	type term struct {
		m workflow.Measurement
		r *float64
	}
	var total float64
	for _, t := range []term{
		{u.Input, price.Input},
		{u.Output, price.Output},
		{u.CacheRead, price.CacheRead},
		{u.CacheWrite, price.CacheWrite},
	} {
		if !t.m.Known || t.r == nil {
			return nil
		}
		total += float64(t.m.Value) * *t.r
	}
	cost := total / tokensPerMillion
	return &cost
}

// BuildSession assembles a SessionRecord from an ALREADY-PARSED RunRecord. It is
// PURE: it resolves the per-(provider, model) usage breakdown, looks each price up
// in cat (a nil cat leaves every price unknown), and derives the local-date
// association in loc (a nil loc defaults to time.Local).
//
// It decodes NO tool-argument value. The association uses only the calls'
// timestamps and their already-derived StepID (workflow steps only), so it is
// ADR-0006 compliant: the value of a `--date` argument is never read or persisted.
func BuildSession(run RunRecord, cat *ModelCatalog, loc *time.Location) SessionRecord {
	if loc == nil {
		loc = time.Local
	}
	rec := SessionRecord{
		SchemaVersion: SchemaVersion,
		SessionID:     firstNonEmpty(run.SessionID, run.RunID),
		Provider:      run.Provider,
		ContentHash:   run.ContentHash,
		StartedAt:     run.StartedAt,
		EndedAt:       run.EndedAt,
		Usage:         run.Usage,
		TotalTokens:   run.Usage.ConsumedTokens(),
		Models:        []SessionModel{},
		Provenance:    run.Provenance,
	}
	rec.Models = buildSessionModels(run.Calls, cat)
	rec.Model = singleModel(rec.Models)
	rec.Price, rec.PriceSource = sessionPrice(rec.Models)
	rec.Cost = sessionCost(rec.Models)
	rec.CacheHitPct = CacheHitPercent(run.Usage)
	rec.LocalDates, rec.LoggedDate, rec.Association = associate(run.Calls, loc)
	return rec
}

// buildSessionModels groups the calls by (provider, model) and resolves each
// group's price and cost. The result is sorted by (provider, model) so it is
// deterministic across re-imports. A model absent from the catalog keeps an
// all-nil Price, a nil Cost, and PriceSourceUnknown.
func buildSessionModels(calls []CallRecord, cat *ModelCatalog) []SessionModel {
	type acc struct {
		provider string
		model    string
		usage    Usage
	}
	order := make([]string, 0)
	byKey := make(map[string]*acc)
	for i := range calls {
		c := calls[i]
		provider := strings.TrimSpace(c.Provider)
		model := strings.TrimSpace(c.Model)
		key := provider + "\x00" + model
		a, ok := byKey[key]
		if !ok {
			a = &acc{provider: provider, model: model, usage: ZeroUsage()}
			byKey[key] = a
			order = append(order, key)
		}
		a.usage = a.usage.Add(c.Usage)
	}
	sort.Strings(order)
	out := make([]SessionModel, 0, len(order))
	for _, key := range order {
		a := byKey[key]
		sm := SessionModel{
			Provider:    a.provider,
			Model:       a.model,
			Usage:       a.usage,
			TotalTokens: a.usage.ConsumedTokens(),
			PriceSource: PriceSourceUnknown,
		}
		if price, ok := cat.Lookup(a.provider, a.model); ok {
			sm.Price = price
			sm.PriceSource = PriceSourceCatalog
		}
		sm.Cost = EstimateCost(a.usage, sm.Price)
		out = append(out, sm)
	}
	return out
}

// singleModel returns the session's model only when exactly one distinct
// (provider, model) ran; a multi-model session is attributed to no single model.
func singleModel(models []SessionModel) string {
	if len(models) == 1 {
		return models[0].Model
	}
	return ""
}

// sessionPrice exposes a session-level Price only when the session is
// single-model AND that model's price resolved from the catalog; otherwise the
// price is unknown (all-nil) and PriceSource is unknown.
func sessionPrice(models []SessionModel) (TokenPrice, PriceSource) {
	if len(models) == 1 && models[0].PriceSource == PriceSourceCatalog {
		return models[0].Price, PriceSourceCatalog
	}
	return TokenPrice{}, PriceSourceUnknown
}

// sessionCost sums the per-model costs. It is nil (UNKNOWN) when there are no
// models or ANY contributing model cost is nil; it is never fabricated from an
// unknown component.
func sessionCost(models []SessionModel) *float64 {
	if len(models) == 0 {
		return nil
	}
	var total float64
	for _, m := range models {
		if m.Cost == nil {
			return nil
		}
		total += *m.Cost
	}
	return &total
}

// associate derives LocalDates, LoggedDate, and Association from the session's
// calls. Only calls attributed to a WORKFLOW step (a recognized step, never
// StepUnknown) and carrying a timestamp contribute. A single distinct local date
// associates the session; zero or two-or-more dates (a local-midnight straddle)
// leaves the association unknown — the day is never guessed.
func associate(calls []CallRecord, loc *time.Location) ([]string, string, AssociationSource) {
	seen := make(map[string]struct{})
	var dates []string
	for i := range calls {
		c := calls[i]
		if !isWorkflowStep(c.Step) || c.Timestamp.IsZero() {
			continue
		}
		d := c.Timestamp.In(loc).Format("2006-01-02")
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		dates = append(dates, d)
	}
	sort.Strings(dates)
	if dates == nil {
		dates = []string{}
	}
	if len(dates) == 1 {
		return dates, dates[0], AssociationLocalStepDate
	}
	return dates, "", AssociationUnknown
}

// isWorkflowStep reports whether a step is a recognized workflow step (excludes
// StepUnknown and any unrecognized step).
func isWorkflowStep(s StepID) bool {
	_, ok := stepOrder[s]
	return ok
}

// firstNonEmpty returns the first trimmed non-empty string, else "".
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}
