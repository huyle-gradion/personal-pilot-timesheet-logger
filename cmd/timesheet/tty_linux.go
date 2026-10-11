//go:build linux

package main

import (
	"syscall"
	"unsafe"
)

// isTerminalFD reports whether fd is a terminal, by issuing an ioctl on the
// ACTUAL descriptor. A pipe, a redirect, or /dev/null is NOT a terminal, so a
// `tee`-piped stdout is correctly rejected.
func isTerminalFD(fd uintptr) bool {
	var termios syscall.Termios
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, syscall.TCGETS,
		uintptr(unsafe.Pointer(&termios)), 0, 0, 0)
	return errno == 0
}
