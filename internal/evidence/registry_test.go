package evidence

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// testPlugin is a configurable evidence.Plugin for registry tests.
type testPlugin struct {
	kind     SourceKind
	reqs     []ConfigRequirement
	declared []SourceKind
	check    func(ctx context.Context, cfg SourceConfig) []ContractCheck
	collect  func(ctx context.Context, cfg SourceConfig, req CollectRequest) (CollectResult, error)
	calls    atomic.Int64
}

func (p *testPlugin) Type() SourceKind                    { return p.kind }
func (p *testPlugin) RequiredConfig() []ConfigRequirement { return p.reqs }
func (p *testPlugin) DeclaredEvidenceKinds() []SourceKind { return p.declared }
func (p *testPlugin) CheckContract(ctx context.Context, cfg SourceConfig) []ContractCheck {
	if p.check != nil {
		return p.check(ctx, cfg)
	}
	return nil
}
func (p *testPlugin) Collect(ctx context.Context, cfg SourceConfig, req CollectRequest) (CollectResult, error) {
	p.calls.Add(1)
	if p.collect != nil {
		return p.collect(ctx, cfg, req)
	}
	return CollectResult{Result: SourceResult{Status: StatusComplete}}, nil
}

func okCollect(title string) func(context.Context, SourceConfig, CollectRequest) (CollectResult, error) {
	return func(context.Context, SourceConfig, CollectRequest) (CollectResult, error) {
		ev := Evidence{ID: StableID(SourceManual, title), Kind: SourceManual, Title: title}
		return CollectResult{
			Evidence: []Evidence{ev},
			Result:   SourceResult{Status: StatusComplete, EvidenceCount: 1},
		}, nil
	}
}

func source(id string, kind SourceKind, enabled bool, cfg map[string]string) SourceConfig {
	return SourceConfig{ID: id, Type: kind, Enabled: enabled, Config: cfg}
}

// TestRegistry_UnknownTypeRefusedAndNeverExecuted proves an unknown plugin type
// yields a RegistryError and is never executed, even when enabled.
func TestRegistry_UnknownTypeRefusedAndNeverExecuted(t *testing.T) {
	reg := NewRegistry()
	known := &testPlugin{kind: SourceManual, declared: []SourceKind{SourceManual}, collect: okCollect("x")}
	reg.Register(known)

	unknown := source("bad", "not-a-real-plugin", true, nil)
	bound, errs := reg.Resolve([]SourceConfig{unknown})
	if len(bound) != 0 {
		t.Errorf("expected no bound plugins for unknown type, got %d", len(bound))
	}
	if len(errs) != 1 || errs[0].Code != CodeUnknownPluginType {
		t.Fatalf("expected one %s RegistryError, got %+v", CodeUnknownPluginType, errs)
	}

	_, summary, err := reg.CollectDay(context.Background(), []SourceConfig{unknown}, CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("CollectDay returned error: %v", err)
	}
	if len(summary.Sources) != 1 {
		t.Fatalf("expected the unknown source to be reported, got %+v", summary.Sources)
	}
	if summary.Sources[0].Status != StatusIncomplete || summary.Sources[0].ErrorCode != CodeUnknownPluginType {
		t.Errorf("expected incomplete/%s, got %+v", CodeUnknownPluginType, summary.Sources[0])
	}
	if summary.Complete {
		t.Error("expected Complete=false with an unknown enabled source")
	}
	if known.calls.Load() != 0 {
		t.Error("registered plugin was executed for an unknown source type")
	}
}

// TestRegistry_DisabledSourceSkipped proves a disabled source is skipped and does
// not block completeness.
func TestRegistry_DisabledSourceSkipped(t *testing.T) {
	reg := NewRegistry()
	p := &testPlugin{kind: SourceManual, declared: []SourceKind{SourceManual}, collect: okCollect("x")}
	reg.Register(p)

	_, summary, err := reg.CollectDay(context.Background(), []SourceConfig{
		source("off", SourceManual, false, nil),
	}, CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("CollectDay returned error: %v", err)
	}
	if summary.Sources[0].Status != StatusSkipped {
		t.Errorf("expected skipped, got %s", summary.Sources[0].Status)
	}
	if !summary.Complete {
		t.Error("a disabled source must not block completeness")
	}
	if p.calls.Load() != 0 {
		t.Error("disabled source was executed")
	}
	if err := RequireAcknowledgment(summary, nil); err != nil {
		t.Errorf("a disabled source must not require acknowledgment, got %v", err)
	}
}

