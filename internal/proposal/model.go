// Package proposal implements the pure, deterministic S4 proposal model: it
// turns collected evidence into topic candidates, activity groups, and proposed
// time blocks. It confirms nothing. Duration stays unknown for git-only groups,
// meetings carry a calendar-known duration with attendance unresolved, and
// manual intervals are proposed (not confirmed).
//
// It imports only internal/evidence plus the standard library, and takes an
// injectable clock via ProposalOptions.Now.
package proposal

import (
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
)

// Default policy values. The four-hour maximum and the 45-minute adjacency gap
// are LOCAL, UNVERIFIED policy per the owner, not verified remote behavior.
const (
	DefaultAdjacencyGap      = 45 * time.Minute
	DefaultMaxEntryMinutes   = 240
	DefaultGridMinutes       = 15
	defaultDisplayTimeLayout = "15:04"
	defaultDateLayout        = "2006-01-02"
)

// BlockKind classifies a proposed time block.
type BlockKind string

const (
	BlockWork    BlockKind = "work"
	BlockMeeting BlockKind = "meeting"
	BlockJoint   BlockKind = "joint"
	BlockManual  BlockKind = "manual"
)

// AttendanceState is a block's attendance. It is unresolved until the user
// confirms it; an accepted invitation only preselects attendance.
type AttendanceState string

const (
	AttendanceUnresolved  AttendanceState = "unresolved"
	AttendanceAttended    AttendanceState = "attended"
	AttendanceNotAttended AttendanceState = "not_attended"
)

// ExclusionReason explains why an evidence item is not proposal input.
type ExclusionReason string

const (
	ExcludeCancelled         ExclusionReason = "cancelled"
	ExcludeDeclined          ExclusionReason = "declined"
	ExcludeUnknownInvitation ExclusionReason = "unknown_invitation"
	// ExcludeAgentContextOnly marks agent-session evidence: it is visible context
	// only and never forms/extends a span or interrupts work.
	ExcludeAgentContextOnly ExclusionReason = "agent_context_only"
	// ExcludeUnsupportedKind marks evidence of a kind the proposal does not model.
	ExcludeUnsupportedKind ExclusionReason = "unsupported_kind"
	// ExcludeMissingRepository marks git evidence with no repository identity, so
	// exported Build can never silently merge two repo-less items on one branch.
	ExcludeMissingRepository ExclusionReason = "missing_repository"
)

// ExcludedEvidence records evidence retained for visibility but not used as
// proposal input, with the reason.
type ExcludedEvidence struct {
	EvidenceID string              `json:"evidence_id"`
	Kind       evidence.SourceKind `json:"kind"`
	Reason     ExclusionReason     `json:"reason"`
}

// TopicCandidate is a reviewable grouping of git evidence: repository plus PR
// identity when a PR is known, otherwise repository plus branch. The repository
// is always part of the identity, so identical branch names in different
// repositories never merge.
type TopicCandidate struct {
	ID          string          `json:"id"`
	Kind        BlockKind       `json:"kind"`
	Repository  string          `json:"repository"`
	PRNumber    int             `json:"pr_number,omitempty"`
	Branch      string          `json:"branch,omitempty"`
	EvidenceIDs []string        `json:"evidence_ids"`
	Groups      []ActivityGroup `json:"groups"`
}

// ActivityGroup is an adjacency-bounded cluster of evidence points within a
// topic. Duration is ALWAYS unknown for git-only groups: commits are activity
// points, not continuous working intervals.
type ActivityGroup struct {
	ID            string            `json:"id"`
	TopicID       string            `json:"topic_id"`
	EvidenceIDs   []string          `json:"evidence_ids"`
	SpanStart     time.Time         `json:"span_start"`
	SpanEnd       time.Time         `json:"span_end"`
	Duration      evidence.Duration `json:"duration"`
	InterruptedBy []string          `json:"interrupted_by,omitempty"`
}

// Project resolution sources.
const (
	ProjectSourceRepoMap    = "repo_map"
	ProjectSourceHint       = "hint"
	ProjectSourceUnresolved = "unresolved"
)

// ProjectResolution is a suggested project and its provenance. Unresolved or
// ambiguous work requires user resolution.
type ProjectResolution struct {
	Project  string `json:"project,omitempty"`
	Source   string `json:"source"`
	Resolved bool   `json:"resolved"`
}

// ProposedTimeBlock is a proposed (never confirmed) block for review.
type ProposedTimeBlock struct {
	ID            string            `json:"id"`
	Kind          BlockKind         `json:"kind"`
	Date          string            `json:"date"`
	Start         string            `json:"start"`
	End           string            `json:"end"`
	StartAt       time.Time         `json:"start_at"`
	EndAt         time.Time         `json:"end_at"`
	Duration      evidence.Duration `json:"duration"`
	Project       ProjectResolution `json:"project"`
	Description   string            `json:"description,omitempty"`
	EvidenceIDs   []string          `json:"evidence_ids"`
	Attendance    AttendanceState   `json:"attendance"`
	Preselected   bool              `json:"preselected"`
	GridSuggested bool              `json:"grid_suggested"`
	Collision     bool              `json:"collision"`
}

// Proposal is the deterministic, unconfirmed draft for one local day.
type Proposal struct {
	Date   string           `json:"date"`
	Topics []TopicCandidate `json:"topics"`
	// Work holds materialized git work blocks. A git group interrupted by a
	// meeting is materialized as a joint block (Kind=joint) combining the meeting
	// and the commits; its duration is the meeting's known duration and it counts
	// once.
	Work     []ProposedTimeBlock `json:"work,omitempty"`
	Meetings []ProposedTimeBlock `json:"meetings"`
	Manual   []ProposedTimeBlock `json:"manual"`
	Excluded []ExcludedEvidence  `json:"excluded"`
	// Changed holds evidence IDs that differ from a prior review; it is populated
	// by the refresh layer (S4-IMPL-2), not by a fresh Build.
	Changed  []string `json:"changed,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// ProposalOptions configures Build. Location and Date define the local day;
// Now is the injectable clock.
type ProposalOptions struct {
	Location        *time.Location    `json:"-"`
	Date            time.Time         `json:"-"`
	AdjacencyGap    time.Duration     `json:"-"`
	MaxEntryMinutes int               `json:"-"`
	ProjectMap      map[string]string `json:"project_map,omitempty"`
	Now             time.Time         `json:"-"`
}
