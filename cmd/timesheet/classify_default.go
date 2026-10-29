package main

import (
	"fmt"
	"maps"
	"time"

	"github.com/huyle-gradion/pilot-timesheet/internal/timesheet"
)

func runClassifyDefault(f *finalizeFlags) error {
	h, err := openHeadless(f.configPath, f.envFile)
	if err != nil {
		return err
	}
	defer h.done()
	res, err := h.call("open_day", timesheet.Request{Day: f.date})
	if err != nil {
		return err
	}
	summary, ok := res.(timesheet.DaySummary)
	if !ok {
		return fmt.Errorf("classify: unexpected open_day payload")
	}
	if len(summary.Confirmed.Segments) > 0 {
		return fmt.Errorf("classify --configured-default refuses finalized/saved days; use a reviewed classify --file decision to change saved classifications")
	}
	cfg := h.cfg
	project, err := resolveProject("classify", cfg, f.project)
	if err != nil {
		return err
	}
	choice, err := h.session.ConfiguredDefault()
	if err != nil {
		return err
	}
	artifacts, err := timesheet.ReadDayArtifacts(cfg.StatePath, f.date)
	if err != nil {
		return err
	}
	if artifacts.Entries == nil {
		return fmt.Errorf("classify: no recorded entries; run record first")
	}
	units := ledgerUnits(artifacts.Entries.Entries, f.date)
	if !sidecarBinds(f.date, artifacts.Choices, stagedBlocksFrom(units, artifacts.StageMap)) {
		return fmt.Errorf("classify: stale classification sidecar; review the current staged content and repair with classify --file")
	}
	staged, err := applyStage(h, cfg, "classify", f.date, project, summary)
	if err != nil {
		return err
	}
	blocks, err := stagedBlocks(cfg, f.date)
	if err != nil {
		return err
	}
	if !sidecarBinds(f.date, artifacts.Choices, blocks) {
		return fmt.Errorf("classify: stale classification sidecar after staging; review and repair with classify --file")
	}
	selections := maps.Clone(artifacts.Choices)
	if selections == nil {
		selections = make(map[string]timesheet.ClassificationChoiceEntry)
	}
	for id, block := range blocks {
		if _, selected := selections[id]; selected {
			continue
		}
		bound := choice
		bound.ContentDigest = timesheet.BlockContentDigest(f.date, block)
		selections[id] = bound
	}
	applied, err := applySelections(h, f.date, summary.Revision, selections)
	if err != nil {
		return err
	}
	if err := timesheet.WriteClassificationChoices(cfg.StatePath, timesheet.AppVersion, f.date, selections, time.Now()); err != nil {
		return err
	}
	return printJSON(ClassifyReceipt{
		Date: f.date, SidecarFile: timesheet.ChoicesPath(cfg.StatePath, f.date), Applied: applied,
		Selections: len(applied), Revision: summary.Revision, StagedIntervals: staged.StagedIntervals,
	})
}
