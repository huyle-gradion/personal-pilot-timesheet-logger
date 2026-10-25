package timesheet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// --- TOPIC-OVERFLOW-1: durable per-topic evidence overflow (issue #146) -----

// overflowFixtureItem builds one synthetic attributable provider item.
func overflowFixtureItem(seq int, kind, at, repo, resource string) EvidenceItem {
	return EvidenceItem{
		Ref:      fmt.Sprintf("ev-%s-%04d", kind, seq),
		Kind:     kind,
		Time:     at,
		Repo:     repo,
		Resource: resource,
		Title:    fmt.Sprintf("event %d", seq),
	}
}

// TestBuildGithubTopicsOverflowRetention: a 60-item topic retains exactly 50
// Items plus 10 OverflowItems, both time-sorted; FirstAt/LastAt stay computed
// from the retained Items only (overflow never extends the span).
func TestBuildGithubTopicsOverflowRetention(t *testing.T) {
	var items []EvidenceItem
	for i := 0; i < 50; i++ {
		items = append(items, overflowFixtureItem(i, "commit",
			fmt.Sprintf("2026-10-20T%02d:%02d:00+07:00", 10+i/60, i%60),
			"acme/api", "pr:acme/api:12"))
	}
	// The ten overflow items carry later timestamps than every retained item,
	// so a span extension would be visible in LastAt.
	for i := 0; i < 10; i++ {
		items = append(items, overflowFixtureItem(50+i, "issue_comment",
			fmt.Sprintf("2026-10-20T18:%02d:00+07:00", i),
			"acme/api", "pr:acme/api:12"))
	}
	topics := buildGithubTopics(items)
	if len(topics) != 1 {
		t.Fatalf("topics = %d, want 1", len(topics))
	}
	topic := topics[0]
	if len(topic.Items) != 50 {
		t.Fatalf("retained Items = %d, want 50", len(topic.Items))
	}
	if len(topic.OverflowItems) != 10 {
		t.Fatalf("retained OverflowItems = %d, want 10", len(topic.OverflowItems))
	}
	if topic.DroppedItems != 0 {
		t.Fatalf("DroppedItems = %d, want 0 (everything fits under the overflow ceiling)", topic.DroppedItems)
	}
	if !topic.Truncated {
		t.Fatalf("topic must be truncated when items fall beyond the retained Items cap")
	}
	if topic.FirstAt != "2026-10-20T10:00:00+07:00" || topic.LastAt != "2026-10-20T10:49:00+07:00" {
		t.Fatalf("FirstAt/LastAt = %q/%q, want the retained-Items span only", topic.FirstAt, topic.LastAt)
	}
	for i, item := range topic.OverflowItems {
		if !strings.HasPrefix(item.Time, "2026-10-20T18:") {
			t.Fatalf("OverflowItems[%d].Time = %q, overflow must not leak into Items", i, item.Time)
		}
	}
	for i := 1; i < len(topic.OverflowItems); i++ {
		if topic.OverflowItems[i-1].Time > topic.OverflowItems[i].Time {
			t.Fatalf("OverflowItems not time-sorted at %d", i)
		}
	}
}

// TestBuildGithubTopicsPerTopicOverflowCeiling: a 250-item single topic
// retains 50 Items + 150 OverflowItems and records the 50 items beyond the
// per-topic overflow ceiling as an honest DroppedItems loss.
func TestBuildGithubTopicsPerTopicOverflowCeiling(t *testing.T) {
	var items []EvidenceItem
	for i := 0; i < 250; i++ {
		items = append(items, overflowFixtureItem(i, "commit",
			fmt.Sprintf("2026-10-20T%02d:%02d:00+07:00", 8+i/60, i%60),
			"acme/api", "pr:acme/api:12"))
	}
	topics := buildGithubTopics(items)
	if len(topics) != 1 {
		t.Fatalf("topics = %d, want 1", len(topics))
	}
	topic := topics[0]
	if len(topic.Items) != maxTopicItems {
		t.Fatalf("retained Items = %d, want %d", len(topic.Items), maxTopicItems)
	}
	if len(topic.OverflowItems) != maxTopicOverflowItems {
		t.Fatalf("retained OverflowItems = %d, want %d", len(topic.OverflowItems), maxTopicOverflowItems)
	}
	if topic.DroppedItems != 250-maxTopicItems-maxTopicOverflowItems {
		t.Fatalf("DroppedItems = %d, want %d", topic.DroppedItems, 250-maxTopicItems-maxTopicOverflowItems)
	}
	if !topic.Truncated {
		t.Fatalf("topic must be truncated past all ceilings")
	}
}

