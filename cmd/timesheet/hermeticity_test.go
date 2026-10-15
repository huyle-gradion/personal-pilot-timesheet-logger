package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/keychain"
)

// GATE-HERMETICITY-1 tripwire.
//
// The standard battery (go test ./...) must never read the owner's live macOS
// Keychain. TestMain installs a tripwire resolver as the package-level
// keychain.DefaultResolver for the whole test binary. Any test that reaches
// keychain.Lookup — directly, or through internal/git's ResolveAuthToken — without
// first installing its own hermetic resolver hits this trap instead of the live
// `security` binary. The trap:
//
//	(a) fails the offending test AT THE POINT OF USE with a distinctive,
//	    secret-free terminal error, so the failure names the test that leaked; and
//	(b) counts invocations, so TestMain fails the whole package loudly afterwards
//	    even if a leak happens to be swallowed by a tolerant code path.
//
// Tests that legitimately need a resolved credential install their own hermetic
// fake via keychain.SetDefaultResolver (the existing seam); the guided/bulk tests
// that merely run git preflight carry a hermetic token via run.Deps.GitToken
// instead. Production is unaffected: nothing here runs outside `go test`.
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
