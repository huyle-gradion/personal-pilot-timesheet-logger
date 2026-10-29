package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

func savedNextClassificationFixture(t *testing.T) (string, []string, *stubRemote) {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SYNTHETIC_GRADION_TOKEN", "synthetic-token")
	stub := &stubRemote{entries: map[string][]map[string]any{testDate: {}}}
	server := httptest.NewServer(stub)
	t.Cleanup(server.Close)
	cfg := writeTestConfigWithGradion(t, server.URL)
	envFile := filepath.Join(filepath.Dir(cfg), "synthetic.env")
	writeAnswerFile(t, envFile, "SYNTHETIC_GRADION_TOKEN=synthetic-token\n")
	args := []string{"--config", cfg, "--env-file", envFile, "--date", testDate}
	seedEntries(t, cfg, testDate, []timesheet.ReviewedBlock{
		timedBlock("work-1", "09:00", "10:00", "Build the bridge"),
		timedBlock("work-2", "11:00", "12:00", "Review the deck"),
	})
	seedCatalog(t, cfg, "cat-1", timesheet.ClassificationOption{
		ID: "cls-dev", Name: "Development", Code: "DEV", ActivityType: "engineering",
	})
	runStageCapture(t, args)
	for _, flow := range []func([]string) error{runConfirm, runSave} {
		if _, _, err := captureOutput(t, func() error { return flow(args) }); err != nil {
			t.Fatalf("finalize synthetic day: %v", err)
		}
	}
	return cfg, args, stub
}

