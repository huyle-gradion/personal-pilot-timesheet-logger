package timesheet

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The headless surface is the deterministic [Script] layer for the timesheet
// skill: BuildDraft turns one day's collected review into the compact draft the
// skill's single judgment step reviews, and RecordEntries persists the reviewed
// result as the day's log entries. Neither function performs collection,
// confirmation, approval, submission, or any network access.

// DraftSourceItem is one bounded evidence reference inside a draft block.
type DraftSourceItem struct {
	Kind  string `json:"kind"`
	Repo  string `json:"repo,omitempty"`
	Title string `json:"title,omitempty"`
	Time  string `json:"time,omitempty"`
	URL   string `json:"url,omitempty"`
}

// DraftBlock is one time block of the day: a calendar meeting, a confirmed
// work interval outside every meeting, the span of unallocated evidence, or —
// when the day has no time anchors at all — one whole-day block.
type DraftBlock struct {
	BlockID          string            `json:"block_id"`
	Kind             string            `json:"kind"` // meeting|work|evidence|day
	Start            string            `json:"start,omitempty"`
	End              string            `json:"end,omitempty"`
	Title            string            `json:"title,omitempty"`
	TopicCandidates  []string          `json:"topic_candidates,omitempty"`
	PRs              []string          `json:"prs,omitempty"`
	Sources          []DraftSourceItem `json:"sources,omitempty"`
	SourcesTruncated bool              `json:"sources_truncated,omitempty"`
	Completeness     map[string]string `json:"completeness,omitempty"`
}

// Draft is the entire input of the skill's judgment step. It carries one day
// only, never raw provider payloads, and never credentials.
type Draft struct {
	Date       string       `json:"date"`
	Timezone   string       `json:"timezone"`
	AppVersion string       `json:"app_version"`
	DayNote    string       `json:"day_note,omitempty"`
	Flags      []string     `json:"flags,omitempty"`
	Blocks     []DraftBlock `json:"blocks"`
}

const draftSourcesPerBlock = 8

