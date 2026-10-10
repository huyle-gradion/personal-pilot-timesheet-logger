package proposal

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	"github.com/huyle-gradion/personal-pilot-timesheet-logger/internal/evidence"
)

// hashID builds a deterministic, content-addressed ID: sha256 over
// length-prefixed parts, rendered "<prefix>_<32 hex chars>".
func hashID(prefix string, parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		var lenBuf [8]byte
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(p)))
		_, _ = h.Write(lenBuf[:])
		_, _ = h.Write([]byte(p))
	}
	sum := h.Sum(nil)
	return prefix + "_" + hex.EncodeToString(sum[:16])
}

// point is one evidence activity instant within a topic.
type point struct {
	EvidenceID string
	At         time.Time
}

// meetingInterval is a meeting used to interrupt inferred work.
type meetingInterval struct {
	ID    string
	Start time.Time
	End   time.Time
}

// pointsFor returns the activity points an evidence item contributes. For git
// evidence, each commit SourceRef's own At is a point (so a PR that folds several
// commits yields one point per commit); it falls back to the evidence Start when
// no per-commit time exists.
func pointsFor(e evidence.Evidence) []point {
	var pts []point
	for _, ref := range e.SourceRefs {
		if ref.Kind == evidence.SourceLocalGit && !ref.At.IsZero() {
			pts = append(pts, point{EvidenceID: e.ID, At: ref.At})
		}
	}
	if len(pts) == 0 && !e.Start.IsZero() {
		pts = append(pts, point{EvidenceID: e.ID, At: e.Start})
	}
	return pts
}

// formGroups clusters points within a topic using the adjacency gap. A meeting
// whose start falls strictly inside an open gap breaks the group. Duration is
// always unknown; SpanStart is the first point and SpanEnd the last point.
func formGroups(topicID string, pts []point, meetings []meetingInterval, gap time.Duration) []ActivityGroup {
	if len(pts) == 0 {
		return nil
	}
	sorted := make([]point, len(pts))
	copy(sorted, pts)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].At.Equal(sorted[j].At) {
			return sorted[i].EvidenceID < sorted[j].EvidenceID
		}
		return sorted[i].At.Before(sorted[j].At)
	})

	var groups []ActivityGroup
	cur := []point{sorted[0]}
	for i := 1; i < len(sorted); i++ {
		prev := sorted[i-1]
		next := sorted[i]
		brk := next.At.Sub(prev.At) > gap
		if !brk {
			for _, m := range meetings {
				if m.Start.After(prev.At) && m.Start.Before(next.At) {
					brk = true
					break
				}
			}
		}
		if brk {
			groups = append(groups, buildGroup(topicID, cur, meetings))
			cur = []point{next}
			continue
		}
		cur = append(cur, next)
	}
	groups = append(groups, buildGroup(topicID, cur, meetings))
	return groups
}

// buildGroup builds one ActivityGroup from a run of points.
func buildGroup(topicID string, pts []point, meetings []meetingInterval) ActivityGroup {
	ids := make([]string, 0, len(pts))
	seen := make(map[string]bool, len(pts))
	for _, p := range pts {
		if !seen[p.EvidenceID] {
			seen[p.EvidenceID] = true
			ids = append(ids, p.EvidenceID)
		}
	}
	spanStart := pts[0].At
	spanEnd := pts[len(pts)-1].At

	var interrupted []string
	for _, m := range meetings {
		if intervalsOverlap(m.Start, m.End, spanStart, spanEnd) {
			interrupted = append(interrupted, m.ID)
		}
	}
	sort.Strings(interrupted)

	return ActivityGroup{
		ID:            hashID("group", topicID, spanStart.UTC().Format(time.RFC3339Nano), strings.Join(ids, ",")),
		TopicID:       topicID,
		EvidenceIDs:   ids,
		SpanStart:     spanStart,
		SpanEnd:       spanEnd,
		Duration:      evidence.UnknownDuration(),
		InterruptedBy: interrupted,
	}
}

// intervalsOverlap reports whether closed intervals [aStart,aEnd] and
// [bStart,bEnd] overlap (touching counts).
func intervalsOverlap(aStart, aEnd, bStart, bEnd time.Time) bool {
	return !aEnd.Before(bStart) && !aStart.After(bEnd)
}
