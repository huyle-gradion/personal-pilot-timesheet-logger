package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

// The headless subcommands are the skill's [Script] layer. They construct the
// same session serve uses and drive its request dispatcher in-process; they
// never open a second protocol surface and never touch the network beyond what
// the configured collectors already do inside collect.

type headlessFlags struct {
	configPath string
	envFile    string
	date       string
	file       string
}

func parseHeadlessFlags(name string, args []string, needDate, needFile bool) (*headlessFlags, error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	f := &headlessFlags{}
	flags.StringVar(&f.configPath, "config", "", "fixed configuration path (required)")
	flags.StringVar(&f.envFile, "env-file", "", "fixed environment-file path (optional; no file values when omitted)")
	flags.StringVar(&f.date, "date", "", "day to operate on, YYYY-MM-DD"+map[bool]string{true: " (required)", false: " (optional)"}[needDate])
	if needFile {
		flags.StringVar(&f.file, "file", "", "reviewed draft JSON path (required)")
	}
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, fmt.Errorf("%s does not accept positional arguments", name)
	}
	if f.configPath == "" {
		return nil, fmt.Errorf("%s requires --config", name)
	}
	if needDate {
		if f.date == "" {
			return nil, fmt.Errorf("%s requires --date", name)
		}
		if _, err := time.Parse("2006-01-02", f.date); err != nil {
			return nil, fmt.Errorf("%s: --date must be YYYY-MM-DD", name)
		}
	}
	if needFile && f.file == "" {
		return nil, fmt.Errorf("%s requires --file", name)
	}
	return f, nil
}

// recordFlags carries the record subcommand's inputs: --config/--env-file/
// --date always, plus exactly one input mode — the recovery --file reviewed
// JSON, or the descriptions-only --draft + --descriptions pair.
type recordFlags struct {
	configPath   string
	envFile      string
	date         string
	file         string
	draft        string
	descriptions string
}

// parseRecordFlags parses record's flag surface. The two input modes are
// mutually exclusive and one is required: --file alone, or --draft AND
// --descriptions together.
func parseRecordFlags(args []string) (*recordFlags, error) {
	flags := flag.NewFlagSet("record", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	f := &recordFlags{}
	flags.StringVar(&f.configPath, "config", "", "fixed configuration path (required)")
	flags.StringVar(&f.envFile, "env-file", "", "fixed environment-file path (optional; no file values when omitted)")
	flags.StringVar(&f.date, "date", "", "day to record, YYYY-MM-DD (required)")
	flags.StringVar(&f.file, "file", "", "reviewed draft JSON path (recovery input mode)")
	flags.StringVar(&f.draft, "draft", "", "draft JSON path from `timesheet draft` (descriptions-only input mode, with --descriptions)")
	flags.StringVar(&f.descriptions, "descriptions", "", "descriptions-only JSON path (descriptions-only input mode, with --draft)")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, fmt.Errorf("record does not accept positional arguments")
	}
	if f.configPath == "" {
		return nil, fmt.Errorf("record requires --config")
	}
	if f.date == "" {
		return nil, fmt.Errorf("record requires --date")
	}
	if _, err := time.Parse("2006-01-02", f.date); err != nil {
		return nil, fmt.Errorf("record: --date must be YYYY-MM-DD")
	}
	if f.file != "" && (f.draft != "" || f.descriptions != "") {
		return nil, fmt.Errorf("record takes either --file or --draft with --descriptions, not both input modes")
	}
	switch {
	case f.file != "":
	case f.draft != "" && f.descriptions != "":
	default:
		return nil, fmt.Errorf("record requires either --file or both --draft and --descriptions")
	}
	return f, nil
}

// headlessSession mirrors runServe's loading path and drives HandleLine
// directly, one typed request at a time.
type headlessSession struct {
	session *timesheet.Session
	seq     int
	close   func()
}

