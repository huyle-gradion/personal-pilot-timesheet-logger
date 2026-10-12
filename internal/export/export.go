// Package export renders a reviewed day into a managed daily-note section and
// writes it atomically, preserving everything outside its markers byte-for-byte.
//
// It NEVER calls the remote: it derives status purely from the workflow record.
// The rendered block carries only non-secret, stable data (times, project,
// description, outcome status and stable error codes): never a secret, a feed URL,
// or a raw remote body.
package export

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/submit"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// Managed-section markers (owner decision: HTML comments, v1). A legacy
// `## Clustered timesheet` block is left untouched: there is no silent migration.
const (
	MarkerBegin = "<!-- timesheet:begin v1 -->"
	MarkerEnd   = "<!-- timesheet:end -->"
)

// Status is the derived export status of a reviewed day.
type Status string

const (
	StatusReviewedNotSubmitted Status = "reviewed-not-submitted"
	StatusSubmitted            Status = "submitted"
	StatusPartial              Status = "partial"
	StatusFailed               Status = "failed"
	StatusUnresolved           Status = "unresolved"
	StatusReviewedEmpty        Status = "reviewed-empty"
)

// Row is one non-secret line in a note section.
type Row struct {
	LocalID     string `json:"local_id,omitempty"`
	Start       string `json:"start,omitempty"`
	End         string `json:"end,omitempty"`
	Project     string `json:"project,omitempty"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status,omitempty"`
	Code        string `json:"code,omitempty"`
	// Carry is an ADDITIVE S9 marker on a carryover row, e.g.
	// "-> 2026-10-07 00:00-00:30 (group split_ab12)". It names the later local
	// date and the linked split group so a carry is visible in the export.
	Carry string `json:"carry,omitempty"`
}

// Note is the exportable summary of one day.
type Note struct {
	Date      string `json:"date"`
	Status    Status `json:"status"`
	Confirmed []Row  `json:"confirmed,omitempty"`
	Outcomes  []Row  `json:"outcomes,omitempty"`
	// Carries is an ADDITIVE S9 field: the cross-midnight carry items originating
	// on this day, each targeting a later local day.
	Carries  []Row    `json:"carries,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// Result reports the outcome of one Export call.
type Result struct {
	Path    string `json:"path"`
	Status  Status `json:"status"`
	Written bool   `json:"written"`
}

// Exporter writes a managed daily-note section.
type Exporter interface {
	Export(ctx context.Context, note Note) (Result, error)
}

// DeriveStatus classifies a record's export status. Precedence: an unresolved
// operation → unresolved; a mix of failed and confirmed outcomes → partial; ALL
// terminal outcomes failed (none confirmed) → failed; all terminal outcomes
// confirmed → submitted; a reviewed empty day → reviewed-empty; otherwise
// reviewed-not-submitted.
func DeriveStatus(rec workflow.Record) Status {
	if blocked, _ := submit.RestartGate(rec); blocked {
		return StatusUnresolved
	}
	confirmed, failed := 0, 0
	for _, o := range rec.Outcomes {
		switch o.Status {
		case workflow.OutcomeConfirmed:
			confirmed++
		case workflow.OutcomeFailed:
			failed++
		}
	}
	switch {
	case failed > 0 && confirmed > 0:
		return StatusPartial
	case failed > 0 && confirmed == 0:
		return StatusFailed
	case confirmed > 0 && failed == 0:
		return StatusSubmitted
	}
	if rec.Review != nil && rec.Review.Reviewed && rec.Review.Empty {
		return StatusReviewedEmpty
	}
	return StatusReviewedNotSubmitted
}

// BuildNote derives the exportable note (pure; no remote access).
func BuildNote(rec workflow.Record) Note {
	note := Note{Date: rec.Date, Status: DeriveStatus(rec)}
	for _, ci := range rec.Confirmed {
		row := Row{
			LocalID: ci.ID, Start: ci.StartTime, End: ci.EndTime,
			Project: ci.Project, Description: ci.Description,
		}
		if ci.Carryover {
			if g := strings.TrimSpace(ci.SplitGroupID); g != "" {
				row.Carry = "carryover (group " + g + ")"
			} else {
				row.Carry = "carryover from the previous day"
			}
		}
		note.Confirmed = append(note.Confirmed, row)
	}
	// A cross-midnight carry is visible on the ORIGINATING day's export: a marker
	// naming the later local date and the linked group, plus a warning bullet.
	for _, c := range rec.Carries {
		marker := carryMarker(c.Date, c.Start, c.End, c.SplitGroupID)
		note.Carries = append(note.Carries, Row{
			Start: c.Start, End: c.End, Status: "carryover", Carry: marker,
		})
		note.Warnings = append(note.Warnings, "carry:"+c.Date+" "+c.Start+"-"+c.End+" (group "+c.SplitGroupID+")")
	}
	for _, o := range rec.Outcomes {
		note.Outcomes = append(note.Outcomes, Row{
			LocalID: o.LocalID, Status: string(o.Status), Code: o.ErrorCode,
		})
	}
	if rec.Collection != nil && !rec.Collection.Complete {
		for _, s := range rec.Collection.Sources {
			if s.Status == evidence.StatusComplete || s.Status == evidence.StatusSkipped {
				continue
			}
			note.Warnings = append(note.Warnings, "source_incomplete:"+s.SourceID+":"+s.ErrorCode)
		}
	}
	return note
}

// carryMarker renders the non-secret carry marker naming the later local date
// and the linked split group.
func carryMarker(date, start, end, group string) string {
	return "-> " + date + " " + start + "-" + end + " (group " + group + ")"
}

// Render returns the marker-delimited markdown block for a note. It emits only
// non-secret data and stable codes.
func Render(note Note) string {
	var sb strings.Builder
	sb.WriteString(MarkerBegin)
	sb.WriteString("\n## Timesheet — ")
	sb.WriteString(mdCell(note.Date))
	sb.WriteString("\n\nStatus: `")
	sb.WriteString(string(note.Status))
	sb.WriteString("`\n")

	if len(note.Confirmed) > 0 {
		sb.WriteString("\n### Confirmed\n\n| Start | End | Project | Description |\n| --- | --- | --- | --- |\n")
		for _, r := range note.Confirmed {
			sb.WriteString("| " + mdCell(r.Start) + " | " + mdCell(r.End) + " | " + mdCell(r.Project) + " | " + mdCell(r.Description) + " |\n")
		}
		// A carryover row (the later-day fragment of a cross-midnight split) is
		// marked in the WRITTEN note, so the carry is visible on BOTH days.
		for _, r := range note.Confirmed {
			if strings.TrimSpace(r.Carry) != "" {
				sb.WriteString("- " + mdCell(r.Start) + "-" + mdCell(r.End) + " — " + mdCell(r.Carry) + "\n")
			}
		}
	}
	if len(note.Outcomes) > 0 {
		sb.WriteString("\n### Submission outcomes\n\n| Local | Status | Code |\n| --- | --- | --- |\n")
		for _, r := range note.Outcomes {
			sb.WriteString("| " + mdCell(r.LocalID) + " | " + mdCell(r.Status) + " | " + mdCell(r.Code) + " |\n")
		}
	}
	if len(note.Carries) > 0 {
		sb.WriteString("\n### Carryover to the next day\n\n")
		for _, r := range note.Carries {
			sb.WriteString("- " + mdCell(r.Carry) + "\n")
		}
	}
	if len(note.Warnings) > 0 {
		sb.WriteString("\n### Warnings\n\n")
		for _, w := range note.Warnings {
			sb.WriteString("- " + mdCell(w) + "\n")
		}
	}
	sb.WriteString("\n")
	sb.WriteString(MarkerEnd)
	return sb.String()
}

