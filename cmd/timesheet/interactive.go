package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/run"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/selection"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// errAborted reports that the user cancelled, hit EOF, or a TUI form errored
// before a deliberate answer. NOTHING is applied on abort.
var errAborted = errors.New("interaction aborted")

// interactionMode selects how the guided `run` interacts with the user.
type interactionMode int

const (
	// modeScripted is the existing, non-interactive behavior.
	modeScripted interactionMode = iota
	// modePlain is the safe line-based interaction (works on a pipe; EOF aborts).
	modePlain
	// modeTUI is the huh terminal UI, only when both descriptors are terminals.
	modeTUI
)

// guidedRequest is the resolved flag + environment state for mode selection.
type guidedRequest struct {
	Interactive    bool
	Plain          bool
	NonInteractive bool
	Decisions      bool
	StdinTTY       bool
	StdoutTTY      bool
	CI             bool
}

// resolveMode picks the interaction mode. Precedence: an explicit decisions file
// or a non-interactive flag forces the scripted path; --plain forces the safe
// plain path; --interactive needs a terminal on BOTH stdin and stdout and a
// non-CI environment, else it REFUSES (never a silent fallback); the default is
// scripted, so existing headless behavior and exit codes are unchanged.
func resolveMode(req guidedRequest) (interactionMode, error) {
	if req.Decisions || req.NonInteractive {
		return modeScripted, nil
	}
	if req.Plain {
		return modePlain, nil
	}
	if req.Interactive {
		if req.StdinTTY && req.StdoutTTY && !req.CI {
			return modeTUI, nil
		}
		return modeScripted, errors.New("--interactive needs a terminal on both stdin and stdout; use --plain for a safe line-based session or --decisions for a scripted review")
	}
	return modeScripted, nil
}

// promptOption is one selectable choice.
type promptOption struct {
	Label string
	Hint  string
}

// prompter is the interaction seam. The TUI and plain implementations satisfy it,
// and tests inject a fake to drive the REAL CLI path without a terminal.
type prompter interface {
	Choose(ctx context.Context, title string, options []promptOption, def int) (int, error)
	MultiChoose(ctx context.Context, title string, options []promptOption) ([]int, error)
	Input(ctx context.Context, title, placeholder, initial string) (string, error)
	Confirm(ctx context.Context, title string, def bool) (bool, error)
}

// guided holds the shared state for one guided day.
type guided struct {
	r      *run.Runner
	cfg    cliConfig
	store  workflow.Store
	p      prompter
	stdout io.Writer
	stderr io.Writer
	date   string
	tz     string
	// ack holds the explicit --ack source ids, seeded into the staged decisions
	// (validated against the day's collection) instead of being prompted again.
	ack string
	// cat is the catalog loaded for the classification step, reused to validate
	// every staged classification against the shared selection contract.
	cat *gradion.Catalog
	// catalogMemo memoizes catalogs fetched during THIS run, keyed by the
	// normalized query, with the provenance line captured at fetch time so a
	// reuse prints the ORIGINAL fetch's provenance. It is in-memory only: never
	// persisted, never shared across runs, and it only ever reuses a SUCCESSFUL
	// live fetch. A memoized catalog is a per-run reuse, never remote proof, and
	// never substitutes for the submit-time live re-proof.
	catalogMemo map[string]catalogMemoEntry
	// catalogFetch is the catalog fetch seam. Production callers leave it nil so
	// loadCatalog uses the real fetchCatalog; tests inject a counting fake.
	catalogFetch func(ctx context.Context, cfg cliConfig, useCache bool, search string) (gradion.Catalog, error)
	// carryCount is the number of cross-midnight carry items visible for this day
	// (carries originating on this day plus carry blocks surfaced from the
	// previous day). It is shown in the confirmation summary.
	carryCount int
	// evByID indexes the day's collected evidence by id, so a proposed block or
	// narrative can render its SOURCE provenance (git repo/branch, calendar,
	// journal note/heading/lines) instead of only an opaque evidence id.
	evByID map[string]evidence.Evidence
}

// catalogMemoEntry is one in-run memoized catalog fetch: the catalog and the
// provenance line captured when it was fetched.
type catalogMemoEntry struct {
	cat  gradion.Catalog
	prov string
}

// cmdRunGuided is the guided `run`: preflight -> collect -> proposal -> optional
// editorial -> staged interactive review -> authoritative classification ->
// deliberate local-review confirmation -> exact preview -> export. It NEVER calls
// Approve or Submit. Collection and editorial may persist before an abort; no
// review, approval, or submission is applied until the deliberate confirmation.
func cmdRunGuided(ctx context.Context, r *run.Runner, cfg cliConfig, store workflow.Store, editorActive, deep bool, ack, date string, p prompter, stdout, stderr io.Writer) int {
	d, err := singleDate(r, date)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet:", err)
		return exitError
	}
	g := &guided{r: r, cfg: cfg, store: store, p: p, stdout: stdout, stderr: stderr, date: d, tz: tzLabel(cfg), ack: ack}

	// Preflight.
	pre, err := r.Preflight(ctx, run.PreflightOptions{Deep: deep})
	if err != nil {
		fmt.Fprintln(stderr, "timesheet: preflight:", err)
		return exitError
	}
	g.printHeader(pre)
	if !pre.OK {
		fmt.Fprintln(stderr, "timesheet: preflight refused")
		return exitBlocked
	}

	// Collect.
	collectRes, err := r.Collect(ctx, d)
	if err != nil {
		fmt.Fprintf(stderr, "timesheet: collect %s: %v\n", d, err)
		return exitError
	}
	// Read the day's record BEFORE the proposal: Propose persists nothing (a pure
	// view), so this record is still current, and the Sources section can report
	// the day's DURABLE collection state (the same state the acknowledgment step
	// uses) rather than only this pass's summary.
	rec, err := store.GetDay(ctx, d)
	if err != nil {
		fmt.Fprintf(stderr, "timesheet: record %s: %v\n", d, err)
		return exitError
	}
	g.printCollection(collectRes, rec)

	// Proposal.
	prop, err := r.Propose(ctx, d)
	if err != nil {
		fmt.Fprintf(stderr, "timesheet: propose %s: %v\n", d, err)
		return exitError
	}

	// Optional editorial.
	if editorActive {
		if _, err := r.Synthesize(ctx, d, prop); err != nil {
			fmt.Fprintf(stderr, "timesheet: synthesize %s: %v\n", d, err)
			return exitError
		}
		if prop, err = r.CurrentProposal(ctx, d); err != nil {
			fmt.Fprintf(stderr, "timesheet: proposal %s: %v\n", d, err)
			return exitError
		}
	}

	evByID := evidenceIndex(rec)
	g.evByID = evByID
	g.printOverview(prop, rec, evByID)

	// Staged interactive review.
	decisions, err := g.gatherReview(ctx, prop, rec, evByID)
	if err != nil {
		if errors.Is(err, errAborted) {
			fmt.Fprintln(stderr, "timesheet: guided run aborted; no review was applied")
			return exitError
		}
		fmt.Fprintln(stderr, "timesheet:", err)
		return exitError
	}

	// Deliberate local-review confirmation. Nothing is applied before this.
	ok, err := g.confirmSummary(ctx, decisions)
	if err != nil {
		fmt.Fprintln(stderr, "timesheet: guided run aborted; no review was applied")
		return exitError
	}
	if !ok {
		fmt.Fprintln(stderr, "timesheet: guided run not confirmed; no review was applied")
		return exitError
	}

	if err := r.ApplyReview(ctx, d, decisions); err != nil {
		fmt.Fprintf(stderr, "timesheet: review %s: %v\n", d, err)
		return exitError
	}
	fmt.Fprintf(stdout, "%s: review applied\n", d)

	// Exact preview (zero remote writes) + export.
	planRes, err := r.Preview(ctx, d, classificationResolver(cfg))
	if err != nil {
		fmt.Fprintf(stderr, "timesheet: preview %s: %v\n", d, err)
		return exitError
	}
	g.printPlan(planRes)

	exp, err := r.Export(ctx, d)
	if err != nil {
		fmt.Fprintf(stderr, "timesheet: export %s: %v\n", d, err)
		return exitError
	}
	if exp.Written {
		fmt.Fprintf(stdout, "%s: exported %s (%s)\n", d, exp.Path, exp.Status)
	}

	if planRes.Blocked {
		fmt.Fprintf(stdout, "preview blocked: %s\n", strings.Join(planRes.BlockReasons, ", "))
		return exitBlocked
	}
	fmt.Fprintf(stdout, "next step: timesheet approve --plan %s\n", planRes.Plan.PlanID)
	fmt.Fprintln(stdout, "note: submission is a separate, explicit step; this guided run never approves or submits.")
	return exitOK
}

// --- staged review ----------------------------------------------------------

func (g *guided) gatherReview(ctx context.Context, prop proposal.Proposal, rec *workflow.Record, evByID map[string]evidence.Evidence) (run.ReviewDecisions, error) {
	var dec run.ReviewDecisions
	g.carryCount = countCarries(prop)

	// Explicit --ack ids are STAGED here (validated against the day's collection)
	// and applied only on the final deliberate confirmation, so an abort leaves no
	// acknowledgment behind. Already-acknowledged sources are not prompted again.
	g.seedAcks(rec, &dec)
	if err := g.reviewIncompleteSources(ctx, rec, &dec); err != nil {
		return dec, err
	}

	existing := existingConfirmed(rec)
	if len(existing) > 0 {
		g.section("Existing confirmed work")
		for _, ci := range existing {
			fmt.Fprintf(g.stdout, "  %-9s %s  %s-%s  %s  %s\n",
				"existing", shortID(ci.ID), ci.StartTime, ci.EndTime, orDash(ci.Project), oneLine(ci.Description))
		}
		// An optional whole-day reset. When chosen, the per-interval prompts are
		// skipped: the reset clears the WHOLE local review decision set, so
		// nothing staged for an individual interval could survive it.
		reset, err := g.p.Confirm(ctx, "Reset this day's local review?", false)
		if err != nil {
			return dec, err
		}
		if reset {
			dec.Reset = true
		} else {
			// A fragment is NON-removable when it, or ANY member of its split
			// group, has a remote mapping (a split group is removed as a whole).
			// stagedGroups records a group already staged for removal, so an
			// interactive pass stages at most ONE removal per group: apply removes
			// the whole group on the first id and a second id would fail the apply
			// with unconfirm_unknown_interval.
			nonRemovable := nonRemovableIntervals(rec)
			stagedGroups := map[string]bool{}
			for _, ci := range existing {
				if err := g.reviewExisting(ctx, ci, nonRemovable[ci.ID], stagedGroups, &dec); err != nil {
					return dec, err
				}
			}
		}
	}

	// Proposed work + manual blocks first, so a colliding meeting can join a
	// block the user already confirmed.
	for _, b := range append(append([]proposal.ProposedTimeBlock{}, prop.Work...), prop.Manual...) {
		if err := g.reviewBlock(ctx, b, prop, &dec, false); err != nil {
			return dec, err
		}
	}
	for _, b := range prop.Meetings {
		if err := g.reviewBlock(ctx, b, prop, &dec, true); err != nil {
			return dec, err
		}
	}

	if err := g.reviewNarratives(ctx, prop, evByID, &dec); err != nil {
		return dec, err
	}

	// Missing-work question, asked ONCE per day, ALWAYS.
	missing, err := g.p.Confirm(ctx, "Was any work missing from the list above?", false)
	if err != nil {
		return dec, err
	}
	if missing {
		if err := g.addManualWork(ctx, prop, evByID, &dec); err != nil {
			return dec, err
		}
	}

	// Optional bulk actions over an exact, mutually-exclusive scope. Staged only,
	// ADD-IF-ABSENT (a per-block decision always wins), with an exact affected-block
	// preview and explicit consent. Nothing is written until the final atomic apply.
	if err := g.bulkReview(ctx, prop, rec, &dec); err != nil {
		return dec, err
	}

	// Authoritative classification/activity selection (gathered BEFORE the
	// deliberate confirmation, so it is applied in the SAME atomic review).
	if err := g.classificationStep(ctx, prop, rec, &dec); err != nil {
		return dec, err
	}
	// Re-validate every staged classification against the shared selection
	// contract, so a bogus id/activity-type pair can never reach the record.
	if err := g.validateClassifications(&dec); err != nil {
		return dec, err
	}

	// A day is reviewed-empty only when nothing remains confirmed after the
	// staged removals, nothing is staged to confirm, and the day is not being
	// reset. Removing ONE fragment of a split group removes the WHOLE group, so
	// the remaining set is computed from the ids AND groups the removals cover.
	// A day EMPTIED by removals/reset is therefore recorded honestly instead of
	// being left half-applied (confirmed=0 but not reviewed).
	if remainingConfirmedAfterRemovals(existing, dec.Unconfirm) == 0 &&
		len(dec.Confirm) == 0 && !hasManualConfirm(dec) && !dec.Reset {
		dec.MarkEmpty = true
	}
	return dec, nil
}

