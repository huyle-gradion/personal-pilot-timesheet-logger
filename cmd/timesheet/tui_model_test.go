package main

import (
	"errors"
	"testing"

	"github.com/huyle-gradion/pilot-timesheet/internal/timesheet"
)

// --- the pure reducer: table-driven, headless, no terminal state --------------

func dayModel() tuiModel {
	return tuiModel{
		env:  tuiEnv{day: "2026-10-20", project: "WORK"},
		snap: loopSnapshot{day: "2026-10-20", project: "WORK", action: nextAction{Day: "2026-10-20"}},
	}
}

func busyModel() tuiModel {
	m := dayModel()
	m.busy = true
	return m
}

// TestUpdateKeyTable pins the key map: r re-derives, q and ctrl+c close, every
// other key is a no-op, and while an op is in flight every key is ignored so
// two effects can never run on the session at once.
func TestUpdateKeyTable(t *testing.T) {
	tests := []struct {
		name       string
		model      tuiModel
		key        string
		wantBusy   bool
		wantEffect bool
	}{
		{"r re-derives", dayModel(), "r", true, true},
		{"q closes", dayModel(), "q", false, true},
		{"ctrl+c closes", dayModel(), "ctrl+c", false, true},
		{"other keys are a no-op", dayModel(), "x", false, false},
		{"busy locks re-derive", busyModel(), "r", true, false},
		{"busy locks quit", busyModel(), "q", true, false},
		{"busy locks every other key", busyModel(), "x", true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, eff := update(tc.model, keyEvent{key: tc.key})
			if m.busy != tc.wantBusy {
				t.Fatalf("busy = %v, want %v", m.busy, tc.wantBusy)
			}
			if (eff != nil) != tc.wantEffect {
				t.Fatalf("effect = %v, want presence %v", eff, tc.wantEffect)
			}
			if m.stop {
				t.Fatal("no key may stop the program directly; the close effect does")
			}
		})
	}
}

// TestUpdateEventsTable pins every event's state transition.
func TestUpdateEventsTable(t *testing.T) {
	t.Run("tick replaces the snapshot and clears busy and error", func(t *testing.T) {
		m := busyModel()
		m.err = errors.New("older failure")
		fresh := loopSnapshot{day: "d", project: "p", action: nextAction{Day: "d", State: stateStopped, Reason: "r"}}
		m, eff := update(m, tickEvent{snap: fresh})
		if eff != nil || m.busy || m.err != nil {
			t.Fatalf("tick left busy/err/effect: %+v %v", m, eff)
		}
		if m.snap.action.State != stateStopped || m.snap.action.Reason != "r" {
			t.Fatalf("snapshot = %+v, want the fresh stop document", m.snap)
		}
	})
	t.Run("tick failure keeps the last good snapshot", func(t *testing.T) {
		m := dayModel()
		before := m.snap
		m, _ = update(m, actDoneEvent{err: errors.New("boom")})
		if m.busy || m.err == nil || m.snap != before {
			t.Fatalf("tick failure = %+v, want busy cleared, error set, snapshot kept", m)
		}
	})
	t.Run("close sent stops the program", func(t *testing.T) {
		m, _ := update(dayModel(), closeSentEvent{err: errors.New("close failed")})
		if !m.stop || m.closeErr == nil {
			t.Fatalf("close sent = %+v, want stop with the close error kept", m)
		}
	})
	t.Run("unknown events are ignored", func(t *testing.T) {
		m := dayModel()
		got, _ := update(m, unknownTestEvent{})
		if got.snap != m.snap || got.busy || got.stop {
			t.Fatalf("unknown event mutated the model: %+v", got)
		}
	})
}

// TestReDeriveAfterErrorSupersedesReport pins the honest busy line: a new
// re-derive clears the stale error report while it runs, so the footer shows
// the busy lock, not an older failure.
func TestReDeriveAfterErrorSupersedesReport(t *testing.T) {
	m := dayModel()
	m, _ = update(m, actDoneEvent{err: errors.New("boom")})
	if m.err == nil {
		t.Fatal("the failure was not reported")
	}
	m, eff := update(m, keyEvent{key: "r"})
	if eff == nil || !m.busy || m.err != nil {
		t.Fatalf("re-derive after an error = busy %v err %v effect %v, want busy with the error superseded", m.busy, m.err, eff)
	}
}

type unknownTestEvent struct{}

type teaWindowMsg struct{}

func (unknownTestEvent) isTuiEvent() {}

// TestPreflightBuildsFirstSnapshot drives the real preflight over a scratch
// store: an empty day derives the run action before any screen starts.
func TestPreflightBuildsFirstSnapshot(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	pretendTTY(t)
	cfgPath := writeRunConfig(t)
	m, err := preflightTui(&nextFlags{configPath: cfgPath, date: "2026-10-20"})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	defer func() { _ = m.env.h.store.Close() }()
	if m.snap.action.State != stateAction || m.snap.action.Step != "run" {
		t.Fatalf("first snapshot = %+v, want action/run", m.snap.action)
	}
	if m.snap.day != "2026-10-20" || m.snap.project != "WORK" {
		t.Fatalf("snapshot day/project = %q/%q, want the resolved day and project", m.snap.day, m.snap.project)
	}
	if m.env.h == nil || m.env.h.store == nil || m.env.h.session == nil {
		t.Fatal("preflight built no held session over its own store")
	}
}

// TestEnvTickRebuildsLoopSnapshot pins the tick recipe: one call, one rebuilt
// snapshot, the caller's store still open afterwards.
func TestEnvTickRebuildsLoopSnapshot(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	pretendTTY(t)
	cfgPath := writeRunConfig(t)
	m, err := preflightTui(&nextFlags{configPath: cfgPath, date: "2026-10-20"})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	defer func() { _ = m.env.h.store.Close() }()
	ev := m.env.tick()
	te, ok := ev.(tickEvent)
	if !ok {
		t.Fatalf("tick returned %T, want tickEvent", ev)
	}
	if te.snap.day != "2026-10-20" || te.snap.action.State != stateAction {
		t.Fatalf("tick snapshot = %+v, want the derived action for the day", te.snap)
	}
	// The store is still held: a second read through the same lock succeeds.
	if _, err := timesheet.ReadWorkflowSnapshot(m.env.cfg, m.env.h.store, "2026-10-20"); err != nil {
		t.Fatalf("the tick closed the caller's store: %v", err)
	}
}
