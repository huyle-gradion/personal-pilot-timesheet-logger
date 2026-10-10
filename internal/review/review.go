// Package review implements the S4 review/confirmation layer: pure actions that
// mutate a workflow.Record and are applied inside workflow.Store.UpdateDay or a
// Session.Update, plus a readiness gate. It persists only through the S1 store.
//
// It confirms nothing automatically: manual intervals require an explicit
// ConfirmProposed; a meeting marked not-attended is excluded from work but
// retained as visible evidence; a reviewed empty day stays empty (no zero-hour
// block).
package review

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/proposal"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// Typed operating errors.
var (
	ErrNilRecord        = errors.New("review: nil record")
	ErrBlockNotFound    = errors.New("review: confirmed block not found")
	ErrEvidenceNotFound = errors.New("review: collected evidence not found")
	ErrInvalidInterval  = errors.New("review: invalid interval")
)

// Stable exclusion reasons.
const (
	ReasonNotAttended  = "not_attended"
	ReasonUserExcluded = "user_excluded"
)

const (
	displayLayout = "15:04"
	dateLayout    = "2006-01-02"
	minutesPerDay = 24 * 60
)

// nowFunc is the injectable clock. Tests override it with SetClock.
var nowFunc = func() time.Time { return time.Now().UTC() }

// SetClock overrides the package clock (tests only).
func SetClock(f func() time.Time) { nowFunc = f }

// ConfirmOptions tunes ConfirmProposed.
type ConfirmOptions struct {
	Project    string
	Attendance *bool
	Now        time.Time
	// Location is the display location used when a block must be split to the
	// maximum entry length (nil means UTC).
	Location *time.Location
}

// ManualInterval is a user-entered interval. AddManualInterval turns it into a
// SourceManual evidence item and a PROPOSED manual block (via the next proposal
// recompute), never a confirmed interval. UUID is the stable identity component;
// when empty a fresh UUID is generated. Editing the description (same UUID) keeps
// the same evidence identity.
type ManualInterval struct {
	UUID        string
	StartAt     time.Time
	EndAt       time.Time
	Project     string
	Description string
	Now         time.Time
}

// ConfirmMeetingsBulk confirms a set of meetings at once. attended=true creates
// one ConfirmedInterval{Attendance:true} per meeting; attended=false records an
// attendance decision and EXCLUDES the meeting from work, retaining the evidence.
func ConfirmMeetingsBulk(r *workflow.Record, evIDs []string, attended bool) error {
	if r == nil {
		return ErrNilRecord
	}
	now := nowFunc()
	for _, id := range evIDs {
		e, ok := findCollectedEvidence(r, id)
		if !ok {
			return fmt.Errorf("%w: %s", ErrEvidenceNotFound, id)
		}
		if !attended {
			markAttendance(r, id, false, now)
			// Remove/supersede any previously confirmed interval for this meeting
			// (evidence retained).
			if err := ExcludeEvidence(r, id, ReasonNotAttended); err != nil {
				return err
			}
			continue
		}
		start := e.Start.Format(displayLayout)
		end := e.End.Format(displayLayout)
		upsertConfirmed(r, workflow.ConfirmedInterval{
			ID:            hashID("conf-meeting", r.Date, id),
			Date:          r.Date,
			StartTime:     start,
			EndTime:       end,
			Project:       firstHint(e.ProjectHints),
			Description:   evidenceText(e),
			Attendance:    true,
			Kind:          string(proposal.BlockMeeting),
			EvidenceIDs:   []string{id},
			OriginalStart: start,
			OriginalEnd:   end,
			ConfirmedAt:   now,
		})
	}
	return nil
}

