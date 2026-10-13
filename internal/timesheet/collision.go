package timesheet

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Named constants and TigerStyle bounds.
const (
	MaxPromptRetriesCount    = 3
	MaxCollisionsCount       = 25
	MaxReviewTableWidthBytes = 120
	ReviewCommandTimeoutSecs = 60 * time.Second
)

// Invariant assertion identifiers.
const (
	AssertReviewContextNil  = "ASSERT_REVIEW_001_NIL_CONTEXT"
	AssertReviewEmptyBlocks = "ASSERT_REVIEW_002_EMPTY_BLOCKS"
)

// Typed operating errors.
var (
	ErrExcessiveCollisions = errors.New("capacity exceeded: collisions count exceeds maximum allowed")
	ErrInvalidResolution   = errors.New("invalid collision resolution strategy")
	ErrCollisionNotFound   = errors.New("collision not found for specified block")
)

// CollisionResolution defines the strategy for resolving overlaps.
type CollisionResolution string

const (
	ResolutionKeepBoth CollisionResolution = "keep_both"
	ResolutionSeparate CollisionResolution = "separate"
	ResolutionDiscard  CollisionResolution = "discard"
)

// Collision represents a detected interval overlap between a calendar meeting and a git block.
type Collision struct {
	BlockID          string              `json:"blockId"`
	MeetingID        string              `json:"meetingId,omitempty"`
	MeetingTitle     string              `json:"meetingTitle"`
	MeetingStartTime string              `json:"meetingStartTime,omitempty"`
	MeetingEndTime   string              `json:"meetingEndTime,omitempty"`
	GitID            string              `json:"gitId,omitempty"`
	GitTitle         string              `json:"gitTitle"`
	GitStartTime     string              `json:"gitStartTime,omitempty"`
	GitEndTime       string              `json:"gitEndTime,omitempty"`
	StartTime        string              `json:"startTime"` // Git block start time
	EndTime          string              `json:"endTime"`   // Git block end time
	Resolution       CollisionResolution `json:"resolution,omitempty"`
}

// DetectCollisions finds all interval overlaps between calendar meetings and git activity blocks.
func DetectCollisions(ctx context.Context, blocks []TimeBlock) ([]Collision, error) {
	Assert(ctx != nil, AssertReviewContextNil)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if len(blocks) == 0 {
		return []Collision{}, nil
	}

	var calendarBlocks []TimeBlock
	var gitBlocks []TimeBlock
	for _, b := range blocks {
		if strings.EqualFold(b.Source, "calendar") {
			calendarBlocks = append(calendarBlocks, b)
		} else if strings.EqualFold(b.Source, "git") {
			gitBlocks = append(gitBlocks, b)
		}
	}

	if len(calendarBlocks) == 0 || len(gitBlocks) == 0 {
		return []Collision{}, nil
	}

	type interval struct {
		block TimeBlock
		start time.Time
		end   time.Time
	}

	calIntervals := make([]interval, 0, len(calendarBlocks))
	for _, cb := range calendarBlocks {
		st, et, err := parseBlockTimes(cb, time.UTC)
		if err != nil {
			return nil, err
		}
		calIntervals = append(calIntervals, interval{block: cb, start: st, end: et})
	}

	gitIntervals := make([]interval, 0, len(gitBlocks))
	for _, gb := range gitBlocks {
		st, et, err := parseBlockTimes(gb, time.UTC)
		if err != nil {
			return nil, err
		}
		gitIntervals = append(gitIntervals, interval{block: gb, start: st, end: et})
	}

	var collisions []Collision
	for _, gi := range gitIntervals {
		for _, ci := range calIntervals {
			// Check if Git interval overlaps with Calendar interval:
			// max(startGit, startCal) < min(endGit, endCal)
			if gi.start.Before(ci.end) && ci.start.Before(gi.end) {
				if len(collisions) >= MaxCollisionsCount {
					return nil, ErrExcessiveCollisions
				}
				collisions = append(collisions, Collision{
					BlockID:          gi.block.ID,
					MeetingID:        ci.block.ID,
					MeetingTitle:     ci.block.Title,
					MeetingStartTime: ci.block.StartTime,
					MeetingEndTime:   ci.block.EndTime,
					GitID:            gi.block.ID,
					GitTitle:         gi.block.Title,
					GitStartTime:     gi.block.StartTime,
					GitEndTime:       gi.block.EndTime,
					StartTime:        gi.block.StartTime,
					EndTime:          gi.block.EndTime,
				})
			}
		}
	}

	return collisions, nil
}

