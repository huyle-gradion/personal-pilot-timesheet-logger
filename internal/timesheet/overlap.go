package timesheet

import (
	"sort"
	"strconv"
	"strings"
)

// powSpan is the projection every overlap consumer reduces a block or entry
// to: its stable id, its kind and its same-day minute span. The merge reasons
// only about the span and breaks ties by id, so a meeting is an ordinary
// cluster member; kind is what tells a reader that, and each consumer derives
// the merged kind from its own members.
type powSpan struct {
	id           string
	kind         string
	startM, endM int
}

// mergeSpans clusters spans into connected components of strict overlap:
// end == start merely touches and stays separate, chained overlaps join one
// component, and a gap between members is never bridged. Each cluster lists
// its members' original indices in the pinned order (start minute, then end
// minute, then id), so the first member is the head every consumer derives the
// merged block's span and identity from.
func mergeSpans(spans []powSpan) [][]int {
	order := make([]int, len(spans))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		ia, ib := spans[order[a]], spans[order[b]]
		if ia.startM != ib.startM {
			return ia.startM < ib.startM
		}
		if ia.endM != ib.endM {
			return ia.endM < ib.endM
		}
		return ia.id < ib.id
	})
	var clusters [][]int
	var ends []int
	for _, i := range order {
		last := len(clusters) - 1
		// Sorted by start, so a member overlaps the component exactly when it
		// starts before the component's union end.
		if last >= 0 && spans[i].startM < ends[last] {
			clusters[last] = append(clusters[last], i)
			if spans[i].endM > ends[last] {
				ends[last] = spans[i].endM
			}
			continue
		}
		clusters = append(clusters, []int{i})
		ends = append(ends, spans[i].endM)
	}
	return clusters
}

// resolveBlockIDs disambiguates duplicate tentative block ids. Fixed ids
// (singleton blocks) are reserved unchanged; the rest are assigned in
// ascending id order, the first keeping the plain id and each later one taking
// the next free "-2", "-3" suffix. The draft's id assignment, record's cluster
// merge and the stage projection all use it, so the same merged content yields
// the same id on every path.
func resolveBlockIDs(ids []string, fixed []bool) []string {
	out := append([]string(nil), ids...)
	used := make(map[string]bool, len(ids))
	for i := range ids {
		if fixed[i] && !used[ids[i]] {
			used[ids[i]] = true
			continue
		}
		fixed[i] = false
	}
	order := make([]int, 0, len(ids))
	for i := range ids {
		if !fixed[i] {
			order = append(order, i)
		}
	}
	sort.SliceStable(order, func(a, b int) bool { return ids[order[a]] < ids[order[b]] })
	for _, i := range order {
		id := ids[i]
		if !used[id] {
			used[id] = true
			out[i] = id
			continue
		}
		for n := 2; ; n++ {
			candidate := id + "-" + strconv.Itoa(n)
			if !used[candidate] {
				used[candidate] = true
				out[i] = candidate
				break
			}
		}
	}
	return out
}

// joinText folds contributor texts into one deterministic line: the head's
// text first, then " + " and each contributor's, skipping an empty text and
// one already contained in the accumulated line. The same draft is rendered
// again by record and stage, so the join must depend only on the cluster's
// order.
func joinText(texts ...string) string {
	out := ""
	for _, t := range texts {
		t = strings.TrimSpace(t)
		if t == "" || strings.Contains(out, t) {
			continue
		}
		if out == "" {
			out = t
			continue
		}
		out += " + " + t
	}
	return out
}
