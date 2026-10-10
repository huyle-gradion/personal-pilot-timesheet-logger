package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/api"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/keychain"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/storage"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

// fakeKeychainResolver is an in-process Resolver test double; it avoids
// spawning the real `security` binary (and its subprocess/deadline flakiness).
type fakeKeychainResolver struct {
	secret string
	err    error
}

func (f fakeKeychainResolver) Lookup(context.Context, string, string) (string, error) {
	return f.secret, f.err
}

func createTestStorageRecord(t *testing.T, dir, date string, blocks []timesheet.TimeBlock) {
	t.Helper()
	store, err := storage.NewJSONFileStore(dir)
	if err != nil {
		t.Fatalf("failed to create test store: %v", err)
	}

	record := storage.TimesheetRecord{
		Date:       date,
		UpdatedAt:  time.Now().UTC(),
		Version:    1,
		TotalHours: 4.0,
		Blocks:     blocks,
	}

	if err := store.SaveDay(context.Background(), record); err != nil {
		t.Fatalf("failed to save test day: %v", err)
	}
}

func TestCLI_MissingToken_ExitUsage(t *testing.T) {
	t.Setenv("TIMESHEET_API_TOKEN", "")
	// Deterministic: inject an in-process resolver reporting an absent item.
	t.Cleanup(keychain.SetDefaultResolver(fakeKeychainResolver{
		err: &keychain.OperatingError{Outcome: keychain.OutcomeNotFound, Code: "KEYCHAIN_ERR_NOT_FOUND", Message: "absent"},
	}))
	var out, errOut bytes.Buffer

	args := []string{"-date=2026-10-08", "-token="}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage (64), got %d (err: %s)", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "missing TIMESHEET_API_TOKEN") {
		t.Errorf("expected missing token error message, got: %s", errOut.String())
	}
}

// TestCLI_KeychainTokenResolution verifies the third token source: with -token
// empty and TIMESHEET_API_TOKEN unset, the token is read from the Keychain via an
// injected in-process resolver and used by the read-only preview.
func TestCLI_KeychainTokenResolution(t *testing.T) {
	t.Cleanup(keychain.SetDefaultResolver(fakeKeychainResolver{secret: "keychain-token-abc123"}))
	t.Setenv("TIMESHEET_API_TOKEN", "")

	tempStorage := t.TempDir()
	tempEnvDir := t.TempDir()
	targetDate := "2026-10-08"
	createTestStorageRecord(t, tempStorage, targetDate, []timesheet.TimeBlock{
		{ID: "block-1", Date: targetDate, StartTime: "09:00", EndTime: "10:00", Description: "Keychain task"},
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer keychain-token-abc123" {
			t.Errorf("expected keychain-derived token, got: %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]api.RemoteEntry{})
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	args := []string{
		"-date=" + targetDate,
		"-dry-run=true",
		"-env=" + filepath.Join(tempEnvDir, "no-such.env"),
		"-storage-dir=" + tempStorage,
		"-base-url=" + server.URL,
	}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK with keychain token, got %d (err: %s)", code, errOut.String())
	}
}

// TestCLI_KeychainTerminalError_ExitErrNoRequest proves a non-NotFound Keychain
// failure stops credential resolution: it is surfaced as a typed, secret-free
// error, returns a non-usage failure code, and performs no remote request.
func TestCLI_KeychainTerminalError_ExitErrNoRequest(t *testing.T) {
	t.Setenv("TIMESHEET_API_TOKEN", "")

	const secret = "gradion-super-secret-token-value"
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
	}))
	defer server.Close()

	t.Cleanup(keychain.SetDefaultResolver(fakeKeychainResolver{
		// Wrongly returning a secret alongside an error must not leak it.
		secret: secret,
		err: &keychain.OperatingError{
			Outcome: keychain.OutcomeTerminal,
			Code:    "KEYCHAIN_ERR_TIMEOUT",
			Message: "keychain lookup exceeded deadline",
		},
	}))

	var out, errOut bytes.Buffer
	args := []string{
		"-date=2026-10-08",
		"-dry-run=true",
		"-env=" + filepath.Join(t.TempDir(), "no-such.env"),
		"-storage-dir=" + t.TempDir(),
		"-base-url=" + server.URL,
	}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitErr {
		t.Fatalf("expected ExitErr on terminal keychain failure, got %d (err: %s)", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), ErrCodeKeychainFailed) {
		t.Errorf("expected %s, got: %s", ErrCodeKeychainFailed, errOut.String())
	}
	if !strings.Contains(errOut.String(), "KEYCHAIN_ERR_TIMEOUT") {
		t.Errorf("expected typed keychain code, got: %s", errOut.String())
	}
	if strings.Contains(errOut.String(), secret) {
		t.Errorf("secret leaked into diagnostics: %s", errOut.String())
	}
	if got := atomic.LoadInt32(&requests); got != 0 {
		t.Errorf("expected zero remote requests on terminal keychain failure, got %d", got)
	}
}

