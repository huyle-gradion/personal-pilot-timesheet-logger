package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
// empty and TIMESHEET_API_TOKEN unset, the token is read from the Keychain via
// an injected in-process resolver.
func TestCLI_KeychainTokenResolution(t *testing.T) {
	t.Cleanup(keychain.SetDefaultResolver(fakeKeychainResolver{secret: "keychain-token-abc123"}))
	t.Setenv("TIMESHEET_API_TOKEN", "")

	tempStorage := t.TempDir()
	tempMarkdown := t.TempDir()
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
		"-markdown-dir=" + tempMarkdown,
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
		"-markdown-dir=" + t.TempDir(),
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

// TestCLI_UncertainWrite_408_StopsRun proves HTTP 408 on a non-idempotent write
// is treated as an uncertain outcome: one attempt, no retry, no subsequent
// writes, and an unresolved (partial) result.
func TestCLI_UncertainWrite_408_StopsRun(t *testing.T) {
	tempStorage := t.TempDir()
	tempMarkdown := t.TempDir()
	targetDate := "2026-10-08"
	createTestStorageRecord(t, tempStorage, targetDate, []timesheet.TimeBlock{
		{ID: "block-1", Date: targetDate, StartTime: "09:00", EndTime: "10:00", Description: "Create one"},
		{ID: "block-2", Date: targetDate, StartTime: "10:00", EndTime: "11:00", Description: "Create two"},
	})

	var postCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]api.RemoteEntry{})
		case http.MethodPost:
			atomic.AddInt32(&postCount, 1)
			w.WriteHeader(http.StatusRequestTimeout)
			_, _ = w.Write([]byte("timeout"))
		}
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	args := []string{
		"-date=" + targetDate,
		"-dry-run=false",
		"-approve",
		"-storage-dir=" + tempStorage,
		"-markdown-dir=" + tempMarkdown,
		"-token=test-token",
		"-base-url=" + server.URL,
	}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitErr {
		t.Fatalf("expected ExitErr on 408 uncertain write, got %d (err: %s)", code, errOut.String())
	}
	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Errorf("expected exactly 1 POST attempt on 408, got %d", got)
	}
	content, err := os.ReadFile(filepath.Join(tempMarkdown, targetDate+".md"))
	if err != nil {
		t.Fatalf("expected markdown record: %v", err)
	}
	if !strings.Contains(string(content), "Partial Submission (Unresolved)") {
		t.Errorf("expected unresolved status, got: %s", string(content))
	}
	if strings.Contains(string(content), "Create two") {
		t.Errorf("never-dispatched planned item must not be rendered, got: %s", string(content))
	}
}

// TestCLI_NonDryRunRequiresApproval verifies that a non-dry-run without
// -approve performs zero remote mutations and returns ExitUsage.
func TestCLI_NonDryRunRequiresApproval(t *testing.T) {
	tempStorage := t.TempDir()
	tempMarkdown := t.TempDir()
	targetDate := "2026-10-08"
	createTestStorageRecord(t, tempStorage, targetDate, []timesheet.TimeBlock{
		{ID: "block-1", Date: targetDate, StartTime: "09:00", EndTime: "10:00", Description: "Needs create"},
	})

	var postCount, patchCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]api.RemoteEntry{})
		case http.MethodPost:
			atomic.AddInt32(&postCount, 1)
		case http.MethodPatch:
			atomic.AddInt32(&patchCount, 1)
		}
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	args := []string{
		"-date=" + targetDate,
		"-dry-run=false",
		"-storage-dir=" + tempStorage,
		"-markdown-dir=" + tempMarkdown,
		"-token=test-token",
		"-base-url=" + server.URL,
	}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage without -approve, got %d (err: %s)", code, errOut.String())
	}
	if atomic.LoadInt32(&postCount) != 0 || atomic.LoadInt32(&patchCount) != 0 {
		t.Errorf("expected zero mutations without -approve, got POST=%d PATCH=%d",
			atomic.LoadInt32(&postCount), atomic.LoadInt32(&patchCount))
	}
	if !strings.Contains(out.String(), "-approve") {
		t.Errorf("expected approval instruction in output, got: %s", out.String())
	}
	// No markdown record should be produced for an unapproved run.
	if _, err := os.Stat(filepath.Join(tempMarkdown, targetDate+".md")); !os.IsNotExist(err) {
		t.Errorf("expected no markdown record for unapproved run")
	}
}

