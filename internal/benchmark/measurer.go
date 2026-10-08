package benchmark

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"syscall"
	"time"
)

// ExecutionResult captures the measured wall clock time, peak memory, and outputs of an executed process.
type ExecutionResult struct {
	WallClockMS int64
	PeakRSSMB   float64
	Stdout      string
	Stderr      string
}

// RusageToMB converts ru_maxrss to megabytes based on the operating system semantics.
// Darwin (macOS) returns bytes; Linux and BSD return kilobytes.
func RusageToMB(maxrss int64) float64 {
	if maxrss <= 0 {
		return 0.0
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "ios" {
		// Darwin reports ru_maxrss in bytes
		return float64(maxrss) / (1024.0 * 1024.0)
	}
	// Linux, FreeBSD, and OpenBSD report ru_maxrss in kilobytes
	return float64(maxrss) / 1024.0
}

// CurrentProcessRSSMB inspects getrusage for RUSAGE_SELF to determine peak memory of the current process.
func CurrentProcessRSSMB() float64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err == nil && ru.Maxrss > 0 {
		return RusageToMB(ru.Maxrss)
	}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return float64(m.Sys) / (1024.0 * 1024.0)
}

// MeasureCommand executes a command, timing its wall-clock duration and measuring peak RSS memory.
func MeasureCommand(ctx context.Context, name string, args ...string) (ExecutionResult, error) {
	Assert(ctx != nil, AssertBenchmarkContextNil)

	cmd := exec.CommandContext(ctx, name, args...)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	start := time.Now()
	err := cmd.Run()
	wallClockMS := time.Since(start).Milliseconds()

	var peakRSSMB float64
	if cmd.ProcessState != nil {
		if ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok && ru != nil {
			peakRSSMB = RusageToMB(ru.Maxrss)
		}
	}

	if peakRSSMB <= 0 {
		peakRSSMB = CurrentProcessRSSMB()
	}

	res := ExecutionResult{
		WallClockMS: wallClockMS,
		PeakRSSMB:   peakRSSMB,
		Stdout:      stdoutBuf.String(),
		Stderr:      stderrBuf.String(),
	}

	if err != nil {
		return res, fmt.Errorf("command execution failed (%s %v): %w (stderr: %s)", name, args, err, res.Stderr)
	}

	return res, nil
}
