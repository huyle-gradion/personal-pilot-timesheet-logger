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

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/editorial"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence/plugins"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/export"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/keychain"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/mcp"
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
	for _, want := range []string{"timezone", "credentials", "configuration"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("want %q in the preflight output:\n%s", want, stdout)
		}
	}
}

// F1 (PREFLIGHT-RUNTIME-1): the `--deep` flag description must mirror the usage
// line (the flag package prints it on any parse error).
func TestRunCLI_DeepFlagDescriptionAligned(t *testing.T) {
	code, _, stderr := runArgs(t, "preflight", "--bogus")
	if code != exitUsage {
		t.Fatalf("want a usage exit, got %d", code)
	}
	if !strings.Contains(stderr, "validate ALL configured sources (including disabled) during preflight") {
		t.Fatalf("the -deep flag description must match the usage line, got:\n%s", stderr)
	}
	if strings.Contains(stderr, "deeper contract verification") {
		t.Fatalf("the stale -deep description must be gone, got:\n%s", stderr)
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

// submitFakeRemote is a hermetic run.Remote whose writes are counted (and never
// expected on the blocked path).
type submitFakeRemote struct{ writes int }

func (f *submitFakeRemote) Initialize(context.Context) (mcp.ServerInfo, error) {
	return mcp.ServerInfo{}, nil
}
func (f *submitFakeRemote) VerifyCapabilities(context.Context) error { return nil }
func (f *submitFakeRemote) Classifications(context.Context, string) ([]gradion.Classification, error) {
	return nil, nil
}
func (f *submitFakeRemote) ClassificationCatalog(_ context.Context, query string) (gradion.Catalog, error) {
	return gradion.Catalog{Complete: true, Query: query, Source: "live", ObservedAt: time.Now().UTC(),
		Options: []gradion.ClassificationOption{{ID: "cls-1"}}}, nil
}
func (f *submitFakeRemote) ListEntries(context.Context, string, string, int) (gradion.Listing, error) {
	return gradion.Listing{Complete: true, ObservedAt: time.Now().UTC()}, nil
}
func (f *submitFakeRemote) CreateEntry(context.Context, gradion.CreateRequest) (gradion.WriteResult, error) {
	f.writes++
	return gradion.WriteResult{}, nil
}
func (f *submitFakeRemote) EditEntry(context.Context, gradion.EditRequest) (gradion.WriteResult, error) {
	f.writes++
	return gradion.WriteResult{}, nil
}

// W1: the CLI submit entry point fails closed (missing_catalog) when a remote is
// present but no catalog can be obtained — BEFORE any write.
func TestRunCLI_SubmitMissingCatalogBlocksZeroWrites(t *testing.T) {
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		rec.Plans = []workflow.SubmissionPlan{{PlanID: "p1", Date: testDate}}
		rec.Approvals = []workflow.Approval{{ApprovalID: "p1", PlanID: "p1", Date: testDate}}
	})
	store, err := workflow.NewJSONFileStore(storage)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	remote := &submitFakeRemote{}
	r, err := run.New(run.Deps{Store: store, Registry: evidence.NewRegistry(), Remote: remote}, run.Config{})
	if err != nil {
		t.Fatalf("run.New: %v", err)
	}

	// No configured endpoint => no catalog is supplied to Submit, which must fail
	// closed with missing_catalog against the present remote.
	var stdout, stderr bytes.Buffer
	code := cmdSubmit(context.Background(), r, cliConfig{}, testDate, "", false, &stdout, &stderr)
	if code != exitBlocked {
		t.Fatalf("want a blocked submit, got %d (%s)", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), run.BlockMissingCatalog) {
		t.Fatalf("want %s, got:\n%s", run.BlockMissingCatalog, stdout.String())
	}
	if remote.writes != 0 {
		t.Fatalf("a missing catalog must produce ZERO writes, got %d", remote.writes)
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

// F2 (CONFIG-COLLECT-1): buildDeps must NOT resolve a git credential for a
// mode:"local" git source, so a terminal Keychain failure cannot abort a
// read-only subcommand.
func TestRunCLI_LocalGitModeDoesNotResolveCredential(t *testing.T) {
	restore := keychain.SetDefaultResolver(terminalResolver{})
	t.Cleanup(restore)
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	vault := filepath.Join(dir, "vault")
	cfg := cliConfig{Vault: vault, DailyDir: "Daily"}
	cfg.Evidence.Sources = []evidence.SourceConfig{{
		ID: "git-work", Type: plugins.TypeGit, Enabled: true,
		Config: map[string]string{plugins.GitConfigMode: plugins.GitModeLocal},
	}}
	cfgPath := writeConfig(t, dir, cfg)
	seedRecord(t, storage, testDate, func(rec *workflow.Record) { *rec = readyRecord(testDate) })

	code, _, stderr := runArgs(t, "export", "--config", cfgPath, "--storage-dir", storage, "--date", testDate)
	if code != exitOK {
		t.Fatalf("a mode:local git source must not resolve a credential: %d (%s)", code, stderr)
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

// S8-FOLLOWUP-1: a day without evidence must produce an empty draft, not an error.
func TestRunCLI_ReviewEmptyDaySucceeds(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{}) // NO configured sources

	code, stdout, stderr := runArgs(t, "review", "--config", cfgPath, "--storage-dir", storage, "--date", testDate, "--no-editorial")
	if code != exitOK {
		t.Fatalf("review on an empty day must succeed, got %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "proposal (reviewed=0 blocks") {
		t.Fatalf("want a 0-block proposal, got:\n%s", stdout)
	}
	// The record is materialized (a bare review persists the day) and, with NO
	// sources, no collection summary is fabricated.
	rec := loadRecord(t, storage, testDate)
	if rec.Collection != nil {
		t.Fatalf("no sources must not fabricate a collection summary, got %+v", rec.Collection)
	}
}

// S8-FOLLOWUP-1: review --decisions mark_empty on a NO-RECORD day lazily creates
// the record, marks it reviewed-empty, and exports a reviewed-empty note.
func TestRunCLI_ReviewMarkEmptyCreatesRecordAndExports(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	vault := filepath.Join(dir, "vault")
	cfgPath := writeConfig(t, dir, cliConfig{Vault: vault, DailyDir: "Daily"})

	decisionsPath := filepath.Join(dir, "mark_empty.json")
	writeJSON(t, decisionsPath, run.ReviewDecisions{MarkEmpty: true})

	code, _, stderr := runArgs(t, "review", "--config", cfgPath, "--storage-dir", storage, "--date", testDate, "--decisions", decisionsPath, "--no-editorial")
	if code != exitOK {
		t.Fatalf("mark-empty review must succeed on a no-record day, got %d (%s)", code, stderr)
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Review == nil || !rec.Review.Reviewed || !rec.Review.Empty {
		t.Fatalf("want a reviewed-empty record, got %+v", rec.Review)
	}
	if len(rec.Confirmed) != 0 {
		t.Fatalf("a reviewed-empty day must invent no entries, got %+v", rec.Confirmed)
	}

	code, stdout, stderr := runArgs(t, "export", "--config", cfgPath, "--storage-dir", storage, "--date", testDate)
	if code != exitOK {
		t.Fatalf("export: %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "reviewed-empty") {
		t.Fatalf("want a reviewed-empty status, got:\n%s", stdout)
	}
	blob, err := os.ReadFile(filepath.Join(vault, "Daily", testDate+".md"))
	if err != nil {
		t.Fatalf("read note: %v", err)
	}
	if !strings.Contains(string(blob), "reviewed-empty") {
		t.Fatalf("want a reviewed-empty note, got:\n%s", blob)
	}
}

// F5: `review` with NO sources must not clobber seeded Confirmed intervals or
// Review decisions (the removed guard + the empty Collect are upserts only).
func TestRunCLI_ReviewNoSourcesPreservesDecisions(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{}) // NO sources

	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		rec.Review.AcknowledgedSources = []string{"cal"}
	})
	before := loadRecord(t, storage, testDate)

	code, _, stderr := runArgs(t, "review", "--config", cfgPath, "--storage-dir", storage, "--date", testDate, "--no-editorial")
	if code != exitOK {
		t.Fatalf("review: %d (%s)", code, stderr)
	}
	after := loadRecord(t, storage, testDate)
	if len(after.Confirmed) != len(before.Confirmed) || after.Confirmed[0].Description != before.Confirmed[0].Description {
		t.Fatalf("Confirmed must be preserved:\n before=%+v\n after=%+v", before.Confirmed, after.Confirmed)
	}
	if after.Review == nil || !after.Review.Reviewed || len(after.Review.AcknowledgedSources) != 1 || after.Review.AcknowledgedSources[0] != "cal" {
		t.Fatalf("Review decisions must be preserved, got %+v", after.Review)
	}
}

// F6: the empty-day path WITH an active editor must succeed and invoke NO agent
// (an empty proposal has zero targets). The command is deliberately a
// non-existent binary, so a test that passes proves the editor was never run.
func TestRunCLI_ReviewEmptyDayWithEditor(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := cliConfig{Editorial: editorial.Config{
		Enabled: true, Adapter: "command", Command: []string{"/nonexistent-editorial-agent"},
	}}
	cfgPath := writeConfig(t, dir, cfg)

	code, stdout, stderr := runArgs(t, "review", "--config", cfgPath, "--storage-dir", storage, "--date", testDate)
	if code != exitOK {
		t.Fatalf("review with an editor on an empty day must succeed, got %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "proposal (reviewed=0 blocks") {
		t.Fatalf("want a 0-block proposal, got:\n%s", stdout)
	}
	rec := loadRecord(t, storage, testDate)
	if rec.Usage == nil || rec.Usage.Invoked || rec.Usage.EditorialRuns != 0 {
		t.Fatalf("an empty proposal must invoke no editor, got %+v", rec.Usage)
	}
}

// S8-FOLLOWUP-2: the preview must show the EXACT operations + a correct total,
// while keeping the machine-readable plan_id:/operations: lines.
func TestRunCLI_PreviewShowsOperationsAndTotal(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfg := cliConfig{Classifications: map[string]string{"Proj": "cls-1"}}
	cfgPath := writeConfig(t, dir, cfg)
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		rec.Confirmed = []workflow.ConfirmedInterval{
			{ID: "c1", Date: testDate, StartTime: "09:00", EndTime: "10:30", Project: "Proj", Description: "first", Attendance: true, ClassificationID: "cls-1"},
			{ID: "c2", Date: testDate, StartTime: "11:00", EndTime: "15:30", Project: "Proj", Description: "second", Attendance: true, ClassificationID: "cls-1"},
		}
	})

	code, stdout, stderr := runArgs(t, "preview", "--config", cfgPath, "--storage-dir", storage, "--date", testDate, "--offline")
	if code != exitOK {
		t.Fatalf("preview: %d (%s)", code, stderr)
	}
	for _, want := range []string{
		"plan_id: ",
		"operations: 2",
		"remote observed: ",
		"remote complete: true",
		"  create 09:00-10:30 Proj first",
		"  create 11:00-15:30 Proj second",
		"total: 6h 0m across 2 operation(s)",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("preview output missing %q:\n%s", want, stdout)
		}
	}
}

// S8-FOLLOWUP-2: a blocked preview keeps the existing `preview blocked:` behavior.
func TestRunCLI_PreviewBlockedStillPrintsReason(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		rec.Confirmed[0].Project = "" // unresolved project -> blocked
	})

	code, stdout, _ := runArgs(t, "preview", "--config", cfgPath, "--storage-dir", storage, "--date", testDate, "--offline")
	if code != exitBlocked {
		t.Fatalf("want a blocked exit, got %d (%s)", code, stdout)
	}
	if !strings.Contains(stdout, "preview blocked: ") {
		t.Fatalf("want the blocked message, got:\n%s", stdout)
	}
}

// S8F2-1: the label comes from the CONFIRMED INTERVAL's own project, not a
// reverse classification-id lookup (which mislabels duplicate ids / unmapped
// projects).
func TestRunCLI_PreviewLabelsFromIntervalProject(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	// Two projects share cls-1 (a reverse lookup would pick "Alpha"); "Gamma" is
	// absent from the map.
	cfg := cliConfig{Classifications: map[string]string{"Alpha": "cls-1", "Zeta": "cls-1"}}
	cfgPath := writeConfig(t, dir, cfg)
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		rec.Confirmed = []workflow.ConfirmedInterval{
			{ID: "c1", Date: testDate, StartTime: "09:00", EndTime: "10:00", Project: "Zeta", Description: "first", Attendance: true, ClassificationID: "cls-1"},
			{ID: "c2", Date: testDate, StartTime: "11:00", EndTime: "12:00", Project: "Gamma", Description: "second", Attendance: true, ClassificationID: "cls-9"},
		}
	})

	code, stdout, stderr := runArgs(t, "preview", "--config", cfgPath, "--storage-dir", storage, "--date", testDate, "--offline")
	if code != exitOK {
		t.Fatalf("preview: %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "  create 09:00-10:00 Zeta first") {
		t.Errorf("the label must be the interval's project (Zeta), not a reverse-map pick:\n%s", stdout)
	}
	if strings.Contains(stdout, "Alpha") {
		t.Errorf("the reverse-map name Alpha must not appear:\n%s", stdout)
	}
	if !strings.Contains(stdout, "  create 11:00-12:00 Gamma second") {
		t.Errorf("an unmapped interval must still show its own project (Gamma):\n%s", stdout)
	}
}

