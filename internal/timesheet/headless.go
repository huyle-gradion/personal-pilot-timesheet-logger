package timesheet

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
//
// The draft is the AI-visible representation only. Audit-only fields stay out
// of it: Sources and SourcesTruncated are kept in the Go struct for the record
// path but never serialize into the draft JSON, and the full per-item evidence
// (titles, URLs) remains durable in the store's day evidence regardless.
type DraftBlock struct {
	BlockID         string   `json:"block_id"`
	Kind            string   `json:"kind"` // meeting|work|evidence|day
	Start           string   `json:"start,omitempty"`
	End             string   `json:"end,omitempty"`
	Title           string   `json:"title,omitempty"`
	TopicCandidates []string `json:"topic_candidates,omitempty"`
	PRs             []string `json:"prs,omitempty"`
	// PRsInline is the exact "PRs: ..." description suffix rendered script-side
	// from the block's PR labels in stable numeric order. When present, the
	// judgment step copies it verbatim into the block description; record
	// validates only that a block with PRs carries a "PRs:" list — prs_inline
	// is the exact string the script supplies for it.
	PRsInline string `json:"prs_inline,omitempty"`
	// Sources and SourcesTruncated never serialize into the AI-visible draft
	// (audit-only content; the durable store and entries schema keep theirs).
	Sources          []DraftSourceItem `json:"-"`
	SourcesTruncated bool              `json:"-"`
	Completeness     map[string]string `json:"completeness,omitempty"`
}

// Draft is the entire input of the skill's judgment step. It carries one day
// only, never raw provider payloads, and never credentials. Its size is
// bounded on mechanical days by fixed rendering rules: topic titles collapse
// per the compactTopics rules and topic_candidates is capped at
// maxDraftTopicCandidates with an honest overflow line. Durable state and
// recorded entries keep full fidelity; only this output is compacted.
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
		// overflow carries each topic candidate's durable overflow counts,
		// aligned with blk.TopicCandidates, so compactTopicsRender can attach
		// combined summaries to the grouped lines after #148's grouping runs.
		overflow []*topicOverflow
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

	// Topics attach to the first block their [first,last] span overlaps,
	// with candidates visited in the shared fixed mapping order
	// (draftTopicTarget) that record-time source re-attachment also uses.
	targets := make([]draftSpan, len(blocks))
	for i := range blocks {
		targets[i] = draftSpan{index: i, startM: blocks[i].startM, endM: blocks[i].endM}
	}
	var unallocated []TopicCandidate
	for _, t := range topics {
		firstM, okF := draftClock(t.FirstAt)
		lastM, okL := draftClock(t.LastAt)
		if okF && okL {
			if i := draftTopicTarget(targets, firstM, lastM); i >= 0 {
				// Bare titles only: #148's grouping and cap run on titles
				// exactly as pinned; overflow summaries attach afterwards,
				// per rendered line (issue #146).
				blocks[i].blk.TopicCandidates = append(blocks[i].blk.TopicCandidates, t.Title)
				blocks[i].overflow = append(blocks[i].overflow, topicOverflowOf(t))
				blocks[i].blk = mergeTopicItems(blocks[i].blk, t)
				continue
			}
		}
		unallocated = append(unallocated, t)
	}

	if len(unallocated) > 0 {
		eb := block{blk: DraftBlock{Kind: "evidence", Completeness: completeness}}
		minM, maxM := -1, -1
		for _, t := range unallocated {
			eb.blk.TopicCandidates = append(eb.blk.TopicCandidates, t.Title)
			eb.overflow = append(eb.overflow, topicOverflowOf(t))
			eb.blk = mergeTopicItems(eb.blk, t)
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
			eb.blk.Start, eb.blk.End = clockString(minM), clockString(maxM)
		} else {
			eb.blk.Start, eb.blk.End = "", ""
		}
		blocks = append(blocks, eb)
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
		blocks[i].blk.TopicCandidates = compactTopicsRender(blocks[i].blk.TopicCandidates, blocks[i].overflow)
		blocks[i].blk.PRsInline = prsInline(blocks[i].blk.PRs)
		d.Blocks = append(d.Blocks, blocks[i].blk)
	}
	return d
}