// TestRegistry_PanicBecomesIncompleteStableCode proves a panicking source becomes
// incomplete with a stable code and is never silently dropped.
func TestRegistry_PanicBecomesIncompleteStableCode(t *testing.T) {
	reg := NewRegistry()
	p := &testPlugin{
		kind:     SourceManual,
		declared: []SourceKind{SourceManual},
		collect:  func(context.Context, SourceConfig, CollectRequest) (CollectResult, error) { panic("boom") },
	}
	reg.Register(p)

	ev, summary, err := reg.CollectDay(context.Background(), []SourceConfig{
		source("s", SourceManual, true, nil),
	}, CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("CollectDay returned error: %v", err)
	}
	if len(ev) != 0 {
		t.Errorf("expected no evidence from a panicking source, got %d", len(ev))
	}
	if len(summary.Sources) != 1 || summary.Sources[0].Status != StatusIncomplete {
		t.Fatalf("expected one incomplete source, got %+v", summary.Sources)
	}
	if summary.Sources[0].ErrorCode != CodeSourcePanic {
		t.Errorf("expected stable code %s, got %q", CodeSourcePanic, summary.Sources[0].ErrorCode)
	}
	if summary.Complete {
		t.Error("expected Complete=false")
	}
}

// TestRegistry_FailingSourceIncomplete proves a source that returns an error is
// reported incomplete with a stable code, not surfaced as a function error.
func TestRegistry_FailingSourceIncomplete(t *testing.T) {
	reg := NewRegistry()
	p := &testPlugin{
		kind:     SourceManual,
		declared: []SourceKind{SourceManual},
		collect: func(context.Context, SourceConfig, CollectRequest) (CollectResult, error) {
			return CollectResult{}, errors.New("network exploded with token abc123")
		},
	}
	reg.Register(p)

	_, summary, err := reg.CollectDay(context.Background(), []SourceConfig{
		source("s", SourceManual, true, nil),
	}, CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("per-source failure must not be a function error, got %v", err)
	}
	if summary.Sources[0].ErrorCode != CodeSourceError {
		t.Errorf("expected stable code %s, got %q", CodeSourceError, summary.Sources[0].ErrorCode)
	}
	if strings.Contains(summary.Sources[0].Message, "abc123") {
		t.Errorf("raw error text leaked into SourceResult.Message: %q", summary.Sources[0].Message)
	}
}

// TestRegistry_TimeoutBecomesStableCode proves the bounded timeout is honored.
func TestRegistry_TimeoutBecomesStableCode(t *testing.T) {
	reg := NewRegistry()
	reg.SourceTimeout = 20 * time.Millisecond
	p := &testPlugin{
		kind:     SourceManual,
		declared: []SourceKind{SourceManual},
		collect: func(context.Context, SourceConfig, CollectRequest) (CollectResult, error) {
			time.Sleep(150 * time.Millisecond)
			return CollectResult{Result: SourceResult{Status: StatusComplete}}, nil
		},
	}
	reg.Register(p)

	_, summary, err := reg.CollectDay(context.Background(), []SourceConfig{
		source("slow", SourceManual, true, nil),
	}, CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("CollectDay returned error: %v", err)
	}
	if summary.Sources[0].ErrorCode != CodeSourceTimeout {
		t.Errorf("expected %s, got %+v", CodeSourceTimeout, summary.Sources[0])
	}
}