// TestBuildGithubTopicsTotalOverflowCeiling: enough topics to exceed the
// 600-item attempt-wide overflow pool. Eight topics of 130 items each leave
// 80 beyond-cap items per topic (640 total); the pool fills in de-duplicated
// item arrival order, so topics 1-7 retain their full 80 and topic 8 retains
// the last 40 with its remaining 40 counted lost.
func TestBuildGithubTopicsTotalOverflowCeiling(t *testing.T) {
	var items []EvidenceItem
	for topic := 0; topic < 8; topic++ {
		resource := fmt.Sprintf("pr:acme/api:%d", 100+topic)
		for i := 0; i < 130; i++ {
			items = append(items, overflowFixtureItem(topic*1000+i, "commit",
				fmt.Sprintf("2026-10-20T%02d:%02d:00+07:00", 8+topic, i%60),
				"acme/api", resource))
		}
	}
	topics := buildGithubTopics(items)
	if len(topics) != 8 {
		t.Fatalf("topics = %d, want 8", len(topics))
	}
	totalKept, totalLost := 0, 0
	for i, topic := range topics {
		wantKept := 80
		wantLost := 0
		if i == 7 {
			wantKept = 40
			wantLost = 40
		}
		if len(topic.OverflowItems) != wantKept {
			t.Fatalf("topic %d OverflowItems = %d, want %d", i, len(topic.OverflowItems), wantKept)
		}
		if topic.DroppedItems != wantLost {
			t.Fatalf("topic %d DroppedItems = %d, want %d", i, topic.DroppedItems, wantLost)
		}
		totalKept += len(topic.OverflowItems)
		totalLost += topic.DroppedItems
	}
	if totalKept != maxTotalOverflowItems {
		t.Fatalf("total retained overflow = %d, want exactly the %d-item pool", totalKept, maxTotalOverflowItems)
	}
	if totalLost != 40 {
		t.Fatalf("total lost overflow = %d, want 40", totalLost)
	}
}

// TestBuildGithubTopicsDedupProvenanceMergeWithOverflow: the same provider
// event seen through two endpoint families is merged before grouping even
// when the duplicate straddles the retained Items cap — the merged
// provenance survives on the retained item and the duplicate never becomes
// an overflow item.
func TestBuildGithubTopicsDedupProvenanceMergeWithOverflow(t *testing.T) {
	var items []EvidenceItem
	for i := 0; i < 49; i++ {
		items = append(items, overflowFixtureItem(i, "commit",
			fmt.Sprintf("2026-10-20T10:%02d:00+07:00", i),
			"acme/api", "pr:acme/api:12"))
	}
	// Item 50 (the last retained one) and item 51 (the first overflow
	// candidate) share one provider event ref seen via two families.
	retained := overflowFixtureItem(49, "pull_request", "2026-10-20T10:49:00+07:00", "acme/api", "pr:acme/api:12")
	retained.Ref = "shared-event"
	retained.Provenance = "pull_requests"
	dup := retained
	dup.Provenance = "repo_events"
	items = append(items, retained, dup)
	for i := 0; i < 5; i++ {
		items = append(items, overflowFixtureItem(100+i, "issue_comment",
			fmt.Sprintf("2026-10-20T11:%02d:00+07:00", i),
			"acme/api", "pr:acme/api:12"))
	}
	topics := buildGithubTopics(items)
	if len(topics) != 1 {
		t.Fatalf("topics = %d, want 1", len(topics))
	}
	topic := topics[0]
	if len(topic.Items) != 50 || len(topic.OverflowItems) != 5 {
		t.Fatalf("retained = %d Items + %d overflow, want 50 + 5 (the duplicate is merged, not retained)",
			len(topic.Items), len(topic.OverflowItems))
	}
	var merged []string
	for _, item := range append(append([]EvidenceItem(nil), topic.Items...), topic.OverflowItems...) {
		if item.Ref == "shared-event" {
			merged = strings.Split(item.Provenance, ",")
		}
	}
	if len(merged) != 2 || merged[0] != "pull_requests" || merged[1] != "repo_events" {
		t.Fatalf("merged provenance = %v, want [pull_requests repo_events]", merged)
	}
}

