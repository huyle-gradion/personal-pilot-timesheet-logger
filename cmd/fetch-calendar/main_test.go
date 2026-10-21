package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/calendar"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/keychain"
)

const sampleICS = `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:event-1@google.com
DTSTART:20261006T020000Z
DTEND:20261006T033000Z
SUMMARY:Architecture Review
DESCRIPTION:Review Go migration and #42
LOCATION:Room 404
END:VEVENT
END:VCALENDAR`

func writeTempFile(t *testing.T, dir, filename string, content []byte) string {
	t.Helper()
	p := filepath.Join(dir, filename)
	if err := os.WriteFile(p, content, 0600); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	return p
}

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

func TestRun_SuccessJSON(t *testing.T) {
	tmpDir := t.TempDir()
	icsPath := writeTempFile(t, tmpDir, "valid.ics", []byte(sampleICS))

	var stdout, stderr bytes.Buffer
	ctx := context.Background()
	args := []string{
		"-file", icsPath,
		"-date", "2026-10-06",
		"-tz", "Asia/Ho_Chi_Minh",
		"-no-cache",
	}

	code := run(ctx, args, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d. Stderr: %s", code, stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, `"title": "Architecture Review"`) {
		t.Errorf("expected title in JSON output, got: %s", out)
	}
	if !strings.Contains(out, `"source": "calendar"`) {
		t.Errorf("expected source in JSON output, got: %s", out)
	}
	if !strings.Contains(out, `"status": "CONFIRMED"`) {
		t.Errorf("expected status in JSON output, got: %s", out)
	}
	if !strings.Contains(out, `"timestamp": "2026-10-06T09:00:00+07:00"`) {
		t.Errorf("expected RFC 3339 timestamp in JSON output, got: %s", out)
	}
	if !strings.Contains(out, `"endTime": "2026-10-06T10:30:00+07:00"`) {
		t.Errorf("expected RFC 3339 endTime in JSON output, got: %s", out)
	}
	if !strings.Contains(out, "#42") {
		t.Errorf("expected PR #42 in JSON output, got: %s", out)
	}
	if !strings.Contains(out, `"id": "gcal-event-1@google.com"`) {
		t.Errorf("expected event ID in JSON output, got: %s", out)
	}
}

func TestRun_DeclinedFilterWithFlag(t *testing.T) {
	icsContent := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:event-declined@google.com
DTSTART:20261006T020000Z
DTEND:20261006T030000Z
SUMMARY:Declined Standup
ATTENDEE;PARTSTAT=DECLINED:mailto:huy.leminh@gradion.com
END:VEVENT
BEGIN:VEVENT
UID:event-active@google.com
DTSTART:20261006T040000Z
DTEND:20261006T050000Z
SUMMARY:Active Sync
ATTENDEE;PARTSTAT=ACCEPTED:mailto:huy.leminh@gradion.com
END:VEVENT
END:VCALENDAR`

	tmpDir := t.TempDir()
	icsPath := writeTempFile(t, tmpDir, "test.ics", []byte(icsContent))

	var stdout, stderr bytes.Buffer
	ctx := context.Background()
	args := []string{
		"-file", icsPath,
		"-date", "2026-10-06",
		"-user-email", "huy.leminh@gradion.com",
		"-no-cache",
	}

	code := run(ctx, args, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d. Stderr: %s", code, stderr.String())
	}

	out := stdout.String()
	if strings.Contains(out, "Declined Standup") {
		t.Errorf("expected declined meeting to be filtered, found in output: %s", out)
	}
	if !strings.Contains(out, "Active Sync") {
		t.Errorf("expected active meeting in output, got: %s", out)
	}
}

func TestRun_SkipTransparentFlag(t *testing.T) {
	icsContent := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:event-transp@google.com
DTSTART:20261006T020000Z
DTEND:20261006T030000Z
SUMMARY:Transparent Reminder
TRANSP:TRANSPARENT
END:VEVENT
BEGIN:VEVENT
UID:event-busy@google.com
DTSTART:20261006T040000Z
DTEND:20261006T050000Z
SUMMARY:Busy Meeting
TRANSP:OPAQUE
END:VEVENT
END:VCALENDAR`

	tmpDir := t.TempDir()
	icsPath := writeTempFile(t, tmpDir, "transp.ics", []byte(icsContent))

	// Without flag: both are present and transparent is flagged
	var stdout1, stderr1 bytes.Buffer
	code1 := run(context.Background(), []string{"-file", icsPath, "-date", "2026-10-06", "-no-cache"}, &stdout1, &stderr1)
	if code1 != 0 {
		t.Fatalf("expected code 0, got %d", code1)
	}
	out1 := stdout1.String()
	if !strings.Contains(out1, "Transparent Reminder") || !strings.Contains(out1, `"transparent": true`) {
		t.Errorf("expected transparent event to be flagged with transparent: true, got: %s", out1)
	}

	// With -skip-transparent flag: transparent is omitted
	var stdout2, stderr2 bytes.Buffer
	code2 := run(context.Background(), []string{"-file", icsPath, "-date", "2026-10-06", "-skip-transparent", "-no-cache"}, &stdout2, &stderr2)
	if code2 != 0 {
		t.Fatalf("expected code 0, got %d", code2)
	}
	out2 := stdout2.String()
	if strings.Contains(out2, "Transparent Reminder") {
		t.Errorf("expected transparent event to be skipped with -skip-transparent, got: %s", out2)
	}
	if !strings.Contains(out2, "Busy Meeting") {
		t.Errorf("expected busy meeting in output, got: %s", out2)
	}
}