// ResolveCollision applies the specified resolution strategy to a colliding meeting and git block pair.
func ResolveCollision(meeting TimeBlock, gitBlock TimeBlock, res CollisionResolution) ([]TimeBlock, error) {
	// Defensively normalize order if passed in reverse
	if strings.EqualFold(meeting.Source, "git") && strings.EqualFold(gitBlock.Source, "calendar") {
		meeting, gitBlock = gitBlock, meeting
	}

	switch res {
	case ResolutionKeepBoth:
		stMeeting, etMeeting, err := parseBlockTimes(meeting, time.UTC)
		if err != nil {
			return nil, err
		}
		stGit, etGit, err := parseBlockTimes(gitBlock, time.UTC)
		if err != nil {
			return nil, err
		}

		stJoint := stMeeting
		if stGit.Before(stJoint) {
			stJoint = stGit
		}
		etJoint := etMeeting
		if etGit.After(etJoint) {
			etJoint = etGit
		}

		dateStr := meeting.Date
		if dateStr == "" {
			dateStr = gitBlock.Date
		}

		title := meeting.Title
		if gitBlock.Title != "" {
			title = fmt.Sprintf("%s & %s", meeting.Title, gitBlock.Title)
		}

		var descParts []string
		if strings.TrimSpace(meeting.Description) != "" {
			descParts = append(descParts, meeting.Description)
		}
		if strings.TrimSpace(gitBlock.Description) != "" {
			descParts = append(descParts, gitBlock.Description)
		}
		combinedDesc := strings.Join(descParts, "\n")

		combinedEntries := make([]SimpleEntry, 0, len(meeting.Entries)+len(gitBlock.Entries))
		combinedEntries = append(combinedEntries, meeting.Entries...)
		combinedEntries = append(combinedEntries, gitBlock.Entries...)

		jointBlock := TimeBlock{
			ID:           fmt.Sprintf("joint-%s-%s-%s", dateStr, stJoint.Format("1504"), etJoint.Format("1504")),
			Source:       "joint",
			Category:     meeting.Category,
			StartTime:    stJoint.Format("15:04"),
			EndTime:      etJoint.Format("15:04"),
			Date:         dateStr,
			Title:        title,
			Description:  combinedDesc,
			Entries:      combinedEntries,
			HasCollision: false,
		}
		return []TimeBlock{jointBlock}, nil

	case ResolutionSeparate:
		meetingCopy := meeting
		meetingCopy.HasCollision = false

		gitCopy := gitBlock
		gitCopy.HasCollision = false

		stGit, etGit, err := parseBlockTimes(gitBlock, time.UTC)
		if err != nil {
			return nil, err
		}
		_, etMeeting, err := parseBlockTimes(meeting, time.UTC)
		if err != nil {
			return nil, err
		}

		dur := etGit.Sub(stGit)
		if dur <= 0 {
			dur = time.Duration(MinClumpDurationMinutes) * time.Minute
		}

		// Shift git block to start at meeting end time
		newStart := etMeeting
		// Snap to 15-minute grid if required
		newEnd := newStart.Add(dur)

		gitCopy.StartTime = newStart.Format("15:04")
		gitCopy.EndTime = newEnd.Format("15:04")
		gitCopy.ID = fmt.Sprintf("git-%s-%s-%s-%s", gitCopy.Date, gitCopy.Category, newStart.Format("1504"), newEnd.Format("1504"))

		return []TimeBlock{meetingCopy, gitCopy}, nil

	case ResolutionDiscard:
		meetingCopy := meeting
		meetingCopy.HasCollision = false
		return []TimeBlock{meetingCopy}, nil

	default:
		return nil, ErrInvalidResolution
	}
}

