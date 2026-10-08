package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

	code := run(ctx, []string{"-date", "2026-10-06"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit code 1 for missing source, got %d", code)
	}
	if !strings.Contains(stderr.String(), ErrCodeMissingSource) {
		t.Errorf("expected %s in stderr, got: %s", ErrCodeMissingSource, stderr.String())
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
