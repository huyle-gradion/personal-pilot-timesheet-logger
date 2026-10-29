package main

// The TUI's pure reducer: model, events, update. This file imports no terminal
// package — tui.go adapts between bubbletea's driver types and these events,
// so table-driven tests drive the reducer and the views headless.
//
// The loop is the model. loopSnapshot is rebuilt on every tick from the same
// derivation `next` runs (buildNextObservation + deriveDayNext); the screen
// renders it verbatim and every act maps from the derived step. benchState
// below is the TUI's only mutable domain state: operator text that has not
// gone through a session op. It grants no authority and dies with the process.

import (
	"github.com/huyle-gradion/pilot-timesheet/internal/timesheet"
)

// loopSnapshot is one tick's derived view of the day: the day, the resolved
// project, and the typed action the loop derived. Nothing in it survives a
// tick.
type loopSnapshot struct {
	day     string
	project string
	action  nextAction
}

// tuiEnv is the immutable process context the effects close over: one store,
// one session, one held lock. It is set once before the alt screen starts and
// never mutated.
type tuiEnv struct {
	cfg        *timesheet.Config
	configPath string
	envFile    string
	day        string
	project    string
	h          *headlessSession
}

// tuiModel is the Elm-architecture state. update is the only writer.
type tuiModel struct {
	env  tuiEnv
	snap loopSnapshot

	// busy is the in-flight gate: while one op runs to completion, mutating
	// keys are ignored and the view shows an honest busy line. There is no
	// cancellation; HandleLine takes no context.
	busy bool
	// err is the last failed op or tick; the view renders its message and the
	// loop keeps the last good snapshot.
	err error
	// closeErr is the close op's failure on quit, if any; the driver prints it
	// to stderr after the program exits.
	closeErr error

	width  int
	height int

	// bench is the operator's authoring state (Unit 3): text and picks that
	// have not gone through a session op.
	bench benchState

	// stop ends the program; the driver returns tea.Quit when it is set.
	stop bool
}

// benchState is the TUI's only mutable domain state: operator text that has
// not gone through a session op. It grants no authority and dies with the
// process.
type benchState struct {
	screen tuiScreen
	text   string
}

// tuiScreen names the screen the operator is on. The day screen is the loop
// itself; the other screens are acts the derived step licenses.
type tuiScreen int

const (
	screenDay tuiScreen = iota
)

// tuiEvent is one input to the pure update. tui.go adapts bubbletea's driver
// messages onto these; effects return them as bubbletea messages verbatim.
type tuiEvent interface{ isTuiEvent() }

type keyEvent struct{ key string }

func (keyEvent) isTuiEvent() {}

type windowEvent struct{ width, height int }

func (windowEvent) isTuiEvent() {}

// tickEvent carries the rebuilt loop snapshot.
type tickEvent struct{ snap loopSnapshot }

func (tickEvent) isTuiEvent() {}

type tickFailedEvent struct{ err error }

func (tickFailedEvent) isTuiEvent() {}

// closeSentEvent reports the close op was sent (the op's error, if any).
type closeSentEvent struct{ err error }

func (closeSentEvent) isTuiEvent() {}

// effect is one pending IO step: a function the driver runs to completion on
// its own goroutine, returning the event the reducer consumes next. update
// never calls one; the driver does.
type effect func() tuiEvent

// update is the pure reducer: (model, event) -> (model, effect). It reads no
// terminal state, performs no IO, and returns at most one effect.
func update(m tuiModel, ev tuiEvent) (tuiModel, effect) {
	switch e := ev.(type) {
	case keyEvent:
		return updateKey(m, e.key)
	case windowEvent:
		m.width, m.height = e.width, e.height
		return m, nil
	case tickEvent:
		m.busy, m.err = false, nil
		m.snap = e.snap
		return m, nil
	case tickFailedEvent:
		m.busy, m.err = false, e.err
		return m, nil
	case closeSentEvent:
		m.stop, m.closeErr = true, e.err
		return m, nil
	default:
		return m, nil
	}
}

// updateKey maps keys to acts. While an op is in flight every key is ignored
// (no cancellation: a slow op blocks the screen with an honest busy line), so
// two effects can never run on the session at once.
func updateKey(m tuiModel, key string) (tuiModel, effect) {
	if m.busy {
		return m, nil
	}
	switch key {
	case "q", "ctrl+c":
		return m, m.env.sendClose
	case "r":
		// A new op supersedes the last failure's report; the busy lock makes
		// the refresh the only thing the screen can be doing.
		m.busy, m.err = true, nil
		return m, m.env.tick
	default:
		return m, nil
	}
}