// remainingConfirmedAfterRemovals counts the existing confirmed intervals that
// would SURVIVE the staged removals. An interval is removed when its id is
// staged, OR when any member of its non-empty split group is staged (a split
// group is removed as a whole).
func remainingConfirmedAfterRemovals(existing []workflow.ConfirmedInterval, unconfirm []run.UnconfirmDecision) int {
	staged := map[string]bool{}
	for _, u := range unconfirm {
		staged[u.BlockID] = true
	}
	stagedGroups := map[string]bool{}
	for _, ci := range existing {
		if staged[ci.ID] {
			if g := strings.TrimSpace(ci.SplitGroupID); g != "" {
				stagedGroups[g] = true
			}
		}
	}
	remaining := 0
	for _, ci := range existing {
		if staged[ci.ID] {
			continue
		}
		if g := strings.TrimSpace(ci.SplitGroupID); g != "" && stagedGroups[g] {
			continue
		}
		remaining++
	}
	return remaining
}

// mappedLocalIDs indexes the confirmed-interval ids that have a remote mapping,
// so the guided review can teach the edit-instead-of-remove rule for a submitted
// interval.
func mappedLocalIDs(rec *workflow.Record) map[string]bool {
	out := map[string]bool{}
	if rec == nil {
		return out
	}
	for _, m := range rec.RemoteMappings {
		out[m.LocalID] = true
	}
	return out
}

// nonRemovableIntervals returns the set of confirmed-interval ids the guided
// review must present as NON-removable: an interval that itself has a remote
// mapping, or whose non-empty split group has a member with a remote mapping. A
// split group is removed as a whole, so a mapped sibling blocks the whole group
// and the guided path must tell the truth about it.
func nonRemovableIntervals(rec *workflow.Record) map[string]bool {
	out := map[string]bool{}
	if rec == nil {
		return out
	}
	mappedLocal := mappedLocalIDs(rec)
	mappedGroups := map[string]bool{}
	for _, ci := range rec.Confirmed {
		if !mappedLocal[ci.ID] {
			continue
		}
		if g := strings.TrimSpace(ci.SplitGroupID); g != "" {
			mappedGroups[g] = true
		}
	}
	for _, ci := range rec.Confirmed {
		if mappedLocal[ci.ID] {
			out[ci.ID] = true
			continue
		}
		if g := strings.TrimSpace(ci.SplitGroupID); g != "" && mappedGroups[g] {
			out[ci.ID] = true
		}
	}
	return out
}

func (g *guided) reviewExisting(ctx context.Context, ci workflow.ConfirmedInterval, nonRemovable bool, stagedGroups map[string]bool, dec *run.ReviewDecisions) error {
	title := fmt.Sprintf("Existing %s  %s-%s  %s  %s", shortID(ci.ID), ci.StartTime, ci.EndTime, orDash(ci.Project), oneLine(ci.Description))
	opts := []promptOption{
		{Label: "Keep as is"},
		{Label: editOptionLabel(ci.Project)},
	}
	group := strings.TrimSpace(ci.SplitGroupID)
	alreadyStaged := group != "" && stagedGroups[group]
	switch {
	case nonRemovable:
		// A submitted interval cannot be removed locally: the remote entry is
		// edited, never deleted. This option stages NOTHING and teaches the rule.
		opts = append(opts, promptOption{Label: "Submitted - edit the remote entry instead; it cannot be removed"})
	case alreadyStaged:
		// Another fragment of the SAME split group is already staged for removal;
		// the whole group is removed as one, so this fragment offers no second
		// removal.
		opts = append(opts, promptOption{Label: "Removed with the rest of this split group"})
	default:
		opts = append(opts, promptOption{Label: "Remove from this day (unconfirm)"})
	}
	idx, err := g.p.Choose(ctx, title, opts, 0)
	if err != nil {
		return err
	}
	switch idx {
	case 0:
		return nil
	case 1:
		return g.editConfirmed(ctx, ci.ID, ci.StartTime, ci.EndTime, ci.Project, ci.Description, dec)
	default:
		if nonRemovable || alreadyStaged {
			return nil
		}
		dec.Unconfirm = append(dec.Unconfirm, run.UnconfirmDecision{BlockID: ci.ID, Reason: "user_retracted"})
		if group != "" {
			stagedGroups[group] = true
		}
		return nil
	}
}

// seedAcks stages the explicit --ack source ids, validating each against the
// day's collection. An id that is not a source of this collection is reported and
// skipped (never acknowledged blindly).
func (g *guided) seedAcks(rec *workflow.Record, dec *run.ReviewDecisions) {
	ids := splitCSV(g.ack)
	if len(ids) == 0 {
		return
	}
	known := map[string]bool{}
	if rec.Collection != nil {
		for _, sr := range rec.Collection.Sources {
			known[sr.SourceID] = true
		}
	}
	for _, id := range ids {
		if !known[id] {
			fmt.Fprintf(g.stderr, "timesheet: --ack %s is not a source of this day's collection\n", id)
			continue
		}
		dec.Acknowledge = append(dec.Acknowledge, id)
	}
}

// pendingIncompleteSources returns the collection's sources that are neither
// complete nor skipped and are not yet acknowledged, either durably (rec.Review)
// or by a staged decision. It is the ONE source of truth for both the Sources
// summary line and the acknowledgment step, so the two never disagree.
func pendingIncompleteSources(rec *workflow.Record, stagedAcks []string) []evidence.SourceResult {
	if rec == nil || rec.Collection == nil || rec.Collection.Complete {
		return nil
	}
	ack := map[string]bool{}
	if rec.Review != nil {
		for _, s := range rec.Review.AcknowledgedSources {
			ack[s] = true
		}
	}
	for _, s := range stagedAcks {
		ack[s] = true
	}
	var pending []evidence.SourceResult
	for _, sr := range rec.Collection.Sources {
		if sr.Status == evidence.StatusComplete || sr.Status == evidence.StatusSkipped || ack[sr.SourceID] {
			continue
		}
		pending = append(pending, sr)
	}
	return pending
}

// reviewIncompleteSources surfaces every incomplete source that is not yet
// acknowledged and offers to acknowledge it, so the day can proceed without
// silently presenting a partial collection as complete.
func (g *guided) reviewIncompleteSources(ctx context.Context, rec *workflow.Record, dec *run.ReviewDecisions) error {
	pending := pendingIncompleteSources(rec, dec.Acknowledge)
	if len(pending) == 0 {
		return nil
	}
	g.section("Incomplete sources")
	fmt.Fprintf(g.stdout, "  %d source(s) did not complete. The preview is BLOCKED until each is acknowledged.\n", len(pending))
	for _, sr := range pending {
		fmt.Fprintf(g.stdout, "  ! %s (%s): %s\n", sr.SourceID, sourceKindLabel(sr.Type), incompleteDetail(sr))
	}
	ok, err := g.p.Confirm(ctx, "Acknowledge these incomplete sources?", false)
	if err != nil {
		return err
	}
	if ok {
		for _, sr := range pending {
			dec.Acknowledge = append(dec.Acknowledge, sr.SourceID)
		}
	}
	return nil
}

func (g *guided) reviewBlock(ctx context.Context, b proposal.ProposedTimeBlock, prop proposal.Proposal, dec *run.ReviewDecisions, isMeeting bool) error {
	opts := []promptOption{
		{Label: "Confirm as reviewed work"},
		{Label: "Exclude from work"},
		{Label: editOptionLabel(b.Project.Project)},
		{Label: "Leave unconfirmed"},
	}
	var collides []proposal.ProposedTimeBlock
	if isMeeting {
		collides = collidingWork(b, collisionCandidates(prop))
		if len(collides) > 0 && hasConfirm(*dec, collides[0].ID) {
			opts = append(opts, promptOption{Label: "Attended, joined with the overlapping interval (counts once)"})
		}
	}
	idx, err := g.p.Choose(ctx, g.blockTitle(b), opts, 0)
	if err != nil {
		return err
	}
	switch idx {
	case 0:
		if isMeeting {
			attended, err := g.meetingAttendance(ctx, b)
			if err != nil {
				return err
			}
			if !attended {
				dec.Exclude = append(dec.Exclude, run.ExcludeDecision{EvidenceID: firstEvidence(b), Reason: "not_attended"})
				return nil
			}
			att := true
			addConfirm(dec, run.ConfirmDecision{BlockID: b.ID, Attendance: &att})
			return nil
		}
		addConfirm(dec, run.ConfirmDecision{BlockID: b.ID})
	case 1:
		dec.Exclude = append(dec.Exclude, run.ExcludeDecision{EvidenceID: firstEvidence(b)})
	case 2:
		if isMeeting {
			attended, err := g.meetingAttendance(ctx, b)
			if err != nil {
				return err
			}
			if !attended {
				dec.Exclude = append(dec.Exclude, run.ExcludeDecision{EvidenceID: firstEvidence(b), Reason: "not_attended"})
				return nil
			}
			att := true
			addConfirm(dec, run.ConfirmDecision{BlockID: b.ID, Attendance: &att})
		} else {
			addConfirm(dec, run.ConfirmDecision{BlockID: b.ID})
		}
		if err := g.editProposed(ctx, b, dec); err != nil {
			return err
		}
	case 3:
		// leave unconfirmed
	case 4:
		work := collides[0]
		dec.Join = append(dec.Join, run.JoinDecision{WorkBlockID: work.ID, MeetingEvID: firstEvidence(b)})
	}
	return nil
}

// meetingAttendance resolves a meeting's attendance. When the invitation was
// ACCEPTED (b.Preselected) attendance is treated as attended and the value is
// STATED, with NO prompt: an accepted invitation is the documented default, not a
// genuine unknown. Only an invitation that is NOT accepted is asked, because only
// then is attendance genuinely unknown. It returns attended=false only when the
// owner answers that they did not attend (never for an accepted invitation).
func (g *guided) meetingAttendance(ctx context.Context, b proposal.ProposedTimeBlock) (bool, error) {
	if b.Preselected {
		fmt.Fprintf(g.stdout, "  attendance: attended (invitation accepted; stated default, not prompted)\n")
		return true, nil
	}
	return g.p.Confirm(ctx, "Did you attend this meeting?", b.Preselected)
}

