package workflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// MergeEvidence upserts evidence references by ID. It never touches Confirmed, so
// an evidence refresh preserves user-confirmed intervals and their user-authored
// Description/Project. References without an ID have no identity and are appended.
func MergeEvidence(r *Record, refs []EvidenceReference) {
	if r == nil {
		return
	}
	for _, ref := range refs {
		replaced := false
		if ref.ID != "" {
			for i := range r.Evidence {
				if r.Evidence[i].ID == ref.ID {
					r.Evidence[i] = ref
					replaced = true
					break
				}
			}
		}
		if !replaced {
			r.Evidence = append(r.Evidence, ref)
		}
	}
}

// MergeConfirmed upserts confirmed intervals by ID. A zero-valued incoming field
// never clobbers an existing non-zero one: Description, Project, Kind, Attendance,
// EvidenceIDs, Manual, OriginalStart/End, GridAdjusted, SplitGroupID, PartIndex,
// offsets, and the Start/End/Date times are preserved when the incoming value is
// empty/zero. ConfirmedAt is (re)set to now when incoming is zero. Evidence is
// never mutated.
func MergeConfirmed(r *Record, intervals []ConfirmedInterval) {
	if r == nil {
		return
	}
	now := time.Now().UTC()
	for _, incoming := range intervals {
		if incoming.ConfirmedAt.IsZero() {
			incoming.ConfirmedAt = now
		}

		index := -1
		if incoming.ID != "" {
			for i := range r.Confirmed {
				if r.Confirmed[i].ID == incoming.ID {
					index = i
					break
				}
			}
		}

		if index >= 0 {
			incoming = preserveConfirmedFields(incoming, r.Confirmed[index])
			r.Confirmed[index] = incoming
			continue
		}
		r.Confirmed = append(r.Confirmed, incoming)
	}
}

// preserveConfirmedFields keeps an existing non-zero field when the incoming
// value is its zero value, so a re-assert cannot wipe user decisions or S4
// review metadata.
func preserveConfirmedFields(incoming, existing ConfirmedInterval) ConfirmedInterval {
	if incoming.Date == "" {
		incoming.Date = existing.Date
	}
	if incoming.StartTime == "" {
		incoming.StartTime = existing.StartTime
	}
	if incoming.EndTime == "" {
		incoming.EndTime = existing.EndTime
	}
	if incoming.Description == "" {
		incoming.Description = existing.Description
	}
	if incoming.Project == "" {
		incoming.Project = existing.Project
	}
	if incoming.Kind == "" {
		incoming.Kind = existing.Kind
	}
	if !incoming.Attendance {
		incoming.Attendance = existing.Attendance
	}
	if len(incoming.EvidenceIDs) == 0 {
		incoming.EvidenceIDs = existing.EvidenceIDs
	}
	if !incoming.Manual {
		incoming.Manual = existing.Manual
	}
	if incoming.OriginalStart == "" {
		incoming.OriginalStart = existing.OriginalStart
	}
	if incoming.OriginalEnd == "" {
		incoming.OriginalEnd = existing.OriginalEnd
	}
	if !incoming.GridAdjusted {
		incoming.GridAdjusted = existing.GridAdjusted
	}
	if incoming.SplitGroupID == "" {
		incoming.SplitGroupID = existing.SplitGroupID
	}
	if incoming.PartIndex == 0 {
		incoming.PartIndex = existing.PartIndex
	}
	if incoming.StartOffsetMin == 0 {
		incoming.StartOffsetMin = existing.StartOffsetMin
	}
	if incoming.EndOffsetMin == nil {
		incoming.EndOffsetMin = existing.EndOffsetMin
	}
	if incoming.ClassificationID == "" {
		incoming.ClassificationID = existing.ClassificationID
	}
	return incoming
}

