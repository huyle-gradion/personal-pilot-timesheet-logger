package timesheet

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	MaxEntriesPerDayCount = 200
	MaxTimeBlocksCount    = 50
	MinClumpDurationMins  = 30
	GridAlignmentMinutes  = 15
	DefaultCommitGapMins  = 45
)

const (
	AssertTimesheetContextNil   = "ASSERT_TIMESHEET_001_NIL_CONTEXT"
	AssertTimesheetDateZero     = "ASSERT_TIMESHEET_002_ZERO_TARGET_DATE"
	AssertTimesheetExcessData   = "ASSERT_TIMESHEET_003_EXCESSIVE_ENTRIES"
	AssertTimesheetExcessBlocks = "ASSERT_TIMESHEET_004_EXCESSIVE_BLOCKS"
)

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
	Location         *time.Location
	CoreHoursStart   string
	CoreHoursEnd     string
	LunchStart       string
	LunchEnd         string
	MinBlockMinutes  int
	RoundGridMinutes int
	MaxCommitGapMins int
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

func ClusterEntries(ctx context.Context, entries []SimpleEntry, targetDate time.Time, opts ClusterOptions) ([]TimeBlock, error) {
	Assert(ctx != nil, AssertTimesheetContextNil)
	Assert(!targetDate.IsZero(), AssertTimesheetDateZero)

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	Assert(len(entries) <= MaxEntriesPerDayCount, AssertTimesheetExcessData)

	if len(entries) == 0 {
		return []TimeBlock{}, nil
	}

	loc := opts.Location
	if loc == nil {
		loc = time.Local
	}
	minBlockMinutes := opts.MinBlockMinutes
	if minBlockMinutes <= 0 {
		minBlockMinutes = MinClumpDurationMins
	}
	roundGridMinutes := opts.RoundGridMinutes
	if roundGridMinutes <= 0 {
		roundGridMinutes = GridAlignmentMinutes
	}
	maxCommitGapMins := opts.MaxCommitGapMins
	if maxCommitGapMins <= 0 {
		maxCommitGapMins = DefaultCommitGapMins
	}

	targetDateStr := targetDate.In(loc).Format("2006-01-02")
	var calendarItems []parsedItem
	var gitItems []parsedItem

	for _, entry := range entries {
		st, err := parseTimestamp(entry.Timestamp, loc)
		if err != nil {
			continue
		}
		if st.In(loc).Format("2006-01-02") != targetDateStr {
			continue
		}
		et := st
		if entry.EndTime != "" {
			if parsedEnd, err := parseTimestamp(entry.EndTime, loc); err == nil {
				et = parsedEnd
			}
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

	var calendarBlocks []TimeBlock
	for _, ci := range calendarItems {
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

	var gitClusters []rawCluster
	for _, gi := range gitItems {
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
		if gitClusters[lastIdx].category == cat && gap <= time.Duration(maxCommitGapMins)*time.Minute {
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

	changed := true
	for changed {
		changed = false
		var merged []rawCluster
		i := 0
		for i < len(gitClusters) {
			if i+1 < len(gitClusters) && gitClusters[i].category == gitClusters[i+1].category {
				gap := gitClusters[i+1].startTime.Sub(gitClusters[i].endTime)
				dur1 := gitClusters[i].endTime.Sub(gitClusters[i].startTime)
				dur2 := gitClusters[i+1].endTime.Sub(gitClusters[i+1].startTime)
				isMicro1 := dur1 < time.Duration(minBlockMinutes)*time.Minute
				isMicro2 := dur2 < time.Duration(minBlockMinutes)*time.Minute

				if gap <= 60*time.Minute && (isMicro1 || isMicro2) {
					merged = append(merged, rawCluster{
						category:  gitClusters[i].category,
						startTime: gitClusters[i].startTime,
						endTime:   gitClusters[i+1].endTime,
						entries:   append(gitClusters[i].entries, gitClusters[i+1].entries...),
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

	var gitBlocks []TimeBlock
	for _, gc := range gitClusters {
		snappedStart, snappedEnd := snapToGrid(gc.startTime, gc.endTime, minBlockMinutes, roundGridMinutes)
		var bullets []string
		seen := make(map[string]bool)
		for _, e := range gc.entries {
			trimmed := strings.TrimSpace(e.Title)
			if trimmed != "" && !seen[trimmed] {
				seen[trimmed] = true
				bullets = append(bullets, "- "+trimmed)
			}
		}
		blockTitle := gc.entries[0].Title
		for _, e := range gc.entries {
			if strings.TrimSpace(e.Title) != "" {
				blockTitle = e.Title
				break
			}
		}
		gitBlocks = append(gitBlocks, TimeBlock{
			ID:           fmt.Sprintf("git-%s-%s-%s", gc.category, snappedStart.Format("1504"), snappedEnd.Format("1504")),
			Source:       "git",
			Category:     gc.category,
			StartTime:    snappedStart.Format("15:04"),
			EndTime:      snappedEnd.Format("15:04"),
			Date:         targetDateStr,
			Title:        blockTitle,
			Description:  strings.Join(bullets, "\n"),
			Entries:      gc.entries,
			HasCollision: false,
		})
	}

	for i := range gitBlocks {
		gStart, _ := time.ParseInLocation("15:04", gitBlocks[i].StartTime, loc)
		gEnd, _ := time.ParseInLocation("15:04", gitBlocks[i].EndTime, loc)
		for _, cb := range calendarBlocks {
			cStart, _ := time.ParseInLocation("15:04", cb.StartTime, loc)
			cEnd, _ := time.ParseInLocation("15:04", cb.EndTime, loc)
			if gStart.Before(cEnd) && cStart.Before(gEnd) {
				gitBlocks[i].HasCollision = true
				break
			}
		}
	}

	allBlocks := append(calendarBlocks, gitBlocks...)
	sort.Slice(allBlocks, func(i, j int) bool {
		if allBlocks[i].StartTime == allBlocks[j].StartTime {
			return allBlocks[i].Source == "calendar"
		}
		return allBlocks[i].StartTime < allBlocks[j].StartTime
	})

	Assert(len(allBlocks) <= MaxTimeBlocksCount, AssertTimesheetExcessBlocks)
	return allBlocks, nil
}

func snapToGrid(start, end time.Time, minDurationMins, gridMinutes int) (time.Time, time.Time) {
	startMinute := start.Minute()
	snappedStartMinute := (startMinute / gridMinutes) * gridMinutes
	snappedStart := time.Date(start.Year(), start.Month(), start.Day(), start.Hour(), snappedStartMinute, 0, 0, start.Location())

	duration := end.Sub(snappedStart)
	minDuration := time.Duration(minDurationMins) * time.Minute

	var snappedEnd time.Time
	if duration <= minDuration {
		snappedEnd = snappedStart.Add(minDuration)
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
			snappedEnd = snappedStart.Add(minDuration)
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
	return time.Time{}, fmt.Errorf("invalid timestamp format: %s", ts)
}
