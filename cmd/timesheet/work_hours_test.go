package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

// writeWorkHoursRunConfig writes the synthetic calendar config with an optional
// work_hours object spliced in verbatim.
func writeWorkHoursRunConfig(t *testing.T, workHours string) string {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	body := `{
  "timezone": "Asia/Ho_Chi_Minh",
  "state_path": "` + filepath.Join(dir, "state.json") + `",
  "projects": [{"id": "WORK", "name": "Work", "default_classification": "DEV"}],
  "sources": {"enabled": true, "calendar": {"enabled": true, "user": "me@example.com"}}`
	if workHours != "" {
		body += `,
  "work_hours": ` + workHours
	}
	body += "\n}\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestDraftAnnotatesOutsideWorkHours proves the draft subcommand wires the
// loaded policy into the annotation: the 10:00-11:00 meeting is before a
// 13:00-18:00 policy window on a Tuesday.
func TestDraftAnnotatesOutsideWorkHours(t *testing.T) {
	cfg := writeWorkHoursRunConfig(t, `{"days": ["mon", "tue", "wed", "thu", "fri"], "windows": [{"start": "13:00", "end": "18:00"}]}`)
	srv, _ := startFeed(t)
	envFile := writeRunEnvFile(t, srv.URL)

	out, _, err := captureOutput(t, func() error {
		return runDraft([]string{"--config", cfg, "--env-file", envFile, "--date", "2026-10-20"})
	})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	var draft timesheet.Draft
	if err := json.Unmarshal(out, &draft); err != nil {
		t.Fatalf("draft JSON: %v", err)
	}
	if len(draft.Blocks) != 1 {
		t.Fatalf("blocks = %d, want the one synthetic meeting: %s", len(draft.Blocks), out)
	}
	b := draft.Blocks[0]
	if !b.OutsideWorkHours || b.WorkHoursReason != "before_window" {
		t.Fatalf("block = %+v, want outside_work_hours with before_window", b)
	}
}

// TestDraftWithoutWorkHoursUnannotated proves a config with no work_hours emits
// no annotation fields, so today's draft bytes are unchanged.
func TestDraftWithoutWorkHoursUnannotated(t *testing.T) {
	cfg := writeWorkHoursRunConfig(t, "")
	srv, _ := startFeed(t)
	envFile := writeRunEnvFile(t, srv.URL)

	out, _, err := captureOutput(t, func() error {
		return runDraft([]string{"--config", cfg, "--env-file", envFile, "--date", "2026-10-20"})
	})
	if err != nil {
		t.Fatalf("draft: %v", err)
	}
	if bytes.Contains(out, []byte("outside_work_hours")) || bytes.Contains(out, []byte("work_hours_reason")) {
		t.Fatalf("no-policy draft carries annotation fields: %s", out)
	}
	var draft timesheet.Draft
	if err := json.Unmarshal(out, &draft); err != nil {
		t.Fatalf("draft JSON: %v", err)
	}
	if len(draft.Blocks) == 0 || draft.Blocks[0].OutsideWorkHours || draft.Blocks[0].WorkHoursReason != "" {
		t.Fatalf("no-policy draft annotated a block: %+v", draft.Blocks)
	}
}
