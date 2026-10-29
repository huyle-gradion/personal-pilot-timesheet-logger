package main

import (
	"sort"
	"strings"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

func hasClassificationUnselectedBlocker(blockers []timesheet.Blocker) bool {
	for _, b := range blockers {
		if b.Code == "classification_unselected" {
			return true
		}
	}
	return false
}

func deriveFinalizedBlockedPlan(o nextObservation, n nextAction, plan *timesheet.PlanSnapshot) nextAction {
	if !hasClassificationUnselectedBlocker(plan.Blockers) {
		return deriveBlockedPlan(o, n, plan)
	}
	if !o.GradionEnabled {
		return n.withStop("saved intervals need reviewed classifications, but gradion.enabled is false; enable the remote catalog before classifying")
	}
	own := make(map[string]timesheet.Interval, len(o.Workflow.Confirmed))
	var blocks []nextBlock
	for _, iv := range o.Workflow.Confirmed {
		own[iv.ID] = iv
		if strings.TrimSpace(iv.RemoteClassificationID) == "" {
			blocks = append(blocks, nextBlock{
				IntervalID: iv.ID, Start: iv.Start, End: iv.End, Description: iv.Description,
			})
		}
	}
	for _, b := range plan.Blockers {
		if b.Code != "classification_unselected" {
			continue
		}
		if _, ok := own[b.LocalID]; !ok {
			return n.withStop("interval " + quote(b.LocalID) + " needs classification but is not owned by this saved day; review and classify it on its source day, then preview this date again")
		}
	}
	digest := timesheet.SavedClassificationDigest(o.Day, o.Workflow.Revision, o.Workflow.Confirmed)
	if o.ChoicesBinding == digest {
		if o.ChoicesError != nil {
			return n.withStop("the current saved classification answer is invalid: " + o.ChoicesError.Error())
		}
		progress := false
		for id := range o.Choices {
			if iv, ok := own[id]; ok && strings.TrimSpace(iv.RemoteClassificationID) == "" {
				progress = true
			}
		}
		if !progress {
			return n.withStop("the bound classification answer selects no missing saved intervals; author matching choices to remove classification_unselected")
		}
		return n.withAction("classify", append(o.argv("classify"), "--file", o.ChoicesPath),
			"the answer binds to the current saved revision and selects missing classifications; classify re-finalizes before a fresh preview")
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i].IntervalID < blocks[j].IntervalID })
	return n.withJudgment("classify", nextJudgment{
		Kind: judgmentClassifications, Day: o.Day, AnswerFile: o.ChoicesPath,
		LedgerDigest: digest, Blocks: blocks,
		Protocol: "write {\"ledger_digest\":\"" + digest +
			"\",\"selections\":{\"<interval_id>\":{\"catalog_id\":\"...\",\"classification_id\":\"...\"}}} to " +
			o.ChoicesPath + "; copy ledger_digest and interval_ids verbatim from this report; choose explicit catalog_id and classification_id from a classifications --query receipt. " +
			"The blocks are the saved intervals still missing classifications. Empty choices cannot remove this blocker. Return to next after authoring the answer; a fresh plan must verify any remaining blockers after classification.",
	})
}