// S8F2-2: a description with a newline must be collapsed to one line.
func TestRunCLI_PreviewCollapsesDescription(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		rec.Confirmed[0].Description = "line one\nline two"
	})

	code, stdout, stderr := runArgs(t, "preview", "--config", cfgPath, "--storage-dir", storage, "--date", testDate, "--offline")
	if code != exitOK {
		t.Fatalf("preview: %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "  create 09:00-10:00 Proj line one line two") {
		t.Errorf("a multi-line description must collapse to one line:\n%s", stdout)
	}
}

// S8F2-4: a midnight-crossing interval contributes its exact total minutes.
func TestRunCLI_PreviewMidnightTotal(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{})
	seedRecord(t, storage, testDate, func(rec *workflow.Record) {
		*rec = readyRecord(testDate)
		rec.Confirmed[0].StartTime = "23:00"
		rec.Confirmed[0].EndTime = "01:00"
	})

	code, stdout, stderr := runArgs(t, "preview", "--config", cfgPath, "--storage-dir", storage, "--date", testDate, "--offline")
	if code != exitOK {
		t.Fatalf("preview: %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, "total: 2h 0m across 1 operation(s)") {
		t.Errorf("a 23:00-01:00 interval must total 2h 0m:\n%s", stdout)
	}
}

