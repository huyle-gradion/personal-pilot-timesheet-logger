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

// --- descriptions-only judgment step (issue #152) -----------------------------

// DescriptionsFile is the descriptions-only judgment step's entire output: the
// one small file the AI writes instead of a full reviewed draft. Notes is one
// plain day-level string; the JSON form accepts a string only (never an
// array), and Descriptions maps each draft block_id to its description prose.
// The PRs list is never part of it: record appends each block's prs_inline.
type DescriptionsFile struct {
	Notes        string            `json:"notes,omitempty"`
	Descriptions map[string]string `json:"descriptions"`
}

// ParseDescriptions parses the descriptions file strictly. A notes field that
// is present must be a JSON string — an array or any other type is a
// validation error, so the AI-facing schema never exposes an array form.
// An absent (or null) notes field is an empty string.
func ParseDescriptions(data []byte) (DescriptionsFile, error) {
	var raw struct {
		Notes        json.RawMessage   `json:"notes"`
		Descriptions map[string]string `json:"descriptions"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return DescriptionsFile{}, newError(CodeBadRequest,
			`record: the descriptions file is not valid JSON for the expected schema {"notes": "<string>", "descriptions": {"<block_id>": "<prose>"}}`)
	}
	out := DescriptionsFile{Descriptions: raw.Descriptions}
	if trimmed := strings.TrimSpace(string(raw.Notes)); len(trimmed) > 0 && trimmed != "null" {
		if !strings.HasPrefix(trimmed, `"`) {
			return DescriptionsFile{}, newError(CodeBadRequest,
				`record: the descriptions file's "notes" must be one JSON string, not an array or other type`)
		}
		if err := json.Unmarshal(raw.Notes, &out.Notes); err != nil {
			return DescriptionsFile{}, newError(CodeBadRequest,
				`record: the descriptions file's "notes" must be one JSON string, not an array or other type`)
		}
	}
	return out, nil
}

// ComposeReviewed composes the reviewed draft the record path consumes from
// the draft and the descriptions-only judgment output. Every draft block must
// get a description: a missing or empty one is a validation error listing the
// offending block_ids, and a descriptions entry naming a block_id the draft
// does not contain is a validation error. When a block carries prs_inline and
// its text is not already contained in the description, it is appended to the
// description end with a single space separator (never duplicated; blocks
// without PRs are unchanged). The returned reviewed draft then runs the EXACT
// existing RecordEntries validation and merge path — no validation rule
// changes for either input mode.
func ComposeReviewed(draft Draft, desc DescriptionsFile) (ReviewedDraft, error) {
	known := make(map[string]bool, len(draft.Blocks))
	for _, b := range draft.Blocks {
		known[b.BlockID] = true
	}
	var missing []string
	for _, b := range draft.Blocks {
		if d := desc.Descriptions[b.BlockID]; strings.TrimSpace(d) == "" {
			missing = append(missing, b.BlockID)
		}
	}
	if len(missing) > 0 {
		if desc.Descriptions == nil {
			// A wholly absent descriptions map would list every block id and
			// misdirect the AI; name the real problem instead.
			return ReviewedDraft{}, newError(CodeBadRequest,
				"record: the descriptions file has no descriptions map")
		}
		return ReviewedDraft{}, newError(CodeBadRequest,
			"record: every block needs a description; missing or empty: "+strings.Join(missing, ", "))
	}
	var unknown []string
	for id := range desc.Descriptions {
		if !known[id] {
			unknown = append(unknown, id)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		return ReviewedDraft{}, newError(CodeBadRequest,
			"record: the descriptions file names block ids the draft does not contain: "+strings.Join(unknown, ", "))
	}
	reviewed := ReviewedDraft{
		Date: draft.Date, Timezone: draft.Timezone, DayNote: draft.DayNote,
		Flags: draft.Flags, Notes: desc.Notes,
	}
	for _, b := range draft.Blocks {
		d := desc.Descriptions[b.BlockID]
		// Suffix check, not Contains: a prefix-sharing PR id ("PRs: #12 …"
		// inside "PRs: #123 …") must not make the append look already done.
		// Trailing whitespace of any kind is stripped so the separator is a
		// single space.
		if b.PRsInline != "" && !strings.HasSuffix(strings.TrimRight(d, " \t\n\r"), b.PRsInline) {
			d = strings.TrimRight(d, " \t\n\r") + " " + b.PRsInline
		}
		reviewed.Blocks = append(reviewed.Blocks, ReviewedBlock{DraftBlock: b, Description: d})
	}
	return reviewed, nil
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

// RecordReceipt reports one recording run. Removed counts the day's prior
// blocks a --replace run dropped (issue #162); merge runs never remove, so
// the field stays absent there.
type RecordReceipt struct {
	Date         string `json:"date"`
	EntriesTotal int    `json:"entries_total"`
	Written      int    `json:"written"`
	Removed      int    `json:"removed,omitempty"`
	Path         string `json:"path"`
}

// EntriesPath returns the per-day log path derived from the state path.
func EntriesPath(statePath, date string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(statePath)), "entries", date+".json")
}

