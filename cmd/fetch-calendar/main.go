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
	MaxEmailLengthBytes    int           = 256              // 256 bytes maximum email argument length
	CommandTimeoutDuration time.Duration = 30 * time.Second // 30s overall command execution deadline
)

// Operating exit codes conforming to sysexits.h.
const (
	ExitOK        = 0
	ExitUsage     = 64 // EX_USAGE (command line usage error / preflight missing config)
	ExitErr       = 1
	ExitFlagParse = 2 // flag.ContinueOnError convention (unchanged by ADR-0004 pre-flight)
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
//
// Missing configuration is NOT one of these: it is reported by the pre-flight
// step (ADR-0004) as a guided message plus EX_USAGE, so CAL_CLI_MISSING_SOURCE
// and CAL_CLI_KEYCHAIN_FAILED are retired with the inline resolver.
const (
	ErrCodeInvalidDate     = "CAL_CLI_INVALID_DATE"
	ErrCodeInvalidTimezone = "CAL_CLI_INVALID_TIMEZONE"
	ErrCodeInvalidFormat   = "CAL_CLI_INVALID_FORMAT"
	ErrCodeBoundExceeded   = "CAL_CLI_BOUND_EXCEEDED"
	ErrCodeEnvFailed       = "CAL_CLI_ENV_FAILED"
	ErrCodeTimeout         = "CAL_CLI_TIMEOUT"
)

// Config represents validated CLI options.
type Config struct {
	// ExplicitSource is the source named on the command line (-file, then -url).
	// It is empty when the source must come from the environment or Keychain.
	ExplicitSource string
	// Source is the source resolved by the pre-flight step in run().
	Source          string
	Date            string
	Timezone        string
	Format          string
	Minified        bool
	NoCache         bool
	UserEmail       string
	SkipTransparent bool
}

// loadEnv loads environment variables from path with bounded size and symlink checks.
func loadEnv(path string) error {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if errors.Is(err, syscall.ELOOP) {
			return fmt.Errorf("[%s] symlinks are not permitted for env files", ErrCodeEnvFailed)
		}
		return fmt.Errorf("[%s] failed to open env file", ErrCodeEnvFailed)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("[%s] failed to access env file metadata", ErrCodeEnvFailed)
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

// parseConfig parses CLI flags and enforces the flag-level bounds (date,
// timezone, format). The source and user-email bounds depend on the resolved
// credential, so they are applied in run() after the pre-flight step.
//
// It performs NO credential lookup: source resolution (environment, then
// Keychain) is the pre-flight step in run(), per ADR-0004. The explicit
// command-line source is carried through unchanged so the pre-flight step can
// short-circuit on it.
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
	userEmailFlag := fs.String("user-email", "", "Calendar user email to filter declined invitations (defaults to GCAL_USER_EMAIL or URL path)")
	skipTransparentFlag := fs.Bool("skip-transparent", false, "Filter out transparent (available/free) calendar events")

	if err := fs.Parse(args); err != nil {
		return nil, ExitFlagParse, err
	}

	// Explicit command-line source only: -file wins over -url. Environment and
	// Keychain resolution belongs to the pre-flight step.
	explicitSource := *fileFlag
	if explicitSource == "" {
		explicitSource = *urlFlag
	}

	if len(*dateFlag) > MaxDateLengthBytes {
		return nil, ExitErr, fmt.Errorf("[%s] date length exceeds limit of %d bytes", ErrCodeBoundExceeded, MaxDateLengthBytes)
	}
	if len(*tzFlag) > MaxTimezoneLengthBytes {
		return nil, ExitErr, fmt.Errorf("[%s] timezone length exceeds limit of %d bytes", ErrCodeBoundExceeded, MaxTimezoneLengthBytes)
	}
	if len(*formatFlag) > MaxFormatLengthBytes {
		return nil, ExitErr, fmt.Errorf("[%s] format length exceeds limit of %d bytes", ErrCodeBoundExceeded, MaxFormatLengthBytes)
	}

	userEmail := strings.TrimSpace(*userEmailFlag)
	if userEmail == "" {
		userEmail = strings.TrimSpace(os.Getenv("GCAL_USER_EMAIL"))
	}
	if userEmail == "" {
		userEmail = strings.TrimSpace(os.Getenv("USER_EMAIL"))
	}

	if *formatFlag != "json" && *formatFlag != "entry" {
		return nil, ExitErr, fmt.Errorf("[%s] invalid format %q, allowed options: 'json', 'entry'", ErrCodeInvalidFormat, *formatFlag)
	}

	skipTransparent := *skipTransparentFlag
	if !skipTransparent {
		if envVal := os.Getenv("GCAL_SKIP_TRANSPARENT"); envVal == "1" || strings.EqualFold(envVal, "true") {
			skipTransparent = true
		}
	}

	return &Config{
		ExplicitSource:  explicitSource,
		Date:            *dateFlag,
		Timezone:        *tzFlag,
		Format:          *formatFlag,
		Minified:        *minifiedFlag,
		NoCache:         *noCacheFlag,
		UserEmail:       userEmail,
		SkipTransparent: skipTransparent,
	}, ExitOK, nil
}

// run coordinates the calendar fetch, parsing, and formatting under context cancellation.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	Assert(stdout != nil && stderr != nil, AssertWritersNonNil)

	if err := loadEnv(".env"); err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return ExitErr
	}

	cfg, exitCode, err := parseConfig(args, stderr)
	if err != nil {
		if exitCode == ExitFlagParse {
			return ExitFlagParse
		}
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return exitCode
	}
	Assert(cfg != nil, AssertConfigNonNil)

	// 1. Pre-flight check (ADR-0004): credential presence with a guided
	// onboarding message. Missing configuration is a usage error (EX_USAGE).
	preflight := calendar.CheckPreflight(ctx, cfg.ExplicitSource)
	if !preflight.Ready {
		fmt.Fprintf(stderr, "Pre-flight check failed:\n%s\n", preflight.Guide)
		return ExitUsage
	}
	cfg.Source = preflight.Source

	if len(cfg.Source) > MaxSourceLengthBytes {
		fmt.Fprintf(stderr, "Error: [%s] source length exceeds limit of %d bytes\n", ErrCodeBoundExceeded, MaxSourceLengthBytes)
		return ExitErr
	}
	if cfg.UserEmail == "" {
		cfg.UserEmail = calendar.ExtractEmailFromICSURL(cfg.Source)
	}
	if len(cfg.UserEmail) > MaxEmailLengthBytes {
		fmt.Fprintf(stderr, "Error: [%s] user email length exceeds limit of %d bytes\n", ErrCodeBoundExceeded, MaxEmailLengthBytes)
		return ExitErr
	}

	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		fmt.Fprintf(stderr, "Error: [%s] invalid timezone %q: %v\n", ErrCodeInvalidTimezone, cfg.Timezone, err)
		return ExitErr
	}

	targetDate, err := time.ParseInLocation("2006-01-02", cfg.Date, loc)
	if err != nil {
		fmt.Fprintf(stderr, "Error: [%s] invalid date format %q (expected YYYY-MM-DD)\n", ErrCodeInvalidDate, cfg.Date)
		return ExitErr
	}

	runCtx, cancel := context.WithTimeout(ctx, CommandTimeoutDuration)
	defer cancel()

	cachePath := filepath.Join(".cache", "calendar_cache.ics")
	icsData, err := calendar.FetchICS(runCtx, cfg.Source, cachePath, cfg.NoCache)
	if err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			fmt.Fprintf(stderr, "Error: [%s] operation timed out after %v\n", ErrCodeTimeout, CommandTimeoutDuration)
			return ExitErr
		}
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return ExitErr
	}

	entries, err := calendar.ParseCalendarEventsWithOptions(icsData, calendar.ParseOptions{
		TargetDate:      targetDate,
		Location:        loc,
		UserEmail:       cfg.UserEmail,
		SkipTransparent: cfg.SkipTransparent,
	})
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return ExitErr
	}

	if cfg.Format == "entry" {
		fmt.Fprintln(stdout, calendar.FormatMarkdown(entries))
		return ExitOK
	}

	jsonBytes, err := calendar.FormatJSON(entries, cfg.Minified)
	if err != nil {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return ExitErr
	}
	fmt.Fprintln(stdout, string(jsonBytes))
	return ExitOK
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	os.Exit(code)
}