// ResolveAll applies a uniform resolution policy across all colliding blocks in non-interactive mode.
func ResolveAll(ctx context.Context, blocks []TimeBlock, defaultAction CollisionResolution) ([]TimeBlock, error) {
	Assert(ctx != nil, AssertReviewContextNil)

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	if defaultAction != ResolutionKeepBoth && defaultAction != ResolutionSeparate && defaultAction != ResolutionDiscard {
		return nil, ErrInvalidResolution
	}

	if len(blocks) == 0 {
		return []TimeBlock{}, nil
	}

	collisions, err := DetectCollisions(ctx, blocks)
	if err != nil {
		return nil, err
	}

	if len(collisions) == 0 {
		result := make([]TimeBlock, len(blocks))
		copy(result, blocks)
		for i := range result {
			result[i].HasCollision = false
		}
		return result, nil
	}

	// Map colliding git block IDs to their meeting collision
	gitCollisions := make(map[string]Collision)
	for _, col := range collisions {
		gitCollisions[col.GitID] = col
	}

	meetingsByID := make(map[string]TimeBlock)
	for _, b := range blocks {
		if strings.EqualFold(b.Source, "calendar") {
			meetingsByID[b.ID] = b
		}
	}

	var resolved []TimeBlock

	switch defaultAction {
	case ResolutionDiscard:
		for _, b := range blocks {
			if strings.EqualFold(b.Source, "git") {
				if _, collides := gitCollisions[b.ID]; collides {
					// Discard colliding git block
					continue
				}
			}
			bCopy := b
			bCopy.HasCollision = false
			resolved = append(resolved, bCopy)
		}

	case ResolutionSeparate:
		// Collect meetings for subsequent interval shift checks
		var meetings []TimeBlock
		for _, b := range blocks {
			if strings.EqualFold(b.Source, "calendar") {
				mCopy := b
				mCopy.HasCollision = false
				meetings = append(meetings, mCopy)
				resolved = append(resolved, mCopy)
			}
		}

		for _, b := range blocks {
			if strings.EqualFold(b.Source, "calendar") {
				continue
			}
			if col, collides := gitCollisions[b.ID]; collides {
				meeting, ok := meetingsByID[col.MeetingID]
				if ok {
					pair, resErr := ResolveCollision(meeting, b, ResolutionSeparate)
					if resErr != nil {
						return nil, resErr
					}
					// pair[1] is the shifted git block
					shiftedGit := pair[1]
					// Check against all other meetings to ensure it does not still overlap
					for _, otherM := range meetings {
						stM, etM, _ := parseBlockTimes(otherM, time.UTC)
						stG, etG, _ := parseBlockTimes(shiftedGit, time.UTC)
						if stG.Before(etM) && stM.Before(etG) {
							dur := etG.Sub(stG)
							shiftedGit.StartTime = etM.Format("15:04")
							shiftedGit.EndTime = etM.Add(dur).Format("15:04")
							shiftedGit.ID = fmt.Sprintf("git-%s-%s-%s-%s", shiftedGit.Date, shiftedGit.Category, shiftedGit.StartTime, shiftedGit.EndTime)
						}
					}
					resolved = append(resolved, shiftedGit)
					continue
				}
			}
			bCopy := b
			bCopy.HasCollision = false
			resolved = append(resolved, bCopy)
		}

	case ResolutionKeepBoth:
		// Map meeting ID to colliding git blocks
		meetingGitBlocks := make(map[string][]TimeBlock)
		for _, b := range blocks {
			if strings.EqualFold(b.Source, "git") {
				if col, collides := gitCollisions[b.ID]; collides {
					meetingGitBlocks[col.MeetingID] = append(meetingGitBlocks[col.MeetingID], b)
				}
			}
		}

		for _, b := range blocks {
			if strings.EqualFold(b.Source, "calendar") {
				gBlocks, collides := meetingGitBlocks[b.ID]
				if !collides || len(gBlocks) == 0 {
					bCopy := b
					bCopy.HasCollision = false
					resolved = append(resolved, bCopy)
					continue
				}
				// Merge meeting with each colliding git block sequentially
				currentJoint := b
				for _, gb := range gBlocks {
					pair, resErr := ResolveCollision(currentJoint, gb, ResolutionKeepBoth)
					if resErr != nil {
						return nil, resErr
					}
					currentJoint = pair[0]
				}
				resolved = append(resolved, currentJoint)
			} else if strings.EqualFold(b.Source, "git") {
				if _, collides := gitCollisions[b.ID]; collides {
					// Already merged into joint block
					continue
				}
				bCopy := b
				bCopy.HasCollision = false
				resolved = append(resolved, bCopy)
			} else {
				bCopy := b
				bCopy.HasCollision = false
				resolved = append(resolved, bCopy)
			}
		}
	}

	// Sort resolved blocks chronologically by StartTime
	sort.Slice(resolved, func(i, j int) bool {
		return resolved[i].StartTime < resolved[j].StartTime
	})

	if len(resolved) > MaxTimeBlocksCount {
		return nil, ErrExcessiveBlocks
	}

	return resolved, nil
}
