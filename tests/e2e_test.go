//go:build e2e

// This end-to-end suite exercises the headless pipeline against hermetic mock
// servers. The legacy REST SUBMISSION flow (create/edit/delete + plan-derived
// markdown) was retired in S5 containment and its read surface (internal/api) and
// stub binary (cmd/sync-timesheet) were deleted in S8; the approval-bound MCP
// submission e2e remains deferred to S9. The tests below cover collection
// (fetch-git with its outage fallback), benchmarking, and the new cmd/timesheet
// surface (preflight, review, export, preview, approve, and a fail-closed/read-only
// submit).
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
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/benchmark"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

var (
	binDir       string
	binFetchGit  string
	binTimesheet string
	binBenchmark string
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
		{"fetch-git", "./cmd/fetch-git", &binFetchGit},
		{"timesheet", "./cmd/timesheet", &binTimesheet},
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

// readOnlyGradionServer is gone: internal/api (the legacy REST read surface) was
// deleted in S8. The new submission path speaks MCP and its e2e is deferred to S9;
// the cmd/timesheet surface is exercised below with --offline (zero remote access).

// valueAfterLine extracts the value following a "key:" prefix on its own line.
func valueAfterLine(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key) {
			return strings.TrimSpace(strings.TrimPrefix(line, key))
		}
	}
	return ""
}

