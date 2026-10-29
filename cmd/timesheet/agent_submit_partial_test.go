package main

import (
	"encoding/json"
	"testing"

	"github.com/huyle-gradion/pilot-timesheet/internal/timesheet"
)

func TestAgentSubmitPartialReceiptSurvivesRestartWithoutRetry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg, remote := savedDayForSubmit(t, "Two chunks", nil, "second-tool")
	seedEntries(t, cfg, testDate, []timesheet.ReviewedBlock{timedBlock("work-1", "09:00", "14:00", "Two chunks")})
	staged := runStageCapture(t, []string{"--config", cfg, "--date", testDate, "--revise"})
	selections := map[string]timesheet.ClassificationChoiceEntry{}
	for _, interval := range staged.Staged {
		selections[interval.IntervalID] = timesheet.ClassificationChoiceEntry{CatalogID: "cat-1", ClassificationID: "cls-dev"}
	}
	data, err := json.Marshal(map[string]map[string]timesheet.ClassificationChoiceEntry{"selections": selections})
	if err != nil {
		t.Fatal(err)
	}
	choices := writeChoicesFile(t, cfg, string(data))
	runClassifyCapture(t, []string{"--config", cfg, "--date", testDate, "--file", choices})
	review := agentReview(t, cfg)
	if len(review.Plan.Operations) != 2 {
		t.Fatalf("review=%+v", review)
	}
	var receipt timesheet.SubmissionReceipt
	out := captureStdout(t, func() {
		if err := runAgentSubmit(agentConfirmArgs(cfg, review)); err != nil {
			t.Fatal(err)
		}
	})
	if err := json.Unmarshal([]byte(out), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Confirmed != 1 || receipt.Unresolved != 1 || remote.writeCount() != 2 {
		t.Fatalf("receipt=%+v writes=%d", receipt, remote.writeCount())
	}
	status := runSubmitStatusCapture(t, []string{"--config", cfg, "--date", testDate})
	if len(status.Unresolved) != 1 {
		t.Fatalf("status=%+v", status)
	}
	if err := runAgentSubmit(agentConfirmArgs(cfg, review)); err == nil || remote.writeCount() != 2 {
		t.Fatalf("retry err=%v writes=%d", err, remote.writeCount())
	}
}