func openHeadless(configPath, envFile string) (*headlessSession, error) {
	cfg, err := timesheet.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	env := map[string]string{}
	if envFile != "" {
		fileEnv, err := timesheet.LoadEnvFile(envFile)
		if err != nil {
			return nil, err
		}
		env = timesheet.MergeEnv(fileEnv)
	}
	store, err := timesheet.OpenStore(cfg.StatePath)
	if err != nil {
		return nil, err
	}
	return &headlessSession{
		session: timesheet.NewSession(cfg, env, store),
		close:   func() { _ = store.Close() },
	}, nil
}

func (h *headlessSession) done() { h.close() }

func (h *headlessSession) call(op string, req timesheet.Request) (any, error) {
	h.seq++
	req.ID = fmt.Sprintf("headless-%d", h.seq)
	req.Op = op
	line, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	resp, _ := h.session.HandleLine(line)
	if resp.Error != nil {
		return nil, fmt.Errorf("%s: %s", resp.Error.Code, resp.Error.Message)
	}
	return resp.Result, nil
}

// jsonBytes renders one headless result exactly as it appears on stdout:
// indented JSON plus one trailing newline. run writes these same bytes to the
// artifacts file, so a redirect of run's stdout and the stored artifact are
// byte-identical by construction.
func jsonBytes(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func printJSON(v any) error {
	data, err := jsonBytes(v)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

// runCollect performs one collection attempt and prints its bounded result.
func runCollect(args []string) error {
	f, err := parseHeadlessFlags("collect", args, true, false)
	if err != nil {
		return err
	}
	h, err := openHeadless(f.configPath, f.envFile)
	if err != nil {
		return err
	}
	defer h.done()
	if _, err := h.call("open_day", timesheet.Request{Day: f.date}); err != nil {
		return err
	}
	res, err := h.call("collect", timesheet.Request{Day: f.date})
	if err != nil {
		return err
	}
	_, _ = h.call("close", timesheet.Request{})
	return printJSON(res)
}

// buildDayDraft is the shared core of the draft and run subcommands: it opens
// the store, runs exactly one bounded collection attempt for the day, builds
// the compact draft and closes the store. Neither caller duplicates this flow.
func buildDayDraft(configPath, envFile, date string) (timesheet.Draft, error) {
	h, err := openHeadless(configPath, envFile)
	if err != nil {
		return timesheet.Draft{}, err
	}
	defer h.done()
	if _, err := h.call("open_day", timesheet.Request{Day: date}); err != nil {
		return timesheet.Draft{}, err
	}
	if _, err := h.call("collect", timesheet.Request{Day: date}); err != nil {
		return timesheet.Draft{}, err
	}
	res, err := h.call("review", timesheet.Request{Day: date})
	if err != nil {
		return timesheet.Draft{}, err
	}
	review, ok := res.(timesheet.Review)
	if !ok {
		return timesheet.Draft{}, fmt.Errorf("draft: unexpected review payload")
	}
	_, _ = h.call("close", timesheet.Request{})
	return timesheet.BuildDraft(review, timesheet.AppVersion), nil
}

// runDraft collects the day and prints the compact draft for the judgment step.
func runDraft(args []string) error {
	f, err := parseHeadlessFlags("draft", args, true, false)
	if err != nil {
		return err
	}
	draft, err := buildDayDraft(f.configPath, f.envFile, f.date)
	if err != nil {
		return err
	}
	return printJSON(draft)
}

// runRun composes the collect and draft subcommands' single shared flow in one
// process: one bounded collection attempt, then the compact draft. stdout
// carries ONLY the draft JSON — the collect source report is not printed; its
// substance is already in the draft's flags and per-block completeness. The
// same draft bytes are also written to the deterministic artifacts directory
// `$TMPDIR/timesheet/<date>/draft.json` (created as needed), and the artifacts
// path is printed on stderr so stdout stays pure draft JSON: redirecting
// stdout yields exactly the draft bytes.
func runRun(args []string) error {
	f, err := parseHeadlessFlags("run", args, true, false)
	if err != nil {
		return err
	}
	draft, err := buildDayDraft(f.configPath, f.envFile, f.date)
	if err != nil {
		return err
	}
	data, err := jsonBytes(draft)
	if err != nil {
		return err
	}
	dir := filepath.Join(os.TempDir(), "timesheet", f.date)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("run: the artifacts directory cannot be created")
	}
	artifacts := filepath.Join(dir, "draft.json")
	if err := os.WriteFile(artifacts, data, 0o600); err != nil {
		return fmt.Errorf("run: the artifacts draft file cannot be written")
	}
	fmt.Fprintf(os.Stderr, "run: artifacts: %s\n", artifacts)
	_, err = os.Stdout.Write(data)
	return err
}

// runRecord validates the reviewed result and merges it into the day's
// entries. It has two input modes:
//
//   - --draft <draft.json> --descriptions <desc.json>: the descriptions-only
//     judgment step (issue #152). record loads the draft, applies the
//     per-block descriptions and the one notes string, appends each block's
//     prs_inline when not already contained, and runs the exact same
//     validation and merge path as --file.
//   - --file <reviewed.json>: the full reviewed file. Unchanged and still the
//     recovery path.
//
// It opens no store: recording never blocks on, or interferes with, a running
// serve process.
func runRecord(args []string) error {
	f, err := parseRecordFlags(args)
	if err != nil {
		return err
	}
	var reviewed timesheet.ReviewedDraft
	if f.draft != "" {
		draftData, err := os.ReadFile(f.draft)
		if err != nil {
			return fmt.Errorf("record: the draft file cannot be read")
		}
		var draft timesheet.Draft
		if err := json.Unmarshal(draftData, &draft); err != nil {
			return fmt.Errorf("record: the draft file is not valid JSON for the expected draft schema")
		}
		descData, err := os.ReadFile(f.descriptions)
		if err != nil {
			return fmt.Errorf("record: the descriptions file cannot be read")
		}
		desc, err := timesheet.ParseDescriptions(descData)
		if err != nil {
			return err
		}
		reviewed, err = timesheet.ComposeReviewed(draft, desc)
		if err != nil {
			return err
		}
	} else {
		data, err := os.ReadFile(f.file)
		if err != nil {
			return fmt.Errorf("record: the reviewed draft file cannot be read")
		}
		if err := json.Unmarshal(data, &reviewed); err != nil {
			return fmt.Errorf("record: the reviewed draft is not valid JSON for the expected schema")
		}
	}
	// Load the configuration before the date check so a bad --config reports
	// its own root cause, not a misleading date mismatch.
	cfg, err := timesheet.LoadConfig(f.configPath)
	if err != nil {
		return err
	}
	if reviewed.Date != f.date {
		return fmt.Errorf("record: --date %s does not match the reviewed draft's date %s", f.date, reviewed.Date)
	}
	receipt, err := timesheet.RecordEntries(cfg.StatePath, timesheet.AppVersion, reviewed, time.Now())
	if err != nil {
		return err
	}
	return printJSON(receipt)
}

// runStatus prints the store status and, for a given date, the recorded
// entries summary.
func runStatus(args []string) error {
	f, err := parseHeadlessFlags("status", args, false, false)
	if err != nil {
		return err
	}
	h, err := openHeadless(f.configPath, f.envFile)
	if err != nil {
		return err
	}
	defer h.done()
	res, err := h.call("status", timesheet.Request{})
	if err != nil {
		return err
	}
	_, _ = h.call("close", timesheet.Request{})
	if f.date == "" {
		return printJSON(res)
	}
	if _, err := time.Parse("2006-01-02", f.date); err != nil {
		return fmt.Errorf("status: --date must be YYYY-MM-DD")
	}
	cfg, err := timesheet.LoadConfig(f.configPath)
	if err != nil {
		return err
	}
	entries, err := timesheet.ReadEntries(cfg.StatePath, f.date)
	if err != nil {
		return err
	}
	summary := map[string]any{
		"status":  res,
		"date":    f.date,
		"entries": 0,
	}
	if entries != nil {
		summary["entries"] = len(entries.Entries)
		lines := make([]string, 0, len(entries.Entries))
		for _, e := range entries.Entries {
			lines = append(lines, fmt.Sprintf("%s-%s %s", e.Start, e.End, e.Description))
		}
		summary["recorded"] = lines
		summary["entries_file"] = timesheet.EntriesPath(cfg.StatePath, f.date)
	}
	return printJSON(summary)
}

// --- SUBMIT-1 day finalization commands -----------------------------------------
//
// stage/acknowledge/decide/confirm/save drive the existing Session API (the
// same ops serve dispatches) to carry one recorded day from its entries file to
// a durable confirmed day. Each command is one process: open the day, run its
// ops, close. They hold the store's exclusive single-writer lock, so they
// cannot run while a serve process holds the store (unlike record, which opens
// no store). Nothing here approves or submits anything.

// finalizeFlags carries the stage/confirm/save inputs: --config/--env-file/
// --date always, plus the optional --project. Recorded entries carry no project
// of their own, so the staged intervals take the configuration's single
// project, or the --project named when several are defined.
type finalizeFlags struct {
	configPath string
	envFile    string
	date       string
	project    string
}

func parseFinalizeFlags(name string, args []string) (*finalizeFlags, error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	f := &finalizeFlags{}
	flags.StringVar(&f.configPath, "config", "", "fixed configuration path (required)")
	flags.StringVar(&f.envFile, "env-file", "", "fixed environment-file path (optional; no file values when omitted)")
	flags.StringVar(&f.date, "date", "", "day to operate on, YYYY-MM-DD (required)")
	flags.StringVar(&f.project, "project", "", "project id for staged intervals (required when configuration defines multiple projects)")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, fmt.Errorf("%s does not accept positional arguments", name)
	}
	if f.configPath == "" {
		return nil, fmt.Errorf("%s requires --config", name)
	}
	if f.date == "" {
		return nil, fmt.Errorf("%s requires --date", name)
	}
	if _, err := time.Parse("2006-01-02", f.date); err != nil {
		return nil, fmt.Errorf("%s: --date must be YYYY-MM-DD", name)
	}
	return f, nil
}

