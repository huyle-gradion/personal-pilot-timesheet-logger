package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/huyle-gradion/pilot-timesheet/internal/timesheet"
)

// --- tui verb boundary: gate, store lock, and the loop pipeline ---------------
//
// The TUI's derivation pipeline is the next pipeline; the gates fire before
// the alt screen starts; typed errors survive the in-process session call.

// pretendTTY overrides the terminal gate so tests can reach preflight.
func pretendTTY(t *testing.T) {
	t.Helper()
	saved := ttyGate
	ttyGate = func() bool { return true }
	t.Cleanup(func() { ttyGate = saved })
}

// TestTuiPipelineMatchesNextDerivation is the seam's acceptance proof: after
// one run leaves a draft artifact, the snapshot preflightTui derives is the
// exact document `next` prints for the same day.
func TestTuiPipelineMatchesNextDerivation(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	pretendTTY(t)
	cfg := writeRunConfig(t)
	srv, _ := startFeed(t)
	envFile := writeRunEnvFile(t, srv.URL)
	day := "2026-10-20"
	args := []string{"--config", cfg, "--env-file", envFile, "--date", day}

	if _, _, err := captureOutput(t, func() error { return runRun(args) }); err != nil {
		t.Fatalf("run: %v", err)
	}
	nextOut, _, err := captureOutput(t, func() error { return runNext(args) })
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	m, err := preflightTui(&nextFlags{configPath: cfg, envFile: envFile, date: day})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	tuiOut, err := json.MarshalIndent(m.snap.action, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	tuiOut = append(tuiOut, '\n')
	if !bytes.Equal(nextOut, tuiOut) {
		t.Fatalf("tui snapshot differs from next's document:\nnext: %s\ntui:  %s", nextOut, tuiOut)
	}
	if m.snap.action.State != stateJudgment || m.snap.action.Judgment == nil ||
		m.snap.action.Judgment.Kind != judgmentBlockDescriptions {
		t.Fatalf("snapshot = %+v, want the block-descriptions judgment", m.snap.action)
	}
	_ = m.env.h.store.Close()
}

// TestTuiEventFromMsg pins the driver's text boundary: KeyRunes and KeySpace
// with Alt unset become text, every structural or modified key stays a named
// key event.
func TestTuiEventFromMsg(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.Msg
		want any
	}{
		{"runes are text", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}, textEvent{runes: []rune{'a'}}},
		{"a paste is one text event", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("abc")}, textEvent{runes: []rune("abc")}},
		{"space is text", tea.KeyMsg{Type: tea.KeySpace}, textEvent{runes: []rune{' '}}},
		{"a named key stays named", tea.KeyMsg{Type: tea.KeyDelete}, keyEvent{key: "delete"}},
		{"enter stays named", tea.KeyMsg{Type: tea.KeyEnter}, keyEvent{key: "enter"}},
		{"a modified rune stays named", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}, Alt: true}, keyEvent{key: "alt+r"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tuiEventFromMsg(tc.msg)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("tuiEventFromMsg = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// TestTuiVerbRefusesNonTerminal pins the structural gate: without terminals on
// both stdio sides the verb refuses before opening any store.
func TestTuiVerbRefusesNonTerminal(t *testing.T) {
	cfgPath := writeRunConfig(t)
	_, _, runErr := captureOutput(t, func() error {
		return runTui([]string{"--config", cfgPath, "--date", "2026-10-20"})
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "interactive-only") {
		t.Fatalf("tui without a terminal = %v, want the interactive-only refusal", runErr)
	}
}

// TestTuiVerbRefusesLockedStore checks the single-writer boundary: a held
// store (a second process shape) meets the store's own store_locked refusal,
// typed end to end, before any screen starts.
func TestTuiVerbRefusesLockedStore(t *testing.T) {
	pretendTTY(t)
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