func TestCLI_OversizedToken_ExitUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	oversized := strings.Repeat("A", MaxTokenLengthBytes+10)
	args := []string{"-date=2026-10-08", "-token=" + oversized}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage (64), got %d", code)
	}
	if !strings.Contains(errOut.String(), "token exceeds maximum length") {
		t.Errorf("expected token length error, got: %s", errOut.String())
	}
}

func TestCLI_InvalidDate_ExitUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	args := []string{"-date=2026/10/08", "-token=some-token"}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage (64), got %d (err: %s)", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "invalid -date") {
		t.Errorf("expected invalid date error, got: %s", errOut.String())
	}
}

func TestCLI_StorageNotFound_ExitErr(t *testing.T) {
	tempDir := t.TempDir()
	var out, errOut bytes.Buffer

	args := []string{
		"-date=2026-10-08",
		"-token=test-token",
		"-storage-dir=" + tempDir,
	}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitErr {
		t.Fatalf("expected ExitErr (1), got %d", code)
	}
	if !strings.Contains(errOut.String(), "no timesheet record found") {
		t.Errorf("expected record not found message, got: %s", errOut.String())
	}
}

func TestCLI_StorageEmptyBlocks_ExitErr(t *testing.T) {
	tempDir := t.TempDir()
	createTestStorageRecord(t, tempDir, "2026-10-08", []timesheet.TimeBlock{})

	var out, errOut bytes.Buffer
	args := []string{
		"-date=2026-10-08",
		"-token=test-token",
		"-storage-dir=" + tempDir,
	}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitErr {
		t.Fatalf("expected ExitErr (1), got %d", code)
	}
	if !strings.Contains(errOut.String(), "has 0 time blocks") {
		t.Errorf("expected 0 time blocks message, got: %s", errOut.String())
	}
}

func TestCLI_EnvFileLoading(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env.test")
	envContent := "TIMESHEET_API_TOKEN=token-from-env-file\n"
	if err := os.WriteFile(envPath, []byte(envContent), 0600); err != nil {
		t.Fatalf("failed to write test env file: %v", err)
	}

	t.Setenv("TIMESHEET_API_TOKEN", "")

	targetDate := "2026-10-08"
	createTestStorageRecord(t, tempDir, targetDate, []timesheet.TimeBlock{
		{ID: "block-1", Date: targetDate, StartTime: "09:00", EndTime: "10:00", Description: "Morning standup"},
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token-from-env-file" {
			t.Errorf("expected token from env file, got: %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]api.RemoteEntry{})
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	args := []string{
		"-date=" + targetDate,
		"-dry-run=true",
		"-env=" + envPath,
		"-storage-dir=" + tempDir,
		"-base-url=" + server.URL,
	}

	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d (err: %s)", code, errOut.String())
	}
}

// TestCLI_DryRun_ReadOnlyPreview proves the read-only preview lists remote
// entries and performs ZERO mutations.
func TestCLI_DryRun_ReadOnlyPreview(t *testing.T) {
	tempStorage := t.TempDir()
	targetDate := "2026-10-08"
	createTestStorageRecord(t, tempStorage, targetDate, []timesheet.TimeBlock{
		{ID: "block-1", Date: targetDate, StartTime: "09:00", EndTime: "10:00", Description: "Team standup"},
		{ID: "block-2", Date: targetDate, StartTime: "10:00", EndTime: "12:00", Description: "Implementation"},
	})

	var postCount, patchCount, deleteCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]api.RemoteEntry{{ID: "remote-1", Date: targetDate, StartTime: "09:00", EndTime: "10:00"}})
		case http.MethodPost:
			atomic.AddInt32(&postCount, 1)
		case http.MethodPatch:
			atomic.AddInt32(&patchCount, 1)
		case http.MethodDelete:
			atomic.AddInt32(&deleteCount, 1)
		default:
			t.Errorf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	args := []string{
		"-date=" + targetDate,
		"-dry-run=true",
		"-storage-dir=" + tempStorage,
		"-token=test-token",
		"-base-url=" + server.URL,
	}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d (err: %s)", code, errOut.String())
	}
	if atomic.LoadInt32(&postCount) != 0 || atomic.LoadInt32(&patchCount) != 0 || atomic.LoadInt32(&deleteCount) != 0 {
		t.Errorf("read-only preview must not mutate: POST=%d PATCH=%d DELETE=%d",
			atomic.LoadInt32(&postCount), atomic.LoadInt32(&patchCount), atomic.LoadInt32(&deleteCount))
	}
	if !strings.Contains(out.String(), "Read-only preview") {
		t.Errorf("expected read-only preview in stdout, got: %s", out.String())
	}
}

