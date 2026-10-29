package main

// The TUI's step recipes over one held headlessSession, built in tui.go over
// the TUI's own store; openHeadless is never called here. Every recipe runs to
// completion inside one effect, so the session stays single-owner by
// construction. stepAct is the total table over the derived step, and the acts
// reuse the verbs' own entry cores: the TUI duplicates zero pipeline
// decisions.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/huyle-gradion/pilot-timesheet/internal/timesheet"
)

// stepAct names the act the derived step licenses, total over the derivation:
// an action step's verb, a judgment kind to author, human_gates, or "" for a
// render-only state (stopped, underivable, unknown). classify's two action
// sub-cases name themselves through the argv.
func stepAct(a nextAction) string {
	switch a.State {
	case stateJudgment:
		if a.Judgment != nil {
			return a.Judgment.Kind
		}
	case stateHumanGates:
		return stateHumanGates
	case stateAction:
		if a.Action != nil {
			if a.Step == "classify" && slices.Contains(a.Action.Cmd, "--configured-default") {
				return "classify-default"
			}
			return a.Step
		}
	}
	return ""
}

// act runs the one act the derived step licenses; the reducer re-derives
// after every success.
func (e tuiEnv) act(step, project string) any {
	var err error
	switch step {
	case "run":
		err = e.actRun()
	case "record":
		err = e.actRecord()
	case "stage":
		err = e.actStage(project)
	case "classify":
		err = e.actClassify(project)
	case "classify-default":
		err = e.actClassifyDefault(project)
	case "save":
		err = e.actSave(project)
	case "plan":
		_, err = e.h.call("preview_plan", timesheet.Request{Day: e.day})
	default:
		err = fmt.Errorf("tui: the derived step offers no act")
	}
	return actDoneEvent{err: err}
}

// artifactPath joins one artifacts-file name for the day.
func (e tuiEnv) artifactPath(name string) (string, error) {
	dir, err := dayArtifactsDir(e.cfg.StatePath, e.day)
	return filepath.Join(dir, name), err
}

// actRun is the run verb's core plus the artifacts draft write.
func (e tuiEnv) actRun() error {
	draft, _, err := collectDraft(e.h, e.day)
	if err != nil {
		return err
	}
	data, err := jsonBytes(draft)
	if err != nil {
		return err
	}
	dir, err := dayArtifactsDir(e.cfg.StatePath, e.day)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("tui: the artifacts directory cannot be created")
	}
	return os.WriteFile(filepath.Join(dir, "draft.json"), data, 0o600)
}

// actRecord is record's descriptions-only core over the artifacts files; it
// opens no store, like the record verb.
func (e tuiEnv) actRecord() error {
	dir, err := dayArtifactsDir(e.cfg.StatePath, e.day)
	if err != nil {
		return err
	}
	reviewed, err := composeDescriptionsOnly(filepath.Join(dir, "draft.json"), filepath.Join(dir, "descriptions.json"))
	if err != nil {
		return err
	}
	if reviewed.Date != e.day {
		return fmt.Errorf("tui: the reviewed draft's date %s does not match the resolved day %s", reviewed.Date, e.day)
	}
	_, err = timesheet.RecordEntries(e.cfg.StatePath, timesheet.AppVersion, reviewed, false, time.Now())
	return err
}

// actStage mirrors the stage verb on a not-yet-finalized day (a finalized day
// is a stopped reason naming --revise, never an act).
func (e tuiEnv) actStage(project string) error {
	summary, err := openFinalizeDay(e.h, "tui", e.day)
	if err != nil {
		return err
	}
	_, err = applyStage(e.h, e.cfg, "tui", e.day, project, summary)
	return err
}

// actClassify, actClassifyDefault and actSave are the verbs' own entries over
// the held session.
func (e tuiEnv) actClassify(project string) error {
	dir, err := dayArtifactsDir(e.cfg.StatePath, e.day)
	if err != nil {
		return err
	}
	_, err = classifyFileDay(e.h, e.cfg, e.day, project, filepath.Join(dir, "choices.json"))
	return err
}

