package evidence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// SourceStatus classifies the outcome of collecting one source.
type SourceStatus string

const (
	StatusComplete   SourceStatus = "complete"
	StatusPartial    SourceStatus = "partial"
	StatusIncomplete SourceStatus = "incomplete"
	StatusSkipped    SourceStatus = "skipped"
)

// Stable registry and contract-check codes. Callers branch on these, never on
// message strings.
const (
	// Registry-level codes.
	CodeUnknownPluginType = "REGISTRY_UNKNOWN_PLUGIN_TYPE"
	// Collection-failure codes.
	CodeSourcePanic    = "SOURCE_PANIC"
	CodeSourceError    = "SOURCE_ERROR"
	CodeSourceTimeout  = "SOURCE_TIMEOUT"
	CodeSourceCanceled = "SOURCE_CANCELED"
	// Contract-check failure codes.
	CodeConfigRequiredMissing = "CONTRACT_CONFIG_REQUIRED_MISSING"
	CodeConfigSecretNotRef    = "CONTRACT_CONFIG_SECRET_NOT_KEYCHAIN_REF"
	CodeDeclaredKindsMismatch = "CONTRACT_DECLARED_KINDS_MISMATCH"
	CodeNondeterministic      = "CONTRACT_NONDETERMINISTIC"
	CodeSecretLeak            = "CONTRACT_SECRET_LEAK"
	CodeCheckFailed           = "CONTRACT_CHECK_FAILED"
)

const (
	// DefaultSourceTimeout bounds a single source's collection. It is a bound,
	// not an availability guarantee.
	DefaultSourceTimeout = 30 * time.Second
	// keychainRefPrefix marks a config value that references a Keychain secret
	// rather than containing one.
	keychainRefPrefix = "keychain:"
)

// errSourcePanic is an internal sentinel used to move a recovered panic out of
// the collecting goroutine without embedding the panic value (which could carry a
// secret) into any output.
var errSourcePanic = errors.New("source panicked")

// ErrCollectionIncomplete is the sentinel wrapped by RequireAcknowledgment.
var ErrCollectionIncomplete = errors.New("evidence collection incomplete")

// ConfigRequirement declares one config key a plugin needs. Secret marks keys
// whose values must be "keychain:<service>" references, never raw secrets.
type ConfigRequirement struct {
	Key      string `json:"key"`
	Secret   bool   `json:"secret"`
	Required bool   `json:"required"`
}

