package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/export"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/keychain"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/run"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/submit"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

const testDate = "2026-10-06"

func runArgs(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runCLI(context.Background(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func writeConfig(t *testing.T, dir string, cfg cliConfig) string {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	path := filepath.Join(dir, "timesheet.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func seedRecord(t *testing.T, storageDir, date string, mutate func(*workflow.Record)) {
	t.Helper()
	store, err := workflow.NewJSONFileStore(storageDir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if _, err := store.UpdateDay(context.Background(), date, func(rec *workflow.Record) error {
		if mutate != nil {
			mutate(rec)
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

func loadRecord(t *testing.T, storageDir, date string) workflow.Record {
	t.Helper()
	store, err := workflow.NewJSONFileStore(storageDir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	rec, err := store.GetDay(context.Background(), date)
	if err != nil {
		t.Fatalf("get day: %v", err)
	}
	return *rec
}

func gitEvidence() evidence.Evidence {
	start := time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC)
	return evidence.Evidence{
		ID: "e1", Kind: evidence.SourceLocalGit, Title: "fix parser",
		Start: start, End: start.Add(30 * time.Minute), Duration: evidence.UnknownDuration(),
		SourceRefs: []evidence.SourceRef{{Kind: evidence.SourceLocalGit, Repository: "acme/repo", Branch: "feat/x", At: start}},
		ObservedAt: start,
	}
}

func readyRecord(date string) workflow.Record {
	return workflow.Record{
		SchemaVersion: workflow.SchemaVersion,
		Date:          date,
		Confirmed: []workflow.ConfirmedInterval{{
			ID: "c1", Date: date, StartTime: "09:00", EndTime: "10:00",
			Project: "Proj", Description: "work", Attendance: true, ClassificationID: "cls-1",
		}},
		Review: &workflow.ReviewState{Reviewed: true},
	}
}

func parseBlockID(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"work ", "meeting ", "manual "} {
			if strings.HasPrefix(line, prefix) {
				fields := strings.Fields(line)
				if len(fields) >= 2 {
					return fields[1]
				}
			}
		}
	}
	return ""
}

func valueAfter(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key) {
			return strings.TrimSpace(strings.TrimPrefix(line, key))
		}
	}
	return ""
}

func TestRunCLI_UnknownSubcommand(t *testing.T) {
	code, _, stderr := runArgs(t, "bogus")
	if code != exitUsage {
		t.Fatalf("want a usage exit, got %d (%s)", code, stderr)
	}
}

func TestRunCLI_PreflightOK(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, cliConfig{})
	code, stdout, stderr := runArgs(t, "preflight", "--config", cfgPath, "--storage-dir", filepath.Join(dir, "records"))
	if code != exitOK {
		t.Fatalf("want ok, got %d (%s)", code, stderr)
	}
	for _, want := range []string{"timezone", "credentials", "contracts"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("want %q in the preflight output:\n%s", want, stdout)
		}
	}
}

func TestRunCLI_PreflightInvalidTimezoneRefused(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	cfg := cliConfig{}
	cfg.Evidence.Timezone = "Not/AZone"
	cfgPath := writeConfig(t, dir, cfg)
	code, stdout, _ := runArgs(t, "preflight", "--config", cfgPath, "--storage-dir", filepath.Join(dir, "records"))
	if code != exitBlocked {
		t.Fatalf("want a blocked exit, got %d", code)
	}
	if !strings.Contains(stdout, "RUN_PREFLIGHT_TIMEZONE_INVALID") {
		t.Fatalf("want the timezone code, got:\n%s", stdout)
	}
}

func TestRunCLI_ReviewAppliesDecisions(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence, gitEvidence())
	})

	// Read-only proposal print.
	code, stdout, stderr := runArgs(t, "review", "--config", cfgPath, "--storage-dir", storage, "--date", testDate, "--no-editorial")
	if code != exitOK {
		t.Fatalf("review: %d (%s)", code, stderr)
	}
	blockID := parseBlockID(stdout)
	if blockID == "" {
		t.Fatalf("want a block id in the proposal:\n%s", stdout)
	}

	decisionsPath := filepath.Join(dir, "decisions.json")
	writeJSON(t, decisionsPath, run.ReviewDecisions{Confirm: []run.ConfirmDecision{{BlockID: blockID}}})
	code, _, stderr = runArgs(t, "review", "--config", cfgPath, "--storage-dir", storage, "--date", testDate, "--decisions", decisionsPath)
	if code != exitOK {
		t.Fatalf("review apply: %d (%s)", code, stderr)
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Confirmed) != 1 {
		t.Fatalf("want a confirmed interval, got %+v", rec.Confirmed)
	}
}

