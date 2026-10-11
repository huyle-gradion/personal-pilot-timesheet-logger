package proposal

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
)

// Build turns collected evidence into a deterministic, unconfirmed proposal for
// one local day. It confirms nothing: git durations stay unknown, meetings carry
// a calendar-known duration with attendance unresolved, and manual intervals are
// proposed (not confirmed). Cancelled/declined/unknown-invitation evidence is
// retained as Excluded, never dropped. Identical input in any order yields an
// identical Proposal.
func Build(ev []evidence.Evidence, opts ProposalOptions) (Proposal, error) {
	loc := opts.Location
	if loc == nil {
		loc = time.UTC
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	date := opts.Date
	if date.IsZero() {
		date = now.In(loc)
	}
	dayStart := time.Date(date.In(loc).Year(), date.In(loc).Month(), date.In(loc).Day(), 0, 0, 0, 0, loc)
	dateStr := dayStart.Format(defaultDateLayout)

	gap := opts.AdjacencyGap
	if gap <= 0 {
		gap = DefaultAdjacencyGap
	}
	maxEntry := opts.MaxEntryMinutes
	if maxEntry <= 0 {
		maxEntry = DefaultMaxEntryMinutes
	}

	prop := Proposal{Date: dateStr}
	if len(ev) == 0 {
		prop.Warnings = normalizeWarnings([]string{"empty_day"})
		return prop, nil
	}

	var calendarEv, gitEv, manualEv []evidence.Evidence
	var journalEv []evidence.Evidence
	for _, e := range ev {
		if !evidence.IsProposalInput(e) {
			prop.Excluded = append(prop.Excluded, ExcludedEvidence{
				EvidenceID: e.ID, Kind: e.Kind, Reason: exclusionReason(e),
			})
			continue
		}
		switch e.Kind {
		case evidence.SourceCalendar:
			calendarEv = append(calendarEv, e)
		case evidence.SourceLocalGit, evidence.SourceRemotePR:
			// Git evidence must carry a repository identity; otherwise exported
			// Build could silently merge two repo-less items on one branch.
			if topicKeyOf(e).repository == "" {
				prop.Excluded = append(prop.Excluded, ExcludedEvidence{
					EvidenceID: e.ID, Kind: e.Kind, Reason: ExcludeMissingRepository,
				})
				continue
			}
			gitEv = append(gitEv, e)
		case evidence.SourceManual:
			manualEv = append(manualEv, e)
		case evidence.SourceJournal:
			// A detached journal item is retained as visible history and is
			// never re-proposed as a narrative candidate.
			if e.JournalState == evidence.JournalDetached {
				prop.Excluded = append(prop.Excluded, excludedFromEvidence(e, ExcludeDetached))
				continue
			}
			journalEv = append(journalEv, e)
		case evidence.SourceAgentSession:
			// Context only: visible, but never a block, span, or interruption.
			prop.Excluded = append(prop.Excluded, ExcludedEvidence{
				EvidenceID: e.ID, Kind: e.Kind, Reason: ExcludeAgentContextOnly,
			})
		default:
			prop.Excluded = append(prop.Excluded, ExcludedEvidence{
				EvidenceID: e.ID, Kind: e.Kind, Reason: ExcludeUnsupportedKind,
			})
		}
	}

	var warnings []string

	meetings, meetingCarry := buildMeetings(calendarEv, loc, dateStr, opts.ProjectMap)
	for _, d := range meetingCarry {
		warnings = append(warnings, "carryover:"+d)
	}
	manual, manualCarry := buildManual(manualEv, loc, dateStr, opts.ProjectMap)
	for _, d := range manualCarry {
		warnings = append(warnings, "carryover:"+d)
	}

	meetingIntervals := make([]meetingInterval, 0, len(meetings))
	for _, m := range meetings {
		meetingIntervals = append(meetingIntervals, meetingInterval{ID: m.ID, Start: m.StartAt, End: m.EndAt})
	}
	sort.Slice(meetingIntervals, func(i, j int) bool {
		if meetingIntervals[i].Start.Equal(meetingIntervals[j].Start) {
			return meetingIntervals[i].ID < meetingIntervals[j].ID
		}
		return meetingIntervals[i].Start.Before(meetingIntervals[j].Start)
	})

	prop.Topics = buildTopics(gitEv, meetingIntervals, gap)

	// A meeting that overlaps any git group marks both the meeting (Collision) and
	// the group (InterruptedBy, set during grouping).
	groupSpans := collectGroupSpans(prop.Topics)
	for i := range meetings {
		for _, gs := range groupSpans {
			if intervalsOverlap(meetings[i].StartAt, meetings[i].EndAt, gs.start, gs.end) {
				meetings[i].Collision = true
				warnings = append(warnings, "collision:"+meetings[i].ID)
				break
			}
		}
	}

	// A block longer than the maximum entry length is split into linked fragments
	// (the four-hour rule) at assignment time, so every proposed block is at most
	// maxEntry minutes and the exact totals are preserved. Meetings are split at
	// midnight first (in buildMeetings), so a fragment never crosses a day.
	prop.Meetings = splitBlocks(meetings, maxEntry, loc)
	prop.Manual = splitBlocks(manual, maxEntry, loc)
	prop.Work = splitBlocks(buildWorkBlocks(prop.Topics, gitEv, meetings, opts.ProjectMap, loc), maxEntry, loc)
	prop.Narratives = buildNarratives(journalEv, dateStr)
	sortProposal(&prop)
	prop.Warnings = normalizeWarnings(warnings)
	return prop, nil
}

// buildWorkBlocks materializes each git Activity Group into a ProposedTimeBlock.
// A group interrupted by a meeting becomes a joint block (Kind=joint) combining
// the meeting and the commits (union span, meeting-known duration, both texts,
// Collision); otherwise it is a work block with an unknown duration. Project
// hints come from the git adapter (resolved via ProjectMap, never titles).
func buildWorkBlocks(topics []TopicCandidate, gitEv []evidence.Evidence, meetings []ProposedTimeBlock, projectMap map[string]string, loc *time.Location) []ProposedTimeBlock {
	evByID := make(map[string]evidence.Evidence, len(gitEv))
	for _, e := range gitEv {
		evByID[e.ID] = e
	}

	var out []ProposedTimeBlock
	for _, t := range topics {
		for _, g := range t.Groups {
			var hints []string
			for _, id := range g.EvidenceIDs {
				if e, ok := evByID[id]; ok {
					hints = append(hints, e.ProjectHints...)
				}
			}
			project := ResolveProject(projectMap, t.Repository, hints)
			title := gitGroupTitle(g, evByID)

			var meeting *ProposedTimeBlock
			for i := range meetings {
				if intervalsOverlap(meetings[i].StartAt, meetings[i].EndAt, g.SpanStart, g.SpanEnd) {
					meeting = &meetings[i]
					break
				}
			}

			if meeting != nil {
				start := earlierTime(g.SpanStart, meeting.StartAt)
				end := laterTime(g.SpanEnd, meeting.EndAt)
				out = append(out, ProposedTimeBlock{
					ID:            hashID("joint", t.ID, g.ID, meeting.ID),
					Kind:          BlockJoint,
					Date:          start.In(loc).Format(defaultDateLayout),
					Start:         start.In(loc).Format(defaultDisplayTimeLayout),
					End:           end.In(loc).Format(defaultDisplayTimeLayout),
					StartAt:       start,
					EndAt:         end,
					Duration:      meeting.Duration,
					Project:       project,
					Description:   joinNonEmpty(title, meeting.Description),
					EvidenceIDs:   uniqueSorted(append(append([]string{}, g.EvidenceIDs...), meeting.EvidenceIDs...)),
					Attendance:    AttendanceUnresolved,
					Collision:     true,
					GridSuggested: !onGrid(start, loc),
				})
				continue
			}

			out = append(out, ProposedTimeBlock{
				ID:            hashID("work", t.ID, g.ID),
				Kind:          BlockWork,
				Date:          g.SpanStart.In(loc).Format(defaultDateLayout),
				Start:         g.SpanStart.In(loc).Format(defaultDisplayTimeLayout),
				End:           g.SpanEnd.In(loc).Format(defaultDisplayTimeLayout),
				StartAt:       g.SpanStart,
				EndAt:         g.SpanEnd,
				Duration:      evidence.UnknownDuration(),
				Project:       project,
				Description:   title,
				EvidenceIDs:   append([]string{}, g.EvidenceIDs...),
				Attendance:    AttendanceUnresolved,
				Collision:     len(g.InterruptedBy) > 0,
				GridSuggested: !onGrid(g.SpanStart, loc),
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].StartAt.Equal(out[j].StartAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].StartAt.Before(out[j].StartAt)
	})
	return out
}

// gitGroupTitle returns the first non-empty evidence title in a group.
func gitGroupTitle(g ActivityGroup, evByID map[string]evidence.Evidence) string {
	for _, id := range g.EvidenceIDs {
		if e, ok := evByID[id]; ok {
			if t := strings.TrimSpace(e.Title); t != "" {
				return t
			}
		}
	}
	return ""
}

func earlierTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func laterTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func joinNonEmpty(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			kept = append(kept, s)
		}
	}
	return strings.Join(kept, "\n")
}