// TestRegistry_ContractCheckFailureExcludes proves a plugin failing a config
// contract check is excluded and reported incomplete with the check's code.
func TestRegistry_ContractCheckFailureExcludes(t *testing.T) {
	reg := NewRegistry()
	p := &testPlugin{
		kind:     SourceManual,
		declared: []SourceKind{SourceManual},
		reqs:     []ConfigRequirement{{Key: "api_key", Secret: true, Required: true}},
		collect:  okCollect("should-not-run"),
	}
	reg.Register(p)

	ev, summary, err := reg.CollectDay(context.Background(), []SourceConfig{
		source("s", SourceManual, true, map[string]string{"api_key": "raw-secret-value"}),
	}, CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("CollectDay returned error: %v", err)
	}
	if len(ev) != 0 {
		t.Errorf("evidence from a check-failed plugin must be excluded, got %d", len(ev))
	}
	if summary.Sources[0].Status != StatusIncomplete || summary.Sources[0].ErrorCode != CodeConfigSecretNotRef {
		t.Errorf("expected incomplete/%s, got %+v", CodeConfigSecretNotRef, summary.Sources[0])
	}
	if p.calls.Load() != 0 {
		t.Error("plugin was executed despite a failed contract check")
	}
}

// TestRegistry_DeclaredKindMismatchExcludes proves evidence of an undeclared kind
// fails the plugin out.
func TestRegistry_DeclaredKindMismatchExcludes(t *testing.T) {
	reg := NewRegistry()
	p := &testPlugin{
		kind:     SourceManual,
		declared: []SourceKind{SourceCalendar}, // does not include manual
		collect:  okCollect("manual-evidence"),
	}
	reg.Register(p)

	ev, summary, err := reg.CollectDay(context.Background(), []SourceConfig{
		source("s", SourceManual, true, nil),
	}, CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("CollectDay returned error: %v", err)
	}
	if len(ev) != 0 {
		t.Errorf("evidence of an undeclared kind must be excluded, got %d", len(ev))
	}
	if summary.Sources[0].ErrorCode != CodeDeclaredKindsMismatch {
		t.Errorf("expected %s, got %+v", CodeDeclaredKindsMismatch, summary.Sources[0])
	}
}

// TestRegistry_CollectDayInvokesCollectOnce proves live collection invokes a
// source's Collect exactly once (no doubled live cost).
func TestRegistry_CollectDayInvokesCollectOnce(t *testing.T) {
	reg := NewRegistry()
	p := &testPlugin{kind: SourceManual, declared: []SourceKind{SourceManual}, collect: okCollect("x")}
	reg.Register(p)

	_, summary, err := reg.CollectDay(context.Background(), []SourceConfig{
		source("s", SourceManual, true, nil),
	}, CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("CollectDay returned error: %v", err)
	}
	if !summary.Complete {
		t.Fatalf("expected complete collection, got %+v", summary)
	}
	if got := p.calls.Load(); got != 1 {
		t.Errorf("CollectDay must invoke Collect exactly once, got %d", got)
	}
}

// TestRegistry_VerifyContractsDetectsNondeterminism proves the determinism check
// lives in the contract path (fixed-input re-run), not in live collection: live
// CollectDay succeeds once, while VerifyContracts flags the unstable IDs.
func TestRegistry_VerifyContractsDetectsNondeterminism(t *testing.T) {
	reg := NewRegistry()
	p := &testPlugin{
		kind:     SourceManual,
		declared: []SourceKind{SourceManual},
		collect: func(_ context.Context, _ SourceConfig, _ CollectRequest) (CollectResult, error) {
			// IDs vary per call -> nondeterministic.
			ev := Evidence{ID: StableID(SourceManual, time.Now().UTC().Format(time.RFC3339Nano)), Kind: SourceManual}
			return CollectResult{Evidence: []Evidence{ev}, Result: SourceResult{Status: StatusComplete, EvidenceCount: 1}}, nil
		},
	}
	reg.Register(p)
	src := source("s", SourceManual, true, nil)

	// Live collection: one invocation, no determinism check.
	_, summary, err := reg.CollectDay(context.Background(), []SourceConfig{src}, CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("CollectDay returned error: %v", err)
	}
	if summary.Sources[0].Status != StatusComplete {
		t.Fatalf("live collection must not run the determinism check, got %+v", summary.Sources[0])
	}
	if got := p.calls.Load(); got != 1 {
		t.Errorf("live CollectDay invoked Collect %d times, want 1", got)
	}

	// Contract path: fixed-input re-run detects the nondeterminism.
	results, err := reg.VerifyContracts(context.Background(), []SourceConfig{src})
	if err != nil {
		t.Fatalf("VerifyContracts returned error: %v", err)
	}
	if len(results) != 1 || results[0].ErrorCode != CodeNondeterministic {
		t.Fatalf("expected %s from VerifyContracts, got %+v", CodeNondeterministic, results)
	}
}