// ConfirmProposed confirms a proposed block as one ConfirmedInterval. Manual
// blocks are marked Manual; a meeting's attendance defaults to its preselection.
func ConfirmProposed(r *workflow.Record, block proposal.ProposedTimeBlock, opts ConfirmOptions) error {
	if r == nil {
		return ErrNilRecord
	}
	now := opts.Now
	if now.IsZero() {
		now = nowFunc()
	}
	attendance := block.Kind != proposal.BlockMeeting || block.Preselected
	if opts.Attendance != nil {
		attendance = *opts.Attendance
	}
	project := block.Project.Project
	if p := strings.TrimSpace(opts.Project); p != "" {
		project = p
	}
	date := block.Date
	if date == "" {
		date = r.Date
	}
	// A block whose span exceeds the maximum entry length is persisted as linked,
	// contiguous fragments (the four-hour rule), each carrying the shared
	// SplitGroupID and a sequential PartIndex; the exact total minutes are
	// preserved. A block within the bound persists as a single interval.
	for _, b := range proposal.SplitBlock(block, proposal.DefaultMaxEntryMinutes, opts.Location) {
		bDate := b.Date
		if bDate == "" {
			bDate = date
		}
		upsertConfirmed(r, workflow.ConfirmedInterval{
			ID:            hashID("conf-"+string(b.Kind), bDate, b.ID),
			Date:          bDate,
			StartTime:     b.Start,
			EndTime:       b.End,
			Project:       project,
			Description:   b.Description,
			Attendance:    attendance,
			Kind:          string(b.Kind),
			Manual:        b.Kind == proposal.BlockManual,
			EvidenceIDs:   append([]string{}, b.EvidenceIDs...),
			OriginalStart: b.Start,
			OriginalEnd:   b.End,
			GridAdjusted:  b.GridSuggested,
			SplitGroupID:  b.SplitGroupID,
			PartIndex:     b.PartIndex,
			ConfirmedAt:   now,
		})
	}
	return nil
}

// ResolveProject sets the project on a confirmed block.
func ResolveProject(r *workflow.Record, blockID, project string) error {
	if r == nil {
		return ErrNilRecord
	}
	ci := findConfirmed(r, blockID)
	if ci == nil {
		return fmt.Errorf("%w: %s", ErrBlockNotFound, blockID)
	}
	ci.Project = strings.TrimSpace(project)
	return nil
}

// JoinCollision joins a work block and a meeting into ONE joint ConfirmedInterval
// (union span, both evidence IDs, both texts). It counts once: the duration is
// the meeting's known duration and the original work interval is replaced.
func JoinCollision(r *workflow.Record, workBlockID, meetingEvID string) error {
	if r == nil {
		return ErrNilRecord
	}
	work := findConfirmed(r, workBlockID)
	if work == nil {
		return fmt.Errorf("%w: %s", ErrBlockNotFound, workBlockID)
	}
	meeting, ok := findCollectedEvidence(r, meetingEvID)
	if !ok {
		return fmt.Errorf("%w: %s", ErrEvidenceNotFound, meetingEvID)
	}

	start, end := unionSpan(work.StartTime, work.EndTime, meeting.Start.Format(displayLayout), meeting.End.Format(displayLayout))
	joint := *work
	joint.ID = hashID("conf-joint", work.Date, workBlockID, meetingEvID)
	joint.Kind = string(proposal.BlockJoint)
	joint.Manual = false
	joint.StartTime = start
	joint.EndTime = end
	joint.EvidenceIDs = unionStrings(work.EvidenceIDs, []string{meetingEvID})
	joint.Description = joinTexts(work.Description, evidenceText(meeting))
	joint.Attendance = true
	joint.ConfirmedAt = nowFunc()

	removeConfirmed(r, workBlockID)
	upsertConfirmed(r, joint)
	return nil
}

// TrimWork adjusts a confirmed block's start/end, preserving the originals and
// recording the offset in minutes.
func TrimWork(r *workflow.Record, blockID, newStart, newEnd string) error {
	if r == nil {
		return ErrNilRecord
	}
	ci := findConfirmed(r, blockID)
	if ci == nil {
		return fmt.Errorf("%w: %s", ErrBlockNotFound, blockID)
	}
	sm, ok1 := parseHHMM(newStart)
	em, ok2 := parseHHMM(newEnd)
	if !ok1 || !ok2 || em < sm {
		return ErrInvalidInterval
	}
	if ci.OriginalStart == "" {
		ci.OriginalStart = ci.StartTime
	}
	if ci.OriginalEnd == "" {
		ci.OriginalEnd = ci.EndTime
	}
	if old, ok := parseHHMM(ci.StartTime); ok {
		ci.StartOffsetMin = sm - old
	}
	if old, ok := parseHHMM(ci.EndTime); ok {
		off := em - old
		ci.EndOffsetMin = &off
	}
	ci.StartTime = newStart
	ci.EndTime = newEnd
	ci.GridAdjusted = !onGridStr(newStart) || !onGridStr(newEnd)
	return nil
}