func (e tuiEnv) actClassifyDefault(project string) error {
	_, err := classifyDefaultDay(e.h, e.day, project)
	return err
}

func (e tuiEnv) actSave(project string) error {
	_, err := saveDay(e.h, e.cfg, "tui", e.day, project)
	return err
}

// tick rebuilds the loop snapshot: the same derivation `next` runs, over the
// store the TUI holds; a stop document is a snapshot like any other.
func (e tuiEnv) tick() any {
	obs, stop, err := buildNextObservation(e.cfg, e.configPath, e.envFile, e.day, e.project, e.h.store)
	if err != nil {
		return actDoneEvent{err: err}
	}
	snap := loopSnapshot{day: e.day, project: obs.Project}
	if stop != nil {
		snap.action = *stop
		return tickEvent{snap: snap}
	}
	snap.action = deriveDayNext(obs)
	if snap.action.State == stateHumanGates && snap.action.Human != nil {
		// The gates screen renders the day's approvals and unresolved writes;
		// an operator who dismissed the receipt re-acquires the active
		// approval_id here. A failed read leaves the status absent.
		_, snap.status, _ = e.gateArtifact(gateSubmit)
	}
	return tickEvent{snap: snap}
}

// gateArtifact fetches the gate's fresh artifact (approve: preview_plan; submit:
// submission_status). openGate and confirmGate fetch through it, so the
// dialog's display and its pre-op re-check always run the same read.
func (e tuiEnv) gateArtifact(kind string) (*timesheet.PlanPreview, *timesheet.SubmissionStatusResult, error) {
	op := "preview_plan"
	if kind == gateSubmit {
		op = "submission_status"
	}
	res, err := e.h.call(op, timesheet.Request{Day: e.day})
	if err != nil {
		return nil, nil, err
	}
	if kind == gateSubmit {
		status, ok := res.(timesheet.SubmissionStatusResult)
		if !ok {
			return nil, nil, fmt.Errorf("tui: unexpected submission_status payload")
		}
		return nil, &status, nil
	}
	preview, ok := res.(timesheet.PlanPreview)
	if !ok {
		return nil, nil, fmt.Errorf("tui: unexpected preview_plan payload")
	}
	return &preview, nil, nil
}

// openGate opens the dialog on the fresh artifact, prompting for the exact id
// the operator must type back.
func (e tuiEnv) openGate(kind string) any {
	preview, status, err := e.gateArtifact(kind)
	if err != nil {
		return actDoneEvent{err: err}
	}
	prompt := "type the exact plan_id above to approve"
	if kind == gateSubmit {
		prompt = "type the exact approval_id shown above to submit"
	}
	var approvals []timesheet.ApprovalRow
	if status != nil {
		approvals = status.Approvals
	}
	return editorOpenedEvent{kind: kind, answerFile: prompt, preview: preview, approvals: approvals}
}

// confirmGate re-runs the freshness check immediately before any op could be
// emitted; the decision itself is the reducer's.
func (e tuiEnv) confirmGate(kind, echo string) any {
	preview, status, err := e.gateArtifact(kind)
	if err != nil {
		return actDoneEvent{err: err}
	}
	return gateCheckEvent{fresh: preview, status: status, echo: echo}
}

// approveOp emits approve_plan once, behind the gate; refusals pass through
// verbatim, nothing is retried.
func (e tuiEnv) approveOp(planID string) any {
	res, err := e.h.call("approve_plan", timesheet.Request{Day: e.day, PlanID: planID})
	if err != nil {
		return actDoneEvent{err: err}
	}
	receipt, ok := res.(timesheet.ApprovalReceipt)
	if !ok {
		return actDoneEvent{err: fmt.Errorf("tui: unexpected approve_plan payload")}
	}
	return actDoneEvent{notice: fmt.Sprintf("approved: approval_id %s for plan %s (%d operation(s))",
		receipt.ApprovalID, receipt.PlanID, receipt.Operations)}
}