// TestStoreOverflowRoundTrip: overflow items survive a full durable
// round-trip (commit, close, reload) exactly, and the same collection input
// builds a byte-identical state projection twice.
func TestStoreOverflowRoundTrip(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	var items []EvidenceItem
	for i := 0; i < 60; i++ {
		items = append(items, overflowFixtureItem(i, "commit",
			fmt.Sprintf("2026-10-26T%02d:%02d:00+07:00", 9+i/60, i%60),
			"acme/api", "pr:acme/api:12"))
	}
	topics := buildGithubTopics(items)

	st, err := OpenStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CommitEvidence("2026-10-26", &DayEvidence{Topics: topics}); err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(st.Evidence("2026-10-26").Topics)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := OpenStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	after, err := json.Marshal(st2.Evidence("2026-10-26").Topics)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("store round-trip changed the evidence topics:\nbefore: %s\nafter:  %s", before, after)
	}
	loaded := st2.Evidence("2026-10-26").Topics
	if len(loaded) != 1 || len(loaded[0].Items) != 50 || len(loaded[0].OverflowItems) != 10 {
		t.Fatalf("reloaded topics = %+v, want 1 topic with 50 Items + 10 OverflowItems", loaded)
	}
	if !reflect.DeepEqual(loaded[0], topics[0]) {
		t.Fatalf("reloaded topic differs from the built topic")
	}

	// Determinism: the same input built twice marshals byte-identically.
	topics2 := buildGithubTopics(items)
	again, err := json.Marshal(topics2)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(again) {
		t.Fatalf("state projection is not deterministic:\nfirst:  %s\nsecond: %s", before, again)
	}
}

