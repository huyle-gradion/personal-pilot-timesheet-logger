package timesheet

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	MaxEntriesPerDayCount   = 200
	MaxTimeBlocksCount      = 50
	MinClumpDurationMinutes = 30
	GridAlignmentMinutes    = 15
	DefaultCommitGapMinutes = 45
	MaxClumpingIterations   = 100

	// Backward-compatibility aliases
	MinClumpDurationMins = MinClumpDurationMinutes
	DefaultCommitGapMins = DefaultCommitGapMinutes
)

const (
	AssertTimesheetContextNil = "ASSERT_TIMESHEET_001_NIL_CONTEXT"
	AssertTimesheetDateZero   = "ASSERT_TIMESHEET_002_ZERO_TARGET_DATE"
)

var (
	ErrExcessiveEntries = errors.New("capacity exceeded: entries count exceeds maximum per day")
	ErrExcessiveBlocks  = errors.New("capacity exceeded: time blocks count exceeds maximum per day")
	ErrInvalidTimestamp = errors.New("invalid timestamp format: unable to parse timestamp")
)

// Assert terminates the process on violated internal invariants per TigerStyle.
func Assert(condition bool, invariantID string) {
	if !condition {
		panic(invariantID)
	}
}

type TimeBlock struct {
	ID           string           `json:"id"`
	Source       string           `json:"source"`
	Category     SemanticCategory `json:"category"`
	StartTime    string           `json:"startTime"`
	EndTime      string           `json:"endTime"`
	Date         string           `json:"date"`
	Title        string           `json:"title"`
	Description  string           `json:"description"`
	Entries      []SimpleEntry    `json:"entries"`
	HasCollision bool             `json:"collision"`
}

type ClusterOptions struct {
	Location            *time.Location
	CoreHoursStart      string
	CoreHoursEnd        string
	LunchStart          string
	LunchEnd            string
	MinBlockMinutes     int
	RoundGridMinutes    int
	MaxCommitGapMinutes int
}

// IsCoreHours returns true if the given HH:MM time falls within the configured CoreHoursStart and CoreHoursEnd.
func (opts ClusterOptions) IsCoreHours(timeStr string) bool {
	start := opts.CoreHoursStart
	if start == "" {
		start = "09:00"
	}
	end := opts.CoreHoursEnd
	if end == "" {
		end = "18:00"
	}
	return timeStr >= start && timeStr <= end
}

// IsLunchHours returns true if the given HH:MM time falls within the configured LunchStart and LunchEnd.
func (opts ClusterOptions) IsLunchHours(timeStr string) bool {
	start := opts.LunchStart
	if start == "" {
		start = "12:00"
	}
	end := opts.LunchEnd
	if end == "" {
		end = "13:00"
	}
	return timeStr >= start && timeStr < end
}

type parsedItem struct {
	entry     SimpleEntry
	startTime time.Time
	endTime   time.Time
}

type rawCluster struct {
	category  SemanticCategory
	startTime time.Time
	endTime   time.Time
	entries   []SimpleEntry
}

// ClusterEntries clusters and normalizes calendar and git entries into non-overlapping or flagged focus blocks.
func ClusterEntries(ctx context.Context, entries []SimpleEntry, targetDate time.Time, opts ClusterOptions) ([]TimeBlock, error) {
	Assert(ctx != nil, AssertTimesheetContextNil)
	Assert(!targetDate.IsZero(), AssertTimesheetDateZero)

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if len(entries) > MaxEntriesPerDayCount {
		return nil, ErrExcessiveEntries
	}

	if len(entries) == 0 {
		return []TimeBlock{}, nil
	}

	loc := opts.Location
	if loc == nil {
		loc = time.Local
	}
	minBlockMinutes := opts.MinBlockMinutes
	if minBlockMinutes <= 0 {
		minBlockMinutes = MinClumpDurationMinutes
	}
	roundGridMinutes := opts.RoundGridMinutes
	if roundGridMinutes <= 0 {
		roundGridMinutes = GridAlignmentMinutes
	}
	maxCommitGapMinutes := opts.MaxCommitGapMinutes
	if maxCommitGapMinutes <= 0 {
		maxCommitGapMinutes = DefaultCommitGapMinutes
	}
	if opts.CoreHoursStart == "" {
		opts.CoreHoursStart = "09:00"
	}
	if opts.CoreHoursEnd == "" {
		opts.CoreHoursEnd = "18:00"
	}
	if opts.LunchStart == "" {
		opts.LunchStart = "12:00"
	}
	if opts.LunchEnd == "" {
		opts.LunchEnd = "13:00"
	}

	targetDateStr := targetDate.In(loc).Format("2006-01-02")
	calendarItems, gitItems, err := parseAndFilterEntries(entries, targetDateStr, loc)
	if err != nil {
		return nil, err
	}

	calendarBlocks := createCalendarBlocks(calendarItems, targetDateStr, loc)
	rawGitClusters := clusterGitItems(gitItems, maxCommitGapMinutes)
	mergedClusters := mergeMicroClusters(rawGitClusters, minBlockMinutes)
	gitBlocks := buildGitBlocks(mergedClusters, targetDateStr, minBlockMinutes, roundGridMinutes)

	resolvedGitBlocks, err := detectCollisions(gitBlocks, calendarBlocks, loc)
	if err != nil {
		return nil, err
	}

	return combineAndSortBlocks(calendarBlocks, resolvedGitBlocks)
}

