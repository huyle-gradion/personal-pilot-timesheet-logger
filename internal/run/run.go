// Package run composes the committed S1-S7 packages into the shared deterministic
// workflow that both the CLI and the agent skill call. It owns NO accounting or
// submission logic of its own: it sequences evidence collection, proposal, review,
// editorial synthesis, submission preview/approval/execution, reconciliation, and
// managed export.
//
// It is hermetic by construction: every collaborator (Remote, Editor, Exporter,
// Keychain, Now) is injected, and a nil collaborator disables its stage.
package run

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/editorial"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/export"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/keychain"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/mcp"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/submit"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// Stable, secret-free run codes.
const (
	CodeTimezoneInvalid      = "RUN_PREFLIGHT_TIMEZONE_INVALID"
	CodeKeychainTerminal     = "RUN_PREFLIGHT_KEYCHAIN_TERMINAL"
	CodeKeychainMissing      = "RUN_PREFLIGHT_KEYCHAIN_MISSING"
	CodeConfigurationInvalid = "RUN_PREFLIGHT_CONFIGURATION_INVALID"
	CodeRemoteUnavailable    = "RUN_PREFLIGHT_REMOTE_UNAVAILABLE"
	CodeGitCredential        = "RUN_PREFLIGHT_GIT_CREDENTIAL"
	CodeGitCredentialMissing = "RUN_PREFLIGHT_GIT_CREDENTIAL_MISSING"

	BlockNoActiveApproval  = "no_active_approval"
	BlockApprovalMismatch  = "approval_mismatch"
	BlockRemoteUnavailable = "remote_unavailable"
	// BlockMissingCatalog is returned when a remote is configured but the caller
	// supplied NO classification catalog: submission fails closed BEFORE any write
	// rather than skipping classification validation.
	BlockMissingCatalog = "missing_catalog"
	// BlockStaleCatalog is returned when the supplied catalog is older than the
	// shared gradion.CatalogMaxAge policy: submission fails closed BEFORE any write.
	BlockStaleCatalog = "stale_catalog"

	// BlockUnconfirmMappedRemote is the stable reason for an unconfirm of an
	// interval that is already submitted: the operator must edit the remote entry
	// instead of removing it.
	BlockUnconfirmMappedRemote = "unconfirm_mapped_remote"
	// BlockUnconfirmUnknownInterval is the stable reason for an unconfirm that
	// names no confirmed interval on the day (a typo is never silent).
	BlockUnconfirmUnknownInterval = "unconfirm_unknown_interval"
	// BlockResetSubmittedInterval is the stable reason for a reset refused because
	// a confirmed interval has a remote mapping.
	BlockResetSubmittedInterval = "reset_submitted_interval"
	// BlockResetUnresolved is the stable reason for a reset refused because an
	// unresolved (in_flight) remote operation remains.
	BlockResetUnresolved = "reset_unresolved_submission"
)

// keychainRefPrefix marks a config value that references a Keychain secret rather
// than containing one.
const keychainRefPrefix = "keychain:"

// Remote is the live Gradion surface used by the runner. A nil Remote means the
// run is OFFLINE (no remote reads or writes). *gradion.Client satisfies it.
type Remote interface {
	submit.RemoteObserver
	submit.RemoteWriter
	Initialize(ctx context.Context) (mcp.ServerInfo, error)
	VerifyCapabilities(ctx context.Context) error
	Classifications(ctx context.Context, query string) ([]gradion.Classification, error)
	ClassificationCatalog(ctx context.Context, query string) (gradion.Catalog, error)
}

// Deps are the injected collaborators. A nil Remote/Editor/Exporter disables that
// stage; a nil Keychain uses the default resolver; a nil Now uses the wall clock.
type Deps struct {
	Store    workflow.Store
	Registry *evidence.Registry
	Remote   Remote
	Editor   editorial.Editor
	Exporter export.Exporter
	Keychain keychain.Resolver
	Now      func() time.Time
	// GitToken is an explicit git credential override (tests/CI). Empty in
	// production, where git.ResolveAuthToken resolves it from env/Keychain/gh.
	GitToken string
}

// Config is the non-secret run configuration.
type Config struct {
	Evidence evidence.Config `json:"evidence"`
	// Date optionally pins the run date (YYYY-MM-DD); empty means today in the
	// configured timezone.
	Date string `json:"date,omitempty"`
}

// Runner sequences the shared workflow for one day.
type Runner struct {
	deps   Deps
	cfg    Config
	loc    *time.Location
	locErr error
}

// New returns a Runner. It resolves the configured timezone but does not fail on
// it: Preflight reports an invalid timezone as a structured check.
func New(deps Deps, cfg Config) (*Runner, error) {
	if deps.Store == nil {
		return nil, errors.New("run: nil store")
	}
	if deps.Registry == nil {
		return nil, errors.New("run: nil registry")
	}
	r := &Runner{deps: deps, cfg: cfg}
	r.loc, r.locErr = loadLocation(cfg.Evidence.Timezone)
	return r, nil
}

func loadLocation(tz string) (*time.Location, error) {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("run: invalid timezone %q", tz)
	}
	return loc, nil
}

func (r *Runner) now() time.Time {
	if r.deps.Now != nil {
		return r.deps.Now()
	}
	return time.Now().UTC()
}

// Date returns the run's target date: the configured Date, or today in the
// configured timezone.
func (r *Runner) Date() (string, error) {
	if d := strings.TrimSpace(r.cfg.Date); d != "" {
		return d, nil
	}
	if r.locErr != nil {
		return "", r.locErr
	}
	return r.now().In(r.loc).Format("2006-01-02"), nil
}

func (r *Runner) keychainResolver() keychain.Resolver {
	if r.deps.Keychain != nil {
		return r.deps.Keychain
	}
	return keychain.NewResolver()
}

// dayTime parses a YYYY-MM-DD date in the configured location.
func (r *Runner) dayTime(date string) (time.Time, error) {
	loc := r.loc
	if loc == nil {
		loc = time.UTC
	}
	t, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("run: invalid date %q", date)
	}
	return t, nil
}

func keychainService(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, keychainRefPrefix) {
		return "", false
	}
	svc := strings.TrimSpace(strings.TrimPrefix(v, keychainRefPrefix))
	if svc == "" {
		return "", false
	}
	return svc, true
}

func keychainCode(err error) string {
	var opErr *keychain.OperatingError
	if errors.As(err, &opErr) && opErr.Code != "" {
		return opErr.Code
	}
	return CodeKeychainTerminal
}
