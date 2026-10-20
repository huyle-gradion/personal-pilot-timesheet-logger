package editorial

import (
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

func TestEvaluateBudget(t *testing.T) {
	if got := EvaluateBudget(workflow.KnownMeasurement(999), 1000); got != BudgetKnownAvailable {
		t.Errorf("want available, got %s", got)
	}
	if got := EvaluateBudget(workflow.KnownMeasurement(1000), 1000); got != BudgetKnownExhausted {
		t.Errorf("want exhausted at the target, got %s", got)
	}
	if got := EvaluateBudget(workflow.KnownMeasurement(2000), 1000); got != BudgetKnownExhausted {
		t.Errorf("want exhausted above the target, got %s", got)
	}
	if got := EvaluateBudget(workflow.UnknownMeasurement(), 1000); got != BudgetUnknown {
		t.Errorf("unknown spent must be BudgetUnknown, not zero/exhausted; got %s", got)
	}
	if got := EvaluateBudget(workflow.KnownMeasurement(5), 0); got != BudgetKnownAvailable {
		t.Errorf("a zero target must use the default; got %s", got)
	}
	if SoftDailyTokenTarget != 1000 {
		t.Errorf("the owner-confirmed soft target must be 1000, got %d", SoftDailyTokenTarget)
	}
}

func TestUsage_UnknownIsNotZero(t *testing.T) {
	if UnknownUsage().Total().Known {
		t.Errorf("an unknown usage must not total as known")
	}
	if KnownUsage(1, 2, 3).Total().Value != 6 {
		t.Errorf("want total 6, got %d", KnownUsage(1, 2, 3).Total().Value)
	}
	mixed := KnownUsage(1, 1, 1).Add(UnknownUsage())
	if mixed.Total().Known {
		t.Errorf("adding an unknown component must yield an unknown total")
	}
	if KnownUsage(10, 20, 5).Add(KnownUsage(1, 2, 3)).Total().Value != 41 {
		t.Errorf("known add must sum")
	}
}

func TestReport_EditorialAndOrchestrationNotSummed(t *testing.T) {
	report := Report{
		Editorial:     KnownUsage(10, 20, 0),
		Orchestration: KnownUsage(100, 200, 50),
	}
	if report.Editorial.Total().Value != 30 {
		t.Errorf("want editorial 30, got %d", report.Editorial.Total().Value)
	}
	if report.Orchestration.Total().Value != 350 {
		t.Errorf("want orchestration 350, got %d", report.Orchestration.Total().Value)
	}
	// They are independent: a change to one never changes the other.
	report.Editorial = KnownUsage(999, 0, 0)
	if report.Orchestration.Total().Value != 350 {
		t.Errorf("editorial and orchestration must never be summed")
	}
}