// TestStoreLoadsLegacyStateWithoutOverflowField: a pre-#146 state file (no
// overflow fields anywhere) loads unchanged, and re-marshaling its evidence
// reproduces the fixture bytes exactly (legacy field order preserved).
func TestStoreLoadsLegacyStateWithoutOverflowField(t *testing.T) {
	legacyTopics := []TopicCandidate{{
		Ref: "tc:gh:acme/api#pr:12", Title: "Pull request #12 (acme/api)", Provider: "github",
		Truncated: true, DroppedItems: 33,
		Items: []EvidenceItem{
			{Ref: "ev-1", Kind: "commit", Time: "2026-10-20T10:00:00+07:00", Repo: "acme/api"},
			{Ref: "ev-2", Kind: "commit", Time: "2026-10-20T10:30:00+07:00", Repo: "acme/api"},
		},
		FirstAt: "2026-10-20T10:00:00+07:00", LastAt: "2026-10-20T10:30:00+07:00",
	}}
	topicsJSON, err := json.Marshal(legacyTopics)
	if err != nil {
		t.Fatal(err)
	}
	stateJSON := fmt.Sprintf(`{"schema":2,"days":{},"evidence":{"2026-10-20":{"topics":%s}}}`, topicsJSON)
	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(statePath, []byte(stateJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := OpenStore(statePath)
	if err != nil {
		t.Fatalf("pre-#146 state must load unchanged: %v", err)
	}
	defer st.Close()
	ev := st.Evidence("2026-10-20")
	if ev == nil || len(ev.Topics) != 1 || len(ev.Topics[0].OverflowItems) != 0 {
		t.Fatalf("legacy evidence = %+v, want one topic with no overflow field", ev)
	}
	if !ev.Topics[0].Truncated || ev.Topics[0].DroppedItems != 33 {
		t.Fatalf("legacy truncation detail lost: %+v", ev.Topics[0])
	}
	reloaded, err := json.Marshal(ev.Topics)
	if err != nil {
		t.Fatal(err)
	}
	if string(reloaded) != string(topicsJSON) {
		t.Fatalf("legacy evidence re-marshal is not byte-identical:\nwant: %s\ngot:  %s", topicsJSON, reloaded)
	}
}

// TestStoreValidatesOverflowItems: overflow items are durable evidence and
// pass the same fail-closed identity validation as retained Items; a
// negative loss count is structurally invalid.
func TestStoreValidatesOverflowItems(t *testing.T) {
	dir := t.TempDir()
	seq := 0
	base := func(topics any) string {
		t.Helper()
		topicsJSON, err := json.Marshal(topics)
		if err != nil {
			t.Fatal(err)
		}
		seq++
		path := filepath.Join(dir, fmt.Sprintf("state-%d.json", seq))
		body := fmt.Sprintf(`{"schema":2,"days":{},"evidence":{"2026-10-26":{"topics":%s}}}`, topicsJSON)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	valid := []TopicCandidate{{
		Ref: "tc:gh:acme/api#pr:12", Title: "Pull request #12 (acme/api)", Provider: "github",
		Items:         []EvidenceItem{{Ref: "ev-1", Kind: "commit", Time: "2026-10-26T10:00:00+07:00"}},
		OverflowItems: []EvidenceItem{{Ref: "ev-2", Kind: "commit", Time: "2026-10-26T11:00:00+07:00"}},
	}}
	st, err := OpenStore(base(valid))
	if err != nil {
		t.Fatalf("valid overflow must load: %v", err)
	}
	st.Close()

	noRef := []TopicCandidate{{
		Ref: "tc:gh:acme/api#pr:12", Title: "Pull request #12 (acme/api)", Provider: "github",
		Items:         []EvidenceItem{{Ref: "ev-1", Kind: "commit", Time: "2026-10-26T10:00:00+07:00"}},
		OverflowItems: []EvidenceItem{{Kind: "commit", Time: "2026-10-26T11:00:00+07:00"}},
	}}
	if st, err := OpenStore(base(noRef)); err == nil {
		st.Close()
		t.Fatalf("overflow item without identity must fail closed")
	}
	negative := []TopicCandidate{{
		Ref: "tc:gh:acme/api#pr:12", Title: "Pull request #12 (acme/api)", Provider: "github",
		Items:        []EvidenceItem{{Ref: "ev-1", Kind: "commit", Time: "2026-10-26T10:00:00+07:00"}},
		DroppedItems: -1,
	}}
	if st, err := OpenStore(base(negative)); err == nil {
		st.Close()
		t.Fatalf("negative dropped-item count must fail closed")
	}
}

// overflowDraftTopic builds one truncated synthetic topic: three retained
// items, ten overflow items of two kinds spanning 14:05-16:20 local, and an
// honest five-item loss beyond the ceilings.
func overflowDraftTopic() TopicCandidate {
	overflow := []EvidenceItem{}
	for i := 0; i < 6; i++ {
		overflow = append(overflow, overflowFixtureItem(i, "commit",
			fmt.Sprintf("2026-10-26T14:%02d:00+07:00", 5+i), "acme/api", "pr:acme/api:12"))
	}
	for i := 0; i < 4; i++ {
		overflow = append(overflow, overflowFixtureItem(10+i, "issue_comment",
			fmt.Sprintf("2026-10-26T16:%02d:00+07:00", 5+i*5), "acme/api", "pr:acme/api:12"))
	}
	return TopicCandidate{
		Title:   "Pull request #12 (acme/api)",
		FirstAt: "2026-10-26T10:00:00+07:00", LastAt: "2026-10-26T10:30:00+07:00",
		Items: []EvidenceItem{
			{Ref: "ev-a", Kind: "commit", Time: "2026-10-26T10:00:00+07:00", Repo: "acme/api"},
			{Ref: "ev-b", Kind: "commit", Time: "2026-10-26T10:10:00+07:00", Repo: "acme/api"},
			{Ref: "ev-c", Kind: "pull_request", Time: "2026-10-26T10:30:00+07:00", Repo: "acme/api",
				Resource: "pr:acme/api:12"},
		},
		OverflowItems: overflow,
		DroppedItems:  5,
		Truncated:     true,
	}
}

// TestBuildDraftTruncatedTopicOverflowSummary: a truncated topic's
// topic_candidates line carries the kind counts, the honest lost count and
// the first/last overflow timestamps in a deterministic format; the same
// input builds a byte-identical draft twice.
func TestBuildDraftTruncatedTopicOverflowSummary(t *testing.T) {
	review := draftReview(nil, nil, []TopicCandidate{overflowDraftTopic()}, nil)
	d := BuildDraft(review, "test")
	eb := evidenceBlock(t, d)
	if len(eb.TopicCandidates) != 1 {
		t.Fatalf("topic_candidates = %+v, want one line", eb.TopicCandidates)
	}
	want := "Pull request #12 (acme/api)" +
		" — overflow: +10 kept (commit 6, issue_comment 4), 5 lost, 14:05–16:20"
	if eb.TopicCandidates[0] != want {
		t.Fatalf("topic line = %q, want %q", eb.TopicCandidates[0], want)
	}
	raw1, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	raw2, err := json.Marshal(BuildDraft(review, "test"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw1) != string(raw2) {
		t.Fatalf("draft is not deterministic:\nfirst:  %s\nsecond: %s", raw1, raw2)
	}
}

// TestBuildDraftOverflowLostOnlySummary: a topic truncated past the overflow
// ceilings with nothing retained renders its loss honestly (no kept part, no
// span), still deterministically.
func TestBuildDraftOverflowLostOnlySummary(t *testing.T) {
	topic := overflowDraftTopic()
	topic.OverflowItems = nil
	topic.DroppedItems = 33
	review := draftReview(nil, nil, []TopicCandidate{topic}, nil)
	eb := evidenceBlock(t, BuildDraft(review, "test"))
	want := "Pull request #12 (acme/api) — overflow: 33 lost"
	if eb.TopicCandidates[0] != want {
		t.Fatalf("topic line = %q, want %q", eb.TopicCandidates[0], want)
	}
}

// TestBuildDraftNonTruncatedTopicsByteIdentical: topics without overflow
// render byte-identically to the pre-#146 renderer — the bare title, no
// overflow marker anywhere in the draft.
func TestBuildDraftNonTruncatedTopicsByteIdentical(t *testing.T) {
	topic := overflowDraftTopic()
	topic.OverflowItems = nil
	topic.DroppedItems = 0
	topic.Truncated = false
	if line := draftOverflowSummary(topic); line != "" {
		t.Fatalf("non-truncated topic summary = %q, want empty", line)
	}
	review := draftReview(nil, nil, []TopicCandidate{topic}, nil)
	eb := evidenceBlock(t, BuildDraft(review, "test"))
	if eb.TopicCandidates[0] != topic.Title {
		t.Fatalf("non-truncated topic line = %q, want the bare title %q", eb.TopicCandidates[0], topic.Title)
	}
	raw, err := json.Marshal(BuildDraft(review, "test"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "overflow") {
		t.Fatalf("non-truncated draft contains an overflow marker: %s", raw)
	}
}

// TestBuildDraftGroupedTruncatedPRsCarryCombinedSummary: several truncated
// per-PR topics in one repository collapse into ONE grouped line (the #148
// grouping invariant) that carries the combined overflow summary — summed
// kept counts per kind, summed lost count, and the union span.
func TestBuildDraftGroupedTruncatedPRsCarryCombinedSummary(t *testing.T) {
	var topics []TopicCandidate
	keptPerTopic := []int{10, 11, 12, 13, 14} // sums to 60
	lostPerTopic := []int{1, 2, 3, 4, 5}      // sums to 15
	for i := 0; i < 5; i++ {
		num := 10 + i
		var overflow []EvidenceItem
		for k := 0; k < keptPerTopic[i]; k++ {
			hour, minute := 9+i, 0
			if i == 4 && k == keptPerTopic[i]-1 {
				hour, minute = 16, 50 // push the union span's last timestamp
			}
			overflow = append(overflow, overflowFixtureItem(i*100+k, "commit",
				fmt.Sprintf("2026-10-26T%02d:%02d:00+07:00", hour, minute),
				"acme/api", fmt.Sprintf("pr:acme/api:%d", num)))
		}
		topics = append(topics, TopicCandidate{
			Title:   fmt.Sprintf("Pull request #%d (acme/api)", num),
			FirstAt: "2026-10-26T10:00:00+07:00", LastAt: "2026-10-26T10:30:00+07:00",
			Items: []EvidenceItem{{
				Ref: fmt.Sprintf("ev-pr-%d", num), Kind: "pull_request",
				Time: "2026-10-26T10:00:00+07:00", Repo: "acme/api",
				Resource: fmt.Sprintf("pr:acme/api:%d", num),
			}},
			OverflowItems: overflow,
			DroppedItems:  lostPerTopic[i],
			Truncated:     true,
		})
	}
	eb := evidenceBlock(t, BuildDraft(draftReview(nil, nil, topics, nil), "test"))
	want := "5 PRs (#10–#14) in acme/api — overflow: +60 kept (commit 60), 15 lost, 09:00–16:50"
	if len(eb.TopicCandidates) != 1 || eb.TopicCandidates[0] != want {
		t.Fatalf("topic_candidates = %+v, want one grouped line %q", eb.TopicCandidates, want)
	}
}

// TestBuildDraftCollapsedRepeatsCarryCombinedSummary: identically titled
// truncated topics collapse into one "N× title" line carrying the combined
// overflow summary of every occurrence.
func TestBuildDraftCollapsedRepeatsCarryCombinedSummary(t *testing.T) {
	mk := func(seq, lost, kept int, at string) TopicCandidate {
		return TopicCandidate{
			Title:   "Deploy run (acme/api)",
			FirstAt: "2026-10-26T10:00:00+07:00", LastAt: "2026-10-26T10:30:00+07:00",
			Items: []EvidenceItem{{
				Ref: fmt.Sprintf("ev-ok-%d", seq), Kind: "commit",
				Time: "2026-10-26T10:00:00+07:00", Repo: "acme/api",
			}},
			OverflowItems: []EvidenceItem{
				overflowFixtureItem(seq, "commit", at, "acme/api", "pr:acme/api:12"),
			},
			DroppedItems: lost,
			Truncated:    true,
		}
	}
	topics := []TopicCandidate{mk(1, 1, 1, "2026-10-26T09:00:00+07:00"), mk(2, 2, 1, "2026-10-26T17:30:00+07:00")}
	eb := evidenceBlock(t, BuildDraft(draftReview(nil, nil, topics, nil), "test"))
	want := "2× Deploy run (acme/api) — overflow: +2 kept (commit 2), 3 lost, 09:00–17:30"
	if len(eb.TopicCandidates) != 1 || eb.TopicCandidates[0] != want {
		t.Fatalf("topic_candidates = %+v, want one collapsed line %q", eb.TopicCandidates, want)
	}
}

// TestBuildDraftMoreLineCarriesAggregateLoss: when the 12-line cap omits
// rendered lines that carried overflow or loss, the "+N more" line itself
// carries their aggregate, so the headline loss count is never hidden
// behind the cap.
func TestBuildDraftMoreLineCarriesAggregateLoss(t *testing.T) {
	var topics []TopicCandidate
	for i := 0; i < 12; i++ {
		topics = append(topics, TopicCandidate{
			Title: fmt.Sprintf("Deploy run %d (acme/api)", i),
			Items: []EvidenceItem{{
				Ref: fmt.Sprintf("ev-ok-%d", i), Kind: "commit",
				Time: "2026-10-26T10:00:00+07:00", Repo: "acme/api",
			}},
		})
	}
	// Three distinct truncated titles render after the twelve: they are the
	// lines the cap omits, and their losses must surface on "+N more".
	for i := 0; i < 3; i++ {
		topics = append(topics, TopicCandidate{
			Title: fmt.Sprintf("Incident review %d (acme/api)", i),
			Items: []EvidenceItem{{
				Ref: fmt.Sprintf("ev-tr-%d", i), Kind: "commit",
				Time: "2026-10-26T10:00:00+07:00", Repo: "acme/api",
			}},
			OverflowItems: []EvidenceItem{
				overflowFixtureItem(i, "commit", fmt.Sprintf("2026-10-26T1%d:00:00+07:00", 1+i), "acme/api", "pr:acme/api:20"),
				overflowFixtureItem(10+i, "issue_comment", fmt.Sprintf("2026-10-26T1%d:30:00+07:00", 1+i), "acme/api", "pr:acme/api:20"),
			},
			DroppedItems: i + 1,
			Truncated:    true,
		})
	}
	eb := evidenceBlock(t, BuildDraft(draftReview(nil, nil, topics, nil), "test"))
	if len(eb.TopicCandidates) != maxDraftTopicCandidates+1 {
		t.Fatalf("topic_candidates = %d lines, want %d (cap plus one more line)", len(eb.TopicCandidates), maxDraftTopicCandidates+1)
	}
	want := "+3 more — overflow: +6 kept (commit 3, issue_comment 3), 6 lost, 11:00–13:30"
	got := eb.TopicCandidates[maxDraftTopicCandidates]
	if got != want {
		t.Fatalf("more line = %q, want %q", got, want)
	}
	for i, line := range eb.TopicCandidates[:maxDraftTopicCandidates] {
		if strings.Contains(line, "overflow") && i >= 12-3 {
			t.Fatalf("a retained line unexpectedly carries a summary: %q", line)
		}
	}
}

// TestBuildDraftBusyDayWithOverflowStaysBounded: the #148 busy-day fixture
// with every topic truncated (150 kept overflow items + 50 lost each) still
// renders under the 3,000-byte synthetic budget, with each repository's
// grouped PR line carrying the combined overflow summary for its ten topics.
func TestBuildDraftBusyDayWithOverflowStaysBounded(t *testing.T) {
	review := busyDayReview()
	for i := range review.Evidence.Topics {
		topic := &review.Evidence.Topics[i]
		var overflow []EvidenceItem
		for k := 0; k < 75; k++ {
			overflow = append(overflow, overflowFixtureItem(k, "commit",
				fmt.Sprintf("2026-10-20T%02d:%02d:00+07:00", 12+k/60, k%60),
				topic.Items[0].Repo, topic.Items[0].Resource))
		}
		for k := 0; k < 75; k++ {
			overflow = append(overflow, overflowFixtureItem(100+k, "issue_comment",
				fmt.Sprintf("2026-10-20T%02d:%02d:00+07:00", 14+k/60, k%60),
				topic.Items[0].Repo, topic.Items[0].Resource))
		}
		topic.OverflowItems = overflow
		topic.DroppedItems = 50
		topic.Truncated = true
	}
	d := BuildDraft(review, "test")
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(raw); got > 3000 {
		t.Fatalf("busy-day draft with overflow = %d bytes, want <= 3000: %s", got, raw)
	}
	for _, want := range []string{
		"10 PRs (#10–#19) in acme/api — overflow: +1500 kept (commit 750, issue_comment 750), 500 lost, 12:00–15:14",
		"10 PRs (#20–#29) in acme/web — overflow: +1500 kept (commit 750, issue_comment 750), 500 lost, 12:00–15:14",
	} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("draft lacks combined grouped line %q: %s", want, raw)
		}
	}
}
