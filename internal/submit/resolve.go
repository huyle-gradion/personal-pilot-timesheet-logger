package submit

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/gradion"
	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/workflow"
)

// CodeResolvedNotApplied is the stable outcome code recorded when an operation is
// resolved as never having been applied remotely.
const CodeResolvedNotApplied = "SUBMIT_RESOLVED_NOT_APPLIED"

// ResolutionKind is the explicit operator decision for one unresolved operation.
type ResolutionKind string

const (
	// ResolutionAdopt confirms the operation and adopts its remote id, populating
	// the mapping from that identity.
	ResolutionAdopt ResolutionKind = "adopt"
	// ResolutionNotApplied marks the operation failed: the write never applied.
	ResolutionNotApplied ResolutionKind = "not_applied"
)

// Resolution is an explicit decision resolving one unresolved operation, keyed by
// its stable OperationKey. RemoteID/LocalID override the operation's own fields
// when supplied (RemoteID is required for adopt).
//
// The JSON tags are ADDITIVE (S8-FIX-2): the CLI/skill document a snake_case
// resolve.json, and without them an underscored key would silently decode to an
// empty field and drop the decision. They are the single source of truth for the
// decision shape.
type Resolution struct {
	OperationKey string         `json:"operation_key"`
	Kind         ResolutionKind `json:"kind"`
	RemoteID     string         `json:"remote_id,omitempty"`
	LocalID      string         `json:"local_id,omitempty"`
}

// RestartGate reports whether a record must NOT be replayed because it has
// unresolved remote operations: durable in_flight intents, or uncertain /
// unidentified outcomes that are not yet resolved. Reasons are stable, sorted
// strings.
func RestartGate(rec workflow.Record) (blocked bool, reasons []string) {
	seen := make(map[string]bool)
	var out []string
	add := func(r string) {
		if r == "" || seen[r] {
			return
		}
		seen[r] = true
		out = append(out, r)
	}
	for _, o := range workflow.UnresolvedOperations(&rec) {
		add("in_flight:" + o.OperationKey)
	}
	for _, o := range rec.Outcomes {
		if IsUnresolved(o) {
			add(string(o.Status) + ":" + o.OperationKey)
		}
	}
	if len(out) == 0 {
		return false, nil
	}
	sort.Strings(out)
	return true, out
}

// IsUnresolved reports whether an outcome row still needs reconciliation, and is
// the SINGLE definition of "an outcome still needing reconciliation" (the restart
// gate, the resolver, and the reset guard all defer to it). An in_flight intent is
// a durable write whose success was never established; uncertain and unidentified
// are the same condition recorded after a probe. A resolved row never needs
// reconciliation.
func IsUnresolved(o workflow.OperationOutcome) bool {
	if o.Resolved {
		return false
	}
	switch o.Status {
	case workflow.OutcomeInFlight, workflow.OutcomeUncertain, workflow.OutcomeUnidentified:
		return true
	}
	return false
}

// ResolveUnresolved reconciles unresolved operations from EXPLICIT decisions and,
// for an unresolved EDIT only, from a safe observation inference. It never
// auto-resolves a create from a bare absence (owner decision 1) and never resends.
// It mutates rec; the caller persists it.
func ResolveUnresolved(rec *workflow.Record, obs gradion.Listing, decisions []Resolution, now time.Time) error {
	if rec == nil {
		return nil
	}

	observedByID := make(map[string]workflow.RemoteState, len(obs.Entries))
	for _, e := range obs.Entries {
		observedByID[e.RemoteID] = e
	}
	byKey := make(map[string]Resolution, len(decisions))
	for _, d := range decisions {
		byKey[d.OperationKey] = d
	}

	rows := append([]workflow.OperationOutcome(nil), rec.Outcomes...)
	for _, o := range rows {
		if !IsUnresolved(o) {
			continue
		}
		if d, ok := byKey[o.OperationKey]; ok {
			switch d.Kind {
			case ResolutionAdopt:
				if err := adoptUnresolved(rec, o, d, observedByID, now); err != nil {
					return err
				}
			case ResolutionNotApplied:
				markNotApplied(rec, o, now)
			default:
				return fmt.Errorf("submit: unknown resolution kind %q for %s", d.Kind, o.OperationKey)
			}
			continue
		}
		if o.Action == string(workflow.PlanOpEdit) {
			resolveEditFromObservation(rec, o, observedByID, now)
		}
		// A create with no explicit decision stays unresolved: a bare absence is
		// not proof that the create failed.
	}
	return nil
}

