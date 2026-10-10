package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/api"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// ---------------------------------------------------------------------------
// Crash-injecting recorder used by the subprocess harness.
// ---------------------------------------------------------------------------

// crashRecorder wraps the real JSON store and, per mode, aborts the process at a
// precise point so a crash leaves real (non-simulated) partial state on disk:
//
//	after_intent  - exit AFTER persisting the pre-write intent, BEFORE any POST
//	before_second_intent - exit BEFORE persisting the SECOND operation's intent
//	                (first operation's outcome already durable)
//	before_outcome- exit AFTER a 2xx POST, BEFORE persisting the outcome
//	after_outcome - exit AFTER persisting the terminal outcome
//	""            - pass-through (no crash)
//
// os.Exit skips deferred session.Close(), so a crash deliberately leaves a stale
// day lock behind, exactly like a real crash.
type crashRecorder struct {
	store  *workflow.JSONFileStore
	mode   string
	marker string
}

func newCrashRecorder(rootDir, mode, marker string) (*crashRecorder, error) {
	store, err := workflow.NewJSONFileStore(rootDir)
	if err != nil {
		return nil, err
	}
	return &crashRecorder{store: store, mode: mode, marker: marker}, nil
}

func (c *crashRecorder) UpdateDay(ctx context.Context, date string, mutate func(*workflow.Record) error) (*workflow.Record, error) {
	return c.store.UpdateDay(ctx, date, mutate)
}

func (c *crashRecorder) BeginDaySession(ctx context.Context, date string) (submissionSession, error) {
	session, err := c.store.BeginDaySession(ctx, date)
	if err != nil {
		return nil, err
	}
	return &crashSession{session: session, mode: c.mode, marker: c.marker}, nil
}

func (c *crashRecorder) BreakStaleLock(ctx context.Context, date, expectedToken string) error {
	return c.store.BreakStaleLock(ctx, date, expectedToken, true)
}

func (c *crashRecorder) DayLockInfo(ctx context.Context, date string) (workflow.LockInfo, error) {
	return c.store.DayLockInfo(date)
}

type crashSession struct {
	session     workflow.Session
	mode        string
	marker      string
	intentCalls int
}

func (c *crashSession) crash() {
	if c.marker != "" {
		_ = os.WriteFile(c.marker, []byte("crashed\n"), 0600)
	}
	os.Exit(0)
}

func (c *crashSession) RecordIntent(ctx context.Context, outcome workflow.OperationOutcome) error {
	c.intentCalls++
	if c.mode == "before_second_intent" && c.intentCalls >= 2 {
		// Abort before persisting the SECOND operation's intent, leaving the
		// first operation's confirmed outcome durable and no unresolved intent.
		c.crash()
	}
	if _, err := c.session.Update(ctx, func(r *workflow.Record) error {
		workflow.UpsertIntent(r, outcome)
		return nil
	}); err != nil {
		return err
	}
	if c.mode == "after_intent" {
		c.crash()
	}
	return nil
}

