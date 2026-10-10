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

// UpsertOutcome records an operation outcome keyed by OperationKey. A confirmed
// success is never overwritten by a later failed/uncertain outcome. When
// OperationKey is empty it is derived from LocalID + ":" + Action, falling back
// to Date+StartTime+EndTime+Action when both are empty.
func UpsertOutcome(r *Record, outcome OperationOutcome) {
	if r == nil {
		return
	}

	if outcome.OperationKey == "" {
		if outcome.LocalID != "" {
			outcome.OperationKey = outcome.LocalID + ":" + outcome.Action
		} else {
			outcome.OperationKey = outcome.Date + outcome.StartTime + outcome.EndTime + outcome.Action
		}
	}

	index := -1
	for i := range r.Outcomes {
		if r.Outcomes[i].OperationKey == outcome.OperationKey {
			index = i
			break
		}
	}

	if index >= 0 {
		if r.Outcomes[index].Status == OutcomeConfirmed {
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