// RecordEntries validates the reviewed draft and merges it into the day's
// entries file atomically. It is idempotent per block_id: an existing block is
// replaced, other blocks are preserved, and a second run of the same input
// changes nothing but UpdatedAt. With replace (issue #162), the reviewed draft
// is the day's complete content: all blocks for the date are replaced with the
// draft's blocks (block ids from the draft; blocks absent from it are removed
// from the entries file) — the re-collected-day case where the new generation
// supersedes the old. The property within one replace run is unchanged:
// re-running the same replace is idempotent, and per-block validation is
// identical. Before merging it re-attaches each block's
// audit-only source references from the day's durable evidence (the
// AI-visible draft no longer carries them); recording never writes state and
// never blocks on a running serve.
func RecordEntries(statePath, appVersion string, reviewed ReviewedDraft, replace bool, now time.Time) (RecordReceipt, error) {
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
	// Merge mode preserves blocks the draft does not mention; replace mode
	// (issue #162) starts empty, so blocks absent from the draft are removed
	// and the draft is the day's complete content.
	if !replace {
		for _, e := range existing.Entries {
			kept[e.BlockID] = e
		}
	}
	removed := 0
	if replace {
		for _, e := range existing.Entries {
			if !seen[e.BlockID] {
				removed++
			}
		}
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
		Date: reviewed.Date, EntriesTotal: len(merged.Entries), Written: written, Removed: removed, Path: path,
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

// --- SUBMIT-1 stage-map sidecar -------------------------------------------------
//
// The stage command converts the day's recorded entries into staged intervals.
// The sidecar maps each block_id to the staged interval_id it became, for
// same-process restage (edit/remove in place instead of duplicating) and for
// audit. It does NOT make staged state survive a process: Day.Staged is
// session-only, so every fresh command re-derives the staged set from the
// entries ledger and re-adds it; the interval ids that re-derivation produces
// are stable in practice only while nothing is saved. Like the entries file
// the sidecar lives under the entries directory, carries no state-schema
// weight, and is never read by serve or the plan builder.

// StagePath returns the per-day stage-map sidecar path derived from the state
// path: entries/<date>.stage.json, next to the day's entries file.
func StagePath(statePath, date string) string {
	return strings.TrimSuffix(EntriesPath(statePath, date), ".json") + ".stage.json"
}

// StageMapFile is the durable block_id → interval_id sidecar for one day.
type StageMapFile struct {
	Date       string            `json:"date"`
	AppVersion string            `json:"app_version"`
	UpdatedAt  string            `json:"updated_at"`
	Map        map[string]string `json:"map"`
}

// ReadStageMap loads the day's stage map. A missing file is (nil, nil).
func ReadStageMap(statePath, date string) (map[string]string, error) {
	data, err := os.ReadFile(StagePath(statePath, date))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, newError(CodeStateInvalid, "the stage map file cannot be read")
	}
	var f StageMapFile
	if err := json.Unmarshal(data, &f); err != nil || f.Date != date {
		return nil, newError(CodeStateInvalid, "the stage map file is not valid for this date")
	}
	return f.Map, nil
}

// WriteStageMap persists the day's stage map atomically, mirroring the entries
// file's write discipline: one temporary file, one rename.
func WriteStageMap(statePath, appVersion, date string, mapping map[string]string, now time.Time) error {
	if mapping == nil {
		mapping = map[string]string{}
	}
	f := StageMapFile{
		Date:       date,
		AppVersion: appVersion,
		UpdatedAt:  now.UTC().Format(time.RFC3339),
		Map:        mapping,
	}
	path := StagePath(statePath, date)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return newError(CodeStateInvalid, "the entries directory cannot be created")
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return newError(CodeInternal, "the stage map cannot be encoded")
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return newError(CodeStateInvalid, "the stage map file cannot be written")
	}
	if err := os.Rename(tmp, path); err != nil {
		return newError(CodeStateInvalid, "the stage map file cannot be replaced")
	}
	return nil
}

