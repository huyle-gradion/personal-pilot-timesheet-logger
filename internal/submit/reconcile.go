// Package submit implements the S6 submission layer: planning, reconciliation,
// approval, and execution.
//
// Planning, reconciliation, and approval (plan.go, reconcile.go, approval.go) are
// PURE: they operate on an already-fetched observation and perform no transport.
// Execution (execute.go, observer.go, resolve.go) performs the approved writes
// through injected RemoteObserver/RemoteWriter interfaces, takes the exclusive day
// lock, re-validates the approval against a fresh observation, writes a durable
// in_flight intent before every single dispatch, and never retries a write. Remote
// entries are editable but never removable; slot equality NEVER establishes a
// match; mappings are created only from an explicit adoption or a confirmed write.
package submit

import (
	"sort"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// MatchKind classifies how a local confirmed interval relates to remote state.
type MatchKind string

const (
	// MatchMapped means a stable mapping binds the local interval to a remote id.
	MatchMapped MatchKind = "mapped"
	// MatchCandidate means NO mapping exists; a remote slot merely OVERLAPS. A
	// candidate is a review suggestion and NEVER establishes a match.
	MatchCandidate MatchKind = "candidate"
	// MatchConflict means a mapping exists but the observed remote is missing or
	// its content differs from the mapping's last observation; it needs
	// reconciliation.
	MatchConflict MatchKind = "conflict"
)

// LocalDecision describes one confirmed interval's reconciliation state. Kind is
// empty when the interval has no mapping and no overlapping remote.
type LocalDecision struct {
	LocalID  string    `json:"local_id"`
	RemoteID string    `json:"remote_id,omitempty"`
	Kind     MatchKind `json:"kind,omitempty"`
	Overlaps []string  `json:"overlaps,omitempty"`
}

// RemoteDecision describes one observed remote entry's reconciliation state.
// Orphaned means a mapping references this remote but the local interval is gone.
// AdoptedLocalID is the local id from an explicit adoption (empty for a foreign
// adoption).
type RemoteDecision struct {
	RemoteID       string               `json:"remote_id"`
	LocalID        string               `json:"local_id,omitempty"`
	Mapped         bool                 `json:"mapped"`
	Adopted        bool                 `json:"adopted"`
	AdoptedLocalID string               `json:"adopted_local_id,omitempty"`
	Orphaned       bool                 `json:"orphaned"`
	Observed       workflow.RemoteState `json:"observed"`
}

// Adoption is an explicit user decision linking a remote entry to a local
// interval. LocalID empty means the remote is foreign (no local counterpart); a
// foreign adoption is acknowledged but creates no local mapping.
type Adoption struct {
	RemoteID  string    `json:"remote_id"`
	LocalID   string    `json:"local_id,omitempty"`
	AdoptedAt time.Time `json:"adopted_at"`
}

// ReconcileResult is the pure reconciliation of a record against an observation.
type ReconcileResult struct {
	Complete   bool             `json:"complete"`
	ObservedAt time.Time        `json:"observed_at"`
	Locals     []LocalDecision  `json:"locals"`
	Remotes    []RemoteDecision `json:"remotes"`
	Unresolved []string         `json:"unresolved,omitempty"`
}

// Reconcile compares a record to a remote observation. Slot overlap ONLY blocks
// (it never establishes a match); mappings are keyed by stable IDs. It surfaces
// unresolved conditions (conflicts, missing mapped remotes, orphaned mappings,
// and adoptions that reference an unobserved remote).
func Reconcile(rec workflow.Record, obs gradion.Listing, adoptions []Adoption, now time.Time) ReconcileResult {
	// An UNOBSERVED listing (offline: no remote was contacted) has NO observation
	// time: never fabricate a local "now" for it, exactly as submit.observedAt does
	// not. The now fallback applies ONLY to an OBSERVED listing that carries no
	// timestamp.
	observedAt := obs.ObservedAt
	if obs.Observed && observedAt.IsZero() {
		observedAt = now
	}

	observedByID := make(map[string]workflow.RemoteState, len(obs.Entries))
	for _, e := range obs.Entries {
		observedByID[e.RemoteID] = e
	}

	byLocal, persistedByLocal, _ := effectiveMappings(rec, adoptions)

	mappedLocalByRemote := make(map[string]string, len(byLocal))
	for localID, remoteID := range byLocal {
		mappedLocalByRemote[remoteID] = localID
	}

	confirmedByID := make(map[string]bool, len(rec.Confirmed))
	for _, ci := range rec.Confirmed {
		confirmedByID[ci.ID] = true
	}

	adoptedByRemote := make(map[string]Adoption, len(adoptions))
	for _, ad := range adoptions {
		adoptedByRemote[ad.RemoteID] = ad
	}

	var unresolved []string

	intervals := append([]workflow.ConfirmedInterval(nil), rec.Confirmed...)
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].ID < intervals[j].ID })

	locals := make([]LocalDecision, 0, len(intervals))
	for _, ci := range intervals {
		local := LocalDecision{LocalID: ci.ID}
		if remoteID, mapped := byLocal[ci.ID]; mapped {
			local.RemoteID = remoteID
			observed, present := observedByID[remoteID]
			prior, hasPersisted := persistedByLocal[ci.ID]
			isPersisted := hasPersisted && prior.RemoteID == remoteID
			switch {
			case !present:
				local.Kind = MatchConflict
				if isPersisted {
					unresolved = append(unresolved, "mapped_remote_missing:"+ci.ID)
				}
			case isPersisted && prior.LastObserved.ContentObserved && !RemoteContentEqual(prior.LastObserved, observed):
				// A conflict is asserted ONLY when the persisted baseline was
				// itself observation-derived AND its content differs from the
				// fresh observation. An UNOBSERVED (legacy) baseline is treated as
				// "content unknown", never as a conflict: the baseline's trust is
				// not silently upgraded and the record is not rewritten, but a
				// conflict that cannot be proven is not asserted.
				local.Kind = MatchConflict
				unresolved = append(unresolved, "remote_conflict:"+ci.ID)
			default:
				local.Kind = MatchMapped
			}
		} else {
			for _, e := range obs.Entries {
				if slotsOverlap(ci.Date, ci.StartTime, ci.EndTime, e.Date, e.StartTime, e.EndTime) {
					local.Overlaps = append(local.Overlaps, e.RemoteID)
				}
			}
			sort.Strings(local.Overlaps)
			if len(local.Overlaps) > 0 {
				local.Kind = MatchCandidate
			}
		}
		locals = append(locals, local)
	}

	entries := append([]workflow.RemoteState(nil), obs.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].RemoteID < entries[j].RemoteID })

	remotes := make([]RemoteDecision, 0, len(entries))
	for _, e := range entries {
		rd := RemoteDecision{RemoteID: e.RemoteID, Observed: e}
		if localID, ok := mappedLocalByRemote[e.RemoteID]; ok {
			rd.Mapped = true
			rd.LocalID = localID
			if !confirmedByID[localID] {
				rd.Orphaned = true
			}
		}
		if _, ok := adoptedByRemote[e.RemoteID]; ok {
			rd.Adopted = true
			rd.AdoptedLocalID = adoptedByRemote[e.RemoteID].LocalID
		}
		remotes = append(remotes, rd)
	}

	// Orphaned mappings: a mapping whose local interval is no longer present.
	for _, m := range rec.RemoteMappings {
		if !confirmedByID[m.LocalID] {
			unresolved = append(unresolved, "orphaned_mapping:"+m.LocalID)
		}
	}

	// Adoptions that reference a remote that is not observed.
	for _, ad := range adoptions {
		if _, ok := observedByID[ad.RemoteID]; !ok {
			unresolved = append(unresolved, "adoption_missing_remote:"+ad.RemoteID)
		}
	}

	return ReconcileResult{
		Complete:   obs.Complete,
		ObservedAt: observedAt,
		Locals:     locals,
		Remotes:    remotes,
		Unresolved: dedupSorted(unresolved),
	}
}