// TestCLI_UncertainWrite_StopsAndUnresolved verifies that a non-idempotent
// write that fails ambiguously (HTTP 500) is attempted exactly once, stops the
// submission immediately with no further writes, and reports unresolved.
func TestCLI_UncertainWrite_StopsAndUnresolved(t *testing.T) {
	tempStorage := t.TempDir()
	tempMarkdown := t.TempDir()
	targetDate := "2026-10-08"
	createTestStorageRecord(t, tempStorage, targetDate, []timesheet.TimeBlock{
		{ID: "block-1", Date: targetDate, StartTime: "09:00", EndTime: "10:00", Description: "Create one"},
		{ID: "block-2", Date: targetDate, StartTime: "10:00", EndTime: "11:00", Description: "Create two"},
	})

	var postCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]api.RemoteEntry{})
		case http.MethodPost:
			atomic.AddInt32(&postCount, 1)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("server error"))
		}
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	args := []string{
		"-date=" + targetDate,
		"-dry-run=false",
		"-approve",
		"-storage-dir=" + tempStorage,
		"-markdown-dir=" + tempMarkdown,
		"-token=test-token",
		"-base-url=" + server.URL,
	}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitErr {
		t.Fatalf("expected ExitErr on uncertain write, got %d (err: %s)", code, errOut.String())
	}
	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Errorf("expected exactly 1 POST attempt (no retry, no subsequent writes), got %d", got)
	}
	if !strings.Contains(errOut.String(), "uncertain") || !strings.Contains(errOut.String(), "reconciled") {
		t.Errorf("expected unresolved reconciliation message, got: %s", errOut.String())
	}
	content, err := os.ReadFile(filepath.Join(tempMarkdown, targetDate+".md"))
	if err != nil {
		t.Fatalf("expected markdown record for unresolved run: %v", err)
	}
	if !strings.Contains(string(content), "Partial Submission (Unresolved)") {
		t.Errorf("expected unresolved status in markdown, got: %s", string(content))
	}
	// F2: only ACTUAL outcomes are rendered. The second create was never
	// dispatched and must not appear.
	if !strings.Contains(string(content), "| uncertain |") {
		t.Errorf("expected the uncertain write to be recorded, got: %s", string(content))
	}
	if strings.Contains(string(content), "Create two") {
		t.Errorf("never-dispatched planned item must not be rendered, got: %s", string(content))
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
	envContent := "TIMESHEET_API_TOKEN=token-from-env-file\nDEFAULT_CLASSIFICATION_ID=custom-classification-id\n"
	if err := os.WriteFile(envPath, []byte(envContent), 0600); err != nil {
		t.Fatalf("failed to write test env file: %v", err)
	}

	t.Setenv("TIMESHEET_API_TOKEN", "")
	t.Setenv("DEFAULT_CLASSIFICATION_ID", "")

	targetDate := "2026-10-08"
	createTestStorageRecord(t, tempDir, targetDate, []timesheet.TimeBlock{
		{
			ID:          "block-1",
			Date:        targetDate,
			StartTime:   "09:00",
			EndTime:     "10:00",
			Description: "Morning standup",
		},
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
		"-markdown-dir=" + tempDir,
		"-base-url=" + server.URL,
	}

	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d (err: %s)", code, errOut.String())
	}
	if !strings.Contains(out.String(), "custom-classification-id") {
		t.Errorf("expected custom classification ID from env file in output, got: %s", out.String())
	}
}