func parseAndFilterEntries(entries []SimpleEntry, targetDateStr string, loc *time.Location) ([]parsedItem, []parsedItem, error) {
	var calendarItems []parsedItem
	var gitItems []parsedItem

	for _, entry := range entries {
		st, err := parseTimestamp(entry.Timestamp, loc)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse entry start timestamp: %w", err)
		}
		if st.In(loc).Format("2006-01-02") != targetDateStr {
			continue
		}
		et := st
		if entry.EndTime != "" {
			parsedEnd, err := parseTimestamp(entry.EndTime, loc)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to parse entry end timestamp: %w", err)
			}
			et = parsedEnd
		} else if entry.Metadata != nil && entry.Metadata.DurationMinutes > 0 {
			et = st.Add(time.Duration(entry.Metadata.DurationMinutes) * time.Minute)
		}

		if entry.Source == "calendar" {
			if entry.Metadata != nil && entry.Metadata.Transparent {
				continue
			}
			calendarItems = append(calendarItems, parsedItem{entry: entry, startTime: st, endTime: et})
		} else if entry.Source == "git" {
			gitItems = append(gitItems, parsedItem{entry: entry, startTime: st, endTime: et})
		}
	}

	sort.Slice(calendarItems, func(i, j int) bool { return calendarItems[i].startTime.Before(calendarItems[j].startTime) })
	sort.Slice(gitItems, func(i, j int) bool { return gitItems[i].startTime.Before(gitItems[j].startTime) })

	return calendarItems, gitItems, nil
}

func createCalendarBlocks(items []parsedItem, targetDateStr string, loc *time.Location) []TimeBlock {
	var calendarBlocks []TimeBlock
	for _, ci := range items {
		calendarBlocks = append(calendarBlocks, TimeBlock{
			ID:           ci.entry.ID,
			Source:       "calendar",
			Category:     CategoryMeeting,
			StartTime:    ci.startTime.In(loc).Format("15:04"),
			EndTime:      ci.endTime.In(loc).Format("15:04"),
			Date:         targetDateStr,
			Title:        ci.entry.Title,
			Description:  ci.entry.Description,
			Entries:      []SimpleEntry{ci.entry},
			HasCollision: false,
		})
	}
	return calendarBlocks
}

func clusterGitItems(items []parsedItem, maxCommitGapMinutes int) []rawCluster {
	var gitClusters []rawCluster
	for _, gi := range items {
		cat := ClassifyEntry(gi.entry)
		if len(gitClusters) == 0 {
			gitClusters = append(gitClusters, rawCluster{
				category:  cat,
				startTime: gi.startTime,
				endTime:   gi.endTime,
				entries:   []SimpleEntry{gi.entry},
			})
			continue
		}
		lastIdx := len(gitClusters) - 1
		gap := gi.startTime.Sub(gitClusters[lastIdx].endTime)
		if gap < 0 {
			gap = 0
		}
		if AreCategoriesCompatible(gitClusters[lastIdx].category, cat) && gap <= time.Duration(maxCommitGapMinutes)*time.Minute {
			gitClusters[lastIdx].category = mergeCategories(gitClusters[lastIdx].category, cat)
			if gi.endTime.After(gitClusters[lastIdx].endTime) {
				gitClusters[lastIdx].endTime = gi.endTime
			}
			gitClusters[lastIdx].entries = append(gitClusters[lastIdx].entries, gi.entry)
		} else {
			gitClusters = append(gitClusters, rawCluster{
				category:  cat,
				startTime: gi.startTime,
				endTime:   gi.endTime,
				entries:   []SimpleEntry{gi.entry},
			})
		}
	}
	return gitClusters
}

