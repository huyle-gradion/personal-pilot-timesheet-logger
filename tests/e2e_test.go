//go:build e2e

// This end-to-end suite exercises the headless pipeline against hermetic mock
// servers. The legacy REST SUBMISSION flow (create/edit/delete + plan-derived
// markdown) was retired in S5 containment: cmd/sync-timesheet now fails closed on
// a submission attempt and offers only a read-only preview. The submission e2e
// tests (idempotency, markdown summary, multi-day submission) were removed with
// the retired path and will be re-created against the approval-bound MCP path in
// S6/S8. The tests below cover collection, review/clustering, the contained
// sync-timesheet surface (fail-closed + read-only preview), and benchmarking.
package tests

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/api"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/benchmark"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/storage"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

var (
	binDir             string
	binFetchCalendar   string
	binFetchGit        string
	binReviewTimesheet string
	binSyncTimesheet   string
	binBenchmark       string
)

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "e2e-binaries-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create temp dir for binaries: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	binDir = tmpDir
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to resolve repo root: %v\n", err)
		os.Exit(1)
	}

	binaries := []struct {
		name string
		pkg  string
		dest *string
	}{
		{"fetch-calendar", "./cmd/fetch-calendar", &binFetchCalendar},
		{"fetch-git", "./cmd/fetch-git", &binFetchGit},
		{"review-timesheet", "./cmd/review-timesheet", &binReviewTimesheet},
		{"sync-timesheet", "./cmd/sync-timesheet", &binSyncTimesheet},
		{"benchmark", "./cmd/benchmark", &binBenchmark},
	}

	for _, b := range binaries {
		binName := b.name
		if runtime.GOOS == "windows" {
			binName += ".exe"
		}
		outPath := filepath.Join(binDir, binName)
		cmd := exec.Command("go", "build", "-o", outPath, b.pkg)
		cmd.Dir = repoRoot
		if output, buildErr := cmd.CombinedOutput(); buildErr != nil {
			fmt.Fprintf(os.Stderr, "failed to build %s: %v\n%s\n", b.name, buildErr, string(output))
			os.Exit(1)
		}
		*b.dest = outPath
	}

	code := m.Run()
	os.Exit(code)
}

func runCmd(ctx context.Context, dir, binPath string, stdin io.Reader, args ...string) (string, string, int, error) {
	cmd := exec.CommandContext(ctx, binPath, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}
	return stdout.String(), stderr.String(), exitCode, err
}

// readOnlyGradionServer is a read-only mock of the legacy Gradion REST surface.
// It serves GET /api/entries only and counts any non-GET attempt so a test can
// assert the contained CLI never writes.
type readOnlyGradionServer struct {
	mu        sync.Mutex
	entries   []api.RemoteEntry
	mutations int
	getCount  int
}

func newReadOnlyGradionServer(entries []api.RemoteEntry) (*httptest.Server, *readOnlyGradionServer) {
	state := &readOnlyGradionServer{entries: entries}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()

		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/entries") {
			state.getCount++
			dateQuery := r.URL.Query().Get("dateFrom")
			filtered := make([]api.RemoteEntry, 0)
			for _, e := range state.entries {
				if dateQuery == "" || e.Date == dateQuery {
					filtered = append(filtered, e)
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(api.RemoteEntriesResponse{Total: len(filtered), Entries: filtered})
			return
		}

		// Any mutation attempt is a containment violation.
		state.mutations++
		w.WriteHeader(http.StatusMethodNotAllowed)
	})

	return httptest.NewServer(handler), state
}

func (s *readOnlyGradionServer) counts() (mutations, gets int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mutations, s.getCount
}