// BuildDraft applies the fixed, documented partitioning rule to one review:
// meetings (not declined, not cancelled, not all-day) become blocks in start
// order; confirmed intervals outside every meeting become work blocks; topics
// attach to the first block their time span overlaps; remaining topics form one
// evidence block; a day with no anchors at all still yields one entry-shaped
// block so the log never silently skips a day.
func BuildDraft(review Review, appVersion string) Draft {
	d := Draft{
		Date:       review.Date,
		Timezone:   review.Timezone,
		AppVersion: appVersion,
	}
	var topics []TopicCandidate
	var meetings []ProposedMeeting
	var completeness map[string]string
	if review.Evidence != nil {
		topics = review.Evidence.Topics
		meetings = review.Evidence.Meetings
		completeness = map[string]string{}
		for _, src := range review.Evidence.Sources {
			if !src.Enabled {
				continue
			}
			completeness[src.Source] = src.Status
			if src.Status != StatusComplete && !src.Acknowledged {
				d.Flags = append(d.Flags, "source_incomplete:"+src.Source)
			}
		}
	}
	if len(review.Confirmed.Segments) == 0 {
		d.Flags = append(d.Flags, "no_confirmed_intervals")
	}

	type block struct {
		blk    DraftBlock
		startM int
		endM   int
		hasM   bool
	}
	var blocks []block
	eligibleMeetings := 0

	for _, m := range meetings {
		if m.Decision == "no" || m.Status == "cancelled" || m.AllDay {
			continue
		}
		startM, okS := draftClock(m.Start)
		endM, okE := draftClock(m.End)
		if !okS || !okE || endM <= startM {
			continue
		}
		eligibleMeetings++
		blocks = append(blocks, block{
			blk: DraftBlock{
				Kind:         "meeting",
				Start:        clockString(startM),
				End:          clockString(endM),
				Title:        m.Title,
				Completeness: completeness,
			},
			startM: startM, endM: endM, hasM: true,
		})
	}

	for _, seg := range review.Confirmed.Segments {
		startM, okS := draftClock(seg.Start)
		endM, okE := draftClock(seg.End)
		if !okS || !okE || endM <= startM {
			continue
		}
		overlaps := false
		for i := range blocks {
			if blocks[i].hasM && startM < blocks[i].endM && endM > blocks[i].startM {
				overlaps = true
				break
			}
		}
		if overlaps {
			continue
		}
		blocks = append(blocks, block{
			blk: DraftBlock{
				Kind:         "work",
				Start:        clockString(startM),
				End:          clockString(endM),
				Title:        seg.Description,
				Completeness: completeness,
			},
			startM: startM, endM: endM, hasM: true,
		})
	}

	// Topics attach to the first block their [first,last] span overlaps.
	var unallocated []TopicCandidate
	for _, t := range topics {
		firstM, okF := draftClock(t.FirstAt)
		lastM, okL := draftClock(t.LastAt)
		assigned := false
		if okF && okL {
			for i := range blocks {
				if firstM < blocks[i].endM && lastM > blocks[i].startM {
					blocks[i].blk.TopicCandidates = append(blocks[i].blk.TopicCandidates, t.Title)
					blocks[i].blk = mergeTopicItems(blocks[i].blk, t)
					assigned = true
					break
				}
			}
		}
		if !assigned {
			unallocated = append(unallocated, t)
		}
	}

	if len(unallocated) > 0 {
		eb := DraftBlock{Kind: "evidence", Completeness: completeness}
		minM, maxM := -1, -1
		for _, t := range unallocated {
			eb.TopicCandidates = append(eb.TopicCandidates, t.Title)
			eb = mergeTopicItems(eb, t)
			for _, item := range t.Items {
				if m, ok := draftClock(item.Time); ok {
					if minM == -1 || m < minM {
						minM = m
					}
					if m > maxM {
						maxM = m
					}
				}
			}
		}
		if minM >= 0 && maxM >= minM {
			eb.Start, eb.End = clockString(minM), clockString(maxM)
		} else {
			eb.Start, eb.End = "", ""
		}
		blocks = append(blocks, block{blk: eb})
	}

	if len(blocks) == 0 {
		blocks = append(blocks, block{blk: DraftBlock{
			Kind:         "day",
			Completeness: completeness,
		}})
	}
	if eligibleMeetings == 0 {
		d.DayNote = "no calendar events"
	}

	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].hasM != blocks[j].hasM {
			return blocks[i].hasM
		}
		if blocks[i].hasM && blocks[i].startM != blocks[j].startM {
			return blocks[i].startM < blocks[j].startM
		}
		return blocks[i].blk.Kind < blocks[j].blk.Kind
	})

	used := map[string]int{}
	for i := range blocks {
		id := blocks[i].blk.Kind + "-" + blocks[i].blk.Start + "-" + blocks[i].blk.End
		if n := used[id]; n > 0 {
			id += "-" + strconv.Itoa(n+1)
		}
		used[id]++
		blocks[i].blk.BlockID = id
		sortPRs(blocks[i].blk.PRs)
		d.Blocks = append(d.Blocks, blocks[i].blk)
	}
	return d
}

// mergeTopicItems folds one topic's bounded evidence references and PR
// identities into a block, respecting the fixed per-block item cap.
func mergeTopicItems(blk DraftBlock, t TopicCandidate) DraftBlock {
	for _, item := range t.Items {
		if strings.HasPrefix(item.Resource, "pr:") {
			if pr := prLabel(item.Resource); pr != "" && !containsString(blk.PRs, pr) {
				blk.PRs = append(blk.PRs, pr)
			}
		}
		if len(blk.Sources) < draftSourcesPerBlock {
			blk.Sources = append(blk.Sources, DraftSourceItem{
				Kind: item.Kind, Repo: item.Repo, Title: item.Title, Time: item.Time, URL: item.URL,
			})
		} else {
			blk.SourcesTruncated = true
		}
	}
	return blk
}