// editOptionLabel names the edit option HONESTLY: the project is editable only
// while it is still UNSET (a set project comes from the configured mapping and is
// kept as-is), so the label names the project only then. The interface stays
// complete: the option still edits times and description in both cases.
func editOptionLabel(project string) string {
	if strings.TrimSpace(project) == "" {
		return "Edit times / project / description"
	}
	return "Edit times / description"
}

// editProposed stages time/project/description edits for a proposed block that is
// being confirmed. An invalid time is re-prompted with a clear error, never
// silently dropped.
func (g *guided) editProposed(ctx context.Context, b proposal.ProposedTimeBlock, dec *run.ReviewDecisions) error {
	start, end, err := g.promptInterval(ctx, b.Start, b.End)
	if err != nil {
		return err
	}
	project, err := g.promptProject(ctx, b.Project.Project)
	if err != nil {
		return err
	}
	desc, err := g.p.Input(ctx, "Description", "blank keeps current", b.Description)
	if err != nil {
		return err
	}
	if p := strings.TrimSpace(project); p != "" && p != b.Project.Project {
		replaceConfirmProject(dec, b.ID, p)
	}
	if start != b.Start || end != b.End {
		dec.Trim = append(dec.Trim, run.TrimDecision{BlockID: b.ID, NewStart: start, NewEnd: end})
	}
	if desc != b.Description {
		dec.Description = append(dec.Description, run.DescriptionDecision{BlockID: b.ID, Text: desc})
	}
	return nil
}

// editConfirmed stages edits for an EXISTING confirmed interval, keyed by its
// confirmed id (never re-proposed as new). An invalid time is re-prompted.
func (g *guided) editConfirmed(ctx context.Context, id, curStart, curEnd, curProject, curDesc string, dec *run.ReviewDecisions) error {
	start, end, err := g.promptInterval(ctx, curStart, curEnd)
	if err != nil {
		return err
	}
	project, err := g.promptProject(ctx, curProject)
	if err != nil {
		return err
	}
	desc, err := g.p.Input(ctx, "Description", "blank keeps current", curDesc)
	if err != nil {
		return err
	}
	if start != curStart || end != curEnd {
		dec.Trim = append(dec.Trim, run.TrimDecision{BlockID: id, NewStart: start, NewEnd: end})
	}
	if project != curProject {
		dec.Project = append(dec.Project, run.ProjectDecision{BlockID: id, Project: project})
	}
	if desc != curDesc {
		dec.Description = append(dec.Description, run.DescriptionDecision{BlockID: id, Text: desc})
	}
	return nil
}

// promptProject resolves a block's project. A project that is ALREADY SET (from
// the configured mapping) is the stated default: it is kept as-is and NOT
// prompted, because asking for a value the mapping already supplies is not a
// genuine unknown. Only a genuinely UNSET (empty) project is asked.
func (g *guided) promptProject(ctx context.Context, current string) (string, error) {
	if p := strings.TrimSpace(current); p != "" {
		fmt.Fprintf(g.stdout, "  project: %s (already set; kept as-is, not prompted)\n", p)
		return p, nil
	}
	v, err := g.p.Input(ctx, "Project", "blank keeps (none)", "")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(v), nil
}

// promptInterval asks for a start/end pair, re-prompting with a clear error on an
// invalid HH:MM or an end before the start. A blank answer keeps the current
// value; it never silently keeps the old times when an invalid edit was given.
func (g *guided) promptInterval(ctx context.Context, curStart, curEnd string) (string, string, error) {
	for {
		start, err := g.promptClock(ctx, "Start (HH:MM)", "blank keeps "+curStart, curStart)
		if err != nil {
			return "", "", err
		}
		end, err := g.promptClock(ctx, "End (HH:MM)", "blank keeps "+curEnd, curEnd)
		if err != nil {
			return "", "", err
		}
		if hhmmLess(end, start) {
			fmt.Fprintf(g.stdout, "end %s must not precede start %s; enter the times again\n", end, start)
			continue
		}
		return start, end, nil
	}
}

// promptClock asks for one HH:MM value, re-prompting on an invalid value.
func (g *guided) promptClock(ctx context.Context, title, placeholder, initial string) (string, error) {
	for {
		v, err := g.p.Input(ctx, title, placeholder, initial)
		if err != nil {
			return "", err
		}
		v = strings.TrimSpace(v)
		if v == "" {
			return initial, nil
		}
		if !validHHMM(v) {
			fmt.Fprintf(g.stdout, "invalid time %q; enter HH:MM or blank to keep %s\n", v, initial)
			continue
		}
		return v, nil
	}
}

// hhmmLess reports whether "HH:MM" a is strictly before b.
func hhmmLess(a, b string) bool {
	am, ok1 := clockMinutes(a)
	bm, ok2 := clockMinutes(b)
	if !ok1 || !ok2 {
		return false
	}
	return am < bm
}

func clockMinutes(v string) (int, bool) {
	if !validHHMM(v) {
		return 0, false
	}
	h, _ := digitPair(v[0], v[1])
	m, _ := digitPair(v[3], v[4])
	return h*60 + m, true
}

func (g *guided) reviewNarratives(ctx context.Context, prop proposal.Proposal, evByID map[string]evidence.Evidence, dec *run.ReviewDecisions) error {
	if len(prop.Narratives) == 0 {
		return nil
	}
	g.section("Journal narratives")
	fmt.Fprintf(g.stdout, "  Untimed context only (%s): a narrative never establishes working time by itself.\n", unknownDuration)
	for _, n := range prop.Narratives {
		fmt.Fprintf(g.stdout, "  %s\n", narrativeProvenance(n, evByID))
		fmt.Fprintf(g.stdout, "    %s\n", oneLine(n.Description))
	}
	for _, n := range prop.Narratives {
		idx, err := g.p.Choose(ctx,
			fmt.Sprintf("Journal narrative · %s · %s · %s", orDash(n.Section), oneLine(n.Description), unknownDuration),
			[]promptOption{
				{Label: "Skip (context only, no time)"},
				{Label: "Exclude from this day"},
			}, 0)
		if err != nil {
			return err
		}
		if idx == 1 {
			dec.Exclude = append(dec.Exclude, run.ExcludeDecision{EvidenceID: n.ID, Reason: "user_excluded"})
		}
	}
	return nil
}

// addManualWork adds explicitly user-timed work. The interval is CONFIRMED in the
// same review (Confirm:true) so it needs no second pass; linked sources add no
// minutes.
func (g *guided) addManualWork(ctx context.Context, prop proposal.Proposal, evByID map[string]evidence.Evidence, dec *run.ReviewDecisions) error {
	for {
		startRaw, err := g.p.Input(ctx, "New interval start (HH:MM)", "blank when finished", "")
		if err != nil {
			return err
		}
		if strings.TrimSpace(startRaw) == "" {
			return nil
		}
		endRaw, err := g.p.Input(ctx, "New interval end (HH:MM)", "", "")
		if err != nil {
			return err
		}
		start, ok1 := g.parseClock(startRaw)
		end, ok2 := g.parseClock(endRaw)
		if !ok1 || !ok2 || !end.After(start) {
			fmt.Fprintln(g.stdout, "invalid interval; start and end must be HH:MM and end after start")
			continue
		}
		project, err := g.p.Input(ctx, "Project", "optional", "")
		if err != nil {
			return err
		}
		desc, err := g.p.Input(ctx, "Description", "what did you do?", "")
		if err != nil {
			return err
		}
		links, err := g.chooseLinks(ctx, prop, evByID)
		if err != nil {
			return err
		}
		dec.Manual = append(dec.Manual, run.ManualDecision{
			UUID: newManualUUID(), Start: start, End: end,
			Project: strings.TrimSpace(project), Description: strings.TrimSpace(desc),
			EvidenceIDs: links, Confirm: true,
		})
		again, err := g.p.Confirm(ctx, "Add another interval?", false)
		if err != nil {
			return err
		}
		if !again {
			return nil
		}
	}
}

// chooseLinks lets the user attach several supporting sources to ONE user-timed
// interval. Linked sources add no minutes.
func (g *guided) chooseLinks(ctx context.Context, prop proposal.Proposal, evByID map[string]evidence.Evidence) ([]string, error) {
	var opts []promptOption
	var ids []string
	for _, n := range prop.Narratives {
		opts = append(opts, promptOption{Label: "journal: " + oneLine(n.Description), Hint: narrativeHint(n, evByID)})
		ids = append(ids, n.ID)
	}
	for _, b := range append(append([]proposal.ProposedTimeBlock{}, prop.Work...), prop.Meetings...) {
		if id := firstEvidence(b); id != "" {
			opts = append(opts, promptOption{Label: "evidence: " + oneLine(b.Description), Hint: b.Start + "-" + b.End})
			ids = append(ids, id)
		}
	}
	if len(opts) == 0 {
		return nil, nil
	}
	sel, err := g.p.MultiChoose(ctx, "Link supporting sources to this interval (they add no minutes):", opts)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, i := range sel {
		if i >= 0 && i < len(ids) && ids[i] != "" {
			out = append(out, ids[i])
		}
	}
	return out, nil
}

// --- bulk actions -----------------------------------------------------------
//
// Bulk actions are an OPTIONAL step that stages a scoped decision onto the SAME
// staged run.ReviewDecisions the per-block review builds, so nothing is written
// until the single atomic ApplyReview and an abort applies nothing. Exactly one
// mutually-exclusive scope applies (all | by kind | by project | explicit ids).
// Bulk never silently resolves a collision, never bulk-confirms an unknown
// duration, a carryover, or an unaccepted block, and never marks a day empty.
// Staging is ADD-IF-ABSENT, so a per-block decision ALWAYS wins regardless of
// order.

// bulkScopeMode selects how a bulk action's affected block set is chosen. Exactly
// one mode applies per action; the scopes are mutually exclusive.
type bulkScopeMode int

const (
	bulkScopeAll bulkScopeMode = iota
	bulkScopeByKind
	bulkScopeByProject
	bulkScopeByIDs
)

// bulkScope is one mutually-exclusive scope: every block, one block kind, one
// project, or an explicit block-id list.
type bulkScope struct {
	mode    bulkScopeMode
	kind    proposal.BlockKind
	project string
	ids     []string
}

// bulkActionKind is a staged bulk action. The set is limited to the owner-confirmed
// actions.
type bulkActionKind int

const (
	bulkActionConfirm bulkActionKind = iota
	bulkActionExclude
	bulkActionAssignProject
	bulkActionAssignClassification
)

// bulkAction is one bulk action with its parameter (the project for an assign, the
// chosen catalog option for a classification).
type bulkAction struct {
	kind     bulkActionKind
	project  string
	classOpt gradion.ClassificationOption
}

// bulkDisposition is how one in-scope block is treated by a planned action.
type bulkDisposition int

const (
	// bulkApply means the action stages a decision for this block.
	bulkApply bulkDisposition = iota
	// bulkSkipUnknown means the duration is not known: never bulk-confirmed.
	bulkSkipUnknown
	// bulkSkipCollision means the block overlaps an already-confirmed (or
	// per-block-confirmed, or in-scope applied) work/manual interval: it requires
	// an explicit per-block collision decision.
	bulkSkipCollision
	// bulkSkipExplicit means the block requires an explicit confirmation
	// (carryover, or a block that is not preselected).
	bulkSkipExplicit
	// bulkSkipNoEvidence means there is no evidence id to exclude.
	bulkSkipNoEvidence
)