// draftSpan is one topic-attachment candidate: a block index and its
// [startM,endM) span in local minutes.
type draftSpan struct {
	index  int
	startM int
	endM   int
}

// draftTopicTarget picks the block a topic attaches to: the first candidate
// in the fixed mapping order — start time, then end time, then candidate
// input order — whose span overlaps the topic's [first,last] span, or -1.
// BuildDraft and record-time source re-attachment share this one code path,
// so the block a recorded source lands on is provably the block the draft
// attached it to. The reviewed blocks record reads arrive in the draft's own
// (stable) display order, so candidate input order ties resolve identically
// on both sides.
func draftTopicTarget(cands []draftSpan, firstM, lastM int) int {
	best := -1
	for _, c := range cands {
		if firstM >= c.endM || lastM <= c.startM {
			continue
		}
		if best < 0 || c.startM < cands[best].startM ||
			(c.startM == cands[best].startM && c.endM < cands[best].endM) {
			best = c.index
		}
	}
	return best
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

// topicOverflow accumulates one topic's — or one rendered group's — durable
// overflow counts for the AI-visible draft (issue #146): the count of items
// retained durably beyond the per-topic draft cap by item kind, the count
// lost beyond the fixed overflow ceilings, and the union span of the
// overflow items' timestamps as local minutes. Grouping combines topics by
// summation, so a grouped/mechanical rendered line carries ONE combined
// summary and the "+N more" line can carry the aggregate of the omitted
// lines' losses.
type topicOverflow struct {
	kinds      map[string]int
	kept       int
	lost       int
	minM, maxM int
}

// topicOverflowOf collects one topic's overflow counts. It returns nil for a
// topic with nothing beyond the retained Items cap, so non-truncated topics
// contribute nothing to any rendered line.
func topicOverflowOf(t TopicCandidate) *topicOverflow {
	if len(t.OverflowItems) == 0 && t.DroppedItems == 0 {
		return nil
	}
	o := &topicOverflow{kinds: map[string]int{}, minM: -1, maxM: -1}
	for _, item := range t.OverflowItems {
		k := item.Kind
		if k == "" {
			k = "unclassified"
		}
		o.kinds[k]++
		o.kept++
		if m, ok := draftClock(item.Time); ok {
			if o.minM < 0 || m < o.minM {
				o.minM = m
			}
			if o.maxM < 0 || m > o.maxM {
				o.maxM = m
			}
		}
	}
	o.lost = t.DroppedItems
	return o
}

// merge folds another overflow accumulator into this one (nil is a no-op).
func (o *topicOverflow) merge(p *topicOverflow) {
	if p == nil || o == nil {
		return
	}
	for k, n := range p.kinds {
		o.kinds[k] += n
	}
	o.kept += p.kept
	o.lost += p.lost
	if p.minM >= 0 {
		if o.minM < 0 || p.minM < o.minM {
			o.minM = p.minM
		}
		if o.maxM < 0 || p.maxM > o.maxM {
			o.maxM = p.maxM
		}
	}
}

// empty reports whether the accumulator carries no overflow and no loss.
func (o *topicOverflow) empty() bool {
	return o == nil || (o.kept == 0 && o.lost == 0)
}

// suffix renders the deterministic overflow summary for one line: the kept
// count with per-kind counts in alphabetical order, the honest lost count,
// and the first/last overflow timestamps as local clock times (computed over
// the overflow items only — a topic's FirstAt/LastAt span is unchanged by
// overflow retention — unparseable timestamps are skipped). Empty for a
// group with nothing beyond the retained Items cap.
func (o *topicOverflow) suffix() string {
	if o.empty() {
		return ""
	}
	var parts []string
	if o.kept > 0 {
		names := make([]string, 0, len(o.kinds))
		for k := range o.kinds {
			names = append(names, k)
		}
		sort.Strings(names)
		counts := make([]string, 0, len(names))
		for _, k := range names {
			counts = append(counts, fmt.Sprintf("%s %d", k, o.kinds[k]))
		}
		parts = append(parts, fmt.Sprintf("+%d kept (%s)", o.kept, strings.Join(counts, ", ")))
	}
	if o.lost > 0 {
		parts = append(parts, fmt.Sprintf("%d lost", o.lost))
	}
	if o.minM >= 0 {
		parts = append(parts, clockString(o.minM)+"–"+clockString(o.maxM))
	}
	return " — overflow: " + strings.Join(parts, ", ")
}

// draftOverflowSummary renders one topic's own overflow summary: the same
// deterministic suffix a verbatim (non-grouped) rendered line carries.
func draftOverflowSummary(t TopicCandidate) string {
	return topicOverflowOf(t).suffix()
}

// prLabel renders "pr:owner/name:12" as "#12 owner/name".
func prLabel(resource string) string {
	parts := strings.Split(resource, ":")
	if len(parts) != 3 || parts[2] == "" {
		return ""
	}
	return "#" + parts[2] + " " + parts[1]
}

// sortPRs orders PR labels by their numeric PR number, then by label text, so
// the draft's prs and prs_inline order is stable and human-numeric ("#2"
// before "#10"). The pre-change order was lexicographic; the numeric order is
// a deliberate representation change required for prs_inline.
func sortPRs(prs []string) {
	sort.SliceStable(prs, func(i, j int) bool {
		ni, oki := prLabelNumber(prs[i])
		nj, okj := prLabelNumber(prs[j])
		if oki && okj && ni != nj {
			return ni < nj
		}
		return prs[i] < prs[j]
	})
}

// prLabelNumber extracts the numeric part of a "#N repo" label.
func prLabelNumber(label string) (int, bool) {
	if len(label) == 0 || label[0] != '#' {
		return 0, false
	}
	rest := label[1:]
	if end := strings.IndexByte(rest, ' '); end >= 0 {
		rest = rest[:end]
	}
	n, err := strconv.Atoi(rest)
	if err != nil {
		return 0, false
	}
	return n, true
}

// prsInline renders the exact "PRs: ..." description suffix a block with PRs
// must carry: the block's labels joined into one line in stable numeric
// order. The judgment step copies it verbatim into the description; record
// validates only the presence of a "PRs:" list in the description, and
// prs_inline is the exact string the script supplies for it, so rendering it
// script-side removes the dropped-list rejection mode. A block with no PRs
// has no prs_inline.
func prsInline(prs []string) string {
	if len(prs) == 0 {
		return ""
	}
	ordered := append([]string(nil), prs...)
	sortPRs(ordered)
	return "PRs: " + strings.Join(ordered, ", ")
}

// draftPRTitleRE matches the mechanical per-PR topic title the GitHub
// collector renders ("Pull request #12 (acme/api)"). The number and
// repository are the grouping identity; grouping never depends on free text.
var draftPRTitleRE = regexp.MustCompile(`^Pull request #(\d+) \(([^)]+)\)$`)

// draftPRTitle splits a mechanical per-PR topic title into repository and
// number.
func draftPRTitle(title string) (repo string, num int, ok bool) {
	m := draftPRTitleRE.FindStringSubmatch(title)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return "", 0, false
	}
	return m[2], n, true
}

