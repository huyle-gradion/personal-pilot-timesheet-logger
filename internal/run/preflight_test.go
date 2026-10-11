package run

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence/plugins"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/keychain"
)

func TestPreflight_IncompleteConfigurationRefuses(t *testing.T) {
	store := newStore(t)
	reg := evidence.NewRegistry()
	plug := &fakePlugin{
		kind:     evidence.SourceLocalGit,
		required: []evidence.ConfigRequirement{{Key: "repo", Required: true}},
	}
	reg.Register(plug)
	sources := []evidence.SourceConfig{{ID: "git", Type: evidence.SourceLocalGit, Enabled: true, Config: map[string]string{}}}

	r := newRunner(t, Deps{Store: store, Registry: reg, Keychain: &fakeKeychain{}}, Config{
		Evidence: evidence.Config{Timezone: "UTC", Sources: sources},
	})
	res, err := r.Preflight(ctx, PreflightOptions{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if res.OK {
		t.Fatalf("an invalid configuration must refuse preflight: %+v", res.Checks)
	}
	if !hasCheck(res.Checks, "configuration", CodeConfigurationInvalid) {
		t.Fatalf("want a configuration failure, got %+v", res.Checks)
	}
	if plug.liveCalls != 0 {
		t.Fatalf("refusal must run ZERO live collection, got %d", plug.liveCalls)
	}
}

func TestPreflight_PassingContractsAndRemote(t *testing.T) {
	store := newStore(t)
	reg := evidence.NewRegistry()
	plug := &fakePlugin{kind: evidence.SourceCalendar, ev: nil}
	reg.Register(plug)
	remote := newFakeRemote()
	sources := []evidence.SourceConfig{{ID: "cal", Type: evidence.SourceCalendar, Enabled: true}}

	r := newRunner(t, Deps{Store: store, Registry: reg, Remote: remote, Keychain: &fakeKeychain{}}, Config{
		Evidence: evidence.Config{Timezone: "UTC", Sources: sources},
	})
	res, err := r.Preflight(ctx, PreflightOptions{Deep: true})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if !res.OK {
		t.Fatalf("want OK, got %+v", res.Checks)
	}
	if remote.initCalls != 1 || remote.verifyCalls != 1 {
		t.Fatalf("want one initialize and one verify, got %d/%d", remote.initCalls, remote.verifyCalls)
	}
}

func TestPreflight_InvalidTimezoneRefused(t *testing.T) {
	store := newStore(t)
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Keychain: &fakeKeychain{}}, Config{
		Evidence: evidence.Config{Timezone: "Not/AZone"},
	})
	res, err := r.Preflight(ctx, PreflightOptions{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if res.OK || !hasCheck(res.Checks, "timezone", CodeTimezoneInvalid) {
		t.Fatalf("want a timezone failure, got %+v", res.Checks)
	}
}

// S8L1-3: a wholly absent GitHub credential must be flagged for an enabled git
// source.
func TestPreflight_MissingGitCredentialRefused(t *testing.T) {
	store := newStore(t)
	// Make every credential source unavailable: empty env, absent keychain item,
	// and no `gh` on PATH.
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("PATH", t.TempDir())
	restore := keychain.SetDefaultResolver(&fakeKeychain{})
	t.Cleanup(restore)

	sources := []evidence.SourceConfig{{ID: "git", Type: evidence.SourceLocalGit, Enabled: true}}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Keychain: &fakeKeychain{}}, Config{
		Evidence: evidence.Config{Timezone: "UTC", Sources: sources},
	})
	r.deps.GitToken = "" // force resolution to fall through to an absent credential

	res, err := r.Preflight(ctx, PreflightOptions{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if res.OK || !hasCheck(res.Checks, "git_credential", CodeGitCredentialMissing) {
		t.Fatalf("want a missing git credential failure, got %+v", res.Checks)
	}
}

// F2 (CONFIG-COLLECT-1): a mode:"local" git source needs NO remote credential.
// An absent GitHub credential must not fail the git_credential check for it, and
// preflight must still pass (configuration is satisfied by the injected plugin).
func TestPreflight_LocalGitModeNeedsNoCredential(t *testing.T) {
	store := newStore(t)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("PATH", t.TempDir())
	restore := keychain.SetDefaultResolver(&fakeKeychain{})
	t.Cleanup(restore)

	reg := evidence.NewRegistry()
	reg.Register(&fakePlugin{kind: plugins.TypeGit}) // nil checks -> contract passes
	sources := []evidence.SourceConfig{{
		ID: "git", Type: plugins.TypeGit, Enabled: true,
		Config: map[string]string{plugins.GitConfigMode: plugins.GitModeLocal},
	}}
	r := newRunner(t, Deps{Store: store, Registry: reg, Keychain: &fakeKeychain{}}, Config{
		Evidence: evidence.Config{Timezone: "UTC", Sources: sources},
	})
	r.deps.GitToken = "" // force resolution to fall through to an absent credential

	res, err := r.Preflight(ctx, PreflightOptions{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if hasCheck(res.Checks, "git_credential", CodeGitCredentialMissing) {
		t.Fatalf("a mode:local git source must not require a credential, got %+v", res.Checks)
	}
	if !res.OK {
		t.Fatalf("want OK for a local-only git source, got %+v", res.Checks)
	}
}

// F2 (CONFIG-COLLECT-1): a default/both git source still requires a remote
// credential (the remote path must not be weakened).
func TestPreflight_RemoteGitModeStillNeedsCredential(t *testing.T) {
	store := newStore(t)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("PATH", t.TempDir())
	restore := keychain.SetDefaultResolver(&fakeKeychain{})
	t.Cleanup(restore)

	reg := evidence.NewRegistry()
	reg.Register(&fakePlugin{kind: plugins.TypeGit})
	sources := []evidence.SourceConfig{{
		ID: "git", Type: plugins.TypeGit, Enabled: true,
		Config: map[string]string{plugins.GitConfigRepos: "acme/app"}, // default mode = both
	}}
	r := newRunner(t, Deps{Store: store, Registry: reg, Keychain: &fakeKeychain{}}, Config{
		Evidence: evidence.Config{Timezone: "UTC", Sources: sources},
	})
	r.deps.GitToken = ""

	res, err := r.Preflight(ctx, PreflightOptions{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if res.OK || !hasCheck(res.Checks, "git_credential", CodeGitCredentialMissing) {
		t.Fatalf("a default/both git source must still require a credential, got %+v", res.Checks)
	}
}

// PREFLIGHT-RUNTIME-1: a mode:"local" git source passes preflight OFFLINE with NO
// git credential and NO fixture. It uses the REAL git plugin, proving the runtime
// path does not require a fixture (and does not stat the checkout path).
func TestPreflight_LocalGitModeOfflineWithoutFixture(t *testing.T) {
	store := newStore(t)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("PATH", t.TempDir())
	restore := keychain.SetDefaultResolver(&fakeKeychain{})
	t.Cleanup(restore)

	reg := evidence.NewRegistry()
	reg.Register(plugins.NewGitPlugin(nil, nil)) // real git plugin; NO fixture
	sources := []evidence.SourceConfig{{
		ID: "git", Type: plugins.TypeGit, Enabled: true,
		Config: map[string]string{
			plugins.GitConfigMode:       plugins.GitModeLocal,
			plugins.GitConfigLocalRepos: "does-not-need-to-exist", // no filesystem check
		},
	}}
	r := newRunner(t, Deps{Store: store, Registry: reg, Keychain: &fakeKeychain{}}, Config{
		Evidence: evidence.Config{Timezone: "UTC", Sources: sources},
	})
	r.deps.GitToken = ""

	res, err := r.Preflight(ctx, PreflightOptions{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if !res.OK {
		t.Fatalf("local-only git must pass preflight offline without a credential or fixture, got %+v", res.Checks)
	}
	if !hasPassingCheck(res.Checks, "configuration") {
		t.Fatalf("want a passing configuration check, got %+v", res.Checks)
	}
}

// PREFLIGHT-RUNTIME-1: --deep validates ALL configured sources (including disabled,
// reported skipped) WITHOUT fixtures.
func TestPreflight_DeepValidatesAllSourcesWithoutFixtures(t *testing.T) {
	store := newStore(t)
	reg := evidence.NewRegistry()
	reg.Register(plugins.NewGitPlugin(nil, nil))
	sources := []evidence.SourceConfig{
		{ID: "git-on", Type: plugins.TypeGit, Enabled: true, Config: map[string]string{plugins.GitConfigMode: plugins.GitModeLocal}},
		{ID: "git-off", Type: plugins.TypeGit, Enabled: false},
	}
	r := newRunner(t, Deps{Store: store, Registry: reg, Keychain: &fakeKeychain{}}, Config{
		Evidence: evidence.Config{Timezone: "UTC", Sources: sources},
	})

	res, err := r.Preflight(ctx, PreflightOptions{Deep: true})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if !res.OK {
		t.Fatalf("deep preflight must pass without fixtures, got %+v", res.Checks)
	}
	if !hasPassingCheck(res.Checks, "configuration") {
		t.Fatalf("want a passing configuration check, got %+v", res.Checks)
	}
	sawSkipped := false
	for _, sr := range res.Sources {
		if sr.SourceID == "git-off" && sr.Status == evidence.StatusSkipped {
			sawSkipped = true
		}
	}
	if !sawSkipped {
		t.Fatalf("deep validation must include the disabled source as skipped, got %+v", res.Sources)
	}
}

func hasPassingCheck(checks []Check, name string) bool {
	for _, c := range checks {
		if c.Name == name && c.Passed {
			return true
		}
	}
	return false
}

// S8L1-7: a remote Initialize/VerifyCapabilities failure refuses the run and there
// is NO REST fallback (zero writes).
func TestPreflight_RemoteFailureRefuses(t *testing.T) {
	store := newStore(t)

	initFail := newFakeRemote()
	initFail.initErr = errors.New("mcp down")
	r1 := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: initFail, Keychain: &fakeKeychain{}}, Config{
		Evidence: evidence.Config{Timezone: "UTC"},
	})
	res1, err := r1.Preflight(ctx, PreflightOptions{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if res1.OK || !hasCheck(res1.Checks, "remote", CodeRemoteUnavailable) {
		t.Fatalf("want a remote failure, got %+v", res1.Checks)
	}

	verifyFail := newFakeRemote()
	verifyFail.verifyErr = errors.New("tool unavailable")
	r2 := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: verifyFail, Keychain: &fakeKeychain{}}, Config{
		Evidence: evidence.Config{Timezone: "UTC"},
	})
	res2, err := r2.Preflight(ctx, PreflightOptions{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if res2.OK || !hasCheck(res2.Checks, "remote", CodeRemoteUnavailable) {
		t.Fatalf("want a verify failure, got %+v", res2.Checks)
	}
	if verifyFail.creates != 0 || verifyFail.edits != 0 {
		t.Fatalf("there is NO REST fallback: zero writes expected")
	}
}

// F2 (PREFLIGHT-RUNTIME-1): a DISABLED source with an absent keychain: reference
// must NOT fail preflight (disabled sources are skipped and never block).
func TestPreflight_DisabledSourceCredentialSkipped(t *testing.T) {
	store := newStore(t)
	sources := []evidence.SourceConfig{{
		ID: "cal-off", Type: evidence.SourceCalendar, Enabled: false,
		Config: map[string]string{"feed_ref": "keychain:calendar_feed_url"},
	}}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Keychain: &fakeKeychain{}}, Config{
		Evidence: evidence.Config{Timezone: "UTC", Sources: sources},
	})

	res, err := r.Preflight(ctx, PreflightOptions{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if !res.OK {
		t.Fatalf("a disabled source must not block preflight, got %+v", res.Checks)
	}
	if hasCheck(res.Checks, "credential_cal-off_feed_ref", CodeKeychainMissing) {
		t.Fatalf("a disabled source's credential must not be checked, got %+v", res.Checks)
	}
}

// F2 (PREFLIGHT-RUNTIME-1): fail-closed for ENABLED sources — an enabled source
// with an absent keychain: reference still reports CodeKeychainMissing.
func TestPreflight_EnabledSourceMissingCredentialFails(t *testing.T) {
	store := newStore(t)
	reg := evidence.NewRegistry()
	reg.Register(plugins.NewCalendarPlugin())
	sources := []evidence.SourceConfig{{
		ID: "cal-on", Type: evidence.SourceCalendar, Enabled: true,
		Config: map[string]string{"feed_ref": "keychain:calendar_feed_url"},
	}}
	r := newRunner(t, Deps{Store: store, Registry: reg, Keychain: &fakeKeychain{}}, Config{
		Evidence: evidence.Config{Timezone: "UTC", Sources: sources},
	})

	res, err := r.Preflight(ctx, PreflightOptions{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if res.OK || !hasCheck(res.Checks, "credential_cal-on_feed_ref", CodeKeychainMissing) {
		t.Fatalf("an enabled source with an absent ref must fail %s, got %+v", CodeKeychainMissing, res.Checks)
	}
}

// A terminal Keychain failure is reported with a typed code, stops the run before
// ANY remote request, and never leaks a secret.
func TestPreflight_TerminalKeychainFailureIsSecretFree(t *testing.T) {
	store := newStore(t)
	remote := newFakeRemote()
	kc := &fakeKeychain{err: &keychain.OperatingError{
		Outcome: keychain.OutcomeTerminal,
		Code:    "KEYCHAIN_ERR_TIMEOUT",
		Message: "keychain lookup exceeded deadline",
	}}
	sources := []evidence.SourceConfig{{
		ID: "cal", Type: evidence.SourceCalendar, Enabled: true,
		Config: map[string]string{"feed_ref": "keychain:calendar_feed_url"},
	}}
	r := newRunner(t, Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote, Keychain: kc}, Config{
		Evidence: evidence.Config{Timezone: "UTC", Sources: sources},
	})

	res, err := r.Preflight(ctx, PreflightOptions{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if res.OK {
		t.Fatalf("a terminal keychain failure must refuse preflight")
	}
	if !hasCheck(res.Checks, "credential_cal_feed_ref", CodeKeychainTerminal) {
		t.Fatalf("want a typed keychain failure, got %+v", res.Checks)
	}
	if remote.initCalls != 0 || remote.verifyCalls != 0 || remote.listCalls != 0 || remote.creates != 0 {
		t.Fatalf("a terminal failure must make ZERO remote requests")
	}
	blob, _ := json.Marshal(res)
	if strings.Contains(string(blob), "SECRET") || strings.Contains(string(blob), "feed_url_secret") {
		t.Fatalf("no secret may appear in the output: %s", blob)
	}
}