// bulkPlan is the pure preview of one bulk action: the in-scope candidates, each
// one's disposition, and the affected (applied) blocks.
type bulkPlan struct {
	candidates []proposal.ProposedTimeBlock
	disp       map[string]bulkDisposition
	applied    []proposal.ProposedTimeBlock
}

// bulkReview is the optional "Bulk actions" step. It offers the owner-confirmed
// actions over an exact scope, prints the EXACT affected-block list with each
// block's per-block action and counts, and requires an explicit consent before
// staging anything. An empty scope stages nothing and never falls through to
// mark-empty.
func (g *guided) bulkReview(ctx context.Context, prop proposal.Proposal, rec *workflow.Record, dec *run.ReviewDecisions) error {
	idx, err := g.p.Choose(ctx, "Bulk actions (optional)", []promptOption{
		{Label: "No bulk actions"},
		{Label: "Bulk-confirm eligible blocks"},
		{Label: "Bulk-exclude blocks"},
		{Label: "Bulk-assign a project"},
		{Label: "Bulk-assign a classification"},
	}, 0)
	if err != nil {
		return err
	}
	if idx == 0 {
		return nil
	}
	if idx == 4 {
		return g.bulkClassification(ctx, prop, rec, dec)
	}
	var action bulkAction
	switch idx {
	case 1:
		action = bulkAction{kind: bulkActionConfirm}
	case 2:
		action = bulkAction{kind: bulkActionExclude}
	case 3:
		project, err := g.p.Input(ctx, "Project to assign", "required", "")
		if err != nil {
			return err
		}
		action = bulkAction{kind: bulkActionAssignProject, project: strings.TrimSpace(project)}
		if action.project == "" {
			g.section("Bulk actions")
			fmt.Fprintln(g.stdout, "  a bulk project assignment needs a project; nothing staged")
			return nil
		}
	}

	scope, err := g.chooseBulkScope(ctx, prop)
	if err != nil {
		return err
	}

	// An empty scope stages nothing and NEVER marks the day empty.
	if len(g.bulkCandidates(prop, scope)) == 0 {
		g.section("Bulk actions")
		fmt.Fprintf(g.stdout, "  action: %s · scope: %s\n", bulkActionLabel(action), bulkScopeLabel(scope))
		fmt.Fprintln(g.stdout, "  no blocks match this scope; nothing staged")
		return nil
	}

	plan := g.planBulk(prop, scope, action, rec, *dec)
	g.printBulkPreview(rec, scope, action, plan)
	if len(plan.applied) == 0 {
		fmt.Fprintln(g.stdout, "  nothing to stage for this action; nothing staged")
		return nil
	}
	ok, err := g.p.Confirm(ctx, "Stage these bulk actions? (nothing is written until the final review confirmation)", false)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(g.stdout, "  bulk action declined; nothing staged")
		return nil
	}
	stageBulk(dec, action, plan.applied)
	return nil
}

// chooseBulkScope asks for the one mutually-exclusive scope of a bulk action.
func (g *guided) chooseBulkScope(ctx context.Context, prop proposal.Proposal) (bulkScope, error) {
	idx, err := g.p.Choose(ctx, "Bulk scope (exactly one)", []promptOption{
		{Label: "All blocks"},
		{Label: "By kind"},
		{Label: "By project"},
		{Label: "Explicit block ids"},
	}, 0)
	if err != nil {
		return bulkScope{}, err
	}
	switch idx {
	case 1:
		kinds := []proposal.BlockKind{proposal.BlockWork, proposal.BlockMeeting, proposal.BlockJoint, proposal.BlockManual}
		opts := make([]promptOption, 0, len(kinds))
		for _, k := range kinds {
			opts = append(opts, promptOption{Label: blockKindLabel(k)})
		}
		kidx, err := g.p.Choose(ctx, "Block kind", opts, 0)
		if err != nil {
			return bulkScope{}, err
		}
		return bulkScope{mode: bulkScopeByKind, kind: kinds[kidx]}, nil
	case 2:
		projects := g.bulkProjects(prop)
		if len(projects) == 0 {
			// No block carries a project: an empty scope.
			return bulkScope{mode: bulkScopeByProject}, nil
		}
		opts := make([]promptOption, 0, len(projects))
		for _, p := range projects {
			opts = append(opts, promptOption{Label: p})
		}
		pidx, err := g.p.Choose(ctx, "Project", opts, 0)
		if err != nil {
			return bulkScope{}, err
		}
		return bulkScope{mode: bulkScopeByProject, project: projects[pidx]}, nil
	case 3:
		raw, err := g.p.Input(ctx, "Block ids (comma-separated)", "short or full id", "")
		if err != nil {
			return bulkScope{}, err
		}
		return bulkScope{mode: bulkScopeByIDs, ids: splitCSV(raw)}, nil
	default:
		return bulkScope{mode: bulkScopeAll}, nil
	}
}

