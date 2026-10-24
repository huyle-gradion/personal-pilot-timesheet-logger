package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	body := `{
  "timezone": "Asia/Ho_Chi_Minh",
  "state_path": "` + filepath.Join(dir, "state.json") + `",
  "projects": [{"id": "WORK", "name": "Work", "default_classification": "DEV"}]
}`
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// Regression: status --date with no recorded entries file must report zero
// entries, not dereference the missing file (previously a nil panic).
func TestRunStatusDateWithoutEntriesFile(t *testing.T) {
	cfg := writeTestConfig(t)
	if err := runStatus([]string{"--config", cfg, "--date", "2026-10-24"}); err != nil {
		t.Fatalf("status with no entries file failed: %v", err)
	}
}

func TestRunRecordRejectsDateMismatch(t *testing.T) {
	cfg := writeTestConfig(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "reviewed.json")
	body := `{"date":"2026-10-20","blocks":[{"block_id":"b","description":"x"}]}`
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	err := runRecord([]string{"--config", cfg, "--date", "2026-10-24", "--file", file})
	if err == nil {
		t.Fatal("expected a date mismatch error")
	}
}