// submitOp emits submit_plan exactly once behind the gate; there is no retry,
// and an unknown outcome stays a durable unresolved write.
func (e tuiEnv) submitOp(approvalID string) any {
	res, err := e.h.call("submit_plan", timesheet.Request{Day: e.day, ApprovalID: approvalID})
	if err != nil {
		return actDoneEvent{err: err}
	}
	receipt, ok := res.(timesheet.SubmissionReceipt)
	if !ok {
		return actDoneEvent{err: fmt.Errorf("tui: unexpected submit_plan payload")}
	}
	return actDoneEvent{notice: fmt.Sprintf("submission status: %s (confirmed %d, unresolved %d, unattempted %d)",
		receipt.Status, receipt.Confirmed, receipt.Unresolved, receipt.Unattempted)}
}

// refreshAfterStale re-runs review and re-derives, reopening the bench form
// fresh when the loop still asks for the same judgment.
func (e tuiEnv) refreshAfterStale(reopen string) any {
	if _, err := e.h.call("review", timesheet.Request{Day: e.day}); err != nil {
		return actDoneEvent{err: err}
	}
	te, ok := e.tick().(tickEvent)
	if !ok {
		return actDoneEvent{err: fmt.Errorf("tui: the re-derivation produced no state")}
	}
	te.reopen = reopen
	return te
}

// sendClose sends the close op and lets the program stop; unsaved state and
// bench text die with the process.
func (e tuiEnv) sendClose() any {
	_, err := e.h.call("close", timesheet.Request{})
	return closeSentEvent{err}
}

// openEditor pre-fills the bench form with the exact document the judgment's
// protocol names, the digest copied verbatim into its first field. Block ids
// come from the draft artifact; the digest binding stays the only validator.
func (e tuiEnv) openEditor(kind, digest, answerFile string, blocks []nextBlock) any {
	var body any
	switch kind {
	case judgmentBlockDescriptions:
		dir, err := dayArtifactsDir(e.cfg.StatePath, e.day)
		if err != nil {
			return editorOpenedEvent{err: err}
		}
		data, err := os.ReadFile(filepath.Join(dir, "draft.json"))
		if err != nil {
			return editorOpenedEvent{err: fmt.Errorf("tui: the draft file cannot be read")}
		}
		var draft timesheet.Draft
		if err := json.Unmarshal(data, &draft); err != nil {
			return editorOpenedEvent{err: fmt.Errorf("tui: the draft file is not valid JSON for the expected draft schema")}
		}
		descriptions := map[string]string{}
		for _, b := range draft.Blocks {
			descriptions[b.BlockID] = ""
		}
		body = map[string]any{"draft_digest": digest, "notes": "", "descriptions": descriptions}
	case judgmentClassifications:
		selections := make(map[string]timesheet.ClassificationChoiceEntry, len(blocks))
		for _, b := range blocks {
			selections[b.IntervalID] = timesheet.ClassificationChoiceEntry{}
		}
		body = map[string]any{"ledger_digest": digest, "selections": selections}
	default:
		return editorOpenedEvent{err: fmt.Errorf("tui: the judgment kind offers no authoring form")}
	}
	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		return editorOpenedEvent{err: err}
	}
	return editorOpenedEvent{kind: kind, digest: digest, answerFile: answerFile, text: string(data) + "\n"}
}

// writeBench writes the loop's own answer file exactly as the operator
// authored it: no digest is recomputed, no second validation runs; the next
// tick and the verbs' fail-closed replays stay the only validators.
func (e tuiEnv) writeBench(kind string, bench benchState) any {
	text := bench.text
	if strings.TrimSpace(text) == "" {
		return actDoneEvent{err: fmt.Errorf("tui: the answer document is empty")}
	}
	if bench.digest != "" && !strings.Contains(text, bench.digest) {
		return actDoneEvent{err: fmt.Errorf("tui: the answer document lost the judgment's digest; abandon the form and open it fresh")}
	}
	if err := os.WriteFile(bench.answerFile, []byte(text), 0o600); err != nil {
		return actDoneEvent{err: fmt.Errorf("tui: the answer file cannot be written")}
	}
	return actDoneEvent{}
}