// adoptUnresolved confirms an unresolved operation and adopts its remote id,
// reusing the layer-1 adoption mechanism to create or refresh the mapping.
func adoptUnresolved(rec *workflow.Record, o workflow.OperationOutcome, d Resolution, observedByID map[string]workflow.RemoteState, now time.Time) error {
	remoteID := strings.TrimSpace(d.RemoteID)
	if remoteID == "" {
		remoteID = strings.TrimSpace(o.RemoteID)
	}
	if remoteID == "" {
		return fmt.Errorf("submit: adopt requires a remote id for operation %s", o.OperationKey)
	}
	localID := strings.TrimSpace(d.LocalID)
	if localID == "" {
		localID = strings.TrimSpace(o.LocalID)
	}

	resolved := o
	resolved.Status = workflow.OutcomeConfirmed
	resolved.Resolved = true
	resolved.RemoteID = remoteID
	resolved.RecordedAt = now
	workflow.UpsertOutcome(rec, resolved)

	observed, ok := observedByID[remoteID]
	if !ok {
		// The identity is known but the content is not observed, so no mapping can
		// be built. The outcome is still confirmed.
		return nil
	}
	res := ReconcileResult{
		ObservedAt: now,
		Remotes: []RemoteDecision{{
			RemoteID:       remoteID,
			Adopted:        true,
			AdoptedLocalID: localID,
			Observed:       observed,
		}},
	}
	return ApplyReconcile(rec, res, now)
}

// markNotApplied resolves an operation as failed (never applied remotely).
func markNotApplied(rec *workflow.Record, o workflow.OperationOutcome, now time.Time) {
	resolved := o
	resolved.Status = workflow.OutcomeFailed
	resolved.Resolved = true
	resolved.ErrorCode = CodeResolvedNotApplied
	resolved.RecordedAt = now
	workflow.UpsertOutcome(rec, resolved)
}

// resolveEditFromObservation safely resolves an unresolved EDIT from the current
// remote content: post-edit content proves the edit applied (confirmed); pre-edit
// content proves it did not (failed). Absent or ambiguous content stays
// unresolved. It never resends.
func resolveEditFromObservation(rec *workflow.Record, o workflow.OperationOutcome, observedByID map[string]workflow.RemoteState, now time.Time) {
	observed, present := observedByID[strings.TrimSpace(o.RemoteID)]
	if !present {
		return // absent: leave unresolved
	}
	prior, ok := mappingByLocalID(rec, o.LocalID)
	if !ok {
		return // no pre-edit baseline: leave unresolved
	}
	if !prior.LastObserved.ContentObserved {
		// A legacy/plan-echo LastObserved is not verified content: never compare
		// against it. Leave the operation unresolved.
		return
	}

	postEdit := prior.LastObserved
	postEdit.Date = o.Date
	postEdit.StartTime = o.StartTime
	postEdit.EndTime = o.EndTime
	postEdit.Description = o.Description

	switch {
	case RemoteContentEqual(observed, postEdit):
		resolved := o
		resolved.Status = workflow.OutcomeConfirmed
		resolved.Resolved = true
		resolved.RemoteID = o.RemoteID
		resolved.RecordedAt = now
		workflow.UpsertOutcome(rec, resolved)
		refreshMappingLastObserved(rec, o.LocalID, observed, now)
	case RemoteContentEqual(observed, prior.LastObserved):
		markNotApplied(rec, o, now)
	default:
		// Ambiguous content: leave unresolved.
	}
}

func mappingByLocalID(rec *workflow.Record, localID string) (workflow.RemoteMapping, bool) {
	for _, m := range rec.RemoteMappings {
		if m.LocalID == localID {
			return m, true
		}
	}
	return workflow.RemoteMapping{}, false
}

func refreshMappingLastObserved(rec *workflow.Record, localID string, observed workflow.RemoteState, now time.Time) {
	observed.ContentObserved = true
	for i := range rec.RemoteMappings {
		if rec.RemoteMappings[i].LocalID == localID {
			rec.RemoteMappings[i].LastObserved = observed
			rec.RemoteMappings[i].UpdatedAt = now
			return
		}
	}
}