// TestRegistry_VerifyContractsAcceptsDeterministic proves a deterministic plugin
// passes the contract path.
func TestRegistry_VerifyContractsAcceptsDeterministic(t *testing.T) {
	reg := NewRegistry()
	p := &testPlugin{kind: SourceManual, declared: []SourceKind{SourceManual}, collect: okCollect("stable")}
	reg.Register(p)

	results, err := reg.VerifyContracts(context.Background(), []SourceConfig{source("s", SourceManual, true, nil)})
	if err != nil {
		t.Fatalf("VerifyContracts returned error: %v", err)
	}
	if results[0].Status != StatusComplete {
		t.Errorf("expected complete contract check, got %+v", results[0])
	}
}

// TestRegistry_CanceledDistinctFromTimeout proves a caller cancellation yields
// CodeSourceCanceled, distinct from a bounded-timeout CodeSourceTimeout.
func TestRegistry_CanceledDistinctFromTimeout(t *testing.T) {
	reg := NewRegistry()
	p := &testPlugin{
		kind:     SourceManual,
		declared: []SourceKind{SourceManual},
		collect: func(ctx context.Context, _ SourceConfig, _ CollectRequest) (CollectResult, error) {
			<-ctx.Done()
			return CollectResult{}, ctx.Err()
		},
	}
	reg.Register(p)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, summary, err := reg.CollectDay(ctx, []SourceConfig{source("s", SourceManual, true, nil)}, CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("CollectDay returned error: %v", err)
	}
	if summary.Sources[0].ErrorCode != CodeSourceCanceled {
		t.Errorf("expected %s, got %+v", CodeSourceCanceled, summary.Sources[0])
	}
}

// TestRegistry_PluginReturningCanceledIsCanceled proves a plugin that itself
// returns context.Canceled maps to CodeSourceCanceled, not CodeSourceError.
func TestRegistry_PluginReturningCanceledIsCanceled(t *testing.T) {
	reg := NewRegistry()
	p := &testPlugin{
		kind:     SourceManual,
		declared: []SourceKind{SourceManual},
		collect: func(context.Context, SourceConfig, CollectRequest) (CollectResult, error) {
			return CollectResult{}, context.Canceled
		},
	}
	reg.Register(p)

	_, summary, err := reg.CollectDay(context.Background(), []SourceConfig{source("s", SourceManual, true, nil)}, CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("CollectDay returned error: %v", err)
	}
	if summary.Sources[0].ErrorCode != CodeSourceCanceled {
		t.Errorf("expected %s, got %+v", CodeSourceCanceled, summary.Sources[0])
	}
}