// effectiveMappings folds persisted mappings and explicit adoptions into one
// effective view for planning and reconciliation.
//
//   - byLocal: localID -> effective remoteID. A persisted mapping wins over an
//     adoption for the same local, so an established identity is never silently
//     replaced during planning. An adoption with a non-empty LocalID acts as a
//     mapping even before it is persisted, so planning emits an edit/no-op rather
//     than a duplicate create (owner decision 2 / ADR-0005).
//   - persistedByLocal: the persisted mapping for a local, used for the
//     LastObserved conflict check.
//   - claimedRemoteIDs: every remote claimed by a persisted mapping or by an
//     adoption. A foreign adoption (empty LocalID) claims its remote but maps no
//     local interval, so it never suppresses an unrelated create.
func effectiveMappings(rec workflow.Record, adoptions []Adoption) (byLocal map[string]string, persistedByLocal map[string]workflow.RemoteMapping, claimedRemoteIDs map[string]bool) {
	byLocal = make(map[string]string, len(rec.RemoteMappings)+len(adoptions))
	persistedByLocal = make(map[string]workflow.RemoteMapping, len(rec.RemoteMappings))
	claimedRemoteIDs = make(map[string]bool, len(rec.RemoteMappings)+len(adoptions))

	for _, m := range rec.RemoteMappings {
		persistedByLocal[m.LocalID] = m
		if _, exists := byLocal[m.LocalID]; !exists {
			byLocal[m.LocalID] = m.RemoteID
		}
		claimedRemoteIDs[m.RemoteID] = true
	}
	for _, ad := range adoptions {
		remoteID := strings.TrimSpace(ad.RemoteID)
		if remoteID == "" {
			continue
		}
		claimedRemoteIDs[remoteID] = true
		localID := strings.TrimSpace(ad.LocalID)
		if localID == "" {
			// Foreign remote: claimed, but no local interval is mapped.
			continue
		}
		if _, exists := byLocal[localID]; !exists {
			byLocal[localID] = remoteID
		}
	}
	return byLocal, persistedByLocal, claimedRemoteIDs
}

