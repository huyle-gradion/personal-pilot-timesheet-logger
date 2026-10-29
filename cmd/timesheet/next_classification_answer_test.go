package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huyle-gradion/pilot-timesheet/internal/timesheet"
)

func TestNextReadyPlanIgnoresStaleMalformedClassificationSelections(t *testing.T) {
	cfg, args, _ := savedNextClassificationFixture(t)
	day := readDurableDay(t, cfg, testDate)
	choices := map[string]timesheet.ClassificationChoiceEntry{}
	for _, iv := range day.Confirmed {
		choices[iv.ID] = timesheet.ClassificationChoiceEntry{CatalogID: "cat-1", ClassificationID: "cls-dev"}
	}
	body, err := json.Marshal(struct {
		Selections map[string]timesheet.ClassificationChoiceEntry `json:"selections"`
	}{choices})
	if err != nil {
		t.Fatal(err)
	}
	file := writeChoicesFile(t, cfg, string(body))
	runClassifyCapture(t, append(args, "--file", file))
	if plan := runPlanCapture(t, args); plan.State != timesheet.PlanReady {
		t.Fatalf("fixture plan must be ready: %+v", plan)
	}
	config, err := timesheet.LoadConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := dayArtifactsDir(config.StatePath, testDate)
	if err != nil {
		t.Fatal(err)
	}
	for _, answer := range []string{
		`{"ledger_digest":"old","selections":{"obsolete":{"classification_id":"cls-dev"}}}`,
		`{"ledger_digest":"old","selections":"invalid"}`,
		`{"ledger_digest":42,"selections":{}}`,
		`{invalid`,
	} {
		writeAnswerFile(t, filepath.Join(dir, "choices.json"), answer)
		if got := nextDoc(t, args...); got.State != stateHumanGates {
			t.Fatalf("irrelevant malformed choices must not hide a ready plan: %+v", got)
		}
	}
}

func TestNextBlockedSavedDayRejudgesStaleMalformedClassificationSelections(t *testing.T) {
	cfg, args, _ := savedNextClassificationFixture(t)
	plan := runPlanCapture(t, args)
	ageSavedClassificationPlan(t, cfg, plan.PlanID)
	initial := nextDoc(t, args...)
	if initial.Judgment == nil {
		t.Fatalf("fixture requires a judgment: %+v", initial)
	}
	for _, answer := range []string{
		`{"ledger_digest":"old","selections":{"obsolete":{"classification_id":"cls-dev"}}}`,
		`{"ledger_digest":"old","selections":"invalid"}`,
		`{invalid`,
	} {
		writeAnswerFile(t, initial.Judgment.AnswerFile, answer)
		if got := nextDoc(t, args...); got.State != stateJudgment || got.Judgment == nil || got.Judgment.LedgerDigest != initial.Judgment.LedgerDigest {
			t.Fatalf("stale malformed selections must be replaced by a fresh judgment: %+v", got)
		}
	}
}

func TestNextStagedCurrentMalformedSelectionsRemainClassifyOwned(t *testing.T) {
	o := unclassifiedDay()
	_, _, digest := proofOf(o.Artifacts.Entries.Entries, o.Artifacts.StageMap)
	o.ChoicesPath = filepath.Join(t.TempDir(), "choices.json")
	writeAnswerFile(t, o.ChoicesPath, `{"ledger_digest":"`+digest+`","selections":{"iv-1":{"classification_id":"cls-dev"}}}`)
	answer, err := readClassificationAnswer(o.ChoicesPath)
	if err != nil {
		t.Fatal(err)
	}
	o.ChoicesPresent, o.ChoicesBinding = answer.Present, answer.Binding
	o.Choices, o.ChoicesError = answer.Selections, answer.SelectionError
	assertCmd(t, deriveDayNext(o), o.classifyArgv())
}

func TestNextBlockedSavedDayRejectsCurrentMalformedClassificationSelections(t *testing.T) {
	cfg, args, _ := savedNextClassificationFixture(t)
	plan := runPlanCapture(t, args)
	ageSavedClassificationPlan(t, cfg, plan.PlanID)
	initial := nextDoc(t, args...)
	if initial.Judgment == nil {
		t.Fatalf("fixture requires a judgment: %+v", initial)
	}
	writeAnswerFile(t, initial.Judgment.AnswerFile, `{"ledger_digest":"`+initial.Judgment.LedgerDigest+`","selections":{"`+initial.Judgment.Blocks[0].IntervalID+`":{"classification_id":"cls-dev"}}}`)
	got := nextDoc(t, args...)
	if got.State != stateStopped || got.Action != nil || !strings.Contains(got.Reason, string(timesheet.CodeBadRequest)) {
		t.Fatalf("current malformed required selections must report their validation error: %+v", got)
	}
}