// resolveProject returns the project id staged intervals carry: the explicit
// --project, or the configuration's only project when none was named. An
// unknown project id fails later in add_interval's own validation, which the
// command passes through untouched.
func resolveProject(name string, cfg *timesheet.Config, explicit string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		return explicit, nil
	}
	if len(cfg.Projects) == 1 {
		return cfg.Projects[0].ID, nil
	}
	return "", fmt.Errorf("%s: configuration defines %d projects; --project is required", name, len(cfg.Projects))
}

// acknowledgeFlags carries the acknowledge inputs. The collection_id must be
// copied verbatim from a prior collect receipt; the acknowledge_source op
// fails closed on a missing or stale id and this command passes that error
// through untouched.
type acknowledgeFlags struct {
	configPath   string
	envFile      string
	date         string
	source       string
	collectionID string
}

func parseAcknowledgeFlags(args []string) (*acknowledgeFlags, error) {
	flags := flag.NewFlagSet("acknowledge", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	f := &acknowledgeFlags{}
	flags.StringVar(&f.configPath, "config", "", "fixed configuration path (required)")
	flags.StringVar(&f.envFile, "env-file", "", "fixed environment-file path (optional; no file values when omitted)")
	flags.StringVar(&f.date, "date", "", "day to operate on, YYYY-MM-DD (required)")
	flags.StringVar(&f.source, "source", "", "collected source being reviewed: github or calendar (required)")
	flags.StringVar(&f.collectionID, "collection-id", "", "collection_id copied verbatim from the prior collect receipt (required)")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, fmt.Errorf("acknowledge does not accept positional arguments")
	}
	if f.configPath == "" {
		return nil, fmt.Errorf("acknowledge requires --config")
	}
	if f.date == "" {
		return nil, fmt.Errorf("acknowledge requires --date")
	}
	if _, err := time.Parse("2006-01-02", f.date); err != nil {
		return nil, fmt.Errorf("acknowledge: --date must be YYYY-MM-DD")
	}
	if f.source != timesheet.SourceGithub && f.source != timesheet.SourceCalendar {
		return nil, fmt.Errorf("acknowledge: --source must be github or calendar")
	}
	if f.collectionID == "" {
		return nil, fmt.Errorf("acknowledge requires --collection-id copied verbatim from the prior collect receipt")
	}
	return f, nil
}