// S8F2-4: the edit suffix and the (unclassified) fallback (renderer unit tests).
func TestFormatPlanOperation_EditSuffixAndUnclassified(t *testing.T) {
	edit := workflow.PlanOperation{
		LocalID: "c1", Kind: workflow.PlanOpEdit, RemoteID: "r1",
		StartTime: "09:00", EndTime: "10:00", ClassificationID: "cls-1", Description: "work",
	}
	line := formatPlanOperation(edit, cliConfig{}, nil)
	if !strings.Contains(line, "  edit 09:00-10:00 cls-1 work") || !strings.Contains(line, " remote=r1") {
		t.Fatalf("an edit must show the target remote id, got %q", line)
	}

	if got := operationLabel(workflow.PlanOperation{LocalID: "c1", ClassificationID: ""}, cliConfig{}, nil); got != "(unclassified)" {
		t.Fatalf("want (unclassified), got %q", got)
	}
	// A confirmed interval's project wins over the reverse lookup.
	projects := map[string]string{"c1": "Zeta"}
	if got := operationLabel(workflow.PlanOperation{LocalID: "c1", ClassificationID: "cls-1"}, cliConfig{Classifications: map[string]string{"Alpha": "cls-1"}}, projects); got != "Zeta" {
		t.Fatalf("want the interval project Zeta, got %q", got)
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

// CONFIG-COLLECT-1 Finding A: `review` with NO configured sources must print the
// exact no-sources line and still produce an empty draft.
func TestRunCLI_ReviewReportsNoEvidenceSources(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	dir := t.TempDir()
	storage := filepath.Join(dir, "records")
	cfgPath := writeConfig(t, dir, cliConfig{}) // NO sources

	code, stdout, stderr := runArgs(t, "review", "--config", cfgPath, "--storage-dir", storage, "--date", testDate, "--no-editorial")
	if code != exitOK {
		t.Fatalf("review: %d (%s)", code, stderr)
	}
	if !strings.Contains(stdout, testDate+": No evidence sources configured.") {
		t.Fatalf("want the exact no-sources line, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "proposal (reviewed=0 blocks") {
		t.Fatalf("an empty day must still produce an empty draft, got:\n%s", stdout)
	}
}

// CONFIG-COLLECT-1 visibility: printSourceStatus prints a line for every source
// that is not complete/skipped, and nothing for the rest.
func TestPrintSourceStatus_ShowsOnlyNonCompleteSources(t *testing.T) {
	var buf bytes.Buffer
	sum := evidence.CollectionSummary{
		Date: testDate,
		Sources: []evidence.SourceResult{
			{SourceID: "ok", Status: evidence.StatusComplete},
			{SourceID: "off", Status: evidence.StatusSkipped},
			{SourceID: "git", Status: evidence.StatusPartial, Warnings: []string{"GIT_REMOTE_FETCH_FAILED"}},
			{SourceID: "cal", Status: evidence.StatusIncomplete, ErrorCode: "SOURCE_ERROR"},
		},
	}
	printSourceStatus(&buf, sum)
	out := buf.String()
	if !strings.Contains(out, "source git: partial (GIT_REMOTE_FETCH_FAILED)") {
		t.Errorf("want the partial source line, got:\n%s", out)
	}
	if !strings.Contains(out, "source cal: incomplete (SOURCE_ERROR)") {
		t.Errorf("want the incomplete source line, got:\n%s", out)
	}
	if strings.Contains(out, "source ok:") || strings.Contains(out, "source off:") {
		t.Errorf("complete/skipped sources must not be printed, got:\n%s", out)
	}
}

// stubResolver returns fixed values for known keychain services.
type stubResolver struct{ values map[string]string }

func (s stubResolver) Lookup(ctx context.Context, service, account string) (string, error) {
	if v, ok := s.values[service]; ok {
		return v, nil
	}
	return "", &keychain.OperatingError{Outcome: keychain.OutcomeNotFound, Code: "KEYCHAIN_ERR_NOT_FOUND", Message: "absent"}
}

// PREFLIGHT-RUNTIME-1: the SHIPPED example configs validate their source
// configuration without any fixture. External deps are mocked: a fake Keychain
// resolver, a GitToken for the mode:"both" example, and no Remote (offline).
func TestShippedExamples_PreflightConfigurationPasses(t *testing.T) {
	kc := stubResolver{values: map[string]string{
		"calendar_feed_url":        "https://example.invalid/feed.ics",
		"gradion.workspace.ApiKey": "test-token",
	}}
	for _, name := range []string{"timesheet.example.json", "timesheet.local-git.example.json", "timesheet.journal.example.json"} {
		cfg, err := loadConfig(name)
		if err != nil {
			t.Fatalf("%s: loadConfig: %v", name, err)
		}
		store, err := workflow.NewJSONFileStore(t.TempDir())
		if err != nil {
			t.Fatalf("%s: store: %v", name, err)
		}
		r, err := run.New(run.Deps{
			Store:    store,
			Registry: buildRegistry("test-git-token"),
			Keychain: kc,
			GitToken: "test-git-token",
		}, cfg.Config)
		if err != nil {
			t.Fatalf("%s: run.New: %v", name, err)
		}
		res, err := r.Preflight(context.Background(), run.PreflightOptions{})
		if err != nil {
			t.Fatalf("%s: Preflight: %v", name, err)
		}
		if !res.OK {
			t.Fatalf("%s: shipped example must pass preflight configuration, got %+v", name, res.Checks)
		}
		found := false
		for _, c := range res.Checks {
			if c.Name == "configuration" && c.Passed {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: want a passing configuration check, got %+v", name, res.Checks)
		}
	}
}