func TestRun_SuccessEntryMarkdown(t *testing.T) {
	tmpDir := t.TempDir()
	icsPath := writeTempFile(t, tmpDir, "valid.ics", []byte(sampleICS))

	var stdout, stderr bytes.Buffer
	ctx := context.Background()
	args := []string{
		"-file", icsPath,
		"-date", "2026-10-06",
		"-tz", "Asia/Ho_Chi_Minh",
		"-format", "entry",
		"-no-cache",
	}

	code := run(ctx, args, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d. Stderr: %s", code, stderr.String())
	}

	out := stdout.String()
	if !strings.Contains(out, "### Entry 1:") {
		t.Errorf("expected Section 3.2 markdown format, got: %s", out)
	}
	if !strings.Contains(out, "PRs: #42") {
		t.Errorf("expected PRs in markdown output, got: %s", out)
	}
}

func TestRun_MissingSource(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ctx := context.Background()

	// Ensure env var is unset for this test
	origEnv := os.Getenv("GCAL_ICS_URL")
	_ = os.Unsetenv("GCAL_ICS_URL")
	defer func() {
		if origEnv != "" {
			_ = os.Setenv("GCAL_ICS_URL", origEnv)
		}
	}()

	// Inject an absent-item resolver so a real calendar_feed_url is never read.
	restore := keychain.SetDefaultResolver(fakeResolver{err: keychainNotFound()})
	t.Cleanup(restore)

	code := run(ctx, []string{"-date", "2026-10-06"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("expected exit code %d (EX_USAGE) for missing source, got %d (stderr: %s)", ExitUsage, code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Pre-flight check failed:") {
		t.Errorf("expected pre-flight banner in stderr, got: %s", stderr.String())
	}
	// The guide must name every resolution path, including the Keychain command
	// and where the secret address comes from.
	for _, want := range []string{
		"calendar_feed_url",
		"export GCAL_ICS_URL",
		"-url=",
		"-file=",
		"Integrate calendar",
		"Secret address in iCal format",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("expected guide to contain %q, got: %s", want, stderr.String())
		}
	}
	if strings.Contains(stderr.String(), "CAL_CLI_MISSING_SOURCE") {
		t.Errorf("missing configuration must be guided pre-flight, not a bare error, got: %s", stderr.String())
	}
}

// TestRun_EnvSourceHandoffToFetcher proves the source resolved by pre-flight is
// the one the fetcher actually uses. Nothing is passed on the command line, so
// ExplicitSource is empty: only cfg.Source = preflight.Source can make the run
// succeed, and the output must carry the env-named fixture's event.
//
// Red mechanism: reverting the handoff to `cfg.Source = cfg.ExplicitSource`
// yields an empty Source, so FetchICS fails with CAL_ERR_EMPTY_SOURCE and run()
// returns ExitErr (1) with no stdout — this test fails on the exit-code and
// output assertions.
func TestRun_EnvSourceHandoffToFetcher(t *testing.T) {
	icsContent := `BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:event-env-handoff@google.com
DTSTART:20261006T060000Z
DTEND:20261006T070000Z
SUMMARY:Env Feed Handoff
END:VEVENT
END:VCALENDAR`

	tmpDir := t.TempDir()
	icsPath := writeTempFile(t, tmpDir, "env-feed.ics", []byte(icsContent))
	t.Setenv("GCAL_ICS_URL", icsPath)

	// Absent-item resolver: the Keychain is never the source here, and a real
	// calendar_feed_url item is never read.
	restore := keychain.SetDefaultResolver(fakeResolver{err: keychainNotFound()})
	t.Cleanup(restore)

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"-date", "2026-10-06", "-no-cache"}, &stdout, &stderr)
	if code != ExitOK {
		t.Fatalf("expected exit code %d from the env-resolved source, got %d (stderr: %s)", ExitOK, code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Env Feed Handoff") {
		t.Fatalf("expected the fetched fixture's event in output, got: %s", stdout.String())
	}
}

