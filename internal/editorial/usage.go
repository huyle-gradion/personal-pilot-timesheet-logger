package editorial

import (
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/benchmark"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// EvaluateBudget classifies the soft daily editorial budget. A target <= 0 uses
// SoftDailyTokenTarget. An unknown spent total is BudgetUnknown: it is NEVER
// treated as zero and NEVER assumed exhausted.
func EvaluateBudget(spent workflow.Measurement, target int64) BudgetState {
	if target <= 0 {
		target = SoftDailyTokenTarget
	}
	if !spent.Known {
		return BudgetUnknown
	}
	if spent.Value >= target {
		return BudgetKnownExhausted
	}
	return BudgetKnownAvailable
}

// OrchestrationUsageFromBenchmark adapts a benchmark TokenUsage into an editorial
// Usage. Orchestration usage is reported SEPARATELY and is never summed with
// editorial usage.
func OrchestrationUsageFromBenchmark(tu benchmark.TokenUsage) Usage {
	return KnownUsage(tu.InputTokens, tu.OutputTokens, tu.CacheTokens)
}

// DefaultMeasureRSS reports the current process peak RSS in MB via benchmark.
func DefaultMeasureRSS() float64 { return benchmark.CurrentProcessRSSMB() }

// normalizeSpent treats an unknown Spent component as a KNOWN zero, so the soft
// daily target is always enforced: an omitted Options.Spent (the zero Usage) must
// never disable the budget. A caller with accumulated spend MUST pass Known values;
// this only fills in the omitted/unknown case. It never treats spend as unlimited.
func normalizeSpent(u Usage) Usage {
	return Usage{
		Input:  knownOrZero(u.Input),
		Output: knownOrZero(u.Output),
		Cache:  knownOrZero(u.Cache),
	}
}

func knownOrZero(m workflow.Measurement) workflow.Measurement {
	if m.Known {
		return m
	}
	return workflow.KnownMeasurement(0)
}