func TestCLI_DryRun_ZeroMutations(t *testing.T) {
	tempStorage := t.TempDir()
	tempMarkdown := t.TempDir()
	targetDate := "2026-10-08"

	blocks := []timesheet.TimeBlock{
		{
			ID:          "block-1",
			Date:        targetDate,
			StartTime:   "09:00",
			EndTime:     "10:00",
			Description: "Team standup and planning",
		},
		{
			ID:          "block-2",
			Date:        targetDate,
			StartTime:   "10:00",
			EndTime:     "12:00",
			Description: "Core architecture implementation",
		},
	}
	createTestStorageRecord(t, tempStorage, targetDate, blocks)

	var postCount, patchCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]api.RemoteEntry{
				{
					ID:               "remote-1",
					Date:             targetDate,
					StartTime:        "09:00",
					EndTime:          "10:00",
					ClassificationID: api.DefaultClassificationID,
					Description:      "Team standup and planning", // Identical
				},
			})
		case http.MethodPost:
			atomic.AddInt32(&postCount, 1)
			w.WriteHeader(http.StatusCreated)
		case http.MethodPatch:
			atomic.AddInt32(&patchCount, 1)
			w.WriteHeader(http.StatusOK)
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
		"-markdown-dir=" + tempMarkdown,
		"-token=test-token",
		"-base-url=" + server.URL,
	}

	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK (0), got %d (err: %s)", code, errOut.String())
	}

	// Verify ZERO network mutations in dry-run mode
	if atomic.LoadInt32(&postCount) != 0 {
		t.Errorf("expected 0 POST calls during dry-run, got %d", atomic.LoadInt32(&postCount))
	}
	if atomic.LoadInt32(&patchCount) != 0 {
		t.Errorf("expected 0 PATCH calls during dry-run, got %d", atomic.LoadInt32(&patchCount))
	}

	// Verify stdout contains plan table and dry-run summary
	stdout := out.String()
	if !strings.Contains(stdout, "DRY-RUN PREVIEW") {
		t.Errorf("expected DRY-RUN PREVIEW in stdout, got: %s", stdout)
	}
	if !strings.Contains(stdout, "No remote modifications executed") {
		t.Errorf("expected acknowledgement message in stdout, got: %s", stdout)
	}

	// Verify markdown file was generated
	mdFile := filepath.Join(tempMarkdown, targetDate+".md")
	content, err := os.ReadFile(mdFile)
	if err != nil {
		t.Fatalf("failed to read generated markdown: %v", err)
	}
	if !strings.Contains(string(content), "Dry-Run Preview") {
		t.Errorf("expected Dry-Run status in markdown, got: %s", string(content))
	}
}