// MergeReviewState merges an incoming ReviewState into r.Reviewed flags are
// unioned, excluded-evidence and attendance rows are upserted by EvidenceID,
// acknowledged sources are unioned, and LastProposalHash/ReviewedAt take the
// latest non-zero value.
func MergeReviewState(r *Record, incoming ReviewState) {
	if r == nil {
		return
	}
	if r.Review == nil {
		cp := cloneReviewState(incoming)
		r.Review = &cp
		return
	}
	cur := r.Review
	if incoming.Reviewed {
		cur.Reviewed = true
	}
	if incoming.Empty {
		cur.Empty = true
	}
	if !incoming.ReviewedAt.IsZero() {
		cur.ReviewedAt = incoming.ReviewedAt
	}
	if incoming.LastProposalHash != "" {
		cur.LastProposalHash = incoming.LastProposalHash
	}
	for _, ex := range incoming.ExcludedEvidence {
		cur.ExcludedEvidence = upsertExcluded(cur.ExcludedEvidence, ex)
	}
	for _, ad := range incoming.Attendance {
		cur.Attendance = upsertAttendance(cur.Attendance, ad)
	}
	cur.AcknowledgedSources = unionStrings(cur.AcknowledgedSources, incoming.AcknowledgedSources)
}

func upsertExcluded(rows []ExcludedEvidence, incoming ExcludedEvidence) []ExcludedEvidence {
	for i := range rows {
		if rows[i].EvidenceID == incoming.EvidenceID {
			rows[i] = incoming
			return rows
		}
	}
	return append(rows, incoming)
}

func upsertAttendance(rows []AttendanceDecision, incoming AttendanceDecision) []AttendanceDecision {
	for i := range rows {
		if rows[i].EvidenceID == incoming.EvidenceID {
			rows[i] = incoming
			return rows
		}
	}
	return append(rows, incoming)
}

