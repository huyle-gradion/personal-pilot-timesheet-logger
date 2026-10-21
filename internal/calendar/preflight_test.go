package calendar

import (
	"context"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/keychain"
)

// fakeResolver is an in-process keychain.Resolver stand-in: no subprocess is
// spawned and the real macOS Keychain is never consulted.
type fakeResolver struct {
	secret string
	err    error
}

func (f fakeResolver) Lookup(context.Context, string, string) (string, error) {
	return f.secret, f.err
}

// keychainNotFound mirrors the typed error the real resolver returns for an
// absent Keychain item.
func keychainNotFound() error {
	return &keychain.OperatingError{
		Outcome: keychain.OutcomeNotFound,
		Code:    "KEYCHAIN_ERR_NOT_FOUND",
		Message: "absent",
	}
}

// keychainTerminal mirrors a non-NotFound Keychain failure (timeout, access
// failure, malformed output, ...).
func keychainTerminal(code, message string) error {
	return &keychain.OperatingError{
		Outcome: keychain.OutcomeTerminal,
		Code:    code,
		Message: message,
	}
}

// mustNotReadKeychain installs a resolver that fails the test if consulted,
// making "this higher-precedence source wins" assertions airtight.
func mustNotReadKeychain(t *testing.T) {
	t.Helper()
	restore := keychain.SetDefaultResolver(fakeResolver{
		err: &keychain.OperatingError{
			Outcome: keychain.OutcomeTerminal,
			Code:    "TEST_KEYCHAIN_UNEXPECTED",
			Message: "keychain must not be read",
		},
	})
	t.Cleanup(restore)
}

func TestCheckPreflight_ExplicitSourceReady(t *testing.T) {
	const url = "https://example.com/calendar/ical/user/token/basic.ics"
	t.Setenv("GCAL_ICS_URL", "")
	mustNotReadKeychain(t)

	status := CheckPreflight(context.Background(), url)
	if !status.Ready {
		t.Fatalf("expected Ready=true, got false (missing: %v, guide: %s)", status.MissingKeys, status.Guide)
	}
	if len(status.MissingKeys) != 0 {
		t.Errorf("expected 0 missing keys, got %v", status.MissingKeys)
	}
	if status.Source != url {
		t.Errorf("expected Source %q, got %q", url, status.Source)
	}
	if status.Error != "" {
		t.Errorf("expected empty Error, got %q", status.Error)
	}
}

// TestCheckPreflight_FileSourceReady proves a local .ics path is a valid source
// and never reaches the Keychain.
func TestCheckPreflight_FileSourceReady(t *testing.T) {
	t.Setenv("GCAL_ICS_URL", "")
	mustNotReadKeychain(t)

	status := CheckPreflight(context.Background(), "./testdata/event.ics")
	if !status.Ready {
		t.Fatalf("expected Ready=true for a local file, got false (guide: %s)", status.Guide)
	}
	if status.Source != "./testdata/event.ics" {
		t.Errorf("expected Source to be the file path, got %q", status.Source)
	}
}

// TestCheckPreflight_EnvSourceReady proves GCAL_ICS_URL is honoured, and that
// the env var outranks the Keychain (which is never read).
func TestCheckPreflight_EnvSourceReady(t *testing.T) {
	const url = "https://example.com/calendar/ical/env/token/basic.ics"
	t.Setenv("GCAL_ICS_URL", url)
	mustNotReadKeychain(t)

	status := CheckPreflight(context.Background(), "")
	if !status.Ready {
		t.Fatalf("expected Ready=true from env, got false (guide: %s)", status.Guide)
	}
	if status.Source != url {
		t.Errorf("expected Source %q from env, got %q", url, status.Source)
	}
}

// TestCheckPreflight_ExplicitBeatsEnv proves -file/-url beats the environment.
func TestCheckPreflight_ExplicitBeatsEnv(t *testing.T) {
	t.Setenv("GCAL_ICS_URL", "https://example.com/env.ics")
	mustNotReadKeychain(t)

	const explicit = "https://example.com/explicit.ics"
	status := CheckPreflight(context.Background(), explicit)
	if !status.Ready || status.Source != explicit {
		t.Fatalf("expected explicit source %q to win, got ready=%v source=%q", explicit, status.Ready, status.Source)
	}
}

// TestCheckPreflight_KeychainSourceReady proves the Keychain is the last
// resolution step and its value is returned as Source.
func TestCheckPreflight_KeychainSourceReady(t *testing.T) {
	t.Setenv("GCAL_ICS_URL", "")

	const url = "https://example.com/calendar/ical/keychain/token/basic.ics"
	restore := keychain.SetDefaultResolver(fakeResolver{secret: url})
	t.Cleanup(restore)

	status := CheckPreflight(context.Background(), "")
	if !status.Ready {
		t.Fatalf("expected Ready=true from Keychain, got false (guide: %s)", status.Guide)
	}
	if status.Source != url {
		t.Errorf("expected Source %q from Keychain, got %q", url, status.Source)
	}
}