// TestRegistry_RequireAcknowledgment proves an incomplete source blocks until it
// is explicitly acknowledged, and that acking it clears the error.
func TestRegistry_RequireAcknowledgment(t *testing.T) {
	reg := NewRegistry()
	p := &testPlugin{
		kind:     SourceManual,
		declared: []SourceKind{SourceManual},
		collect: func(context.Context, SourceConfig, CollectRequest) (CollectResult, error) {
			return CollectResult{}, errors.New("down")
		},
	}
	reg.Register(p)

	_, summary, err := reg.CollectDay(context.Background(), []SourceConfig{
		source("cal", SourceCalendar, true, nil),
		source("git", SourceManual, true, nil),
	}, CollectRequest{Date: "2026-10-08"})
	// "cal" is unknown type -> incomplete; "git" fails -> incomplete.
	if err != nil {
		t.Fatalf("CollectDay returned error: %v", err)
	}

	err = RequireAcknowledgment(summary, nil)
	if !errors.Is(err, ErrCollectionIncomplete) {
		t.Fatalf("expected ErrCollectionIncomplete, got %v", err)
	}
	if !strings.Contains(err.Error(), "cal") || !strings.Contains(err.Error(), "git") {
		t.Errorf("error must name every unacked source, got %q", err.Error())
	}

	if err := RequireAcknowledgment(summary, map[string]bool{"cal": true}); err == nil {
		t.Error("expected error while git remains unacked")
	}
	if err := RequireAcknowledgment(summary, map[string]bool{"cal": true, "git": true}); err != nil {
		t.Errorf("expected nil once all non-complete sources are acked, got %v", err)
	}
}

// TestRegistry_SentinelSecretNeverSerialized proves a sentinel secret in config
// never appears in serialized evidence, results, or errors.
func TestRegistry_SentinelSecretNeverSerialized(t *testing.T) {
	const sentinel = "SENTINEL-SECRET-8f3a2b"
	reg := NewRegistry()
	p := &testPlugin{
		kind:     SourceManual,
		declared: []SourceKind{SourceManual},
		reqs:     []ConfigRequirement{{Key: "token", Secret: true, Required: true}},
		collect:  okCollect("work"),
	}
	reg.Register(p)

	// Case 1: a well-formed keychain reference is used and must not be serialized
	// into results.
	reg2 := NewRegistry()
	p2 := &testPlugin{
		kind:     SourceManual,
		declared: []SourceKind{SourceManual},
		reqs:     []ConfigRequirement{{Key: "token", Secret: true, Required: true}},
		collect:  okCollect("work"),
	}
	reg2.Register(p2)
	ev, summary, err := reg2.CollectDay(context.Background(), []SourceConfig{
		source("ok", SourceManual, true, map[string]string{"token": "keychain:gradion.workspace"}),
	}, CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("CollectDay returned error: %v", err)
	}
	assertNoSecret(t, "well-formed ref", dump(ev, summary), "keychain:gradion.workspace")

	// Case 2: a raw sentinel secret is misconfigured; the plugin is excluded and
	// the sentinel must never appear in the serialized output or error text.
	ev, summary, err = reg.CollectDay(context.Background(), []SourceConfig{
		source("leaky", SourceManual, true, map[string]string{"token": sentinel}),
	}, CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("CollectDay returned error: %v", err)
	}
	assertNoSecret(t, "raw sentinel", dump(ev, summary), sentinel)
	if summary.Sources[0].ErrorCode != CodeConfigSecretNotRef {
		t.Errorf("expected the misconfigured secret to fail the check, got %+v", summary.Sources[0])
	}
}

func assertNoSecret(t *testing.T, label, artifact, secret string) {
	t.Helper()
	if strings.Contains(artifact, secret) {
		t.Errorf("%s: secret leaked into serialized output: %s", label, artifact)
	}
}

func dump(ev []Evidence, summary CollectionSummary) string {
	data, _ := json.Marshal(struct {
		Evidence []Evidence        `json:"evidence"`
		Summary  CollectionSummary `json:"summary"`
	}{Evidence: ev, Summary: summary})
	return string(data)
}

// TestRegistry_WarningsPreservedOnIncomplete proves a source's self-reported
// warnings survive an incomplete result.
func TestRegistry_WarningsPreservedOnIncomplete(t *testing.T) {
	reg := NewRegistry()
	p := &testPlugin{
		kind:     SourceManual,
		declared: []SourceKind{SourceManual},
		collect: func(context.Context, SourceConfig, CollectRequest) (CollectResult, error) {
			return CollectResult{Result: SourceResult{Status: StatusIncomplete, Warnings: []string{"w-incomplete"}}}, nil
		},
	}
	reg.Register(p)

	_, summary, err := reg.CollectDay(context.Background(), []SourceConfig{source("s", SourceManual, true, nil)}, CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("CollectDay returned error: %v", err)
	}
	if summary.Sources[0].Status != StatusIncomplete {
		t.Fatalf("expected incomplete, got %+v", summary.Sources[0])
	}
	if len(summary.Sources[0].Warnings) != 1 || summary.Sources[0].Warnings[0] != "w-incomplete" {
		t.Errorf("warnings dropped on the incomplete path: %v", summary.Sources[0].Warnings)
	}
}

