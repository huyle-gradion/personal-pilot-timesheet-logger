package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

func TestConfiguredDefaultNextDoesNotRequireJudgmentArtifacts(t *testing.T) {
	cfg, args := configuredDefaultFixture(t)
	seedEntries(t, cfg, testDate, []timesheet.ReviewedBlock{timedBlock("one", "09:00", "10:00", "Build one")})
	runStageCapture(t, args)
	dir, err := dayArtifactsDir(statePathFor(cfg), testDate)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "choices.json"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	step := nextDoc(t, args...)
	if step.State != stateAction || step.Step != "classify" {
		t.Fatalf("irrelevant judgment file must not block configured default: %+v", step)
	}
}

func TestConfiguredDefaultChangesOnlyFutureUnselectedIntervals(t *testing.T) {
	cfg, args := configuredDefaultFixture(t)
	first := timedBlock("a-first", "09:00", "10:00", "Build first")
	seedEntries(t, cfg, testDate, []timesheet.ReviewedBlock{first})
	runClassifyCapture(t, append(args, "--configured-default"))
	before, err := timesheet.ReadClassificationChoices(statePathFor(cfg), testDate)
	if err != nil {
		t.Fatal(err)
	}
	updateDefaultConfig(t, cfg, func(c *timesheet.Config) {
		d := c.Gradion.DefaultClassification
		d.ClassificationID = "cls-override"
		d.Code = "OTHER"
		d.Name = "Explicit Override"
		d.ActivityType = "Engineering"
	})
	seedEntries(t, cfg, testDate, []timesheet.ReviewedBlock{first, timedBlock("z-later", "11:00", "12:00", "Build later")})
	runClassifyCapture(t, append(args, "--configured-default"))
	after, err := timesheet.ReadClassificationChoices(statePathFor(cfg), testDate)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 {
		t.Fatalf("choices = %+v", after)
	}
	for id, choice := range before {
		if after[id] != choice {
			t.Fatalf("prior selection changed: %+v", after)
		}
	}
	for id, choice := range after {
		if _, prior := before[id]; !prior && choice.ClassificationID != "cls-override" {
			t.Fatalf("future selection = %+v", choice)
		}
	}
	updateDefaultConfig(t, cfg, func(c *timesheet.Config) { c.Gradion.DefaultClassification.Name = "Invalid future default" })
	if step := nextDoc(t, args...); step.Step != "save" {
		t.Fatalf("fully selected day must not redefault: %+v", step)
	}
}