func TestRunCLI_ExportWritesNote(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	vault := filepath.Join(dir, "vault")
	cfgPath := writeConfig(t, dir, cliConfig{Vault: vault, DailyDir: "Daily"})
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
	})

	code, stdout, stderr := runArgs(t, "export", "--config", cfgPath, "--storage-dir", storage, "--date", testDate)
	if code != exitOK {
		t.Fatalf("export: %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "exported") {
		t.Fatalf("want an exported message, got:\n%s", stdout)
	}
	blob, err := os.ReadFile(filepath.Join(vault, "Daily", testDate+".md"))
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	if !strings.Contains(string(blob), export.MarkerBegin) {
		t.Fatalf("want the managed marker, got:\n%s", blob)
	}
}

func TestRunCLI_PreviewApproveSubmitOffline(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := cliConfig{Classifications: map[string]string{"Proj": "cls-1"}}
	cfgPath := writeConfig(t, dir, cfg)
	seedRecord(t, storage, testDate, func(rec *workflow.Record) { *rec = readyRecord(testDate) })

	code, stdout, stderr := runArgs(t, "preview", "--config", cfgPath, "--storage-dir", storage, "--date", testDate)
	if code != exitOK {
		t.Fatalf("preview: %d (%s)", code, stderr)
	}
	planID := valueAfter(stdout, "plan_id:")
	if planID == "" {
		t.Fatalf("want a plan_id, got:\n%s", stdout)
	}

	code, stdout, stderr = runArgs(t, "approve", "--config", cfgPath, "--storage-dir", storage, "--date", testDate, "--plan", planID)
	if code != exitOK {
		t.Fatalf("approve: %d (%s)", code, stderr)
	}
	if valueAfter(stdout, "approval_id:") == "" {
		t.Fatalf("want an approval_id, got:\n%s", stdout)
	}

	// Offline submission is refused (remote unavailable) with zero writes.
	code, stdout, _ = runArgs(t, "submit", "--config", cfgPath, "--storage-dir", storage, "--date", testDate, "--offline")
	if code != exitBlocked {
		t.Fatalf("want a blocked submit, got %d (%s)", code, stdout)
	}
	if !strings.Contains(stdout, "remote_unavailable") {
		t.Fatalf("want remote_unavailable, got:\n%s", stdout)
	}
}

func TestRunCLI_SubmitWithoutApprovalBlocked(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedRecord(t, storage, testDate, func(rec *workflow.Record) { *rec = readyRecord(testDate) })

	code, stdout, _ := runArgs(t, "submit", "--config", cfgPath, "--storage-dir", storage, "--date", testDate)
	if code != exitBlocked {
		t.Fatalf("want a blocked submit, got %d (%s)", code, stdout)
	}
	if !strings.Contains(stdout, "no_active_approval") {
		t.Fatalf("want no_active_approval, got:\n%s", stdout)
	}
}

func TestRunCLI_ApproveRequiresPlan(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	cfgPath := writeConfig(t, dir, cliConfig{})
	code, _, _ := runArgs(t, "approve", "--config", cfgPath, "--storage-dir", filepath.Join(dir, "records"), "--date", testDate)
	if code != exitUsage {
		t.Fatalf("approve without --plan must be a usage error, got %d", code)
	}
}