func mergeMicroClusters(gitClusters []rawCluster, minBlockMinutes int) []rawCluster {
	changed := true
	for iter := 0; changed && iter < MaxClumpingIterations; iter++ {
		changed = false
		var merged []rawCluster
		i := 0
		for i < len(gitClusters) {
			if i+1 < len(gitClusters) && AreCategoriesCompatible(gitClusters[i].category, gitClusters[i+1].category) {
				gap := gitClusters[i+1].startTime.Sub(gitClusters[i].endTime)
				dur1 := gitClusters[i].endTime.Sub(gitClusters[i].startTime)
				dur2 := gitClusters[i+1].endTime.Sub(gitClusters[i+1].startTime)
				isMicro1 := dur1 < time.Duration(minBlockMinutes)*time.Minute
				isMicro2 := dur2 < time.Duration(minBlockMinutes)*time.Minute

				if gap <= 60*time.Minute && (isMicro1 || isMicro2) {
					combinedEntries := make([]SimpleEntry, 0, len(gitClusters[i].entries)+len(gitClusters[i+1].entries))
					combinedEntries = append(combinedEntries, gitClusters[i].entries...)
					combinedEntries = append(combinedEntries, gitClusters[i+1].entries...)

					merged = append(merged, rawCluster{
						category:  mergeCategories(gitClusters[i].category, gitClusters[i+1].category),
						startTime: gitClusters[i].startTime,
						endTime:   gitClusters[i+1].endTime,
						entries:   combinedEntries,
					})
					i += 2
					changed = true
					continue
				}
			}
			merged = append(merged, gitClusters[i])
			i++
		}
		gitClusters = merged
	}
	return gitClusters
}

func buildGitBlocks(clusters []rawCluster, targetDateStr string, minBlockMinutes, roundGridMinutes int) []TimeBlock {
	var gitBlocks []TimeBlock
	for _, gc := range clusters {
		if len(gc.entries) == 0 {
			continue
		}
		snappedStart, snappedEnd := snapToGrid(gc.startTime, gc.endTime, minBlockMinutes, roundGridMinutes)
		var bullets []string
		seen := make(map[string]bool)
		var prNums []int
		seenPR := make(map[int]bool)

		for _, e := range gc.entries {
			trimmed := strings.TrimSpace(e.Title)
			if trimmed != "" && !seen[trimmed] {
				seen[trimmed] = true
				bullets = append(bullets, "- "+trimmed)
			}
			if e.Metadata != nil && e.Metadata.PRNumber > 0 {
				if !seenPR[e.Metadata.PRNumber] {
					seenPR[e.Metadata.PRNumber] = true
					prNums = append(prNums, e.Metadata.PRNumber)
				}
			}
		}

		blockTitle := gc.entries[0].Title
		for _, e := range gc.entries {
			if strings.TrimSpace(e.Title) != "" {
				blockTitle = e.Title
				break
			}
		}

		desc := strings.Join(bullets, "\n")
		if len(prNums) > 0 {
			sort.Slice(prNums, func(i, j int) bool { return prNums[i] > prNums[j] })
			var prParts []string
			for _, pr := range prNums {
				prParts = append(prParts, fmt.Sprintf("#%d", pr))
			}
			prLine := "PRs: " + strings.Join(prParts, ", ")
			if desc != "" {
				desc = desc + "\n" + prLine
			} else {
				desc = prLine
			}
		}

		blockID := fmt.Sprintf("git-%s-%s-%s-%s", targetDateStr, gc.category, snappedStart.Format("1504"), snappedEnd.Format("1504"))
		gitBlocks = append(gitBlocks, TimeBlock{
			ID:           blockID,
			Source:       "git",
			Category:     gc.category,
			StartTime:    snappedStart.Format("15:04"),
			EndTime:      snappedEnd.Format("15:04"),
			Date:         targetDateStr,
			Title:        blockTitle,
			Description:  desc,
			Entries:      gc.entries,
			HasCollision: false,
		})
	}
	return gitBlocks
}