// exclusionReason maps a filtered-out evidence item to a stable reason.
func exclusionReason(e evidence.Evidence) ExclusionReason {
	if e.Status == evidence.EventStatusCancelled {
		return ExcludeCancelled
	}
	switch e.Invitation {
	case evidence.InvitationDeclined:
		return ExcludeDeclined
	case evidence.InvitationUnknown:
		return ExcludeUnknownInvitation
	default:
		return ExcludeUnknownInvitation
	}
}

// splitForDay splits an interval at local midnight, returning the same-day
// fragments and the distinct carryover dates (next-day fragments).
func splitForDay(start, end time.Time, loc *time.Location, dateStr string) ([]Fragment, []string) {
	frags := SplitAtMidnight(start, end, loc)
	if len(frags) == 0 {
		frags = []Fragment{{
			StartAt: start, EndAt: end,
			Minutes: DurationMinutes(start, end),
			Date:    start.In(loc).Format(defaultDateLayout),
		}}
	}
	var same []Fragment
	var carry []string
	for _, f := range frags {
		if f.Date == dateStr {
			same = append(same, f)
		} else {
			carry = append(carry, f.Date)
		}
	}
	return same, carry
}

// buildMeetings builds meeting blocks from calendar evidence.
func buildMeetings(calendarEv []evidence.Evidence, loc *time.Location, dateStr string, projectMap map[string]string) ([]ProposedTimeBlock, []string) {
	var blocks []ProposedTimeBlock
	var carry []string
	for _, e := range calendarEv {
		start, end := e.Start, e.End
		if start.IsZero() {
			continue
		}
		if end.Before(start) {
			end = start
		}
		frags, c := splitForDay(start, end, loc, dateStr)
		carry = append(carry, c...)
		for _, f := range frags {
			dur := evidence.UnknownDuration()
			if e.Duration.Known {
				dur = evidence.KnownDuration(f.Minutes)
			}
			blocks = append(blocks, ProposedTimeBlock{
				ID:            hashID("meeting", dateStr, f.StartAt.UTC().Format(time.RFC3339Nano), e.ID),
				Kind:          BlockMeeting,
				Date:          f.Date,
				Start:         f.StartAt.In(loc).Format(defaultDisplayTimeLayout),
				End:           f.EndAt.In(loc).Format(defaultDisplayTimeLayout),
				StartAt:       f.StartAt,
				EndAt:         f.EndAt,
				Duration:      dur,
				Project:       ResolveProject(projectMap, "", e.ProjectHints),
				Description:   blockDescription(e),
				EvidenceIDs:   []string{e.ID},
				Attendance:    AttendanceUnresolved,
				Preselected:   e.Invitation == evidence.InvitationAccepted,
				GridSuggested: !onGrid(f.StartAt, loc),
			})
		}
	}
	return blocks, carry
}