func TestPreflight_KeychainMissNotReady(t *testing.T) {
	t.Setenv("GCAL_ICS_URL", "")

	// An absent-item resolver must fall through to "missing", guided pre-flight.
	restore := keychain.SetDefaultResolver(fakeResolver{err: keychainNotFound()})
	t.Cleanup(restore)

	status := calendar.CheckPreflight(context.Background(), "")
	if status.Ready {
		t.Fatal("expected Ready=false on a Keychain miss")
	}
	if status.Source != "" {
		t.Fatalf("expected no Source on a Keychain miss, got %q", status.Source)
	}
	found := false
	for _, k := range status.MissingKeys {
		if k == "GCAL_ICS_URL" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected GCAL_ICS_URL in MissingKeys, got %v", status.MissingKeys)
	}
	if !strings.Contains(status.Guide, "calendar_feed_url") {
		t.Fatalf("expected the Keychain fix in the guide, got: %s", status.Guide)
	}
}

// TestPreflight_KeychainTerminalErrorSurfaced proves a non-NotFound Keychain
// failure is surfaced as a typed, secret-free pre-flight failure.
func TestPreflight_KeychainTerminalErrorSurfaced(t *testing.T) {
	t.Setenv("GCAL_ICS_URL", "")

	const secret = "super-secret-feed-token-value"
	restore := keychain.SetDefaultResolver(fakeResolver{
		// A resolver that (wrongly) returns both a secret and an error must not
		// let the secret reach diagnostics.
		secret: secret,
		err: &keychain.OperatingError{
			Outcome: keychain.OutcomeTerminal,
			Code:    "KEYCHAIN_ERR_TIMEOUT",
			Message: "keychain lookup exceeded deadline",
		},
	})
	t.Cleanup(restore)

	status := calendar.CheckPreflight(context.Background(), "")
	if status.Ready {
		t.Fatal("expected Ready=false on a terminal Keychain failure")
	}
	if !strings.Contains(status.Error, "KEYCHAIN_ERR_TIMEOUT") {
		t.Fatalf("expected typed keychain code, got: %v", status.Error)
	}
	if strings.Contains(status.Guide+status.Error, secret) {
		t.Fatalf("secret leaked into diagnostics: %v", status.Error)
	}
}

// TestRun_KeychainTerminalErrorStopsBeforeFetch proves run() reports the
// Keychain failure as a guided pre-flight failure (EX_USAGE) and never attempts
// a calendar fetch.
func TestRun_KeychainTerminalErrorStopsBeforeFetch(t *testing.T) {
	t.Setenv("GCAL_ICS_URL", "")

	restore := keychain.SetDefaultResolver(fakeResolver{
		err: &keychain.OperatingError{
			Outcome: keychain.OutcomeTerminal,
			Code:    "KEYCHAIN_ERR_ACCESS",
			Message: "keychain access denied",
		},
	})
	t.Cleanup(restore)

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"-date", "2026-10-06"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("expected exit code %d (EX_USAGE), got %d (stderr: %s)", ExitUsage, code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "Pre-flight check failed:") {
		t.Fatalf("expected pre-flight banner in stderr, got: %s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "KEYCHAIN_ERR_ACCESS") {
		t.Fatalf("expected keychain failure in stderr, got: %s", stderr.String())
	}
	if strings.Contains(stdout.String(), "BEGIN:VCALENDAR") {
		t.Fatalf("must not fetch calendar after a terminal keychain failure")
	}
}

func TestRun_InvalidDate(t *testing.T) {
	tmpDir := t.TempDir()
	icsPath := writeTempFile(t, tmpDir, "valid.ics", []byte(sampleICS))

	var stdout, stderr bytes.Buffer
	ctx := context.Background()
	args := []string{
		"-file", icsPath,
		"-date", "invalid-date-format",
	}

	code := run(ctx, args, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit code 1 for invalid date, got %d", code)
	}
	if !strings.Contains(stderr.String(), ErrCodeInvalidDate) {
		t.Errorf("expected %s in stderr, got: %s", ErrCodeInvalidDate, stderr.String())
	}
}

func TestRun_InvalidTimezone(t *testing.T) {
	tmpDir := t.TempDir()
	icsPath := writeTempFile(t, tmpDir, "valid.ics", []byte(sampleICS))

	var stdout, stderr bytes.Buffer
	ctx := context.Background()
	args := []string{
		"-file", icsPath,
		"-date", "2026-10-06",
		"-tz", "Invalid/Timezone_Name",
	}

	code := run(ctx, args, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit code 1 for invalid timezone, got %d", code)
	}
	if !strings.Contains(stderr.String(), ErrCodeInvalidTimezone) {
		t.Errorf("expected %s in stderr, got: %s", ErrCodeInvalidTimezone, stderr.String())
	}
}

func TestRun_InvalidFormat(t *testing.T) {
	tmpDir := t.TempDir()
	icsPath := writeTempFile(t, tmpDir, "valid.ics", []byte(sampleICS))

	var stdout, stderr bytes.Buffer
	ctx := context.Background()
	args := []string{
		"-file", icsPath,
		"-format", "xml",
	}

	code := run(ctx, args, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit code 1 for invalid format, got %d", code)
	}
	if !strings.Contains(stderr.String(), ErrCodeInvalidFormat) {
		t.Errorf("expected %s in stderr, got: %s", ErrCodeInvalidFormat, stderr.String())
	}
}

func TestRun_BoundExceeded(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ctx := context.Background()

	// No -file/-url source: keep Keychain tooling off PATH so a real
	// calendar_feed_url item is never read.
	t.Setenv("GCAL_ICS_URL", "")
	t.Setenv("PATH", t.TempDir())

	// Test oversized date argument
	hugeDate := strings.Repeat("2026-", 10)
	code := run(ctx, []string{"-date", hugeDate}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit code 1 for bound exceeded, got %d", code)
	}
	if !strings.Contains(stderr.String(), ErrCodeBoundExceeded) {
		t.Errorf("expected %s in stderr, got: %s", ErrCodeBoundExceeded, stderr.String())
	}
}

func TestRun_FlagSyntaxError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ctx := context.Background()

	code := run(ctx, []string{"-unknown-flag-xyz"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit code 2 for flag parse error, got %d", code)
	}
}

func TestHelpOutput_NoSecretLeak(t *testing.T) {
	origEnv := os.Getenv("GCAL_ICS_URL")
	_ = os.Setenv("GCAL_ICS_URL", "https://calendar.google.com/calendar/ical/user/private-supersecrettoken123/basic.ics")
	defer func() {
		if origEnv != "" {
			_ = os.Setenv("GCAL_ICS_URL", origEnv)
		} else {
			_ = os.Unsetenv("GCAL_ICS_URL")
		}
	}()

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"-h"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("expected exit code 2 for -h, got %d", code)
	}
	if strings.Contains(stderr.String(), "supersecrettoken123") {
		t.Fatalf("stderr leaked secret token: %s", stderr.String())
	}
}