// decideFlags carries the decide inputs. The decision surface here is the
// attendance verdict only (attended or skipped); the protocol's pending value
// is a collection-time state, never a CLI decision.
type decideFlags struct {
	configPath   string
	envFile      string
	date         string
	meetingID    string
	decision     string
	collectionID string
}

func parseDecideFlags(args []string) (*decideFlags, error) {
	flags := flag.NewFlagSet("decide", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	f := &decideFlags{}
	flags.StringVar(&f.configPath, "config", "", "fixed configuration path (required)")
	flags.StringVar(&f.envFile, "env-file", "", "fixed environment-file path (optional; no file values when omitted)")
	flags.StringVar(&f.date, "date", "", "day to operate on, YYYY-MM-DD (required)")
	flags.StringVar(&f.meetingID, "meeting-id", "", "meeting ref copied verbatim from the reviewed draft or collect receipt (required)")
	flags.StringVar(&f.decision, "decision", "", "attendance decision: attended or skipped (required)")
	flags.StringVar(&f.collectionID, "collection-id", "", "collection_id copied verbatim from the prior collect receipt (required)")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, fmt.Errorf("decide does not accept positional arguments")
	}
	if f.configPath == "" {
		return nil, fmt.Errorf("decide requires --config")
	}
	if f.date == "" {
		return nil, fmt.Errorf("decide requires --date")
	}
	if _, err := time.Parse("2006-01-02", f.date); err != nil {
		return nil, fmt.Errorf("decide: --date must be YYYY-MM-DD")
	}
	if f.meetingID == "" {
		return nil, fmt.Errorf("decide requires --meeting-id")
	}
	if f.decision != "attended" && f.decision != "skipped" {
		return nil, fmt.Errorf("decide: --decision must be attended or skipped")
	}
	if f.collectionID == "" {
		return nil, fmt.Errorf("decide requires --collection-id copied verbatim from the prior collect receipt")
	}
	return f, nil
}