// buildManual builds manual blocks. Manual intervals are user-provided evidence
// but are PROPOSED, not confirmed: Preselected is always false so an explicit
// confirm step is required.
func buildManual(manualEv []evidence.Evidence, loc *time.Location, dateStr string, projectMap map[string]string) ([]ProposedTimeBlock, []string) {
	var blocks []ProposedTimeBlock
	var carry []string
	for _, e := range manualEv {
		start, end := e.Start, e.End
		if start.IsZero() {
			continue
		}
		if end.Before(start) {
			end = start
		}
		frags, c := splitForDay(start, end, loc, dateStr)
		carry = append(carry, c...)
		for _, f := range frags {
			blocks = append(blocks, ProposedTimeBlock{
				ID:            hashID("manual", dateStr, f.StartAt.UTC().Format(time.RFC3339Nano), e.ID),
				Kind:          BlockManual,
				Date:          f.Date,
				Start:         f.StartAt.In(loc).Format(defaultDisplayTimeLayout),
				End:           f.EndAt.In(loc).Format(defaultDisplayTimeLayout),
				StartAt:       f.StartAt,
				EndAt:         f.EndAt,
				Duration:      evidence.KnownDuration(f.Minutes),
				Project:       ResolveProject(projectMap, "", e.ProjectHints),
				Description:   blockDescription(e),
				EvidenceIDs:   manualEvidenceIDs(e),
				Attendance:    AttendanceUnresolved,
				Preselected:   false,
				GridSuggested: !onGrid(f.StartAt, loc),
			})
		}
	}
	return blocks, carry
}