func TestNextFinalizedClassificationProgressesToHumanGates(t *testing.T) {
	cfg, args, stub := savedNextClassificationFixture(t)
	before := readDurableDay(t, cfg, testDate)
	blocked := runPlanCapture(t, args)
	if blocked.State != "blocked" || !hasBlocker(blocked.Blockers, "classification_unselected", "") {
		t.Fatalf("initial plan = %+v, want blocked on classification_unselected", blocked)
	}
	ageSavedClassificationPlan(t, cfg, blocked.PlanID)

	step := nextDoc(t, args...)
	if step.State != stateJudgment || step.Step != "classify" || step.Judgment == nil || step.Judgment.Kind != judgmentClassifications {
		t.Fatalf("next = %+v, want classification judgment for the saved day", step)
	}
	judgment := step.Judgment
	if judgment.LedgerDigest == "" {
		t.Fatal("saved-day classification judgment has no ledger_digest content binding")
	}
	if len(judgment.Blocks) != len(before.Confirmed) {
		t.Fatalf("judgment blocks = %+v, want every durable interval", judgment.Blocks)
	}
	byID := map[string]timesheet.Interval{}
	for _, iv := range before.Confirmed {
		byID[iv.ID] = iv
	}
	selections := map[string]timesheet.ClassificationChoiceEntry{}
	for _, block := range judgment.Blocks {
		iv, ok := byID[block.IntervalID]
		if !ok || block.Description != iv.Description || block.Start != iv.Start || block.End != iv.End {
			t.Fatalf("judgment block = %+v, want durable interval %+v", block, iv)
		}
		selections[block.IntervalID] = timesheet.ClassificationChoiceEntry{CatalogID: "cat-1", ClassificationID: "cls-dev"}
	}
	body, err := json.Marshal(struct {
		LedgerDigest string                                         `json:"ledger_digest"`
		Selections   map[string]timesheet.ClassificationChoiceEntry `json:"selections"`
	}{judgment.LedgerDigest, selections})
	if err != nil {
		t.Fatal(err)
	}
	if parsed, err := timesheet.ParseClassificationChoices(body); err != nil || len(parsed) != 2 {
		t.Fatalf("answer is not an existing classification choices document: %v, %v", parsed, err)
	}
	writeAnswerFile(t, judgment.AnswerFile, string(body))

	step = nextDoc(t, args...)
	if step.State != stateAction || step.Step != "classify" || step.Action == nil {
		t.Fatalf("next after bound answer = %+v, want executable classify", step)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wantArgv := append([]string{exe, "classify"}, args...)
	wantArgv = append(wantArgv, "--file", judgment.AnswerFile)
	if !reflect.DeepEqual(step.Action.Cmd, wantArgv) {
		t.Fatalf("classify argv = %#v, want %#v", step.Action.Cmd, wantArgv)
	}
	receipt := runClassifyCapture(t, step.Action.Cmd[2:])
	if !receipt.Refinalized || receipt.Revision != before.Revision+1 || receipt.Selections != 2 {
		t.Fatalf("classify = %+v, want both selections re-finalized at revision %d", receipt, before.Revision+1)
	}
	after := readDurableDay(t, cfg, testDate)
	if len(after.Confirmed) != 2 || after.Revision != before.Revision+1 {
		t.Fatalf("durable day = %+v, want two intervals at the new revision", after)
	}
	for _, iv := range after.Confirmed {
		old, ok := byID[iv.ID]
		if !ok || iv.Description != old.Description || iv.Start != old.Start || iv.End != old.End {
			t.Fatalf("re-finalization changed interval content: before %+v, after %+v", old, iv)
		}
		if iv.RemoteCatalogID != "cat-1" || iv.RemoteClassificationID != "cls-dev" {
			t.Fatalf("saved interval = %+v, want the synthetic selection", iv)
		}
	}

	step = nextDoc(t, args...)
	if step.State != stateAction || step.Step != "plan" || step.Action == nil {
		t.Fatalf("next after classify = %+v, want a fresh plan action", step)
	}
	ready := runPlanCapture(t, step.Action.Cmd[2:])
	if ready.State != "ready" || len(ready.Blockers) != 0 || ready.PlanID == blocked.PlanID {
		t.Fatalf("fresh plan = %+v, want a new ready plan", ready)
	}
	step = nextDoc(t, args...)
	if step.State != stateHumanGates || step.Human == nil || step.Action != nil || step.Judgment != nil {
		t.Fatalf("next after ready preview = %+v, want terminal human_gates", step)
	}
	store, err := timesheet.OpenStore(statePathFor(cfg))
	if err != nil {
		t.Fatal(err)
	}
	state := store.State()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if len(state.Approvals) != 0 || len(state.Intents) != 0 || len(state.Executions) != 0 {
		t.Fatal("the next/classify/plan loop crossed the approval or submission boundary")
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	for _, call := range stub.calls {
		if call != "timesheet__get_my_week" {
			t.Fatalf("unexpected remote tool %q; only synthetic preview reads are permitted", call)
		}
	}
}

func ageSavedClassificationPlan(t *testing.T, cfg, id string) {
	t.Helper()
	store, err := timesheet.OpenStore(statePathFor(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	state := store.State()
	state.Plans[id].CreatedAt = "2026-10-26T00:00:00Z"
	if err := store.CommitState(state); err != nil {
		t.Fatal(err)
	}
}

func TestClassifyFinalizedRejectsStaleLedgerBindingWithoutMutation(t *testing.T) {
	cfg, args, _ := savedNextClassificationFixture(t)
	day := readDurableDay(t, cfg, testDate)
	choices := `{"ledger_digest":"stale-saved-content","selections":{"` + day.Confirmed[0].ID + `":{"catalog_id":"cat-1","classification_id":"cls-dev"}}}`
	file := writeChoicesFile(t, cfg, choices)
	before, err := os.ReadFile(statePathFor(cfg))
	if err != nil {
		t.Fatal(err)
	}
	_, _, classifyErr := captureOutput(t, func() error {
		return runClassify(append(args, "--file", file))
	})
	after, err := os.ReadFile(statePathFor(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if classifyErr == nil {
		t.Error("finalized classify accepted a stale ledger_digest")
	}
	if !bytes.Equal(before, after) {
		t.Error("finalized classify mutated the durable store despite a stale ledger_digest")
	}
}