// StageBlockRef pairs one recorded block with the staged interval it maps to.
type StageBlockRef struct {
	BlockID    string `json:"block_id"`
	IntervalID string `json:"interval_id,omitempty"`
}

// StageSkip is one recorded entry stage did not convert, with the reason.
// Entries without time anchors are listed honestly and never given invented
// times.
type StageSkip struct {
	BlockID string `json:"block_id"`
	Reason  string `json:"reason"`
}

// StageReceipt is stage's stdout receipt. Every list is in block_id order, so
// the output is deterministic.
type StageReceipt struct {
	Date            string          `json:"date"`
	EntriesFile     string          `json:"entries_file"`
	StageFile       string          `json:"stage_file"`
	Staged          []StageBlockRef `json:"staged"`
	Updated         []StageBlockRef `json:"updated"`
	Unchanged       []StageBlockRef `json:"unchanged"`
	Removed         []StageBlockRef `json:"removed"`
	Skipped         []StageSkip     `json:"skipped"`
	Revision        int             `json:"revision"`
	StagedIntervals int             `json:"staged_intervals"`
}

// applyStage converts the day's recorded entries into staged intervals through
// the existing add_interval/edit_interval/remove_interval session ops, using
// the <date>.stage.json sidecar to keep the staged set exactly matching the
// current entries file. The sidecar's edit and remove branches fire only
// within the process that staged (whenever a mapped block's interval already
// exists in the open day's staged set); they are not a cross-process
// persistence mechanism.
//
// Because Day.Staged is session-only by design (it is never durable), every
// command that needs the staged set re-derives it from the entries ledger
// inside its own process; this function is that derivation, and stage, confirm
// and save all run it. A fresh process starts from an EMPTY staged set, so a
// cross-process re-stage re-adds via add_interval; the re-derived ids match the
// previous ones only because nothing was yet durable (the day's NextInterval
// counter is not saved until confirm+save). The caller must have verified via
// openFinalizeDay that the day carries no confirmed intervals.
func applyStage(h *headlessSession, cfg *timesheet.Config, name, date, project string, summary timesheet.DaySummary) (*StageReceipt, error) {
	revision := summary.Revision
	stagedIDs := map[string]bool{}
	segmentByID := map[string]timesheet.Segment{}
	for _, seg := range summary.Staged.Segments {
		if seg.OriginDate == date {
			stagedIDs[seg.IntervalID] = true
			segmentByID[seg.IntervalID] = seg
		}
	}
	entries, err := timesheet.ReadEntries(cfg.StatePath, date)
	if err != nil {
		return nil, err
	}
	if entries == nil {
		return nil, fmt.Errorf("%s: no recorded entries file for %s; run record first", name, date)
	}
	stageMap, err := timesheet.ReadStageMap(cfg.StatePath, date)
	if err != nil {
		return nil, err
	}

	// Deterministic processing order: the recorded blocks sorted by block_id.
	ids := make([]string, 0, len(entries.Entries))
	byID := make(map[string]timesheet.EntryRecord, len(entries.Entries))
	for _, e := range entries.Entries {
		ids = append(ids, e.BlockID)
		byID[e.BlockID] = e
	}
	sort.Strings(ids)

	receipt := &StageReceipt{
		Date:        date,
		EntriesFile: timesheet.EntriesPath(cfg.StatePath, date),
		StageFile:   timesheet.StagePath(cfg.StatePath, date),
		Staged:      []StageBlockRef{},
		Updated:     []StageBlockRef{},
		Unchanged:   []StageBlockRef{},
		Removed:     []StageBlockRef{},
		Skipped:     []StageSkip{},
	}
	next := map[string]string{}
	desired := map[string]bool{}
	for _, id := range ids {
		e := byID[id]
		if e.Start == "" || e.End == "" {
			missing := "start"
			if e.Start != "" {
				missing = "end"
			}
			receipt.Skipped = append(receipt.Skipped, StageSkip{
				BlockID: id,
				Reason:  fmt.Sprintf("entry has no %s time (kind %s); times are never invented", missing, e.Kind),
			})
			continue
		}
		desired[id] = true
		text := fmt.Sprintf("%s %s-%s %s %s", date, e.Start, e.End, project, e.Description)
		// Same-process restage path: the mapped interval already exists in this
		// process's staged set, so edit or keep it in place. A fresh process
		// never lands here — its staged set starts empty and the block is
		// re-added below.
		if mapped, ok := stageMap[id]; ok && stagedIDs[mapped] {
			seg := segmentByID[mapped]
			if seg.Start == e.Start && seg.End == e.End && seg.Project == project && seg.Description == e.Description {
				receipt.Unchanged = append(receipt.Unchanged, StageBlockRef{BlockID: id, IntervalID: mapped})
				next[id] = mapped
				continue
			}
			if _, err := h.call("edit_interval", timesheet.Request{
				Day: date, Revision: &revision, IntervalID: mapped, Text: text,
			}); err != nil {
				return nil, err
			}
			receipt.Updated = append(receipt.Updated, StageBlockRef{BlockID: id, IntervalID: mapped})
			next[id] = mapped
			continue
		}
		res, err := h.call("add_interval", timesheet.Request{Day: date, Revision: &revision, Text: text})
		if err != nil {
			return nil, err
		}
		mutation, ok := res.(timesheet.IntervalMutation)
		if !ok {
			return nil, fmt.Errorf("%s: unexpected add_interval payload", name)
		}
		receipt.Staged = append(receipt.Staged, StageBlockRef{BlockID: id, IntervalID: mutation.IntervalID})
		next[id] = mutation.IntervalID
	}

	// Entries removed from the file lose their staged intervals (same-process
	// path only; a stale map entry whose interval no longer exists is simply
	// dropped).
	stale := make([]string, 0, len(stageMap))
	for id := range stageMap {
		if !desired[id] {
			stale = append(stale, id)
		}
	}
	sort.Strings(stale)
	for _, id := range stale {
		mapped := stageMap[id]
		if stagedIDs[mapped] {
			if _, err := h.call("remove_interval", timesheet.Request{
				Day: date, Revision: &revision, IntervalID: mapped,
			}); err != nil {
				return nil, err
			}
			receipt.Removed = append(receipt.Removed, StageBlockRef{BlockID: id, IntervalID: mapped})
		}
	}

	if err := timesheet.WriteStageMap(cfg.StatePath, timesheet.AppVersion, date, next, time.Now()); err != nil {
		return nil, err
	}
	// Re-open the day so the receipt reports the staged set exactly as the
	// session now sees it (open_day is idempotent within the process).
	res, err := h.call("open_day", timesheet.Request{Day: date})
	if err != nil {
		return nil, err
	}
	final, ok := res.(timesheet.DaySummary)
	if !ok {
		return nil, fmt.Errorf("%s: unexpected open_day payload", name)
	}
	for _, seg := range final.Staged.Segments {
		if seg.OriginDate == date {
			receipt.StagedIntervals++
		}
	}
	receipt.Revision = final.Revision
	return receipt, nil
}