func (c *crashSession) RecordOutcome(ctx context.Context, outcome workflow.OperationOutcome, mapping *workflow.RemoteMapping) error {
	if c.mode == "before_outcome" {
		c.crash()
	}
	_, err := c.session.Update(ctx, func(r *workflow.Record) error {
		workflow.UpsertOutcome(r, outcome)
		if mapping != nil {
			return workflow.UpsertMapping(r, *mapping)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if c.mode == "after_outcome" {
		c.crash()
	}
	return nil
}

func (c *crashSession) UnresolvedOperations(ctx context.Context) ([]workflow.OperationOutcome, error) {
	record, err := c.session.Get(ctx)
	if err != nil {
		if errors.Is(err, workflow.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return workflow.UnresolvedOperations(record), nil
}

func (c *crashSession) Close() error { return c.session.Close() }

// TestCLI_CrashSubprocess is the child-process body. It is inert in the parent
// suite and only acts when BE_SYNC_CLI_CRASH=1.
func TestCLI_CrashSubprocess(t *testing.T) {
	if os.Getenv("BE_SYNC_CLI_CRASH") != "1" {
		t.Skip("crash subprocess helper")
	}

	original := newWorkflowRecorder
	defer func() { newWorkflowRecorder = original }()
	newWorkflowRecorder = func(rootDir string) (workflowRecorder, error) {
		return newCrashRecorder(rootDir, os.Getenv("CRASH_MODE"), os.Getenv("CRASH_MARKER_PATH"))
	}

	args := []string{
		"-date=" + os.Getenv("CRASH_DATE"),
		"-dry-run=false",
		"-approve",
		"-env=" + filepath.Join(os.Getenv("CRASH_MARKDOWN_DIR"), "no-such.env"),
		"-storage-dir=" + os.Getenv("CRASH_STORAGE_DIR"),
		"-markdown-dir=" + os.Getenv("CRASH_MARKDOWN_DIR"),
		"-token=test-token",
		"-base-url=" + os.Getenv("CRASH_BASE_URL"),
	}
	var out, errOut bytes.Buffer
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	fmt.Fprintf(os.Stderr, "child normal exit code=%d\n%s", code, errOut.String())
	os.Exit(code)
}

func crashChildEnv(mode, marker, date, storageDir, markdownDir, baseURL string) []string {
	return append(os.Environ(),
		"BE_SYNC_CLI_CRASH=1",
		"CRASH_MODE="+mode,
		"CRASH_MARKER_PATH="+marker,
		"CRASH_DATE="+date,
		"CRASH_STORAGE_DIR="+storageDir,
		"CRASH_MARKDOWN_DIR="+markdownDir,
		"CRASH_BASE_URL="+baseURL,
	)
}

func startCrashChild(t *testing.T, mode, marker, date, storageDir, markdownDir, baseURL string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestCLI_CrashSubprocess")
	cmd.Env = crashChildEnv(mode, marker, date, storageDir, markdownDir, baseURL)
	buf := &bytes.Buffer{}
	cmd.Stdout = buf
	cmd.Stderr = buf
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start crash child: %v", err)
	}
	return cmd, buf
}

func runCrashChild(t *testing.T, mode, marker, date, storageDir, markdownDir, baseURL string) (int, string) {
	t.Helper()
	cmd, buf := startCrashChild(t, mode, marker, date, storageDir, markdownDir, baseURL)
	err := cmd.Wait()
	if err == nil {
		return 0, buf.String()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), buf.String()
	}
	t.Fatalf("crash child failed to run: %v", err)
	return -1, buf.String()
}

// ---------------------------------------------------------------------------
// Small helpers.
// ---------------------------------------------------------------------------

func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to spawn helper process for a dead PID: %v", err)
	}
	return cmd.Process.Pid
}

func workflowRecordPath(storageDir, date string) string {
	return filepath.Join(storageDir, "workflow", date+".json")
}

func lockFilePath(storageDir, date string) string {
	return filepath.Join(storageDir, "workflow", "."+date+".lock")
}

func writeStaleLock(t *testing.T, storageDir, date, token string) {
	t.Helper()
	host, err := os.Hostname()
	if err != nil {
		t.Fatalf("hostname: %v", err)
	}
	dir := filepath.Join(storageDir, "workflow")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir workflow: %v", err)
	}
	content := fmt.Sprintf("pid=%d host=%s token=%s acquired_at=%s\n", deadPID(t), host, token, time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(lockFilePath(storageDir, date), []byte(content), 0600); err != nil {
		t.Fatalf("write stale lock: %v", err)
	}
}

func readLockToken(t *testing.T, storageDir, date string) string {
	t.Helper()
	data, err := os.ReadFile(lockFilePath(storageDir, date))
	if err != nil {
		t.Fatalf("expected a leftover lock file: %v", err)
	}
	for _, field := range strings.Fields(string(data)) {
		key, value, ok := strings.Cut(field, "=")
		if ok && key == "token" {
			return value
		}
	}
	t.Fatalf("no token in lock file: %q", string(data))
	return ""
}

func loadWorkflowRecord(t *testing.T, storageDir, date string) workflow.Record {
	t.Helper()
	data, err := os.ReadFile(workflowRecordPath(storageDir, date))
	if err != nil {
		t.Fatalf("expected a durable workflow record: %v", err)
	}
	var record workflow.Record
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("decode workflow record: %v", err)
	}
	return record
}

func inProcessArgs(date, storageDir, markdownDir, baseURL string, extra ...string) []string {
	args := []string{
		"-date=" + date,
		"-dry-run=false",
		"-approve",
		"-env=" + filepath.Join(markdownDir, "no-such.env"),
		"-storage-dir=" + storageDir,
		"-markdown-dir=" + markdownDir,
		"-token=test-token",
		"-base-url=" + baseURL,
	}
	return append(args, extra...)
}

// ---------------------------------------------------------------------------
// Failure-injecting recorders for in-process tests.
// ---------------------------------------------------------------------------

type intentFailRecorder struct{ err error }

func (r intentFailRecorder) UpdateDay(context.Context, string, func(*workflow.Record) error) (*workflow.Record, error) {
	return nil, r.err
}
func (r intentFailRecorder) BeginDaySession(context.Context, string) (submissionSession, error) {
	return intentFailSession{err: r.err}, nil
}
func (r intentFailRecorder) BreakStaleLock(context.Context, string, string) error { return nil }
func (r intentFailRecorder) DayLockInfo(context.Context, string) (workflow.LockInfo, error) {
	return workflow.LockInfo{}, nil
}

type intentFailSession struct{ err error }

func (s intentFailSession) RecordIntent(context.Context, workflow.OperationOutcome) error {
	return s.err
}
func (s intentFailSession) RecordOutcome(context.Context, workflow.OperationOutcome, *workflow.RemoteMapping) error {
	return nil
}
func (s intentFailSession) UnresolvedOperations(context.Context) ([]workflow.OperationOutcome, error) {
	return nil, nil
}
func (s intentFailSession) Close() error { return nil }

type beginFailRecorder struct{ err error }

func (r beginFailRecorder) UpdateDay(context.Context, string, func(*workflow.Record) error) (*workflow.Record, error) {
	return nil, r.err
}
func (r beginFailRecorder) BeginDaySession(context.Context, string) (submissionSession, error) {
	return nil, r.err
}
func (r beginFailRecorder) BreakStaleLock(context.Context, string, string) error { return nil }
func (r beginFailRecorder) DayLockInfo(context.Context, string) (workflow.LockInfo, error) {
	return workflow.LockInfo{}, nil
}