// ContractCheck records the result of one pre-enablement contract check. Detail
// never contains a secret value.
type ContractCheck struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Code   string `json:"code,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// SourceResult is the durable result of collecting one source.
type SourceResult struct {
	SourceID      string          `json:"source_id"`
	Type          SourceKind      `json:"type"`
	Status        SourceStatus    `json:"status"`
	EvidenceCount int             `json:"evidence_count"`
	ErrorCode     string          `json:"error_code,omitempty"`
	Message       string          `json:"message,omitempty"`
	Checks        []ContractCheck `json:"checks,omitempty"`
	Warnings      []string        `json:"warnings,omitempty"`
	StartedAt     time.Time       `json:"started_at,omitempty"`
	FinishedAt    time.Time       `json:"finished_at,omitempty"`
}

// CollectionSummary aggregates per-source results for one date. Complete is true
// only when EVERY enabled source is complete; disabled sources are skipped and
// do not block.
type CollectionSummary struct {
	Date     string         `json:"date"`
	Sources  []SourceResult `json:"sources"`
	Complete bool           `json:"complete"`
}

// CollectMode selects between live collection and contract validation. Contract
// mode tells an adapter to use FIXED, deterministic input (a fixture) so the
// registry can verify stable IDs without touching a live window.
type CollectMode string

const (
	// ModeLive is the default live collection mode.
	ModeLive CollectMode = "live"
	// ModeContract is the fixed-input contract-validation mode.
	ModeContract CollectMode = "contract"
)

// CollectRequest carries the date a collection targets and the collection mode.
type CollectRequest struct {
	Date string      `json:"date"`
	Mode CollectMode `json:"mode,omitempty"`
}

// IsContract reports whether the request asks for fixed-input contract mode.
func (r CollectRequest) IsContract() bool { return r.Mode == ModeContract }

// CollectResult is a plugin's raw output: evidence plus a self-reported result.
// The registry fills SourceID, Type, and timing.
type CollectResult struct {
	Evidence []Evidence   `json:"evidence"`
	Result   SourceResult `json:"result"`
}

// Plugin is a compile-time source adapter. Registry v1 has NO dynamic executable
// loading: a plugin is a Go type registered in-process, so "unknown executables
// are never auto-enabled" is structural. Contract checks are neither a security
// sandbox nor an availability guarantee; they fail a plugin out of a single run,
// they do not contain a hostile one.
//
// Collect MUST honor CollectRequest.IsContract(): in contract mode it uses fixed,
// deterministic input (a fixture) and never touches a live source, so the
// registry can verify stable IDs without doubled live cost or a shifting window.
type Plugin interface {
	Type() SourceKind
	RequiredConfig() []ConfigRequirement
	DeclaredEvidenceKinds() []SourceKind
	CheckContract(ctx context.Context, cfg SourceConfig) []ContractCheck
	Collect(ctx context.Context, cfg SourceConfig, req CollectRequest) (CollectResult, error)
}

// BoundPlugin binds a configured source to its registered plugin.
type BoundPlugin struct {
	Source SourceConfig
	Plugin Plugin
}

// RegistryError reports a config entry that could not be resolved. Unknown plugin
// types are never enabled or executed.
type RegistryError struct {
	SourceID string     `json:"source_id"`
	Type     SourceKind `json:"type"`
	Code     string     `json:"code"`
	Message  string     `json:"message"`
}

func (e RegistryError) Error() string {
	return fmt.Sprintf("registry error [%s] source %q type %q: %s", e.Code, e.SourceID, e.Type, e.Message)
}

// Registry is the compile-time plugin registry.
type Registry struct {
	mu            sync.RWMutex
	plugins       map[SourceKind]Plugin
	SourceTimeout time.Duration
}

// NewRegistry returns an empty registry with the default bounded source timeout.
func NewRegistry() *Registry {
	return &Registry{plugins: map[SourceKind]Plugin{}, SourceTimeout: DefaultSourceTimeout}
}

// Register adds a plugin for its declared type. A nil plugin panics: it is a
// programmer invariant, not a recoverable operating error.
func (r *Registry) Register(p Plugin) {
	if p == nil {
		panic("evidence: Register called with nil plugin")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.plugins[p.Type()] = p
}

func (r *Registry) lookup(t SourceKind) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins[t]
	return p, ok
}

// Resolve binds each ENABLED source to its plugin. A disabled source is not
// resolved (it is skipped at collection). An enabled source whose type has no
// registered plugin yields a RegistryError and is never executed or enabled.
func (r *Registry) Resolve(sources []SourceConfig) ([]BoundPlugin, []RegistryError) {
	var bound []BoundPlugin
	var errs []RegistryError
	for _, s := range sources {
		if !s.Enabled {
			continue
		}
		p, ok := r.lookup(s.Type)
		if !ok {
			errs = append(errs, RegistryError{
				SourceID: s.ID,
				Type:     s.Type,
				Code:     CodeUnknownPluginType,
				Message:  "unknown plugin type; not enabled or executed",
			})
			continue
		}
		bound = append(bound, BoundPlugin{Source: s, Plugin: p})
	}
	return bound, errs
}

// CollectDay runs every enabled plugin in LIVE mode, recovers panics per source,
// and never silently drops a source: every configured source appears in the
// summary. Each enabled source's Collect is invoked EXACTLY ONCE. Determinism is
// a contract-check concern verified by VerifyContracts against fixed input, NOT
// during live collection. A per-source failure becomes an incomplete SourceResult
// with a stable code; it is never returned as the function error. Complete is
// true only when every enabled source is complete. An already-canceled context is
// the only function-level error.
func (r *Registry) CollectDay(ctx context.Context, sources []SourceConfig, req CollectRequest) ([]Evidence, CollectionSummary, error) {
	summary := CollectionSummary{Date: req.Date, Complete: true}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			summary.Complete = false
			return nil, summary, err
		}
	}

	var all []Evidence
	for _, s := range sources {
		ev, sr := r.collectSource(ctx, s, req)
		summary.Sources = append(summary.Sources, sr)
		if s.Enabled && sr.Status != StatusComplete {
			summary.Complete = false
		}
		all = append(all, ev...)
	}
	return all, summary, nil
}

// collectSource is the LIVE collection path for one configured source. It
// invokes the plugin's Collect exactly once and never returns an error: failures
// are encoded in the result. Determinism is NOT checked here (that requires a
// fixed-input re-run owned by VerifyContracts).
func (r *Registry) collectSource(ctx context.Context, s SourceConfig, req CollectRequest) ([]Evidence, SourceResult) {
	now := time.Now().UTC()
	result := SourceResult{SourceID: s.ID, Type: s.Type, StartedAt: now}

	if !s.Enabled {
		return nil, skipResult(result)
	}

	p, ok := r.lookup(s.Type)
	if !ok {
		return nil, failResult(result, nil, CodeUnknownPluginType, "unknown plugin type; not enabled or executed")
	}

	checks := r.preCollectChecks(ctx, p, s)
	if code, name := firstFailure(checks); code != "" {
		return nil, failResult(result, checks, code, "contract check failed: "+name)
	}

	// Exactly one live invocation.
	res, code, msg := r.collectOnce(ctx, p, s, req)
	if code != "" {
		return nil, failResult(result, checks, code, msg)
	}
	// Preserve the source's self-reported warnings even if a later check fails or
	// the source reports incomplete, so a warning is never silently lost.
	result.Warnings = res.Result.Warnings

	checks = append(checks, checkDeclaredKinds(p.DeclaredEvidenceKinds(), res.Evidence))
	checks = append(checks, checkRedaction(s, p.RequiredConfig(), res.Evidence, res.Result, checks))
	if code, name := firstFailure(checks); code != "" {
		return nil, failResult(result, checks, code, "contract check failed: "+name)
	}

	status := res.Result.Status
	if status == "" {
		status = StatusComplete
	}
	if status == StatusIncomplete {
		return nil, failResult(result, checks, CodeSourceError, "source reported incomplete status")
	}

	result.Status = status
	result.EvidenceCount = len(res.Evidence)
	result.Checks = checks
	result.Warnings = res.Result.Warnings
	result.FinishedAt = time.Now().UTC()
	return res.Evidence, result
}

// VerifyContracts runs FIXTURE-BASED behavioral verification for each configured
// source in CONTRACT mode against FIXED input, including the determinism check
// that requires two Collect invocations. The fixture is a TEST INPUT, not runtime
// configuration: this path is for contract/behavioral tests and pre-enablement
// verification of a plugin, NOT for the runtime preflight source check (see
// ValidateSources). It never runs during live collection. Each source yields one
// SourceResult; a failed check yields incomplete with a stable code. An
// already-canceled context is the only function-level error.
func (r *Registry) VerifyContracts(ctx context.Context, sources []SourceConfig) ([]SourceResult, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	results := make([]SourceResult, 0, len(sources))
	for _, s := range sources {
		results = append(results, r.contractSource(ctx, s))
	}
	return results, nil
}

// PreflightContracts runs FIXTURE-BASED contract verification for the ENABLED
// sources and returns per-source results. It is a TEST/contract-only path: it
// requires a fixture and is NOT used for the runtime preflight source check (see
// ValidateSources). A caller must refuse enablement of any source whose status is
// not StatusComplete. Disabled sources are not included. An already-canceled
// context is the only function-level error.
func (r *Registry) PreflightContracts(ctx context.Context, sources []SourceConfig) ([]SourceResult, error) {
	enabled := make([]SourceConfig, 0, len(sources))
	for _, s := range sources {
		if s.Enabled {
			enabled = append(enabled, s)
		}
	}
	return r.VerifyContracts(ctx, enabled)
}

// ValidateSources performs RUNTIME source validation for each configured source:
// it checks config requirements and the plugin's declared contract (CheckContract)
// WITHOUT running a fixture-based Collect, without a determinism re-run, and
// without any network or filesystem-availability claim. A disabled source is
// skipped; an unknown type fails; a failed check yields incomplete with a stable
// code; otherwise the source is complete with zero evidence.
//
// Fixture-based behavioral/determinism verification (VerifyContracts /
// PreflightContracts) is a TEST input, NOT runtime configuration: a fixture is
// never required to validate a source at runtime. An already-canceled context is
// the only function-level error.
func (r *Registry) ValidateSources(ctx context.Context, sources []SourceConfig) ([]SourceResult, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	results := make([]SourceResult, 0, len(sources))
	for _, s := range sources {
		results = append(results, r.validateSource(ctx, s))
	}
	return results, nil
}

// validateSource is the runtime validation path for one configured source. It
// runs the config requirements and the plugin's own CheckContract, and never
// collects (no fixture, no live fetch, no filesystem-availability claim).
func (r *Registry) validateSource(ctx context.Context, s SourceConfig) SourceResult {
	now := time.Now().UTC()
	result := SourceResult{SourceID: s.ID, Type: s.Type, StartedAt: now}

	if !s.Enabled {
		return skipResult(result)
	}

	p, ok := r.lookup(s.Type)
	if !ok {
		return failResult(result, nil, CodeUnknownPluginType, "unknown plugin type; not enabled or executed")
	}

	checks := r.preCollectChecks(ctx, p, s)
	if code, name := firstFailure(checks); code != "" {
		return failResult(result, checks, code, "configuration check failed: "+name)
	}

	result.Status = StatusComplete
	result.EvidenceCount = 0
	result.Checks = checks
	result.FinishedAt = time.Now().UTC()
	return result
}

// SourcesConfigured reports whether every ENABLED source result is complete, for
// the runtime ValidateSources path. A skipped (disabled) source never blocks. It
// deliberately does NOT change ContractsComplete, which treats every
// non-complete result as a failure and is used by the fixture-based paths.
func SourcesConfigured(results []SourceResult) bool {
	for _, r := range results {
		if r.Status == StatusSkipped {
			continue
		}
		if r.Status != StatusComplete {
			return false
		}
	}
	return true
}

// ContractsComplete reports whether every result is complete, so a caller can
// refuse enablement when any contract result is not.
func ContractsComplete(results []SourceResult) bool {
	for _, r := range results {
		if r.Status != StatusComplete {
			return false
		}
	}
	return true
}

// contractSource is the CONTRACT-mode validation path for one configured source.
// It runs the plugin twice on fixed input and evaluates determinism, declared
// kinds, and redaction.
func (r *Registry) contractSource(ctx context.Context, s SourceConfig) SourceResult {
	now := time.Now().UTC()
	result := SourceResult{SourceID: s.ID, Type: s.Type, StartedAt: now}

	if !s.Enabled {
		return skipResult(result)
	}

	p, ok := r.lookup(s.Type)
	if !ok {
		return failResult(result, nil, CodeUnknownPluginType, "unknown plugin type; not enabled or executed")
	}

	checks := r.preCollectChecks(ctx, p, s)
	if code, name := firstFailure(checks); code != "" {
		return failResult(result, checks, code, "contract check failed: "+name)
	}

	// Two fixed-input runs prove stable IDs. This is contract validation, not
	// live collection.
	req := CollectRequest{Mode: ModeContract}
	res1, code1, msg1 := r.collectOnce(ctx, p, s, req)
	if code1 != "" {
		return failResult(result, checks, code1, msg1)
	}
	// Preserve self-reported warnings on the contract path too.
	result.Warnings = res1.Result.Warnings
	res2, code2, _ := r.collectOnce(ctx, p, s, req)
	if code2 != "" {
		return failResult(result, checks, code2, "determinism re-run failed")
	}

	checks = append(checks, checkDeclaredKinds(p.DeclaredEvidenceKinds(), res1.Evidence))
	checks = append(checks, checkDeterminism(res1.Evidence, res2.Evidence))
	checks = append(checks, checkRedaction(s, p.RequiredConfig(), res1.Evidence, res1.Result, checks))
	if code, name := firstFailure(checks); code != "" {
		return failResult(result, checks, code, "contract check failed: "+name)
	}

	result.Status = StatusComplete
	result.EvidenceCount = len(res1.Evidence)
	result.Checks = checks
	result.FinishedAt = time.Now().UTC()
	return result
}

// preCollectChecks runs the config checks and the plugin's own CheckContract.
// Config failure short-circuits before any plugin code runs.
func (r *Registry) preCollectChecks(ctx context.Context, p Plugin, s SourceConfig) []ContractCheck {
	checks := checkConfigRequirements(s, p.RequiredConfig())
	if code, _ := firstFailure(checks); code != "" {
		return checks
	}
	return append(checks, safeCheckContract(ctx, p, s)...)
}

// skipResult marks a disabled source skipped (non-blocking).
func skipResult(result SourceResult) SourceResult {
	result.Status = StatusSkipped
	result.Message = "source disabled"
	result.FinishedAt = time.Now().UTC()
	return result
}

// failResult marks a source incomplete with a stable code.
func failResult(result SourceResult, checks []ContractCheck, code, msg string) SourceResult {
	result.Checks = checks
	result.Status = StatusIncomplete
	result.ErrorCode = code
	result.Message = msg
	result.FinishedAt = time.Now().UTC()
	return result
}

// collectOnce runs a single Collect with the bounded timeout and panic recovery.
// A returned non-empty code means the evidence must be excluded.
func (r *Registry) collectOnce(ctx context.Context, p Plugin, s SourceConfig, req CollectRequest) (CollectResult, string, string) {
	timeout := r.SourceTimeout
	if timeout <= 0 {
		timeout = DefaultSourceTimeout
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type outcome struct {
		res CollectResult
		err error
	}
	ch := make(chan outcome, 1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				ch <- outcome{err: errSourcePanic}
			}
		}()
		res, err := p.Collect(runCtx, s, req)
		ch <- outcome{res: res, err: err}
	}()

	select {
	case o := <-ch:
		if errors.Is(o.err, errSourcePanic) {
			return CollectResult{}, CodeSourcePanic, "source panicked during collection"
		}
		if o.err != nil {
			// Raw error text is never surfaced: it could carry a secret. Only a
			// stable code is retained, with cancellation distinguished from a
			// timeout.
			if errors.Is(o.err, context.Canceled) {
				return CollectResult{}, CodeSourceCanceled, "source collection canceled"
			}
			if errors.Is(o.err, context.DeadlineExceeded) {
				return CollectResult{}, CodeSourceTimeout, "source exceeded bounded timeout"
			}
			return CollectResult{}, CodeSourceError, "source collection failed"
		}
		return o.res, "", ""
	case <-runCtx.Done():
		// Distinguish a caller cancellation from the bounded timeout firing.
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return CollectResult{}, CodeSourceTimeout, "source exceeded bounded timeout"
		}
		return CollectResult{}, CodeSourceCanceled, "source collection canceled"
	}
}

// safeCheckContract invokes the plugin's own CheckContract with panic recovery,
// so a panicking check becomes a failed check rather than crashing collection.
func safeCheckContract(ctx context.Context, p Plugin, s SourceConfig) (checks []ContractCheck) {
	defer func() {
		if rec := recover(); rec != nil {
			checks = append(checks, ContractCheck{
				Name:   "plugin_check_contract",
				Passed: false,
				Code:   CodeCheckFailed,
				Detail: "plugin CheckContract panicked",
			})
		}
	}()
	return p.CheckContract(ctx, s)
}

// checkConfigRequirements verifies required config is present and every secret
// field is a keychain reference. Detail references the KEY only, never the value.
func checkConfigRequirements(s SourceConfig, reqs []ConfigRequirement) []ContractCheck {
	var checks []ContractCheck
	for _, req := range reqs {
		v, ok := s.Config[req.Key]
		present := ok && strings.TrimSpace(v) != ""

		if req.Required && !present {
			checks = append(checks, ContractCheck{
				Name:   "config_required_" + req.Key,
				Passed: false,
				Code:   CodeConfigRequiredMissing,
				Detail: "required config key " + req.Key + " is missing",
			})
			continue
		}
		if req.Secret && present && !strings.HasPrefix(v, keychainRefPrefix) {
			checks = append(checks, ContractCheck{
				Name:   "config_secret_ref_" + req.Key,
				Passed: false,
				Code:   CodeConfigSecretNotRef,
				Detail: "secret config key " + req.Key + " must be a keychain: reference",
			})
			continue
		}
		checks = append(checks, ContractCheck{Name: "config_" + req.Key, Passed: true})
	}
	return checks
}

// checkDeclaredKinds verifies every collected evidence kind is declared by the
// plugin.
func checkDeclaredKinds(declared []SourceKind, ev []Evidence) ContractCheck {
	allowed := make(map[SourceKind]bool, len(declared))
	for _, k := range declared {
		allowed[k] = true
	}
	for _, e := range ev {
		if !allowed[e.Kind] {
			return ContractCheck{
				Name:   "declared_evidence_kinds",
				Passed: false,
				Code:   CodeDeclaredKindsMismatch,
				Detail: "evidence kind " + string(e.Kind) + " is not declared by the plugin",
			}
		}
	}
	return ContractCheck{Name: "declared_evidence_kinds", Passed: true}
}

// checkDeterminism verifies two runs on identical input produced the same set of
// evidence IDs.
func checkDeterminism(a, b []Evidence) ContractCheck {
	if !sameIDSet(a, b) {
		return ContractCheck{
			Name:   "deterministic_ids",
			Passed: false,
			Code:   CodeNondeterministic,
			Detail: "collect produced differing evidence ID sets on identical input",
		}
	}
	return ContractCheck{Name: "deterministic_ids", Passed: true}
}

// checkRedaction verifies that no secret-flagged config value appears in the
// serialized evidence, source result, or checks. Detail names the key only.
func checkRedaction(s SourceConfig, reqs []ConfigRequirement, ev []Evidence, res SourceResult, checks []ContractCheck) ContractCheck {
	artifact, err := json.Marshal(struct {
		Evidence []Evidence      `json:"evidence"`
		Result   SourceResult    `json:"result"`
		Checks   []ContractCheck `json:"checks"`
	}{Evidence: ev, Result: res, Checks: checks})
	if err != nil {
		return ContractCheck{Name: "secret_redaction", Passed: false, Code: CodeSecretLeak, Detail: "failed to serialize output for redaction check"}
	}
	for _, req := range reqs {
		if !req.Secret {
			continue
		}
		v := s.Config[req.Key]
		if v == "" {
			continue
		}
		if strings.Contains(string(artifact), v) {
			return ContractCheck{
				Name:   "secret_redaction",
				Passed: false,
				Code:   CodeSecretLeak,
				Detail: "secret config key " + req.Key + " leaked into serialized output",
			}
		}
	}
	return ContractCheck{Name: "secret_redaction", Passed: true}
}

// firstFailure returns the code and name of the first failing check.
func firstFailure(checks []ContractCheck) (code, name string) {
	for _, c := range checks {
		if !c.Passed {
			return c.Code, c.Name
		}
	}
	return "", ""
}

// sameIDSet reports whether two evidence slices carry the same set of IDs.
func sameIDSet(a, b []Evidence) bool {
	as := idSet(a)
	bs := idSet(b)
	if len(as) != len(bs) {
		return false
	}
	for id := range as {
		if _, ok := bs[id]; !ok {
			return false
		}
	}
	return true
}

func idSet(ev []Evidence) map[string]struct{} {
	s := make(map[string]struct{}, len(ev))
	for _, e := range ev {
		s[e.ID] = struct{}{}
	}
	return s
}

// RequireAcknowledgment returns an error naming every enabled source that is
// neither complete nor skipped and lacks an explicit acknowledgment. A nil or
// empty ack map acknowledges nothing.
func RequireAcknowledgment(sum CollectionSummary, ack map[string]bool) error {
	var missing []string
	for _, sr := range sum.Sources {
		if sr.Status == StatusComplete || sr.Status == StatusSkipped {
			continue
		}
		if ack[sr.SourceID] {
			continue
		}
		missing = append(missing, sr.SourceID)
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("%w: sources require explicit acknowledgment: %s", ErrCollectionIncomplete, strings.Join(missing, ", "))
}