// F1: the DOCUMENTED snake_case resolve.json shape must decode into
// submit.Resolution (struct tags), and the decision must reach ResolveUnresolved.
func TestRunCLI_ResolveParsesDocumentedKeys(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})

	key := workflow.DeriveOperationKey(workflow.OperationOutcome{
		Date: testDate, Action: "create", LocalID: "c1", StartTime: "09:00", EndTime: "10:00",
	})
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		workflow.UpsertOutcome(rec, workflow.OperationOutcome{
			OperationKey: key, LocalID: "c1", Action: "create", Status: workflow.OutcomeUncertain,
			Date: testDate, StartTime: "09:00", EndTime: "10:00",
		})
	})

	// A plain map, so ONLY the struct tags can decode it.
	decisionsPath := filepath.Join(dir, "resolve.json")
	writeJSON(t, decisionsPath, []map[string]string{{
		"operation_key": key, "kind": "adopt", "remote_id": "r1", "local_id": "c1",
	}})

	code, stdout, stderr := runArgs(t, "resolve", "--config", cfgPath, "--storage-dir", storage, "--date", testDate, "--decisions", decisionsPath)
	if code != exitOK {
		t.Fatalf("resolve: %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "resolved 1/1") {
		t.Fatalf("want 1/1 resolved, got:\n%s", stdout)
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Outcomes) != 1 || rec.Outcomes[0].Status != workflow.OutcomeConfirmed {
		t.Fatalf("the documented-key decision must reach ResolveUnresolved, got %+v", rec.Outcomes)
	}
}

// F2: a decision that matches no unresolved operation must be an explicit error.
func TestRunCLI_ResolveUnmatchedDecisionErrors(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedRecord(t, storage, testDate, func(rec *workflow.Record) { *rec = readyRecord(testDate) })

	decisionsPath := filepath.Join(dir, "resolve.json")
	writeJSON(t, decisionsPath, []map[string]string{{"operation_key": "no-such-key", "kind": "not_applied"}})

	code, stdout, stderr := runArgs(t, "resolve", "--config", cfgPath, "--storage-dir", storage, "--date", testDate, "--decisions", decisionsPath)
	if code != exitError {
		t.Fatalf("an unmatched decision must be an error, got %d (%s)", code, stdout)
	}
	if !strings.Contains(stdout, "resolved 0/1") {
		t.Fatalf("want 0/1 resolved, got:\n%s", stdout)
	}
	if !strings.Contains(stderr, "matched no unresolved operation") {
		t.Fatalf("want an explicit warning, got:\n%s", stderr)
	}
}

// terminalResolver always returns a terminal Keychain failure.
type terminalResolver struct{}

func (terminalResolver) Lookup(ctx context.Context, service, account string) (string, error) {
	return "", &keychain.OperatingError{Outcome: keychain.OutcomeTerminal, Code: "KEYCHAIN_ERR_TIMEOUT", Message: "terminal"}
}

// F3: a read-only subcommand must not resolve a git credential (so a terminal
// Keychain failure cannot abort it).
func TestRunCLI_ExportDoesNotResolveGitCredential(t *testing.T) {
	restore := keychain.SetDefaultResolver(terminalResolver{})
	t.Cleanup(restore)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	vault := filepath.Join(dir, "vault")
	cfgPath := writeConfig(t, dir, cliConfig{Vault: vault, DailyDir: "Daily"})
	seedRecord(t, storage, testDate, func(rec *workflow.Record) { *rec = readyRecord(testDate) })

	code, _, stderr := runArgs(t, "export", "--config", cfgPath, "--storage-dir", storage, "--date", testDate)
	if code != exitOK {
		t.Fatalf("export must not resolve a git credential: %d (%s)", code, stderr)
	}
}