// draftPRRanges renders sorted, de-duplicated PR numbers as contiguous numeric
// ranges joined by ", ", each "#a–#b" or a lone "#a". Different repositories
// and non-adjacent numbers never merge into one range.
func draftPRRanges(nums []int) string {
	sorted := append([]int(nil), nums...)
	sort.Ints(sorted)
	parts := make([]string, 0, len(sorted))
	for i := 0; i < len(sorted); {
		j := i
		for j+1 < len(sorted) && sorted[j+1] == sorted[j]+1 {
			j++
		}
		if j == i {
			parts = append(parts, fmt.Sprintf("#%d", sorted[i]))
		} else {
			parts = append(parts, fmt.Sprintf("#%d–#%d", sorted[i], sorted[j]))
		}
		i = j + 1
	}
	return strings.Join(parts, ", ")
}

// compactTopics renders one block's AI-visible topic_candidates from bare
// titles — the exact #148 grouping and cap behavior.
func compactTopics(titles []string) []string {
	return compactTopicsRender(titles, nil)
}

// compactTopicsRender is compactTopics with an optional per-title overflow
// payload (issue #146). Grouping and capping see the bare titles exactly as
// #148 pinned them; the overflow summaries attach afterwards, per rendered
// line:
//
//   - A grouped line ("N PRs (#a–#b) in repo") or a collapsed repeat line
//     ("N× title") carries ONE combined summary for the topics it
//     aggregates: summed per-kind kept counts, summed lost count, and the
//     union first/last overflow span. A lone PR or verbatim distinct title
//     keeps its own per-topic summary.
//   - When the rendered list is capped, the omitted lines' overflow
//     aggregates onto the "+N more" line itself, so the headline loss count
//     is never hidden behind the cap.
//
// With all-nil payloads (or via compactTopics) the output is byte-identical
// to the #148 renderer.
func compactTopicsRender(titles []string, overflows []*topicOverflow) []string {
	ov := func(i int) *topicOverflow {
		if i < len(overflows) {
			return overflows[i]
		}
		return nil
	}
	type prGroup struct {
		nums []int
		seen map[int]bool
		sum  *topicOverflow
	}
	prGroups := map[string]*prGroup{}
	var prRepoOrder []string
	repeat := map[string]int{}
	repeatSum := map[string]*topicOverflow{}
	for i, t := range titles {
		if repo, num, ok := draftPRTitle(t); ok {
			g, seen := prGroups[repo]
			if !seen {
				g = &prGroup{seen: map[int]bool{}, sum: &topicOverflow{kinds: map[string]int{}, minM: -1, maxM: -1}}
				prGroups[repo] = g
				prRepoOrder = append(prRepoOrder, repo)
			}
			if !g.seen[num] {
				g.seen[num] = true
				g.nums = append(g.nums, num)
			}
			g.sum.merge(ov(i))
			continue
		}
		repeat[t]++
		if repeatSum[t] == nil {
			repeatSum[t] = &topicOverflow{kinds: map[string]int{}, minM: -1, maxM: -1}
		}
		repeatSum[t].merge(ov(i))
	}
	var out []string
	lineSums := []*topicOverflow(nil)
	renderedPR := map[string]bool{}
	renderedTitle := map[string]bool{}
	for i, t := range titles {
		if repo, _, ok := draftPRTitle(t); ok {
			if renderedPR[repo] {
				continue
			}
			renderedPR[repo] = true
			g := prGroups[repo]
			if len(g.nums) == 1 {
				out = append(out, t+g.sum.suffix())
				lineSums = append(lineSums, g.sum)
				continue
			}
			out = append(out, fmt.Sprintf("%d PRs (%s) in %s", len(g.nums), draftPRRanges(g.nums), repo)+g.sum.suffix())
			lineSums = append(lineSums, g.sum)
			continue
		}
		if renderedTitle[t] {
			continue
		}
		renderedTitle[t] = true
		if repeat[t] >= 2 {
			out = append(out, fmt.Sprintf("%d× %s", repeat[t], t)+repeatSum[t].suffix())
			lineSums = append(lineSums, repeatSum[t])
			continue
		}
		out = append(out, t+ov(i).suffix())
		lineSums = append(lineSums, ov(i))
	}
	if len(out) > maxDraftTopicCandidates {
		// The omitted rendered lines' overflow aggregates onto the "+N more"
		// line: the loss signal survives the cap, never hidden behind it.
		agg := &topicOverflow{kinds: map[string]int{}, minM: -1, maxM: -1}
		for _, s := range lineSums[maxDraftTopicCandidates:] {
			agg.merge(s)
		}
		more := fmt.Sprintf("+%d more", len(out)-maxDraftTopicCandidates) + agg.suffix()
		out = append(out[:maxDraftTopicCandidates:maxDraftTopicCandidates], more)
	}
	return out
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
// is the grouped topic summary; when PRs is non-empty, RecordEntries validates
// only that the description carries a "PRs:" list — prs_inline is the exact
// string the script renders for it.
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
// changes nothing but UpdatedAt. Before merging it re-attaches each block's
// audit-only source references from the day's durable evidence (the
// AI-visible draft no longer carries them); recording never writes state and
// never blocks on a running serve.
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

	// Re-attach the audit-only source references from the day's durable
	// evidence. The AI-visible draft no longer carries sources, so the
	// reviewed file cannot echo them; the recorded entry must still keep the
	// full source references the pre-change draft carried (orchestrator-
	// authorized DRAFT-COMPACT-1 amendment). This only fills entry fields —
	// no validation rule, schema, or draft shape changes.
	if ev := readDayEvidenceForRecord(statePath, reviewed.Date); ev != nil {
		reattachSources(ev, reviewed.Blocks)
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

// --- record-time source re-attachment -----------------------------------------

// stateEvidenceView is a minimal read-only projection of the durable state
// file: record parses only the evidence section and never anything else.
type stateEvidenceView struct {
	Evidence map[string]*DayEvidence `json:"evidence"`
}

// readDayEvidenceForRecord reads one day's durable evidence from the state
// file for record-time source re-attachment. It never takes the store's
// single-writer lock and never writes state, so recording still never blocks
// on, or interferes with, a running serve process (the state file is replaced
// atomically, so a lock-free read sees one complete version). A missing,
// unreadable, or invalid state file yields nil: re-attachment is audit
// parity, never a record precondition, and the day still records.
func readDayEvidenceForRecord(statePath, date string) *DayEvidence {
	if statePath == "" || date == "" {
		return nil
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil
	}
	var view stateEvidenceView
	if err := json.Unmarshal(data, &view); err != nil {
		return nil
	}
	return view.Evidence[date]
}

// reattachSources re-derives each reviewed block's audit-only source
// references from the day's durable evidence, using the exact same
// topic→block mapping (draftTopicTarget) and mergeTopicItems folding (with
// the same draftSourcesPerBlock cap and sources_truncated flag) that
// BuildDraft applies, so a recorded entry carries exactly the sources the
// draft attached to that block: meeting and work blocks take the topics whose
// [first,last] span overlaps them in the shared fixed order, and every
// unallocated topic folds into the evidence block. The state's evidence is
// authoritative: anything the reviewed file echoed is replaced, keeping
// recorded sources identical whether or not the reviewed file carried them.
// A whole-day block never carries sources.
func reattachSources(ev *DayEvidence, blocks []ReviewedBlock) {
	var cands []draftSpan
	evidenceIdx := -1
	for i := range blocks {
		switch blocks[i].Kind {
		case "meeting", "work":
			startM, okS := draftClock(blocks[i].Start)
			endM, okE := draftClock(blocks[i].End)
			if okS && okE && endM > startM {
				cands = append(cands, draftSpan{index: i, startM: startM, endM: endM})
			}
		case "evidence":
			evidenceIdx = i
		}
	}
	if len(cands) == 0 && evidenceIdx < 0 {
		return
	}
	folds := make([]DraftBlock, len(blocks))
	for _, t := range ev.Topics {
		firstM, okF := draftClock(t.FirstAt)
		lastM, okL := draftClock(t.LastAt)
		assigned := -1
		if okF && okL {
			assigned = draftTopicTarget(cands, firstM, lastM)
		}
		if assigned < 0 {
			assigned = evidenceIdx
		}
		if assigned < 0 {
			continue
		}
		folds[assigned] = mergeTopicItems(folds[assigned], t)
	}
	for i := range blocks {
		blocks[i].Sources = folds[i].Sources
		blocks[i].SourcesTruncated = folds[i].SourcesTruncated
	}
}