// ExcludeEvidence records an exclusion and removes the evidence from confirmed
// work. The evidence itself is retained. A confirmed interval that loses all of
// its evidence-backed references is dropped.
func ExcludeEvidence(r *workflow.Record, evID, reason string) error {
	if r == nil {
		return ErrNilRecord
	}
	if strings.TrimSpace(reason) == "" {
		reason = ReasonUserExcluded
	}
	addExclusion(r, evID, reason, nowFunc())

	kept := r.Confirmed[:0]
	for _, ci := range r.Confirmed {
		if len(ci.EvidenceIDs) == 0 {
			kept = append(kept, ci)
			continue
		}
		ids := removeString(ci.EvidenceIDs, evID)
		if len(ids) == 0 && !ci.Manual {
			continue
		}
		ci.EvidenceIDs = ids
		kept = append(kept, ci)
	}
	r.Confirmed = kept
	return nil
}

// AddManualInterval appends or updates a SourceManual evidence item keyed by its
// stable identity evidence.ManualID(date, uuid). It does NOT confirm work: the
// proposed manual block materializes on the next proposal recompute and must be
// confirmed explicitly with ConfirmProposed. Editing the description with the
// same UUID updates the same evidence item instead of creating a duplicate.
func AddManualInterval(r *workflow.Record, m ManualInterval) error {
	if r == nil {
		return ErrNilRecord
	}
	if !m.EndAt.After(m.StartAt) {
		return ErrInvalidInterval
	}
	now := m.Now
	if now.IsZero() {
		now = nowFunc()
	}
	uuid := strings.TrimSpace(m.UUID)
	if uuid == "" {
		uuid = newUUID()
	}
	date := r.Date
	if date == "" {
		date = m.StartAt.Format(dateLayout)
	}
	id := evidence.ManualID(date, uuid)

	title := strings.TrimSpace(m.Description)
	if title == "" {
		title = "Manual interval"
	}

	for i := range r.CollectedEvidence {
		if r.CollectedEvidence[i].ID != id {
			continue
		}
		r.CollectedEvidence[i].Title = title
		r.CollectedEvidence[i].Description = strings.TrimSpace(m.Description)
		r.CollectedEvidence[i].Start = m.StartAt
		r.CollectedEvidence[i].End = m.EndAt
		r.CollectedEvidence[i].Duration = evidence.KnownDuration(proposal.DurationMinutes(m.StartAt, m.EndAt))
		if p := strings.TrimSpace(m.Project); p != "" {
			r.CollectedEvidence[i].ProjectHints = []string{p}
		}
		return nil
	}

	ev := evidence.Evidence{
		ID:          id,
		Kind:        evidence.SourceManual,
		Title:       title,
		Description: strings.TrimSpace(m.Description),
		Start:       m.StartAt,
		End:         m.EndAt,
		Duration:    evidence.KnownDuration(proposal.DurationMinutes(m.StartAt, m.EndAt)),
		SourceRefs:  []evidence.SourceRef{{Kind: evidence.SourceManual}},
		ObservedAt:  now,
	}
	if p := strings.TrimSpace(m.Project); p != "" {
		ev.ProjectHints = []string{p}
	}
	r.CollectedEvidence = append(r.CollectedEvidence, ev)
	return nil
}

// MarkReviewedEmpty records a reviewed day with no work, preserving any prior
// exclusions, attendance decisions, and acknowledgments. Confirmed stays
// untouched/empty: no zero-hour block is invented.
func MarkReviewedEmpty(r *workflow.Record) error {
	if r == nil {
		return ErrNilRecord
	}
	if r.Review == nil {
		r.Review = &workflow.ReviewState{}
	}
	r.Review.Reviewed = true
	r.Review.Empty = true
	r.Review.ReviewedAt = nowFunc()
	return nil
}

// AcknowledgeSources records explicit acknowledgment of incomplete sources.
func AcknowledgeSources(r *workflow.Record, sourceIDs []string) error {
	if r == nil {
		return ErrNilRecord
	}
	if r.Review == nil {
		r.Review = &workflow.ReviewState{}
	}
	r.Review.AcknowledgedSources = unionStrings(r.Review.AcknowledgedSources, sourceIDs)
	return nil
}