// ---------------------------------------------------------------------------
// Tests.
// ---------------------------------------------------------------------------

// TestCLI_Crash_AfterIntent_RestartBlocked proves a crash after the durable intent
// but before any remote call leaves ZERO POSTs, a persisted in_flight row, and a
// stale lock, and that a restart (after a manual break) is blocked with
// SYNC_CLI_UNRESOLVED_OPERATION and still ZERO POSTs.
func TestCLI_Crash_AfterIntent_RestartBlocked(t *testing.T) {
	storageDir := t.TempDir()
	markdownDir := t.TempDir()
	date := "2026-10-08"
	createTestStorageRecord(t, storageDir, date, []timesheet.TimeBlock{
		{ID: "block-1", Date: date, StartTime: "09:00", EndTime: "10:00", Description: "Create one"},
	})

	var postCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode([]api.RemoteEntry{})
		case http.MethodPost:
			atomic.AddInt32(&postCount, 1)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"created-1"}`))
		}
	}))
	defer server.Close()

	marker := filepath.Join(t.TempDir(), "crashed.marker")
	if code, output := runCrashChild(t, "after_intent", marker, date, storageDir, markdownDir, server.URL); code != 0 {
		t.Fatalf("expected crash child exit 0, got %d\n%s", code, output)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("child did not reach the crash point: %v", err)
	}
	if got := atomic.LoadInt32(&postCount); got != 0 {
		t.Fatalf("expected zero POSTs before the remote call, got %d", got)
	}
	if _, err := os.Stat(lockFilePath(storageDir, date)); err != nil {
		t.Fatalf("expected a leftover stale lock after the crash: %v", err)
	}

	record := loadWorkflowRecord(t, storageDir, date)
	pending := workflow.UnresolvedOperations(&record)
	if len(pending) != 1 || pending[0].Status != workflow.OutcomeInFlight {
		t.Fatalf("expected one persisted in_flight intent, got %+v", record.Outcomes)
	}

	token := readLockToken(t, storageDir, date)
	var out, errOut bytes.Buffer
	code := run(context.Background(), inProcessArgs(date, storageDir, markdownDir, server.URL, "-break-lock="+token), strings.NewReader(""), &out, &errOut)
	if code == ExitOK {
		t.Fatalf("expected non-zero on restart with an unresolved operation")
	}
	if !strings.Contains(errOut.String(), ErrCodeUnresolvedOperation) {
		t.Fatalf("expected %s, got: %s", ErrCodeUnresolvedOperation, errOut.String())
	}
	if got := atomic.LoadInt32(&postCount); got != 0 {
		t.Fatalf("restart must not POST while unresolved, got %d", got)
	}
}

// TestCLI_Crash_BeforeOutcome_RestartBlocked proves a crash after a 2xx POST but
// before the outcome is persisted yields exactly 1 POST, a stale lock, and a
// restart blocked by the unresolved in_flight intent with no further POSTs.
func TestCLI_Crash_BeforeOutcome_RestartBlocked(t *testing.T) {
	storageDir := t.TempDir()
	markdownDir := t.TempDir()
	date := "2026-10-08"
	createTestStorageRecord(t, storageDir, date, []timesheet.TimeBlock{
		{ID: "block-1", Date: date, StartTime: "09:00", EndTime: "10:00", Description: "Create one"},
	})

	var postCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode([]api.RemoteEntry{})
		case http.MethodPost:
			atomic.AddInt32(&postCount, 1)
			var req api.CreateEntryRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.RemoteEntry{
				ID: "created-1", Date: req.Date, StartTime: req.StartTime, EndTime: req.EndTime,
				ClassificationID: req.ClassificationID, Description: req.Description,
			})
		}
	}))
	defer server.Close()

	marker := filepath.Join(t.TempDir(), "crashed.marker")
	if code, output := runCrashChild(t, "before_outcome", marker, date, storageDir, markdownDir, server.URL); code != 0 {
		t.Fatalf("expected crash child exit 0, got %d\n%s", code, output)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("child did not reach the crash point: %v", err)
	}
	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Fatalf("expected exactly 1 POST, got %d", got)
	}
	if _, err := os.Stat(lockFilePath(storageDir, date)); err != nil {
		t.Fatalf("expected a leftover stale lock after the crash: %v", err)
	}
	record := loadWorkflowRecord(t, storageDir, date)
	if len(workflow.UnresolvedOperations(&record)) != 1 {
		t.Fatalf("expected the in_flight intent to remain unresolved, got %+v", record.Outcomes)
	}

	token := readLockToken(t, storageDir, date)
	var out, errOut bytes.Buffer
	code := run(context.Background(), inProcessArgs(date, storageDir, markdownDir, server.URL, "-break-lock="+token), strings.NewReader(""), &out, &errOut)
	if code == ExitOK {
		t.Fatalf("expected non-zero on restart with an unresolved operation")
	}
	if !strings.Contains(errOut.String(), ErrCodeUnresolvedOperation) {
		t.Fatalf("expected %s, got: %s", ErrCodeUnresolvedOperation, errOut.String())
	}
	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Fatalf("restart must not POST while unresolved, got %d", got)
	}
}

// TestCLI_Crash_AfterOutcome_RestartSkips proves that once the terminal outcome is
// persisted, a restart (after a manual break of the stale lock) proceeds and
// reconciles the already-created entry as a skip with no duplicate POST.
func TestCLI_Crash_AfterOutcome_RestartSkips(t *testing.T) {
	storageDir := t.TempDir()
	markdownDir := t.TempDir()
	date := "2026-10-08"
	createTestStorageRecord(t, storageDir, date, []timesheet.TimeBlock{
		{ID: "block-1", Date: date, StartTime: "09:00", EndTime: "10:00", Description: "Create one"},
	})

	var mu sync.Mutex
	remoteDB := map[string]api.RemoteEntry{}
	var postCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			mu.Lock()
			list := make([]api.RemoteEntry, 0, len(remoteDB))
			for _, e := range remoteDB {
				list = append(list, e)
			}
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(list)
		case http.MethodPost:
			n := atomic.AddInt32(&postCount, 1)
			var req api.CreateEntryRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			entry := api.RemoteEntry{
				ID: fmt.Sprintf("created-%d", n), Date: req.Date, StartTime: req.StartTime,
				EndTime: req.EndTime, ClassificationID: req.ClassificationID, Description: req.Description,
			}
			mu.Lock()
			remoteDB[entry.ID] = entry
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(entry)
		}
	}))
	defer server.Close()

	marker := filepath.Join(t.TempDir(), "crashed.marker")
	if code, output := runCrashChild(t, "after_outcome", marker, date, storageDir, markdownDir, server.URL); code != 0 {
		t.Fatalf("expected crash child exit 0, got %d\n%s", code, output)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("child did not reach the crash point: %v", err)
	}
	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Fatalf("expected exactly 1 POST, got %d", got)
	}
	record := loadWorkflowRecord(t, storageDir, date)
	if len(workflow.UnresolvedOperations(&record)) != 0 {
		t.Fatalf("a persisted terminal outcome must not leave an in_flight row, got %+v", record.Outcomes)
	}

	token := readLockToken(t, storageDir, date)
	var out, errOut bytes.Buffer
	code := run(context.Background(), inProcessArgs(date, storageDir, markdownDir, server.URL, "-break-lock="+token), strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK on restart after break-lock, got %d (err: %s)", code, errOut.String())
	}
	if strings.Contains(errOut.String(), ErrCodeUnresolvedOperation) {
		t.Fatalf("restart should not be blocked once the outcome is terminal: %s", errOut.String())
	}
	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Fatalf("restart must skip the already-created entry (no duplicate POST), got %d POSTs", got)
	}
}

// TestCLI_IntentPersistFailure_ZeroPOST proves a failed pre-write intent halts the
// submission with ZERO remote writes and a typed code.
func TestCLI_IntentPersistFailure_ZeroPOST(t *testing.T) {
	storageDir := t.TempDir()
	markdownDir := t.TempDir()
	date := "2026-10-08"
	createTestStorageRecord(t, storageDir, date, []timesheet.TimeBlock{
		{ID: "block-1", Date: date, StartTime: "09:00", EndTime: "10:00", Description: "Create one"},
	})

	var postCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			atomic.AddInt32(&postCount, 1)
		}
		_ = json.NewEncoder(w).Encode([]api.RemoteEntry{})
	}))
	defer server.Close()

	original := newWorkflowRecorder
	defer func() { newWorkflowRecorder = original }()
	newWorkflowRecorder = func(string) (workflowRecorder, error) {
		return intentFailRecorder{err: errors.New("intent fsync failed")}, nil
	}

	var out, errOut bytes.Buffer
	code := run(context.Background(), inProcessArgs(date, storageDir, markdownDir, server.URL), strings.NewReader(""), &out, &errOut)
	if code == ExitOK {
		t.Fatalf("expected non-zero on intent persistence failure")
	}
	if !strings.Contains(errOut.String(), ErrCodeIntentPersistFailed) {
		t.Fatalf("expected %s, got: %s", ErrCodeIntentPersistFailed, errOut.String())
	}
	if got := atomic.LoadInt32(&postCount); got != 0 {
		t.Fatalf("a failed intent must produce ZERO remote writes, got %d POSTs", got)
	}
}

// TestCLI_BeginDaySessionFailure_ZeroPOST proves an error acquiring the day
// session stops the run with ZERO remote writes.
func TestCLI_BeginDaySessionFailure_ZeroPOST(t *testing.T) {
	storageDir := t.TempDir()
	markdownDir := t.TempDir()
	date := "2026-10-08"
	createTestStorageRecord(t, storageDir, date, []timesheet.TimeBlock{
		{ID: "block-1", Date: date, StartTime: "09:00", EndTime: "10:00", Description: "Create one"},
	})

	var postCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			atomic.AddInt32(&postCount, 1)
		}
		_ = json.NewEncoder(w).Encode([]api.RemoteEntry{})
	}))
	defer server.Close()

	original := newWorkflowRecorder
	defer func() { newWorkflowRecorder = original }()
	newWorkflowRecorder = func(string) (workflowRecorder, error) {
		return beginFailRecorder{err: errors.New("lock acquire failed")}, nil
	}

	var out, errOut bytes.Buffer
	code := run(context.Background(), inProcessArgs(date, storageDir, markdownDir, server.URL), strings.NewReader(""), &out, &errOut)
	if code == ExitOK {
		t.Fatalf("expected non-zero on BeginDaySession failure")
	}
	if !strings.Contains(errOut.String(), ErrCodeRecorderInitFailed) {
		t.Fatalf("expected typed code %s, got: %s", ErrCodeRecorderInitFailed, errOut.String())
	}
	if got := atomic.LoadInt32(&postCount); got != 0 {
		t.Fatalf("expected ZERO remote writes, got %d POSTs", got)
	}
}

// TestCLI_SeededInFlightRow_BlocksSubmission proves a pre-existing in_flight row
// blocks a live run with SYNC_CLI_UNRESOLVED_OPERATION, prints the operation, and
// performs ZERO remote writes.
func TestCLI_SeededInFlightRow_BlocksSubmission(t *testing.T) {
	storageDir := t.TempDir()
	markdownDir := t.TempDir()
	date := "2026-10-08"
	createTestStorageRecord(t, storageDir, date, []timesheet.TimeBlock{
		{ID: "block-1", Date: date, StartTime: "09:00", EndTime: "10:00", Description: "Create one"},
	})

	wfStore, err := workflow.NewJSONFileStore(storageDir)
	if err != nil {
		t.Fatalf("workflow store: %v", err)
	}
	if _, err := wfStore.UpdateDay(context.Background(), date, func(r *workflow.Record) error {
		workflow.UpsertIntent(r, workflow.OperationOutcome{
			OperationKey: "seeded-in-flight",
			LocalID:      "block-1",
			Action:       "create",
			Status:       workflow.OutcomeInFlight,
			Date:         date,
			StartTime:    "09:00",
			EndTime:      "10:00",
			IntentAt:     time.Now().UTC(),
		})
		return nil
	}); err != nil {
		t.Fatalf("seed in_flight row: %v", err)
	}

	var postCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			atomic.AddInt32(&postCount, 1)
		}
		_ = json.NewEncoder(w).Encode([]api.RemoteEntry{})
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	code := run(context.Background(), inProcessArgs(date, storageDir, markdownDir, server.URL), strings.NewReader(""), &out, &errOut)
	if code == ExitOK {
		t.Fatalf("expected non-zero for an unresolved seeded operation")
	}
	if !strings.Contains(errOut.String(), ErrCodeUnresolvedOperation) {
		t.Fatalf("expected %s, got: %s", ErrCodeUnresolvedOperation, errOut.String())
	}
	if !strings.Contains(errOut.String(), "action=create") || !strings.Contains(errOut.String(), "09:00 - 10:00") {
		t.Fatalf("expected the unresolved operation to be printed, got: %s", errOut.String())
	}
	if got := atomic.LoadInt32(&postCount); got != 0 {
		t.Fatalf("expected ZERO remote writes for a blocked submission, got %d POSTs", got)
	}
}

// TestCLI_UncertainWrite_BlocksRestart proves an uncertain write is persisted as
// a terminal unresolved (not in_flight) outcome, uses exactly 1 POST, and blocks
// a subsequent restart with ZERO further POSTs.
func TestCLI_UncertainWrite_BlocksRestart(t *testing.T) {
	storageDir := t.TempDir()
	markdownDir := t.TempDir()
	date := "2026-10-08"
	createTestStorageRecord(t, storageDir, date, []timesheet.TimeBlock{
		{ID: "block-1", Date: date, StartTime: "09:00", EndTime: "10:00", Description: "Create one"},
	})

	var postCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode([]api.RemoteEntry{})
		case http.MethodPost:
			atomic.AddInt32(&postCount, 1)
			w.WriteHeader(http.StatusRequestTimeout)
			_, _ = w.Write([]byte("timeout"))
		}
	}))
	defer server.Close()

	var out1, errOut1 bytes.Buffer
	if code := run(context.Background(), inProcessArgs(date, storageDir, markdownDir, server.URL), strings.NewReader(""), &out1, &errOut1); code == ExitOK {
		t.Fatalf("expected non-zero on an uncertain write")
	}
	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Fatalf("expected exactly 1 POST, got %d", got)
	}
	record := loadWorkflowRecord(t, storageDir, date)
	if len(workflow.UnresolvedOperations(&record)) != 0 {
		t.Fatalf("an uncertain outcome must not remain in_flight, got %+v", record.Outcomes)
	}
	uncertain := false
	for _, o := range record.Outcomes {
		if o.Status == workflow.OutcomeUncertain && !o.Resolved {
			uncertain = true
		}
	}
	if !uncertain {
		t.Fatalf("expected a persisted unresolved uncertain outcome, got %+v", record.Outcomes)
	}

	var out2, errOut2 bytes.Buffer
	if code := run(context.Background(), inProcessArgs(date, storageDir, markdownDir, server.URL), strings.NewReader(""), &out2, &errOut2); code == ExitOK {
		t.Fatalf("expected non-zero on restart after an uncertain write")
	}
	if !strings.Contains(errOut2.String(), ErrCodeUnresolvedOperation) {
		t.Fatalf("expected %s on restart, got: %s", ErrCodeUnresolvedOperation, errOut2.String())
	}
	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Fatalf("restart must not POST after an uncertain write, got %d POSTs", got)
	}
}

// TestCLI_DeadLock_AutoRefuses_BreakLockProceeds proves a stale lock refuses an
// automatic run with SYNC_CLI_STALE_LOCK and ZERO writes, while a manual
// -break-lock with the correct token proceeds.
func TestCLI_DeadLock_AutoRefuses_BreakLockProceeds(t *testing.T) {
	storageDir := t.TempDir()
	markdownDir := t.TempDir()
	date := "2026-10-08"
	createTestStorageRecord(t, storageDir, date, []timesheet.TimeBlock{
		{ID: "block-1", Date: date, StartTime: "09:00", EndTime: "10:00", Description: "Create one"},
	})
	const token = "00112233445566aa"
	writeStaleLock(t, storageDir, date, token)

	var postCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode([]api.RemoteEntry{})
		case http.MethodPost:
			atomic.AddInt32(&postCount, 1)
			var req api.CreateEntryRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.RemoteEntry{
				ID: "created-1", Date: req.Date, StartTime: req.StartTime, EndTime: req.EndTime,
				ClassificationID: req.ClassificationID, Description: req.Description,
			})
		}
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	if code := run(context.Background(), inProcessArgs(date, storageDir, markdownDir, server.URL), strings.NewReader(""), &out, &errOut); code == ExitOK {
		t.Fatalf("expected non-zero on a stale lock")
	}
	if !strings.Contains(errOut.String(), ErrCodeStaleLock) {
		t.Fatalf("expected %s, got: %s", ErrCodeStaleLock, errOut.String())
	}
	if got := atomic.LoadInt32(&postCount); got != 0 {
		t.Fatalf("expected ZERO remote writes on a stale lock, got %d POSTs", got)
	}

	var out2, errOut2 bytes.Buffer
	if code := run(context.Background(), inProcessArgs(date, storageDir, markdownDir, server.URL, "-break-lock="+token), strings.NewReader(""), &out2, &errOut2); code != ExitOK {
		t.Fatalf("expected ExitOK after -break-lock, got %d (err: %s)", code, errOut2.String())
	}
	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Fatalf("expected exactly 1 POST after breaking the lock, got %d", got)
	}
}

// TestCLI_ConcurrentProcesses_OneWriter proves two concurrent processes for the
// same day yield exactly one writer while the other exits with
// SYNC_CLI_WRITER_BUSY and performs ZERO remote writes.
func TestCLI_ConcurrentProcesses_OneWriter(t *testing.T) {
	storageDir := t.TempDir()
	markdownDir := t.TempDir()
	date := "2026-10-08"
	createTestStorageRecord(t, storageDir, date, []timesheet.TimeBlock{
		{ID: "block-1", Date: date, StartTime: "09:00", EndTime: "10:00", Description: "Create one"},
	})

	postEntered := make(chan struct{})
	var postOnce sync.Once
	releasePost := make(chan struct{})
	var postCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode([]api.RemoteEntry{})
		case http.MethodPost:
			atomic.AddInt32(&postCount, 1)
			postOnce.Do(func() { close(postEntered) })
			<-releasePost
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"created-1"}`))
		}
	}))
	defer server.Close()

	// Process A acquires the lock and blocks inside the POST, holding it.
	cmdA, bufA := startCrashChild(t, "", "", date, storageDir, markdownDir, server.URL)
	select {
	case <-postEntered:
	case <-time.After(15 * time.Second):
		_ = cmdA.Process.Kill()
		t.Fatalf("process A never reached its POST; lock not held\n%s", bufA.String())
	}

	// Process B must fail fast as a second writer, with ZERO POSTs.
	codeB, outB := runCrashChild(t, "", "", date, storageDir, markdownDir, server.URL)
	if codeB == 0 {
		t.Fatalf("expected the second writer to fail, got exit 0\n%s", outB)
	}
	if !strings.Contains(outB, ErrCodeWriterBusy) {
		t.Fatalf("expected %s for the second writer, got: %s", ErrCodeWriterBusy, outB)
	}

	close(releasePost)
	if err := cmdA.Wait(); err != nil {
		t.Fatalf("process A failed after release: %v\n%s", err, bufA.String())
	}
	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Fatalf("expected exactly one remote write across both processes, got %d", got)
	}
}