// prLabel renders "pr:owner/name:12" as "#12 owner/name".
func prLabel(resource string) string {
	parts := strings.Split(resource, ":")
	if len(parts) != 3 || parts[2] == "" {
		return ""
	}
	return "#" + parts[2] + " " + parts[1]
}

func sortPRs(prs []string) {
	sort.SliceStable(prs, func(i, j int) bool { return prs[i] < prs[j] })
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// draftClock accepts the two time shapes the review carries — "HH:MM" for
// intervals and RFC 3339 for provider timestamps — and returns local minutes.
func draftClock(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if len(s) >= 16 && (s[10] == 'T' || s[10] == ' ') {
		if t, err := time.Parse(time.RFC3339, strings.Replace(s, " ", "T", 1)); err == nil {
			return t.Hour()*60 + t.Minute(), true
		}
	}
	if t, err := time.Parse("15:04", s); err == nil {
		return t.Hour()*60 + t.Minute(), true
	}
	return 0, false
}

func clockString(minutes int) string {
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

// --- reviewed-draft recording ------------------------------------------------

// ReviewedBlock is a draft block after the skill's judgment step. Description
// is the grouped topic summary; when PRs is non-empty the description must end
// with the inline "PRs:" list, which RecordEntries enforces.
type ReviewedBlock struct {
	DraftBlock
	Description string `json:"description"`
	Notes       string `json:"notes,omitempty"`
}

// ReviewedDraft is the judgment step's output: the draft with per-block
// descriptions and optional day-level notes filled in.
type ReviewedDraft struct {
	Date     string          `json:"date"`
	Timezone string          `json:"timezone"`
	DayNote  string          `json:"day_note,omitempty"`
	Flags    []string        `json:"flags,omitempty"`
	Notes    string          `json:"notes,omitempty"`
	Blocks   []ReviewedBlock `json:"blocks"`
}

// EntryRecord is one stored log entry. It keeps the full source references so
// any entry can be checked against the providers it came from.
type EntryRecord struct {
	BlockID          string            `json:"block_id"`
	Date             string            `json:"date"`
	Start            string            `json:"start,omitempty"`
	End              string            `json:"end,omitempty"`
	Description      string            `json:"description"`
	PRs              []string          `json:"prs,omitempty"`
	Kind             string            `json:"kind"`
	TopicCandidates  []string          `json:"topic_candidates,omitempty"`
	Sources          []DraftSourceItem `json:"sources,omitempty"`
	SourcesTruncated bool              `json:"sources_truncated,omitempty"`
	Completeness     map[string]string `json:"completeness,omitempty"`
	Notes            string            `json:"notes,omitempty"`
	RecordedAt       string            `json:"recorded_at"`
}

// EntriesFile is the durable per-day log. One file per date, merged by block:
// re-recording a block updates it in place and never duplicates it.
type EntriesFile struct {
	Date       string        `json:"date"`
	AppVersion string        `json:"app_version"`
	UpdatedAt  string        `json:"updated_at"`
	Entries    []EntryRecord `json:"entries"`
}

// RecordReceipt reports one recording run.
type RecordReceipt struct {
	Date         string `json:"date"`
	EntriesTotal int    `json:"entries_total"`
	Written      int    `json:"written"`
	Path         string `json:"path"`
}

// EntriesPath returns the per-day log path derived from the state path.
func EntriesPath(statePath, date string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(statePath)), "entries", date+".json")
}