// F4: --ack applies independently of --decisions, and does not mark the day
// reviewed by itself.
func TestRunCLI_AckWithoutDecisions(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		rec.CollectedEvidence = append(rec.CollectedEvidence, gitEvidence())
	})

	code, stdout, stderr := runArgs(t, "review", "--config", cfgPath, "--storage-dir", storage, "--date", testDate, "--ack", "cal,git", "--no-editorial")
	if code != exitOK {
		t.Fatalf("review --ack: %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "acknowledged") {
		t.Fatalf("want an acknowledgment message, got:\n%s", stdout)
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Review == nil || len(rec.Review.AcknowledgedSources) != 2 {
		t.Fatalf("want 2 acknowledged sources, got %+v", rec.Review)
	}
	if rec.Review.Reviewed {
		t.Fatalf("--ack alone must not mark the day reviewed")
	}
}

// F5: --from/--to iterates days (per-day isolation).
func TestRunCLI_DateRangeIteration(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	vault := filepath.Join(dir, "vault")
	cfgPath := writeConfig(t, dir, cliConfig{Vault: vault, DailyDir: "Daily"})
	seedRecord(t, storage, "2026-10-06", func(rec *workflow.Record) { *rec = readyRecord("2026-10-06") })
	seedRecord(t, storage, "2026-10-07", func(rec *workflow.Record) { *rec = readyRecord("2026-10-07") })

	code, stdout, stderr := runArgs(t, "export", "--config", cfgPath, "--storage-dir", storage, "--from", "2026-10-06", "--to", "2026-10-07")
	if code != exitOK {
		t.Fatalf("export range: %d (%s)", code, stderr)
	}
	for _, d := range []string{"2026-10-06", "2026-10-07"} {
		if !strings.Contains(stdout, d) {
			t.Errorf("want %s exported, got:\n%s", d, stdout)
		}
		if _, err := os.Stat(filepath.Join(vault, "Daily", d+".md")); err != nil {
			t.Errorf("want a note for %s: %v", d, err)
		}
	}
}

// F5: resolveSecretRef rejects a raw secret and an empty service.
func TestResolveSecretRef_RejectsRawSecret(t *testing.T) {
	if _, err := resolveSecretRef(context.Background(), "sk-live-raw-secret"); err == nil {
		t.Fatalf("a raw secret must be rejected")
	}
	if _, err := resolveSecretRef(context.Background(), "keychain:"); err == nil {
		t.Fatalf("an empty keychain service must be rejected")
	}
	if tok, err := resolveSecretRef(context.Background(), ""); err != nil || tok != "" {
		t.Fatalf("an empty reference must be a no-op, got %q %v", tok, err)
	}
}

func TestRunCLI_ResolveAdopts(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})

	key := workflow.DeriveOperationKey(workflow.OperationOutcome{
		Date: testDate, Action: "create", LocalID: "c1", StartTime: "09:00", EndTime: "10:00",
	})
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		workflow.UpsertOutcome(rec, workflow.OperationOutcome{
			OperationKey: key, LocalID: "c1", Action: "create", Status: workflow.OutcomeUncertain,
			Date: testDate, StartTime: "09:00", EndTime: "10:00",
		})
	})

	decisionsPath := filepath.Join(dir, "resolve.json")
	writeJSON(t, decisionsPath, []submit.Resolution{{
		OperationKey: key, Kind: submit.ResolutionAdopt, RemoteID: "r1", LocalID: "c1",
	}})
	code, _, stderr := runArgs(t, "resolve", "--config", cfgPath, "--storage-dir", storage, "--date", testDate, "--decisions", decisionsPath)
	if code != exitOK {
		t.Fatalf("resolve: %d (%s)", code, stderr)
	}
	rec := loadRecord(t, storage, testDate)
	if len(rec.Outcomes) != 1 || rec.Outcomes[0].Status != workflow.OutcomeConfirmed {
		t.Fatalf("want a confirmed outcome, got %+v", rec.Outcomes)
	}
}