// noteExporter writes the managed section into <vault>/<dailyDir>/<date>.md.
type noteExporter struct {
	vaultPath string
	dailyDir  string
}

// NewNoteExporter returns an Exporter rooted at vaultPath with a daily-note
// subdirectory. It performs NO IO.
func NewNoteExporter(vaultPath, dailyDir string) Exporter {
	return &noteExporter{vaultPath: strings.TrimSpace(vaultPath), dailyDir: strings.TrimSpace(dailyDir)}
}

func (e *noteExporter) path(date string) string {
	return filepath.Join(e.vaultPath, e.dailyDir, date+".md")
}

// Export splices the rendered block into the daily note, preserving everything
// outside the markers byte-for-byte, and writes atomically. It is idempotent: an
// identical second export produces byte-identical content and never a second
// block. It NEVER calls the remote.
func (e *noteExporter) Export(ctx context.Context, note Note) (Result, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
	}
	if strings.TrimSpace(note.Date) == "" {
		return Result{}, errors.New("export: note date must not be empty")
	}
	path := e.path(note.Date)

	existing, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Result{}, err
	}

	updated := spliceBlock(string(existing), Render(note))
	if err := atomicWrite(path, []byte(updated)); err != nil {
		return Result{}, err
	}
	return Result{Path: path, Status: note.Status, Written: true}, nil
}

// spliceBlock replaces the content between the markers (inclusive) with block, or
// appends block when the markers are absent. Text outside the markers is preserved
// exactly.
func spliceBlock(existing, block string) string {
	begin := strings.Index(existing, MarkerBegin)
	if begin < 0 {
		return appendBlock(existing, block)
	}
	end := strings.Index(existing[begin:], MarkerEnd)
	if end < 0 {
		return appendBlock(existing, block)
	}
	end += begin + len(MarkerEnd)
	return existing[:begin] + block + existing[end:]
}

func appendBlock(existing, block string) string {
	if strings.TrimSpace(existing) == "" {
		return block + "\n"
	}
	out := existing
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out + "\n" + block + "\n"
}

// atomicWrite writes data to path via a temp file in the same directory, fsyncs
// the file, renames it into place, then fsyncs the parent directory so the rename
// survives a crash (mirroring workflow.writeRecordFile).
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".timesheet-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}

	dirHandle, err := os.Open(dir)
	if err != nil {
		return err
	}
	if syncErr := dirHandle.Sync(); syncErr != nil {
		_ = dirHandle.Close()
		return syncErr
	}
	return dirHandle.Close()
}

// mdCell neutralizes table-breaking characters in a cell.
func mdCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}