// RecordEntries validates the reviewed draft and merges it into the day's
// entries file atomically. It is idempotent per block_id: an existing block is
// replaced, other blocks are preserved, and a second run of the same input
// changes nothing but UpdatedAt.
func RecordEntries(statePath, appVersion string, reviewed ReviewedDraft, now time.Time) (RecordReceipt, error) {
	if _, err := time.Parse("2006-01-02", reviewed.Date); err != nil {
		return RecordReceipt{}, newError(CodeInvalidDate, "record: date must be YYYY-MM-DD")
	}
	if len(reviewed.Blocks) == 0 {
		return RecordReceipt{}, newError(CodeBadRequest, "record: at least one block is required")
	}
	seen := map[string]bool{}
	for _, b := range reviewed.Blocks {
		id := strings.TrimSpace(b.BlockID)
		if id == "" {
			return RecordReceipt{}, newError(CodeBadRequest, "record: every block needs a block_id")
		}
		if seen[id] {
			return RecordReceipt{}, newError(CodeBadRequest, "record: duplicate block_id "+id)
		}
		seen[id] = true
		if strings.TrimSpace(b.Description) == "" {
			return RecordReceipt{}, newError(CodeBadRequest, "record: block "+id+" has an empty description")
		}
		if len(b.PRs) > 0 && !strings.Contains(b.Description, "PRs:") {
			return RecordReceipt{}, newError(CodeBadRequest,
				"record: block "+id+" has PRs but its description does not carry the inline PRs: list")
		}
	}

	path := EntriesPath(statePath, reviewed.Date)
	var existing EntriesFile
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &existing); err != nil || existing.Date != reviewed.Date {
			return RecordReceipt{}, newError(CodeStateInvalid, "record: the existing entries file is not a valid file for this date")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return RecordReceipt{}, newError(CodeStateInvalid, "record: the existing entries file cannot be read")
	}

	stamp := now.UTC().Format(time.RFC3339)
	merged := EntriesFile{Date: reviewed.Date, AppVersion: appVersion, UpdatedAt: stamp}
	kept := map[string]EntryRecord{}
	for _, e := range existing.Entries {
		kept[e.BlockID] = e
	}
	written := 0
	for _, b := range reviewed.Blocks {
		kept[b.BlockID] = EntryRecord{
			BlockID: b.BlockID, Date: reviewed.Date, Start: b.Start, End: b.End,
			Description: strings.TrimSpace(b.Description), PRs: b.PRs, Kind: b.Kind,
			TopicCandidates: b.TopicCandidates, Sources: b.Sources, SourcesTruncated: b.SourcesTruncated,
			Completeness: b.Completeness, Notes: b.Notes, RecordedAt: stamp,
		}
		written++
	}
	for _, e := range kept {
		merged.Entries = append(merged.Entries, e)
	}
	sort.SliceStable(merged.Entries, func(i, j int) bool {
		if merged.Entries[i].Start != merged.Entries[j].Start {
			return merged.Entries[i].Start < merged.Entries[j].Start
		}
		return merged.Entries[i].BlockID < merged.Entries[j].BlockID
	})

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return RecordReceipt{}, newError(CodeStateInvalid, "record: the entries directory cannot be created")
	}
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return RecordReceipt{}, newError(CodeInternal, "record: entries cannot be encoded")
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return RecordReceipt{}, newError(CodeStateInvalid, "record: the entries file cannot be written")
	}
	if err := os.Rename(tmp, path); err != nil {
		return RecordReceipt{}, newError(CodeStateInvalid, "record: the entries file cannot be replaced")
	}
	return RecordReceipt{
		Date: reviewed.Date, EntriesTotal: len(merged.Entries), Written: written, Path: path,
	}, nil
}

// ReadEntries loads the day's entries file. A missing file is (nil, nil).
func ReadEntries(statePath, date string) (*EntriesFile, error) {
	data, err := os.ReadFile(EntriesPath(statePath, date))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, newError(CodeStateInvalid, "the entries file cannot be read")
	}
	var f EntriesFile
	if err := json.Unmarshal(data, &f); err != nil || f.Date != date {
		return nil, newError(CodeStateInvalid, "the entries file is not valid for this date")
	}
	return &f, nil
}