func TestE2E_FullHeadlessPipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	testDate := "2026-10-08"
	baseTemp := t.TempDir()

	cacheDir := filepath.Join(baseTemp, ".cache", "timesheet", testDate)
	storageDir := filepath.Join(baseTemp, "storage")
	benchmarksDir := filepath.Join(baseTemp, "benchmarks")

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		t.Fatalf("creating cache dir: %v", err)
	}

	// 1. Mock Calendar Feed Server
	icsData := fmt.Sprintf(`BEGIN:VCALENDAR
VERSION:2.0
PRODID:-//Test//EN
BEGIN:VEVENT
UID:event-active-sprint@google.com
DTSTART:20261008T090000Z
DTEND:20261008T100000Z
SUMMARY:Sprint Planning
END:VEVENT
BEGIN:VEVENT
UID:event-declined@google.com
DTSTART:20261008T100000Z
DTEND:20261008T110000Z
SUMMARY:Declined Meeting
ATTENDEE;CUTYPE=INDIVIDUAL;ROLE=REQ-PARTICIPANT;PARTSTAT=DECLINED;CN=Huy Le:mailto:test@example.com
END:VEVENT
BEGIN:VEVENT
UID:event-cancelled@google.com
DTSTART:20261008T110000Z
DTEND:20261008T120000Z
SUMMARY:Cancelled Sync
STATUS:CANCELLED
END:VEVENT
BEGIN:VEVENT
UID:event-transparent@google.com
DTSTART:20261008T130000Z
DTEND:20261008T140000Z
SUMMARY:Focus Time
TRANSP:TRANSPARENT
END:VEVENT
BEGIN:VEVENT
UID:event-arch-sync@google.com
DTSTART:20261008T140000Z
DTEND:20261008T150000Z
SUMMARY:Architecture Review
END:VEVENT
END:VCALENDAR`)

	calServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = w.Write([]byte(icsData))
	}))
	defer calServer.Close()

	// 2. Mock GitHub API Server
	ghServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/user":
			_, _ = w.Write([]byte(`{"login":"testuser"}`))
		case strings.Contains(r.URL.Path, "/commits"):
			_, _ = w.Write([]byte(`[
				{
					"sha": "a1b2c3d4e5f67890",
					"commit": {
						"author": {"name": "Test User", "email": "test@example.com", "date": "2026-10-08T10:30:00Z"},
						"message": "feat(calendar): implement ical parser"
					}
				},
				{
					"sha": "b2c3d4e5f6789012",
					"commit": {
						"author": {"name": "Test User", "email": "test@example.com", "date": "2026-10-08T11:15:00Z"},
						"message": "test(calendar): add recurrence unit tests"
					}
				}
			]`))
		case strings.HasSuffix(r.URL.Path, "/pulls"):
			_, _ = w.Write([]byte(`[
				{
					"number": 1,
					"title": "feat: calendar parser",
					"state": "open",
					"created_at": "2026-10-08T09:00:00Z",
					"updated_at": "2026-10-08T11:30:00Z",
					"head": {"ref": "feat/calendar", "sha": "b2c3d4e5f6789012"},
					"user": {"login": "testuser"}
				}
			]`))
		case strings.Contains(r.URL.Path, "/pulls/1/commits"):
			_, _ = w.Write([]byte(`[
				{"sha": "a1b2c3d4e5f67890"},
				{"sha": "b2c3d4e5f6789012"}
			]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ghServer.Close()

	// 3. Read-only mock Gradion Server
	gradionServer, gradionState := newReadOnlyGradionServer(nil)
	defer gradionServer.Close()

	// Step 1: fetch-calendar
	calJSONPath := filepath.Join(cacheDir, "calendar.json")
	calOut, calErrOut, code, err := runCmd(ctx, "", binFetchCalendar, nil,
		"-url", calServer.URL,
		"-date", testDate,
		"-tz", "UTC",
		"-user-email", "test@example.com",
		"-skip-transparent",
	)
	if err != nil || code != 0 {
		t.Fatalf("fetch-calendar failed with code %d: %v\nstderr: %s", code, err, calErrOut)
	}
	if writeErr := os.WriteFile(calJSONPath, []byte(calOut), 0644); writeErr != nil {
		t.Fatalf("saving calendar.json: %v", writeErr)
	}

	var calEntries []timesheet.SimpleEntry
	if err := json.Unmarshal([]byte(calOut), &calEntries); err != nil {
		t.Fatalf("unmarshaling calendar entries: %v", err)
	}
	if len(calEntries) != 2 {
		t.Fatalf("expected 2 calendar entries after filtering declined/cancelled/transparent, got %d", len(calEntries))
	}

	// Step 2: fetch-git
	gitJSONPath := filepath.Join(cacheDir, "git.json")
	gitOut, gitErrOut, code, err := runCmd(ctx, "", binFetchGit, nil,
		"-date", testDate,
		"-tz", "UTC",
		"-github-base-url", ghServer.URL,
		"-token", "test-github-token",
		"-repos", "huyle-gradion/personal-pilot-timesheet-logger",
		"-author", "testuser",
	)
	if err != nil || code != 0 {
		t.Fatalf("fetch-git failed with code %d: %v\nstderr: %s", code, err, gitErrOut)
	}
	if writeErr := os.WriteFile(gitJSONPath, []byte(gitOut), 0644); writeErr != nil {
		t.Fatalf("saving git.json: %v", writeErr)
	}

	var gitEntries []timesheet.SimpleEntry
	if err := json.Unmarshal([]byte(gitOut), &gitEntries); err != nil {
		t.Fatalf("unmarshaling git entries: %v", err)
	}
	if len(gitEntries) == 0 {
		t.Fatalf("expected git entries, got none")
	}

	// Step 3: Combine and Cluster time blocks
	combinedEntries := append(calEntries, gitEntries...)
	combinedBytes, _ := json.MarshalIndent(combinedEntries, "", "  ")
	combinedPath := filepath.Join(cacheDir, "combined.json")
	if err := os.WriteFile(combinedPath, combinedBytes, 0644); err != nil {
		t.Fatalf("writing combined.json: %v", err)
	}

	skeletonPath := filepath.Join(cacheDir, "skeleton.json")
	_, revErrOut, code, err := runCmd(ctx, "", binReviewTimesheet, nil,
		"-date", testDate,
		"-input", combinedPath,
		"-non-interactive",
		"-auto-resolve", "keep_both",
		"-confirm",
		"-output", skeletonPath,
		"-storage-dir", storageDir,
	)
	if err != nil || code != 0 {
		t.Fatalf("clustering via review-timesheet failed with code %d: %v\nstderr: %s", code, err, revErrOut)
	}

	skeletonBytes, err := os.ReadFile(skeletonPath)
	if err != nil {
		t.Fatalf("reading skeleton.json: %v", err)
	}
	var skeletonRecord storage.TimesheetRecord
	if err := json.Unmarshal(skeletonBytes, &skeletonRecord); err != nil {
		t.Fatalf("parsing skeleton record: %v", err)
	}
	if len(skeletonRecord.Blocks) == 0 {
		t.Fatalf("expected clustered blocks in skeleton.json, got 0")
	}

	// Step 4: Collision review and Tier 2 storage persistence
	_, revErrOut2, code, err := runCmd(ctx, "", binReviewTimesheet, nil,
		"-date", testDate,
		"-input", skeletonPath,
		"-non-interactive",
		"-auto-resolve", "keep_both",
		"-confirm",
		"-storage-dir", storageDir,
		"-sink", "console",
	)
	if err != nil || code != 0 {
		t.Fatalf("persisting review failed with code %d: %v\nstderr: %s", code, err, revErrOut2)
	}

	store, err := storage.NewJSONFileStore(storageDir)
	if err != nil {
		t.Fatalf("creating JSONFileStore: %v", err)
	}
	savedRecord, err := store.GetDay(ctx, testDate)
	if err != nil {
		t.Fatalf("loading saved record: %v", err)
	}
	if savedRecord.Version < 1 || len(savedRecord.Blocks) == 0 {
		t.Fatalf("invalid saved record: version=%d, blocks=%d", savedRecord.Version, len(savedRecord.Blocks))
	}

	// Step 5: A submission attempt FAILS CLOSED with zero remote writes (the
	// legacy REST write path is retired; no flag re-enables it).
	_, syncErrOut, code, _ := runCmd(ctx, "", binSyncTimesheet, nil,
		"-date", testDate,
		"-storage-dir", storageDir,
		"-base-url", gradionServer.URL,
		"-token", "test-gradion-token",
	)
	if code == 0 {
		t.Fatalf("expected a non-zero exit for a retired submission attempt, got %d", code)
	}
	if !strings.Contains(syncErrOut, "SYNC_CLI_REST_RETIRED") {
		t.Fatalf("expected SYNC_CLI_REST_RETIRED in stderr, got: %s", syncErrOut)
	}
	if mutations, _ := gradionState.counts(); mutations != 0 {
		t.Fatalf("retired submission must make zero remote writes, got %d mutation attempts", mutations)
	}

	// Step 6: The read-only preview works and performs zero mutations.
	previewOut, previewErrOut, code, err := runCmd(ctx, "", binSyncTimesheet, nil,
		"-date", testDate,
		"-dry-run",
		"-storage-dir", storageDir,
		"-base-url", gradionServer.URL,
		"-token", "test-gradion-token",
	)
	if err != nil || code != 0 {
		t.Fatalf("read-only preview failed with code %d: %v\nstderr: %s", code, err, previewErrOut)
	}
	if !strings.Contains(previewOut, "Read-only preview") {
		t.Fatalf("expected a read-only preview message, got: %s", previewOut)
	}
	if mutations, gets := gradionState.counts(); mutations != 0 || gets == 0 {
		t.Fatalf("preview must read without writing: mutations=%d gets=%d", mutations, gets)
	}

	// Step 7: Empirical benchmark logging
	transcriptPath := filepath.Join(cacheDir, "transcript.jsonl")
	transcriptContent := `{"usage": {"input_tokens": 190, "output_tokens": 75, "cache_read_input_tokens": 30, "total_tokens": 295}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(transcriptContent), 0644); err != nil {
		t.Fatalf("writing transcript: %v", err)
	}

	csvPath := filepath.Join(benchmarksDir, "metrics.csv")
	benchOut, benchErrOut, code, err := runCmd(ctx, "", binBenchmark, nil,
		"-record",
		"-date", testDate,
		"-transcript", transcriptPath,
		"-csv", csvPath,
		"-storage-dir", storageDir,
		"-wall-clock-ms", "420",
		"-chart",
	)
	if err != nil || code != 0 {
		t.Fatalf("benchmark failed with code %d: %v\nstderr: %s", code, err, benchErrOut)
	}
	if !strings.Contains(benchOut, "Recorded benchmark metric") {
		t.Fatalf("expected benchmark output confirmation, got: %s", benchOut)
	}

	metrics, err := benchmark.ReadMetrics(csvPath)
	if err != nil {
		t.Fatalf("reading metrics CSV: %v", err)
	}
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric row, got %d", len(metrics))
	}
	if metrics[0].TotalTokens != 295 || metrics[0].Date != testDate {
		t.Fatalf("metric mismatch: %+v", metrics[0])
	}
}

