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
	"strings"
	"syscall"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/calendar"
)

// Named finite bounds with explicit units (TigerStyle).
const (
	MaxEnvFileSizeBytes    int64         = 64 * 1024        // 64 KiB maximum .env file size
	MaxEnvLineLengthBytes  int           = 2048             // 2048 bytes maximum per line in .env
	MaxSourceLengthBytes   int           = 2048             // 2048 bytes maximum URL/path length
	MaxDateLengthBytes     int           = 32               // 32 bytes maximum date argument length
	MaxTimezoneLengthBytes int           = 64               // 64 bytes maximum timezone name length
	MaxFormatLengthBytes   int           = 16               // 16 bytes maximum format argument length
	CommandTimeoutDuration time.Duration = 30 * time.Second // 30s overall command execution deadline
)

// Invariant assertion identifiers (TigerStyle).
// Exclusively used for impossible programmer-contract violations.
const (
	AssertWritersNonNil = "ASSERT_CLI_001_NIL_IO_WRITER"
	AssertConfigNonNil  = "ASSERT_CLI_002_NIL_CONFIG"
)

// Assert checks an internal programmer-contract invariant and panics with a fixed identifier if violated.
func Assert(condition bool, invariantID string) {
	if !condition {
		panic(invariantID)
	}
}

// Operating error codes for CLI level errors (TigerStyle).
const (
	ErrCodeMissingSource   = "CAL_CLI_MISSING_SOURCE"
	ErrCodeInvalidDate     = "CAL_CLI_INVALID_DATE"
	ErrCodeInvalidTimezone = "CAL_CLI_INVALID_TIMEZONE"
	ErrCodeInvalidFormat   = "CAL_CLI_INVALID_FORMAT"
	ErrCodeBoundExceeded   = "CAL_CLI_BOUND_EXCEEDED"
	ErrCodeEnvFailed       = "CAL_CLI_ENV_FAILED"
	ErrCodeTimeout         = "CAL_CLI_TIMEOUT"
)

// Config represents validated CLI options.
type Config struct {
	Source   string
	Date     string
	Timezone string
	Format   string
	Minified bool
	NoCache  bool
}

// loadEnv loads environment variables from path with bounded size and symlink checks.
func loadEnv(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("[%s] failed to access env file", ErrCodeEnvFailed)
	}

	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("[%s] symlinks are not permitted for env files", ErrCodeEnvFailed)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("[%s] env file is not a regular file", ErrCodeEnvFailed)
	}
	if fi.Size() > MaxEnvFileSizeBytes {
		return fmt.Errorf("[%s] env file size %d exceeds limit of %d bytes", ErrCodeBoundExceeded, fi.Size(), MaxEnvFileSizeBytes)
	}

	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("[%s] failed to open env file", ErrCodeEnvFailed)
	}
	defer f.Close()

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

// parseConfig parses CLI flags and enforces finite bounds on all string inputs.
func parseConfig(args []string, stderr io.Writer) (*Config, int, error) {
	fs := flag.NewFlagSet("fetch-calendar", flag.ContinueOnError)
	fs.SetOutput(stderr)

	urlFlag := fs.String("url", "", "Secret iCal URL (or set GCAL_ICS_URL env var)")
	fileFlag := fs.String("file", "", "Local .ics file for testing")
	dateFlag := fs.String("date", time.Now().Format("2006-01-02"), "Target date YYYY-MM-DD (default: today)")
	tzFlag := fs.String("tz", "Asia/Ho_Chi_Minh", "Local timezone name (default: Asia/Ho_Chi_Minh)")
	formatFlag := fs.String("format", "json", "Output format: 'json' (default Simple Entry array) or 'entry' (Section 3.2 markdown)")
	minifiedFlag := fs.Bool("minified", false, "Output single-line JSON without indentation")
	noCacheFlag := fs.Bool("no-cache", false, "Bypass cached .ics file")

	if err := fs.Parse(args); err != nil {
		return nil, 2, err
	}

	source := *fileFlag
	if source == "" {
		source = *urlFlag
	}
	if source == "" {
		source = os.Getenv("GCAL_ICS_URL")
	}

	if len(source) > int(MaxSourceLengthBytes) {
		return nil, 1, fmt.Errorf("[%s] source length exceeds limit of %d bytes", ErrCodeBoundExceeded, MaxSourceLengthBytes)
	}
	if len(*dateFlag) > MaxDateLengthBytes {
		return nil, 1, fmt.Errorf("[%s] date length exceeds limit of %d bytes", ErrCodeBoundExceeded, MaxDateLengthBytes)
	}
	if len(*tzFlag) > MaxTimezoneLengthBytes {
		return nil, 1, fmt.Errorf("[%s] timezone length exceeds limit of %d bytes", ErrCodeBoundExceeded, MaxTimezoneLengthBytes)
	}
	if len(*formatFlag) > MaxFormatLengthBytes {
		return nil, 1, fmt.Errorf("[%s] format length exceeds limit of %d bytes", ErrCodeBoundExceeded, MaxFormatLengthBytes)
	}

	if *formatFlag != "json" && *formatFlag != "entry" {
		return nil, 1, fmt.Errorf("[%s] invalid format %q, allowed options: 'json', 'entry'", ErrCodeInvalidFormat, *formatFlag)
	}

	if source == "" {
		return nil, 1, fmt.Errorf("[%s] no iCal source provided. Set GCAL_ICS_URL or pass -url or -file", ErrCodeMissingSource)
	}

	return &Config{
		Source:   source,
		Date:     *dateFlag,
		Timezone: *tzFlag,
		Format:   *formatFlag,
		Minified: *minifiedFlag,
		NoCache:  *noCacheFlag,
	}, 0, nil
}

// run coordinates the calendar fetch, parsing, and formatting under context cancellation.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	Assert(stdout != nil && stderr != nil, AssertWritersNonNil)

	if err := loadEnv(".env"); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	cfg, exitCode, err := parseConfig(args, stderr)
	if err != nil {
		if exitCode == 2 {
			return 2
		}
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return exitCode
	}
	Assert(cfg != nil, AssertConfigNonNil)

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		fmt.Fprintf(stderr, "Error: [%s] invalid timezone %q: %v\n", ErrCodeInvalidTimezone, cfg.Timezone, err)
		return 1
	}

	targetDate, err := time.ParseInLocation("2006-01-02", cfg.Date, loc)
	if err != nil {
		fmt.Fprintf(stderr, "Error: [%s] invalid date format %q (expected YYYY-MM-DD)\n", ErrCodeInvalidDate, cfg.Date)
		return 1
	}

	runCtx, cancel := context.WithTimeout(ctx, CommandTimeoutDuration)
	defer cancel()

	cachePath := filepath.Join(".cache", "calendar_cache.ics")
	icsData, err := calendar.FetchICS(runCtx, cfg.Source, cachePath, cfg.NoCache)
	if err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			fmt.Fprintf(stderr, "Error: [%s] operation timed out after %v\n", ErrCodeTimeout, CommandTimeoutDuration)
			return 1
		}
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	entries, err := calendar.ParseCalendarEvents(icsData, targetDate, loc)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	if cfg.Format == "entry" {
		fmt.Fprintln(stdout, calendar.FormatMarkdown(entries))
		return 0
	}

	jsonBytes, err := calendar.FormatJSON(entries, cfg.Minified)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, string(jsonBytes))
	return 0
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(code)
}
