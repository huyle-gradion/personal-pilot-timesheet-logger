// Package evidence defines the S3 evidence contracts shared by the deterministic
// workflow: source identity, registry, config, and per-kind stable identity.
//
// Design boundaries (docs/timesheet-redesign.md):
//   - Evidence carries provenance only. Description holds source-derived text;
//     user-authored text belongs to S4's ConfirmedInterval.
//   - Cancelled events and declined/unknown invitations are RETAINED as evidence
//     with their status. Exclusion happens later at proposal time (S4), never
//     here.
//   - Evidence never automatically becomes confirmed working time.
//
// The package deliberately has no dependency on internal/workflow; the import
// direction is workflow -> evidence.
package evidence

import "time"

// SourceKind identifies where an evidence item came from. These are the S3
// source kinds: calendar, local git, remote PR activity, manual input, and
// optional agent sessions.
type SourceKind string

const (
	SourceCalendar     SourceKind = "calendar"
	SourceLocalGit     SourceKind = "local_git"
	SourceRemotePR     SourceKind = "remote_pr"
	SourceManual       SourceKind = "manual"
	SourceAgentSession SourceKind = "agent_session"
)

// InvitationStatus is a tri-state (plus "none") invitation response. "unknown"
// is a REAL value and is never defaulted to accepted: iCalendar PARTSTAT
// indicates an invitation response, not actual attendance.
type InvitationStatus string

const (
	// InvitationUnknown is an unreadable/absent response. It is never treated as
	// accepted.
	InvitationUnknown     InvitationStatus = "unknown"
	InvitationAccepted    InvitationStatus = "accepted"
	InvitationDeclined    InvitationStatus = "declined"
	InvitationTentative   InvitationStatus = "tentative"
	InvitationNeedsAction InvitationStatus = "needs_action"
	// InvitationNone means the event carries no invitation (e.g. a personal
	// calendar block).
	InvitationNone InvitationStatus = "none"
)

// Event status values retained on Evidence. A cancelled event is RETAINED with
// this status; it is excluded only at proposal time.
const (
	EventStatusConfirmed = "confirmed"
	EventStatusTentative = "tentative"
	EventStatusCancelled = "cancelled"
	EventStatusUnknown   = "unknown"
)

// Duration is a possibly-unknown elapsed duration. Known==false means the
// duration is UNKNOWN and must never be read as zero minutes: commits, pull
// requests, and agent events all have Known==false. Use UnknownDuration for
// those; an unknown duration is never conflated with a genuine zero.
type Duration struct {
	Known   bool `json:"known"`
	Minutes int  `json:"minutes"`
}

// UnknownDuration returns a Duration whose elapsation is not known. Its Minutes
// field is meaningless and must not be read as a real zero.
func UnknownDuration() Duration { return Duration{Known: false} }

// KnownDuration returns a Duration of a concrete number of minutes.
func KnownDuration(minutes int) Duration { return Duration{Known: true, Minutes: minutes} }

// IsUnknown reports whether the duration is unknown (and therefore must not be
// interpreted as zero minutes).
func (d Duration) IsUnknown() bool { return !d.Known }

// SourceRef carries only non-secret provenance for an evidence item. It never
// carries a feed URL secret, a credential, or a filesystem path. A remote-less
// repository is identified by its owner-configured non-secret id.
//
// There is deliberately NO free-form URL field: a resolved secret feed URL could
// be written into durable records with no check failing. If a non-secret web link
// is genuinely needed later, it must be a constrained, guarded field added
// deliberately by the adapter slice (S3-IMPL-2), not a free-form string here.
type SourceRef struct {
	Kind       SourceKind `json:"kind"`
	FeedID     string     `json:"feed_id,omitempty"`
	Repository string     `json:"repository,omitempty"`
	Branch     string     `json:"branch,omitempty"`
	PRNumber   int        `json:"pr_number,omitempty"`
	CommitSHA  string     `json:"commit_sha,omitempty"`
	EventUID   string     `json:"event_uid,omitempty"`
	SessionID  string     `json:"session_id,omitempty"`
	// At is this ref's own non-secret observation time (for a git ref, the
	// commit's timestamp). SourceRef is NOT an identity input, so adding At does
	// not change any StableID. It lets the proposal layer place each folded commit
	// at its own time instead of only the parent evidence's span.
	At time.Time `json:"at,omitempty"`
}

// TopicHint suggests a reviewable grouping. PRNumber==0 means the caller falls
// back to Repository+Branch. Identical branch names in different repositories
// never establish one topic.
type TopicHint struct {
	Repository string `json:"repository,omitempty"`
	PRNumber   int    `json:"pr_number,omitempty"`
	Branch     string `json:"branch,omitempty"`
}

// Evidence is a single, source-identified observation. Description holds
// source-derived text only; user-authored text belongs to S4's
// ConfirmedInterval. Evidence is never confirmed working time by itself.
type Evidence struct {
	ID           string           `json:"id"`
	Kind         SourceKind       `json:"kind"`
	Title        string           `json:"title"`
	Description  string           `json:"description,omitempty"`
	Start        time.Time        `json:"start,omitempty"`
	End          time.Time        `json:"end,omitempty"`
	AllDay       bool             `json:"all_day,omitempty"`
	Status       string           `json:"status,omitempty"`
	Invitation   InvitationStatus `json:"invitation,omitempty"`
	Duration     Duration         `json:"duration"`
	SourceRefs   []SourceRef      `json:"source_refs,omitempty"`
	TopicHint    *TopicHint       `json:"topic_hint,omitempty"`
	ProjectHints []string         `json:"project_hints,omitempty"`
	ObservedAt   time.Time        `json:"observed_at,omitempty"`
}