func parseBlockTimes(b TimeBlock, loc *time.Location) (time.Time, time.Time, error) {
	st, err := time.ParseInLocation("2006-01-02 15:04", b.Date+" "+b.StartTime, loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	et, err := time.ParseInLocation("2006-01-02 15:04", b.Date+" "+b.EndTime, loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if !et.After(st) {
		et = et.Add(24 * time.Hour)
	}
	return st, et, nil
}

func detectCollisions(gitBlocks, calendarBlocks []TimeBlock, loc *time.Location) ([]TimeBlock, error) {
	calIntervals := make([]struct{ start, end time.Time }, len(calendarBlocks))
	for i, cb := range calendarBlocks {
		st, et, err := parseBlockTimes(cb, loc)
		if err != nil {
			return nil, err
		}
		calIntervals[i] = struct{ start, end time.Time }{start: st, end: et}
	}

	gitIntervals := make([]struct{ start, end time.Time }, len(gitBlocks))
	for i, gb := range gitBlocks {
		st, et, err := parseBlockTimes(gb, loc)
		if err != nil {
			return nil, err
		}
		gitIntervals[i] = struct{ start, end time.Time }{start: st, end: et}
	}

	// 1. Check Git blocks against Calendar blocks
	for i := range gitBlocks {
		gStart := gitIntervals[i].start
		gEnd := gitIntervals[i].end
		for _, cal := range calIntervals {
			if gStart.Before(cal.end) && cal.start.Before(gEnd) {
				gitBlocks[i].HasCollision = true
				break
			}
		}
	}

	// 2. Check Git blocks against other Git blocks
	for i := 0; i < len(gitBlocks); i++ {
		for j := i + 1; j < len(gitBlocks); j++ {
			g1Start := gitIntervals[i].start
			g1End := gitIntervals[i].end
			g2Start := gitIntervals[j].start
			g2End := gitIntervals[j].end
			if g1Start.Before(g2End) && g2Start.Before(g1End) {
				gitBlocks[i].HasCollision = true
				gitBlocks[j].HasCollision = true
			}
		}
	}

	return gitBlocks, nil
}

func combineAndSortBlocks(calendarBlocks, gitBlocks []TimeBlock) ([]TimeBlock, error) {
	allBlocks := append(calendarBlocks, gitBlocks...)
	sort.Slice(allBlocks, func(i, j int) bool {
		if allBlocks[i].StartTime == allBlocks[j].StartTime {
			return allBlocks[i].Source == "calendar"
		}
		return allBlocks[i].StartTime < allBlocks[j].StartTime
	})

	if len(allBlocks) > MaxTimeBlocksCount {
		return nil, ErrExcessiveBlocks
	}
	return allBlocks, nil
}

func snapToGrid(start, end time.Time, minDurationMinutes, gridMinutes int) (time.Time, time.Time) {
	startMinute := start.Minute()
	snappedStartMinute := (startMinute / gridMinutes) * gridMinutes
	snappedStart := time.Date(start.Year(), start.Month(), start.Day(), start.Hour(), snappedStartMinute, 0, 0, start.Location())

	duration := end.Sub(snappedStart)
	minDuration := time.Duration(minDurationMinutes) * time.Minute

	var snappedEnd time.Time
	if duration <= minDuration {
		targetEnd := snappedStart.Add(minDuration)
		endMinute := targetEnd.Minute()
		rem := endMinute % gridMinutes
		if rem != 0 || targetEnd.Second() != 0 || targetEnd.Nanosecond() != 0 {
			targetEnd = targetEnd.Add(time.Duration(gridMinutes-rem) * time.Minute).Truncate(time.Minute)
		}
		snappedEnd = targetEnd
	} else {
		endMinute := end.Minute()
		rem := endMinute % gridMinutes
		var snappedEndMinute int
		if rem == 0 && end.Second() == 0 && end.Nanosecond() == 0 {
			snappedEndMinute = endMinute
		} else {
			snappedEndMinute = ((endMinute / gridMinutes) + 1) * gridMinutes
		}
		snappedEnd = time.Date(end.Year(), end.Month(), end.Day(), end.Hour(), 0, 0, 0, end.Location()).Add(time.Duration(snappedEndMinute) * time.Minute)
		if snappedEnd.Sub(snappedStart) < minDuration {
			targetEnd := snappedStart.Add(minDuration)
			endMinute := targetEnd.Minute()
			rem := endMinute % gridMinutes
			if rem != 0 || targetEnd.Second() != 0 || targetEnd.Nanosecond() != 0 {
				targetEnd = targetEnd.Add(time.Duration(gridMinutes-rem) * time.Minute).Truncate(time.Minute)
			}
			snappedEnd = targetEnd
		}
	}
	return snappedStart, snappedEnd
}

func parseTimestamp(ts string, loc *time.Location) (time.Time, error) {
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, f := range formats {
		if t, err := time.Parse(f, ts); err == nil {
			return t.In(loc), nil
		}
		if t, err := time.ParseInLocation(f, ts, loc); err == nil {
			return t.In(loc), nil
		}
	}
	return time.Time{}, ErrInvalidTimestamp
}
