package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/huyle-gradion/pilot-timesheet/internal/timesheet"
)

// --- tui verb seam: the TUI pipeline is the next pipeline --------------------
//
// Unit-1 acceptance: the tui verb derives the exact document `next` derives
// over the same artifacts, the observation builder keeps the caller's store
// open (the TUI holds the lock across ticks), and typed errors survive the
// in-process session call.

// TestTuiVerbMatchesNextByteForByte is the seam's acceptance proof: for the
// same scratch day and artifacts, tui's stdout is byte-identical to next's.
func TestTuiVerbMatchesNextByteForByte(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	cfg := writeRunConfig(t)
	srv, _ := startFeed(t)
	envFile := writeRunEnvFile(t, srv.URL)
	day := "2026-10-20"
	args := []string{"--config", cfg, "--env-file", envFile, "--date", day}

	// One run leaves a draft artifact, so the derivation has real content.
	if _, _, err := captureOutput(t, func() error { return runRun(args) }); err != nil {
		t.Fatalf("run: %v", err)
	}

	nextOut, _, err := captureOutput(t, func() error { return runNext(args) })
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	tuiOut, _, err := captureOutput(t, func() error { return runTui(args) })
	if err != nil {
		t.Fatalf("tui: %v", err)
	}
	if !bytes.Equal(nextOut, tuiOut) {
		t.Fatalf("tui stdout differs from next stdout:\nnext: %s\ntui:  %s", nextOut, tuiOut)
	}
	var doc nextAction
	if err := json.Unmarshal(tuiOut, &doc); err != nil {
		t.Fatalf("tui did not print one typed JSON action: %v\n%s", err, tuiOut)
	}
	if doc.Day != day || doc.State != stateJudgment || doc.Judgment == nil {
		t.Fatalf("tui derived %+v, want the block-descriptions judgment for %s", doc, day)
	}
}

// TestBuildNextObservationKeepsStoreOpen pins the property the TUI depends on:
// the builder never closes the caller's store, so successive ticks reuse one
// held lock.
func TestBuildNextObservationKeepsStoreOpen(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	cfgPath := writeRunConfig(t)
	cfg, err := timesheet.LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	day := "2026-10-20"
	store, err := timesheet.OpenStore(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	for tick := 0; tick < 2; tick++ {
		if _, stop, err := buildNextObservation(cfg, cfgPath, "", day, "", store); err != nil || stop != nil {
			t.Fatalf("tick %d: err=%v stop=%+v, want a clean observation", tick, err, stop)
		}
	}
}

// TestTuiVerbRefusesLockedStore checks the single-writer boundary: a second
// process shape (here, a held store in the same test) meets the store's own
// store_locked refusal, typed end to end.
func TestTuiVerbRefusesLockedStore(t *testing.T) {
	cfgPath := writeRunConfig(t)
	cfg, err := timesheet.LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	held, err := timesheet.OpenStore(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	_, _, runErr := captureOutput(t, func() error {
		return runTui([]string{"--config", cfgPath, "--date", "2026-10-20"})
	})
	var app *timesheet.AppError
	if !errors.As(runErr, &app) || app.Code != timesheet.CodeStoreLocked {
		t.Fatalf("tui on a locked store = %v, want the typed store_locked refusal", runErr)
	}
}

// TestHeadlessCallReturnsTypedError pins the call seam's contract: the error a
// caller receives is the protocol error's own typed AppError, and its Error()
// renders the exact "code: message" bytes every existing caller prints.
func TestHeadlessCallReturnsTypedError(t *testing.T) {
	cfgPath := writeRunConfig(t)
	h, err := openHeadless(cfgPath, "")
	if err != nil {
		t.Fatal(err)
	}
	defer h.done()
	rev := 0
	_, callErr := h.call("add_interval", timesheet.Request{Day: "2026-10-20", Revision: &rev})
	var app *timesheet.AppError
	if !errors.As(callErr, &app) {
		t.Fatalf("call error = %v, want a typed *timesheet.AppError", callErr)
	}
	if app.Code != timesheet.CodeUnknownDay {
		t.Fatalf("error code = %q, want %q", app.Code, timesheet.CodeUnknownDay)
	}
	if app.Error() != "unknown_day: day is not open; call open_day first" {
		t.Fatalf("Error() = %q, want the exact printed bytes", app.Error())
	}
}