func TestLoadEnv_SymlinkRejected(t *testing.T) {
	tmpDir := t.TempDir()
	realPath := writeTempFile(t, tmpDir, "real.env", []byte("TEST_KEY=123\n"))
	symlinkPath := filepath.Join(tmpDir, "symlink.env")
	if err := os.Symlink(realPath, symlinkPath); err != nil {
		t.Fatal(err)
	}

	err := loadEnv(symlinkPath)
	if err == nil {
		t.Fatal("expected error on symlink env file, got nil")
	}
	if !strings.Contains(err.Error(), ErrCodeEnvFailed) {
		t.Errorf("expected %s, got %v", ErrCodeEnvFailed, err)
	}
}

func TestLoadEnv_BoundExceeded(t *testing.T) {
	tmpDir := t.TempDir()
	largeContent := make([]byte, MaxEnvFileSizeBytes+10)
	largePath := writeTempFile(t, tmpDir, "large.env", largeContent)

	err := loadEnv(largePath)
	if err == nil {
		t.Fatal("expected error on oversized env file, got nil")
	}
	if !strings.Contains(err.Error(), ErrCodeBoundExceeded) {
		t.Errorf("expected %s, got %v", ErrCodeBoundExceeded, err)
	}
}

func TestAssertion_Subprocess(t *testing.T) {
	if os.Getenv("BE_FATAL_ASSERTION_CLI") == "1" {
		// Deliberately trigger invariant violation: nil writer
		run(context.Background(), nil, nil, nil)
		return
	}
	if os.Getenv("BE_FATAL_ASSERTION_CLI_CONFIG") == "1" {
		// Deliberately trigger invariant violation: nil config
		Assert(false, AssertConfigNonNil)
		return
	}

	tests := []struct {
		name    string
		envKey  string
		wantTag string
	}{
		{"WritersNonNil", "BE_FATAL_ASSERTION_CLI=1", AssertWritersNonNil},
		{"ConfigNonNil", "BE_FATAL_ASSERTION_CLI_CONFIG=1", AssertConfigNonNil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestAssertion_Subprocess")
			cmd.Env = append(os.Environ(), tt.envKey)
			out, err := cmd.CombinedOutput()

			if err == nil {
				t.Fatalf("expected fatal assertion to exit with error, but exited successfully. Output: %s", string(out))
			}

			if !strings.Contains(string(out), tt.wantTag) {
				t.Errorf("expected output to contain invariant %s, got: %s", tt.wantTag, string(out))
			}
		})
	}
}