// ---------------------------------------------------------------------------
// R1: truthful multi-op intent-failure diagnostic.
// ---------------------------------------------------------------------------

// nthIntentFailRecorder wraps the real store and fails RecordIntent on its Nth
// call, so earlier operations complete (and persist) normally before the halt.
type nthIntentFailRecorder struct {
	store  *workflow.JSONFileStore
	failOn int
}

func (r *nthIntentFailRecorder) BeginDaySession(ctx context.Context, date string) (submissionSession, error) {
	session, err := r.store.BeginDaySession(ctx, date)
	if err != nil {
		return nil, err
	}
	return &nthIntentFailSession{session: session, failOn: r.failOn}, nil
}

func (r *nthIntentFailRecorder) BreakStaleLock(ctx context.Context, date, expectedToken string) error {
	return r.store.BreakStaleLock(ctx, date, expectedToken, true)
}

func (r *nthIntentFailRecorder) DayLockInfo(ctx context.Context, date string) (workflow.LockInfo, error) {
	return r.store.DayLockInfo(date)
}

type nthIntentFailSession struct {
	session workflow.Session
	failOn  int
	calls   int
}

func (s *nthIntentFailSession) RecordIntent(ctx context.Context, outcome workflow.OperationOutcome) error {
	s.calls++
	if s.calls == s.failOn {
		return errors.New("simulated intent fsync failure")
	}
	_, err := s.session.Update(ctx, func(r *workflow.Record) error {
		workflow.UpsertIntent(r, outcome)
		return nil
	})
	return err
}