// openFinalizeDay opens the day for a stage/confirm/save command and refuses
// when it already carries confirmed intervals. The staged set is derived from
// the entries ledger, and a finalized day is never re-derived or silently
// mutated; divergence after confirmation is detected downstream by the plan's
// content hash (SUBMIT-3), never by restaging over confirmed work.
func openFinalizeDay(h *headlessSession, name, date string) (timesheet.DaySummary, error) {
	res, err := h.call("open_day", timesheet.Request{Day: date})
	if err != nil {
		return timesheet.DaySummary{}, err
	}
	summary, ok := res.(timesheet.DaySummary)
	if !ok {
		return timesheet.DaySummary{}, fmt.Errorf("%s: unexpected open_day payload", name)
	}
	if len(summary.Confirmed.Segments) > 0 {
		return timesheet.DaySummary{}, fmt.Errorf(
			"%s: %s already has confirmed intervals; the day is finalized and confirmed work is never restaged", name, date)
	}
	return summary, nil
}

// noteStageSkips mirrors stage's skip list on stderr for confirm and save, so
// the honesty of the never-invented-times rule survives into the commands that
// consume the derived staging.
func noteStageSkips(name string, receipt *StageReceipt) {
	if len(receipt.Skipped) == 0 {
		return
	}
	ids := make([]string, 0, len(receipt.Skipped))
	for _, s := range receipt.Skipped {
		ids = append(ids, s.BlockID)
	}
	fmt.Fprintf(os.Stderr, "%s: skipped entries without time anchors: %s\n", name, strings.Join(ids, ", "))
}

