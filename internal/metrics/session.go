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
	// An OBSERVED total is the transcript's own call counters. It is labelled so a
	// later `--declare-total` can never downgrade it (see DeclareSessionTotal). An
	// unavailable total carries the UNKNOWN source, never `observed`.
	if rec.TotalTokens.Known {
		rec.TotalTokensSource = TotalTokensSourceObserved
	} else {
		rec.TotalTokensSource = TotalTokensSourceUnknown
	}
	rec.Models = buildSessionModels(run.Calls, cat)
	rec.Model = singleModel(rec.Models)
	rec.ModelSource = modelSourceFor(run)
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
		}
		sm.Price, sm.PriceSource = resolveCatalogPrice(a.provider, a.model, cat)
		sm.Cost = EstimateCost(a.usage, sm.Price)
		out = append(out, sm)
	}
	return out
}

// resolveCatalogPrice resolves a (provider, model) price and its label. It is the
// SINGLE place that resolves a catalog price, so buildSessionModels and
// DeclareSessionModel cannot drift. The lookup key is (provider, model) ONLY: there
// is deliberately no model-only fallback. A present-but-empty catalog cost (key
// exists, every rate nil) is UNKNOWN, not `catalog`: the label is never stronger
// than the evidence.
func resolveCatalogPrice(provider, model string, cat *ModelCatalog) (TokenPrice, PriceSource) {
	if price, ok := cat.Lookup(provider, model); ok && !priceIsUnknown(price) {
		return price, PriceSourceCatalog
	}
	return TokenPrice{}, PriceSourceUnknown
}

// priceIsUnknown reports whether a resolved TokenPrice carries NO evidence: every
// one of its four rates is nil. Such a price is treated as unknown rather than
// labelled `catalog` (see resolveCatalogPrice).
func priceIsUnknown(p TokenPrice) bool {
	return p.Input == nil && p.Output == nil && p.CacheRead == nil && p.CacheWrite == nil
}

// modelSourceFor labels how the session's model identifier was established. A
// model the transcript itself carried is OBSERVED; an empty model is NEVER
// observed. The run-level Model is checked first, then the calls, so a resumed
// session whose model appears only on its calls is still observed.
func modelSourceFor(run RunRecord) ModelSource {
	if strings.TrimSpace(run.Model) != "" {
		return ModelSourceObserved
	}
	for i := range run.Calls {
		if strings.TrimSpace(run.Calls[i].Model) != "" {
			return ModelSourceObserved
		}
	}
	return ModelSourceUnknown
}

// EffectiveModelSource returns the record's EFFECTIVE model source, normalizing the
// EMPTY value a LEGACY record carries (one written before model_source existed): a
// record that carries a model identifier was observed from the transcript, while one
// that carries none was not. It is the model-axis twin of
// EffectiveTotalTokensSource, so both declaration axes rank a legacy record
// correctly instead of reading it as "unknown".
func EffectiveModelSource(s SessionRecord) ModelSource {
	switch s.ModelSource {
	case ModelSourceObserved, ModelSourceDeclared, ModelSourceUnknown:
		return s.ModelSource
	}
	if strings.TrimSpace(s.Model) != "" {
		return ModelSourceObserved
	}
	return ModelSourceUnknown
}

// DeclareSessionModel stamps an OWNER-DECLARED model onto a session whose
// transcript carries no model identifier. It returns false, changing nothing, when
// model is blank or the session's model is already OBSERVED: observed outranks
// declared (mirroring mergeProvenance's rank), so a declaration never overwrites
// transcript evidence — including a LEGACY record, whose empty source is normalized
// by EffectiveModelSource.
//
// On success it sets Model and every Models[i].Model, re-resolves each model's
// price by the (provider, model) lookup ONLY (no model-only fallback), and
// re-derives each cost. An unknown price stays unknown and a cost with any
// unknown contributor stays nil (never 0).
func DeclareSessionModel(s *SessionRecord, model string, cat *ModelCatalog) bool {
	if s == nil {
		return false
	}
	declared := strings.TrimSpace(model)
	if declared == "" || EffectiveModelSource(*s) == ModelSourceObserved {
		return false
	}
	ReattributeSessionModel(s, declared, cat)
	s.ModelSource = ModelSourceDeclared
	return true
}