// manualEvidenceIDs returns a manual item's own identity followed by the linked
// source evidence IDs it cites. The linked IDs add NO minutes: the manual
// interval's own duration is authoritative. The result is sorted and deduped so
// the proposed block (and any four-hour fragment copied from it) is stable.
func manualEvidenceIDs(e evidence.Evidence) []string {
	return uniqueSorted(append([]string{e.ID}, e.LinkedEvidenceIDs...))
}

// blockDescription returns source-derived text only.
func blockDescription(e evidence.Evidence) string {
	if d := strings.TrimSpace(e.Description); d != "" {
		return d
	}
	return strings.TrimSpace(e.Title)
}

// buildNarratives turns active journal evidence into UNTIMED narrative
// candidates. Candidates are sorted deterministically by (section, id) so
// identical input in any order yields an identical result. Project hints are
// raw, non-billing hints; the duration is always UNKNOWN and no times are set.
func buildNarratives(journalEv []evidence.Evidence, dateStr string) []NarrativeCandidate {
	out := make([]NarrativeCandidate, 0, len(journalEv))
	for _, e := range journalEv {
		section := ""
		if len(e.SourceRefs) > 0 {
			section = strings.TrimSpace(e.SourceRefs[0].Section)
		}
		out = append(out, NarrativeCandidate{
			ID:           e.ID,
			Section:      section,
			Date:         dateStr,
			Title:        strings.TrimSpace(e.Title),
			Description:  strings.TrimSpace(e.Description),
			EvidenceIDs:  []string{e.ID},
			ProjectHints: dedupSortedStrings(e.ProjectHints),
			Duration:     evidence.UnknownDuration(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Section == out[j].Section {
			return out[i].ID < out[j].ID
		}
		return out[i].Section < out[j].Section
	})
	return out
}

// excludedFromEvidence builds an excluded entry carrying any journal provenance
// (title/path/heading/line/date) from the evidence, so a detached item is
// identifiable in review rather than an opaque id.
func excludedFromEvidence(e evidence.Evidence, reason ExclusionReason) ExcludedEvidence {
	ex := ExcludedEvidence{
		EvidenceID: e.ID,
		Kind:       e.Kind,
		Reason:     reason,
		Title:      strings.TrimSpace(e.Title),
	}
	for _, ref := range e.SourceRefs {
		if ref.Kind != evidence.SourceJournal {
			continue
		}
		ex.Section = ref.Section
		ex.NotePath = ref.NotePath
		ex.HeadingPath = ref.HeadingPath
		ex.LineStart = ref.LineStart
		ex.LineEnd = ref.LineEnd
		ex.StatedDate = ref.StatedDate
		break
	}
	return ex
}

// dedupSortedStrings returns the sorted, deduped, non-empty values.
func dedupSortedStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	var out []string
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// topicKey is the identity of a topic: repository plus PR number when a PR is
// known, otherwise repository plus branch.
type topicKey struct {
	repository string
	prNumber   int
	branch     string
}

func topicKeyOf(e evidence.Evidence) topicKey {
	repo, pr, branch := "", 0, ""
	if e.TopicHint != nil {
		repo = strings.TrimSpace(e.TopicHint.Repository)
		pr = e.TopicHint.PRNumber
		branch = strings.TrimSpace(e.TopicHint.Branch)
	}
	if repo == "" {
		for _, ref := range e.SourceRefs {
			if strings.TrimSpace(ref.Repository) != "" {
				repo = strings.TrimSpace(ref.Repository)
				break
			}
		}
	}
	if pr > 0 {
		branch = "" // PR identity ignores the branch name
	}
	return topicKey{repository: repo, prNumber: pr, branch: branch}
}

// buildTopics partitions git evidence into topics and forms activity groups.
func buildTopics(gitEv []evidence.Evidence, meetings []meetingInterval, gap time.Duration) []TopicCandidate {
	type topicData struct {
		key topicKey
		ids []string
		pts []point
	}
	byKey := make(map[topicKey]*topicData)
	for _, e := range gitEv {
		k := topicKeyOf(e)
		td, ok := byKey[k]
		if !ok {
			td = &topicData{key: k}
			byKey[k] = td
		}
		td.ids = append(td.ids, e.ID)
		td.pts = append(td.pts, pointsFor(e)...)
	}

	topics := make([]TopicCandidate, 0, len(byKey))
	for _, td := range byKey {
		topicID := hashID("topic", td.key.repository, strconv.Itoa(td.key.prNumber), td.key.branch)
		groups := formGroups(topicID, td.pts, meetings, gap)
		sort.Slice(groups, func(i, j int) bool {
			if groups[i].SpanStart.Equal(groups[j].SpanStart) {
				return groups[i].ID < groups[j].ID
			}
			return groups[i].SpanStart.Before(groups[j].SpanStart)
		})
		topics = append(topics, TopicCandidate{
			ID:          topicID,
			Kind:        BlockWork,
			Repository:  td.key.repository,
			PRNumber:    td.key.prNumber,
			Branch:      td.key.branch,
			EvidenceIDs: uniqueSorted(td.ids),
			Groups:      groups,
		})
	}
	sort.Slice(topics, func(i, j int) bool { return topics[i].ID < topics[j].ID })
	return topics
}

type span struct {
	start time.Time
	end   time.Time
}

func collectGroupSpans(topics []TopicCandidate) []span {
	var spans []span
	for _, t := range topics {
		for _, g := range t.Groups {
			spans = append(spans, span{start: g.SpanStart, end: g.SpanEnd})
		}
	}
	return spans
}

func sortProposal(p *Proposal) {
	sort.Slice(p.Work, func(i, j int) bool {
		if p.Work[i].StartAt.Equal(p.Work[j].StartAt) {
			return p.Work[i].ID < p.Work[j].ID
		}
		return p.Work[i].StartAt.Before(p.Work[j].StartAt)
	})
	sort.Slice(p.Meetings, func(i, j int) bool {
		if p.Meetings[i].StartAt.Equal(p.Meetings[j].StartAt) {
			return p.Meetings[i].ID < p.Meetings[j].ID
		}
		return p.Meetings[i].StartAt.Before(p.Meetings[j].StartAt)
	})
	sort.Slice(p.Manual, func(i, j int) bool {
		if p.Manual[i].StartAt.Equal(p.Manual[j].StartAt) {
			return p.Manual[i].ID < p.Manual[j].ID
		}
		return p.Manual[i].StartAt.Before(p.Manual[j].StartAt)
	})
	sort.Slice(p.Excluded, func(i, j int) bool {
		if p.Excluded[i].EvidenceID == p.Excluded[j].EvidenceID {
			return p.Excluded[i].Reason < p.Excluded[j].Reason
		}
		return p.Excluded[i].EvidenceID < p.Excluded[j].EvidenceID
	})
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]bool, len(values))
	var out []string
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func normalizeWarnings(warnings []string) []string {
	if len(warnings) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(warnings))
	var out []string
	for _, w := range warnings {
		w = strings.TrimSpace(w)
		if w == "" || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

// onGrid reports whether t sits on the review grid in loc.
func onGrid(t time.Time, loc *time.Location) bool {
	lt := t.In(loc)
	return lt.Minute()%DefaultGridMinutes == 0 && lt.Second() == 0 && lt.Nanosecond() == 0
}

// SplitBlock splits a proposed block whose SPAN exceeds maxMinutes into linked,
// contiguous, non-overlapping fragments of at most maxMinutes, preserving the
// exact total span minutes. Fragments share a SplitGroupID with sequential
// PartIndex values and copy every other field (identity changes to a stable
// per-fragment ID). A block within the bound, or with an unusable span, is
// returned unchanged as a single-element slice, so callers never branch.
func SplitBlock(block ProposedTimeBlock, maxMinutes int, loc *time.Location) []ProposedTimeBlock {
	if maxMinutes <= 0 {
		maxMinutes = DefaultMaxEntryMinutes
	}
	if loc == nil {
		loc = time.UTC
	}
	if block.StartAt.IsZero() || block.EndAt.IsZero() || !block.EndAt.After(block.StartAt) {
		return []ProposedTimeBlock{block}
	}
	if DurationMinutes(block.StartAt, block.EndAt) <= maxMinutes {
		return []ProposedTimeBlock{block}
	}

	frags := SplitAtMaxDuration(block.StartAt, block.EndAt, maxMinutes, loc)
	out := make([]ProposedTimeBlock, 0, len(frags))
	for _, f := range frags {
		b := block
		b.ID = hashID("split", block.ID, strconv.Itoa(f.PartIndex))
		b.Date = f.Date
		b.Start = f.StartAt.In(loc).Format(defaultDisplayTimeLayout)
		b.End = f.EndAt.In(loc).Format(defaultDisplayTimeLayout)
		b.StartAt = f.StartAt
		b.EndAt = f.EndAt
		b.SplitGroupID = f.SplitGroupID
		b.PartIndex = f.PartIndex
		if block.Duration.Known {
			b.Duration = evidence.KnownDuration(f.Minutes)
		}
		b.GridSuggested = !onGrid(f.StartAt, loc)
		out = append(out, b)
	}
	return out
}

// splitBlocks applies SplitBlock to every block, concatenating the fragments.
func splitBlocks(blocks []ProposedTimeBlock, maxMinutes int, loc *time.Location) []ProposedTimeBlock {
	out := make([]ProposedTimeBlock, 0, len(blocks))
	for _, b := range blocks {
		out = append(out, SplitBlock(b, maxMinutes, loc)...)
	}
	return out
}
