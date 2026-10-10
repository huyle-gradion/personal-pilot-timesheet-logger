package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/api"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/keychain"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/storage"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// Named constants and TigerStyle bounds.
const (
	MaxEnvFileSizeBytes    int64         = 64 * 1024
	MaxEnvLineLengthBytes  int           = 2048
	MaxDateLengthBytes     int           = 32
	MaxTokenLengthBytes    int           = 512
	MaxURLLengthBytes      int           = 2048
	CommandTimeoutDuration time.Duration = 60 * time.Second
)

// Sysexits exit codes.
const (
	ExitOK    = 0
	ExitUsage = 64 // EX_USAGE
	ExitErr   = 1
)

// Stable, secret-free CLI error codes.
const (
	ErrCodeKeychainFailed     = "SYNC_CLI_KEYCHAIN_FAILED"
	ErrCodePersistenceFailed  = "SYNC_CLI_PERSISTENCE_FAILED"
	ErrCodeRecorderInitFailed = "SYNC_CLI_RECORDER_INIT_FAILED"
)

// RecoveryRequiredStatus is the distinct markdown status reported when a remote
// write succeeded but its outcome could not be persisted locally.
const RecoveryRequiredStatus = "Recovery Required (Persistence Failed)"

// workflowRecorder persists durable, revisioned operation outcomes for a day.
type workflowRecorder interface {
	UpdateDay(ctx context.Context, date string, mutate func(*workflow.Record) error) (*workflow.Record, error)
}

// newWorkflowRecorder is a test seam for injecting a recorder (or a failing one).
var newWorkflowRecorder = func(rootDir string) (workflowRecorder, error) {
	return workflow.NewJSONFileStore(rootDir)
}

// Invariant assertion identifiers.
const (
	AssertSyncContextNil = "ASSERT_SYNC_001_NIL_CONTEXT"
	AssertSyncWritersNil = "ASSERT_SYNC_002_NIL_WRITERS"
)

var datePattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})$`)

// Assert terminates the process on violated internal invariants per TigerStyle.
func Assert(condition bool, invariantID string) {
	if !condition {
		panic(invariantID)
	}
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	code := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	os.Exit(code)
}

// loadEnv loads environment variables from a file if not already set.
func loadEnv(path string) error {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() || fi.Size() > MaxEnvFileSizeBytes {
		return nil
	}

	limitedReader := io.LimitReader(f, MaxEnvFileSizeBytes+1)
	scanner := bufio.NewScanner(limitedReader)
	scanner.Buffer(make([]byte, MaxEnvLineLengthBytes), MaxEnvLineLengthBytes)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		k := strings.TrimSpace(parts[0])
		v := strings.Trim(strings.TrimSpace(parts[1]), "\"'")
		if os.Getenv(k) == "" {
			_ = os.Setenv(k, v)
		}
	}
	return scanner.Err()
}

func run(ctx context.Context, args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	Assert(ctx != nil, AssertSyncContextNil)
	Assert(out != nil, AssertSyncWritersNil)
	Assert(errOut != nil, AssertSyncWritersNil)

	fs := flag.NewFlagSet("sync-timesheet", flag.ContinueOnError)
	fs.SetOutput(errOut)

	var (
		targetDate       string
		dryRun           bool
		approve          bool
		storageDir       string
		token            string
		classificationID string
		baseURL          string
		envFile          string
		markdownDir      string
		writeMarkdown    bool
	)

	today := time.Now().UTC().Format("2006-01-02")
	fs.StringVar(&targetDate, "date", today, "Target date in YYYY-MM-DD format")
	fs.BoolVar(&dryRun, "dry-run", false, "Preview reconciliation plan without sending network writes")
	fs.BoolVar(&approve, "approve", false, "Temporary mutation gate: required to execute a non-dry-run sync (not revision-bound approval)")
	fs.StringVar(&storageDir, "storage-dir", "", "Directory for Tier 2 persistent storage (default: ~/.timesheet/records)")
	fs.StringVar(&token, "token", "", "Gradion API Bearer token (default: TIMESHEET_API_TOKEN env var)")
	fs.StringVar(&classificationID, "classification-id", "", "Default classification ID (default: DEFAULT_CLASSIFICATION_ID or fallback)")
	fs.StringVar(&baseURL, "base-url", api.DefaultBaseURL, "Gradion API base URL")
	fs.StringVar(&envFile, "env", ".env", "Path to .env configuration file")
	fs.StringVar(&markdownDir, "markdown-dir", "timesheet", "Directory for clean markdown timesheet records")
	fs.BoolVar(&writeMarkdown, "write-markdown", true, "Write clean Markdown record in <markdown-dir>/YYYY-MM-DD.md")

	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	// 1. Load .env file
	if envErr := loadEnv(envFile); envErr != nil {
		fmt.Fprintf(errOut, "warning: loading env file %s: %v\n", envFile, envErr)
	}

	// 2. Validate date
	targetDate = strings.TrimSpace(targetDate)
	if len(targetDate) > MaxDateLengthBytes || !datePattern.MatchString(targetDate) {
		fmt.Fprintf(errOut, "error: invalid -date %q: must be YYYY-MM-DD\n", targetDate)
		return ExitUsage
	}

	// 3. Resolve API Bearer token: -token -> TIMESHEET_API_TOKEN env -> macOS Keychain.
	// The token value is never logged; only the non-secret service label appears.
	token = strings.TrimSpace(token)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("TIMESHEET_API_TOKEN"))
	}
	if token == "" {
		// Keychain is the authoritative secret store. Only a genuine NotFound
		// falls through to the missing-token error below. Any other failure
		// (timeout, access failure, malformed/oversized output, ...) stops
		// resolution and is surfaced as a typed, secret-free error; it must
		// never silently downgrade to a legacy credential source.
		kcToken, kcErr := keychain.Lookup(ctx, keychain.ServiceGradionMCP)
		switch {
		case kcErr == nil:
			token = strings.TrimSpace(kcToken)
		case keychain.IsNotFound(kcErr):
			// Absent item: fall through to the missing-token error.
		default:
			fmt.Fprintf(errOut, "error: [%s] keychain lookup for %s failed: %v\n", ErrCodeKeychainFailed, keychain.ServiceGradionMCP, kcErr)
			return ExitErr
		}
	}
	if len(token) > MaxTokenLengthBytes {
		fmt.Fprintf(errOut, "error: token exceeds maximum length of %d bytes\n", MaxTokenLengthBytes)
		return ExitUsage
	}
	if token == "" {
		fmt.Fprintln(errOut, "error: missing TIMESHEET_API_TOKEN. Export TIMESHEET_API_TOKEN, provide -token, or store it in the macOS Keychain (service "+keychain.ServiceGradionMCP+").")
		return ExitUsage
	}

	// 4. Resolve Classification ID
	classificationID = strings.TrimSpace(classificationID)
	if classificationID == "" {
		classificationID = strings.TrimSpace(os.Getenv("DEFAULT_CLASSIFICATION_ID"))
	}
	if classificationID == "" {
		classificationID = api.DefaultClassificationID
	}

	// 5. Resolve storage directory and load local record
	dataDir := storage.ResolveDataDir(storageDir)
	store, storeErr := storage.NewJSONFileStore(dataDir)
	if storeErr != nil {
		fmt.Fprintf(errOut, "error: initializing storage: %v\n", storeErr)
		return ExitErr
	}

	record, recErr := store.GetDay(ctx, targetDate)
	if recErr != nil {
		if errors.Is(recErr, storage.ErrRecordNotFound) {
			fmt.Fprintf(errOut, "error: no timesheet record found for date %s in %s. Run review-timesheet first to finalize your schedule.\n", targetDate, dataDir)
		} else {
			fmt.Fprintf(errOut, "error: reading timesheet record for %s: %v\n", targetDate, recErr)
		}
		return ExitErr
	}

	if len(record.Blocks) == 0 {
		fmt.Fprintf(errOut, "error: timesheet record for %s has 0 time blocks. Run review-timesheet first.\n", targetDate)
		return ExitErr
	}

	// 6. Initialize Gradion API Client
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = api.DefaultBaseURL
	}
	client, clientErr := api.NewClient(baseURL, token)
	if clientErr != nil {
		fmt.Fprintf(errOut, "error: initializing Gradion API client: %v\n", clientErr)
		return ExitErr
	}

	// 7. Fetch existing remote entries (Query-before-write)
	remoteEntries, getErr := client.GetEntries(ctx, targetDate)
	if getErr != nil {
		fmt.Fprintf(errOut, "error: querying remote entries from %s: %v\n", baseURL, getErr)
		return ExitErr
	}

	// 8. Deterministic two-way reconciliation diff
	items, reconErr := api.Reconcile(ctx, record.Blocks, remoteEntries, classificationID)
	if reconErr != nil {
		fmt.Fprintf(errOut, "error: reconciling timesheet entries: %v\n", reconErr)
		return ExitErr
	}

	summary := api.BuildSummary(targetDate, items)

	// 9. Render ASCII reconciliation plan table
	renderPlanTable(out, summary, dryRun)

	// 9b. Approval gate: remote mutations are deferred until explicit approval.
	// A dry-run performs zero remote mutations and needs no approval.
	if !dryRun && !approve {
		fmt.Fprintln(out, "\nRemote mutations require explicit approval.")
		fmt.Fprintln(out, "Re-run with -approve to execute, or -dry-run to preview.")
		return ExitUsage
	}

	// 10. Execute mutations if not dry-run
	unresolved := false
	hasMutationError := false
	recoveryRequired := false
	var outcomes []operationOutcome

	if dryRun {
		fmt.Fprintf(out, "\n[DRY-RUN] Simulation complete: %d to create, %d to update, %d identical (skipped). No remote modifications executed.\n",
			summary.Created, summary.Updated, summary.Skipped)
	} else {
		// Durable outcome persistence is constructed only after approval passes.
		// Each executed operation's outcome is persisted immediately AFTER its
		// remote response, so the record of a COMPLETED mutation is written
		// before the loop continues. The window between a remote write and its
		// local persistence is NOT crash-protected, and durable pre-write
		// intent/recovery is S2 scope.
		recorder, recorderErr := newWorkflowRecorder(dataDir)
		if recorderErr != nil {
			fmt.Fprintf(errOut, "error: [%s] initializing workflow recorder: %v\n", ErrCodeRecorderInitFailed, recorderErr)
			return ExitErr
		}

		persistOutcome := func(outcome workflow.OperationOutcome, mapping *workflow.RemoteMapping) error {
			_, persistErr := recorder.UpdateDay(ctx, targetDate, func(record *workflow.Record) error {
				workflow.UpsertOutcome(record, outcome)
				if mapping != nil {
					if mapErr := workflow.UpsertMapping(record, *mapping); mapErr != nil {
						return mapErr
					}
				}
				return nil
			})
			return persistErr
		}

		// reportPersistenceFailure is secret-free: it carries a stable code and a
		// local file error only, never a token, URL, or raw remote error body.
		// wroteRemote distinguishes a confirmed remote write from a terminal
		// failure/uncertain result where no remote write succeeded. The message
		// is guidance to reconcile remote state before retrying; it does not
		// assert an enforced guarantee. Durable unresolved-state enforcement is
		// S2-owned.
		reportPersistenceFailure := func(wroteRemote bool, persistErr error) {
			if wroteRemote {
				fmt.Fprintf(errOut, "error: [%s] a remote write succeeded but local persistence failed; reconcile remote state before retrying: %v\n", ErrCodePersistenceFailed, persistErr)
				return
			}
			fmt.Fprintf(errOut, "error: [%s] local persistence failed; reconcile local and remote state before retrying: %v\n", ErrCodePersistenceFailed, persistErr)
		}

		fmt.Fprintln(out, "\nExecuting remote synchronization mutations...")

		for _, item := range items {
			switch item.Action {
			case api.ActionCreate:
				req := api.CreateEntryRequest{
					Date:             item.Date,
					StartTime:        item.StartTime,
					EndTime:          item.EndTime,
					ClassificationID: item.ClassificationID,
					Description:      item.Description,
				}
				created, cErr := client.CreateEntry(ctx, req)
				if cErr != nil {
					if api.IsUncertain(cErr) {
						fmt.Fprintf(errOut, "error: uncertain write creating entry [%s - %s]: %v\n", item.StartTime, item.EndTime, cErr)
						outcomes = append(outcomes, newOutcome("uncertain", item, ""))
						unresolved = true
						if persistErr := persistOutcome(workflowFailureOutcome(item, workflow.OutcomeUncertain, "", false), nil); persistErr != nil {
							reportPersistenceFailure(false, persistErr)
							recoveryRequired = true
						}
						break
					}
					fmt.Fprintf(errOut, "error: creating entry [%s - %s]: %v\n", item.StartTime, item.EndTime, cErr)
					outcomes = append(outcomes, newOutcome("failed", item, ""))
					if persistErr := persistOutcome(workflowFailureOutcome(item, workflow.OutcomeFailed, operatingErrorCode(cErr), true), nil); persistErr != nil {
						reportPersistenceFailure(false, persistErr)
						recoveryRequired = true
						break
					}
					hasMutationError = true
				} else {
					identified, persistErr := persistConfirmedOutcome(persistOutcome, item, *created)
					switch {
					case persistErr != nil:
						outcomes = append(outcomes, newOutcome("create", item, created.ID))
						reportPersistenceFailure(true, persistErr)
						recoveryRequired = true
					case !identified:
						fmt.Fprintf(errOut, "warning: remote create succeeded for [%s - %s] but returned no stable entry ID; reconciliation is required before any retry.\n", item.StartTime, item.EndTime)
						outcomes = append(outcomes, newOutcome("unidentified", item, ""))
						unresolved = true
					default:
						fmt.Fprintf(out, "Created remote entry %s [%s - %s]\n", created.ID, item.StartTime, item.EndTime)
						outcomes = append(outcomes, newOutcome("create", item, created.ID))
					}
				}
			case api.ActionUpdate:
				req := api.PatchEntryRequest{
					DTO: api.PatchEntryDTO{
						Description: item.Description,
					},
					Reason: "Automated timesheet sync update",
				}
				updated, pErr := client.PatchEntry(ctx, item.RemoteID, req)
				if pErr != nil {
					if api.IsUncertain(pErr) {
						fmt.Fprintf(errOut, "error: uncertain write patching entry %s [%s - %s]: %v\n", item.RemoteID, item.StartTime, item.EndTime, pErr)
						outcomes = append(outcomes, newOutcome("uncertain", item, item.RemoteID))
						unresolved = true
						uncertain := workflowFailureOutcome(item, workflow.OutcomeUncertain, "", false)
						uncertain.RemoteID = item.RemoteID
						if persistErr := persistOutcome(uncertain, nil); persistErr != nil {
							reportPersistenceFailure(false, persistErr)
							recoveryRequired = true
						}
						break
					}
					fmt.Fprintf(errOut, "error: patching entry %s [%s - %s]: %v\n", item.RemoteID, item.StartTime, item.EndTime, pErr)
					outcomes = append(outcomes, newOutcome("failed", item, item.RemoteID))
					failed := workflowFailureOutcome(item, workflow.OutcomeFailed, operatingErrorCode(pErr), true)
					failed.RemoteID = item.RemoteID
					if persistErr := persistOutcome(failed, nil); persistErr != nil {
						reportPersistenceFailure(false, persistErr)
						recoveryRequired = true
						break
					}
					hasMutationError = true
				} else {
					identified, persistErr := persistConfirmedOutcome(persistOutcome, item, *updated)
					switch {
					case persistErr != nil:
						outcomes = append(outcomes, newOutcome("update", item, updated.ID))
						reportPersistenceFailure(true, persistErr)
						recoveryRequired = true
					case !identified:
						fmt.Fprintf(errOut, "warning: remote update succeeded for [%s - %s] but returned no stable entry ID; reconciliation is required before any retry.\n", item.StartTime, item.EndTime)
						outcomes = append(outcomes, newOutcome("unidentified", item, updated.ID))
						unresolved = true
					default:
						fmt.Fprintf(out, "Updated remote entry %s [%s - %s]\n", updated.ID, item.StartTime, item.EndTime)
						outcomes = append(outcomes, newOutcome("update", item, updated.ID))
					}
				}
			case api.ActionSkip:
				fmt.Fprintf(out, "Skipped [%s - %s]: %s\n", item.StartTime, item.EndTime, item.Reason)
				outcomes = append(outcomes, newOutcome("skip", item, item.RemoteID))
			}

			// An uncertain write or a local persistence failure stops the run:
			// never send further writes and never blindly retry a mutation.
			if unresolved || recoveryRequired {
				break
			}
		}

		if recoveryRequired {
			fmt.Fprintln(errOut, "error: local persistence failed; recovery is required before continuing and no further remote writes were attempted.")
		} else if unresolved {
			fmt.Fprintln(errOut, "warning: submission is partial and unresolved: an uncertain remote write must be re-fetched and reconciled before any retry.")
		} else if hasMutationError {
			fmt.Fprintln(errOut, "warning: one or more mutations failed during synchronization.")
		} else {
			fmt.Fprintf(out, "\nSynchronization successful: %d created, %d updated, %d skipped.\n",
				summary.Created, summary.Updated, summary.Skipped)
		}
	}

	// Determine the honest submission status from the ACTUAL outcome. Only a
	// full, clean success is reported as Synchronized.
	status := "Synchronized"
	switch {
	case dryRun:
		status = "Dry-Run Preview (Not submitted)"
	case recoveryRequired:
		status = RecoveryRequiredStatus
	case unresolved:
		status = "Partial Submission (Unresolved)"
	case hasMutationError:
		status = "Partial Submission (Failed)"
	}

	// 11. Write clean Markdown record in timesheet/YYYY-MM-DD.md
	if writeMarkdown {
		if mdErr := writeMarkdownFile(markdownDir, targetDate, summary, dryRun, status, outcomes); mdErr != nil {
			fmt.Fprintf(errOut, "warning: writing markdown record: %v\n", mdErr)
		} else {
			mdPath := filepath.Join(markdownDir, targetDate+".md")
			fmt.Fprintf(out, "Generated clean Markdown record in %s\n", mdPath)
		}
	}

	if recoveryRequired || unresolved || hasMutationError {
		return ExitErr
	}

	return ExitOK
}

// operatingErrorCode extracts only the stable code from a typed API error, so no
// raw error message or response body is ever persisted.
func operatingErrorCode(err error) string {
	var opErr *api.OperatingError
	if errors.As(err, &opErr) && opErr.Code != "" {
		return opErr.Code
	}
	return "API_ERR_UNKNOWN"
}

// workflowFailureOutcome builds a durable non-success outcome. ErrorCode carries
// only a stable string; raw messages/bodies are never persisted.
func workflowFailureOutcome(item api.ReconciledItem, status workflow.OutcomeStatus, errorCode string, resolved bool) workflow.OperationOutcome {
	return workflow.OperationOutcome{
		LocalID:     item.LocalBlockID,
		Action:      string(item.Action),
		Status:      status,
		Date:        item.Date,
		StartTime:   item.StartTime,
		EndTime:     item.EndTime,
		Description: item.Description,
		ErrorCode:   errorCode,
		Resolved:    resolved,
		RecordedAt:  time.Now().UTC(),
	}
}

// persistConfirmedOutcome records a remote write result. A remote response with
// a non-empty ID is a confirmed success (identified=true) and, when the local
// block ID is also non-empty, records a remote mapping built from the returned
// entry. A successful response that carries NO remote ID is recorded as an
// unresolved "unidentified" outcome with no mapping (identified=false); identity
// is never fabricated from time-slot equality.
func persistConfirmedOutcome(
	persist func(workflow.OperationOutcome, *workflow.RemoteMapping) error,
	item api.ReconciledItem,
	remote api.RemoteEntry,
) (identified bool, err error) {
	now := time.Now().UTC()

	if remote.ID == "" {
		outcome := workflow.OperationOutcome{
			LocalID:     item.LocalBlockID,
			Action:      string(item.Action),
			Status:      workflow.OutcomeUnidentified,
			Date:        item.Date,
			StartTime:   item.StartTime,
			EndTime:     item.EndTime,
			Description: item.Description,
			ErrorCode:   "REMOTE_ID_MISSING",
			Resolved:    false,
			RecordedAt:  now,
		}
		if persistErr := persist(outcome, nil); persistErr != nil {
			return false, persistErr
		}
		return false, nil
	}

	outcome := workflow.OperationOutcome{
		LocalID:     item.LocalBlockID,
		RemoteID:    remote.ID,
		Action:      string(item.Action),
		Status:      workflow.OutcomeConfirmed,
		Date:        item.Date,
		StartTime:   item.StartTime,
		EndTime:     item.EndTime,
		Description: item.Description,
		Resolved:    true,
		RecordedAt:  now,
	}

	var mapping *workflow.RemoteMapping
	if item.LocalBlockID != "" {
		mapping = &workflow.RemoteMapping{
			LocalID:          item.LocalBlockID,
			RemoteID:         remote.ID,
			Date:             item.Date,
			StartTime:        item.StartTime,
			EndTime:          item.EndTime,
			ClassificationID: item.ClassificationID,
			Description:      item.Description,
			LastObserved: workflow.RemoteState{
				RemoteID:         remote.ID,
				Date:             remote.Date,
				StartTime:        remote.StartTime,
				EndTime:          remote.EndTime,
				ClassificationID: remote.ClassificationID,
				Description:      remote.Description,
				ObservedAt:       now,
			},
			CreatedAt: now,
			UpdatedAt: now,
		}
	}

	if persistErr := persist(outcome, mapping); persistErr != nil {
		return false, persistErr
	}
	return true, nil
}

// operationOutcome records the ACTUAL result of a single planned mutation so the
// markdown record reports remote truth rather than the pre-write plan.
type operationOutcome struct {
	StartTime        string
	EndTime          string
	Action           string // create | update | skip | failed | uncertain
	ClassificationID string
	Description      string
	RemoteID         string
}

func newOutcome(action string, item api.ReconciledItem, remoteID string) operationOutcome {
	return operationOutcome{
		StartTime:        item.StartTime,
		EndTime:          item.EndTime,
		Action:           action,
		ClassificationID: item.ClassificationID,
		Description:      item.Description,
		RemoteID:         remoteID,
	}
}

// outcomeCounts tallies clear outcomes; "uncertain" is intentionally excluded
// because its remote result is unknown.
func outcomeCounts(outcomes []operationOutcome) (created, updated, skipped, failed int) {
	for _, o := range outcomes {
		switch o.Action {
		case "create":
			created++
		case "update":
			updated++
		case "skip":
			skipped++
		case "failed":
			failed++
		}
	}
	return created, updated, skipped, failed
}

func renderPlanTable(out io.Writer, summary api.SyncSummary, dryRun bool) {
	fmt.Fprintf(out, "\n========================================================================================\n")
	if dryRun {
		fmt.Fprintf(out, " TIMESHEET RECONCILIATION PLAN (DRY-RUN PREVIEW) - %s (%.2f hours)\n", summary.Date, summary.TotalHours)
	} else {
		fmt.Fprintf(out, " TIMESHEET RECONCILIATION PLAN - %s (%.2f hours)\n", summary.Date, summary.TotalHours)
	}
	fmt.Fprintf(out, "========================================================================================\n")
	fmt.Fprintf(out, "%-10s | %-13s | %-36s | %s\n", "ACTION", "TIME SLOT", "CLASSIFICATION", "DESCRIPTION")
	fmt.Fprintf(out, "-----------|---------------|--------------------------------------|---------------------\n")

	for _, item := range summary.Items {
		actionTag := strings.ToUpper(string(item.Action))
		slot := fmt.Sprintf("%s - %s", item.StartTime, item.EndTime)

		desc := item.Description
		if len(desc) > 35 {
			desc = desc[:32] + "..."
		}

		fmt.Fprintf(out, "%-10s | %-13s | %-36s | %s\n",
			actionTag,
			slot,
			item.ClassificationID,
			desc,
		)
	}
	fmt.Fprintf(out, "----------------------------------------------------------------------------------------\n")
	fmt.Fprintf(out, "Summary: %d to create, %d to update, %d to skip, total %.2f hours\n",
		summary.Created, summary.Updated, summary.Skipped, summary.TotalHours)
}

func formatMarkdownRecord(date string, summary api.SyncSummary, dryRun bool, status string, outcomes []operationOutcome) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Timesheet: %s\n\n", date))

	sb.WriteString(fmt.Sprintf("- **Status**: %s\n", status))
	sb.WriteString(fmt.Sprintf("- **Total hours**: %.2fh\n", summary.TotalHours))

	created, updated, skipped, failed := outcomeCounts(outcomes)
	if dryRun {
		created, updated, skipped, failed = summary.Created, summary.Updated, summary.Skipped, 0
	}
	if failed > 0 {
		sb.WriteString(fmt.Sprintf("- **Created**: %d | **Updated**: %d | **Skipped**: %d | **Failed**: %d\n", created, updated, skipped, failed))
	} else {
		sb.WriteString(fmt.Sprintf("- **Created**: %d | **Updated**: %d | **Skipped**: %d\n", created, updated, skipped))
	}
	sb.WriteString(fmt.Sprintf("- **Sync timestamp**: %s\n\n", time.Now().UTC().Format(time.RFC3339)))

	sb.WriteString("## Entries\n\n")
	sb.WriteString("| Start | End | Action | Classification | Description |\n")
	sb.WriteString("|---|---|---|---|---|\n")

	// Dry-run previews the plan; a live run reports the ACTUAL outcomes only,
	// so operations never dispatched after an uncertain stop are not shown.
	rows := outcomes
	if dryRun {
		rows = plannedOutcomes(summary)
	}

	for _, r := range rows {
		desc := strings.ReplaceAll(r.Description, "|", "\\|")
		desc = strings.ReplaceAll(desc, "\n", " ")
		if r.RemoteID != "" {
			desc = fmt.Sprintf("%s [remote: %s]", desc, r.RemoteID)
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n",
			r.StartTime,
			r.EndTime,
			r.Action,
			r.ClassificationID,
			desc,
		))
	}

	return sb.String()
}

// plannedOutcomes converts the reconciliation plan into preview rows for dry-run.
func plannedOutcomes(summary api.SyncSummary) []operationOutcome {
	rows := make([]operationOutcome, 0, len(summary.Items))
	for _, item := range summary.Items {
		rows = append(rows, operationOutcome{
			StartTime:        item.StartTime,
			EndTime:          item.EndTime,
			Action:           string(item.Action),
			ClassificationID: item.ClassificationID,
			Description:      item.Description,
			RemoteID:         item.RemoteID,
		})
	}
	return rows
}

func writeMarkdownFile(dir, date string, summary api.SyncSummary, dryRun bool, status string, outcomes []operationOutcome) error {
	cleanDir := filepath.Clean(dir)
	if err := os.MkdirAll(cleanDir, 0755); err != nil {
		return fmt.Errorf("creating directory %s: %w", cleanDir, err)
	}

	filePath := filepath.Join(cleanDir, date+".md")
	content := formatMarkdownRecord(date, summary, dryRun, status, outcomes)

	tmpFile := filePath + fmt.Sprintf(".tmp-%d", time.Now().UnixNano())
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}

	if err := os.Rename(tmpFile, filePath); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("renaming temp file: %w", err)
	}

	return nil
}
