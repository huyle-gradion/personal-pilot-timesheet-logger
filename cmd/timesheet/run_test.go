package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// --- RUN-SURFACE-1: run composes collect+draft (issue #155) ------------------

// captureOutput redirects the process stdout and stderr around fn and returns
// what the run wrote to each stream.
func captureOutput(t *testing.T, fn func() error) (stdout, stderr []byte, err error) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	savedOut, savedErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	defer func() { os.Stdout, os.Stderr = savedOut, savedErr }()

	outDone := make(chan []byte, 1)
	errDone := make(chan []byte, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(outR)
		outDone <- buf.Bytes()
	}()
	go func() {
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(errR)
		errDone <- buf.Bytes()
	}()

	runErr := fn()
	outW.Close()
	errW.Close()
	return <-outDone, <-errDone, runErr
}

// writeRunConfig writes a synthetic config with the calendar collector enabled.
func writeRunConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	body := `{
  "timezone": "Asia/Ho_Chi_Minh",
  "state_path": "` + filepath.Join(dir, "state.json") + `",
  "projects": [{"id": "WORK", "name": "Work", "default_classification": "DEV"}],
  "sources": {"enabled": true, "calendar": {"enabled": true, "user": "me@example.com"}}
}`
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// startFeed starts a loopback iCalendar feed that serves one deterministic
// timed event on 2026-10-20 (03:00Z-04:00Z = 10:00-11:00 in the recorded
// timezone) and counts the fetches. No external network is touched.
func startFeed(t *testing.T) (*httptest.Server, *int64) {
	t.Helper()
	var fetches int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&fetches, 1)
		fmt.Fprint(w, "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//EN\r\nBEGIN:VEVENT\r\nUID:evt-1@test\r\nDTSTAMP:20261020T000000Z\r\nDTSTART:20261020T030000Z\r\nDTEND:20261020T040000Z\r\nSUMMARY:Standup\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n")
	}))
	t.Cleanup(srv.Close)
	return srv, &fetches
}

// writeRunEnvFile writes a synthetic env file naming the loopback feed. The
// test writes this file as a fixture; it never reads an operator credential
// file and never echoes the value back beyond the fixture itself.
func writeRunEnvFile(t *testing.T, feedURL string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "env.local")
	if err := os.WriteFile(path, []byte("GCAL_ICS_URL="+feedURL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRunMatchesDraftByteForByte is the core acceptance proof: for the same
// synthetic evidence and state, run's stdout is byte-identical to draft's
// stdout, the artifacts file equals stdout byte-for-byte, and stderr names
// the deterministic artifacts directory.
func TestRunMatchesDraftByteForByte(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir()) // hermetic artifacts directory
	cfg := writeRunConfig(t)
	srv, fetches := startFeed(t)
	envFile := writeRunEnvFile(t, srv.URL)

	day := "2026-10-20"

	runOut, runErrOut, err := captureOutput(t, func() error {
		return runRun([]string{"--config", cfg, "--env-file", envFile, "--date", day})
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := atomic.LoadInt64(fetches); got != 1 {
		t.Fatalf("collect executed %d times in one run invocation, want exactly 1", got)
	}

	draftOut, _, err := captureOutput(t, func() error {
		return runDraft([]string{"--config", cfg, "--env-file", envFile, "--date", day})
	})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}

	if len(draftOut) == 0 {
		t.Fatal("draft produced no output")
	}
	if !bytes.Equal(runOut, draftOut) {
		t.Fatalf("run stdout differs from draft stdout:\nrun:   %q\ndraft: %q", runOut, draftOut)
	}

	// stderr names the deterministic artifacts directory.
	artifacts := filepath.Join(os.TempDir(), "timesheet", day, "draft.json")
	if !strings.Contains(string(runErrOut), artifacts) {
		t.Fatalf("stderr %q does not name the artifacts path %q", runErrOut, artifacts)
	}
	// stdout is pure draft JSON: no source report, no artifacts line.
	if strings.Contains(string(runOut), "sources") || strings.Contains(string(runOut), "artifacts") {
		t.Fatalf("stdout is not pure draft JSON: %q", runOut)
	}
	// The stored artifact is byte-identical to stdout.
	stored, err := os.ReadFile(artifacts)
	if err != nil {
		t.Fatalf("read artifacts file: %v", err)
	}
	if !bytes.Equal(stored, runOut) {
		t.Fatalf("artifacts file differs from run stdout:\nfile:  %q\nstdout: %q", stored, runOut)
	}
	// The draft carries the synthetic meeting and no source-report fields.
	if !strings.Contains(string(runOut), `"kind": "meeting"`) {
		t.Fatalf("draft does not contain the synthetic meeting block: %s", runOut)
	}
}

// TestRunCollectsExactlyOncePerInvocation asserts the one-attempt semantics:
// a second run invocation performs exactly one more collection attempt, never
// two (the fused command must not double-collect).
func TestRunCollectsExactlyOncePerInvocation(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	cfg := writeRunConfig(t)
	srv, fetches := startFeed(t)
	envFile := writeRunEnvFile(t, srv.URL)

	for i := 1; i <= 3; i++ {
		if _, _, err := captureOutput(t, func() error {
			return runRun([]string{"--config", cfg, "--env-file", envFile, "--date", "2026-10-20"})
		}); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
		if got := atomic.LoadInt64(fetches); got != int64(i) {
			t.Fatalf("after run %d: %d collection fetches, want exactly %d per invocation", i, got, i)
		}
	}
}

// TestRunFlagErrors pins run's flag surface to the same error style as
// collect/draft.
func TestRunFlagErrors(t *testing.T) {
	cfg := writeRunConfig(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing config", args: []string{"--date", "2026-10-20"}, want: "run requires --config"},
		{name: "missing date", args: []string{"--config", cfg}, want: "run requires --date"},
		{name: "bad date format", args: []string{"--config", cfg, "--date", "20-10-2026"}, want: "run: --date must be YYYY-MM-DD"},
		{name: "positional args", args: []string{"--config", cfg, "--date", "2026-10-20", "extra"}, want: "run does not accept positional arguments"},
	}
	for _, tc := range cases {
		err := runRun(tc.args)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err = %v, want %q", tc.name, err, tc.want)
		}
	}
	if !errors.Is(runRun([]string{"--help"}), flag.ErrHelp) {
		t.Fatalf("--help should surface flag.ErrHelp, got %v", runRun([]string{"--help"}))
	}
}