// runStage converts the day's recorded entries file into staged intervals.
// Natural order: run --date (collect) first, then stage, then acknowledge
// and/or decide with the collection_id from that collect receipt, then confirm,
// then save. Holds the store's exclusive lock.
func runStage(args []string) error {
	f, err := parseFinalizeFlags("stage", args)
	if err != nil {
		return err
	}
	cfg, err := timesheet.LoadConfig(f.configPath)
	if err != nil {
		return err
	}
	project, err := resolveProject("stage", cfg, f.project)
	if err != nil {
		return err
	}
	h, err := openHeadless(f.configPath, f.envFile)
	if err != nil {
		return err
	}
	defer h.done()
	summary, err := openFinalizeDay(h, "stage", f.date)
	if err != nil {
		return err
	}
	receipt, err := applyStage(h, cfg, "stage", f.date, project, summary)
	if err != nil {
		return err
	}
	_, _ = h.call("close", timesheet.Request{})
	return printJSON(receipt)
}

// runAcknowledge wraps the acknowledge_source op: it marks this day's recorded
// attempt for the named source as reviewed, bound to the exact collection_id
// from the prior collect receipt. A missing or stale id fails closed in the op
// and the error is passed through untouched.
func runAcknowledge(args []string) error {
	f, err := parseAcknowledgeFlags(args)
	if err != nil {
		return err
	}
	h, err := openHeadless(f.configPath, f.envFile)
	if err != nil {
		return err
	}
	defer h.done()
	if _, err := h.call("open_day", timesheet.Request{Day: f.date}); err != nil {
		return err
	}
	res, err := h.call("acknowledge_source", timesheet.Request{
		Day: f.date, Source: f.source, CollectionID: f.collectionID,
	})
	if err != nil {
		return err
	}
	_, _ = h.call("close", timesheet.Request{})
	return printJSON(res)
}

