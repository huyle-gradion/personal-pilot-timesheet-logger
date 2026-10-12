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
	g.printCollection(collectRes)

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

	rec, err := store.GetDay(ctx, d)
	if err != nil {
		fmt.Fprintf(stderr, "timesheet: record %s: %v\n", d, err)
		return exitError
	}
	evByID := evidenceIndex(rec)
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
		fmt.Fprintf(g.stdout, "\nExisting confirmed work for %s:\n", g.date)
		for _, ci := range existing {
			fmt.Fprintf(g.stdout, "  %s  %s-%s  %s  %s\n", shortID(ci.ID), ci.StartTime, ci.EndTime, orDash(ci.Project), oneLine(ci.Description))
		}
		for _, ci := range existing {
			if err := g.reviewExisting(ctx, ci, &dec); err != nil {
				return dec, err
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

	// A day with no existing and no staged confirmed work is a reviewed-empty
	// day. Existing confirmed work is never dropped and never marked empty.
	if len(existing) == 0 && len(dec.Confirm) == 0 && !hasManualConfirm(dec) {
		dec.MarkEmpty = true
	}
	return dec, nil
}

func (g *guided) reviewExisting(ctx context.Context, ci workflow.ConfirmedInterval, dec *run.ReviewDecisions) error {
	title := fmt.Sprintf("Existing %s  %s-%s  %s  %s", shortID(ci.ID), ci.StartTime, ci.EndTime, orDash(ci.Project), oneLine(ci.Description))
	idx, err := g.p.Choose(ctx, title, []promptOption{
		{Label: "Keep as is"},
		{Label: "Edit times / project / description"},
	}, 0)
	if err != nil {
		return err
	}
	if idx == 0 {
		return nil
	}
	return g.editConfirmed(ctx, ci.ID, ci.StartTime, ci.EndTime, ci.Project, ci.Description, dec)
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

// reviewIncompleteSources surfaces every incomplete source that is not yet
// acknowledged and offers to acknowledge it, so the day can proceed without
// silently presenting a partial collection as complete.
func (g *guided) reviewIncompleteSources(ctx context.Context, rec *workflow.Record, dec *run.ReviewDecisions) error {
	if rec.Collection == nil || rec.Collection.Complete {
		return nil
	}
	ack := map[string]bool{}
	if rec.Review != nil {
		for _, s := range rec.Review.AcknowledgedSources {
			ack[s] = true
		}
	}
	for _, s := range dec.Acknowledge {
		ack[s] = true
	}
	var pending []evidence.SourceResult
	for _, sr := range rec.Collection.Sources {
		if sr.Status == evidence.StatusComplete || sr.Status == evidence.StatusSkipped || ack[sr.SourceID] {
			continue
		}
		pending = append(pending, sr)
	}
	if len(pending) == 0 {
		return nil
	}
	fmt.Fprintf(g.stdout, "\nIncomplete sources (the preview blocks until acknowledged):\n")
	for _, sr := range pending {
		fmt.Fprintf(g.stdout, "  %s: %s\n", sr.SourceID, sr.Status)
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
		{Label: "Edit times / project / description"},
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
			attended, err := g.p.Confirm(ctx, "Did you attend this meeting?", b.Preselected)
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
			attended, err := g.p.Confirm(ctx, "Did you attend this meeting?", b.Preselected)
			if err != nil {
				return err
			}
			if !attended {
				dec.Exclude = append(dec.Exclude, run.ExcludeDecision{EvidenceID: firstEvidence(b), Reason: "not_attended"})
				return nil
			}
		}
		addConfirm(dec, run.ConfirmDecision{BlockID: b.ID})
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

// editProposed stages time/project/description edits for a proposed block that is
// being confirmed. An invalid time is re-prompted with a clear error, never
// silently dropped.
func (g *guided) editProposed(ctx context.Context, b proposal.ProposedTimeBlock, dec *run.ReviewDecisions) error {
	start, end, err := g.promptInterval(ctx, b.Start, b.End)
	if err != nil {
		return err
	}
	project, err := g.p.Input(ctx, "Project", "blank keeps "+orDash(b.Project.Project), b.Project.Project)
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
	project, err := g.p.Input(ctx, "Project", "blank keeps "+orDash(curProject), curProject)
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
	if strings.TrimSpace(project) != curProject {
		dec.Project = append(dec.Project, run.ProjectDecision{BlockID: id, Project: strings.TrimSpace(project)})
	}
	if desc != curDesc {
		dec.Description = append(dec.Description, run.DescriptionDecision{BlockID: id, Text: desc})
	}
	return nil
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
	fmt.Fprintf(g.stdout, "\nJournal narrative candidates (untimed, duration unknown):\n")
	for _, n := range prop.Narratives {
		fmt.Fprintf(g.stdout, "  %s  %s\n", narrativeProvenance(n, evByID), oneLine(n.Description))
	}
	for _, n := range prop.Narratives {
		idx, err := g.p.Choose(ctx,
			fmt.Sprintf("Journal note [%s]: %s", n.Section, oneLine(n.Description)),
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
		opts = append(opts, promptOption{Label: "journal: " + oneLine(n.Description), Hint: narrativeProvenance(n, evByID)})
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
	for _, ci := range existingConfirmed(rec) {
		out = append(out, classTarget{key: ci.ID, label: fmt.Sprintf("%s %s-%s", shortID(ci.ID), ci.StartTime, ci.EndTime), project: ci.Project, current: ci.ClassificationID})
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
			label:   fmt.Sprintf("manual %s-%s", m.Start.Format("15:04"), m.End.Format("15:04")),
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
	cat, prov, err := g.loadCatalog(ctx, "")
	if err != nil {
		fmt.Fprintf(g.stdout, "\nclassification: catalog unavailable (%v); classification stays unset and the preview will block honestly\n", err)
		return nil
	}
	g.cat = &cat
	fmt.Fprintf(g.stdout, "\nclassification catalog: %s\n", prov)
	g.printUnappliedSuggestions(targets)
	retained := 0
	for i := range targets {
		// An interval that already carries an explicit classification is KEPT as
		// is; the guided run only gathers the choices that are still unset.
		if strings.TrimSpace(targets[i].current) != "" {
			retained++
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
		dec.Classification = append(dec.Classification, run.ClassificationDecision{
			BlockID: targets[i].key, ClassificationID: opt.ID, ActivityType: opt.ActivityType,
			ClassificationCode: opt.Code, ClassificationName: opt.Name,
		})
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
			cur = nc
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

// printUnappliedSuggestions shows the config-map and recent-selection hints,
// clearly labeled as NOT applied. No default classification is ever assigned.
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
			if m, ok := prefs.MostRecent(); ok {
				fmt.Fprintf(g.stdout, "suggestion (unapplied): recent selection %s (%s)\n", m.Name, m.Code)
			}
		}
	}
}

// --- confirmation + rendering ----------------------------------------------

func (g *guided) confirmSummary(ctx context.Context, dec run.ReviewDecisions) (bool, error) {
	fmt.Fprintf(g.stdout, "\nReview summary for %s (%s):\n", g.date, g.tz)
	fmt.Fprintf(g.stdout, "  confirm: %d\n", len(dec.Confirm))
	fmt.Fprintf(g.stdout, "  exclude: %d\n", len(dec.Exclude))
	fmt.Fprintf(g.stdout, "  edits: %d time, %d project, %d description\n", len(dec.Trim), len(dec.Project), len(dec.Description))
	fmt.Fprintf(g.stdout, "  manual intervals: %d\n", len(dec.Manual))
	fmt.Fprintf(g.stdout, "  classification chosen: %d\n", len(dec.Classification)+manualClassified(dec))
	if g.carryCount > 0 {
		fmt.Fprintf(g.stdout, "  carryover items: %d (a carry on the next day is confirmed separately)\n", g.carryCount)
	}
	if dec.MarkEmpty {
		fmt.Fprintln(g.stdout, "  result: reviewed with no work (no entries)")
	}
	return g.p.Confirm(ctx, "Apply this review and mark the day reviewed?", false)
}

func (g *guided) printHeader(pre run.PreflightResult) {
	fmt.Fprintf(g.stdout, "date: %s  timezone: %s\n", g.date, g.tz)
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

func (g *guided) printCollection(cr run.CollectResult) {
	if cr.NoSourcesConfigured {
		fmt.Fprintf(g.stdout, "%s: No evidence sources configured.\n", g.date)
	}
	printSourceStatus(g.stdout, cr.Summary)
	if !cr.NoSourcesConfigured && len(cr.Evidence) == 0 && collectionComplete(cr.Summary) {
		fmt.Fprintf(g.stdout, "%s: no evidence collected.\n", g.date)
	}
}

func (g *guided) printOverview(prop proposal.Proposal, rec *workflow.Record, evByID map[string]evidence.Evidence) {
	blocks := len(prop.Work) + len(prop.Meetings) + len(prop.Manual)
	fmt.Fprintf(g.stdout, "\nProposal for %s: %d block(s), %d journal narrative(s), %d excluded\n",
		g.date, blocks, len(prop.Narratives), len(prop.Excluded))
	for _, b := range prop.Work {
		fmt.Fprintf(g.stdout, "  work     %s\n", g.blockLabel(b))
	}
	for _, b := range prop.Meetings {
		fmt.Fprintf(g.stdout, "  meeting  %s\n", g.blockLabel(b))
	}
	for _, b := range prop.Manual {
		fmt.Fprintf(g.stdout, "  manual   %s\n", g.blockLabel(b))
	}
	for _, n := range prop.Narratives {
		fmt.Fprintf(g.stdout, "  narrative %s  %s\n", narrativeProvenance(n, evByID), oneLine(n.Description))
	}
	for _, c := range prop.Carries {
		fmt.Fprintf(g.stdout, "  carry    -> %s %s-%s (group %s)\n", c.Date, c.Start, c.End, shortID(c.SplitGroupID))
	}
	for _, ex := range prop.Excluded {
		line := fmt.Sprintf("  excluded %s reason=%s", shortID(ex.EvidenceID), ex.Reason)
		if ex.NotePath != "" {
			line += " note=" + oneLine(ex.NotePath)
		}
		if ex.HeadingPath != "" {
			line += " heading=" + oneLine(ex.HeadingPath)
		}
		if ex.LineStart > 0 {
			line += fmt.Sprintf(" lines=%d-%d", ex.LineStart, ex.LineEnd)
		}
		fmt.Fprintln(g.stdout, line)
	}
}

func (g *guided) printPlan(res run.PlanResult) {
	if res.Blocked {
		printSuggestions(g.stdout, res.Suggestions)
		fmt.Fprintf(g.stdout, "preview blocked: %s\n", strings.Join(res.BlockReasons, ", "))
		return
	}
	fmt.Fprintf(g.stdout, "plan_id: %s\n", res.Plan.PlanID)
	fmt.Fprintf(g.stdout, "operations: %d\n", len(res.Plan.Operations))
	printSuggestions(g.stdout, res.Suggestions)
	projects := confirmedProjects(context.Background(), g.store, g.date)
	fmt.Fprintf(g.stdout, "remote observed: %s\n", res.Plan.RemoteObservedAt.Format(time.RFC3339))
	fmt.Fprintf(g.stdout, "remote complete: %t\n", res.Plan.RemoteComplete)
	total := 0
	for _, op := range res.Plan.Operations {
		fmt.Fprintln(g.stdout, formatPlanOperation(op, g.cfg, projects))
		total += minutesBetween(op.StartTime, op.EndTime)
	}
	fmt.Fprintf(g.stdout, "total: %dh %dm across %d operation(s)\n", total/60, total%60, len(res.Plan.Operations))
}

// blockTitle is the block line used as a prompt title.
func (g *guided) blockTitle(b proposal.ProposedTimeBlock) string {
	return fmt.Sprintf("%s %s  %s", blockKindLabel(b.Kind), b.Start+"-"+b.End, g.blockLabel(b))
}

func (g *guided) blockLabel(b proposal.ProposedTimeBlock) string {
	dur := "duration unknown"
	if b.Duration.Known {
		dur = fmt.Sprintf("%dm", b.Duration.Minutes)
	}
	line := fmt.Sprintf("%s %s-%s %s %s", shortID(b.ID), b.Start, b.End, orDash(b.Project.Project), dur)
	if b.Kind == proposal.BlockMeeting {
		line += " attendance=" + string(b.Attendance)
	}
	if b.Collision {
		line += " collision"
	}
	if b.Carryover {
		line += " carryover"
	}
	if strings.TrimSpace(b.Description) != "" {
		line += "  " + oneLine(b.Description)
	}
	return line
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
	parts := []string{}
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
	parts = append(parts, "duration=unknown")
	return strings.Join(parts, " ")
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
