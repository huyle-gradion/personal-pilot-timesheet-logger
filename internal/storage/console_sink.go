package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// ConsoleSink prints a human-readable summary table of a timesheet to a writer (stdout by default).
type ConsoleSink struct {
	writer io.Writer
}

// NewConsoleSink creates a ConsoleSink writing to w (or os.Stdout if w is nil).
func NewConsoleSink(w io.Writer) *ConsoleSink {
	if w == nil {
		w = os.Stdout
	}
	return &ConsoleSink{writer: w}
}

// Name returns the identifier for this exporter sink.
func (s *ConsoleSink) Name() string {
	return "console"
}

// Export formats and writes the timesheet table to the underlying writer.
func (s *ConsoleSink) Export(ctx context.Context, record TimesheetRecord) error {
	Assert(ctx != nil, AssertStorageContextNil)
	if err := ctx.Err(); err != nil {
		return err
	}

	header := fmt.Sprintf("Timesheet for %s (Total: %.2fh, Version: %d)\n", record.Date, record.TotalHours, record.Version)
	divider := strings.Repeat("-", 80) + "\n"

	if _, err := io.WriteString(s.writer, header); err != nil {
		return err
	}
	if _, err := io.WriteString(s.writer, divider); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.writer, "%-15s %-10s %-14s %s\n", "TIME", "SOURCE", "CATEGORY", "TITLE / DETAILS"); err != nil {
		return err
	}
	if _, err := io.WriteString(s.writer, divider); err != nil {
		return err
	}

	for _, block := range record.Blocks {
		timeRange := fmt.Sprintf("%s - %s", block.StartTime, block.EndTime)
		details := block.Title
		if block.Description != "" {
			details += fmt.Sprintf(" (%s)", strings.ReplaceAll(block.Description, "\n", " "))
		}
		if _, err := fmt.Fprintf(s.writer, "%-15s %-10s %-14s %s\n", timeRange, block.Source, string(block.Category), details); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(s.writer, divider); err != nil {
		return err
	}

	return nil
}