// TestCheckPreflight_MissingSource proves an absent item guides the user to the
// Keychain command, the env var, the flags, and the exact Google Calendar
// location of the secret address.
func TestCheckPreflight_MissingSource(t *testing.T) {
	t.Setenv("GCAL_ICS_URL", "")
	restore := keychain.SetDefaultResolver(fakeResolver{err: keychainNotFound()})
	t.Cleanup(restore)

	status := CheckPreflight(context.Background(), "")
	if status.Ready {
		t.Fatal("expected Ready=false when the source is missing, got true")
	}
	if status.Source != "" {
		t.Errorf("expected no Source when not ready, got %q", status.Source)
	}
	if status.Error != "" {
		t.Errorf("expected no Error for a genuine NotFound, got %q", status.Error)
	}

	foundKey := false
	for _, k := range status.MissingKeys {
		if k == "GCAL_ICS_URL" {
			foundKey = true
		}
	}
	if !foundKey {
		t.Errorf("expected GCAL_ICS_URL in MissingKeys, got %v", status.MissingKeys)
	}

	wantContains := []string{
		"security add-generic-password -s calendar_feed_url -a \"$USER\" -l calendar_feed_url -w",
		"never place it on the command line",
		"export GCAL_ICS_URL",
		"-url=",
		"-file=",
		"Integrate calendar",
		"Secret address in iCal format",
	}
	for _, want := range wantContains {
		if !strings.Contains(status.Guide, want) {
			t.Errorf("expected guide to contain %q, got:\n%s", want, status.Guide)
		}
	}
}

// TestCheckPreflight_KeychainTerminalErrorNotReady proves a non-NotFound
// Keychain failure is surfaced as a typed, secret-free error and does not
// silently degrade to "missing source".
func TestCheckPreflight_KeychainTerminalErrorNotReady(t *testing.T) {
	t.Setenv("GCAL_ICS_URL", "")

	const secret = "super-secret-feed-token-value"
	restore := keychain.SetDefaultResolver(fakeResolver{
		// A resolver that (wrongly) returns both a secret and an error must not
		// let the secret reach diagnostics.
		secret: secret,
		err:    keychainTerminal("KEYCHAIN_ERR_TIMEOUT", "keychain lookup exceeded deadline"),
	})
	t.Cleanup(restore)

	status := CheckPreflight(context.Background(), "")
	if status.Ready {
		t.Fatal("expected Ready=false on a terminal Keychain failure, got true")
	}
	if status.Source != "" {
		t.Errorf("expected no Source on a terminal Keychain failure, got %q", status.Source)
	}
	if !strings.Contains(status.Error, "KEYCHAIN_ERR_TIMEOUT") {
		t.Errorf("expected typed Keychain code in Error, got %q", status.Error)
	}
	if !strings.Contains(status.Guide, "lookup failed") {
		t.Errorf("expected a lookup-failure explanation in the guide, got:\n%s", status.Guide)
	}
	if strings.Contains(status.Guide+status.Error, secret) {
		t.Fatalf("secret leaked into diagnostics: guide=%q error=%q", status.Guide, status.Error)
	}
}

// TestResolveSource_KeychainNotFoundIsMissing proves only a genuine NotFound
// falls through to "missing" (empty source, no error).
func TestResolveSource_KeychainNotFoundIsMissing(t *testing.T) {
	t.Setenv("GCAL_ICS_URL", "")
	restore := keychain.SetDefaultResolver(fakeResolver{err: keychainNotFound()})
	t.Cleanup(restore)

	src, err := ResolveSource(context.Background(), "")
	if err != nil {
		t.Fatalf("expected no error for a Keychain NotFound, got %v", err)
	}
	if src != "" {
		t.Fatalf("expected empty source for a Keychain NotFound, got %q", src)
	}
}

// TestResolveSource_KeychainTerminalErrorStopsResolution proves a non-NotFound
// failure stops resolution instead of falling through to "missing".
func TestResolveSource_KeychainTerminalErrorStopsResolution(t *testing.T) {
	t.Setenv("GCAL_ICS_URL", "")
	restore := keychain.SetDefaultResolver(fakeResolver{
		err: keychainTerminal("KEYCHAIN_ERR_ACCESS", "keychain access denied"),
	})
	t.Cleanup(restore)

	src, err := ResolveSource(context.Background(), "")
	if err == nil {
		t.Fatalf("expected terminal Keychain error to stop resolution, got source %q", src)
	}
	if src != "" {
		t.Fatalf("expected no source on a terminal Keychain error, got %q", src)
	}
	if !strings.Contains(err.Error(), "KEYCHAIN_ERR_ACCESS") {
		t.Fatalf("expected typed Keychain code in error, got: %v", err)
	}
}
