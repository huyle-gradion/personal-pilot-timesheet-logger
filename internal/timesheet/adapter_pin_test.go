package timesheet

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// TestAdapterAppVersionPinned guards the negotiated identity pairing between
// the Pi adapter (pi/transport.ts APP_VERSION) and this package's AppVersion
// constant (model.go). The transport fails closed on a handshake mismatch, so
// silent drift here breaks every adapter-launched child. This test parses the
// TypeScript literal and fails the build if the two constants diverge.
func TestAdapterAppVersionPinned(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed to locate this test file")
	}
	// Repo root is two directories above internal/timesheet.
	transportPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "pi", "transport.ts")

	raw, err := os.ReadFile(transportPath)
	if err != nil {
		t.Fatalf("read adapter transport: %v", err)
	}

	re := regexp.MustCompile(`(?m)^export const APP_VERSION = "([^"]+)";`)
	m := re.FindSubmatch(raw)
	if m == nil {
		t.Fatalf("no APP_VERSION literal found in pi/transport.ts")
	}
	adapterVersion := string(m[1])

	if adapterVersion != AppVersion {
		t.Fatalf("adapter APP_VERSION %q diverges from internal AppVersion %q — update pi/transport.ts and internal/timesheet/model.go together", adapterVersion, AppVersion)
	}
}
