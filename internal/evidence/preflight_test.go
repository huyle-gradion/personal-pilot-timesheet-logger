package evidence

import (
	"context"
	"testing"
)

// TestRegistry_PreflightContracts proves the preflight helper runs contract
// verification for enabled sources only and reports per-source results so a
// caller can refuse enablement on a non-complete result.
func TestRegistry_PreflightContracts(t *testing.T) {
	reg := NewRegistry()
	good := &testPlugin{kind: SourceManual, declared: []SourceKind{SourceManual}, collect: okCollect("good")}
	bad := &testPlugin{
		kind:     SourceCalendar,
		declared: []SourceKind{SourceManual},
		reqs:     []ConfigRequirement{{Key: "token", Secret: true, Required: true}},
		collect:  okCollect("bad"),
	}
	reg.Register(good)
	reg.Register(bad)

	sources := []SourceConfig{
		{ID: "good", Type: SourceManual, Enabled: true},
		{ID: "bad", Type: SourceCalendar, Enabled: true},
		{ID: "off", Type: SourceManual, Enabled: false},
	}

	results, err := reg.PreflightContracts(context.Background(), sources)
	if err != nil {
		t.Fatalf("PreflightContracts: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 enabled results (disabled excluded), got %d", len(results))
	}

	byID := map[string]SourceResult{}
	for _, r := range results {
		byID[r.SourceID] = r
	}
	if byID["good"].Status != StatusComplete {
		t.Errorf("expected good complete, got %+v", byID["good"])
	}
	if byID["bad"].Status != StatusIncomplete || byID["bad"].ErrorCode != CodeConfigRequiredMissing {
		t.Errorf("expected bad incomplete/%s, got %+v", CodeConfigRequiredMissing, byID["bad"])
	}
	if ContractsComplete(results) {
		t.Error("ContractsComplete must be false when any result is not complete")
	}
}

// TestRegistry_PreflightContractsAllComplete proves an all-complete set reports
// ready.
func TestRegistry_PreflightContractsAllComplete(t *testing.T) {
	reg := NewRegistry()
	p := &testPlugin{kind: SourceManual, declared: []SourceKind{SourceManual}, collect: okCollect("x")}
	reg.Register(p)

	results, err := reg.PreflightContracts(context.Background(), []SourceConfig{
		{ID: "a", Type: SourceManual, Enabled: true},
		{ID: "b", Type: SourceManual, Enabled: true},
	})
	if err != nil {
		t.Fatalf("PreflightContracts: %v", err)
	}
	if !ContractsComplete(results) {
		t.Errorf("expected all complete, got %+v", results)
	}
}
