//go:build !darwin && !linux

package main

// isTerminalFD conservatively reports false on platforms without a supported
// ioctl check. The guided run then refuses a TUI request and points the user at
// --plain or --decisions, so a terminal is never assumed.
func isTerminalFD(uintptr) bool { return false }