// ReattributeSessionModel applies a model IDENTIFIER to the record's CURRENT
// per-model evidence and re-resolves prices and costs, WITHOUT touching
// ModelSource. It is the evidence half of DeclareSessionModel, shared by callers
// that keep a stored model id across a NEW parse of the same session (a grown
// transcript): the id is a declaration about the session, but the usage/price
// evidence belongs to the parse that just happened, so sum(Models[].TotalTokens)
// keeps matching TotalTokens and the cost keeps reflecting the tokens actually
// seen. Prices resolve by the (provider, model) lookup ONLY (no model-only
// fallback); an unknown price stays unknown and a cost with any unknown
// contributor stays nil (never 0). A blank id changes nothing and returns false.
func ReattributeSessionModel(s *SessionRecord, model string, cat *ModelCatalog) bool {
	if s == nil {
		return false
	}
	id := strings.TrimSpace(model)
	if id == "" {
		return false
	}
	s.Model = id
	for i := range s.Models {
		s.Models[i].Model = id
		s.Models[i].Price, s.Models[i].PriceSource = resolveCatalogPrice(s.Models[i].Provider, id, cat)
		s.Models[i].Cost = EstimateCost(s.Models[i].Usage, s.Models[i].Price)
	}
	s.Price, s.PriceSource = sessionPrice(s.Models)
	s.Cost = sessionCost(s.Models)
	return true
}

// SynthesizedSessionTotal returns the LOWER-BOUND session total for a DECLARED
// (`--declare-total`) run: the sum of each call's KNOWN CONSUMED components —
// exactly the four components ConsumedTokens sums (input + output + cacheRead +
// cacheWrite), each contributing only WHEN KNOWN.
//
// Reasoning is EXCLUDED: it is not a consumed component (ConsumedTokens excludes
// it too), so counting it here would produce a figure that is not a lower bound of
// the total it stands in for. That is the whole point of the label: every term is
// a component of the quantity being estimated.
//
// This is computed HERE, at session level, from the already-parsed counters: the
// per-call CallRecord usage components stay exactly as parsed (a component the
// provider did not report stays UNKNOWN), ConsumedTokens is unchanged, and no
// internal/workflow schema or adapter is touched. An unknown component contributes
// nothing, so the figure can only understate the session — which is why it is
// labelled synthesized and never presented as observed.
//
// A run whose EVERY consumed component is unknown stays UNKNOWN: a synthesized 0
// would claim a measurement nobody made.
func SynthesizedSessionTotal(calls []CallRecord) workflow.Measurement {
	total := int64(0)
	known := false
	for i := range calls {
		u := calls[i].Usage
		for _, m := range []workflow.Measurement{u.Input, u.Output, u.CacheRead, u.CacheWrite} {
			if !m.Known {
				continue
			}
			total += m.Value
			known = true
		}
	}
	if !known {
		return workflow.UnknownMeasurement()
	}
	return workflow.KnownMeasurement(total)
}

// EffectiveTotalTokensSource returns the record's EFFECTIVE total source,
// normalizing the EMPTY value a LEGACY record carries (one written before
// total_source existed): a total that is known there was observed from the
// transcript's own counters, and an unknown one was simply not observed.
//
// This is the ONE normalization site, and DeclareSessionTotal and the CLI's
// import precedence both read it, so a legacy observed total can never be
// downgraded to synthesized by a later declaration.
func EffectiveTotalTokensSource(s SessionRecord) TotalTokensSource {
	switch s.TotalTokensSource {
	case TotalTokensSourceObserved, TotalTokensSourceSynthesized, TotalTokensSourceUnknown:
		return s.TotalTokensSource
	}
	if s.TotalTokens.Known {
		return TotalTokensSourceObserved
	}
	return TotalTokensSourceUnknown
}

// DeclareSessionTotal stamps a SYNTHESIZED (lower-bound) session total for a run
// whose transcript carries no total. It returns false, changing nothing, when the
// session's total is already OBSERVED (observed outranks synthesized, so a
// declaration never downgrades a real total — including a LEGACY record whose empty
// source means observed-when-known) or when no consumed component is known.
func DeclareSessionTotal(s *SessionRecord, run RunRecord) bool {
	if s == nil || EffectiveTotalTokensSource(*s) == TotalTokensSourceObserved {
		return false
	}
	total := SynthesizedSessionTotal(run.Calls)
	if !total.Known {
		return false
	}
	s.TotalTokens = total
	s.TotalTokensSource = TotalTokensSourceSynthesized
	return true
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