// ApplyReconcile persists reconciliation state into a record. It populates or
// updates S1 mappings ONLY from explicit adoptions (never from a bare read or
// skip), refreshes LastObserved for re-observed mapped entries, and never
// deletes. Foreign adoptions (empty LocalID) are acknowledged but create no local
// mapping. It returns an error only for a programmer/identity failure.
func ApplyReconcile(rec *workflow.Record, res ReconcileResult, now time.Time) error {
	if rec == nil {
		return nil
	}

	observedByID := make(map[string]workflow.RemoteState, len(res.Remotes))
	for _, rd := range res.Remotes {
		observedByID[rd.RemoteID] = rd.Observed
	}

	// Refresh LastObserved for re-observed mapped entries (identity unchanged).
	// Binding fields are deliberately not refreshed here; LastObserved is the
	// single carrier of observed truth.
	for i := range rec.RemoteMappings {
		if observed, ok := observedByID[rec.RemoteMappings[i].RemoteID]; ok {
			rec.RemoteMappings[i].LastObserved = observed
			rec.RemoteMappings[i].UpdatedAt = now
		}
	}

	// Populate mappings from explicit adoptions with a local interval.
	confirmedByID := make(map[string]workflow.ConfirmedInterval, len(rec.Confirmed))
	for _, ci := range rec.Confirmed {
		confirmedByID[ci.ID] = ci
	}
	for _, ad := range res.adoptions() {
		localID := strings.TrimSpace(ad.LocalID)
		if localID == "" {
			// Foreign remote: acknowledged, not linked to a local interval.
			continue
		}
		ci, ok := confirmedByID[localID]
		if !ok {
			continue
		}
		observed, ok := observedByID[ad.RemoteID]
		if !ok {
			continue
		}
		classificationID := ci.ClassificationID
		if strings.TrimSpace(classificationID) == "" {
			classificationID = observed.ClassificationID
		}
		mapping, err := gradion.BuildMapping(localID, gradion.PlannedEntry{
			RemoteID:         observed.RemoteID,
			Date:             ci.Date,
			StartTime:        ci.StartTime,
			EndTime:          ci.EndTime,
			ClassificationID: classificationID,
			Description:      ci.Description,
		}, observed, now)
		if err != nil {
			return err
		}
		if err := workflow.UpsertMapping(rec, mapping); err != nil {
			return err
		}
	}

	return nil
}

// adoptions recovers the explicit adoptions carried in a result via its remote
// decisions. It is a small helper so ApplyReconcile can act without a second
// adoptions argument.
func (res ReconcileResult) adoptions() []Adoption {
	var out []Adoption
	for _, rd := range res.Remotes {
		if rd.Adopted {
			out = append(out, Adoption{RemoteID: rd.RemoteID, LocalID: rd.AdoptedLocalID})
		}
	}
	return out
}