// --- SUBMIT-2 classification-choices sidecar ------------------------------------
//
// classify applies reviewed classification selections to the day's staged
// intervals. Like the staged set itself, a selection made on a staged interval
// is session-only: it lives on Day.Staged entries and becomes durable only
// when confirm+save commit the day. Because every command is a one-shot
// process that re-derives the staged set from the entries ledger, the raw
// selections must survive the process boundary on their own: this sidecar —
// entries/<date>.classify.json, next to the entries file — persists the
// interval_id → {catalog_id, classification_id} map, each selection
// content-bound to the recorded block it was chosen for (BlockContentDigest),
// so confirm and save can replay it against a provably identical re-derived
// staged set and fail closed on any entries drift. It is the same pattern as
// the stage map, carries no state-schema weight, and is never read by serve
// or the plan builder.

// ChoicesPath returns the per-day classification-choices sidecar path derived
// from the state path: entries/<date>.classify.json, next to the day's entries
// file and stage map.
func ChoicesPath(statePath, date string) string {
	return strings.TrimSuffix(EntriesPath(statePath, date), ".json") + ".classify.json"
}

// ClassificationChoiceEntry is one interval's reviewed selection: the exact
// catalog_id and classification_id the operator chose, both copied verbatim
// from prior receipts, plus the digest of the recorded block content the
// selection was chosen for (BlockContentDigest). The digest is what makes a
// replay safe: staged interval ids re-derive sequentially, so an entries
// shift between classify and confirm/save could silently re-attach a
// selection to the wrong successor interval — the digest fails closed on any
// such drift instead.
type ClassificationChoiceEntry struct {
	CatalogID        string `json:"catalog_id"`
	ClassificationID string `json:"classification_id"`
	ContentDigest    string `json:"content_digest"`
}

// BlockContentDigest is the deterministic content binding of one recorded
// block: the SHA-256 hex of the newline-joined date, block_id, start, end and
// description, with one trailing newline. These are exactly the fields a
// staged interval's text is derived from, so any change that would alter the
// block's staged interval (time, id or description) changes the digest, and
// any re-derivation whose blocks differ from those the selections were chosen
// against is refused instead of replayed.
func BlockContentDigest(date string, e EntryRecord) string {
	return sha256Hex([]byte(strings.Join([]string{
		date, e.BlockID, e.Start, e.End, e.Description,
	}, "\n") + "\n"))
}

// ClassificationChoicesFile is the durable per-day interval_id → selection
// sidecar. Its Selections shape is also the exact schema of classify's --file
// input, so a choices file written from a stage receipt and the persisted
// sidecar are the same document.
type ClassificationChoicesFile struct {
	Date       string                               `json:"date"`
	AppVersion string                               `json:"app_version"`
	UpdatedAt  string                               `json:"updated_at"`
	Selections map[string]ClassificationChoiceEntry `json:"selections"`
}

