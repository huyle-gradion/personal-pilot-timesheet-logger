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

	for _, b := range meetings {
		if b.Duration.Known && b.Duration.Minutes > maxEntry {
			warnings = append(warnings, "exceeds_max_entry:"+b.ID)
		}
	}
	for _, b := range manual {
		if b.Duration.Known && b.Duration.Minutes > maxEntry {
			warnings = append(warnings, "exceeds_max_entry:"+b.ID)
		}
	}

	prop.Meetings = meetings
	prop.Manual = manual
	sortProposal(&prop)
	prop.Warnings = normalizeWarnings(warnings)
	return prop, nil
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
				EvidenceIDs:   []string{e.ID},
				Attendance:    AttendanceUnresolved,
				Preselected:   false,
				GridSuggested: !onGrid(f.StartAt, loc),
			})
		}
	}
	return blocks, carry
}

// blockDescription returns source-derived text only.
func blockDescription(e evidence.Evidence) string {
	if d := strings.TrimSpace(e.Description); d != "" {
		return d
	}
	return strings.TrimSpace(e.Title)
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