func (s *nthIntentFailSession) RecordOutcome(ctx context.Context, outcome workflow.OperationOutcome, mapping *workflow.RemoteMapping) error {
	_, err := s.session.Update(ctx, func(r *workflow.Record) error {
		workflow.UpsertOutcome(r, outcome)
		if mapping != nil {
			return workflow.UpsertMapping(r, *mapping)
		}
		return nil
	})
	return err
}

func (s *nthIntentFailSession) UnresolvedOperations(ctx context.Context) ([]workflow.OperationOutcome, error) {
	record, err := s.session.Get(ctx)
	if err != nil {
		if errors.Is(err, workflow.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return workflow.UnresolvedOperations(record), nil
}

func (s *nthIntentFailSession) Close() error { return s.session.Close() }

// TestCLI_IntentPersistFailure_MultiOpReportsPriorWrites proves the intent-failure
// diagnostic is truthful for a multi-op plan: op #1 writes remotely and persists
// a confirmed outcome, op #2's intent fails, and the diagnostic reports the prior
// remote write rather than claiming zero, with a non-zero exit.
func TestCLI_IntentPersistFailure_MultiOpReportsPriorWrites(t *testing.T) {
	storageDir := t.TempDir()
	markdownDir := t.TempDir()
	date := "2026-10-08"
	createTestStorageRecord(t, storageDir, date, []timesheet.TimeBlock{
		{ID: "block-1", Date: date, StartTime: "09:00", EndTime: "10:00", Description: "Create one"},
		{ID: "block-2", Date: date, StartTime: "10:00", EndTime: "11:00", Description: "Create two"},
	})

	var postCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode([]api.RemoteEntry{})
		case http.MethodPost:
			n := atomic.AddInt32(&postCount, 1)
			var req api.CreateEntryRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(api.RemoteEntry{
				ID: fmt.Sprintf("created-%d", n), Date: req.Date, StartTime: req.StartTime,
				EndTime: req.EndTime, ClassificationID: req.ClassificationID, Description: req.Description,
			})
		}
	}))
	defer server.Close()

	original := newWorkflowRecorder
	defer func() { newWorkflowRecorder = original }()
	newWorkflowRecorder = func(root string) (workflowRecorder, error) {
		store, err := workflow.NewJSONFileStore(root)
		if err != nil {
			return nil, err
		}
		return &nthIntentFailRecorder{store: store, failOn: 2}, nil
	}

	var out, errOut bytes.Buffer
	code := run(context.Background(), inProcessArgs(date, storageDir, markdownDir, server.URL), strings.NewReader(""), &out, &errOut)
	if code == ExitOK {
		t.Fatalf("expected non-zero on intent persistence failure")
	}
	if !strings.Contains(errOut.String(), ErrCodeIntentPersistFailed) {
		t.Fatalf("expected %s, got: %s", ErrCodeIntentPersistFailed, errOut.String())
	}
	if strings.Contains(errOut.String(), "zero remote writes") {
		t.Fatalf("diagnostic must not claim zero remote writes after op #1 succeeded: %s", errOut.String())
	}
	if !strings.Contains(errOut.String(), "1 remote write(s) already executed") {
		t.Fatalf("diagnostic must report the prior remote write, got: %s", errOut.String())
	}
	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Fatalf("expected exactly 1 POST (op #1), got %d", got)
	}

	record := loadWorkflowRecord(t, storageDir, date)
	confirmed := false
	for _, o := range record.Outcomes {
		if o.Status == workflow.OutcomeConfirmed && o.Resolved {
			confirmed = true
		}
	}
	if !confirmed {
		t.Fatalf("expected op #1's confirmed outcome to be durable, got %+v", record.Outcomes)
	}
}