func unionStrings(a, b []string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	var out []string
	for _, s := range a {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, s := range b {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// cloneReviewState deep-copies a ReviewState so the returned value shares no
// slice backing arrays with the caller.
func cloneReviewState(in ReviewState) ReviewState {
	out := in
	out.ExcludedEvidence = append([]ExcludedEvidence(nil), in.ExcludedEvidence...)
	out.Attendance = append([]AttendanceDecision(nil), in.Attendance...)
	out.AcknowledgedSources = append([]string(nil), in.AcknowledgedSources...)
	return out
}

// operationKeySeparator joins the stable identity fields of an operation. The
// ASCII unit separator never appears in dates, action names, IDs, or HH:MM
// times, so it cannot be confused with field content.
const operationKeySeparator = "\x1f"

// DeriveOperationKey returns a deterministic operation identity from the stable
// fields of an operation: Date, Action, RemoteID, LocalID, StartTime, EndTime,
// joined by the unit separator. Description and Classification are deliberately
// excluded so editing text does not change identity. The planned RemoteID is the
// one known BEFORE the write (empty for a create), so the key is stable across
// the intent and the terminal outcome even when a create later returns a remote
// ID. This closes the F5 same-slot / empty-LocalID collision.
func DeriveOperationKey(o OperationOutcome) string {
	return strings.Join([]string{
		o.Date,
		o.Action,
		o.RemoteID,
		o.LocalID,
		o.StartTime,
		o.EndTime,
	}, operationKeySeparator)
}

// UpsertIntent records a durable pre-write operation intent as in_flight. It is a
// NO-OP when a terminal (non-in_flight) row already exists for the key, so a
// resolved operation is never resurrected into an unresolved state. When the key
// is empty it is derived via DeriveOperationKey.
func UpsertIntent(r *Record, intent OperationOutcome) {
	if r == nil {
		return
	}
	if intent.OperationKey == "" {
		intent.OperationKey = DeriveOperationKey(intent)
	}

	for i := range r.Outcomes {
		if r.Outcomes[i].OperationKey != intent.OperationKey {
			continue
		}
		if r.Outcomes[i].Status != OutcomeInFlight {
			// A terminal result already exists: never resurrect a resolved op.
			return
		}
		r.Outcomes[i] = intent
		return
	}
	r.Outcomes = append(r.Outcomes, intent)
}

// UnresolvedOperations returns the durable in_flight intent rows of a record.
// These are operations whose remote effect is unknown and whose replay must be
// blocked until reconciled. A nil record yields no results.
func UnresolvedOperations(r *Record) []OperationOutcome {
	if r == nil {
		return nil
	}
	var pending []OperationOutcome
	for _, o := range r.Outcomes {
		if o.Status == OutcomeInFlight {
			pending = append(pending, o)
		}
	}
	return pending
}

// UpsertOutcome records an operation outcome keyed by OperationKey. A confirmed
// success is never overwritten by a later failed/uncertain outcome. A terminal
// (non-in_flight) row is never downgraded back to an in_flight intent. When
// OperationKey is empty it is derived via DeriveOperationKey, exactly as
// UpsertIntent does, so an intent and its outcome always resolve to the same
// ledger row and cannot create a duplicate.
func UpsertOutcome(r *Record, outcome OperationOutcome) {
	if r == nil {
		return
	}

	if outcome.OperationKey == "" {
		outcome.OperationKey = DeriveOperationKey(outcome)
	}

	index := -1
	for i := range r.Outcomes {
		if r.Outcomes[i].OperationKey == outcome.OperationKey {
			index = i
			break
		}
	}

	if index >= 0 {
		existing := r.Outcomes[index].Status
		if existing == OutcomeConfirmed {
			return
		}
		// Never downgrade a terminal row back to an unresolved in_flight intent.
		if outcome.Status == OutcomeInFlight && existing != OutcomeInFlight {
			return
		}
		r.Outcomes[index] = outcome
		return
	}
	r.Outcomes = append(r.Outcomes, outcome)
}

// UpsertMapping binds a local identity to a stable remote ID, keyed by LocalID.
// A non-empty RemoteID and LocalID are required; identity is never fabricated.
// On update it refreshes RemoteID/LastObserved and bumps UpdatedAt, preserving
// CreatedAt.
func UpsertMapping(r *Record, mapping RemoteMapping) error {
	if strings.TrimSpace(mapping.RemoteID) == "" {
		return ErrMappingWithoutRemoteID
	}
	if strings.TrimSpace(mapping.LocalID) == "" {
		return ErrMappingWithoutLocalID
	}
	if r == nil {
		return nil
	}

	now := time.Now().UTC()
	for i := range r.RemoteMappings {
		if r.RemoteMappings[i].LocalID == mapping.LocalID {
			mapping.CreatedAt = r.RemoteMappings[i].CreatedAt
			mapping.UpdatedAt = now
			r.RemoteMappings[i] = mapping
			return nil
		}
	}

	if mapping.CreatedAt.IsZero() {
		mapping.CreatedAt = now
	}
	mapping.UpdatedAt = now
	r.RemoteMappings = append(r.RemoteMappings, mapping)
	return nil
}

// --- S6 plan/approval representation helpers --------------------------------

// PlanHash returns the content-addressed identity of a plan: sha256 over the
// canonical JSON of {Date, LocalFingerprint, RemoteObservedAt, RemoteComplete,
// Operations}, EXCLUDING PlanID, CreatedAt, and RecordRevision. Operations are
// ordered by a full-content key (see canonicalOperations), so the hash is a pure
// function of the operation multiset.
func PlanHash(plan SubmissionPlan) string {
	canonical := struct {
		Date             string          `json:"date"`
		LocalFingerprint string          `json:"local_fingerprint"`
		RemoteObservedAt time.Time       `json:"remote_observed_at"`
		RemoteComplete   bool            `json:"remote_complete"`
		Operations       []PlanOperation `json:"operations"`
	}{
		Date:             plan.Date,
		LocalFingerprint: plan.LocalFingerprint,
		RemoteObservedAt: plan.RemoteObservedAt.UTC(),
		RemoteComplete:   plan.RemoteComplete,
		Operations:       canonicalOperations(plan.Operations),
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// canonicalOperations returns a deterministically ordered copy of ops. It sorts
// with a STABLE sort over a full-content key, so even operations that share
// (LocalID, Kind, RemoteID) hash independently of their input order: the result
// is a pure function of the operation multiset.
func canonicalOperations(ops []PlanOperation) []PlanOperation {
	out := make([]PlanOperation, len(ops))
	copy(out, ops)
	sort.SliceStable(out, func(i, j int) bool {
		return canonicalOperationKey(out[i]) < canonicalOperationKey(out[j])
	})
	return out
}

// canonicalOperationKey is a deterministic, full-content encoding of one
// operation used as a stable sort key. Identical content yields an identical key,
// so interchangeable operations never affect the resulting order.
func canonicalOperationKey(op PlanOperation) string {
	data, err := json.Marshal(op)
	if err != nil {
		return ""
	}
	return string(data)
}

// LocalFingerprint returns a content hash over the plan-relevant local state:
// confirmed intervals (identity, times, project, description, attendance, kind,
// and ClassificationID), review decisions, and the mappings used (identity fields
// only; LastObserved and timestamps are excluded). Record.Revision is deliberately
// EXCLUDED so a revision bump alone does not invalidate an otherwise-identical
// plan.
//
// The classification resolver's output is also deliberately NOT hashed here: an
// approval binds the exact approved operation content (PlanHash covers each
// operation's resolved classification), so a resolver change yields a different
// plan on the next BuildPlan and therefore requires a fresh approval. It is not a
// ValidateApproval defect.
func LocalFingerprint(rec Record) string {
	type confirmed struct {
		ID               string `json:"id"`
		Date             string `json:"date"`
		StartTime        string `json:"start_time"`
		EndTime          string `json:"end_time"`
		Project          string `json:"project"`
		Description      string `json:"description"`
		Attendance       bool   `json:"attendance"`
		Kind             string `json:"kind"`
		ClassificationID string `json:"classification_id"`
	}
	type reviewExcluded struct {
		EvidenceID string `json:"evidence_id"`
		Reason     string `json:"reason"`
	}
	type reviewAttendance struct {
		EvidenceID string `json:"evidence_id"`
		Attended   bool   `json:"attended"`
	}
	type review struct {
		Reviewed     bool               `json:"reviewed"`
		Empty        bool               `json:"empty"`
		Excluded     []reviewExcluded   `json:"excluded"`
		Attendance   []reviewAttendance `json:"attendance"`
		Acknowledged []string           `json:"acknowledged"`
	}
	type mapping struct {
		LocalID          string `json:"local_id"`
		RemoteID         string `json:"remote_id"`
		Date             string `json:"date"`
		StartTime        string `json:"start_time"`
		EndTime          string `json:"end_time"`
		ClassificationID string `json:"classification_id"`
		Description      string `json:"description"`
	}

	confirmedRows := make([]confirmed, 0, len(rec.Confirmed))
	for _, c := range rec.Confirmed {
		confirmedRows = append(confirmedRows, confirmed{
			ID: c.ID, Date: c.Date, StartTime: c.StartTime, EndTime: c.EndTime,
			Project: c.Project, Description: c.Description, Attendance: c.Attendance,
			Kind: c.Kind, ClassificationID: c.ClassificationID,
		})
	}
	sort.Slice(confirmedRows, func(i, j int) bool { return confirmedRows[i].ID < confirmedRows[j].ID })

	var reviewRow review
	if rec.Review != nil {
		reviewRow = review{Reviewed: rec.Review.Reviewed, Empty: rec.Review.Empty}
		for _, ex := range rec.Review.ExcludedEvidence {
			reviewRow.Excluded = append(reviewRow.Excluded, reviewExcluded{EvidenceID: ex.EvidenceID, Reason: ex.Reason})
		}
		for _, ad := range rec.Review.Attendance {
			reviewRow.Attendance = append(reviewRow.Attendance, reviewAttendance{EvidenceID: ad.EvidenceID, Attended: ad.Attended})
		}
		reviewRow.Acknowledged = append([]string(nil), rec.Review.AcknowledgedSources...)
		sort.Slice(reviewRow.Excluded, func(i, j int) bool { return reviewRow.Excluded[i].EvidenceID < reviewRow.Excluded[j].EvidenceID })
		sort.Slice(reviewRow.Attendance, func(i, j int) bool { return reviewRow.Attendance[i].EvidenceID < reviewRow.Attendance[j].EvidenceID })
		sort.Strings(reviewRow.Acknowledged)
	}

	mappingRows := make([]mapping, 0, len(rec.RemoteMappings))
	for _, m := range rec.RemoteMappings {
		mappingRows = append(mappingRows, mapping{
			LocalID: m.LocalID, RemoteID: m.RemoteID, Date: m.Date,
			StartTime: m.StartTime, EndTime: m.EndTime,
			ClassificationID: m.ClassificationID, Description: m.Description,
		})
	}
	sort.Slice(mappingRows, func(i, j int) bool { return mappingRows[i].LocalID < mappingRows[j].LocalID })

	canonical := struct {
		Confirmed []confirmed `json:"confirmed"`
		Review    review      `json:"review"`
		Mappings  []mapping   `json:"mappings"`
	}{Confirmed: confirmedRows, Review: reviewRow, Mappings: mappingRows}

	data, err := json.Marshal(canonical)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// UpsertPlan inserts or replaces a plan by PlanID.
func UpsertPlan(r *Record, plan SubmissionPlan) {
	if r == nil {
		return
	}
	for i := range r.Plans {
		if r.Plans[i].PlanID == plan.PlanID {
			r.Plans[i] = plan
			return
		}
	}
	r.Plans = append(r.Plans, plan)
}

// FindPlan returns the plan with the given PlanID.
func FindPlan(r *Record, planID string) (SubmissionPlan, bool) {
	if r == nil {
		return SubmissionPlan{}, false
	}
	for _, p := range r.Plans {
		if p.PlanID == planID {
			return p, true
		}
	}
	return SubmissionPlan{}, false
}

// UpsertApproval inserts or replaces an approval by ApprovalID.
func UpsertApproval(r *Record, a Approval) {
	if r == nil {
		return
	}
	for i := range r.Approvals {
		if r.Approvals[i].ApprovalID == a.ApprovalID {
			r.Approvals[i] = a
			return
		}
	}
	r.Approvals = append(r.Approvals, a)
}

// InvalidateApproval marks an approval invalidated with a stable reason and time.
// It never overwrites an existing invalidation.
func InvalidateApproval(r *Record, approvalID, reason string, now time.Time) {
	if r == nil {
		return
	}
	for i := range r.Approvals {
		if r.Approvals[i].ApprovalID != approvalID {
			continue
		}
		if r.Approvals[i].InvalidatedAt == nil {
			t := now
			r.Approvals[i].InvalidatedAt = &t
			r.Approvals[i].InvalidatedReason = reason
		}
		return
	}
}

// SupersedeApproval marks every other approval for the date superseded, keeping
// keepApprovalID active.
func SupersedeApproval(r *Record, keepApprovalID, date string) {
	if r == nil {
		return
	}
	for i := range r.Approvals {
		if r.Approvals[i].Date == date && r.Approvals[i].ApprovalID != keepApprovalID {
			r.Approvals[i].Superseded = true
		}
	}
}

// ActiveApproval returns the current non-superseded, non-invalidated approval for
// the date (the latest ApprovedAt when several qualify).
func ActiveApproval(r *Record, date string) (Approval, bool) {
	if r == nil {
		return Approval{}, false
	}
	var best Approval
	found := false
	for _, a := range r.Approvals {
		if a.Date != date || a.Superseded || a.InvalidatedAt != nil {
			continue
		}
		if !found || a.ApprovedAt.After(best.ApprovedAt) {
			best = a
			found = true
		}
	}
	return best, found
}

// --- S7 editorial representation helpers ------------------------------------

// UpsertEditorialSuggestion inserts or replaces an editorial suggestion keyed by
// TargetID. Editorial suggestions are TEXT ONLY: they never feed LocalFingerprint
// or PlanHash.
func UpsertEditorialSuggestion(r *Record, s EditorialSuggestion) {
	if r == nil {
		return
	}
	for i := range r.Editorial {
		if r.Editorial[i].TargetID == s.TargetID {
			r.Editorial[i] = s
			return
		}
	}
	r.Editorial = append(r.Editorial, s)
}

// FindEditorialSuggestion returns the editorial suggestion for a target id.
func FindEditorialSuggestion(r *Record, targetID string) (EditorialSuggestion, bool) {
	if r == nil {
		return EditorialSuggestion{}, false
	}
	for _, s := range r.Editorial {
		if s.TargetID == targetID {
			return s, true
		}
	}
	return EditorialSuggestion{}, false
}

// SetUsageReport replaces the daily usage report. Editorial and orchestration
// usage stay separate; the report never feeds LocalFingerprint or PlanHash.
func SetUsageReport(r *Record, report EditorialUsage) {
	if r == nil {
		return
	}
	reportCopy := report
	r.Usage = &reportCopy
}