// bulkProjects returns the distinct, sorted non-empty projects among the proposed
// blocks, for the "by project" scope.
func (g *guided) bulkProjects(prop proposal.Proposal) []string {
	seen := map[string]bool{}
	var out []string
	for _, b := range proposedBlocks(prop) {
		p := strings.TrimSpace(b.Project.Project)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// bulkCandidates returns the proposed blocks in the given scope, in a stable
// (start, id) order.
func (g *guided) bulkCandidates(prop proposal.Proposal, scope bulkScope) []proposal.ProposedTimeBlock {
	var out []proposal.ProposedTimeBlock
	for _, b := range proposedBlocks(prop) {
		if bulkScopeMatches(scope, b) {
			out = append(out, b)
		}
	}
	return out
}

// bulkScopeMatches reports whether a block is in the scope. An explicit id list
// matches a full block id or its short form.
func bulkScopeMatches(scope bulkScope, b proposal.ProposedTimeBlock) bool {
	switch scope.mode {
	case bulkScopeAll:
		return true
	case bulkScopeByKind:
		return b.Kind == scope.kind
	case bulkScopeByProject:
		p := strings.TrimSpace(scope.project)
		if p == "" {
			// No named project: the scope matches nothing (never "no project").
			return false
		}
		return strings.TrimSpace(b.Project.Project) == p
	case bulkScopeByIDs:
		for _, id := range scope.ids {
			id = strings.TrimSpace(id)
			if id != "" && (id == b.ID || id == shortID(b.ID)) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// bulkMinutesPerDay is one day in minutes, for cross-midnight clock spans.
const bulkMinutesPerDay = 24 * 60

// planBulk computes (without mutating anything) the exact affected set and the
// disposition of every in-scope block, so the preview matches what would be
// staged. rec and dec supply the already-confirmed / per-block-confirmed work an
// overlap must respect (see bulkMeetingCollision).
func (g *guided) planBulk(prop proposal.Proposal, scope bulkScope, action bulkAction, rec *workflow.Record, dec run.ReviewDecisions) bulkPlan {
	candidates := g.bulkCandidates(prop, scope)
	// In-scope work/manual blocks this action would itself confirm. Empty today
	// (only a preselected meeting is eligible), but kept so an overlap with a
	// block this action confirms is never silently resolved.
	appliedWork := map[string]bool{}
	if action.kind != bulkActionExclude {
		for _, b := range candidates {
			if b.Kind != proposal.BlockMeeting && bulkConfirmEligible(b) {
				appliedWork[b.ID] = true
			}
		}
	}
	plan := bulkPlan{candidates: candidates, disp: make(map[string]bulkDisposition, len(candidates))}
	for _, b := range candidates {
		d := bulkDispositionFor(prop, action, b, rec, dec, appliedWork)
		plan.disp[b.ID] = d
		if d == bulkApply {
			plan.applied = append(plan.applied, b)
		}
	}
	return plan
}

// bulkDispositionFor classifies one block for an action. A confirm/assign never
// touches a colliding block, an unknown duration, or a block that needs an
// explicit confirmation; an exclude needs at least one evidence id.
func bulkDispositionFor(prop proposal.Proposal, action bulkAction, b proposal.ProposedTimeBlock, rec *workflow.Record, dec run.ReviewDecisions, appliedWork map[string]bool) bulkDisposition {
	if action.kind == bulkActionExclude {
		if len(blockEvidenceIDs(b)) == 0 {
			return bulkSkipNoEvidence
		}
		return bulkApply
	}
	if bulkMeetingCollision(b, prop, rec, dec, appliedWork) {
		return bulkSkipCollision
	}
	if !b.Duration.Known {
		return bulkSkipUnknown
	}
	if !bulkConfirmEligible(b) {
		return bulkSkipExplicit
	}
	return bulkApply
}

// bulkConfirmEligible reports whether a block may be BULK-confirmed: only a block
// with a KNOWN duration that is preselected and is not a carryover qualifies.
// Unknown-duration, carryover, and non-preselected blocks each require an explicit
// per-block confirmation; a joint block (its overlap) is never bulk-confirmed.
func bulkConfirmEligible(b proposal.ProposedTimeBlock) bool {
	if b.Kind == proposal.BlockJoint {
		return false
	}
	return b.Duration.Known && !b.Carryover && b.Preselected
}

// bulkMeetingCollision reports whether a proposed meeting (or joint block) would
// overlap a work/manual interval that is, or will be, a confirmed interval:
//   - a durable confirmed interval in rec (already confirmed), or
//   - a proposal block staged as confirmed per-block (dec.Confirm), or
//   - an in-scope work/manual block APPLIED by this bulk action, or
//   - a manual interval staged as confirmed in this review (dec.Manual Confirm).
//
// Bulk never resolves such an overlap: the meeting is surfaced and requires an
// explicit per-block collision decision. An in-scope work/manual block that is
// skipped (not applied) is NOT a collision, because confirming the meeting alone
// creates no overlap.
func bulkMeetingCollision(b proposal.ProposedTimeBlock, prop proposal.Proposal, rec *workflow.Record, dec run.ReviewDecisions, appliedWork map[string]bool) bool {
	if b.Kind == proposal.BlockJoint {
		return true
	}
	if b.Kind != proposal.BlockMeeting {
		return false
	}
	if bulkOverlapsConfirmed(b, rec) {
		return true
	}
	var blocking []proposal.ProposedTimeBlock
	blocks := proposalBlockIndex(prop)
	for _, c := range dec.Confirm {
		if w, ok := blocks[c.BlockID]; ok && w.Kind != proposal.BlockMeeting {
			blocking = append(blocking, w)
		}
	}
	for _, w := range collisionCandidates(prop) {
		if appliedWork[w.ID] {
			blocking = append(blocking, w)
		}
	}
	// A manual interval staged as confirmed in THIS review (e.g. added via the
	// missing-work question, which runs BEFORE the bulk step) is materialized with
	// its absolute span, so a meeting overlapping it is never bulk-confirmed into a
	// double-count.
	for _, m := range dec.Manual {
		if !m.Confirm || m.Start.IsZero() || m.End.IsZero() {
			continue
		}
		blocking = append(blocking, proposal.ProposedTimeBlock{
			Kind: proposal.BlockManual, StartAt: m.Start, EndAt: m.End,
		})
	}
	return len(collidingWork(b, blocking)) > 0
}

// bulkOverlapsConfirmed reports whether a proposed block's clock span overlaps a
// durable confirmed work/manual interval (a confirmed meeting is not a work
// collision). A cross-midnight interval is treated as crossing midnight.
func bulkOverlapsConfirmed(b proposal.ProposedTimeBlock, rec *workflow.Record) bool {
	if rec == nil {
		return false
	}
	bs, be, ok := blockMinuteRange(b)
	if !ok {
		return false
	}
	for _, ci := range rec.Confirmed {
		if ci.Kind == string(proposal.BlockMeeting) {
			continue
		}
		cs, ok1 := clockMinutes(ci.StartTime)
		ce, ok2 := clockMinutes(ci.EndTime)
		if !ok1 || !ok2 {
			continue
		}
		if ce <= cs {
			ce += bulkMinutesPerDay
		}
		if bs < ce && cs < be {
			return true
		}
	}
	return false
}

// blockMinuteRange returns a proposed block's [start,end) clock minutes, treating
// an end at or before the start as crossing midnight.
func blockMinuteRange(b proposal.ProposedTimeBlock) (int, int, bool) {
	s, ok1 := clockMinutes(b.Start)
	e, ok2 := clockMinutes(b.End)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	if e <= s {
		e += bulkMinutesPerDay
	}
	return s, e, true
}

// blockEvidenceIDs returns a block's deduped, non-empty evidence ids.
func blockEvidenceIDs(b proposal.ProposedTimeBlock) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range b.EvidenceIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// stageBulk stages the affected blocks onto the decisions with ADD-IF-ABSENT
// semantics, so a per-block decision always wins regardless of order.
func stageBulk(dec *run.ReviewDecisions, action bulkAction, applied []proposal.ProposedTimeBlock) {
	for _, b := range applied {
		switch action.kind {
		case bulkActionConfirm:
			addConfirmAbsent(dec, run.ConfirmDecision{BlockID: b.ID})
		case bulkActionExclude:
			// Exclude EVERY evidence id of the block (deduped), so a
			// multi-evidence block is fully removed.
			for _, ev := range blockEvidenceIDs(b) {
				addExcludeAbsent(dec, run.ExcludeDecision{EvidenceID: ev, Reason: "user_excluded"})
			}
		case bulkActionAssignProject:
			addConfirmAbsent(dec, run.ConfirmDecision{BlockID: b.ID, Project: strings.TrimSpace(action.project)})
		}
	}
}

// --- bulk classification ----------------------------------------------------

// bulkClassificationPlan is the preview of a bulk classification action: the
// classifiable intervals it would set, and the ones it skips because they already
// carry a classification (or a staged choice).
type bulkClassificationPlan struct {
	applied []classTarget
	skipped []classTarget
}

// bulkClassification is the optional "Bulk-assign a classification" action. The
// user picks ONE classification through the shared catalog UI (searchable), the
// step prints the EXACT affected intervals and counts, and requires an explicit
// consent before staging. It only stages decisions on the SAME atomic review; an
// abort stages nothing, and a per-block decision always wins.
func (g *guided) bulkClassification(ctx context.Context, prop proposal.Proposal, rec *workflow.Record, dec *run.ReviewDecisions) error {
	cat, prov, err := g.loadCatalog(ctx, "")
	if err != nil {
		g.section("Bulk actions")
		fmt.Fprintf(g.stdout, "  catalog unavailable (%v); a bulk classification cannot proceed\n", err)
		return nil
	}
	g.cat = &cat
	opt, ok, err := g.chooseClassification(ctx, classTarget{label: "bulk classification"}, cat)
	if err != nil {
		return err
	}
	if !ok {
		g.section("Bulk actions")
		fmt.Fprintln(g.stdout, "  no classification chosen; nothing staged")
		return nil
	}
	plan := g.planBulkClassification(prop, rec, dec)
	g.printBulkClassificationPreview(opt, prov, plan)
	if len(plan.applied) == 0 {
		fmt.Fprintln(g.stdout, "  nothing to stage for this action; nothing staged")
		return nil
	}
	ok, err = g.p.Confirm(ctx, "Stage this bulk classification? (nothing is written until the final review confirmation)", false)
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(g.stdout, "  bulk classification declined; nothing staged")
		return nil
	}
	stageBulkClassification(dec, opt, plan.applied)
	return nil
}

// planBulkClassification computes the classifiable intervals a bulk classification
// would set: every target that does not already carry a classification and has no
// staged choice. An already-classified interval is retained (skipped), never
// overwritten.
func (g *guided) planBulkClassification(prop proposal.Proposal, rec *workflow.Record, dec *run.ReviewDecisions) bulkClassificationPlan {
	var plan bulkClassificationPlan
	for _, t := range g.classTargets(prop, rec, dec) {
		if strings.TrimSpace(t.current) != "" || (t.manual == nil && stagedClassification(*dec, t.key)) {
			plan.skipped = append(plan.skipped, t)
			continue
		}
		plan.applied = append(plan.applied, t)
	}
	return plan
}

// printBulkClassificationPreview prints the EXACT affected-interval list, the
// counts, and the catalog provenance before consent.
func (g *guided) printBulkClassificationPreview(opt gradion.ClassificationOption, prov string, plan bulkClassificationPlan) {
	g.section("Bulk actions")
	fmt.Fprintf(g.stdout, "  action: %s\n", bulkClassificationLabel(opt))
	fmt.Fprintf(g.stdout, "  catalog: %s\n", prov)
	fmt.Fprintf(g.stdout, "  scope: all classifiable intervals (%d block(s))\n", len(plan.applied)+len(plan.skipped))
	for _, t := range plan.applied {
		fmt.Fprintf(g.stdout, "  %-18s %s\n", "apply", t.label)
	}
	for _, t := range plan.skipped {
		fmt.Fprintf(g.stdout, "  %-18s %s\n", "skip (classified)", t.label)
	}
	fmt.Fprintf(g.stdout, "  %s · applied %d · skipped already-classified %d\n",
		bulkClassificationLabel(opt), len(plan.applied), len(plan.skipped))
}

// stageBulkClassification stages the chosen classification onto every applied
// target, ADD-IF-ABSENT, so a per-block classification decision always wins. The
// stored activity type is the catalog's DERIVED value (re-validated after staging).
func stageBulkClassification(dec *run.ReviewDecisions, opt gradion.ClassificationOption, applied []classTarget) {
	for _, t := range applied {
		if t.manual != nil {
			t.manual.ClassificationID = opt.ID
			t.manual.ActivityType = opt.ActivityType
			t.manual.ClassificationCode = opt.Code
			t.manual.ClassificationName = opt.Name
			continue
		}
		addClassificationAbsent(dec, run.ClassificationDecision{
			BlockID: t.key, ClassificationID: opt.ID, ActivityType: opt.ActivityType,
			ClassificationCode: opt.Code, ClassificationName: opt.Name,
		})
	}
}

// stagedClassification reports whether a block already has a staged classification
// decision (e.g. from the bulk classification action).
func stagedClassification(dec run.ReviewDecisions, blockID string) bool {
	for _, c := range dec.Classification {
		if c.BlockID == blockID {
			return true
		}
	}
	return false
}

// addClassificationAbsent stages a classification ONLY when the block has no staged
// one, so a per-block decision always wins regardless of order.
func addClassificationAbsent(dec *run.ReviewDecisions, c run.ClassificationDecision) {
	if stagedClassification(*dec, c.BlockID) {
		return
	}
	dec.Classification = append(dec.Classification, c)
}

// addClassification stages a classification, REPLACING any staged choice for the
// same block, so an explicit per-block classification always wins.
func addClassification(dec *run.ReviewDecisions, c run.ClassificationDecision) {
	for i := range dec.Classification {
		if dec.Classification[i].BlockID == c.BlockID {
			dec.Classification[i] = c
			return
		}
	}
	dec.Classification = append(dec.Classification, c)
}

// bulkClassificationLabel renders the chosen classification option as a human
// label for the bulk preview.
func bulkClassificationLabel(opt gradion.ClassificationOption) string {
	return fmt.Sprintf("assign-classification %s (%s) [%s]", opt.Name, opt.Code, opt.ActivityType)
}

// proposedBlocks returns every proposed block (work, meeting, manual) in a stable
// (start, id) order.
func proposedBlocks(prop proposal.Proposal) []proposal.ProposedTimeBlock {
	out := make([]proposal.ProposedTimeBlock, 0, len(prop.Work)+len(prop.Meetings)+len(prop.Manual))
	out = append(out, prop.Work...)
	out = append(out, prop.Meetings...)
	out = append(out, prop.Manual...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartAt.Equal(out[j].StartAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].StartAt.Before(out[j].StartAt)
	})
	return out
}

// printBulkPreview prints the EXACT affected-block list with each block's
// per-block action, the counts, and any exclude warning, before consent.
func (g *guided) printBulkPreview(rec *workflow.Record, scope bulkScope, action bulkAction, plan bulkPlan) {
	g.section("Bulk actions")
	fmt.Fprintf(g.stdout, "  action: %s\n", bulkActionLabel(action))
	fmt.Fprintf(g.stdout, "  scope: %s (%d block(s))\n", bulkScopeLabel(scope), len(plan.candidates))
	counts := map[bulkDisposition]int{}
	for _, b := range plan.candidates {
		d := plan.disp[b.ID]
		counts[d]++
		fmt.Fprintf(g.stdout, "  %-18s %s\n", bulkDispositionLabel(d), g.blockLabel(b))
	}
	fmt.Fprintf(g.stdout, "  %s %d · skipped unknown-duration %d · requires explicit collision %d\n",
		bulkActionLabel(action), counts[bulkApply], counts[bulkSkipUnknown], counts[bulkSkipCollision])
	if counts[bulkSkipExplicit] > 0 {
		fmt.Fprintf(g.stdout, "  requires an explicit confirmation %d\n", counts[bulkSkipExplicit])
	}
	if counts[bulkSkipNoEvidence] > 0 {
		fmt.Fprintf(g.stdout, "  skipped (no evidence to exclude) %d\n", counts[bulkSkipNoEvidence])
	}
	g.printBulkWarnings(rec, action, plan)
}

// printBulkWarnings surfaces, before consent, any confirmed interval that a bulk
// exclude would drop because it would lose ALL of its evidence: every evidence id
// of the interval is covered by the excluded set (and the interval is not manual).
func (g *guided) printBulkWarnings(rec *workflow.Record, action bulkAction, plan bulkPlan) {
	if action.kind != bulkActionExclude || rec == nil {
		return
	}
	excludedEv := map[string]bool{}
	for _, b := range plan.applied {
		for _, ev := range blockEvidenceIDs(b) {
			excludedEv[ev] = true
		}
	}
	for _, ci := range rec.Confirmed {
		if ci.Manual || len(ci.EvidenceIDs) == 0 {
			continue
		}
		losesAll := true
		for _, ev := range ci.EvidenceIDs {
			if !excludedEv[ev] {
				losesAll = false
				break
			}
		}
		if losesAll {
			fmt.Fprintf(g.stdout, "  ! excluding would drop confirmed interval %s (loses all evidence)\n", shortID(ci.ID))
		}
	}
}

func bulkActionLabel(a bulkAction) string {
	switch a.kind {
	case bulkActionConfirm:
		return "confirm"
	case bulkActionExclude:
		return "exclude"
	case bulkActionAssignProject:
		return "assign-project " + orDash(a.project) + " (confirms the block)"
	case bulkActionAssignClassification:
		return bulkClassificationLabel(a.classOpt)
	default:
		return "?"
	}
}

func bulkScopeLabel(scope bulkScope) string {
	switch scope.mode {
	case bulkScopeAll:
		return "all blocks"
	case bulkScopeByKind:
		return "kind " + blockKindLabel(scope.kind)
	case bulkScopeByProject:
		return "project " + orDash(scope.project)
	case bulkScopeByIDs:
		return "explicit ids"
	default:
		return "?"
	}
}

func bulkDispositionLabel(d bulkDisposition) string {
	switch d {
	case bulkApply:
		return "apply"
	case bulkSkipUnknown:
		return "skip (unknown)"
	case bulkSkipCollision:
		return "skip (collision)"
	case bulkSkipExplicit:
		return "skip (explicit)"
	case bulkSkipNoEvidence:
		return "skip (no evidence)"
	default:
		return "?"
	}
}

// addConfirmAbsent stages a confirm decision ONLY when the block has no staged
// confirm yet, so a per-block decision always wins regardless of order.
func addConfirmAbsent(dec *run.ReviewDecisions, c run.ConfirmDecision) {
	if hasConfirm(*dec, c.BlockID) {
		return
	}
	dec.Confirm = append(dec.Confirm, c)
}

// hasExclude reports whether an evidence id is already staged for exclusion.
func hasExclude(dec run.ReviewDecisions, evID string) bool {
	for _, e := range dec.Exclude {
		if e.EvidenceID == evID {
			return true
		}
	}
	return false
}

// addExcludeAbsent stages an exclude decision ONLY when that evidence id is not
// already staged as excluded.
func addExcludeAbsent(dec *run.ReviewDecisions, ex run.ExcludeDecision) {
	if hasExclude(*dec, ex.EvidenceID) {
		return
	}
	dec.Exclude = append(dec.Exclude, ex)
}

// --- classification ---------------------------------------------------------

type classTarget struct {
	key     string
	label   string
	project string
	current string
	manual  *run.ManualDecision
}

func (g *guided) classTargets(prop proposal.Proposal, rec *workflow.Record, dec *run.ReviewDecisions) []classTarget {
	var out []classTarget
	blocks := proposalBlockIndex(prop)
	// A staged reset removes every existing interval, so classifying one would
	// stage a decision against a removed block. Existing intervals are therefore
	// not classifiable when a reset is staged.
	if !dec.Reset {
		for _, ci := range existingConfirmed(rec) {
			out = append(out, classTarget{key: ci.ID, label: fmt.Sprintf("existing interval %s · %s-%s", shortID(ci.ID), ci.StartTime, ci.EndTime), project: ci.Project, current: ci.ClassificationID})
		}
	}
	for _, c := range dec.Confirm {
		b, ok := blocks[c.BlockID]
		if !ok {
			continue
		}
		proj := b.Project.Project
		if strings.TrimSpace(c.Project) != "" {
			proj = c.Project
		}
		out = append(out, classTarget{key: c.BlockID, label: g.blockLabel(b), project: proj})
	}
	for i := range dec.Manual {
		if !dec.Manual[i].Confirm {
			continue
		}
		m := &dec.Manual[i]
		out = append(out, classTarget{
			label:   fmt.Sprintf("manual interval %s-%s", m.Start.Format("15:04"), m.End.Format("15:04")),
			project: m.Project,
			current: m.ClassificationID,
			manual:  m,
		})
	}
	return out
}

func (g *guided) classificationStep(ctx context.Context, prop proposal.Proposal, rec *workflow.Record, dec *run.ReviewDecisions) error {
	targets := g.classTargets(prop, rec, dec)
	if len(targets) == 0 {
		return nil
	}
	g.section("Classification")
	cat, prov, err := g.loadCatalog(ctx, "")
	if err != nil {
		fmt.Fprintf(g.stdout, "  catalog unavailable (%v); classification stays unset and the preview will block honestly\n", err)
		return nil
	}
	// A bulk-classification search may have WIDENED g.cat beyond the unfiltered
	// first page. UNION the freshly loaded catalog with the already-set g.cat
	// rather than replacing it, so a beyond-page option staged by the bulk action
	// still re-resolves in validateClassifications (C6). When g.cat is unset
	// (no bulk action ran), it is simply the freshly loaded catalog.
	if g.cat != nil {
		*g.cat = selection.UnionCatalogs(cat, *g.cat)
		cat = *g.cat
	} else {
		g.cat = &cat
	}
	fmt.Fprintf(g.stdout, "  catalog: %s\n", prov)
	g.printUnappliedSuggestions(targets)
	retained := 0
	bulkSet := 0
	for i := range targets {
		// An interval that already carries an explicit classification is KEPT as
		// is; the guided run only gathers the choices that are still unset.
		if strings.TrimSpace(targets[i].current) != "" {
			retained++
			continue
		}
		// A block the bulk-classification action already staged is left as chosen
		// (bulk add-if-absent): it is not prompted again. A per-block decision
		// still wins — addClassification replaces a staged choice.
		if targets[i].manual == nil && stagedClassification(*dec, targets[i].key) {
			bulkSet++
			continue
		}
		opt, ok, err := g.chooseClassification(ctx, targets[i], cat)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if targets[i].manual != nil {
			targets[i].manual.ClassificationID = opt.ID
			targets[i].manual.ActivityType = opt.ActivityType
			targets[i].manual.ClassificationCode = opt.Code
			targets[i].manual.ClassificationName = opt.Name
			continue
		}
		addClassification(dec, run.ClassificationDecision{
			BlockID: targets[i].key, ClassificationID: opt.ID, ActivityType: opt.ActivityType,
			ClassificationCode: opt.Code, ClassificationName: opt.Name,
		})
	}
	if bulkSet > 0 {
		fmt.Fprintf(g.stdout, "%d interval(s) were classified by the bulk action\n", bulkSet)
	}
	if retained > 0 {
		fmt.Fprintf(g.stdout, "%d interval(s) keep their existing classification; change one with `timesheet classify select --date %s --block-id <ID> --classification <CODE|ID>`\n", retained, g.date)
	}
	return nil
}

// validateClassifications re-resolves every staged classification against the
// catalog using the shared selection contract, replacing the activity type with
// the catalog's DERIVED value and refusing an inconsistent or unresolvable choice.
func (g *guided) validateClassifications(dec *run.ReviewDecisions) error {
	if g.cat == nil {
		return nil
	}
	for i := range dec.Classification {
		opt, err := run.AuthoritativeClassificationOption(*g.cat, dec.Classification[i].ClassificationID, dec.Classification[i].ActivityType)
		if err != nil {
			return err
		}
		dec.Classification[i].ClassificationID = opt.ID
		dec.Classification[i].ActivityType = opt.ActivityType
		dec.Classification[i].ClassificationCode = opt.Code
		dec.Classification[i].ClassificationName = opt.Name
	}
	for i := range dec.Manual {
		if strings.TrimSpace(dec.Manual[i].ClassificationID) == "" {
			continue
		}
		opt, err := run.AuthoritativeClassificationOption(*g.cat, dec.Manual[i].ClassificationID, dec.Manual[i].ActivityType)
		if err != nil {
			return err
		}
		dec.Manual[i].ClassificationID = opt.ID
		dec.Manual[i].ActivityType = opt.ActivityType
		dec.Manual[i].ClassificationCode = opt.Code
		dec.Manual[i].ClassificationName = opt.Name
	}
	return nil
}

// chooseClassification resolves ONE authoritative classification choice from a
// VALIDATED catalog option. The activity type is the option's derived property,
// never free-form, and no default is ever assigned.
func (g *guided) chooseClassification(ctx context.Context, t classTarget, cat gradion.Catalog) (gradion.ClassificationOption, bool, error) {
	cur := cat
	for {
		limit := len(cur.Options)
		if limit > 12 {
			limit = 12
		}
		var opts []promptOption
		vals := make([]gradion.ClassificationOption, 0, limit)
		for i := 0; i < limit; i++ {
			o := cur.Options[i]
			opts = append(opts, promptOption{Label: fmt.Sprintf("%s (%s)", o.Name, o.Code), Hint: o.ActivityType})
			vals = append(vals, o)
		}
		searchIdx := len(opts)
		opts = append(opts, promptOption{Label: "Search the catalog"})
		typeIdx := len(opts)
		opts = append(opts, promptOption{Label: "Type a code or id"})
		skipIdx := len(opts)
		opts = append(opts, promptOption{Label: "Leave unset"})

		title := fmt.Sprintf("Classification for %s (current: %s)", t.label, orDash(t.current))
		idx, err := g.p.Choose(ctx, title, opts, skipIdx)
		if err != nil {
			return gradion.ClassificationOption{}, false, err
		}
		switch {
		case idx < len(vals):
			return vals[idx], true, nil
		case idx == searchIdx:
			q, err := g.p.Input(ctx, "Search the catalog", "substring", "")
			if err != nil {
				return gradion.ClassificationOption{}, false, err
			}
			nc, _, err := g.loadCatalog(ctx, q)
			if err != nil {
				fmt.Fprintf(g.stdout, "search failed: %v\n", err)
				continue
			}
			if len(nc.Options) == 0 {
				// A filtered/partial catalog NEVER proves absence: say the choice
				// may lie beyond the first 20 rows and offer search again, exactly
				// as the classify path does. Never assert the choice does not exist.
				fmt.Fprintln(g.stdout, selectionNotFoundMessage(q, false, false, nc))
				continue
			}
			cur = nc
			// Widen the VALIDATION catalog with the search results, so a choice
			// found beyond the unfiltered first page still re-resolves through
			// AuthoritativeClassificationOption at the end of the step — exactly
			// as `classify select` unions a targeted catalog. This never assigns
			// anything; it only keeps a legitimately retrieved option resolvable.
			if g.cat != nil {
				*g.cat = selection.UnionCatalogs(*g.cat, nc)
			}
		case idx == typeIdx:
			tok, err := g.p.Input(ctx, "Classification code or id", "", "")
			if err != nil {
				return gradion.ClassificationOption{}, false, err
			}
			o, err := selection.ResolveToken(cur, tok)
			if err != nil {
				fmt.Fprintf(g.stdout, "not resolved: %v\n", err)
				continue
			}
			return o, true, nil
		default:
			return gradion.ClassificationOption{}, false, nil
		}
	}
}

// loadCatalog fetches a live catalog (cached under the query), falling back to a
// FRESH cache entry. It reuses the classify path's fetchCatalog, so accounting is
// not duplicated. Within ONE guided run a query is fetched at most once: a repeat
// returns the memoized catalog AND the provenance captured at the original fetch,
// so the printed provenance stays stable. The memo is per-run only (in memory),
// never persisted, and only ever holds a SUCCESSFUL live fetch — a live error
// still runs the cache fallback, and the fallback result is never memoized.
func (g *guided) loadCatalog(ctx context.Context, query string) (gradion.Catalog, string, error) {
	key := strings.TrimSpace(query)
	if e, ok := g.catalogMemo[key]; ok {
		return e.cat, e.prov, nil
	}
	fetch := g.catalogFetch
	if fetch == nil {
		fetch = fetchCatalog
	}
	cat, err := fetch(ctx, g.cfg, false, query)
	if err == nil {
		prov := selectionProvenanceLine(cat, time.Now().UTC())
		if g.catalogMemo == nil {
			g.catalogMemo = make(map[string]catalogMemoEntry)
		}
		g.catalogMemo[key] = catalogMemoEntry{cat: cat, prov: prov}
		return cat, prov, nil
	}
	cachePath, perr := selection.DefaultCatalogCachePath()
	if perr == nil {
		if cached, cerr := selection.LoadCatalogCache(cachePath, query); cerr == nil {
			if prov, fresh := selection.Freshness(cached, time.Now().UTC()); fresh {
				return cached, prov, nil
			}
		}
	}
	return gradion.Catalog{}, "", err
}

// printUnappliedSuggestions shows the config-map, recent-selection, and first-run
// hints, clearly labeled as NOT applied. It mirrors printSelectionHint's
// precedence: recent selections (up to maxRecentHints) take precedence over the
// first-run suggestion, which is resolved against the FETCHED catalog (never
// fabricated). No default classification is ever assigned.
func (g *guided) printUnappliedSuggestions(targets []classTarget) {
	for _, t := range targets {
		p := strings.TrimSpace(t.project)
		if p == "" {
			continue
		}
		if id, ok := g.cfg.Classifications[p]; ok && strings.TrimSpace(id) != "" {
			fmt.Fprintf(g.stdout, "suggestion (unapplied): %s -> %s from the project map\n", t.label, id)
		}
	}
	if prefsPath, err := selection.DefaultPath(); err == nil {
		if prefs, err := selection.Load(prefsPath); err == nil {
			if recents := prefs.RecentN(maxRecentHints); len(recents) > 0 {
				for _, m := range recents {
					fmt.Fprintf(g.stdout, "suggestion (unapplied): recent selection %s (%s) [%s] · %s\n",
						m.Name, m.Code, m.ActivityType, idPrefix(m.ClassificationID))
				}
				return
			}
		}
	}
	if g.cat == nil {
		return
	}
	opt, why, ok := selection.FirstRunSuggestion(*g.cat)
	if !ok {
		fmt.Fprintf(g.stdout, "suggestion (unapplied): first-run suggestion unavailable: %s\n", why)
		return
	}
	fmt.Fprintf(g.stdout, "suggestion (unapplied): first-run %s (%s) [%s] · %s\n",
		opt.Name, opt.Code, opt.ActivityType, idPrefix(opt.ID))
}

// --- confirmation + rendering ----------------------------------------------
//
// The guided report is TEXT ONLY (no ANSI color): a piped, logged, or
// screen-reader session reads exactly the same facts, and the machine-readable
// lines (plan_id:, operations:, preview blocked:, total:, the per-operation
// lines) are never altered by presentation. Sections are titled and underlined
// so the day reads top-down. The REAL order is:
//
//	header -> sources -> proposal -> review -> summary -> plan
//
// where the "review" phase is itself ordered:
//
//	incomplete sources (acknowledged) -> existing work -> proposed blocks ->
//	journal narratives -> classification
//
// The Sources section points forward to the acknowledgment step, so an incomplete
// source is visible where it is collected and confirmed where it is acknowledged.

// guidedTitle heads the report, so the day's flow has an obvious entry point.
const guidedTitle = "timesheet · guided review"

// unknownDuration is the ONE unmistakable marker for an item whose duration is
// NOT known: an untimed journal narrative, or a git work block whose start-end is
// an observation window, never confirmed working time. It is deliberately never a
// number, so an unknown duration can never be mistaken for timed work.
const unknownDuration = "duration=unknown"

// section prints a titled section header: a blank line, the title, then an
// underline of the same width. It is text only (no color), so the structure is
// preserved in a pipe, a log, or a screen reader.
func (g *guided) section(title string) {
	fmt.Fprintf(g.stdout, "\n%s\n%s\n", title, strings.Repeat("─", utf8.RuneCountInString(title)))
}

func (g *guided) confirmSummary(ctx context.Context, dec run.ReviewDecisions) (bool, error) {
	g.section(fmt.Sprintf("Review summary (%s, %s)", g.date, g.tz))
	fmt.Fprintf(g.stdout, "  %-22s %d\n", "confirm:", len(dec.Confirm))
	fmt.Fprintf(g.stdout, "  %-22s %d\n", "exclude:", len(dec.Exclude))
	fmt.Fprintf(g.stdout, "  %-22s %d time · %d project · %d description\n", "edits:", len(dec.Trim), len(dec.Project), len(dec.Description))
	fmt.Fprintf(g.stdout, "  %-22s %d\n", "manual intervals:", len(dec.Manual))
	fmt.Fprintf(g.stdout, "  %-22s %d\n", "classification chosen:", len(dec.Classification)+manualClassified(dec))
	if len(dec.Unconfirm) > 0 {
		fmt.Fprintf(g.stdout, "  %-22s %d\n", "unconfirm:", len(dec.Unconfirm))
	}
	if dec.Reset {
		fmt.Fprintf(g.stdout, "  %-22s yes (clears the day's local review)\n", "reset review:")
	}
	if g.carryCount > 0 {
		fmt.Fprintf(g.stdout, "  %-22s %d (a carry on the next day is confirmed separately)\n", "carryover items:", g.carryCount)
	}
	if dec.MarkEmpty {
		fmt.Fprintf(g.stdout, "  %-22s reviewed with no work (no entries)\n", "result:")
	}
	return g.p.Confirm(ctx, "Apply this review and mark the day reviewed?", false)
}

func (g *guided) printHeader(pre run.PreflightResult) {
	fmt.Fprintln(g.stdout, guidedTitle)
	fmt.Fprintf(g.stdout, "date: %s  timezone: %s\n", g.date, g.tz)
	g.section("Preflight")
	for _, c := range pre.Checks {
		status := "ok"
		if !c.Passed {
			status = "FAIL"
		}
		line := fmt.Sprintf("  %-4s %s", status, c.Name)
		if c.Code != "" {
			line += " [" + c.Code + "]"
		}
		fmt.Fprintln(g.stdout, line)
	}
}

func (g *guided) printCollection(cr run.CollectResult, rec *workflow.Record) {
	g.section("Sources")
	if cr.NoSourcesConfigured {
		fmt.Fprintf(g.stdout, "%s: No evidence sources configured.\n", g.date)
	} else {
		var b strings.Builder
		printSourceStatus(&b, cr.Summary)
		switch {
		case b.Len() > 0:
			fmt.Fprint(g.stdout, b.String())
		case len(cr.Evidence) == 0 && collectionComplete(cr.Summary):
			fmt.Fprintf(g.stdout, "%s: no evidence collected.\n", g.date)
		default:
			fmt.Fprintf(g.stdout, "  all enabled sources complete (%d evidence item(s))\n", len(cr.Evidence))
		}
	}
	// Surface an incomplete-source pointer HERE, next to the sources it names, so
	// the warning is easy to find. The acknowledgment step itself stays in the
	// review phase (see reviewIncompleteSources); both read the SAME durable
	// collection state, so they never disagree.
	if pending := pendingIncompleteSources(rec, splitCSV(g.ack)); len(pending) > 0 {
		fmt.Fprintf(g.stdout, "  %d source(s) incomplete -> the preview blocks until acknowledged in the review step below\n", len(pending))
	}
	// Surface the collection-time remote signals (failed read, the day's OBSERVED
	// remote entries, and duplicate-looking collisions) in the SAME review section
	// the scripted `review` reports them, so the agent sees them before deciding.
	printRemoteObservation(g.stdout, g.date, cr)
}

func (g *guided) printOverview(prop proposal.Proposal, rec *workflow.Record, evByID map[string]evidence.Evidence) {
	blocks := len(prop.Work) + len(prop.Meetings) + len(prop.Manual)
	g.section("Proposal")
	fmt.Fprintf(g.stdout, "  %d block(s) · %d journal narrative(s) · %d excluded\n",
		blocks, len(prop.Narratives), len(prop.Excluded))
	for _, b := range prop.Work {
		fmt.Fprintf(g.stdout, "  %-9s %s\n", blockKindLabel(b.Kind), g.blockLabel(b))
	}
	for _, b := range prop.Meetings {
		fmt.Fprintf(g.stdout, "  %-9s %s\n", blockKindLabel(b.Kind), g.blockLabel(b))
	}
	for _, b := range prop.Manual {
		fmt.Fprintf(g.stdout, "  %-9s %s\n", blockKindLabel(b.Kind), g.blockLabel(b))
	}
	for _, n := range prop.Narratives {
		fmt.Fprintf(g.stdout, "  %-9s %s\n", "narrative", oneLine(n.Description))
		fmt.Fprintf(g.stdout, "  %-9s %s\n", "", narrativeProvenance(n, evByID))
	}
	for _, c := range prop.Carries {
		fmt.Fprintf(g.stdout, "  %-9s -> %s %s-%s  split-group %s\n", "carry", c.Date, c.Start, c.End, shortID(c.SplitGroupID))
	}
	for _, ex := range prop.Excluded {
		fmt.Fprintf(g.stdout, "  %-9s %s\n", "excluded", excludedLabel(ex))
	}
}

// excludedLabel renders one excluded-evidence line with its human reason and any
// journal provenance, so a detached note item is identifiable even when it has no
// heading. Title and the note's own stated date are included because they are the
// two fields that most often identify a detached item.
func excludedLabel(ex proposal.ExcludedEvidence) string {
	line := fmt.Sprintf("%s  reason=%s", shortID(ex.EvidenceID), exclusionReasonLabel(ex.Reason))
	if ex.Title != "" {
		line += "  title=" + oneLine(ex.Title)
	}
	if ex.NotePath != "" {
		line += "  note=" + oneLine(ex.NotePath)
	}
	if ex.HeadingPath != "" {
		line += "  heading=" + oneLine(ex.HeadingPath)
	}
	if ex.LineStart > 0 {
		line += fmt.Sprintf("  lines=%d-%d", ex.LineStart, ex.LineEnd)
	}
	if ex.StatedDate != "" {
		line += "  stated=" + ex.StatedDate
	}
	return line
}

func (g *guided) printPlan(res run.PlanResult) {
	g.section("Submission plan")
	if res.Blocked {
		printSuggestions(g.stdout, res.Suggestions)
		fmt.Fprintf(g.stdout, "preview blocked: %s\n", strings.Join(res.BlockReasons, ", "))
		return
	}
	fmt.Fprintf(g.stdout, "plan_id: %s\n", res.Plan.PlanID)
	fmt.Fprintf(g.stdout, "operations: %d\n", len(res.Plan.Operations))
	printSuggestions(g.stdout, res.Suggestions)
	projects := confirmedProjects(context.Background(), g.store, g.date)
	if res.RemoteObserved {
		fmt.Fprintf(g.stdout, "remote observed: %s\n", res.Plan.RemoteObservedAt.Format(time.RFC3339))
		fmt.Fprintf(g.stdout, "remote complete: %t\n", res.Plan.RemoteComplete)
	} else {
		fmt.Fprintln(g.stdout, "remote observed: unobserved (offline; no remote was contacted)")
		fmt.Fprintln(g.stdout, "remote complete: false")
	}
	total := 0
	for _, op := range res.Plan.Operations {
		fmt.Fprintln(g.stdout, formatPlanOperation(op, g.cfg, projects))
		total += minutesBetween(op.StartTime, op.EndTime)
	}
	fmt.Fprintf(g.stdout, "total: %dh %dm across %d operation(s)\n", total/60, total%60, len(res.Plan.Operations))
}

// blockTitle is the block line used as a prompt title: the human kind, then the
// structured label with its span, duration, project, markers, and provenance.
func (g *guided) blockTitle(b proposal.ProposedTimeBlock) string {
	return fmt.Sprintf("%s · %s", blockKindLabel(b.Kind), g.blockLabel(b))
}

// blockLabel renders one proposed block as a consistent, human line:
//
//	<id>  <start>-<end>  <duration|duration=unknown>  <project>  [markers]  [provenance]  <description>
//
// The duration is a number ONLY when it is known; otherwise it is the unmistakable
// unknownDuration marker, so an untimed block never reads as timed work.
func (g *guided) blockLabel(b proposal.ProposedTimeBlock) string {
	fields := []string{
		shortID(b.ID),
		b.Start + "-" + b.End,
		durationLabel(b.Duration),
		orDash(b.Project.Project),
	}
	if markers := g.blockMarkers(b); markers != "" {
		fields = append(fields, markers)
	}
	if prov := g.blockProvenance(b); prov != "" {
		fields = append(fields, prov)
	}
	line := strings.Join(fields, "  ")
	if d := strings.TrimSpace(b.Description); d != "" {
		line += "  " + oneLine(d)
	}
	return line
}

// blockMarkers renders the block's status markers: attendance (meetings only),
// a collision, a carryover, and a split-group link.
func (g *guided) blockMarkers(b proposal.ProposedTimeBlock) string {
	var m []string
	if b.Kind == proposal.BlockMeeting {
		m = append(m, attendanceLabel(b.Attendance))
	}
	if b.Collision {
		m = append(m, "collision")
	}
	if b.Carryover {
		m = append(m, "carryover")
	}
	if b.SplitGroupID != "" {
		m = append(m, "split-group "+shortID(b.SplitGroupID))
	}
	return strings.Join(m, "  ")
}

// blockProvenance renders the block's SOURCE provenance from its evidence, so a
// git block names its repo/branch and a calendar block names its kind instead of
// showing only an opaque evidence id. Duplicate labels are collapsed.
func (g *guided) blockProvenance(b proposal.ProposedTimeBlock) string {
	seen := map[string]bool{}
	var parts []string
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		parts = append(parts, s)
	}
	for _, id := range b.EvidenceIDs {
		e, ok := g.evByID[id]
		if !ok {
			continue
		}
		if len(e.SourceRefs) == 0 {
			add(sourceKindLabel(e.Kind))
			continue
		}
		for _, ref := range e.SourceRefs {
			add(sourceRefLabel(ref))
		}
	}
	return strings.Join(parts, "  ")
}

func durationLabel(d evidence.Duration) string {
	if !d.Known {
		return unknownDuration
	}
	return humanMinutes(d.Minutes)
}

// humanMinutes renders a known minute count as "45m" or "1h 30m".
func humanMinutes(m int) string {
	if m < 60 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh %dm", m/60, m%60)
}

func attendanceLabel(a proposal.AttendanceState) string {
	switch a {
	case proposal.AttendanceAttended:
		return "attendance: attended"
	case proposal.AttendanceNotAttended:
		return "attendance: not attended"
	case proposal.AttendanceUnresolved:
		return "attendance: unconfirmed"
	default:
		return "attendance: " + string(a)
	}
}

func sourceKindLabel(k evidence.SourceKind) string {
	switch k {
	case evidence.SourceJournal:
		return "journal"
	case evidence.SourceCalendar:
		return "calendar"
	case evidence.SourceLocalGit:
		return "git"
	case evidence.SourceRemotePR:
		return "git (remote)"
	case evidence.SourceManual:
		return "manual"
	case evidence.SourceAgentSession:
		return "agent session"
	default:
		return string(k)
	}
}

// sourceRefLabel renders one non-secret provenance reference as a short, human
// label. It never prints a path outside the journal's RELATIVE note path or a URL.
func sourceRefLabel(ref evidence.SourceRef) string {
	switch ref.Kind {
	case evidence.SourceLocalGit, evidence.SourceRemotePR:
		label := "git"
		if r := strings.TrimSpace(ref.Repository); r != "" {
			label += " " + oneLine(r)
		}
		if br := strings.TrimSpace(ref.Branch); br != "" {
			label += "@" + oneLine(br)
		}
		return label
	case evidence.SourceCalendar:
		return "calendar"
	case evidence.SourceJournal:
		if p := strings.TrimSpace(ref.NotePath); p != "" {
			return "journal " + oneLine(p)
		}
		return "journal"
	case evidence.SourceManual:
		return "manual"
	case evidence.SourceAgentSession:
		return "agent session"
	default:
		return sourceKindLabel(ref.Kind)
	}
}

func exclusionReasonLabel(r proposal.ExclusionReason) string {
	switch r {
	case proposal.ExcludeCancelled:
		return "cancelled"
	case proposal.ExcludeDeclined:
		return "declined"
	case proposal.ExcludeUnknownInvitation:
		return "unknown invitation"
	case proposal.ExcludeAgentContextOnly:
		return "agent context only"
	case proposal.ExcludeUnsupportedKind:
		return "unsupported kind"
	case proposal.ExcludeMissingRepository:
		return "missing repository"
	case proposal.ExcludeDetached:
		return "detached"
	default:
		return string(r)
	}
}

// incompleteDetail picks the most useful secret-free detail for an incomplete
// source: the stable error code, else its warnings, else its message.
func incompleteDetail(sr evidence.SourceResult) string {
	if sr.ErrorCode != "" {
		return sr.ErrorCode
	}
	if len(sr.Warnings) > 0 {
		return strings.Join(sr.Warnings, ", ")
	}
	if sr.Message != "" {
		return sr.Message
	}
	return "no detail"
}

// parseClock parses an "HH:MM" clock on the guided day in the configured zone.
func (g *guided) parseClock(v string) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if !validHHMM(v) {
		return time.Time{}, false
	}
	loc := time.UTC
	if tz := strings.TrimSpace(g.cfg.Evidence.Timezone); tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	t, err := time.ParseInLocation("2006-01-02 15:04", g.date+" "+v, loc)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// --- small helpers ----------------------------------------------------------

func tzLabel(cfg cliConfig) string {
	if tz := strings.TrimSpace(cfg.Evidence.Timezone); tz != "" {
		return tz
	}
	return "UTC (default)"
}

func evidenceIndex(rec *workflow.Record) map[string]evidence.Evidence {
	out := make(map[string]evidence.Evidence)
	if rec == nil {
		return out
	}
	for _, e := range rec.CollectedEvidence {
		out[e.ID] = e
	}
	return out
}

func existingConfirmed(rec *workflow.Record) []workflow.ConfirmedInterval {
	if rec == nil {
		return nil
	}
	out := append([]workflow.ConfirmedInterval(nil), rec.Confirmed...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func proposalBlockIndex(prop proposal.Proposal) map[string]proposal.ProposedTimeBlock {
	out := make(map[string]proposal.ProposedTimeBlock)
	for _, b := range prop.Work {
		out[b.ID] = b
	}
	for _, b := range prop.Meetings {
		out[b.ID] = b
	}
	for _, b := range prop.Manual {
		out[b.ID] = b
	}
	return out
}

// countCarries counts the cross-midnight carries visible for this day: the
// carries originating on this day plus the carry blocks surfaced from the
// previous day.
func countCarries(prop proposal.Proposal) int {
	n := len(prop.Carries)
	for _, b := range prop.Work {
		if b.Carryover {
			n++
		}
	}
	for _, b := range prop.Meetings {
		if b.Carryover {
			n++
		}
	}
	for _, b := range prop.Manual {
		if b.Carryover {
			n++
		}
	}
	return n
}

func narrativeProvenance(n proposal.NarrativeCandidate, evByID map[string]evidence.Evidence) string {
	parts := []string{"journal"}
	if n.Section != "" {
		parts = append(parts, "section="+n.Section)
	}
	for _, id := range n.EvidenceIDs {
		e, ok := evByID[id]
		if !ok {
			continue
		}
		for _, ref := range e.SourceRefs {
			if ref.Kind != evidence.SourceJournal {
				continue
			}
			if ref.NotePath != "" {
				parts = append(parts, "note="+oneLine(ref.NotePath))
			}
			if ref.HeadingPath != "" {
				parts = append(parts, "heading="+oneLine(ref.HeadingPath))
			}
			if ref.LineStart > 0 {
				parts = append(parts, fmt.Sprintf("lines=%d-%d", ref.LineStart, ref.LineEnd))
			}
			// The note's OWN stated date is shown as quoted provenance, never
			// rewritten to the target date.
			if d := strings.TrimSpace(ref.StatedDate); d != "" {
				parts = append(parts, "stated="+d)
			}
			break
		}
	}
	parts = append(parts, unknownDuration)
	return strings.Join(parts, "  ")
}

// narrativeHint is a narrative's provenance WITHOUT its leading "journal" label,
// for a context (a link option) that already labels the item as a journal item.
func narrativeHint(n proposal.NarrativeCandidate, evByID map[string]evidence.Evidence) string {
	return strings.TrimPrefix(narrativeProvenance(n, evByID), "journal  ")
}

// collisionCandidates returns the blocks a meeting may collide with: work and
// manual blocks (both can become confirmed intervals a meeting joins).
func collisionCandidates(prop proposal.Proposal) []proposal.ProposedTimeBlock {
	out := make([]proposal.ProposedTimeBlock, 0, len(prop.Work)+len(prop.Manual))
	out = append(out, prop.Work...)
	out = append(out, prop.Manual...)
	return out
}

func collidingWork(meeting proposal.ProposedTimeBlock, work []proposal.ProposedTimeBlock) []proposal.ProposedTimeBlock {
	var out []proposal.ProposedTimeBlock
	for _, w := range work {
		if meeting.StartAt.Before(w.EndAt) && w.StartAt.Before(meeting.EndAt) {
			out = append(out, w)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func blockKindLabel(k proposal.BlockKind) string {
	switch k {
	case proposal.BlockWork:
		return "work"
	case proposal.BlockMeeting:
		return "meeting"
	case proposal.BlockJoint:
		return "joint"
	case proposal.BlockManual:
		return "manual"
	default:
		return string(k)
	}
}

func firstEvidence(b proposal.ProposedTimeBlock) string {
	if len(b.EvidenceIDs) == 0 {
		return ""
	}
	return b.EvidenceIDs[0]
}

func addConfirm(dec *run.ReviewDecisions, c run.ConfirmDecision) {
	for i := range dec.Confirm {
		if dec.Confirm[i].BlockID == c.BlockID {
			dec.Confirm[i] = c
			return
		}
	}
	dec.Confirm = append(dec.Confirm, c)
}

func replaceConfirmProject(dec *run.ReviewDecisions, blockID, project string) {
	for i := range dec.Confirm {
		if dec.Confirm[i].BlockID == blockID {
			dec.Confirm[i].Project = project
			return
		}
	}
	dec.Confirm = append(dec.Confirm, run.ConfirmDecision{BlockID: blockID, Project: project})
}

func hasConfirm(dec run.ReviewDecisions, blockID string) bool {
	for _, c := range dec.Confirm {
		if c.BlockID == blockID {
			return true
		}
	}
	return false
}

func hasManualConfirm(dec run.ReviewDecisions) bool {
	for _, m := range dec.Manual {
		if m.Confirm {
			return true
		}
	}
	return false
}

func manualClassified(dec run.ReviewDecisions) int {
	n := 0
	for _, m := range dec.Manual {
		if strings.TrimSpace(m.ClassificationID) != "" {
			n++
		}
	}
	return n
}

func collectionComplete(sum evidence.CollectionSummary) bool {
	for _, sr := range sum.Sources {
		if sr.Status != evidence.StatusComplete && sr.Status != evidence.StatusSkipped {
			return false
		}
	}
	return true
}

func validHHMM(v string) bool {
	v = strings.TrimSpace(v)
	if len(v) != 5 || v[2] != ':' {
		return false
	}
	h, ok1 := digitPair(v[0], v[1])
	m, ok2 := digitPair(v[3], v[4])
	return ok1 && ok2 && h < 24 && m < 60
}

func digitPair(a, b byte) (int, bool) {
	if a < '0' || a > '9' || b < '0' || b > '9' {
		return 0, false
	}
	return int(a-'0')*10 + int(b-'0'), true
}

func shortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 10 {
		return id[:10]
	}
	return id
}

func orDash(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(none)"
	}
	return s
}

func newManualUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
