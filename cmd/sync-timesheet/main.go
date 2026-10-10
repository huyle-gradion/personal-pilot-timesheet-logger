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
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/api"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/keychain"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/storage"
)

// Named constants and TigerStyle bounds.
const (
	MaxEnvFileSizeBytes   int64 = 64 * 1024
	MaxEnvLineLengthBytes int   = 2048
	MaxDateLengthBytes    int   = 32
	MaxTokenLengthBytes   int   = 512
)

// Sysexits exit codes.
const (
	ExitOK    = 0
	ExitUsage = 64 // EX_USAGE
	ExitErr   = 1
)

// Stable, secret-free CLI error codes.
const (
	ErrCodeKeychainFailed = "SYNC_CLI_KEYCHAIN_FAILED"
	// ErrCodeRESTRetired marks the retired legacy REST submission path. There is
	// deliberately no flag that re-enables submission.
	ErrCodeRESTRetired = "SYNC_CLI_REST_RETIRED"
)

// ErrMsgRESTRetired is the typed, secret-free message returned when a caller
// attempts a submission. New remote writes go through the approval-bound MCP
// path (S6/S8); this legacy command cannot create, edit, or delete.
const ErrMsgRESTRetired = "REST submission retired; MCP submission pending S6/S8"

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

// run validates configuration and a local record. It NEVER mutates remote state:
// the legacy REST submission path is retired and fails closed. A read-only
// preview (-dry-run) may list remote entries without any mutation.
func run(ctx context.Context, args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	Assert(ctx != nil, AssertSyncContextNil)
	Assert(out != nil, AssertSyncWritersNil)
	Assert(errOut != nil, AssertSyncWritersNil)

	fs := flag.NewFlagSet("sync-timesheet", flag.ContinueOnError)
	fs.SetOutput(errOut)

	var (
		targetDate string
		dryRun     bool
		storageDir string
		token      string
		baseURL    string
		envFile    string
	)

	today := time.Now().UTC().Format("2006-01-02")
	fs.StringVar(&targetDate, "date", today, "Target date in YYYY-MM-DD format")
	fs.BoolVar(&dryRun, "dry-run", false, "Read-only preview: list remote entries; never mutates")
	fs.StringVar(&storageDir, "storage-dir", "", "Directory for Tier 2 persistent storage (default: ~/.timesheet/records)")
	fs.StringVar(&token, "token", "", "Gradion API Bearer token (default: TIMESHEET_API_TOKEN env var)")
	fs.StringVar(&baseURL, "base-url", api.DefaultBaseURL, "Gradion API base URL")
	fs.StringVar(&envFile, "env", ".env", "Path to .env configuration file")

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

	// 3. Resolve API Bearer token: -token -> TIMESHEET_API_TOKEN env -> macOS
	// Keychain. The token value is never logged; only the non-secret service label
	// appears.
	token = strings.TrimSpace(token)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("TIMESHEET_API_TOKEN"))
	}
	if token == "" {
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

	// 4. Read the local record (read-only diagnostic).
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

	// 5. The legacy REST submission path is RETIRED. Fail closed BEFORE any remote
	// call: this command cannot create, edit, or delete. There is deliberately no
	// flag that re-enables submission.
	if !dryRun {
		fmt.Fprintf(errOut, "error: [%s] %s. No remote writes were attempted.\n", ErrCodeRESTRetired, ErrMsgRESTRetired)
		return ExitErr
	}

	// 6. Read-only preview (no mutations).
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = api.DefaultBaseURL
	}
	client, clientErr := api.NewClient(baseURL, token)
	if clientErr != nil {
		fmt.Fprintf(errOut, "error: initializing Gradion API client: %v\n", clientErr)
		return ExitErr
	}

	remoteEntries, getErr := client.GetEntries(ctx, targetDate)
	if getErr != nil {
		fmt.Fprintf(errOut, "error: querying remote entries from %s: %v\n", baseURL, getErr)
		return ExitErr
	}

	fmt.Fprintf(out, "Read-only preview for %s: %d local block(s), %d remote entr(ies). No remote writes.\n",
		targetDate, len(record.Blocks), len(remoteEntries))
	return ExitOK
}
