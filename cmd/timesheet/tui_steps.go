package main

// The TUI's step recipes over one held headlessSession. The session is built
// literally over the TUI's own store and session in tui.go; openHeadless is
// never called here, because it would open a second store. Every recipe runs
// to completion inside one effect (one bubbletea command), so the session
// stays single-owner by construction.

import (
	"github.com/huyle-gradion/pilot-timesheet/internal/timesheet"
)

// tick rebuilds the loop snapshot: the same derivation `next` runs, over the
// store the TUI holds. A stop document is a snapshot like any other — the
// screen renders its reason verbatim.
func (e tuiEnv) tick() tuiEvent {
	obs, stop, err := buildNextObservation(e.cfg, e.configPath, e.envFile, e.day, e.project, e.h.store)
	if err != nil {
		return tickFailedEvent{err}
	}
	snap := loopSnapshot{day: e.day, project: obs.Project}
	if stop != nil {
		snap.action = *stop
		return tickEvent{snap}
	}
	snap.action = deriveDayNext(obs)
	return tickEvent{snap}
}

// sendClose sends the close op and lets the program stop. Unsaved session
// state and bench text die with the process; nothing replays on restart.
func (e tuiEnv) sendClose() tuiEvent {
	_, err := e.h.call("close", timesheet.Request{})
	return closeSentEvent{err}
}