// runDecide wraps the decide_meeting op: it records the explicit attended or
// skipped decision for one proposed meeting, bound to the exact collection_id
// from the prior collect receipt. A missing or stale id fails closed in the op
// and the error is passed through untouched.
func runDecide(args []string) error {
	f, err := parseDecideFlags(args)
	if err != nil {
		return err
	}
	h, err := openHeadless(f.configPath, f.envFile)
	if err != nil {
		return err
	}
	defer h.done()
	if _, err := h.call("open_day", timesheet.Request{Day: f.date}); err != nil {
		return err
	}
	res, err := h.call("decide_meeting", timesheet.Request{
		Day: f.date, MeetingID: f.meetingID, Decision: f.decision, CollectionID: f.collectionID,
	})
	if err != nil {
		return err
	}
	_, _ = h.call("close", timesheet.Request{})
	return printJSON(res)
}

// runConfirm derives the staged set from the entries ledger, then wraps the
// confirm op, including its source gate: every enabled source needs a
// collection attempt for the date, and a non-complete attempt needs an explicit
// acknowledgment. A missing acknowledgment fails with the op's gate error
// naming what is missing, passed through verbatim. The receipt is in-memory
// only; save makes it durable.
func runConfirm(args []string) error {
	f, err := parseFinalizeFlags("confirm", args)
	if err != nil {
		return err
	}
	cfg, err := timesheet.LoadConfig(f.configPath)
	if err != nil {
		return err
	}
	project, err := resolveProject("confirm", cfg, f.project)
	if err != nil {
		return err
	}
	h, err := openHeadless(f.configPath, f.envFile)
	if err != nil {
		return err
	}
	defer h.done()
	summary, err := openFinalizeDay(h, "confirm", f.date)
	if err != nil {
		return err
	}
	staged, err := applyStage(h, cfg, "confirm", f.date, project, summary)
	if err != nil {
		return err
	}
	noteStageSkips("confirm", staged)
	res, err := h.call("confirm", timesheet.Request{Day: f.date, Revision: &summary.Revision})
	if err != nil {
		return err
	}
	receipt, ok := res.(timesheet.ConfirmReceipt)
	if !ok {
		return fmt.Errorf("confirm: unexpected confirm payload")
	}
	_, _ = h.call("close", timesheet.Request{})
	return printJSON(receipt)
}

// runSave derives the staged set from the entries ledger, confirms it (source
// gate included), and wraps the save op: one durable Commit of the confirmed
// day. After a successful save the day is final; further stage/confirm/save
// runs are refused.
func runSave(args []string) error {
	f, err := parseFinalizeFlags("save", args)
	if err != nil {
		return err
	}
	cfg, err := timesheet.LoadConfig(f.configPath)
	if err != nil {
		return err
	}
	project, err := resolveProject("save", cfg, f.project)
	if err != nil {
		return err
	}
	h, err := openHeadless(f.configPath, f.envFile)
	if err != nil {
		return err
	}
	defer h.done()
	summary, err := openFinalizeDay(h, "save", f.date)
	if err != nil {
		return err
	}
	staged, err := applyStage(h, cfg, "save", f.date, project, summary)
	if err != nil {
		return err
	}
	noteStageSkips("save", staged)
	confirmed, err := h.call("confirm", timesheet.Request{Day: f.date, Revision: &summary.Revision})
	if err != nil {
		return err
	}
	confirmReceipt, ok := confirmed.(timesheet.ConfirmReceipt)
	if !ok {
		return fmt.Errorf("save: unexpected confirm payload")
	}
	res, err := h.call("save", timesheet.Request{Day: f.date, Revision: &confirmReceipt.Revision})
	if err != nil {
		return err
	}
	receipt, ok := res.(timesheet.SaveReceipt)
	if !ok {
		return fmt.Errorf("save: unexpected save payload")
	}
	_, _ = h.call("close", timesheet.Request{})
	return printJSON(receipt)
}