// ParseClassificationChoices parses classify's --file input strictly: one
// optional selections map keyed by interval_id. An absent or empty selections
// map is valid and means "no selections" — an honest no-op. A selection
// missing either id is a validation error naming the interval, and a file
// that is not valid JSON names the expected schema so it is diagnosable
// without reading it.
func ParseClassificationChoices(data []byte) (map[string]ClassificationChoiceEntry, error) {
	var raw struct {
		Selections map[string]ClassificationChoiceEntry `json:"selections"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, newError(CodeBadRequest,
			`classify: the choices file is not valid JSON for the expected schema {"selections": {"<interval_id>": {"catalog_id": "...", "classification_id": "..."}}}`)
	}
	ids := make([]string, 0, len(raw.Selections))
	for id := range raw.Selections {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		sel := raw.Selections[id]
		if strings.TrimSpace(sel.CatalogID) == "" {
			return nil, newError(CodeBadRequest,
				"classify: interval "+id+" is missing catalog_id; every selection must name the catalog it reviewed")
		}
		if strings.TrimSpace(sel.ClassificationID) == "" {
			return nil, newError(CodeBadRequest,
				"classify: interval "+id+" is missing classification_id")
		}
	}
	return raw.Selections, nil
}

// ReadClassificationChoices loads the day's classification choices. A missing
// file is (nil, nil). A present file without a selections key is fail-closed
// state_invalid naming the file and the missing key: silence there would let
// confirm and save proceed unclassified while believing the day was
// classified. An explicit empty map ("selections": {}) remains the valid
// no-op/clear.
func ReadClassificationChoices(statePath, date string) (map[string]ClassificationChoiceEntry, error) {
	data, err := os.ReadFile(ChoicesPath(statePath, date))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, newError(CodeStateInvalid, "the classification choices file cannot be read")
	}
	var f ClassificationChoicesFile
	if err := json.Unmarshal(data, &f); err != nil || f.Date != date {
		return nil, newError(CodeStateInvalid, "the classification choices file is not valid for this date")
	}
	if f.Selections == nil {
		return nil, newError(CodeStateInvalid,
			"the classification choices file ("+ChoicesPath(statePath, date)+") has no selections key; expected {\"selections\": {}} at minimum")
	}
	return f.Selections, nil
}

// WriteClassificationChoices persists the day's classification choices
// atomically, mirroring the entries file's write discipline: one temporary
// file, one rename. The written map replaces any previous one: the latest
// choices file defines the day's selections, so an empty map clears them.
func WriteClassificationChoices(statePath, appVersion, date string, selections map[string]ClassificationChoiceEntry, now time.Time) error {
	if selections == nil {
		selections = map[string]ClassificationChoiceEntry{}
	}
	f := ClassificationChoicesFile{
		Date:       date,
		AppVersion: appVersion,
		UpdatedAt:  now.UTC().Format(time.RFC3339),
		Selections: selections,
	}
	path := ChoicesPath(statePath, date)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return newError(CodeStateInvalid, "the entries directory cannot be created")
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return newError(CodeInternal, "the classification choices cannot be encoded")
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return newError(CodeStateInvalid, "the classification choices file cannot be written")
	}
	if err := os.Rename(tmp, path); err != nil {
		return newError(CodeStateInvalid, "the classification choices file cannot be replaced")
	}
	return nil
}

// --- SUBMIT-3 remote-identity decision file -------------------------------------
//
// resolve applies one explicit human identity/content decision against the
// day's current remote observation. The decision replaces the interactive pi
// picker: the human reads the reconcile (or plan) receipt, then authors one
// small JSON file whose fields are copied verbatim from that receipt, and the
// command passes it to the resolve_remote op. Every op-level refusal — a wrong
// or stale observation_id, a remote_id the observation does not carry, adopt
// without confirmed durable local work, a decision that would steal another
// allocation's binding — happens in the op and fails closed; this parser only
// checks the file's own shape.

// ResolutionDecisionFile is resolve's --file input. Revision is the day
// revision copied verbatim from the reconcile receipt (the op fails closed on
// a stale one); ObservationID and RemoteID identify the exact observation and
// observed entry the human reviewed; LocalID names the confirmed durable local
// interval for adopt and accept_observed and must be omitted for foreign.
type ResolutionDecisionFile struct {
	Revision      int    `json:"revision"`
	ObservationID string `json:"observation_id"`
	Decision      string `json:"decision"`
	RemoteID      string `json:"remote_id"`
	LocalID       string `json:"local_id,omitempty"`
}

// ParseResolutionDecision parses resolve's --file input strictly. A file that
// is not valid JSON names the expected schema so it is diagnosable without
// reading it; a missing or negative revision is a validation error (the op
// itself requires the revision and refuses a stale one). The decision value
// and the id relationships are the op's own fail-closed checks, passed through
// verbatim.
func ParseResolutionDecision(data []byte) (ResolutionDecisionFile, error) {
	var raw struct {
		Revision      *int   `json:"revision"`
		ObservationID string `json:"observation_id"`
		Decision      string `json:"decision"`
		RemoteID      string `json:"remote_id"`
		LocalID       string `json:"local_id"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return ResolutionDecisionFile{}, newError(CodeBadRequest,
			`resolve: the decision file is not valid JSON for the expected schema {"revision": <n>, "observation_id": "...", "decision": "adopt|accept_observed|foreign", "remote_id": "...", "local_id": "..."} (local_id omitted for foreign)`)
	}
	if raw.Revision == nil {
		return ResolutionDecisionFile{}, newError(CodeBadRequest,
			"resolve: the decision file is missing revision; copy it verbatim from the reconcile receipt")
	}
	if *raw.Revision < 0 {
		return ResolutionDecisionFile{}, newError(CodeBadRequest,
			"resolve: the decision file's revision must not be negative")
	}
	return ResolutionDecisionFile{
		Revision: *raw.Revision, ObservationID: raw.ObservationID, Decision: raw.Decision,
		RemoteID: raw.RemoteID, LocalID: raw.LocalID,
	}, nil
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
