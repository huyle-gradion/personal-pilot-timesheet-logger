package timesheet

// Comment-family window walk (issue #193).
//
// The review-comment and issue-comment list endpoints accept an explicit
// sort=created&direction=asc declaration and a `since` lower bound that the
// provider applies as a strict `updated_at > since` comparison. Two facts
// follow, and nothing beyond them is inferred:
//
//   - pulling `since` back by commentWindowOverlap keeps a comment whose
//     updated_at equals the day's start from being lost to the strict bound;
//     the exact half-open client filter prunes the overlap again.
//   - under the declared ascending created_at order, a full page whose LAST
//     parsed created_at is at/after the exclusive window end proves every
//     later page is past the window too, so the walk may stop after
//     processing that whole page. The premise is the declared endpoint sort,
//     never an ordering guessed from sampled pages.
//
// A page carrying an unparseable created_at proves nothing: the page's end
// cannot be established, so it never stops the walk.

import "time"

const (
	// stopWindowPassed is the familyWalk stop recorded by the early stop.
	stopWindowPassed = "window_passed"
	// commentWindowOverlap pulls the comment query's strict-after `since`
	// bound back far enough to cover a comment updated exactly at the day's
	// start. The client filter keeps the window itself exact.
	commentWindowOverlap = time.Minute
)

// commentWindow accumulates the parsed created_at watermarks of one comment
// walk in provider order. The watermarks are reporting detail only; the stop
// proof is observePage's page-end result under the declared sort.
type commentWindow struct {
	windowEnd    time.Time
	coveredFrom  time.Time
	coveredUntil time.Time
}

// commentWindowSince renders the overlapped lower bound for `since`.
func commentWindowSince(windowStart time.Time) string {
	return windowStart.UTC().Add(-commentWindowOverlap).Format(time.RFC3339)
}

// observePage records one decoded page's parsed created_at values and reports
// whether the page's end proves the window has passed. Every timestamp on the
// page must parse: one malformed value makes the page end unknowable, so no
// coverage proof is manufactured from it.
func (w *commentWindow) observePage(docs []commentDoc) bool {
	var last time.Time
	for i := range docs {
		at, err := time.Parse(time.RFC3339, docs[i].CreatedAt)
		if err != nil {
			return false
		}
		if w.coveredFrom.IsZero() {
			w.coveredFrom = at
		}
		w.coveredUntil = at
		last = at
	}
	return len(docs) > 0 && !last.Before(w.windowEnd)
}
