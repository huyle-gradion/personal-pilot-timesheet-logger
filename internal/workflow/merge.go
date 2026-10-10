package workflow

import (
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

// MergeConfirmed upserts confirmed intervals by ID. When an incoming Description
// or Project is empty and an existing entry is present, the existing value is
// preserved. ConfirmedAt is (re)set to now when incoming is zero. Evidence is
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
			existing := r.Confirmed[index]
			if incoming.Description == "" {
				incoming.Description = existing.Description
			}
			if incoming.Project == "" {
				incoming.Project = existing.Project
			}
			r.Confirmed[index] = incoming
			continue
		}
		r.Confirmed = append(r.Confirmed, incoming)
	}
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
