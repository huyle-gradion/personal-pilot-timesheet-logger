package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/keychain"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/run"
)

// GATE-HERMETICITY-1 tripwire.
//
// Guarantee, stated at proven strength (calibrated; not a universal claim):
//
//	(a) The OBSERVED leak is closed. cmd/timesheet's guided/bulk tests used to make
//	    51 live `security find-generic-password -s gh_auth_token` reads per run; with
//	    the hermetic git token in newGuidedRunner that is now 0, measured with a
//	    PATH-`security` detector over the full battery.
//	(b) The two paths that resolve Keychain secrets in the battery are GUARDED: the
//	    package-level keychain.Lookup (via keychain.DefaultResolver) and
//	    internal/run's keychainResolver when deps.Keychain is nil (which now also
//	    routes through keychain.DefaultResolver). Any test on either path that does
//	    not install its own hermetic resolver hits this trap instead of the live
//	    `security` binary.
//
// This is NOT a claim that the battery is hermetic against ANY conceivable live
// Keychain read; it is that no live read was observed and the resolving seam is
// trapped. TestMain installs the trap as keychain.DefaultResolver for the whole
// test binary. The trap:
//
//	(i) fails the offending test AT THE POINT OF USE with a distinctive, secret-free
//	    terminal error, so the failure names the test that leaked; and
//	(ii) counts invocations, so TestMain fails the whole package loudly afterwards
//	     even if a leak happens to be swallowed by a tolerant code path.
//
// Tests that legitimately need a resolved credential install their own hermetic fake
// via keychain.SetDefaultResolver (the existing seam); the guided/bulk tests that
// merely run git preflight carry a hermetic token via run.Deps.GitToken instead.
// Production is unaffected: nothing here runs outside `go test`.
type gateHermeticityTrap struct{ calls *int64 }

// TripwireCode is the stable, secret-free code the trap returns.
const TripwireCode = "KEYCHAIN_ERR_TEST_TRIPWIRE"

func (t gateHermeticityTrap) Lookup(_ context.Context, service, _ string) (string, error) {
	atomic.AddInt64(t.calls, 1)
	return "", &keychain.OperatingError{
		Outcome: keychain.OutcomeTerminal,
		Code:    TripwireCode,
		Message: fmt.Sprintf("live Keychain read attempted during tests for service %q; "+
			"install a hermetic resolver via keychain.SetDefaultResolver", service),
	}
}

func TestMain(m *testing.M) {
	var calls int64
	restore := keychain.SetDefaultResolver(gateHermeticityTrap{calls: &calls})
	code := m.Run()
	restore()

	if n := atomic.LoadInt64(&calls); n != 0 {
		fmt.Fprintf(os.Stderr,
			"GATE-HERMETICITY-1 FAIL: %d live macOS Keychain read(s) attempted during the "+
				"cmd/timesheet test battery; tests must not read the live Keychain.\n", n)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// TestHermeticityTripwire_FailsClosed is a self-check that the trap fails closed
// with the distinctive, secret-free code. It invokes a fresh trap with a local
// counter directly, so it neither touches the live Keychain nor the package-level
// counter TestMain reads — it is not itself a leak.
func TestHermeticityTripwire_FailsClosed(t *testing.T) {
	_, err := gateHermeticityTrap{calls: new(int64)}.Lookup(context.Background(), "gate-self-check", "")
	if err == nil {
		t.Fatal("the tripwire must fail closed")
	}
	var opErr *keychain.OperatingError
	if !errors.As(err, &opErr) || opErr.Code != TripwireCode {
		t.Fatalf("want tripwire code %q, got %v", TripwireCode, err)
	}
}

// TestPreflight_KeychainRefSourceTrapped proves internal/run's keychainResolver
// (deps.Keychain unset) resolves a `keychain:`-ref source through the package-level
// keychain.DefaultResolver — the same seam TestMain's tripwire occupies — so a live
// read is impossible on that path (review F1). It installs the trap type with a
// LOCAL counter so this deliberate probe does not trip the package-level guard, and
// asserts the trap fired exactly once with its distinctive code. On the pre-F1 code
// keychainResolver built a fresh keychain.NewResolver(), bypassing the seam: this
// assertion would then see 0 trap calls (after a live read).
func TestPreflight_KeychainRefSourceTrapped(t *testing.T) {
	var calls int64
	restore := keychain.SetDefaultResolver(gateHermeticityTrap{calls: &calls})
	t.Cleanup(restore)

	cfg := guidedTestConfig()
	cfg.Evidence.Sources = []evidence.SourceConfig{{
		ID: "cal-on", Type: evidence.SourceCalendar, Enabled: true,
		Config: map[string]string{"feed_ref": "keychain:gh_auth_token"},
	}}
	// newGuidedRunner leaves deps.Keychain nil (the default-resolver path) and sets a
	// hermetic GitToken, isolating the keychain-ref source as the only resolver call.
	r, _ := newGuidedRunner(t, filepath.Join(t.TempDir(), "records"), nil, cfg)

	res, err := r.Preflight(context.Background(), run.PreflightOptions{})
	if err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if n := atomic.LoadInt64(&calls); n != 1 {
		t.Fatalf("the keychain-ref source must resolve through keychain.DefaultResolver "+
			"(the trapped seam); got %d trap call(s)", n)
	}
	if res.OK {
		t.Fatalf("the trap's terminal error must refuse preflight, got %+v", res.Checks)
	}
	if !hasCheck(res.Checks, "credential_cal-on_feed_ref", run.CodeKeychainTerminal, TripwireCode) {
		t.Fatalf("want the trap reported as a terminal keychain failure, got %+v", res.Checks)
	}
}

func hasCheck(checks []run.Check, name, code, detail string) bool {
	for _, c := range checks {
		if c.Name == name && c.Code == code && c.Detail == detail {
			return true
		}
	}
	return false
}