// TestCLI_NonDryRunFailsClosed proves a submission attempt fails closed with a
// typed, secret-free error and makes ZERO remote requests (the legacy REST write
// path is retired; no flag re-enables it).
func TestCLI_NonDryRunFailsClosed(t *testing.T) {
	tempStorage := t.TempDir()
	targetDate := "2026-10-08"
	createTestStorageRecord(t, tempStorage, targetDate, []timesheet.TimeBlock{
		{ID: "block-1", Date: targetDate, StartTime: "09:00", EndTime: "10:00", Description: "Needs create"},
	})

	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	args := []string{
		"-date=" + targetDate,
		"-dry-run=false",
		"-storage-dir=" + tempStorage,
		"-token=test-token",
		"-base-url=" + server.URL,
	}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitErr {
		t.Fatalf("expected ExitErr (retired REST path), got %d (err: %s)", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), ErrCodeRESTRetired) || !strings.Contains(errOut.String(), ErrMsgRESTRetired) {
		t.Errorf("expected fail-closed retired message, got: %s", errOut.String())
	}
	if got := atomic.LoadInt32(&requests); got != 0 {
		t.Errorf("the retired path must make zero remote requests, got %d", got)
	}
}

func TestCLI_FlagParseError_ExitUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	args := []string{"-unrecognized-flag"}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage, got %d", code)
	}
}

// TestCLI_NoApprovalFlag proves the -approve gate is gone: passing it is a flag
// parse error, so no flag can re-enable submission.
func TestCLI_NoApprovalFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	args := []string{"-date=2026-10-08", "-token=test-token", "-approve"}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage for the removed -approve flag, got %d", code)
	}
}

func TestCLI_LoadEnv_IrregularAndOversized(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Directory as env file -> should be ignored without error
	if err := loadEnv(tempDir); err != nil {
		t.Errorf("expected nil error on directory env path, got: %v", err)
	}

	// 2. Oversized env file -> ignored without error
	oversizedPath := filepath.Join(tempDir, "oversized.env")
	bigContent := make([]byte, MaxEnvFileSizeBytes+10)
	if err := os.WriteFile(oversizedPath, bigContent, 0644); err != nil {
		t.Fatal(err)
	}
	if err := loadEnv(oversizedPath); err != nil {
		t.Errorf("expected nil error on oversized env file, got: %v", err)
	}
}

func TestCLI_SubprocessAssertions(t *testing.T) {
	if os.Getenv("BE_SYNC_CLI_SUBPROCESS") == "1" {
		which := os.Getenv("ASSERT_CASE")
		switch which {
		case "nil_ctx":
			_ = run(nil, []string{}, strings.NewReader(""), os.Stdout, os.Stderr)
		case "nil_out":
			_ = run(context.Background(), []string{}, strings.NewReader(""), nil, os.Stderr)
		case "nil_err_out":
			_ = run(context.Background(), []string{}, strings.NewReader(""), os.Stdout, nil)
		}
		return
	}

	testCases := []struct {
		name       string
		assertCase string
		expected   string
	}{
		{"nil_ctx", "nil_ctx", AssertSyncContextNil},
		{"nil_out", "nil_out", AssertSyncWritersNil},
		{"nil_err_out", "nil_err_out", AssertSyncWritersNil},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=TestCLI_SubprocessAssertions")
			cmd.Env = append(os.Environ(), "BE_SYNC_CLI_SUBPROCESS=1", "ASSERT_CASE="+tc.assertCase)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("expected subprocess to crash with panic, but it succeeded: %s", string(out))
			}
			if !strings.Contains(string(out), tc.expected) {
				t.Fatalf("expected panic output to contain %q, got: %s", tc.expected, string(out))
			}
		})
	}
}

// TestCLI_NoTokenLeakOnFailClosed proves the token never appears in the
// diagnostics of a fail-closed (retired) run.
func TestCLI_NoTokenLeakOnFailClosed(t *testing.T) {
	const token = "gradion-super-secret-token-value"
	tempStorage := t.TempDir()
	targetDate := "2026-10-08"
	createTestStorageRecord(t, tempStorage, targetDate, []timesheet.TimeBlock{
		{ID: "block-1", Date: targetDate, StartTime: "09:00", EndTime: "10:00", Description: "x"},
	})

	var out, errOut bytes.Buffer
	args := []string{"-date=" + targetDate, "-storage-dir=" + tempStorage, "-token=" + token}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitErr {
		t.Fatalf("expected ExitErr, got %d", code)
	}
	if strings.Contains(out.String(), token) || strings.Contains(errOut.String(), token) {
		t.Errorf("token leaked into diagnostics: out=%q err=%q", out.String(), errOut.String())
	}
}