// MarkReviewed marks the day reviewed and records the proposal hash it was
// reviewed against.
func MarkReviewed(r *workflow.Record, proposalHash string) error {
	if r == nil {
		return ErrNilRecord
	}
	if r.Review == nil {
		r.Review = &workflow.ReviewState{}
	}
	r.Review.Reviewed = true
	r.Review.ReviewedAt = nowFunc()
	if proposalHash != "" {
		r.Review.LastProposalHash = proposalHash
	}
	return nil
}

// CurrentProposal recomputes the proposal from the record's collected evidence,
// honoring Review.ExcludedEvidence: excluded evidence is filtered out of proposed
// blocks and surfaced as excluded instead. It populates Changed with evidence IDs
// not covered by confirmed/excluded decisions (or confirmed/excluded IDs no
// longer collected). Nothing is auto-incorporated.
func CurrentProposal(r workflow.Record, opts proposal.ProposalOptions) (proposal.Proposal, error) {
	excludedReason := map[string]string{}
	if r.Review != nil {
		for _, ex := range r.Review.ExcludedEvidence {
			excludedReason[ex.EvidenceID] = ex.Reason
		}
	}

	var included []evidence.Evidence
	var excluded []evidence.Evidence
	for _, e := range r.CollectedEvidence {
		if _, ok := excludedReason[e.ID]; ok {
			excluded = append(excluded, e)
			continue
		}
		included = append(included, e)
	}

	prop, err := proposal.Build(included, opts)
	if err != nil {
		return prop, err
	}

	for _, e := range excluded {
		prop.Excluded = append(prop.Excluded, proposal.ExcludedEvidence{
			EvidenceID: e.ID,
			Kind:       e.Kind,
			Reason:     proposal.ExclusionReason(excludedReason[e.ID]),
		})
	}
	sort.Slice(prop.Excluded, func(i, j int) bool {
		if prop.Excluded[i].EvidenceID == prop.Excluded[j].EvidenceID {
			return prop.Excluded[i].Reason < prop.Excluded[j].Reason
		}
		return prop.Excluded[i].EvidenceID < prop.Excluded[j].EvidenceID
	})

	prop.Changed = changedEvidenceIDs(prop, r)
	return prop, nil
}

// ProposalHash returns a deterministic content hash of a proposal, ignoring the
// Changed field.
func ProposalHash(p proposal.Proposal) string {
	cp := p
	cp.Changed = nil
	data, err := json.Marshal(cp)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:16])
}

// --- helpers ----------------------------------------------------------------

func hashID(prefix string, parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(len(p)))
		_, _ = h.Write(b[:])
		_, _ = h.Write([]byte(p))
	}
	return prefix + "_" + hex.EncodeToString(h.Sum(nil)[:16])
}

// newUUID returns a random RFC 4122 v4 UUID string (falling back to a
// time-derived value if the system RNG is unavailable).
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%016x", nowFunc().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func findCollectedEvidence(r *workflow.Record, id string) (evidence.Evidence, bool) {
	for _, e := range r.CollectedEvidence {
		if e.ID == id {
			return e, true
		}
	}
	return evidence.Evidence{}, false
}

func findConfirmed(r *workflow.Record, id string) *workflow.ConfirmedInterval {
	for i := range r.Confirmed {
		if r.Confirmed[i].ID == id {
			return &r.Confirmed[i]
		}
	}
	return nil
}

func upsertConfirmed(r *workflow.Record, ci workflow.ConfirmedInterval) {
	for i := range r.Confirmed {
		if r.Confirmed[i].ID == ci.ID {
			r.Confirmed[i] = ci
			clearEmpty(r)
			return
		}
	}
	r.Confirmed = append(r.Confirmed, ci)
	clearEmpty(r)
}

// clearEmpty drops a stale reviewed-empty flag: once a day has a confirmed
// block, it is no longer an empty day, so readiness must not short-circuit.
func clearEmpty(r *workflow.Record) {
	if r.Review != nil && r.Review.Empty {
		r.Review.Empty = false
	}
}

func removeConfirmed(r *workflow.Record, id string) {
	kept := r.Confirmed[:0]
	for _, ci := range r.Confirmed {
		if ci.ID != id {
			kept = append(kept, ci)
		}
	}
	r.Confirmed = kept
}

