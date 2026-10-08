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
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/storage"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

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
		"-storage-dir=" + tempStorage,
		"-markdown-dir=" + tempMarkdown,
		"-token=test-token",
		"-base-url=" + server.URL,
	}

	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK with mutation warnings, got %d", code)
	}
	if !strings.Contains(errOut.String(), "one or more mutations failed") {
		t.Errorf("expected mutation failure warning, got: %s", errOut.String())
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
