package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huyle-gradion/pilot-timesheet/internal/timesheet"
)

func configuredDefaultFixture(t *testing.T) (string, []string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("SYNTHETIC_TOKEN", "synthetic")
	cfg := writeTestConfig(t)
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	raw["gradion"] = json.RawMessage(`{"enabled":true,"endpoint":"http://127.0.0.1:1/mcp","token_env":"SYNTHETIC_TOKEN","default_classification":{"catalog_id":"cat-default","classification_id":"cls-academy","code":"ACADEMY","name":"Synthetic Academy","activity_type":"Training"}}`)
	data, err = json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, data, 0600); err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(filepath.Dir(cfg), "synthetic.env")
	if err := os.WriteFile(env, []byte("SYNTHETIC_TOKEN=synthetic\n"), 0600); err != nil {
		t.Fatal(err)
	}
	seedCatalog(t, cfg, "cat-default", timesheet.ClassificationOption{ID: "cls-academy", Code: "ACADEMY", Name: "Synthetic Academy", ActivityType: "Training"}, timesheet.ClassificationOption{ID: "cls-override", Code: "OTHER", Name: "Explicit Override", ActivityType: "Engineering"})
	return cfg, []string{"--config", cfg, "--env-file", env, "--date", testDate}
}

func updateDefaultConfig(t *testing.T, path string, update func(*timesheet.Config)) {
	t.Helper()
	cfg, err := timesheet.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	update(cfg)
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestConfiguredDefaultPreservesExplicitChoicesAndPriorDefaults(t *testing.T) {
	cfg, args := configuredDefaultFixture(t)
	seedEntries(t, cfg, testDate, []timesheet.ReviewedBlock{
		timedBlock("one", "09:00", "10:00", "Build one"), timedBlock("two", "11:00", "12:00", "Build two"),
	})
	stage := runStageCapture(t, args)
	overrideID := stage.Staged[0].IntervalID
	file := writeChoicesFile(t, cfg, `{"selections":{"`+overrideID+`":{"catalog_id":"cat-default","classification_id":"cls-override"}}}`)
	runClassifyCapture(t, append(args, "--file", file))
	runClassifyCapture(t, append(args, "--configured-default"))
	before, err := timesheet.ReadClassificationChoices(statePathFor(cfg), testDate)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 2 || before[overrideID].ClassificationID != "cls-override" {
		t.Fatalf("choices = %+v", before)
	}
	updateDefaultConfig(t, cfg, func(c *timesheet.Config) {
		d := c.Gradion.DefaultClassification
		d.ClassificationID = "cls-override"
		d.Code = "OTHER"
		d.Name = "Explicit Override"
		d.ActivityType = "Engineering"
	})
	runClassifyCapture(t, append(args, "--configured-default"))
	after, err := timesheet.ReadClassificationChoices(statePathFor(cfg), testDate)
	if err != nil {
		t.Fatal(err)
	}
	for id, choice := range before {
		if after[id] != choice {
			t.Fatalf("existing choice changed: %s %+v -> %+v", id, choice, after[id])
		}
	}
	step := nextDoc(t, args...)
	if step.Step != "save" {
		t.Fatalf("fully selected day must save, got %+v", step)
	}
}

func TestConfiguredDefaultFillsExplicitEmptySidecar(t *testing.T) {
	cfg, args := configuredDefaultFixture(t)
	seedEntries(t, cfg, testDate, []timesheet.ReviewedBlock{timedBlock("one", "09:00", "10:00", "Build one")})
	file := writeChoicesFile(t, cfg, `{"selections":{}}`)
	runClassifyCapture(t, append(args, "--file", file))
	step := nextDoc(t, args...)
	if step.Step != "classify" || step.Action == nil || !strings.Contains(strings.Join(step.Action.Cmd, " "), "--configured-default") {
		t.Fatalf("empty choices must route default: %+v", step)
	}
	receipt := runClassifyCapture(t, step.Action.Cmd[2:])
	if receipt.Selections != 1 {
		t.Fatalf("receipt = %+v", receipt)
	}
}

func TestConfiguredDefaultStaleSidecarFailsWithoutReplacingChoices(t *testing.T) {
	cfg, args := configuredDefaultFixture(t)
	seedEntries(t, cfg, testDate, []timesheet.ReviewedBlock{timedBlock("one", "09:00", "10:00", "Build one")})
	runClassifyCapture(t, append(args, "--configured-default"))
	path := timesheet.ChoicesPath(statePathFor(cfg), testDate)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	seedEntries(t, cfg, testDate, []timesheet.ReviewedBlock{timedBlock("one", "09:00", "10:00", "Changed content")})
	_, _, applyErr := captureOutput(t, func() error { return runClassify(append(args, "--configured-default")) })
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if applyErr == nil || !bytes.Equal(before, after) {
		t.Fatalf("stale choices changed, err = %v", applyErr)
	}
	step := nextDoc(t, args...)
	if step.State != stateStopped || !strings.Contains(step.Reason, "--file") {
		t.Fatalf("stale sidecar must stop with repair: %+v", step)
	}
}

func TestConfiguredDefaultInvalidCacheFailsClosed(t *testing.T) {
	for _, problem := range []string{"missing catalog", "semantic mismatch"} {
		t.Run(problem, func(t *testing.T) {
			cfg, args := configuredDefaultFixture(t)
			seedEntries(t, cfg, testDate, []timesheet.ReviewedBlock{timedBlock("one", "09:00", "10:00", "Build one")})
			runStageCapture(t, args)
			updateDefaultConfig(t, cfg, func(c *timesheet.Config) {
				if problem == "missing catalog" {
					c.Gradion.DefaultClassification.CatalogID = "not-cached"
				} else {
					c.Gradion.DefaultClassification.Name = "Wrong semantics"
				}
			})
			_, _, err := captureOutput(t, func() error { return runClassify(append(args, "--configured-default")) })
			if err == nil {
				t.Fatal("invalid cache accepted")
			}
			choices, err := timesheet.ReadClassificationChoices(statePathFor(cfg), testDate)
			if err != nil || choices != nil {
				t.Fatalf("unexpected choices %+v, err %v", choices, err)
			}
			if step := nextDoc(t, args...); step.State != stateStopped {
				t.Fatalf("invalid binding must stop: %+v", step)
			}
		})
	}
}

func TestConfiguredDefaultRefusesFinalizedWithoutMutation(t *testing.T) {
	cfg, args := configuredDefaultFixture(t)
	seedEntries(t, cfg, testDate, []timesheet.ReviewedBlock{timedBlock("one", "09:00", "10:00", "Build one")})
	runClassifyCapture(t, append(args, "--configured-default"))
	if err := runSave(args); err != nil {
		t.Fatal(err)
	}
	paths := []string{statePathFor(cfg), timesheet.ChoicesPath(statePathFor(cfg), testDate)}
	before := make([][]byte, len(paths))
	for i, path := range paths {
		var err error
		before[i], err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := captureOutput(t, func() error { return runClassify(append(args, "--configured-default")) })
	if err == nil {
		t.Fatal("finalized day accepted")
	}
	for i, path := range paths {
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before[i], after) {
			t.Fatalf("finalized file mutated: %s", path)
		}
	}
}

func TestConfiguredDefaultFlagsAreExclusive(t *testing.T) {
	_, args := configuredDefaultFixture(t)
	if _, err := parseClassifyFlags(append(args, "--configured-default", "--file", "unused.json")); err == nil {
		t.Fatal("conflicting flags accepted")
	}
}

func TestConfiguredDefaultFillsSplitAndMeetingIntervals(t *testing.T) {
	cfg, args := configuredDefaultFixture(t)
	work := timedBlock("work", "08:00", "13:00", "Build synthetic feature")
	meeting := timedBlock("meeting", "14:00", "15:00", "Discuss synthetic feature")
	meeting.Kind = "meeting"
	seedEntries(t, cfg, testDate, []timesheet.ReviewedBlock{work, meeting})
	if err := runClassify(append(args, "--configured-default")); err != nil {
		t.Fatal(err)
	}
	choices, err := timesheet.ReadClassificationChoices(statePathFor(cfg), testDate)
	if err != nil {
		t.Fatal(err)
	}
	if len(choices) != 3 {
		t.Fatalf("choices = %v, want all 3 split/meeting intervals", choices)
	}
	for _, choice := range choices {
		if choice.ClassificationID != "cls-academy" || choice.ContentDigest == "" {
			t.Fatalf("choice = %+v", choice)
		}
	}
}