// ---------------------------------------------------------------------------
// R2: -break-lock with -dry-run is a usage error.
// ---------------------------------------------------------------------------

func TestCLI_BreakLockDryRun_ExitUsage(t *testing.T) {
	var out, errOut bytes.Buffer
	args := []string{"-date=2026-10-08", "-dry-run=true", "-break-lock=00112233445566aa", "-token=test-token"}
	code := run(context.Background(), args, strings.NewReader(""), &out, &errOut)
	if code != ExitUsage {
		t.Fatalf("expected ExitUsage for -break-lock with -dry-run, got %d (err: %s)", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "-break-lock") || !strings.Contains(errOut.String(), "-dry-run") {
		t.Fatalf("expected a clear -break-lock/-dry-run usage message, got: %s", errOut.String())
	}
}

// ---------------------------------------------------------------------------
// R8(i): multi-op partial progress across a crash.
// ---------------------------------------------------------------------------

// TestCLI_Crash_PartialProgress_RestartExecutesRemaining proves that when op #1's
// outcome is durable and the process aborts before op #2's intent, a restart
// skips op #1 (no duplicate) and executes op #2 exactly once.
func TestCLI_Crash_PartialProgress_RestartExecutesRemaining(t *testing.T) {
	storageDir := t.TempDir()
	markdownDir := t.TempDir()
	date := "2026-10-08"
	createTestStorageRecord(t, storageDir, date, []timesheet.TimeBlock{
		{ID: "block-1", Date: date, StartTime: "09:00", EndTime: "10:00", Description: "Create one"},
		{ID: "block-2", Date: date, StartTime: "10:00", EndTime: "11:00", Description: "Create two"},
	})

	var mu sync.Mutex
	remoteDB := map[string]api.RemoteEntry{}
	var postCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			mu.Lock()
			list := make([]api.RemoteEntry, 0, len(remoteDB))
			for _, e := range remoteDB {
				list = append(list, e)
			}
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(list)
		case http.MethodPost:
			n := atomic.AddInt32(&postCount, 1)
			var req api.CreateEntryRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			entry := api.RemoteEntry{
				ID: fmt.Sprintf("created-%d", n), Date: req.Date, StartTime: req.StartTime,
				EndTime: req.EndTime, ClassificationID: req.ClassificationID, Description: req.Description,
			}
			mu.Lock()
			remoteDB[entry.ID] = entry
			mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(entry)
		}
	}))
	defer server.Close()

	marker := filepath.Join(t.TempDir(), "crashed.marker")
	if code, output := runCrashChild(t, "before_second_intent", marker, date, storageDir, markdownDir, server.URL); code != 0 {
		t.Fatalf("expected crash child exit 0, got %d\n%s", code, output)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("child did not reach the crash point: %v", err)
	}
	if got := atomic.LoadInt32(&postCount); got != 1 {
		t.Fatalf("expected exactly 1 POST before the crash, got %d", got)
	}
	if _, err := os.Stat(lockFilePath(storageDir, date)); err != nil {
		t.Fatalf("expected a leftover stale lock after the crash: %v", err)
	}
	record := loadWorkflowRecord(t, storageDir, date)
	if len(workflow.UnresolvedOperations(&record)) != 0 {
		t.Fatalf("expected no unresolved intent after op #1 completed, got %+v", record.Outcomes)
	}

	token := readLockToken(t, storageDir, date)
	var out, errOut bytes.Buffer
	code := run(context.Background(), inProcessArgs(date, storageDir, markdownDir, server.URL, "-break-lock="+token), strings.NewReader(""), &out, &errOut)
	if code != ExitOK {
		t.Fatalf("expected ExitOK on restart, got %d (err: %s)", code, errOut.String())
	}
	if got := atomic.LoadInt32(&postCount); got != 2 {
		t.Fatalf("expected exactly 2 POSTs total (one per op, no duplicate), got %d", got)
	}
	mu.Lock()
	entries := len(remoteDB)
	mu.Unlock()
	if entries != 2 {
		t.Fatalf("expected exactly 2 remote entries (one per op), got %d", entries)
	}
}