// TestRegistry_WarningsPreservedWhenCheckFails proves warnings survive a
// contract-check failure on the live path.
func TestRegistry_WarningsPreservedWhenCheckFails(t *testing.T) {
	reg := NewRegistry()
	p := &testPlugin{
		kind:     SourceManual,
		declared: []SourceKind{SourceCalendar}, // manual evidence is undeclared
		collect: func(context.Context, SourceConfig, CollectRequest) (CollectResult, error) {
			return CollectResult{
				Evidence: []Evidence{{ID: StableID(SourceManual, "x"), Kind: SourceManual}},
				Result:   SourceResult{Status: StatusComplete, EvidenceCount: 1, Warnings: []string{"w-check"}},
			}, nil
		},
	}
	reg.Register(p)

	_, summary, err := reg.CollectDay(context.Background(), []SourceConfig{source("s", SourceManual, true, nil)}, CollectRequest{Date: "2026-10-08"})
	if err != nil {
		t.Fatalf("CollectDay returned error: %v", err)
	}
	if summary.Sources[0].ErrorCode != CodeDeclaredKindsMismatch {
		t.Fatalf("expected %s, got %+v", CodeDeclaredKindsMismatch, summary.Sources[0])
	}
	if len(summary.Sources[0].Warnings) != 1 || summary.Sources[0].Warnings[0] != "w-check" {
		t.Errorf("warnings dropped on the check-failure path: %v", summary.Sources[0].Warnings)
	}
}

// TestRegistry_WarningsPreservedOnContractPath proves warnings survive contract
// verification too.
func TestRegistry_WarningsPreservedOnContractPath(t *testing.T) {
	reg := NewRegistry()
	p := &testPlugin{
		kind:     SourceManual,
		declared: []SourceKind{SourceManual},
		collect: func(context.Context, SourceConfig, CollectRequest) (CollectResult, error) {
			return CollectResult{
				Evidence: []Evidence{{ID: StableID(SourceManual, "x"), Kind: SourceManual}},
				Result:   SourceResult{Status: StatusComplete, EvidenceCount: 1, Warnings: []string{"w-contract"}},
			}, nil
		},
	}
	reg.Register(p)

	results, err := reg.VerifyContracts(context.Background(), []SourceConfig{source("s", SourceManual, true, nil)})
	if err != nil {
		t.Fatalf("VerifyContracts returned error: %v", err)
	}
	if len(results) != 1 || results[0].Status != StatusComplete {
		t.Fatalf("expected one complete contract result, got %+v", results)
	}
	if len(results[0].Warnings) != 1 || results[0].Warnings[0] != "w-contract" {
		t.Errorf("warnings dropped on the contract path: %v", results[0].Warnings)
	}
}

// --- ValidateSources: runtime configuration validation ----------------------

// TestRegistry_ValidateSourcesRuntime proves the runtime path validates a valid
// config WITHOUT a fixture and never collects.
func TestRegistry_ValidateSourcesRuntime(t *testing.T) {
	reg := NewRegistry()
	good := &testPlugin{kind: SourceManual, declared: []SourceKind{SourceManual}}
	reg.Register(good)

	results, err := reg.ValidateSources(context.Background(), []SourceConfig{
		source("ok", SourceManual, true, nil),
	})
	if err != nil {
		t.Fatalf("ValidateSources: %v", err)
	}
	if len(results) != 1 || results[0].Status != StatusComplete {
		t.Fatalf("a valid config must validate complete without a fixture, got %+v", results)
	}
	if results[0].EvidenceCount != 0 {
		t.Errorf("runtime validation must report zero evidence, got %d", results[0].EvidenceCount)
	}
	if good.calls.Load() != 0 {
		t.Errorf("runtime validation must not collect, got %d collect call(s)", good.calls.Load())
	}
	if !SourcesConfigured(results) {
		t.Error("a complete runtime result must be configured")
	}
}