func TestCLI_LiveSync_MutationsAndIdempotency(t *testing.T) {
	tempStorage := t.TempDir()
	tempMarkdown := t.TempDir()
	targetDate := "2026-10-08"

	blocks := []timesheet.TimeBlock{
		{
			ID:          "block-1",
			Date:        targetDate,
			StartTime:   "09:00",
			EndTime:     "10:00",
			Description: "Sprint planning", // Matches remote-1
		},
		{
			ID:          "block-2",
			Date:        targetDate,
			StartTime:   "10:00",
			EndTime:     "12:00",
			Description: "Updated feature description", // Modified vs remote-2
		},
		{
			ID:          "block-3",
			Date:        targetDate,
			StartTime:   "13:00",
			EndTime:     "15:00",
			Description: "New afternoon task", // Needs POST
		},
	}
	createTestStorageRecord(t, tempStorage, targetDate, blocks)

	// Simulated remote database
	remoteDB := map[string]api.RemoteEntry{
		"remote-1": {
			ID:               "remote-1",
			Date:             targetDate,
			StartTime:        "09:00",
			EndTime:          "10:00",
			ClassificationID: api.DefaultClassificationID,
			Description:      "Sprint planning",
		},
		"remote-2": {
			ID:               "remote-2",
			Date:             targetDate,
			StartTime:        "10:00",
			EndTime:          "12:00",
			ClassificationID: api.DefaultClassificationID,
			Description:      "Old feature description",
		},
	}

	var postCount, patchCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			var list []api.RemoteEntry
			for _, v := range remoteDB {
				list = append(list, v)
			}
			_ = json.NewEncoder(w).Encode(list)
		case http.MethodPost:
			atomic.AddInt32(&postCount, 1)
			var req api.CreateEntryRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			newID := fmt.Sprintf("created-%d", atomic.LoadInt32(&postCount))
			entry := api.RemoteEntry{
				ID:               newID,
				Date:             req.Date,
				StartTime:        req.StartTime,
				EndTime:          req.EndTime,
				ClassificationID: req.ClassificationID,
				Description:      req.Description,
			}
			remoteDB[newID] = entry
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(entry)
		case http.MethodPatch:
			atomic.AddInt32(&patchCount, 1)
			id := filepath.Base(r.URL.Path)
			var req api.PatchEntryRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if existing, ok := remoteDB[id]; ok {
				existing.Description = req.DTO.Description
				remoteDB[id] = existing
				w.WriteHeader(http.StatusOK)
				_ = json.NewEncoder(w).Encode(existing)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		}
	}))
	defer server.Close()

	// --- RUN 1: Live synchronization ---
	var out1, errOut1 bytes.Buffer
	args := []string{
		"-date=" + targetDate,
		"-dry-run=false",
		"-approve",
		"-storage-dir=" + tempStorage,
		"-markdown-dir=" + tempMarkdown,
		"-token=test-token",
		"-base-url=" + server.URL,
	}

	code1 := run(context.Background(), args, strings.NewReader(""), &out1, &errOut1)
	if code1 != ExitOK {
		t.Fatalf("expected ExitOK on run 1, got %d (err: %s)", code1, errOut1.String())
	}

	if atomic.LoadInt32(&postCount) != 1 {
		t.Errorf("expected 1 POST call on run 1, got %d", atomic.LoadInt32(&postCount))
	}
	if atomic.LoadInt32(&patchCount) != 1 {
		t.Errorf("expected 1 PATCH call on run 1, got %d", atomic.LoadInt32(&patchCount))
	}

	// Verify markdown output
	mdFile := filepath.Join(tempMarkdown, targetDate+".md")
	content, err := os.ReadFile(mdFile)
	if err != nil {
		t.Fatalf("failed to read generated markdown: %v", err)
	}
	if !strings.Contains(string(content), "**Status**: Synchronized") {
		t.Errorf("expected Synchronized status in markdown, got: %s", string(content))
	}
	// F2: successes persist the stable remote ID inside the Description cell.
	if !strings.Contains(string(content), "[remote: created-1]") {
		t.Errorf("expected created remote ID in markdown, got: %s", string(content))
	}
	if !strings.Contains(string(content), "[remote: remote-2]") {
		t.Errorf("expected updated remote ID in markdown, got: %s", string(content))
	}

	// --- RUN 2: Re-running must produce ZERO duplicate entries (DoD 3.3 Idempotency) ---
	var out2, errOut2 bytes.Buffer
	code2 := run(context.Background(), args, strings.NewReader(""), &out2, &errOut2)
	if code2 != ExitOK {
		t.Fatalf("expected ExitOK on run 2, got %d (err: %s)", code2, errOut2.String())
	}

	// Mutations count should NOT have increased!
	if atomic.LoadInt32(&postCount) != 1 {
		t.Errorf("idempotency violation: expected POST count to remain 1, got %d", atomic.LoadInt32(&postCount))
	}
	if atomic.LoadInt32(&patchCount) != 1 {
		t.Errorf("idempotency violation: expected PATCH count to remain 1, got %d", atomic.LoadInt32(&patchCount))
	}

	if !strings.Contains(out2.String(), "0 created, 0 updated, 3 skipped") {
		t.Errorf("expected 0 created, 0 updated, 3 skipped on re-run, got:\n%s", out2.String())
	}
}