func TestE2E_FullHeadlessPipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	testDate := "2026-10-08"
	baseTemp := t.TempDir()

	benchmarksDir := filepath.Join(baseTemp, "benchmarks")

	// Step 5: The new cmd/timesheet surface (offline, hermetic). Seed the workflow
	// record with collected evidence, then preflight -> review -> export ->
	// preview -> approve -> a fail-closed submit.
	workflowDir := filepath.Join(baseTemp, "workflow-records")
	t.Setenv("GITHUB_TOKEN", "e2e-git-token")
	cfgPath := filepath.Join(baseTemp, "timesheet.json")
	vaultDir := filepath.Join(baseTemp, "vault")
	configDoc := map[string]any{
		"storage_dir":     workflowDir,
		"vault":           vaultDir,
		"daily_dir":       "Daily",
		"classifications": map[string]string{"Proj": "cls-1"},
	}
	cfgBytes, _ := json.Marshal(configDoc)
	if err := os.WriteFile(cfgPath, cfgBytes, 0o600); err != nil {
		t.Fatalf("writing timesheet config: %v", err)
	}

	wfStore, err := workflow.NewJSONFileStore(workflowDir)
	if err != nil {
		t.Fatalf("workflow store: %v", err)
	}
	commitStart := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	if _, err := wfStore.UpdateDay(ctx, testDate, func(rec *workflow.Record) error {
		rec.CollectedEvidence = append(rec.CollectedEvidence, evidence.Evidence{
			ID: "e2e-commit", Kind: evidence.SourceLocalGit, Title: "feat: calendar parser",
			Start: commitStart, End: commitStart.Add(30 * time.Minute), Duration: evidence.UnknownDuration(),
			SourceRefs: []evidence.SourceRef{{Kind: evidence.SourceLocalGit, Repository: "acme/repo", Branch: "feat/x", At: commitStart}},
			ObservedAt: commitStart,
		})
		rec.Confirmed = append(rec.Confirmed, workflow.ConfirmedInterval{
			ID: "c1", Date: testDate, StartTime: "09:00", EndTime: "10:00",
			Project: "Proj", Description: "work", Attendance: true, ClassificationID: "cls-1",
		})
		rec.Review = &workflow.ReviewState{Reviewed: true}
		return nil
	}); err != nil {
		t.Fatalf("seeding workflow record: %v", err)
	}

	// Step 5a: preflight (offline; no remote).
	preOut, preErr, code, err := runCmd(ctx, "", binTimesheet, nil,
		"preflight", "--config", cfgPath, "--storage-dir", workflowDir, "--offline",
	)
	if err != nil || code != 0 {
		t.Fatalf("timesheet preflight failed with code %d: %v\nstderr: %s", code, err, preErr)
	}
	if !strings.Contains(preOut, "timezone") || !strings.Contains(preOut, "configuration") {
		t.Fatalf("preflight output missing checks: %s", preOut)
	}

	// Step 5b: review (read-only proposal print).
	revOut, revErr, code, err := runCmd(ctx, "", binTimesheet, nil,
		"review", "--config", cfgPath, "--storage-dir", workflowDir, "--date", testDate, "--no-editorial",
	)
	if err != nil || code != 0 {
		t.Fatalf("timesheet review failed with code %d: %v\nstderr: %s", code, err, revErr)
	}
	if !strings.Contains(revOut, "proposal") {
		t.Fatalf("review output missing the proposal: %s", revOut)
	}

	// Step 5c: export (read-only with respect to the remote).
	if _, expErr, code, err := runCmd(ctx, "", binTimesheet, nil,
		"export", "--config", cfgPath, "--storage-dir", workflowDir, "--date", testDate,
	); err != nil || code != 0 {
		t.Fatalf("timesheet export failed with code %d: %v\nstderr: %s", code, err, expErr)
	}
	noteBytes, err := os.ReadFile(filepath.Join(vaultDir, "Daily", testDate+".md"))
	if err != nil {
		t.Fatalf("reading exported note: %v", err)
	}
	if !strings.Contains(string(noteBytes), "<!-- timesheet:begin v1 -->") {
		t.Fatalf("exported note missing the managed marker:\n%s", noteBytes)
	}

	// Step 5d: preview (zero remote writes) then approve the exact plan.
	prevOut, prevErr, code, err := runCmd(ctx, "", binTimesheet, nil,
		"preview", "--config", cfgPath, "--storage-dir", workflowDir, "--date", testDate, "--offline",
	)
	if err != nil || code != 0 {
		t.Fatalf("timesheet preview failed with code %d: %v\nstderr: %s", code, err, prevErr)
	}
	planID := valueAfterLine(prevOut, "plan_id:")
	if planID == "" {
		t.Fatalf("preview output missing plan_id: %s", prevOut)
	}
	apprOut, apprErr, code, err := runCmd(ctx, "", binTimesheet, nil,
		"approve", "--config", cfgPath, "--storage-dir", workflowDir, "--date", testDate, "--plan", planID,
	)
	if err != nil || code != 0 {
		t.Fatalf("timesheet approve failed with code %d: %v\nstderr: %s", code, err, apprErr)
	}
	if valueAfterLine(apprOut, "approval_id:") == "" {
		t.Fatalf("approve output missing approval_id: %s", apprOut)
	}

	// Step 5e: an OFFLINE submission FAILS CLOSED with zero remote writes.
	subOut, _, code, _ := runCmd(ctx, "", binTimesheet, nil,
		"submit", "--config", cfgPath, "--storage-dir", workflowDir, "--date", testDate, "--offline",
	)
	if code == 0 {
		t.Fatalf("an offline submission must fail closed, got exit 0: %s", subOut)
	}
	if !strings.Contains(subOut, "remote_unavailable") {
		t.Fatalf("expected a fail-closed remote_unavailable message, got: %s", subOut)
	}

	// Step 7: Empirical benchmark logging. The token columns are RECORD-FED: the
	// benchmark lane copies the day's editorial-child usage from the workflow
	// record and parses no transcript at all.
	benchRecords := filepath.Join(baseTemp, "benchmark-records")
	benchStore, err := workflow.NewJSONFileStore(benchRecords)
	if err != nil {
		t.Fatalf("benchmark workflow store: %v", err)
	}
	if _, err := benchStore.UpdateDay(ctx, testDate, func(rec *workflow.Record) error {
		rec.Usage = &workflow.EditorialUsage{
			Date:            testDate,
			Editorial:       workflow.KnownMeasurement(295),
			EditorialInput:  workflow.KnownMeasurement(190),
			EditorialOutput: workflow.KnownMeasurement(75),
			EditorialCache:  workflow.KnownMeasurement(30),
			EditorialRuns:   1,
			Invoked:         true,
		}
		return nil
	}); err != nil {
		t.Fatalf("seeding benchmark record: %v", err)
	}

	csvPath := filepath.Join(benchmarksDir, "metrics.csv")
	benchOut, benchErrOut, code, err := runCmd(ctx, "", binBenchmark, nil,
		"-record",
		"-date", testDate,
		"-csv", csvPath,
		"-storage-dir", benchRecords,
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
	if metrics[0].Date != testDate ||
		metrics[0].TotalTokens == nil || *metrics[0].TotalTokens != 295 ||
		metrics[0].InputTokens == nil || *metrics[0].InputTokens != 190 ||
		metrics[0].OutputTokens == nil || *metrics[0].OutputTokens != 75 ||
		metrics[0].CacheTokens == nil || *metrics[0].CacheTokens != 30 {
		t.Fatalf("record-fed metric mismatch: %+v", metrics[0])
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

	// The token columns are the day's EDITORIAL-CHILD lane, copied from the real
	// workflow store; no transcript is written or parsed anywhere in this test.
	days := []struct {
		date                  string
		in, out, cache, total int64
	}{
		{"2026-10-06", 120, 45, 15, 180},
		{"2026-10-07", 210, 80, 5, 295},
		{"2026-10-08", 310, 110, 0, 420},
	}

	recordsRoot := filepath.Join(baseTemp, "records")
	store, err := workflow.NewJSONFileStore(recordsRoot)
	if err != nil {
		t.Fatalf("workflow store: %v", err)
	}
	for _, d := range days {
		d := d
		if _, err := store.UpdateDay(ctx, d.date, func(rec *workflow.Record) error {
			rec.Usage = &workflow.EditorialUsage{
				Date:            d.date,
				Editorial:       workflow.KnownMeasurement(d.total),
				EditorialInput:  workflow.KnownMeasurement(d.in),
				EditorialOutput: workflow.KnownMeasurement(d.out),
				EditorialCache:  workflow.KnownMeasurement(d.cache),
				EditorialRuns:   1,
				Invoked:         true,
			}
			return nil
		}); err != nil {
			t.Fatalf("seeding record for %s: %v", d.date, err)
		}

		_, stderr, code, err := runCmd(ctx, "", binBenchmark, nil,
			"-record",
			"-date", d.date,
			"-csv", csvPath,
			"-storage-dir", recordsRoot,
			"-wall-clock-ms", "350",
			"-peak-rss-mb", "14.5",
			"-entries", "3",
		)
		if err != nil || code != 0 {
			t.Fatalf("recording benchmark for %s failed: %v\nstderr: %s", d.date, err, stderr)
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
		if totalToks != days[idx-1].total {
			t.Errorf("row %d total tokens = %d, want the record's %d", idx, totalToks, days[idx-1].total)
		}
		if totalToks >= workflow.EditorialTokenCeiling {
			t.Errorf("row %d exceeded the editorial ceiling %d: got %d", idx, workflow.EditorialTokenCeiling, totalToks)
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
