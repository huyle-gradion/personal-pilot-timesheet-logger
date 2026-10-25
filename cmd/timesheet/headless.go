package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
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

func printJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
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

// runDraft collects the day and prints the compact draft for the judgment step.
func runDraft(args []string) error {
	f, err := parseHeadlessFlags("draft", args, true, false)
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
	if _, err := h.call("collect", timesheet.Request{Day: f.date}); err != nil {
		return err
	}
	res, err := h.call("review", timesheet.Request{Day: f.date})
	if err != nil {
		return err
	}
	review, ok := res.(timesheet.Review)
	if !ok {
		return fmt.Errorf("draft: unexpected review payload")
	}
	_, _ = h.call("close", timesheet.Request{})
	return printJSON(timesheet.BuildDraft(review, timesheet.AppVersion))
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
