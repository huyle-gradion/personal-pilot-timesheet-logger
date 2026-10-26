package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

// TestStageMergedClusterEndToEnd is issue #183 rule 1 at stage time: two
// recorded entries that overlap stage as one interval over the union span
// under the head's block id, the other contributor is named in the receipt,
// and confirm plus save persist the merged interval. The entries file is
// written directly because record itself merges (the reviewed-file path is
// pinned separately); stage must merge whatever the ledger holds.
func TestStageMergedClusterEndToEnd(t *testing.T) {
	cfg := writeTestConfig(t)
	if err := os.MkdirAll(filepath.Dir(timesheet.EntriesPath(statePathFor(cfg), testDate)), 0o700); err != nil {
		t.Fatalf("entries dir: %v", err)
	}
	writeEntriesFile(t, cfg, testDate, []timesheet.EntryRecord{
		{BlockID: "work-09:00-11:00", Date: testDate, Start: "09:00", End: "11:00", Kind: "work", Description: "Bridge PRs"},
		{BlockID: "work-10:00-12:00", Date: testDate, Start: "10:00", End: "12:00", Kind: "work", Description: "Ops deploy"},
	})
	receipt := runStageCapture(t, []string{"--config", cfg, "--date", testDate})
	if len(receipt.Staged) != 1 || receipt.Staged[0].BlockID != "work-09:00-11:00" || receipt.Staged[0].Chunk != 0 {
		t.Fatalf("staged = %+v, want one unsplit interval under the head", receipt.Staged)
	}
	if receipt.StagedIntervals != 1 {
		t.Fatalf("staged_intervals = %d, want 1", receipt.StagedIntervals)
	}
	if len(receipt.Skipped) != 1 || receipt.Skipped[0].BlockID != "work-10:00-12:00" {
		t.Fatalf("skipped = %+v, want the merged-away contributor", receipt.Skipped)
	}
	reason := receipt.Skipped[0].Reason
	if !strings.Contains(reason, "work-09:00-11:00") || strings.Contains(reason, "fully claimed") {
		t.Fatalf("skip reason = %q, want it naming the head and never the fully-claimed reason", reason)
	}
	if err := runConfirm([]string{"--config", cfg, "--date", testDate}); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := runSave([]string{"--config", cfg, "--date", testDate}); err != nil {
		t.Fatalf("save: %v", err)
	}
	day := readDurableDay(t, cfg, testDate)
	if len(day.Confirmed) != 1 {
		t.Fatalf("confirmed = %+v, want the single merged interval", day.Confirmed)
	}
	iv := day.Confirmed[0]
	if iv.Start != "09:00" || iv.End != "12:00" {
		t.Fatalf("confirmed interval = %s-%s, want the union span", iv.Start, iv.End)
	}
	if !strings.Contains(iv.Description, "Bridge PRs") || !strings.Contains(iv.Description, "Ops deploy") {
		t.Fatalf("confirmed description = %q, want both contributors named", iv.Description)
	}
}