// ---------------------------------------------------------------------------
// R8(ii): unidentified restart gate.
// ---------------------------------------------------------------------------

// TestCLI_UnidentifiedRestartGate_ZeroPOST proves a seeded terminal-but-unresolved
// unidentified row blocks a live run with SYNC_CLI_UNRESOLVED_OPERATION and ZERO
// remote writes.
func TestCLI_UnidentifiedRestartGate_ZeroPOST(t *testing.T) {
	storageDir := t.TempDir()
	markdownDir := t.TempDir()
	date := "2026-10-08"
	createTestStorageRecord(t, storageDir, date, []timesheet.TimeBlock{
		{ID: "block-1", Date: date, StartTime: "09:00", EndTime: "10:00", Description: "Create one"},
	})

	wfStore, err := workflow.NewJSONFileStore(storageDir)
	if err != nil {
		t.Fatalf("workflow store: %v", err)
	}
	if _, err := wfStore.UpdateDay(context.Background(), date, func(r *workflow.Record) error {
		workflow.UpsertOutcome(r, workflow.OperationOutcome{
			OperationKey: "seeded-unidentified",
			LocalID:      "block-1",
			Action:       "create",
			Status:       workflow.OutcomeUnidentified,
			Date:         date,
			StartTime:    "09:00",
			EndTime:      "10:00",
			ErrorCode:    "REMOTE_ID_MISSING",
			Resolved:     false,
		})
		return nil
	}); err != nil {
		t.Fatalf("seed unidentified row: %v", err)
	}

	var postCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			atomic.AddInt32(&postCount, 1)
		}
		_ = json.NewEncoder(w).Encode([]api.RemoteEntry{})
	}))
	defer server.Close()

	var out, errOut bytes.Buffer
	code := run(context.Background(), inProcessArgs(date, storageDir, markdownDir, server.URL), strings.NewReader(""), &out, &errOut)
	if code == ExitOK {
		t.Fatalf("expected non-zero for an unresolved unidentified row")
	}
	if !strings.Contains(errOut.String(), ErrCodeUnresolvedOperation) {
		t.Fatalf("expected %s, got: %s", ErrCodeUnresolvedOperation, errOut.String())
	}
	if got := atomic.LoadInt32(&postCount); got != 0 {
		t.Fatalf("expected ZERO remote writes, got %d POSTs", got)
	}
}
