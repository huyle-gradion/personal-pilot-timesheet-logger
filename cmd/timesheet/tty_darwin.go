//go:build darwin

package main

import (
	"syscall"
	"unsafe"
)

// isTerminalFD reports whether fd is a terminal, by issuing an ioctl on the
// ACTUAL descriptor. A pipe, a redirect, or /dev/null is NOT a terminal, so a
// `tee`-piped stdout is correctly rejected. The check never inspects the Go
// os.File mode alone (which treats /dev/null as a character device).
func isTerminalFD(fd uintptr) bool {
	var termios syscall.Termios
	_, _, errno := syscall.Syscall6(syscall.SYS_IOCTL, fd, syscall.TIOCGETA,
		uintptr(unsafe.Pointer(&termios)), 0, 0, 0)
	return errno == 0
}
