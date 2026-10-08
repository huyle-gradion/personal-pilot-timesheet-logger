package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/storage"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/timesheet"
)

// Named constants and TigerStyle bounds.
const (
	MaxPromptRetriesCount    = 3
	MaxCollisionsCount       = 25
	MaxReviewTableWidthBytes = 120
	ReviewCommandTimeoutSecs = 60 * time.Second
	MaxBlocksCount           = 50
	MaxInputSizeBytes        = 10 * 1024 * 1024 // 10 MiB limit
)

// Sysexits exit codes.
const (
	ExitOK    = 0
	ExitUsage = 64 // EX_USAGE
	ExitErr   = 1
)

// Invariant assertion identifiers.
const (
	AssertReviewContextNil  = "ASSERT_REVIEW_001_NIL_CONTEXT"
	AssertReviewWritersNil  = "ASSERT_REVIEW_002_NIL_WRITER"
	AssertReviewEmptyBlocks = "ASSERT_REVIEW_003_EMPTY_BLOCKS"
)

var datePattern = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})$`)
var timePattern = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// Assert validates internal invariants.
func Assert(condition bool, invariantID string) {
	if !condition {
		panic(invariantID)
	}
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	exitCode := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	os.Exit(exitCode)
}

func run(ctx context.Context, args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	Assert(ctx != nil, AssertReviewContextNil)
	Assert(out != nil, AssertReviewWritersNil)
	Assert(errOut != nil, AssertReviewWritersNil)

	fs := flag.NewFlagSet("review-timesheet", flag.ContinueOnError)
	fs.SetOutput(errOut)

	var (
		inputPath      string
		targetDate     string
		nonInteractive bool
		autoResolve    string
		storageDir     string
		outputPath     string
		confirm        bool
		sinkType       string
	)

	fs.StringVar(&inputPath, "input", "", "Path to clustered JSON file, or '-' for stdin")
	fs.StringVar(&targetDate, "date", time.Now().UTC().Format("2006-01-02"), "Target date in YYYY-MM-DD format")
	fs.BoolVar(&nonInteractive, "non-interactive", false, "Run in non-interactive batch mode")
	fs.StringVar(&autoResolve, "auto-resolve", "keep_both", "Resolution policy when non-interactive (keep_both, separate, discard)")
	fs.StringVar(&storageDir, "storage-dir", "", "Directory for Tier 2 persistent storage")
	fs.StringVar(&outputPath, "output", "", "Optional destination path for resolved JSON output (or '-' for stdout)")
	fs.BoolVar(&confirm, "confirm", false, "Bypass interactive confirmation before saving")
	fs.StringVar(&sinkType, "sink", "console", "Pluggable sink export (console, obsidian, none, all)")

	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	targetDate = strings.TrimSpace(targetDate)
	if !datePattern.MatchString(targetDate) {
		fmt.Fprintf(errOut, "error: invalid -date %q: must be YYYY-MM-DD\n", targetDate)
		return ExitUsage
	}

	autoResolve = strings.TrimSpace(autoResolve)
	switch autoResolve {
	case string(timesheet.ResolutionKeepBoth), string(timesheet.ResolutionSeparate), string(timesheet.ResolutionDiscard):
	default:
		fmt.Fprintf(errOut, "error: invalid -auto-resolve strategy %q: must be keep_both, separate, or discard\n", autoResolve)
		return ExitUsage
	}

	dataDir := storage.ResolveDataDir(storageDir)
	store, storeErr := storage.NewJSONFileStore(dataDir)
	if storeErr != nil {
		fmt.Fprintf(errOut, "error: initializing storage: %v\n", storeErr)
		return ExitErr
	}

	// Step 1: Check existing record in Tier 2 storage
	var existingRecord *storage.TimesheetRecord
	rec, err := store.GetDay(ctx, targetDate)
	if err == nil && rec != nil {
		existingRecord = rec
		if !nonInteractive {
			fmt.Fprintf(out, "Found existing record for %s (version %d, %.2f hours, %d blocks).\n",
				targetDate, rec.Version, rec.TotalHours, len(rec.Blocks))
		}
	} else if err != nil && !errors.Is(err, storage.ErrRecordNotFound) {
		fmt.Fprintf(errOut, "warning: checking storage record for %s: %v\n", targetDate, err)
	}

	// Step 2: Load input blocks
	var blocks []timesheet.TimeBlock
	inputPath = strings.TrimSpace(inputPath)

	if inputPath != "" {
		loadedBlocks, loadErr := loadInputBlocks(ctx, inputPath, in, targetDate)
		if loadErr != nil {
			fmt.Fprintf(errOut, "error: loading input blocks: %v\n", loadErr)
			return ExitUsage
		}

		if existingRecord != nil {
			// Merge existing blocks with newly loaded blocks (deduplicate by ID)
			existingIDs := make(map[string]bool)
			for _, eb := range existingRecord.Blocks {
				existingIDs[eb.ID] = true
				blocks = append(blocks, eb)
			}
			for _, lb := range loadedBlocks {
				if !existingIDs[lb.ID] {
					blocks = append(blocks, lb)
				}
			}
		} else {
			blocks = loadedBlocks
		}
	} else if existingRecord != nil {
		blocks = existingRecord.Blocks
	} else {
		// Attempt to read from stdin if piped
		stdinBlocks, stdinErr := readFromReader(ctx, in, targetDate)
		if stdinErr == nil && len(stdinBlocks) > 0 {
			blocks = stdinBlocks
		} else {
			fmt.Fprintf(errOut, "error: no input provided and no existing timesheet found for date %s\n", targetDate)
			return ExitUsage
		}
	}

	if len(blocks) == 0 {
		fmt.Fprintf(errOut, "error: no timesheet blocks available to review for date %s\n", targetDate)
		return ExitUsage
	}

	reader := bufio.NewReader(in)

	// Step 3: Collision detection and resolution
	collisions, colErr := timesheet.DetectCollisions(ctx, blocks)
	if colErr != nil {
		fmt.Fprintf(errOut, "error: detecting collisions: %v\n", colErr)
		return ExitErr
	}

	if len(collisions) > 0 {
		if nonInteractive {
			resBlocks, resErr := timesheet.ResolveAll(ctx, blocks, timesheet.CollisionResolution(autoResolve))
			if resErr != nil {
				fmt.Fprintf(errOut, "error: auto-resolving collisions: %v\n", resErr)
				return ExitErr
			}
			blocks = resBlocks
		} else {
			resBlocks, resErr := resolveCollisionsInteractive(ctx, blocks, collisions, reader, out)
			if resErr != nil {
				fmt.Fprintf(errOut, "error: interactive collision resolution: %v\n", resErr)
				return ExitErr
			}
			blocks = resBlocks
		}
	}

	// Step 4: Display schedule table
	renderScheduleTable(out, blocks)

	// Step 5: Interactive editing loop (if interactive mode)
	if !nonInteractive {
		editedBlocks, shouldSave, loopErr := interactiveEditLoop(targetDate, blocks, reader, out)
		if loopErr != nil {
			fmt.Fprintf(errOut, "error: interactive edit loop: %v\n", loopErr)
			return ExitErr
		}
		if !shouldSave {
			fmt.Fprintln(out, "Exiting without saving.")
			return ExitOK
		}
		blocks = editedBlocks

		if !confirm {
			fmt.Fprint(out, "\nPersist and prepare for submission? [Y/n]: ")
			confirmInput, _ := reader.ReadString('\n')
			trimmed := strings.ToLower(strings.TrimSpace(confirmInput))
			if trimmed == "n" || trimmed == "no" {
				fmt.Fprintln(out, "Aborted by user.")
				return ExitOK
			}
		}
	}

	// Step 6: Calculate total hours and build TimesheetRecord
	totalHours := calculateTotalHours(blocks)
	record := storage.TimesheetRecord{
		Date:       targetDate,
		TotalHours: totalHours,
		Blocks:     blocks,
	}

	// Step 7: Atomic persistence to Tier 2 storage
	if saveErr := store.SaveDay(ctx, record); saveErr != nil {
		fmt.Fprintf(errOut, "error: saving timesheet record: %v\n", saveErr)
		return ExitErr
	}

	// Read updated record to get incremented version
	savedRecord, getErr := store.GetDay(ctx, targetDate)
	if getErr == nil && savedRecord != nil {
		record = *savedRecord
	}

	if !nonInteractive {
		fmt.Fprintf(out, "\nSuccessfully persisted timesheet for %s (version %d, %.2f hours) to %s\n",
			record.Date, record.Version, record.TotalHours, dataDir)
	}

	// Step 8: Dispatch to exporter sinks
	dispatchSinks(ctx, record, sinkType, out, errOut)

	// Step 9: Handle -output JSON destination
	if outputPath != "" {
		data, mErr := json.MarshalIndent(record, "", "  ")
		if mErr != nil {
			fmt.Fprintf(errOut, "error: marshaling record: %v\n", mErr)
			return ExitErr
		}
		if outputPath == "-" {
			fmt.Fprintln(out, string(data))
		} else {
			if wErr := os.WriteFile(outputPath, data, 0600); wErr != nil {
				fmt.Fprintf(errOut, "error: writing output to %s: %v\n", outputPath, wErr)
				return ExitErr
			}
		}
	}

	return ExitOK
}

func loadInputBlocks(ctx context.Context, inputPath string, in io.Reader, targetDate string) ([]timesheet.TimeBlock, error) {
	if inputPath == "-" {
		return readFromReader(ctx, in, targetDate)
	}

	f, err := os.Open(inputPath)
	if err != nil {
		return nil, fmt.Errorf("opening input file: %w", err)
	}
	defer f.Close()

	return readFromReader(ctx, f, targetDate)
}

func readFromReader(ctx context.Context, r io.Reader, targetDate string) ([]timesheet.TimeBlock, error) {
	lr := io.LimitReader(r, MaxInputSizeBytes)
	bytes, err := io.ReadAll(lr)
	if err != nil {
		return nil, fmt.Errorf("reading input: %w", err)
	}
	if len(bytes) == 0 {
		return nil, errors.New("empty input")
	}

	// 1. Try unmarshaling as []timesheet.TimeBlock
	var blocks []timesheet.TimeBlock
	if err := json.Unmarshal(bytes, &blocks); err == nil && len(blocks) > 0 && blocks[0].StartTime != "" {
		return blocks, nil
	}

	// 2. Try unmarshaling as storage.TimesheetRecord
	var record storage.TimesheetRecord
	if err := json.Unmarshal(bytes, &record); err == nil && len(record.Blocks) > 0 {
		return record.Blocks, nil
	}

	// 3. Try unmarshaling as []timesheet.SimpleEntry and cluster on the fly
	var entries []timesheet.SimpleEntry
	if err := json.Unmarshal(bytes, &entries); err == nil && len(entries) > 0 {
		parsedDate, pErr := time.Parse("2006-01-02", targetDate)
		if pErr != nil {
			parsedDate = time.Now().UTC()
		}
		opts := timesheet.ClusterOptions{
			Location: time.UTC,
		}
		return timesheet.ClusterEntries(ctx, entries, parsedDate, opts)
	}

	return nil, errors.New("unrecognized input format: expected []TimeBlock, TimesheetRecord, or []SimpleEntry")
}

func resolveCollisionsInteractive(
	ctx context.Context,
	blocks []timesheet.TimeBlock,
	collisions []timesheet.Collision,
	reader *bufio.Reader,
	out io.Writer,
) ([]timesheet.TimeBlock, error) {
	currentBlocks := blocks

	for i, col := range collisions {
		fmt.Fprintf(out, "\nCollision %d of %d detected:\n", i+1, len(collisions))
		fmt.Fprintf(out, "  Calendar : %s (%s - %s)\n", col.MeetingTitle, col.MeetingStartTime, col.MeetingEndTime)
		fmt.Fprintf(out, "  Git      : %s (%s - %s)\n\n", col.GitTitle, col.GitStartTime, col.GitEndTime)
		fmt.Fprintln(out, "Resolution options:")
		fmt.Fprintln(out, "  [1] Keep both (merge into joint entry)")
		fmt.Fprintln(out, "  [2] Separate (keep meeting, shift git block to start after meeting)")
		fmt.Fprintln(out, "  [3] Discard git activity during meeting")

		var strategy timesheet.CollisionResolution
		for attempt := 0; attempt < MaxPromptRetriesCount; attempt++ {
			fmt.Fprint(out, "Select [1-3] (default 1): ")
			input, rErr := reader.ReadString('\n')
			if rErr != nil && !errors.Is(rErr, io.EOF) {
				return nil, rErr
			}
			choice := strings.TrimSpace(input)
			if choice == "" || choice == "1" {
				strategy = timesheet.ResolutionKeepBoth
				break
			} else if choice == "2" {
				strategy = timesheet.ResolutionSeparate
				break
			} else if choice == "3" {
				strategy = timesheet.ResolutionDiscard
				break
			} else {
				fmt.Fprintln(out, "Invalid selection. Please choose 1, 2, or 3.")
			}
		}

		if strategy == "" {
			strategy = timesheet.ResolutionKeepBoth
		}

		// Apply strategy to the current blocks
		resBlocks, resErr := timesheet.ResolveAll(ctx, currentBlocks, strategy)
		if resErr != nil {
			return nil, resErr
		}
		currentBlocks = resBlocks
	}

	return currentBlocks, nil
}

func renderScheduleTable(out io.Writer, blocks []timesheet.TimeBlock) {
	fmt.Fprintln(out, "")
	fmt.Fprintf(out, "%-3s %-15s %-9s %-10s %-15s %-35s %-10s\n",
		"#", "Time Window", "Duration", "Source", "Category", "Title", "Collision")
	fmt.Fprintln(out, strings.Repeat("-", 100))

	categoryMinutes := make(map[timesheet.SemanticCategory]int)
	totalMinutes := 0

	for i, b := range blocks {
		durMins := calculateBlockDurationMinutes(b)
		totalMinutes += durMins
		categoryMinutes[b.Category] += durMins

		colStr := "-"
		if b.HasCollision {
			colStr = "YES"
		}

		titleTrunc := b.Title
		if len(titleTrunc) > 34 {
			titleTrunc = titleTrunc[:31] + "..."
		}

		fmt.Fprintf(out, "%-3d %-15s %-9s %-10s %-15s %-35s %-10s\n",
			i+1,
			fmt.Sprintf("%s - %s", b.StartTime, b.EndTime),
			fmt.Sprintf("%dm", durMins),
			b.Source,
			b.Category,
			titleTrunc,
			colStr,
		)
	}

	fmt.Fprintln(out, strings.Repeat("-", 100))
	totalHours := float64(totalMinutes) / 60.0
	fmt.Fprintf(out, "Total time: %.2f hours across %d blocks\n", totalHours, len(blocks))

	var catBreakdown []string
	var categories []string
	for cat := range categoryMinutes {
		categories = append(categories, string(cat))
	}
	sort.Strings(categories)

	for _, catStr := range categories {
		cat := timesheet.SemanticCategory(catStr)
		mins := categoryMinutes[cat]
		pct := 0
		if totalMinutes > 0 {
			pct = (mins * 100) / totalMinutes
		}
		catBreakdown = append(catBreakdown, fmt.Sprintf("%s: %.2fh (%d%%)", cat, float64(mins)/60.0, pct))
	}
	fmt.Fprintf(out, "Breakdown: %s\n", strings.Join(catBreakdown, ", "))
}

func interactiveEditLoop(
	targetDate string,
	blocks []timesheet.TimeBlock,
	reader *bufio.Reader,
	out io.Writer,
) ([]timesheet.TimeBlock, bool, error) {
	currentBlocks := make([]timesheet.TimeBlock, len(blocks))
	copy(currentBlocks, blocks)

	for {
		fmt.Fprintln(out, "\nActions:")
		fmt.Fprintln(out, "  [e <#>] Edit block (e.g. 'e 1')")
		fmt.Fprintln(out, "  [a]     Add manual block")
		fmt.Fprintln(out, "  [d <#>] Delete block (e.g. 'd 2')")
		fmt.Fprintln(out, "  [s]     Save and persist")
		fmt.Fprintln(out, "  [q]     Quit without saving")
		fmt.Fprint(out, "Enter action: ")

		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return currentBlocks, true, nil
			}
			return nil, false, err
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		action := strings.ToLower(parts[0])

		switch action {
		case "s":
			return currentBlocks, true, nil
		case "q":
			return currentBlocks, false, nil

		case "d":
			if len(parts) < 2 {
				fmt.Fprintln(out, "Usage: d <block_number>")
				continue
			}
			idx, pErr := strconv.Atoi(parts[1])
			if pErr != nil || idx < 1 || idx > len(currentBlocks) {
				fmt.Fprintf(out, "Invalid block number: must be between 1 and %d\n", len(currentBlocks))
				continue
			}
			removed := currentBlocks[idx-1]
			currentBlocks = append(currentBlocks[:idx-1], currentBlocks[idx:]...)
			fmt.Fprintf(out, "Deleted block #%d (%s).\n", idx, removed.Title)
			renderScheduleTable(out, currentBlocks)

		case "e":
			if len(parts) < 2 {
				fmt.Fprintln(out, "Usage: e <block_number>")
				continue
			}
			idx, pErr := strconv.Atoi(parts[1])
			if pErr != nil || idx < 1 || idx > len(currentBlocks) {
				fmt.Fprintf(out, "Invalid block number: must be between 1 and %d\n", len(currentBlocks))
				continue
			}
			b := &currentBlocks[idx-1]

			fmt.Fprintf(out, "Start time [HH:MM] (%s): ", b.StartTime)
			newStart, _ := reader.ReadString('\n')
			if trimmed := strings.TrimSpace(newStart); trimmed != "" {
				if timePattern.MatchString(trimmed) {
					b.StartTime = trimmed
				} else {
					fmt.Fprintln(out, "Invalid time format, keeping original.")
				}
			}

			fmt.Fprintf(out, "End time [HH:MM] (%s): ", b.EndTime)
			newEnd, _ := reader.ReadString('\n')
			if trimmed := strings.TrimSpace(newEnd); trimmed != "" {
				if timePattern.MatchString(trimmed) {
					b.EndTime = trimmed
				} else {
					fmt.Fprintln(out, "Invalid time format, keeping original.")
				}
			}

			fmt.Fprintf(out, "Category [development/maintenance/research_docs/meeting/other] (%s): ", b.Category)
			newCat, _ := reader.ReadString('\n')
			if trimmed := strings.TrimSpace(newCat); trimmed != "" {
				b.Category = timesheet.SemanticCategory(trimmed)
			}

			fmt.Fprintf(out, "Title (%s): ", b.Title)
			newTitle, _ := reader.ReadString('\n')
			if trimmed := strings.TrimSpace(newTitle); trimmed != "" {
				b.Title = trimmed
			}

			sortBlocksChronologically(currentBlocks)
			renderScheduleTable(out, currentBlocks)

		case "a":
			fmt.Fprint(out, "Start time [HH:MM]: ")
			stInput, _ := reader.ReadString('\n')
			st := strings.TrimSpace(stInput)
			if !timePattern.MatchString(st) {
				fmt.Fprintln(out, "Invalid start time format (expected HH:MM).")
				continue
			}

			fmt.Fprint(out, "End time [HH:MM]: ")
			etInput, _ := reader.ReadString('\n')
			et := strings.TrimSpace(etInput)
			if !timePattern.MatchString(et) {
				fmt.Fprintln(out, "Invalid end time format (expected HH:MM).")
				continue
			}

			fmt.Fprint(out, "Category [development/maintenance/research_docs/meeting/other] (default: development): ")
			catInput, _ := reader.ReadString('\n')
			cat := strings.TrimSpace(catInput)
			if cat == "" {
				cat = string(timesheet.CategoryDevelopment)
			}

			fmt.Fprint(out, "Title: ")
			titleInput, _ := reader.ReadString('\n')
			title := strings.TrimSpace(titleInput)
			if title == "" {
				title = "Manual work log"
			}

			fmt.Fprint(out, "Description: ")
			descInput, _ := reader.ReadString('\n')
			desc := strings.TrimSpace(descInput)

			newBlock := timesheet.TimeBlock{
				ID:           fmt.Sprintf("manual-%s-%s-%s", targetDate, strings.ReplaceAll(st, ":", ""), strings.ReplaceAll(et, ":", "")),
				Source:       "manual",
				Category:     timesheet.SemanticCategory(cat),
				StartTime:    st,
				EndTime:      et,
				Date:         targetDate,
				Title:        title,
				Description:  desc,
				HasCollision: false,
			}
			currentBlocks = append(currentBlocks, newBlock)
			sortBlocksChronologically(currentBlocks)
			fmt.Fprintln(out, "Added manual block.")
			renderScheduleTable(out, currentBlocks)

		default:
			fmt.Fprintf(out, "Unknown action %q. Type 's' to save or 'q' to quit.\n", action)
		}
	}
}

func calculateBlockDurationMinutes(b timesheet.TimeBlock) int {
	st, err1 := time.Parse("15:04", b.StartTime)
	et, err2 := time.Parse("15:04", b.EndTime)
	if err1 != nil || err2 != nil {
		return 30
	}
	if !et.After(st) {
		et = et.Add(24 * time.Hour)
	}
	return int(et.Sub(st).Minutes())
}

func calculateTotalHours(blocks []timesheet.TimeBlock) float64 {
	totalMinutes := 0
	for _, b := range blocks {
		totalMinutes += calculateBlockDurationMinutes(b)
	}
	return float64(totalMinutes) / 60.0
}

func sortBlocksChronologically(blocks []timesheet.TimeBlock) {
	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].StartTime < blocks[j].StartTime
	})
}

func dispatchSinks(ctx context.Context, record storage.TimesheetRecord, sinkType string, out io.Writer, errOut io.Writer) {
	sinkType = strings.ToLower(strings.TrimSpace(sinkType))
	if sinkType == "none" {
		return
	}

	if sinkType == "console" || sinkType == "all" {
		consoleSink := storage.NewConsoleSink(out)
		_ = consoleSink.Export(ctx, record)
	}

	if sinkType == "obsidian" || sinkType == "all" {
		vaultDir := strings.TrimSpace(os.Getenv("OBSIDIAN_VAULT_DIR"))
		if vaultDir == "" {
			home, _ := os.UserHomeDir()
			vaultDir = filepath.Join(home, "ai-database")
		}
		obsidianSink, oErr := storage.NewObsidianSink(vaultDir)
		if oErr != nil {
			fmt.Fprintf(errOut, "warning: initializing obsidian sink: %v\n", oErr)
		} else if expErr := obsidianSink.Export(ctx, record); expErr != nil {
			fmt.Fprintf(errOut, "warning: obsidian sink export: %v\n", expErr)
		} else {
			fmt.Fprintf(out, "Successfully exported timesheet table to Obsidian Daily Log (%s)\n", record.Date)
		}
	}
}