func TestCLI_MutationErrors(t *testing.T) {
	tempStorage := t.TempDir()
	tempMarkdown := t.TempDir()
	targetDate := "2026-10-08"

	blocks := []timesheet.TimeBlock{
		{
			ID:          "block-1",
			Date:        targetDate,
			StartTime:   "09:00",
			EndTime:     "10:00",
			Description: "Task that will fail on post",
		},
		{
			ID:          "block-2",
			Date:        targetDate,
			StartTime:   "10:00",
			EndTime:     "11:00",
			Description: "Task that will fail on patch",
		},
	}
	createTestStorageRecord(t, tempStorage, targetDate, blocks)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]api.RemoteEntry{
				{
					ID:          "rem-2",
					Date:        targetDate,
					StartTime:   "10:00",
					EndTime:     "11:00",
					Description: "Old desc",
				},
			})
		case http.MethodPost:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad request"))
		case http.MethodPatch:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("not found"))
		}
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	args := []string{
		"-date=" + targetDate,
		"-dry-run=false",
		"-approve",
		"-storage-dir=" + tempStorage,
		"-markdown-dir=" + tempMarkdown,
		"-token=test-token",
		"-base-url=" + server.URL,
	}

	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitErr {
		t.Fatalf("expected ExitErr on terminal mutation failures, got %d", code)
	}
	if !strings.Contains(errOut.String(), "one or more mutations failed") {
		t.Errorf("expected mutation failure warning, got: %s", errOut.String())
	}

	// Terminal failures must be reported honestly, not as Synchronized.
	content, err := os.ReadFile(filepath.Join(tempMarkdown, targetDate+".md"))
	if err != nil {
		t.Fatalf("expected markdown record for failed run: %v", err)
	}
	if !strings.Contains(string(content), "**Status**: Partial Submission (Failed)") {
		t.Errorf("expected Partial Submission (Failed) status, got: %s", string(content))
	}
	if strings.Contains(string(content), "**Status**: Synchronized") {
		t.Errorf("terminal failures must not be reported as Synchronized, got: %s", string(content))
	}
	if !strings.Contains(string(content), "| failed |") {
		t.Errorf("expected actual 'failed' outcomes in markdown, got: %s", string(content))
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

func TestCLI_MarkdownDisabled(t *testing.T) {
	tempStorage := t.TempDir()
	tempMarkdown := t.TempDir()
	targetDate := "2026-10-08"

	blocks := []timesheet.TimeBlock{
		{
			ID:          "block-1",
			Date:        targetDate,
			StartTime:   "09:00",
			EndTime:     "10:00",
			Description: "Sprint planning",
		},
	}
	createTestStorageRecord(t, tempStorage, targetDate, blocks)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]api.RemoteEntry{})
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	args := []string{
		"-date=" + targetDate,
		"-dry-run=true",
		"-storage-dir=" + tempStorage,
		"-markdown-dir=" + tempMarkdown,
		"-write-markdown=false",
		"-token=test-token",
		"-base-url=" + server.URL,
	}

	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK, got %d (err: %s)", code, errOut.String())
	}

	mdFile := filepath.Join(tempMarkdown, targetDate+".md")
	if _, err := os.Stat(mdFile); !os.IsNotExist(err) {
		t.Errorf("expected markdown file to NOT exist when write-markdown=false")
	}
}

func TestCLI_MarkdownWriteError_Warning(t *testing.T) {
	tempStorage := t.TempDir()
	tempFileAsDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(tempFileAsDir, []byte("file"), 0644); err != nil {
		t.Fatal(err)
	}
	targetDate := "2026-10-08"

	blocks := []timesheet.TimeBlock{
		{
			ID:          "block-1",
			Date:        targetDate,
			StartTime:   "09:00",
			EndTime:     "10:00",
			Description: "Sprint planning",
		},
	}
	createTestStorageRecord(t, tempStorage, targetDate, blocks)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]api.RemoteEntry{})
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	args := []string{
		"-date=" + targetDate,
		"-dry-run=true",
		"-storage-dir=" + tempStorage,
		"-markdown-dir=" + filepath.Join(tempFileAsDir, "sub"),
		"-token=test-token",
		"-base-url=" + server.URL,
	}

	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK even if markdown write warns, got %d", code)
	}
	if !strings.Contains(errOut.String(), "warning: writing markdown record") {
		t.Errorf("expected markdown write warning, got: %s", errOut.String())
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
