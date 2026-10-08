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
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/storage"
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

	// 3. Resolve API Bearer token
	token = strings.TrimSpace(token)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("TIMESHEET_API_TOKEN"))
	}
	if len(token) > MaxTokenLengthBytes {
		fmt.Fprintf(errOut, "error: token exceeds maximum length of %d bytes\n", MaxTokenLengthBytes)
		return ExitUsage
	}
	if token == "" {
		fmt.Fprintln(errOut, "error: missing TIMESHEET_API_TOKEN. Export TIMESHEET_API_TOKEN or provide -token flag.")
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

	// 10. Execute mutations if not dry-run
	if dryRun {
		fmt.Fprintf(out, "\n[DRY-RUN] Simulation complete: %d to create, %d to update, %d identical (skipped). No remote modifications executed.\n",
			summary.Created, summary.Updated, summary.Skipped)
	} else {
		fmt.Fprintln(out, "\nExecuting remote synchronization mutations...")
		hasMutationError := false

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
					fmt.Fprintf(errOut, "error: creating entry [%s - %s]: %v\n", item.StartTime, item.EndTime, cErr)
					hasMutationError = true
				} else {
					fmt.Fprintf(out, "Created remote entry %s [%s - %s]\n", created.ID, item.StartTime, item.EndTime)
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
					fmt.Fprintf(errOut, "error: patching entry %s [%s - %s]: %v\n", item.RemoteID, item.StartTime, item.EndTime, pErr)
					hasMutationError = true
				} else {
					fmt.Fprintf(out, "Updated remote entry %s [%s - %s]\n", updated.ID, item.StartTime, item.EndTime)
				}
			case api.ActionSkip:
				fmt.Fprintf(out, "Skipped [%s - %s]: %s\n", item.StartTime, item.EndTime, item.Reason)
			}
		}

		if hasMutationError {
			fmt.Fprintln(errOut, "warning: one or more mutations failed during synchronization.")
		} else {
			fmt.Fprintf(out, "\nSynchronization successful: %d created, %d updated, %d skipped.\n",
				summary.Created, summary.Updated, summary.Skipped)
		}
	}

	// 11. Write clean Markdown record in timesheet/YYYY-MM-DD.md
	if writeMarkdown {
		if mdErr := writeMarkdownFile(markdownDir, targetDate, summary, dryRun); mdErr != nil {
			fmt.Fprintf(errOut, "warning: writing markdown record: %v\n", mdErr)
		} else {
			mdPath := filepath.Join(markdownDir, targetDate+".md")
			fmt.Fprintf(out, "Generated clean Markdown record in %s\n", mdPath)
		}
	}

	return ExitOK
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

func formatMarkdownRecord(date string, summary api.SyncSummary, dryRun bool) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Timesheet: %s\n\n", date))

	status := "Synchronized"
	if dryRun {
		status = "Dry-Run Preview (Not submitted)"
	}
	sb.WriteString(fmt.Sprintf("- **Status**: %s\n", status))
	sb.WriteString(fmt.Sprintf("- **Total hours**: %.2fh\n", summary.TotalHours))
	sb.WriteString(fmt.Sprintf("- **Created**: %d | **Updated**: %d | **Skipped**: %d\n", summary.Created, summary.Updated, summary.Skipped))
	sb.WriteString(fmt.Sprintf("- **Sync timestamp**: %s\n\n", time.Now().UTC().Format(time.RFC3339)))

	sb.WriteString("## Entries\n\n")
	sb.WriteString("| Start | End | Action | Classification | Description |\n")
	sb.WriteString("|---|---|---|---|---|\n")

	for _, item := range summary.Items {
		desc := strings.ReplaceAll(item.Description, "|", "\\|")
		desc = strings.ReplaceAll(desc, "\n", " ")
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n",
			item.StartTime,
			item.EndTime,
			item.Action,
			item.ClassificationID,
			desc,
		))
	}

	return sb.String()
}

func writeMarkdownFile(dir, date string, summary api.SyncSummary, dryRun bool) error {
	cleanDir := filepath.Clean(dir)
	if err := os.MkdirAll(cleanDir, 0755); err != nil {
		return fmt.Errorf("creating directory %s: %w", cleanDir, err)
	}

	filePath := filepath.Join(cleanDir, date+".md")
	content := formatMarkdownRecord(date, summary, dryRun)

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
