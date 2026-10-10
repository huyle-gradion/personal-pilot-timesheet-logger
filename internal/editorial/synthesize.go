package editorial

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

// DefaultMaxAttempts is the editor call budget per target (one retry).
const DefaultMaxAttempts = 2

// Options tunes Synthesize.
type Options struct {
	// Now is the injectable clock.
	Now time.Time
	// Target is the soft daily editorial token target (0 = SoftDailyTokenTarget).
	Target int64
	// Spent is the already-spent editorial EDITORIAL usage for the day (prior
	// runs). A caller with accumulated spend MUST pass Known values: an unknown or
	// omitted Spent is treated as a KNOWN zero (a fresh day), so the soft target is
	// always enforced. It is never treated as unlimited.
	//
	// S8 OBLIGATION: this report REPLACES the day's usage; it does not accumulate
	// across reruns. S8 must pass the accumulated Known Spent and add the run.
	Spent Usage
	// Orchestration is the separately-reported orchestration usage. It is NEVER
	// summed with editorial usage.
	Orchestration Usage
	// MaxAttempts bounds editor calls per target (0 = DefaultMaxAttempts).
	MaxAttempts int
	// MeasureRSS reports peak RSS in MB (nil = benchmark.CurrentProcessRSSMB).
	MeasureRSS func() float64
	// Clock returns the current time (nil = time.Now, or Options.Now when set).
	Clock func() time.Time
}

// TargetHash returns the content-addressed identity of a target's REQUEST content:
// sha256 over the canonical JSON of {Kind, Date, Repository, PRNumber, Branch,
// Project, Facts}. ID and EvidenceIDs are DELIBERATELY excluded: the editorial text
// derives only from the facts (and this non-time identity), so a changed evidence
// SET that yields identical facts reuses cached text safely. Any change to a fact
// changes the hash and reinvokes the editor.
func TargetHash(t Target) string {
	canonical := struct {
		Kind       string `json:"kind"`
		Date       string `json:"date"`
		Repository string `json:"repository"`
		PRNumber   int    `json:"pr_number"`
		Branch     string `json:"branch"`
		Project    string `json:"project"`
		Facts      []Fact `json:"facts"`
	}{
		Kind:       t.Kind,
		Date:       t.Date,
		Repository: t.Repository,
		PRNumber:   t.PRNumber,
		Branch:     t.Branch,
		Project:    t.Project,
		Facts:      canonicalFacts(t.Facts),
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// canonicalFacts returns a deterministically ordered copy of facts.
func canonicalFacts(facts []Fact) []Fact {
	out := make([]Fact, len(facts))
	copy(out, facts)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Text < out[j].Text
	})
	return out
}

// Synthesize produces exactly ONE suggestion per target, so it NEVER blocks
// review. Per target: a cached suggestion whose request hash matches is reused
// with zero tokens; a Known-exhausted budget or a nil editor yields deterministic
// text; otherwise the editor is called up to MaxAttempts (accumulating usage),
// validated FAIL-CLOSED, and on persistent failure the deterministic text is used.
//
// The editor is called ONCE PER TARGET (a single-target Request). S8 may batch
// targets into one call; the per-target contract here keeps retry/fallback and
// usage accounting exact.
func Synthesize(ctx context.Context, editor Editor, req Request, cache map[string]Suggestion, opts Options) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("editorial: nil context")
	}
	target := opts.Target
	if target <= 0 {
		target = SoftDailyTokenTarget
	}
	maxAttempts := opts.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}
	clock := opts.Clock
	if clock == nil {
		now := opts.Now
		clock = func() time.Time {
			if now.IsZero() {
				return time.Now()
			}
			return now
		}
	}
	rss := opts.MeasureRSS
	if rss == nil {
		rss = DefaultMeasureRSS
	}

	start := clock()
	report := Report{
		Date:          req.Date,
		Editorial:     KnownUsage(0, 0, 0),
		Orchestration: opts.Orchestration,
	}
	spent := normalizeSpent(opts.Spent)

	suggestions := make([]Suggestion, 0, len(req.Targets))
	for _, t := range req.Targets {
		hash := TargetHash(t)
		if c, ok := cache[t.ID]; ok && c.RequestHash == hash {
			reused := c
			reused.Origin = OriginCache
			reused.Warnings = append([]string(nil), c.Warnings...)
			suggestions = append(suggestions, reused)
			continue
		}

		if editor == nil || EvaluateBudget(spent.Total(), target) == BudgetKnownExhausted {
			suggestions = append(suggestions, Suggestion{
				TargetID:    t.ID,
				RequestHash: hash,
				Text:        DeterministicText(t),
				Origin:      OriginDeterministic,
			})
			continue
		}

		chosen, usage, runs := callEditor(ctx, editor, req.Date, t, hash, maxAttempts)
		report.Invoked = true
		report.EditorialRuns += runs
		report.Editorial = report.Editorial.Add(usage)
		spent = spent.Add(usage)
		suggestions = append(suggestions, chosen)
	}

	report.RuntimeMS = clock().Sub(start).Milliseconds()
	report.PeakRSSMB = rss()
	return Result{Date: req.Date, Suggestions: suggestions, Report: report}, nil
}

// callEditor calls the editor for one target up to maxAttempts times, accumulating
// usage, validating each response FAIL-CLOSED, and returning the first valid
// suggestion or the deterministic fallback. It returns the runs attempted.
func callEditor(ctx context.Context, editor Editor, date string, t Target, hash string, maxAttempts int) (Suggestion, Usage, int) {
	allowed := map[string]struct{}{t.ID: {}}
	total := KnownUsage(0, 0, 0)
	runs := 0
	for attempt := 0; attempt < maxAttempts; attempt++ {
		runs++
		resp, usage, err := editor.Synthesize(ctx, Request{Date: date, Targets: []Target{t}})
		total = total.Add(usage)
		if err != nil {
			continue
		}
		payload, mErr := json.Marshal(resp)
		if mErr != nil {
			continue
		}
		parsed, vErr := ParseResponse(bytes.NewReader(payload), allowed, Bounds{})
		if vErr != nil {
			continue
		}
		text, ok := parsed.Suggestions[t.ID]
		if !ok || strings.TrimSpace(text.Title) == "" || strings.TrimSpace(text.Description) == "" {
			continue
		}
		return Suggestion{
			TargetID:    t.ID,
			RequestHash: hash,
			Text:        text,
			Origin:      OriginEditorial,
			Warnings:    flagUnsupportedImpact(text.Title+" "+text.Description, t.Facts),
		}, total, runs
	}
	return Suggestion{
		TargetID:    t.ID,
		RequestHash: hash,
		Text:        DeterministicText(t),
		Origin:      OriginDeterministic,
	}, total, runs
}