func addExclusion(r *workflow.Record, evID, reason string, now time.Time) {
	if r.Review == nil {
		r.Review = &workflow.ReviewState{}
	}
	ex := workflow.ExcludedEvidence{EvidenceID: evID, Reason: reason, ExcludedAt: now}
	for i := range r.Review.ExcludedEvidence {
		if r.Review.ExcludedEvidence[i].EvidenceID == evID {
			r.Review.ExcludedEvidence[i] = ex
			return
		}
	}
	r.Review.ExcludedEvidence = append(r.Review.ExcludedEvidence, ex)
}

func markAttendance(r *workflow.Record, evID string, attended bool, now time.Time) {
	if r.Review == nil {
		r.Review = &workflow.ReviewState{}
	}
	ad := workflow.AttendanceDecision{EvidenceID: evID, Attended: attended, DecidedAt: now}
	for i := range r.Review.Attendance {
		if r.Review.Attendance[i].EvidenceID == evID {
			r.Review.Attendance[i] = ad
			return
		}
	}
	r.Review.Attendance = append(r.Review.Attendance, ad)
}

func evidenceText(e evidence.Evidence) string {
	if d := strings.TrimSpace(e.Description); d != "" {
		return d
	}
	return strings.TrimSpace(e.Title)
}

func firstHint(hints []string) string {
	for _, h := range hints {
		if s := strings.TrimSpace(h); s != "" {
			return s
		}
	}
	return ""
}

func parseHHMM(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	h, err1 := strconv.Atoi(s[:2])
	m, err2 := strconv.Atoi(s[3:])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func onGridStr(s string) bool {
	m, ok := parseHHMM(s)
	if !ok {
		return false
	}
	return m%proposal.DefaultGridMinutes == 0
}

func removeString(list []string, s string) []string {
	var out []string
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

func unionStrings(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	var out []string
	for _, s := range append(append([]string{}, a...), b...) {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func joinTexts(a, b string) string {
	a, b = strings.TrimSpace(a), strings.TrimSpace(b)
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "\n" + b
	}
}

func toMinuteRange(start, end string) (int, int, bool) {
	sm, ok1 := parseHHMM(start)
	em, ok2 := parseHHMM(end)
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	if em <= sm {
		em += minutesPerDay
	}
	return sm, em, true
}

func fromMinutes(m int) string {
	m = ((m % minutesPerDay) + minutesPerDay) % minutesPerDay
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

func unionSpan(s1, e1, s2, e2 string) (string, string) {
	a1, b1, ok1 := toMinuteRange(s1, e1)
	a2, b2, ok2 := toMinuteRange(s2, e2)
	if !ok1 || !ok2 {
		return s1, e1
	}
	start := a1
	if a2 < start {
		start = a2
	}
	end := b1
	if b2 > end {
		end = b2
	}
	return fromMinutes(start), fromMinutes(end)
}

func changedEvidenceIDs(prop proposal.Proposal, r workflow.Record) []string {
	collected := make(map[string]bool, len(r.CollectedEvidence))
	for _, e := range r.CollectedEvidence {
		collected[e.ID] = true
	}
	covered := map[string]bool{}
	for _, c := range r.Confirmed {
		for _, id := range c.EvidenceIDs {
			covered[id] = true
		}
	}
	if r.Review != nil {
		for _, ex := range r.Review.ExcludedEvidence {
			covered[ex.EvidenceID] = true
		}
	}

	propIDs := map[string]bool{}
	for _, t := range prop.Topics {
		for _, id := range t.EvidenceIDs {
			propIDs[id] = true
		}
	}
	for _, b := range prop.Work {
		for _, id := range b.EvidenceIDs {
			propIDs[id] = true
		}
	}
	for _, b := range prop.Meetings {
		for _, id := range b.EvidenceIDs {
			propIDs[id] = true
		}
	}
	for _, b := range prop.Manual {
		for _, id := range b.EvidenceIDs {
			propIDs[id] = true
		}
	}

	changed := map[string]bool{}
	for id := range propIDs {
		if !covered[id] {
			changed[id] = true
		}
	}
	for id := range covered {
		if !collected[id] {
			changed[id] = true
		}
	}
	var out []string
	for id := range changed {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