func TestE2E_GitHubOutageResilience(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	testDate := "2026-10-08"
	baseTemp := t.TempDir()
	localGitDir := filepath.Join(baseTemp, "local-repo")
	if err := os.MkdirAll(localGitDir, 0755); err != nil {
		t.Fatalf("creating local repo dir: %v", err)
	}

	// Initialize local git repo with a commit
	initCmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.name", "Resilience Tester"},
		{"git", "config", "user.email", "resilience@example.com"},
		{"git", "remote", "add", "origin", "https://github.com/huyle-gradion/personal-pilot-timesheet-logger.git"},
	}
	for _, c := range initCmds {
		cmd := exec.CommandContext(ctx, c[0], c[1:]...)
		cmd.Dir = localGitDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init command failed %v: %v\n%s", c, err, string(out))
		}
	}

	// Create commit on target date
	commitCmd := exec.CommandContext(ctx, "git", "commit", "--allow-empty", "-m", "fix: resilient local commit #77")
	commitCmd.Dir = localGitDir
	commitCmd.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE=2026-10-08T10:15:00Z",
		"GIT_COMMITTER_DATE=2026-10-08T10:15:00Z",
	)
	if out, err := commitCmd.CombinedOutput(); err != nil {
		t.Fatalf("creating test git commit: %v\n%s", err, string(out))
	}

	// Mock GitHub Outage Server (returns HTTP 503 Service Unavailable)
	outageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"message": "GitHub API degraded or unavailable"}`))
	}))
	defer outageServer.Close()

	// Execute fetch-git pointing to local repo and outage server
	stdout, stderr, code, err := runCmd(ctx, "", binFetchGit, nil,
		"-date", testDate,
		"-tz", "UTC",
		"-repos", localGitDir,
		"-author", "resilience@example.com",
		"-github-base-url", outageServer.URL,
		"-token", "dummy-pat",
	)

	if err != nil || code != 0 {
		t.Fatalf("fetch-git failed during outage fallback with code %d: %v\nstderr: %s", code, err, stderr)
	}

	// Verify fallback notice in stderr
	if !strings.Contains(stderr, "activating Tier 2 local git fallback") {
		t.Errorf("expected warning in stderr indicating local fallback activation, got: %s", stderr)
	}

	// Verify entries returned from local git history
	var entries []struct {
		ID          string         `json:"id"`
		Source      string         `json:"source"`
		Title       string         `json:"title"`
		Description string         `json:"description"`
		Metadata    map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(stdout), &entries); err != nil {
		t.Fatalf("unmarshaling stdout as entry list: %v\noutput: %s", err, stdout)
	}

	if len(entries) == 0 {
		t.Fatalf("expected at least 1 entry from local git fallback, got 0")
	}

	foundCommit := false
	for _, e := range entries {
		if strings.Contains(e.Title, "fix: resilient local commit #77") {
			foundCommit = true
			if e.Metadata["source_warning"] != "github_api_unavailable_local_fallback" {
				t.Errorf("expected metadata.source_warning to indicate local fallback, got: %v", e.Metadata["source_warning"])
			}
		}
	}

	if !foundCommit {
		t.Fatalf("local commit not found in returned entries: %+v", entries)
	}
}

func TestE2E_BenchmarkMetrics(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	baseTemp := t.TempDir()
	benchDir := filepath.Join(baseTemp, "benchmarks")
	csvPath := filepath.Join(benchDir, "metrics.csv")

	dates := []string{"2026-10-06", "2026-10-07", "2026-10-08"}
	tokens := [][]int64{
		{120, 45, 165},
		{210, 80, 290},
		{310, 110, 420},
	}

	for i, d := range dates {
		transcriptPath := filepath.Join(baseTemp, fmt.Sprintf("transcript-%s.jsonl", d))
		jsonLine := fmt.Sprintf(`{"usage":{"input_tokens":%d,"output_tokens":%d,"total_tokens":%d}}`+"\n",
			tokens[i][0], tokens[i][1], tokens[i][2])
		if err := os.WriteFile(transcriptPath, []byte(jsonLine), 0644); err != nil {
			t.Fatalf("writing transcript for %s: %v", d, err)
		}

		_, stderr, code, err := runCmd(ctx, "", binBenchmark, nil,
			"-record",
			"-date", d,
			"-transcript", transcriptPath,
			"-csv", csvPath,
			"-wall-clock-ms", "350",
			"-peak-rss-mb", "14.5",
			"-entries", "3",
		)
		if err != nil || code != 0 {
			t.Fatalf("recording benchmark for %s failed: %v\nstderr: %s", d, err, stderr)
		}
	}

	// 1. Verify CSV schema and row contents
	f, err := os.Open(csvPath)
	if err != nil {
		t.Fatalf("opening csv: %v", err)
	}
	defer f.Close()

	reader := csv.NewReader(f)
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("parsing csv: %v", err)
	}

	if len(records) != 4 { // Header + 3 data rows
		t.Fatalf("expected 4 rows in CSV, got %d", len(records))
	}

	expectedHeader := strings.Split(benchmark.CSVHeader, ",")
	for idx, col := range expectedHeader {
		if records[0][idx] != col {
			t.Errorf("column header mismatch at %d: expected %s, got %s", idx, col, records[0][idx])
		}
	}

	for idx := 1; idx <= 3; idx++ {
		row := records[idx]
		totalToks, _ := strconv.ParseInt(row[6], 10, 64)
		if totalToks >= benchmark.DefaultTokenCeiling {
			t.Errorf("row %d exceeded token ceiling %d: got %d", idx, benchmark.DefaultTokenCeiling, totalToks)
		}
	}

	// 2. Verify report mode
	reportOut, reportErr, code, err := runCmd(ctx, "", binBenchmark, nil,
		"-report",
		"-csv", csvPath,
	)
	if err != nil || code != 0 {
		t.Fatalf("benchmark -report failed: %v\nstderr: %s", err, reportErr)
	}
	if !strings.Contains(reportOut, "Total days tracked") {
		t.Errorf("report output missing expected summary: %s", reportOut)
	}

	// 3. Verify chart mode
	chartOut, chartErr, code, err := runCmd(ctx, "", binBenchmark, nil,
		"-chart",
		"-csv", csvPath,
	)
	if err != nil || code != 0 {
		t.Fatalf("benchmark -chart failed: %v\nstderr: %s", err, chartErr)
	}
	if !strings.Contains(chartOut, "Daily LLM Token Consumption") {
		t.Errorf("chart output missing header: %s", chartOut)
	}
}