// TestRegistry_ValidateSourcesUnknownTypeFails proves an unknown plugin type
// fails runtime validation.
func TestRegistry_ValidateSourcesUnknownTypeFails(t *testing.T) {
	reg := NewRegistry()
	results, err := reg.ValidateSources(context.Background(), []SourceConfig{source("bad", "nope", true, nil)})
	if err != nil {
		t.Fatalf("ValidateSources: %v", err)
	}
	if len(results) != 1 || results[0].Status != StatusIncomplete || results[0].ErrorCode != CodeUnknownPluginType {
		t.Fatalf("expected incomplete/%s, got %+v", CodeUnknownPluginType, results)
	}
	if SourcesConfigured(results) {
		t.Error("an unknown type must not be configured")
	}
}

// TestRegistry_ValidateSourcesMissingConfigFails proves a missing required config
// key fails runtime validation (fail-closed).
func TestRegistry_ValidateSourcesMissingConfigFails(t *testing.T) {
	reg := NewRegistry()
	p := &testPlugin{kind: SourceManual, declared: []SourceKind{SourceManual}, reqs: []ConfigRequirement{{Key: "token", Secret: true, Required: true}}}
	reg.Register(p)

	results, err := reg.ValidateSources(context.Background(), []SourceConfig{source("s", SourceManual, true, nil)})
	if err != nil {
		t.Fatalf("ValidateSources: %v", err)
	}
	if results[0].Status != StatusIncomplete || results[0].ErrorCode != CodeConfigRequiredMissing {
		t.Fatalf("expected incomplete/%s, got %+v", CodeConfigRequiredMissing, results[0])
	}
}

// TestRegistry_ValidateSourcesCheckContractFailureFails proves the plugin's own
// CheckContract failure fails runtime validation with the check's code.
func TestRegistry_ValidateSourcesCheckContractFailureFails(t *testing.T) {
	reg := NewRegistry()
	p := &testPlugin{
		kind:     SourceManual,
		declared: []SourceKind{SourceManual},
		check: func(context.Context, SourceConfig) []ContractCheck {
			return []ContractCheck{{Name: "custom", Passed: false, Code: "CUSTOM_BAD", Detail: "nope"}}
		},
	}
	reg.Register(p)

	results, err := reg.ValidateSources(context.Background(), []SourceConfig{source("s", SourceManual, true, nil)})
	if err != nil {
		t.Fatalf("ValidateSources: %v", err)
	}
	if results[0].Status != StatusIncomplete || results[0].ErrorCode != "CUSTOM_BAD" {
		t.Fatalf("expected incomplete/CUSTOM_BAD, got %+v", results[0])
	}
}

// TestRegistry_ValidateSourcesDisabledSkippedNonBlocking proves a disabled source
// is skipped and non-blocking, and that SourcesConfigured (unlike ContractsComplete)
// ignores skipped results.
func TestRegistry_ValidateSourcesDisabledSkippedNonBlocking(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&testPlugin{kind: SourceManual, declared: []SourceKind{SourceManual}})

	results, err := reg.ValidateSources(context.Background(), []SourceConfig{source("off", SourceManual, false, nil)})
	if err != nil {
		t.Fatalf("ValidateSources: %v", err)
	}
	if results[0].Status != StatusSkipped {
		t.Fatalf("expected skipped, got %+v", results[0])
	}
	if !SourcesConfigured(results) {
		t.Error("a disabled source must not block the runtime configuration check")
	}
	if ContractsComplete(results) {
		t.Error("ContractsComplete semantics must be unchanged: skipped is not complete")
	}
}
